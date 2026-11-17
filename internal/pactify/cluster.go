package pactify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/kubesink"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// ClusterOptions says which namespaces pacto init --cluster reads and where
// it writes the drafts.
type ClusterOptions struct {
	// Namespaces are read; empty reads every namespace.
	Namespaces []string
	// Out holds a folder per workload, <out>/<namespace>/<name>.
	Out string
	// Now is the clock calls are judged against; nil is time.Now.
	Now func() time.Time
}

// Bulk is what pacto init did to several services.
type Bulk struct {
	Results []*Result `json:"results"`
	// Skipped are the workloads a Pacto already describes, as ns/Kind/name.
	Skipped []string `json:"skipped"`
}

// callWindow is how far back a call counts as a dependency.
// ponytail: fixed; take the observer's dependency window if drafts need to match it.
const callWindow = 24 * time.Hour

func gvr(group, version, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
}

var (
	workloadGVRs = map[string]schema.GroupVersionResource{
		"Deployment":  gvr("apps", "v1", "deployments"),
		"StatefulSet": gvr("apps", "v1", "statefulsets"),
		"DaemonSet":   gvr("apps", "v1", "daemonsets"),
		"CronJob":     gvr("batch", "v1", "cronjobs"),
	}
	gvrServices       = gvr("", "v1", "services")
	gvrPods           = gvr("", "v1", "pods")
	gvrReplicaSets    = gvr("apps", "v1", "replicasets")
	gvrJobs           = gvr("batch", "v1", "jobs")
	gvrEndpointSlices = gvr("discovery.k8s.io", "v1", "endpointslices")
)

// cluster is what was read from the API server.
type cluster struct {
	workloads, services, pods, slices, pactos []unstructured.Unstructured
	// owners maps ns/ReplicaSet/name and ns/Job/name to the workload owning it.
	owners map[string]string
	snaps  []runtimefacts.Snapshot
	// observed is false when no node runtime observation exists, so calls
	// could not be seen at all.
	observed bool
	// nodes maps a node name to its observation.
	nodes map[string]*runtimefacts.Snapshot
	now   time.Time
}

