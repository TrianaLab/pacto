package cli

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// sharedReads is the dashboard's one copy of the delivery objects every view
// lists: each Stage and Operation, listed and watched with the dashboard's
// own ServiceAccount, which may get, list and watch them and nothing more.
// So fifty people looking at once cost the apiserver one watch, not fifty
// lists each.
//
// A person reads from it only what their own RBAC allows: before a list or
// get is answered from memory, the apiserver is asked, in a
// SubjectAccessReview for that person, whether they may make that same
// request (a list across every namespace needs a cluster-wide allow). The
// person is who the apiserver says their token is (a SelfSubjectReview), so
// the dashboard's claim mapping cannot make it ask about someone else. A
// denial is the apiserver's own 403, so the views name the namespace as
// unreadable exactly as before. Whatever it cannot answer exactly (not yet
// synced, a selector or option it does not evaluate, a missing object, a
// person who just wrote) goes to the apiserver with the person's own token.
// Every write goes with the person's token too; this cache never writes.
type sharedReads struct {
	informers map[schema.GroupVersionResource]cache.SharedIndexInformer
	// sa is the dashboard's own client, for SubjectAccessReviews.
	sa dynamic.Interface
	// wrote reports whether the bearer just wrote through the dashboard.
	wrote func(bearer string) bool
	ttl   time.Duration
	now   func() time.Time

	mu       sync.Mutex
	subjects map[string]subjectVerdict
	answers  map[string]verdict
	group    singleflight.Group
}

// subject is a person as the apiserver authenticates them.
type subject struct {
	User   string              `json:"username"`
	UID    string              `json:"uid,omitempty"`
	Groups []string            `json:"groups,omitempty"`
	Extra  map[string][]string `json:"extra,omitempty"`
}

type subjectVerdict struct {
	s     subject
	until time.Time
}

type verdict struct {
	allowed bool
	until   time.Time
}

// sharedGVRs are the delivery objects the dashboard keeps, and the fields of
// each a list may select on: the ones the CRD declares selectable.
var sharedGVRs = map[schema.GroupVersionResource][]string{
	{Group: deliveryGroup, Version: "v1alpha1", Resource: "stages"}:     nil,
	{Group: deliveryGroup, Version: "v1alpha1", Resource: "operations"}: {"spec.stage", "status.outcome"},
}

const deliveryGroup = "delivery.pacto.trianalab.io"

// Bounds. A permission answer is kept 10 seconds, so a revoked RoleBinding
// stops showing a namespace within that; ponytail: past maxAnswers the
// answers start over, an LRU if a dashboard serves more people than that.
const (
	sharedTTL = 10 * time.Second
	sarQPS    = 100
)

var sarGVR = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "subjectaccessreviews"}
var ssarGVR = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"}
var ssrGVR = schema.GroupVersionResource{Group: "authentication.k8s.io", Version: "v1", Resource: "selfsubjectreviews"}

// newSharedReads starts watching the delivery objects with sa until ctx ends.
func newSharedReads(ctx context.Context, sa dynamic.Interface, wrote func(string) bool) *sharedReads {
	s := &sharedReads{informers: map[schema.GroupVersionResource]cache.SharedIndexInformer{}, sa: sa, wrote: wrote,
		ttl: sharedTTL, now: time.Now, subjects: map[string]subjectVerdict{}, answers: map[string]verdict{}}
	for gvr := range sharedGVRs {
		inf := dynamicinformer.NewFilteredDynamicInformer(sa, gvr, metav1.NamespaceAll, 0,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, nil).Informer()
		// Nobody reads who last wrote which field; on 10,000 Operations it is most of the memory.
		_ = inf.SetTransform(func(o any) (any, error) {
			if m, ok := o.(metav1.Object); ok {
				m.SetManagedFields(nil)
			}
			return o, nil
		}) // only fails once started
		s.informers[gvr] = inf
		go inf.RunWithContext(ctx)
	}
	return s
}

