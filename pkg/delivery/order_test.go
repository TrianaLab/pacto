package delivery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// kustomization applies source (an OCIRepository in ns) into target.
func kustomization(ns, source, target string) KustomizationView {
	var k KustomizationView
	k.Metadata.Namespace, k.Metadata.Name = ns, source
	k.Spec.SourceRef.Kind, k.Spec.SourceRef.Name, k.Spec.TargetNamespace = "OCIRepository", source, target
	return k
}

// appliedHere is one Kustomization per Stage, applying its version into the
// Stage's own namespace.
func appliedHere(stages []StageObject) []KustomizationView {
	var out []KustomizationView
	for _, s := range stages {
		out = append(out, kustomization(s.Metadata.Namespace, s.Spec.ApplyRef.Name, s.Metadata.Namespace))
	}
	return out
}

func TestEnvironmentPactos(t *testing.T) {
	labelled := pactoIn("delivery", "f")
	labelled.Metadata.Labels = map[string]string{EnvironmentLabel: "test"}
	pactos := []PactoView{pactoIn("prod", "a"), pactoIn("orders-prod", "b"), pactoIn("test", "c"), pactoIn("loose", "d"), pactoIn("mixed", "e"), labelled, pactoIn("ops", "g")}
	stages := []StageObject{
		stageIn("delivery", "payments-prod", "payments", "prod"),
		stageIn("delivery", "payments-test", "payments-t", "test"),
		stageIn("orders-prod", "orders", "orders", "prod"),
		stageIn("mixed", "x", "x", "test"), stageIn("mixed", "y", "y", "prod"),
		stageIn("ops", "cart", "cart", "test"),
	}
	ks := []KustomizationView{
		kustomization("delivery", "payments", "prod"), kustomization("delivery", "payments-t", "test"),
		kustomization("flux-system", "orders", "orders-prod"), // applies nothing a Stage owns: the source is in another namespace
		kustomization("mixed", "x", "mixed"), kustomization("mixed", "y", "mixed"),
		kustomization("mixed", "x", ""), // no targetNamespace: it places nothing
		kustomization("ops", "cart", "test"),
	}
	ks[2].Spec.SourceRef.Namespace = "orders-prod"
	if got := names(EnvironmentPactos(pactos, stages, ks, nil, "prod")); !slices.Equal(got, []string{"prod/a", "orders-prod/b", "loose/d", "mixed/e", "ops/g"}) {
		// ops holds only a test Stage, but that Stage's version runs in test:
		// nothing places g, so it counts.
		t.Fatalf("prod keeps what runs there, the unplaced and the mixed namespace: %v", got)
	}
	if got := names(EnvironmentPactos(pactos, stages, ks, nil, "test")); !slices.Equal(got, []string{"test/c", "loose/d", "mixed/e", "delivery/f", "ops/g"}) {
		t.Fatalf("test: %v", got)
	}
	// The namespace a Stage object lives in places nothing: the Stages all
	// live in delivery, but only the label places f there.
	if got := names(EnvironmentPactos(pactos, stages, nil, nil, "prod")); !slices.Equal(got, []string{"prod/a", "orders-prod/b", "test/c", "loose/d", "mixed/e", "ops/g"}) {
		t.Fatalf("with no Kustomization every unlabelled Pacto counts: %v", got)
	}
	if got := EnvironmentPactos(pactos, nil, nil, errors.New("forbidden"), "prod"); len(got) != len(pactos) {
		t.Fatalf("unreadable Stages or Kustomizations keep every Pacto: %v", names(got))
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
	pactos = EnvironmentPactos(pactos, in.Stages, appliedHere(in.Stages), in.StagesErr, "prod")
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
		o1: bundle("orders", "1.8.5", paymentsDep(">=2.0.0 <4.0.0")), // also accepts both: the newer one wins
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

func shippingDep(rng string, required bool) contract.Dependency {
	return contract.Dependency{Name: "shipping", Ref: "oci://reg.example/pactos/shipping", Compatibility: rng, Required: required}
}

// runningPacto is a Pacto of a service with no dependencies running in ns.
func runningPacto(ns, name, version string) PactoView {
	p := consumerPacto(ns, name, version, "")
	p.Status.Dependencies = nil
	return p
}

func TestOrderCandidateMustAcceptWhatElseRunsHere(t *testing.T) {
	// orders 1.9.0 accepts payments 2 and 3 but needs shipping ^2.0.0, and prod
	// runs shipping 1.4.0: moving it first would break it, so 1.8.5 moves.
	in, pactos := ordersWorld(map[string]*contract.Bundle{
		o2: bundle("orders", "1.9.0", paymentsDep(">=2.0.0 <4.0.0"), shippingDep("^2.0.0", true)),
		o1: bundle("orders", "1.8.5", paymentsDep(">=2.0.0 <4.0.0"), shippingDep("^1.0.0", true)),
	})
	pactos = append(pactos, runningPacto("orders-prod", "shipping", "1.4.0"), runningPacto("test", "shipping", "2.0.0"))
	if m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3)); m.Candidate == nil || m.Candidate.Digest != o1 {
		t.Fatalf("1.8.5 accepts the shipping running here: %+v", m)
	}
	in.Stages[2].Status.Offered = []string{o2}
	m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
	if m.Candidate != nil || m.Unknown != "" || !strings.HasSuffix(m.Message, "No version of orders that test/orders verified can move first yet "+
		"(orders 1.9.0 accepts shipping ^2.0.0, but shipping 1.4.0 runs here). Ship a newer one that accepts both 3.0.0 and the 2.4.0 running here and what else runs here first.") {
		t.Fatalf("1.9.0 would break against shipping 1.4.0: %+v", m)
	}
	// Without shipping running here Pacto cannot tell: Unknown, not a candidate.
	m = onlyMove(t, orderPlan(t, in, pactos[:len(pactos)-2], payments2, payments3))
	if m.Candidate != nil || !strings.Contains(m.Unknown, "it needs shipping ^2.0.0 and Pacto sees no shipping running here") {
		t.Fatalf("no shipping here: %+v", m)
	}
}

