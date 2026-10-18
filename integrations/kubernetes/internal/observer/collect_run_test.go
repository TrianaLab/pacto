/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// countGets counts Gets of one object type and fails them with failWith when it is set.
func countGets[T client.Object](n *int, failWith error) func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
	return func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(T); ok {
			*n++
			if failWith != nil {
				return failWith
			}
		}
		return c.Get(ctx, key, obj, opts...)
	}
}

// TestCollect_ReadsTheWorkloadOnce proves the workload, persistence, health, kernel and back-compat paths
// share one GET of the workload per Collect.
func TestCollect_ReadsTheWorkloadOnce(t *testing.T) {
	f := newKernelFixture()
	gets := 0
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).
		WithInterceptorFuncs(interceptor.Funcs{Get: countGets[*appsv1.Deployment](&gets, nil)}).Build()
	in := f.input(true)
	in.Contract.Workload = kindService
	in.Contract.State = &contract.State{}
	in.Contract.Capabilities = []contract.Capability{{Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{Type: contract.CapabilityBindingHTTP, Interface: "api", Path: "/healthz"}}}

	o := New(c)
	_, _, err := o.Collect(context.Background(), in)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := o.Observe(context.Background(), kNS, "checkout", "checkout", workloadDeployment); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if gets != 1 {
		t.Fatalf("workload read %d times, want once", gets)
	}
}

// TestCollect_ReportsCollectionFailures proves a failed read surfaces as Collect's error, once per failure,
// and that a clean collection reports none.
func TestCollect_ReportsCollectionFailures(t *testing.T) {
	f := newKernelFixture()
	gets := 0
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).
		WithInterceptorFuncs(interceptor.Funcs{Get: countGets[*appsv1.Deployment](&gets, errors.New("etcd down"))}).Build()
	in := f.input(true)
	in.Contract.Workload = kindService
	in.Contract.State = &contract.State{}

	set, _, err := New(c).Collect(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "workload: failed to get Deployment checkout: etcd down") {
		t.Fatalf("err = %v, want the workload failure", err)
	}
	if n := strings.Count(err.Error(), "etcd down"); n != 1 {
		t.Fatalf("failure reported %d times, want once:\n%v", n, err)
	}
	if o := obsFor(t, set, evidence.WorkloadObserved, "checkout"); o.Outcome != evidence.Failed {
		t.Fatalf("workload outcome %s, want Failed", o.Outcome)
	}
	if gets != 1 {
		t.Fatalf("workload read %d times, want once", gets)
	}
}

func TestCollect_RecordsEveryDimensionsFailure(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]struct {
		funcs interceptor.Funcs
		input func(*CollectInput)
		want  string
	}{
		"name-match discovery": {
			funcs: interceptor.Funcs{Get: countGets[*corev1.Service](new(int), boom)},
			input: func(in *CollectInput) {
				in.InterfaceBindings = nil
				in.EnableInterfaceNameMatchDiscovery = true
			},
			want: "interfaces: failed to get Service",
		},
		"undeclared peer service": {
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if key.Name == "ledger" {
					return boom
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			want: "kernel: failed to get Service " + kNS + "/ledger",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.96.0.20", 5432)}
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).WithInterceptorFuncs(tc.funcs).Build()
			in := f.input(true)
			if tc.input != nil {
				tc.input(&in)
			}
			_, _, err := New(c).Collect(context.Background(), in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestFail_RecordsOnceAndOnlyInACollect(t *testing.T) {
	(&Observer{}).fail("x", errors.New("ignored")) // no Collect in progress: nothing to record into

	o := &Observer{run: newCollectRun()}
	o.fail("dependencies", errors.New("list failed"))
	o.fail("dependencies", errors.New("list failed"))
	o.fail("kernel", errors.New("list failed"))
	if len(o.run.failures) != 2 {
		t.Fatalf("got %v, want one failure per dimension", o.run.failures)
	}
}

func TestCheckReadinessProbeFallback_EndpointListFailure(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	dep := makeDeployment("default", "web", nil)
	dep.Spec.Template.Spec.Containers = []corev1.Container{{
		Name:           "app",
		Ports:          []corev1.ContainerPort{{ContainerPort: 8080}},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz"}}},
	}}
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(svc, dep).
		WithInterceptorFuncs(interceptor.Funcs{List: failList[*discoveryv1.EndpointSliceList]()}).Build()
	o := &Observer{client: c, run: newCollectRun()}

	hasProbe, ready := o.checkReadinessProbeFallback(context.Background(),
		CollectInput{Namespace: "default", ServiceName: "web", WorkloadName: "web", WorkloadKind: workloadDeployment}, svc, 80, "/healthz")
	if !hasProbe || ready {
		t.Fatalf("got (%v, %v), want a probe that cannot be confirmed ready", hasProbe, ready)
	}
	if len(o.run.failures) != 1 || !strings.HasPrefix(o.run.failures[0].Error(), "health: ") {
		t.Fatalf("failures = %v, want the EndpointSlice list failure", o.run.failures)
	}
}

// TestTargetPort pins the one targetPort resolver. An unset numeric targetPort means the port itself, as
// the API server defaults it: resolveTargetPort used to read it as port 0, which no container exposes.
func TestTargetPort(t *testing.T) {
	containers := []corev1.Container{{Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}}}
	cases := []struct {
		name   string
		port   corev1.ServicePort
		want   int32
		wantOK bool
	}{
		{"unset means the port", corev1.ServicePort{Port: 80}, 80, true},
		{"number", corev1.ServicePort{Port: 80, TargetPort: intstr.FromInt32(9090)}, 9090, true},
		{"name", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("http")}, 8080, true},
		{"unknown name", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("grpc")}, 0, false},
	}
	for _, tc := range cases {
		if got, ok := targetPort(tc.port, containers); got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: targetPort = (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}

	o := &Observer{}
	svc := &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}}
	if got, ok := o.resolveTargetPort(svc, 8080, &corev1.PodSpec{Containers: containers}); got != 8080 || !ok {
		t.Errorf("unset targetPort: got (%d, %v), want (8080, true)", got, ok)
	}
	if _, ok := o.resolveTargetPort(svc, 9999, &corev1.PodSpec{}); ok {
		t.Error("a port the Service does not have must not resolve")
	}
}

