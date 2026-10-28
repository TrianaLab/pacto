/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/nodeagent"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// Kernel merge (spec section 4). The node agent publishes one NodeRuntimeObservation per node; the operator
// maps the target's pods onto them and folds what the kernel saw into the Kubernetes observations. The
// manager's cache keeps an informer on those objects once the first List starts it, but no controller is
// triggered by them: they change every publish interval, so the periodic requeue reads them.

const (
	// collectorKernelMerged marks an observation whose verdict used kernel evidence; collectorKernel marks
	// one only the kernel could produce (an undeclared peer).
	collectorKernelMerged = runtimefacts.CollectorKubernetesKernel
	collectorKernel       = runtimefacts.CollectorKernel

	// labelJobName is the label the Job controller stamps on every pod it creates.
	labelJobName = "batch.kubernetes.io/job-name"
)

// kernelPod is one target pod with its kernel coverage (runtimefacts.Classify).
type kernelPod struct {
	pod *corev1.Pod
	runtimefacts.Observed
}

// kernelView is everything the kernel says about one target in one reconcile. failed is set when the
// operator could not read the pods or the node objects, which is a collection failure, not silence.
type kernelView struct {
	pods   []kernelPod
	failed bool
	// now and freshness bound a connection timestamp: one older than freshness before now is history, not
	// evidence. freshness is the largest staleness bound among the nodes that contributed data.
	now       time.Time
	freshness time.Duration
	// nodes (by object name) and staleness classify a pod outside the workload, such as a canary the
	// Service also selects.
	nodes     map[string]*runtimefacts.Snapshot
	staleness time.Duration
}

// facts is the neutral view of the target pods the dependency verdicts judge.
func (kv *kernelView) facts() runtimefacts.View {
	v := runtimefacts.View{Now: kv.now, Freshness: kv.freshness}
	for _, kp := range kv.pods {
		v.Workloads = append(v.Workloads, kp.Observed)
	}
	return v
}

// loadKernelView reads the target's running pods and the node objects and their Leases in the agent namespace. It returns nil
// when kernel evidence is off, so every merge below is a no-op and the output is the Kubernetes-only result.
func (o *Observer) loadKernelView(ctx context.Context, input CollectInput, now time.Time) *kernelView {
	if !input.KernelEvidence || input.WorkloadName == "" {
		return nil
	}
	pods, err := o.targetPods(ctx, input)
	if err != nil {
		// Recorded where it happened: a workload read failure belongs to the workload view.
		return &kernelView{failed: true}
	}
	byNode, err := o.loadNodes(ctx, input)
	if err != nil {
		return &kernelView{failed: true}
	}
	kv := &kernelView{now: now, nodes: byNode, staleness: input.KernelEvidenceStaleness}
	for i := range pods {
		kp := kv.classify(&pods[i])
		if kp.Facts != nil {
			kv.freshness = max(kv.freshness, byNode[pods[i].Spec.NodeName].StalenessBound(kv.staleness))
		}
		kv.pods = append(kv.pods, kp)
	}
	return kv
}

// loadNodes reads the node objects and their Leases in the agent namespace, keyed by object name.
func (o *Observer) loadNodes(ctx context.Context, input CollectInput) (map[string]*runtimefacts.Snapshot, error) {
	// Only objects carrying the agent's labels count, and only by object name: the agent names its object
	// after its node, while status.nodeName is a field any writer could set to speak for another node. The
	// list is read-only here, so the cache hands out its own copies.
	var nodes unversioned.NodeRuntimeObservationList
	if err := o.client.List(ctx, &nodes, client.InNamespace(input.KernelEvidenceNamespace),
		client.MatchingLabels(nodeagent.Labels()), client.UnsafeDisableDeepCopy); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list NodeRuntimeObservations: %w", err))
		return nil, err
	}
	// The heartbeat is the node's Lease, renewed every publish while the observation itself is rewritten only
	// when it changes. An agent that renews no Lease (an older one) still heartbeats through lastPublished.
	var leases coordinationv1.LeaseList
	if err := o.client.List(ctx, &leases, client.InNamespace(input.KernelEvidenceNamespace),
		client.MatchingLabels(nodeagent.Labels()), client.UnsafeDisableDeepCopy); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list node agent Leases: %w", err))
		return nil, err
	}
	renewed := make(map[string]time.Time, len(leases.Items))
	for i := range leases.Items {
		if rt := leases.Items[i].Spec.RenewTime; rt != nil {
			renewed[leases.Items[i].Name] = rt.Time
		}
	}
	byNode := make(map[string]*runtimefacts.Snapshot, len(nodes.Items))
	for i := range nodes.Items {
		snap := snapshotOf(&nodes.Items[i].Status)
		if rt := renewed[nodes.Items[i].Name]; rt.After(snap.LastPublished) {
			snap.LastPublished = rt
		}
		byNode[nodes.Items[i].Name] = snap
	}
	return byNode, nil
}

