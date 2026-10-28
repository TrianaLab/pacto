/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
)

// callerPod is a running pod on node-a in kNS, controlled by owner (kind/name) when kind is set.
func callerPod(name, kind, owner string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: kNS, UID: types.UID("uid-" + name)},
		Spec:       corev1.PodSpec{NodeName: kNode},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if kind != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: kind, Name: owner, Controller: new(true), UID: "o"}}
	}
	return p
}

// hostPod adds a host-network pod in namespace to the node, its sockets shared with the host's.
func hostPod(f *kernelFixture, namespace string) {
	p := callerPod("host", "DaemonSet", "cni")
	p.Namespace, p.Spec.HostNetwork = namespace, true
	f.extra = append(f.extra, p)
	f.node.Status.Pods = append(f.node.Status.Pods, pactoapi.PodRuntimeObservation{UID: string(p.UID), NetnsShared: true})
}

func controlled[T client.Object](obj T, name, kind, owner string) T {
	obj.SetName(name)
	obj.SetNamespace(kNS)
	if kind != "" {
		obj.SetOwnerReferences([]metav1.OwnerReference{{Kind: kind, Name: owner, Controller: new(true), UID: "o"}})
	}
	return obj
}

// callerFixture adds callers of checkout's Service (10.96.0.5:80 or the endpoint 10.0.0.5:8080) to the kernel
// fixture: a Deployment's pod, a CronJob's pod, a DaemonSet's pod, a bare pod, a Pacto target (payments) and
// a ReplicaSet with no Deployment, plus pods of a Pacto that targets an owned ReplicaSet (canary) and one that
// targets a CronJob's Job (report).
func callerFixture() *kernelFixture {
	f := newKernelFixture()
	pods := []*corev1.Pod{
		callerPod("legacy-1", "ReplicaSet", "legacy-abc"),
		callerPod("nightly-1", "Job", "nightly-123"),
		callerPod("agent-1", "DaemonSet", "agent"),
		callerPod("debug", "", ""),
		callerPod("payments-1", "ReplicaSet", "payments-abc"),
		callerPod("orphan-1", "ReplicaSet", "orphan-abc"),
		callerPod("canary-1", "ReplicaSet", "canary-abc"),
		callerPod("report-1", "Job", "report-123"),
	}
	for _, p := range pods {
		f.extra = append(f.extra, p)
		f.node.Status.Pods = append(f.node.Status.Pods, pactoapi.PodRuntimeObservation{
			UID: string(p.UID), Connections: []pactoapi.RuntimeConnection{connOK("10.96.0.5", 80)},
		})
	}
	f.entry().Connections = []pactoapi.RuntimeConnection{connOK("10.0.0.5", 8080)} // checkout calls itself
	f.pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "checkout-abc", Controller: new(true), UID: "o"}}
	f.extra = append(f.extra,
		controlled(&appsv1.ReplicaSet{}, "checkout-abc", "Deployment", "checkout"),
		controlled(&appsv1.ReplicaSet{}, "legacy-abc", "Deployment", "legacy"),
		controlled(&batchv1.Job{}, "nightly-123", "CronJob", "nightly"),
		controlled(&appsv1.ReplicaSet{}, "payments-abc", "Deployment", "payments"),
		controlled(&appsv1.ReplicaSet{}, "orphan-abc", "", ""),
		// A Pacto on an owned ReplicaSet and one on a Job a CronJob owns: their pods are not callers.
		controlled(&appsv1.ReplicaSet{}, "canary-abc", "Deployment", "canary"),
		controlled(&batchv1.Job{}, "report-123", "CronJob", "report"),
		workloadPacto("canary", "ReplicaSet", "canary-abc"),
		workloadPacto("report", "Job", "report-123"),
	)
	return f
}

// workloadPacto is a Pacto that targets the workload kind/name directly.
func workloadPacto(name, kind, workload string) *pactoapi.Pacto {
	p := kPacto(name, "", name, "1.0.0", "registry/"+name+":1.0.0")
	p.Spec.Target.WorkloadRef = &pactoapi.WorkloadRef{Kind: kind, Name: workload}
	return p
}

func (f *kernelFixture) callers(t *testing.T, in func(*CollectInput)) ([]pactoapi.ObservedCaller, *pactoapi.CallerCoverage) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).WithInterceptorFuncs(f.funcs).Build()
	input := f.input(true)
	if in != nil {
		in(&input)
	}
	o := New(c)
	_, _, _ = o.Collect(context.Background(), input)
	return o.ObservedCallers(nil)
}

func names(cs []pactoapi.ObservedCaller) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Namespace+"/"+c.Kind+"/"+c.Name+"/"+c.Source)
	}
	return out
}

