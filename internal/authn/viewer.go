package authn

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/memberlink"
)

// Person is who the apiserver authenticates a caller as: the username and
// groups its RBAC decides on.
type Person struct {
	User   string
	Groups []string
}

// Anonymous is a caller who has not signed in, named as the apiserver names one.
var Anonymous = Person{User: "system:anonymous", Groups: []string{"system:unauthenticated"}}

// Claims is how the cluster maps an ID token to a Kubernetes user: the
// username and groups claims and their prefixes, as the API server's
// structured authentication configuration names them (claimMappings). The
// dashboard asks Kubernetes RBAC about a signed-in person as this mapping
// names them, so it must match the API server's. The zero value is the
// mapping dashboard-authentication.md describes: issuer#sub and the groups claim
// as it is.
type Claims struct {
	// UsernameClaim is the claim the username comes from; empty is "sub".
	UsernameClaim string
	// UsernamePrefix is put before the username. Empty is the issuer URL
	// followed by "#", except for the email claim, as kube-apiserver's
	// --oidc-username-prefix does; "-" puts nothing before it.
	UsernamePrefix string
	// GroupsClaim is the claim the groups come from; empty is "groups".
	GroupsClaim string
	// GroupsPrefix is put before every group.
	GroupsPrefix string
}

// jwtClaims is a JWT's payload claims, unverified; nil for anything else.
func jwtClaims(token string) map[string]json.RawMessage {
	var raw map[string]json.RawMessage
	if parts := strings.Split(token, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			_ = json.Unmarshal(payload, &raw) // a token without claims is nobody
		}
	}
	return raw
}

// Person reads a token the identity layer already verified the way the
// cluster is told to map it. Any group starting with "system:" is dropped:
// the apiserver never takes those from an OIDC token, so passing one an IdP
// lets a user name (system:masters) would make the review answer for a group
// the person does not hold. A token that names no username is Anonymous.
func (c Claims) Person(token string) Person {
	raw := jwtClaims(token)
	str := func(name string) string {
		var v string
		_ = json.Unmarshal(raw[name], &v) // a claim that is no string is no name
		return v
	}
	claim := cmp.Or(c.UsernameClaim, "sub")
	user, iss := str(claim), str("iss")
	if user == "" || iss == "" {
		return Anonymous
	}
	switch {
	case c.UsernamePrefix == "-":
	case c.UsernamePrefix != "":
		user = c.UsernamePrefix + user
	case claim != "email":
		user = iss + "#" + user
	}
	var groups []string
	if g := raw[cmp.Or(c.GroupsClaim, "groups")]; json.Unmarshal(g, &groups) != nil {
		// Some providers send a single group as a string.
		var one string
		if json.Unmarshal(g, &one) == nil && one != "" {
			groups = []string{one}
		}
	}
	out := []string{}
	for _, g := range groups {
		if g = c.GroupsPrefix + g; !strings.HasPrefix(g, "system:") {
			out = append(out, g)
		}
	}
	return Person{User: user, Groups: out}
}

// Review answers whether p may list res (or res.Verb it) in namespace (""
// for every namespace at once).
type Review func(ctx context.Context, p Person, namespace string, res ResourceAttributes) (bool, error)

// ResourceAttributes is the API group and resource a review asks about, the
// verb asked (empty means list) and, for a question about one object, its
// name.
type ResourceAttributes struct {
	Group    string
	Resource string
	Verb     string
	Name     string
}

// ClusterJoinResource is the virtual resource a person must be allowed to
// approve, named by the member's UID, for their approval of a member cluster
// to count. No CRD serves it: only RBAC names it.
var ClusterJoinResource = ResourceAttributes{Group: "pacto.trianalab.io", Resource: "clusterjoins", Verb: "approve"}

// ServiceCreateResource is the virtual resource a person must be allowed to
// create, in the target namespace and named by the service, to create a
// service from a golden path. No CRD serves it: only RBAC names it, so a
// grant can narrow it with resourceNames.
var ServiceCreateResource = ResourceAttributes{Group: "pacto.trianalab.io", Resource: "services", Verb: "create"}

