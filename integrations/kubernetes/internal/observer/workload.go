/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// observeWorkloadDim observes the workload dimension. Spec section 7.1 + AR7.
// Returns nil if NotFound (so engine emits EVIDENCE_MISSING); non-nil for all other cases.
func (o *Observer) observeWorkloadDim(ctx context.Context, input CollectInput, prov evidence.Provenance) *evidence.Observation {
	// Subject is the CONTRACT service name, not the k8s target (spec 7.0 rule 1).
	subj := evidence.SubjectRef{Kind: kindService, Name: input.Contract.Service.Name}

	w := o.workload(ctx, input)
	if w.err != nil {
		// observe* methods swallow NotFound (return nil), so any error here is non-NotFound.
		// Non-NotFound API error -> COLLECTION_FAILED.
		obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Failed, prov)
		return &obs
	}

	if !w.snap.WorkloadExists {
		// Workload NotFound -> emit NO observation (nil) so engine sees obs==nil -> EVIDENCE_MISSING.
		return nil
	}

	observedType := mapWorkloadKindToType(input.WorkloadKind)

	// WORKLOAD_MISMATCH is gated on WorkloadExplicit (AR7).
	if observedType != input.Contract.Workload {
		if input.WorkloadExplicit {
			// Both name AND kind were explicitly set -> mismatch is assertable.
			obs := evidence.NewWorkloadObserved(subj, observedType, prov)
			return &obs
		}
		// Non-explicit kind diff -> EVIDENCE_INSUFFICIENT.
		obs, _ := evidence.NewUnobserved(evidence.WorkloadObserved, subj, evidence.Insufficient, prov)
		return &obs
	}

	// Satisfied.
	obs := evidence.NewWorkloadObserved(subj, observedType, prov)
	return &obs
}

// observePersistenceDim observes the persistence dimension. Spec section 7.2 / B3.
// Returns nil if NotFound (so engine emits EVIDENCE_MISSING); non-nil for all other cases.
func (o *Observer) observePersistenceDim(ctx context.Context, input CollectInput, prov evidence.Provenance) *evidence.Observation {
	subj := evidence.SubjectRef{Kind: kindService, Name: input.Contract.Service.Name}

	w := o.workload(ctx, input)
	if w.err != nil {
		// observe* methods swallow NotFound (return nil), so any error here is non-NotFound.
		// Non-NotFound API error -> COLLECTION_FAILED.
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Failed, prov)
		return &obs
	}

	snapshot := w.snap
	if !snapshot.WorkloadExists {
		// Workload NotFound -> emit NO observation (nil) so engine sees obs==nil -> EVIDENCE_MISSING.
		return nil
	}

	// Classify volumes via the volumeClassifier (spec 7.2 / B3 three-bucket rule).
	class := o.classifyPersistence(snapshot)

	switch class {
	case persistenceDurable:
		// Persistent storage binding declared -> satisfied.
		obs := evidence.NewPersistenceObservation(subj, evidence.PersistenceObservation{Durable: true, Shared: sharedClaim(snapshot)}, prov)
		return &obs
	case persistenceEphemeral:
		// All ephemeral or no volumes -> contradicted. No claim means no scope to report (Shared stays nil).
		obs := evidence.NewPersistenceObserved(subj, false, prov)
		return &obs
	default: // persistenceAmbiguous
		// Any ambiguous volume -> EVIDENCE_INSUFFICIENT.
		obs, _ := evidence.NewUnobserved(evidence.PersistenceObserved, subj, evidence.Insufficient, prov)
		return &obs
	}
}

// sharedClaim reports, for a durable workload, whether one claim is mounted by several replicas (spec
// section 4). A PVC volume in the pod template is the same claim in every pod, so it is shared when more
// than one pod runs; volumeClaimTemplates alone give each replica its own claim.
func sharedClaim(snap *RuntimeSnapshot) *bool {
	if !snap.templateClaim {
		return new(false)
	}
	shared := snap.Replicas != nil && *snap.Replicas > 1 // unset replicas default to one
	return &shared
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
	case workloadJob:
		return jobScope
	case workloadCronJob:
		return scheduledScope
	default:
		return kindService
	}
}

