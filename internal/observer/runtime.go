/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package observer reads Kubernetes resources and produces Evidence (Collector).
package observer

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
)

// InterfaceBinding maps a contract interface to the Service port that serves it (spec section 9.3 / B4).
type InterfaceBinding struct {
	Interface   string
	ServicePort intstr.IntOrString
}

// ObservationWindowUpdate records updated stabilization state for one assertion.
type ObservationWindowUpdate struct {
	Kind    string
	Subject string
	FirstObservedNegativeAt *metav1.Time
}

// CollectInput carries the information the controller passes to Collect. See spec section 9.1.
type CollectInput struct {
	Namespace       string
	ServiceName     string
	WorkloadName    string
	WorkloadKind    string
	ContractRef     string
	WorkloadExplicit bool
	Contract        *contract.Contract
	BundleFS        fs.FS

	InterfaceBindings []InterfaceBinding
	StabilizationWindow time.Duration
	ObservationWindows map[string]*metav1.Time
	Now time.Time

	// TODO(S6.4): ConfigBindings, ProbeEnabled, MetricsEnabled, InterfaceNameMatchDiscovery will be added in later steps.
}

// RuntimeSnapshot is the internal state for k8s observations (kept for internal convenience).
type RuntimeSnapshot struct {
	ServiceExists  bool
	WorkloadExists bool
	WorkloadKind   string
	ServicePorts   []int32
	Replicas       *int32

	DeploymentStrategy      string
	PodManagementPolicy     string
	TerminationGracePeriod  *int64
	ContainerImages         []string
	HasPVC                  bool
	HasEmptyDir             bool
	HealthProbeInitialDelay *int32

	// PersistenceClass is the B3 three-bucket classification for the workload's volumes.
	PersistenceClass persistenceClass
}

// Observer is the k8s Collector.
type Observer struct {
	client client.Client
}

// New creates a new Observer.
func New(c client.Client) *Observer {
	return &Observer{client: c}
}

// Collect implements the new per-dimension collection driven by the contract. Spec section 9.1.
func (o *Observer) Collect(ctx context.Context, input CollectInput) (evidence.EvidenceSet, []ObservationWindowUpdate, error) {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

	// EvidenceSet.Subject is the runtime TARGET (namespace/service), distinct from per-observation assertion identity.
	subject := evidence.SubjectRef{Kind: "service", Name: fmt.Sprintf("%s/%s", input.Namespace, input.ServiceName)}
	if input.ServiceName == "" && input.WorkloadName != "" {
		subject.Name = fmt.Sprintf("%s/%s", input.Namespace, input.WorkloadName)
	}

	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	// Interfaces producer (spec section 7.3 / B1 + B4).
	if len(input.Contract.Interfaces) > 0 {
		iObs, iUpdates := o.observeInterfacesDim(ctx, input, prov, now)
		observations = append(observations, iObs...)
		windowUpdates = append(windowUpdates, iUpdates...)
	}

	// Dependencies producer (spec section 7.6).
	if len(input.Contract.Dependencies) > 0 {
		dObs, dUpdates := o.observeDependenciesDim(ctx, input, prov, now)
		observations = append(observations, dObs...)
		windowUpdates = append(windowUpdates, dUpdates...)
	}

	// TODO(S6.4): health producer (spec section 7.4).
	// TODO(S6.4): metrics producer (spec section 7.5).
	// TODO(S6.6): configurations producer (spec section 7.7).

	// Workload producer (spec section 7.1 + AR7).
	if input.Contract.Workload != "" && input.WorkloadName != "" {
		obs := o.observeWorkloadDim(ctx, input, prov)
		observations = append(observations, obs)
	}

	// Persistence producer (spec section 7.2 / B3).
	if input.Contract.State != nil && input.Contract.State.Persistence.Durability == "persistent" && input.WorkloadName != "" {
		obs := o.observePersistenceDim(ctx, input, prov)
		observations = append(observations, obs)
	}

	return evidence.EvidenceSet{
		Subject:      subject,
		ContractRef:  input.ContractRef,
		Source:       "k8s",
		ObservedAt:   now,
		Observations: observations,
	}, windowUpdates, nil
}

