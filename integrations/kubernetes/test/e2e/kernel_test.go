//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package e2e

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/nodeagent"
)

// The kernel cases run the real reconciler against envtest with kernel evidence on. There is no node agent
// in envtest, so each case publishes the NodeRuntimeObservation the agent would have written, into the
// case's own namespace (which doubles as the agent namespace).

const kernelNode = "node-a"

// createRunningPod creates a Running, Ready pod for the workload on kernelNode, as the kubelet would report
// it, and returns its UID.
func createRunningPod(t *testing.T, ns, app string, containerPort int32) string {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: app + "-0", Namespace: ns, Labels: map[string]string{"app": app}},
		Spec: corev1.PodSpec{NodeName: kernelNode, Containers: []corev1.Container{{
			Name: "main", Image: "app:1", Ports: []corev1.ContainerPort{{ContainerPort: containerPort}},
		}}},
	}
	if err := k8sClient.Create(testCtx, pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	started := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	pod.Status = corev1.PodStatus{
		Phase:      corev1.PodRunning,
		StartTime:  &started,
		Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
	}
	if err := k8sClient.Status().Update(testCtx, pod); err != nil {
		t.Fatalf("set pod status: %v", err)
	}
	return string(pod.UID)
}

// createPodEndpointSlice is the Service's EndpointSlice with one ready endpoint naming the pod, as the
// EndpointSlice controller writes it.
func createPodEndpointSlice(t *testing.T, ns, svcName string, port int32, podUID string) {
	t.Helper()
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: svcName + "-slice", Namespace: ns, Labels: map[string]string{discoveryv1.LabelServiceName: svcName}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Port: &port}},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{"10.244.0.1"},
			Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: ns, UID: types.UID(podUID)},
		}},
	}
	if err := k8sClient.Create(testCtx, slice); err != nil {
		t.Fatalf("create endpointslice: %v", err)
	}
}

// publishNode writes the node agent's view of kernelNode for the pods given.
func publishNode(t *testing.T, ns string, published time.Time, state pactov1alpha1.CollectionState, pods ...pactov1alpha1.PodRuntimeObservation) {
	t.Helper()
	nro := &pactov1alpha1.NodeRuntimeObservation{ObjectMeta: metav1.ObjectMeta{Name: kernelNode, Namespace: ns, Labels: nodeagent.Labels()}}
	if err := k8sClient.Create(testCtx, nro); err != nil {
		t.Fatalf("create node runtime observation: %v", err)
	}
	baseline := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	for i := range pods {
		if pods[i].BaselineAt == nil {
			pods[i].BaselineAt = &baseline
		}
	}
	nro.Status = pactov1alpha1.NodeRuntimeObservationStatus{
		NodeName:               kernelNode,
		PublishIntervalSeconds: 30,
		LastPublished:          &metav1.Time{Time: published},
		Collection:             pactov1alpha1.RuntimeCollection{State: state, Reason: reasonFor(state)},
		Pods:                   pods,
	}
	if err := k8sClient.Status().Update(testCtx, nro); err != nil {
		t.Fatalf("publish node runtime observation: %v", err)
	}
}

func reasonFor(state pactov1alpha1.CollectionState) string {
	if state == pactov1alpha1.CollectionFailed {
		return "AttachFailed"
	}
	return ""
}

// requireCollector asserts the finding's evidence reference names the collector.
func requireCollector(t *testing.T, p *pactov1alpha1.Pacto, code, collector string) {
	t.Helper()
	f := findFinding(p, code)
	if f == nil || len(f.EvidenceRefs) == 0 || f.EvidenceRefs[0].Collector != collector || f.EvidenceRefs[0].DetectedAt == "" {
		t.Fatalf("finding %s: want evidence by %q with a detection time, got %+v", code, collector, f)
	}
}

func clusterIP(t *testing.T, ns, name string) string {
	t.Helper()
	svc := &corev1.Service{}
	if err := k8sClient.Get(testCtx, client.ObjectKey{Namespace: ns, Name: name}, svc); err != nil {
		t.Fatalf("get service: %v", err)
	}
	return svc.Spec.ClusterIP
}

