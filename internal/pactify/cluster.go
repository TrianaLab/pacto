package pactify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	gvrPactos         = gvr("pacto.trianalab.io", "v1alpha1", "pactos")
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
	cl, err := readCluster(ctx, dyn, c.Namespaces)
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
	f := File{Path: "manifests.yaml", Action: Add, Content: cl.manifests(w)}
	if _, err := os.Stat(filepath.Join(dir, f.Path)); err == nil {
		f = File{Path: f.Path, Action: Keep}
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
	if !cl.observed {
		r.Unknown = append(r.Unknown, "dependencies seen at runtime: the cluster holds no node runtime observations, so no calls were seen")
	}
	return r, err
}

func readCluster(ctx context.Context, dyn dynamic.Interface, namespaces []string) (*cluster, error) {
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}
	cl := &cluster{owners: map[string]string{}}
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
	kinds := []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"}
	for _, ns := range namespaces {
		for _, k := range kinds {
			items, err := list(workloadGVRs[k], ns, false)
			if err != nil {
				return nil, err
			}
			cl.workloads = append(cl.workloads, items...)
		}
		for _, into := range []struct {
			r        schema.GroupVersionResource
			to       *[]unstructured.Unstructured
			optional bool
		}{{gvrServices, &cl.services, false}, {gvrPods, &cl.pods, false}, {gvrEndpointSlices, &cl.slices, false}, {gvrPactos, &cl.pactos, true}} {
			items, err := list(into.r, ns, into.optional)
			if err != nil {
				return nil, err
			}
			*into.to = append(*into.to, items...)
		}
		for _, r := range []schema.GroupVersionResource{gvrReplicaSets, gvrJobs} {
			items, err := list(r, ns, false)
			if err != nil {
				return nil, err
			}
			for _, o := range items {
				if ref := metav1.GetControllerOf(&o); ref != nil {
					cl.owners[o.GetNamespace()+"/"+o.GetKind()+"/"+o.GetName()] = o.GetNamespace() + "/" + ref.Kind + "/" + ref.Name
				}
			}
		}
	}
	sort.Slice(cl.workloads, func(i, j int) bool { return key(cl.workloads[i]) < key(cl.workloads[j]) })
	nros, err := list(kubesink.GVR, metav1.NamespaceAll, true)
	if err != nil {
		return nil, err
	}
	for _, n := range nros {
		raw, _ := json.Marshal(n.Object["status"])
		var s runtimefacts.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("reading the node runtime observation %s: %w", n.GetName(), err)
		}
		cl.snaps = append(cl.snaps, s)
	}
	cl.observed = len(cl.snaps) > 0
	return cl, nil
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
// called since since.
func (cl *cluster) inferred(since time.Time) map[string][]Inferred {
	pods := map[string]unstructured.Unstructured{}
	for _, p := range cl.pods {
		pods[string(p.GetUID())] = p
	}
	out := map[string][]Inferred{}
	for _, svc := range cl.services {
		dep := cl.provider(svc)
		sel, _, _ := unstructured.NestedStringMap(svc.Object, "spec", "selector")
		for _, id := range callers(cl.snaps, cl.peers(svc), since) {
			pod, ok := pods[id.ID]
			if !ok || id.Kind != runtimefacts.KindPod || (pod.GetNamespace() == svc.GetNamespace() && selects(sel, pod.GetLabels())) {
				continue // not a pod read here, or the service calling itself
			}
			w := cl.owner(pod)
			if !slices.ContainsFunc(out[w], func(d Inferred) bool { return d.Name == dep.Name }) {
				out[w] = append(out[w], dep)
			}
		}
	}
	return out
}

// provider is the dependency a call to svc is on: pinned to the contract
// version the Pacto describing svc runs, else only named.
func (cl *cluster) provider(svc unstructured.Unstructured) Inferred {
	d := Inferred{Name: svc.GetName(), Where: "calls seen to the Service " + svc.GetNamespace() + "/" + svc.GetName()}
	for _, p := range cl.pactos {
		target, _, _ := unstructured.NestedString(p.Object, "spec", "target", "serviceName")
		if p.GetNamespace() != svc.GetNamespace() || target != svc.GetName() {
			continue
		}
		if n, _, _ := unstructured.NestedString(p.Object, "status", "contract", "serviceName"); nameRE.MatchString(n) {
			d.Name = n
		}
		version, _, _ := unstructured.NestedString(p.Object, "status", "contract", "version")
		ref, _, _ := unstructured.NestedString(p.Object, "status", "contract", "resolvedRef")
		if ref == "" {
			ref, _, _ = unstructured.NestedString(p.Object, "spec", "contractRef", "oci")
		}
		ref = "oci://" + refRepo(ref)
		if v, err := semver.StrictNewVersion(version); err == nil && refRE.MatchString(ref) {
			d.Version, d.Ref = v.String(), ref
		}
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
			if n, ok := dig(p, "port").(int64); ok && ip != "None" {
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
			addrs, _ := dig(ep, "addresses").([]any)
			for _, a := range addrs {
				for _, p := range sports {
					ip, _ := a.(string)
					if n, ok := dig(p, "port").(int64); ok {
						out = append(out, runtimefacts.NewAddress(ip, int32(n)))
					}
				}
			}
		}
	}
	return out
}

// callers are the workloads with a successful connection to any of peers
// since since, sorted by ID.
// ponytail: the same match as runtimefacts.Callers, which the observed-callers
// work adds; call that instead once it lands.
func callers(snaps []runtimefacts.Snapshot, peers []runtimefacts.Address, since time.Time) []runtimefacts.Identity {
	want := map[runtimefacts.Address]bool{}
	for _, p := range peers {
		want[p] = true
	}
	seen := map[runtimefacts.Identity]bool{}
	var out []runtimefacts.Identity
	for _, s := range snaps {
		for _, w := range s.Workloads {
			for _, c := range w.Connections {
				if want[runtimefacts.NewAddress(c.Address, c.Port)] && !c.LastSuccessAt.IsZero() && !c.LastSuccessAt.Before(since) && !seen[w.Identity] {
					seen[w.Identity] = true
					out = append(out, w.Identity)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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
// manifests a folder holds: no namespace, status or server-set fields.
func (cl *cluster) manifests(w unstructured.Unstructured) string {
	labels, _, _ := unstructured.NestedStringMap(w.Object, "spec", "template", "metadata", "labels")
	if w.GetKind() == "CronJob" {
		labels, _, _ = unstructured.NestedStringMap(w.Object, "spec", "jobTemplate", "spec", "template", "metadata", "labels")
	}
	docs := []string{plain(w)}
	for _, s := range cl.services {
		sel, _, _ := unstructured.NestedStringMap(s.Object, "spec", "selector")
		if s.GetNamespace() == w.GetNamespace() && selects(sel, labels) {
			docs = append(docs, plain(s))
		}
	}
	return strings.Join(docs, "---\n")
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
