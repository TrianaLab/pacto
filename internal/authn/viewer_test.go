package authn

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func fakeJWT(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

func TestClaimsPersonDefault(t *testing.T) {
	p := Claims{}.Person(fakeJWT(`{"iss":"https://idp","sub":"u1","groups":["a","b"]}`))
	if p.User != "https://idp#u1" || !slices.Equal(p.Groups, []string{"a", "b"}) {
		t.Errorf("person = %+v", p)
	}
	// An IdP group named like a Kubernetes system group is not passed to the
	// review: the apiserver never grants it from a token.
	p = Claims{}.Person(fakeJWT(`{"iss":"https://idp","sub":"u1","groups":["system:masters","a","system:authenticated"]}`))
	if !slices.Equal(p.Groups, []string{"a"}) {
		t.Errorf("groups = %v, want [a]", p.Groups)
	}
	for _, tok := range []string{"", "a.b", fakeJWT(`{"iss":"https://idp"}`), "h.!!.s"} {
		if p := (Claims{}).Person(tok); p.User != Anonymous.User {
			t.Errorf("%q is %+v, want anonymous", tok, p)
		}
	}
}

func viewerFleet(t *testing.T, namespaces ...string) *fleet.Query {
	t.Helper()
	col := &fleet.Collection{}
	for _, ns := range namespaces {
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: "svc-" + ns, Service: "svc-" + ns})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// reviews answers Pacto reviews with allow and workload reviews with
// allowWorkload (nil refuses every one), recording each namespace a Pacto
// review asked about in asked and every review as resource.group/namespace in
// askedRes.
type reviews struct {
	mu            sync.Mutex
	asked         []string
	askedRes      []string
	allow         func(p Person, ns string) (bool, error)
	allowWorkload func(p Person, ns string, res ResourceAttributes) (bool, error)
}

func (r *reviews) review(_ context.Context, p Person, ns string, res ResourceAttributes) (bool, error) {
	r.mu.Lock()
	r.askedRes = append(r.askedRes, res.Resource+"."+res.Group+"/"+ns)
	if res == PactoResource {
		r.asked = append(r.asked, ns)
	}
	r.mu.Unlock()
	switch {
	case res == PactoResource:
		return r.allow(p, ns)
	case r.allowWorkload == nil:
		return false, nil
	}
	return r.allowWorkload(p, ns, res)
}

func services(q *fleet.Query) []string {
	var out []string
	for _, s := range q.SnapshotForSerialization().Services {
		out = append(out, s.Name)
	}
	slices.Sort(out)
	return out
}

var alice = Person{User: "alice", Groups: []string{"team-x", "team-a"}}

// testViewer is a Viewer over reviews where root reads everything and alice
// and bob read namespace a, and a clock at now.
func testViewer() (*Viewer, *reviews, *time.Time) {
	r := &reviews{allow: func(p Person, ns string) (bool, error) {
		return p.User == "root" || (p.User == "alice" && ns == "a") || (p.User == "bob" && ns == "a"), nil
	}}
	v := NewViewer(r.review)
	now := time.Unix(1000, 0)
	v.now = func() time.Time { return now }
	return v, r, &now
}

func TestViewer(t *testing.T) {
	q := viewerFleet(t, "a", "b", "c")
	v, r, now := testViewer()

	got, err := v.Query(context.Background(), alice, q)
	if err != nil || !slices.Equal(services(got), []string{"svc-a"}) {
		t.Fatalf("alice sees %v, %v", services(got), err)
	}
	if !slices.Equal(slices.Sorted(slices.Values(r.asked)), []string{"", "a", "b", "c"}) {
		t.Errorf("asked %v", r.asked)
	}
	// The same person with her groups in another order asks nothing again.
	r.asked = nil
	again, _ := v.Query(context.Background(), Person{User: "alice", Groups: []string{"team-a", "team-x"}}, q)
	if len(r.asked) != 0 || again != got {
		t.Errorf("a cached answer asked %v and gave a new view", r.asked)
	}
	// Another person who sees the same namespaces shares the view.
	if bob, _ := v.Query(context.Background(), Person{User: "bob"}, q); bob != got {
		t.Error("bob sees the same namespaces and should share alice's view")
	}
	// A cluster-wide reader is settled by one review and sees it all.
	r.asked = nil
	root, _ := v.Query(context.Background(), Person{User: "root"}, q)
	if root != q || !slices.Equal(r.asked, []string{""}) {
		t.Errorf("root asked %v", r.asked)
	}
	// Answers expire.
	*now = now.Add(2 * time.Minute)
	r.asked = nil
	_, _ = v.Query(context.Background(), alice, q)
	if len(r.asked) != 4 {
		t.Errorf("expired answers asked %v", r.asked)
	}
}

func TestViewerSnapshots(t *testing.T) {
	v, r, _ := testViewer()
	if _, err := v.Query(context.Background(), alice, viewerFleet(t, "a", "b", "c")); err != nil {
		t.Fatal(err)
	}
	// A new snapshot starts the views over.
	q2 := viewerFleet(t, "a", "b", "d")
	if got2, _ := v.Query(context.Background(), alice, q2); !slices.Equal(services(got2), []string{"svc-a"}) {
		t.Errorf("new snapshot: %v", services(got2))
	}
	// No namespace: only the cluster-wide question, already answered.
	r.asked = nil
	plain := viewerFleet(t)
	if p, err := v.Query(context.Background(), alice, plain); p != plain || err != nil || len(r.asked) != 0 {
		t.Errorf("a snapshot with no namespace asked %v", r.asked)
	}
}

// TestViewerRegistryOnly: a service with no namespace shows only to a
// cluster-wide reader.
func TestViewerRegistryOnly(t *testing.T) {
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "reg", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}, FS: fstest.MapFS{}}, Digest: "sha256:reg"}},
		Targets: []fleet.RawTarget{{Scope: "a", Kind: fleet.SourceKindKubernetes, Name: "svc-a", Service: "svc-a"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	v, _, _ := testViewer()
	if got, _ := v.Query(context.Background(), alice, q); !slices.Equal(services(got), []string{"svc-a"}) {
		t.Errorf("alice sees %v", services(got))
	}
	if got, _ := v.Query(context.Background(), Person{User: "root"}, q); got != q {
		t.Errorf("root sees %v", services(got))
	}
}

// TestViewerSeesRegistryOnlyOfferingProvider: a service with no namespace that
// declares a platform offering shows to every signed-in viewer.
func TestViewerSeesRegistryOnlyOfferingProvider(t *testing.T) {
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.4",
		Service: contract.Service{Name: "pg", Version: "16.0.0", Offering: "postgresql"}}, FS: fstest.MapFS{}}, Digest: "sha256:pg"}},
		Targets: []fleet.RawTarget{{Scope: "a", Kind: fleet.SourceKindKubernetes, Name: "svc-a", Service: "svc-a"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	v, _, _ := testViewer()
	if got, _ := v.Query(context.Background(), alice, fleet.NewQuery(snap)); !slices.Equal(services(got), []string{"pg", "svc-a"}) {
		t.Errorf("alice sees %v", services(got))
	}
}

func TestViewerBoundsAndFailures(t *testing.T) {
	q := viewerFleet(t, "a")
	boom := errors.New("apiserver down")
	fail := ""
	v := NewViewer(func(_ context.Context, p Person, ns string, _ ResourceAttributes) (bool, error) {
		if ns == fail {
			return false, boom
		}
		return false, nil
	})
	fail = ""
	if _, err := v.Query(context.Background(), Person{User: "x"}, q); !errors.Is(err, ErrReview) {
		t.Errorf("a failed cluster-wide review: %v", err)
	}
	fail = "a"
	if _, err := v.Query(context.Background(), Person{User: "x"}, q); !errors.Is(err, ErrReview) {
		t.Errorf("a failed namespace review: %v", err)
	}
	fail = "-"
	for i := range maxViews + 1 { // distinct snapshots are not needed: distinct people with distinct answers are
		v.viewsOf = q.SnapshotID()
		v.views[string(rune('A'+i))] = q
	}
	if _, err := v.Query(context.Background(), Person{User: "x"}, q); err != nil || len(v.views) != 1 {
		t.Errorf("a full view cache did not start over: %d views, %v", len(v.views), err)
	}
	for i := range maxAnswers {
		v.answers[string(rune(i))] = verdict{}
	}
	if _, err := v.Query(context.Background(), Person{User: "y"}, q); err != nil || len(v.answers) != 2 {
		t.Errorf("a full answer cache did not start over: %d answers, %v", len(v.answers), err)
	}
}

func TestSubjectAccessReview(t *testing.T) {
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var spec map[string]any
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		spec, _, _ = unstructured.NestedMap(obj.Object, "spec")
		ns, _, _ := unstructured.NestedString(obj.Object, "spec", "resourceAttributes", "namespace")
		obj = obj.DeepCopy()
		_ = unstructured.SetNestedField(obj.Object, ns == "shop", "status", "allowed")
		return true, obj, nil
	})
	review := SubjectAccessReview(c)
	ok, err := review(context.Background(), Person{User: "u", Groups: []string{"g"}}, "shop", PactoResource)
	if err != nil || !ok {
		t.Fatalf("shop: %v %v", ok, err)
	}
	attrs := spec["resourceAttributes"].(map[string]any)
	if spec["user"] != "u" || spec["groups"].([]any)[0] != "g" || attrs["verb"] != "list" || attrs["resource"] != "pactos" || attrs["group"] != "pacto.trianalab.io" {
		t.Errorf("spec = %+v", spec)
	}
	if ok, _ := review(context.Background(), Person{User: "u"}, "other", PactoResource); ok {
		t.Error("other namespace allowed")
	}
	c.PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := review(context.Background(), Person{User: "u"}, "shop", PactoResource); err == nil {
		t.Error("a refused review is an error")
	}
}

