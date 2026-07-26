/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/loader"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/finding"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// driftRawYAML is a structurally valid v2 contract declaring a stateful/persistent
// service, used to drive evidence-drift findings during a full reconcile.
const driftRawYAML = `
pactoVersion: "2.0"
service:
  name: drift-svc
  version: 1.0.0
  owner:
    team: team-a
state:
  type: stateful
  dataCriticality: high
  persistence:
    durability: persistent
    scope: shared
workload: service
`

func reconcileReq(name, ns string) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: ns}}
}

func getPacto(t *testing.T, r *PactoReconciler, name, ns string) *pactov1alpha1.Pacto {
	t.Helper()
	p := &pactov1alpha1.Pacto{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: ns}, p); err != nil {
		t.Fatalf("failed to get pacto: %v", err)
	}
	return p
}

// ---------- Reconcile: pull-secret resolution ----------

// Missing pull secret -> resolveOCIAuth errors -> failReconciliation.
func TestReconcile_PullSecretMissing(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "ps-missing", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc", PullSecretRef: "missing"},
		},
	}
	r := newReconciler(pacto)

	if _, err := r.Reconcile(context.Background(), reconcileReq("ps-missing", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "ps-missing", "default")
	// Spec section 9.8: pull-secret read failed -> transient/credentials -> Unknown
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusUnknown {
		t.Errorf("expected Unknown, got %s", got.Status.ContractStatus)
	}
	// Transient failure: the contract was never loaded, so status.validation must stay nil rather than
	// asserting the contract IS invalid (spec section 9.8 / F7).
	if got.Status.Validation != nil {
		t.Errorf("expected nil validation on transient Unknown, got %+v", got.Status.Validation)
	}
}

// Valid dockerconfigjson secret + OCI ref -> ociAuth = auth (line 102), reconcile proceeds.
func TestReconcile_PullSecretSuccess(t *testing.T) {
	dockerCfg := `{"auths":{"ghcr.io":{"username":"u","password":"p"}}}`
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "docker-secret", Namespace: "default"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerCfg)},
	}
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "ps-ok", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc@sha256:abc", PullSecretRef: "docker-secret"},
		},
	}
	r := newReconciler(pacto, secret)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
				RawYAML:  []byte(validContract),
			}, nil
		},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("ps-ok", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "ps-ok", "default")
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusReference {
		t.Errorf("expected Reference, got %s", got.Status.ContractStatus)
	}
}

// ---------- Reconcile: configuration overrides ----------

// Overrides referencing a nonexistent configuration -> applyConfigurationOverrides error -> fail.
func TestReconcile_OverrideError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "ov-err", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
			Overrides: &pactov1alpha1.ContractOverrides{
				Configurations: []pactov1alpha1.ConfigurationOverride{
					{Name: "nonexistent", Values: map[string]string{"k": "v"}},
				},
			},
		},
	}
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
				RawYAML:  []byte(validContract),
			}, nil
		},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("ov-err", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "ov-err", "default")
	// Spec section 9.8: override error -> effective contract cannot be constructed -> Invalid
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusInvalid {
		t.Errorf("expected Invalid, got %s", got.Status.ContractStatus)
	}
}

// Valid overrides matching a configuration -> overridden keys marked in status (153-156).
func TestReconcile_OverrideSuccessMarksKeys(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "ov-ok", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
			Overrides: &pactov1alpha1.ContractOverrides{
				Configurations: []pactov1alpha1.ConfigurationOverride{
					{Name: "default", Values: map[string]string{"db_host": "prod"}},
				},
			},
		},
	}
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "1.0.0"},
					Configurations: []contract.Configuration{
						{Name: "default", Schema: "cfg.json", Values: map[string]any{"db_host": "local"}},
					},
				},
				RawYAML: []byte(validContract),
			}, nil
		},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("ov-ok", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "ov-ok", "default")
	if len(got.Status.Configurations) != 1 {
		t.Fatalf("expected 1 configuration, got %d", len(got.Status.Configurations))
	}
	keys := got.Status.Configurations[0].OverriddenKeys
	if len(keys) != 1 || keys[0] != "db_host" {
		t.Errorf("expected OverriddenKeys=[db_host], got %v", keys)
	}
}

