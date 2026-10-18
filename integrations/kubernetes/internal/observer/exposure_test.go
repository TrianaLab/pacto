/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// routes answers HTTPRoute lists with the given routes (or error) and passes every other list through.
func routes(err error, items ...map[string]any) interceptor.Funcs {
	return interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		u, ok := l.(*unstructured.UnstructuredList)
		if !ok {
			return c.List(ctx, l, opts...)
		}
		if err != nil {
			return err
		}
		for _, it := range items {
			u.Items = append(u.Items, unstructured.Unstructured{Object: it})
		}
		return nil
	}}
}

func route(ns string, refs ...any) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": "r", "namespace": ns},
		"spec":     map[string]any{"rules": []any{"not-a-map", map[string]any{"backendRefs": append([]any{"not-a-map"}, refs...)}}},
	}
}

func ingress(name string, backend *networkingv1.IngressServiceBackend, asDefault bool) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: kNS}}
	if asDefault {
		ing.Spec.DefaultBackend = &networkingv1.IngressBackend{Service: backend}
		return ing
	}
	ing.Spec.Rules = []networkingv1.IngressRule{
		{Host: "no-http"},
		{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
			Paths: []networkingv1.HTTPIngressPath{{Backend: networkingv1.IngressBackend{Service: backend}}},
		}}},
	}
	return ing
}

// withLB publishes the given load balancer addresses on an Ingress.
func withLB(ing *networkingv1.Ingress, addrs ...networkingv1.IngressLoadBalancerIngress) *networkingv1.Ingress {
	ing.Status.LoadBalancer.Ingress = addrs
	return ing
}

