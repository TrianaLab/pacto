package fleet

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

var availNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// availSnap is a hand-built snapshot for availability: services, Kubernetes
// targets and declared dependency edges, as Build would leave them.
type availSnap struct{ *FleetSnapshot }

func newAvailSnap() availSnap {
	return availSnap{&FleetSnapshot{GeneratedAt: availNow, Services: map[ServiceKey]*ServiceRecord{}, Targets: map[TargetKey]*TargetRecord{}, reverseDeps: map[ServiceKey][]ServiceKey{}}}
}

func (a availSnap) svc(name, offering string) *ServiceRecord {
	if s := a.Services[ServiceKey(name)]; s != nil {
		return s
	}
	s := &ServiceRecord{Key: ServiceKey(name), Name: name, Offering: offering}
	if offering != "" {
		s.OfferingProvenance = ProvenanceDeclared
	}
	a.Services[s.Key] = s
	return s
}

// target adds a fresh-by-age target of service svc in namespace scope.
func (a availSnap) target(svc, name, scope, env string, age time.Duration, fs ...finding.Finding) *TargetRecord {
	at := availNow.Add(-age)
	k := NewTargetKey(scope, SourceKindKubernetes, name)
	t := &TargetRecord{Key: k, Name: name, Kind: SourceKindKubernetes, Scope: scope, Service: svc, ServiceKey: ServiceKey(svc), Environment: env, EvidenceAt: &at, Findings: fs, Compliance: StatusCompliant}
	a.Targets[k] = t
	s := a.svc(svc, "")
	s.Targets = append(s.Targets, k)
	if s.Offering != "" {
		t.reportedOfferings = []string{s.Offering} // Restrict reads the offering again
	}
	return t
}

// dep declares consumer's dependency named name, resolved to provider.
func (a availSnap) dep(consumer, provider, name string) {
	a.svc(consumer, "")
	a.Relationships = append(a.Relationships, Relationship{FromService: ServiceKey(consumer), To: name, ToService: ServiceKey(provider), Type: RelationshipDependency, Provenance: ProvenanceDeclared, Resolved: true})
	a.reverseDeps[ServiceKey(provider)] = append(a.reverseDeps[ServiceKey(provider)], ServiceKey(consumer))
}

func (a availSnap) query() *Query { return NewQuery(a.FleetSnapshot) }

func unreachable(dep string) finding.Finding {
	return finding.Finding{Code: finding.CodeDependencyUnreachable, Subject: finding.SubjectRef{Kind: "dependency", Name: dep}, Message: "cannot reach " + dep}
}

func absent() finding.Finding {
	return finding.Finding{Code: finding.CodeInterfaceAbsent, Message: `interface "sql" is not being served`}
}

// envOf is q's availability of provider in env, failing when it is not listed.
func envOf(t *testing.T, a Availability, env string) EnvironmentAvailability {
	t.Helper()
	for _, e := range a.ByEnvironment {
		if e.Environment == env {
			return e
		}
	}
	t.Fatalf("environment %q not in %+v", env, a.ByEnvironment)
	return EnvironmentAvailability{}
}

func providerAvail(a availSnap, name string) Availability {
	q := a.query()
	return q.providerAvailability(q.snap.Services[ServiceKey(name)])
}

func wantState(t *testing.T, e EnvironmentAvailability, state AvailabilityState, prov string) {
	t.Helper()
	if e.State != state || e.Provenance != prov {
		t.Errorf("state = %s (%s), want %s (%s); reason %q", e.State, e.Provenance, state, prov, e.Reason)
	}
}

func TestAvailabilityAvailableFreshTarget(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	av := providerAvail(a, "pg")
	e := envOf(t, av, "prod")
	wantState(t, e, AvailabilityAvailable, ProvenanceObserved)
	if av.State != AvailabilityAvailable || e.Reporting != 1 || e.Targets != 1 || e.Reason != "1 of 1 targets reporting, evidence 1h0m0s old" || !av.AsOf.Equal(availNow) || e.EvidenceAt == nil {
		t.Errorf("availability = %+v", av)
	}
}

func TestAvailabilityIgnoresCompliance(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	tg := a.target("pg", "pg-0", "db", "prod", time.Hour, finding.Finding{Code: finding.CodePolicyViolation, Message: "policy"})
	tg.Compliance = StatusNonCompliant
	wantState(t, envOf(t, providerAvail(a, "pg"), "prod"), AvailabilityAvailable, ProvenanceObserved)
}

