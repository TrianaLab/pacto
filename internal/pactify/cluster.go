package pactify

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
	"github.com/trianalab/pacto/v4/pkg/contract"
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
	// MaxItems bounds how many items each source reads; 0 is
	// DefaultMaxItems. A source stopped by it reads partial.
	MaxItems int
	// CallWindow is how far back a call counts as a dependency; 0 is
	// DefaultCallWindow.
	CallWindow time.Duration
	// Continue resumes a read the workload bound stopped, from the token
	// that read returned in Bulk.Continue.
	Continue string
	// Visible says which namespaces the person the read runs for may read;
	// nil means the API server already authorized the caller's credentials.
	Visible func(namespace string) bool
	// Workload, when set, drafts only the one workload it names: name,
	// ns/name or ns/Kind/name. None, or several, is an error.
	Workload string
}

// The ways ClusterOptions.Workload can fail to name one workload to draft.
var (
	ErrWorkloadNotFound  = errors.New("workload not found")
	ErrWorkloadAmbiguous = errors.New("workload ambiguous")
	ErrAlreadyDeclared   = errors.New("workload already declared")
)

// matchError is a workload match refusal: its message, and the kind of
// refusal errors.Is finds.
type matchError struct {
	kind error
	msg  string
}

func (e *matchError) Error() string { return e.msg }
func (e *matchError) Unwrap() error { return e.kind }

// The defaults of ClusterOptions.
const (
	DefaultMaxItems   = 10000
	DefaultCallWindow = 24 * time.Hour
)

// Bulk is what pacto init did to several services.
type Bulk struct {
	Results []*Result `json:"results"`
	// Skipped are the workloads a Pacto already describes, as ns/Kind/name.
	Skipped []string `json:"skipped"`
	// Coverage is how completely a cluster read saw each source.
	Coverage []contract.InferredCoverage `json:"coverage,omitempty"`
	// Continue resumes a cluster read the workload bound stopped.
	Continue string `json:"continue,omitempty"`
}

func gvr(group, version, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
}

var (
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
	// coverage is how completely each source was read, by source.
	coverage map[string]contract.InferredCoverage
	// cont resumes the read when the workload bound stopped it.
	cont string
	// hiddenServices and hiddenCallers count, per workload as ns/Kind/name,
	// the Services a viewer may not read that its pods called and the pods
	// a viewer may not read that called its Services (restrict).
	hiddenServices, hiddenCallers map[string]int
}

// Cluster drafts a contract for every workload in the namespaces that no
// Pacto describes yet: it writes the workload and its Services into
// <out>/<ns>/<name>/manifests.yaml, pactifies that folder like any other and
// adds the dependencies its pods were seen calling. A draft that fails
// validation does not stop the others; the error names it.
func Cluster(ctx context.Context, dyn dynamic.Interface, base Options, c ClusterOptions, writeFile func(string, []byte, os.FileMode) error) (*Bulk, error) {
	if err := c.check(base); err != nil {
		return nil, err
	}
	nss := slices.Compact(slices.Sorted(slices.Values(c.Namespaces)))
	if len(nss) == 0 {
		nss = []string{metav1.NamespaceAll}
	}
	// A namespace the viewer may not read is never listed, so no bound row
	// counts what it holds. The refusal names only what the caller asked for.
	// ponytail: a read of every namespace still counts hidden ones toward the
	// bound rows; 513-S3 scopes a viewer's read to the namespaces it may see.
	if i := slices.IndexFunc(nss, func(ns string) bool { return c.Visible != nil && ns != metav1.NamespaceAll && !c.Visible(ns) }); i >= 0 {
		return nil, fmt.Errorf("the namespace %s is not one you may read", nss[i])
	}
	cur, err := decodeContinue(c.Continue, nss)
	if err != nil {
		return nil, err
	}
	cl, err := readCluster(ctx, dyn, c, nss, cur)
	since := c.Now().Add(-c.CallWindow)
	cl.restrict(c.Visible, since)
	b := &Bulk{Results: []*Result{}, Skipped: []string{}, Coverage: cl.rows(), Continue: cl.cont}
	if err != nil {
		return b, err
	}
	deps := cl.inferred(since)
	described := cl.described()
	if err := cl.only(c.Workload, described); err != nil {
		return b, err
	}
	var errs []error
	for _, w := range cl.workloads {
		ns, name := w.GetNamespace(), w.GetName()
		k := key(w)
		if described[k] {
			b.Skipped = append(b.Skipped, k)
			continue
		}
		r, err := cl.draft(ctx, w, base, deps[k], c, writeFile)
		if r != nil {
			b.Results = append(b.Results, r)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", ns, name, err))
		}
	}
	return b, errors.Join(errs...)
}