// coverage is the worst coverage among the target's pods. A target with no running pod has nothing the
// kernel could speak for, which is partial: it can never confirm a clean run.
func (kv *kernelView) coverage() string {
	switch {
	case kv == nil:
		return ""
	case kv.failed:
		return runtimefacts.CoverageFailed.String()
	case len(kv.pods) == 0:
		return runtimefacts.CoveragePartial.String()
	}
	worst := runtimefacts.CoverageComplete
	for _, kp := range kv.pods {
		worst = max(worst, kp.Coverage)
	}
	return worst.String()
}

// classify computes one pod's kernel coverage against the node objects this view read.
func (kv *kernelView) classify(pod *corev1.Pod) kernelPod {
	return kernelPod{pod: pod, Observed: runtimefacts.Classify(kv.nodes[pod.Spec.NodeName], string(pod.UID), podStart(pod), kv.now, kv.staleness)}
}

// snapshotOf converts a node object's status into its neutral form. The two share one JSON shape (pinned by
// the parity test), so this copies field for field.
func snapshotOf(st *unversioned.NodeRuntimeObservationStatus) *runtimefacts.Snapshot {
	s := &runtimefacts.Snapshot{
		NodeName:               st.NodeName,
		AgentVersion:           st.AgentVersion,
		KernelRelease:          st.KernelRelease,
		PublishIntervalSeconds: st.PublishIntervalSeconds,
		LastPublished:          timeOf(st.LastPublished),
		ObservingSince:         timeOf(st.ObservingSince),
		Collection: runtimefacts.Collection{
			State:         runtimefacts.CollectorState(st.Collection.State),
			Reason:        st.Collection.Reason,
			Message:       st.Collection.Message,
			DroppedEvents: st.Collection.DroppedEvents,
			LastDropAt:    timeOf(st.Collection.LastDropAt),
			LastResyncAt:  timeOf(st.Collection.LastResyncAt),
		},
	}
	for _, p := range st.Pods {
		w := runtimefacts.Workload{
			Identity:      runtimefacts.Identity{Kind: runtimefacts.KindPod, ID: p.UID},
			Shared:        p.NetnsShared,
			ObservedSince: timeOf(p.ObservedSince),
			BaselineAt:    timeOf(p.BaselineAt),
			Truncated:     p.Truncated,
		}
		for _, l := range p.Listeners {
			w.Listeners = append(w.Listeners, runtimefacts.Listener(l))
		}
		for _, c := range p.Connections {
			w.Connections = append(w.Connections, runtimefacts.Connection{Address: c.Address, Port: c.Port, Successes: c.Successes,
				Failures: c.Failures, LastSuccessAt: timeOf(c.LastSuccessAt), LastFailureAt: timeOf(c.LastFailureAt)})
		}
		s.Workloads = append(s.Workloads, w)
	}
	return s
}

// timeOf is the time t holds, zero when unset.
func timeOf(t *metav1.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}

// podStart is when the pod started, or when it was created if the kubelet has not reported a start yet.
func podStart(pod *corev1.Pod) time.Time {
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime.Time
	}
	return pod.CreationTimestamp.Time
}