func TestAcceptsWhatRunsHere(t *testing.T) {
	in, pactos := ordersWorld(nil)
	pactos = append(pactos, runningPacto("orders-prod", "shipping", "1.4.0"))
	s, err := Snapshot(context.Background(), EnvironmentPactos(pactos, in.Stages, appliedHere(in.Stages), nil, "prod"), nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		dep       contract.Dependency
		why, fail string
	}{
		{shippingDep("^1.0.0", true), "", ""},
		{shippingDep("whatever", true), "", `its range "whatever" on shipping cannot be read`},
		{contract.Dependency{Name: "stock", Compatibility: "^1.0.0"}, "", ""},
		{contract.Dependency{Name: "stock", Compatibility: "^1.0.0", Required: true}, "", "Pacto sees no stock running here"},
	}
	for _, c := range cases {
		why, err := acceptsWhatRunsHere(s, bundle("orders", "1.9.0", c.dep).Contract, "payments")
		if why != c.why || (c.fail == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.fail)) {
			t.Errorf("%+v: %q %v", c.dep, why, err)
		}
	}
	for _, r := range s.Revisions {
		if r.Service == "shipping" {
			r.Version = "not-a-version"
		}
	}
	if _, err := acceptsWhatRunsHere(s, bundle("orders", "1.9.0", shippingDep("^1.0.0", true)).Contract, "payments"); err == nil ||
		!strings.Contains(err.Error(), "the shipping running here has no usable version") {
		t.Fatalf("unreadable running version: %v", err)
	}
}

func TestOrderNeverMovesTheConsumerBack(t *testing.T) {
	// orders 1.7.0 never depended on payments, but prod runs 1.8.0: moving
	// 1.7.0 "first" would roll orders back, so it is no candidate.
	in, pactos := ordersWorld(map[string]*contract.Bundle{
		o2: bundle("orders", "1.7.0"),
		o1: bundle("orders", "1.9.0", paymentsDep(">=2.0.0 <4.0.0")),
	})
	in.Stages[1].Status.Verified = []string{o1} // 1.9.0 ran here once: promoting it again is a rollback
	m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
	if m.Candidate != nil || m.Unknown != "" || !strings.Contains(m.Message, "can move first yet (orders 1.7.0 is not newer than the 1.8.0 running here)") {
		t.Fatalf("an older version is no candidate: %+v", m)
	}
}

func TestNewerThan(t *testing.T) {
	c := bundle("orders", "1.9.0").Contract
	if why, err := newerThan(c, ""); why != "" || err != nil {
		t.Fatal("nothing runs: nothing to roll back")
	}
	if _, err := newerThan(c, "garbage"); err == nil || !strings.Contains(err.Error(), "the orders running here has no usable version") {
		t.Fatalf("running version unreadable: %v", err)
	}
	if _, err := newerThan(bundle("orders", "x").Contract, "1.8.0"); err == nil || !strings.Contains(err.Error(), "its version cannot be read") {
		t.Fatalf("candidate version unreadable: %v", err)
	}
}

