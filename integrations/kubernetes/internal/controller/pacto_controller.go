/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/google/go-containerregistry/pkg/authn"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/credentials"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/metrics"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// ContractLoader abstracts contract loading and tag listing.
type ContractLoader interface {
	Load(ctx context.Context, ociRef, inline string, authOverride *authn.AuthConfig) (*loader.LoadResult, error)
	ListTags(ctx context.Context, ociRef string, authOverride *authn.AuthConfig) ([]string, error)
}

// PactoReconciler reconciles a Pacto object.
type PactoReconciler struct {
	client.Client
	// APIReader reads straight from the API server, bypassing the manager's cache.
	// SetupWithManager wires it; only the pull Secret is read through it, so a Secret's
	// values are never held in an informer store (INV-5).
	//
	// INV-5 is module-wide, not controller-local: the dashboard reconciler reads its
	// OCI Secrets through its own APIReader for the same reason, and a typed Get on
	// EITHER cached client is enough to break it cluster-wide.
	APIReader                         client.Reader
	Scheme                            *runtime.Scheme
	Recorder                          record.EventRecorder
	Loader                            ContractLoader
	StabilizationWindow               time.Duration
	EnableMetricsObservation          bool
	EnableProbing                     bool
	EnableInterfaceNameMatchDiscovery bool
	// KernelEvidence merges the node agent's NodeRuntimeObservations (read from
	// KernelEvidenceNamespace) into the runtime evidence. Those objects are not
	// watched: they change every publish interval, so the periodic requeue reads
	// them. KernelEvidenceStaleness zero means three publish intervals.
	KernelEvidence          bool
	KernelEvidenceNamespace string
	KernelEvidenceStaleness time.Duration
	// CallerIgnoreNamespaces are extra namespaces whose host-network pods leave caller coverage complete.
	CallerIgnoreNamespaces []string
}

// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactos/finalizers,verbs=update
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactorevisions,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=pactorevisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;watch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch

func (r *PactoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the Pacto CR
	pacto := &pactov1alpha1.Pacto{}
	if err := r.Get(ctx, req.NamespacedName, pacto); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Capture the prior readiness condition before the status is reset, so
	// readiness gate transition events fire only on a change of state.
	prevReadiness := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	readinessWasUnmet := prevReadiness != nil && prevReadiness.Status == metav1.ConditionFalse

	// Same for the whole status, which resetDerivedStatus clears below: the value
	// read from the API server is the one the previous reconciliation persisted.
	// Every terminal path threads it back, so the contract status event fires and
	// the status is written only on a change. An empty contract status means
	// never reconciled.
	prev := pacto.Status.DeepCopy()

	// 2. Reset all derived status fields so no stale data survives.
	//    Fields will be repopulated by each step below.
	r.resetDerivedStatus(pacto)

	// 3-5. Load the contract, apply overrides and validate the effective contract.
	ec, failure := r.loadEffectiveContract(ctx, pacto)
	if failure != nil {
		return r.failReconciliation(ctx, pacto, failure.msg, failure.validation, prev, failure.status)
	}
	effectiveContract := ec.effective.Contract

	r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionTrue,
		pactov1alpha1.ReasonContractParsed,
		fmt.Sprintf("Contract %s v%s is valid", effectiveContract.Service.Name, effectiveContract.Service.Version))

	// 6. Populate contract-derived status fields (from effective contract)
	pacto.Status.ContractVersion = effectiveContract.Service.Version
	r.populateContractStatus(pacto, &ec.effective)

	// Mark overridden keys in configuration status
	for i := range pacto.Status.Configurations {
		pacto.Status.Configurations[i].OverriddenKeys = ec.overriddenKeys[pacto.Status.Configurations[i].Name]
	}

	// 6b. Evaluate declared readiness (separate dimension from compliance).
	//     Runs for both reference-only and target contracts.
	r.reconcileReadiness(pacto, effectiveContract, readinessWasUnmet)

	// 7. Ensure PactoRevision for current version
	revisionName, revErr := r.ensureRevision(ctx, pacto, ec.loaded)
	if revErr != nil {
		log.Error(revErr, "Failed to ensure PactoRevision")
	} else {
		pacto.Status.CurrentRevision = revisionName
	}

	// Mirroring every registry tag into a PactoRevision is NOT a step of this
	// reconcile: it costs one ListTags plus one Load per tag, and Reconcile is
	// re-entered on every write to every watched workload. RevisionMirror runs it
	// on its own ticker instead.

	// 8. Reference-only: skip runtime validation
	if pacto.IsReference() {
		r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionTrue,
			pactov1alpha1.ReasonReferenceOnly,
			fmt.Sprintf("Reference contract %s v%s is valid", effectiveContract.Service.Name, effectiveContract.Service.Version))
		pacto.Status.ContractStatus = pactov1alpha1.ContractStatusReference
		pacto.Status.Summary = &pactov1alpha1.Summary{}
		return r.finishReconciliation(ctx, pacto, prev)
	}

	// 9-15. Observe the target and judge the contract against what was observed.
	r.evaluateRuntime(ctx, pacto, ec)
	return r.finishReconciliation(ctx, pacto, prev)
}

