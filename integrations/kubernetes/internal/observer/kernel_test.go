/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/nodeagent"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

const (
	kNS      = "shop"
	kAgentNS = "pacto-system"
	kNode    = "node-a"
	kPodUID  = "uid-checkout-1"
)

var kNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func kTime(ago time.Duration) *metav1.Time {
	t := metav1.NewTime(kNow.Add(-ago))
	return &t
}

func kernelScheme() *runtime.Scheme {
	s := newScheme()
	_ = pactoapi.AddToScheme(s)
	_ = discoveryv1.AddToScheme(s)
	_ = networkingv1.AddToScheme(s)
	_ = coordinationv1.AddToScheme(s)
	return s
}

// kernelFixture is one target (checkout: Service port 80 -> targetPort 8080, one ready pod on node-a), its
// declared provider (payments) and an undeclared Pacto-managed peer (ledger).
type kernelFixture struct {
	pod     *corev1.Pod
	node    *pactoapi.NodeRuntimeObservation
	extra   []client.Object
	windows map[string]*metav1.Time
	funcs   interceptor.Funcs
}

func newKernelFixture() *kernelFixture {
	return &kernelFixture{
		pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "checkout-1", Namespace: kNS, UID: kPodUID, Labels: map[string]string{"app": "checkout"}},
			Spec: corev1.PodSpec{NodeName: kNode, Containers: []corev1.Container{{
				Name: "app", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
			}}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				StartTime:  kTime(10 * time.Minute),
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		node: &pactoapi.NodeRuntimeObservation{
			ObjectMeta: metav1.ObjectMeta{Name: kNode, Namespace: kAgentNS, Labels: nodeagent.Labels()},
			Status: pactoapi.NodeRuntimeObservationStatus{
				NodeName:               kNode,
				PublishIntervalSeconds: 30,
				LastPublished:          kTime(10 * time.Second),
				Collection:             pactoapi.RuntimeCollection{State: pactoapi.CollectionRunning},
				Pods: []pactoapi.PodRuntimeObservation{{
					UID:        kPodUID,
					BaselineAt: kTime(5 * time.Minute),
					Listeners:  []pactoapi.RuntimeListener{{Port: 8080, Family: "IPv4", Address: "0.0.0.0"}},
				}},
			},
		},
	}
}

// connOK is a connection whose latest handshake, 5s ago, succeeded; connRefused one whose latest failed.
func connOK(addr string, port int32) pactoapi.RuntimeConnection {
	return pactoapi.RuntimeConnection{Address: addr, Port: port, Successes: 1, LastSuccessAt: kTime(5 * time.Second)}
}

func connRefused(addr string, port int32) pactoapi.RuntimeConnection {
	return pactoapi.RuntimeConnection{Address: addr, Port: port, Failures: 1, LastFailureAt: kTime(5 * time.Second)}
}

// entry is the target pod's kernel entry.
func (f *kernelFixture) entry() *pactoapi.PodRuntimeObservation { return &f.node.Status.Pods[0] }

func readySlice(svc, addr string, port int32, name string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: svc + "-x", Namespace: kNS, Labels: map[string]string{discoveryv1.LabelServiceName: svc}},
		Ports:      []discoveryv1.EndpointPort{{Name: new(name), Port: new(port)}},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{addr}, Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
		}},
	}
}

// podSlice makes every endpoint of the slice name the pod with the given UID, as the EndpointSlice
// controller does for a selector-backed Service.
func podSlice(s *discoveryv1.EndpointSlice, uid types.UID) *discoveryv1.EndpointSlice {
	for i := range s.Endpoints {
		s.Endpoints[i].TargetRef = &corev1.ObjectReference{Kind: "Pod", Namespace: kNS, UID: uid}
	}
	return s
}

func kService(name, clusterIP string, port, targetPort int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: kNS},
		Spec: corev1.ServiceSpec{
			Selector:  map[string]string{"app": name},
			ClusterIP: clusterIP,
			Ports:     []corev1.ServicePort{{Name: "http", Port: port, TargetPort: intstr.FromInt32(targetPort)}},
		},
	}
}

func kPacto(name, svc, contractSvc, version, ref string) *pactoapi.Pacto {
	return &pactoapi.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: kNS},
		Spec:       pactoapi.PactoSpec{Target: pactoapi.TargetRef{ServiceName: svc}},
		Status: pactoapi.PactoStatus{Contract: &pactoapi.ContractInfo{
			ServiceName: contractSvc, Version: version, ResolvedRef: ref,
		}},
	}
}

func (f *kernelFixture) objects(withKernel bool) []client.Object {
	objs := []client.Object{
		makeDeployment(kNS, "checkout", nil),
		kService("checkout", "10.96.0.5", 80, 8080),
		podSlice(readySlice("checkout", "10.0.0.5", 8080, "http"), kPodUID),
		kPacto("checkout", "checkout", "checkout", "1.0.0", "registry/checkout:1.0.0"),
		kPacto("payments", "payments", "payments", "1.2.0", "registry/payments:1.2.0"),
		kService("payments", "10.96.0.10", 80, 8080),
		readySlice("payments", "10.0.1.7", 8080, "http"),
		kPacto("ledger", "ledger", "ledger", "3.0.0", "registry/ledger:3.0.0"),
		kService("ledger", "10.96.0.20", 5432, 5432),
	}
	if withKernel {
		objs = append(objs, f.pod)
		if f.node != nil {
			objs = append(objs, f.node)
		}
	}
	return append(objs, f.extra...)
}

func kContract() *contract.Contract {
	return &contract.Contract{
		Service:    contract.Service{Name: "checkout"},
		Interfaces: []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI}},
		Dependencies: []contract.Dependency{{
			Name: "payments", Ref: "oci://registry/payments", Required: true, Compatibility: "^1.0.0",
		}},
	}
}

func (f *kernelFixture) input(kernel bool) CollectInput {
	return CollectInput{
		Namespace:               kNS,
		ServiceName:             "checkout",
		ContractRef:             "registry/checkout:1.0.0",
		WorkloadName:            "checkout",
		WorkloadKind:            workloadDeployment,
		Contract:                kContract(),
		InterfaceBindings:       []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(80)}},
		StabilizationWindow:     2 * time.Minute,
		ObservationWindows:      f.windows,
		Now:                     kNow,
		KernelEvidence:          kernel,
		KernelEvidenceNamespace: kAgentNS,
	}
}

