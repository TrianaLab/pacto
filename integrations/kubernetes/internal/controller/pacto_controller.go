/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/google/go-containerregistry/pkg/authn"
	"gopkg.in/yaml.v3"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/credentials"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/metrics"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/observer"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/schemax"
	"github.com/trianalab/pacto/v3/pkg/validation"
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
	APIReader                         client.Reader
	Scheme                            *runtime.Scheme
	Recorder                          record.EventRecorder
	Loader                            ContractLoader
	StabilizationWindow               time.Duration
	EnableMetricsObservation          bool
	EnableProbing                     bool
	EnableInterfaceNameMatchDiscovery bool
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

	// Same for the contract status, which resetDerivedStatus clears below: the
	// value read from the API server is the one the previous reconciliation
	// persisted, and every terminal path threads it back to
	// emitContractStatusEvent. Empty means never reconciled.
	prevContractStatus := pacto.Status.ContractStatus

	// 2. Reset all derived status fields so no stale data survives.
	//    Fields will be repopulated by each step below.
	r.resetDerivedStatus(pacto)

	// 3. Resolve pull secret credentials (if specified)
	var ociAuth *authn.AuthConfig
	if secretName := pacto.Spec.ContractRef.PullSecretRef; secretName != "" {
		auth, secretErr := r.resolveOCIAuth(ctx, pacto.Namespace, secretName, pacto.Spec.ContractRef.OCI)
		if secretErr != nil {
			return r.failReconciliation(ctx, pacto, fmt.Sprintf("failed to read pull secret %q: %v", secretName, secretErr),
				&pactov1alpha1.ValidationResult{
					Valid:  false,
					Errors: []pactov1alpha1.ValidationIssue{{Path: "spec.contractRef.pullSecretRef", Message: secretErr.Error()}},
				}, prevContractStatus, pactov1alpha1.ContractStatusUnknown)
		}
		ociAuth = auth
	}

	// 4. Determine resolution policy and load the contract
	ociRef := pacto.Spec.ContractRef.OCI
	pacto.Status.ResolutionPolicy = resolutionPolicy(ociRef)

	loadResult, err := r.Loader.Load(ctx, ociRef, pacto.Spec.ContractRef.Inline, ociAuth)
	if err != nil {
		status := classifyLoadError(err)
		return r.failReconciliation(ctx, pacto, err.Error(),
			&pactov1alpha1.ValidationResult{
				Valid:  false,
				Errors: []pactov1alpha1.ValidationIssue{{Message: err.Error()}},
			}, prevContractStatus, status)
	}

	// 4b. Apply configuration overrides (if specified)
	effectiveLR, overriddenKeys, overrideErr := applyOverrides(loadResult, pacto.Spec.Overrides)
	if overrideErr != nil {
		return r.failReconciliation(ctx, pacto, fmt.Sprintf("override error: %s", overrideErr.Error()),
			&pactov1alpha1.ValidationResult{
				Valid:  false,
				Errors: []pactov1alpha1.ValidationIssue{{Path: "spec.overrides", Message: overrideErr.Error()}},
			}, prevContractStatus, pactov1alpha1.ContractStatusInvalid)
	}
	effectiveContract := effectiveLR.Contract

	// 5. Structural + cross-field + policy validation (on effective contract)
	contractResult := validation.Validate(effectiveContract, effectiveLR.RawYAML, effectiveLR.BundleFS)
	pacto.Status.Validation = mapValidationResult(contractResult)

	if len(contractResult.Errors) > 0 {
		msg := formatValidationErrors(contractResult.Errors)
		return r.failReconciliation(ctx, pacto, msg, pacto.Status.Validation, prevContractStatus, pactov1alpha1.ContractStatusInvalid)
	}

	r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionTrue,
		pactov1alpha1.ReasonContractParsed,
		fmt.Sprintf("Contract %s v%s is valid", effectiveContract.Service.Name, effectiveContract.Service.Version))

	// 6. Populate contract-derived status fields (from effective contract)
	pacto.Status.ContractVersion = effectiveContract.Service.Version
	r.populateContractStatus(pacto, &effectiveLR)

	// Mark overridden keys in configuration status
	for i := range pacto.Status.Configurations {
		if keys, ok := overriddenKeys[pacto.Status.Configurations[i].Name]; ok {
			pacto.Status.Configurations[i].OverriddenKeys = keys
		}
	}

	// 6b. Evaluate declared readiness (separate dimension from compliance).
	//     Runs for both reference-only and target contracts.
	r.reconcileReadiness(pacto, effectiveContract, readinessWasUnmet)

	// 7. Ensure PactoRevision for current version
	revisionName, revErr := r.ensureRevision(ctx, pacto, loadResult)
	if revErr != nil {
		log.Error(revErr, "Failed to ensure PactoRevision")
	} else {
		pacto.Status.CurrentRevision = revisionName
	}

	if ociRef != "" && !strings.Contains(ociRef, "@") {
		if syncErr := r.syncAllRevisions(ctx, pacto, ociRef, ociAuth); syncErr != nil {
			log.Error(syncErr, "Failed to sync all revisions")
		}
	}

	// 8. Reference-only: skip runtime validation
	if pacto.IsReference() {
		r.setCondition(pacto, pactov1alpha1.ConditionContractValid, metav1.ConditionTrue,
			pactov1alpha1.ReasonReferenceOnly,
			fmt.Sprintf("Reference contract %s v%s is valid", effectiveContract.Service.Name, effectiveContract.Service.Version))
		pacto.Status.ContractStatus = pactov1alpha1.ContractStatusReference
		pacto.Status.Summary = &pactov1alpha1.Summary{}
		return r.finishReconciliation(ctx, pacto, prevContractStatus)
	}

	// 9. Resolve target
	workloadName, workloadKind := pacto.ResolvedWorkload()
	serviceName := pacto.Spec.Target.ServiceName

	// 10. Collect runtime evidence and evaluate findings
	obs := observer.New(r.Client)

	// Build CollectInput from the resolved target (spec section 9.1).
	workloadExplicit := pacto.Spec.Target.WorkloadRef != nil &&
		pacto.Spec.Target.WorkloadRef.Name != "" &&
		pacto.Spec.Target.WorkloadRef.Kind != ""

	// Map InterfaceBindings from CR to observer types
	interfaceBindings := make([]observer.InterfaceBinding, len(pacto.Spec.Target.InterfaceBindings))
	for i, b := range pacto.Spec.Target.InterfaceBindings {
		interfaceBindings[i] = observer.InterfaceBinding{
			Interface:   b.Interface,
			ServicePort: b.ServicePort,
		}
	}

	// Convert ObservationWindows from status into the map format
	observationWindows := make(map[string]*metav1.Time)
	for _, w := range pacto.Status.ObservationWindows {
		key := fmt.Sprintf("%s/%s", w.Kind, w.Subject)
		observationWindows[key] = &w.FirstObservedNegativeAt
	}

	now := time.Now()
	collectInput := observer.CollectInput{
		Namespace:                         pacto.Namespace,
		ServiceName:                       serviceName,
		WorkloadName:                      workloadName,
		WorkloadKind:                      workloadKind,
		ContractRef:                       loadResult.ResolvedRef,
		WorkloadExplicit:                  workloadExplicit,
		Contract:                          effectiveContract,
		BundleFS:                          loadResult.BundleFS,
		InterfaceBindings:                 interfaceBindings,
		ConfigBindings:                    pacto.Spec.Target.ConfigBindings,
		StabilizationWindow:               r.StabilizationWindow,
		ObservationWindows:                observationWindows,
		Now:                               now,
		EnableMetricsObservation:          r.EnableMetricsObservation,
		EnableProbing:                     r.EnableProbing,
		EnableInterfaceNameMatchDiscovery: r.EnableInterfaceNameMatchDiscovery,
	}

	evidenceSet, windowUpdates := obs.Collect(ctx, collectInput)

	// Apply window updates (spec section 9.5): upsert by (Kind, Subject); nil resets the entry; windows for
	// assertions no longer declared by the effective contract are pruned.
	r.applyObservationWindowUpdates(pacto, windowUpdates, declaredWindowKeys(effectiveContract))

	// 11. Populate lean observed runtime into status (backward compat for dashboard)
	snapshot, obsErr := obs.Observe(ctx, pacto.Namespace, serviceName, workloadName, workloadKind)
	if snapshot != nil && snapshot.WorkloadExists {
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

	r.setRuntimeObservedCondition(pacto, obsErr)

	// 12. Resources status (backward compat)
	hasService := serviceName != ""
	if hasService {
		pacto.Status.Resources = &pactov1alpha1.ResourcesStatus{
			Service: &pactov1alpha1.ResourceStatus{
				Name:   serviceName,
				Exists: snapshot != nil && snapshot.ServiceExists,
			},
		}
	}
	if workloadName != "" {
		if pacto.Status.Resources == nil {
			pacto.Status.Resources = &pactov1alpha1.ResourcesStatus{}
		}
		pacto.Status.Resources.Workload = &pactov1alpha1.ResourceStatus{
			Name:   workloadName,
			Kind:   workloadKind,
			Exists: snapshot != nil && snapshot.WorkloadExists,
		}
	}

	// 13. Evaluate: contract × evidence → findings + coverage
	findings, cov := validation.Evaluate(*effectiveContract, evidenceSet)

	// Append contract-only findings from structural validation
	structuralFindings := contractResult.Findings()
	allFindings := append(structuralFindings, findings...)

	// Map findings to status
	pacto.Status.Findings = make([]pactov1alpha1.FindingStatus, 0, len(allFindings))
	for _, f := range allFindings {
		findingStatus := pactov1alpha1.FindingStatus{
			Code:         string(f.Code),
			Severity:     string(f.Severity),
			Category:     string(f.Category),
			Subject:      fmt.Sprintf("%s/%s", f.Subject.Kind, f.Subject.Name),
			ContractPath: f.ContractPath,
			Message:      f.Message,
		}
		for _, er := range f.EvidenceRefs {
			findingStatus.EvidenceRefs = append(findingStatus.EvidenceRefs, pactov1alpha1.EvidenceRefStatus{
				Source:     er.Source,
				ObservedAt: er.ObservedAt,
			})
		}
		pacto.Status.Findings = append(pacto.Status.Findings, findingStatus)
	}

	// 14. Copy EvaluationCoverage. Reported metadata: the engine charges a required
	//     assertion and emits its Unknown finding in the same branch, so a coverage
	//     gap never outranks what the findings already say.
	pacto.Status.EvaluationCoverage = &pactov1alpha1.EvaluationCoverage{
		Evaluated: int32(cov.Evaluated),
		Required:  int32(cov.Required),
	}

	// 15. Count the findings; rank them with the SHARED ladder. Deriving it here too is
	//     what made one contract read Warning in kubectl and Compliant in the dashboard.
	summary := summarizeFindings(allFindings)
	pacto.Status.Summary = &summary
	pacto.Status.ContractStatus = validation.DeriveStatus(allFindings, cov)

	return r.finishReconciliation(ctx, pacto, prevContractStatus)
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
	// Preserve: CurrentRevision (set in step 7), ObservationWindows (temporal state, spec 9.5), LastReconciledAt/ObservedGeneration (set in finish)
}

