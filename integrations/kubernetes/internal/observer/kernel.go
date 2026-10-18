/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/nodeagent"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

// Kernel merge (spec section 4). The node agent publishes one NodeRuntimeObservation per node; the operator
// maps the target's pods onto them and folds what the kernel saw into the Kubernetes observations. The
// manager's cache keeps an informer on those objects once the first List starts it, but no controller is
// triggered by them: they change every publish interval, so the periodic requeue reads them.

const (
	// collectorKernelMerged marks an observation whose verdict used kernel evidence; collectorKernel marks
	// one only the kernel could produce (an undeclared peer).
	collectorKernelMerged = collectorName + "+ebpf"
	collectorKernel       = "ebpf"

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

// loadKernelView reads the target's running pods and the node objects in the agent namespace. It returns nil
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
	// Only objects carrying the agent's labels count, and only by object name: the agent names its object
	// after its node, while status.nodeName is a field any writer could set to speak for another node. The
	// list is read-only here, so the cache hands out its own copies.
	var nodes unversioned.NodeRuntimeObservationList
	if err := o.client.List(ctx, &nodes, client.InNamespace(input.KernelEvidenceNamespace),
		client.MatchingLabels(nodeagent.Labels()), client.UnsafeDisableDeepCopy); err != nil {
		o.fail("kernel", fmt.Errorf("failed to list NodeRuntimeObservations: %w", err))
		return &kernelView{failed: true}
	}
	byNode := make(map[string]*runtimefacts.Snapshot, len(nodes.Items))
	for i := range nodes.Items {
		byNode[nodes.Items[i].Name] = snapshotOf(&nodes.Items[i].Status)
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

// targetPods lists the workload's running, scheduled pods through its selector.
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
		if p.Status.Phase == corev1.PodRunning && p.Spec.NodeName != "" {
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
