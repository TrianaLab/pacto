package impact

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// dep is a declared dependency on name, required or optional.
func dep(name string, required bool) contract.Dependency {
	return contract.Dependency{Name: name, Ref: "ghcr.io/x/" + name + ":1.0.0", Required: required, Compatibility: "^1.0.0"}
}

func rev(name string, deps ...contract.Dependency) fleet.RawRevision {
	return fleet.RawRevision{Bundle: mkBundle(svcContract(name, "1.0.0", contract.Owner{Team: name + "-team"}, deps...))}
}

func target(name string, at *time.Time) fleet.RawTarget {
	return fleet.RawTarget{Scope: "prod", Kind: "deployment", Name: name, Service: name, Compliance: fleet.StatusCompliant, EvidenceAt: at}
}

func buildAt(t *testing.T, col *fleet.Collection) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func event(snap *fleet.FleetSnapshot, kind, service string, opts ...Options) *Result {
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	o.Event = &Event{Kind: kind, Service: service}
	return Analyze(context.Background(), nil, nil, nil, nil, snap, o)
}

func effectsOf(res *Result) map[string]string {
	out := map[string]string{}
	for _, c := range res.Consumers {
		out[c.Service] = c.Effect
	}
	return out
}

// Events walk the same dependents as a contract change over the same
// snapshot, and add the observed and uncontracted callers they always read.
func TestEventUsesDependentsWalk(t *testing.T) {
	snap := buildChain(t)
	contract := analyzeAuth(snap, Options{})
	ev := event(snap, EventUnavailable, "auth-service")
	names := func(r *Result) []string {
		var out []string
		for _, c := range r.Consumers {
			out = append(out, c.Service)
		}
		slices.Sort(out)
		return out
	}
	if !reflect.DeepEqual(names(contract), names(ev)) || ev.Service != "auth-service" || ev.Event.Kind != EventUnavailable || ev.SnapshotID != snap.SnapshotID {
		t.Errorf("contract %v, event %v (%+v)", names(contract), names(ev), ev)
	}
	obs := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("shadow")},
		Observed: []fleet.ObservedEdge{{From: "shadow", To: "auth-service", Count: 2, LastSeen: *ago(time.Hour)}, {From: "k8s:pay/Deployment/legacy", To: "auth-service", Count: 1}}})
	got := effectsOf(event(obs, EventUnavailable, "auth-service"))
	if got["shadow"] != EffectUnknown || got["k8s:pay/Deployment/legacy"] != EffectUnknown || len(got) != 2 {
		t.Errorf("observed and uncontracted callers = %v", got)
	}
}

// The effect is the worst over every declared path, and the path shown is the
// one that justifies it, not the first shortest path.
func TestEventEffectUsesWorstPath(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{
		rev("x"), rev("b-opt", dep("x", true)), rev("c-req", dep("x", true)), rev("a", dep("b-opt", false), dep("c-req", true)),
	}})
	a := consumerNamed(t, event(snap, EventUnavailable, "x"), "a")
	if a.Effect != EffectBreaks || !reflect.DeepEqual(a.Path, []string{"a", "c-req", "x"}) {
		t.Errorf("a = %s via %v", a.Effect, a.Path)
	}
	// Synthetic: the only alternative to the optional path holds a
	// configuration-reference hop, so a is unknown; with a required path too
	// it breaks.
	rel := func(from, to, typ string, required bool) fleet.Relationship {
		return fleet.Relationship{FromService: fleet.ServiceKey(from), ToService: fleet.ServiceKey(to), Type: typ, Required: required,
			Provenance: fleet.ProvenanceDeclared, Resolved: true}
	}
	graph := &fleet.GraphResult{Root: "x", Nodes: []fleet.GraphNode{{Key: "b"}, {Key: "c"}, {Key: "d"}, {Key: "a"}}}
	s := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{
		rel("b", "x", fleet.RelationshipDependency, true), rel("b", "x", fleet.RelationshipConfigRef, false), rel("c", "x", fleet.RelationshipConfigRef, false),
		rel("a", "b", fleet.RelationshipDependency, false), rel("a", "c", fleet.RelationshipDependency, true),
		rel("a", "a", fleet.RelationshipDependency, true), {FromService: "a", ToService: "x", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved, Resolved: true},
	}}
	eff := eventEffects(graph, s)
	if eff["a"].effect != EffectUnknown || !reflect.DeepEqual(eff["a"].path, []fleet.ServiceKey{"a", "c", "x"}) || eff["d"].effect != "" {
		t.Errorf("a = %+v, d = %+v", eff["a"], eff["d"])
	}
	s.Relationships = append(s.Relationships, rel("d", "x", fleet.RelationshipDependency, true), rel("a", "d", fleet.RelationshipDependency, true))
	if eff := eventEffects(graph, s); eff["a"].effect != EffectBreaks {
		t.Errorf("with a required path a = %+v", eff["a"])
	}
	// A cycle between consumers never makes a path through itself.
	s.Relationships = []fleet.Relationship{rel("b", "x", fleet.RelationshipDependency, false), rel("a", "b", fleet.RelationshipDependency, true), rel("b", "a", fleet.RelationshipConfigRef, false)}
	if eff := eventEffects(graph, s); eff["b"].effect != EffectMayDegrade || eff["a"].effect != EffectMayDegrade {
		t.Errorf("cycle: %+v", eff)
	}
}

