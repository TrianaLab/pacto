/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

const (
	opNS   = "pacto-system"
	deploy = "pacto-operator"
	cmName = "pacto-operator-detected"
)

var now = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

func pod(ns, name, owner, image string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(ns + "/" + name)},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if owner != "" {
		yes := true
		p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: owner, UID: types.UID(owner), Controller: &yes}}
	}
	return p
}

func operatorDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: opNS, Name: deploy, UID: "deploy-uid"}}
}

func clusterObjects() []client.Object {
	class := "nginx"
	return []client.Object{
		operatorDeployment(),
		pod("a", "a1", "rs-a", "ghcr.io/acme/app:1"), pod("a", "a2", "rs-a", "ghcr.io/acme/app:1"),
		pod("b", "b1", "", "ghcr.io/acme/web:1"), pod("c", "c1", "rs-c", "quay.io/x/y:1"),
		pod("kube-system", "k1", "", "registry.k8s.io/coredns/coredns"), pod(opNS, "op", "", "ghcr.io/trianalab/op"),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "pending"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "evil.io/x/y"}}}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Spec: corev1.NodeSpec{ProviderID: "kind://docker/kind/n1"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "6.1.0"}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "auth", Name: "dex", Labels: map[string]string{"app.kubernetes.io/name": "dex"}}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "i"}, Spec: networkingv1.IngressSpec{IngressClassName: &class}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "b", Name: "i", Annotations: map[string]string{"kubernetes.io/ingress.class": "nginx"}}},
	}
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = networkingv1.AddToScheme(s)
	return s
}

func newClient(funcs interceptor.Funcs, objs ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).
		WithIndex(&corev1.Pod{}, "status.phase", func(o client.Object) []string { return []string{string(o.(*corev1.Pod).Status.Phase)} }).
		WithInterceptorFuncs(funcs).Build()
}

func servedAll(string) (bool, error) { return true, nil }

func newDetector(c client.WithWatch, served func(string) (bool, error), watched ...string) *Detector {
	d := ForOperator(deploy, opNS, watched, "pacto-node-agent", kubecluster.Identity{ID: "cluster-uid", Name: "kind-pacto"}, c, c, served, nil, nil, detect.GatewayLogsAuto)
	d.Now = func() time.Time { return now }
	return d
}

// readResult is the Result the detector wrote.
func readResult(t *testing.T, c client.Client) (*corev1.ConfigMap, detect.Result) {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: opNS, Name: cmName}, cm); err != nil {
		t.Fatalf("reading the ConfigMap: %v", err)
	}
	res, err := detect.Decode([]byte(cm.Data[detect.ConfigMapKey]))
	if err != nil {
		t.Fatal(err)
	}
	return cm, res
}

func entry(t *testing.T, res detect.Result, kind string) detect.Entry {
	t.Helper()
	i := slices.IndexFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == kind })
	if i < 0 {
		t.Fatalf("no %s entry in %+v", kind, res.Entries)
	}
	return res.Entries[i]
}

// routesInterceptor answers the unstructured HTTPRoute list with routes.
func routesInterceptor(routes ...map[string]any) interceptor.Funcs {
	return interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if u, ok := l.(*unstructured.UnstructuredList); ok {
			for _, r := range routes {
				u.Items = append(u.Items, unstructured.Unstructured{Object: r})
			}
			return nil
		}
		return c.List(ctx, l, opts...)
	}}
}

func route(ns string, parents ...map[string]any) map[string]any {
	refs := make([]any, 0, len(parents))
	for _, p := range parents {
		refs = append(refs, p)
	}
	return map[string]any{"metadata": map[string]any{"namespace": ns, "name": "r"}, "spec": map[string]any{"parentRefs": refs}}
}

func TestDetectorWritesOnlyTheConfigMap(t *testing.T) {
	var writes []string
	count := func(verb string, obj any) { writes = append(writes, verb+" "+reflect.TypeOf(obj).String()) }
	funcs := routesInterceptor()
	funcs.Create = func(_ context.Context, _ client.WithWatch, o client.Object, _ ...client.CreateOption) error {
		count("create", o)
		return nil
	}
	funcs.Update = func(_ context.Context, _ client.WithWatch, o client.Object, _ ...client.UpdateOption) error {
		count("update", o)
		return nil
	}
	funcs.Patch = func(_ context.Context, _ client.WithWatch, o client.Object, _ client.Patch, _ ...client.PatchOption) error {
		count("patch", o)
		return nil
	}
	funcs.Delete = func(_ context.Context, _ client.WithWatch, o client.Object, _ ...client.DeleteOption) error {
		count("delete", o)
		return nil
	}
	funcs.Apply = func(ctx context.Context, c client.WithWatch, o runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
		count("apply", o)
		return c.Apply(ctx, o, opts...)
	}
	c := newClient(funcs, clusterObjects()...)
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(writes, []string{"apply *v1.ConfigMapApplyConfiguration"}) {
		t.Fatalf("writes = %q", writes)
	}
	readResult(t, c) // it is ConfigMap <name> in <namespace>
}