// failReconciliation handles the common pattern for contract-level failures with phase-classified status
// (spec section 9.8): Invalid vs Unknown. The status parameter drives condition reason + summary.
// prevContractStatus is the contract status persisted by the previous reconciliation; it gates the
// transition event (see emitContractStatusEvent).
func (r *PactoReconciler) failReconciliation(ctx context.Context, pacto *pactov1alpha1.Pacto, msg string, valResult *pactov1alpha1.ValidationResult, prevContractStatus, status string) (ctrl.Result, error) {
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

	now := metav1.Now()
	pacto.Status.LastReconciledAt = &now
	pacto.Status.ObservedGeneration = pacto.Generation

	if statusErr := r.Status().Update(ctx, pacto); statusErr != nil {
		log.Error(statusErr, "Failed to update status")
		return ctrl.Result{}, statusErr
	}

	eventReason := pactov1alpha1.EventContractInvalid
	if status == pactov1alpha1.ContractStatusUnknown {
		eventReason = pactov1alpha1.EventContractUnavailable
	}
	r.emitContractStatusEvent(pacto, prevContractStatus, eventReason, msg)

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
// prevContractStatus is the contract status persisted by the previous
// reconciliation; it gates the transition event (see emitContractStatusEvent).
func (r *PactoReconciler) finishReconciliation(ctx context.Context, pacto *pactov1alpha1.Pacto, prevContractStatus string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	now := metav1.Now()
	pacto.Status.LastReconciledAt = &now
	pacto.Status.ObservedGeneration = pacto.Generation

	if statusErr := r.Status().Update(ctx, pacto); statusErr != nil {
		log.Error(statusErr, "Failed to update status")
		return ctrl.Result{}, statusErr
	}

	r.emitContractStatusEvent(pacto, prevContractStatus,
		pactov1alpha1.EventValidationFailed, contractStatusMessage(pacto))

	// Emit Prometheus metrics
	metrics.RecordContractStatus(pacto.Namespace, pacto.Name, pacto.Status.ContractStatus)

	log.Info("Reconciliation complete", "contractStatus", pacto.Status.ContractStatus)
	return ctrl.Result{RequeueAfter: r.requeueInterval(pacto)}, nil
}

// populateContractStatus extracts structured data from the parsed contract into status fields.
// Policy sources are surfaced as metadata only — the operator does not resolve or enforce
// ref-based policies. Local policy schemas bundled in the contract are enforced by
// validation.Validate() during contract loading.
func (r *PactoReconciler) populateContractStatus(pacto *pactov1alpha1.Pacto, lr *loader.LoadResult) {
	c := lr.Contract

	// Initialize slices so JSON output is [] instead of null when empty.
	pacto.Status.Configurations = []pactov1alpha1.ConfigurationInfo{}
	pacto.Status.Policies = []pactov1alpha1.PolicyInfo{}
	pacto.Status.Capabilities = []pactov1alpha1.CapabilityInfo{}

	// Contract info
	info := &pactov1alpha1.ContractInfo{
		ServiceName: c.Service.Name,
		Version:     c.Service.Version,
		ResolvedRef: lr.ResolvedRef,
	}
	if !c.Service.Owner.IsEmpty() {
		info.Owner = mapOwnerToInfo(c.Service.Owner)
		info.OwnerDisplay = c.Service.Owner.DisplayString()
	}
	pacto.Status.Contract = info

	// Interfaces (v2: Type is openapi/asyncapi/grpc, Ref not Contract, no Port)
	for _, iface := range c.Interfaces {
		ii := pactov1alpha1.InterfaceInfo{
			Name:       iface.Name,
			Type:       iface.Type,
			Ref:        iface.Ref,
			Visibility: iface.Visibility,
		}
		pacto.Status.Interfaces = append(pacto.Status.Interfaces, ii)
	}

	// Capabilities (v2)
	for _, cap := range c.Capabilities {
		pacto.Status.Capabilities = append(pacto.Status.Capabilities, pactov1alpha1.CapabilityInfo{
			Type: cap.Type,
			Ref:  cap.Ref,
		})
	}

	// Configurations
	for _, cfg := range c.Configurations {
		ci := pactov1alpha1.ConfigurationInfo{
			Name:      cfg.Name,
			HasSchema: cfg.Schema != "",
			Ref:       cfg.Ref,
		}
		for k, v := range cfg.Values {
			ci.ValueKeys = append(ci.ValueKeys, k)
			if s, ok := v.(string); ok && strings.HasPrefix(s, "secret://") {
				ci.SecretKeys = append(ci.SecretKeys, k)
			}
		}
		sort.Strings(ci.ValueKeys)
		sort.Strings(ci.SecretKeys)
		// Surface configuration content (declared keys + types) so consumers can
		// render it without re-reading the bundle. Literal values win; otherwise
		// extract from the bundled schema.
		if len(cfg.Values) > 0 {
			ci.Properties = toSchemaProps(schemax.Values(cfg.Values))
		} else if cfg.Schema != "" {
			ci.Properties = schemaPropsFromBundle(lr.BundleFS, cfg.Schema)
		}
		pacto.Status.Configurations = append(pacto.Status.Configurations, ci)
	}

	// Dependencies
	for _, dep := range c.Dependencies {
		pacto.Status.Dependencies = append(pacto.Status.Dependencies, pactov1alpha1.DependencyInfo{
			Name:          dep.Name,
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
		})
	}

	// Policies
	for _, pol := range c.Policies {
		pi := pactov1alpha1.PolicyInfo{
			Name:      pol.Name,
			HasSchema: pol.Schema != "",
			Schema:    pol.Schema,
			Ref:       pol.Ref,
		}
		// Surface policy schema content (title/description + keys) for local
		// schemas so consumers can render it without re-reading the bundle.
		if pol.Schema != "" {
			pi.Properties = schemaPropsFromBundle(lr.BundleFS, pol.Schema)
			pi.Title, pi.Description = schemaMetaFromBundle(lr.BundleFS, pol.Schema)
		}
		pacto.Status.Policies = append(pacto.Status.Policies, pi)
	}

	// Metadata
	if len(c.Metadata) > 0 {
		pacto.Status.Metadata = make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			pacto.Status.Metadata[k] = fmt.Sprintf("%v", v)
		}
	}
}