func TestAvailabilityInterfaceAbsentDegraded(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour, absent())
	a.target("pg", "pg-1", "db", "prod", time.Hour)
	e := envOf(t, providerAvail(a, "pg"), "prod")
	wantState(t, e, AvailabilityDegraded, ProvenanceObserved)
	if e.Reason != `1 of 2 targets report interface "sql" is not being served` {
		t.Errorf("reason = %q", e.Reason)
	}
}

func TestAvailabilityInterfaceAbsentAllUnavailable(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour, absent())
	a.target("pg", "pg-1", "db", "prod", time.Hour, absent())
	av := providerAvail(a, "pg")
	e := envOf(t, av, "prod")
	wantState(t, e, AvailabilityUnavailable, ProvenanceObserved)
	if e.Reason != `interface "sql" is not being served` || av.State != AvailabilityUnavailable {
		t.Errorf("availability = %+v", av)
	}
}

// consumersOf adds n consumers of pg with one target each in env; the first
// reports reports of them unreachable.
func consumersOf(a availSnap, env string, n, reports int) {
	for i := range n {
		name := fmt.Sprintf("c%d", i)
		a.dep(name, "pg", "db")
		var fs []finding.Finding
		if i < reports {
			fs = append(fs, unreachable("db"))
		}
		a.target(name, name+"-0", "apps", env, time.Hour, fs...)
	}
}

func TestAvailabilityMixedReportsDegraded(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	consumersOf(a, "staging", 3, 1)
	e := envOf(t, providerAvail(a, "pg"), "staging")
	wantState(t, e, AvailabilityDegraded, ProvenanceInferred)
	if e.Reason != "1 of 3 consumers cannot reach it" || len(e.Unreachable) != 1 || e.Unreachable[0].Kind != KindTarget {
		t.Errorf("env = %+v", e)
	}
}

// A provider outside the observed clusters is not seen: one consumer that
// cannot reach it, among consumers that do, is never unavailable.
func TestAvailabilitySingleReporterAmongReachingConsumersNotUnavailable(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	consumersOf(a, "prod", 2, 1)
	if e := envOf(t, providerAvail(a, "pg"), "prod"); e.State == AvailabilityUnavailable {
		t.Errorf("one reporter among reaching consumers = %+v", e)
	}
}

func TestAvailabilityUnavailableInferred(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	consumersOf(a, "staging", 1, 1)
	e := envOf(t, providerAvail(a, "pg"), "staging")
	wantState(t, e, AvailabilityUnavailable, ProvenanceInferred)
	if e.Reason != "all 1 consumers cannot reach it and no target reports it" {
		t.Errorf("reason = %q", e.Reason)
	}
}

func TestAvailabilityDegradedUnreachableReport(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	consumersOf(a, "prod", 3, 2)
	e := envOf(t, providerAvail(a, "pg"), "prod")
	wantState(t, e, AvailabilityDegraded, ProvenanceInferred)
	if e.Reason != "1 targets reporting; apps/kubernetes/c0-0 and 1 more cannot reach it" {
		t.Errorf("reason = %q", e.Reason)
	}
}

func TestAvailabilityUnreachableOtherDependencyIgnored(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.dep("c", "pg", "db")
	a.target("c", "c-0", "apps", "prod", time.Hour, unreachable("cache"), finding.Finding{Code: finding.CodeDependencyUnreachable, Subject: finding.SubjectRef{Kind: "interface", Name: "db"}})
	wantState(t, envOf(t, providerAvail(a, "pg"), "prod"), AvailabilityAvailable, ProvenanceObserved)
}

func TestAvailabilityStaleReportIgnored(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.dep("c", "pg", "db")
	a.target("c", "c-0", "apps", "prod", 30*time.Hour, unreachable("db"))
	a.dep("pg", "pg", "self") // a provider is never its own consumer
	wantState(t, envOf(t, providerAvail(a, "pg"), "prod"), AvailabilityAvailable, ProvenanceObserved)
}

func TestAvailabilityStaleTargetsUnknown(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", 31*time.Hour)
	a.target("pg", "pg-1", "db", "staging", time.Hour).Stale = true
	a.target("pg", "pg-2", "db", "qa", time.Hour).Quarantined = true
	av := providerAvail(a, "pg")
	for _, env := range []string{"prod", "staging", "qa"} {
		e := envOf(t, av, env)
		wantState(t, e, AvailabilityUnknown, ProvenanceObserved)
		if e.Reason != availabilityStaleReason || e.Reporting != 0 || e.Targets != 1 {
			t.Errorf("%s = %+v", env, e)
		}
	}
	if av.State != AvailabilityUnknown {
		t.Errorf("roll-up = %s", av.State)
	}
}