func TestDetectorHoldsNoControllerOptions(t *testing.T) {
	for field := range reflect.TypeFor[Detector]().Fields() {
		f := field.Type
		for f.Kind() == reflect.Pointer || f.Kind() == reflect.Slice {
			f = f.Elem()
		}
		for _, forbidden := range []string{"/cmd", "/internal/observer", "/internal/controller", "/internal/dashboard"} {
			if strings.Contains(f.PkgPath(), forbidden) {
				t.Errorf("field %s has type %s from %s", field.Name, f, f.PkgPath())
			}
		}
	}
}

func TestDetectorResultAllKinds(t *testing.T) {
	c := newClient(routesInterceptor(
		route("a", map[string]any{"name": "public", "namespace": "infra"}),
		route("b", map[string]any{"name": "public", "namespace": "infra"}, map[string]any{"name": "svc", "kind": "Service"}, map[string]any{}),
		route("c", map[string]any{"name": "own"}),
	), clusterObjects()...)
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	cm, res := readResult(t, c)
	if !res.AsOf.Equal(now) || res.Cluster != "cluster-uid" || res.ClusterName != "kind-pacto" || len(res.Entries) != 8 || cm.Labels[detect.ManagedByLabel] != detect.ManagedBy {
		t.Fatalf("result = %+v", res)
	}
	for kind, ok := range map[string]func(detect.Entry) bool{
		"registry": func(e detect.Entry) bool {
			return e.Value == "ghcr.io/acme" && e.Evidence == "in 2 of 3 namespaces, 67% of workloads" && e.Scope == fleet.ScopeCluster
		},
		"cloud":   func(e detect.Entry) bool { return e.Value == "kind" },
		"nodes":   func(e detect.Entry) bool { return e.Value == "kernel 6.1 on 1 of 1 nodes" },
		"metrics": func(e detect.Entry) bool { return e.Value == "Prometheus Operator" },
		"sign-in": func(e detect.Entry) bool { return e.Value == "Dex" && slices.Equal(e.Namespaces, []string{"auth"}) },
		"exposure": func(e detect.Entry) bool {
			return e.Value == "Gateway API" && strings.Contains(e.Hint, "parentRefs[0].namespace=infra") && e.Scope == fleet.ScopeCluster
		},
	} {
		if e := entry(t, res, kind); !ok(e) {
			t.Errorf("%s = %+v", kind, e)
		}
	}
}

func TestDetectorIngressWhenGatewayNotServed(t *testing.T) {
	c := newClient(interceptor.Funcs{}, clusterObjects()...)
	d := newDetector(c, func(gv string) (bool, error) { return gv != gatewayGV, nil })
	_ = d.Sync(context.Background())
	_, res := readResult(t, c)
	if e := entry(t, res, "exposure"); e.Value != "Ingress class nginx" {
		t.Errorf("exposure = %+v", e)
	}
}

func TestDetectorExcludesSkippedNamespaces(t *testing.T) {
	c := newClient(routesInterceptor(), clusterObjects()...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	for _, d := range entry(t, res, "registry").Details {
		if strings.Contains(d, "registry.k8s.io") || strings.Contains(d, "trianalab") || strings.Contains(d, "evil.io") {
			t.Errorf("skipped or pending pod voted: %q", d)
		}
	}
}

func TestDetectorScopedListsWatchedNamespaces(t *testing.T) {
	var listed []string
	c := newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		o := &client.ListOptions{}
		o.ApplyOptions(opts)
		listed = append(listed, reflect.TypeOf(l).Elem().Name()+":"+o.Namespace)
		if _, ok := l.(*unstructured.UnstructuredList); ok {
			return nil
		}
		return c.List(ctx, l, opts...)
	}}, clusterObjects()...)
	_ = newDetector(c, servedAll, "a", "b").Sync(context.Background())
	for _, l := range listed {
		if strings.HasSuffix(l, ":") {
			t.Errorf("listed across every namespace: %s", l)
		}
	}
	_, res := readResult(t, c)
	if !slices.Equal(res.Namespaces, []string{"a", "b"}) {
		t.Errorf("result namespaces = %v", res.Namespaces)
	}
	e := entry(t, res, "registry")
	if e.Scope != "" || !slices.Equal(e.Namespaces, []string{"a", "b"}) || e.Completeness != detect.Partial {
		t.Errorf("registry = %+v", e)
	}
	for _, kind := range []string{"sign-in", "exposure"} {
		if e := entry(t, res, kind); e.Completeness != detect.Partial || !strings.Contains(e.Reason, "watched namespaces") {
			t.Errorf("%s = %+v", kind, e)
		}
	}
	if e := entry(t, res, "exposure"); e.Scope != "" || len(e.Namespaces) == 0 {
		t.Errorf("exposure = %+v", e)
	}
}