// effectiveContract is the loaded contract with the CR's overrides applied and validated.
type effectiveContract struct {
	loaded         *loader.LoadResult
	effective      loader.LoadResult
	overriddenKeys map[string][]string
	validation     validation.ValidationResult
}

// loadFailure is why no effective contract could be produced, phrased for failReconciliation.
type loadFailure struct {
	msg        string
	validation *pactov1alpha1.ValidationResult
	status     string
}

// loadEffectiveContract resolves the pull secret, loads the contract, applies the configuration overrides
// and validates the result (steps 3 to 5). status.validation and status.resolutionPolicy are set on the way.
func (r *PactoReconciler) loadEffectiveContract(ctx context.Context, pacto *pactov1alpha1.Pacto) (*effectiveContract, *loadFailure) {
	// 3. Resolve pull secret credentials (if specified)
	var ociAuth *authn.AuthConfig
	if secretName := pacto.Spec.ContractRef.PullSecretRef; secretName != "" {
		auth, secretErr := r.resolveOCIAuth(ctx, pacto.Namespace, secretName, pacto.Spec.ContractRef.OCI)
		if secretErr != nil {
			return nil, &loadFailure{
				msg:        fmt.Sprintf("failed to read pull secret %q: %v", secretName, secretErr),
				validation: invalidAt("spec.contractRef.pullSecretRef", secretErr),
				status:     pactov1alpha1.ContractStatusUnknown,
			}
		}
		ociAuth = auth
	}

	// 4. Determine resolution policy and load the contract
	ociRef := pacto.Spec.ContractRef.OCI
	pacto.Status.ResolutionPolicy = resolutionPolicy(ociRef)

	loadResult, err := r.Loader.Load(ctx, ociRef, pacto.Spec.ContractRef.Inline, ociAuth)
	if err != nil {
		return nil, &loadFailure{msg: err.Error(), validation: invalidAt("", err), status: classifyLoadError(err)}
	}
	// Recorded before validation, so an Invalid verdict is tied to its contract too.
	pacto.Status.EvaluatedRevision = &pactov1alpha1.EvaluatedRevision{ContractDigest: loadResult.ResolvedDigest}

	// 4b. Apply configuration overrides (if specified)
	effectiveLR, overriddenKeys, overrideErr := applyOverrides(loadResult, pacto.Spec.Overrides)
	if overrideErr != nil {
		return nil, &loadFailure{
			msg:        fmt.Sprintf("override error: %s", overrideErr.Error()),
			validation: invalidAt("spec.overrides", overrideErr),
			status:     pactov1alpha1.ContractStatusInvalid,
		}
	}

	// 5. Structural + cross-field + policy validation (on effective contract)
	contractResult := validation.Validate(effectiveLR.Contract, effectiveLR.RawYAML, effectiveLR.BundleFS)
	pacto.Status.Validation = mapValidationResult(contractResult)
	if len(contractResult.Errors) > 0 {
		return nil, &loadFailure{
			msg:        formatValidationErrors(contractResult.Errors),
			validation: pacto.Status.Validation,
			status:     pactov1alpha1.ContractStatusInvalid,
		}
	}

	return &effectiveContract{loaded: loadResult, effective: effectiveLR, overriddenKeys: overriddenKeys, validation: contractResult}, nil
}