func (f *kernelFixture) collect(t *testing.T) (evidence.EvidenceSet, []ObservationWindowUpdate) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).WithInterceptorFuncs(f.funcs).Build()
	set, updates, _ := New(c).Collect(context.Background(), f.input(true))
	if err := evidence.ValidateEvidenceSet(set); err != nil {
		t.Fatalf("merged evidence set is invalid: %v", err)
	}
	return set, updates
}

func obsFor(t *testing.T, set evidence.EvidenceSet, kind evidence.ObservationKind, name string) evidence.Observation {
	t.Helper()
	for _, o := range set.Observations {
		if o.Kind == kind && o.Subject.Name == name {
			return o
		}
	}
	t.Fatalf("no %s observation for %q", kind, name)
	return evidence.Observation{}
}

func iface(t *testing.T, set evidence.EvidenceSet) (evidence.Observation, evidence.InterfaceObservation) {
	t.Helper()
	o := obsFor(t, set, evidence.InterfaceObserved, "api")
	p, _ := o.GetInterfaceObservation()
	return o, p
}

func depObs(t *testing.T, set evidence.EvidenceSet, name string) (evidence.Observation, evidence.DependencyObservation) {
	t.Helper()
	o := obsFor(t, set, evidence.DependencyReachable, name)
	p, _ := o.GetDependencyObservation()
	return o, p
}

func windowFor(updates []ObservationWindowUpdate, kind, subject string) (*metav1.Time, bool) {
	for _, u := range updates {
		if u.Kind == kind && u.Subject == subject {
			return u.FirstObservedNegativeAt, true
		}
	}
	return nil, false
}

// TestKernelEvidence_DisabledIsIdentical proves a cluster without kernel evidence reads exactly as before:
// the same objects plus a contradicting node object and pod change nothing while the flag is off.
func TestKernelEvidence_DisabledIsIdentical(t *testing.T) {
	f := newKernelFixture()
	f.entry().Listeners = nil // the kernel would contradict the endpoints if it were consulted
	f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.96.0.20", 5432)}

	with := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).Build()
	without := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(false)...).Build()

	gotSet, gotUpdates, _ := New(with).Collect(context.Background(), f.input(false))
	wantSet, wantUpdates, _ := New(without).Collect(context.Background(), f.input(false))
	if !reflect.DeepEqual(gotSet, wantSet) || !reflect.DeepEqual(gotUpdates, wantUpdates) {
		t.Fatalf("kernel evidence off must not change the output:\n got %+v\nwant %+v", gotSet, wantSet)
	}
	for _, o := range gotSet.Observations {
		if o.Provenance.Collector != collectorName {
			t.Errorf("%s/%s collector = %q, want %q", o.Kind, o.Subject.Name, o.Provenance.Collector, collectorName)
		}
	}
}

func TestKernelMerge_ListenerConfirmsInterface(t *testing.T) {
	f := newKernelFixture()
	set, _ := f.collect(t)
	o, p := iface(t, set)
	if o.Outcome != evidence.Observed || !p.Present || o.Provenance.Collector != collectorKernelMerged {
		t.Fatalf("got %s present=%v collector=%q, want Observed present by %s", o.Outcome, p.Present, o.Provenance.Collector, collectorKernelMerged)
	}
}

// TestKernelMerge_ListenerAbsent is the contradiction only the kernel sees: endpoints say ready, the fully
// observed pod listens on nothing (or only on loopback), so the interface goes absent through the window.
func TestKernelMerge_ListenerAbsent(t *testing.T) {
	for name, listeners := range map[string][]pactoapi.RuntimeListener{
		"no listener":   nil,
		"other port":    {{Port: 9090, Family: "IPv4"}},
		"loopback only": {{Port: 8080, Family: "IPv4", Address: "127.0.0.1"}, {Port: 8080, Family: "IPv6", Address: "::1"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			f.entry().Listeners = listeners
			set, updates := f.collect(t)
			o, _ := iface(t, set)
			w, _ := windowFor(updates, "interface", "api")
			if o.Outcome != evidence.Insufficient || w == nil {
				t.Fatalf("first negative: got %s window=%v, want Insufficient with an open window", o.Outcome, w)
			}

			f.windows = map[string]*metav1.Time{"interface/api": kTime(3 * time.Minute)}
			set, _ = f.collect(t)
			o, p := iface(t, set)
			if o.Outcome != evidence.Observed || p.Present || o.Provenance.Collector != collectorKernelMerged {
				t.Fatalf("beyond window: got %s present=%v collector=%q, want Observed absent", o.Outcome, p.Present, o.Provenance.Collector)
			}
		})
	}
}

func TestKernelMerge_InterfaceCoverage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f *kernelFixture)
		want   evidence.Outcome
	}{
		{"stale heartbeat", func(f *kernelFixture) { f.node.Status.LastPublished = kTime(5 * time.Minute) }, evidence.Stale},
		{"never published", func(f *kernelFixture) { f.node.Status.LastPublished = nil }, evidence.Stale},
		{"no node object", func(f *kernelFixture) { f.node = nil }, evidence.Stale},
		{"collection failed", func(f *kernelFixture) { f.node.Status.Collection.State = pactoapi.CollectionFailed }, evidence.Failed},
		{"degraded", func(f *kernelFixture) {
			f.node.Status.Collection.State = pactoapi.CollectionDegraded
			f.entry().Listeners = nil
		}, evidence.Insufficient},
		{"shared netns", func(f *kernelFixture) { f.entry().NetnsShared = true }, evidence.Insufficient},
		{"truncated", func(f *kernelFixture) { f.entry().Truncated = true; f.entry().Listeners = nil }, evidence.Insufficient},
		{"no baseline", func(f *kernelFixture) { f.entry().BaselineAt = nil; f.entry().Listeners = nil }, evidence.Insufficient},
		{"baseline before pod start", func(f *kernelFixture) {
			f.entry().BaselineAt = kTime(20 * time.Minute)
			f.entry().Listeners = nil
		}, evidence.Insufficient},
		{"drop since baseline", func(f *kernelFixture) {
			f.node.Status.Collection.LastDropAt = kTime(time.Minute)
			f.entry().Listeners = nil
		}, evidence.Insufficient},
		{"pod not mapped yet", func(f *kernelFixture) { f.entry().UID = "someone-else" }, evidence.Insufficient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newKernelFixture()
			tt.mutate(f)
			set, updates := f.collect(t)
			o, _ := iface(t, set)
			if o.Outcome != tt.want {
				t.Fatalf("got %s, want %s", o.Outcome, tt.want)
			}
			if w, ok := windowFor(updates, "interface", "api"); !ok || w != nil {
				t.Errorf("an inconclusive kernel reading must reset the window, got ok=%v window=%v", ok, w)
			}
		})
	}
}