// EnvironmentLadderResource is the virtual resource a person must be allowed
// to approve, in the cluster, for their confirmation of its installation
// ladder to count. No CRD serves it: only RBAC names it.
var EnvironmentLadderResource = ResourceAttributes{Group: "delivery.pacto.trianalab.io", Resource: "environmentladders", Verb: "approve"}

// PactoResource is the resource every fleet answer is reviewed against.
var PactoResource = reviewable("Pacto")

// workloadResources maps the workload kind of an inferred service to the
// resource a person must be allowed to list to see it.
var workloadResources = reviewables("Deployment", "StatefulSet", "DaemonSet", "CronJob")

// gitopsResources maps the GitOps kind a repository was read from, or a
// provider row counts, to the resource a person must be allowed to list to
// see it.
var gitopsResources = reviewables("Application", "GitRepository", "Kustomization", "HelmRelease")

// reviewable is the resource kind decides, from the one list a member
// cluster may be asked about.
func reviewable(kind string) ResourceAttributes {
	r := memberlink.ReviewableResources[kind]
	return ResourceAttributes{Group: r.Group, Resource: r.Resource}
}

// reviewables maps each kind to the resource it decides.
func reviewables(kinds ...string) map[string]ResourceAttributes {
	out := make(map[string]ResourceAttributes, len(kinds))
	for _, k := range kinds {
		out[k] = reviewable(k)
	}
	return out
}

// reviewedAs is every resource a person must be allowed to list in a
// namespace to see kind there, nil for a kind nobody sees, and whether kind
// also needs that namespace's Pactos. A workload does, and so does a Helm
// release, read from workload annotations and needing every workload kind. A
// GitOps object does not: it is another namespace's object, its repository
// shows beside a workload the person already sees, so only list on its kind
// there decides it.
func reviewedAs(kind string) (res []ResourceAttributes, needsPactos bool) {
	if r, ok := workloadResources[kind]; ok {
		return []ResourceAttributes{r}, true
	}
	if r, ok := gitopsResources[kind]; ok {
		return []ResourceAttributes{r}, false
	}
	if kind == fleet.HelmReleaseKind {
		w := workloadResources
		return []ResourceAttributes{w["Deployment"], w["StatefulSet"], w["DaemonSet"], w["CronJob"]}, true
	}
	return nil, false
}

var sarResource = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "subjectaccessreviews"}

// reviewTimeout bounds one SubjectAccessReview.
const reviewTimeout = 5 * time.Second

// SubjectAccessReview asks the apiserver, with dyn's own credentials (the
// dashboard's ServiceAccount, which needs create on subjectaccessreviews).
func SubjectAccessReview(dyn dynamic.Interface) Review {
	return func(ctx context.Context, p Person, namespace string, res ResourceAttributes) (bool, error) {
		return postReview(ctx, dyn.Resource(sarResource), reviewObject("SubjectAccessReview", p, namespace, res))
	}
}

var lsarResource = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "localsubjectaccessreviews"}

// LocalSubjectAccessReview asks the apiserver one namespace at a time, with
// dyn's own credentials, which need create on localsubjectaccessreviews in
// that namespace only: a namespaced Role grants it. Every namespace at once
// ("") has no namespaced form, so it is answered false without a request.
func LocalSubjectAccessReview(dyn dynamic.Interface) Review {
	return func(ctx context.Context, p Person, namespace string, res ResourceAttributes) (bool, error) {
		if namespace == "" {
			return false, nil
		}
		obj := reviewObject("LocalSubjectAccessReview", p, namespace, res)
		obj.SetNamespace(namespace)
		return postReview(ctx, dyn.Resource(lsarResource).Namespace(namespace), obj)
	}
}

// reviewObject is a review of kind asking whether p may do res's verb (list
// when empty) on res in namespace, on the one object res names when set.
func reviewObject(kind string, p Person, namespace string, res ResourceAttributes) *unstructured.Unstructured {
	groups := make([]any, len(p.Groups))
	for i, g := range p.Groups {
		groups[i] = g
	}
	attrs := map[string]any{"namespace": namespace, "verb": cmp.Or(res.Verb, "list"), "group": res.Group, "resource": res.Resource}
	if res.Name != "" {
		attrs["name"] = res.Name
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": kind,
		"spec": map[string]any{"user": p.User, "groups": groups, "resourceAttributes": attrs},
	}}
}