// invalidAt is the one-issue validation result of a contract that could not be obtained or overridden.
func invalidAt(path string, err error) *pactov1alpha1.ValidationResult {
	return &pactov1alpha1.ValidationResult{
		Valid:  false,
		Errors: []pactov1alpha1.ValidationIssue{{Path: path, Message: err.Error()}},
	}
}

// evaluateRuntime collects the target's runtime evidence and judges the effective contract against it
// (steps 9 to 15), filling the runtime half of the status.
func (r *PactoReconciler) evaluateRuntime(ctx context.Context, pacto *pactov1alpha1.Pacto, ec *effectiveContract) {
	effectiveContract := ec.effective.Contract
	obs := observer.New(r.Client)

	// 10. Collect runtime evidence (spec section 9.1).
	evidenceSet, windowUpdates, collectErr := obs.Collect(ctx, r.buildCollectInput(pacto, ec))
	if e := pacto.Status.EvaluatedRevision; e != nil {
		e.KernelCoverage = obs.KernelCoverage()
	}
	pacto.Status.ObservedCallers, pacto.Status.ObservedCallersCoverage = obs.ObservedCallers(pacto.Status.ObservedCallers)
	if c := pacto.Status.ObservedCallersCoverage; c != nil {
		c.RefreshSeconds = int32(r.requeueInterval(pacto) / time.Second)
	}

	// Apply window updates (spec section 9.5): upsert by (Kind, Subject); nil resets the entry; windows for
	// assertions no longer declared by the effective contract are pruned.
	r.applyObservationWindowUpdates(pacto, windowUpdates, declaredWindowKeys(effectiveContract))

	// 11-12. Lean observed runtime and resources status (backward compat for dashboard).
	obsErr := r.observeBackCompat(ctx, obs, pacto)

	// A Collect failure already covers the workload read the back-compat view shares with it.
	r.setRuntimeObservedCondition(pacto, cmp.Or(collectErr, obsErr))

	// 13. Evaluate: contract × evidence → findings + coverage; contract-only structural findings first.
	findings, cov := validation.Evaluate(*effectiveContract, evidenceSet)
	allFindings := append(ec.validation.Findings(), findings...)
	pacto.Status.Findings = mapFindings(allFindings)

	// 14. Copy EvaluationCoverage. Reported metadata: the engine charges a required
	//     assertion and emits its Unknown finding in the same branch, so a coverage
	//     gap never outranks what the findings already say.
	pacto.Status.EvaluationCoverage = &pactov1alpha1.EvaluationCoverage{
		Evaluated:  int32(cov.Evaluated),
		Required:   int32(cov.Required),
		Unverified: capUnverified(cov.Unverified),
	}

	// 15. Count the findings; rank them with the SHARED ladder. Deriving it here too is
	//     what made one contract read Warning in kubectl and Compliant in the dashboard.
	summary := summarizeFindings(allFindings)
	pacto.Status.Summary = &summary
	pacto.Status.ContractStatus = validation.DeriveStatus(allFindings, cov)
}