// kernelInterfaceVerdict judges the Service port against the listeners of the pods behind its ready
// endpoints: those are the pods the Service actually sends the port's traffic to, a canary outside the
// workload included. The agent only sees TCP sockets, so a UDP or SCTP port gets no kernel opinion.
func (o *Observer) kernelInterfaceVerdict(ctx context.Context, input CollectInput, p corev1.ServicePort, ready []discoveryv1.Endpoint) runtimefacts.Verdict {
	kv := input.kernel
	if kv == nil || !runtimefacts.Observable(string(p.Protocol)) {
		return runtimefacts.Silent
	}
	if kv.failed {
		return runtimefacts.Failed
	}
	pods, floor, err := o.backingPods(ctx, input.Namespace, kv, ready)
	if err != nil {
		o.fail("kernel", fmt.Errorf("failed to list Pods: %w", err))
		return runtimefacts.Failed
	}
	return interfaceVerdict(p, pods, floor)
}

// backingPods returns the kernel view of the pods the ready endpoints name, and the coverage floor that
// applies to the verdict. A ready endpoint naming no Pod, or a Pod that is not (yet) a running, scheduled
// pod here, is one the kernel cannot speak for, so the floor rises to partial: silence can then never
// confirm an absence. When no endpoint names a Pod at all, the workload's pods stand in at that floor.
func (o *Observer) backingPods(ctx context.Context, namespace string, kv *kernelView, ready []discoveryv1.Endpoint) ([]kernelPod, runtimefacts.Coverage, error) {
	floor := runtimefacts.CoverageComplete
	uids := map[types.UID]bool{}
	for _, ep := range ready {
		if ep.TargetRef == nil || ep.TargetRef.Kind != "Pod" || ep.TargetRef.UID == "" {
			floor = runtimefacts.CoveragePartial
			continue
		}
		uids[ep.TargetRef.UID] = true
	}
	if len(uids) == 0 {
		return kv.pods, runtimefacts.CoveragePartial, nil
	}
	var list corev1.PodList
	if err := o.client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, floor, err
	}
	var pods []kernelPod
	for i := range list.Items {
		pod := &list.Items[i]
		if uids[pod.UID] && pod.Status.Phase == corev1.PodRunning && pod.Spec.NodeName != "" {
			pods = append(pods, kv.classify(pod))
		}
	}
	if len(pods) < len(uids) {
		floor = runtimefacts.CoveragePartial
	}
	return pods, floor, nil
}

// interfaceVerdict judges the port against the listeners of the ready pods given, no better than floor.
func interfaceVerdict(p corev1.ServicePort, pods []kernelPod, floor runtimefacts.Coverage) runtimefacts.Verdict {
	backing := make([]runtimefacts.Backing, len(pods))
	for i, kp := range pods {
		port, resolved := targetPort(p, kp.pod.Spec.Containers)
		backing[i] = runtimefacts.Backing{Observed: kp.Observed, Ready: podReady(kp.pod), Port: port, Resolved: resolved}
	}
	return runtimefacts.InterfaceVerdict(backing, floor)
}

// dependencyVerdict judges the target pods' outbound connections to the provider's addresses.
func (kv *kernelView) dependencyVerdict(peers []runtimefacts.Address) runtimefacts.Verdict {
	if kv == nil {
		return runtimefacts.Silent
	}
	if kv.failed {
		return runtimefacts.Failed
	}
	return kv.facts().DependencyVerdict(peers)
}