func TestObservedCallers_NamesOwnersAndSkipsPactoTargets(t *testing.T) {
	callers, cov := callerFixture().callers(t, nil)
	want := []string{
		"shop/CronJob/nightly/ebpf", "shop/DaemonSet/agent/ebpf", "shop/Deployment/legacy/ebpf",
		"shop/Pod/debug/ebpf", "shop/ReplicaSet/orphan-abc/ebpf",
	}
	if got := names(callers); !reflect.DeepEqual(got, want) {
		t.Fatalf("callers = %v\nwant %v", got, want)
	}
	if cov == nil || cov.State != pactoapi.CallerCoverageComplete || cov.Reason != "" {
		t.Fatalf("coverage = %+v, want complete", cov)
	}
	for _, c := range callers {
		if !c.FirstSeen.Time.Equal(kNow) {
			t.Fatalf("a new caller is first seen now: %+v", c)
		}
	}
}

func TestObservedCallers_FirstSeenIsKept(t *testing.T) {
	f := callerFixture()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).Build()
	o := New(c)
	if got, cov := o.ObservedCallers(nil); got != nil || cov != nil {
		t.Fatalf("before a Collect: %v %+v", got, cov)
	}
	_, _, _ = o.Collect(context.Background(), f.input(true))
	earlier := metav1.NewTime(kNow.Add(-time.Hour))
	got, _ := o.ObservedCallers([]pactoapi.ObservedCaller{
		{Namespace: kNS, Kind: "Deployment", Name: "legacy", Source: "ebpf", FirstSeen: earlier},
		{Namespace: kNS, Kind: "Deployment", Name: "gone", Source: "ebpf", FirstSeen: earlier},
	})
	for _, c := range got {
		want := kNow
		if c.Name == "legacy" {
			want = earlier.Time
		}
		if c.Name == "gone" || !c.FirstSeen.Time.Equal(want) {
			t.Fatalf("caller %+v, want first seen %v", c, want)
		}
	}
}

func TestObservedCallers_Off(t *testing.T) {
	for name, in := range map[string]func(*CollectInput){
		"kernel evidence off": func(in *CollectInput) { in.KernelEvidence = false },
		"no Service":          func(in *CollectInput) { in.ServiceName = "" },
	} {
		t.Run(name, func(t *testing.T) {
			if callers, cov := callerFixture().callers(t, in); callers != nil || cov != nil {
				t.Fatalf("got %v %+v, want nothing", callers, cov)
			}
		})
	}
}

func TestObservedCallers_Window(t *testing.T) {
	f := callerFixture()
	f.node.Status.Pods[1].Connections[0].LastSuccessAt = kTime(4 * time.Minute) // legacy, older than 90s
	if got := names(mustCallers(t, f, nil)); len(got) != 4 {
		t.Fatalf("a call outside the window counted: %v", got)
	}
	if got := names(mustCallers(t, f, func(in *CollectInput) { in.CallerWindow = 5 * time.Minute })); len(got) != 5 {
		t.Fatalf("a call inside the caller window was missed: %v", got)
	}
}

func mustCallers(t *testing.T, f *kernelFixture, in func(*CollectInput)) []pactoapi.ObservedCaller {
	t.Helper()
	c, _ := f.callers(t, in)
	return c
}

