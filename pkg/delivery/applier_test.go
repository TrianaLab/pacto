package delivery

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

var applierNow = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)

func argoOwner(ns, app string) workloadmanager.Manager {
	return workloadmanager.Manager{Name: workloadmanager.ArgoCD, Product: "Argo CD", OwnerKind: "Application",
		OwnerNamespace: ns, OwnerName: app, Evidence: "tracking annotation"}
}

func kustomizationOwner(ns, name string) workloadmanager.Manager {
	return workloadmanager.Manager{Name: workloadmanager.FluxKustomization, Product: "Flux", OwnerKind: "Kustomization",
		OwnerNamespace: ns, OwnerName: name, Evidence: "kustomize-controller labels"}
}

func helmReleaseOwner(ns, name string) workloadmanager.Manager {
	return workloadmanager.Manager{Name: workloadmanager.FluxHelmRelease, Product: "Flux", OwnerKind: "HelmRelease",
		OwnerNamespace: ns, OwnerName: name, Evidence: "helm-controller labels"}
}

func kustomizationOf(ns, name, srcKind, srcNS, srcName string) KustomizationView {
	var k KustomizationView
	k.Metadata.Namespace, k.Metadata.Name = ns, name
	k.Spec.SourceRef.Kind, k.Spec.SourceRef.Namespace, k.Spec.SourceRef.Name = srcKind, srcNS, srcName
	return k
}

func adopt(ref ApplyRef, ks []KustomizationView, ms ...workloadmanager.Manager) ApplierStatus {
	in := ApplierInput{Namespace: "payments", Ref: ref, Kustomizations: ks, Now: applierNow}
	for i, m := range ms {
		in.Targets = append(in.Targets, ApplierTarget{Workload: "Deployment w" + string(rune('0'+i)), Manager: m})
	}
	return AdoptApplier(in)
}

func wantState(t *testing.T, name string, got ApplierStatus, state, msg string) {
	t.Helper()
	if got.State != state || !strings.Contains(got.Message, msg) {
		t.Errorf("%s: want %s containing %q, got %s %q", name, state, msg, got.State, got.Message)
	}
}

var argoRef = ApplyRef{Kind: ApplierArgoCD, Name: "payments"}

func TestAdoptApplierArgo(t *testing.T) {
	got := adopt(argoRef, nil, argoOwner("payments", "payments"))
	wantState(t, "agree", got, ApplierAdopted, "")
	if got.Manager != "argocd" || got.Owner != "Application payments/payments" || got.Display != "Argo CD (Application payments/payments, tracking annotation)" ||
		got.Completeness != "complete" || got.AsOf != applierNow.Format(time.RFC3339) || got.Since != got.AsOf || got.Workloads[0] != "Deployment w0" {
		t.Errorf("agree: %+v", got)
	}
	wantState(t, "another app", adopt(argoRef, nil, argoOwner("payments", "orders")), ApplierConflict, "Application payments/orders owns Deployment w0")
	wantState(t, "another namespace", adopt(argoRef, nil, argoOwner("argocd", "payments")), ApplierConflict, "payments/payments")
	wantState(t, "plain id", adopt(argoRef, nil, argoOwner("", "payments")), ApplierConflict,
		"Application payments in the Argo CD namespace owns Deployment w0; the Stage applies through payments/payments")
	wantState(t, "flux applyRef", adopt(ApplyRef{Name: "payments"}, nil, argoOwner("payments", "payments")), ApplierConflict,
		"the Stage applies through OCIRepository payments/payments: Pacto never writes a pointer another manager overwrites")
}