// podReady reports whether the pod's Ready condition is True.
func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// servicePeers is every address a client may connect to for the Service: each cluster IP on each Service
// port (kube-proxy rewrites the packet, not the socket) and each endpoint IP on each slice port (socket-level
// load balancing rewrites the socket).
func servicePeers(svc *corev1.Service, epSlices []discoveryv1.EndpointSlice) []runtimefacts.Address {
	peers := map[runtimefacts.Address]bool{}
	ips := svc.Spec.ClusterIPs
	if len(ips) == 0 && svc.Spec.ClusterIP != "" {
		ips = []string{svc.Spec.ClusterIP}
	}
	for _, ip := range ips {
		if ip == corev1.ClusterIPNone {
			continue
		}
		for _, p := range svc.Spec.Ports {
			peers[runtimefacts.NewAddress(ip, p.Port)] = true
		}
	}
	for _, s := range epSlices {
		for _, ep := range s.Endpoints {
			for _, addr := range ep.Addresses {
				for _, sp := range s.Ports {
					if sp.Port != nil {
						peers[runtimefacts.NewAddress(addr, *sp.Port)] = true
					}
				}
			}
		}
	}
	return slices.Collect(maps.Keys(peers))
}

// targetPods lists the workload's running, scheduled pods through its selector. A pod that is shutting
// down is leaving: what it does is not what the workload runs now, so a rollback is never judged on the
// pods of the version it undid.
func (o *Observer) targetPods(ctx context.Context, input CollectInput) ([]corev1.Pod, error) {
	sel, err := o.workloadPodSelector(ctx, input)
	if err != nil || sel == nil {
		return nil, err
	}
	var list corev1.PodList
	if err := o.client.List(ctx, &list, client.InNamespace(input.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list Pods: %w", err))
		return nil, err
	}
	pods := list.Items[:0]
	for _, p := range list.Items {
		if p.Status.Phase == corev1.PodRunning && p.Spec.NodeName != "" && p.DeletionTimestamp == nil {
			pods = append(pods, p)
		}
	}
	return pods, nil
}

// workloadPodSelector returns the selector of the workload's pods, or nil when the workload does not exist
// or selects nothing. A Job without a selector (never the case once admitted) falls back to the label the
// Job controller stamps; a CronJob's pods are those of the Jobs it controls.
func (o *Observer) workloadPodSelector(ctx context.Context, input CollectInput) (labels.Selector, error) {
	if input.WorkloadKind == workloadCronJob {
		return o.cronJobPodSelector(ctx, input)
	}
	w := o.workload(ctx, input)
	if w.err != nil || w.snap.podSelector == nil {
		return nil, w.err
	}
	sel, err := metav1.LabelSelectorAsSelector(w.snap.podSelector)
	if err != nil || sel.Empty() {
		// An empty selector would match every pod in the namespace; never attribute those to the target.
		return nil, err
	}
	return sel, nil
}

// cronJobPodSelector selects the pods of the Jobs the CronJob controls.
func (o *Observer) cronJobPodSelector(ctx context.Context, input CollectInput) (labels.Selector, error) {
	var jobs batchv1.JobList
	if err := o.client.List(ctx, &jobs, client.InNamespace(input.Namespace)); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list Jobs: %w", err))
		return nil, err
	}
	var names []string
	for _, j := range jobs.Items {
		if ref := metav1.GetControllerOf(&j); ref != nil && ref.Kind == workloadCronJob && ref.Name == input.WorkloadName {
			names = append(names, j.Name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	req, err := labels.NewRequirement(labelJobName, selection.In, names)
	if err != nil {
		return nil, err
	}
	return labels.NewSelector().Add(*req), nil
}

// observeUndeclaredPeers reports every Pacto-managed service a target pod connected to successfully that no
// declared dependency resolves to (spec section 4). Only the kernel sees these, so the observation carries
// the ebpf collector. A read error here drops the drift report, never invents one: being unable to look is
// not a contradiction.
func (o *Observer) observeUndeclaredPeers(ctx context.Context, input CollectInput, now time.Time) []evidence.Observation {
	kv := input.kernel
	if kv == nil || kv.failed {
		return nil
	}
	var pactos unversioned.PactoList
	if err := o.client.List(ctx, &pactos); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list Pacto CRs: %w", err))
		return nil
	}
	if unresolvedDependency(input.Contract, pactos.Items) {
		return nil
	}
	index := map[runtimefacts.Address]string{}
	for i := range pactos.Items {
		p := &pactos.Items[i]
		if !undeclaredCandidate(p, input) {
			continue
		}
		svc := &corev1.Service{}
		if err := o.client.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: p.Spec.Target.ServiceName}, svc); err != nil {
			if client.IgnoreNotFound(err) != nil {
				o.fail("kernel", fmt.Errorf("failed to get Service %s/%s: %w", p.Namespace, p.Spec.Target.ServiceName, err))
			}
			continue
		}
		var epSlices discoveryv1.EndpointSliceList
		if err := o.client.List(ctx, &epSlices, client.InNamespace(p.Namespace),
			client.MatchingLabels{discoveryv1.LabelServiceName: svc.Name}); err != nil {
			o.fail("kernel", fmt.Errorf("failed to list EndpointSlices for Service %s/%s: %w", p.Namespace, svc.Name, err))
			continue
		}
		for _, peer := range servicePeers(svc, epSlices.Items) {
			index[peer] = p.Status.Contract.ServiceName
		}
	}
	// The index keeps the last Pacto per address; the binding groups those addresses by service name.
	candidates := runtimefacts.Binding{Dependencies: map[string][]runtimefacts.Address{}}
	for peer, name := range index {
		candidates.Dependencies[name] = append(candidates.Dependencies[name], peer)
	}
	return kv.facts().Undeclared(candidates, evidence.Provenance{Collector: collectorKernel, DetectedAt: now})
}

