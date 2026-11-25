package impact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// stripeUse is a ref-less declared dependency on stripe.
func stripeUse(required bool) contract.Dependency {
	return contract.Dependency{Name: "stripe", External: true, Version: "2024-06-20", Required: required}
}

// withClient is a revision of c whose inferred record names stripe through
// an SDK, with major when it is not nil.
func withClient(c *contract.Contract, major *int) fleet.RawRevision {
	c.PactoVersion = "2.5"
	gomod := []byte("module example.com/payments\n")
	sum := sha256.Sum256(gomod)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
		Sources: []contract.InferredSourceFile{{Path: "go.mod", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[stripe]", Value: "stripe", Source: "go.mod:3", Confidence: "possible",
			External: &contract.InferredExternal{Identity: "stripe", Sources: []contract.InferredExternalSource{{Kind: "sdk", Path: "go.mod", Line: 3}},
				Client: &contract.InferredClient{SDK: "stripe-go", Major: major}}}}}
	return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}, "go.mod": {Data: gomod}}}, Digest: "sha256:" + c.Service.Name}
}

func intp(n int) *int { return &n }

// externalFleet: payments requires stripe (its record names stripe-go v76),
// billing may use it and shadow is only seen calling it.
func externalFleet(t *testing.T, extra ...fleet.RawRevision) *fleet.FleetSnapshot {
	t.Helper()
	snap := buildAt(t, &fleet.Collection{Revisions: append([]fleet.RawRevision{
		withClient(svcContract("payments", "1.0.0", contract.Owner{Team: "pay"}, stripeUse(true)), intp(76)),
		rev("billing", stripeUse(false)), rev("shadow"),
	}, extra...)})
	rec := snap.Externals["stripe"]
	if rec == nil {
		t.Fatal("no stripe record")
	}
	rec.Sources = append(rec.Sources, fleet.ExternalSource{Kind: fleet.ExternalSourceObserved, Service: keyOf(t, snap, "shadow"), Provenance: fleet.ProvenanceObserved},
		fleet.ExternalSource{Kind: fleet.ExternalSourceObserved, Service: keyOf(t, snap, "payments"), Provenance: fleet.ProvenanceObserved})
	return snap
}

func keyOf(t *testing.T, snap *fleet.FleetSnapshot, name string) fleet.ServiceKey {
	t.Helper()
	for k := range snap.Services {
		if _, n := fleet.ParseServiceKey(k); n == name {
			return k
		}
	}
	t.Fatalf("no service %s", name)
	return ""
}

func externalEvent(snap *fleet.FleetSnapshot, kind, id string) *Result {
	return Analyze(context.Background(), nil, nil, nil, nil, snap, Options{Event: &Event{Kind: kind, External: id}})
}

func rowOf(t *testing.T, res *Result, name string) AffectedConsumer {
	t.Helper()
	for _, c := range res.Consumers {
		if c.Service == name {
			return c
		}
	}
	t.Fatalf("no row %s in %+v", name, res.Consumers)
	return AffectedConsumer{}
}

func TestExternalChangedEveryConsumerAttention(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "stripe")
	if len(res.Consumers) != 3 || res.Gate != GateUnverified || !strings.HasPrefix(res.GateReason, GateReasonExternal) || res.Service != "stripe" {
		t.Fatalf("answer %s %q %+v", res.Gate, res.GateReason, res.Consumers)
	}
	for _, c := range res.Consumers {
		if c.Effect != EffectAttention || c.CompatibilityVerdict != CompatibilityUnknown || c.Note == "" {
			t.Errorf("%s: %+v", c.Service, c)
		}
	}
	if res.Blocking() != 0 || res.External == nil || res.External.Identity != "stripe" || res.External.Provider != "" {
		t.Errorf("blocking %d, external %+v", res.Blocking(), res.External)
	}
}

func TestExternalUnavailableEffects(t *testing.T) {
	res := externalEvent(externalFleet(t), EventUnavailable, "stripe")
	got := map[string]string{}
	for _, c := range res.Consumers {
		got[c.Service] = c.Effect + fmt.Sprint(c.Required)
	}
	if want := map[string]string{"payments": "breakstrue", "billing": "may-degradefalse", "shadow": "unknownfalse"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v", got)
	}
}