// TestInterfaceExposure pins the exposure rule: external only on positive evidence of an address outside
// private networks, cluster only for a ClusterIP Service nothing routes to, unknown ("") otherwise.
func TestInterfaceExposure(t *testing.T) {
	port := corev1.ServicePort{Name: "http", Port: 80}
	byNumber := &networkingv1.IngressServiceBackend{Name: "checkout", Port: networkingv1.ServiceBackendPort{Number: 80}}
	byName := &networkingv1.IngressServiceBackend{Name: "checkout", Port: networkingv1.ServiceBackendPort{Name: "http"}}
	otherSvc := &networkingv1.IngressServiceBackend{Name: "other", Port: networkingv1.ServiceBackendPort{Number: 80}}
	otherPort := &networkingv1.IngressServiceBackend{Name: "checkout", Port: networkingv1.ServiceBackendPort{Number: 81}}
	noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute"}}
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "httproutes"}, "", errors.New("rbac"))
	publicIng := networkingv1.IngressLoadBalancerIngress{IP: "203.0.113.10"}
	privateIng := networkingv1.IngressLoadBalancerIngress{IP: "10.1.2.3"}
	lb := func(annotations map[string]string, addrs ...corev1.LoadBalancerIngress) func(*corev1.Service) {
		return func(s *corev1.Service) {
			s.Spec.Type = corev1.ServiceTypeLoadBalancer
			s.Annotations = annotations
			s.Status.LoadBalancer.Ingress = addrs
		}
	}
	nodePort := func(s *corev1.Service) { s.Spec.Type = corev1.ServiceTypeNodePort }

	tests := []struct {
		name  string
		svc   func(*corev1.Service)
		objs  []client.Object
		funcs interceptor.Funcs
		want  evidence.Exposure
	}{
		{name: "node port is unknown", svc: nodePort, want: ""},
		{name: "load balancer without an address", svc: lb(nil), want: ""},
		{name: "load balancer with a public IP", svc: lb(nil, corev1.LoadBalancerIngress{IP: "203.0.113.10"}), want: evidence.ExposureExternal},
		{name: "load balancer with a public hostname", svc: lb(nil, corev1.LoadBalancerIngress{Hostname: "a1.elb.amazonaws.com"}), want: evidence.ExposureExternal},
		{name: "load balancer with an internal ELB hostname", svc: lb(nil, corev1.LoadBalancerIngress{Hostname: "internal-a1.elb.amazonaws.com"}), want: ""},
		{name: "load balancer with private, CGNAT, link-local and ULA addresses", svc: lb(nil,
			corev1.LoadBalancerIngress{IP: "10.0.0.1"}, corev1.LoadBalancerIngress{IP: "100.64.1.1"},
			corev1.LoadBalancerIngress{IP: "169.254.1.1"}, corev1.LoadBalancerIngress{IP: "fd00::1"},
			corev1.LoadBalancerIngress{IP: "127.0.0.1"}, corev1.LoadBalancerIngress{IP: "not-an-ip"}), want: ""},
		{name: "gke internal load balancer", svc: lb(map[string]string{"networking.gke.io/load-balancer-type": "Internal"},
			corev1.LoadBalancerIngress{IP: "203.0.113.10"}), want: ""},
		{name: "aws internal load balancer by CIDR", svc: lb(map[string]string{"service.beta.kubernetes.io/aws-load-balancer-internal": "0.0.0.0/0"},
			corev1.LoadBalancerIngress{IP: "203.0.113.10"}), want: ""},
		{name: "aws internal annotation set to false", svc: lb(map[string]string{"service.beta.kubernetes.io/aws-load-balancer-internal": "false"},
			corev1.LoadBalancerIngress{IP: "203.0.113.10"}), want: evidence.ExposureExternal},
		{name: "public external IP", svc: func(s *corev1.Service) { s.Spec.ExternalIPs = []string{"10.0.0.9", "198.51.100.7"} }, want: evidence.ExposureExternal},
		{name: "private external IP still cluster", svc: func(s *corev1.Service) { s.Spec.ExternalIPs = []string{"10.0.0.9"} }, want: evidence.ExposureInternal},
		{name: "cluster IP and nothing routes to it", objs: []client.Object{ingress("a", otherSvc, false), ingress("b", otherPort, true), ingress("c", nil, true)}, want: evidence.ExposureInternal},
		{name: "ingress rule without an address is unknown", objs: []client.Object{ingress("a", byNumber, false)}, want: ""},
		{name: "ingress with a private address is unknown", objs: []client.Object{withLB(ingress("a", byNumber, false), privateIng)}, want: ""},
		{name: "ingress default backend by name with a public address", objs: []client.Object{withLB(ingress("a", byName, true), privateIng, publicIng)}, want: evidence.ExposureExternal},
		{name: "public ingress makes a node port external", svc: nodePort, objs: []client.Object{withLB(ingress("a", byNumber, false), publicIng)}, want: evidence.ExposureExternal},
		{name: "public address on an ingress to another service", objs: []client.Object{withLB(ingress("a", otherSvc, false), publicIng)}, want: evidence.ExposureInternal},
		{name: "ingress list error", funcs: interceptor.Funcs{List: failList[*networkingv1.IngressList]()}, want: ""},
		{name: "no gateway api", funcs: routes(noMatch), want: evidence.ExposureInternal},
		{name: "httproute forbidden", funcs: routes(forbidden), want: ""},
		{name: "public ingress wins over a route error", objs: []client.Object{withLB(ingress("a", byNumber, false), publicIng)}, funcs: routes(forbidden), want: evidence.ExposureExternal},
		{name: "httproute with defaults is unknown", funcs: routes(nil, route(kNS, map[string]any{"name": "checkout", "port": int64(80)})), want: ""},
		{name: "httproute float port, explicit kind and namespace", funcs: routes(nil, route("gw", map[string]any{
			"kind": "Service", "name": "checkout", "namespace": kNS, "port": float64(80),
		})), want: ""},
		{name: "httproute without port", funcs: routes(nil, route(kNS, map[string]any{"name": "checkout"})), want: ""},
		{name: "httproute to something else", funcs: routes(nil, route(kNS,
			map[string]any{"name": "checkout", "port": int64(81)},
			map[string]any{"name": "checkout", "port": "bogus"},
			map[string]any{"group": "example.com", "name": "checkout"},
			map[string]any{"kind": "ServiceImport", "name": "checkout"},
			map[string]any{"name": "other"},
			map[string]any{"name": "checkout", "namespace": "elsewhere"},
		), route("elsewhere", map[string]any{"name": "checkout"})), want: evidence.ExposureInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: kNS}}
			if tt.svc != nil {
				tt.svc(svc)
			}
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(tt.objs...).WithInterceptorFuncs(tt.funcs).Build()
			if got := New(c).interfaceExposure(context.Background(), &httpRoutes{}, svc, port); got != tt.want {
				t.Fatalf("exposure = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInterfaceFacts_ProtocolAndExposure checks a present interface carries the probed protocol (only when
// probing is on, through the trusted Service DNS name) and its exposure.
func TestInterfaceFacts_ProtocolAndExposure(t *testing.T) {
	f := newKernelFixture()
	np := kService("checkout", "10.96.0.5", 80, 8080)
	np.Spec.Type = corev1.ServiceTypeLoadBalancer
	np.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}}
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(replace(f.objects(false), np)...).Build()
	fp := &fakeMetricsProber{protocol: evidence.ProtocolHTTP2}
	o := &Observer{client: c, prober: fp}

	in := f.input(false)
	set, _, _ := o.Collect(context.Background(), in)
	if _, p := iface(t, set); p.Protocol != "" || p.Exposure != evidence.ExposureExternal || fp.protocolAddr != "" {
		t.Fatalf("probing off: got %+v, probed %q", p, fp.protocolAddr)
	}

	in.EnableProbing = true
	set, _, _ = o.Collect(context.Background(), in)
	if _, p := iface(t, set); p.Protocol != evidence.ProtocolHTTP2 || fp.protocolAddr != "checkout.shop.svc:80" {
		t.Fatalf("probing on: got %+v, probed %q", p, fp.protocolAddr)
	}

	// An absent interface carries no facts: there is nothing serving to classify.
	in.ObservationWindows = map[string]*metav1.Time{"interface/api": kTime(time.Hour)}
	c = fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(dropSlice(replace(f.objects(false), np), "checkout-x")...).Build()
	fp.protocolAddr = ""
	set, _, _ = (&Observer{client: c, prober: fp}).Collect(context.Background(), in)
	if o, p := iface(t, set); o.Outcome != evidence.Observed || p.Present || p.Exposure != "" || p.Protocol != "" || fp.protocolAddr != "" {
		t.Fatalf("absent: got %s %+v, probed %q", o.Outcome, p, fp.protocolAddr)
	}
}