// undeclaredCandidate reports whether another Pacto target could be an undeclared peer: it has a Service, a
// contract service name that is neither this service's nor a declared dependency's name, and no declared
// dependency resolves to it.
func undeclaredCandidate(p *unversioned.Pacto, input CollectInput) bool {
	if p.Status.Contract == nil || p.Spec.Target.ServiceName == "" || p.Status.Contract.ServiceName == "" {
		return false
	}
	if p.Namespace == input.Namespace && p.Spec.Target.ServiceName == input.ServiceName {
		return false // this target itself
	}
	name := p.Status.Contract.ServiceName
	if name == input.Contract.Service.Name {
		return false
	}
	for _, dep := range input.Contract.Dependencies {
		if dep.Name == name || matchesRef(dep.Ref, p.Status.Contract.ResolvedRef) {
			return false
		}
	}
	return true
}

// unresolvedDependency reports whether a declared dependency resolves to no Pacto in the list, neither by its
// ref nor by its name: a local path or other non-OCI ref, or an OCI ref no loaded sibling carries. That
// dependency may be the very service the target reached under another name, so calling any peer undeclared
// could be a false positive. The merge then reports no undeclared peer at all: drift goes unreported, never
// invented. A contract whose dependencies all resolve to siblings keeps the full report. An external
// dependency never has a sibling Pacto (this system does not run it), so it is skipped rather than
// silencing the report for every service that uses one.
func unresolvedDependency(c *contract.Contract, pactos []unversioned.Pacto) bool {
	for _, dep := range c.Dependencies {
		if dep.External {
			continue
		}
		if !slices.ContainsFunc(pactos, func(p unversioned.Pacto) bool {
			return p.Status.Contract != nil &&
				(p.Status.Contract.ServiceName == dep.Name || matchesRef(dep.Ref, p.Status.Contract.ResolvedRef))
		}) {
			return true
		}
	}
	return false
}