func TestOrderSharesOneRegistryBound(t *testing.T) {
	defer func(d time.Duration) { registryTimeout = d }(registryTimeout)
	registryTimeout = 50 * time.Millisecond
	in, pactos := ordersWorld(nil)
	reads := 0
	in.Candidates = func(ctx context.Context, _ StageObject, d string) Revision {
		reads++
		<-ctx.Done() // a registry that never answers
		return Revision{Digest: d, Err: ctx.Err().Error()}
	}
	start := time.Now()
	m := onlyMove(t, orderPlan(t, in, pactos, payments2, payments3))
	if took := time.Since(start); took > 10*registryTimeout || reads != 1 {
		t.Fatalf("one bound for the whole order: took %s, %d reads", took, reads)
	}
	if !strings.Contains(m.Unknown, "sha256:020202020202: the registry did not answer within 50ms") ||
		!strings.Contains(m.Unknown, "sha256:010101010101: not read, the registry did not answer within 50ms") {
		t.Fatalf("the rest is said unread: %+v", m)
	}
}

// dependentPacto is a Pacto running in ns that depends on orders with rng.
func dependentPacto(ns, name, version, rng string) PactoView {
	p := runningPacto(ns, name, version)
	p.Status.Dependencies = []DependencyView{{Name: "orders", Ref: "oci://reg.example/pactos/orders", Required: true, Compatibility: rng}}
	return p
}

func TestOrderCandidateMustKeepItsOwnConsumers(t *testing.T) {
	// orders 2.0.0 accepts payments 2 and 3, but checkout 1.0.0 runs here and
	// accepts orders ^1.0.0: moving 2.0.0 first would break checkout, so 1.9.0 moves.
	in, pactos := ordersWorld(map[string]*contract.Bundle{
		o2: bundle("orders", "2.0.0", paymentsDep(">=2.0.0 <4.0.0")),
		o1: bundle("orders", "1.9.0", paymentsDep(">=2.0.0 <4.0.0")),
	})
	withCheckout := append(slices.Clone(pactos), dependentPacto("prod", "checkout", "1.0.0", "^1.0.0"))
	if m := onlyMove(t, orderPlan(t, in, withCheckout, payments2, payments3)); m.Candidate == nil || m.Candidate.Digest != o1 {
		t.Fatalf("1.9.0 keeps checkout working: %+v", m)
	}
	in.Stages[2].Status.Offered = []string{o2}
	m := onlyMove(t, orderPlan(t, in, withCheckout, payments2, payments3))
	if m.Candidate != nil || m.Unknown != "" || !strings.Contains(m.Message, "can move first yet (checkout 1.0.0 accepts orders ^1.0.0, not 2.0.0)") {
		t.Fatalf("2.0.0 would break checkout: %+v", m)
	}
	// A range Pacto cannot read leaves it Unknown, never a candidate.
	unreadable := append(slices.Clone(pactos), dependentPacto("prod", "checkout", "1.0.0", "whatever"))
	m = onlyMove(t, orderPlan(t, in, unreadable, payments2, payments3))
	if m.Candidate != nil || !strings.Contains(m.Unknown, `the range "whatever" checkout 1.0.0 holds on orders cannot be read`) {
		t.Fatalf("unreadable range: %+v", m)
	}
}

func TestAcceptedHere(t *testing.T) {
	in, pactos := ordersWorld(nil)
	pactos = append(pactos, dependentPacto("prod", "checkout", "1.0.0", "^1.0.0"))
	s, err := Snapshot(context.Background(), EnvironmentPactos(pactos, in.Stages, appliedHere(in.Stages), nil, "prod"), nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	var orders fleet.ServiceKey
	for _, r := range s.Revisions {
		if r.Service == "orders" {
			orders = r.ServiceKey
		}
	}
	if _, err := acceptedHere(s, orders, bundle("orders", "not-a-version").Contract); err == nil || !strings.Contains(err.Error(), "its version cannot be read") {
		t.Fatalf("unreadable candidate version: %v", err)
	}
	if why, err := acceptedHere(s, orders, bundle("orders", "1.9.0").Contract); why != "" || err != nil {
		t.Fatalf("1.9.0 is accepted: %q %v", why, err)
	}
}

// Shipping a consumer first is offered as a way out only when it clears the
// change: every blocked consumer can move and Pacto saw every consumer.
func TestOrderQuestion(t *testing.T) {
	c := &Candidate{Digest: o1}
	orders, inventory := MoveFirst{Service: "orders", Candidate: c}, MoveFirst{Service: "inventory"}
	cases := []struct {
		o    Order
		want string
	}{
		{Order{MoveFirst: []MoveFirst{inventory}}, ""},
		{Order{MoveFirst: []MoveFirst{orders}}, ". Or ship orders first: a version that accepts it already verified upstream (see the plan's order)"},
		{Order{MoveFirst: []MoveFirst{orders, inventory}}, ". orders can move first, but no version of inventory can yet, so that alone does not clear it (see the plan's order)"},
		{Order{MoveFirst: []MoveFirst{orders}, Unknown: []string{"the operational graph is partial"}},
			". orders can move first, but Pacto cannot see every consumer, so that alone may not clear it (see the plan's order)"},
	}
	for _, tc := range cases {
		if got := orderQuestion(&tc.o); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.o, got, tc.want)
		}
	}
}
