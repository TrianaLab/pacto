/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
)

// egressFixture is the kernel fixture with DNS capture running on node-a and the target pod at 10.0.0.5.
func egressFixture(conns ...pactoapi.RuntimeConnection) *kernelFixture {
	f := newKernelFixture()
	f.node.Status.DNS = pactoapi.RuntimeDNS{State: pactoapi.CollectionRunning}
	f.pod.Status.PodIP = "10.0.0.5"
	f.pod.Status.PodIPs = []corev1.PodIP{{IP: "10.0.0.5"}}
	f.entry().Connections = conns
	return f
}

// named is a fresh successful connection to addr:port the pod's own DNS answers named host.
func named(addr, host string, port int32) pactoapi.RuntimeConnection {
	c := connOK(addr, port)
	c.Host = host
	return c
}

// egressRun collects the fixture with the given uncached reader (nil for none) and returns the egress.
func (f *kernelFixture) egressRun(t *testing.T, reader client.Reader, in func(*CollectInput)) ([]pactoapi.EgressStatus, *pactoapi.EgressCoverage) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).WithInterceptorFuncs(f.funcs).Build()
	input := f.input(true)
	if in != nil {
		in(&input)
	}
	o := New(c)
	if reader != nil {
		o = o.WithReader(reader)
	}
	_, _, _ = o.Collect(context.Background(), input)
	return o.ObservedEgress(nil)
}

// nodesReader serves corev1.Nodes with the given pod CIDRs, or fails every read when err is set.
func nodesReader(t *testing.T, err error, cidrs ...string) client.Reader {
	t.Helper()
	b := fake.NewClientBuilder().WithScheme(kernelScheme())
	for i, c := range cidrs {
		b = b.WithObjects(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("n%d", i)}, Spec: corev1.NodeSpec{PodCIDRs: []string{c}}})
	}
	if err != nil {
		b = b.WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return err }})
	}
	return b.Build()
}

func identities(es []pactoapi.EgressStatus) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Identity+"|"+e.Host)
	}
	return out
}

func wantEgressCoverage(t *testing.T, cov *pactoapi.EgressCoverage, state, reason string) {
	t.Helper()
	if cov == nil || cov.State != state || cov.Reason != reason {
		t.Fatalf("coverage = %+v, want %s %q", cov, state, reason)
	}
}