func TestAvailabilityNilEvidenceUnknown(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", 0).EvidenceAt = nil
	e := envOf(t, providerAvail(a, "pg"), "prod")
	wantState(t, e, AvailabilityUnknown, ProvenanceObserved)
	if e.EvidenceAt != nil {
		t.Errorf("evidenceAt = %v", e.EvidenceAt)
	}
}

func TestAvailabilityNoTargetUnknown(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.dep("c", "pg", "db")
	a.target("c", "c-0", "apps", "prod", time.Hour) // reaches it: names no environment
	av := providerAvail(a, "pg")
	if len(av.ByEnvironment) != 1 || av.Total != 1 || av.State != AvailabilityUnknown {
		t.Fatalf("availability = %+v", av)
	}
	wantState(t, av.ByEnvironment[0], AvailabilityUnknown, "unknown")
	if av.ByEnvironment[0].Reason != availabilityNoneReason {
		t.Errorf("reason = %q", av.ByEnvironment[0].Reason)
	}
}

func TestAvailabilityHiddenTargetsUnknown(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.target("pg", "pg-1", "secret", "prod", time.Hour)
	r := a.query().Restrict(func(ns string) bool { return ns != "secret" })
	av := r.providerAvailability(r.snap.Services["pg"])
	e := envOf(t, av, "prod")
	wantState(t, e, AvailabilityUnknown, "unknown")
	if e.Reason != targetsIncompleteReason || e.Targets != 1 {
		t.Errorf("env = %+v", e)
	}
}

// TestAvailabilityRestrictedIsUnknown: a caller who may not read the cluster
// scope sees a declared offering provider with no target it may read:
// unknown, never unavailable.
func TestAvailabilityRestrictedIsUnknown(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	tg := a.target("pg", "pg-0", "", "prod", time.Hour, absent())
	tg.Kind, tg.Scope = SourceKindTargetState, ""
	r := a.query().Restrict(func(ns string) bool { return ns != "" })
	s := r.snap.Services["pg"]
	if s == nil {
		t.Fatal("the declared provider is hidden")
	}
	av := r.providerAvailability(s)
	if av.State != AvailabilityUnknown || av.ByEnvironment[0].Reason != targetsIncompleteReason {
		t.Errorf("availability = %+v", av)
	}
}

func TestAvailabilityPartialSourceUnknown(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.Sources = []SourceState{{ID: "oci", Kind: "oci", Status: SourcePartial}, {ID: "cluster", Kind: SourceKindKubernetes, Status: SourcePartial}}
	e := envOf(t, providerAvail(a, "pg"), "prod")
	wantState(t, e, AvailabilityUnknown, "unknown")
	if e.Reason != "source cluster is partial" {
		t.Errorf("reason = %q", e.Reason)
	}
}

func TestFirstIncompleteTargetSource(t *testing.T) {
	s := &FleetSnapshot{Sources: []SourceState{
		{ID: "oci", Kind: "oci", Status: SourceUnavailable},
		{ID: "k", Kind: SourceKindKubernetes, Status: SourceAvailable},
		{ID: "ev", Kind: SourceKindEvidenceHTTP, Status: SourceStale},
		{ID: "ts", Kind: SourceKindTargetState, Status: SourceUnavailable},
	}}
	if id, status := firstIncompleteTargetSource(s); id != "ev" || status != "stale" || targetPlacementKnown(s) {
		t.Errorf("first = %q %q", id, status)
	}
	s.Sources = s.Sources[:2]
	if id, _ := firstIncompleteTargetSource(s); id != "" || !targetPlacementKnown(s) {
		t.Errorf("complete sources: %q", id)
	}
}

func TestAvailabilityUnlabelledGroup(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "", time.Hour)
	a.target("pg", "pg-1", "db", "prod", time.Hour, absent())
	a.target("pg", "pg-2", "db", "env-10", time.Hour)
	a.target("pg", "pg-3", "db", "env-2", time.Hour)
	av := providerAvail(a, "pg")
	var envs []string
	for _, e := range av.ByEnvironment {
		envs = append(envs, e.Environment)
	}
	if !slices.Equal(envs, []string{"env-2", "env-10", "prod", ""}) || av.State != AvailabilityDegraded {
		t.Errorf("envs = %q, state %s", envs, av.State)
	}
}