// TrimPod is the manager cache's transform for Pods: the operator caches every pod in the watched
// namespaces, so it keeps only the fields the kernel merge reads (identity and labels for the selector,
// the node, the container ports a named targetPort resolves to, the phase, Ready condition, start time and
// pod IPs) and drops the rest, managedFields included. Anything that is not a Pod (a deletion tombstone)
// passes through untouched.
func TrimPod(obj any) (any, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil
	}
	containers := make([]corev1.Container, len(pod.Spec.Containers))
	for i, c := range pod.Spec.Containers {
		containers[i] = corev1.Container{Name: c.Name, Ports: c.Ports}
	}
	var annotations map[string]string
	if h := pod.Annotations[mirrorAnnotation]; h != "" {
		annotations = map[string]string{mirrorAnnotation: h} // podsByUID resolves a static pod by it
	}
	return &corev1.Pod{
		TypeMeta: pod.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:              pod.Name,
			Namespace:         pod.Namespace,
			UID:               pod.UID,
			ResourceVersion:   pod.ResourceVersion,
			CreationTimestamp: pod.CreationTimestamp,
			DeletionTimestamp: pod.DeletionTimestamp,
			Labels:            pod.Labels,
			Annotations:       annotations,
			OwnerReferences:   pod.OwnerReferences,
		},
		Spec: corev1.PodSpec{NodeName: pod.Spec.NodeName, HostNetwork: pod.Spec.HostNetwork, Containers: containers},
		Status: corev1.PodStatus{
			Phase:      pod.Status.Phase,
			Conditions: pod.Status.Conditions,
			StartTime:  pod.Status.StartTime,
			PodIP:      pod.Status.PodIP,
			PodIPs:     pod.Status.PodIPs,
		},
	}, nil
}

// maxObservedCallers caps status.observedCallers; more callers than this make the coverage partial.
const maxObservedCallers = 32

// observeCallers lists the workloads the node agent saw calling the target's Service inside the dependency
// observation window (or input.CallerWindow, if longer) that no Pacto describes, named by the controller that owns their pods, and how far
// that list can be trusted. It returns nothing when kernel evidence is off or the target names no Service.
// Every read it cannot make, and every caller it cannot name, lowers the coverage instead of shortening
// the list silently: a caller Pacto cannot see is one it cannot rule out.
func (o *Observer) observeCallers(ctx context.Context, input CollectInput, now time.Time) ([]unversioned.ObservedCaller, *unversioned.CallerCoverage) {
	if !input.KernelEvidence || input.ServiceName == "" {
		return nil, nil
	}
	in, reason := o.loadCallerInputs(ctx, input)
	if reason != "" {
		return nil, &unversioned.CallerCoverage{State: unversioned.CallerCoverageUnavailable, Reason: reason}
	}
	cov := &unversioned.CallerCoverage{State: unversioned.CallerCoverageComplete}
	byUID := podsByUID(in.pods)
	snaps, freshness := nodeCoverage(cov, input, in, byUID, now)
	callers := o.resolveCallers(ctx, cov, in, byUID, runtimefacts.Callers(snaps, servicePeers(in.svc, in.epSlices), now.Add(-freshness)))
	if len(callers) > maxObservedCallers {
		callers = callers[:maxObservedCallers]
		cov.State, cov.Reason = unversioned.CallerCoveragePartial, "truncated"
	}
	return callers, cov
}

// callerInputs is what observeCallers reads from the cluster.
type callerInputs struct {
	nodes    map[string]*runtimefacts.Snapshot
	svc      *corev1.Service
	epSlices []discoveryv1.EndpointSlice
	pods     []corev1.Pod
	pactos   []unversioned.Pacto
}

// loadCallerInputs reads the node agents' snapshots, the target's Service and its EndpointSlices, every Pod
// and every Pacto. A read it cannot make returns the reason caller coverage is unavailable instead.
func (o *Observer) loadCallerInputs(ctx context.Context, input CollectInput) (callerInputs, string) {
	nodes, err := o.loadNodes(ctx, input)
	if err != nil {
		return callerInputs{}, "agent-data-unreadable"
	}
	if len(nodes) == 0 {
		return callerInputs{}, "no-agent-data"
	}
	svc := &corev1.Service{}
	if err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc); err != nil {
		return callerInputs{}, "service-unreadable"
	}
	var epSlices discoveryv1.EndpointSliceList
	if err := o.client.List(ctx, &epSlices, client.InNamespace(input.Namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: svc.Name}); err != nil {
		return callerInputs{}, "service-unreadable"
	}
	var pods corev1.PodList
	if err := o.client.List(ctx, &pods); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list Pods: %w", err))
		return callerInputs{}, "pods-unreadable"
	}
	var pactos unversioned.PactoList
	if err := o.client.List(ctx, &pactos); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list Pacto CRs: %w", err))
		return callerInputs{}, "pactos-unreadable"
	}
	return callerInputs{nodes: nodes, svc: svc, epSlices: epSlices.Items, pods: pods.Items, pactos: pactos.Items}, ""
}