// An observed direct call no declared edge explains is a path whose effect
// is unknown, so it outranks an optional transitive path.
func TestEventObservedDirectCallOutranksOptionalPath(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{
		rev("x"), rev("b", dep("x", false)), rev("a", dep("b", true)),
	}, Observed: []fleet.ObservedEdge{{From: "a", To: "x", Count: 1}}})
	a := consumerNamed(t, event(snap, EventUnavailable, "x"), "a")
	if a.Effect != EffectUnknown || !reflect.DeepEqual(a.Path, []string{"a", "x"}) || strings.Contains(a.Note, "every path") {
		t.Errorf("a = %s via %v (%s)", a.Effect, a.Path, a.Note)
	}
	// A declared optional direct dependency explains its own observed call.
	snap = buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("x"), rev("b", dep("x", false))},
		Observed: []fleet.ObservedEdge{{From: "b", To: "x", Count: 1}}})
	if b := consumerNamed(t, event(snap, EventUnavailable, "x"), "b"); b.Effect != EffectMayDegrade {
		t.Errorf("b = %s", b.Effect)
	}
}

// A hop holding an optional dependency and a configuration reference is
// unknown in either order: the configuration reference's effect is unknown.
func TestEventHopConfigRefOutranksOptional(t *testing.T) {
	dep := fleet.Relationship{Type: fleet.RelationshipDependency}
	ref := fleet.Relationship{Type: fleet.RelationshipConfigRef}
	req := fleet.Relationship{Type: fleet.RelationshipDependency, Required: true}
	for _, tc := range []struct {
		rels []fleet.Relationship
		want string
	}{
		{[]fleet.Relationship{dep, ref}, EffectUnknown},
		{[]fleet.Relationship{ref, dep}, EffectUnknown},
		{[]fleet.Relationship{dep}, EffectMayDegrade},
		{[]fleet.Relationship{ref}, EffectUnknown},
		{[]fleet.Relationship{ref, req, dep}, EffectBreaks},
		{[]fleet.Relationship{req, ref}, EffectBreaks},
	} {
		g := ""
		for _, r := range tc.rels {
			g = hopGrade(g, r)
		}
		if g != tc.want {
			t.Errorf("%+v = %s, want %s", tc.rels, g, tc.want)
		}
	}
}

// An ambiguous caller name matches several contracts, so the answer never
// says no contract describes it.
func TestEventAmbiguousCallerNote(t *testing.T) {
	snap := &fleet.FleetSnapshot{UncontractedCallers: []fleet.UncontractedCaller{{Caller: "payments-service", ToService: "redis", Ambiguous: true}}}
	c := uncontractedCallers(snap, "redis")
	if len(c) != 1 || !strings.Contains(c[0].Note, "matches several services") {
		t.Fatalf("ambiguous caller = %+v", c)
	}
}

