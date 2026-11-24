/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"cmp"
	"context"
	"net/netip"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// MaxStatusEgress caps status.observedEgress; more externals than this make the coverage partial.
const MaxStatusEgress = 32

// observeEgress lists the systems outside the cluster the node agent saw the target's pods call inside the
// caller window, one entry per identity, with those an Istio egress gateway's access log saw them call when a
// person turned gateway logs on (WithGatewayLogs), and how far that list can be trusted. It returns nothing only when
// kernel evidence is off: a worker with no Service calls out too. Every read it cannot make and every call it
// cannot name lowers the coverage: an external Pacto cannot see is one it cannot rule out.
func (o *Observer) observeEgress(ctx context.Context, input CollectInput, now time.Time) ([]unversioned.EgressStatus, *unversioned.EgressCoverage) {
	if !input.KernelEvidence {
		return nil, nil
	}
	in, reason := o.loadRuntimeInputs(ctx, input)
	if reason != "" {
		return nil, egressCoverage(unversioned.CallerCoverageUnavailable, reason)
	}
	targets, err := o.targetPods(ctx, input)
	if err != nil {
		return nil, egressCoverage(unversioned.CallerCoverageUnavailable, "pods-unreadable")
	}
	cov := egressCoverage(unversioned.CallerCoverageComplete, "")
	cov.Nodes, cov.NodesWithAgent = nodeCounts(in)
	_, freshness := nodeCoverage(&cov.CallerCoverage, input, in, podsByUID(in.pods), now)
	internal, ok := o.internalAddresses(ctx, in.pods)
	refs := make([]runtimefacts.PodRef, 0, len(targets))
	for _, p := range targets {
		refs = append(refs, runtimefacts.PodRef{UID: string(p.UID), Node: p.Spec.NodeName})
		if s := in.nodes[p.Spec.NodeName]; s != nil && s.DNS.State != runtimefacts.StateRunning {
			partialCoverage(&cov.CallerCoverage, "dns-unavailable")
		}
	}
	if !ok {
		partialCoverage(&cov.CallerCoverage, "services-unreadable")
	}
	if len(targets) == 0 {
		// Nothing runs to be seen: a job between runs still calls out when it runs.
		partialCoverage(&cov.CallerCoverage, "no-running-pods")
	}
	named, unnamed := runtimefacts.ExternalEgress(in.nodes, refs, internal, o.podCIDRs(ctx), now.Add(-freshness))
	unnamedCoverage(&cov.CallerCoverage, unnamed)
	out, unclassified := classifyEgress(named, unversioned.EgressSourceKernel)
	if o.gatewayLogs != nil {
		gw := o.gatewayLogs.read(ctx, freshness)
		for _, r := range gw.reasons {
			partialCoverage(&cov.CallerCoverage, r)
		}
		viaGateway, gwUnclassified := gatewayEgress(gw.lines, targets)
		out, unclassified = append(out, viaGateway...), unclassified || gwUnclassified
		slices.SortStableFunc(out, func(a, b unversioned.EgressStatus) int { return cmp.Compare(a.Identity, b.Identity) })
	}
	if unclassified {
		// A legacy system reached by a bare or private-suffix name is still a system the workload calls.
		partialCoverage(&cov.CallerCoverage, "egress-host-unclassified")
	}
	if len(out) > MaxStatusEgress {
		out = out[:MaxStatusEgress]
		partialCoverage(&cov.CallerCoverage, "truncated")
	}
	return out, cov
}

func egressCoverage(state, reason string) *unversioned.EgressCoverage {
	return &unversioned.EgressCoverage{CallerCoverage: unversioned.CallerCoverage{State: state, Reason: reason}}
}

// nodeCounts is how many nodes run pods and how many of those publish node agent data.
func nodeCounts(in callerInputs) (nodes, withAgent int32) {
	seen := map[string]bool{}
	for _, p := range in.pods {
		if p.Status.Phase != corev1.PodRunning || p.Spec.NodeName == "" || seen[p.Spec.NodeName] {
			continue
		}
		seen[p.Spec.NodeName] = true
		nodes++
		if in.nodes[p.Spec.NodeName] != nil {
			withAgent++
		}
	}
	return nodes, withAgent
}

// unnamedCoverage lowers cov for each kind of call the node agent could not name, and for each target pod
// whose calls it could not read: not mapped yet on its node, or on a shared network namespace (a host
// network pod in an ignored namespace too: the caller exemption never applies to the target's own calls).
func unnamedCoverage(cov *unversioned.CallerCoverage, u runtimefacts.Unnamed) {
	for _, r := range []struct {
		n      int
		reason string
	}{{u.Ambiguous, "ambiguous-peers"}, {u.Public, "unnamed-public"}, {u.Private, "unnamed-private-peers"},
		{u.Unobserved, "pod-unobserved"}, {u.Shared, "shared-netns"}} {
		if r.n > 0 {
			partialCoverage(cov, r.reason)
		}
	}
}