func TestPersonFromTokenInfo(t *testing.T) {
	if p := (Claims{}).PersonFromTokenInfo(nil); p.User != Anonymous.User {
		t.Errorf("no caller is %+v", p)
	}
	info := &auth.TokenInfo{Extra: map[string]any{rawTokenKey: fakeJWT(`{"iss":"https://kc","sub":"agent","email":"a@x","roles":["ops"]}`)}}
	if p := (Claims{}).PersonFromTokenInfo(info); p.User != "https://kc#agent" {
		t.Errorf("caller is %+v", p)
	}
	// The remote MCP server maps a caller as the dashboard does.
	c := Claims{UsernameClaim: "email", GroupsClaim: "roles", GroupsPrefix: "oidc:"}
	if p := c.PersonFromTokenInfo(info); p.User != "a@x" || len(p.Groups) != 1 || p.Groups[0] != "oidc:ops" {
		t.Errorf("mapped caller is %+v", p)
	}
}

// TestViewerCallerNamespaces: a k8s caller in a namespace no target runs in is
// reviewed like any other namespace: alice, who may not read f, sees neither
// its name nor its namespace, only a count, and keeps the caller in a.
func TestViewerCallerNamespaces(t *testing.T) {
	k := fleet.SourceKindKubernetes
	col := &fleet.Collection{
		Targets:  []fleet.RawTarget{{Scope: "a", Kind: k, Name: "svc-a", Service: "svc-a"}},
		Observed: []fleet.ObservedEdge{{From: "k8s:f/Deployment/spy", To: "svc-a", Count: 1}, {From: "k8s:a/Deployment/friend", To: "svc-a", Count: 1}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", k, col))
	if err != nil {
		t.Fatal(err)
	}
	v, r, _ := testViewer()
	got, err := v.Query(context.Background(), alice, fleet.NewQuery(snap))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.asked, "f") {
		t.Errorf("asked %v, never about the caller's namespace", r.asked)
	}
	s := got.SnapshotForSerialization()
	var callers []string
	for _, c := range s.UncontractedCallers {
		callers = append(callers, c.Caller)
	}
	if !slices.Equal(callers, []string{"k8s:a/Deployment/friend"}) {
		t.Errorf("alice sees callers %v, want only the one in a", callers)
	}
	if svc := s.Services[fleet.NewServiceKey("svc-a")]; svc == nil || svc.HiddenDependents != 1 {
		t.Errorf("svc-a = %+v, want the f caller counted", svc)
	}
}

func lsarClient(allowed func(ns string) bool) (*dynamicfake.FakeDynamicClient, *[]k8stesting.CreateAction) {
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var seen []k8stesting.CreateAction
	c.PrependReactor("create", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ca := a.(k8stesting.CreateAction)
		seen = append(seen, ca)
		obj := ca.GetObject().(*unstructured.Unstructured).DeepCopy()
		_ = unstructured.SetNestedField(obj.Object, allowed(ca.GetNamespace()), "status", "allowed")
		return true, obj, nil
	})
	return c, &seen
}

