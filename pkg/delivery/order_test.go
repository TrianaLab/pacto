package delivery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func pactoIn(ns, name string) PactoView {
	var p PactoView
	p.Metadata.Namespace, p.Metadata.Name = ns, name
	return p
}

func stageIn(ns, name, service, env string) StageObject {
	return StageObject{Metadata: ObjectMeta{Namespace: ns, Name: name},
		Spec: StageSpec{Service: ServiceRef{Name: service}, Environment: env, ApplyRef: LocalRef{Name: service}}}
}

func names(ps []PactoView) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Metadata.Namespace+"/"+p.Metadata.Name)
	}
	return out
}

func TestEnvironmentPactos(t *testing.T) {
	pactos := []PactoView{pactoIn("prod", "a"), pactoIn("orders-prod", "b"), pactoIn("test", "c"), pactoIn("loose", "d"), pactoIn("mixed", "e")}
	stages := []StageObject{
		stageIn("prod", "payments", "payments", "prod"),
		stageIn("orders-prod", "orders", "orders", "prod"),
		stageIn("test", "payments", "payments", "test"),
		stageIn("mixed", "x", "x", "test"), stageIn("mixed", "y", "y", "prod"),
	}
	if got := names(EnvironmentPactos(pactos, stages, nil, "prod", "prod")); len(got) != 4 || got[0] != "prod/a" || got[1] != "orders-prod/b" ||
		got[2] != "loose/d" || got[3] != "mixed/e" {
		t.Fatalf("prod keeps its own, the unplaceable and the mixed namespace: %v", got)
	}
	if got := names(EnvironmentPactos(pactos, stages, nil, "test", "test")); len(got) != 3 || got[0] != "test/c" {
		t.Fatalf("test: %v", got)
	}
	// The Stage's own namespace always counts, even when it hosts no Stage of
	// that environment in the list (the list may be stale).
	if got := names(EnvironmentPactos(pactos, stages, nil, "test", "staging")); len(got) != 2 || got[0] != "test/c" || got[1] != "loose/d" {
		t.Fatalf("own namespace: %v", got)
	}
	if got := EnvironmentPactos(pactos, nil, errors.New("forbidden"), "prod", "prod"); len(got) != len(pactos) {
		t.Fatalf("unreadable Stages keep every Pacto: %v", names(got))
	}
}

func consumerPacto(ns, name, version, rng string) PactoView {
	p := checkoutPacto(rng)
	p.Metadata.Namespace, p.Metadata.Name = ns, name
	p.Status.Contract.ServiceName, p.Status.Contract.Version = name, version
	p.Status.Contract.Owner = &contract.Owner{Team: "team-" + name}
	p.Status.Contract.ResolvedRef = "oci://reg.example/pactos/" + name + "@" + c3
	return p
}

const (
	o1 = "sha256:0101010101010101010101010101010101010101010101010101010101010101"
	o2 = "sha256:0202020202020202020202020202020202020202020202020202020202020202"
	o3 = "sha256:0303030303030303030303030303030303030303030303030303030303030303"
	o4 = "sha256:0404040404040404040404040404040404040404040404040404040404040404"
	o5 = "sha256:0505050505050505050505050505050505050505050505050505050505050505"
)

// ordersWorld is the vision's example: payments 3.0.0 is breaking, orders
// 1.8.0 runs in prod and accepts only ^2.0.0, and the orders Stage in test
// verified versions of orders. contracts maps each orders digest to what it
// carries (nil: unreadable).
func ordersWorld(contracts map[string]*contract.Bundle) (PlanInput, []PactoView) {
	payments := StageObject{Metadata: ObjectMeta{Namespace: "prod", Name: "payments"}, Spec: stageSpec()}
	ordersProd := stageIn("orders-prod", "orders", "orders", "prod")
	ordersProd.Spec.Candidates.UpstreamStage = &StageRef{Name: "orders", Namespace: "test"}
	ordersProd.Status.Pointer, ordersProd.Status.FailedDigests = o5, []string{o4}
	ordersTest := stageIn("test", "orders", "orders", "test")
	ordersTest.Status.Offered = []string{o1, o2, o3, o4, o5}
	ordersTest.Status.Verified = []string{o1, o2, o4, o5} // o3 was offered once but is not verified now
	in := PlanInput{Stages: []StageObject{payments, ordersProd, ordersTest},
		Candidates: func(_ context.Context, s StageObject, d string) Revision {
			if s.Metadata.Name != "orders" || s.Metadata.Namespace != "orders-prod" {
				panic("candidates are read through the consumer's own Stage")
			}
			if b := contracts[d]; b != nil {
				return Revision{Digest: d, Bundle: b}
			}
			return Revision{Digest: d, Err: "registry down"}
		}}
	pactos := []PactoView{consumerPacto("orders-prod", "orders", "1.8.0", "^2.0.0"), consumerPacto("test", "orders", "1.9.0", ">=2.0.0 <4.0.0")}
	return in, pactos
}