// internalAddresses reports the addresses inside the cluster: every Service's cluster, external and load
// balancer address, every EndpointSlice address, every pod address and every node address the pods carry.
// ok is false when the Services or EndpointSlices could not be listed, so the set may miss some.
func (o *Observer) internalAddresses(ctx context.Context, pods []corev1.Pod) (func(netip.Addr) bool, bool) {
	set := map[netip.Addr]bool{}
	add := func(ips ...string) {
		for _, ip := range ips {
			if a, err := netip.ParseAddr(ip); err == nil {
				set[a.Unmap()] = true
			}
		}
	}
	for _, p := range pods {
		for _, ip := range p.Status.PodIPs {
			add(ip.IP)
		}
		for _, ip := range p.Status.HostIPs {
			add(ip.IP)
		}
		add(p.Status.PodIP, p.Status.HostIP)
	}
	var svcs corev1.ServiceList
	svcErr := o.client.List(ctx, &svcs)
	for _, s := range svcs.Items {
		add(s.Spec.ClusterIP)
		add(s.Spec.ClusterIPs...)
		add(s.Spec.ExternalIPs...)
		for _, ing := range s.Status.LoadBalancer.Ingress {
			add(ing.IP)
		}
	}
	var eps discoveryv1.EndpointSliceList
	sliceErr := o.client.List(ctx, &eps)
	for _, s := range eps.Items {
		for _, ep := range s.Endpoints {
			add(ep.Addresses...)
		}
	}
	return func(a netip.Addr) bool { return set[a] }, svcErr == nil && sliceErr == nil
}

// podCIDRs reports whether an address is inside a node's pod CIDR: the address of a pod that has gone. The
// nodes are read once through the uncached reader (the chart grants nodes list, never watch); when that read
// fails, nothing is explained, so every unexplained private peer counts.
func (o *Observer) podCIDRs(ctx context.Context) func(netip.Addr) bool {
	var cidrs []netip.Prefix
	var nodes corev1.NodeList
	if o.reader != nil && o.reader.List(ctx, &nodes) == nil {
		for _, n := range nodes.Items {
			for _, c := range n.Spec.PodCIDRs {
				if p, err := netip.ParsePrefix(c); err == nil {
					cidrs = append(cidrs, p)
				}
			}
		}
	}
	return func(a netip.Addr) bool {
		return slices.ContainsFunc(cidrs, func(p netip.Prefix) bool { return p.Contains(a) })
	}
}

// classifyEgress names each host source saw with the operator's catalog, drops a host that names nothing (a single
// label, a private suffix) and keeps one entry per identity, the first host in sort order as its example,
// sorted by identity. unclassified reports that a host was dropped, so the list misses a system.
func classifyEgress(named []runtimefacts.Egress, source string) (out []unversioned.EgressStatus, unclassified bool) {
	cat := externalcatalog.Default()
	seen := map[string]bool{}
	for _, e := range named {
		hosts := externalcatalog.NormalizeHost(e.Host)
		var m externalcatalog.Match
		ok := len(hosts) > 0
		if ok {
			m, ok = cat.MatchHost(hosts[0])
		}
		if !ok {
			unclassified = true
			continue
		}
		if seen[string(m.Identity)] {
			continue
		}
		seen[string(m.Identity)] = true
		out = append(out, unversioned.EgressStatus{
			Identity: string(m.Identity), Class: m.Class, Host: hosts[0].Host, Port: e.Port, Source: source,
		})
	}
	slices.SortFunc(out, func(a, b unversioned.EgressStatus) int { return cmp.Compare(a.Identity, b.Identity) })
	return out, unclassified
}

// ObservedEgress is what the last Collect found the target's pods calling outside the cluster
// (observeEgress), with each entry's FirstSeen carried over from prev, so an external seen again does not
// rewrite the status. Both are nil before a Collect or when runtime observation is off.
func (o *Observer) ObservedEgress(prev []unversioned.EgressStatus) ([]unversioned.EgressStatus, *unversioned.EgressCoverage) {
	if o.run == nil {
		return nil, nil
	}
	out := slices.Clone(o.run.egress)
	for i := range out {
		out[i].FirstSeen = metav1.NewTime(o.run.now)
		if j := slices.IndexFunc(prev, func(p unversioned.EgressStatus) bool {
			return p.Identity == out[i].Identity && p.Source == out[i].Source
		}); j >= 0 {
			out[i].FirstSeen = prev[j].FirstSeen
		}
	}
	return out, o.run.egressCoverage
}