func TestEventUnavailableUncontractedCallerNotClear(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service")},
		Observed: []fleet.ObservedEdge{{From: "k8s:pay/Deployment/legacy", To: "auth-service", Count: 1}}})
	res := event(snap, EventUnavailable, "auth-service")
	c := onlyConsumer(t, res)
	if !c.Uncontracted || c.Effect != EffectUnknown || res.Completeness != fleet.CompletenessComplete || res.Gate != GateUnverified {
		t.Errorf("consumer %+v, gate %s", c, res.Gate)
	}
}

func TestEventClearReasonNamesObservationCoverage(t *testing.T) {
	res := event(buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service")}}), EventUnavailable, "auth-service")
	want := "no contract in the complete snapshot declares a dependency on auth-service and no observation source saw a caller; " +
		"a service with no contract can still call it. No observation source was consulted"
	if res.Gate != GateClear || res.GateReason != want {
		t.Errorf("gate %s: %q", res.Gate, res.GateReason)
	}
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("a"), rev("b")},
		Observed: []fleet.ObservedEdge{{From: "a", To: "b", Count: 1}}})
	res = event(snap, EventRemoved, "auth-service", Options{ObservedEdges: []ObservedEdge{{Consumer: "a", Provider: "b"}}})
	if res.Gate != GateClear || !strings.HasSuffix(res.GateReason, "Observation sources consulted: test, traces file") {
		t.Errorf("gate %s: %q", res.Gate, res.GateReason)
	}
	// A traces file that was read and saw no call was still consulted.
	res = event(buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service")}}), EventUnavailable, "auth-service", Options{ObservedEdges: []ObservedEdge{}})
	if res.Gate != GateClear || !strings.HasSuffix(res.GateReason, "Observation sources consulted: traces file") {
		t.Errorf("empty traces file: gate %s: %q", res.Gate, res.GateReason)
	}
}

func TestEventRemovedUnresolvedConfigRefPartial(t *testing.T) {
	res := event(configRefSnap(t, false, refConsumer("orders", "", map[string]any{"size": "medium"}, false)), EventRemoved, "postgresql")
	if res.Completeness != fleet.CompletenessPartial || res.Gate != GateUnverified || limitation(res, "CONFIG_REF_CONSUMERS_UNRESOLVED") == nil ||
		!strings.HasPrefix(res.GateReason, "the answer is partial") {
		t.Errorf("completeness %s, gate %s (%s)", res.Completeness, res.Gate, res.GateReason)
	}
}

func TestEventRemovedUnresolvedPolicyRefPartial(t *testing.T) {
	tagged := policyReferrer("orders", false)
	tagged.Lock = nil
	tagged.Bundle.Contract.Policies[0].Ref = policyRef + ":1"
	snap := policySnap(t, policyProvider("1.0.0"), nil, tagged)
	res := event(snap, EventRemoved, "policy")
	if res.Completeness != fleet.CompletenessPartial || res.Gate != GateUnverified || limitation(res, "POLICY_REF_CONSUMERS_UNRESOLVED") == nil {
		t.Errorf("completeness %s, gate %s, limitations %+v", res.Completeness, res.Gate, res.Limitations)
	}
	// Unavailability does not read policy references.
	if res := event(snap, EventUnavailable, "policy"); res.Gate != GateClear {
		t.Errorf("unavailable gate %s (%s)", res.Gate, res.GateReason)
	}
}

func TestEventGateReasonStaleTargetsNotRunning(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("api-gateway", authDep), rev("web", authDep), rev("cli", authDep)},
		Targets: []fleet.RawTarget{target("api-gateway", ago(48*time.Hour)), target("web", ago(time.Hour)), target("cli", nil)}})
	res := event(snap, EventUnavailable, "auth-service")
	want := "if auth-service becomes unavailable, 3 consumers break (1 runs on known targets: web; 1 was last seen on a target more than 24h before asOf: api-gateway; " +
		"1 has no current target evidence: a target with no evidence time, missing from the snapshot or marked stale by its source: cli). Review each"
	if res.GateReason != want {
		t.Errorf("reason %q\nwant   %q", res.GateReason, want)
	}
}