// Cluster drafts a contract for every workload in the namespaces that no
// Pacto describes yet: it writes the workload and its Services into
// <out>/<ns>/<name>/manifests.yaml, pactifies that folder like any other and
// adds the dependencies its pods were seen calling. A draft that fails
// validation does not stop the others; the error names it.
func Cluster(ctx context.Context, dyn dynamic.Interface, base Options, c ClusterOptions, writeFile func(string, []byte, os.FileMode) error) (*Bulk, error) {
	if c.Out == "" {
		return nil, errors.New("reading a cluster needs a folder to write the drafts in (--out)")
	}
	if base.Name != "" || base.Dir != "" {
		return nil, errors.New("reading a cluster names each service after its workload, so it takes no name or folder")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	cl, err := readCluster(ctx, dyn, c.Namespaces, c.Now())
	if err != nil {
		return nil, err
	}
	deps := cl.inferred(c.Now().Add(-callWindow))
	described := cl.described()
	b := &Bulk{Results: []*Result{}, Skipped: []string{}}
	var errs []error
	for _, w := range cl.workloads {
		ns, name := w.GetNamespace(), w.GetName()
		k := key(w)
		if described[k] {
			b.Skipped = append(b.Skipped, k)
			continue
		}
		r, err := cl.draft(ctx, w, base, deps[k], c.Out, writeFile)
		if r != nil {
			b.Results = append(b.Results, r)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", ns, name, err))
		}
	}
	return b, errors.Join(errs...)
}

// draft pactifies one workload in <out>/<ns>/<name>. A manifests.yaml
// already there is the user's and is kept; a plan reads the new one from a
// scratch folder, so it writes nothing under out.
func (cl *cluster) draft(ctx context.Context, w unstructured.Unstructured, base Options, deps []Inferred, out string,
	writeFile func(string, []byte, os.FileMode) error) (*Result, error) {
	dir := filepath.Join(out, w.GetNamespace(), w.GetName())
	scanned, write := dir, writeFile
	content, keys, urls := cl.manifests(w)
	f := File{Path: "manifests.yaml", Action: Add, Content: content}
	if _, err := os.Stat(filepath.Join(dir, f.Path)); err == nil {
		f, keys, urls = File{Path: f.Path, Action: Keep}, nil, nil
	} else {
		if base.Plan {
			tmp, err := os.MkdirTemp("", "pacto-init-cluster-")
			if err != nil {
				return nil, err
			}
			defer func() { _ = os.RemoveAll(tmp) }()
			scanned, write = filepath.Join(tmp, w.GetName()), os.WriteFile
		}
		if err := os.MkdirAll(scanned, 0o755); err != nil {
			return nil, err
		}
		if err := write(filepath.Join(scanned, f.Path), []byte(f.Content), 0o644); err != nil {
			return nil, err
		}
	}
	opts := base
	opts.Dir, opts.Name, opts.Inferred = scanned, w.GetName(), deps
	r, err := Init(ctx, opts, writeFile)
	if r == nil {
		return nil, err
	}
	r.Dir, r.Path = dir, filepath.Join(dir, "pacto.yaml")
	r.Files = append([]File{f}, r.Files...)
	if len(keys) > 0 {
		secret := w.GetName() + "-env"
		values, them := literalValues(len(keys))
		r.Next = append(r.Next, fmt.Sprintf("manifests.yaml: the cluster sets %s to %s; the draft reads %s from the Secret %s instead, "+
			"so no value reaches the bundle. Before you apply it, create that Secret from a file of KEY=value lines: "+
			"kubectl -n %s create secret generic %s --from-env-file=<file>", strings.Join(keys, ", "), values, them, secret, w.GetNamespace(), secret))
	}
	if len(urls) > 0 {
		r.Next = append(r.Next, fmt.Sprintf("manifests.yaml: %s held a URL naming the service in brackets, which the draft now reads from a Secret, so the draft does not list "+
			"that service as a dependency: add it under dependencies", strings.Join(urls, ", ")))
	}
	if !cl.observed {
		r.Unknown = append(r.Unknown, "dependencies seen at runtime: the cluster holds no node runtime observations, so no calls were seen")
	}
	r.Unknown = append(r.Unknown, cl.unobserved(w)...)
	return r, err
}

// readNamespace reads one namespace's workloads, pods and the owners of
// their ReplicaSets and Jobs.
func (cl *cluster) readNamespace(list func(schema.GroupVersionResource, string, bool) ([]unstructured.Unstructured, error), ns string) error {
	for _, k := range []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"} {
		items, err := list(workloadGVRs[k], ns, false)
		if err != nil {
			return err
		}
		cl.workloads = append(cl.workloads, items...)
	}
	items, err := list(gvrPods, ns, false)
	if err != nil {
		return err
	}
	cl.pods = append(cl.pods, items...)
	for _, r := range []schema.GroupVersionResource{gvrReplicaSets, gvrJobs} {
		items, err := list(r, ns, false)
		if err != nil {
			return err
		}
		for _, o := range items {
			if ref := metav1.GetControllerOf(&o); ref != nil {
				cl.owners[o.GetNamespace()+"/"+o.GetKind()+"/"+o.GetName()] = o.GetNamespace() + "/" + ref.Kind + "/" + ref.Name
			}
		}
	}
	return nil
}

// readObservations reads the node runtime observations' snapshots.
func (cl *cluster) readObservations(nros []unstructured.Unstructured) error {
	for _, n := range nros {
		raw, _ := json.Marshal(n.Object["status"])
		var s runtimefacts.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("reading the node runtime observation %s: %w", n.GetName(), err)
		}
		cl.snaps = append(cl.snaps, s)
	}
	for i := range cl.snaps {
		cl.nodes[cl.snaps[i].NodeName] = &cl.snaps[i]
	}
	return nil
}