func paymentsDep(rng string) contract.Dependency {
	return contract.Dependency{Name: "payments", Ref: "oci://reg.example/pactos/payments", Compatibility: rng}
}

func orderPlan(t *testing.T, in PlanInput, pactos []PactoView, from, to Revision) Plan {
	t.Helper()
	pactos = EnvironmentPactos(pactos, in.Stages, in.StagesErr, "prod", "prod")
	snap, err := Snapshot(context.Background(), pactos, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	in.Spec, in.Stage, in.From, in.To, in.Snapshot, in.Class = promote(from.Digest, to.Digest), stageSpec(), from, to, snap, ClassAgent
	in.Status = StageStatus{Pointer: from.Digest, Verified: []string{from.Digest}, Published: []string{to.Digest}}
	return PlanFor(context.Background(), in)
}

func onlyMove(t *testing.T, p Plan) MoveFirst {
	t.Helper()
	if p.Order == nil || len(p.Order.MoveFirst) != 1 {
		t.Fatalf("want one consumer to move first, got %+v", p.Order)
	}
	return p.Order.MoveFirst[0]
}

var (
	payments2 = rev(dA, c1, bundle("payments", "2.4.0"))
	payments3 = rev(dB, c2, bundle("payments", "3.0.0"))
)

func TestOrderShipOrdersFirst(t *testing.T) {
	in, pactos := ordersWorld(map[string]*contract.Bundle{
		o2: bundle("orders", "1.9.0", paymentsDep(">=2.0.0 <4.0.0")),
		o1: bundle("orders", "1.8.5", paymentsDep("^2.0.0")),
	})
	p := orderPlan(t, in, pactos, payments2, payments3)
	if p.Predicted != PredictHeld || p.Floor.Owners[0] != "team-orders" {
		t.Fatalf("a break still holds for orders' team: %+v", p)
	}
	m := onlyMove(t, p)
	// Prod runs 1.8.0; the 1.9.0 Pacto in test does not count here.
	if m.Service != "orders" || m.Version != "1.8.0" || m.Accepts != "^2.0.0" || m.Unknown != "" {
		t.Fatalf("move first: %+v", m)
	}
	if *m.Stage != (StageRef{Name: "orders", Namespace: "orders-prod"}) {
		t.Fatalf("stage: %+v", m.Stage)
	}
	// o5 runs, o4 failed here, o3 is not verified: o2 is the newest that can move.
	want := &Candidate{Digest: o2, Version: "1.9.0", Accepts: ">=2.0.0 <4.0.0", VerifiedOn: StageRef{Name: "orders", Namespace: "test"}}
	if m.Candidate == nil || *m.Candidate != *want {
		t.Fatalf("candidate: %+v", m.Candidate)
	}
	msg := "orders 1.8.0 runs in prod and accepts payments ^2.0.0, not 3.0.0. orders 1.9.0 (sha256:020202020202) accepts it (>=2.0.0 <4.0.0) " +
		"and verified on test/orders. Ship orders first: promote " + o2 + " on Stage orders-prod/orders."
	if m.Message != msg {
		t.Fatalf("message:\n%s\nwant\n%s", m.Message, msg)
	}
	if len(p.Order.Unknown) != 0 || !strings.Contains(p.Question, "Or ship orders first") {
		t.Fatalf("order unknown %v, question %q", p.Order.Unknown, p.Question)
	}
}

func TestOrderNoCandidateYet(t *testing.T) {
	// 1.9.0 accepts only ^3.0.0: moving it first would break it against the
	// 2.4.0 running here, so it is not a candidate.
	in, pactos := ordersWorld(map[string]*contract.Bundle{
		o2: bundle("orders", "1.9.0", paymentsDep("^3.0.0")),
		o1: bundle("orders", "1.8.5", paymentsDep("^2.0.0")),
	})
	p := orderPlan(t, in, pactos, payments2, payments3)
	m := onlyMove(t, p)
	if m.Candidate != nil || m.Unknown != "" || strings.Contains(p.Question, "Or ship") ||
		!strings.HasSuffix(m.Message, "No version of orders that test/orders verified accepts both 3.0.0 and the 2.4.0 running here yet. Ship one that does first.") {
		t.Fatalf("no candidate: %+v question %q", m, p.Question)
	}
}

func TestOrderCandidateNoLongerDepends(t *testing.T) {
	in, pactos := ordersWorld(map[string]*contract.Bundle{
		o2: bundle("orders", "2.0.0", contract.Dependency{Name: "stock", Compatibility: "^1.0.0"}),
	})
	m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
	if m.Candidate == nil || m.Candidate.Accepts != "" || !strings.Contains(m.Message, "no longer depends on payments") {
		t.Fatalf("a version without the dependency moves first: %+v", m)
	}
}

func TestOrderSaysWhatItCannotRead(t *testing.T) {
	// o2 unreadable, o1 carries a range Pacto cannot parse: unknown, not "none".
	in, pactos := ordersWorld(map[string]*contract.Bundle{o1: bundle("orders", "1.8.5", paymentsDep("whatever"))})
	m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
	if m.Candidate != nil || !strings.Contains(m.Unknown, "sha256:020202020202: registry down") ||
		!strings.Contains(m.Unknown, `its range "whatever" on payments cannot be read`) ||
		!strings.Contains(m.Message, "Pacto cannot tell what would move it first: Pacto could not read every version test/orders verified") {
		t.Fatalf("unreadable: %+v", m)
	}
	in.Candidates = nil
	if m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3)); !strings.Contains(m.Unknown, "no registry access was given") {
		t.Fatalf("no reader: %+v", m)
	}
}