// TestLocalReviewPostsInNamespace: the local review posts a
// LocalSubjectAccessReview into the namespace it asks about, which a
// namespaced Role can grant.
func TestLocalReviewPostsInNamespace(t *testing.T) {
	c, seen := lsarClient(func(ns string) bool { return ns == "payments" })
	review := LocalSubjectAccessReview(c)
	ok, err := review(context.Background(), Person{User: "u", Groups: []string{"g"}}, "payments", PactoResource)
	if err != nil || !ok {
		t.Fatalf("payments: %v %v", ok, err)
	}
	if len(*seen) != 1 {
		t.Fatalf("asked %d times", len(*seen))
	}
	a := (*seen)[0]
	obj := a.GetObject().(*unstructured.Unstructured)
	if a.GetResource().Resource != "localsubjectaccessreviews" || a.GetNamespace() != "payments" ||
		obj.GetKind() != "LocalSubjectAccessReview" || obj.GetNamespace() != "payments" {
		t.Errorf("posted %s in %q: %+v", a.GetResource().Resource, a.GetNamespace(), obj.Object)
	}
	checkReviewSpec(t, obj, "payments")
	if ok, _ := review(context.Background(), Person{User: "u"}, "ledger", PactoResource); ok {
		t.Error("ledger allowed")
	}
	c.PrependReactor("create", "localsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := review(context.Background(), Person{User: "u"}, "payments", PactoResource); err == nil {
		t.Error("a refused review is an error")
	}
}

