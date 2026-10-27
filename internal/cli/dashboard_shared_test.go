package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/trianalab/pacto/v4/internal/authn"
)

var (
	gvrTestStages = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}
	gvrTestOps    = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "operations"}
)

func deliveryObj(kind, ns, name string, fields map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": "delivery.pacto.trianalab.io/v1alpha1", "kind": kind,
		"metadata": map[string]any{"namespace": ns, "name": name, "managedFields": []any{map[string]any{"manager": "x"}}}}
	for k, v := range fields {
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func fakeDelivery(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		gvrTestStages: "StageList", gvrTestOps: "OperationList"}, objs...)
}

// sharedWorld is a cluster as the dashboard's ServiceAccount reads it, with
// RBAC that lets alice read namespace a and root everything.
type sharedWorld struct {
	sa     *dynamicfake.FakeDynamicClient
	s      *sharedReads
	sars   atomic.Int64
	sarErr error
	wrote  atomic.Bool
	now    time.Time
}

func newSharedWorld(t *testing.T) *sharedWorld {
	t.Helper()
	w := &sharedWorld{now: time.Unix(1000, 0)}
	w.sa = fakeDelivery(
		deliveryObj("Stage", "b", "s2", nil), deliveryObj("Stage", "a", "s1", nil), deliveryObj("Stage", "a", "s0", nil),
		deliveryObj("Operation", "a", "ok", map[string]any{"spec": map[string]any{"stage": "s1"}, "status": map[string]any{"outcome": "Succeeded"}}),
		deliveryObj("Operation", "a", "bad", map[string]any{"spec": map[string]any{"stage": "s1"}, "status": map[string]any{"outcome": "Failed"}}),
		deliveryObj("Operation", "a", "other", map[string]any{"spec": map[string]any{"stage": "s0"}}),
	)
	w.sa.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		w.sars.Add(1)
		if w.sarErr != nil {
			return true, nil, w.sarErr
		}
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		user, _, _ := unstructured.NestedString(o.Object, "spec", "user")
		ns, _, _ := unstructured.NestedString(o.Object, "spec", "resourceAttributes", "namespace")
		groups, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "groups")
		extra, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "extra", "k")
		allowed := user == "root" || user == "alice" && ns == "a" && len(groups) == 1 && len(extra) == 1
		o.Object["status"] = map[string]any{"allowed": allowed}
		return true, o, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w.s = newSharedReads(ctx, w.sa, func(b string) bool { return w.wrote.Load() && b == "Bearer alice" })
	w.s.now = func() time.Time { return w.now }
	for _, inf := range w.s.informers {
		if !cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
			t.Fatal("the informers never synced")
		}
	}
	return w
}

// person is a person's own client: the apiserver names them user and holds
// one Stage of its own in each namespace, which tells a read that went to
// the apiserver from one the cache answered.
func person(t *testing.T, user string, ssrErr error) *dynamicfake.FakeDynamicClient {
	t.Helper()
	own := func(ns string) *unstructured.Unstructured {
		o := deliveryObj("Stage", ns, "own", nil)
		unstructured.RemoveNestedField(o.Object, "metadata", "managedFields")
		return o
	}
	c := fakeDelivery(own("a"), own("b"))
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		if ssrErr != nil {
			return true, nil, ssrErr
		}
		ui := map[string]any{"username": user}
		if user == "alice" {
			ui["groups"], ui["extra"] = []any{"team-a"}, map[string]any{"k": []any{"v"}}
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": ui}}}, nil
	})
	return c
}

// namesOf lists the namespace/name of each item, failing t on an error or
// an item that kept its managed fields.
func namesOf(t *testing.T) func(*unstructured.UnstructuredList, error) string {
	return func(l *unstructured.UnstructuredList, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, u := range l.Items {
			if len(u.GetManagedFields()) > 0 {
				t.Fatalf("%s keeps its managed fields", u.GetName())
			}
			out = append(out, u.GetNamespace()+"/"+u.GetName())
		}
		return strings.Join(out, ",")
	}
}