func TestExternalUnavailableNeverBlocks(t *testing.T) {
	res := externalEvent(externalFleet(t), EventUnavailable, "stripe")
	if res.Gate != GateUnverified || res.Blocking() != 0 || res.ReleaseBlocking() {
		t.Errorf("gate %s %q", res.Gate, res.GateReason)
	}
}

// No consumer in a complete snapshot is still no proof: nobody found never
// clears an external.
func TestExternalEventGateAlwaysUnverified(t *testing.T) {
	snap := externalFleet(t)
	snap.Externals["stripe"].Sources = nil
	for _, kind := range []string{EventChanged, EventUnavailable} {
		res := externalEvent(snap, kind, "stripe")
		if len(res.Consumers) != 0 || res.Completeness != fleet.CompletenessComplete || res.Gate != GateUnverified || !strings.HasPrefix(res.GateReason, GateReasonExternal) {
			t.Errorf("%s: %s %s %q", kind, res.Completeness, res.Gate, res.GateReason)
		}
	}
}

// providerFleet holds stripe's inferred contract 0.0.5, checkout pinning
// rng on it and payments and billing declaring it with no range.
func providerFleet(t *testing.T, rng string) *fleet.FleetSnapshot {
	t.Helper()
	revs := []fleet.RawRevision{inferredStripe("0.0.5"), rev("checkout", pinned(rng)), rev("payments", stripeUse(true))}
	return buildAt(t, &fleet.Collection{Revisions: revs})
}

func TestExternalOverInferredProviderForcesUnknown(t *testing.T) {
	res := externalEvent(providerFleet(t, "^2023.10"), EventChanged, "stripe")
	if !hasCode(res.Limitations, LimitationInferredProvider) || res.Gate != GateUnverified || res.External.Provider != stripeInferred {
		t.Fatalf("gate %s, limitations %+v, external %+v", res.Gate, res.Limitations, res.External)
	}
	c := rowOf(t, res, "checkout")
	if c.Compatibility != "^2023.10" || c.CompatibilityVerdict != CompatibilityUnknown || c.Note != inferredProviderNote || c.Effect != EffectAttention {
		t.Errorf("checkout %+v", c)
	}
}

func TestEffectForUse(t *testing.T) {
	for _, c := range []struct {
		required, declared bool
		want               string
	}{{true, true, EffectBreaks}, {false, true, EffectMayDegrade}, {true, false, EffectUnknown}, {false, false, EffectUnknown}} {
		if got := effectForUse(c.required, c.declared); got != c.want {
			t.Errorf("required %t declared %t: %s, want %s", c.required, c.declared, got, c.want)
		}
	}
}

func TestExternalGateReasonNamesShownVersion(t *testing.T) {
	res := externalEvent(providerFleet(t, "^2023.10"), EventChanged, "stripe")
	want := "stripe's inferred contract 0.0.5 is no contract this organization controls, so no range against it can be verified: 1 of 2 consumers pin a range on it. " +
		fleet.InferredExternalNextStep("stripe", "") + ". No observation source was consulted, so an undeclared caller is not listed"
	if res.GateReason != want || strings.Contains(res.GateReason, "  ") {
		t.Errorf("reason %q", res.GateReason)
	}
	r := &Result{inferredExternal: "stripe"}
	if got := r.externalGateReason(); !strings.HasPrefix(got, "stripe's inferred contract is no contract") || strings.Contains(got, "  ") {
		t.Errorf("no version: %q", got)
	}
}

func TestExternalConsumerSources(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "stripe")
	p := rowOf(t, res, "payments")
	if !reflect.DeepEqual(p.Sources, []string{"declared", "observed", "repository"}) || p.Provenance != "declared+observed" ||
		p.Confidence != ConfidenceCorroborated || !reflect.DeepEqual(p.Via, []string{fleet.RelationshipDependency, fleet.ProvenanceInferred, fleet.ProvenanceObserved}) {
		t.Errorf("payments %+v", p)
	}
	if s := rowOf(t, res, "shadow"); !reflect.DeepEqual(s.Sources, []string{"observed"}) || s.Provenance != fleet.ProvenanceObserved || s.Confidence != ConfidenceObserved {
		t.Errorf("shadow %+v", s)
	}
	if b := rowOf(t, res, "billing"); b.Provenance != fleet.ProvenanceDeclared || b.Confidence != ConfidenceDeclared {
		t.Errorf("billing %+v", b)
	}
}