// checkReviewSpec: obj asks whether user u in group g may list Pactos in ns.
func checkReviewSpec(t *testing.T, obj *unstructured.Unstructured, ns string) {
	t.Helper()
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	attrs := spec["resourceAttributes"].(map[string]any)
	if spec["user"] != "u" || spec["groups"].([]any)[0] != "g" || attrs["namespace"] != ns || attrs["verb"] != "list" ||
		attrs["resource"] != "pactos" || attrs["group"] != "pacto.trianalab.io" {
		t.Errorf("spec = %+v", spec)
	}
}

// TestLocalReviewAnswersFalseForAllNamespaces: the question about every
// namespace at once has no namespaced form, so it is answered false unasked.
func TestLocalReviewAnswersFalseForAllNamespaces(t *testing.T) {
	c, seen := lsarClient(func(string) bool { return true })
	ok, err := LocalSubjectAccessReview(c)(context.Background(), Person{User: "root"}, "", PactoResource)
	if ok || err != nil || len(*seen) != 0 {
		t.Errorf("all namespaces: %v %v, %d requests", ok, err, len(*seen))
	}
}

// scopedViewer is a scoped Viewer watching payments and ledger whose review
// fails the test when asked about anything else, and allows alice in payments.
func scopedViewer(t *testing.T) (*Viewer, *reviews) {
	t.Helper()
	r := &reviews{allow: func(p Person, ns string) (bool, error) {
		if ns != "payments" && ns != "ledger" {
			t.Errorf("asked about %q outside the watched namespaces", ns)
		}
		return p.User == "alice" && ns == "payments", nil
	}}
	return NewScopedViewer(r.review, []string{"payments", "ledger"}), r
}

func TestScopedViewerNeverAsksAllNamespaces(t *testing.T) {
	v, r := scopedViewer(t)
	q := viewerFleet(t, "payments", "ledger")
	got, err := v.Query(context.Background(), Person{User: "root"}, q)
	if err != nil || got == q || len(services(got)) != 0 {
		t.Fatalf("root sees %v, %v", services(got), err)
	}
	if slices.Contains(r.asked, "") {
		t.Errorf("asked %v, including every namespace at once", r.asked)
	}
}

func TestScopedViewerSkipsUnwatchedNamespace(t *testing.T) {
	v, r := scopedViewer(t)
	got, err := v.Query(context.Background(), alice, viewerFleet(t, "payments", "kube-system"))
	if err != nil || !slices.Equal(services(got), []string{"svc-payments"}) {
		t.Fatalf("alice sees %v, %v", services(got), err)
	}
	if !slices.Equal(r.asked, []string{"payments"}) {
		t.Errorf("asked %v", r.asked)
	}
}