// client is dyn, a person's own client, with the delivery objects' lists and
// gets answered from the shared cache where that person may read them.
func (s *sharedReads) client(dyn dynamic.Interface, token string) dynamic.Interface {
	return sharedClient{Interface: dyn, s: s, token: token}
}

type sharedClient struct {
	dynamic.Interface
	s     *sharedReads
	token string
}

func (c sharedClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	own := c.Interface.Resource(gvr)
	switch gvr {
	case ssrGVR:
		return selfReview{NamespaceableResourceInterface: own, c: c}
	case ssarGVR:
		return selfAccess{NamespaceableResourceInterface: own, c: c}
	}
	if _, ok := sharedGVRs[gvr]; !ok {
		return own
	}
	return sharedResource{NamespaceableResourceInterface: own, c: c, gvr: gvr}
}

// selfReview answers "who am I" with the answer the permission checks
// already hold for the token.
type selfReview struct {
	dynamic.NamespaceableResourceInterface
	c sharedClient
}

func (r selfReview) Create(ctx context.Context, _ *unstructured.Unstructured, _ metav1.CreateOptions, _ ...string) (*unstructured.Unstructured, error) {
	who, err := r.c.s.whoami(ctx, r.c)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview", "status": map[string]any{"userInfo": who}})
	u := &unstructured.Unstructured{}
	return u, u.UnmarshalJSON(b)
}

// selfAccess answers "may I" on a delivery object from the same cached
// SubjectAccessReview the reads use, so the actions a view offers cost no
// review of their own. Any other question goes to the apiserver.
type selfAccess struct {
	dynamic.NamespaceableResourceInterface
	c sharedClient
}

func (r selfAccess) Create(ctx context.Context, obj *unstructured.Unstructured, o metav1.CreateOptions, sub ...string) (*unstructured.Unstructured, error) {
	attrs, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "resourceAttributes")
	if attrs["group"] != deliveryGroup || attrs["subresource"] != "" || attrs["name"] != "" || len(sub) > 0 {
		return r.NamespaceableResourceInterface.Create(ctx, obj, o, sub...)
	}
	who, err := r.c.s.whoami(ctx, r.c)
	if err != nil {
		return nil, err
	}
	gvr := schema.GroupVersionResource{Group: deliveryGroup, Version: attrs["version"], Resource: attrs["resource"]}
	ok, err := r.c.s.may(ctx, who, attrs["verb"], gvr, attrs["namespace"])
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1",
		"kind": "SelfSubjectAccessReview", "status": map[string]any{"allowed": ok}}}, nil
}

// sharedResource is a resource across every namespace; Namespace narrows it.
type sharedResource struct {
	dynamic.NamespaceableResourceInterface
	c   sharedClient
	gvr schema.GroupVersionResource
}

func (r sharedResource) Namespace(ns string) dynamic.ResourceInterface {
	own := r.NamespaceableResourceInterface.Namespace(ns)
	return sharedNamespaced{ResourceInterface: own, r: r, ns: ns}
}

func (r sharedResource) List(ctx context.Context, o metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return r.list(ctx, metav1.NamespaceAll, o, r.NamespaceableResourceInterface)
}

func (r sharedResource) Get(ctx context.Context, name string, o metav1.GetOptions, sub ...string) (*unstructured.Unstructured, error) {
	return r.get(ctx, metav1.NamespaceAll, name, o, sub, r.NamespaceableResourceInterface)
}

type sharedNamespaced struct {
	dynamic.ResourceInterface
	r  sharedResource
	ns string
}

func (n sharedNamespaced) List(ctx context.Context, o metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return n.r.list(ctx, n.ns, o, n.ResourceInterface)
}

func (n sharedNamespaced) Get(ctx context.Context, name string, o metav1.GetOptions, sub ...string) (*unstructured.Unstructured, error) {
	return n.r.get(ctx, n.ns, name, o, sub, n.ResourceInterface)
}