func TestExternalInferredOnlyConsumer(t *testing.T) {
	snap := externalFleet(t)
	rec := snap.Externals["stripe"]
	rec.Sources = []fleet.ExternalSource{{Kind: fleet.ExternalSourcePodSpec, Service: keyOf(t, snap, "shadow"), Provenance: fleet.ProvenanceInferred}}
	res := externalEvent(snap, EventUnavailable, "stripe")
	if c := rowOf(t, res, "shadow"); c.Confidence != ConfidenceInferred || c.Provenance != fleet.ProvenanceInferred || c.Effect != EffectUnknown ||
		!reflect.DeepEqual(c.Via, []string{fleet.ProvenanceInferred}) {
		t.Errorf("shadow %+v", c)
	}
}

func TestMergeEventConsumerUnionsSources(t *testing.T) {
	first := &fleet.ExternalClient{SDK: "stripe-go", Major: 76}
	rows := mergeEventConsumer(nil, AffectedConsumer{Service: "x", Sources: []string{"observed"}})
	rows = mergeEventConsumer(rows, AffectedConsumer{Service: "x", Sources: []string{"declared", "observed"}, Client: first})
	rows = mergeEventConsumer(rows, AffectedConsumer{Service: "x", Sources: []string{"repository"}, Client: &fleet.ExternalClient{SDK: "other"}})
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Sources, []string{"declared", "observed", "repository"}) || rows[0].Client != first {
		t.Errorf("rows %+v", rows)
	}
}

func TestExternalConsumerClientMajor(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "stripe")
	c := rowOf(t, res, "payments").Client
	if c == nil || c.SDK != "stripe-go" || c.Major != 76 || c.Confidence != "possible" || c.Revision == "" {
		t.Errorf("client %+v", c)
	}
}

// A consumer whose record names stripe through a source no revision carries
// still gets the client its newest revision's record names.
func TestExternalConsumerClientFromNewestRevision(t *testing.T) {
	snap := externalFleet(t)
	rec := snap.Externals["stripe"]
	pay := keyOf(t, snap, "payments")
	rec.Sources = []fleet.ExternalSource{{Kind: fleet.ExternalSourceObserved, Service: pay, Provenance: fleet.ProvenanceObserved}}
	if c := rowOf(t, externalEvent(snap, EventChanged, "stripe"), "payments").Client; c == nil || c.Major != 76 {
		t.Errorf("client %+v", c)
	}
}

func TestExternalConsumerNoClientMajor(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{
		withClient(svcContract("payments", "1.0.0", contract.Owner{}, stripeUse(true)), nil)}})
	res := externalEvent(snap, EventChanged, "stripe")
	if c := rowOf(t, res, "payments"); c.Client != nil {
		t.Errorf("a missing major is absent, never zero: %+v", c.Client)
	}
	if c := rowOf(t, externalEvent(externalFleet(t), EventChanged, "stripe"), "billing"); c.Client != nil {
		t.Errorf("billing names no client: %+v", c.Client)
	}
}

func TestExternalEventCountsHiddenConsumers(t *testing.T) {
	snap := externalFleet(t)
	snap.Externals["stripe"].HiddenConsumers = 2
	res := externalEvent(snap, EventChanged, "stripe")
	if res.Hidden != 2 || len(res.Consumers) != 3 {
		t.Errorf("hidden %d, consumers %d", res.Hidden, len(res.Consumers))
	}
}

func TestExternalNotInFleetPartial(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "adyen")
	if res.Completeness != fleet.CompletenessPartial || !hasCode(res.Limitations, LimitationExternalNotInFleet) || res.Gate != GateUnverified ||
		len(res.Consumers) != 0 || res.External.Identity != "adyen" || res.Pin != "" {
		t.Errorf("answer %s %+v", res.Gate, res.Limitations)
	}
	for _, l := range res.Limitations {
		if l.Code == LimitationExternalNotInFleet && !strings.Contains(l.Message, "pacto fleet search") {
			t.Errorf("message %q", l.Message)
		}
	}
}