func TestKernelMerge_InterfaceMisc(t *testing.T) {
	t.Run("degraded node with a listener still confirms", func(t *testing.T) {
		f := newKernelFixture()
		f.node.Status.Collection.State = pactoapi.CollectionDegraded
		set, _ := f.collect(t)
		if o, p := iface(t, set); o.Outcome != evidence.Observed || !p.Present {
			t.Fatalf("got %s present=%v", o.Outcome, p.Present)
		}
	})
	t.Run("drop before baseline is healed by the resync", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Listeners = nil
		f.node.Status.Collection.LastDropAt = kTime(8 * time.Minute)
		set, _ := f.collect(t)
		if o, _ := iface(t, set); o.Outcome != evidence.Insufficient {
			t.Fatalf("got %s, want the windowed negative", o.Outcome)
		}
	})
	t.Run("no ready pod leaves the kubernetes result", func(t *testing.T) {
		f := newKernelFixture()
		f.pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
		set, _ := f.collect(t)
		if o, p := iface(t, set); o.Outcome != evidence.Observed || !p.Present || o.Provenance.Collector != collectorName {
			t.Fatalf("got %s present=%v collector=%q", o.Outcome, p.Present, o.Provenance.Collector)
		}
	})
	t.Run("pod without a ready condition is not ready", func(t *testing.T) {
		if podReady(&corev1.Pod{}) {
			t.Fatal("a pod without conditions must not be ready")
		}
	})
	t.Run("named target port resolved per pod", func(t *testing.T) {
		f := newKernelFixture()
		svc := kService("checkout", "10.96.0.5", 80, 0)
		svc.Spec.Ports[0].TargetPort = intstr.FromString("http")
		f.extra = nil
		c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(replace(f.objects(true), svc)...).Build()
		set, _, _ := New(c).Collect(context.Background(), f.input(true))
		if o, p := iface(t, set); o.Outcome != evidence.Observed || !p.Present || o.Provenance.Collector != collectorKernelMerged {
			t.Fatalf("got %s present=%v collector=%q", o.Outcome, p.Present, o.Provenance.Collector)
		}

		f.pod.Spec.Containers[0].Ports[0].Name = "other"
		c = fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(replace(f.objects(true), svc)...).Build()
		set, _, _ = New(c).Collect(context.Background(), f.input(true))
		if o, _ := iface(t, set); o.Outcome != evidence.Insufficient {
			t.Fatalf("unresolvable named port: got %s, want Insufficient", o.Outcome)
		}
	})
	t.Run("node object without status.nodeName is keyed by its name", func(t *testing.T) {
		f := newKernelFixture()
		f.node.Status.NodeName = ""
		set, _ := f.collect(t)
		if o, _ := iface(t, set); o.Provenance.Collector != collectorKernelMerged || o.Outcome != evidence.Observed {
			t.Fatalf("got %s collector=%q", o.Outcome, o.Provenance.Collector)
		}
	})
}

// TestKernelMerge_InterfaceJudgesEndpointPods: the verdict is about the pods the Service routes the port to,
// read from the ready endpoints, not the workload's pods.
func TestKernelMerge_InterfaceJudgesEndpointPods(t *testing.T) {
	// canary is a pod the Service selects but the Deployment does not, on the same node.
	canary := func(f *kernelFixture, listening bool) {
		pod := f.pod.DeepCopy()
		pod.Name, pod.UID, pod.Labels = "checkout-canary", "uid-canary", map[string]string{"app": "checkout", "track": "canary"}
		entry := pactoapi.PodRuntimeObservation{UID: "uid-canary", BaselineAt: kTime(5 * time.Minute)}
		if listening {
			entry.Listeners = []pactoapi.RuntimeListener{{Port: 8080, Family: "IPv4", Address: "0.0.0.0"}}
		}
		f.node.Status.Pods = append(f.node.Status.Pods, entry)
		f.extra = append(f.extra, pod)
	}
	slice := func(f *kernelFixture, s *discoveryv1.EndpointSlice) {
		f.extra = append(f.extra, s)
	}
	collect := func(t *testing.T, f *kernelFixture) (evidence.Observation, evidence.InterfaceObservation) {
		t.Helper()
		objs := f.objects(true)
		for _, x := range f.extra {
			objs = replace(objs, x)
		}
		c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(objs...).WithInterceptorFuncs(f.funcs).Build()
		set, _, _ := New(c).Collect(context.Background(), f.input(true))
		return iface(t, set)
	}
	t.Run("a listening canary behind the endpoints satisfies the port", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Listeners = nil // the workload's own pod is deaf, but receives no traffic for the port
		canary(f, true)
		slice(f, podSlice(readySlice("checkout", "10.0.0.6", 8080, "http"), "uid-canary"))
		if o, p := collect(t, f); o.Outcome != evidence.Observed || !p.Present || o.Provenance.Collector != collectorKernelMerged {
			t.Fatalf("got %s present=%v collector=%q, want Observed present", o.Outcome, p.Present, o.Provenance.Collector)
		}
	})
	t.Run("a deaf canary behind the endpoints is the windowed negative", func(t *testing.T) {
		f := newKernelFixture()
		canary(f, false)
		slice(f, podSlice(readySlice("checkout", "10.0.0.6", 8080, "http"), "uid-canary"))
		f.windows = map[string]*metav1.Time{"interface/api": kTime(3 * time.Minute)}
		if o, p := collect(t, f); o.Outcome != evidence.Observed || p.Present {
			t.Fatalf("got %s present=%v, want Observed absent", o.Outcome, p.Present)
		}
	})
	t.Run("endpoints naming no pod fall back to the workload, never negative", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Listeners = nil
		slice(f, readySlice("checkout", "10.0.0.5", 8080, "http"))
		f.windows = map[string]*metav1.Time{"interface/api": kTime(3 * time.Minute)}
		if o, _ := collect(t, f); o.Outcome != evidence.Insufficient {
			t.Fatalf("got %s, want Insufficient", o.Outcome)
		}
		f.entry().Listeners = []pactoapi.RuntimeListener{{Port: 8080, Family: "IPv4"}}
		if o, p := collect(t, f); o.Outcome != evidence.Observed || !p.Present {
			t.Fatalf("listening workload pod: got %s present=%v", o.Outcome, p.Present)
		}
	})
	t.Run("an endpoint naming no pod beside a deaf one is not negative", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Listeners = nil
		s := podSlice(readySlice("checkout", "10.0.0.5", 8080, "http"), kPodUID)
		s.Endpoints = append(s.Endpoints, discoveryv1.Endpoint{Addresses: []string{"10.0.0.9"}, Conditions: discoveryv1.EndpointConditions{Ready: new(true)}})
		slice(f, s)
		f.windows = map[string]*metav1.Time{"interface/api": kTime(3 * time.Minute)}
		if o, _ := collect(t, f); o.Outcome != evidence.Insufficient {
			t.Fatalf("got %s, want Insufficient", o.Outcome)
		}
	})
	t.Run("an endpoint pod not running here leaves the kubernetes result", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Listeners = nil // the workload's pod is deaf, but it is not behind the endpoints
		slice(f, podSlice(readySlice("checkout", "10.0.0.6", 8080, "http"), "uid-gone"))
		if o, p := collect(t, f); o.Outcome != evidence.Observed || !p.Present || o.Provenance.Collector != collectorName {
			t.Fatalf("got %s present=%v collector=%q, want the Kubernetes positive", o.Outcome, p.Present, o.Provenance.Collector)
		}
	})
	t.Run("one endpoint pod missing beside a deaf one is not negative", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Listeners = nil
		s := podSlice(readySlice("checkout", "10.0.0.5", 8080, "http"), kPodUID)
		s.Endpoints = append(s.Endpoints, discoveryv1.Endpoint{Addresses: []string{"10.0.0.6"}, Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
			TargetRef: &corev1.ObjectReference{Kind: "Pod", UID: "uid-gone"}})
		slice(f, s)
		f.windows = map[string]*metav1.Time{"interface/api": kTime(3 * time.Minute)}
		if o, _ := collect(t, f); o.Outcome != evidence.Insufficient {
			t.Fatalf("got %s, want Insufficient", o.Outcome)
		}
	})
	t.Run("a pod list error is a collection failure", func(t *testing.T) {
		f := newKernelFixture()
		lists := 0
		f.funcs = interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			if _, ok := l.(*corev1.PodList); ok {
				if lists++; lists > 1 { // the first is the workload's pods, the second the endpoint pods
					return errors.New("boom")
				}
			}
			return c.List(ctx, l, opts...)
		}}
		if o, _ := collect(t, f); o.Outcome != evidence.Failed {
			t.Fatalf("got %s, want Failed", o.Outcome)
		}
	})
}