func TestCompiledConfigSchema_Cache(t *testing.T) {
	saved := schemaCache.m
	t.Cleanup(func() { schemaCache.m = saved })
	schemaCache.m = map[string]*jsonschema.Schema{}

	bundle := fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object"}`)}}
	first, err := compiledConfigSchema(bundle, "sha256:a", "schema.json")
	if err != nil {
		t.Fatal(err)
	}
	// The digest names the content, so the second read never touches the bundle.
	again, err := compiledConfigSchema(fstest.MapFS{}, "sha256:a", "schema.json")
	if err != nil || again != first {
		t.Fatalf("got (%p, %v), want the cached %p", again, err, first)
	}
	// No digest, no caching: the empty bundle is read and fails.
	if _, err := compiledConfigSchema(fstest.MapFS{}, "", "schema.json"); err == nil {
		t.Fatal("an undigested bundle must be read every time")
	}

	// A full cache is dropped before the next entry goes in.
	for i := range schemaCacheSize {
		schemaCache.m[string(rune('a'+i%26))+strings.Repeat("x", i)] = first
	}
	if _, err := compiledConfigSchema(bundle, "sha256:b", "schema.json"); err != nil {
		t.Fatal(err)
	}
	if len(schemaCache.m) != 1 {
		t.Fatalf("cache holds %d entries after eviction, want 1", len(schemaCache.m))
	}
}

func monitorObject(kind, field string, port any) *unstructured.Unstructured {
	m := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": promGroup + "/v1",
		"kind":       kind,
		"metadata":   map[string]any{"name": "m", "namespace": "default"},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			field:      []any{"not-a-map", map[string]any{"port": port, "path": "/m"}},
		},
	}}
	return m
}

func TestDiscoverFromMonitors_NamedPorts(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", Labels: map[string]string{"app": "web"}},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)},
				{Name: "web-metrics", Port: 9000, TargetPort: intstr.FromString("prom")},
			},
		},
	}
	podSpec := &corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080}, {Name: "prom", ContainerPort: 9100}, {Name: "debug", ContainerPort: 6060},
	}}}}

	t.Run("ServiceMonitor port is a Service port name", func(t *testing.T) {
		o := &Observer{client: fake.NewClientBuilder().WithObjects(monitorObject(promMonitorServiceMonitor, "endpoints", "web-metrics")).Build()}
		if got := o.discoverFromServiceMonitor(context.Background(), "default", svc); got == nil || got.port != 9000 || got.path != "/m" {
			t.Fatalf("got %+v, want port 9000", got)
		}
		o = &Observer{client: fake.NewClientBuilder().WithObjects(monitorObject(promMonitorServiceMonitor, "endpoints", "nope")).Build()}
		if got := o.discoverFromServiceMonitor(context.Background(), "default", svc); got != nil {
			t.Fatalf("an unknown port name resolved to %+v", got)
		}
	})

	t.Run("PodMonitor port is a container port name", func(t *testing.T) {
		for port, want := range map[string]int32{"prom": 9000, "http": 80, "debug": 0, "nope": 0} {
			o := &Observer{client: fake.NewClientBuilder().WithObjects(monitorObject(promMonitorPodMonitor, "podMetricsEndpoints", port)).Build()}
			got := o.discoverFromPodMonitor(context.Background(), "default", svc, podSpec)
			if (want == 0) != (got == nil) || (got != nil && got.port != want) {
				t.Errorf("%s: got %+v, want Service port %d", port, got, want)
			}
		}
		o := &Observer{client: fake.NewClientBuilder().WithObjects(monitorObject(promMonitorPodMonitor, "podMetricsEndpoints", "prom")).Build()}
		if got := o.discoverFromPodMonitor(context.Background(), "default", svc, nil); got != nil {
			t.Fatalf("without a pod template a name cannot resolve, got %+v", got)
		}
	})
}

// TestMonitors_ListedOncePerCollect proves each monitor kind is listed at most once per Collect however
// often discovery asks.
func TestMonitors_ListedOncePerCollect(t *testing.T) {
	lists := 0
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			if _, ok := l.(*unstructured.UnstructuredList); ok {
				lists++
			}
			return cl.List(ctx, l, opts...)
		},
	}).Build()
	o := &Observer{client: c, run: newCollectRun()}
	svc := &corev1.Service{Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "web"}}}
	for range 3 {
		o.discoverFromServiceMonitor(context.Background(), "default", svc)
		o.discoverFromPodMonitor(context.Background(), "default", svc, nil)
	}
	if lists != 2 {
		t.Fatalf("listed %d times, want once per monitor kind", lists)
	}
}