// buildCollectInput maps the resolved target, its bindings and the persisted observation windows onto the
// observer's input (spec section 9.1).
func (r *PactoReconciler) buildCollectInput(pacto *pactov1alpha1.Pacto, ec *effectiveContract) observer.CollectInput {
	workloadName, workloadKind := pacto.ResolvedWorkload()
	workloadRef := pacto.Spec.Target.WorkloadRef

	interfaceBindings := make([]observer.InterfaceBinding, len(pacto.Spec.Target.InterfaceBindings))
	for i, b := range pacto.Spec.Target.InterfaceBindings {
		interfaceBindings[i] = observer.InterfaceBinding{Interface: b.Interface, ServicePort: b.ServicePort}
	}

	observationWindows := make(map[string]*metav1.Time)
	for _, w := range pacto.Status.ObservationWindows {
		observationWindows[fmt.Sprintf("%s/%s", w.Kind, w.Subject)] = &w.FirstObservedNegativeAt
	}

	return observer.CollectInput{
		Namespace:                         pacto.Namespace,
		ServiceName:                       pacto.Spec.Target.ServiceName,
		WorkloadName:                      workloadName,
		WorkloadKind:                      workloadKind,
		ContractRef:                       ec.loaded.ResolvedRef,
		WorkloadExplicit:                  workloadRef != nil && workloadRef.Name != "" && workloadRef.Kind != "",
		Contract:                          ec.effective.Contract,
		BundleFS:                          ec.loaded.BundleFS,
		BundleDigest:                      ec.loaded.ResolvedDigest,
		InterfaceBindings:                 interfaceBindings,
		ConfigBindings:                    pacto.Spec.Target.ConfigBindings,
		StabilizationWindow:               r.StabilizationWindow,
		ObservationWindows:                observationWindows,
		Now:                               time.Now(),
		EnableMetricsObservation:          r.EnableMetricsObservation,
		EnableProbing:                     r.EnableProbing,
		EnableInterfaceNameMatchDiscovery: r.EnableInterfaceNameMatchDiscovery,
		KernelEvidence:                    r.KernelEvidence,
		KernelEvidenceNamespace:           r.KernelEvidenceNamespace,
		KernelEvidenceStaleness:           r.KernelEvidenceStaleness,
		CallerWindow:                      r.requeueInterval(pacto),
		CallerIgnoreNamespaces:            r.CallerIgnoreNamespaces,
	}
}

// observeBackCompat fills status.observedRuntime and status.resources, the lean runtime view the dashboard
// reads, and returns the observation error.
func (r *PactoReconciler) observeBackCompat(ctx context.Context, obs *observer.Observer, pacto *pactov1alpha1.Pacto) error {
	workloadName, workloadKind := pacto.ResolvedWorkload()
	serviceName := pacto.Spec.Target.ServiceName
	snapshot, obsErr := obs.Observe(ctx, pacto.Namespace, serviceName, workloadName, workloadKind)
	if snapshot == nil {
		snapshot = &observer.RuntimeSnapshot{}
	}
	if snapshot.WorkloadExists && pacto.Status.EvaluatedRevision != nil {
		pacto.Status.EvaluatedRevision.Workload = &pactov1alpha1.EvaluatedWorkload{
			Kind: workloadKind, Name: workloadName, UID: snapshot.WorkloadUID, Generation: snapshot.WorkloadGeneration,
		}
	}
	if snapshot.WorkloadExists {
		pacto.Status.ObservedRuntime = &pactov1alpha1.ObservedRuntime{
			WorkloadKind:                   snapshot.WorkloadKind,
			DeploymentStrategy:             snapshot.DeploymentStrategy,
			PodManagementPolicy:            snapshot.PodManagementPolicy,
			TerminationGracePeriodSeconds:  snapshot.TerminationGracePeriod,
			ContainerImages:                snapshot.ContainerImages,
			HasPVC:                         snapshot.HasPVC,
			HasEmptyDir:                    snapshot.HasEmptyDir,
			HealthProbeInitialDelaySeconds: snapshot.HealthProbeInitialDelay,
		}
	}

	if serviceName == "" && workloadName == "" {
		return obsErr
	}
	pacto.Status.Resources = &pactov1alpha1.ResourcesStatus{}
	if serviceName != "" {
		pacto.Status.Resources.Service = &pactov1alpha1.ResourceStatus{Name: serviceName, Exists: snapshot.ServiceExists}
	}
	if workloadName != "" {
		pacto.Status.Resources.Workload = &pactov1alpha1.ResourceStatus{Name: workloadName, Kind: workloadKind, Exists: snapshot.WorkloadExists}
	}
	return obsErr
}