func TestExternalEventKinds(t *testing.T) {
	snap := externalFleet(t)
	for _, kind := range []string{EventRemoved, EventRegressed} {
		if res := externalEvent(snap, kind, "stripe"); !hasCode(res.Limitations, LimitationEventNeedsService) || len(res.Consumers) != 0 || res.Gate != GateUnverified {
			t.Errorf("%s: %+v", kind, res.Limitations)
		}
	}
	res := event(snap, EventChanged, "billing")
	if !hasCode(res.Limitations, LimitationEventNeedsExternal) || res.Completeness != fleet.CompletenessPartial || res.Gate != GateUnverified {
		t.Errorf("changed service: %s %+v", res.Gate, res.Limitations)
	}
}

func TestExternalUnreportedIsPartial(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("billing", stripeUse(false))}, Targets: []fleet.RawTarget{target("billing", ago(0))}})
	for _, tg := range snap.Targets {
		tg.Limitations = append(tg.Limitations, fleet.Limitation{Code: fleet.LimitationExternalsUnreported})
	}
	res := externalEvent(snap, EventChanged, "stripe")
	if res.Completeness != fleet.CompletenessPartial || !hasCode(res.Limitations, fleet.LimitationExternalsUnreported) || len(res.ActiveTargets) != 1 {
		t.Errorf("answer %s %+v %v", res.Completeness, res.Limitations, res.ActiveTargets)
	}
}

// The snapshot's record is never folded: an external beyond any answer's
// node bound still answers.
func TestExternalFoldedStillAnswers(t *testing.T) {
	var deps []contract.Dependency
	for i := range 201 {
		deps = append(deps, contract.Dependency{Name: fmt.Sprintf("legacy-%03d", i), External: true, Version: "v1"})
	}
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("billing", deps...)}})
	if len(snap.Externals) < 201 {
		t.Fatalf("externals %d", len(snap.Externals))
	}
	var last fleet.ExternalKey
	for k := range snap.Externals {
		last = max(last, k)
	}
	res := externalEvent(snap, EventChanged, string(last))
	if len(res.Consumers) != 1 || res.Consumers[0].Service != "billing" {
		t.Errorf("%s consumers %+v", last, res.Consumers)
	}
}

func TestExternalPinSuggestsCommonRange(t *testing.T) {
	snap := providerFleet(t, "^2023.10")
	res := externalEvent(snap, EventChanged, "stripe")
	want := "dependencies:\n  - name: stripe\n    ref: oci://" + stripeInferred + "\n    required: true\n    compatibility: \"^2023.10\"\n"
	if res.Pin != want {
		t.Errorf("pin:\n%s", res.Pin)
	}
}

func TestCommonRangeTiesByString(t *testing.T) {
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{{ToExternal: "stripe", Compatibility: "^2"}, {ToExternal: "stripe", Compatibility: "^1"},
		{ToExternal: "stripe", Compatibility: "^3"}, {ToExternal: "stripe", Compatibility: "^3"}, {ToExternal: "adyen", Compatibility: "^9"}}}
	if got := commonRange(snap, "stripe"); got != "^3" {
		t.Errorf("most common %q", got)
	}
	snap.Relationships = snap.Relationships[:2]
	if got := commonRange(snap, "stripe"); got != "^1" {
		t.Errorf("tie %q", got)
	}
}

// A consumer that pins "unknown" pins no range: it still gets the pin, the
// gate reason does not count it and "unknown" never outvotes a real range.
func TestExternalPinnedUnknownIsNoRange(t *testing.T) {
	res := externalEvent(providerFleet(t, contract.CompatibilityUnknown), EventChanged, "stripe")
	if !strings.Contains(res.GateReason, "0 of 2 consumers pin a range") {
		t.Errorf("reason %q", res.GateReason)
	}
	only := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{inferredStripe("0.0.5"), rev("checkout", pinned(contract.CompatibilityUnknown))}})
	if pin := externalEvent(only, EventChanged, "stripe").Pin; pin == "" {
		t.Error("a consumer pinned to unknown still gets the pin")
	}
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{{ToExternal: "stripe", Compatibility: "unknown"},
		{ToExternal: "stripe", Compatibility: "unknown"}, {ToExternal: "stripe", Compatibility: "^2024.6"}}}
	if got := commonRange(snap, "stripe"); got != "^2024.6" {
		t.Errorf("common range %q, want ^2024.6", got)
	}
}