// check refuses options no read can run with, once their defaults are
// filled in.
func (c *ClusterOptions) check(base Options) error {
	switch {
	case c.Out == "":
		return errors.New("reading a cluster needs a folder to write the drafts in (--out)")
	case base.Name != "" || base.Dir != "":
		return errors.New("reading a cluster names each service after its workload, so it takes no name or folder")
	}
	return c.defaults()
}

// defaults fills the zero options in and refuses the ones no read can run
// with.
func (c *ClusterOptions) defaults() error {
	if c.Now == nil {
		c.Now = time.Now
	}
	c.MaxItems, c.CallWindow = cmp.Or(c.MaxItems, DefaultMaxItems), cmp.Or(c.CallWindow, DefaultCallWindow)
	switch {
	case c.MaxItems < 0:
		return errors.New("the item bound (--max-items) must be positive")
	case c.CallWindow < time.Minute:
		return errors.New("the call window (--call-window) must be at least a minute")
	case c.Visible != nil && c.Continue != "":
		return errors.New("a continue token is not available when reading on behalf of another person")
	}
	return nil
}

// draft pactifies one workload in <out>/<ns>/<name>. A manifests.yaml
// already there is the user's and is kept; a plan reads the new one from a
// scratch folder, so it writes nothing under out.
func (cl *cluster) draft(ctx context.Context, w unstructured.Unstructured, base Options, deps []Inferred, c ClusterOptions,
	writeFile func(string, []byte, os.FileMode) error) (*Result, error) {
	dir := filepath.Join(c.Out, w.GetNamespace(), w.GetName())
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
	facts := cl.facts(w, deps, f.Action == Add, c)
	opts := base
	opts.Dir, opts.Name, opts.Inferred, opts.Cluster = scanned, w.GetName(), deps, &facts
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
	// A service another variable still lists as a dependency needs no line.
	urls = slices.DeleteFunc(urls, func(u string) bool {
		return slices.ContainsFunc(r.Dependencies, func(d Finding) bool { return strings.HasSuffix(u, " ("+d.Name+")") })
	})
	if len(urls) > 0 {
		r.Next = append(r.Next, fmt.Sprintf("manifests.yaml: %s held a URL naming the service in brackets, which the draft now reads from a Secret, so the draft does not list "+
			"that service as a dependency: add it under dependencies", strings.Join(urls, ", ")))
	}
	if !cl.observed {
		r.Unknown = append(r.Unknown, "dependencies seen at runtime: the cluster holds no node runtime observations, so no calls were seen")
	}
	if p, ok := cl.coverage["pactos"]; ok && p.State != contract.CoverageComplete {
		r.Unknown = append(r.Unknown, fmt.Sprintf("whether a Pacto already describes this workload: the Pacto list was %s (%s), so this draft may duplicate a contract", p.State, p.Reason))
	}
	r.Unknown = append(r.Unknown, cl.unobserved(w)...)
	return r, err
}

// restrict drops every workload, pod, Service, EndpointSlice and Pacto in a
// namespace visible does not show, before anything is drafted, after
// counting what was dropped (countHidden). No name of a dropped object
// leaves it. A viewer gets no continue token either, since it carries the
// API server's, which names the next object. A nil visible changes nothing.
func (cl *cluster) restrict(visible func(string) bool, since time.Time) {
	if visible == nil {
		return
	}
	cl.cont = ""
	cl.hiddenServices, cl.hiddenCallers = cl.countHidden(visible, since)
	hidden := func(o unstructured.Unstructured) bool { return !visible(o.GetNamespace()) }
	for _, l := range []*[]unstructured.Unstructured{&cl.workloads, &cl.pods, &cl.services, &cl.slices, &cl.pactos} {
		*l = slices.DeleteFunc(*l, hidden)
	}
	maps.DeleteFunc(cl.owners, func(k, _ string) bool { return !visible(strings.SplitN(k, "/", 2)[0]) })
}