func TestAdoptApplierFluxKustomization(t *testing.T) {
	ref := ApplyRef{Name: "payments"}
	agree := []KustomizationView{kustomizationOf("flux-system", "payments", "OCIRepository", "payments", "payments")}
	got := adopt(ref, agree, kustomizationOwner("flux-system", "payments"))
	wantState(t, "agree", got, ApplierAdopted, "")
	if got.Manager != "flux" || got.Owner != "Kustomization flux-system/payments" {
		t.Errorf("agree: %+v", got)
	}
	same := []KustomizationView{kustomizationOf("payments", "payments", "OCIRepository", "", "payments")}
	wantState(t, "sourceRef namespace defaults to the Kustomization's", adopt(ApplyRef{Kind: ApplierFlux, Name: "payments"}, same,
		kustomizationOwner("payments", "payments")), ApplierAdopted, "")
	other := []KustomizationView{kustomizationOf("payments", "payments", "OCIRepository", "payments", "orders")}
	wantState(t, "another repo", adopt(ref, other, kustomizationOwner("payments", "payments")), ApplierConflict,
		"applies from OCIRepository payments/orders")
	git := []KustomizationView{kustomizationOf("payments", "payments", "GitRepository", "payments", "payments")}
	wantState(t, "git source", adopt(ref, git, kustomizationOwner("payments", "payments")), ApplierConflict, "GitRepository")
	wantState(t, "missing", adopt(ref, nil, kustomizationOwner("payments", "payments")), ApplierUnknown,
		"Pacto could not read Kustomization payments/payments")
	wantState(t, "argo applyRef", adopt(argoRef, agree, kustomizationOwner("flux-system", "payments")), ApplierConflict, "Kustomization")
	unlisted := ApplierInput{Namespace: "payments", Ref: ref, Now: applierNow,
		Unread:  []string{"the Kustomizations: the promoter runs with --flux=false"},
		Targets: []ApplierTarget{{Workload: "Deployment w0", Manager: kustomizationOwner("payments", "payments")}}}
	wantState(t, "unlisted names the cause", AdoptApplier(unlisted), ApplierUnknown,
		"Pacto could not read Kustomization payments/payments, which manages Deployment w0 (the Kustomizations: the promoter runs with --flux=false)")
}

// TestAdoptApplierForeignKustomizationIsOneVerdict: a Kustomization outside
// the Stage namespace that does not apply from the Stage's OCIRepository
// reads the same whether it is missing or applies from elsewhere, and never
// names its source: the workload's labels are not a way to read another
// namespace.
func TestAdoptApplierForeignKustomizationIsOneVerdict(t *testing.T) {
	ref, owner := ApplyRef{Name: "payments"}, kustomizationOwner("team-b", "billing")
	missing := adopt(ref, nil, owner)
	for name, ks := range map[string][]KustomizationView{
		"another repo": {kustomizationOf("team-b", "billing", "OCIRepository", "team-b", "billing-prod")},
		"git source":   {kustomizationOf("team-b", "billing", "GitRepository", "", "billing")},
	} {
		got := adopt(ref, ks, owner)
		if !SameApplier(&missing, &got) || strings.Contains(got.Message, "billing-prod") || strings.Contains(got.Message, "GitRepository") {
			t.Errorf("%s: want the missing verdict %+v, got %+v", name, missing, got)
		}
	}
	wantState(t, "foreign", missing, ApplierUnknown,
		"Pacto cannot confirm that Kustomization team-b/billing, which the labels of Deployment w0 name, applies from OCIRepository payments/payments")
}

func TestAdoptApplierHelmRelease(t *testing.T) {
	ref := ApplyRef{Kind: ApplierHelmRelease, Name: "payments"}
	got := adopt(ref, nil, helmReleaseOwner("payments", "payments"))
	wantState(t, "agree", got, ApplierAdopted, "")
	if got.Manager != "flux" || got.Owner != "HelmRelease payments/payments" {
		t.Errorf("agree: %+v", got)
	}
	wantState(t, "another name", adopt(ref, nil, helmReleaseOwner("payments", "orders")), ApplierConflict, "HelmRelease payments/orders")
	wantState(t, "another namespace", adopt(ref, nil, helmReleaseOwner("other", "payments")), ApplierConflict, "HelmRelease other/payments")
	wantState(t, "oci applyRef", adopt(ApplyRef{Name: "payments"}, nil, helmReleaseOwner("payments", "payments")), ApplierConflict, "HelmRelease")
}