func TestOrderPlacesTheConsumerOrSaysWhy(t *testing.T) {
	in, pactos := ordersWorld(nil)
	cases := []struct {
		name string
		edit func(*PlanInput)
		want string
	}{
		{"stages unreadable", func(in *PlanInput) { in.StagesErr = errors.New("forbidden") }, "the Stages could not be listed (forbidden)"},
		{"no Stage", func(in *PlanInput) { in.Stages = in.Stages[:1] }, "no Stage delivers orders in prod"},
		{"two Stages", func(in *PlanInput) {
			in.Stages = append(in.Stages, stageIn("prod", "orders", "orders", "prod"))
		}, "2 Stages deliver orders in prod (orders-prod/orders and prod/orders)"},
		{"upstream gone", func(in *PlanInput) { in.Stages = in.Stages[:2] }, "its upstream Stage test/orders does not exist or cannot be read"},
	}
	for _, c := range cases {
		in := in
		in.Stages = slices.Clone(in.Stages)
		c.edit(&in)
		m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
		if !strings.HasPrefix(m.Unknown, c.want) || m.Candidate != nil {
			t.Errorf("%s: %+v", c.name, m)
		}
	}
	// A Stage fed straight from its candidate source has nothing verified ahead of it.
	in.Stages[1].Spec.Candidates = Candidates{OCIRepositoryRef: &LocalRef{Name: "orders-candidates"}}
	m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
	if m.Unknown != "" || m.Candidate != nil || !strings.HasSuffix(m.Message,
		"orders takes its versions straight from its candidate source here, so no version of it is verified ahead of prod. Ship one that accepts 3.0.0 first.") {
		t.Fatalf("straight from source: %+v", m)
	}
}

func TestOrderOnAFirstVersionAndAPartialGraph(t *testing.T) {
	in, pactos := ordersWorld(map[string]*contract.Bundle{o2: bundle("orders", "1.9.0", paymentsDep("^3.0.0"))})
	// An unreconciled Pacto in prod: Pacto cannot see every consumer, so the
	// order says the list may be short.
	var ghost PactoView
	ghost.Metadata.Namespace, ghost.Metadata.Name = "prod", "ghost"
	pactos = append(pactos, ghost)
	p := orderPlan(t, in, pactos, Revision{}, payments3)
	m := onlyMove(t, p)
	// Nothing runs here yet, so only 3.0.0 has to be accepted.
	if m.Candidate == nil || m.Candidate.Digest != o2 {
		t.Fatalf("first version: %+v", m)
	}
	if len(p.Order.Unknown) != 1 || !strings.Contains(p.Order.Unknown[0], "PACTO_NOT_RECONCILED") {
		t.Fatalf("partial graph: %v", p.Order.Unknown)
	}
	in.Stages[2].Status.Offered = nil
	if m := onlyMove(t, orderPlan(t, in, pactos, Revision{}, payments3)); !strings.HasSuffix(m.Message, "verified accepts 3.0.0 yet. Ship one that does first.") {
		t.Fatalf("first version, no candidate: %+v", m)
	}
}

func TestAcceptsBoth(t *testing.T) {
	c := bundle("orders", "1.9.0", paymentsDep("^2.0.0")).Contract
	if _, ok, err := acceptsBoth(c, "payments", "2.5.0", "not-a-version"); ok || err == nil {
		t.Fatal("a running version Pacto cannot read is not accepted")
	}
	if rng, ok, err := acceptsBoth(c, "payments", "2.5.0", "2.4.0"); !ok || err != nil || rng != "^2.0.0" {
		t.Fatal("accepts both")
	}
}
