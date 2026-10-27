package authn

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// PersonFromToken reads a token the identity layer already verified the way
// the cluster is told to map it (see delivery-identity.md): the username is
// issuer#sub and the groups claim is taken as it is. A token that names no
// subject is Anonymous.
func PersonFromToken(token string) Person {
	var c struct {
		Issuer  string   `json:"iss"`
		Subject string   `json:"sub"`
		Groups  []string `json:"groups"`
	}
	if parts := strings.Split(token, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			_ = json.Unmarshal(payload, &c) // a token without these claims is nobody
		}
	}
	if c.Issuer == "" || c.Subject == "" {
		return Anonymous
	}
	return Person{User: c.Issuer + "#" + c.Subject, Groups: c.Groups}
}

// Review answers whether p may list Pactos in namespace ("" for every
// namespace at once).
type Review func(ctx context.Context, p Person, namespace string) (bool, error)

var sarResource = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "subjectaccessreviews"}

// reviewTimeout bounds one SubjectAccessReview.
const reviewTimeout = 5 * time.Second

// SubjectAccessReview asks the apiserver, with dyn's own credentials (the
// dashboard's ServiceAccount, which needs create on subjectaccessreviews).
func SubjectAccessReview(dyn dynamic.Interface) Review {
	return func(ctx context.Context, p Person, namespace string) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		groups := make([]any, len(p.Groups))
		for i, g := range p.Groups {
			groups[i] = g
		}
		sar := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "authorization.k8s.io/v1", "kind": "SubjectAccessReview",
			"spec": map[string]any{"user": p.User, "groups": groups, "resourceAttributes": map[string]any{
				"namespace": namespace, "verb": "list", "group": "pacto.trianalab.io", "resource": "pactos"}},
		}}
		out, err := dyn.Resource(sarResource).Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		allowed, _, _ := unstructured.NestedBool(out.Object, "status", "allowed")
		return allowed, nil
	}
}

// Viewer narrows fleet answers to the namespaces a person may read, asking
// Kubernetes RBAC once per person and namespace for ttl.
type Viewer struct {
	review Review
	ttl    time.Duration
	now    func() time.Time

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

// Query is q as p sees it. A snapshot with no Kubernetes namespace needs no
// question. A failed review is an error, never a quietly smaller answer.
func (v *Viewer) Query(ctx context.Context, p Person, q *fleet.Query) (*fleet.Query, error) {
	nss := q.Namespaces()
	if len(nss) == 0 {
		return q, nil
	}
	visible, err := v.visible(ctx, p, nss)
	if err != nil {
		return nil, err
	}
	key := strings.Join(visible, "\x00")
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.viewsOf != q.SnapshotID() || len(v.views) >= maxViews {
		v.viewsOf, v.views = q.SnapshotID(), map[string]*fleet.Query{}
	}
	if r, ok := v.views[key]; ok {
		return r, nil
	}
	r := q.Restrict(func(ns string) bool { _, ok := slices.BinarySearch(visible, ns); return ok })
	v.views[key] = r
	return r, nil
}

// visible returns, sorted, the namespaces of nss p may read. One review for
// every namespace at once comes first: it settles a cluster-wide reader.
func (v *Viewer) visible(ctx context.Context, p Person, nss []string) ([]string, error) {
	who := p.User + "\x00" + strings.Join(slices.Sorted(slices.Values(p.Groups)), "\x00")
	all, err := v.ask(ctx, p, who, "")
	if err != nil || all {
		return nss, err
	}
	allowed := make([]bool, len(nss))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(reviewsAtOnce)
	for i, ns := range nss {
		g.Go(func() (err error) { allowed[i], err = v.ask(gctx, p, who, ns); return err })
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var out []string
	for i, ns := range nss {
		if allowed[i] {
			out = append(out, ns)
		}
	}
	return out, nil
}

// ask is one cached review. Failures are not cached.
func (v *Viewer) ask(ctx context.Context, p Person, who, ns string) (bool, error) {
	key := who + "\x01" + ns
	now := v.now()
	v.mu.Lock()
	a, ok := v.answers[key]
	v.mu.Unlock()
	if ok && now.Before(a.until) {
		return a.allowed, nil
	}
	allowed, err := v.review(ctx, p, ns)
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