func TestDetectorScopedNeverListsNodes(t *testing.T) {
	c := newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, ok := l.(*corev1.NodeList); ok {
			t.Fatal("a scoped detector listed nodes")
		}
		if _, ok := l.(*unstructured.UnstructuredList); ok {
			return nil
		}
		return c.List(ctx, l, opts...)
	}}, clusterObjects()...)
	_ = newDetector(c, servedAll, "a").Sync(context.Background())
}

func TestDetectorScopedNodesUnknown(t *testing.T) {
	c := newClient(routesInterceptor(), clusterObjects()...)
	_ = newDetector(c, servedAll, "a").Sync(context.Background())
	_, res := readResult(t, c)
	for _, kind := range []string{"cloud", "nodes"} {
		if e := entry(t, res, kind); e.Completeness != detect.Unknown || e.Reason != scopedNodes {
			t.Errorf("%s = %+v", kind, e)
		}
	}
}

func TestDetectorDiscoveryErrorIsUnknown(t *testing.T) {
	c := newClient(routesInterceptor(), clusterObjects()...)
	failing := func(string) (bool, error) { return false, errors.New("dial tcp 10.0.0.1:443: i/o timeout") }
	_ = newDetector(c, failing).Sync(context.Background())
	_, res := readResult(t, c)
	for _, kind := range []string{"metrics", "sign-in", "exposure"} {
		e := entry(t, res, kind)
		if e.Completeness != detect.Unknown || strings.Contains(e.Reason, "10.0.0.1") || !strings.Contains(e.Reason, "did not answer") {
			t.Errorf("%s = %+v", kind, e)
		}
	}
}

func TestDetectorForbiddenPodsIsUnknown(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "x"}, "", errors.New("no"))
	c := newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		switch l.(type) {
		case *corev1.PodList, *corev1.NodeList, *networkingv1.IngressList, *corev1.ServiceList, *unstructured.UnstructuredList:
			return forbidden
		}
		return c.List(ctx, l, opts...)
	}}, clusterObjects()...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	for _, kind := range []string{"registry", "cloud", "nodes", "sign-in", "exposure"} {
		if e := entry(t, res, kind); e.Completeness != detect.Unknown || !strings.Contains(e.Reason, "Forbidden") {
			t.Errorf("%s = %+v", kind, e)
		}
	}
}

func TestDetectorRoutesNoMatchIsNone(t *testing.T) {
	c := newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, ok := l.(*unstructured.UnstructuredList); ok {
			return &meta.NoKindMatchError{GroupKind: httpRouteGVK.GroupKind()}
		}
		return c.List(ctx, l, opts...)
	}}, clusterObjects()...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	if e := entry(t, res, "exposure"); e.Value != "Ingress class nginx" {
		t.Errorf("exposure = %+v", e)
	}
}

// pagedList serves a list in pages of one item through Continue tokens,
// counting the pages.
func pagedList(pages *int, items int, fill func(l client.ObjectList, i int)) interceptor.Funcs {
	return interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		switch l.(type) {
		case *corev1.PodList, *corev1.NodeList:
			o := &client.ListOptions{}
			o.ApplyOptions(opts)
			if o.Namespace == detect.GatewayNamespace { // the egress gateway list, one unpaged read
				return c.List(ctx, l, opts...)
			}
			i := 0
			if o.Continue != "" {
				i = len(o.Continue)
			}
			*pages++
			fill(l, i)
			if i+1 < items {
				l.(metav1.ListInterface).SetContinue(strings.Repeat("x", i+1))
			}
			return nil
		case *unstructured.UnstructuredList:
			return nil
		}
		return c.List(ctx, l, opts...)
	}}
}