func TestAdoptApplierConflicts(t *testing.T) {
	helm := workloadmanager.Manager{Name: workloadmanager.Helm, Product: "Helm", OwnerKind: "release", OwnerNamespace: "payments",
		OwnerName: "payments", Evidence: "release annotations"}
	wantState(t, "argo owner, flux applyRef", adopt(ApplyRef{Name: "payments"}, nil, argoOwner("payments", "payments")), ApplierConflict, "Argo CD")
	got := adopt(argoRef, nil, helm)
	wantState(t, "plain helm", got, ApplierConflict, "Helm release payments owns Deployment w0")
	if got.Manager != "helm" || got.Owner != "release payments" {
		t.Errorf("plain helm: %+v", got)
	}
	two := adopt(argoRef, nil, argoOwner("payments", "payments"), kustomizationOwner("flux-system", "payments"))
	wantState(t, "two managers", two, ApplierConflict, "Deployment w1")
	if two.Manager != "flux" || len(two.Workloads) != 2 {
		t.Errorf("the conflict names the workload that contradicts: %+v", two)
	}
}

func TestAdoptApplierUnmanaged(t *testing.T) {
	got := AdoptApplier(ApplierInput{Namespace: "payments", Service: "payments", Ref: ApplyRef{Kind: ApplierHelmRelease, Name: "payments-chart"}, Now: applierNow})
	wantState(t, "no targets", got, ApplierUnmanaged, "no workload to check yet: no Pacto for service payments in payments reports one")
	if got.Manager != "none" || got.Display != "unmanaged" || got.Completeness != "complete" || strings.Contains(got.Message, "payments-chart") {
		t.Errorf("no targets: %+v", got)
	}
	gone := AdoptApplier(ApplierInput{Namespace: "payments", Service: "payments", Ref: argoRef, Missing: []string{"Deployment payments"}, Now: applierNow})
	wantState(t, "a reported workload that is gone", gone, ApplierUnmanaged, "no workload to check yet: Deployment payments, which a Pacto reports, does not exist yet")
}

func TestAdoptApplierNoneIsUnknown(t *testing.T) {
	none := workloadmanager.Manager{Name: workloadmanager.None}
	for _, ref := range []ApplyRef{argoRef, {Name: "payments"}, {Kind: ApplierHelmRelease, Name: "payments"}} {
		got := adopt(ref, nil, none, none)
		wantState(t, ref.Kind, got, ApplierUnknown, "leaves no mark on Deployment w0")
		// No marks prove nothing: the display never says "unmanaged".
		if got.Manager != "none" || got.Display != "no manager marks" {
			t.Errorf("%s: %+v", ref.Kind, got)
		}
	}
}

func TestAdoptApplierMixedNoneIsUnknown(t *testing.T) {
	got := adopt(argoRef, nil, argoOwner("payments", "payments"), workloadmanager.Manager{Name: workloadmanager.None})
	wantState(t, "mixed", got, ApplierUnknown, "leaves no mark on Deployment w1")
}

func TestAdoptApplierEmptyOwnerIsUnknown(t *testing.T) {
	m := workloadmanager.Manager{Name: workloadmanager.ArgoCD, Product: "Argo CD", OwnerKind: "Application", Evidence: "field manager argocd-controller"}
	wantState(t, "fallback", adopt(argoRef, nil, m), ApplierUnknown, "only field manager argocd-controller names the manager of Deployment w0")
}

