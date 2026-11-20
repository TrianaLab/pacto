package delivery

import (
	"fmt"
	"strings"
	"testing"
)

// chartPublishers is the publishers.json the chart's helm-unittest pins
// (tests/delivery_publishers_test.yaml): the same keys are read here.
const chartPublishers = `{"defaultBranch":"main","pinSigner":"keys:\n- name: recorder\n  publicKey: PEM\n  streams:\n  - pins/*/*","policy":"ghcr.io/acme/pacto-policy:v1","policySigners":"keys: []","registry":"ghcr.io/acme","streamsRepository":"ghcr.io/acme/pacto-streams","trustedRoot":true}`

func TestParsePublishersMatchesChart(t *testing.T) {
	c, err := ParsePublishers([]byte(chartPublishers))
	want := PublisherConfig{Registry: "ghcr.io/acme", DefaultBranch: "main", Policy: "ghcr.io/acme/pacto-policy:v1", PolicySigners: "keys: []",
		StreamsRepository: "ghcr.io/acme/pacto-streams", PinSigner: "keys:\n- name: recorder\n  publicKey: PEM\n  streams:\n  - pins/*/*", TrustedRoot: true}
	if err != nil || c != want {
		t.Fatalf("parsed %+v, %v", c, err)
	}
	if _, err := ParsePublishers([]byte("{")); err == nil || !strings.Contains(err.Error(), "publisher trust config") {
		t.Fatalf("a malformed config parsed: %v", err)
	}
}

func TestPublisherConfigRule(t *testing.T) {
	r, why := PublisherConfig{Registry: "ghcr.io/acme"}.Rule()
	if why != "" || r.Subject != acmeRule(t).Subject {
		t.Fatalf("rule %+v, %q", r, why)
	}
	if _, why := (PublisherConfig{}).Rule(); !strings.Contains(why, "delivery.publishers.registry") {
		t.Fatalf("no registry: %q", why)
	}
	if _, why := (PublisherConfig{Registry: "zot.acme.example/acme"}).Rule(); !strings.Contains(why, "names no CI provider") {
		t.Fatalf("no provider: %q", why)
	}
}

func registryStage(ns, name, svc string, p *PublisherStatus) StageObject {
	st := StageObject{Metadata: ObjectMeta{Namespace: ns, Name: name}}
	st.Spec.Service, st.Spec.Candidates.Registry, st.Status.Publisher = ServiceRef{Name: svc}, &RegistryCandidates{}, p
	return st
}

func trustOp(ns, name, stage string, phase Phase, id *PublisherIdentity) OperationObject {
	o := OperationObject{Metadata: ObjectMeta{Namespace: ns, Name: name}}
	o.Spec = OperationSpec{Type: TypeTrust, Stage: stage, Reason: "new signer", Params: Params{To: "sha256:b", Publisher: id}}
	o.Status.Phase = phase
	return o
}

// trustFixture is a payments Stage whose watch judged four digests, an
// orders Stage that judged none and a Flux Stage, with trust operations
// held, concluded, elsewhere, of no identity and of another type.
func trustFixture() ([]StageObject, []OperationObject, PublisherIdentity) {
	other := ciAcme
	other.Subject = strings.Replace(other.Subject, "release", "nightly", 1)
	third := other
	third.Subject = strings.Replace(third.Subject, "nightly", "adhoc", 1)
	stages := []StageObject{
		registryStage("prod", "payments-prod", "payments", &PublisherStatus{Verdicts: []PublisherVerdict{
			{Digest: "sha256:a", Verdict: VerdictAccepted, At: "2026-11-01T00:00:00Z", Identity: &ciAcme},
			{Digest: "sha256:b", Verdict: VerdictNewSigner, At: "2026-11-03T00:00:00Z", Identity: &other},
			{Digest: "sha256:c", Verdict: VerdictNewSigner, At: "2026-11-02T00:00:00Z", Identity: &third},
			{Digest: "sha256:d", Verdict: VerdictUnsigned, At: "2026-11-02T00:00:00Z"},
		}}),
		registryStage("prod", "orders-prod", "orders", nil),
		{Metadata: ObjectMeta{Namespace: "prod", Name: "flux"}, Spec: StageSpec{Service: ServiceRef{Name: "flux-only"}}},
	}
	ops := []OperationObject{
		trustOp("prod", "payments-prod-trust-1", "payments-prod", PhaseHeld, &other),
		trustOp("prod", "done", "payments-prod", PhaseConcluded, &third),
		trustOp("dev", "elsewhere", "payments-prod", PhaseHeld, &third),
		trustOp("prod", "no-identity", "payments-prod", PhaseHeld, nil),
		{Metadata: ObjectMeta{Namespace: "prod", Name: "promote"}, Spec: OperationSpec{Type: TypePromote, Stage: "payments-prod"}},
	}
	return stages, ops, third
}