// ---------- Reconcile: contract validation errors ----------

// A contract that parses but fails structural validation -> failReconciliation (137-140).
func TestReconcile_ContractValidationError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "val-err", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "bad"},
		},
	}
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
				RawYAML:  []byte(`pactoVersion: "1.0"` + "\n"),
			}, nil
		},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("val-err", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "val-err", "default")
	// Spec section 9.8: structural/crossfield errors -> contract malformed -> Invalid
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusInvalid {
		t.Errorf("expected Invalid, got %s", got.Status.ContractStatus)
	}
	if got.Status.Validation == nil || len(got.Status.Validation.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

// ---------- Reconcile: ensureRevision error branch (165-167) ----------

func TestReconcile_EnsureRevisionError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "rev-err", Namespace: "default", UID: "u"},
		Spec:       pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{Inline: validContract}},
	}
	s := newSchemeWithApps()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("create failed")
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
				RawYAML:  []byte(validContract),
			}, nil
		}},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("rev-err", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "rev-err", "default")
	if got.Status.CurrentRevision != "" {
		t.Errorf("expected empty CurrentRevision after ensureRevision error, got %q", got.Status.CurrentRevision)
	}
}

// ---------- Reconcile: syncAllRevisions error branch (171-174) ----------

func TestReconcile_SyncAllRevisionsError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "sync-err", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc:1.0.0"},
		},
	}
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
				RawYAML:  []byte(validContract),
			}, nil
		},
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return nil, fmt.Errorf("registry unreachable")
		},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("sync-err", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "sync-err", "default")
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusReference {
		t.Errorf("expected Reference, got %s", got.Status.ContractStatus)
	}
}

// ---------- Reconcile: workload GET error -> per-dimension Outcome=Failed -> COLLECTION_FAILED finding ----------

func TestReconcile_CollectForTargetError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "collect-err", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc"},
		},
	}
	s := newSchemeWithApps()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.Deployment); ok {
					return fmt.Errorf("deployment get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}, Workload: "service"},
				RawYAML:  []byte(validContract),
			}, nil
		}},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("collect-err", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "collect-err", "default")
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusUnknown {
		t.Errorf("expected Unknown, got %s", got.Status.ContractStatus)
	}
	// Workload GET errored (non-NotFound) -> observeWorkloadDim produces Outcome=Failed observation
	// -> Evaluate emits COLLECTION_FAILED (family-2, SeverityUnknown) -> aggregate Unknown.
	found := false
	for _, f := range got.Status.Findings {
		if f.Code == "COLLECTION_FAILED" {
			found = true
		}
	}
	if !found {
		codes := make([]string, 0, len(got.Status.Findings))
		for _, f := range got.Status.Findings {
			codes = append(codes, f.Code)
		}
		t.Errorf("expected COLLECTION_FAILED finding; got codes: %v", codes)
	}
}

// ---------- Reconcile: findings mapping -> Error -> NonCompliant ----------

