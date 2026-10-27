package authn

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
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

func TestPersonFromToken(t *testing.T) {
	p := PersonFromToken(fakeJWT(`{"iss":"https://idp","sub":"u1","groups":["a","b"]}`))
	if p.User != "https://idp#u1" || !slices.Equal(p.Groups, []string{"a", "b"}) {
		t.Errorf("person = %+v", p)
	}
	// An IdP group named like a Kubernetes system group is not passed to the
	// review: the apiserver never grants it from a token.
	p = PersonFromToken(fakeJWT(`{"iss":"https://idp","sub":"u1","groups":["system:masters","a","system:authenticated"]}`))
	if !slices.Equal(p.Groups, []string{"a"}) {
		t.Errorf("groups = %v, want [a]", p.Groups)
	}
	for _, tok := range []string{"", "a.b", fakeJWT(`{"iss":"https://idp"}`), "h.!!.s"} {
		if p := PersonFromToken(tok); p.User != Anonymous.User {
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

type reviews struct {
	mu    sync.Mutex
	asked []string
	allow func(p Person, ns string) (bool, error)
}

func (r *reviews) review(_ context.Context, p Person, ns string) (bool, error) {
	r.mu.Lock()
	r.asked = append(r.asked, ns)
	r.mu.Unlock()
	return r.allow(p, ns)
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

func TestViewerBoundsAndFailures(t *testing.T) {
	q := viewerFleet(t, "a")
	boom := errors.New("apiserver down")
	fail := ""
	v := NewViewer(func(_ context.Context, p Person, ns string) (bool, error) {
		if ns == fail {
			return false, boom
		}
		return false, nil
	})
	fail = ""
	if _, err := v.Query(context.Background(), Person{User: "x"}, q); !errors.Is(err, boom) {
		t.Errorf("a failed cluster-wide review: %v", err)
	}
	fail = "a"
	if _, err := v.Query(context.Background(), Person{User: "x"}, q); !errors.Is(err, boom) {
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
	ok, err := review(context.Background(), Person{User: "u", Groups: []string{"g"}}, "shop")
	if err != nil || !ok {
		t.Fatalf("shop: %v %v", ok, err)
	}
	attrs := spec["resourceAttributes"].(map[string]any)
	if spec["user"] != "u" || spec["groups"].([]any)[0] != "g" || attrs["verb"] != "list" || attrs["resource"] != "pactos" || attrs["group"] != "pacto.trianalab.io" {
		t.Errorf("spec = %+v", spec)
	}
	if ok, _ := review(context.Background(), Person{User: "u"}, "other"); ok {
		t.Error("other namespace allowed")
	}
	c.PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if _, err := review(context.Background(), Person{User: "u"}, "shop"); err == nil {
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
