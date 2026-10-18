/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package observer reads Kubernetes resources and produces Evidence (Collector).
package observer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/probe"
)

const (
	kindService       = "service"
	kindDependency    = "dependency"
	kindCapability    = "capability"
	kindConfiguration = "configuration"

	workloadDeployment  = "Deployment"
	workloadStatefulSet = "StatefulSet"
	workloadReplicaSet  = "ReplicaSet"
	workloadJob         = "Job"
	workloadCronJob     = "CronJob"

	resourceSecret    = "Secret"
	resourceConfigMap = "ConfigMap"

	formatYAML = "yaml"

	promMonitorServiceMonitor = "ServiceMonitor"
	promMonitorPodMonitor     = "PodMonitor"
	promDefaultPath           = "/metrics"
	promScrapeAnnotation      = "true"
	promGroup                 = "monitoring.coreos.com"

	collectorName  = "k8s-observer"
	scheduledScope = "scheduled"
	jobScope       = "job"
	persistentMode = "persistent"
	metricsPort    = "metrics"
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
	// BundleDigest identifies BundleFS's content (empty for inline contracts): compiled configuration
	// schemas are cached under it, so an unchanged bundle is not recompiled every reconcile.
	BundleDigest string

	InterfaceBindings   []InterfaceBinding
	ConfigBindings      []unversioned.ConfigBinding
	StabilizationWindow time.Duration
	ObservationWindows  map[string]*metav1.Time
	Now                 time.Time

	EnableMetricsObservation bool
	// EnableProbing gates the health dimension's active Tier-A HTTP probe (SSRF surface; opt-in).
	// When false, health uses only passive Tier-B signals (readiness probe + EndpointSlice Ready).
	EnableProbing bool
	// EnableInterfaceNameMatchDiscovery lets the interfaces collector resolve an unbound interface's
	// Service port by matching a Service port named after the interface — positive availability only.
	EnableInterfaceNameMatchDiscovery bool

	// KernelEvidence merges the node agent's NodeRuntimeObservations into the interface and dependency
	// observations (spec section 4). When false the output is exactly the Kubernetes-only result.
	KernelEvidence bool
	// KernelEvidenceNamespace is where the node agent publishes its NodeRuntimeObservations.
	KernelEvidenceNamespace string
	// KernelEvidenceStaleness bounds how old a node's heartbeat may be; zero means three times the
	// node object's publish interval.
	KernelEvidenceStaleness time.Duration

	// kernel is the per-reconcile kernel view Collect loads; nil when kernel evidence is off.
	kernel *kernelView
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

	// templateClaim is true when the pod template mounts a PVC directly (the same claim in every pod).
	templateClaim bool

	// podSpec and podSelector are the workload's pod template and pod selector, read with it. A CronJob
	// has no selector of its own: its pods are found through the Jobs it controls.
	podSpec     *corev1.PodSpec
	podSelector *metav1.LabelSelector
}

// endpointProber defines the interface for HTTP endpoint probing.
type endpointProber interface {
	Probe(ctx context.Context, url string) probe.Result
	ProbeProtocol(ctx context.Context, addr string) evidence.Protocol
}

// serviceAddr is the in-cluster address of a Service port: the only authority a probe ever dials (INV-6).
func serviceAddr(serviceName, namespace string, port int32) string {
	return net.JoinHostPort(serviceName+"."+namespace+".svc", strconv.Itoa(int(port)))
}

// probeURL is the SSRF-safe URL of path on a Service port (probe.URL, INV-6). A path the capability rules
// reject (structural validation refuses such a contract first) yields the empty URL, which the prober
// reports as unreachable: an unsafe path probes nothing.
func probeURL(serviceName, namespace string, port int32, path string) string {
	u, _ := probe.URL(serviceAddr(serviceName, namespace, port), path)
	return u
}

// Observer is the k8s Collector.
type Observer struct {
	client client.Client
	prober endpointProber
	// run is the memo of the Collect in progress (or the last one), nil before the first. Dimensions
	// called on their own work without it: every read then goes to the client.
	run *collectRun
}