func TestSharedReadsServesWhatRBACAllows(t *testing.T) {
	names := namesOf(t)
	w := newSharedWorld(t)
	ctx := context.Background()
	alice := w.s.client(person(t, "alice", nil), "alice")
	stages := alice.Resource(gvrTestStages)

	_, err := stages.List(ctx, metav1.ListOptions{})
	expect(t, apierrors.IsForbidden(err) && strings.Contains(err.Error(), "at the cluster scope") && strings.Contains(err.Error(), `"alice"`),
		"alice may not list every namespace: %v", err)
	expect(t, names(stages.Namespace("a").List(ctx, metav1.ListOptions{})) == "a/s0,a/s1", "alice reads a from the cache, sorted")
	_, err = stages.Namespace("b").List(ctx, metav1.ListOptions{})
	expect(t, apierrors.IsForbidden(err) && strings.Contains(err.Error(), `in the namespace "b"`), "b is not alice's: %v", err)

	got, err := stages.Namespace("a").Get(ctx, "s1", metav1.GetOptions{})
	expect(t, err == nil && got.GetName() == "s1" && len(got.GetManagedFields()) == 0, "alice gets a/s1: %v", err)
	_, err = stages.Namespace("b").Get(ctx, "s2", metav1.GetOptions{})
	expect(t, apierrors.IsForbidden(err), "alice may not get b/s2: %v", err)
	got, err = stages.Namespace("a").Get(ctx, "own", metav1.GetOptions{})
	expect(t, err == nil && got.GetName() == "own", "a Stage the cache has not seen yet is read from the apiserver: %v", err)
	_, err = stages.Namespace("a").Get(ctx, "s1", metav1.GetOptions{}, "status")
	expect(t, apierrors.IsNotFound(err), "a subresource goes to the apiserver: %v", err)
	_, err = stages.Get(ctx, "s1", metav1.GetOptions{})
	expect(t, err != nil, "a namespaced object has no cluster-wide get: %v", err)

	// Answers are kept ttl, then asked again.
	n := w.sars.Load()
	_, _ = stages.Namespace("a").List(ctx, metav1.ListOptions{})
	expect(t, w.sars.Load() == n, "a kept answer asks nothing")
	w.now = w.now.Add(sharedTTL)
	_, _ = stages.Namespace("a").List(ctx, metav1.ListOptions{})
	expect(t, w.sars.Load() == n+1, "an old answer is asked again")

	root := w.s.client(person(t, "root", nil), "root")
	expect(t, names(root.Resource(gvrTestStages).List(ctx, metav1.ListOptions{})) == "a/s0,a/s1,b/s2", "root reads every namespace")

	// Who am I is the answer the checks hold.
	me, err := alice.Resource(ssrGVR).Create(ctx, &unstructured.Unstructured{}, metav1.CreateOptions{})
	user, _, _ := unstructured.NestedString(me.Object, "status", "userInfo", "username")
	expect(t, err == nil && user == "alice" && me.GetKind() == "SelfSubjectReview", "who am I: %v %v", me, err)

	// Anything else is the person's own client.
	_, isShared := alice.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).(sharedResource)
	expect(t, !isShared, "configmaps are not shared")
}

// TestSharedReadsAnswersMayI: a delivery "may I" is the cached review the
// reads use; any other question, or a failing review, reaches the caller.
func TestSharedReadsAnswersMayI(t *testing.T) {
	w := newSharedWorld(t)
	ctx := context.Background()
	own := person(t, "alice", nil)
	own.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": true}}}, nil
	})
	alice := w.s.client(own, "alice").Resource(ssarGVR)
	may := func(group, verb, ns string) (bool, error) {
		r, err := alice.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
			"resourceAttributes": map[string]any{"group": group, "verb": verb, "resource": "approvals", "namespace": ns}}}}, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		ok, _, _ := unstructured.NestedBool(r.Object, "status", "allowed")
		return ok, nil
	}
	ok, err := may(deliveryGroup, "create", "a")
	expect(t, ok && err == nil, "alice may act in a: %v", err)
	ok, err = may(deliveryGroup, "create", "b")
	expect(t, !ok && err == nil, "alice may not act in b: %v", err)
	n := w.sars.Load()
	_, _ = may(deliveryGroup, "create", "a")
	expect(t, w.sars.Load() == n, "a kept answer asks nothing")
	ok, err = may("apps", "create", "b")
	expect(t, ok && err == nil && w.sars.Load() == n, "another group asks the apiserver with alice's token: %v", err)
	w.sarErr, w.now = errors.New("apiserver down"), w.now.Add(sharedTTL)
	_, err = may(deliveryGroup, "create", "a")
	expect(t, err != nil, "a failed review is an error, never an allow")
	_, err = w.s.client(person(t, "bob", errors.New("token expired")), "bob").Resource(ssarGVR).Create(ctx,
		&unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
			"resourceAttributes": map[string]any{"group": deliveryGroup}}}}, metav1.CreateOptions{})
	expect(t, err != nil, "a token the apiserver does not know is an error")
}

