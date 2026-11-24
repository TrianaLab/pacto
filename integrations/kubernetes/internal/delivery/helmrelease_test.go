/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	ctrlrt "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func hrStage(pointer string) *dv1.Stage {
	s := stageObj(pointer)
	s.Spec.ApplyRef.Kind = delivery.ApplierHelmRelease
	return s
}

// hrOp is a promote whose intent digest binds the HelmRelease Stage's policy.
func hrOp(from, to string) *dv1.Operation {
	o := opObj("p1", delivery.TypePromote, from, to, bot)
	o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, hrStage("").Spec)
	return o
}

func (e *env) setHR(u *unstructured.Unstructured) {
	e.t.Helper()
	_ = e.c.Delete(context.Background(), u)
	e.create(u)
}

func (e *env) chart() *unstructured.Unstructured {
	e.t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCI)
	if err := e.c.Get(context.Background(), keyOf("payments-chart"), u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

func withHR(u *unstructured.Unstructured, edit func(map[string]any)) *unstructured.Unstructured {
	edit(u.Object)
	return u
}

// TestHelmReleaseApplierReads: the applier of a HelmRelease Stage is the
// HelmRelease, and its version source is the OCIRepository its chartRef
// names in the Stage namespace, with or without an explicit namespace. A
// HelmRelease is read from the cache only while the promoter watches them.
func TestHelmReleaseApplierReads(t *testing.T) {
	for name, hr := range map[string]*unstructured.Unstructured{
		"no namespace": helmReleaseObj("True", "", dA, dA),
		"the Stage namespace": withHR(helmReleaseObj("True", "", dA, dA), func(o map[string]any) {
			_ = unstructured.SetNestedField(o, ns, "spec", "chartRef", "namespace")
		}),
	} {
		e := newEnv(t, hrStage(dA), hr, chartRepo(dA))
		a, err := e.op.Cluster.freshApplier(t.Context(), ns, hrStage(dA).Spec.ApplyRef)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.object().GetName() != "payments" || a.src().obj.GetName() != "payments-chart" || a.src().digest != dA || a.src().repo != repo ||
			a.target() != "spec.ref.digest of OCIRepository payments-chart (chart of HelmRelease payments)" {
			t.Fatalf("%s: %s %s %s %s", name, a.object().GetName(), a.src().obj.GetName(), a.src().digest, a.target())
		}
	}
	e := newEnv(t, hrStage(dA), helmReleaseObj("True", "", dA, dA), chartRepo(dA))
	stale := helmReleaseObj("True", "", dA, dA)
	stale.SetLabels(map[string]string{ApplyRefLabel: "cached"})
	e.staleCache(stale, chartRepo(dA))
	read := func() string {
		a, err := e.op.Cluster.applier(t.Context(), ns, hrStage(dA).Spec.ApplyRef)
		if err != nil {
			t.Fatal(err)
		}
		return a.object().GetLabels()[ApplyRefLabel]
	}
	if got := read(); got != "payments-prod" {
		t.Fatalf("an unwatched HelmRelease is read live, got %q", got)
	}
	e.op.Cluster.FluxHelm = true
	if got := read(); got != "cached" {
		t.Fatalf("a watched HelmRelease is read from the cache, got %q", got)
	}
}

// TestHelmReleaseRefusesCrossNamespaceChart: a chartRef naming an
// OCIRepository in another namespace is refused on Ready, by name, and the
// promoter neither reads nor writes that OCIRepository during a promote.
func TestHelmReleaseRefusesCrossNamespaceChart(t *testing.T) {
	hr := withHR(helmReleaseObj("True", "", dA, dA), func(o map[string]any) {
		_ = unstructured.SetNestedField(o, "other", "spec", "chartRef", "namespace")
	})
	foreign := chartRepo(dA)
	foreign.SetNamespace("other")
	e := newEnv(t, hrStage(dA), hr, foreign, verifiedRepo("payments-candidates", "latest@"+dB),
		helmDeployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	ready := conditionOf(e.getStage().Status.Conditions, "Ready")
	if ready == nil || ready.Reason != delivery.ReadyReasonApplierUnreadable || !strings.Contains(ready.Message, "other/payments-chart") {
		t.Fatalf("want Ready False ApplierUnreadable naming other/payments-chart: %+v", ready)
	}
	var touched atomic.Int32
	spy := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if kindOf(obj) == kindOCIRepository && key.Namespace == "other" {
				touched.Add(1)
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if kindOf(obj) == kindOCIRepository {
				touched.Add(1)
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	}
	e.op.Cluster.Client = interceptor.NewClient(e.c, spy)
	e.st.Cluster.Client = e.op.Cluster.Client
	e.create(hrOp(dA, dB))
	o := e.drive("p1")
	if touched.Load() != 0 || o.Status.Outcome == delivery.Succeeded {
		t.Fatalf("the foreign chart source was read or written (%d) or the promote succeeded: %+v", touched.Load(), o.Status)
	}
	if err := e.reconcileStage("payments-prod"); err != nil || touched.Load() != 0 {
		t.Fatalf("a Stage reconcile touched the foreign chart source: %v %d", err, touched.Load())
	}
}

// TestHelmReleaseApplied: applied means Ready at the digest; a failure at
// the digest is confirmed; progress and another digest are "not yet".
func TestHelmReleaseApplied(t *testing.T) {
	noHistory := helmReleaseObj("True", "", "", dB)
	_ = unstructured.SetNestedSlice(noHistory.Object, []any{map[string]any{"status": "deployed"}}, "status", "history")
	empty := helmReleaseObj("True", "", "", dB)
	unstructured.RemoveNestedField(empty.Object, "status", "history")
	for _, tc := range []struct {
		name       string
		hr         *unstructured.Unstructured
		ok, failed bool
		msg        string
	}{
		{"ready at the digest", helmReleaseObj("True", "UpgradeSucceeded", dB, dB), true, false, ""},
		{"ready at another digest", helmReleaseObj("True", "UpgradeSucceeded", dA, dA), false, false, "has not released"},
		{"ready at another digest after attempting it", helmReleaseObj("True", "", dA, dB), false, false, "has not released"},
		{"progressing", helmReleaseObj("Unknown", "Progressing", dA, dB), false, false, "has not released"},
		{"progressing with retry", helmReleaseObj("False", "ProgressingWithRetry", dA, dB), false, false, "has not released"},
		{"dependency not ready", helmReleaseObj("False", "DependencyNotReady", dA, dB), false, false, "has not released"},
		{"failed at the digest", helmReleaseObj("False", "UpgradeFailed", dA, dB), false, true, "failed to release " + dB + ": UpgradeFailed"},
		{"failed at another digest", helmReleaseObj("False", "UpgradeFailed", dA, dA), false, false, "has not released"},
		{"history without a digest", noHistory, true, false, ""},
		{"no history", empty, true, false, ""},
	} {
		a := helmReleaseApplier{hr: tc.hr, chart: &source{obj: chartRepo(dB)}}
		ok, failed, msg, err := a.applied(t.Context(), Cluster{}, ns, dB)
		if err != nil || ok != tc.ok || failed != tc.failed || !strings.Contains(msg, tc.msg) {
			t.Errorf("%s: ok=%v failed=%v msg=%q err=%v", tc.name, ok, failed, msg, err)
		}
	}
}

// TestHelmReleasePromoteShipsAndVerifies: on a Stage applied by a HelmRelease
// the promoter writes only the chart OCIRepository's spec.ref.digest, asks
// Flux to reconcile and confirms the move once the HelmRelease is Ready at
// that digest.
func TestHelmReleasePromoteShipsAndVerifies(t *testing.T) {
	e := newEnv(t, hrStage(dA), helmReleaseObj("True", "", dA, dA), chartRepo(dA),
		helmDeployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	e.create(hrOp(dA, dB))
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseRunning || e.ociDigest("payments-chart") != dB || e.chart().GetAnnotations()["reconcile.fluxcd.io/requestedAt"] == "" {
		t.Fatalf("want Running with the chart digest written and a reconcile asked, got %s %s (%s)", o.Status.Phase, e.ociDigest("payments-chart"), o.Status.Message)
	}
	if o = e.drive("p1"); !strings.Contains(o.Status.Message, "has not released") {
		t.Fatalf("waits for helm-controller: %q", o.Status.Message)
	}
	e.setHR(helmReleaseObj("Unknown", "Progressing", dA, dB))
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("Progressing is not released yet: %s %q", o.Status.Phase, o.Status.Message)
	}
	e.setHR(helmReleaseObj("True", "UpgradeSucceeded", dB, dB))
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseVerifying {
		t.Fatalf("want Verifying once the HelmRelease released B, got %s (%s)", o.Status.Phase, o.Status.Message)
	}
	e.evaluate(c2, "Compliant")
	wantOutcome(t, e.drive("p1"), delivery.Succeeded, "Verified")
}

// TestHelmReleaseRollsBack: a release of the digest that fails is a
// confirmed failure, and the automatic rollback writes the previous digest
// back on the chart source.
func TestHelmReleaseRollsBack(t *testing.T) {
	e := newEnv(t, hrStage(dA), helmReleaseObj("True", "", dA, dA), chartRepo(dA),
		helmDeployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	e.create(hrOp(dA, dB))
	e.drive("p1")
	e.setHR(helmReleaseObj("False", "UpgradeFailed", dA, dB))
	wantOutcome(t, e.drive("p1"), delivery.Failed, "ApplyFailed")
	rb := e.drive("p1-rollback")
	if rb.Spec.Type != delivery.TypeRollback || rb.Status.Phase != delivery.PhaseRunning || e.ociDigest("payments-chart") != dA {
		t.Fatalf("want the rollback running with A written back: %s %s %s (%s)", rb.Spec.Type, rb.Status.Phase, e.ociDigest("payments-chart"), rb.Status.Message)
	}
}

// TestHelmReleaseMediated: values, valuesFrom and postRenderers, a missing
// serviceAccountName and an unguarded chart source are named, by path and
// never by value; a clean HelmRelease is mediated by its pointer alone.
func TestHelmReleaseMediated(t *testing.T) {
	set := func(path ...string) func(map[string]any) {
		return func(o map[string]any) { _ = unstructured.SetNestedField(o, map[string]any{"x": "y"}, path...) }
	}
	for _, tc := range []struct {
		name, reason, msg string
		edit              func(map[string]any)
		chartLabels       map[string]string
	}{
		{"clean", "PointerOnly", "only the pointer", func(map[string]any) {}, nil},
		{"values", "ApplierOverrides", "sets spec.values", set("spec", "values"), nil},
		{"valuesFrom", "ApplierOverrides", "sets spec.valuesFrom", func(o map[string]any) {
			_ = unstructured.SetNestedSlice(o, []any{map[string]any{"kind": "Secret", "name": "v"}}, "spec", "valuesFrom")
		}, nil},
		{"postRenderers", "ApplierOverrides", "sets spec.postRenderers", func(o map[string]any) {
			_ = unstructured.SetNestedSlice(o, []any{map[string]any{"kustomize": map[string]any{}}}, "spec", "postRenderers")
		}, nil},
		{"no service account", "ApplierUnscoped", "sets no serviceAccountName", func(o map[string]any) {
			unstructured.RemoveNestedField(o, "spec", "serviceAccountName")
		}, nil},
		{"unlabelled chart", "ApplyRefUnguarded", "chart OCIRepository payments-chart", func(map[string]any) {}, map[string]string{}},
		{"unlabelled HelmRelease", "ApplyRefUnguarded", "the apply object payments", func(o map[string]any) {
			unstructured.RemoveNestedField(o, "metadata", "labels")
		}, nil},
	} {
		chart := chartRepo(dA)
		if tc.chartLabels != nil {
			chart.SetLabels(tc.chartLabels)
		}
		a := helmReleaseApplier{hr: withHR(helmReleaseObj("True", "", dA, dA), tc.edit), chart: &source{obj: chart}}
		c, err := Cluster{}.mediated(t.Context(), ns, "prod", a)
		if err != nil || c.Reason != tc.reason || !strings.Contains(c.Message, tc.msg) {
			t.Errorf("%s: %+v %v", tc.name, c, err)
		}
	}
	// A secret in inline values reaches no condition, verdict or record.
	secret := withHR(helmReleaseObj("True", "", dA, dA), func(o map[string]any) {
		_ = unstructured.SetNestedField(o, map[string]any{"password": "s3cr3t"}, "spec", "values")
	})
	e := newEnv(t, hrStage(dA), secret, chartRepo(dA), verifiedRepo("payments-candidates", "latest@"+dB),
		helmDeployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	med := conditionOf(e.getStage().Status.Conditions, ConditionMediated)
	if med == nil || med.Reason != "ApplierOverrides" || !strings.Contains(med.Message, "spec.values") {
		t.Fatalf("Mediated names the path: %+v", med)
	}
	e.create(hrOp(dA, dB))
	e.drive("p1")
	stage, _ := json.Marshal(e.getStage())
	op, _ := json.Marshal(e.getOp("p1"))
	if strings.Contains(string(stage)+string(op), "s3cr3t") {
		t.Fatalf("a value of spec.values reached the Stage or the operation:\n%s\n%s", stage, op)
	}
}

// TestHelmReleaseNotReady: what keeps a HelmRelease from taking a version is
// named on Ready, with the fix for a chart from spec.chart.
func TestHelmReleaseNotReady(t *testing.T) {
	hr := func(edit func(map[string]any)) *unstructured.Unstructured {
		return withHR(helmReleaseObj("True", "", dA, dA), edit)
	}
	other := chartRepo(dA)
	other.SetLabels(map[string]string{ApplyRefLabel: "payments-test"})
	unlabelled := chartRepo(dA)
	unlabelled.SetLabels(nil)
	for _, tc := range []struct {
		name   string
		objs   []client.Object
		reason string
	}{
		{"ready", []client.Object{hr(func(map[string]any) {}), chartRepo(dA)}, "Ready"},
		{"spec.chart", []client.Object{hr(func(o map[string]any) {
			unstructured.RemoveNestedField(o, "spec", "chartRef")
			_ = unstructured.SetNestedMap(o, map[string]any{"spec": map[string]any{"chart": "payments"}}, "spec", "chart")
		})}, delivery.ReadyReasonApplierNotChartRef},
		{"chartRef to a HelmChart", []client.Object{hr(func(o map[string]any) {
			_ = unstructured.SetNestedField(o, "HelmChart", "spec", "chartRef", "kind")
		})}, delivery.ReadyReasonApplierUnreadable},
		{"no chartRef", []client.Object{hr(func(o map[string]any) { unstructured.RemoveNestedField(o, "spec", "chartRef") })}, delivery.ReadyReasonApplierUnreadable},
		{"chart source missing", []client.Object{hr(func(map[string]any) {})}, delivery.ReadyReasonApplierUnreadable},
		{"HelmRelease missing", nil, delivery.ReadyReasonApplierUnreadable},
		{"unlabelled HelmRelease", []client.Object{hr(func(o map[string]any) { unstructured.RemoveNestedField(o, "metadata", "labels") }), chartRepo(dA)},
			delivery.ReadyReasonApplierUnlabelled},
		{"unlabelled chart source", []client.Object{hr(func(map[string]any) {}), unlabelled}, delivery.ReadyReasonApplierUnlabelled},
		{"chart source of another Stage", []client.Object{hr(func(map[string]any) {}), other}, delivery.ReadyReasonApplierUnlabelled},
	} {
		e := newEnv(t, append(tc.objs, hrStage(dA), verifiedRepo("payments-candidates", "latest@"+dB))...)
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		ready := conditionOf(e.getStage().Status.Conditions, "Ready")
		if ready == nil || ready.Reason != tc.reason || ready.Message == "" {
			t.Errorf("%s: Ready %+v", tc.name, ready)
		}
	}
	if fix := delivery.ReadyFix(delivery.ReadyReasonApplierNotChartRef); !strings.Contains(fix, "spec.chartRef at an OCIRepository") {
		t.Fatalf("the fix names chartRef: %q", fix)
	}
}

// TestHelmReleaseWitness: an edit of the chart digest outside Pacto is
// witnessed, naming the chart source and its HelmRelease.
func TestHelmReleaseWitness(t *testing.T) {
	e := newEnv(t, hrStage(dA), helmReleaseObj("True", "", dC, dC), chartRepo(dC))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	ops := e.ops()
	if len(ops) != 1 || !ops[0].Spec.Witnessed || ops[0].Spec.Params.To != dC ||
		!strings.Contains(ops[0].Spec.Reason, "spec.ref.digest of OCIRepository payments-chart (chart of HelmRelease payments)") {
		t.Fatalf("want one witnessed override naming the chart source: %+v", ops)
	}
}

// TestStageRefKeysHelmRelease: a HelmRelease Stage is indexed by its
// HelmRelease, an event on it wakes the Stage, and the stage reconciler
// watches HelmReleases only when the promoter is told to.
func TestStageRefKeysHelmRelease(t *testing.T) {
	s := hrStage(dA)
	if keys := stageRefKeys(s); !slices.Contains(keys, refKey(ns, delivery.ApplierHelmRelease, "payments")) ||
		slices.Contains(keys, refKey(ns, kindOCIRepository, "payments")) {
		t.Fatalf("keys %v", keys)
	}
	r := &StageReconciler{Client: fake.NewClientBuilder().WithScheme(newScheme()).WithIndex(&dv1.Stage{}, stageRefs, stageRefKeys).WithObjects(s).Build()}
	hr := helmReleaseObj("True", "", dA, dA)
	if reqs := r.stagesReading(helmReleaseKeys)(t.Context(), hr); len(reqs) != 1 || reqs[0].Name != "payments-prod" {
		t.Fatalf("a HelmRelease event woke %+v", reqs)
	}
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(dv1.GroupVersion.WithKind("Stage"), meta.RESTScopeNamespace)
	mapper.Add(gvkHelmRelease, meta.RESTScopeNamespace)
	skip := true
	mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
		Metrics: metricsserver.Options{BindAddress: "0"}, Controller: config.Controller{SkipNameValidation: &skip},
		MapperProvider: func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&StageReconciler{Cluster: Cluster{FluxHelm: true}}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
}