// countHidden counts per workload the distinct hidden Services its visible
// pods called since since, and the distinct hidden pods that called a
// visible Service selecting its pods. Only pods read here are known, so a
// caller in a namespace the read did not cover is not counted.
func (cl *cluster) countHidden(visible func(string) bool, since time.Time) (services, callers map[string]int) {
	pods := map[string]unstructured.Unstructured{}
	for _, p := range cl.pods {
		pods[string(p.GetUID())] = p
	}
	seen := map[[3]string]bool{}
	services, callers = map[string]int{}, map[string]int{}
	add := func(m map[string]int, which, w, id string) {
		if k := [3]string{which, w, id}; !seen[k] {
			seen[k] = true
			m[w]++
		}
	}
	for _, svc := range cl.services {
		shown := visible(svc.GetNamespace())
		for _, id := range runtimefacts.Callers(cl.snaps, cl.peers(svc), since) {
			pod, ok := pods[id.ID]
			if !ok || id.Kind != runtimefacts.KindPod || visible(pod.GetNamespace()) == shown {
				continue
			}
			if shown {
				for _, w := range cl.selected(svc) {
					add(callers, "caller", w, id.ID)
				}
				continue
			}
			add(services, "service", cl.owner(pod), svc.GetNamespace()+"/"+svc.GetName())
		}
	}
	return services, callers
}