// observeWorkloadDim observes the workload dimension. Spec section 7.1 + AR7.
func (o *Observer) observeWorkloadDim(ctx context.Context, input CollectInput, prov evidence.Provenance) evidence.Observation {
	// Subject is the CONTRACT service name, not the k8s target (spec 7.0 rule 1).
	subj := evidence.SubjectRef{Kind: "service", Name: input.Contract.Service.Name}

	snapshot := &RuntimeSnapshot{WorkloadKind: input.WorkloadKind}
	err := o.observeWorkload(ctx, input.Namespace, input.WorkloadName, input.WorkloadKind, snapshot)

	if err != nil {
		// observe* methods swallow NotFound (return nil), so any error here is non-NotFound.
		// Non-NotFound API error -> COLLECTION_FAILED.
		obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Failed, prov)
		return obs
	}

	if !snapshot.WorkloadExists {
		// Workload GET succeeded (NotFound swallowed) but object not found -> EVIDENCE_MISSING.
		obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Unsupported, prov)
		return obs
	}

	observedType := mapWorkloadKindToType(input.WorkloadKind)

	// WORKLOAD_MISMATCH is gated on WorkloadExplicit (AR7).
	if observedType != input.Contract.Workload {
		if input.WorkloadExplicit {
			// Both name AND kind were explicitly set -> mismatch is assertable.
			return evidence.NewWorkloadObserved(subj, observedType, prov)
		}
		// Non-explicit kind diff -> EVIDENCE_INSUFFICIENT.
		obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Insufficient, prov)
		return obs
	}

	// Satisfied.
	return evidence.NewWorkloadObserved(subj, observedType, prov)
}

// observePersistenceDim observes the persistence dimension. Spec section 7.2 / B3.
func (o *Observer) observePersistenceDim(ctx context.Context, input CollectInput, prov evidence.Provenance) evidence.Observation {
	subj := evidence.SubjectRef{Kind: "service", Name: input.Contract.Service.Name}

	snapshot := &RuntimeSnapshot{WorkloadKind: input.WorkloadKind}
	err := o.observeWorkload(ctx, input.Namespace, input.WorkloadName, input.WorkloadKind, snapshot)

	if err != nil {
		// observe* methods swallow NotFound (return nil), so any error here is non-NotFound.
		// Non-NotFound API error -> COLLECTION_FAILED.
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Failed, prov)
		return obs
	}

	if !snapshot.WorkloadExists {
		// Workload GET succeeded (NotFound swallowed) but object not found -> EVIDENCE_MISSING.
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Unsupported, prov)
		return obs
	}

	// Classify volumes via the volumeClassifier (spec 7.2 / B3 three-bucket rule).
	class := o.classifyPersistence(snapshot)

	switch class {
	case persistenceDurable:
		// Persistent storage binding declared -> satisfied.
		return evidence.NewPersistenceObserved(subj, true, prov)
	case persistenceEphemeral:
		// All ephemeral or no volumes -> contradicted.
		return evidence.NewPersistenceObserved(subj, false, prov)
	default: // persistenceAmbiguous
		// Any ambiguous volume -> EVIDENCE_INSUFFICIENT.
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Insufficient, prov)
		return obs
	}
}

type persistenceClass int

const (
	persistenceDurable persistenceClass = iota
	persistenceEphemeral
	persistenceAmbiguous
)

// classifyPersistence returns the pre-computed persistence class from the snapshot.
func (o *Observer) classifyPersistence(snapshot *RuntimeSnapshot) persistenceClass {
	return snapshot.PersistenceClass
}


func mapWorkloadKindToType(kind string) string {
	switch kind {
	case "Job":
		return "job"
	case "CronJob":
		return "scheduled"
	default:
		return "service"
	}
}

