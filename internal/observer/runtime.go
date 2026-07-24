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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v2/pkg/collector"
	"github.com/trianalab/pacto/v2/pkg/evidence"
)

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
}

// Observer is the k8s Collector: implements collector.Collector.
type Observer struct {
	client client.Client
}

var _ collector.Collector = (*Observer)(nil)

// New creates a new Observer.
func New(c client.Client) *Observer {
	return &Observer{client: c}
}

// Collect implements collector.Collector by observing k8s resources and emitting typed Evidence.
func (o *Observer) Collect(ctx context.Context, subject evidence.SubjectRef) (evidence.EvidenceSet, error) {
	// Subject.Name format: "namespace/serviceName" or "namespace/workloadName"
	// For now we'll need namespace+serviceName+workloadName passed separately
	// (the controller already knows these). Temporary: expose a CollectForTarget helper.
	return evidence.EvidenceSet{}, fmt.Errorf("Collect not yet implemented; use CollectForTarget")
}

// CollectForTarget collects k8s evidence for a specific service+workload target and returns an EvidenceSet.
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
				obs := evidence.NewInterfaceObserved(subject, fmt.Sprintf("port-%d", port.Port), "http", true, prov)
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
			observations = append(observations, evidence.NewWorkloadObserved(subject, mapWorkloadKindToType(workloadKind), prov))

			// Persistence observation
			durable := snapshot.HasPVC
			observations = append(observations, evidence.NewPersistenceObserved(subject, durable, prov))
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

// Observe reads the target Service and workload, returning a snapshot (backward compat).
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
	if len(sts.Spec.VolumeClaimTemplates) > 0 {
		snap.HasPVC = true
	}
	o.extractPodTemplateInfo(&sts.Spec.Template.Spec, snap)
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

	// Volume analysis
	for _, vol := range podSpec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			snap.HasPVC = true
		}
		if vol.EmptyDir != nil {
			snap.HasEmptyDir = true
		}
	}
}