// selected are the workloads, as ns/Kind/name, whose pods svc selects.
func (cl *cluster) selected(svc unstructured.Unstructured) []string {
	sel, _, _ := unstructured.NestedStringMap(svc.Object, "spec", "selector")
	var out []string
	for _, p := range cl.pods {
		if w := cl.owner(p); p.GetNamespace() == svc.GetNamespace() && selects(sel, p.GetLabels()) && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// readNamespace reads one namespace's pods and the owners of its
// ReplicaSets and Jobs, each source up to its bound.
func (cl *cluster) readNamespace(rd *reader, ns string) {
	pods, _, _, _ := rd.page(gvrPods, ns, "pods", false, "")
	cl.pods = append(cl.pods, pods...)
	for _, s := range []struct {
		r      schema.GroupVersionResource
		source string
	}{{gvrReplicaSets, "replicasets"}, {gvrJobs, "jobs"}} {
		items, _, _, _ := rd.page(s.r, ns, s.source, false, "")
		for _, o := range items {
			if ref := metav1.GetControllerOf(&o); ref != nil {
				cl.owners[o.GetNamespace()+"/"+o.GetKind()+"/"+o.GetName()] = o.GetNamespace() + "/" + ref.Kind + "/" + ref.Name
			}
		}
	}
}

// readWorkloads reads the workloads of nss from cur, each kind of each
// namespace in turn, until the end or the workload bound. Stopped by the
// bound, it keeps where to resume in cl.cont. It returns the error of a
// workload list when none succeeded.
func (cl *cluster) readWorkloads(rd *reader, nss []string, cur cursor) error {
	var first error
	read := false
	tok := cur.C
	for i := cur.I; i < len(nss); i++ {
		for k := cur.K; k < len(workloadKinds); k++ {
			items, next, more, err := rd.page(k8sclient.WorkloadGVRs[workloadKinds[k]], nss[i], "workloads", false, tok)
			tok = ""
			cl.workloads = append(cl.workloads, items...)
			if more {
				cl.cont = encodeContinue(cursor{N: cur.N, I: i, K: k, C: next})
				return nil
			}
			if err != nil {
				first = cmp.Or(first, err)
				continue
			}
			read = true
		}
		cur.K = 0
	}
	if !read {
		return first
	}
	return nil
}

// workloadKinds are the workload kinds read, in the order a continue token
// counts them.
var workloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"}

// readObservations reads the node runtime observations' snapshots. A
// malformed one is skipped and fails the runtime-calls source.
func (cl *cluster) readObservations(rd *reader, nros []unstructured.Unstructured) {
	for _, n := range nros {
		raw, _ := json.Marshal(n.Object["status"])
		var s runtimefacts.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			rd.mark("runtime-calls", contract.CoverageFailed, "listing failed (malformed observation)")
			continue
		}
		cl.snaps = append(cl.snaps, s)
	}
	for i := range cl.snaps {
		cl.nodes[cl.snaps[i].NodeName] = &cl.snaps[i]
	}
}

func readCluster(ctx context.Context, dyn dynamic.Interface, c ClusterOptions, nss []string, cur cursor) (*cluster, error) {
	cl := &cluster{owners: map[string]string{}, nodes: map[string]*runtimefacts.Snapshot{}, now: c.Now()}
	rd := &reader{ctx: ctx, dyn: dyn, max: c.MaxItems, count: map[string]int{}, coverage: map[string]contract.InferredCoverage{}}
	cl.coverage = rd.coverage
	err := cl.readWorkloads(rd, nss, cur)
	for _, ns := range nss {
		cl.readNamespace(rd, ns)
	}
	sort.Slice(cl.workloads, func(i, j int) bool { return key(cl.workloads[i]) < key(cl.workloads[j]) })
	// Services, their endpoints and Pactos are read in every namespace: a pod
	// here may call a Service anywhere, and only the drafts keep to the
	// namespaces asked for.
	for _, into := range []struct {
		r        schema.GroupVersionResource
		source   string
		to       *[]unstructured.Unstructured
		optional bool
	}{{gvrServices, "services", &cl.services, false}, {gvrEndpointSlices, "endpointslices", &cl.slices, false},
		{k8sclient.PactosGVR, "pactos", &cl.pactos, true}} {
		*into.to, _, _, _ = rd.page(into.r, metav1.NamespaceAll, into.source, into.optional, "")
	}
	nros, _, _, _ := rd.page(kubesink.GVR, metav1.NamespaceAll, "runtime-calls", true, "")
	cl.readObservations(rd, nros)
	cl.observed = len(cl.snaps) > 0
	if !cl.observed {
		rd.mark("runtime-calls", contract.CoverageFailed, "no node runtime observations")
	}
	return cl, err
}

// reader lists sources page by page, each up to its bound, and keeps how
// completely it saw each source.
type reader struct {
	ctx      context.Context
	dyn      dynamic.Interface
	max      int
	count    map[string]int
	coverage map[string]contract.InferredCoverage
}

// page lists r in ns from the API continue tok, counting toward source's
// bound, until the list ends or the bound is reached. more is true when the
// bound stopped it before the list ended (or before it started), with next
// the API continue to resume from. A list error fails the source and is
// returned; a missing optional CRD is no error and no rows.
// ponytail: one bound per source; a per-namespace bound if one namespace starves the rest.
func (rd *reader) page(r schema.GroupVersionResource, ns, source string, optional bool, tok string) ([]unstructured.Unstructured, string, bool, error) {
	var items []unstructured.Unstructured
	for {
		left := rd.max - rd.count[source]
		if left <= 0 {
			rd.mark(source, contract.CoveragePartial, fmt.Sprintf("stopped at the item bound (%d)", rd.max))
			return items, tok, true, nil
		}
		l, err := rd.dyn.Resource(r).Namespace(ns).List(rd.ctx, metav1.ListOptions{Limit: int64(min(500, left)), Continue: tok})
		switch {
		case optional && apierrors.IsNotFound(err):
			return items, "", false, nil // the CRD is not installed
		case err != nil:
			rd.mark(source, contract.CoverageFailed, failure(err))
			return items, "", false, fmt.Errorf("listing %s: %w", r.Resource, err)
		}
		// ponytail: a server ignoring Limit sends more than left; the whole page
		// is kept, so the bound overshoots by that page and no item is skipped
		// on resume.
		tok = l.GetContinue()
		rd.count[source] += len(l.Items)
		items = append(items, l.Items...)
		if tok == "" {
			return items, "", false, nil
		}
	}
}

// mark records state for source unless a worse one is already recorded:
// failed outranks partial, which outranks complete.
func (rd *reader) mark(source, state, reason string) {
	rank := map[string]int{contract.CoverageComplete: 0, contract.CoveragePartial: 1, contract.CoverageFailed: 2}
	if old, ok := rd.coverage[source]; ok && rank[old.State] >= rank[state] {
		return
	}
	rd.coverage[source] = contract.InferredCoverage{Source: source, State: state, Reason: reason}
}

// rows are the coverage rows in contract.CoverageSources order, a source
// with no row being complete.
func (cl *cluster) rows() []contract.InferredCoverage {
	out := make([]contract.InferredCoverage, 0, len(contract.CoverageSources))
	for _, s := range contract.CoverageSources {
		out = append(out, cmp.Or(cl.coverage[s], contract.InferredCoverage{Source: s, State: contract.CoverageComplete}))
	}
	return out
}

// failure is the fixed coverage reason for a list error: none names an
// object, since an API server error text may.
func failure(err error) string {
	var status apierrors.APIStatus
	switch {
	case apierrors.IsForbidden(err):
		return "forbidden"
	case apierrors.IsNotFound(err):
		return "not found"
	case apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err):
		return "timed out"
	case apierrors.IsResourceExpired(err):
		return "the continue token expired"
	case errors.As(err, &status) && status.Status().Code >= 100 && status.Status().Code <= 599:
		return fmt.Sprintf("listing failed (%d)", status.Status().Code)
	}
	return "listing failed (no response)"
}