// A call seen from an inferred service is inferred, never observed.
func TestExternalInferredCallerIsInferred(t *testing.T) {
	snap := externalFleet(t)
	rec := snap.Externals["stripe"]
	for i := range rec.Sources {
		if rec.Sources[i].Kind == fleet.ExternalSourceObserved && rec.Sources[i].Service == keyOf(t, snap, "shadow") {
			rec.Sources[i].Provenance = fleet.ProvenanceInferred
		}
	}
	c := rowOf(t, externalEvent(snap, EventChanged, "stripe"), "shadow")
	if !reflect.DeepEqual(c.Sources, []string{"inferred"}) || c.Provenance != fleet.ProvenanceInferred || c.Confidence != ConfidenceInferred ||
		!reflect.DeepEqual(c.Via, []string{fleet.ProvenanceInferred}) {
		t.Errorf("shadow %+v", c)
	}
}

// A stale egress call is evidence with its time and lowers confidence to
// unknown; its source is named in the gate reason.
func TestExternalStaleEgressLowersConfidence(t *testing.T) {
	snap := externalFleet(t)
	seen := snap.GeneratedAt.Add(-21 * 24 * time.Hour)
	snap.Relationships = append(snap.Relationships, fleet.Relationship{Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved,
		FromService: keyOf(t, snap, "shadow"), ToExternal: "stripe", Source: "otel", ObservedCount: 3, LastSeen: &seen})
	res := externalEvent(snap, EventChanged, "stripe")
	c := rowOf(t, res, "shadow")
	if c.Confidence != ConfidenceUnknown || len(c.Evidence) != 1 || c.Evidence[0].Kind != EvidenceObservedCall || !c.Evidence[0].Stale {
		t.Errorf("shadow %+v", c)
	}
	if !strings.HasSuffix(res.GateReason, "Observation sources consulted: otel") || hasCode(res.Limitations, LimitationExternalEgressUnread) {
		t.Errorf("reason %q, limitations %+v", res.GateReason, res.Limitations)
	}
}

// With no observation source the answer says an undeclared caller is unseen.
func TestExternalNoEgressSourceSaysSo(t *testing.T) {
	res := externalEvent(externalFleet(t), EventUnavailable, "stripe")
	if !hasCode(res.Limitations, LimitationExternalEgressUnread) || !strings.Contains(res.GateReason, "No observation source was consulted") {
		t.Errorf("reason %q, limitations %+v", res.GateReason, res.Limitations)
	}
}

func TestExternalPinUnknownWhenNoneDeclared(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{inferredStripe("0.0.5"), rev("payments", stripeUse(true))}})
	res := externalEvent(snap, EventChanged, "stripe")
	if !strings.Contains(res.Pin, "    ref: oci://"+stripeInferred+"\n") || !strings.HasSuffix(res.Pin, "    compatibility: \"unknown\"\n") {
		t.Errorf("pin:\n%s", res.Pin)
	}
}

func TestExternalPinRefLessWithoutInferredContract(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "stripe")
	want := "dependencies:\n  - name: stripe\n    external: true\n    required: true\n    version: <the API version you call>\n"
	if res.Pin != want {
		t.Errorf("pin:\n%s", res.Pin)
	}
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("billing", contract.Dependency{Name: "legacy", External: true, Identity: "host/corp-legacy.example", Version: "v1"})}})
	if pin := externalEvent(snap, EventChanged, "host/corp-legacy.example").Pin; !strings.Contains(pin, "  - name: corp-legacy-example\n    external: true\n    identity: host/corp-legacy.example\n") {
		t.Errorf("identity pin:\n%s", pin)
	}
}

