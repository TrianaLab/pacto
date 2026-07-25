/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package observer reads Kubernetes resources and produces Evidence (Collector).
package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/prober"
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
	Kind                    string
	Subject                 string
	FirstObservedNegativeAt *metav1.Time
}

// CollectInput carries the information the controller passes to Collect. See spec section 9.1.
type CollectInput struct {
	Namespace        string
	ServiceName      string
	WorkloadName     string
	WorkloadKind     string
	ContractRef      string
	WorkloadExplicit bool
	Contract         *contract.Contract
	BundleFS         fs.FS

	InterfaceBindings   []InterfaceBinding
	ConfigBindings      []unversioned.ConfigBinding
	StabilizationWindow time.Duration
	ObservationWindows  map[string]*metav1.Time
	Now                 time.Time

	// TODO(S6.4): ProbeEnabled, MetricsEnabled, InterfaceNameMatchDiscovery will be added in later steps.
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

// endpointProber defines the interface for HTTP endpoint probing.
type endpointProber interface {
	Probe(ctx context.Context, url string) prober.Result
}

// Observer is the k8s Collector.
type Observer struct {
	client client.Client
	prober endpointProber
}

// New creates a new Observer.
func New(c client.Client) *Observer {
	return &Observer{
		client: c,
		prober: prober.New(5 * time.Second),
	}
}

// Collect implements the new per-dimension collection driven by the contract. Spec section 9.1.
func (o *Observer) Collect(ctx context.Context, input CollectInput) (evidence.EvidenceSet, []ObservationWindowUpdate) {
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

	// Health producer (spec section 7.4).
	for _, cap := range input.Contract.Capabilities {
		if cap.Type == contract.CapabilityHealth {
			obs, updates := o.observeHealthDim(ctx, input, cap, prov, now)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, updates...)
		}
	}

	// Metrics producer (spec section 7.5).
	if obs, updates := o.observeMetricsDim(ctx, input, prov, now); obs != nil {
		observations = append(observations, *obs)
		windowUpdates = append(windowUpdates, updates...)
	}

	// Configurations producer (spec section 7.7).
	if len(input.Contract.Configurations) > 0 {
		cObs, cUpdates := o.observeConfigurationsDim(ctx, input, prov, now)
		observations = append(observations, cObs...)
		windowUpdates = append(windowUpdates, cUpdates...)
	}

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
	}, windowUpdates
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
			Kind:                    "interface",
			Subject:                 iface.Name,
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

// emitDependencyReachability applies stabilization and emits the appropriate observation/window-update.
// ponytail: dedupe the stabilize-then-emit-Observed-or-Insufficient pattern.
func emitDependencyReachability(
	subj evidence.SubjectRef,
	depName string,
	isNegative bool,
	existingWindow *metav1.Time,
	now time.Time,
	stabilizationWindow time.Duration,
	prov evidence.Provenance,
) (evidence.Observation, ObservationWindowUpdate) {
	outcome, updatedWindow := stabilize(existingWindow, isNegative, now, stabilizationWindow)

	var obs evidence.Observation
	if outcome == evidence.Observed {
		obs = evidence.NewDependencyReachable(subj, !isNegative, prov)
	} else {
		obs, _ = evidence.NewUnobserved(evidence.DependencyReachable, subj, outcome, prov)
	}

	update := ObservationWindowUpdate{
		Kind:                    "dependency",
		Subject:                 depName,
		FirstObservedNegativeAt: updatedWindow,
	}

	return obs, update
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
			obs, update := emitDependencyReachability(subj, dep.Name, true, input.ObservationWindows[windowKey], now, input.StabilizationWindow, prov)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, update)
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
		obs, update := emitDependencyReachability(subj, dep.Name, isNegative, input.ObservationWindows[windowKey], now, input.StabilizationWindow, prov)
		observations = append(observations, obs)
		windowUpdates = append(windowUpdates, update)
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