func TestAdoptApplierManagerEnum(t *testing.T) {
	for name, want := range map[string]string{
		workloadmanager.ArgoCD: "argocd", workloadmanager.FluxKustomization: "flux", workloadmanager.FluxHelmRelease: "flux",
		workloadmanager.Helm: "helm", workloadmanager.Unsupported: "unsupported", workloadmanager.Ambiguous: "ambiguous",
		workloadmanager.None: "none", "": "none", "something-new": "unsupported",
	} {
		if got := managerEnum(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
	if got := adopt(ApplyRef{Name: "payments"}, nil, kustomizationOwner("flux-system", "payments")); got.Manager != "flux" ||
		!strings.HasPrefix(got.Owner, "Kustomization ") {
		t.Errorf("flux kustomization: %+v", got)
	}
}

func TestAdoptApplierUnreadableIsUnknown(t *testing.T) {
	in := ApplierInput{Namespace: "payments", Ref: argoRef, Now: applierNow, Unread: []string{"Deployment w1: forbidden"},
		Targets: []ApplierTarget{{Workload: "Deployment w0", Manager: argoOwner("payments", "payments")}}}
	got := AdoptApplier(in)
	wantState(t, "one unread", got, ApplierUnknown, "Pacto could not read Deployment w1: forbidden")
	if got.Completeness != "partial" {
		t.Errorf("one unread: %+v", got)
	}
	in.Targets = nil
	got = AdoptApplier(in)
	wantState(t, "nothing read", got, ApplierUnknown, "Pacto could not read")
	if got.Completeness != "partial" || got.Manager != "" {
		t.Errorf("nothing read: %+v", got)
	}
	in.Targets = []ApplierTarget{{Workload: "Deployment w0", Manager: argoOwner("payments", "orders")}}
	if got = AdoptApplier(in); got.State != ApplierConflict || got.Completeness != "partial" {
		t.Errorf("a confirmed contradiction stays one: %+v", got)
	}
}

func TestAdoptApplierUnsupportedIsUnknown(t *testing.T) {
	fleetMark := workloadmanager.Manager{Name: workloadmanager.Unsupported, Product: "Rancher Fleet", Evidence: "bundle annotation"}
	got := adopt(argoRef, nil, fleetMark)
	wantState(t, "unsupported", got, ApplierUnknown, "Rancher Fleet (bundle annotation), not supported, manages Deployment w0")
	if got.Manager != "unsupported" || got.Owner != "" {
		t.Errorf("unsupported: %+v", got)
	}
	amb := workloadmanager.Manager{Name: workloadmanager.Ambiguous, Evidence: "Argo CD tracking annotation and Flux kustomize-controller labels"}
	wantState(t, "ambiguous", adopt(argoRef, nil, amb), ApplierUnknown, "marks of more than one manager")
	other := workloadmanager.Manager{Name: "something-new", Product: "Something", OwnerKind: "Thing", OwnerName: "x", Evidence: "a mark"}
	wantState(t, "a name this core does not know", adopt(argoRef, nil, other), ApplierUnknown, "Something (Thing x, a mark) manages Deployment w0")
}

func TestSameApplierIgnoresTimes(t *testing.T) {
	a := adopt(argoRef, nil, argoOwner("payments", "payments"))
	b := a
	b.Workloads = append([]string(nil), a.Workloads...)
	b.Since, b.AsOf = "2020-01-01T00:00:00Z", "2021-01-01T00:00:00Z"
	if !SameApplier(&a, &b) {
		t.Fatal("times are ignored")
	}
	b.Message = "other"
	if SameApplier(&a, &b) || SameApplier(&a, nil) || SameApplier(nil, &a) || !SameApplier(nil, nil) {
		t.Fatal("content counts, and nil equals only nil")
	}
	b = a
	b.Workloads = []string{"Deployment other"}
	if SameApplier(&a, &b) {
		t.Fatal("workloads count")
	}
}

// applierPlan plans a promote that passes every other gate, with the Stage's
// applyRef ref and status.applier st.
func applierPlan(t *testing.T, ref ApplyRef, st *ApplierStatus) Plan {
	t.Helper()
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	snap, err := Snapshot(t.Context(), []PactoView{checkoutPacto("^1.0.0")}, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	stage := stageSpec()
	stage.ApplyRef = ref
	return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stage, From: from, To: to, Snapshot: snap, Class: ClassAgent,
		Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}, Applier: st}})
}

func applierOf(state string) *ApplierStatus {
	return &ApplierStatus{Manager: "argocd", Owner: "Application payments/payments", Display: "Argo CD (Application payments/payments, tracking annotation)",
		State: state, Message: state + " message", Completeness: "complete"}
}