// Every pin, wrapped in a 2.5 service, is a contract Pacto accepts.
func TestExternalPinValidates(t *testing.T) {
	pins := []string{
		externalEvent(providerFleet(t, "^2023.10"), EventChanged, "stripe").Pin,
		externalEvent(buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{inferredStripe("0.0.5"), rev("payments", stripeUse(true))}}), EventChanged, "stripe").Pin,
		externalEvent(externalFleet(t), EventChanged, "stripe").Pin,
	}
	for _, pin := range pins {
		raw := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n  version: 1.0.0\n" + strings.ReplaceAll(pin, "<the API version you call>", "2024-06-20")
		c, err := contract.Parse(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		if r := validation.Validate(c, []byte(raw), nil); !r.IsValid() {
			t.Errorf("%v\n%s", r.Errors, raw)
		}
	}
}

func TestExternalPinAbsentWhenAllPin(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{inferredStripe("0.0.5"), rev("checkout", pinned("^2023.10"))}})
	if res := externalEvent(snap, EventChanged, "stripe"); res.Pin != "" || len(res.Consumers) != 1 {
		t.Errorf("pin %q, consumers %+v", res.Pin, res.Consumers)
	}
}

func TestExternalResultCarriesProviderChange(t *testing.T) {
	snap := providerFleet(t, "^2023.10")
	change := &fleet.ExternalProviderChange{From: "0.0.4", To: "0.0.5", Added: []string{"clients stripe-go/77"}}
	snap.Externals["stripe"].Provider.Change = change
	res := externalEvent(snap, EventChanged, "stripe")
	if res.External.Change != change || res.External.Class == "" {
		t.Errorf("external %+v", res.External)
	}
}

func TestEventOrderAttentionLane(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "stripe")
	want := []TallyBucket{{OutcomeBreaks, 0}, {OutcomeMayDegrade, 0}, {OutcomeAttention, 3}, {OutcomeUnknown, 0}}
	if !reflect.DeepEqual(res.ConsumerTallies.ByOutcome, want) {
		t.Errorf("changed lanes %+v", res.ConsumerTallies.ByOutcome)
	}
	un := externalEvent(externalFleet(t), EventUnavailable, "stripe")
	if want := []TallyBucket{{OutcomeBreaks, 1}, {OutcomeMayDegrade, 1}, {OutcomeUnknown, 1}}; !reflect.DeepEqual(un.ConsumerTallies.ByOutcome, want) {
		t.Errorf("unavailable lanes %+v", un.ConsumerTallies.ByOutcome)
	}
	if effectRank(EffectAttention) != effectRank(EffectUnknown) {
		t.Error("attention ranks beside unknown")
	}
}

func TestProductCarriesExternalPinSourcesClient(t *testing.T) {
	snap := externalFleet(t)
	res := externalEvent(snap, EventChanged, "stripe")
	p := BuildProductOutcome(fleet.NewQuery(snap).ProductMeta(), snap, "", nil, nil, res, 0, 0, OutcomeAttention)
	if p.Service.Kind != fleet.KindExternal || p.Service.Key != "stripe" || p.External != res.External || p.Pin != res.Pin || p.Pin == "" {
		t.Fatalf("service %+v, external %+v, pin %q", p.Service, p.External, p.Pin)
	}
	if p.Consumers.Matching != 3 || p.Consumers.Items[1].Service.Label != "payments" || p.Consumers.Items[1].Client == nil || p.Consumers.Items[1].Client.Major != 76 {
		t.Errorf("attention page %d %+v", p.Consumers.Matching, p.Consumers.Items)
	}
	for _, c := range p.Consumers.Items {
		if len(c.Sources) == 0 || c.Effect != EffectAttention || c.Path[len(c.Path)-1].Kind != fleet.KindExternal {
			t.Errorf("%s: %+v", c.Service.Key, c)
		}
	}
}

// An answer about an external the fleet holds no record of still names it.
func TestProductExternalNotInFleetRef(t *testing.T) {
	snap := externalFleet(t)
	p := BuildProductOutcome(fleet.NewQuery(snap).ProductMeta(), snap, "", nil, nil, externalEvent(snap, EventChanged, "adyen"), 0, 0, "")
	if p.Service.Kind != fleet.KindExternal || p.Service.Key != "adyen" || p.Service.Status != fleet.StatusUnknown || p.Pin != "" {
		t.Errorf("service %+v", p.Service)
	}
}