// replace swaps the object with the same name and type in objs.
func replace(objs []client.Object, o client.Object) []client.Object {
	out := make([]client.Object, 0, len(objs))
	for _, x := range objs {
		if reflect.TypeOf(x) == reflect.TypeOf(o) && x.GetName() == o.GetName() && x.GetNamespace() == o.GetNamespace() {
			continue
		}
		out = append(out, x)
	}
	return append(out, o)
}

func TestKernelMerge_Dependency(t *testing.T) {
	t.Run("connect ok to the cluster IP confirms reachability and reports the version", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.96.0.10", 80)}
		set, _ := f.collect(t)
		o, p := depObs(t, set, "payments")
		if o.Outcome != evidence.Observed || !p.Reachable || p.Version != "1.2.0" || o.Provenance.Collector != collectorKernelMerged {
			t.Fatalf("got %s reachable=%v version=%q collector=%q", o.Outcome, p.Reachable, p.Version, o.Provenance.Collector)
		}
	})
	t.Run("refused at the endpoint is a windowed negative", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Connections = []pactoapi.RuntimeConnection{connRefused("::ffff:10.0.1.7", 8080)}
		set, updates := f.collect(t)
		if o, _ := depObs(t, set, "payments"); o.Outcome != evidence.Insufficient {
			t.Fatalf("first negative: got %s", o.Outcome)
		}
		if w, _ := windowFor(updates, "dependency", "payments"); w == nil {
			t.Fatal("the negative must open the dependency window")
		}
		f.windows = map[string]*metav1.Time{"dependency/payments": kTime(3 * time.Minute)}
		set, _ = f.collect(t)
		o, p := depObs(t, set, "payments")
		if o.Outcome != evidence.Observed || p.Reachable || o.Provenance.Collector != collectorKernelMerged {
			t.Fatalf("beyond window: got %s reachable=%v collector=%q", o.Outcome, p.Reachable, o.Provenance.Collector)
		}
	})
	t.Run("silence leaves the kubernetes result", func(t *testing.T) {
		f := newKernelFixture()
		f.entry().Connections = []pactoapi.RuntimeConnection{connRefused("192.0.2.1", 443)}
		set, _ := f.collect(t)
		if o, p := depObs(t, set, "payments"); o.Outcome != evidence.Observed || !p.Reachable || o.Provenance.Collector != collectorName {
			t.Fatalf("got %s reachable=%v collector=%q", o.Outcome, p.Reachable, o.Provenance.Collector)
		}
	})
	t.Run("stale node keeps a conclusive kubernetes result", func(t *testing.T) {
		f := newKernelFixture()
		f.node.Status.LastPublished = kTime(time.Hour)
		set, _ := f.collect(t)
		if o, _ := depObs(t, set, "payments"); o.Outcome != evidence.Observed || o.Provenance.Collector != collectorName {
			t.Fatalf("got %s collector=%q", o.Outcome, o.Provenance.Collector)
		}
	})
	for name, mutate := range map[string]func(*kernelFixture){
		"stale":  func(f *kernelFixture) { f.node.Status.LastPublished = kTime(time.Hour) },
		"failed": func(f *kernelFixture) { f.node.Status.Collection.State = pactoapi.CollectionFailed },
	} {
		t.Run(name+" node replaces an inconclusive kubernetes result", func(t *testing.T) {
			f := newKernelFixture()
			mutate(f)
			// payments has no ready endpoint: the Kubernetes result is a first (windowed) negative.
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(dropSlice(f.objects(true), "payments-x")...).Build()
			set, _, _ := New(c).Collect(context.Background(), f.input(true))
			want := map[string]evidence.Outcome{"stale": evidence.Stale, "failed": evidence.Failed}[name]
			if o, _ := depObs(t, set, "payments"); o.Outcome != want || o.Provenance.Collector != collectorKernelMerged {
				t.Fatalf("got %s collector=%q, want %s", o.Outcome, o.Provenance.Collector, want)
			}
		})
	}
}

func dropSlice(objs []client.Object, name string) []client.Object {
	out := objs[:0]
	for _, o := range objs {
		if _, ok := o.(*discoveryv1.EndpointSlice); ok && o.GetName() == name {
			continue
		}
		out = append(out, o)
	}
	return out
}