// TestInterfaceAvailability_PortNotTargetPort is the regression for spec 6.1: EndpointSlices list the
// target (container) port, so a Service with port 80 -> targetPort 8080 must read its ready endpoint.
func TestInterfaceAvailability_PortNotTargetPort(t *testing.T) {
	f := newKernelFixture()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(false)...).Build()
	set, updates, _ := New(c).Collect(context.Background(), f.input(false))
	if o, p := iface(t, set); o.Outcome != evidence.Observed || !p.Present {
		t.Fatalf("got %s present=%v, want the ready endpoint on targetPort 8080 to count", o.Outcome, p.Present)
	}
	if w, _ := windowFor(updates, "interface", "api"); w != nil {
		t.Fatal("a present interface must not open a window")
	}
}

func TestSliceCoversPort(t *testing.T) {
	named := corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromString("web")}
	unset := corev1.ServicePort{Port: 80}
	slice := func(name *string, port *int32) discoveryv1.EndpointSlice {
		return discoveryv1.EndpointSlice{Ports: []discoveryv1.EndpointPort{{Name: name, Port: port}}}
	}
	tests := []struct {
		name  string
		p     corev1.ServicePort
		slice discoveryv1.EndpointSlice
		want  bool
	}{
		{"named target matched by the service port name", named, slice(new("http"), new(int32(9999))), true},
		{"named target, unnamed slice port", named, slice(nil, new(int32(80))), false},
		{"named target, other name", named, slice(new("grpc"), new(int32(80))), false},
		{"unset target defaults to the port", unset, slice(nil, new(int32(80))), true},
		{"int target, slice port without number", unset, slice(nil, nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sliceCoversPort(tt.slice, tt.p); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSharedClaim(t *testing.T) {
	pvc := corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}
	deploy := func(replicas *int32, vol *corev1.Volume) *appsv1.Deployment {
		d := makeDeployment(kNS, "checkout", vol)
		d.Spec.Replicas = replicas
		return d
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: kNS},
		Spec: appsv1.StatefulSetSpec{
			Replicas:             new(int32(3)),
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data"}}},
		},
	}
	tests := []struct {
		name     string
		kind     string
		obj      client.Object
		scope    string
		durable  string
		want     *bool
		wantFind bool
	}{
		{"one claim, three replicas", workloadDeployment, deploy(new(int32(3)), &pvc), "local", "persistent", new(true), true},
		{"one claim, default single replica", workloadDeployment, deploy(nil, &pvc), "local", "persistent", new(false), true},
		{"per-replica claims", workloadStatefulSet, sts, "shared", "persistent", new(false), true},
		{"no claim, ephemeral state still observed", workloadDeployment, deploy(new(int32(2)), nil), "local", "ephemeral", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(tt.obj).Build()
			ctr := &contract.Contract{
				Service: contract.Service{Name: "checkout"},
				State:   &contract.State{Persistence: contract.Persistence{Durability: tt.durable, Scope: tt.scope}},
			}
			set, _, _ := New(c).Collect(context.Background(), CollectInput{
				Namespace: kNS, WorkloadName: "checkout", WorkloadKind: tt.kind, Contract: ctr, Now: kNow,
			})
			o := obsFor(t, set, evidence.PersistenceObserved, "checkout")
			p, err := o.GetPersistenceObservation()
			if err != nil {
				t.Fatal(err)
			}
			if (p.Shared == nil) != (tt.want == nil) || (p.Shared != nil && *p.Shared != *tt.want) {
				t.Fatalf("Shared = %v, want %v", p.Shared, tt.want)
			}
		})
	}
}