// Observe is kept for controller back-compat (dashboard ObservedRuntime). Will be removed in a future step.
func (o *Observer) Observe(ctx context.Context, namespace, serviceName, workloadName, workloadKind string) (*RuntimeSnapshot, error) {
	snapshot := &RuntimeSnapshot{
		WorkloadKind: workloadKind,
	}

	// Observe Service (for ServicePorts, ServiceExists)
	if serviceName != "" {
		svc := &corev1.Service{}
		err := o.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: serviceName}, svc)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return nil, fmt.Errorf("failed to get service %s: %w", serviceName, err)
			}
		} else {
			snapshot.ServiceExists = true
			for _, port := range svc.Spec.Ports {
				snapshot.ServicePorts = append(snapshot.ServicePorts, port.Port)
			}
		}
	}

	// Observe Workload
	if workloadName != "" {
		if err := o.observeWorkload(ctx, namespace, workloadName, workloadKind, snapshot); err != nil {
			return nil, err
		}
	}

	return snapshot, nil
}

// observeWorkload reads the workload resource and populates extended snapshot fields.
func (o *Observer) observeWorkload(ctx context.Context, namespace, name, kind string, snap *RuntimeSnapshot) error {
	key := types.NamespacedName{Namespace: namespace, Name: name}

	switch kind {
	case "Deployment":
		return o.observeDeployment(ctx, key, snap)
	case "StatefulSet":
		return o.observeStatefulSet(ctx, key, snap)
	case "ReplicaSet":
		return o.observeReplicaSet(ctx, key, snap)
	case "Job":
		return o.observeJob(ctx, key, snap)
	case "CronJob":
		return o.observeCronJob(ctx, key, snap)
	default:
		return o.observeDeployment(ctx, key, snap)
	}
}