func TestKernelMerge_UndeclaredPeer(t *testing.T) {
	f := newKernelFixture()
	f.entry().Connections = []pactoapi.RuntimeConnection{
		connOK("10.96.0.20", 5432),    // ledger: undeclared
		connOK("10.96.0.10", 80),      // payments: declared
		connOK("10.96.0.5", 80),       // checkout itself
		connRefused("10.96.0.21", 80), // a failure proves nothing
		connOK("203.0.113.9", 443),    // not Pacto-managed
	}
	set, _ := f.collect(t)
	o, p := depObs(t, set, "ledger")
	if !p.Reachable || !p.Undeclared || o.Provenance.Collector != collectorKernel {
		t.Fatalf("got %+v collector=%q, want an undeclared reachable peer by %s", p, o.Provenance.Collector, collectorKernel)
	}
	var undeclared int
	for _, o := range set.Observations {
		if p, _ := o.GetDependencyObservation(); p.Undeclared {
			undeclared++
		}
	}
	if undeclared != 1 {
		t.Fatalf("got %d undeclared peers, want only ledger", undeclared)
	}
}

// TestKernelMerge_UndeclaredKeptWhenDependencyResolvesByName checks a non-OCI dependency ref does not
// suppress the report when the dependency's name resolves it to a sibling.
func TestKernelMerge_UndeclaredKeptWhenDependencyResolvesByName(t *testing.T) {
	f := newKernelFixture()
	f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.96.0.20", 5432)}
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).Build()
	in := f.input(true)
	in.Contract.Dependencies[0].Ref = "../payments"
	set, _, _ := New(c).Collect(context.Background(), in)
	if _, p := depObs(t, set, "ledger"); !p.Undeclared {
		t.Fatalf("got %+v, want ledger reported undeclared", p)
	}
}

func TestKernelMerge_UndeclaredExclusions(t *testing.T) {
	conn := []pactoapi.RuntimeConnection{connOK("10.96.0.20", 5432)}
	tests := []struct {
		name  string
		ledgr *pactoapi.Pacto
		funcs interceptor.Funcs
		input func(*CollectInput)
	}{
		{name: "name equals a declared dependency", ledgr: kPacto("ledger", "ledger", "payments", "3.0.0", "registry/other:1")},
		{name: "a declared dependency resolves to it", input: func(in *CollectInput) {
			in.Contract.Dependencies = append(in.Contract.Dependencies, contract.Dependency{Name: "books", Ref: "oci://registry/ledger"})
		}},
		// A declared dependency no sibling resolves to may be the reached peer under another name.
		{name: "a local dependency ref resolves to no sibling", input: func(in *CollectInput) {
			in.Contract.Dependencies = append(in.Contract.Dependencies, contract.Dependency{Name: "books", Ref: "../books"})
		}},
		{name: "an OCI dependency ref resolves to no sibling", input: func(in *CollectInput) {
			in.Contract.Dependencies = append(in.Contract.Dependencies, contract.Dependency{Name: "books", Ref: "oci://registry/elsewhere"})
		}},
		{name: "same contract service as the target", ledgr: kPacto("ledger", "ledger", "checkout", "3.0.0", "registry/ledger:3")},
		{name: "no contract status", ledgr: &pactoapi.Pacto{
			ObjectMeta: metav1.ObjectMeta{Name: "ledger", Namespace: kNS},
			Spec:       pactoapi.PactoSpec{Target: pactoapi.TargetRef{ServiceName: "ledger"}},
		}},
		{name: "reference-only", ledgr: kPacto("ledger", "", "ledger", "3.0.0", "registry/ledger:3")},
		{name: "service missing", ledgr: kPacto("ledger", "ledger-gone", "ledger", "3.0.0", "registry/ledger:3")},
		{name: "pacto list error", funcs: interceptor.Funcs{List: failList[*pactoapi.PactoList]()}},
		{name: "slice list error", funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			lo := (&client.ListOptions{}).ApplyOptions(opts)
			if _, ok := l.(*discoveryv1.EndpointSliceList); ok && lo.LabelSelector != nil && strings.Contains(lo.LabelSelector.String(), "ledger") {
				return errors.New("boom")
			}
			return c.List(ctx, l, opts...)
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newKernelFixture()
			f.entry().Connections = conn
			objs := f.objects(true)
			if tt.ledgr != nil {
				objs = replace(objs, tt.ledgr)
			}
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(objs...).WithInterceptorFuncs(tt.funcs).Build()
			in := f.input(true)
			if tt.input != nil {
				tt.input(&in)
			}
			set, _, _ := New(c).Collect(context.Background(), in)
			for _, o := range set.Observations {
				if p, _ := o.GetDependencyObservation(); p.Undeclared {
					t.Fatalf("unexpected undeclared peer %q", o.Subject.Name)
				}
			}
		})
	}
}

// failList fails every List of the given list type and passes the rest through.
func failList[T client.ObjectList]() func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
	return func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, ok := l.(T); ok {
			return errors.New("boom")
		}
		return c.List(ctx, l, opts...)
	}
}

func TestKernelMerge_LoadFailures(t *testing.T) {
	for name, funcs := range map[string]interceptor.Funcs{
		"pod list":  {List: failList[*corev1.PodList]()},
		"node list": {List: failList[*pactoapi.NodeRuntimeObservationList]()},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			f.funcs = funcs
			f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.96.0.20", 5432)}
			set, _ := f.collect(t)
			if o, _ := iface(t, set); o.Outcome != evidence.Failed {
				t.Errorf("interface: got %s, want Failed", o.Outcome)
			}
			// A conclusive Kubernetes dependency result stands; no undeclared peer is invented.
			if o, _ := depObs(t, set, "payments"); o.Outcome != evidence.Observed {
				t.Errorf("dependency: got %s, want Observed", o.Outcome)
			}
			for _, o := range set.Observations {
				if p, _ := o.GetDependencyObservation(); p.Undeclared {
					t.Errorf("unexpected undeclared peer %q", o.Subject.Name)
				}
			}
		})
	}
	t.Run("dependency verdict on a failed view", func(t *testing.T) {
		if v := (&kernelView{failed: true}).dependencyVerdict(nil); v != runtimefacts.Failed {
			t.Fatalf("got %v", v)
		}
	})
}