// mapFindings copies engine findings into their status form.
func mapFindings(findings []finding.Finding) []pactov1alpha1.FindingStatus {
	out := make([]pactov1alpha1.FindingStatus, 0, len(findings))
	for _, f := range findings {
		st := pactov1alpha1.FindingStatus{
			Code:         string(f.Code),
			Severity:     string(f.Severity),
			Category:     string(f.Category),
			Subject:      fmt.Sprintf("%s/%s", f.Subject.Kind, f.Subject.Name),
			ContractPath: f.ContractPath,
			Message:      f.Message,
		}
		for _, er := range f.EvidenceRefs {
			st.EvidenceRefs = append(st.EvidenceRefs, pactov1alpha1.EvidenceRefStatus{
				Source:     er.Source,
				ObservedAt: er.ObservedAt,
				Collector:  er.Collector,
				DetectedAt: er.DetectedAt,
			})
		}
		out = append(out, st)
	}
	return out
}

// resetDerivedStatus clears all status fields that are recomputed each reconciliation.
// This prevents stale data from a previous reconciliation from surviving.
func (r *PactoReconciler) resetDerivedStatus(pacto *pactov1alpha1.Pacto) {
	pacto.Status.ContractStatus = ""
	pacto.Status.ResolutionPolicy = ""
	pacto.Status.Summary = nil
	pacto.Status.ContractVersion = ""
	pacto.Status.Contract = nil
	pacto.Status.Validation = nil
	pacto.Status.Resources = nil
	pacto.Status.Interfaces = nil
	pacto.Status.Configurations = nil
	pacto.Status.Dependencies = nil
	pacto.Status.Policies = nil
	pacto.Status.ObservedRuntime = nil
	pacto.Status.EvaluatedRevision = nil
	pacto.Status.Readiness = nil
	pacto.Status.Metadata = nil
	// Conditions are intentionally NOT wiped: they are sticky observations managed
	// by meta.SetStatusCondition (via setCondition), which preserves each condition's
	// LastTransitionTime while the status is unchanged and stamps ObservedGeneration
	// so a not-re-set condition is detectably stale. Wiping them reset every
	// LastTransitionTime to now on every reconcile.
	pacto.Status.Findings = nil
	pacto.Status.Capabilities = nil
	pacto.Status.EvaluationCoverage = nil
	// Callers are kept for their FirstSeen; until a runtime evaluation rewrites them, a coverage that was
	// reported reads unavailable, never as the complete list of an older evaluation. It keeps the refresh
	// interval, so delivery reads it as not evaluated rather than as a status the operator stopped writing.
	if c := pacto.Status.ObservedCallersCoverage; c != nil {
		pacto.Status.ObservedCallersCoverage = &pactov1alpha1.CallerCoverage{State: pactov1alpha1.CallerCoverageUnavailable, Reason: "not-evaluated", RefreshSeconds: c.RefreshSeconds}
	}
	// Preserve: CurrentRevision (set in step 7), ObservationWindows (temporal state, spec 9.5), LastReconciledAt/ObservedGeneration (set in finish)
}