func (o *Observer) observeDeployment(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	dep := &appsv1.Deployment{}
	if err := o.client.Get(ctx, key, dep); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get Deployment %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	if dep.Spec.Strategy.Type != "" {
		snap.DeploymentStrategy = string(dep.Spec.Strategy.Type)
	}
	if dep.Spec.Replicas != nil {
		snap.Replicas = dep.Spec.Replicas
	}
	o.extractPodTemplateInfo(&dep.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeStatefulSet(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	sts := &appsv1.StatefulSet{}
	if err := o.client.Get(ctx, key, sts); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get StatefulSet %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	snap.PodManagementPolicy = string(sts.Spec.PodManagementPolicy)
	if sts.Spec.Replicas != nil {
		snap.Replicas = sts.Spec.Replicas
	}
	hasVCT := len(sts.Spec.VolumeClaimTemplates) > 0
	if hasVCT {
		snap.HasPVC = true
		// VolumeClaimTemplates are persistent-binding-declared (spec 7.2).
		snap.PersistenceClass = persistenceDurable
	}
	o.extractPodTemplateInfo(&sts.Spec.Template.Spec, snap)
	// If VCT declared persistent, don't let pod volumes downgrade it.
	if hasVCT {
		snap.PersistenceClass = persistenceDurable
	}
	return nil
}

func (o *Observer) observeReplicaSet(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	rs := &appsv1.ReplicaSet{}
	if err := o.client.Get(ctx, key, rs); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get ReplicaSet %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	o.extractPodTemplateInfo(&rs.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeJob(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	job := &batchv1.Job{}
	if err := o.client.Get(ctx, key, job); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get Job %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	o.extractPodTemplateInfo(&job.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeCronJob(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	cj := &batchv1.CronJob{}
	if err := o.client.Get(ctx, key, cj); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get CronJob %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	o.extractPodTemplateInfo(&cj.Spec.JobTemplate.Spec.Template.Spec, snap)
	return nil
}

// extractPodTemplateInfo reads common fields from a PodSpec into the snapshot.
func (o *Observer) extractPodTemplateInfo(podSpec *corev1.PodSpec, snap *RuntimeSnapshot) {
	// Termination grace period
	snap.TerminationGracePeriod = podSpec.TerminationGracePeriodSeconds

	// Container images
	for _, c := range podSpec.Containers {
		snap.ContainerImages = append(snap.ContainerImages, c.Image)
	}

	// Health probe initial delay (from first container's readiness or liveness probe)
	if len(podSpec.Containers) > 0 {
		c := podSpec.Containers[0]
		if c.ReadinessProbe != nil {
			delay := c.ReadinessProbe.InitialDelaySeconds
			snap.HealthProbeInitialDelay = &delay
		} else if c.LivenessProbe != nil {
			delay := c.LivenessProbe.InitialDelaySeconds
			snap.HealthProbeInitialDelay = &delay
		}
	}

	// Volume analysis (B3 three-bucket classification, spec section 7.2).
	hasPersistent := false
	hasAmbiguous := false

	for _, vol := range podSpec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			snap.HasPVC = true
			hasPersistent = true
		} else if isExplicitlyEphemeral(&vol) {
			if vol.EmptyDir != nil {
				snap.HasEmptyDir = true
			}
			// Explicitly ephemeral volume, tracked implicitly by absence of hasPersistent/hasAmbiguous
		} else {
			hasAmbiguous = true
		}
	}

	// Classify: persistent wins, then ambiguous, then ephemeral (including no volumes).
	if hasPersistent {
		snap.PersistenceClass = persistenceDurable
	} else if hasAmbiguous {
		snap.PersistenceClass = persistenceAmbiguous
	} else {
		// All explicitly ephemeral or no volumes -> ephemeral.
		snap.PersistenceClass = persistenceEphemeral
	}
}

// isExplicitlyEphemeral returns true if the volume is in the B3 explicitly-ephemeral closed set.
func isExplicitlyEphemeral(vol *corev1.Volume) bool {
	// Spec section 7.2: emptyDir, projected, configMap, secret, downwardAPI.
	return vol.EmptyDir != nil ||
		vol.Projected != nil ||
		vol.ConfigMap != nil ||
		vol.Secret != nil ||
		vol.DownwardAPI != nil
}

// stabilize applies the stabilization window logic to a single observation (spec section 9.5 / B5). It is a
// PURE function: given the existing window state, whether the current observation is negative, and the current
// time, it returns the appropriate Outcome and the updated window state.
func stabilize(existing *metav1.Time, isNegative bool, now time.Time, window time.Duration) (evidence.Outcome, *metav1.Time) {
	if !isNegative {
		// Non-negative observation -> reset the window (nil) and emit Observed (caller will stamp Present=true).
		return evidence.Observed, nil
	}

	// isNegative == true
	if existing == nil {
		// First negative observation -> start the window.
		t := metav1.NewTime(now)
		return evidence.Insufficient, &t
	}

	// Negative with an existing window -> check if we're beyond the stabilization window.
	elapsed := now.Sub(existing.Time)
	if elapsed < window {
		// Still within the window -> Insufficient (Unknown).
		return evidence.Insufficient, existing
	}

	// Beyond the window -> confirmed negative (caller will emit Observed + Present=false).
	return evidence.Observed, existing
}

// observeInterfacesDim observes all declared interfaces. Spec section 7.3 / B1 + B4.
func (o *Observer) observeInterfacesDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) ([]evidence.Observation, []ObservationWindowUpdate) {
	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	for _, iface := range input.Contract.Interfaces {
		subj := evidence.SubjectRef{Kind: "interface", Name: iface.Name}
		windowKey := fmt.Sprintf("interface/%s", iface.Name)

		// Find the binding for this interface.
		binding := findInterfaceBinding(input.InterfaceBindings, iface.Name)
		if binding == nil {
			// No binding -> OBSERVATION_UNSUPPORTED (spec section 7.3).
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		// Read the Service + EndpointSlices to determine ready endpoints.
		readyCount, err := o.countReadyEndpoints(ctx, input.Namespace, input.ServiceName, binding.ServicePort)

		if err != nil {
			// API error (non-NotFound) -> COLLECTION_FAILED.
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, evidence.Failed, prov)
			observations = append(observations, obs)
			continue
		}

		if readyCount < 0 {
			// Service NotFound or port not mappable -> EVIDENCE_MISSING / EVIDENCE_INSUFFICIENT.
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		// We have a mappable port and a ready-endpoint count.
		isNegative := readyCount == 0
		existing := input.ObservationWindows[windowKey]

		outcome, updatedWindow := stabilize(existing, isNegative, now, input.StabilizationWindow)

		if outcome == evidence.Observed {
			// Beyond window (or positive) -> emit Observed with the appropriate Present flag.
			observations = append(observations, evidence.NewInterfaceObserved(subj, iface.Type, !isNegative, prov))
		} else {
			// Insufficient (within window or first negative) -> emit Insufficient.
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, outcome, prov)
			observations = append(observations, obs)
		}

		windowUpdates = append(windowUpdates, ObservationWindowUpdate{
			Kind:    "interface",
			Subject: iface.Name,
			FirstObservedNegativeAt: updatedWindow,
		})
	}

	return observations, windowUpdates
}

// findInterfaceBinding locates the binding for a given interface name.
func findInterfaceBinding(bindings []InterfaceBinding, ifaceName string) *InterfaceBinding {
	for i := range bindings {
		if bindings[i].Interface == ifaceName {
			return &bindings[i]
		}
	}
	return nil
}

// countReadyEndpoints reads the Service and EndpointSlices for the given service and port, then returns the
// count of Ready endpoints covering that port. Returns -1 if the Service is NotFound or the port cannot be
// mapped; a non-NotFound API error is returned as error.
func (o *Observer) countReadyEndpoints(ctx context.Context, namespace, serviceName string, servicePort intstr.IntOrString) (int, error) {
	// Read the Service.
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: serviceName}, svc)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return 0, fmt.Errorf("failed to get Service %s: %w", serviceName, err)
		}
		// Service NotFound -> -1 (unmappable).
		return -1, nil
	}

	// Check if the Service is selector-backed (not ExternalName).
	if svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Selector) == 0 {
		// Selector-less / ExternalName -> unmappable.
		return -1, nil
	}

	// Resolve the service port to a target port number.
	var targetPort int32
	found := false
	for _, p := range svc.Spec.Ports {
		if matchesServicePort(p, servicePort) {
			// For counting ready endpoints, we use the service port itself (EndpointSlice ports match service ports).
			targetPort = p.Port
			found = true
			break
		}
	}
	if !found {
		// Port not found in Service -> unmappable.
		return -1, nil
	}

	// List EndpointSlices for this Service.
	slices := &discoveryv1.EndpointSliceList{}
	err = o.client.List(ctx, slices, client.InNamespace(namespace), client.MatchingLabels{
		discoveryv1.LabelServiceName: serviceName,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list EndpointSlices for Service %s: %w", serviceName, err)
	}

	if len(slices.Items) == 0 {
		// Empty slice list (freshly-created Service) -> zero ready (will be windowed).
		return 0, nil
	}

	// Count Ready endpoints covering the target port.
	readyCount := 0
	for _, slice := range slices.Items {
		// Check if this slice has a port matching our target port.
		hasPort := false
		for _, slicePort := range slice.Ports {
			if slicePort.Port != nil && *slicePort.Port == targetPort {
				hasPort = true
				break
			}
		}
		if !hasPort {
			continue
		}

		// Count ready endpoints in this slice.
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				readyCount++
			}
		}
	}

	return readyCount, nil
}