func TestEventRequiredIsBreaks(t *testing.T) {
	res := event(evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))}), EventUnavailable, "auth-service")
	c := onlyConsumer(t, res)
	if c.Effect != EffectBreaks || !c.Required || c.Confidence != ConfidenceContractual || !reflect.DeepEqual(c.Via, []string{fleet.RelationshipDependency}) ||
		!strings.Contains(c.Note, "required") {
		t.Errorf("consumer = %+v", c)
	}
	if res.GateReason != "if auth-service becomes unavailable, 1 consumer breaks (1 runs on known targets: api-gateway). Review each" {
		t.Errorf("reason %q", res.GateReason)
	}
}

func TestEventOptionalIsMayDegrade(t *testing.T) {
	res := event(evidenceSnap(t, []fleet.RawRevision{gwRev(dep("auth-service", false))}, nil), EventUnavailable, "auth-service")
	c := onlyConsumer(t, res)
	if c.Effect != EffectMayDegrade || c.Required || res.Gate != GateUnverified || !strings.Contains(c.Note, "optional") ||
		res.GateReason != "if auth-service becomes unavailable, 1 may degrade. Review each" {
		t.Errorf("consumer = %+v, reason %q", c, res.GateReason)
	}
	if res.ConsumerTallies.ByOutcome[1] != (TallyBucket{Key: OutcomeMayDegrade, Count: 1}) {
		t.Errorf("tallies %+v", res.ConsumerTallies.ByOutcome)
	}
}

func TestEventConfigRefIsUnknown(t *testing.T) {
	res := event(configRefSnap(t, false, refConsumer("orders", "", map[string]any{"size": "medium"}, true)), EventUnavailable, "postgresql")
	c := onlyConsumer(t, res)
	if c.Effect != EffectUnknown || !reflect.DeepEqual(c.Via, []string{fleet.RelationshipConfigRef}) || c.Provenance != fleet.ProvenanceDeclared {
		t.Errorf("consumer = %+v", c)
	}
	if res.GateReason != "if postgresql becomes unavailable, 1 is unknown. Review each" {
		t.Errorf("reason %q", res.GateReason)
	}
}

func TestEventTransitiveAllRequiredBreaksInferred(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("api-gateway", authDep), rev("frontend", dep("api-gateway", true))}})
	c := consumerNamed(t, event(snap, EventUnavailable, "auth-service"), "frontend")
	if c.Effect != EffectBreaks || c.Depth != 2 || c.Confidence != ConfidenceInferred || c.Required ||
		!reflect.DeepEqual(c.Path, []string{"frontend", "api-gateway", "auth-service"}) {
		t.Errorf("frontend = %+v", c)
	}
}

func TestEventTransitiveOptionalHopMayDegrade(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("api-gateway", authDep), rev("frontend", dep("api-gateway", false))}})
	got := effectsOf(event(snap, EventRemoved, "auth-service"))
	if got["frontend"] != EffectMayDegrade || got["api-gateway"] != EffectBreaks {
		t.Errorf("effects %v", got)
	}
}

func TestEventNeverCompatible(t *testing.T) {
	res := event(buildChain(t), EventUnavailable, "auth-service")
	for _, c := range res.Consumers {
		if c.CompatibilityVerdict != CompatibilityUnknown {
			t.Errorf("%s verdict %s", c.Service, c.CompatibilityVerdict)
		}
	}
	p := BuildProductOutcome(fleet.NewQuery(buildChain(t)).ProductMeta(), buildChain(t), "auth-service", nil, nil, res, 0, 0, "")
	for _, b := range p.Consumers.ByVerdict {
		if b.Key == CompatibilityCompatible && b.Count != 0 {
			t.Errorf("by verdict %+v", p.Consumers.ByVerdict)
		}
	}
}