func TestApplierGateAdoptedPasses(t *testing.T) {
	for _, state := range []string{ApplierAdopted, ApplierUnmanaged} {
		p := applierPlan(t, argoRef, applierOf(state))
		v := verdict(t, p, GateApplier)
		if v.Result != Pass || p.Predicted != PredictProceeds || len(p.Conflicts) != 0 || v.Phase != "pre" ||
			v.Citation["declared"] != "Application payments" || v.Citation["observed"] != "Argo CD (Application payments/payments, tracking annotation)" ||
			v.Citation["state"] != state {
			t.Fatalf("%s: %+v %+v", state, v, p)
		}
		if len(p.Verdicts) < 3 || p.Verdicts[1].Gate != GateApplier {
			t.Fatalf("%s: the applier gate runs right after pacto.published: %+v", state, p.Verdicts)
		}
	}
}

func TestApplierGateConflictRejects(t *testing.T) {
	p := applierPlan(t, argoRef, applierOf(ApplierConflict))
	if v := verdict(t, p, GateApplier); v.Result != Fail || v.Message != "Conflict message" || p.Predicted != PredictRejected || len(p.Verdicts) != 2 {
		t.Fatalf("conflict: %+v", p)
	}
}

func TestApplierGateUnknownRejects(t *testing.T) {
	p := applierPlan(t, argoRef, applierOf(ApplierUnknown))
	if v := verdict(t, p, GateApplier); v.Result != Unknown || p.Predicted != PredictRejected || p.Floor.Human || p.Question != "" || len(p.Verdicts) != 2 {
		t.Fatalf("unknown: %+v", p)
	}
}

// TestApplierGateRunsBeforeAnUnknownPublication: when Pacto cannot read the
// incoming artifact, the published gate asks a person to accept the unknown;
// an applier that is not Pass must reject first, since nobody can accept it.
func TestApplierGateRunsBeforeAnUnknownPublication(t *testing.T) {
	plan := func(st *ApplierStatus) Plan {
		from, to := rev(dA, c1, bundle("payments", "1.0.0")), Revision{Digest: dB, Err: "registry down"}
		return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(), From: from, To: to, Class: ClassAgent,
			Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}, Applier: st}})
	}
	for state, reason := range map[string]string{ApplierConflict: "ApplierConflict: ", ApplierUnknown: "ApplierUnknown: "} {
		p := plan(applierOf(state))
		if p.Verdicts[0].Gate != GatePublished || p.Verdicts[0].Result != Unknown {
			t.Fatalf("%s: published must read Unknown: %+v", state, p.Verdicts)
		}
		if v := verdict(t, p, GateApplier); v.Result == Pass || p.Predicted != PredictRejected || p.Floor.Human ||
			len(p.Conflicts) != 1 || !strings.HasPrefix(p.Conflicts[0], reason) {
			t.Fatalf("%s: %+v", state, p)
		}
	}
	if p := plan(applierOf(ApplierAdopted)); p.Predicted != PredictHeld || p.Floor.Decision != AcceptUnknown || verdict(t, p, GateApplier).Result != Pass {
		t.Fatalf("an adopted applier still leaves the unknown publication to a person: %+v", p)
	}
}

func TestApplierGateAppendsConflict(t *testing.T) {
	if p := applierPlan(t, argoRef, applierOf(ApplierConflict)); len(p.Conflicts) != 1 || p.Conflicts[0] != "ApplierConflict: Conflict message" {
		t.Fatalf("fail: %q", p.Conflicts)
	}
	if p := applierPlan(t, argoRef, applierOf(ApplierUnknown)); len(p.Conflicts) != 1 || p.Conflicts[0] != "ApplierUnknown: Unknown message" {
		t.Fatalf("unknown: %q", p.Conflicts)
	}
}

func TestProgressNamesApplierUnknown(t *testing.T) {
	pl := applierPlan(t, argoRef, applierOf(ApplierUnknown))
	p := ProgressOf(ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: &PlanAnswer{Plan: pl}})
	if r := reasonOf(p, "PlanRejected"); !strings.Contains(r.Message, "ApplierUnknown: Unknown message") || strings.Contains(r.Message, "a conflict") {
		t.Errorf("progress: %+v", r)
	}
	if next := strings.Join(PlanNext(pl, ClassAgent, stageSpec()), "\n"); !strings.Contains(next, "ApplierUnknown: Unknown message") {
		t.Errorf("next: %s", next)
	}
}