// failReconciliation handles the common pattern for contract-level failures with phase-classified status
// (spec section 9.8): Invalid vs Unknown. The status parameter drives condition reason + summary.
// prev is the status persisted by the previous reconciliation; its contract status gates the
// transition event (see emitContractStatusEvent) and the whole of it gates the write (see writeStatus).
func (r *PactoReconciler) failReconciliation(ctx context.Context, pacto *pactov1alpha1.Pacto, msg string, valResult *pactov1alpha1.ValidationResult, prev *pactov1alpha1.PactoStatus, status string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	switch status {
	case pactov1alpha1.ContractStatusInvalid:
		r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionFalse,
			pactov1alpha1.ReasonContractInvalid, msg)
		pacto.Status.Summary = &pactov1alpha1.Summary{ErrorCount: 1}
		// The contract was loaded and judged invalid -> record the validation result.
		pacto.Status.Validation = valResult
	case pactov1alpha1.ContractStatusUnknown:
		r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionUnknown,
			pactov1alpha1.ReasonContractUnavailable, msg)
		pacto.Status.Summary = &pactov1alpha1.Summary{UnknownCount: 1}
		// Transient obtain-failure (spec section 9.8): the contract was never loaded, so leave
		// status.validation nil rather than asserting the contract IS invalid.
	default:
		// Fallback for unexpected status
		r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionFalse,
			pactov1alpha1.ReasonContractInvalid, msg)
		pacto.Status.Summary = &pactov1alpha1.Summary{ErrorCount: 1}
		pacto.Status.Validation = valResult
		status = pactov1alpha1.ContractStatusInvalid
	}

	pacto.Status.ContractStatus = status

	if statusErr := r.writeStatus(ctx, pacto, prev); statusErr != nil {
		log.Error(statusErr, "Failed to update status")
		return ctrl.Result{}, statusErr
	}

	eventReason := pactov1alpha1.EventContractInvalid
	if status == pactov1alpha1.ContractStatusUnknown {
		eventReason = pactov1alpha1.EventContractUnavailable
	}
	r.emitContractStatusEvent(pacto, prev.ContractStatus, eventReason, msg)

	// Emit metrics
	metrics.RecordContractStatus(pacto.Namespace, pacto.Name, pacto.Status.ContractStatus)

	return ctrl.Result{RequeueAfter: r.requeueInterval(pacto)}, nil
}

// classifyLoadError maps OCI load errors to Invalid vs Unknown (spec section 9.8).
// Only the transient obtain-failures are enumerated; everything else — malformed ref,
// corrupt bundle, unsatisfiable constraint, any error pkg/oci grows later — is Invalid.
// Listing the Invalid side too was a second closed enumeration the compiler could not
// check, so a new transient type would have missed BOTH arms and still read Invalid.
func classifyLoadError(err error) string {
	var (
		unreachable *oci.RegistryUnreachableError
		unauthed    *oci.AuthenticationError
		notFound    *oci.ArtifactNotFoundError
	)
	if errors.As(err, &unreachable) || errors.As(err, &unauthed) || errors.As(err, &notFound) {
		return pactov1alpha1.ContractStatusUnknown
	}
	return pactov1alpha1.ContractStatusInvalid // fail-closed
}

// applyObservationWindowUpdates persists window updates into status.observationWindows (spec section 9.5).
// Upsert by (Kind, Subject); a nil FirstObservedNegativeAt removes/resets the entry. Windows whose key is
// NOT in declaredKeys (the assertion set of the CURRENT effective contract) are pruned, so removing an
// assertion drops its stale window instead of leaving it to fire a premature confirmed-negative or to grow
// status unbounded under churn. Still-declared entries that had no update this cycle are preserved.
func (r *PactoReconciler) applyObservationWindowUpdates(pacto *pactov1alpha1.Pacto, updates []observer.ObservationWindowUpdate, declaredKeys map[string]bool) {
	// Build a map of current windows keyed by (Kind/Subject)
	windowMap := make(map[string]*pactov1alpha1.ObservationWindow)
	for i := range pacto.Status.ObservationWindows {
		w := &pacto.Status.ObservationWindows[i]
		key := fmt.Sprintf("%s/%s", w.Kind, w.Subject)
		windowMap[key] = w
	}

	// Apply updates
	for _, u := range updates {
		key := fmt.Sprintf("%s/%s", u.Kind, u.Subject)
		if u.FirstObservedNegativeAt == nil {
			// Reset: remove the entry
			delete(windowMap, key)
		} else {
			// Upsert
			if existing, ok := windowMap[key]; ok {
				existing.FirstObservedNegativeAt = *u.FirstObservedNegativeAt
			} else {
				windowMap[key] = &pactov1alpha1.ObservationWindow{
					Kind:                    u.Kind,
					Subject:                 u.Subject,
					FirstObservedNegativeAt: *u.FirstObservedNegativeAt,
				}
			}
		}
	}

	// Prune windows for assertions no longer declared by the contract.
	for key := range windowMap {
		if !declaredKeys[key] {
			delete(windowMap, key)
		}
	}

	// Rebuild the slice from the map
	windows := make([]pactov1alpha1.ObservationWindow, 0, len(windowMap))
	for _, w := range windowMap {
		windows = append(windows, *w)
	}
	pacto.Status.ObservationWindows = windows
}