// partialCoverage marks cov partial for reason, unless an earlier reason already did.
func partialCoverage(cov *unversioned.CallerCoverage, reason string) {
	if cov.State == unversioned.CallerCoverageComplete {
		cov.State, cov.Reason = unversioned.CallerCoveragePartial, reason
	}
}

// nodeCoverage grades how far the node agents' snapshots can be trusted, lowering cov for every stale,
// failed or degraded node, running pod on a node without an agent, unexempt shared network namespace and
// truncated workload. It returns the snapshots in node order and the freshness window to read callers in.
func nodeCoverage(cov *unversioned.CallerCoverage, input CollectInput, in callerInputs, byUID map[string]*corev1.Pod,
	now time.Time) ([]runtimefacts.Snapshot, time.Duration) {
	freshness := input.CallerWindow
	snaps := make([]runtimefacts.Snapshot, 0, len(in.nodes))
	for _, name := range slices.Sorted(maps.Keys(in.nodes)) {
		s := in.nodes[name]
		bound := s.StalenessBound(input.KernelEvidenceStaleness)
		freshness = max(freshness, bound)
		switch {
		case s.LastPublished.IsZero() || now.Sub(s.LastPublished) > bound:
			partialCoverage(cov, "stale-node")
		case s.Collection.State == runtimefacts.StateFailed:
			partialCoverage(cov, "collection-failed")
		case s.Collection.State != runtimefacts.StateRunning:
			partialCoverage(cov, "degraded-node")
		}
		snaps = append(snaps, *s)
	}
	for i := range in.pods {
		p := &in.pods[i]
		if p.Status.Phase == corev1.PodRunning && p.Spec.NodeName != "" && in.nodes[p.Spec.NodeName] == nil {
			partialCoverage(cov, "node-without-agent")
		}
	}
	for _, s := range snaps {
		for _, w := range s.Workloads {
			// A pod sharing the host's network namespace hides its calls among the host's, unless it runs in a
			// namespace whose pods are taken not to call workloads: the cluster's own (CNI, kube-proxy), the
			// node agent's, or one the operator was told to ignore.
			if pod := byUID[w.ID]; w.Shared && (pod == nil || !callerIgnored(input, pod.Namespace)) {
				partialCoverage(cov, "shared-netns")
			}
			if w.Truncated {
				partialCoverage(cov, "truncated")
			}
		}
	}
	return snaps, freshness
}

// resolveCallers names each caller ID by the controller at the top of its pod's owner chain, dropping
// those a Pacto describes, sorted. A caller it cannot name lowers cov.
func (o *Observer) resolveCallers(ctx context.Context, cov *unversioned.CallerCoverage, in callerInputs,
	byUID map[string]*corev1.Pod, ids []runtimefacts.Identity) []unversioned.ObservedCaller {
	seen := map[unversioned.ObservedCaller]bool{}
	for _, id := range ids {
		pod := byUID[id.ID]
		if pod == nil {
			partialCoverage(cov, "unresolved-caller")
			continue
		}
		chain, err := o.podOwners(ctx, pod)
		if err != nil {
			partialCoverage(cov, "unresolved-caller")
			continue
		}
		if slices.ContainsFunc(chain, func(w ownerRef) bool { return pactoTarget(in.pactos, pod.Namespace, w.kind, w.name) }) {
			continue
		}
		top := chain[len(chain)-1]
		seen[unversioned.ObservedCaller{Namespace: pod.Namespace, Kind: top.kind, Name: top.name, Source: collectorKernel}] = true
	}
	return slices.SortedFunc(maps.Keys(seen), func(a, b unversioned.ObservedCaller) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
}

// systemNamespaces hold the cluster's own pods (kube-proxy) that run on the host network and do not call
// workloads, so their shared network namespace does not make caller coverage partial. Only namespaces every
// cluster has belong here: a namespace that may not exist (a CNI's, like calico-system) could be created by
// anyone allowed to create namespaces, and a host-network pod there would then call unseen while coverage
// reads complete. The chart lists those in callerIgnoreNamespaces instead, where an admin can drop them.
var systemNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}