func TestEventGateNeverClearWithConsumers(t *testing.T) {
	for _, kind := range []string{EventUnavailable, EventRemoved, EventRegressed} {
		if res := event(buildChain(t), kind, "auth-service"); res.Gate != GateUnverified || res.Classification != "" {
			t.Errorf("%s gate %s", kind, res.Gate)
		}
	}
}

func TestEventGateClearOnlyCompleteEmpty(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service")}})
	if res := event(snap, EventUnavailable, "auth-service"); res.Gate != GateClear || len(res.Consumers) != 0 {
		t.Errorf("gate %s", res.Gate)
	}
	snap.Completeness = fleet.CompletenessPartial
	if res := event(snap, EventUnavailable, "auth-service"); res.Gate != GateUnverified {
		t.Errorf("partial gate %s", res.Gate)
	}
}

// A consumer that would block a contract change only makes an event unverified.
func TestEventGateNeverBlocked(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(dep("auth-service", true))}, []fleet.RawTarget{gwTarget(ago(time.Hour))})
	if res := analyzeAuth(snap, Options{}); res.Gate != GateBlocked {
		t.Fatalf("contract gate %s", res.Gate)
	}
	if res := event(snap, EventUnavailable, "auth-service"); res.Gate != GateUnverified || res.ReleaseBlocking() {
		t.Errorf("event gate %s", res.Gate)
	}
}

func TestEventServiceNotInFleet(t *testing.T) {
	res := event(buildChain(t), EventUnavailable, "nope")
	if res.Completeness != fleet.CompletenessPartial || res.Gate != GateUnverified || limitation(res, "SERVICE_NOT_IN_FLEET") == nil || res.Service != "nope" {
		t.Errorf("res = %+v", res)
	}
}