func TestTrustOfRuleAndServices(t *testing.T) {
	stages, ops, _ := trustFixture()
	v := TrustOf(PublisherConfig{Registry: "ghcr.io/acme"}, stages, ops, func(_ string, s ServiceRef) []IdentityPin {
		if s == payments {
			return []IdentityPin{{Service: payments, Subject: ciAcme.Subject}}
		}
		return []IdentityPin{}
	})
	if v.RuleSource != PublisherSourceRule || v.Rule == nil || v.NoRule != "" || v.Policy != nil || len(v.Services) != 2 ||
		v.Services[0].Service.Name != "orders" || v.Services[1].Service != payments {
		t.Fatalf("view %+v", v)
	}
	if o := v.Services[0]; len(o.Pins) != 0 || len(o.Pending) != 0 || len(o.Verdicts) != 0 {
		t.Fatalf("orders %+v", o)
	}
	if v := TrustOf(PublisherConfig{}, stages, nil, nil); v.RuleSource != PublisherSourceNone || v.Rule != nil || v.NoRule == "" {
		t.Fatalf("no rule %+v", v)
	}
}

// TestTrustOfPending: a held trust operation of the Stage is pending with
// its name; a new signer no operation asks about yet is pending with none.
func TestTrustOfPending(t *testing.T) {
	stages, ops, third := trustFixture()
	p := TrustOf(PublisherConfig{}, stages, ops, func(string, ServiceRef) []IdentityPin { return []IdentityPin{{}} }).Services[1]
	if len(p.Pins) != 1 || len(p.Verdicts) != 4 || p.Verdicts[0].Digest != "sha256:b" || len(p.Pending) != 2 ||
		p.Pending[0].Operation != "payments-prod-trust-1" || p.Pending[1].Operation != "" || p.Pending[1].Identity.Subject != third.Subject {
		t.Fatalf("payments %+v", p)
	}
}

func TestTrustOfPolicy(t *testing.T) {
	stages := []StageObject{
		registryStage("prod", "a", "payments", &PublisherStatus{Source: PublisherSourcePolicy, PolicyDigest: "sha256:old", PolicyAt: "2026-11-01T00:00:00Z"}),
		registryStage("prod", "b", "payments", &PublisherStatus{Source: PublisherSourcePolicy, PolicyDigest: "sha256:new", PolicyAt: "2026-11-02T00:00:00Z"}),
		// A high-water mark of a policy this watch no longer judges with.
		registryStage("prod", "d", "payments", &PublisherStatus{Source: PublisherSourceNone, PolicyDigest: "sha256:newest", PolicyAt: "2026-11-03T00:00:00Z"}),
		registryStage("prod", "c", "payments", nil),
	}
	v := TrustOf(PublisherConfig{Registry: "ghcr.io/acme", Policy: "ghcr.io/acme/policy:v1"}, stages, nil, nil)
	if v.RuleSource != PublisherSourcePolicy || v.Rule != nil || v.NoRule != "" || *v.Policy != (PolicyState{Ref: "ghcr.io/acme/policy:v1", Digest: "sha256:new", At: "2026-11-02T00:00:00Z"}) {
		t.Fatalf("policy %+v %+v", v, v.Policy)
	}
	v = TrustOf(PublisherConfig{Policy: "ghcr.io/acme/policy:v1"}, stages[2:], nil, nil)
	if v.Policy.Digest != "" || !strings.Contains(v.NoRule, "only pinned signers") {
		t.Fatalf("unverified policy %+v", v)
	}
}

func TestTrustOfBounds(t *testing.T) {
	var verdicts []PublisherVerdict
	var ops []OperationObject
	var pins []IdentityPin
	for i := range 30 {
		id := ciAcme
		id.Subject = fmt.Sprintf("%s-%d", id.Subject, i)
		verdicts = append(verdicts, PublisherVerdict{Digest: fmt.Sprint(i), Verdict: VerdictNewSigner, Identity: &id})
		ops = append(ops, trustOp("prod", fmt.Sprint("op-", i), "s", PhaseHeld, &id))
		pins = append(pins, IdentityPin{Service: payments})
	}
	v := TrustOf(PublisherConfig{}, []StageObject{registryStage("prod", "s", "payments", &PublisherStatus{Verdicts: verdicts})}, ops,
		func(string, ServiceRef) []IdentityPin { return pins })
	if s := v.Services[0]; len(s.Pins) != MaxTrustItems || len(s.Pending) != MaxTrustItems || len(s.Verdicts) != MaxTrustItems {
		t.Fatalf("bounds %d %d %d", len(s.Pins), len(s.Pending), len(s.Verdicts))
	}
}