// Observe is kept for controller back-compat (dashboard ObservedRuntime). Will be removed in a future step.
func (o *Observer) Observe(ctx context.Context, namespace, serviceName, workloadName, workloadKind string) (*RuntimeSnapshot, error) {
	snapshot := &RuntimeSnapshot{
		WorkloadKind: workloadKind,
	}

	// Observe Workload: the view Collect already read, when it read this one.
	if workloadName != "" {
		w := o.workload(ctx, CollectInput{Namespace: namespace, WorkloadName: workloadName, WorkloadKind: workloadKind})
		if w.err != nil {
			return nil, w.err
		}
		copied := *w.snap
		snapshot = &copied
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

	return snapshot, nil
}

// workloadView is the target workload as read once per Collect: the workload, persistence, health, metrics
// and kernel paths all share this one GET.
type workloadView struct {
	key  types.NamespacedName
	kind string
	snap *RuntimeSnapshot
	err  error
}

// workload returns the target workload, reading it on the first call of a Collect. A read error is recorded
// once, here, however many dimensions then report it.
func (o *Observer) workload(ctx context.Context, input CollectInput) *workloadView {
	key := types.NamespacedName{Namespace: input.Namespace, Name: input.WorkloadName}
	if key.Name == "" {
		// No workload targeted: nothing to read, and nothing exists.
		return &workloadView{key: key, kind: input.WorkloadKind, snap: &RuntimeSnapshot{WorkloadKind: input.WorkloadKind}}
	}
	if o.run != nil && o.run.workload != nil && o.run.workload.key == key && o.run.workload.kind == input.WorkloadKind {
		return o.run.workload
	}
	w := &workloadView{key: key, kind: input.WorkloadKind, snap: &RuntimeSnapshot{WorkloadKind: input.WorkloadKind}}
	if w.err = o.observeWorkload(ctx, key.Namespace, key.Name, w.kind, w.snap); w.err != nil {
		o.fail("workload", w.err)
	}
	if o.run != nil {
		o.run.workload = w
	}
	return w
}

// observeWorkload reads the workload resource and populates extended snapshot fields.
func (o *Observer) observeWorkload(ctx context.Context, namespace, name, kind string, snap *RuntimeSnapshot) error {
	key := types.NamespacedName{Namespace: namespace, Name: name}

	switch kind {
	case workloadDeployment:
		return o.observeDeployment(ctx, key, snap)
	case workloadStatefulSet:
		return o.observeStatefulSet(ctx, key, snap)
	case workloadReplicaSet:
		return o.observeReplicaSet(ctx, key, snap)
	case workloadJob:
		return o.observeJob(ctx, key, snap)
	case workloadCronJob:
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
	snap.podSelector = dep.Spec.Selector
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
	snap.podSelector = sts.Spec.Selector
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
	snap.Replicas = rs.Spec.Replicas
	snap.podSelector = rs.Spec.Selector
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
	snap.Replicas = job.Spec.Parallelism // pods that run at once, which is what can share a claim
	snap.podSelector = job.Spec.Selector
	if snap.podSelector == nil {
		// Never the case once admitted; the label the Job controller stamps on its pods stands in.
		snap.podSelector = &metav1.LabelSelector{MatchLabels: map[string]string{labelJobName: job.Name}}
	}
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
	snap.podSpec = podSpec

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
			snap.templateClaim = snap.templateClaim || writableClaim(podSpec, &vol)
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

// writableClaim reports whether a pod template PVC volume can be written: a claim opened read-only, or
// mounted read-only by every container that mounts it, is safe for any number of replicas to share, so it
// does not make the scope shared. A claim no container mounts keeps the conservative answer (writable).
func writableClaim(podSpec *corev1.PodSpec, vol *corev1.Volume) bool {
	if vol.PersistentVolumeClaim.ReadOnly {
		return false
	}
	mounted := false
	for _, containers := range [][]corev1.Container{podSpec.InitContainers, podSpec.Containers} {
		for _, c := range containers {
			for _, m := range c.VolumeMounts {
				if m.Name != vol.Name {
					continue
				}
				if !m.ReadOnly {
					return true
				}
				mounted = true
			}
		}
	}
	return !mounted
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

// getPodSpec is the target workload's pod template, nil when the workload cannot be read or does not exist.
func (o *Observer) getPodSpec(ctx context.Context, input CollectInput) *corev1.PodSpec {
	w := o.workload(ctx, input)
	if w.err != nil || !w.snap.WorkloadExists {
		return nil
	}
	return w.snap.podSpec
}