// schemaPropsFromBundle reads a JSON Schema file from the bundle FS and returns
// its flattened properties as status entries. Returns nil when the bundle, file,
// or properties are unavailable (the dashboard then falls back to metadata only).
func schemaPropsFromBundle(fsys fs.FS, path string) []pactov1alpha1.SchemaProperty {
	if fsys == nil || path == "" {
		return nil
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	return toSchemaProps(schemax.Properties(data, path))
}

// schemaMetaFromBundle reads the title and description from a JSON Schema file
// in the bundle FS.
func schemaMetaFromBundle(fsys fs.FS, path string) (title, description string) {
	if fsys == nil || path == "" {
		return "", ""
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", ""
	}
	return schemax.Meta(data, path)
}

// toSchemaProps converts shared schemax properties into CRD status properties.
func toSchemaProps(ps []schemax.Property) []pactov1alpha1.SchemaProperty {
	if len(ps) == 0 {
		return nil
	}
	out := make([]pactov1alpha1.SchemaProperty, 0, len(ps))
	for _, p := range ps {
		out = append(out, pactov1alpha1.SchemaProperty{Key: p.Key, Value: p.Value, Type: p.Type})
	}
	return out
}

func (r *PactoReconciler) setCondition(pacto *pactov1alpha1.Pacto, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&pacto.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: pacto.Generation,
		Reason:             reason,
		Message:            message,
	})
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