// TestTrustOfPinsPerNamespace: a service's pins are read per namespace of
// its registry Stages, newest first; one namespace that could not be read
// makes them unknown (null), never an empty list.
func TestTrustOfPinsPerNamespace(t *testing.T) {
	stages := []StageObject{registryStage("prod", "a", "payments", nil), registryStage("dev", "b", "payments", nil), registryStage("dev", "c", "payments", nil)}
	var asked []string
	v := TrustOf(PublisherConfig{}, stages, nil, func(ns string, _ ServiceRef) []IdentityPin {
		asked = append(asked, ns)
		return []IdentityPin{{Namespace: ns, At: map[string]string{"dev": "2026-11-01T00:00:00Z", "prod": "2026-11-02T00:00:00Z"}[ns]}}
	})
	if strings.Join(asked, ",") != "dev,prod" || len(v.Services[0].Pins) != 2 || v.Services[0].Pins[0].Namespace != "prod" {
		t.Fatalf("asked %v, pins %+v", asked, v.Services[0].Pins)
	}
	v = TrustOf(PublisherConfig{}, stages, nil, func(ns string, _ ServiceRef) []IdentityPin {
		if ns == "dev" {
			return nil
		}
		return []IdentityPin{{Namespace: ns}}
	})
	if v.Services[0].Pins != nil {
		t.Fatalf("unknown pins read as %+v", v.Services[0].Pins)
	}
}

// TestTrustOfWatchUnreadable: a registry Stage whose watch cannot judge is
// named in unreadable, so the answer is never complete on stale verdicts.
func TestTrustOfWatchUnreadable(t *testing.T) {
	down := registryStage("prod", "a", "payments", &PublisherStatus{})
	down.Status.Conditions = []Condition{{Type: ConditionReady, Status: "False", Reason: ReadyReasonRegistryUnreadable, Message: "the registry cannot be listed"}}
	other := registryStage("prod", "b", "payments", nil)
	other.Status.Conditions = []Condition{{Type: ConditionReady, Status: "False", Reason: "NoApplier"}}
	ok := registryStage("prod", "c", "payments", nil)
	ok.Status.Conditions = []Condition{{Type: ConditionReady, Status: "True", Reason: ConditionReady}}
	v := TrustOf(PublisherConfig{}, []StageObject{down, other, ok, registryStage("prod", "d", "payments", nil)}, nil, nil)
	if len(v.Unreadable) != 1 || !strings.Contains(v.Unreadable[0], "prod/a cannot judge") || !strings.Contains(v.Unreadable[0], "cannot be listed") {
		t.Fatalf("unreadable %v", v.Unreadable)
	}
}

// TestTrustOfAnsweredSigner: a new signer whose trust operation a person
// already answered is listed with that operation and how it ended, never as
// a signer waiting with no operation.
func TestTrustOfAnsweredSigner(t *testing.T) {
	id := ciAcme
	st := registryStage("prod", "s", "payments", &PublisherStatus{Verdicts: []PublisherVerdict{{Digest: "sha256:b", Verdict: VerdictNewSigner, Identity: &id}}})
	for outcome, want := range map[Outcome]string{
		Rejected:  "refused by bob at 2026-11-17T00:00:00Z in s-trust-",
		Succeeded: "confirmed at 2026-11-17T00:00:00Z",
		Expired:   "ended Expired at 2026-11-17T00:00:00Z; it is asked again",
	} {
		op := trustOp("prod", TrustOperationName("s", id), "s", PhaseConcluded, &id)
		op.Status.Outcome, op.Status.Times.Finished = outcome, "2026-11-17T00:00:00Z"
		if outcome == Rejected {
			op.Status.Approvals = []ApprovalRecord{{By: "bob", Decision: Reject}}
		}
		p := TrustOf(PublisherConfig{}, []StageObject{st}, []OperationObject{op}, nil).Services[0].Pending
		if len(p) != 1 || p[0].Operation != op.Metadata.Name || !strings.Contains(p[0].Reason, want) {
			t.Errorf("%s: %+v", outcome, p)
		}
	}
	op := trustOp("prod", TrustOperationName("s", id), "s", PhaseConcluded, &id)
	op.Status.Outcome = Rejected
	if p := TrustOf(PublisherConfig{}, []StageObject{st}, []OperationObject{op}, nil).Services[0].Pending; !strings.Contains(p[0].Reason, "refused by a person") {
		t.Errorf("no approver: %+v", p)
	}
}