// mirrorAnnotation names, on a static pod's mirror in the API, the config hash the kubelet runs it under.
const mirrorAnnotation = "kubernetes.io/config.mirror"

// podsByUID indexes pods by the ID the node agent reports: the UID, and for a static pod's mirror the
// kubelet's config hash its cgroup carries instead, so the control plane's host-network pods on a kubeadm
// node resolve rather than count as unknown.
func podsByUID(items []corev1.Pod) map[string]*corev1.Pod {
	byUID := make(map[string]*corev1.Pod, len(items))
	for i := range items {
		p := &items[i]
		byUID[string(p.UID)] = p
		if h := p.Annotations[mirrorAnnotation]; h != "" {
			byUID[h] = p
		}
	}
	return byUID
}

// callerIgnored reports whether a host-network pod in the namespace leaves caller coverage complete: a
// system namespace, the node agent's own or one in input.CallerIgnoreNamespaces.
func callerIgnored(input CollectInput, namespace string) bool {
	return namespace == input.KernelEvidenceNamespace || slices.Contains(systemNamespaces, namespace) ||
		slices.Contains(input.CallerIgnoreNamespaces, namespace)
}

// ownerRef is one link of a pod's controller chain.
type ownerRef struct{ kind, name string }

// podOwners is the pod's controller chain, nearest first: its direct controller and, for a ReplicaSet or a Job,
// that controller's own controller (a Deployment or a CronJob). A pod no controller owns is its own chain. The
// caller is named after the last link; a Pacto on any link means the pod is that Pacto's.
func (o *Observer) podOwners(ctx context.Context, pod *corev1.Pod) ([]ownerRef, error) {
	ref := metav1.GetControllerOf(pod)
	if ref == nil {
		return []ownerRef{{"Pod", pod.Name}}, nil
	}
	chain := []ownerRef{{ref.Kind, ref.Name}}
	var parent client.Object
	switch ref.Kind {
	case workloadReplicaSet:
		parent = &appsv1.ReplicaSet{}
	case workloadJob:
		parent = &batchv1.Job{}
	default:
		return chain, nil
	}
	if err := o.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, parent); err != nil {
		return nil, err
	}
	if up := metav1.GetControllerOf(parent); up != nil {
		chain = append(chain, ownerRef{up.Kind, up.Name})
	}
	return chain, nil
}

// pactoTarget reports whether a Pacto in the namespace targets the workload.
func pactoTarget(pactos []unversioned.Pacto, namespace, kind, name string) bool {
	return slices.ContainsFunc(pactos, func(p unversioned.Pacto) bool {
		n, k := p.ResolvedWorkload()
		return p.Namespace == namespace && n == name && k == kind
	})
}

// ObservedCallers is what the last Collect found calling the target's Service (observeCallers), with each
// caller's FirstSeen carried over from prev, the list the status held before, so a caller seen again does
// not rewrite the status. A new caller is first seen at the Collect's time. Both are nil before a Collect or
// when runtime observation is off.
func (o *Observer) ObservedCallers(prev []unversioned.ObservedCaller) ([]unversioned.ObservedCaller, *unversioned.CallerCoverage) {
	if o.run == nil {
		return nil, nil
	}
	callers := slices.Clone(o.run.callers)
	for i := range callers {
		callers[i].FirstSeen = metav1.NewTime(o.run.now)
		if j := slices.IndexFunc(prev, func(p unversioned.ObservedCaller) bool {
			return p.Namespace == callers[i].Namespace && p.Kind == callers[i].Kind && p.Name == callers[i].Name && p.Source == callers[i].Source
		}); j >= 0 {
			callers[i].FirstSeen = prev[j].FirstSeen
		}
	}
	return callers, o.run.callerCoverage
}
