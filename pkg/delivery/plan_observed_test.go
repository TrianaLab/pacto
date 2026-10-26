package delivery

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// paymentsPacto is the provider's own Pacto, with the callers and coverage the
// operator wrote.
func paymentsPacto(cov *CallerCoverageView, callers ...ObservedCallerView) PactoView {
	var p PactoView
	p.Metadata.Namespace, p.Metadata.Name = "prod", "payments"
	p.Status.ContractStatus = "Compliant"
	p.Status.LastReconciledAt = now.Format(time.RFC3339)
	p.Status.Contract = &ContractInfoView{ServiceName: "payments", Version: "1.0.0",
		Owner: &contract.Owner{Team: "team-payments"}, ResolvedRef: "oci://reg.example/pactos/payments@" + c1}
	p.Status.ObservedCallers = callers
	p.Status.ObservedCallersCoverage = cov
	return p
}

func complete() *CallerCoverageView {
	return &CallerCoverageView{State: "complete", RefreshSeconds: 300}
}

var legacy = ObservedCallerView{Namespace: "prod", Kind: "Deployment", Name: "legacy", Source: "ebpf"}

// breakingSides is a change the diff classifies as BREAKING; minorSides one it
// classifies as NON_BREAKING.
func breakingSides() (Revision, Revision) {
	old, nu := bundle("payments", "1.0.0"), bundle("payments", "1.1.0")
	old.Contract.Workload, nu.Contract.Workload = "service", "job"
	return rev(dA, c1, old), rev(dB, c2, nu)
}

func minorSides() (Revision, Revision) {
	return rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.0.1"))
}