// applyConfigurationOverrides creates a shallow copy of the contract with configuration
// overrides merged in. The original contract is never mutated (important for loader cache safety).
// Returns the effective contract, a map of overridden key names per configuration, and any error.
//
// This implements name-based configuration matching rather than reusing the pacto library's
// override.Apply (which operates on raw YAML with index-based --set paths). Name-based matching
// is more appropriate for a declarative CRD where array ordering should not affect behavior.
// The merge semantics (override values win) are aligned with the CLI model.
func applyConfigurationOverrides(c *contract.Contract, overrides *pactov1alpha1.ContractOverrides) (*contract.Contract, map[string][]string, error) {
	if overrides == nil || len(overrides.Configurations) == 0 {
		return c, nil, nil
	}

	// Build lookup of existing configurations by name.
	configByName := make(map[string]int, len(c.Configurations))
	for i, cfg := range c.Configurations {
		configByName[cfg.Name] = i
	}

	// Validate all override names exist before applying any.
	for _, ov := range overrides.Configurations {
		if _, ok := configByName[ov.Name]; !ok {
			return nil, nil, fmt.Errorf("configuration %q not found in contract", ov.Name)
		}
	}

	// Shallow-copy the contract and deep-copy the Configurations slice + Values maps.
	effective := *c
	effective.Configurations = make([]contract.Configuration, len(c.Configurations))
	for i, cfg := range c.Configurations {
		effective.Configurations[i] = cfg
		if cfg.Values != nil {
			effective.Configurations[i].Values = make(map[string]any, len(cfg.Values))
			maps.Copy(effective.Configurations[i].Values, cfg.Values)
		}
	}

	// Merge override values (override wins).
	overriddenKeys := make(map[string][]string, len(overrides.Configurations))
	for _, ov := range overrides.Configurations {
		idx := configByName[ov.Name]
		if effective.Configurations[idx].Values == nil {
			effective.Configurations[idx].Values = make(map[string]any, len(ov.Values))
		}
		keys := make([]string, 0, len(ov.Values))
		for k, v := range ov.Values {
			effective.Configurations[idx].Values[k] = v
			keys = append(keys, k)
		}
		sort.Strings(keys)
		overriddenKeys[ov.Name] = keys
	}

	return &effective, overriddenKeys, nil
}