func TestKernelMerge_NoWorkloadIsOff(t *testing.T) {
	f := newKernelFixture()
	in := f.input(true)
	in.WorkloadName = ""
	if kv := New(fake.NewClientBuilder().Build()).loadKernelView(context.Background(), in, kNow); kv != nil {
		t.Fatal("a target without a workload has no pods to attribute")
	}
}

func TestServicePeers(t *testing.T) {
	svc := &corev1.Service{Spec: corev1.ServiceSpec{
		ClusterIPs: []string{"10.96.0.1", "fd00::1"},
		Ports:      []corev1.ServicePort{{Port: 80}},
	}}
	headless := &corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: corev1.ClusterIPNone, Ports: []corev1.ServicePort{{Port: 80}}}}
	single := &corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: "10.96.0.2", Ports: []corev1.ServicePort{{Port: 80}}}}
	slices := []discoveryv1.EndpointSlice{{
		Ports:     []discoveryv1.EndpointPort{{Port: nil}, {Port: new(int32(8080))}},
		Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.9"}}},
	}}

	got := map[string]bool{}
	for _, a := range servicePeers(svc, slices) {
		got[a.String()] = true
	}
	for _, k := range []string{"10.96.0.1:80", "[fd00::1]:80", "10.0.0.9:8080"} {
		if !got[k] {
			t.Errorf("missing peer %s in %v", k, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %v, want 3 peers", got)
	}
	if got := servicePeers(headless, nil); len(got) != 0 {
		t.Errorf("headless: got %v", got)
	}
	if got := servicePeers(single, nil); len(got) != 1 || got[0].String() != "10.96.0.2:80" {
		t.Errorf("single cluster IP: got %v", got)
	}
}

func TestWorkloadPodSelector(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}}
	owned := func(name, cron string) *batchv1.Job {
		return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: kNS, OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "batch/v1", Kind: workloadCronJob, Name: cron, UID: types.UID(cron), Controller: new(true),
		}}}}
	}
	tests := []struct {
		name    string
		kind    string
		objs    []client.Object
		funcs   interceptor.Funcs
		want    string // selector string; "" means none
		wantErr bool
	}{
		{name: "deployment", kind: workloadDeployment, objs: []client.Object{&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}, Spec: appsv1.DeploymentSpec{Selector: sel}}}, want: "app=x"},
		{name: "statefulset", kind: workloadStatefulSet, objs: []client.Object{&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}, Spec: appsv1.StatefulSetSpec{Selector: sel}}}, want: "app=x"},
		{name: "replicaset", kind: workloadReplicaSet, objs: []client.Object{&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}, Spec: appsv1.ReplicaSetSpec{Selector: sel}}}, want: "app=x"},
		{name: "job with selector", kind: workloadJob, objs: []client.Object{&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}, Spec: batchv1.JobSpec{Selector: sel}}}, want: "app=x"},
		{name: "job without selector", kind: workloadJob, objs: []client.Object{&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}}}, want: labelJobName + "=w"},
		{name: "cronjob", kind: workloadCronJob, objs: []client.Object{owned("w-1", "w"), owned("other-1", "other"), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "loose", Namespace: kNS}}}, want: labelJobName + " in (w-1)"},
		{name: "cronjob without jobs", kind: workloadCronJob},
		{name: "cronjob job list error", kind: workloadCronJob, funcs: interceptor.Funcs{List: failList[*batchv1.JobList]()}, wantErr: true},
		{name: "cronjob unlabelable job name", kind: workloadCronJob, objs: []client.Object{owned(strings.Repeat("j", 70), "w")}, wantErr: true},
		{name: "not found", kind: workloadDeployment},
		{name: "get error", kind: workloadDeployment, funcs: interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("boom")
		}}, wantErr: true},
		{name: "nil selector", kind: workloadDeployment, objs: []client.Object{&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}}}},
		{name: "empty selector selects nothing", kind: workloadDeployment, objs: []client.Object{&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}, Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{}}}}},
		{name: "invalid selector", kind: workloadDeployment, objs: []client.Object{&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: kNS}, Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "a", Operator: "Bogus"}},
		}}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(tt.objs...).WithInterceptorFuncs(tt.funcs).Build()
			got, err := New(c).workloadPodSelector(context.Background(), CollectInput{Namespace: kNS, WorkloadName: "w", WorkloadKind: tt.kind})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			gotStr := ""
			if got != nil {
				gotStr = got.String()
			}
			if gotStr != tt.want {
				t.Fatalf("selector = %q, want %q", gotStr, tt.want)
			}
		})
	}
}

func TestTargetPods(t *testing.T) {
	pod := func(name string, phase corev1.PodPhase, node string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: kNS, Labels: map[string]string{"app": "checkout"}},
			Spec:       corev1.PodSpec{NodeName: node},
			Status:     corev1.PodStatus{Phase: phase},
		}
	}
	objs := []client.Object{
		makeDeployment(kNS, "checkout", nil),
		pod("running", corev1.PodRunning, kNode),
		pod("pending", corev1.PodPending, ""),
		pod("done", corev1.PodSucceeded, kNode),
	}
	leaving := pod("leaving", corev1.PodRunning, kNode)
	leaving.DeletionTimestamp, leaving.Finalizers = &metav1.Time{Time: time.Now()}, []string{"test/hold"}
	objs = append(objs, leaving)
	in := CollectInput{Namespace: kNS, WorkloadName: "checkout", WorkloadKind: workloadDeployment}

	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(objs...).Build()
	pods, err := New(c).targetPods(context.Background(), in)
	if err != nil || len(pods) != 1 || pods[0].Name != "running" {
		t.Fatalf("got %v, %v; want only the running pod, not the one shutting down", pods, err)
	}

	c = fake.NewClientBuilder().WithScheme(kernelScheme()).Build()
	if pods, err := New(c).targetPods(context.Background(), in); err != nil || pods != nil {
		t.Fatalf("missing workload: got %v, %v", pods, err)
	}
}

// TestKernelMerge_FreshnessFollowsTheStalenessBound checks the connection freshness bound is the node's
// staleness bound: three publish intervals by default, the flag when set.
func TestKernelMerge_FreshnessFollowsTheStalenessBound(t *testing.T) {
	f := newKernelFixture()
	f.entry().Connections = []pactoapi.RuntimeConnection{{Address: "10.0.1.7", Port: 8080, Failures: 1, LastFailureAt: kTime(2 * time.Minute)}}
	set, _ := f.collect(t)
	if o, p := depObs(t, set, "payments"); o.Outcome != evidence.Observed || !p.Reachable || o.Provenance.Collector != collectorName {
		t.Fatalf("a failure older than 3x30s must be silent: got %s reachable=%v collector=%q", o.Outcome, p.Reachable, o.Provenance.Collector)
	}
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).Build()
	in := f.input(true)
	in.KernelEvidenceStaleness = 5 * time.Minute
	set, _, _ = New(c).Collect(context.Background(), in)
	if o, _ := depObs(t, set, "payments"); o.Outcome != evidence.Insufficient || o.Provenance.Collector != collectorKernelMerged {
		t.Fatalf("within the configured bound the failure is a windowed negative: got %s collector=%q", o.Outcome, o.Provenance.Collector)
	}
}