// postReview creates review through r and reads its answer.
func postReview(ctx context.Context, r dynamic.ResourceInterface, review *unstructured.Unstructured) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
	defer cancel()
	out, err := r.Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}
	allowed, _, _ := unstructured.NestedBool(out.Object, "status", "allowed")
	return allowed, nil
}

// Viewer narrows fleet answers to what a person may read in each cluster,
// asking each cluster's own reviewer once per person, resource and namespace
// for ttl.
type Viewer struct {
	reviews Reviews
	// home is the reviewer key of the sign-in cluster, the only one that
	// reviews a cluster-local identity (ServiceAccount, node); nil takes the
	// only reviewer when there is one.
	home func(context.Context) (string, bool)
	ttl  time.Duration
	now  func() time.Time
	// scoped viewers never ask about every namespace at once and ask only
	// about the watched namespaces; every other namespace is not visible.
	scoped  bool
	watched map[string]bool

	mu      sync.Mutex
	answers map[string]verdict
	// views are restricted queries of the snapshot viewsOf, one per set of
	// visible pairs and access marks, so people who see the same share one.
	viewsOf string
	views   map[string]*fleet.Query
}

// Reviews is the reviewer of each cluster, keyed by cluster id; the key ""
// reviews the items that carry no cluster id. A cluster with no reviewer is
// not reviewable: its items are hidden from every signed-in reader.
type Reviews func(ctx context.Context) map[string]Review

type verdict struct {
	allowed bool
	until   time.Time
}

// Bounds of the caches. ponytail: past them a cache starts over; an LRU when
// one dashboard serves more people than that.
const (
	maxAnswers = 8192
	maxViews   = 64
	// reviewsAtOnce bounds the reviews one request has in flight per cluster.
	reviewsAtOnce = 8
)

// clusterReviewTimeout bounds every review of one cluster for one request:
// past it the cluster is hidden as review-failed and the others still answer.
// A var so tests can shorten it.
var clusterReviewTimeout = reviewTimeout

// NewViewer asks each cluster's reviewer, keeping each answer a minute.
func NewViewer(reviews Reviews) *Viewer {
	return &Viewer{reviews: reviews, ttl: time.Minute, now: time.Now, answers: map[string]verdict{}, views: map[string]*fleet.Query{}}
}

// NewScopedViewer asks each cluster's reviewer only about the watched
// namespaces, never about every namespace at once, so nobody reads the whole
// fleet through it and a service without a namespace shows to nobody. Every
// other namespace, such as that of an observed caller in kube-system, is not
// visible and is never asked about: the reviewer may hold no grant there.
func NewScopedViewer(reviews Reviews, watched []string) *Viewer {
	v := NewViewer(reviews)
	v.scoped, v.watched = true, map[string]bool{}
	for _, ns := range watched {
		v.watched[ns] = true
	}
	return v
}

// SignInCluster sets the reviewer key of the cluster people sign in against:
// a ServiceAccount or node identity exists only there, so only that cluster
// reviews one. Unknown (false) reviews such an identity nowhere.
func (v *Viewer) SignInCluster(home func(context.Context) (string, bool)) *Viewer {
	v.home = home
	return v
}

// clusterWork is what one cluster's reviewer is asked and what it answered.
type clusterWork struct {
	key    string
	review Review
	// nss and scopes are the namespaces and workload scopes of the items this
	// reviewer decides; shows says there is at least one.
	nss    []string
	scopes []fleet.WorkloadScope
	shows  bool

	visible      []string
	all          bool
	workloads    map[fleet.WorkloadScope]bool
	allWorkloads bool
	err          error
}

// sees reports whether the reviewer let the person read anything.
func (w *clusterWork) sees() bool { return w.all || len(w.visible) > 0 || len(w.workloads) > 0 }