// matchesServicePort reports whether a Service port matches the given IntOrString selector.
func matchesServicePort(p corev1.ServicePort, selector intstr.IntOrString) bool {
	switch selector.Type {
	case intstr.Int:
		return p.Port == selector.IntVal
	case intstr.String:
		return p.Name == selector.StrVal
	default:
		return false
	}
}

// observeDependenciesDim observes all declared dependencies. Spec section 7.6 / B5.
// ponytail: sibling-CR matching + windowed reachability negatives.
func (o *Observer) observeDependenciesDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) ([]evidence.Observation, []ObservationWindowUpdate) {
	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	for _, dep := range input.Contract.Dependencies {
		subj := evidence.SubjectRef{Kind: "dependency", Name: dep.Name}
		windowKey := fmt.Sprintf("dependency/%s", dep.Name)

		// Resolve the dependency to an in-cluster sibling Pacto CR (spec section 7.6).
		target, err := o.resolveDependencyToSibling(ctx, input.Namespace, dep)

		if err != nil {
			// Controller-runtime client error (not a "no match" case) -> COLLECTION_FAILED.
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Failed, prov)
			observations = append(observations, obs)
			continue
		}

		if target == nil {
			// No reliable sibling match (external / not pacto-managed) -> Unsupported (Unknown), never NonCompliant.
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		if target.serviceName == "" {
			// Sibling matched but reference-only (empty spec.target.serviceName) -> Insufficient (Unknown).
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Insufficient, prov)
			observations = append(observations, obs)
			continue
		}

		// Check if the resolved Service is ExternalName -> Unsupported.
		svc := &corev1.Service{}
		err = o.client.Get(ctx, types.NamespacedName{Namespace: target.namespace, Name: target.serviceName}, svc)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				// Non-NotFound API error -> COLLECTION_FAILED.
				obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Failed, prov)
				observations = append(observations, obs)
				continue
			}
			// Service NotFound -> a negative, apply stabilization.
			existing := input.ObservationWindows[windowKey]
			outcome, updatedWindow := stabilize(existing, true, now, input.StabilizationWindow)

			if outcome == evidence.Observed {
				// Beyond window -> confirmed negative.
				observations = append(observations, evidence.NewDependencyReachable(subj, false, prov))
			} else {
				// Within window -> Insufficient.
				obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, outcome, prov)
				observations = append(observations, obs)
			}

			windowUpdates = append(windowUpdates, ObservationWindowUpdate{
				Kind:    "dependency",
				Subject: dep.Name,
				FirstObservedNegativeAt: updatedWindow,
			})
			continue
		}

		// Service exists -> check if it's ExternalName.
		if svc.Spec.Type == corev1.ServiceTypeExternalName {
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		// Count ready endpoints for the dependency Service (protocol-agnostic, using any port).
		// Reuse countReadyEndpoints with a dummy port (we count all ready endpoints).
		readyCount := o.countReadyEndpointsForService(ctx, target.namespace, target.serviceName)

		if readyCount < 0 {
			// Error listing EndpointSlices -> COLLECTION_FAILED.
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Failed, prov)
			observations = append(observations, obs)
			continue
		}

		// We have a reliable ready-endpoint count.
		isNegative := readyCount == 0
		existing := input.ObservationWindows[windowKey]

		outcome, updatedWindow := stabilize(existing, isNegative, now, input.StabilizationWindow)

		if outcome == evidence.Observed {
			// Beyond window (or positive) -> emit Observed with the appropriate Reachable flag.
			observations = append(observations, evidence.NewDependencyReachable(subj, !isNegative, prov))
		} else {
			// Insufficient (within window or first negative) -> emit Insufficient.
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, outcome, prov)
			observations = append(observations, obs)
		}

		windowUpdates = append(windowUpdates, ObservationWindowUpdate{
			Kind:    "dependency",
			Subject: dep.Name,
			FirstObservedNegativeAt: updatedWindow,
		})
	}

	return observations, windowUpdates
}