// TestKernelMerge_PositiveNeverOverridesKubernetesNegative is the regression for a kernel success masking
// a provider with no ready endpoint: the two contradict, so the result is Insufficient, before and after
// the Kubernetes window matures, and the window keeps running.
func TestKernelMerge_PositiveNeverOverridesKubernetesNegative(t *testing.T) {
	for name, windows := range map[string]map[string]*metav1.Time{
		"first negative":    nil,
		"beyond the window": {"dependency/payments": kTime(time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			f.windows = windows
			f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.96.0.10", 80)}
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(dropSlice(f.objects(true), "payments-x")...).Build()
			set, updates, _ := New(c).Collect(context.Background(), f.input(true))
			o, _ := depObs(t, set, "payments")
			if o.Outcome != evidence.Insufficient || o.Provenance.Collector != collectorKernelMerged {
				t.Fatalf("got %s collector=%q, want Insufficient", o.Outcome, o.Provenance.Collector)
			}
			if w, _ := windowFor(updates, "dependency", "payments"); w == nil {
				t.Fatal("the Kubernetes negative must keep its window open")
			}
		})
	}
}

// TestKernelMerge_UndeclaredNeedsAFreshSuccess checks an undeclared peer follows the same timestamp rule:
// a stale success, a latest failure or a success from before the pod started reports nothing.
func TestKernelMerge_UndeclaredNeedsAFreshSuccess(t *testing.T) {
	for name, c := range map[string]pactoapi.RuntimeConnection{
		"stale success":       {Address: "10.96.0.20", Port: 5432, Successes: 5, LastSuccessAt: kTime(time.Hour)},
		"latest was refused":  {Address: "10.96.0.20", Port: 5432, Successes: 5, Failures: 1, LastSuccessAt: kTime(time.Minute), LastFailureAt: kTime(time.Second)},
		"before the pod life": {Address: "10.96.0.20", Port: 5432, Successes: 5, LastSuccessAt: kTime(11 * time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			f.node.Status.PublishIntervalSeconds = 600 // a bound wide enough that only the rule under test decides
			f.entry().Connections = []pactoapi.RuntimeConnection{c}
			set, _ := f.collect(t)
			for _, o := range set.Observations {
				if p, _ := o.GetDependencyObservation(); p.Undeclared {
					t.Fatalf("unexpected undeclared peer %q", o.Subject.Name)
				}
			}
		})
	}
}

// TestKernelMerge_NonTCPPortIsSilent checks the kernel gives no opinion on a UDP or SCTP Service port: the
// agent sees TCP sockets only, so a missing TCP listener there proves nothing.
func TestKernelMerge_NonTCPPortIsSilent(t *testing.T) {
	for _, proto := range []corev1.Protocol{corev1.ProtocolUDP, corev1.ProtocolSCTP} {
		t.Run(string(proto), func(t *testing.T) {
			f := newKernelFixture()
			f.entry().Listeners = nil
			svc := kService("checkout", "10.96.0.5", 80, 8080)
			svc.Spec.Ports[0].Protocol = proto
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(replace(f.objects(true), svc)...).Build()
			set, _, _ := New(c).Collect(context.Background(), f.input(true))
			if o, p := iface(t, set); o.Outcome != evidence.Observed || !p.Present || o.Provenance.Collector != collectorName {
				t.Fatalf("got %s present=%v collector=%q, want the Kubernetes result", o.Outcome, p.Present, o.Provenance.Collector)
			}
		})
	}
}

// TestKernelMerge_NodeObjectIdentity checks only agent-labelled objects count, and only under their own
// name: an unlabelled object, or one whose status.nodeName claims another node, speaks for nobody.
func TestKernelMerge_NodeObjectIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*pactoapi.NodeRuntimeObservation){
		"no agent labels":        func(n *pactoapi.NodeRuntimeObservation) { n.Labels = nil },
		"status claims the node": func(n *pactoapi.NodeRuntimeObservation) { n.Name = "impostor" },
		"partial agent label set": func(n *pactoapi.NodeRuntimeObservation) {
			n.Labels = map[string]string{nodeagent.LabelName: nodeagent.Name}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			mutate(f.node)
			set, _ := f.collect(t)
			if o, _ := iface(t, set); o.Outcome != evidence.Stale {
				t.Fatalf("got %s, want Stale: no trusted object for the node", o.Outcome)
			}
		})
	}
}

func TestPodStart_FallsBackToCreation(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: *kTime(time.Minute)}}
	if got := podStart(pod); !got.Equal(kTime(time.Minute).Time) {
		t.Fatalf("got %v, want the creation time", got)
	}
}

// TestTrimPod checks the cache transform keeps every pod field the kernel merge reads and drops the rest.
func TestTrimPod(t *testing.T) {
	full := newKernelFixture().pod.DeepCopy()
	full.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubelet"}}
	full.Annotations = map[string]string{"big": "blob"}
	full.Spec.Containers[0].Image = "img"
	full.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "SECRETISH", Value: "x"}}
	full.Spec.Volumes = []corev1.Volume{{Name: "v"}}
	full.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app"}}

	got, err := TrimPod(full)
	if err != nil {
		t.Fatal(err)
	}
	pod := got.(*corev1.Pod)
	if pod.ManagedFields != nil || pod.Annotations != nil || pod.Spec.Volumes != nil || pod.Status.ContainerStatuses != nil ||
		pod.Spec.Containers[0].Image != "" || pod.Spec.Containers[0].Env != nil {
		t.Fatalf("unread fields kept: %+v", pod)
	}
	if pod.Name != full.Name || pod.UID != full.UID || !reflect.DeepEqual(pod.Labels, full.Labels) || pod.Spec.NodeName != kNode ||
		!reflect.DeepEqual(pod.Spec.Containers[0].Ports, full.Spec.Containers[0].Ports) || pod.Status.Phase != corev1.PodRunning ||
		!podReady(pod) || !podStart(pod).Equal(podStart(full)) {
		t.Fatalf("read fields lost: %+v", pod)
	}
	tombstone := "not a pod"
	if got, _ := TrimPod(tombstone); got != tombstone {
		t.Fatal("a non-pod must pass through")
	}
}