// TestApplierGateCitesOnlyStageAndStatus: the verdict cites the Stage's
// applyRef and what the promoter observed, nothing from the contract, and the
// observed state alone decides.
func TestApplierGateCitesOnlyStageAndStatus(t *testing.T) {
	want := []string{"declared", "observed", "state"}
	for _, state := range []string{ApplierAdopted, ApplierUnmanaged, ApplierConflict, ApplierUnknown} {
		v := verdict(t, applierPlan(t, argoRef, applierOf(state)), GateApplier)
		if got := slices.Sorted(maps.Keys(v.Citation)); !slices.Equal(got, want) {
			t.Fatalf("%s: citation keys %v", state, got)
		}
	}
	if v := verdict(t, applierPlan(t, ApplyRef{Name: "payments"}, applierOf(ApplierAdopted)), GateApplier); v.Result != Pass {
		t.Fatalf("an adopted OCIRepository Stage passes: %+v", v)
	}
}

func TestApplierGateNilStatusAddsNoVerdict(t *testing.T) {
	p := applierPlan(t, argoRef, nil)
	for _, v := range p.Verdicts {
		if v.Gate == GateApplier {
			t.Fatalf("no status, no verdict: %+v", p.Verdicts)
		}
	}
	if p.Predicted != PredictProceeds {
		t.Fatalf("%+v", p)
	}
	if v, ok := applierVerdict(PlanInput{Stage: StageSpec{ApplyRef: argoRef}, Status: StageStatus{Applier: applierOf(ApplierAdopted)}}); !ok || v.Result != Pass {
		t.Fatalf("an unread version reads detection only: %+v", v)
	}
}

func TestApplierGateOnlyOnPromote(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	st := StageStatus{Pointer: dA, Verified: []string{dB}, Applier: applierOf(ApplierConflict)}
	p := autonomousPlan(PlanInput{Spec: OperationSpec{Type: TypeRollback, Facet: FacetPointer, Params: Params{From: dA, To: dB}},
		Stage: stageSpec(), Status: st, From: from, To: to, Class: ClassController})
	if p.Predicted == PredictRejected || len(p.Verdicts) != 0 {
		t.Fatalf("a rollback has no applier gate: %+v", p)
	}
}

func TestReadyFixKnowsApplierReasons(t *testing.T) {
	for _, r := range []string{ReadyReasonApplierConflict, ReadyReasonApplierUnknown, ReadyReasonApplierNotChartRef} {
		if ReadyFix(r) == "" {
			t.Errorf("%s has no fix", r)
		}
	}
	if !strings.Contains(ReadyFix(ReadyReasonApplierNotOCI), "Argo CD") {
		t.Error("ApplierNotOCI stays Argo-only")
	}
}

func TestRowOfCarriesApplier(t *testing.T) {
	st := applierOf(ApplierAdopted)
	if row := RowOf(rowStage(LevelAdvise, StageStatus{Applier: st}), RowInput{}); row.Applier != st {
		t.Fatalf("row: %+v", row.Applier)
	}
	if row := RowOf(rowStage(LevelAdvise, StageStatus{}), RowInput{}); row.Applier != nil {
		t.Fatalf("no status: %+v", row.Applier)
	}
}

// TestApplierStale: past staleAt is stale; without one, older than twice
// the default resync is; an unreadable asOf is not.
func TestApplierStale(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	for asOf, want := range map[string]bool{"2026-11-15T09:59:00Z": false, "2026-11-15T09:57:59Z": true, "": false, "yesterday": false} {
		if got := (&ApplierStatus{AsOf: asOf}).Stale(now); got != want {
			t.Errorf("asOf %q: stale %v, want %v", asOf, got, want)
		}
	}
	// A promoter run with --resync=5m checks every five minutes: an asOf four
	// minutes old is fresh until its staleAt.
	if (&ApplierStatus{AsOf: "2026-11-15T09:56:00Z", StaleAt: "2026-11-15T10:06:00Z"}).Stale(now) {
		t.Error("a staleAt in the future is not stale")
	}
	if !(&ApplierStatus{AsOf: "2026-11-15T09:59:00Z", StaleAt: "2026-11-15T09:59:59Z"}).Stale(now) {
		t.Error("a staleAt in the past is stale")
	}
}