func planIn(t *testing.T, from, to Revision, pactos ...PactoView) PlanInput {
	t.Helper()
	snap, err := Snapshot(context.Background(), pactos, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	return PlanInput{Spec: promote(dA, dB), Stage: stageSpec(), Status: StageStatus{Rung: RungAutonomous, Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From: from, To: to, Snapshot: snap, Class: ClassAgent}
}

func TestPlanUncontractedCallerHoldsABreakingChange(t *testing.T) {
	from, to := breakingSides()
	in := planIn(t, from, to, paymentsPacto(complete(), legacy))
	for _, declaredOnly := range []bool{false, true} {
		in.Stage.Gates.Impact.DeclaredConsumersOnly = declaredOnly
		p := PlanFor(context.Background(), in)
		v := verdict(t, p, GateImpact)
		if v.Result != Unknown || p.Floor.Decision != AcceptUnknown ||
			!strings.Contains(v.Message, "k8s:prod/Deployment/legacy calls payments at runtime (ebpf) but has no contract") ||
			!strings.Contains(v.Message, "run `pacto init --cluster --namespace prod --out <dir>` for it") || !strings.Contains(p.Question, "k8s:prod/Deployment/legacy") {
			t.Fatalf("declaredConsumersOnly=%v: an uncontracted caller must hold a breaking change: %+v", declaredOnly, p)
		}
	}
	from, to = minorSides()
	p := PlanFor(context.Background(), planIn(t, from, to, paymentsPacto(complete(), legacy)))
	if p.Predicted != PredictProceeds {
		t.Fatalf("a non-breaking change keeps shipping past an uncontracted caller: %+v", p)
	}
	// It still cites the caller, which keeps autonomous from being proposed.
	if v := verdict(t, p, GateImpact); v.Citation["uncontracted"] != "1" {
		t.Fatalf("the uncontracted caller is cited: %+v", v)
	}
	if got := ProposeRung(StageSpec{}, StageStatus{Rung: RungAutoNonBreaking}, &p); !slices.Contains(kinds(got), BlockerMissingContract) {
		t.Fatalf("missing-contract blocks autonomous: %+v", got)
	}
}

func TestPlanCallerCoverageIsPerProvider(t *testing.T) {
	reconciled := func(p PactoView, at string) PactoView { p.Status.LastReconciledAt = at; return p }
	cases := map[string]struct {
		pacto PactoView
		line  string
	}{
		"partial": {paymentsPacto(&CallerCoverageView{State: "partial", Reason: "stale-node", RefreshSeconds: 300}),
			"runtime callers of payments cannot be ruled out: a node agent has not reported recently (coverage: partial, stale-node)"},
		"a status that stopped updating": {reconciled(paymentsPacto(complete()), now.Add(-11*time.Minute).Format(time.RFC3339)),
			"runtime callers of payments cannot be ruled out: the operator has not updated this status recently (coverage: unavailable, stale-status)"},
		"no refresh interval": {paymentsPacto(&CallerCoverageView{State: "complete"}), "(coverage: unavailable, stale-status)"},
		"no reconcile time":   {reconciled(paymentsPacto(complete()), ""), "(coverage: unavailable, stale-status)"},
		"host network": {paymentsPacto(&CallerCoverageView{State: "partial", Reason: "shared-netns", RefreshSeconds: 300}),
			"a pod on the host network can call it and the node agent cannot tell which one (coverage: partial, shared-netns)"},
		"an evaluation that did not run": {paymentsPacto(&CallerCoverageView{State: "unavailable", Reason: "not-evaluated", RefreshSeconds: 300}),
			"cannot be ruled out: the operator could not evaluate this Pacto's runtime evidence (coverage: unavailable, not-evaluated)"},
		"a reason this build does not know": {paymentsPacto(&CallerCoverageView{State: "partial", Reason: "new-thing", RefreshSeconds: 300}),
			"cannot be ruled out: the operator reported new-thing (coverage: partial, new-thing)"},
	}
	for name, c := range cases {
		from, to := breakingSides()
		in := planIn(t, from, to, c.pacto)
		in.Stage.Gates.Impact.DeclaredConsumersOnly = true
		p := PlanFor(context.Background(), in)
		v := verdict(t, p, GateImpact)
		if v.Result != Unknown || !strings.Contains(v.Message, c.line) || v.Citation["completeness"] != "complete" {
			t.Fatalf("%s: want Unknown naming %q on a complete graph, got %+v", name, c.line, v)
		}
		from, to = minorSides()
		in = planIn(t, from, to, c.pacto)
		p = PlanFor(context.Background(), in)
		if p.Predicted != PredictProceeds {
			t.Fatalf("%s: coverage holds only a change that is not plainly non-breaking: %+v", name, p)
		}
		if v := verdict(t, p, GateImpact); v.Citation["callersUnseen"] != "1" {
			t.Fatalf("%s: unseen callers are cited whatever the classification: %+v", name, v)
		}
	}
	from, to := breakingSides()
	in := planIn(t, from, to, reconciled(paymentsPacto(complete()), now.Add(-9*time.Minute).Format(time.RFC3339)))
	in.Stage.Gates.Impact.DeclaredConsumersOnly = true
	if p := PlanFor(context.Background(), in); p.Predicted != PredictProceeds {
		t.Fatalf("a fresh complete coverage with no caller holds nothing: %+v", p)
	}
}

// TestPlanContractedCallerFromAnUndeclaredDependency reads a node agent's
// DEPENDENCY_UNDECLARED finding on another Pacto as an observed consumer; a
// finding no node agent raised is not runtime evidence.
func TestPlanContractedCallerFromAnUndeclaredDependency(t *testing.T) {
	ledger := func(collector string) PactoView {
		p := checkoutPacto("")
		p.Metadata.Name = "ledger"
		p.Status.Contract.ServiceName = "ledger"
		p.Status.Dependencies = nil
		p.Status.Findings = []FindingView{{Code: "DEPENDENCY_UNDECLARED", Subject: "dependency/payments",
			EvidenceRefs: []EvidenceRefView{{Collector: collector}}}}
		return p
	}
	from, to := breakingSides()
	in := planIn(t, from, to, ledger("ebpf"))
	in.Stage.Gates.Impact.DeclaredConsumersOnly = true
	if v := verdict(t, PlanFor(context.Background(), in), GateImpact); v.Result != Unknown || !strings.Contains(v.Message, "ledger has no usable compatibility range") {
		t.Fatalf("an observed contracted caller must hold a breaking change: %+v", v)
	}
	in = planIn(t, from, to, ledger("k8s-observer"))
	in.Stage.Gates.Impact.DeclaredConsumersOnly = true
	if p := PlanFor(context.Background(), in); p.Predicted != PredictProceeds {
		t.Fatalf("a finding no node agent raised is no call: %+v", p)
	}
}

func TestPlanStagingCallerStaysOutOfAProdPlan(t *testing.T) {
	staging := paymentsPacto(complete(), legacy)
	staging.Metadata.Namespace = "staging"
	staging.Metadata.Labels = map[string]string{EnvironmentLabel: "staging"}
	stages := []StageObject{{Spec: StageSpec{Environment: "prod"}}, {Spec: StageSpec{Environment: "staging"}}}
	prod, _ := EnvironmentPactos([]PactoView{staging}, stages, nil, nil, "prod")
	from, to := breakingSides()
	in := planIn(t, from, to, prod...)
	in.Stage.Gates.Impact.DeclaredConsumersOnly = true
	if p := PlanFor(context.Background(), in); p.Predicted != PredictProceeds {
		t.Fatalf("a staging caller held a prod plan: %+v", p)
	}
}

// TestObservedEvidenceOnlyAddsHolds is rule 0.1 across the plan fixtures: the
// consumers held with observed evidence are always a superset of those held
// without it.
func TestObservedEvidenceOnlyAddsHolds(t *testing.T) {
	undeclared := checkoutPacto("^1.0.0")
	undeclared.Status.Findings = []FindingView{{Code: "DEPENDENCY_UNDECLARED", Subject: "dependency/payments", EvidenceRefs: []EvidenceRefView{{Collector: "ebpf"}}}}
	fixtures := [][]PactoView{
		{checkoutPacto("^1.0.0")}, {checkoutPacto("")}, {checkoutPacto(">=2")}, {undeclared},
		{paymentsPacto(complete(), legacy)}, {paymentsPacto(complete(), legacy), checkoutPacto("")},
	}
	sides := [][2]Revision{}
	for _, f := range []func() (Revision, Revision){breakingSides, minorSides} {
		from, to := f()
		sides = append(sides, [2]Revision{from, to})
	}
	for i, pactos := range fixtures {
		for _, s := range sides {
			in := planIn(t, s[0], s[1], pactos...)
			held := func(observed bool) []string {
				res := impact.Analyze(context.Background(), s[0].Bundle.Contract, s[1].Bundle.Contract, nil, nil, in.Snapshot,
					impact.Options{Domain: contract.OCIDomain(s[1].ContractRef), IncludeObserved: observed})
				var out []string
				for _, c := range res.Consumers {
					if c.CompatibilityVerdict == impact.CompatibilityIncompatible || unknownConsumer(res, c) {
						out = append(out, c.Service)
					}
				}
				return out
			}
			with, without := held(true), held(false)
			for _, c := range without {
				if !slices.Contains(with, c) {
					t.Fatalf("fixture %d: observed evidence dropped the hold on %s (with %v, without %v)", i, c, with, without)
				}
			}
		}
	}
}

func TestCallerHelpers(t *testing.T) {
	if CallerName("ns", "Deployment", "x") != "k8s:ns/Deployment/x" || callerNamespace("k8s:ns/Deployment/x") != "ns" {
		t.Fatal("caller names")
	}
}