// declaredWindowKeys returns the set of observation-window keys (Kind/Subject) for every assertion the
// effective contract currently declares. Only these dimensions ever open a window, so any status window
// whose key is absent belongs to an assertion that was removed and must be pruned.
func declaredWindowKeys(c *contract.Contract) map[string]bool {
	keys := make(map[string]bool)
	for _, iface := range c.Interfaces {
		keys["interface/"+iface.Name] = true
	}
	for _, dep := range c.Dependencies {
		keys["dependency/"+dep.Name] = true
	}
	for _, cap := range c.Capabilities {
		keys["capability/"+cap.AssertionKey()] = true
	}
	for _, cfg := range c.Configurations {
		keys["configuration/"+cfg.Name] = true
	}
	return keys
}

// contractStatusDegraded reports whether a contract status is one a promotion gate
// should block on. Compliant and Reference are the healthy terminal states; the
// empty string means "not evaluated yet" and is deliberately NOT degraded, so a
// Pacto that is born non-compliant still emits its first warning event.
func contractStatusDegraded(status string) bool {
	switch status {
	case "", pactov1alpha1.ContractStatusCompliant, pactov1alpha1.ContractStatusReference:
		return false
	default:
		return true
	}
}

// contractStatusMessage renders the event body for a completed evaluation. Summary
// is set by every path that reaches finishReconciliation, but a nil one must still
// produce an event rather than silently drop the transition.
func contractStatusMessage(pacto *pactov1alpha1.Pacto) string {
	summary := pacto.Status.Summary
	if summary == nil {
		summary = &pactov1alpha1.Summary{}
	}
	return fmt.Sprintf("ContractStatus: %s, %d errors, %d warnings",
		pacto.Status.ContractStatus, summary.ErrorCount, summary.WarningCount)
}

// emitContractStatusEvent records a contract-status event only when the status
// CHANGED since the reconciliation that last persisted it (prev, captured before
// resetDerivedStatus wiped it). The controller watches Deployments, ReplicaSets,
// StatefulSets, Jobs, CronJobs and Services, so an unguarded event fires once per
// workload status write — a rolling update alone produces dozens. warnReason is
// the reason for the degraded direction; recovery always uses EventContractRecovered.
// This mirrors the readiness gate guard (see reconcileReadiness).
func (r *PactoReconciler) emitContractStatusEvent(pacto *pactov1alpha1.Pacto, prev, warnReason, msg string) {
	current := pacto.Status.ContractStatus
	switch {
	case contractStatusDegraded(current) && current != prev:
		// Includes escalation between degraded states (Warning -> NonCompliant).
		r.Recorder.Event(pacto, corev1.EventTypeWarning, warnReason, msg)
	case !contractStatusDegraded(current) && contractStatusDegraded(prev):
		r.Recorder.Event(pacto, corev1.EventTypeNormal, pactov1alpha1.EventContractRecovered, msg)
	}
}

// finishReconciliation sets final metadata, persists status, and emits metrics.
// prev is the status persisted by the previous reconciliation; its contract
// status gates the transition event (see emitContractStatusEvent) and the whole
// of it gates the write (see writeStatus).
func (r *PactoReconciler) finishReconciliation(ctx context.Context, pacto *pactov1alpha1.Pacto, prev *pactov1alpha1.PactoStatus) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if statusErr := r.writeStatus(ctx, pacto, prev); statusErr != nil {
		log.Error(statusErr, "Failed to update status")
		return ctrl.Result{}, statusErr
	}

	r.emitContractStatusEvent(pacto, prev.ContractStatus,
		pactov1alpha1.EventValidationFailed, contractStatusMessage(pacto))

	// Emit Prometheus metrics
	metrics.RecordContractStatus(pacto.Namespace, pacto.Name, pacto.Status.ContractStatus)

	log.Info("Reconciliation complete", "contractStatus", pacto.Status.ContractStatus)
	return ctrl.Result{RequeueAfter: r.requeueInterval(pacto)}, nil
}