// TestScopedViewerUnwatchedCallerDoesNotFailRequest: an observed caller in
// kube-system, where a review would be Forbidden, is hidden and counted and
// never fails the request.
func TestScopedViewerUnwatchedCallerDoesNotFailRequest(t *testing.T) {
	k := fleet.SourceKindKubernetes
	col := &fleet.Collection{
		Targets:  []fleet.RawTarget{{Scope: "payments", Kind: k, Name: "svc-p", Service: "svc-p"}},
		Observed: []fleet.ObservedEdge{{From: "k8s:kube-system/Deployment/coredns", To: "svc-p", Count: 1}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", k, col))
	if err != nil {
		t.Fatal(err)
	}
	r := &reviews{allow: func(_ Person, ns string) (bool, error) {
		if ns != "payments" {
			return false, errors.New("forbidden")
		}
		return true, nil
	}}
	got, err := NewScopedViewer(r.review, []string{"payments"}).Query(context.Background(), alice, fleet.NewQuery(snap))
	if err != nil {
		t.Fatal(err)
	}
	s := got.SnapshotForSerialization()
	if len(s.UncontractedCallers) != 0 {
		t.Errorf("alice sees callers %+v", s.UncontractedCallers)
	}
	if svc := s.Services[fleet.NewServiceKey("svc-p")]; svc == nil || svc.HiddenDependents != 1 {
		t.Errorf("svc-p = %+v, want the kube-system caller counted", svc)
	}
}

func TestScopedViewerFailedReviewIsError(t *testing.T) {
	boom := errors.New(`localsubjectaccessreviews.authorization.k8s.io is forbidden: cannot create resource in the namespace "payments"`)
	v := NewScopedViewer(func(context.Context, Person, string, ResourceAttributes) (bool, error) { return false, boom }, []string{"payments"})
	_, err := v.Query(context.Background(), alice, viewerFleet(t, "payments"))
	if !errors.Is(err, ErrReview) {
		t.Errorf("a failed review in a watched namespace: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "payments") {
		t.Errorf("the error names the namespace it was asked in: %v", err)
	}
}

// TestScopedViewerUnreadableNamespaceDoesNotFailRequest: a watched namespace
// the source could not read, where the review is refused by the same missing
// grant, is never asked about, so the readable namespace still answers.
func TestScopedViewerUnreadableNamespaceDoesNotFailRequest(t *testing.T) {
	k := fleet.SourceKindKubernetes
	src := fleet.NewMemorySource("c1", k, &fleet.Collection{
		Targets:     []fleet.RawTarget{{Scope: "payments", Kind: k, Name: "svc-payments", Service: "svc-payments"}},
		State:       &fleet.SourceState{Status: fleet.SourcePartial, Namespaces: []string{"payments"}, NamespacesTotal: 1},
		Limitations: []fleet.Limitation{fleet.NamespaceUnreadableLimitation("c1", "ledger", "forbidden")},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	r := &reviews{allow: func(_ Person, ns string) (bool, error) {
		if ns != "payments" {
			return false, errors.New("forbidden")
		}
		return true, nil
	}}
	got, err := NewScopedViewer(r.review, []string{"payments", "ledger"}).Query(context.Background(), alice, fleet.NewQuery(snap))
	if err != nil || !slices.Equal(services(got), []string{"svc-payments"}) {
		t.Fatalf("alice sees %v, %v", services(got), err)
	}
	for _, l := range got.SnapshotForSerialization().Limitations {
		if strings.Contains(l.Message, "ledger") {
			t.Errorf("alice learns of the unreadable namespace: %q", l.Message)
		}
	}
}

func TestScopedViewerRestrictsToAllowed(t *testing.T) {
	v, _ := scopedViewer(t)
	got, err := v.Query(context.Background(), alice, viewerFleet(t, "payments", "ledger", "other"))
	if err != nil || !slices.Equal(services(got), []string{"svc-payments"}) {
		t.Errorf("alice sees %v, %v", services(got), err)
	}
}

// inferredFleet: svc-a runs in shop beside two inferred services, the cart
// Deployment and the nightly CronJob.
func inferredFleet(t *testing.T) *fleet.Query {
	t.Helper()
	k := fleet.SourceKindKubernetes
	declared := fleet.NewMemorySource("k8s", k, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: k, Name: "svc-a", Service: "svc-a"}}})
	workloads := fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
		{Namespace: "shop", Kind: "Deployment", Name: "cart"}, {Namespace: "shop", Kind: "CronJob", Name: "nightly"},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, declared, workloads)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestViewerAsksWorkloadReviewForInferred: Pacto read alone never shows an
// inferred service. A person must also be allowed to list its workload kind
// in its namespace, asked through SAR or, for a scoped viewer, LSAR, and a
// cluster-wide Pacto reader sees the whole fleet only with cluster-wide read
// of every such kind.
func TestViewerAsksWorkloadReviewForInferred(t *testing.T) {
	cart, nightly := "k8s:shop/Deployment/cart", "k8s:shop/CronJob/nightly"
	noDeployments := func(_ Person, _ string, res ResourceAttributes) (bool, error) {
		return res.Resource != "deployments", nil
	}
	q := inferredFleet(t)

	r := &reviews{allow: func(_ Person, ns string) (bool, error) { return ns == "shop", nil }, allowWorkload: noDeployments}
	v := NewViewer(r.review)
	got, err := v.Query(context.Background(), alice, q)
	if err != nil || !slices.Equal(services(got), []string{nightly, "svc-a"}) {
		t.Fatalf("alice sees %v, %v", services(got), err)
	}
	for _, want := range []string{"pactos.pacto.trianalab.io/shop", "deployments.apps/shop", "cronjobs.batch/shop"} {
		if !slices.Contains(r.askedRes, want) {
			t.Errorf("asked %v, never %s", r.askedRes, want)
		}
	}
	if slices.Contains(r.askedRes, "deployments.apps/") {
		t.Errorf("asked %v about deployments everywhere for a namespace reader", r.askedRes)
	}
	asked := len(r.askedRes)
	if _, err := v.Query(context.Background(), alice, q); err != nil || len(r.askedRes) != asked {
		t.Errorf("answers are not cached per resource: %v, %v", r.askedRes, err)
	}
	checkClusterWideWorkloadReview(t, q, nightly)
	checkScopedWorkloadReview(t, q, cart, nightly)
}

// checkClusterWideWorkloadReview: a cluster-wide Pacto reader sees all of q
// only with cluster-wide read of every inferred workload kind.
func checkClusterWideWorkloadReview(t *testing.T, q *fleet.Query, nightly string) {
	t.Helper()
	root := &reviews{allow: func(Person, string) (bool, error) { return true, nil }, allowWorkload: func(_ Person, ns string, res ResourceAttributes) (bool, error) {
		return res.Resource == "cronjobs" && ns == "", nil
	}}
	got, err := NewViewer(root.review).Query(context.Background(), alice, q)
	if err != nil || got == q || !slices.Equal(services(got), []string{nightly, "svc-a"}) {
		t.Errorf("a cluster-wide Pacto reader without cluster-wide Deployment read sees %v, %v", services(got), err)
	}
	if !slices.Contains(root.askedRes, "deployments.apps/") || !slices.Contains(root.askedRes, "deployments.apps/shop") || slices.Contains(root.askedRes, "cronjobs.batch/shop") {
		t.Errorf("asked %v", root.askedRes)
	}
	everything := &reviews{allow: func(Person, string) (bool, error) { return true, nil }, allowWorkload: func(Person, string, ResourceAttributes) (bool, error) { return true, nil }}
	if got, err := NewViewer(everything.review).Query(context.Background(), alice, q); err != nil || got != q {
		t.Errorf("a cluster-wide reader of everything gets a restricted view: %v", err)
	}
}

// checkScopedWorkloadReview: a scoped viewer asks the workload review through
// LSAR in the watched namespace.
func checkScopedWorkloadReview(t *testing.T, q *fleet.Query, cart, nightly string) {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var seen []string
	c.PrependReactor("create", "localsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		res, _, _ := unstructured.NestedString(obj.Object, "spec", "resourceAttributes", "resource")
		group, _, _ := unstructured.NestedString(obj.Object, "spec", "resourceAttributes", "group")
		seen = append(seen, res+"."+group+"/"+obj.GetNamespace())
		_ = unstructured.SetNestedField(obj.Object, res != "deployments", "status", "allowed")
		return true, obj, nil
	})
	got, err := NewScopedViewer(LocalSubjectAccessReview(c), []string{"shop"}).Query(context.Background(), alice, q)
	if err != nil || slices.Contains(services(got), cart) || !slices.Contains(services(got), nightly) {
		t.Errorf("the scoped viewer shows %v, %v", services(got), err)
	}
	if !slices.Contains(seen, "deployments.apps/shop") || !slices.Contains(seen, "pactos.pacto.trianalab.io/shop") {
		t.Errorf("the scoped viewer posted %v", seen)
	}
}

// TestViewerWorkloadReviewEdges: a workload kind Pacto has no resource for is
// never visible, even to a reader of everything, and a failed workload review
// fails the answer as a failed Pacto review does.
func TestViewerWorkloadReviewEdges(t *testing.T) {
	workloads := fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
		{Namespace: "shop", Kind: "Rollout", Name: "canary"}, {Namespace: "shop", Kind: "Deployment", Name: "cart"},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, workloads)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	yes := func(Person, string) (bool, error) { return true, nil }
	everything := &reviews{allow: yes, allowWorkload: func(Person, string, ResourceAttributes) (bool, error) { return true, nil }}
	got, err := NewViewer(everything.review).Query(context.Background(), alice, q)
	if err != nil || !slices.Equal(services(got), []string{"k8s:shop/Deployment/cart"}) {
		t.Errorf("a reader of everything sees %v, %v", services(got), err)
	}
	boom := errors.New("apiserver down")
	for name, fail := range map[string]func(Person, string, ResourceAttributes) (bool, error){
		"everywhere": func(Person, string, ResourceAttributes) (bool, error) { return false, boom },
		"namespace": func(_ Person, ns string, _ ResourceAttributes) (bool, error) {
			if ns == "" {
				return false, nil
			}
			return false, boom
		},
	} {
		r := &reviews{allow: yes, allowWorkload: fail}
		if _, err := NewViewer(r.review).Query(context.Background(), alice, q); !errors.Is(err, ErrReview) {
			t.Errorf("%s: a failed workload review is %v", name, err)
		}
	}
}