// Query is q as p sees it. Each cluster is reviewed by its own reviewer, in
// parallel and within clusterReviewTimeout: a cluster-wide reader there sees
// all of it, anyone else the namespaces they may read. An inferred service
// also needs p to be allowed to list its workload kind in its namespace. A
// service without a namespace shows only to someone every reviewer answered
// cluster-wide yes for. A cluster with no reviewer, whose review failed or
// where p may read nothing (with several clusters) is hidden, named in a
// CLUSTER_HIDDEN limitation, and the answer is partial. Query fails only when
// ctx ends.
func (v *Viewer) Query(ctx context.Context, p Person, q *fleet.Query) (*fleet.Query, error) {
	who := reviewer(p)
	m := v.reviews(ctx)
	route := v.router(ctx, m, p)
	work, access := v.plan(m, route, q)
	v.reviewAll(ctx, p, who, work)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	everywhere := readsEverywhere(m, work)
	for _, w := range work {
		markAccess(ctx, access, w, len(m) > 1)
	}
	if everywhere && len(access) == 0 {
		return q, nil
	}
	visible, workloadVisible := predicates(work, route, everywhere)
	key := viewKey(work, access, everywhere)
	v.mu.Lock()
	r, ok := v.views[key]
	ok = ok && v.viewsOf == q.SnapshotID()
	v.mu.Unlock()
	if ok {
		return r, nil
	}
	// Restricting a large snapshot takes a while, so it runs unlocked: two
	// people asking at once may both build the same view.
	r = q.Restrict(visible, workloadVisible).WithClusterAccess(q, access)
	v.mu.Lock()
	if v.viewsOf != q.SnapshotID() || len(v.views) >= maxViews {
		v.viewsOf, v.views = q.SnapshotID(), map[string]*fleet.Query{}
	}
	v.views[key] = r
	v.mu.Unlock()
	return r, nil
}

// readsEverywhere reports whether every reviewer of m answered that p reads
// every namespace and every workload kind in its cluster; false without one.
func readsEverywhere(m map[string]Review, work map[string]*clusterWork) bool {
	everywhere := len(m) > 0
	for k := range m {
		w := work[k]
		everywhere = everywhere && w != nil && w.err == nil && w.all && w.allWorkloads
	}
	return everywhere
}

// predicates are the Restrict predicates of the reviewed work: a pair or a
// workload scope is visible when its cluster's reviewer let p read it, and
// the items without a cluster or namespace when every reviewer did.
func predicates(work map[string]*clusterWork, route func(string) (string, bool), everywhere bool) (func(string, string) bool, func(string, string, string) bool) {
	visible := func(cluster, ns string) bool {
		if cluster == "" && ns == "" {
			return everywhere
		}
		w := routed(work, route, cluster)
		if w == nil {
			return false
		}
		_, ok := slices.BinarySearch(w.visible, ns)
		return w.all || (ok && ns != "")
	}
	workloadVisible := func(cluster, ns, kind string) bool {
		w := routed(work, route, cluster)
		return w != nil && w.workloads[fleet.WorkloadScope{Namespace: ns, Kind: kind}]
	}
	return visible, workloadVisible
}

// router returns, for a cluster id, the key of the reviewer that decides its
// items for p, false when none does.
func (v *Viewer) router(ctx context.Context, m map[string]Review, p Person) func(string) (string, bool) {
	local := clusterLocal(p)
	var home string
	homeOK := false
	if local {
		home, homeOK = v.homeKey(ctx, m)
	}
	return func(cluster string) (string, bool) {
		key, ok := reviewerFor(m, cluster)
		if ok && local && (!homeOK || key != home) {
			return "", false
		}
		return key, ok
	}
}

// homeKey is the reviewer key of the sign-in cluster.
func (v *Viewer) homeKey(ctx context.Context, m map[string]Review) (string, bool) {
	if v.home != nil {
		return v.home(ctx)
	}
	if len(m) == 1 {
		for k := range m {
			return k, true
		}
	}
	return "", false
}

// reviewerFor is the key of the reviewer of cluster: its own, or the only
// one for an item with no cluster id (a single cluster names nothing).
func reviewerFor(m map[string]Review, cluster string) (string, bool) {
	if _, ok := m[cluster]; ok {
		return cluster, true
	}
	if cluster == "" && len(m) == 1 {
		for k := range m {
			return k, true
		}
	}
	return "", false
}

// clusterLocal reports an identity that exists in one cluster only: a
// ServiceAccount or a node.
func clusterLocal(p Person) bool {
	return strings.HasPrefix(p.User, "system:serviceaccount:") || strings.HasPrefix(p.User, "system:node:") ||
		slices.Contains(p.Groups, "system:serviceaccounts") || slices.Contains(p.Groups, "system:nodes")
}