// observeHealthDim observes the health capability (spec section 7.4). Returns exactly one observation and any
// window updates for the assertion.
// ponytail: discriminated http binding, tier-A direct probe + tier-B READINESS-fallback, windowed 404.
func (o *Observer) observeHealthDim(ctx context.Context, input CollectInput, cap contract.Capability, prov evidence.Provenance, now time.Time) (evidence.Observation, []ObservationWindowUpdate) {
	subj := evidence.SubjectRef{Kind: "capability", Name: cap.AssertionKey()} // "health"

	// No binding -> Unsupported.
	if cap.Binding == nil {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Non-HTTP binding -> Unsupported (grpc not implemented).
	if cap.Binding.Type != contract.CapabilityBindingHTTP {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Resolve the owning interface to its Service port (B4 reuse).
	binding := findInterfaceBinding(input.InterfaceBindings, cap.Binding.Interface)
	if binding == nil {
		// Owning interface has no binding -> Unsupported (cannot resolve probe target).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Resolve the Service port number.
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error -> Failed.
			obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
			return obs, nil
		}
		// Service NotFound -> Unsupported (cannot probe).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	var targetPort int32
	found := false
	for _, p := range svc.Spec.Ports {
		if matchesServicePort(p, binding.ServicePort) {
			targetPort = p.Port
			found = true
			break
		}
	}
	if !found {
		// Bound port not found in Service -> Unsupported.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Build the probe URL (SSRF-safe via prober.BuildURL — INV-6).
	probeURL := prober.BuildURL(input.ServiceName, input.Namespace, targetPort, cap.Binding.Path)

	// Tier A: direct in-cluster probe.
	result := o.prober.Probe(ctx, probeURL)

	return handleHealthProbeResult(result, subj, cap.AssertionKey(), input, prov, now, o, ctx)
}

// handleHealthProbeResult interprets the prober result and returns the appropriate observation/window-update.
// Extracted for testability (can test result-handling logic without real HTTP).
// ponytail: tier-A probe result -> satisfied/404-windowed/insufficient, then tier-B fallback.
func handleHealthProbeResult(
	result prober.Result,
	subj evidence.SubjectRef,
	assertionKey string,
	input CollectInput,
	prov evidence.Provenance,
	now time.Time,
	obs *Observer,
	ctx context.Context,
) (evidence.Observation, []ObservationWindowUpdate) {
	windowKey := fmt.Sprintf("capability/%s", assertionKey)

	if result.Reachable {
		// Probe succeeded.
		if result.StatusCode >= 200 && result.StatusCode < 400 {
			// 2xx/3xx -> satisfied (Tier A).
			return evidence.NewCapabilityObserved(subj, true, prov), nil
		}

		if result.StatusCode == 404 {
			// Declared-path 404 -> windowed negative (spec section 7.4).
			isNegative := true
			existing := input.ObservationWindows[windowKey]
			outcome, updatedWindow := stabilize(existing, isNegative, now, input.StabilizationWindow)

			var observation evidence.Observation
			if outcome == evidence.Observed {
				// Beyond window -> confirmed absent.
				observation = evidence.NewCapabilityObserved(subj, false, prov)
			} else {
				// Within window -> Insufficient.
				observation, _ = evidence.NewUnobserved(evidence.CapabilityObserved, subj, outcome, prov)
			}

			update := ObservationWindowUpdate{
				Kind:                    "capability",
				Subject:                 assertionKey,
				FirstObservedNegativeAt: updatedWindow,
			}
			return observation, []ObservationWindowUpdate{update}
		}

		// 5xx/501/405 -> endpoint exists but unhealthy/not-serving-GET -> Insufficient (not absent).
		observation, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return observation, nil
	}

	// Direct probe failed (transport error) -> try Tier B (READINESS-probe fallback).
	if obs != nil {
		hasReadinessProbe, podReady := obs.checkReadinessProbeFallbackFromInput(ctx, input)

		if hasReadinessProbe && podReady {
			// Tier B satisfied (lower confidence).
			return evidence.NewCapabilityObserved(subj, true, prov), nil
		}
	}

	// Probe failed and no READINESS fallback -> Failed (transport error).
	observation, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
	return observation, nil
}

// checkReadinessProbeFallbackFromInput is a wrapper that resolves the Service from the input and calls
// checkReadinessProbeFallback.
func (o *Observer) checkReadinessProbeFallbackFromInput(ctx context.Context, input CollectInput) (bool, bool) {
	// Resolve the Service (we need it for checkReadinessProbeFallback).
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc)
	if err != nil {
		return false, false
	}

	// Find the service port from the first interface binding (health uses the owning interface's port).
	var servicePort int32
	if len(input.InterfaceBindings) > 0 {
		for _, p := range svc.Spec.Ports {
			if matchesServicePort(p, input.InterfaceBindings[0].ServicePort) {
				servicePort = p.Port
				break
			}
		}
	}

	if servicePort == 0 {
		return false, false
	}

	return o.checkReadinessProbeFallback(ctx, input, svc, servicePort)
}

// checkReadinessProbeFallback checks if the workload has an httpGet READINESS probe (not liveness) configured
// on the container backing the Service target port, and if the Pod is Ready. Returns (hasReadinessProbe, podReady).
// ponytail: sidecar-safe container selection via Service target port -> pod container port match.
func (o *Observer) checkReadinessProbeFallback(ctx context.Context, input CollectInput, svc *corev1.Service, servicePort int32) (bool, bool) {
	// Read the workload to get the PodSpec.
	snapshot := &RuntimeSnapshot{WorkloadKind: input.WorkloadKind}
	err := o.observeWorkload(ctx, input.Namespace, input.WorkloadName, input.WorkloadKind, snapshot)
	if err != nil || !snapshot.WorkloadExists {
		return false, false
	}

	// Read the PodSpec from the workload.
	var podSpec *corev1.PodSpec
	key := types.NamespacedName{Namespace: input.Namespace, Name: input.WorkloadName}
	switch input.WorkloadKind {
	case "Deployment":
		dep := &appsv1.Deployment{}
		if err := o.client.Get(ctx, key, dep); err == nil {
			podSpec = &dep.Spec.Template.Spec
		}
	case "StatefulSet":
		sts := &appsv1.StatefulSet{}
		if err := o.client.Get(ctx, key, sts); err == nil {
			podSpec = &sts.Spec.Template.Spec
		}
	case "ReplicaSet":
		rs := &appsv1.ReplicaSet{}
		if err := o.client.Get(ctx, key, rs); err == nil {
			podSpec = &rs.Spec.Template.Spec
		}
	case "Job":
		job := &batchv1.Job{}
		if err := o.client.Get(ctx, key, job); err == nil {
			podSpec = &job.Spec.Template.Spec
		}
	case "CronJob":
		cj := &batchv1.CronJob{}
		if err := o.client.Get(ctx, key, cj); err == nil {
			podSpec = &cj.Spec.JobTemplate.Spec.Template.Spec
		}
	}

	if podSpec == nil {
		return false, false
	}

	// Resolve the Service target port to a container port number.
	// Find the Service port definition.
	var targetContainerPort int32
	foundTargetPort := false
	for _, p := range svc.Spec.Ports {
		if p.Port == servicePort {
			// Resolve TargetPort (which can be a port number or name).
			switch p.TargetPort.Type {
			case intstr.Int:
				targetContainerPort = p.TargetPort.IntVal
			case intstr.String:
				// Named port -> resolve via container ports.
				for _, c := range podSpec.Containers {
					for _, cp := range c.Ports {
						if cp.Name == p.TargetPort.StrVal {
							targetContainerPort = cp.ContainerPort
							foundTargetPort = true
							break
						}
					}
					if foundTargetPort {
						break
					}
				}
			}
			if !foundTargetPort && p.TargetPort.Type == intstr.Int {
				targetContainerPort = p.TargetPort.IntVal
				foundTargetPort = true
			}
			break
		}
	}

	if !foundTargetPort {
		return false, false
	}

	// Find the container that exposes targetContainerPort and has an httpGet READINESS probe.
	hasHTTPGetReadiness := false
	for _, c := range podSpec.Containers {
		// Check if this container exposes the target port.
		exposesPort := false
		for _, cp := range c.Ports {
			if cp.ContainerPort == targetContainerPort {
				exposesPort = true
				break
			}
		}

		if !exposesPort {
			continue
		}

		// Check if this container has an httpGet READINESS probe (not liveness).
		if c.ReadinessProbe != nil && c.ReadinessProbe.HTTPGet != nil {
			hasHTTPGetReadiness = true
			break
		}
	}

	if !hasHTTPGetReadiness {
		return false, false
	}

	// Check if the Pod is Ready via EndpointSlice (preferred over reading pods — lower privilege).
	readyCount, err := o.countReadyEndpoints(ctx, input.Namespace, input.ServiceName, intstr.FromInt32(servicePort))
	if err != nil || readyCount <= 0 {
		return true, false
	}

	return true, true
}

// observeConfigurationsDim observes all declared configurations (spec section 7.7 / B6 Secret + B7 ConfigMap).
// ponytail: metadata-only Secret GET (INV-5); ConfigMap decode+validate+discard; stabilize() for NotFound negatives.
func (o *Observer) observeConfigurationsDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) ([]evidence.Observation, []ObservationWindowUpdate) {
	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	for _, cfg := range input.Contract.Configurations {
		subj := evidence.SubjectRef{Kind: "configuration", Name: cfg.Name}
		windowKey := "configuration/" + cfg.Name

		// Find binding for this configuration.
		binding := findConfigBinding(input.ConfigBindings, cfg.Name)
		if binding == nil {
			// No binding -> Unsupported (Unknown) for required; optional configs emit nothing per spec.
			if cfg.Required {
				obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Unsupported, prov)
				observations = append(observations, obs)
			}
			continue
		}

		// Route by binding kind.
		if binding.Kind == "Secret" {
			obs, updates := o.observeSecretConfiguration(ctx, input, cfg, binding, subj, windowKey, prov, now)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, updates...)
		} else if binding.Kind == "ConfigMap" {
			obs, updates := o.observeConfigMapConfiguration(ctx, input, cfg, binding, subj, windowKey, prov, now)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, updates...)
		}
	}

	return observations, windowUpdates
}