func TestObserveEgressNamesExternalPeer(t *testing.T) {
	got, cov := egressFixture(named("54.1.1.1", "api.stripe.com", 443)).egressRun(t, nil, nil)
	want := []pactoapi.EgressStatus{{Identity: "stripe", Class: "vendor-api", Host: "api.stripe.com", Port: 443,
		Source: pactoapi.EgressSourceKernel, FirstSeen: metav1.NewTime(kNow)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("egress = %+v\nwant %+v", got, want)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
	if cov.Nodes != 1 || cov.NodesWithAgent != 1 {
		t.Fatalf("node counts = %d/%d, want 1/1", cov.NodesWithAgent, cov.Nodes)
	}
}

func TestObserveEgressSkipsServiceAndPodIPs(t *testing.T) {
	f := egressFixture(
		named("10.96.0.10", "payments.example.com", 80),     // a ClusterIP
		named("10.0.1.7", "payments-pod.example.com", 8080), // an endpoint
		named("10.0.0.5", "self.example.com", 8080),         // a pod IP
	)
	got, cov := f.egressRun(t, nil, nil)
	if len(got) != 0 {
		t.Fatalf("a cluster address is no egress: %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

func TestObserveEgressDedupesByIdentity(t *testing.T) {
	got, _ := egressFixture(
		named("52.1.1.1", "b.s3.amazonaws.com", 443),
		named("52.1.1.2", "a.s3.amazonaws.com", 443),
	).egressRun(t, nil, nil)
	if want := []string{"aws/s3|a.s3.amazonaws.com"}; !reflect.DeepEqual(identities(got), want) {
		t.Fatalf("egress = %v, want %v", identities(got), want)
	}
}

func TestObserveEgressUnclassifiedHostByRegistrableDomain(t *testing.T) {
	got, _ := egressFixture(named("54.9.9.9", "a.corp-legacy.com", 443)).egressRun(t, nil, nil)
	if len(got) != 1 || got[0].Identity != "host/corp-legacy.com" || got[0].Class != "unclassified" {
		t.Fatalf("egress = %+v", got)
	}
}

func TestObserveEgressDropsNamelessHosts(t *testing.T) {
	got, cov := egressFixture(named("54.9.9.8", "legacy-db", 5432), named("54.9.9.7", "foo.internal", 443)).egressRun(t, nil, nil)
	if len(got) != 0 {
		t.Fatalf("a host that names nothing is dropped: %+v", got)
	}
	// The system behind it is still called, so the list cannot read as complete.
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "egress-host-unclassified")
}

func TestObserveEgressCap32Truncated(t *testing.T) {
	conns := make([]pactoapi.RuntimeConnection, 0, MaxStatusEgress+3)
	for i := range MaxStatusEgress + 3 {
		conns = append(conns, named(fmt.Sprintf("54.0.0.%d", i+1), fmt.Sprintf("api.vendor%02d.com", i), 443))
	}
	got, cov := egressFixture(conns...).egressRun(t, nil, nil)
	if len(got) != MaxStatusEgress {
		t.Fatalf("egress = %d entries, want %d", len(got), MaxStatusEgress)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "truncated")
}

func TestObserveEgressCoverageNodesWithAgent(t *testing.T) {
	f := egressFixture(named("54.1.1.1", "api.stripe.com", 443))
	for i, node := range []string{"node-b", "node-c", "node-d"} {
		p := callerPod(fmt.Sprintf("other-%d", i), "", "")
		p.Spec.NodeName = node
		f.extra = append(f.extra, p)
		if node != "node-d" {
			nro := f.node.DeepCopy()
			nro.Name, nro.Status.NodeName, nro.Status.Pods = node, node, nil
			f.extra = append(f.extra, nro)
		}
	}
	// No corev1.Node is ever read through the cached client.
	f.funcs = interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, ok := l.(*corev1.NodeList); ok {
			t.Fatal("a Node list went through the cached client")
		}
		return c.List(ctx, l, opts...)
	}}
	got, cov := f.egressRun(t, nodesReader(t, nil), nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "node-without-agent")
	if cov.Nodes != 4 || cov.NodesWithAgent != 3 {
		t.Fatalf("node counts = %d/%d, want 3/4", cov.NodesWithAgent, cov.Nodes)
	}
	if len(got) != 1 {
		t.Fatalf("egress = %+v", got)
	}
}

func TestObserveEgressPactoWithoutService(t *testing.T) {
	f := egressFixture(named("54.1.1.1", "api.stripe.com", 443))
	f.funcs = interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*corev1.Service); ok {
			return errors.New("no Service is read")
		}
		return c.Get(ctx, key, obj, opts...)
	}}
	got, cov := f.egressRun(t, nil, func(in *CollectInput) { in.ServiceName = "" })
	if want := []string{"stripe|api.stripe.com"}; !reflect.DeepEqual(identities(got), want) {
		t.Fatalf("egress = %v, want %v", identities(got), want)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

func TestObserveEgressUnnamedPublicPeerPartial(t *testing.T) {
	_, cov := egressFixture(connOK("54.1.1.1", 443)).egressRun(t, nil, nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "unnamed-public")
}

func TestObserveEgressUnnamedPrivatePeerOutsidePodCIDRPartial(t *testing.T) {
	_, cov := egressFixture(connOK("10.50.0.9", 5432)).egressRun(t, nodesReader(t, nil, "10.244.0.0/24"), nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "unnamed-private-peers")
}

func TestObserveEgressStalePodCIDRAddressIgnored(t *testing.T) {
	_, cov := egressFixture(connOK("10.244.0.9", 8080)).egressRun(t, nodesReader(t, nil, "10.244.0.0/24"), nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

func TestObserveEgressNodesUnreadableCountsPrivatePeers(t *testing.T) {
	_, cov := egressFixture(connOK("10.244.0.9", 8080)).egressRun(t, nodesReader(t, errors.New("forbidden"), "10.244.0.0/24"), nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "unnamed-private-peers")
}

func TestObserveEgressAmbiguousPeerPartial(t *testing.T) {
	c := connOK("104.16.0.1", 443)
	c.Ambiguous = true
	got, cov := egressFixture(c).egressRun(t, nil, nil)
	if len(got) != 0 {
		t.Fatalf("a shared address names nothing: %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "ambiguous-peers")
}

func TestObserveEgressDNSFailedPartial(t *testing.T) {
	f := egressFixture(named("54.1.1.1", "api.stripe.com", 443))
	f.node.Status.DNS = pactoapi.RuntimeDNS{State: pactoapi.CollectionFailed, Reason: "DNSAttachFailed"}
	_, cov := f.egressRun(t, nil, nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "dns-unavailable")
}

func TestObserveEgressOldAgentPartial(t *testing.T) {
	f := egressFixture(connOK("54.1.1.1", 443))
	f.node.Status.DNS = pactoapi.RuntimeDNS{} // an older agent writes no dns block
	_, cov := f.egressRun(t, nil, nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "dns-unavailable")
}

func TestObserveEgressOffWithoutKernelEvidence(t *testing.T) {
	got, cov := egressFixture(named("54.1.1.1", "api.stripe.com", 443)).egressRun(t, nil, func(in *CollectInput) { in.KernelEvidence = false })
	if got != nil || cov != nil {
		t.Fatalf("kernel evidence off claims nothing: %+v %+v", got, cov)
	}
	if got, cov := New(nil).ObservedEgress(nil); got != nil || cov != nil {
		t.Fatalf("before a Collect: %+v %+v", got, cov)
	}
}

func TestObserveEgressFirstSeenStable(t *testing.T) {
	f := egressFixture(named("54.1.1.1", "api.stripe.com", 443), named("52.1.1.1", "a.s3.amazonaws.com", 443))
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).Build()
	o := New(c)
	_, _, _ = o.Collect(context.Background(), f.input(true))
	earlier := metav1.NewTime(kNow.Add(-time.Hour))
	prev := []pactoapi.EgressStatus{{Identity: "stripe", Source: pactoapi.EgressSourceKernel, FirstSeen: earlier}}
	got, _ := o.ObservedEgress(prev)
	if !got[1].FirstSeen.Equal(&earlier) || !got[0].FirstSeen.Time.Equal(kNow) {
		t.Fatalf("FirstSeen = %v, %v", got[0].FirstSeen, got[1].FirstSeen)
	}
}

func TestObserveEgressReadFailures(t *testing.T) {
	cases := map[string]struct {
		funcs  interceptor.Funcs
		mutate func(*kernelFixture)
		state  string
		reason string
	}{
		"no agent data":   {mutate: func(f *kernelFixture) { f.node = nil }, state: pactoapi.CallerCoverageUnavailable, reason: "no-agent-data"},
		"agent data":      {funcs: interceptor.Funcs{List: failList[*pactoapi.NodeRuntimeObservationList]()}, state: pactoapi.CallerCoverageUnavailable, reason: "agent-data-unreadable"},
		"pods":            {funcs: interceptor.Funcs{List: failList[*corev1.PodList]()}, state: pactoapi.CallerCoverageUnavailable, reason: "pods-unreadable"},
		"pactos":          {funcs: interceptor.Funcs{List: failList[*pactoapi.PactoList]()}, state: pactoapi.CallerCoverageUnavailable, reason: "pactos-unreadable"},
		"services":        {funcs: interceptor.Funcs{List: failList[*corev1.ServiceList]()}, state: pactoapi.CallerCoveragePartial, reason: "services-unreadable"},
		"endpoint slices": {funcs: interceptor.Funcs{List: failList[*discoveryv1.EndpointSliceList]()}, state: pactoapi.CallerCoveragePartial, reason: "services-unreadable"},
		"no running pod":  {mutate: func(f *kernelFixture) { f.pod.Status.Phase = corev1.PodSucceeded }, state: pactoapi.CallerCoveragePartial, reason: "no-running-pods"},
		"stale node":      {mutate: func(f *kernelFixture) { f.node.Status.LastPublished = kTime(time.Hour) }, state: pactoapi.CallerCoveragePartial, reason: "stale-node"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := egressFixture(named("54.1.1.1", "api.stripe.com", 443))
			f.funcs = tc.funcs
			if tc.mutate != nil {
				tc.mutate(f)
			}
			_, cov := f.egressRun(t, nil, nil)
			wantEgressCoverage(t, cov, tc.state, tc.reason)
		})
	}
}

func TestObserveEgressTargetPodsUnreadable(t *testing.T) {
	f := egressFixture(named("54.1.1.1", "api.stripe.com", 443))
	// The cluster-wide pod list passes; the target's own (namespaced, selected) one fails.
	f.funcs = interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, ok := l.(*corev1.PodList); ok && len(opts) > 0 {
			return errors.New("boom")
		}
		return c.List(ctx, l, opts...)
	}}
	_, cov := f.egressRun(t, nil, nil)
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageUnavailable, "pods-unreadable")
}