func TestKernelEvidence(t *testing.T) {
	ifaceContract := buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})
	ifaceTarget := pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")}
	running := pactov1alpha1.CollectionRunning

	// ifaceCase is a ready Service whose one ready endpoint is a Running, Ready pod of the svc Deployment.
	ifaceCase := func(t *testing.T) (string, string) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createDeployment(t, ns, "svc", deployOpts{})
		uid := createRunningPod(t, ns, "svc", port)
		createPodEndpointSlice(t, ns, "svc", port, uid)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{Inline: ifaceContract}, Target: ifaceTarget})
		return ns, uid
	}

	// The verdict follows the endpoints: a canary outside the Deployment that serves the port satisfies the
	// interface even though the Deployment's own pod listens on nothing.
	t.Run("canary_behind_the_endpoints_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createService(t, ns, "svc", port)
		createDeployment(t, ns, "svc", deployOpts{})
		deaf := createRunningPod(t, ns, "svc", port)
		canary := createRunningPod(t, ns, "canary", port)
		createPodEndpointSlice(t, ns, "svc", port, canary)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{Inline: ifaceContract}, Target: ifaceTarget})
		publishNode(t, ns, time.Now(), running,
			pactov1alpha1.PodRuntimeObservation{UID: deaf},
			pactov1alpha1.PodRuntimeObservation{UID: canary, Listeners: []pactov1alpha1.RuntimeListener{{Port: port, Family: "IPv4", Address: "0.0.0.0"}}})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireNoFinding(t, p, "INTERFACE_ABSENT")
	})

	t.Run("listener_present_compliant", func(t *testing.T) {
		ns, uid := ifaceCase(t)
		publishNode(t, ns, time.Now(), running, pactov1alpha1.PodRuntimeObservation{
			UID: uid, Listeners: []pactov1alpha1.RuntimeListener{{Port: port, Family: "IPv4", Address: "0.0.0.0"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	// The contradiction only the kernel sees: the endpoint is Ready, the fully observed pod serves
	// nothing on the target port (a loopback-only listener does not count).
	t.Run("listener_absent_noncompliant_INTERFACE_ABSENT", func(t *testing.T) {
		ns, uid := ifaceCase(t)
		publishNode(t, ns, time.Now(), running, pactov1alpha1.PodRuntimeObservation{
			UID: uid, Listeners: []pactov1alpha1.RuntimeListener{{Port: port, Family: "IPv4", Address: "127.0.0.1"}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_INSUFFICIENT")
		backdateWindows(t, "p", ns)
		p = reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "INTERFACE_ABSENT")
		requireCollector(t, p, "INTERFACE_ABSENT", "k8s-observer+ebpf")
		requireNoIP(t, p)

		// The same cluster without kernel evidence reads the endpoints alone: Compliant.
		p = reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
	})

	t.Run("stale_node_unknown_EVIDENCE_STALE", func(t *testing.T) {
		ns, uid := ifaceCase(t)
		publishNode(t, ns, time.Now().Add(-time.Hour), running, pactov1alpha1.PodRuntimeObservation{UID: uid})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "EVIDENCE_STALE")
		requireNoFinding(t, p, "INTERFACE_ABSENT")
	})

	t.Run("failed_collection_unknown_COLLECTION_FAILED", func(t *testing.T) {
		ns, uid := ifaceCase(t)
		publishNode(t, ns, time.Now(), pactov1alpha1.CollectionFailed, pactov1alpha1.PodRuntimeObservation{UID: uid})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		requireFinding(t, p, "COLLECTION_FAILED")
		requireCollector(t, p, "COLLECTION_FAILED", "k8s-observer+ebpf")
	})

	// depCase is a ready provider (pay-svc, bound by the sibling Pacto) and a consumer workload main-svc with
	// one pod on kernelNode. It returns the namespace, the consumer pod UID and the provider's cluster IP.
	payContract := func(ns string) string {
		return buildContract(contractSpec{name: "app", deps: []depSpec{{
			name: "payments", ref: "oci://ghcr.io/e2e/" + ns + "-payments", required: true, compat: "1.x",
		}}})
	}
	depCase := func(t *testing.T) (string, string, string) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "ghcr.io/e2e/"+ns+"-payments:1.0.0", "1.0.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 1)
		createDeployment(t, ns, "main-svc", deployOpts{})
		uid := createRunningPod(t, ns, "main-svc", port)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: payContract(ns)},
			Target:      pactov1alpha1.TargetRef{ServiceName: "main-svc"},
		})
		return ns, uid, clusterIP(t, ns, "pay-svc")
	}
	t.Run("dependency_reachable_via_kernel_compliant", func(t *testing.T) {
		ns, uid, ip := depCase(t)
		publishNode(t, ns, time.Now(), running, pactov1alpha1.PodRuntimeObservation{
			UID: uid, Connections: []pactov1alpha1.RuntimeConnection{{Address: ip, Port: port, Successes: 3, LastSuccessAt: &metav1.Time{Time: time.Now()}}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusCompliant)
		requireCoverage(t, p, 1, 1)
	})

	// Endpoints say ready, but every connection the consumer made to the provider was refused.
	t.Run("dependency_refused_noncompliant_DEPENDENCY_UNREACHABLE", func(t *testing.T) {
		ns, uid, _ := depCase(t)
		publishNode(t, ns, time.Now(), running, pactov1alpha1.PodRuntimeObservation{
			UID: uid, Connections: []pactov1alpha1.RuntimeConnection{{Address: "10.244.0.1", Port: port, Failures: 5, LastFailureAt: &metav1.Time{Time: time.Now()}}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusUnknown)
		backdateWindows(t, "p", ns)
		p = reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusNonCompliant)
		requireFinding(t, p, "DEPENDENCY_UNREACHABLE")
		requireCollector(t, p, "DEPENDENCY_UNREACHABLE", "k8s-observer+ebpf")
		requireNoIP(t, p)
	})

	// The consumer talks to a Pacto-managed service it does not declare: drift, a warning only.
	t.Run("undeclared_peer_warning_DEPENDENCY_UNDECLARED", func(t *testing.T) {
		ns, uid, _ := depCase(t)
		createSiblingPacto(t, ns, "ledger", "ledger-svc", "ghcr.io/e2e/"+ns+"-ledger:2.0.0", "2.0.0")
		createService(t, ns, "ledger-svc", 5432)
		publishNode(t, ns, time.Now(), running, pactov1alpha1.PodRuntimeObservation{
			UID: uid, Connections: []pactov1alpha1.RuntimeConnection{{Address: clusterIP(t, ns, "ledger-svc"), Port: 5432, Successes: 1, LastSuccessAt: &metav1.Time{Time: time.Now()}}},
		})
		p := reconcile(t, "p", ns, reconcileOpts{kernelNamespace: ns}).pacto
		requireStatus(t, p, pactov1alpha1.ContractStatusWarning)
		requireFinding(t, p, "DEPENDENCY_UNDECLARED")
		requireCollector(t, p, "DEPENDENCY_UNDECLARED", "ebpf")
		requireNoIP(t, p)
	})
}

// TestObserverRegressions covers the observer bugs kernel evidence would otherwise contradict.
func TestObserverRegressions(t *testing.T) {
	// EndpointSlices carry the target (container) port: a Service with port 80 -> targetPort 8080 must read
	// its ready endpoint, not a false INTERFACE_ABSENT.
	t.Run("service_port_differs_from_target_port_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: ns},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "svc"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(port)}},
			},
		}
		if err := k8sClient.Create(testCtx, svc); err != nil {
			t.Fatalf("create service: %v", err)
		}
		yes, p := true, port
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta:  metav1.ObjectMeta{Name: "svc-slice", Namespace: ns, Labels: map[string]string{discoveryv1.LabelServiceName: "svc"}},
			AddressType: discoveryv1.AddressTypeIPv4,
			Ports:       []discoveryv1.EndpointPort{{Name: new("http"), Port: &p}},
			Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &yes}}},
		}
		if err := k8sClient.Create(testCtx, slice); err != nil {
			t.Fatalf("create endpointslice: %v", err)
		}
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", ifaces: []ifaceSpec{{name: "api"}}})},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: []pactov1alpha1.InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(80)}}},
		})
		got := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, got, pactov1alpha1.ContractStatusCompliant)
		requireNoFinding(t, got, "INTERFACE_ABSENT")
	})

	// A contract writes oci://registry/repo; the loader stores the sibling's resolvedRef without the scheme.
	t.Run("oci_dependency_matches_schemeless_resolved_ref_compliant", func(t *testing.T) {
		ns := newNamespace(t)
		createSiblingPacto(t, ns, "pay", "pay-svc", "ghcr.io/e2e/"+ns+"-payments:1.4.0", "1.4.0")
		createService(t, ns, "pay-svc", port)
		createEndpointSlice(t, ns, "pay-svc", port, 1)
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: buildContract(contractSpec{name: "app", deps: []depSpec{{
				name: "payments", ref: "oci://ghcr.io/e2e/" + ns + "-payments", required: true, compat: "^1.0.0",
			}}})},
			Target: pactov1alpha1.TargetRef{ServiceName: "main-svc"},
		})
		got := reconcile(t, "p", ns, reconcileOpts{}).pacto
		requireStatus(t, got, pactov1alpha1.ContractStatusCompliant)
		requireNoFinding(t, got, "OBSERVATION_UNSUPPORTED")
	})
}