// findConfigBinding locates the binding for a given configuration name.
func findConfigBinding(bindings []unversioned.ConfigBinding, cfgName string) *unversioned.ConfigBinding {
	for i := range bindings {
		if bindings[i].Configuration == cfgName {
			return &bindings[i]
		}
	}
	return nil
}

// observeSecretConfiguration observes a Secret-backed configuration (B6 metadata-only). Spec section 7.7.
// ponytail: PartialObjectMetadata GET, no .Data read; presence -> Insufficient; NotFound -> windowed.
func (o *Observer) observeSecretConfiguration(
	ctx context.Context,
	input CollectInput,
	cfg contract.Configuration,
	binding *unversioned.ConfigBinding,
	subj evidence.SubjectRef,
	windowKey string,
	prov evidence.Provenance,
	now time.Time,
) (evidence.Observation, []ObservationWindowUpdate) {
	// Metadata-only GET (INV-5 — Secret VALUES never enter operator memory).
	partial := &metav1.PartialObjectMetadata{}
	partial.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	err := o.client.Get(ctx, client.ObjectKey{Namespace: input.Namespace, Name: binding.Name}, partial)

	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error (RBAC-denied / other) -> Failed.
			obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Failed, prov)
			return obs, nil
		}
		// Secret NotFound -> windowed negative.
		isNegative := true
		existing := input.ObservationWindows[windowKey]
		outcome, updatedWindow := stabilize(existing, isNegative, now, input.StabilizationWindow)

		var obs evidence.Observation
		if outcome == evidence.Observed {
			// Beyond window -> confirmed absent.
			obs = evidence.NewConfigurationPresent(subj, false, prov)
		} else {
			// Within window -> Insufficient.
			obs, _ = evidence.NewUnobserved(evidence.ConfigurationPresent, subj, outcome, prov)
		}

		update := ObservationWindowUpdate{
			Kind:                    "configuration",
			Subject:                 cfg.Name,
			FirstObservedNegativeAt: updatedWindow,
		}
		return obs, []ObservationWindowUpdate{update}
	}

	// Secret present (metadata-only) -> Insufficient (existence established, conformance unverified).
	obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
	// Reset window on positive.
	update := ObservationWindowUpdate{
		Kind:                    "configuration",
		Subject:                 cfg.Name,
		FirstObservedNegativeAt: nil,
	}
	return obs, []ObservationWindowUpdate{update}
}