// pgProvisioned is configRefSnap with a valid revision digest for orders and
// a provider report per environment, the last one stale.
func pgProvisioned(t *testing.T, envs ...string) *fleet.FleetSnapshot {
	t.Helper()
	orders := refConsumer("orders", "", map[string]any{"size": "medium"}, true)
	orders.Digest = validDigest("a")
	orders.Bundle.Contract.Dependencies = []contract.Dependency{{Name: "postgresql", Ref: "oci://x/postgresql:16.1.0", Required: true, Compatibility: "^16.0.0"}}
	pg := pgContract("16.1.0", "provisioning", true)
	pg.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-10-01", Sunset: "2027-01-01"}
	var reports []fleet.RawProvisioning
	for i, env := range envs {
		at := evidenceNow.Add(-time.Hour)
		if i == len(envs)-1 {
			at = evidenceNow.Add(-30 * 24 * time.Hour)
		}
		reports = append(reports, fleet.RawProvisioning{Service: "orders", Digest: orders.Digest, Kind: "configuration", Name: "db", Environment: env,
			Provider: "aws", ExternalRef: "orders-db", Phase: "Ready", Outcome: "Observed", ObservedAt: at, AcceptedAt: &at})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: &contract.Bundle{Contract: pg, FS: pgSchema("small")}, Digest: "sha256:pg", RequestedRef: "oci://x/postgresql:16.1.0",
		}, orders}}),
		fleet.NewMemorySource("evidence", "evidence-http", &fleet.Collection{Provisioning: reports}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestEventRemovedResources(t *testing.T) {
	res := event(pgProvisioned(t, "production", "staging"), EventRemoved, "postgresql")
	c := onlyConsumer(t, res)
	var phases []string
	for _, r := range c.Resources {
		phases = append(phases, r.Environment+"="+r.Phase+"/"+map[bool]string{true: "stale", false: "fresh"}[r.Stale])
	}
	// The dependency on postgresql has no report: its one status is Unknown.
	if c.ResourcesTotal != 3 || !reflect.DeepEqual(phases, []string{"production=Ready/fresh", "staging=Unknown/stale", "=Unknown/fresh"}) {
		t.Errorf("resources %v (%d)", phases, c.ResourcesTotal)
	}
	if res := event(pgProvisioned(t, "production"), EventUnavailable, "postgresql"); len(onlyConsumer(t, res).Resources) != 0 {
		t.Errorf("unavailable named resources")
	}
	many := make([]fleet.ProvisioningStatus, MaxConsumerResources+3)
	for i := range many {
		many[i].Entry = strconv.Itoa(i)
	}
	got, total := resourcesOf([]fleet.OfferingUse{{Consumer: fleet.EntityRef{Key: "a"}, Provisioning: many}, {Consumer: fleet.EntityRef{Key: "b"}, Provisioning: many}}, "a")
	if len(got) != MaxConsumerResources || total != MaxConsumerResources+3 {
		t.Errorf("bounded %d of %d", len(got), total)
	}
	// A dependency and a configuration reference with the same status in one
	// environment name it once.
	unknown := []fleet.ProvisioningStatus{{Environment: "production", Phase: "Unknown"}}
	if got, total := resourcesOf([]fleet.OfferingUse{{Consumer: fleet.EntityRef{Key: "a"}, Provisioning: unknown}, {Consumer: fleet.EntityRef{Key: "a"}, Provisioning: unknown}}, "a"); len(got) != 1 || total != 1 {
		t.Errorf("duplicates: %d of %d", len(got), total)
	}
}

func TestEventRemovedLifecycleEvidence(t *testing.T) {
	c := onlyConsumer(t, event(pgProvisioned(t, "production"), EventRemoved, "postgresql"))
	i := slices.IndexFunc(c.Evidence, func(e Evidence) bool { return e.Kind == EvidenceLifecycle })
	if i < 0 || c.Evidence[i].Message != "postgresql declares its lifecycle: deprecated 2026-10-01, sunset 2027-01-01" || c.Evidence[i].Provenance != fleet.ProvenanceDeclared {
		t.Errorf("evidence %+v", c.Evidence)
	}
}

func TestEventRemovedPolicyReferrersUnknown(t *testing.T) {
	snap := policySnap(t, policyProvider("1.0.0"), []string{"orders"}, policyReferrer("orders", true, dep("policy", false)))
	c := onlyConsumer(t, event(snap, EventRemoved, "policy"))
	if c.Effect != EffectUnknown || !slices.Contains(c.Via, fleet.RelationshipPolicyRef) || !strings.Contains(c.Note, "its policy reference will no longer resolve, so its next revision fails validation") {
		t.Errorf("referrer = %+v", c)
	}
	snap = policySnap(t, policyProvider("1.0.0"), nil, policyReferrer("orders", true))
	if res := event(snap, EventUnavailable, "policy"); len(res.Consumers) != 0 {
		t.Errorf("unavailable listed referrers %+v", res.Consumers)
	}
	if c := onlyConsumer(t, event(snap, EventRemoved, "policy")); c.Effect != EffectUnknown || c.CompatibilityVerdict != CompatibilityUnknown {
		t.Errorf("referrer = %+v", c)
	}
}

func regressed(snap *fleet.FleetSnapshot, reg *Regression) *Result {
	return Analyze(context.Background(), nil, nil, nil, nil, snap, Options{Event: &Event{Kind: EventRegressed, Service: "auth-service", Revision: "sha256:bad"}, Regression: reg})
}

func TestEventRegressedNoEvidenceLimitation(t *testing.T) {
	res := regressed(buildChain(t), nil)
	l := limitation(res, LimitationDeliveryEvidenceUnavailable)
	if l == nil || l.Message != "no delivery evidence was read for this answer" || strings.Contains(res.GateReason, "no failure") ||
		!strings.HasPrefix(res.GateReason, "if auth-service regressed at sha256:bad, ") {
		t.Errorf("limitation %+v, reason %q", l, res.GateReason)
	}
}

func TestEventRegressedUnavailableReason(t *testing.T) {
	res := regressed(buildChain(t), &Regression{Unavailable: "forbidden in shop"})
	if l := limitation(res, LimitationDeliveryEvidenceUnavailable); l == nil || l.Message != "forbidden in shop" || strings.Contains(res.GateReason, "no failure") {
		t.Errorf("limitation %+v, reason %q", l, res.GateReason)
	}
}

func TestEventRegressedEvidenceCited(t *testing.T) {
	ev := []RegressionEvidence{
		{Namespace: "shop", Stage: "prod", Digest: "sha256:bad", Kind: RegressionCheck, Gate: "slo", Result: "Fail", Message: "error rate 5%"},
		{Namespace: "shop", Stage: "prod", Digest: "sha256:bad", Kind: RegressionFailed},
		{Namespace: "shop", Stage: "prod", Digest: "sha256:bad", Kind: RegressionRollback, Message: "pages"},
	}
	for range MaxRegressionEvidence {
		ev = append(ev, RegressionEvidence{Namespace: "x", Stage: "y", Kind: RegressionFailed})
	}
	res := regressed(buildChain(t), &Regression{Evidence: ev})
	if len(res.Regression) != MaxRegressionEvidence || limitation(res, LimitationDeliveryEvidenceUnavailable) != nil ||
		!strings.Contains(res.GateReason, "Delivery recorded: shop/prod gate slo failed (error rate 5%); shop/prod lists it as failed; shop/prod rolled it back (pages)") ||
		!strings.HasSuffix(res.GateReason, " and 47 more") {
		t.Errorf("regression %d, reason %q", len(res.Regression), res.GateReason)
	}
	if l := limitation(res, LimitationDeliveryEvidenceTruncated); l == nil || l.Message != "3 more delivery facts about this revision are not shown: the answer keeps the first 50" {
		t.Errorf("truncation limitation %+v", l)
	}
}

func TestEventRegressedNothingRecorded(t *testing.T) {
	res := regressed(buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service")}}), &Regression{})
	if res.Gate != GateClear || !strings.HasSuffix(res.GateReason, ". Delivery recorded no failure of sha256:bad in the Stages you may read; the regression is your hypothesis") {
		t.Errorf("gate %s: %q", res.Gate, res.GateReason)
	}
}

func TestEventTalliesEffectOrder(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("a", authDep), rev("b", dep("auth-service", false))}})
	res := event(snap, EventUnavailable, "auth-service")
	want := []TallyBucket{{OutcomeBreaks, 1}, {OutcomeMayDegrade, 1}, {OutcomeUnknown, 0}}
	if !reflect.DeepEqual(res.ConsumerTallies.ByOutcome, want) || res.ConsumerTallies.ByOwner[0].Incompatible != 1 {
		t.Errorf("tallies %+v", res.ConsumerTallies)
	}
	empty := event(buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service")}}), EventUnavailable, "auth-service")
	if !reflect.DeepEqual(empty.ConsumerTallies.ByOutcome, []TallyBucket{{OutcomeBreaks, 0}, {OutcomeMayDegrade, 0}, {OutcomeUnknown, 0}}) {
		t.Errorf("empty tallies %+v", empty.ConsumerTallies.ByOutcome)
	}
	if outcomeOf(CompatibilityUnknown, false, EffectBreaks) != OutcomeBreaks {
		t.Error("an effect is the outcome")
	}
}

