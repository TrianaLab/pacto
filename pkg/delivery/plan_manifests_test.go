package delivery

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// inspectedInput promotes payments 1.0.0 to a BREAKING 2.0.0 that checkout
// (^1.0.0) does not accept, with the inspector answering insp and counting
// its reads.
func inspectedInput(t *testing.T, insp ManifestInspection, reads *int) PlanInput {
	t.Helper()
	old, next := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
	old.Contract.Workload, next.Contract.Workload = "service", "job"
	from, to := rev(dA, c1, old), rev(dB, c2, next)
	snap, err := Snapshot(context.Background(), []PactoView{checkoutPacto("^1.0.0")}, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	return PlanInput{Spec: promote(dA, dB), Stage: stageSpec(), From: from, To: to, Snapshot: snap, Class: ClassAgent,
		Status:           StageStatus{Level: LevelAutonomous, Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		InspectManifests: true,
		Manifests: func(context.Context) ManifestInspection {
			*reads++
			return insp
		}}
}

var (
	refusedInspection = ManifestInspection{Artifact: "manifests", Namespace: "prod", Total: 1,
		Objects: []ManifestObject{{Kind: "ClusterRoleBinding", Name: "orders-admin"}}, Refused: []string{"ClusterRoleBinding orders-admin is cluster-scoped"}}
	unreadInspection = ManifestInspection{Artifact: "chart", Namespace: "prod", Unread: []string{"the chart carries a subchart: charts/x.tgz"}}
	passInspection   = ManifestInspection{Artifact: "manifests", Namespace: "prod", Total: 1, Objects: []ManifestObject{{Kind: "Deployment", Name: "payments"}}}
)

func TestManifestsGateRejectsBeforeDiff(t *testing.T) {
	var reads int
	p := PlanFor(context.Background(), inspectedInput(t, refusedInspection, &reads))
	v := verdict(t, p, GateManifests)
	if p.Predicted != PredictRejected || v.Result != Fail || reads != 1 || p.Question != "" ||
		slices.ContainsFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateDiff || v.Gate == GateImpact }) {
		t.Fatalf("a refused artifact rejects before the diff: %+v", p)
	}
	if v.Citation["refused"] != "ClusterRoleBinding orders-admin is cluster-scoped" || v.Citation["objects"] != "ClusterRoleBinding orders-admin" {
		t.Fatalf("citation %+v", v.Citation)
	}
}

func TestManifestsGateUnknownAsksAPerson(t *testing.T) {
	var reads int
	in := inspectedInput(t, unreadInspection, &reads)
	in.From.Bundle.Contract.Workload = "job" // no breaking change
	p := PlanFor(context.Background(), in)
	if p.Predicted != PredictHeld || p.Floor.Decision != AcceptUnknown ||
		!strings.HasPrefix(p.Question, "Pacto cannot tell what sha256:bbbbbbbbbbbb applies: the chart carries a subchart: charts/x.tgz. Accept the unknown?") {
		t.Fatalf("an unread artifact asks a person: %+v", p)
	}
}

func TestManifestsUnknownSurvivesBreakingImpact(t *testing.T) {
	var reads int
	base := inspectedInput(t, unreadInspection, &reads)
	base.InspectManifests = false
	if p := PlanFor(context.Background(), base); p.Floor.Decision != Approve {
		t.Fatalf("control: the breaking change asks the consumer's owners to approve: %+v", p)
	}
	p := PlanFor(context.Background(), inspectedInput(t, unreadInspection, &reads))
	if p.Floor.Decision != AcceptUnknown || !strings.Contains(p.Question, "what sha256:bbbbbbbbbbbb applies") ||
		slices.ContainsFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateImpact }) {
		t.Fatalf("the unknown inspection keeps its question over the breaking impact: %+v", p)
	}
}

func TestManifestsAndPublishUnknownBothAsked(t *testing.T) {
	var reads int
	in := inspectedInput(t, unreadInspection, &reads)
	in.To = Revision{Digest: dB, Err: "registry down"}
	p := PlanFor(context.Background(), in)
	if !strings.HasPrefix(p.Question, "Pacto cannot tell whether pacto publish published sha256:bbbbbbbbbbbb: ") ||
		!strings.Contains(p.Question, "registry down; and what sha256:bbbbbbbbbbbb applies: the chart carries a subchart: charts/x.tgz. Accept the unknown?") {
		t.Fatalf("one question names both: %q", p.Question)
	}
	// A refused artifact rejects even while the publication is unknown.
	in = inspectedInput(t, refusedInspection, &reads)
	in.To = Revision{Digest: dB, Err: "registry down"}
	if p := PlanFor(context.Background(), in); p.Predicted != PredictRejected {
		t.Fatalf("refused while unpublished-unknown: %+v", p)
	}
	// A clean artifact leaves the publication's own question.
	in = inspectedInput(t, passInspection, &reads)
	in.To = Revision{Digest: dB, Err: "registry down"}
	if p := PlanFor(context.Background(), in); !strings.HasPrefix(p.Question, "Pacto cannot tell whether pacto publish published") || strings.Contains(p.Question, "applies") {
		t.Fatalf("pass while unpublished-unknown: %q", p.Question)
	}
}