// applyOverrides returns the load result the rest of the reconcile should use: the
// contract with spec.overrides merged into BOTH halves -- the parsed struct and the
// bytes -- or an error if either half cannot be produced.
//
// Both halves, because the two are read by different consumers and the loader does not
// guarantee they came from the same place: the OCI path takes Contract from the bundle
// and RawYAML from the bundle FS, and can leave the bytes empty entirely. Merging only
// the struct is what let an override reach the status while validation still read the
// original document.
func applyOverrides(loadResult *loader.LoadResult, overrides *pactov1alpha1.ContractOverrides) (loader.LoadResult, map[string][]string, error) {
	effective := *loadResult
	if overrides == nil || len(overrides.Configurations) == 0 {
		return effective, nil, nil
	}
	merged, keys, err := applyConfigurationOverrides(loadResult.Contract, overrides)
	if err != nil {
		return effective, nil, err
	}
	// Validation layers 1 and 3 read the BYTES, not the struct, so the merged contract
	// has to reach them as bytes -- otherwise policy enforcement never sees
	// spec.overrides and an override that violates a policy still reports
	// ContractValid=True.
	mergedYAML, err := patchConfigurationValues(loadResult.RawYAML, overrides)
	if err != nil {
		return effective, nil, err
	}
	effective.Contract, effective.RawYAML = merged, mergedYAML
	return effective, keys, nil
}