func TestSharedReadsSelectsOperations(t *testing.T) {
	names := namesOf(t)
	w := newSharedWorld(t)
	ctx := context.Background()
	ops := w.s.client(person(t, "alice", nil), "alice").Resource(gvrTestOps).Namespace("a")
	expect(t, names(ops.List(ctx, metav1.ListOptions{FieldSelector: "status.outcome!=Succeeded"})) == "a/bad,a/other", "not succeeded")
	expect(t, names(ops.List(ctx, metav1.ListOptions{FieldSelector: "spec.stage=s1,metadata.name=bad"})) == "a/bad", "one Stage's")
	// What the cache does not evaluate goes to the apiserver, which holds none.
	for _, o := range []metav1.ListOptions{{LabelSelector: "x=y"}, {FieldSelector: "spec.type=Promote"}, {Limit: 1}} {
		expect(t, names(ops.List(ctx, o)) == "", "%+v goes to the apiserver", o)
	}
	_, ok := sharedResource{gvr: gvrTestOps}.selector(metav1.ListOptions{FieldSelector: "a"})
	expect(t, !ok, "a selector the cache cannot read goes to the apiserver, which refuses it")
}

func TestSharedReadsFallsBack(t *testing.T) {
	names := namesOf(t)
	w := newSharedWorld(t)
	ctx := context.Background()
	alice := w.s.client(person(t, "alice", nil), "alice").Resource(gvrTestStages).Namespace("a")

	w.wrote.Store(true)
	expect(t, names(alice.List(ctx, metav1.ListOptions{})) == "a/own", "a person who just wrote reads the apiserver")
	got, err := alice.Get(ctx, "own", metav1.GetOptions{})
	expect(t, err == nil && got.GetName() == "own", "and gets from it: %v", err)
	w.wrote.Store(false)

	unsynced := &sharedReads{informers: map[schema.GroupVersionResource]cache.SharedIndexInformer{
		gvrTestStages: cache.NewSharedIndexInformer(&cache.ListWatch{}, &unstructured.Unstructured{}, 0, cache.Indexers{})}, wrote: func(string) bool { return false }}
	expect(t, names(unsynced.client(person(t, "alice", nil), "alice").Resource(gvrTestStages).Namespace("b").List(ctx, metav1.ListOptions{})) == "b/own",
		"an unsynced cache sends the person to the apiserver")

	// A check that cannot be made is an error, never an allow.
	w.sarErr = errors.New("apiserver down")
	w.now = w.now.Add(sharedTTL)
	_, err = alice.List(ctx, metav1.ListOptions{})
	expect(t, err != nil && strings.Contains(err.Error(), "apiserver down"), "a failed review: %v", err)
	_, err = w.s.client(person(t, "bob", errors.New("token expired")), "bob").Resource(gvrTestStages).Namespace("a").List(ctx, metav1.ListOptions{})
	expect(t, err != nil && strings.Contains(err.Error(), "token expired"), "a token the apiserver refuses: %v", err)
	_, err = w.s.client(person(t, "bob", errors.New("token expired")), "bob").Resource(ssrGVR).Create(ctx, nil, metav1.CreateOptions{})
	expect(t, err != nil, "who am I with a refused token: %v", err)

	// Full caches start over.
	for i := range maxAnswers {
		w.s.answers[string(rune(i))] = verdict{}
	}
	w.s.keep(func() {})
	expect(t, len(w.s.answers) == 0, "a full cache starts over")
}

func TestReadCacheRemembersWriters(t *testing.T) {
	c := newReadCache()
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	rt := c.wrap(&backend{})
	call(t, rt, http.MethodPost, "https://k/apis/x", "alice")
	expect(t, c.wrote("Bearer alice") && !c.wrote("Bearer bob"), "alice just wrote")
	now = now.Add(readTTL)
	expect(t, !c.wrote("Bearer alice"), "a while ago")
	call(t, rt, http.MethodPost, "https://k/apis/x", "bob")
	expect(t, len(c.writers) == 1, "old writers are dropped: %v", c.writers)
}

func TestDashboardDeliveryReadsShared(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.token-login", true)
	id := identityFor(t, v, "0.0.0.0", &rest.Config{Host: "https://k"})
	sa := fakeDelivery()
	id.newClient = func(*rest.Config) (dynamic.Interface, error) { return sa, nil }
	id.state = func(context.Context) authn.State { return authn.State{SignedIn: true, Token: "alice"} }
	api, err := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil && id.shared != nil, "a signed-in person starts the shared cache: %v", err)
	dd := api.(dashDelivery)
	_, shared := dd.d.Dynamic.(sharedClient)
	expect(t, shared && dd.act.Dynamic == sa, "reads go through the shared cache, actions do not")

	failing := identityFor(t, v, "0.0.0.0", &rest.Config{Host: "https://k"})
	failing.newClient = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("no") }
	expect(t, failing.sharedReads() == nil, "no client, no shared cache")
	local := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://k"})
	expect(t, local.sharedReads() == nil, "local mode reads as the kubeconfig user")
}