func TestSharedClaim_ReplicaSetAndJob(t *testing.T) {
	snap := &RuntimeSnapshot{}
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: kNS}, Spec: appsv1.ReplicaSetSpec{Replicas: new(int32(2))}},
	).Build()
	if err := New(c).observeWorkload(context.Background(), kNS, "rs", workloadReplicaSet, snap); err != nil || snap.Replicas == nil || *snap.Replicas != 2 {
		t.Fatalf("ReplicaSet replicas not read: %v %v", err, snap.Replicas)
	}
}

// The HTTPRoute read bypasses the cache, so one Collect lists the routes once however many interfaces it
// judges.
func TestInterfaceExposure_ListsRoutesOncePerCollect(t *testing.T) {
	f := newKernelFixture()
	in := f.input(false)
	in.Contract.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI}, {Name: "admin", Type: contract.InterfaceTypeOpenAPI}}
	in.InterfaceBindings = []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(80)}, {Interface: "admin", ServicePort: intstr.FromInt32(80)}}
	lists := 0
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(false)...).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			if u, ok := l.(*unstructured.UnstructuredList); ok && u.GroupVersionKind() == httpRouteGVK {
				lists++
			}
			return c.List(ctx, l, opts...)
		},
	}).Build()
	set, _, _ := New(c).Collect(context.Background(), in)
	for _, name := range []string{"api", "admin"} {
		o := obsFor(t, set, evidence.InterfaceObserved, name)
		if p, _ := o.GetInterfaceObservation(); p.Exposure != evidence.ExposureInternal {
			t.Fatalf("%s exposure = %q, want internal", name, p.Exposure)
		}
	}
	if lists != 1 {
		t.Fatalf("HTTPRoute lists = %d, want 1", lists)
	}
}