func TestManifestsNeverReadForUnpublished(t *testing.T) {
	ctx := context.Background()
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod", Name: "payments-prod"}, Spec: stageSpec(),
		Status: StageStatus{Level: LevelAutonomous, Pointer: dA, Verified: []string{dA}}}
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	plan := func(stage StageObject) (Plan, int) {
		src := &fakeSources{from: from, to: to, inspection: refusedInspection}
		in, err := AssemblePlanInput(ctx, src, promote(dA, dB), stage, ClassAgent, now, true)
		if err != nil {
			t.Fatal(err)
		}
		if src.inspected != 0 {
			t.Fatal("assembling the input reads no artifact")
		}
		return PlanFor(ctx, in), src.inspected
	}
	if p, n := plan(stage); n != 0 || p.Predicted != PredictRejected {
		t.Fatalf("unpublished: read %d, %+v", n, p)
	}
	stage.Status.Published = []string{dB}
	stage.Status.Applier = &ApplierStatus{State: ApplierUnknown, Message: NoApplierMessage}
	if p, n := plan(stage); n != 0 || p.Predicted != PredictRejected {
		t.Fatalf("applier unknown: read %d, %+v", n, p)
	}
	stage.Status.Applier = &ApplierStatus{State: ApplierAdopted}
	if p, n := plan(stage); n != 1 || verdict(t, p, GateManifests).Result != Fail {
		t.Fatalf("published and adopted: read %d, %+v", n, p)
	}
}

func TestPlanWithoutInspectorUnchanged(t *testing.T) {
	var reads int
	for _, insp := range []ManifestInspection{refusedInspection, unreadInspection} {
		in := inspectedInput(t, insp, &reads)
		in.InspectManifests = false
		base := in
		base.Manifests = nil
		got, want := PlanFor(context.Background(), in), PlanFor(context.Background(), base)
		if !reflect.DeepEqual(got, want) || reads != 0 {
			t.Fatalf("the flag off plans as before: %+v vs %+v (reads %d)", got, want, reads)
		}
	}
	// On, with nothing to inspect (no closure), the plan is the same too.
	in := inspectedInput(t, passInspection, &reads)
	in.Manifests = nil
	base := in
	base.InspectManifests = false
	if !reflect.DeepEqual(PlanFor(context.Background(), in), PlanFor(context.Background(), base)) {
		t.Fatal("no closure plans as before")
	}
	// A clean artifact adds its Pass verdict and changes nothing else.
	in = inspectedInput(t, passInspection, &reads)
	base = in
	base.InspectManifests = false
	got, want := PlanFor(context.Background(), in), PlanFor(context.Background(), base)
	got.Verdicts = slices.DeleteFunc(got.Verdicts, func(v Verdict) bool { return v.Gate == GateManifests })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pass: %+v vs %+v", got, want)
	}
}

func TestRollbackNeverInspects(t *testing.T) {
	src := &fakeSources{from: rev(dB, c2, bundle("payments", "1.1.0")), to: rev(dA, c1, bundle("payments", "1.0.0")), inspection: refusedInspection}
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"}, Spec: stageSpec(), Status: StageStatus{Pointer: dB}}
	rb := OperationSpec{Type: TypeRollback, Stage: "payments-prod", Facet: FacetPointer, Params: Params{From: dB, To: dA}}
	in, err := AssemblePlanInput(context.Background(), src, rb, stage, ClassAgent, now, true)
	if err != nil || in.Manifests != nil || !in.InspectManifests {
		t.Fatalf("a rollback gets no inspection: %+v %v", in, err)
	}
	PlanFor(context.Background(), in)
	if src.inspected != 0 {
		t.Fatal("a rollback reads no artifact")
	}
	if in, _ := AssemblePlanInput(context.Background(), src, promote(dA, ""), stage, ClassAgent, now, true); in.Manifests != nil {
		t.Fatal("a promote with no target reads no artifact")
	}
}

func TestAdoptApplierNoAPIIsUnknown(t *testing.T) {
	st := AdoptApplier(ApplierInput{Namespace: "prod", NoAPI: true, Unread: []string{"x"}, Now: applierNow})
	if st.State != ApplierUnknown || st.Manager != "none" || st.Message != NoApplierMessage || st.Completeness != "partial" {
		t.Fatalf("no applier API: %+v", st)
	}
	var reads int
	in := inspectedInput(t, passInspection, &reads)
	in.Status.Applier = &st
	if p := PlanFor(context.Background(), in); p.Predicted != PredictRejected || reads != 0 ||
		!slices.Contains(p.Conflicts, "ApplierUnknown: "+NoApplierMessage) {
		t.Fatalf("no applier rejects: %+v", p)
	}
}

func TestTier0ListsManifestsGate(t *testing.T) {
	typ, _ := TypeByID(TypePromote)
	if i := slices.Index(typ.PreGates, GateManifests); i != slices.Index(typ.PreGates, GateApplier)+1 ||
		!strings.Contains(typ.ApprovalFloor, "(pacto.manifests) is rejected and nobody can accept it") {
		t.Fatalf("tier0 promote: %+v", typ)
	}
}