func TestAvailabilityRollUp(t *testing.T) {
	env := func(states ...AvailabilityState) []EnvironmentAvailability {
		var out []EnvironmentAvailability
		for _, s := range states {
			out = append(out, EnvironmentAvailability{State: s})
		}
		return out
	}
	cases := []struct {
		in   []EnvironmentAvailability
		want AvailabilityState
	}{
		{nil, AvailabilityUnknown},
		{env(AvailabilityAvailable, AvailabilityAvailable), AvailabilityAvailable},
		{env(AvailabilityUnavailable, AvailabilityUnavailable), AvailabilityUnavailable},
		{env(AvailabilityUnknown), AvailabilityUnknown},
		{env(AvailabilityAvailable, AvailabilityUnknown), AvailabilityDegraded},
		{env(AvailabilityAvailable, AvailabilityUnavailable), AvailabilityDegraded},
		{env(AvailabilityDegraded), AvailabilityDegraded},
	}
	for i, c := range cases {
		if got := rollUp(c.in); got != c.want {
			t.Errorf("%d: rollUp = %s, want %s", i, got, c.want)
		}
	}
}

func TestAvailabilityEvidenceBounded(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	consumersOf(a, "prod", MaxAvailabilityEvidence+3, MaxAvailabilityEvidence+3)
	for i := range MaxDetailPreview + 2 {
		a.target("pg", fmt.Sprintf("pg-%d", i), "db", fmt.Sprintf("zz%02d", i), time.Hour)
	}
	av := providerAvail(a, "pg")
	if e := envOf(t, av, "prod"); len(e.Unreachable) != MaxAvailabilityEvidence {
		t.Errorf("unreachable = %d", len(e.Unreachable))
	}
	if len(av.ByEnvironment) != MaxDetailPreview || av.Total != MaxDetailPreview+3 {
		t.Errorf("environments = %d of %d", len(av.ByEnvironment), av.Total)
	}
}

// twoProviders is an offering postgresql with providers pg and pg-ha.
func twoProviders() availSnap {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.svc("pg-ha", "postgresql")
	return a
}

func offeringAvail(a availSnap) Availability {
	q := a.query()
	return q.offeringAvailability("postgresql", []*ServiceRecord{q.snap.Services["pg"], q.snap.Services["pg-ha"]})
}

func TestOfferingAvailabilityBestProvider(t *testing.T) {
	a := twoProviders()
	a.target("pg", "pg-0", "db", "prod", 2*time.Hour)
	a.target("pg-ha", "ha-0", "db", "prod", time.Hour, absent())
	a.target("pg-ha", "ha-1", "db", "staging", time.Hour)
	a.target("pg", "pg-1", "db", "staging", 3*time.Hour)
	av := offeringAvail(a)
	prod := envOf(t, av, "prod")
	if prod.State != AvailabilityAvailable || len(prod.Providers) != 1 || prod.Providers[0].Key != "pg" {
		t.Errorf("prod = %+v", prod)
	}
	staging := envOf(t, av, "staging")
	if staging.State != AvailabilityAvailable || len(staging.Providers) != 2 || staging.Reporting != 2 || staging.Targets != 2 || !staging.EvidenceAt.Equal(availNow.Add(-time.Hour)) {
		t.Errorf("staging = %+v", staging)
	}
	if av.State != AvailabilityAvailable {
		t.Errorf("roll-up = %s", av.State)
	}
}

func TestOfferingAvailabilityUnknownOutranksUnavailable(t *testing.T) {
	a := twoProviders()
	a.target("pg", "pg-0", "db", "prod", time.Hour, absent())
	a.target("pg-ha", "ha-0", "db", "prod", 40*time.Hour)
	av := offeringAvail(a)
	if e := envOf(t, av, "prod"); e.State != AvailabilityUnknown || e.Providers[0].Key != "pg-ha" || av.State == AvailabilityUnavailable {
		t.Errorf("availability = %+v", av)
	}
}

// restrictedOffering is postgresql with a visible provider pg unavailable in
// prod and a provider pg-ha only in the hidden namespace secret.
func restrictedOffering() *Query {
	a := twoProviders()
	a.target("pg", "pg-0", "db", "prod", time.Hour, absent())
	a.target("pg-ha", "ha-0", "secret", "prod", time.Hour)
	a.svc("other", "hidden-only")
	a.target("other", "o-0", "secret", "prod", time.Hour)
	return a.query().Restrict(func(ns string) bool { return ns != "secret" })
}