// TestViewerReviewsProviderCounts: a provider count is workload data. A
// cluster-wide Pacto reader with no inferred service to review still gets the
// count only when they may list its kind.
func TestViewerReviewsProviderCounts(t *testing.T) {
	k := fleet.SourceKindKubernetes
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", k, &fleet.Collection{
		Targets:   []fleet.RawTarget{{Scope: "shop", Kind: k, Name: "svc-a", Service: "svc-a"}},
		Providers: []fleet.Provider{{ID: fleet.ProviderKubernetesWorkloads, Counts: []fleet.ProviderCount{{Kind: "Deployment", Namespace: "shop", N: 40}}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	r := &reviews{allow: func(Person, string) (bool, error) { return true, nil }}
	got, err := NewViewer(r.review).Query(context.Background(), alice, q)
	if err != nil || got == q {
		t.Fatalf("an unlisted workload kind kept the whole query: %v", err)
	}
	if ev := got.SnapshotForSerialization().Providers[0].Evidence; ev != "none you may see (+40 hidden by your permissions)" {
		t.Errorf("evidence = %q", ev)
	}
	if !slices.Contains(r.askedRes, "deployments.apps/") {
		t.Errorf("asked %v, never deployments everywhere", r.askedRes)
	}
}

// TestViewerReviewsDetectedNamespaces: the namespace of a detected row's
// Gateway is reviewed even though no service runs there; a reader allowed
// there gets the row whole, anyone else redacted.
func TestViewerReviewsDetectedNamespaces(t *testing.T) {
	k := fleet.SourceKindKubernetes
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", k, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "a", Kind: k, Name: "svc-a", Service: "svc-a"}}}),
		fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: []fleet.Provider{{
			ID: "exposure", Name: "Exposure", Evidence: "Gateway API on a Gateway", Provenance: fleet.ProvenanceInferred, Hint: "--set dashboard.httpRoute.enabled=true",
			Objects: []fleet.ProviderObject{{Kind: "Gateway", Namespace: "infra", Name: "public"}},
		}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	r := &reviews{allow: func(p Person, ns string) (bool, error) { return ns == "a" || (p.User == "carol" && ns == "infra"), nil }}
	v := NewViewer(r.review)
	got, err := v.Query(context.Background(), alice, q)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.asked, "infra") {
		t.Errorf("asked %v, never the Gateway namespace", r.asked)
	}
	if p := got.SnapshotForSerialization().Providers[0]; p.Hint != "" || len(p.Objects) != 0 {
		t.Errorf("alice sees the Gateway row whole: %+v", p)
	}
	got, err = v.Query(context.Background(), Person{User: "carol"}, q)
	if err != nil {
		t.Fatal(err)
	}
	if p := got.SnapshotForSerialization().Providers[0]; p.Hint == "" || len(p.Objects) != 1 {
		t.Errorf("carol, who reads infra, gets the row redacted: %+v", p)
	}
}

// repositoryFleet: cart is the Argo CD Application cart, whose repository
// Pacto read from the Application in argocd; ledger is the Flux Kustomization
// shop/ledger, whose repository it read from the GitRepository in
// flux-system.
func repositoryFleet(t *testing.T) *fleet.Query {
	t.Helper()
	workloads := fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
		{Namespace: "shop", Kind: "Deployment", Name: "cart",
			Group:        &fleet.InferredGroup{Rule: "argocd", Name: "cart", From: "label argocd.argoproj.io/instance"},
			Repositories: []fleet.InferredRepository{{URL: "https://github.com/acme/shop", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"}}},
		{Namespace: "shop", Kind: "StatefulSet", Name: "ledger",
			Group:        &fleet.InferredGroup{Rule: "flux-kustomization", Namespace: "shop", Name: "ledger", From: "labels kustomize.toolkit.fluxcd.io/name and namespace"},
			Repositories: []fleet.InferredRepository{{URL: "https://github.com/acme/ledger", From: "GitRepository flux-system/ledger spec.url", Namespace: "flux-system", Kind: "GitRepository"}}},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, workloads)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// repositoryURLs are the repository URLs q shows, sorted.
func repositoryURLs(q *fleet.Query) []string {
	var out []string
	for _, s := range q.SnapshotForSerialization().Services {
		if s.Workload != nil {
			for _, r := range s.Workload.Repositories {
				out = append(out, r.URL)
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestViewerReviewsRepositoryKinds: a repository is reviewed against the kind
// it was read from (applications.argoproj.io, gitrepositories in
// source.toolkit.fluxcd.io) in its namespace, so Pacto read in argocd alone
// never shows an Application's URL.
func TestViewerReviewsRepositoryKinds(t *testing.T) {
	q := repositoryFleet(t)
	r := &reviews{allow: func(_ Person, ns string) (bool, error) { return ns != "", nil }, allowWorkload: func(_ Person, ns string, res ResourceAttributes) (bool, error) {
		return ns != "" && res.Resource != "applications", nil
	}}
	got, err := NewViewer(r.review).Query(context.Background(), alice, q)
	if err != nil {
		t.Fatal(err)
	}
	if urls := repositoryURLs(got); !slices.Equal(urls, []string{"https://github.com/acme/ledger"}) {
		t.Errorf("alice sees repositories %v", urls)
	}
	for _, want := range []string{"applications.argoproj.io/argocd", "gitrepositories.source.toolkit.fluxcd.io/flux-system", "pactos.pacto.trianalab.io/argocd"} {
		if !slices.Contains(r.askedRes, want) {
			t.Errorf("asked %v, never %s", r.askedRes, want)
		}
	}
	for _, kind := range []string{"Kustomization", "HelmRelease"} {
		if _, ok := workloadResources[kind]; !ok {
			t.Errorf("no resource reviews a repository read from a %s", kind)
		}
	}
}

// TestViewerKeyIncludesRepositoryScopes: two people with the same namespaces
// who differ only in listing Applications in argocd never share a cached
// view, and a cluster-wide reader of Pactos and workloads who may not list
// Applications never takes the fast path.
func TestViewerKeyIncludesRepositoryScopes(t *testing.T) {
	q := repositoryFleet(t)
	r := &reviews{allow: func(_ Person, ns string) (bool, error) { return ns != "", nil }, allowWorkload: func(p Person, ns string, res ResourceAttributes) (bool, error) {
		return ns != "" && (res.Resource != "applications" || p.User == "bob"), nil
	}}
	v := NewViewer(r.review)
	a, err := v.Query(context.Background(), alice, q)
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.Query(context.Background(), Person{User: "bob"}, q)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || slices.Contains(repositoryURLs(a), "https://github.com/acme/shop") || !slices.Contains(repositoryURLs(b), "https://github.com/acme/shop") {
		t.Errorf("alice sees %v and bob %v", repositoryURLs(a), repositoryURLs(b))
	}
	root := &reviews{allow: func(Person, string) (bool, error) { return true, nil }, allowWorkload: func(_ Person, _ string, res ResourceAttributes) (bool, error) {
		return res.Resource != "applications", nil
	}}
	got, err := NewViewer(root.review).Query(context.Background(), alice, q)
	if err != nil || got == q || slices.Contains(repositoryURLs(got), "https://github.com/acme/shop") {
		t.Errorf("a reader of everything but Applications sees %v, %v", repositoryURLs(got), err)
	}
}