func readCluster(ctx context.Context, dyn dynamic.Interface, namespaces []string, now time.Time) (*cluster, error) {
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}
	cl := &cluster{owners: map[string]string{}, nodes: map[string]*runtimefacts.Snapshot{}, now: now}
	list := func(r schema.GroupVersionResource, ns string, optional bool) ([]unstructured.Unstructured, error) {
		l, err := dyn.Resource(r).Namespace(ns).List(ctx, metav1.ListOptions{})
		if optional && apierrors.IsNotFound(err) {
			return nil, nil // the CRD is not installed
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", r.Resource, err)
		}
		return l.Items, nil
	}
	for _, ns := range namespaces {
		if err := cl.readNamespace(list, ns); err != nil {
			return nil, err
		}
	}
	sort.Slice(cl.workloads, func(i, j int) bool { return key(cl.workloads[i]) < key(cl.workloads[j]) })
	// Services, their endpoints and Pactos are read in every namespace: a pod
	// here may call a Service anywhere, and only the drafts keep to the
	// namespaces asked for.
	for _, into := range []struct {
		r        schema.GroupVersionResource
		to       *[]unstructured.Unstructured
		optional bool
	}{{gvrServices, &cl.services, false}, {gvrEndpointSlices, &cl.slices, false}, {k8sclient.PactosGVR, &cl.pactos, true}} {
		items, err := list(into.r, metav1.NamespaceAll, into.optional)
		if err != nil {
			return nil, err
		}
		*into.to = items
	}
	nros, err := list(kubesink.GVR, metav1.NamespaceAll, true)
	if err != nil {
		return nil, err
	}
	if err := cl.readObservations(nros); err != nil {
		return nil, err
	}
	cl.observed = len(cl.snaps) > 0
	return cl, nil
}