func TestObservedCallers_Coverage(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*kernelFixture)
		input        func(*CollectInput)
		state        string
		reason       string
		callersAfter int
	}{
		{name: "node list fails", state: pactoapi.CallerCoverageUnavailable, reason: "agent-data-unreadable",
			setup: func(f *kernelFixture) {
				f.funcs = interceptor.Funcs{List: failList[*pactoapi.NodeRuntimeObservationList]()}
			}},
		{name: "no agent data", state: pactoapi.CallerCoverageUnavailable, reason: "no-agent-data",
			setup: func(f *kernelFixture) { f.node = nil }},
		{name: "Service missing", state: pactoapi.CallerCoverageUnavailable, reason: "service-unreadable",
			input: func(in *CollectInput) { in.ServiceName = "gone" }},
		{name: "slices unreadable", state: pactoapi.CallerCoverageUnavailable, reason: "service-unreadable",
			setup: func(f *kernelFixture) {
				f.funcs = interceptor.Funcs{List: failList[*discoveryv1.EndpointSliceList]()}
			}},
		{name: "pods unreadable", state: pactoapi.CallerCoverageUnavailable, reason: "pods-unreadable",
			setup: func(f *kernelFixture) { f.funcs = interceptor.Funcs{List: failList[*corev1.PodList]()} }},
		{name: "pactos unreadable", state: pactoapi.CallerCoverageUnavailable, reason: "pactos-unreadable",
			setup: func(f *kernelFixture) { f.funcs = interceptor.Funcs{List: failList[*pactoapi.PactoList]()} }},
		{name: "stale node", state: pactoapi.CallerCoveragePartial, reason: "stale-node", callersAfter: 5,
			setup: func(f *kernelFixture) { f.node.Status.LastPublished = kTime(time.Hour) }},
		{name: "collection failed", state: pactoapi.CallerCoveragePartial, reason: "collection-failed", callersAfter: 5,
			setup: func(f *kernelFixture) { f.node.Status.Collection.State = pactoapi.CollectionFailed }},
		{name: "degraded", state: pactoapi.CallerCoveragePartial, reason: "degraded-node", callersAfter: 5,
			setup: func(f *kernelFixture) { f.node.Status.Collection.State = pactoapi.CollectionDegraded }},
		{name: "shared netns", state: pactoapi.CallerCoveragePartial, reason: "shared-netns", callersAfter: 5,
			setup: func(f *kernelFixture) { f.node.Status.Pods[2].NetnsShared = true }},
		{name: "shared netns, pod unknown", state: pactoapi.CallerCoveragePartial, reason: "shared-netns", callersAfter: 5,
			setup: func(f *kernelFixture) {
				f.node.Status.Pods = append(f.node.Status.Pods, pactoapi.PodRuntimeObservation{UID: "uid-vanished", NetnsShared: true})
			}},
		{name: "host network in kube-system", state: pactoapi.CallerCoverageComplete, callersAfter: 5,
			setup: func(f *kernelFixture) { hostPod(f, "kube-system") }},
		{name: "static pod on the host network in kube-system", state: pactoapi.CallerCoverageComplete, callersAfter: 5,
			setup: func(f *kernelFixture) {
				hostPod(f, "kube-system")
				p := f.extra[len(f.extra)-1].(*corev1.Pod)
				p.Annotations = map[string]string{mirrorAnnotation: "0123456789abcdef0123456789abcdef"}
				f.node.Status.Pods[len(f.node.Status.Pods)-1].UID = "0123456789abcdef0123456789abcdef"
			}},
		{name: "host network in the agent namespace", state: pactoapi.CallerCoverageComplete, callersAfter: 5,
			setup: func(f *kernelFixture) { hostPod(f, kAgentNS) }},
		{name: "host network in an ignored namespace", state: pactoapi.CallerCoverageComplete, callersAfter: 5,
			setup: func(f *kernelFixture) { hostPod(f, "cilium") },
			input: func(in *CollectInput) { in.CallerIgnoreNamespaces = []string{"cilium"} }},
		// A CNI namespace a cluster may not have is not built in: anyone who may create namespaces could
		// squat it and call unseen.
		{name: "host network in an unlisted CNI namespace", state: pactoapi.CallerCoveragePartial, reason: "shared-netns", callersAfter: 5,
			setup: func(f *kernelFixture) { hostPod(f, "calico-system") }},
		{name: "host network in a workload namespace", state: pactoapi.CallerCoveragePartial, reason: "shared-netns", callersAfter: 5,
			setup: func(f *kernelFixture) { hostPod(f, "batch") }},
		{name: "truncated entry", state: pactoapi.CallerCoveragePartial, reason: "truncated", callersAfter: 5,
			setup: func(f *kernelFixture) { f.node.Status.Pods[2].Truncated = true }},
		{name: "node without agent", state: pactoapi.CallerCoveragePartial, reason: "node-without-agent", callersAfter: 5,
			setup: func(f *kernelFixture) {
				p := callerPod("elsewhere", "", "")
				p.Spec.NodeName = "node-b"
				f.extra = append(f.extra, p)
			}},
		{name: "caller pod unknown", state: pactoapi.CallerCoveragePartial, reason: "unresolved-caller", callersAfter: 5,
			setup: func(f *kernelFixture) {
				f.node.Status.Pods = append(f.node.Status.Pods, pactoapi.PodRuntimeObservation{
					UID: "uid-vanished", Connections: []pactoapi.RuntimeConnection{connOK("10.96.0.5", 80)}})
			}},
		{name: "owner unreadable", state: pactoapi.CallerCoveragePartial, reason: "unresolved-caller", callersAfter: 4,
			setup: func(f *kernelFixture) {
				f.funcs = interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*batchv1.Job); ok {
						return fmt.Errorf("boom")
					}
					return c.Get(ctx, key, obj, opts...)
				}}
			}},
		{name: "more than the cap", state: pactoapi.CallerCoveragePartial, reason: "truncated", callersAfter: maxObservedCallers,
			setup: func(f *kernelFixture) {
				for i := range maxObservedCallers {
					p := callerPod(fmt.Sprintf("bare-%02d", i), "", "")
					f.extra = append(f.extra, p)
					f.node.Status.Pods = append(f.node.Status.Pods, pactoapi.PodRuntimeObservation{
						UID: string(p.UID), Connections: []pactoapi.RuntimeConnection{connOK("10.96.0.5", 80)}})
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := callerFixture()
			if tt.setup != nil {
				tt.setup(f)
			}
			callers, cov := f.callers(t, tt.input)
			if cov == nil || cov.State != tt.state || cov.Reason != tt.reason {
				t.Fatalf("coverage = %+v, want %s/%s", cov, tt.state, tt.reason)
			}
			if len(callers) != tt.callersAfter {
				t.Fatalf("callers = %v, want %d", names(callers), tt.callersAfter)
			}
		})
	}
}
