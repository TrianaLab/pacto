package authn

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
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

// Person reads a token the identity layer already verified the way the
// cluster is told to map it. Any group starting with "system:" is dropped:
// the apiserver never takes those from an OIDC token, so passing one an IdP
// lets a user name (system:masters) would make the review answer for a group
// the person does not hold. A token that names no username is Anonymous.
func (c Claims) Person(token string) Person {
	var raw map[string]json.RawMessage
	if parts := strings.Split(token, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			_ = json.Unmarshal(payload, &raw) // a token without claims is nobody
		}
	}
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

// Review answers whether p may list res in namespace ("" for every namespace
// at once).
type Review func(ctx context.Context, p Person, namespace string, res ResourceAttributes) (bool, error)

// ResourceAttributes is the API group and resource a review asks about.
type ResourceAttributes struct {
	Group    string
	Resource string
}

// PactoResource is the resource every fleet answer is reviewed against.
var PactoResource = ResourceAttributes{Group: "pacto.trianalab.io", Resource: "pactos"}

// workloadResources maps the workload kind of an inferred service to the
// resource a person must be allowed to list to see it.
var workloadResources = map[string]ResourceAttributes{
	"Deployment":  {Group: "apps", Resource: "deployments"},
	"StatefulSet": {Group: "apps", Resource: "statefulsets"},
	"DaemonSet":   {Group: "apps", Resource: "daemonsets"},
	"CronJob":     {Group: "batch", Resource: "cronjobs"},
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

// reviewObject is a review of kind asking whether p may list res in namespace.
func reviewObject(kind string, p Person, namespace string, res ResourceAttributes) *unstructured.Unstructured {
	groups := make([]any, len(p.Groups))
	for i, g := range p.Groups {
		groups[i] = g
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": kind,
		"spec": map[string]any{"user": p.User, "groups": groups, "resourceAttributes": map[string]any{
			"namespace": namespace, "verb": "list", "group": res.Group, "resource": res.Resource}},
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

// Viewer narrows fleet answers to the namespaces a person may read, asking
// Kubernetes RBAC once per person and namespace for ttl.
type Viewer struct {
	review Review
	ttl    time.Duration
	now    func() time.Time
	// scoped viewers never ask about every namespace at once and ask only
	// about the watched namespaces; every other namespace is not visible.
	scoped  bool
	watched map[string]bool

	mu      sync.Mutex
	answers map[string]verdict
	// views are restricted queries of the snapshot viewsOf, one per set of
	// visible namespaces, so people who see the same namespaces share one.
	viewsOf string
	views   map[string]*fleet.Query
}

type verdict struct {
	allowed bool
	until   time.Time
}

// Bounds of the caches. ponytail: past them a cache starts over; an LRU when
// one dashboard serves more people than that.
const (
	maxAnswers = 8192
	maxViews   = 64
	// reviewsAtOnce bounds the reviews one request has in flight.
	reviewsAtOnce = 8
)

// NewViewer asks review, keeping each answer a minute.
func NewViewer(review Review) *Viewer {
	return &Viewer{review: review, ttl: time.Minute, now: time.Now, answers: map[string]verdict{}, views: map[string]*fleet.Query{}}
}

// NewScopedViewer asks review only about the watched namespaces, never about
// every namespace at once, so nobody reads the whole fleet through it and a
// service without a namespace shows to nobody. Every other namespace, such as
// that of an observed caller in kube-system, is not visible and is never
// asked about: the reviewer may hold no grant there.
func NewScopedViewer(review Review, watched []string) *Viewer {
	v := NewViewer(review)
	v.scoped, v.watched = true, map[string]bool{}
	for _, ns := range watched {
		v.watched[ns] = true
	}
	return v
}

// ErrReview is what a person learns of a failed review. It names no
// namespace: an apiserver error names the one it was asked in, which the
// person may not read. The cause is logged.
var ErrReview = errors.New("the apiserver could not review which namespaces you may read")

// Query is q as p sees it. A cluster-wide reader sees all of it; anyone else
// sees the namespaces they may read and nothing without a namespace, such as
// a service known only from a registry. An inferred service also needs p to
// be allowed to list its workload kind in its namespace, so a cluster-wide
// Pacto reader sees all of q only when they may list every such kind
// cluster-wide too. A failed review is ErrReview, never a quietly smaller
// answer.
func (v *Viewer) Query(ctx context.Context, p Person, q *fleet.Query) (*fleet.Query, error) {
	who := p.User + "\x00" + strings.Join(slices.Sorted(slices.Values(p.Groups)), "\x00")
	scopes := q.InferredWorkloadScopes()
	visible, all, err := v.visible(ctx, p, who, q.Namespaces())
	var workloads map[fleet.WorkloadScope]bool
	allWorkloads := false
	if err == nil {
		workloads, allWorkloads, err = v.workloads(ctx, p, who, scopes, all, visible)
	}
	if err != nil {
		slog.WarnContext(ctx, "reviewing which namespaces a person may read failed", "error", err)
		return nil, ErrReview
	}
	if all && allWorkloads {
		return q, nil
	}
	nsVisible := func(ns string) bool { _, ok := slices.BinarySearch(visible, ns); return ok && ns != "" }
	key := strings.Join(visible, "\x00")
	if all {
		nsVisible, key = func(string) bool { return true }, "\x01all"
	}
	for _, sc := range scopes {
		if workloads[sc] {
			key += "\x02" + sc.Namespace + "\x00" + sc.Kind
		}
	}
	v.mu.Lock()
	r, ok := v.views[key]
	ok = ok && v.viewsOf == q.SnapshotID()
	v.mu.Unlock()
	if ok {
		return r, nil
	}
	// Restricting a large snapshot takes a while, so it runs unlocked: two
	// people asking at once may both build the same view.
	r = q.Restrict(nsVisible, func(ns, kind string) bool { return workloads[fleet.WorkloadScope{Namespace: ns, Kind: kind}] })
	v.mu.Lock()
	if v.viewsOf != q.SnapshotID() || len(v.views) >= maxViews {
		v.viewsOf, v.views = q.SnapshotID(), map[string]*fleet.Query{}
	}
	v.views[key] = r
	v.mu.Unlock()
	return r, nil
}

// visible returns, sorted, the namespaces of nss p may read, and whether p
// reads every namespace. One review for every namespace at once comes first:
// it settles a cluster-wide reader. A scoped viewer skips it and asks only
// about the watched namespaces.
func (v *Viewer) visible(ctx context.Context, p Person, who string, nss []string) ([]string, bool, error) {
	if !v.scoped {
		all, err := v.ask(ctx, p, who, "", PactoResource)
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
		g.Go(func() (err error) { allowed[i], err = v.ask(gctx, p, who, ns, PactoResource); return err })
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
// about each scope in a visible namespace; a scoped viewer asks only about
// scopes in watched namespaces.
func (v *Viewer) workloads(ctx context.Context, p Person, who string, scopes []fleet.WorkloadScope, allNS bool, visible []string) (map[fleet.WorkloadScope]bool, bool, error) {
	everywhere := map[string]bool{}
	if allNS && !v.scoped {
		var err error
		if everywhere, err = v.kindsEverywhere(ctx, p, who, scopes); err != nil {
			return nil, false, err
		}
	}
	allowed := make([]bool, len(scopes))
	all := true
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(reviewsAtOnce)
	for i, sc := range scopes {
		res, known := workloadResources[sc.Kind]
		_, nsOK := slices.BinarySearch(visible, sc.Namespace)
		switch {
		case everywhere[sc.Kind]:
			allowed[i] = true
		case !known || (v.scoped && !v.watched[sc.Namespace]) || (!allNS && !nsOK):
			all = false
		default:
			all = false
			g.Go(func() (err error) { allowed[i], err = v.ask(gctx, p, who, sc.Namespace, res); return err })
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
func (v *Viewer) kindsEverywhere(ctx context.Context, p Person, who string, scopes []fleet.WorkloadScope) (map[string]bool, error) {
	out := map[string]bool{}
	for _, sc := range scopes {
		res, known := workloadResources[sc.Kind]
		if _, asked := out[sc.Kind]; known && !asked {
			ok, err := v.ask(ctx, p, who, "", res)
			if err != nil {
				return nil, err
			}
			out[sc.Kind] = ok
		}
	}
	return out, nil
}

// ask is one cached review. Failures are not cached.
func (v *Viewer) ask(ctx context.Context, p Person, who, ns string, res ResourceAttributes) (bool, error) {
	key := who + "\x01" + ns + "\x01" + res.Resource + "." + res.Group
	now := v.now()
	v.mu.Lock()
	a, ok := v.answers[key]
	v.mu.Unlock()
	if ok && now.Before(a.until) {
		return a.allowed, nil
	}
	allowed, err := v.review(ctx, p, ns, res)
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