// patchConfigurationValues rewrites ONLY the overridden configuration values inside
// the loader's own YAML document and returns the result. Every other byte -- every
// unrelated key, every explicitly-empty value -- survives exactly as the file wrote it.
//
// The obvious alternative, re-marshalling the merged struct, is a fail-open:
// contract.Configuration tags Schema, Ref and Values `omitempty`, so a key that was
// present-but-empty in the file (`values: {}` beside a `ref:`, say) disappears from
// the round trip and layer 1 stops seeing the structural violation it was written to
// catch. A bare `overrides: {}` on the CR was enough to turn Invalid into valid, and
// the GitOps promotion gate reads that verdict.
//
// Every failure here is an error, never a fall back to the unpatched bytes: bytes
// that do not carry the override are exactly the state the audit reported.
func patchConfigurationValues(raw []byte, overrides *pactov1alpha1.ContractOverrides) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("re-reading the contract document: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("contract document is empty")
	}
	configs := mappingValue(doc.Content[0], "configurations")
	if configs == nil || configs.Kind != yaml.SequenceNode {
		return nil, errors.New("contract declares no configurations to override")
	}

	for _, ov := range overrides.Configurations {
		entry := configurationNamed(configs, ov.Name)
		if entry == nil {
			return nil, fmt.Errorf("configuration %q not found in the contract document", ov.Name)
		}
		values := mappingValue(entry, "values")
		switch {
		case values == nil:
			values = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "values"}, values)
		case values.Tag == "!!null":
			// `values:` with nothing under it. The override is what gives it content.
			values.Kind, values.Tag, values.Value, values.Style = yaml.MappingNode, "!!map", "", 0
		case values.Kind != yaml.MappingNode:
			return nil, fmt.Errorf("configuration %q declares a non-mapping values block", ov.Name)
		}
		// Sorted so the patched document is byte-identical run to run; Go map order
		// would otherwise reshuffle newly inserted keys on every reconcile.
		for _, k := range slices.Sorted(maps.Keys(ov.Values)) {
			setMappingValue(values, k, ov.Values[k])
		}
	}
	return yaml.Marshal(&doc)
}

// mappingValue returns the value node for key in a mapping node, or nil. A yaml.v3
// mapping stores Content as alternating key, value pairs.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// setMappingValue sets key to a string scalar, replacing any existing value. The
// override CRD types values as map[string]string, and applyConfigurationOverrides
// merges those same strings into the struct, so both halves agree on the type: a
// numeric-looking override is a string in the document exactly as it is in the
// struct, and a schema expecting a number rejects both.
func setMappingValue(mapping *yaml.Node, key, value string) {
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = scalar
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, scalar)
}

// configurationNamed returns the configurations[] entry whose name is name, or nil.
func configurationNamed(configs *yaml.Node, name string) *yaml.Node {
	for _, entry := range configs.Content {
		if n := mappingValue(entry, "name"); n != nil && n.Value == name {
			return entry
		}
	}
	return nil
}

// --- Helpers ---

func mapValidationResult(vr validation.ValidationResult) *pactov1alpha1.ValidationResult {
	result := &pactov1alpha1.ValidationResult{Valid: len(vr.Errors) == 0}
	for _, e := range vr.Errors {
		result.Errors = append(result.Errors, pactov1alpha1.ValidationIssue{
			Code: e.Code, Path: e.Path, Message: e.Message,
		})
	}
	for _, w := range vr.Warnings {
		result.Warnings = append(result.Warnings, pactov1alpha1.ValidationIssue{
			Code: w.Code, Path: w.Path, Message: w.Message,
		})
	}
	return result
}

// summarizeFindings counts findings by severity. It does NOT rank them: the compliance
// ladder has one producer, validation.DeriveStatus (spec section 1.4).
func summarizeFindings(findings []finding.Finding) pactov1alpha1.Summary {
	var summary pactov1alpha1.Summary
	for _, f := range findings {
		switch f.Severity {
		case finding.SeverityError:
			summary.ErrorCount++
		case finding.SeverityUnknown:
			summary.UnknownCount++
		case finding.SeverityWarning:
			summary.WarningCount++
		case finding.SeverityInfo:
			summary.InfoCount++
		}
	}
	return summary
}

func formatValidationErrors(validationErrors []contract.ValidationError) string {
	var details []string
	for _, e := range validationErrors {
		if e.Path != "" {
			details = append(details, fmt.Sprintf("%s: %s", e.Path, e.Message))
		} else {
			details = append(details, e.Message)
		}
	}
	return fmt.Sprintf("Contract validation failed: %s", strings.Join(details, "; "))
}

// --- PactoRevision management ---