func TestOfferingAvailabilityHiddenProviderNeverUnavailable(t *testing.T) {
	o := offeringData(t, restrictedOffering(), "postgresql")
	e := envOf(t, o.Availability, "prod")
	wantState(t, e, AvailabilityUnknown, "unknown")
	if e.Reason != "1 provider you may not see" || !e.Partial || o.HiddenProviders != 1 || o.Providers.Total != 1 {
		t.Errorf("offering = %+v", o)
	}
	if blob, _ := json.Marshal(o); strings.Contains(string(blob), "pg-ha") || strings.Contains(string(blob), "ha-0") {
		t.Errorf("the hidden provider leaks: %s", blob)
	}
	// An available state keeps its state and says what it may not count.
	if got := floorHidden(EnvironmentAvailability{State: AvailabilityAvailable, Reason: "ok"}, 2); got.State != AvailabilityAvailable || got.Reason != "ok; 2 providers you may not see" || !got.Partial {
		t.Errorf("floor = %+v", got)
	}
}

func TestOfferingHiddenProvidersOnlyHiddenAbsent(t *testing.T) {
	r := restrictedOffering()
	if _, err := r.EntityDetail(KindOffering, "hidden-only"); err == nil {
		t.Error("an offering only hidden services provide is revealed")
	}
	if _, ok := r.snap.hiddenProviders["hidden-only"]; ok {
		t.Errorf("hidden providers = %v", r.snap.hiddenProviders)
	}
}

func TestAvailabilityHiddenConsumerReportPartial(t *testing.T) {
	a := newAvailSnap()
	a.svc("pg", "postgresql")
	a.target("pg", "pg-0", "db", "prod", time.Hour)
	a.dep("secret-app", "pg", "db")
	a.target("secret-app", "s-0", "secret", "prod", time.Hour, unreachable("db"))
	r := a.query().Restrict(func(ns string) bool { return ns != "secret" })
	o := offeringData(t, r, "postgresql")
	e := envOf(t, o.Providers.Items[0].Availability, "prod")
	wantState(t, e, AvailabilityAvailable, ProvenanceObserved)
	if !e.Partial || !strings.HasSuffix(e.Reason, availabilityHiddenSuffix) || len(e.Unreachable) != 0 || o.HiddenConsumers != 1 {
		t.Errorf("env = %+v, hidden consumers %d", e, o.HiddenConsumers)
	}
	if blob, _ := json.Marshal(o); strings.Contains(string(blob), "secret-app") {
		t.Errorf("the hidden consumer leaks: %s", blob)
	}
	// A visible consumer that lost a target to the view makes it partial too.
	b := newAvailSnap()
	b.svc("pg", "postgresql")
	b.dep("c", "pg", "db")
	b.target("c", "c-0", "apps", "prod", time.Hour, unreachable("db"))
	b.target("c", "c-1", "secret", "prod", time.Hour)
	r = b.query().Restrict(func(ns string) bool { return ns != "secret" })
	if e := envOf(t, r.providerAvailability(r.snap.Services["pg"]), "prod"); !e.Partial || e.State != AvailabilityUnavailable {
		t.Errorf("consumer with hidden targets: %+v", e)
	}
}

// TestOfferingDetailJSONKeepsFields: every key an offering detail carried
// before availability is still there.
func TestOfferingDetailJSONKeepsFields(t *testing.T) {
	o := offeringData(t, offeringDetailFleet(t), "shared-config")
	blob, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(blob, &top)
	for _, k := range []string{"providers", "consumers", "hiddenConsumers", "availability"} {
		if _, ok := top[k]; !ok {
			t.Errorf("offering detail lost %q: %s", k, blob)
		}
	}
	var prov struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(top["providers"], &prov)
	for _, k := range []string{"service", "versions", "interfaces", "configurations", "policies", "offeringProvenance", "availability"} {
		if _, ok := prov.Items[0][k]; !ok {
			t.Errorf("provider lost %q", k)
		}
	}
	if o.Availability.State != AvailabilityUnknown || o.Providers.Items[0].Availability.State != AvailabilityUnknown {
		t.Errorf("no fresh target: offering %s, provider %s", o.Availability.State, o.Providers.Items[0].Availability.State)
	}
}