// writeStatus persists the status only when it differs from prev, the status the
// previous reconciliation persisted, or when lastReconciledAt is older than half
// the check interval. Reconcile is re-entered on every write to every watched
// workload, so stamping lastReconciledAt unconditionally wrote the status on each
// of those events. The heartbeat keeps lastReconciledAt within one check interval
// of now, which is what delivery and the fleet read as a live operator.
func (r *PactoReconciler) writeStatus(ctx context.Context, pacto *pactov1alpha1.Pacto, prev *pactov1alpha1.PactoStatus) error {
	pacto.Status.ObservedGeneration = pacto.Generation
	now := metav1.Now()
	if prev != nil && prev.LastReconciledAt != nil && now.Sub(prev.LastReconciledAt.Time) < r.requeueInterval(pacto)/2 {
		pacto.Status.LastReconciledAt = prev.LastReconciledAt
		if equality.Semantic.DeepEqual(&pacto.Status, prev) {
			return nil
		}
	}
	pacto.Status.LastReconciledAt = &now
	return r.Status().Update(ctx, pacto)
}

func (r *PactoReconciler) requeueInterval(pacto *pactov1alpha1.Pacto) time.Duration {
	if pacto.Spec.CheckIntervalSeconds > 0 {
		return time.Duration(pacto.Spec.CheckIntervalSeconds) * time.Second
	}
	return 5 * time.Minute
}

// resolutionPolicy determines the OCI resolution policy from the ref shape.
// Returns empty string for inline contracts (no OCI ref).
func resolutionPolicy(ociRef string) string {
	if ociRef == "" {
		return ""
	}
	if oci.HasExplicitTag(ociRef) {
		if strings.Contains(ociRef, "@") {
			return pactov1alpha1.ResolutionPolicyPinnedDigest
		}
		return pactov1alpha1.ResolutionPolicyPinnedTag
	}
	return pactov1alpha1.ResolutionPolicyLatest
}

// --- Override application ---

// resolveOCIAuth reads a Secret and extracts OCI registry credentials.
// Supports opaque secrets (token or username+password) and kubernetes.io/dockerconfigjson secrets.
// For dockerconfigjson secrets, the registry is extracted from the OCI reference to select the
// matching auth entry.
// The read goes through APIReader (uncached, straight to the API server): a typed Get on
// the manager's client would lazily start the very Secret informer the metadata-only watch
// exists to avoid.
func (r *PactoReconciler) resolveOCIAuth(ctx context.Context, namespace, secretName, ociRef string) (*authn.AuthConfig, error) {
	secret := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret); err != nil {
		return nil, fmt.Errorf("secret %q not found: %w", secretName, err)
	}

	registry := credentials.RegistryFromRef(ociRef)
	return credentials.FromSecret(secret, registry)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PactoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.APIReader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		// Generation only: a status write, the operator's own included, is not a
		// reason to reconcile again. Without the filter every write re-entered
		// Reconcile at once, which is why statuses were written in pairs.
		For(&pactov1alpha1.Pacto{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&pactov1alpha1.PactoRevision{}).
		Watches(&corev1.Service{}, enqueueForTarget(mgr.GetClient())).
		// Metadata-only: the mapper reads name/namespace, and a typed watch would park
		// every Secret's .Data in the informer store, breaking INV-5 cluster-wide before
		// the observer's careful metadata-only reads ever run.
		Watches(&corev1.Secret{}, enqueueForPullSecret(mgr.GetClient()), builder.OnlyMetadata).
		Watches(&appsv1.Deployment{}, enqueueForTarget(mgr.GetClient())).
		Watches(&appsv1.StatefulSet{}, enqueueForTarget(mgr.GetClient())).
		Watches(&appsv1.ReplicaSet{}, enqueueForTarget(mgr.GetClient())).
		Watches(&batchv1.Job{}, enqueueForTarget(mgr.GetClient())).
		Watches(&batchv1.CronJob{}, enqueueForTarget(mgr.GetClient())).
		Named("pacto").
		Complete(r)
}