// dependencyTarget is the resolved in-cluster target for a dependency.
type dependencyTarget struct {
	namespace   string
	serviceName string
}

// resolveDependencyToSibling resolves a contract dependency to an in-cluster Pacto CR (spec section 7.6).
// Returns nil if no reliable match exists (external / not pacto-managed).
// Returns an error only for controller-runtime client failures.
// ponytail: sibling-CR identity match via status.contract (resolvedRef / serviceName+version) + spec.target.serviceName.
func (o *Observer) resolveDependencyToSibling(ctx context.Context, currentNS string, dep contract.Dependency) (*dependencyTarget, error) {
	// List all Pacto CRs. For single-namespace manager cache, this only sees CRs in the watched namespace(s).
	// Cross-namespace dependencies invisible under a single-namespace cache -> a distinct COLLECTION_FAILED per spec.
	var pactoList unversioned.PactoList
	if err := o.client.List(ctx, &pactoList); err != nil {
		return nil, fmt.Errorf("failed to list Pacto CRs: %w", err)
	}

	// Match dep.Ref / compatibility to a sibling's status.contract (resolvedRef / serviceName+version).
	// Spec section 7.6: match dep.Ref / dep.Compatibility to sibling status.contract (resolvedRef / serviceName+version),
	// then read sibling's spec.target.serviceName + namespace for Service coordinates.
	// The spec does not detail the EXACT matching rule, so I implement a reasonable interpretation:
	// - Match on resolvedRef prefix (ignoring tag/digest suffix for unversioned deps).
	// - If compatibility is specified, check semver constraint against sibling's version.

	for _, sibling := range pactoList.Items {
		if sibling.Status.Contract == nil {
			continue
		}

		// Check if the resolvedRef matches the dep.Ref.
		if !matchesRef(dep.Ref, sibling.Status.Contract.ResolvedRef) {
			continue
		}

		// If compatibility is specified, check semver constraint.
		if dep.Compatibility != "" {
			if !matchesCompatibility(dep.Compatibility, sibling.Status.Contract.Version) {
				continue
			}
		}

		// Match found -> extract target Service coordinates.
		// Check if sibling has a spec.target.serviceName (not reference-only).
		if sibling.Spec.Target.ServiceName == "" {
			// Reference-only contract -> return a target with empty serviceName (caller handles as Insufficient).
			return &dependencyTarget{namespace: sibling.Namespace, serviceName: ""}, nil
		}

		return &dependencyTarget{
			namespace:   sibling.Namespace,
			serviceName: sibling.Spec.Target.ServiceName,
		}, nil
	}

	// No matching sibling CR -> external / not pacto-managed.
	return nil, nil
}