// routed is the work of cluster's reviewer, nil when it has none or failed.
func routed(work map[string]*clusterWork, route func(string) (string, bool), cluster string) *clusterWork {
	key, ok := route(cluster)
	if w := work[key]; ok && w != nil && w.err == nil {
		return w
	}
	return nil
}

// plan groups q's namespaces and workload scopes by the reviewer that
// decides them and marks the clusters no reviewer decides. A viewer that is
// not scoped also asks every other reviewer about every namespace at once,
// which a service without a namespace needs.
func (v *Viewer) plan(m map[string]Review, route func(string) (string, bool), q *fleet.Query) (map[string]*clusterWork, map[string]string) {
	work, access := map[string]*clusterWork{}, map[string]string{}
	of := func(cluster string) *clusterWork {
		key, ok := route(cluster)
		if !ok {
			access[cluster] = fleet.ClusterNotReviewable
			return nil
		}
		if work[key] == nil {
			work[key] = &clusterWork{key: key, review: m[key]}
		}
		return work[key]
	}
	for _, cn := range q.Namespaces() {
		if w := of(cn.Cluster); w != nil {
			w.nss, w.shows = append(w.nss, cn.Namespace), true
		}
	}
	for _, sc := range q.InferredWorkloadScopes() {
		if w := of(sc.Cluster); w != nil {
			w.scopes, w.shows = append(w.scopes, fleet.WorkloadScope{Namespace: sc.Namespace, Kind: sc.Kind}), true
		}
	}
	// A cluster only a non-Kubernetes target or a provider row carries still
	// needs a reviewer, or the full-snapshot shortcut would show it unreviewed.
	for _, c := range q.ClusterIDs() {
		of(c)
	}
	if !v.scoped {
		for k := range m {
			if _, ok := route(k); ok && work[k] == nil {
				work[k] = &clusterWork{key: k, review: m[k]}
			}
		}
	}
	for _, w := range work {
		slices.Sort(w.nss)
		w.nss = slices.Compact(w.nss)
		slices.SortFunc(w.scopes, func(a, b fleet.WorkloadScope) int {
			return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Kind, b.Kind))
		})
		w.scopes = slices.Compact(w.scopes)
	}
	return work, access
}

// reviewAll reviews every cluster's work at once, each within
// clusterReviewTimeout.
func (v *Viewer) reviewAll(ctx context.Context, p Person, who string, work map[string]*clusterWork) {
	var wg sync.WaitGroup
	for _, w := range work {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, clusterReviewTimeout)
			defer cancel()
			v.reviewCluster(cctx, p, who, w)
		})
	}
	wg.Wait()
}

// reviewCluster asks w's reviewer about w's namespaces, then its workload
// scopes.
func (v *Viewer) reviewCluster(ctx context.Context, p Person, who string, w *clusterWork) {
	w.visible, w.all, w.err = v.visible(ctx, w, p, who, w.nss)
	if w.err == nil {
		w.workloads, w.allWorkloads, w.err = v.workloads(ctx, w, p, who, w.scopes, w.all, w.visible)
	}
}

// markAccess records why a cluster shows nothing: its review failed, or
// (with several reviewers, for a cluster with items) p may read nothing
// there. A failure is logged with the cluster id.
func markAccess(ctx context.Context, access map[string]string, w *clusterWork, several bool) {
	if w.err != nil {
		slog.WarnContext(ctx, "reviewing which namespaces a person may read failed", "cluster", w.key, "error", w.err)
	}
	switch {
	case w.err != nil:
		// Even with no namespace to decide, a failed review hides what has
		// no namespace, so it is named, never read as a permission denial.
		access[w.key] = fleet.ClusterReviewFailed
	case w.shows && several && !w.sees():
		access[w.key] = fleet.ClusterNoAccess
	}
}