// A target whose declared workload/persistence contradict observed evidence yields
// Error findings (WORKLOAD_MISMATCH + PERSISTENCE_MISMATCH), driving the mapping
// loop, error counting, NonCompliant status, workload-only Resources init, and the
// finishReconciliation ValidationFailed event.
func TestReconcile_DriftFindingsWarning(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "drift", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: driftRawYAML},
			Target:      pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "drift-job", Kind: "Job"}},
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "drift-job", Namespace: "default"},
		Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "img:v1"}}}}},
	}
	s := newSchemeWithApps()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto, job).Build()
	recorder := record.NewFakeRecorder(20)
	r := &PactoReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: recorder,
		Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service:  contract.Service{Name: "drift-svc", Version: "1.0.0"},
					Workload: "service",
					State:    &contract.State{Type: contract.StateStateful, Persistence: contract.Persistence{Durability: contract.DurabilityPersistent}},
				},
				RawYAML: []byte(driftRawYAML),
			}, nil
		}},
	}

	if _, err := r.Reconcile(context.Background(), reconcileReq("drift", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "drift", "default")
	// Workload + persistence are required assertions (workload != "", durability=persistent).
	// Contradicted required assertions -> SeverityError (spec section 4.2) -> NonCompliant.
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusNonCompliant {
		t.Errorf("expected NonCompliant, got %s", got.Status.ContractStatus)
	}
	if got.Status.Summary == nil || got.Status.Summary.ErrorCount == 0 {
		t.Errorf("expected error findings, got summary %+v", got.Status.Summary)
	}
	// Resources.Workload initialized from workloadName when no serviceName present.
	if got.Status.Resources == nil || got.Status.Resources.Workload == nil {
		t.Error("expected Resources.Workload to be populated for workload-only target")
	}
	// At least one finding carries evidence refs.
	hasEvidence := false
	for _, f := range got.Status.Findings {
		if len(f.EvidenceRefs) > 0 {
			hasEvidence = true
		}
	}
	if !hasEvidence {
		t.Error("expected at least one finding with evidence refs")
	}
	// ValidationFailed event fired for the non-compliant status with a summary.
	sawEvent := false
	for len(recorder.Events) > 0 {
		if strings.Contains(<-recorder.Events, "ValidationFailed") {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Error("expected ValidationFailed event on Warning finish")
	}
}

// ---------- failReconciliation: status update error (342-345) ----------

func TestReconcile_FailReconciliationStatusUpdateError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "fail-su", Namespace: "default", UID: "u"},
		Spec:       pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{Inline: validContract}},
	}
	s := newScheme()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*pactov1alpha1.Pacto); ok {
					return fmt.Errorf("status update failed")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return nil, fmt.Errorf("load failed")
		}},
	}

	_, err := r.Reconcile(context.Background(), reconcileReq("fail-su", "default"))
	if err == nil {
		t.Fatal("expected error from status update failure")
	}
	if !strings.Contains(err.Error(), "status update failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------- summarizeFindings ----------

// The pure summarizer must count every severity and derive status accordingly.
// pacto v2's evidence engine currently emits only warnings, so the error/info
// counting and NonCompliant status are exercised here at the unit level.
func TestSummarizeFindings(t *testing.T) {
	// Mixed severities: error dominates -> NonCompliant, all counts populated.
	summary, status := summarizeFindings([]finding.Finding{
		{Severity: finding.SeverityError},
		{Severity: finding.SeverityWarning},
		{Severity: finding.SeverityInfo},
	})
	if summary.ErrorCount != 1 || summary.WarningCount != 1 || summary.InfoCount != 1 {
		t.Errorf("expected 1/1/1 counts, got %d/%d/%d", summary.ErrorCount, summary.WarningCount, summary.InfoCount)
	}
	if status != pactov1alpha1.ContractStatusNonCompliant {
		t.Errorf("expected NonCompliant with an error finding, got %s", status)
	}

	// Warning-only -> Warning.
	_, status = summarizeFindings([]finding.Finding{{Severity: finding.SeverityWarning}})
	if status != pactov1alpha1.ContractStatusWarning {
		t.Errorf("expected Warning, got %s", status)
	}

	// No findings -> Compliant.
	summary, status = summarizeFindings(nil)
	if status != pactov1alpha1.ContractStatusCompliant {
		t.Errorf("expected Compliant, got %s", status)
	}
	if summary.ErrorCount != 0 || summary.WarningCount != 0 || summary.InfoCount != 0 {
		t.Errorf("expected zero counts, got %+v", summary)
	}
}

// ---------- applyConfigurationOverrides: nil / empty overrides ----------

func TestApplyConfigurationOverrides_NilAndEmpty(t *testing.T) {
	c := &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}}

	got, keys, err := applyConfigurationOverrides(c, nil)
	if err != nil || got != c || keys != nil {
		t.Errorf("nil overrides: expected (c, nil, nil), got (%v, %v, %v)", got, keys, err)
	}

	got, keys, err = applyConfigurationOverrides(c, &pactov1alpha1.ContractOverrides{})
	if err != nil || got != c || keys != nil {
		t.Errorf("empty overrides: expected (c, nil, nil), got (%v, %v, %v)", got, keys, err)
	}
}