func TestEventProductNilRevisions(t *testing.T) {
	snap := buildChain(t)
	res := regressed(snap, &Regression{Evidence: []RegressionEvidence{{Kind: RegressionFailed}}})
	p := BuildProductOutcome(fleet.NewQuery(snap).ProductMeta(), snap, "auth-service", nil, nil, res, 0, 0, OutcomeBreaks)
	if p.OldRevision != nil || p.NewRevision != nil || p.Event == nil || p.Event.Kind != EventRegressed || len(p.Regression) != 1 ||
		p.Consumers.Matching != 1 || p.Consumers.Items[0].Effect != EffectBreaks {
		t.Errorf("product = %+v", p)
	}
}

func TestEventHighlightsOrder(t *testing.T) {
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("a", dep("auth-service", false)), rev("b", authDep)},
		Observed: []fleet.ObservedEdge{{From: "k8s:pay/Deployment/legacy", To: "auth-service", Count: 1}}})
	res := event(snap, EventUnavailable, "auth-service")
	p := BuildProductOutcome(fleet.NewQuery(snap).ProductMeta(), snap, "auth-service", nil, nil, res, 0, 0, "")
	var hl, items []string
	for _, h := range p.Consumers.Highlights {
		hl = append(hl, h.Service.Label+"="+h.Effect)
	}
	for _, h := range p.Consumers.Items {
		items = append(items, h.Service.Label)
	}
	want := []string{"b=breaks", "a=may-degrade", "k8s:pay/Deployment/legacy=unknown"}
	if !reflect.DeepEqual(hl, want) || !reflect.DeepEqual(items, []string{"b", "a", "k8s:pay/Deployment/legacy"}) {
		t.Errorf("highlights %v, items %v", hl, items)
	}
}