// viewKey names the view work and access build, so people who see the same
// share it.
func viewKey(work map[string]*clusterWork, access map[string]string, everywhere bool) string {
	var b strings.Builder
	if everywhere {
		b.WriteString("\x04all")
	}
	for _, k := range slices.Sorted(maps.Keys(work)) {
		w := work[k]
		b.WriteString("\x03" + k)
		switch {
		case w.err != nil:
			b.WriteString("\x01failed")
			continue
		case w.all:
			b.WriteString("\x01all")
		}
		b.WriteString("\x01" + strings.Join(w.visible, "\x00"))
		for _, sc := range w.scopes {
			if w.workloads[sc] {
				b.WriteString("\x02" + sc.Namespace + "\x00" + sc.Kind)
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(access)) {
		b.WriteString("\x05" + k + "\x00" + access[k])
	}
	return b.String()
}

// visible returns, sorted, the namespaces of nss p may read in w's cluster,
// and whether p reads every namespace there. One review for every namespace
// at once comes first: it settles a cluster-wide reader. A scoped viewer
// skips it and asks only about the watched namespaces.
func (v *Viewer) visible(ctx context.Context, w *clusterWork, p Person, who string, nss []string) ([]string, bool, error) {
	if !v.scoped {
		all, err := v.ask(ctx, w, p, who, "", PactoResource)
		if err != nil || all {
			return nss, all, err
		}
	}
	allowed := make([]bool, len(nss))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(reviewsAtOnce)
	for i, ns := range nss {
		if v.scoped && !v.watched[ns] {
			continue
		}
		g.Go(func() (err error) { allowed[i], err = v.ask(gctx, w, p, who, ns, PactoResource); return err })
	}
	if err := g.Wait(); err != nil {
		return nil, false, err
	}
	var out []string
	for i, ns := range nss {
		if allowed[i] {
			out = append(out, ns)
		}
	}
	return out, false, nil
}

// workloads returns the scopes p may list the workload kind of, and whether p
// may list every such kind cluster-wide. An unknown kind is never visible. A
// viewer that is not scoped asks once per kind about every namespace at once
// (only for a cluster-wide Pacto reader, the one it could settle) and then
// about each scope in a visible namespace, or in any namespace for a kind
// that needs no Pactos there; a scoped viewer asks only about scopes in
// watched namespaces.
func (v *Viewer) workloads(ctx context.Context, w *clusterWork, p Person, who string, scopes []fleet.WorkloadScope, allNS bool, visible []string) (map[fleet.WorkloadScope]bool, bool, error) {
	everywhere := map[string]bool{}
	if allNS && !v.scoped {
		var err error
		if everywhere, err = v.kindsEverywhere(ctx, w, p, who, scopes); err != nil {
			return nil, false, err
		}
	}
	allowed := make([]bool, len(scopes))
	all := true
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(reviewsAtOnce)
	for i, sc := range scopes {
		res, needsPactos := reviewedAs(sc.Kind)
		_, nsOK := slices.BinarySearch(visible, sc.Namespace)
		switch {
		case everywhere[sc.Kind]:
			allowed[i] = true
		case res == nil || (v.scoped && !v.watched[sc.Namespace]) || (needsPactos && !allNS && !nsOK):
			all = false
		default:
			all = false
			g.Go(func() (err error) { allowed[i], err = v.askAll(gctx, w, p, who, sc.Namespace, res); return err })
		}
	}
	if err := g.Wait(); err != nil {
		return nil, false, err
	}
	out := map[fleet.WorkloadScope]bool{}
	for i, sc := range scopes {
		if allowed[i] {
			out[sc] = true
		}
	}
	return out, all, nil
}

// kindsEverywhere asks, once per known workload kind of scopes, whether p may
// list it in every namespace at once.
func (v *Viewer) kindsEverywhere(ctx context.Context, w *clusterWork, p Person, who string, scopes []fleet.WorkloadScope) (map[string]bool, error) {
	out := map[string]bool{}
	for _, sc := range scopes {
		res, _ := reviewedAs(sc.Kind)
		if _, asked := out[sc.Kind]; res != nil && !asked {
			ok, err := v.askAll(ctx, w, p, who, "", res)
			if err != nil {
				return nil, err
			}
			out[sc.Kind] = ok
		}
	}
	return out, nil
}

// askAll reports whether p may list every one of res in ns.
func (v *Viewer) askAll(ctx context.Context, w *clusterWork, p Person, who, ns string, res []ResourceAttributes) (bool, error) {
	for _, r := range res {
		if ok, err := v.ask(ctx, w, p, who, ns, r); err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// reviewer is p as an answer cache key: the user and the sorted groups.
func reviewer(p Person) string {
	return p.User + "\x00" + strings.Join(slices.Sorted(slices.Values(p.Groups)), "\x00")
}

// ErrClusterNotReviewable is a question about a cluster no reviewer decides
// for the person.
var ErrClusterNotReviewable = errors.New("this cluster cannot review you")

// PactoKind names a Pacto resource to MayUpdateWorkload.
const PactoKind = "Pacto"

// MayUpdateWorkload asks the reviewer of cluster (the only one when cluster
// is empty) whether p may update the workload kind/name in namespace (or the
// Pacto resource name, for PactoKind): the rule delivery counts approvals
// by. A kind Pacto does not read is never allowed; a cluster nobody reviews p in is ErrClusterNotReviewable.
func (v *Viewer) MayUpdateWorkload(ctx context.Context, p Person, cluster, namespace, kind, name string) (bool, error) {
	res, ok := workloadResources[kind]
	if kind == PactoKind {
		res, ok = PactoResource, true
	}
	if !ok {
		return false, nil
	}
	res.Verb, res.Name = "update", name
	return v.MayUpdate(ctx, p, cluster, namespace, res)
}

// MayUpdate asks the reviewer of cluster (the only one when cluster is
// empty) whether p may do res (its verb and name) in namespace, "" for a
// cluster-scoped object. A cluster nobody reviews p in, and a member cluster,
// which answers only list over its fixed kinds, are ErrClusterNotReviewable.
func (v *Viewer) MayUpdate(ctx context.Context, p Person, cluster, namespace string, res ResourceAttributes) (bool, error) {
	m := v.reviews(ctx)
	key, ok := v.router(ctx, m, p)(cluster)
	if !ok {
		return false, ErrClusterNotReviewable
	}
	allowed, err := v.ask(ctx, &clusterWork{key: key, review: m[key]}, p, reviewer(p), namespace, res)
	if errors.Is(err, errMemberListOnly) {
		return false, ErrClusterNotReviewable
	}
	return allowed, err
}

// MayCreateService asks the reviewer of cluster (the only one when cluster
// is empty) whether p may create the service name in namespace
// (ServiceCreateResource named by the service). A cluster nobody reviews p
// in is ErrClusterNotReviewable.
func (v *Viewer) MayCreateService(ctx context.Context, p Person, cluster, namespace, name string) (bool, error) {
	res := ServiceCreateResource
	res.Name = name
	return v.MayUpdate(ctx, p, cluster, namespace, res)
}

// MayUpdatePactos asks the reviewer of the sign-in cluster, the one Pacto is
// installed in (the only one when nothing names it), whether p may update
// pactos in namespace; ErrClusterNotReviewable when no reviewer decides it.
func (v *Viewer) MayUpdatePactos(ctx context.Context, p Person, namespace string) (bool, error) {
	m := v.reviews(ctx)
	key, ok := v.homeKey(ctx, m)
	if _, has := m[key]; !ok || !has {
		return false, ErrClusterNotReviewable
	}
	res := PactoResource
	res.Verb = "update"
	return v.ask(ctx, &clusterWork{key: key, review: m[key]}, p, reviewer(p), namespace, res)
}

// ask is one cached review in w's cluster. Failures are not cached.
func (v *Viewer) ask(ctx context.Context, w *clusterWork, p Person, who, ns string, res ResourceAttributes) (bool, error) {
	key := w.key + "\x01" + who + "\x01" + res.Resource + "." + res.Group + "\x01" + cmp.Or(res.Verb, "list") + "\x01" + res.Name + "\x01" + ns
	now := v.now()
	v.mu.Lock()
	a, ok := v.answers[key]
	v.mu.Unlock()
	if ok && now.Before(a.until) {
		return a.allowed, nil
	}
	allowed, err := w.review(ctx, p, ns, res)
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	if len(v.answers) >= maxAnswers {
		v.answers = map[string]verdict{}
	}
	v.answers[key] = verdict{allowed: allowed, until: now.Add(v.ttl)}
	v.mu.Unlock()
	return allowed, nil
}