// ---------- ensureRevision internal branches ----------

func newSchemeWithApps() *runtime.Scheme {
	s := newScheme()
	_ = appsv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	return s
}

func ensureRevisionReconciler(t *testing.T, objs ...client.Object) *PactoReconciler {
	t.Helper()
	s := newScheme()
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{})
	if len(objs) > 0 {
		cb = cb.WithObjects(objs...)
	}
	return &PactoReconciler{Client: cb.Build(), Scheme: s, Recorder: record.NewFakeRecorder(20), Loader: &mockLoader{}}
}

// Empty contract version -> revision name uses "unknown" (654-656).
func TestEnsureRevision_EmptyVersion(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "ev", Namespace: "default", UID: "u"}}
	r := ensureRevisionReconciler(t, pacto)
	lr := &loader.LoadResult{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc"}},
		RawYAML:  []byte("yaml"),
	}
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(name, "unknown") {
		t.Errorf("expected revision name to contain 'unknown', got %q", name)
	}
}

// Very long pacto name -> revision name truncated to 253 chars (660-662).
func TestEnsureRevision_NameTruncated(t *testing.T) {
	longName := strings.Repeat("a", 300)
	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: longName, Namespace: "default", UID: "u"}}
	r := ensureRevisionReconciler(t, pacto)
	lr := &loader.LoadResult{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:  []byte("yaml"),
	}
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(name) != 253 {
		t.Errorf("expected truncated name length 253, got %d", len(name))
	}
}

// Existing revision with unpopulated status -> backfill; status update error is logged (667-676).
func TestEnsureRevision_BackfillStatusUpdateError(t *testing.T) {
	pactoName := "bf"
	rawYAML := []byte("bf-yaml")
	hash := fmt.Sprintf("%x", sha256.Sum256(rawYAML))
	shortHash := hash[:7]
	revName := fmt.Sprintf("%s-1-0-0-%s", pactoName, shortHash)

	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: pactoName, Namespace: "default", UID: "u"}}
	existing := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{Name: revName, Namespace: "default"},
		// Status left empty: ContractHash == "" and CreatedAt == nil -> triggers backfill.
	}

	s := newScheme()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto, existing).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("backfill status update failed")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(20), Loader: &mockLoader{}}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:  rawYAML,
	}
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error (status error is only logged): %v", err)
	}
	if name != revName {
		t.Errorf("expected revision name %q, got %q", revName, name)
	}
}

// ResolvedRef empty but spec OCI set -> source.OCI from spec (688-691).
func TestEnsureRevision_SourceFromSpecOCI(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "spec-oci", Namespace: "default", UID: "u"},
		Spec:       pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc:1.0.0"}},
	}
	r := ensureRevisionReconciler(t, pacto)
	lr := &loader.LoadResult{
		Contract:       &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:        []byte("yaml"),
		ResolvedRef:    "",
		ResolvedDigest: "sha256:digest",
	}
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rev := &pactov1alpha1.PactoRevision{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: "default"}, rev); err != nil {
		t.Fatalf("failed to get created revision: %v", err)
	}
	if rev.Spec.Source.OCI != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected source.OCI from spec, got %q", rev.Spec.Source.OCI)
	}
}