// unobserved are the Unknown lines, sorted, for the pods of w whose calls
// the node they run on could not show: no current observation, a failed
// collection, or a pod the observation has not mapped or cannot tell from
// the host. With no observation at all the draft already says so once.
func (cl *cluster) unobserved(w unstructured.Unstructured) []string {
	var out []string
	for _, p := range cl.pods {
		node, _, _ := unstructured.NestedString(p.Object, "spec", "nodeName")
		if !cl.observed || node == "" || cl.owner(p) != key(w) {
			continue
		}
		started, _, _ := unstructured.NestedString(p.Object, "status", "startTime")
		start, _ := time.Parse(time.RFC3339, started) // ponytail: zero when unset; Classify uses it only for Complete
		o := runtimefacts.Classify(cl.nodes[node], string(p.GetUID()), start, cl.now, 0)
		if o.Facts != nil {
			continue
		}
		line := "dependencies seen at runtime: node " + node + " has no current observation, so calls from it could not be seen"
		switch o.Coverage {
		case runtimefacts.CoverageFailed:
			line = "dependencies seen at runtime: node " + node + " failed to collect, so calls from it could not be seen"
		case runtimefacts.CoveragePartial:
			line = "dependencies seen at runtime: pod " + p.GetName() + " on node " + node + " is not mapped in its observation or shares the host network, so its calls could not be seen"
		}
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out
}

func key(o unstructured.Unstructured) string {
	return o.GetNamespace() + "/" + o.GetKind() + "/" + o.GetName()
}

// described are the workloads a Pacto targets, as ns/Kind/name.
func (cl *cluster) described() map[string]bool {
	out := map[string]bool{}
	for _, p := range cl.pactos {
		name, _, _ := unstructured.NestedString(p.Object, "spec", "target", "workloadRef", "name")
		kind, _, _ := unstructured.NestedString(p.Object, "spec", "target", "workloadRef", "kind")
		if name == "" {
			name, _, _ = unstructured.NestedString(p.Object, "spec", "target", "serviceName")
		}
		if kind == "" {
			kind = "Deployment"
		}
		out[p.GetNamespace()+"/"+kind+"/"+name] = true
	}
	return out
}

// inferred maps each workload, as ns/Kind/name, to the services its pods
// called since since. Services that resolve to one contract are merged.
func (cl *cluster) inferred(since time.Time) map[string][]Inferred {
	pods := map[string]unstructured.Unstructured{}
	for _, p := range cl.pods {
		pods[string(p.GetUID())] = p
	}
	out := map[string][]Inferred{}
	for _, svc := range cl.services {
		dep := cl.provider(svc)
		sel, _, _ := unstructured.NestedStringMap(svc.Object, "spec", "selector")
		for _, id := range runtimefacts.Callers(cl.snaps, cl.peers(svc), since) {
			pod, ok := pods[id.ID]
			if !ok || id.Kind != runtimefacts.KindPod || (pod.GetNamespace() == svc.GetNamespace() && selects(sel, pod.GetLabels())) {
				continue // not a pod read here, or the service calling itself
			}
			w := cl.owner(pod)
			out[w] = mergeInferred(out[w], dep)
		}
	}
	for _, list := range out {
		for i := range list {
			finishInferred(&list[i])
		}
	}
	return out
}

// mergeInferred adds d to list: an entry with d's name takes d's Services,
// refs and versions. finishInferred settles what the merged entry proposes.
func mergeInferred(list []Inferred, d Inferred) []Inferred {
	i := slices.IndexFunc(list, func(e Inferred) bool { return e.Name == d.Name })
	if i < 0 {
		return append(list, d)
	}
	e := &list[i]
	if !slices.ContainsFunc(d.services, func(s string) bool { return !slices.Contains(e.services, s) }) {
		return list // another pod of the workload calling Services already merged
	}
	e.services = union(e.services, d.services)
	e.Where = callsSeen(e.services)
	e.refs, e.withRef, e.described = union(e.refs, d.refs), union(e.withRef, d.withRef), union(e.described, d.described)
	for _, u := range d.Unknown {
		if !slices.Contains(e.Unknown, u) {
			e.Unknown = append(e.Unknown, u)
		}
	}
	e.conflict = e.conflict || d.conflict
	e.Versions = slices.Concat(e.Versions, d.Versions)
	return list
}

// finishInferred settles a merged entry: its versions when every Service
// whose Pactos name a contract names the same one, else none and one note
// naming each ref. A proposal names a called Service no Pacto describes.
func finishInferred(e *Inferred) {
	if len(e.refs) > 1 {
		e.Unknown = append(e.Unknown, fmt.Sprintf("the version of %s to propose a range from: %s name different contracts (%s), so no range is proposed",
			e.Name, theServices(e.withRef), joinAnd(e.refs)))
		e.conflict = true
	}
	if len(e.refs) > 0 {
		e.Ref = e.refs[0]
	}
	if e.conflict {
		e.Versions = nil
		return
	}
	e.Versions = ascending(e.Versions)
	if len(e.Versions) == 0 {
		return
	}
	for _, s := range e.services {
		if !slices.Contains(e.withRef, s) {
			e.Unknown = append(e.Unknown, fmt.Sprintf("the version of %s: no reconciled Pacto describing it names a contract, so the proposal comes only from the Pactos describing %s",
				s, theServices(e.described)))
		}
	}
}

// union is the sorted, distinct union of a and b.
func union(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

// callsSeen is where a dependency was found: the Services, as ns/name, its
// calls were seen to.
func callsSeen(services []string) string {
	return "calls seen to " + theServices(services)
}

// theServices names services as "the Service a" or "the Services a and b",
// "" when there are none.
func theServices(services []string) string {
	switch len(services) {
	case 0:
		return ""
	case 1:
		return "the Service " + services[0]
	}
	return "the Services " + joinAnd(services)
}

// joinAnd joins parts as "a, b and c".
func joinAnd(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// ascending sorts semver versions ascending and drops repeats and anything
// that is not a version.
func ascending(versions []string) []string {
	vs := make([]*semver.Version, 0, len(versions))
	for _, s := range versions {
		if v, err := semver.StrictNewVersion(s); err == nil {
			vs = append(vs, v)
		}
	}
	slices.SortFunc(vs, func(a, b *semver.Version) int { return a.Compare(b) })
	vs = slices.CompactFunc(vs, func(a, b *semver.Version) bool { return a.Equal(b) })
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return out
}

// description is what one reconciled Pacto says about the Service it
// describes: name is "" when its status names no contract, version is ""
// when it reports no usable one.
type description struct {
	version, name, ref, pacto string
}

// provider is the dependency a call to svc is on, with every contract
// version the reconciled Pactos describing svc report, when they agree on
// one contract. A Pacto whose status is not reconciled with its spec, or
// that reports no usable version, adds a note and no version; one with a
// ref still has to agree on it.
func (cl *cluster) provider(svc unstructured.Unstructured) Inferred {
	svcID := svc.GetNamespace() + "/" + svc.GetName()
	d := Inferred{Name: svc.GetName(), Where: callsSeen([]string{svcID}), services: []string{svcID}}
	var found []description
	for _, p := range cl.pactos {
		target, _, _ := unstructured.NestedString(p.Object, "spec", "target", "serviceName")
		if p.GetNamespace() != svc.GetNamespace() || target != svc.GetName() {
			continue
		}
		pacto := p.GetNamespace() + "/" + p.GetName()
		note := "the version of " + svcID + " that Pacto " + pacto + " describes: "
		if og, _, _ := unstructured.NestedInt64(p.Object, "status", "observedGeneration"); og != p.GetGeneration() {
			d.Unknown = append(d.Unknown, note+"its status is not reconciled with its spec, so no range is proposed from it")
			continue
		}
		f := description{pacto: pacto}
		if n, _, _ := unstructured.NestedString(p.Object, "status", "contract", "serviceName"); nameRE.MatchString(n) {
			f.name = n
		}
		version, _, _ := unstructured.NestedString(p.Object, "status", "contract", "version")
		ref, _, _ := unstructured.NestedString(p.Object, "status", "contract", "resolvedRef")
		if ref == "" {
			ref, _, _ = unstructured.NestedString(p.Object, "spec", "contractRef", "oci")
		}
		f.ref = "oci://" + refRepo(ref)
		if v, err := semver.StrictNewVersion(version); err == nil {
			f.version = v.String()
		}
		switch {
		case !refRE.MatchString(f.ref):
			d.Unknown = append(d.Unknown, note+"its status and spec name no usable contract ref, so no range is proposed from it")
			continue
		case f.version == "":
			d.Unknown = append(d.Unknown, note+"its status reports no contract version, so no range is proposed from it")
		}
		found = append(found, f)
	}
	return fromPactos(d, found)
}

// fromPactos completes d from the Pactos describing its Service: their
// versions when they agree on one contract, else the first contract by ref
// and a note naming each one.
func fromPactos(d Inferred, found []description) Inferred {
	if len(found) == 0 {
		return d
	}
	slices.SortFunc(found, func(a, b description) int {
		return cmp.Or(cmp.Compare(a.ref, b.ref), cmp.Compare(a.name, b.name), cmp.Compare(a.pacto, b.pacto))
	})
	d.Ref, d.refs, d.withRef = found[0].ref, []string{found[0].ref}, d.services
	var names []string
	for _, f := range found {
		if f.name != "" && !slices.Contains(names, f.name) {
			names = append(names, f.name)
		}
	}
	if len(names) > 0 {
		d.Name = names[0]
	}
	if found[0].ref != found[len(found)-1].ref || len(names) > 1 {
		var each []string
		for _, f := range found {
			each = append(each, cmp.Or(f.name, "an unnamed contract")+" at "+f.ref+" in Pacto "+f.pacto)
		}
		d.Unknown = append(d.Unknown, "the version of "+d.services[0]+" to propose a range from: the Pactos describing it name different contracts ("+joinAnd(each)+"), so no range is proposed")
		d.conflict = true
		return d
	}
	for _, f := range found {
		d.Versions = append(d.Versions, f.version)
	}
	if d.Versions = ascending(d.Versions); len(d.Versions) > 0 {
		d.described = d.services
	}
	return d
}

// owner is the workload a pod belongs to, as ns/Kind/name: a ReplicaSet's
// Deployment, a Job's CronJob, else the pod's own controller or the pod.
func (cl *cluster) owner(pod unstructured.Unstructured) string {
	k := pod.GetNamespace() + "/Pod/" + pod.GetName()
	if ref := metav1.GetControllerOf(&pod); ref != nil {
		k = pod.GetNamespace() + "/" + ref.Kind + "/" + ref.Name
	}
	if o, ok := cl.owners[k]; ok {
		return o
	}
	return k
}

// peers are the addresses a call to svc reaches: its cluster IPs on its
// ports, and its endpoints on theirs.
func (cl *cluster) peers(svc unstructured.Unstructured) []runtimefacts.Address {
	var out []runtimefacts.Address
	ips, _, _ := unstructured.NestedStringSlice(svc.Object, "spec", "clusterIPs")
	if ip, _, _ := unstructured.NestedString(svc.Object, "spec", "clusterIP"); len(ips) == 0 && ip != "" {
		ips = []string{ip}
	}
	ports, _, _ := unstructured.NestedSlice(svc.Object, "spec", "ports")
	for _, ip := range ips {
		for _, p := range ports {
			if n, ok := discover.Dig(p, "port").(int64); ok && ip != "None" {
				out = append(out, runtimefacts.NewAddress(ip, int32(n)))
			}
		}
	}
	for _, s := range cl.slices {
		if s.GetNamespace() != svc.GetNamespace() || s.GetLabels()["kubernetes.io/service-name"] != svc.GetName() {
			continue
		}
		eps, _, _ := unstructured.NestedSlice(s.Object, "endpoints")
		sports, _, _ := unstructured.NestedSlice(s.Object, "ports")
		for _, ep := range eps {
			addrs, _ := discover.Dig(ep, "addresses").([]any)
			for _, a := range addrs {
				for _, p := range sports {
					ip, _ := a.(string)
					if n, ok := discover.Dig(p, "port").(int64); ok {
						out = append(out, runtimefacts.NewAddress(ip, int32(n)))
					}
				}
			}
		}
	}
	return out
}

// selects reports whether a Service selector picks a pod with labels.
func selects(sel, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// manifests is the workload and the Services that select its pods, as
// manifests a folder holds: no namespace, status or server-set fields. A
// sensitive literal in the workload's environment reads from the Secret
// <name>-env instead; it returns that Secret's keys and the rewritten
// variables whose URL named a dependency (secretEnv). w is never changed.
func (cl *cluster) manifests(w unstructured.Unstructured) (string, []string, []string) {
	c := w.DeepCopy()
	labels, _, _ := unstructured.NestedStringMap(c.Object, "spec", "template", "metadata", "labels")
	if c.GetKind() == "CronJob" {
		labels, _, _ = unstructured.NestedStringMap(c.Object, "spec", "jobTemplate", "spec", "template", "metadata", "labels")
	}
	var services []unstructured.Unstructured
	objects := map[string]bool{w.GetName(): true}
	for _, s := range cl.services {
		sel, _, _ := unstructured.NestedStringMap(s.Object, "spec", "selector")
		if s.GetNamespace() == w.GetNamespace() && selects(sel, labels) {
			services, objects[s.GetName()] = append(services, s), true
		}
	}
	keys, urls := secretEnv(c, w.GetName()+"-env", objects)
	docs := []string{plain(*c)}
	for _, s := range services {
		docs = append(docs, plain(s))
	}
	return strings.Join(docs, "---\n"), keys, urls
}

// secretEnv rewrites every env entry of w's containers and initContainers
// whose literal value is sensitive, by name or as a URL with credentials, to
// read the key of the same name from the Secret secret. A name set to
// different literals in two containers reads <container>.<name>, so the two
// never share one value. It returns the Secret's keys and, for each rewritten
// container variable the scan would have read a dependency from, "NAME
// (service)", both sorted; objects are the names the draft defines, which the
// scan never reads as a dependency. The values are compared in memory and
// never kept.
// ponytail: args and command literals stay as they are; 513-S2 strips them.
func secretEnv(w *unstructured.Unstructured, secret string, objects map[string]bool) ([]string, []string) {
	pod := []string{"spec", "template", "spec"}
	if w.GetKind() == "CronJob" {
		pod = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	}
	fields := []string{"containers", "initContainers"}
	lists := make([][]any, len(fields))
	for i, f := range fields {
		lists[i], _, _ = unstructured.NestedSlice(w.Object, slices.Concat(pod, []string{f})...)
	}
	seen, split := map[string]string{}, map[string]bool{}
	sensitiveEnv(lists, func(_ int, _ string, e map[string]any) {
		name, value := e["name"].(string), e["value"].(string)
		if v, ok := seen[name]; ok && v != value {
			split[name] = true
		}
		seen[name] = value
	})
	keys, urls := map[string]bool{}, map[string]bool{}
	sensitiveEnv(lists, func(list int, container string, e map[string]any) {
		k := e["name"].(string)
		// The scan reads containers only (discover.readWorkload).
		if d := discover.EnvDependencies([]discover.EnvVar{{Name: k, Value: e["value"].(string)}}, objects, w.GetName()); list == 0 && len(d) > 0 {
			urls[k+" ("+d[0].Name+")"] = true
		}
		if split[k] {
			k = container + "." + k
		}
		delete(e, "value")
		e["valueFrom"] = map[string]any{"secretKeyRef": map[string]any{"name": secret, "key": k}}
		keys[k] = true
	})
	for i, f := range fields {
		if lists[i] != nil {
			_ = unstructured.SetNestedSlice(w.Object, lists[i], slices.Concat(pod, []string{f})...) // ponytail: the path held a slice a moment ago
		}
	}
	return slices.Sorted(maps.Keys(keys)), slices.Sorted(maps.Keys(urls))
}

// sensitiveEnv calls fn with each env entry whose literal value is sensitive,
// with the index of its list in lists and its container's name.
func sensitiveEnv(lists [][]any, fn func(list int, container string, e map[string]any)) {
	for i, list := range lists {
		for _, c := range list {
			cm, _ := c.(map[string]any)
			name, _ := cm["name"].(string)
			envs, _ := cm["env"].([]any)
			for _, e := range envs {
				em, _ := e.(map[string]any)
				key, _ := em["name"].(string)
				value, _ := em["value"].(string)
				if discover.SensitiveLiteral(key, value) {
					fn(i, name, em)
				}
			}
		}
	}
}

// plain is o as a manifest: apiVersion, kind, name, labels and spec, less
// the addresses the cluster assigned a Service.
func plain(o unstructured.Unstructured) string {
	meta := map[string]any{"name": o.GetName()}
	if l := o.GetLabels(); len(l) > 0 {
		meta["labels"] = l
	}
	spec, _, _ := unstructured.NestedMap(o.Object, "spec")
	delete(spec, "clusterIP")
	delete(spec, "clusterIPs")
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(map[string]any{"apiVersion": o.GetAPIVersion(), "kind": o.GetKind(), "metadata": meta, "spec": spec}) // ponytail: maps from JSON always encode
	return b.String()
}