// cursor is where a read stopped by the workload bound resumes: the
// sha256 of the sorted namespace list, the namespace and workload kind
// indexes and the API continue.
type cursor struct {
	N string `json:"n"`
	I int    `json:"i"`
	K int    `json:"k"`
	C string `json:"c,omitempty"`
}

func encodeContinue(c cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeContinue reads token for a read of nss; "" starts at the beginning.
func decodeContinue(token string, nss []string) (cursor, error) {
	sum := sha256.Sum256([]byte(strings.Join(nss, "\n")))
	c := cursor{N: hex.EncodeToString(sum[:])}
	if token == "" {
		return c, nil
	}
	var got cursor
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil {
		err = json.Unmarshal(raw, &got)
	}
	switch {
	case err != nil || got.I < 0 || got.I >= len(nss) || got.K < 0 || got.K >= len(workloadKinds):
		return c, errors.New("the continue token is malformed: run again without the token")
	case got.N != c.N:
		return c, errors.New("the continue token was made for other namespaces: run again without the token")
	}
	return got, nil
}

// unobserved are the Unknown lines, sorted, for the pods of w whose calls
// the node they run on could not show: no current observation, a failed
// collection, or a pod the observation has not mapped or cannot tell from
// the host. With no observation at all the draft already says so once.
func (cl *cluster) unobserved(w unstructured.Unstructured) []string {
	var out []string
	pods := 0
	cl.eachPod(w, func(p unstructured.Unstructured, node string, o runtimefacts.Observed) {
		pods++
		if o.Facts != nil {
			return
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
	})
	if cl.observed && pods == 0 {
		out = append(out, "dependencies seen at runtime: no pod of the workload was running on a node when the cluster was read, so no calls were seen")
	}
	sort.Strings(out)
	return out
}

// eachPod calls fn with each pod of w scheduled on a node, that node and
// what its observation shows of the pod, when any node was observed.
func (cl *cluster) eachPod(w unstructured.Unstructured, fn func(p unstructured.Unstructured, node string, o runtimefacts.Observed)) {
	for _, p := range cl.pods {
		node, _, _ := unstructured.NestedString(p.Object, "spec", "nodeName")
		if !cl.observed || node == "" || cl.owner(p) != key(w) {
			continue
		}
		started, _, _ := unstructured.NestedString(p.Object, "status", "startTime")
		start, _ := time.Parse(time.RFC3339, started) // ponytail: zero when unset; Classify uses it only for Complete
		fn(p, node, runtimefacts.Classify(cl.nodes[node], string(p.GetUID()), start, cl.now, 0))
	}
}

// facts is what the read shows about w beyond the folder drafted from it,
// deps being the services its pods called; snap says the folder's
// manifests.yaml was written from this read. A call is partial when a node
// of w's pods was not observed, a source it is read through was not read in
// full or w called a Service the viewer may not read (Decision 6).
func (cl *cluster) facts(w unstructured.Unstructured, deps []Inferred, snap bool, c ClusterOptions) discover.ClusterFacts {
	f := discover.ClusterFacts{Object: key(w), AsOf: cl.now, CallWindow: c.CallWindow, MaxItems: c.MaxItems, Images: cl.images(w), Hidden: cl.hidden(w)}
	if snap {
		f.Snapshots = []string{"manifests.yaml"}
	}
	asOf, k, m := cl.nodesObserved(w)
	partial := k < m || f.Hidden.Services > 0 || !cl.complete("pods", "replicasets", "jobs", "services", "endpointslices")
	for _, d := range deps {
		f.Calls = append(f.Calls, discover.ClusterCall{Name: d.Name, Ref: d.Ref, Proposed: proposedRange(d.Versions), Versions: reportedVersions(d.Versions),
			AsOf: asOf, Partial: partial || len(d.Unknown) > 0 || !cl.complete("pactos")})
	}
	f.Coverage = cl.rows()
	rc := &f.Coverage[len(f.Coverage)-1] // runtime-calls, for this workload
	if reason := cl.callsCut(f.Hidden.Services, k, m); rc.State == contract.CoverageComplete && reason != "" {
		*rc = contract.InferredCoverage{Source: rc.Source, State: contract.CoveragePartial, Reason: reason}
	}
	return f
}

// callsCut is why a workload's runtime calls were not all seen, "" when they
// were: a called Service is hidden, a node of its pods was not observed, its
// pods may not all be tied to it, or none of its pods ran on a node, so a
// workload with no call seen never reads as calling nothing.
func (cl *cluster) callsCut(hidden, k, m int) string {
	switch {
	case hidden > 0:
		return fmt.Sprintf("%d called services you may not read", hidden)
	case k < m:
		return fmt.Sprintf("%d of %d nodes observed", k, m)
	case !cl.complete("pods", "replicasets", "jobs"):
		return "the pods were not all read"
	case m == 0:
		return "no running pods observed"
	}
	return ""
}

// nodesObserved is how many (k) of the m nodes w's pods run on showed every
// pod of w there, and the oldest observation among them.
func (cl *cluster) nodesObserved(w unstructured.Unstructured) (asOf time.Time, k, m int) {
	seen := map[string]bool{} // a node, to whether it showed every pod of w on it
	cl.eachPod(w, func(_ unstructured.Unstructured, node string, o runtimefacts.Observed) {
		if s := cl.nodes[node]; s != nil && !s.LastPublished.IsZero() && (asOf.IsZero() || s.LastPublished.Before(asOf)) {
			asOf = s.LastPublished
		}
		ok, known := seen[node]
		seen[node] = (!known || ok) && o.Facts != nil
	})
	for _, ok := range seen {
		if ok {
			k++
		}
	}
	return asOf, k, len(seen)
}

// reportedVersions joins versions for a cluster:fleet source, dropping the
// highest ones that would take it past the record's 256-byte bound.
func reportedVersions(versions []string) string {
	const room = 256 - len("cluster:fleet reported versions ")
	out := strings.Join(versions, ", ")
	for n := len(versions) - 1; len(out) > room; n-- {
		out = strings.Join(versions[:n], ", ")
	}
	return out
}

// complete reports whether every one of sources was read in full.
func (cl *cluster) complete(sources ...string) bool {
	return !slices.ContainsFunc(sources, func(s string) bool {
		return cmp.Or(cl.coverage[s].State, contract.CoverageComplete) != contract.CoverageComplete
	})
}

// hidden counts what w's draft may not name (restrict).
func (cl *cluster) hidden(w unstructured.Unstructured) contract.InferredHidden {
	return contract.InferredHidden{Services: cl.hiddenServices[key(w)], Callers: cl.hiddenCallers[key(w)]}
}

// digestRE is the sha256 digest at the end of a container status imageID.
var digestRE = regexp.MustCompile(`@(sha256:[0-9a-f]{64})$`)

// images are the digests the containers of w's pods run, partial when the
// pods, or the ReplicaSets and Jobs that tie them to w, were not all read.
func (cl *cluster) images(w unstructured.Unstructured) []discover.ClusterImage {
	var out []discover.ClusterImage
	for _, p := range cl.pods {
		statuses, _, _ := unstructured.NestedSlice(p.Object, "status", "containerStatuses")
		for _, st := range statuses {
			name, _ := discover.Dig(st, "name").(string)
			id, _ := discover.Dig(st, "imageID").(string)
			if m := digestRE.FindStringSubmatch(id); m != nil && name != "" && cl.owner(p) == key(w) {
				out = append(out, discover.ClusterImage{Container: name, Digest: m[1], Partial: !cl.complete("pods", "replicasets", "jobs")})
			}
		}
	}
	return out
}

func key(o unstructured.Unstructured) string {
	return o.GetNamespace() + "/" + o.GetKind() + "/" + o.GetName()
}

// only keeps the one workload w names (name, ns/name or ns/Kind/name) that
// no Pacto describes; an empty w keeps them all. It runs after restrict, so
// a workload the viewer may not read is never counted.
func (cl *cluster) only(w string, described map[string]bool) error {
	if w == "" {
		return nil
	}
	parts := strings.Split(w, "/")
	match := func(o unstructured.Unstructured) bool {
		switch len(parts) {
		case 1:
			return o.GetName() == w
		case 2:
			return o.GetNamespace() == parts[0] && o.GetName() == parts[1]
		}
		return key(o) == w
	}
	var found []unstructured.Unstructured
	declared := ""
	for _, o := range cl.workloads {
		switch {
		case !match(o):
		case described[key(o)]:
			declared = key(o)
		default:
			found = append(found, o)
		}
	}
	switch {
	case len(found) == 1:
		cl.workloads = found
		return nil
	case len(found) > 1:
		return &matchError{ErrWorkloadAmbiguous, fmt.Sprintf("%d workloads are named %s: pass ns/Kind/name", len(found), w)}
	case declared != "":
		return &matchError{ErrAlreadyDeclared, declared + " already has a contract: change it with pacto publish"}
	}
	return &matchError{ErrWorkloadNotFound, "no workload named " + w + " that no Pacto describes"}
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
// never share one value. It rewrites each sensitive literal in their command
// and args the same way (secretArgs). It returns the Secret's keys and, for
// each rewritten variable the scan would have read a dependency from, "NAME
// (service)", both sorted; objects are the names the draft defines, which the
// scan never reads as a dependency. The values are compared in memory and
// never kept.
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
	keys, urls, taken := map[string]bool{}, map[string]bool{}, map[string]string{}
	sensitiveEnv(lists, func(_ int, container string, e map[string]any) {
		k, value := e["name"].(string), e["value"].(string)
		if d := discover.EnvDependencies([]discover.EnvVar{{Name: k, Value: value}}, objects, w.GetName()); len(d) > 0 {
			urls[k+" ("+d[0].Name+")"] = true
		}
		if split[k] {
			k = container + "." + k
		}
		delete(e, "value")
		e["valueFrom"] = map[string]any{"secretKeyRef": map[string]any{"name": secret, "key": k}}
		keys[k], taken[k] = true, value
	})
	for _, list := range lists {
		for _, c := range list {
			if cm, ok := c.(map[string]any); ok {
				secretArgs(cm, secret, taken, keys)
			}
		}
	}
	for i, f := range fields {
		if lists[i] != nil {
			_ = unstructured.SetNestedSlice(w.Object, lists[i], slices.Concat(pod, []string{f})...) // ponytail: the path held a slice a moment ago
		}
	}
	return slices.Sorted(maps.Keys(keys)), slices.Sorted(maps.Keys(urls))
}

// argAssign is a command or args item that sets a value: --flag=value,
// -f=value or NAME=value.
var argAssign = regexp.MustCompile(`^(--?)?([A-Za-z_][A-Za-z0-9_.-]*)=(.+)$`)

// argFlag is a bare --flag or -f item, whose value may be the next item.
var argFlag = regexp.MustCompile(`^--?([A-Za-z][A-Za-z0-9_.-]*)$`)

// argWord is a whitespace-separated word of a shell script.
var argWord = regexp.MustCompile(`\S+`)

// args rewrites the sensitive literals in one container's command and args.
type args struct {
	cm        map[string]any
	container string
	secret    string
	env       map[string]bool   // the names the container's env already sets
	added     map[string]string // the env names args added, to their literal
	taken     map[string]string // the Secret's keys, to their literal
	keys      map[string]bool
}

// secretArgs rewrites, in the container c's command and args, each literal
// that is sensitive by its flag's name or as a URL with credentials: an item
// --flag=literal or NAME=literal, the item after a bare --flag or -f, a bare
// URL, and the same forms as words of a sh -c or bash -c script. Each becomes
// $(NAME), NAME being the upper-cased flag (URL for a bare URL), ARG_NAME when
// the container's env already sets NAME. An env entry NAME reading the key
// NAME of the Secret secret joins the container's own env, because Kubernetes
// expands $(NAME) only from it; a key taken by a different literal reads
// <container>.NAME. taken maps the Secret's keys to their literal and keys
// collects them.
// ponytail: a script word is split on whitespace, so a quoted value or one a ; ends is moved whole with its quotes; a shell parser when a script needs one.
func secretArgs(c map[string]any, secret string, taken map[string]string, keys map[string]bool) {
	a := &args{cm: c, secret: secret, env: map[string]bool{}, added: map[string]string{}, taken: taken, keys: keys}
	a.container, _ = c["name"].(string)
	envs, _ := c["env"].([]any)
	for _, e := range envs {
		em, _ := e.(map[string]any)
		n, _ := em["name"].(string)
		a.env[n] = true
	}
	cmd, _ := c["command"].([]any)
	arg, _ := c["args"].([]any)
	argv := slices.Concat(cmd, arg)
	a.rewrite(argv)
	if cmd != nil {
		c["command"] = argv[:len(cmd)]
	}
	if arg != nil {
		c["args"] = argv[len(cmd):]
	}
}

// rewrite rewrites argv in place.
func (a *args) rewrite(argv []any) {
	for i := 0; i < len(argv); i++ {
		s, _ := argv[i].(string)
		next := ""
		if i+1 < len(argv) {
			next, _ = argv[i+1].(string)
		}
		flag := argFlag.FindStringSubmatch(s)
		switch {
		case (path.Base(s) == "sh" || path.Base(s) == "bash") && next == "-c" && i+2 < len(argv):
			if script, ok := argv[i+2].(string); ok {
				argv[i+2] = argWord.ReplaceAllStringFunc(script, a.word)
			}
			i += 2
		case flag != nil && !strings.HasPrefix(next, "-") && discover.SensitiveLiteral(flag[1], next):
			argv[i+1] = a.ref(flag[1], next)
			i++
		default:
			if w := a.word(s); w != s {
				argv[i] = w
			}
		}
	}
}

// word is s with its sensitive literal read from the Secret.
func (a *args) word(s string) string {
	if m := argAssign.FindStringSubmatch(s); m != nil && discover.SensitiveLiteral(m[2], m[3]) {
		return m[1] + m[2] + "=" + a.ref(m[2], m[3])
	}
	if discover.SensitiveLiteral("", s) {
		return a.ref("url", s)
	}
	return s
}

// ref is the $(NAME) reference that reads literal, the value of flag, from
// the Secret, adding the env entry NAME the first time.
func (a *args) ref(flag, literal string) string {
	name := strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
	if a.env[name] {
		name = "ARG_" + name
	}
	base := name
	for n := 2; a.env[name] || a.added[name] != "" && a.added[name] != literal; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	if a.added[name] == "" {
		key := name
		if v, ok := a.taken[key]; ok && v != literal {
			key = a.container + "." + name
		}
		a.added[name], a.taken[key], a.keys[key] = literal, literal, true
		envs, _ := a.cm["env"].([]any)
		a.cm["env"] = append(envs, map[string]any{"name": name, "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": a.secret, "key": key}}})
	}
	return "$(" + name + ")"
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