// Hidden policy referrers count toward hidden consumers only when the event
// reads policy references.
func TestEventHiddenReferrersOnlyForRemoved(t *testing.T) {
	snap := buildChain(t)
	snap.Services["auth-service"].HiddenPolicyReferrers = 2
	for kind, want := range map[string]int{EventRemoved: 2, EventUnavailable: 0} {
		p := BuildProductOutcome(fleet.NewQuery(snap).ProductMeta(), snap, "auth-service", nil, nil, event(snap, kind, "auth-service"), 0, 0, "")
		if p.Consumers.Hidden != want {
			t.Errorf("%s hidden %d", kind, p.Consumers.Hidden)
		}
	}
}

// A removed policy provider's referrer keeps a dependent's worse effect, and
// carries its stale target and the provider's declared lifecycle.
func TestEventRemovedReferrerMergesAndCitesLifecycle(t *testing.T) {
	snap := policySnap(t, policyProvider("1.0.0"), nil, policyReferrer("orders", true, dep("policy", true)))
	if c := onlyConsumer(t, event(snap, EventRemoved, "policy")); c.Effect != EffectBreaks || len(c.Via) != 2 {
		t.Errorf("referrer = %+v", c)
	}
	provider := policyProvider("1.0.0")
	provider.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-10-01"}
	snap = buildAt(t, &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: provider, FS: policySchema(openPolicy)},
			Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0"}, policyReferrer("orders", true)},
		Targets: []fleet.RawTarget{target("orders", ago(30*time.Hour))},
	})
	c := onlyConsumer(t, event(snap, EventRemoved, "policy"))
	if c.Confidence != ConfidenceUnknown || !slices.ContainsFunc(c.Evidence, func(e Evidence) bool { return e.Kind == EvidenceLifecycle }) {
		t.Errorf("referrer = %+v", c)
	}
}

// An observed caller Pacto cannot name makes an event answer partial.
func TestEventUnresolvedTraceCallerPartial(t *testing.T) {
	res := event(buildChain(t), EventUnavailable, "auth-service", Options{ObservedEdges: []ObservedEdge{{Consumer: "ghost", Provider: "auth-service"}}})
	if res.Completeness != fleet.CompletenessPartial || limitation(res, ObservedIdentityUnresolved) == nil {
		t.Errorf("completeness %s, limitations %+v", res.Completeness, res.Limitations)
	}
}

// A referrer that also breaks through a transitive required path keeps the
// path that justifies breaks, not its unknown policy hop.
func TestEventRemovedReferrerKeepsTheBreaksPath(t *testing.T) {
	billing := fleet.RawRevision{Bundle: mkBundle(svcContract("billing", "1.0.0", contract.Owner{Team: "billing"}, dep("policy", true)))}
	snap := policySnap(t, policyProvider("1.0.0"), nil, billing, policyReferrer("orders", true, dep("billing", true)))
	res := event(snap, EventRemoved, "policy")
	i := slices.IndexFunc(res.Consumers, func(c AffectedConsumer) bool { return c.Service == "orders" })
	if i < 0 {
		t.Fatalf("consumers = %+v", res.Consumers)
	}
	if c := res.Consumers[i]; c.Effect != EffectBreaks || c.Direct || !reflect.DeepEqual(c.Path, []string{"orders", "billing", "policy"}) ||
		!slices.Contains(c.Via, fleet.RelationshipPolicyRef) {
		t.Errorf("orders = %+v", c)
	}
}
