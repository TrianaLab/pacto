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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
)

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
	// TODO(S6.3): InterfaceBindings, ConfigBindings, ProbeEnabled, MetricsEnabled,
	// InterfaceNameMatchDiscovery, StabilizationWindow, ObservationWindows will be added in later steps.
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
func (o *Observer) Collect(ctx context.Context, input CollectInput) (evidence.EvidenceSet, error) {
	now := time.Now()
	prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

	// EvidenceSet.Subject is the runtime TARGET (namespace/service), distinct from per-observation assertion identity.
	subject := evidence.SubjectRef{Kind: "service", Name: fmt.Sprintf("%s/%s", input.Namespace, input.ServiceName)}
	if input.ServiceName == "" && input.WorkloadName != "" {
		subject.Name = fmt.Sprintf("%s/%s", input.Namespace, input.WorkloadName)
	}

	var observations []evidence.Observation

	// TODO(S6.3 step 1): interfaces producer (spec section 7.3).
	// TODO(S6.3 step 1): health producer (spec section 7.4).
	// TODO(S6.3 step 1): metrics producer (spec section 7.5).
	// TODO(S6.3 step 1): dependencies producer (spec section 7.6).
	// TODO(S6.3 step 1): configurations producer (spec section 7.7).

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
	}, nil
}

// observeWorkloadDim observes the workload dimension. Spec section 7.1 + AR7.
func (o *Observer) observeWorkloadDim(ctx context.Context, input CollectInput, prov evidence.Provenance) evidence.Observation {
	// Subject is the CONTRACT service name, not the k8s target (spec 7.0 rule 1).
	subj := evidence.SubjectRef{Kind: "service", Name: input.Contract.Service.Name}

	snapshot := &RuntimeSnapshot{WorkloadKind: input.WorkloadKind}
	err := o.observeWorkload(ctx, input.Namespace, input.WorkloadName, input.WorkloadKind, snapshot)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// NotFound -> EVIDENCE_MISSING (mapped by Evaluate).
			obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Unsupported, prov)
			return obs
		}
		// Non-NotFound API error -> COLLECTION_FAILED.
		obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Failed, prov)
		return obs
	}

	if !snapshot.WorkloadExists {
		// Workload GET succeeded but object not found -> EVIDENCE_MISSING.
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
		if apierrors.IsNotFound(err) {
			obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Unsupported, prov)
			return obs
		}
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Failed, prov)
		return obs
	}

	if !snapshot.WorkloadExists {
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
	case persistenceAmbiguous:
		// Any ambiguous volume -> EVIDENCE_INSUFFICIENT.
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Insufficient, prov)
		return obs
	default:
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

// CollectForTarget is DEPRECATED; kept temporarily for controller compatibility. Will be removed in S6.3 step 2.
func (o *Observer) CollectForTarget(ctx context.Context, namespace, serviceName, workloadName, workloadKind, contractRef string) (evidence.EvidenceSet, error) {
	now := time.Now()
	prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}
	subject := evidence.SubjectRef{Kind: "service", Name: fmt.Sprintf("%s/%s", namespace, serviceName)}
	if serviceName == "" && workloadName != "" {
		subject.Name = fmt.Sprintf("%s/%s", namespace, workloadName)
	}

	var observations []evidence.Observation

	// Observe Service
	if serviceName != "" {
		svc := &corev1.Service{}
		err := o.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: serviceName}, svc)
		if err != nil && client.IgnoreNotFound(err) != nil {
			return evidence.EvidenceSet{}, fmt.Errorf("failed to get service %s: %w", serviceName, err)
		}
		if err == nil {
			// Service ports → interface observations (derive from port number, mark as present)
			for _, port := range svc.Spec.Ports {
				ifaceSubj := evidence.SubjectRef{Kind: "interface", Name: fmt.Sprintf("port-%d", port.Port)}
				obs := evidence.NewInterfaceObserved(ifaceSubj, "http", true, prov)
				observations = append(observations, obs)
			}
		}
	}

	// Observe Workload
	if workloadName != "" {
		snapshot := &RuntimeSnapshot{WorkloadKind: workloadKind}
		if err := o.observeWorkload(ctx, namespace, workloadName, workloadKind, snapshot); err != nil {
			return evidence.EvidenceSet{}, err
		}

		if snapshot.WorkloadExists {
			// Workload type observation
			wlSubj := evidence.SubjectRef{Kind: "service", Name: subject.Name}
			observations = append(observations, evidence.NewWorkloadObserved(wlSubj, mapWorkloadKindToType(workloadKind), prov))

			// Persistence observation
			durable := snapshot.HasPVC
			observations = append(observations, evidence.NewPersistenceObserved(wlSubj, durable, prov))
		}
	}

	return evidence.EvidenceSet{
		Subject:      subject,
		ContractRef:  contractRef,
		Source:       "k8s",
		ObservedAt:   now,
		Observations: observations,
	}, nil
}

// Observe is DEPRECATED; kept temporarily for controller compatibility. Will be removed in S6.3 step 2.
func (o *Observer) Observe(ctx context.Context, namespace, serviceName, workloadName, workloadKind string) (*RuntimeSnapshot, error) {
	snapshot := &RuntimeSnapshot{
		WorkloadKind: workloadKind,
	}

	// Observe Service
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
	hasEphemeral := false

	for _, vol := range podSpec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			snap.HasPVC = true
			hasPersistent = true
		} else if isExplicitlyEphemeral(&vol) {
			hasEphemeral = true
			if vol.EmptyDir != nil {
				snap.HasEmptyDir = true
			}
		} else {
			hasAmbiguous = true
		}
	}

	// Classify: persistent wins, then ambiguous, then ephemeral.
	if hasPersistent {
		snap.PersistenceClass = persistenceDurable
	} else if hasAmbiguous {
		snap.PersistenceClass = persistenceAmbiguous
	} else if hasEphemeral || len(podSpec.Volumes) == 0 {
		// All ephemeral or no volumes -> ephemeral.
		snap.PersistenceClass = persistenceEphemeral
	} else {
		snap.PersistenceClass = persistenceAmbiguous
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