// observeConfigMapConfiguration observes a ConfigMap-backed configuration (B7 decode+validate). Spec section 7.7.
// ponytail: decode key+format, validate against local schema, discard content, emit boolean result only.
func (o *Observer) observeConfigMapConfiguration(
	ctx context.Context,
	input CollectInput,
	cfg contract.Configuration,
	binding *unversioned.ConfigBinding,
	subj evidence.SubjectRef,
	windowKey string,
	prov evidence.Provenance,
	now time.Time,
) (evidence.Observation, []ObservationWindowUpdate) {
	// GET the ConfigMap.
	cm := &corev1.ConfigMap{}
	err := o.client.Get(ctx, client.ObjectKey{Namespace: input.Namespace, Name: binding.Name}, cm)

	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error -> Failed.
			obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Failed, prov)
			return obs, nil
		}
		// ConfigMap NotFound -> windowed negative (required scope).
		isNegative := true
		existing := input.ObservationWindows[windowKey]
		outcome, updatedWindow := stabilize(existing, isNegative, now, input.StabilizationWindow)

		var obs evidence.Observation
		if outcome == evidence.Observed {
			// Beyond window -> confirmed absent.
			obs = evidence.NewConfigurationPresent(subj, false, prov)
		} else {
			// Within window -> Insufficient.
			obs, _ = evidence.NewUnobserved(evidence.ConfigurationPresent, subj, outcome, prov)
		}

		update := ObservationWindowUpdate{
			Kind:                    "configuration",
			Subject:                 cfg.Name,
			FirstObservedNegativeAt: updatedWindow,
		}
		return obs, []ObservationWindowUpdate{update}
	}

	// ConfigMap exists. Without Key+Format -> Insufficient (existence-only, never full-conformance).
	if binding.Key == "" || binding.Format == "" {
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		// Reset window on positive.
		update := ObservationWindowUpdate{
			Kind:                    "configuration",
			Subject:                 cfg.Name,
			FirstObservedNegativeAt: nil,
		}
		return obs, []ObservationWindowUpdate{update}
	}

	// With Key+Format: decode+validate the named key.
	content, ok := cm.Data[binding.Key]
	if !ok {
		// Key missing in ConfigMap -> Insufficient (parse failure).
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		// Reset window on positive (ConfigMap present, just missing key).
		update := ObservationWindowUpdate{
			Kind:                    "configuration",
			Subject:                 cfg.Name,
			FirstObservedNegativeAt: nil,
		}
		return obs, []ObservationWindowUpdate{update}
	}

	// Parse the content by Format (yaml/json).
	parsed, parseErr := parseConfigContent(content, binding.Format)
	if parseErr != nil {
		// Parse failure -> Insufficient.
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		update := ObservationWindowUpdate{
			Kind:                    "configuration",
			Subject:                 cfg.Name,
			FirstObservedNegativeAt: nil,
		}
		return obs, []ObservationWindowUpdate{update}
	}

	// Validate parsed doc against the configuration's LOCAL schema.
	conforms, err := validateConfigAgainstSchema(parsed, cfg, input.BundleFS)
	if err != nil {
		// Schema remote/unresolvable -> Insufficient (collector cannot validate).
		obs, _ := evidence.NewUnobserved(evidence.ConfigurationPresent, subj, evidence.Insufficient, prov)
		update := ObservationWindowUpdate{
			Kind:                    "configuration",
			Subject:                 cfg.Name,
			FirstObservedNegativeAt: nil,
		}
		return obs, []ObservationWindowUpdate{update}
	}

	// Discard source content (INV-5). Emit only the boolean result.
	obs := evidence.NewConfigurationPresent(subj, conforms, prov)
	update := ObservationWindowUpdate{
		Kind:                    "configuration",
		Subject:                 cfg.Name,
		FirstObservedNegativeAt: nil,
	}
	return obs, []ObservationWindowUpdate{update}
}