// matchesRef reports whether a dependency ref matches a sibling's resolvedRef.
// ponytail: base-ref match ignoring tag/digest suffix.
func matchesRef(depRef, siblingRef string) bool {
	// Extract the base ref (without tag/digest) from both and compare.
	// This handles unversioned refs (oci://registry/service-pacto) matching versioned resolvedRefs
	// (oci://registry/service-pacto:1.2.3 or oci://registry/service-pacto@sha256:...).
	depBase := stripRefSuffix(depRef)
	siblingBase := stripRefSuffix(siblingRef)
	return depBase == siblingBase
}

// stripRefSuffix removes trailing :tag or @digest from an OCI ref.
// ponytail: scan from end, skip scheme's "://".
func stripRefSuffix(ref string) string {
	// Find the FIRST occurrence of ':' or '@' AFTER the scheme (oci://).
	// We scan from the end to find the last such separator.
	schemeEnd := 0
	if len(ref) >= 6 && ref[:6] == "oci://" {
		schemeEnd = 6
	}
	// Find the last '@' (digest separator).
	if idx := lastIndexAfter(ref, '@', schemeEnd); idx >= 0 {
		return ref[:idx]
	}
	// Find the last ':' after the scheme (tag separator).
	if idx := lastIndexAfter(ref, ':', schemeEnd); idx >= 0 {
		return ref[:idx]
	}
	return ref
}

// lastIndexAfter finds the last occurrence of c in s after position start.
// Returns -1 if not found.
func lastIndexAfter(s string, c byte, start int) int {
	for i := len(s) - 1; i >= start; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// matchesCompatibility reports whether a version satisfies a semver compatibility constraint.
// ponytail: minimal semver constraint check — "1.x" matches "1.2.3", exact match otherwise.
func matchesCompatibility(constraint, version string) bool {
	// Minimal implementation: support "X.x" pattern (major version match) and exact match.
	// "1.x" matches any "1.y.z", "2.x" matches any "2.y.z", etc.
	if len(constraint) >= 2 && constraint[len(constraint)-2:] == ".x" {
		// Extract major version from constraint.
		major := constraint[:len(constraint)-2]
		// Check if version starts with the same major.
		return len(version) > len(major) && version[:len(major)] == major && version[len(major)] == '.'
	}
	// Otherwise, require exact match.
	return constraint == version
}

// countReadyEndpointsForService counts ready endpoints for a Service (protocol-agnostic, any port).
// Returns -1 on error (distinct from zero ready).
// ponytail: reuse EndpointSlice listing logic, count all ready endpoints regardless of port.
func (o *Observer) countReadyEndpointsForService(ctx context.Context, namespace, serviceName string) int {
	slices := &discoveryv1.EndpointSliceList{}
	err := o.client.List(ctx, slices, client.InNamespace(namespace), client.MatchingLabels{
		discoveryv1.LabelServiceName: serviceName,
	})
	if err != nil {
		// List error -> -1 (distinct from zero ready).
		return -1
	}

	if len(slices.Items) == 0 {
		// Empty slice list (freshly-created Service) -> zero ready (will be windowed).
		return 0
	}

	// Count all ready endpoints across all slices (protocol-agnostic).
	readyCount := 0
	for _, slice := range slices.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				readyCount++
			}
		}
	}

	return readyCount
}