// usable is the informer for r when the cache can answer the person now.
func (r sharedResource) usable() cache.SharedIndexInformer {
	inf := r.c.s.informers[r.gvr]
	if inf == nil || !inf.HasSynced() || r.c.s.wrote(bearer(r.c.token)) {
		return nil
	}
	return inf
}

func (r sharedResource) list(ctx context.Context, ns string, o metav1.ListOptions, own dynamic.ResourceInterface) (*unstructured.UnstructuredList, error) {
	sel, ok := r.selector(o)
	inf := r.usable()
	if !ok || inf == nil {
		return own.List(ctx, o)
	}
	if err := r.c.s.allow(ctx, r.c, "list", r.gvr, ns, ""); err != nil {
		return nil, err
	}
	var objs []any
	if ns == metav1.NamespaceAll {
		objs = inf.GetStore().List()
	} else {
		objs, _ = inf.GetIndexer().ByIndex(cache.NamespaceIndex, ns) // the index is registered above
	}
	out := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": r.gvr.GroupVersion().String(), "kind": "List"}}
	for _, o := range objs {
		u := o.(*unstructured.Unstructured)
		if sel.Matches(objectFields(u, sharedGVRs[r.gvr])) {
			out.Items = append(out.Items, *u.DeepCopy())
		}
	}
	// In the apiserver's order: by namespace, then name.
	slices.SortFunc(out.Items, func(a, b unstructured.Unstructured) int {
		return cmp.Or(cmp.Compare(a.GetNamespace(), b.GetNamespace()), cmp.Compare(a.GetName(), b.GetName()))
	})
	return out, nil
}

// selector is the field selector of o when the cache evaluates everything o
// asks for exactly: no label selector, paging or resource version, and only
// fields the CRD lets the apiserver select on.
func (r sharedResource) selector(o metav1.ListOptions) (fields.Selector, bool) {
	if o.LabelSelector != "" || o.Limit != 0 || o.Continue != "" || o.ResourceVersion != "" || o.Watch {
		return nil, false
	}
	sel, err := fields.ParseSelector(o.FieldSelector)
	if err != nil {
		return nil, false
	}
	for _, req := range sel.Requirements() {
		if req.Field != "metadata.name" && req.Field != "metadata.namespace" && !slices.Contains(sharedGVRs[r.gvr], req.Field) {
			return nil, false
		}
	}
	return sel, true
}

// objectFields are the fields of u a selector may name.
func objectFields(u *unstructured.Unstructured, selectable []string) fields.Set {
	set := fields.Set{"metadata.name": u.GetName(), "metadata.namespace": u.GetNamespace()}
	for _, f := range selectable {
		set[f], _, _ = unstructured.NestedString(u.Object, strings.Split(f, ".")...)
	}
	return set
}

func (r sharedResource) get(ctx context.Context, ns, name string, o metav1.GetOptions, sub []string, own dynamic.ResourceInterface) (*unstructured.Unstructured, error) {
	inf := r.usable()
	if inf == nil || len(sub) > 0 || o.ResourceVersion != "" || ns == metav1.NamespaceAll {
		return own.Get(ctx, name, o, sub...)
	}
	if err := r.c.s.allow(ctx, r.c, "get", r.gvr, ns, name); err != nil {
		return nil, err
	}
	obj, ok, _ := inf.GetStore().GetByKey(ns + "/" + name) // a store's GetByKey never fails
	if !ok {
		// Perhaps created a moment ago: the apiserver knows.
		return own.Get(ctx, name, o, sub...)
	}
	return obj.(*unstructured.Unstructured).DeepCopy(), nil
}