// observeMetricsDim observes the metrics capability (spec section 7.5 / Refinement D). Returns nil if no metrics
// capability is declared, otherwise exactly one observation. No reliable operator-side negative this release.
// ponytail: discovery precedence (ServiceMonitor > annotations > named-port > contract), then active probe.
func (o *Observer) observeMetricsDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) (*evidence.Observation, []ObservationWindowUpdate) {
	// Find the metrics capability.
	var metricsCap *contract.Capability
	for i := range input.Contract.Capabilities {
		if input.Contract.Capabilities[i].Type == contract.CapabilityMetrics {
			metricsCap = &input.Contract.Capabilities[i]
			break
		}
	}

	if metricsCap == nil {
		return nil, nil
	}

	subj := evidence.SubjectRef{Kind: "capability", Name: metricsCap.AssertionKey()} // "metrics"

	// No binding -> Unsupported.
	if metricsCap.Binding == nil {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Non-HTTP binding -> Unsupported (grpc not implemented).
	if metricsCap.Binding.Type != contract.CapabilityBindingHTTP {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Resolve the owning interface to its Service port (B4 reuse).
	binding := findInterfaceBinding(input.InterfaceBindings, metricsCap.Binding.Interface)
	if binding == nil {
		// Owning interface has no binding -> Unsupported (cannot resolve probe target).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Get the Service.
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error -> Failed.
			obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
			return &obs, nil
		}
		// Service NotFound -> Unsupported (cannot probe).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Resolve the interface-bound port number.
	var boundPort int32
	found := false
	for _, p := range svc.Spec.Ports {
		if matchesServicePort(p, binding.ServicePort) {
			boundPort = p.Port
			found = true
			break
		}
	}
	if !found {
		// Bound port not found in Service -> Unsupported.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Discovery precedence (spec section 7.5): first that yields a path+port wins.
	target, method := o.discoverMetricsTarget(ctx, input.Namespace, input.ServiceName, svc, metricsCap.Binding.Path, boundPort)
	if target == nil {
		// No discovery path succeeded -> Unsupported (Unknown).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Build the probe URL (SSRF-safe via prober.BuildURL — INV-6).
	probeURL := prober.BuildURL(input.ServiceName, input.Namespace, target.port, target.path)

	// Direct in-cluster probe (metrics has no tier-B fallback).
	result := o.prober.Probe(ctx, probeURL)

	if !result.Reachable {
		// Transport error -> Failed.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
		return &obs, nil
	}

	// Reachable but not 200 -> Insufficient (no reliable negative for metrics).
	if result.StatusCode != 200 {
		// 404/410/5xx/401-403 -> Insufficient (Unknown), not a violation.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return &obs, nil
	}

	// 200 but body does not parse as Prometheus -> Insufficient.
	if !result.PrometheusParsed {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return &obs, nil
	}

	// 200 + real Prometheus content -> satisfied. Update provenance with discovery method.
	prov.Collector = fmt.Sprintf("k8s-observer/metrics:%s", method)
	obs := evidence.NewCapabilityObserved(subj, true, prov)
	return &obs, nil
}

// parseConfigContent parses a config value by format (yaml|json). Returns the parsed document as any.
// ponytail: yaml/json unmarshal into any, return parsed doc for schema validation.
func parseConfigContent(content, format string) (any, error) {
	var parsed any
	var err error
	switch format {
	case "yaml":
		err = yaml.Unmarshal([]byte(content), &parsed)
	case "json":
		err = json.Unmarshal([]byte(content), &parsed)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	return parsed, err
}

// validateConfigAgainstSchema validates a parsed config document against the configuration's local schema.
// Returns (conforms, error). error != nil means schema unresolvable (remote ref / not in bundle).
// ponytail: reuse pkg/validation compileConfigSchema pattern (santhosh-tekuri/jsonschema/v6), no new dep.
func validateConfigAgainstSchema(parsed any, cfg contract.Configuration, bundleFS fs.FS) (bool, error) {
	// Remote ref schema -> cannot resolve -> error (Insufficient).
	if cfg.Ref != "" {
		return false, fmt.Errorf("remote ref schema not resolvable locally")
	}
	if cfg.Schema == "" {
		// No schema -> cannot validate -> error.
		return false, fmt.Errorf("no schema defined")
	}

	// Parse the schema string into a JSON doc (per pkg/validation/crossfield.go compileConfigSchema pattern).
	var schemaDoc any
	if err := json.Unmarshal([]byte(cfg.Schema), &schemaDoc); err != nil {
		return false, fmt.Errorf("failed to parse schema: %w", err)
	}

	// Compile the schema.
	compiler := jsonschema.NewCompiler()
	compiler.AddResource("mem:///config-schema.json", schemaDoc) //nolint:errcheck
	schema, err := compiler.Compile("mem:///config-schema.json")
	if err != nil {
		return false, fmt.Errorf("failed to compile schema: %w", err)
	}

	// Validate the parsed doc.
	if err := schema.Validate(parsed); err != nil {
		// Validation failed -> non-conform.
		return false, nil
	}

	// Conforms.
	return true, nil
}

// metricsTarget is a discovered scrape target (path+port).
type metricsTarget struct {
	path string
	port int32
}

// discoverMetricsTarget implements the 4-step precedence (spec section 7.5): ServiceMonitor/PodMonitor >
// annotations > named-port > contract binding.path. Returns (target, method) or (nil, "") if no path succeeds.
// ponytail: precedence ladder, first valid path+port wins.
func (o *Observer) discoverMetricsTarget(ctx context.Context, namespace, serviceName string, service *corev1.Service, contractPath string, boundPort int32) (*metricsTarget, string) {
	// 1. ServiceMonitor/PodMonitor (unstructured read, extract path+port).
	if t := o.discoverFromServiceMonitor(ctx, namespace, serviceName, service); t != nil {
		return t, "servicemonitor"
	}
	if t := o.discoverFromPodMonitor(ctx, namespace, service); t != nil {
		return t, "podmonitor"
	}

	// 2. prometheus.io annotations (scrape=true + path + port).
	if t := discoverFromAnnotations(service); t != nil {
		return t, "annotation"
	}

	// 3. Named metrics port ("metrics" / "http-metrics").
	if t := discoverFromNamedPort(service, contractPath); t != nil {
		return t, "named-port"
	}

	// 4. Contract binding.path on the bound port (if path is specified).
	if contractPath == "" {
		return nil, ""
	}
	return &metricsTarget{path: contractPath, port: boundPort}, "probe"
}

// discoverFromServiceMonitor reads ServiceMonitor CRDs via unstructured (no Go dep on prometheus-operator).
// Returns path+port from the first matching endpoint, or nil if CRD absent / no match / list error.
// ponytail: unstructured List + extract endpoint path/port, skip auth fields (INV-5).
func (o *Observer) discoverFromServiceMonitor(ctx context.Context, namespace, serviceName string, service *corev1.Service) *metricsTarget {
	gvk := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitor"}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)

	if err := o.client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		// CRD not installed / NoKindMatchError -> fall through (not Failed).
		return nil
	}

	for _, item := range list.Items {
		// Check if this ServiceMonitor targets the Service via matchLabels.
		selector, found, _ := unstructured.NestedMap(item.Object, "spec", "selector", "matchLabels")
		if !found || !labelsMatch(service.Labels, selector) {
			continue
		}

		// Extract endpoints[].path + port (INV-5: skip auth fields).
		endpoints, _, _ := unstructured.NestedSlice(item.Object, "spec", "endpoints")
		for _, ep := range endpoints {
			epMap, ok := ep.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := epMap["path"].(string)
			if path == "" {
				path = "/metrics" // default
			}
			port := extractPort(epMap)
			if port > 0 {
				return &metricsTarget{path: path, port: port}
			}
		}
	}
	return nil
}

// discoverFromPodMonitor reads PodMonitor CRDs via unstructured. Returns path+port or nil.
// ponytail: unstructured List + extract podMetricsEndpoints path/port, skip auth fields.
func (o *Observer) discoverFromPodMonitor(ctx context.Context, namespace string, service *corev1.Service) *metricsTarget {
	gvk := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)

	if err := o.client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil
	}

	for _, item := range list.Items {
		// Check selector.matchLabels against Service selector (PodMonitor targets pods).
		selector, found, _ := unstructured.NestedMap(item.Object, "spec", "selector", "matchLabels")
		if !found || service.Spec.Selector == nil || !labelsMatch(service.Spec.Selector, selector) {
			continue
		}

		// Extract podMetricsEndpoints[].path + port (INV-5: skip auth).
		endpoints, _, _ := unstructured.NestedSlice(item.Object, "spec", "podMetricsEndpoints")
		for _, ep := range endpoints {
			epMap, ok := ep.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := epMap["path"].(string)
			if path == "" {
				path = "/metrics"
			}
			port := extractPort(epMap)
			if port > 0 {
				return &metricsTarget{path: path, port: port}
			}
		}
	}
	return nil
}

// extractPort extracts port from an endpoint map (int64 or string name). Returns 0 if absent/invalid.
// ponytail: handle port as number or string, safe cast.
func extractPort(epMap map[string]interface{}) int32 {
	portVal, ok := epMap["port"]
	if !ok {
		return 0
	}
	switch v := portVal.(type) {
	case int64:
		return int32(v)
	case string:
		// Named port in ServiceMonitor/PodMonitor -> cannot resolve to number without pod template.
		// Treat as unresolved (0) so this path yields nothing.
		return 0
	default:
		return 0
	}
}

// labelsMatch reports whether all selector labels are present in target with matching values.
// ponytail: subset match, selector ⊆ target.
func labelsMatch(target map[string]string, selector map[string]interface{}) bool {
	for k, v := range selector {
		vs, ok := v.(string)
		if !ok || target[k] != vs {
			return false
		}
	}
	return true
}

// discoverFromAnnotations extracts prometheus.io annotations from the Service or Pod. Returns nil if not set.
// ponytail: scrape=true + path + port from annotations.
func discoverFromAnnotations(svc *corev1.Service) *metricsTarget {
	if svc.Annotations == nil {
		return nil
	}
	if svc.Annotations["prometheus.io/scrape"] != "true" {
		return nil
	}
	path := svc.Annotations["prometheus.io/path"]
	if path == "" {
		path = "/metrics"
	}
	portStr := svc.Annotations["prometheus.io/port"]
	if portStr == "" {
		return nil
	}
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil || port <= 0 {
		return nil
	}
	return &metricsTarget{path: path, port: int32(port)}
}

// discoverFromNamedPort finds a Service port named "metrics" or "http-metrics". Returns path+port or nil.
// ponytail: scan ports for metrics/http-metrics name.
func discoverFromNamedPort(svc *corev1.Service, fallbackPath string) *metricsTarget {
	for _, p := range svc.Spec.Ports {
		if p.Name == "metrics" || p.Name == "http-metrics" {
			path := fallbackPath
			if path == "" {
				path = "/metrics"
			}
			return &metricsTarget{path: path, port: p.Port}
		}
	}
	return nil
}