func TestExternalStaleConsumerUnknownConfidence(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("billing", stripeUse(false))}, Targets: []fleet.RawTarget{target("billing", ago(30*time.Hour))}})
	res := externalEvent(snap, EventChanged, "stripe")
	if c := rowOf(t, res, "billing"); c.Confidence != ConfidenceUnknown || !hasCode(res.Limitations, "CONSUMER_EVIDENCE_STALE") {
		t.Errorf("billing %+v, limitations %+v", c, res.Limitations)
	}
}

// signedStripe is stripe's operator-signed inferred revision at version,
// seen an hour before evidenceNow and linking the OpenAPI digest fill.
func signedStripe(version, fill string) fleet.RawRevision {
	r := inferredStripe(version)
	c := stripeContract(version)
	c.Metadata = map[string]any{fleet.ExternalOpenAPIMetadataKey: "https://docs.stripe.com/openapi.json@sha256:" + strings.Repeat(fill, 64)}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"}, Fields: []contract.Inferred{},
		Surface: &contract.InferredSurface{Identity: "stripe", Class: "vendor-api", AsOf: evidenceNow.Add(-time.Hour).Format(time.RFC3339),
			Completeness: &contract.SurfaceCompleteness{State: "complete"}}}
	r.Bundle = &contract.Bundle{Contract: c, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}}
	return r
}

// The EXTERNAL_CHANGED item counts the consumers the external answer lists,
// hidden ones included, in the full and in a restricted view.
// engineStripe is signedStripe linking digest a and recording the observed
// engine.
func engineStripe(version, engine string) fleet.RawRevision {
	r := signedStripe(version, "a")
	r.Bundle.Contract.Metadata[fleet.ExternalEngineMetadataKey] = engine
	return r
}

func TestExternalChangedCountMatchesImpact(t *testing.T) {
	t.Run("openapi", func(t *testing.T) {
		externalChangedCountMatchesImpact(t, signedStripe("0.0.1", "a"), signedStripe("0.0.2", "b"))
	})
	t.Run("engine", func(t *testing.T) {
		externalChangedCountMatchesImpact(t, engineStripe("0.0.1", "postgres 15"), engineStripe("0.0.2", "postgres 16"))
	})
}

// externalChangedCountMatchesImpact checks the item over stripe's revisions
// older and newer; a restricted view raises no engine-only item.
func externalChangedCountMatchesImpact(t *testing.T, older, newer fleet.RawRevision) {
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{older, newer, rev("payments", stripeUse(true)), rev("billing", stripeUse(false)), rev("checkout", pinned(""))},
	}
	engineOnly := newer.Bundle.Contract.Metadata[fleet.ExternalEngineMetadataKey] != nil
	for name, ns := range map[string]string{"payments": "prod", "checkout": "prod", "billing": "secret"} {
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Compliance: fleet.StatusCompliant, EvidenceAt: ago(0)})
	}
	// A Kubernetes source names the namespaces a restriction hides.
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	full := fleet.NewQuery(snap)
	for name, q := range map[string]*fleet.Query{"full": full, "restricted": full.Restrict(func(_, ns string) bool { return ns != "secret" }, nil)} {
		view := q.SnapshotForSerialization()
		res := externalEvent(view, EventChanged, "stripe")
		al, err := q.Attention(fleet.AttentionFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var label string
		for _, it := range al.Items {
			if it.Code == fleet.AttentionExternalChanged {
				label = it.Label
			}
		}
		if engineOnly && name == "restricted" {
			if label != "" {
				t.Errorf("restricted engine-only change raised %q", label)
			}
			continue
		}
		if want := fmt.Sprintf(": %d consumers to review", len(res.Consumers)+res.Hidden); label == "" || !strings.HasSuffix(label, want) {
			t.Errorf("%s: label %q, want suffix %q (consumers %d, hidden %d)", name, label, want, len(res.Consumers), res.Hidden)
		}
		if name == "restricted" && res.Hidden == 0 {
			t.Errorf("restricted: billing is hidden, hidden = %d", res.Hidden)
		}
	}
}