// collectRun is what one Collect reads once and shares: the workload, each Prometheus monitor list and
// every collection failure the dimensions hit.
type collectRun struct {
	workload *workloadView
	monitors map[string]*monitorList
	failures []error
	seen     map[string]bool
}

func newCollectRun() *collectRun {
	return &collectRun{monitors: map[string]*monitorList{}, seen: map[string]bool{}}
}

// fail records a read the observer could not make. The same failure seen by several dimensions or
// assertions (one Pacto list feeds every dependency) is recorded once.
func (o *Observer) fail(dimension string, err error) {
	if o.run == nil {
		return
	}
	err = fmt.Errorf("%s: %w", dimension, err)
	if o.run.seen[err.Error()] {
		return
	}
	o.run.seen[err.Error()] = true
	o.run.failures = append(o.run.failures, err)
}

// New creates a new Observer.
func New(c client.Client) *Observer {
	return &Observer{
		client: c,
		prober: probe.New(5 * time.Second),
	}
}

// Collect implements the new per-dimension collection driven by the contract. Spec section 9.1. The error
// joins every read that failed: the evidence set is still complete (a failed read is a Failed observation),
// but the caller must not report the collection as successful.
func (o *Observer) Collect(ctx context.Context, input CollectInput) (evidence.EvidenceSet, []ObservationWindowUpdate, error) {
	o.run = newCollectRun()
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	prov := evidence.Provenance{Collector: collectorName, DetectedAt: now}

	// EvidenceSet.Subject is the runtime TARGET (namespace/service), distinct from per-observation assertion identity.
	subject := evidence.SubjectRef{Kind: kindService, Name: fmt.Sprintf("%s/%s", input.Namespace, input.ServiceName)}
	if input.ServiceName == "" && input.WorkloadName != "" {
		subject.Name = fmt.Sprintf("%s/%s", input.Namespace, input.WorkloadName)
	}

	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	// Kernel evidence is read once per reconcile and merged into the producers below (spec section 4).
	input.kernel = o.loadKernelView(ctx, input, now)

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
		if obs := o.observeWorkloadDim(ctx, input, prov); obs != nil {
			observations = append(observations, *obs)
		}
	}

	// Persistence producer (spec section 7.2 / B3). It runs for any declared state: durability is asserted
	// only for persistent state, but the scope facet is judged either way.
	if input.Contract.State != nil && input.WorkloadName != "" {
		if obs := o.observePersistenceDim(ctx, input, prov); obs != nil {
			observations = append(observations, *obs)
		}
	}

	// Peers only the kernel sees: Pacto-managed services no declared dependency resolves to.
	observations = append(observations, o.observeUndeclaredPeers(ctx, input, now)...)

	log := logf.FromContext(ctx)
	for _, err := range o.run.failures {
		log.Error(err, "Runtime evidence collection failed", "namespace", input.Namespace, "service", input.ServiceName, "workload", input.WorkloadName)
	}

	return evidence.EvidenceSet{
		Subject:      subject,
		ContractRef:  input.ContractRef,
		Source:       "k8s",
		ObservedAt:   now,
		Observations: observations,
	}, windowUpdates, errors.Join(o.run.failures...)
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

// windowed runs a negative reading through its assertion's stabilization window: beyond the window it is
// the confirmed observation, within it an Unobserved of the same kind and subject. The update carries the
// window either way.
func windowed(input CollectInput, kind, subject string, confirmed evidence.Observation, now time.Time) (evidence.Observation, []ObservationWindowUpdate) {
	outcome, first := stabilize(input.ObservationWindows[kind+"/"+subject], true, now, input.StabilizationWindow)
	update := []ObservationWindowUpdate{{Kind: kind, Subject: subject, FirstObservedNegativeAt: first}}
	if outcome == evidence.Observed {
		return confirmed, update
	}
	obs, _ := evidence.NewUnobserved(confirmed.Kind, confirmed.Subject, outcome, confirmed.Provenance)
	return obs, update
}

// resetWindow clears an assertion's stabilization window: any non-negative reading restarts it, so a later
// negative opens a fresh window instead of reading as beyond the old one.
func resetWindow(kind, subject string) []ObservationWindowUpdate {
	return []ObservationWindowUpdate{{Kind: kind, Subject: subject}}
}