func TestDetectorPagesPodsAndNodes(t *testing.T) {
	pages := 0
	c := newClient(pagedList(&pages, 3, func(l client.ObjectList, i int) {
		switch l := l.(type) {
		case *corev1.PodList:
			l.Items = []corev1.Pod{*pod(string(rune('a'+i)), "p", "", "ghcr.io/acme/app")}
		case *corev1.NodeList:
			l.Items = []corev1.Node{{Spec: corev1.NodeSpec{ProviderID: "kind://n"}}}
		}
	}), operatorDeployment())
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	if pages != 6 {
		t.Errorf("pages = %d, want 3 of pods and 3 of nodes", pages)
	}
	if e := entry(t, res, "registry"); e.Evidence != "in 3 of 3 namespaces, 100% of workloads" || e.Completeness != detect.Complete {
		t.Errorf("registry = %+v", e)
	}
	if e := entry(t, res, "cloud"); e.Evidence != "on 3 of 3 nodes" {
		t.Errorf("cloud = %+v", e)
	}
}

func TestDetectorCapsPods(t *testing.T) {
	pages := 0
	big := func(l client.ObjectList, i int) {
		switch l := l.(type) {
		case *corev1.PodList:
			l.Items = make([]corev1.Pod, detect.MaxPods/2+1)
			for j := range l.Items {
				l.Items[j] = *pod(string(rune('a'+i)), "p", "", "ghcr.io/acme/app")
				l.Items[j].UID = types.UID(strings.Repeat("u", i) + string(rune(j)))
			}
		case *corev1.NodeList:
			l.Items = make([]corev1.Node, detect.MaxNodes/2+1)
			for j := range l.Items {
				l.Items[j].Spec.ProviderID = "kind://n"
			}
		}
	}
	c := newClient(pagedList(&pages, 3, big), operatorDeployment())
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	if e := entry(t, res, "registry"); e.Completeness != detect.Partial || !strings.Contains(e.Reason, "sampled the first 20000 running pods") {
		t.Errorf("registry = %+v", e)
	}
	if e := entry(t, res, "cloud"); e.Completeness != detect.Partial || !strings.Contains(e.Reason, "sampled the first 5000 nodes") {
		t.Errorf("cloud = %+v", e)
	}
	if e := entry(t, res, "nodes"); !strings.Contains(e.Reason, "not confirmed on the node; sampled the first 5000 nodes") {
		t.Errorf("nodes = %+v", e)
	}
	if pages != 4 {
		t.Errorf("pages = %d, want the reads to stop at the caps", pages)
	}
}

func TestDetectorOwnerAndLabels(t *testing.T) {
	c := newClient(routesInterceptor(), clusterObjects()...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	cm, _ := readResult(t, c)
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].Kind != "Deployment" || cm.OwnerReferences[0].Name != deploy || cm.OwnerReferences[0].UID != "deploy-uid" {
		t.Errorf("owner = %+v", cm.OwnerReferences)
	}
	want := map[string]string{"app.kubernetes.io/part-of": "pacto", "app.kubernetes.io/component": "detection", "app.kubernetes.io/managed-by": "pacto-operator"}
	if !reflect.DeepEqual(cm.Labels, want) {
		t.Errorf("labels = %v", cm.Labels)
	}
}

func TestDetectorSyncNeverFails(t *testing.T) {
	funcs := routesInterceptor()
	funcs.Apply = func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
		return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, cmName, errors.New("x"))
	}
	c := newClient(funcs, clusterObjects()...)
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Errorf("Sync = %v, want nil", err)
	}
}

func TestDetectorNoOwnerNoWrite(t *testing.T) {
	c := newClient(routesInterceptor(), clusterObjects()[1:]...)
	d := newDetector(c, servedAll)
	for range 2 {
		if err := d.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: opNS, Name: cmName}, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("an ownerless ConfigMap was written: %v", err)
	}
	if err := c.Create(context.Background(), operatorDeployment()); err != nil {
		t.Fatal(err)
	}
	_ = d.Sync(context.Background())
	if cm, _ := readResult(t, c); len(cm.OwnerReferences) != 1 {
		t.Errorf("owner = %+v", cm.OwnerReferences)
	}
}

func TestDetectorStartStopsWithContext(t *testing.T) {
	c := newClient(routesInterceptor(), clusterObjects()...)
	d := newDetector(c, servedAll)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	readResult(t, c)
}