func (r *PactoReconciler) ensureRevision(ctx context.Context, pacto *pactov1alpha1.Pacto, loadResult *loader.LoadResult) (string, error) {
	log := logf.FromContext(ctx)

	hash := fmt.Sprintf("%x", sha256.Sum256(loadResult.RawYAML))
	shortHash := hash[:7]

	version := loadResult.Contract.Service.Version
	if version == "" {
		version = "unknown"
	}

	sanitizedVersion := strings.ReplaceAll(version, ".", "-")
	revisionName := fmt.Sprintf("%s-%s-%s", pacto.Name, sanitizedVersion, shortHash)
	if len(revisionName) > 253 {
		revisionName = revisionName[:253]
	}

	existing := &pactov1alpha1.PactoRevision{}
	err := r.Get(ctx, client.ObjectKey{Namespace: pacto.Namespace, Name: revisionName}, existing)
	if err == nil {
		if existing.Status.ContractHash == "" || existing.Status.CreatedAt == nil {
			now := metav1.Now()
			existing.Status.Resolved = true
			existing.Status.ContractHash = hash
			if existing.Status.CreatedAt == nil {
				existing.Status.CreatedAt = &now
			}
			if statusErr := r.Status().Update(ctx, existing); statusErr != nil {
				log.V(1).Info("Failed to backfill revision status", "revision", revisionName, "error", statusErr)
			}
		}
		return revisionName, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to check for existing revision: %w", err)
	}

	source := pactov1alpha1.RevisionSource{}
	if loadResult.ResolvedRef != "" {
		source.OCI = loadResult.ResolvedRef
		source.Digest = loadResult.ResolvedDigest
	} else if pacto.Spec.ContractRef.OCI != "" {
		source.OCI = pacto.Spec.ContractRef.OCI
		source.Digest = loadResult.ResolvedDigest
	} else {
		source.Inline = true
	}

	now := metav1.Now()
	revision := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revisionName,
			Namespace: pacto.Namespace,
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       pacto.Name,
				pactov1alpha1.LabelRevisionVersion: version,
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:     version,
			Source:      source,
			PactoRef:    pacto.Name,
			ServiceName: loadResult.Contract.Service.Name,
		},
	}

	if err := ctrl.SetControllerReference(pacto, revision, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, revision); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return revisionName, nil
		}
		return "", fmt.Errorf("failed to create PactoRevision: %w", err)
	}

	revision.Status = pactov1alpha1.PactoRevisionStatus{
		Resolved:     true,
		ContractHash: hash,
		CreatedAt:    &now,
	}
	if statusErr := r.Status().Update(ctx, revision); statusErr != nil {
		log.Error(statusErr, "Failed to update PactoRevision status", "revision", revisionName)
	}

	log.Info("Created PactoRevision", "revision", revisionName, "version", version, "hash", shortHash)
	r.Recorder.Eventf(pacto, corev1.EventTypeNormal, "RevisionCreated", "Created revision %s for contract v%s", revisionName, version)

	return revisionName, nil
}

// setRuntimeObservedCondition records whether runtime evidence was collected. On an
// observation API error it reports False (ReasonObservationFailed) rather than
// claiming success, so a failure is distinguishable from a genuine observation.
func (r *PactoReconciler) setRuntimeObservedCondition(pacto *pactov1alpha1.Pacto, obsErr error) {
	if obsErr != nil {
		r.setCondition(pacto, pactov1alpha1.ConditionRuntimeObserved, metav1.ConditionFalse,
			pactov1alpha1.ReasonObservationFailed, fmt.Sprintf("Runtime observation failed: %v", obsErr))
		return
	}
	r.setCondition(pacto, pactov1alpha1.ConditionRuntimeObserved, metav1.ConditionTrue,
		pactov1alpha1.ReasonFound, "Runtime evidence collected successfully")
}