func TestObserveCallersUnchangedAfterSplit(t *testing.T) {
	callers, cov := callerFixture().callers(t, nil)
	if got := names(callers); len(got) != 5 || cov.State != pactoapi.CallerCoverageComplete {
		t.Fatalf("callers = %v, coverage = %+v", got, cov)
	}
	f := callerFixture()
	f.funcs = interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*corev1.Service); ok && key == (types.NamespacedName{Namespace: kNS, Name: "checkout"}) {
			return errors.New("boom")
		}
		return c.Get(ctx, key, obj, opts...)
	}}
	if _, cov := f.callers(t, nil); cov.State != pactoapi.CallerCoverageUnavailable || cov.Reason != "service-unreadable" {
		t.Fatalf("coverage = %+v, want unavailable service-unreadable", cov)
	}
	f = callerFixture()
	f.funcs = interceptor.Funcs{List: failList[*discoveryv1.EndpointSliceList]()}
	if _, cov := f.callers(t, nil); cov.State != pactoapi.CallerCoverageUnavailable || cov.Reason != "service-unreadable" {
		t.Fatalf("coverage = %+v, want unavailable service-unreadable", cov)
	}
}

func TestObserveEgressSkipsNodeAndLoadBalancerAddresses(t *testing.T) {
	f := egressFixture(
		named("192.168.1.10", "node.example.com", 10250),  // a node address
		named("203.0.113.7", "shop.example.com", 443),     // the cluster's own load balancer
		named("203.0.113.8", "shop-ext.example.com", 443), // a Service external IP
	)
	f.pod.Status.HostIPs = []corev1.HostIP{{IP: "192.168.1.10"}}
	lb := kService("edge", "10.96.0.30", 443, 8443)
	lb.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "203.0.113.7"}}
	lb.Spec.ExternalIPs = []string{"203.0.113.8"}
	f.extra = append(f.extra, lb)
	got, cov := f.egressRun(t, nil, nil)
	if len(got) != 0 {
		t.Fatalf("a cluster address is no egress: %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

// TestObserveEgressPodMissingFromFreshSnapshotPartial: the node's snapshot is fresh and running but its
// last scan came before the target pod, so the pod's calls are not read: unobserved, never complete.
func TestObserveEgressPodMissingFromFreshSnapshotPartial(t *testing.T) {
	f := egressFixture()
	f.entry().UID = "another-pod"
	got, cov := f.egressRun(t, nil, nil)
	if len(got) != 0 {
		t.Fatalf("egress = %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "pod-unobserved")
}

// TestObserveEgressHostNetworkTargetInIgnoredNamespacePartial: the caller exemption of an ignored namespace
// never covers the target's own calls; a target on a shared network namespace has calls nobody can read.
func TestObserveEgressHostNetworkTargetInIgnoredNamespacePartial(t *testing.T) {
	f := egressFixture(named("54.1.1.1", "api.datadoghq.com", 443))
	f.entry().NetnsShared = true
	got, cov := f.egressRun(t, nil, func(in *CollectInput) { in.CallerIgnoreNamespaces = []string{kNS} })
	if len(got) != 0 {
		t.Fatalf("egress = %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "shared-netns")
}