func discoveryServer(t *testing.T, status int) *rest.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","code":%d}`, status)
			return
		}
		_, _ = w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"monitoring.coreos.com/v1","resources":[]}`))
	}))
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

func TestServedForNotFoundIsFalse(t *testing.T) {
	if ok, err := ServedFor(discoveryServer(t, http.StatusNotFound))(monitoringGV); ok || err != nil {
		t.Errorf("served = %v, %v; want false, nil", ok, err)
	}
	if ok, err := ServedFor(discoveryServer(t, http.StatusOK))(monitoringGV); !ok || err != nil {
		t.Errorf("served = %v, %v; want true, nil", ok, err)
	}
}

func TestServedForErrorIsError(t *testing.T) {
	if _, err := ServedFor(discoveryServer(t, http.StatusForbidden))(monitoringGV); err == nil {
		t.Error("a Forbidden discovery read as not served")
	}
	bad := &rest.Config{Host: "https://127.0.0.1:1", TLSClientConfig: rest.TLSClientConfig{CAFile: "/nonexistent/ca.crt"}}
	if _, err := ServedFor(bad)(monitoringGV); err == nil {
		t.Error("an unbuildable client read as not served")
	}
}

// pulling is a running pod in ns that names pull Secrets.
func pulling(ns, name, image string, secrets ...string) *corev1.Pod {
	p := pod(ns, name, "", image)
	for _, s := range secrets {
		p.Spec.ImagePullSecrets = append(p.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: s})
	}
	return p
}

func TestDetectorCopiesPullSecretNames(t *testing.T) {
	c := newClient(interceptor.Funcs{}, operatorDeployment(),
		pulling("a", "a1", "ghcr.io/acme/app:1", "ghcr-pull"), pulling("b", "b1", "ghcr.io/acme/web:1", "ghcr-pull", "other"),
		pulling("b", "b2", "ghcr.io/acme/web:1", "other"))
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	if e := entry(t, res, "pull-secret"); e.Value != "b/other" || !strings.Contains(e.Evidence, "named in the pod specs of 2 running workloads") {
		t.Errorf("pull-secret = %+v", e)
	}
	// No pod names a pull Secret: no entry, and no unknown row either.
	c = newClient(interceptor.Funcs{}, clusterObjects()...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	if _, res := readResult(t, c); slices.ContainsFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == "pull-secret" }) {
		t.Errorf("entries = %+v", res.Entries)
	}
}

func TestDetectorWritesInstallationBlock(t *testing.T) {
	c := newClient(interceptor.Funcs{}, clusterObjects()...)
	d := newDetector(c, servedAll)
	if err := d.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, res := readResult(t, c); res.Installation != nil {
		t.Errorf("no --registry, still an installation: %+v", res.Installation)
	}
	inst := &detect.Installation{Registry: "ghcr.io/acme", Targets: "ghcr.io/acme/pacto-targets", SnapshotKey: "AAEC", Pushes: []string{"ghcr.io/acme/pacto-targets"}}
	d.Installation, d.LastPush = inst, func() *detect.PushResult { return nil }
	_ = d.Sync(context.Background())
	_, res := readResult(t, c)
	if res.Installation == nil || res.Installation.Registry != "ghcr.io/acme" || res.Installation.LastPush != nil ||
		!slices.Equal(res.Installation.Pushes, inst.Pushes) {
		t.Errorf("installation = %+v", res.Installation)
	}
}

func TestDetectorCopiesLastPush(t *testing.T) {
	c := newClient(interceptor.Funcs{}, clusterObjects()...)
	last := &detect.PushResult{At: now, Ref: "ghcr.io/acme/pacto-targets:cluster-cluster-uid"}
	inst := &detect.Installation{Registry: "ghcr.io/acme"}
	d := ForOperator(deploy, opNS, nil, "", kubecluster.Identity{ID: "cluster-uid"}, c, c, servedAll, inst, func() *detect.PushResult { return last }, detect.GatewayLogsAuto)
	d.Now = func() time.Time { return now }
	_ = d.Sync(context.Background())
	_, res := readResult(t, c)
	if res.Installation == nil || res.Installation.LastPush == nil || res.Installation.LastPush.Ref != last.Ref || inst.LastPush != nil {
		t.Errorf("installation = %+v, configured = %+v", res.Installation, inst)
	}
}

func TestDetectorReportsIstioEgress(t *testing.T) {
	for _, c := range []struct {
		served func(string) (bool, error)
		want   string
	}{
		{servedAll, detect.Complete},
		{func(gv string) (bool, error) { return gv != istioGV, nil }, detect.Unknown},
		{func(gv string) (bool, error) {
			if gv == istioGV {
				return false, errors.New("dial tcp 10.0.0.1:443: i/o timeout")
			}
			return true, nil
		}, detect.Unknown},
	} {
		c0 := newClient(interceptor.Funcs{}, clusterObjects()...)
		_ = newDetector(c0, c.served).Sync(context.Background())
		_, res := readResult(t, c0)
		if e := entry(t, res, "istio-egress"); e.Completeness != c.want || e.Scope != fleet.ScopeCluster || strings.Contains(e.Reason, "10.0.0.1") {
			t.Errorf("istio-egress = %+v", e)
		}
	}
}

func TestDetectorReportsCiliumFQDN(t *testing.T) {
	for _, c := range []struct {
		served func(string) (bool, error)
		want   string
	}{
		{servedAll, detect.Complete},
		{func(gv string) (bool, error) { return gv != ciliumGV, nil }, detect.Unknown},
		{func(gv string) (bool, error) {
			if gv == ciliumGV {
				return false, errors.New("dial tcp 10.0.0.1:443: i/o timeout")
			}
			return true, nil
		}, detect.Unknown},
	} {
		c0 := newClient(interceptor.Funcs{}, clusterObjects()...)
		_ = newDetector(c0, c.served).Sync(context.Background())
		_, res := readResult(t, c0)
		if e := entry(t, res, "cilium-fqdn"); e.Completeness != c.want || e.Scope != fleet.ScopeCluster || strings.Contains(e.Reason, "10.0.0.1") {
			t.Errorf("cilium-fqdn = %+v", e)
		}
	}
}

// egressGatewayPod is a running pod labelled istio=egressgateway in ns, owned by the istio-egressgateway
// Deployment through its ReplicaSet.
func egressGatewayPod(ns, name string) *corev1.Pod {
	p := pod(ns, name, "istio-egressgateway-7d9f", "docker.io/istio/proxyv2:1.24.0")
	p.Labels = map[string]string{"istio": "egressgateway", appsv1.DefaultDeploymentUniqueLabelKey: "7d9f"}
	return p
}

func TestDetectorReportsEgressGateway(t *testing.T) {
	bare := egressGatewayPod(detect.GatewayNamespace, "bare")
	bare.OwnerReferences = nil
	objs := append(clusterObjects(), egressGatewayPod(detect.GatewayNamespace, "istio-egressgateway-7d9f-a"), bare)
	c := newClient(routesInterceptor(), objs...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	e := entry(t, res, "egress-gateway")
	want := []detect.Object{{Kind: "Pod", Namespace: detect.GatewayNamespace, Name: "istio-egressgateway-7d9f-a"}}
	if e.Completeness != detect.Partial || e.Evidence != "1 Istio egress gateway found; logs: auto" || !slices.Equal(e.Objects, want) ||
		e.Scope != fleet.ScopeCluster || !strings.Contains(e.Hint, "controller.egressGatewayLogs=on") {
		t.Errorf("egress-gateway = %+v", e)
	}

	// Without Istio nothing is listed and no entry is written.
	c = newClient(routesInterceptor(), objs...)
	_ = newDetector(c, func(gv string) (bool, error) { return gv != istioGV, nil }).Sync(context.Background())
	if _, res := readResult(t, c); slices.ContainsFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == "egress-gateway" }) {
		t.Errorf("entries = %+v", res.Entries)
	}

	// A list the operator may not make is unknown, never "no gateway".
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("no"))
	c = newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if lo := (&client.ListOptions{}).ApplyOptions(opts); lo.Namespace == detect.GatewayNamespace {
			return forbidden
		}
		return c.List(ctx, l, opts...)
	}}, objs...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res = readResult(t, c)
	if e := entry(t, res, "egress-gateway"); e.Completeness != detect.Unknown || !strings.Contains(e.Reason, "Forbidden") {
		t.Errorf("forbidden egress-gateway = %+v", e)
	}
}

func TestDetectorIgnoresForeignEgressGatewayLabel(t *testing.T) {
	// A tenant labels its own pod in shop: the detector never lists outside istio-system, so there is no gateway.
	c := newClient(routesInterceptor(), append(clusterObjects(), egressGatewayPod("shop", "tenant"))...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	if _, res := readResult(t, c); slices.ContainsFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == "egress-gateway" }) {
		t.Errorf("entries = %+v", res.Entries)
	}
}