// TestSnapshotParity pins the neutral runtimefacts.Snapshot to the NodeRuntimeObservation status: a fully
// populated status and its snapshot encode to the same JSON, each decodes strictly into the other without
// loss, and snapshotOf is that same conversion. Timestamps are whole seconds, the resource's precision.
func TestSnapshotParity(t *testing.T) {
	status := pactoapi.NodeRuntimeObservationStatus{
		NodeName: kNode, AgentVersion: "v1", KernelRelease: "6.12.0", PublishIntervalSeconds: 30,
		LastPublished: kTime(time.Second), ObservingSince: kTime(time.Hour),
		Collection: pactoapi.RuntimeCollection{State: pactoapi.CollectionDegraded, Reason: "EventsDropped", Message: "m",
			DroppedEvents: 3, LastDropAt: kTime(time.Minute), LastResyncAt: kTime(2 * time.Minute)},
		Pods: []pactoapi.PodRuntimeObservation{{
			UID: kPodUID, NetnsShared: true, ObservedSince: kTime(time.Hour), BaselineAt: kTime(time.Minute), Truncated: true,
			Listeners: []pactoapi.RuntimeListener{{Port: 8080, Family: "IPv6", Address: "::", Process: "app"}},
			Connections: []pactoapi.RuntimeConnection{{Address: "10.0.1.7", Port: 8080, Successes: 2, Failures: 1,
				LastSuccessAt: kTime(time.Second), LastFailureAt: kTime(2 * time.Second)}},
		}},
	}
	crdJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var snap runtimefacts.Snapshot
	if err := strictjson.Unmarshal(crdJSON, &snap); err != nil {
		t.Fatalf("CRD status -> Snapshot: %v", err)
	}
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(crdJSON, snapJSON) {
		t.Fatalf("shapes differ:\n crd  %s\n snap %s", crdJSON, snapJSON)
	}
	var back pactoapi.NodeRuntimeObservationStatus
	if err := strictjson.Unmarshal(snapJSON, &back); err != nil {
		t.Fatalf("Snapshot -> CRD status: %v", err)
	}
	// metav1.Time decodes into the local zone, so the comparison is on the wire form.
	if backJSON, _ := json.Marshal(back); !bytes.Equal(backJSON, crdJSON) {
		t.Fatalf("round trip lost data:\n got  %s\n want %s", backJSON, crdJSON)
	}
	if got := snapshotOf(&status); !reflect.DeepEqual(*got, snap) {
		t.Fatalf("snapshotOf:\n got  %+v\n want %+v", *got, snap)
	}
}

// TestKernelMerge_LeaseHeartbeat checks the node's Lease is its heartbeat: the agent rewrites the
// observation only when it changes, so an old lastPublished with a freshly renewed Lease is fresh. A Lease
// renewed before lastPublished, one without the agent labels or without a renewTime changes nothing (an
// older agent heartbeats through lastPublished alone), and a failed Lease read is a collection failure.
func TestKernelMerge_LeaseHeartbeat(t *testing.T) {
	lease := func(renewed *metav1.MicroTime, labels map[string]string) *coordinationv1.Lease {
		return &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: kNode, Namespace: kAgentNS, Labels: labels},
			Spec:       coordinationv1.LeaseSpec{RenewTime: renewed},
		}
	}
	micro := func(ago time.Duration) *metav1.MicroTime { m := metav1.NewMicroTime(kTime(ago).Time); return &m }
	for name, tc := range map[string]struct {
		lease *coordinationv1.Lease
		funcs interceptor.Funcs
		want  evidence.Outcome
	}{
		"renewed lease keeps an unchanged observation fresh": {lease: lease(micro(5*time.Second), nodeagent.Labels()), want: evidence.Observed},
		"no lease: lastPublished decides":                    {want: evidence.Stale},
		"lease renewed long ago":                             {lease: lease(micro(20*time.Minute), nodeagent.Labels()), want: evidence.Stale},
		"lease without renewTime":                            {lease: lease(nil, nodeagent.Labels()), want: evidence.Stale},
		"lease without the agent labels":                     {lease: lease(micro(5*time.Second), nil), want: evidence.Stale},
		"lease list fails": {lease: lease(micro(5*time.Second), nodeagent.Labels()),
			funcs: interceptor.Funcs{List: failList[*coordinationv1.LeaseList]()}, want: evidence.Failed},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			f.node.Status.LastPublished = kTime(10 * time.Minute)
			if tc.lease != nil {
				f.extra = append(f.extra, tc.lease)
			}
			f.funcs = tc.funcs
			set, _ := f.collect(t)
			if o, _ := iface(t, set); o.Outcome != tc.want {
				t.Fatalf("got %s, want %s", o.Outcome, tc.want)
			}
		})
	}
}

// TestKernelCoverage is what an evaluation reports about the node agent's view of the target's pods, so
// a reader of the status (Pacto delivery's claim gate) can tell "observed and clean" from "not observed".
func TestKernelCoverage(t *testing.T) {
	cases := map[string]struct {
		mutate func(f *kernelFixture)
		kernel bool
		want   string
	}{
		"complete":          {func(*kernelFixture) {}, true, "Complete"},
		"stale":             {func(f *kernelFixture) { f.node.Status.LastPublished = kTime(5 * time.Minute) }, true, "Stale"},
		"collection failed": {func(f *kernelFixture) { f.node.Status.Collection.State = pactoapi.CollectionFailed }, true, "Failed"},
		"shared netns":      {func(f *kernelFixture) { f.entry().NetnsShared = true }, true, "Partial"},
		"no pod":            {func(f *kernelFixture) { f.pod.Spec.NodeName = ""; f.pod.Status.Phase = corev1.PodPending }, true, "Partial"},
		"pod list failed":   {func(f *kernelFixture) { f.funcs = interceptor.Funcs{List: failList[*corev1.PodList]()} }, true, "Failed"},
		"kernel off":        {func(*kernelFixture) {}, false, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newKernelFixture()
			c.mutate(f)
			cl := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).WithInterceptorFuncs(f.funcs).Build()
			o := New(cl)
			if got := o.KernelCoverage(); got != "" {
				t.Fatalf("before any Collect: %q", got)
			}
			_, _, _ = o.Collect(context.Background(), f.input(c.kernel))
			if got := o.KernelCoverage(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