func TestImpactExternalSubjectClaimedNamesDeclared(t *testing.T) {
	snap := claimedStripeFleet(t, inferredStripe("2023.10.15"))
	plain := externalEvent(claimedStripeFleet(t, inferredStripe("2023.10.15")), EventChanged, "stripe")
	res := externalEvent(snap, EventChanged, "stripe")
	want := "external stripe is claimed by stripe 0.1.0; the impact of a change to its contract is pacto impact oci://ghcr.io/acme/stripe:0.1.0 <new>"
	if !reflect.DeepEqual(res.Notes, []string{want}) {
		t.Fatalf("notes %q", res.Notes)
	}
	if res.Gate != GateUnverified || res.Gate != plain.Gate || len(res.Consumers) != len(plain.Consumers) {
		t.Errorf("gate %s, consumers %+v", res.Gate, res.Consumers)
	}
	for _, c := range res.Consumers {
		if c.Effect != EffectAttention {
			t.Errorf("%s: %s", c.Service, c.Effect)
		}
	}
}

func TestImpactExternalSubjectClaimHiddenSaysNothing(t *testing.T) {
	snap := claimedStripeFleet(t, inferredStripe("2023.10.15"))
	snap.Externals["stripe"].Claim = &fleet.ExternalClaim{Hidden: true}
	res := externalEvent(snap, EventChanged, "stripe")
	if len(res.Notes) != 0 || strings.Contains(res.GateReason, "ghcr.io/acme/stripe,") || strings.Contains(res.GateReason, "pacto claim") {
		t.Errorf("notes %q, gate reason %q", res.Notes, res.GateReason)
	}
}

func TestImpactExternalSubjectUnclaimedNamesClaim(t *testing.T) {
	res := externalEvent(externalFleet(t), EventChanged, "stripe")
	want := "external stripe has no claim: name its owner with pacto claim stripe --owner <team>"
	if !reflect.DeepEqual(res.Notes, []string{want}) || res.Gate != GateUnverified {
		t.Errorf("gate %s, notes %q", res.Gate, res.Notes)
	}
}

func TestImpactExternalSubjectClaimantsNamesNoClaim(t *testing.T) {
	snap := externalFleet(t)
	snap.Externals["stripe"].Claimants = 2
	res := externalEvent(snap, EventChanged, "stripe")
	want := "external stripe is claimed by 2 services, so no claim counts until all but one are retracted"
	if !reflect.DeepEqual(res.Notes, []string{want}) {
		t.Errorf("notes %q", res.Notes)
	}
}

// TestExternalEventConsumerClusters: on two clusters each consumer of an
// external system names the clusters of its uses: an observed or pod-spec use
// its target's, a declared use every target the consumer runs on.
func TestExternalEventConsumerClusters(t *testing.T) {
	use := func(svc string) fleet.RawExternalUse {
		return fleet.RawExternalUse{Service: svc, Namespace: "prod", Identity: "stripe", Class: "vendor-api", Confidence: "likely", Kind: "env",
			Where: "deployment/" + svc + " env STRIPE_API_BASE", Host: "api.stripe.com"}
	}
	eu := clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "checkout", "payments", "ledger")
	us := clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow, "ledger", "payments")
	for src, user := range map[fleet.Source]string{eu: "checkout", us: "ledger"} {
		col, _ := src.Collect(context.Background())
		col.ExternalUses = []fleet.RawExternalUse{use(user)}
		for i := range col.Targets {
			col.Targets[i].Kind = fleet.SourceKindKubernetes // a pod-spec use names its Kubernetes target
		}
	}
	snap := clusterFleet(t, []fleet.RawRevision{clusterRev("checkout"), clusterRev("ledger"), rev("payments", stripeUse(true))}, eu, us)
	res := externalEvent(snap, EventChanged, "stripe")
	if len(res.Clusters) != 2 {
		t.Fatalf("clusters %+v", res.Clusters)
	}
	for name, want := range map[string][]string{"checkout": {euID}, "ledger": {usID}, "payments": {usID, euID}} {
		if got := rowOf(t, res, name).Clusters; !slices.Equal(got, want) {
			t.Errorf("%s clusters %v, want %v", name, got, want)
		}
	}
}