// shortDigest returns the first 12 characters of a digest for display, or the
// whole string when it is shorter. A digest can be empty or malformed (e.g. a
// registry that omits it), so slicing [:12] unconditionally would panic.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// revisionsBySourceRef indexes this Pacto's revisions by spec.source.oci. A force-push
// leaves TWO revisions on the same ref, so the newest wins: comparing the registry against
// the superseded one would re-raise TagOverwritten on every reconcile.
// ponytail: creationTimestamp ordering, ties go to List order. Revisions are created
// seconds apart, so a tie needs a same-second double force-push to even be observable.
func (r *PactoReconciler) revisionsBySourceRef(ctx context.Context, pacto *pactov1alpha1.Pacto) (map[string]*pactov1alpha1.PactoRevision, error) {
	revList := &pactov1alpha1.PactoRevisionList{}
	if err := r.List(ctx, revList,
		client.InNamespace(pacto.Namespace),
		client.MatchingLabels{pactov1alpha1.LabelPactoName: pacto.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list revisions: %w", err)
	}

	byRef := make(map[string]*pactov1alpha1.PactoRevision, len(revList.Items))
	for i := range revList.Items {
		rev := &revList.Items[i]
		// An inline revision has an empty ref; taggedRef never is, so it is simply never hit.
		ref := rev.Spec.Source.OCI
		if cur, ok := byRef[ref]; ok && rev.CreationTimestamp.Before(&cur.CreationTimestamp) {
			continue
		}
		byRef[ref] = rev
	}
	return byRef, nil
}

func (r *PactoReconciler) syncAllRevisions(ctx context.Context, pacto *pactov1alpha1.Pacto, baseRef string, ociAuth *authn.AuthConfig) error {
	log := logf.FromContext(ctx)

	tags, err := r.Loader.ListTags(ctx, baseRef, ociAuth)
	if err != nil {
		return fmt.Errorf("failed to list tags: %w", err)
	}

	// Index the existing revisions by the ref ensureRevision actually WRITES
	// (spec.source.oci). Keying on the version label instead compared a registry tag
	// against service.version from inside the contract, so the everyday "v1.2.0" tag on
	// a "1.2.0" version never matched and the force-push check below never ran.
	byRef, err := r.revisionsBySourceRef(ctx, pacto)
	if err != nil {
		return err
	}

	for _, tag := range tags {
		taggedRef := strings.TrimPrefix(baseRef, "oci://")
		if idx := strings.LastIndex(taggedRef, ":"); idx > strings.LastIndex(taggedRef, "/") {
			taggedRef = taggedRef[:idx]
		}
		taggedRef = taggedRef + ":" + tag

		// If a revision exists for this tag, check whether the digest still matches.
		// A mismatch means the tag was force-pushed (overwritten) on the registry.
		if existing, ok := byRef[taggedRef]; ok {
			storedDigest := existing.Spec.Source.Digest
			if storedDigest == "" {
				// Revision predates digest tracking — skip drift check.
				continue
			}

			loadResult, loadErr := r.Loader.Load(ctx, taggedRef, "", ociAuth)
			if loadErr != nil {
				log.V(1).Info("Skipping digest check: failed to load", "tag", tag, "error", loadErr)
				continue
			}

			if loadResult.ResolvedDigest == storedDigest {
				continue // Digest matches — tag was not overwritten.
			}

			log.Info("Detected force-push: OCI digest changed for tag",
				"tag", tag,
				"oldDigest", storedDigest,
				"newDigest", loadResult.ResolvedDigest,
				"oldRevision", existing.Name)
			r.Recorder.Eventf(pacto, corev1.EventTypeWarning, "TagOverwritten",
				"Tag %s was force-pushed (digest changed from %s to %s)", tag, shortDigest(storedDigest), shortDigest(loadResult.ResolvedDigest))

			revName, revErr := r.ensureRevision(ctx, pacto, loadResult)
			if revErr != nil {
				log.V(1).Info("Failed to create revision for force-pushed tag", "tag", tag, "error", revErr)
				continue
			}
			log.Info("Created new revision for force-pushed tag", "tag", tag, "revision", revName)
			continue
		}

		loadResult, loadErr := r.Loader.Load(ctx, taggedRef, "", ociAuth)
		if loadErr != nil {
			log.V(1).Info("Skipping tag: failed to load", "tag", tag, "error", loadErr)
			continue
		}

		revName, revErr := r.ensureRevision(ctx, pacto, loadResult)
		if revErr != nil {
			log.V(1).Info("Skipping tag: failed to create revision", "tag", tag, "error", revErr)
			continue
		}
		log.V(1).Info("Synced revision for tag", "tag", tag, "revision", revName)
	}

	return nil
}

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
		For(&pactov1alpha1.Pacto{}).
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

// mapOwnerToInfo converts a contract.Owner to the CRD OwnerInfo representation.
// Owner is object-only (team, DRI, contacts); fields are mapped directly.
func mapOwnerToInfo(o contract.Owner) *pactov1alpha1.OwnerInfo {
	if o.IsEmpty() {
		return nil
	}
	result := &pactov1alpha1.OwnerInfo{
		Team: o.Team,
		DRI:  o.DRI,
	}
	for _, c := range o.Contacts {
		result.Contacts = append(result.Contacts, pactov1alpha1.OwnerContact{
			Type:    c.Type,
			Value:   c.Value,
			Purpose: c.Purpose,
		})
	}
	return result
}