// SetControllerReference fails when the owner type is not registered in the scheme (713-715).
func TestEnsureRevision_SetOwnerReferenceError(t *testing.T) {
	// Scheme registers PactoRevision but intentionally NOT Pacto.
	s := runtime.NewScheme()
	s.AddKnownTypes(pactov1alpha1.GroupVersion, &pactov1alpha1.PactoRevision{}, &pactov1alpha1.PactoRevisionList{})
	metav1.AddToGroupVersion(s, pactov1alpha1.GroupVersion)

	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &PactoReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(20), Loader: &mockLoader{}}

	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "no-gvk", Namespace: "default", UID: "u"}}
	lr := &loader.LoadResult{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:  []byte("yaml"),
	}
	_, err := r.ensureRevision(context.Background(), pacto, lr)
	if err == nil {
		t.Fatal("expected error from SetControllerReference")
	}
	if !strings.Contains(err.Error(), "owner reference") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Create returns AlreadyExists -> ensureRevision returns the name without error (718-720).
func TestEnsureRevision_CreateAlreadyExists(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "ae", Namespace: "default", UID: "u"}}
	s := newScheme()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return apierrors.NewAlreadyExists(schema.GroupResource{Group: "pacto.trianalab.io", Resource: "pactorevisions"}, obj.GetName())
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(20), Loader: &mockLoader{}}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:  []byte("yaml"),
	}
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error on AlreadyExists: %v", err)
	}
	if name == "" {
		t.Error("expected non-empty revision name on AlreadyExists")
	}
}

// Create succeeds but the post-create status update fails -> logged, no error (729-731).
func TestEnsureRevision_PostCreateStatusUpdateError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "pcsu", Namespace: "default", UID: "u"}}
	s := newScheme()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("post-create status update failed")
				}
				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(20), Loader: &mockLoader{}}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:  []byte("yaml"),
	}
	name, err := r.ensureRevision(context.Background(), pacto, lr)
	if err != nil {
		t.Fatalf("unexpected error (status error is only logged): %v", err)
	}
	if name == "" {
		t.Error("expected non-empty revision name")
	}
}

// ---------- syncAllRevisions: force-push + ensureRevision error (795-797) ----------

func TestSyncAllRevisions_ForcePush_EnsureRevisionError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "fp-rev-err", Namespace: "default", UID: "u"},
	}
	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fp-rev-err-1-0-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "fp-rev-err",
				pactov1alpha1.LabelRevisionVersion: "1.0.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "fp-rev-err",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0", Digest: "sha256:olddigest000"},
		},
	}

	s := newScheme()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto, existingRev).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("create failed")
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{
			listTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.0.0"}, nil },
			loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
				return &loader.LoadResult{
					Contract:       &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
					RawYAML:        []byte("new-yaml"),
					ResolvedDigest: "sha256:newdigest111",
				}, nil
			},
		},
	}

	if err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil); err != nil {
		t.Fatalf("unexpected error (should continue on ensureRevision error): %v", err)
	}
}

// Sanity: setCondition helper reachable via a non-reference reconcile is exercised
// by the drift test above; this assert guards the meta helper wiring.
func TestReconcile_DriftSetsRuntimeObservedCondition(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "drift2", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
			Target:      pactov1alpha1.TargetRef{ServiceName: "svc2"},
		},
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc2", Namespace: "default"}}
	s := newSchemeWithApps()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto, svc).Build()
	r := &PactoReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}, Workload: "service"},
				RawYAML:  []byte(validContract),
			}, nil
		}},
	}
	if _, err := r.Reconcile(context.Background(), reconcileReq("drift2", "default")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := getPacto(t, r, "drift2", "default")
	cond := meta.FindStatusCondition(got.Status.Conditions, pactov1alpha1.ConditionRuntimeObserved)
	if cond == nil {
		t.Error("expected RuntimeObserved condition")
	}
}