// allow is nil when the person c acts for may verb gvr in ns ("" for every
// namespace), else the 403 the apiserver would answer. A review that fails
// is that error, never a quiet allow.
func (s *sharedReads) allow(ctx context.Context, c sharedClient, verb string, gvr schema.GroupVersionResource, ns, name string) error {
	who, err := s.whoami(ctx, c)
	if err != nil {
		return err
	}
	ok, err := s.may(ctx, who, verb, gvr, ns)
	if err != nil || ok {
		return err
	}
	where := "at the cluster scope"
	if ns != "" {
		where = fmt.Sprintf("in the namespace %q", ns)
	}
	return apierrors.NewForbidden(gvr.GroupResource(), name,
		fmt.Errorf("User %q cannot %s resource %q in API group %q %s", who.User, verb, gvr.Resource, gvr.Group, where)) //nolint:staticcheck // the apiserver's own wording
}

// whoami is who the apiserver says c's token is, kept ttl.
func (s *sharedReads) whoami(ctx context.Context, c sharedClient) (subject, error) {
	sum := sha256.Sum256([]byte(c.token))
	key := string(sum[:])
	s.mu.Lock()
	v, ok := s.subjects[key]
	s.mu.Unlock()
	if ok && s.now().Before(v.until) {
		return v.s, nil
	}
	out, err, _ := s.group.Do("who\x00"+key, func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		defer cancel()
		r, err := c.Interface.Resource(ssrGVR).Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"}}, metav1.CreateOptions{})
		if err != nil {
			return subject{}, err
		}
		var st struct {
			Status struct {
				UserInfo subject `json:"userInfo"`
			} `json:"status"`
		}
		b, _ := r.MarshalJSON()
		_ = json.Unmarshal(b, &st) // an answer without a user is nobody, whom RBAC allows nothing
		s.keep(func() { s.subjects[key] = subjectVerdict{st.Status.UserInfo, s.now().Add(s.ttl)} })
		return st.Status.UserInfo, nil
	})
	return out.(subject), err
}

// may asks, with the dashboard's own credentials, whether who may verb gvr in
// ns; answers are kept ttl and concurrent identical questions share one.
func (s *sharedReads) may(ctx context.Context, who subject, verb string, gvr schema.GroupVersionResource, ns string) (bool, error) {
	b, _ := json.Marshal(who)
	key := string(b) + "\x00" + verb + "\x00" + gvr.Resource + "\x00" + ns
	s.mu.Lock()
	v, ok := s.answers[key]
	s.mu.Unlock()
	if ok && s.now().Before(v.until) {
		return v.allowed, nil
	}
	out, err, _ := s.group.Do("may\x00"+key, func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		defer cancel()
		spec := map[string]any{"user": who.User, "uid": who.UID, "resourceAttributes": map[string]any{
			"namespace": ns, "verb": verb, "group": gvr.Group, "version": gvr.Version, "resource": gvr.Resource}}
		if len(who.Groups) > 0 {
			spec["groups"] = anyStrings(who.Groups)
		}
		if len(who.Extra) > 0 {
			extra := map[string]any{}
			for k, vs := range who.Extra {
				extra[k] = anyStrings(vs)
			}
			spec["extra"] = extra
		}
		r, err := s.sa.Resource(sarGVR).Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "authorization.k8s.io/v1", "kind": "SubjectAccessReview", "spec": spec}}, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("asking the apiserver what %s may read: %w", who.User, err)
		}
		allowed, _, _ := unstructured.NestedBool(r.Object, "status", "allowed")
		s.keep(func() { s.answers[key] = verdict{allowed, s.now().Add(s.ttl)} })
		return allowed, nil
	})
	return out.(bool), err
}

// keep runs put under the lock, starting the caches over when full.
func (s *sharedReads) keep(put func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.answers)+len(s.subjects) >= maxAnswers {
		s.answers, s.subjects = map[string]verdict{}, map[string]subjectVerdict{}
	}
	put()
}

const maxAnswers = 8192

func anyStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// bearer is the Authorization header a token is sent in, as readCache names
// the credentials of a request.
func bearer(token string) string { return "Bearer " + token }
