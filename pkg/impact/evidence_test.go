package impact

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// evidenceNow is the fixed snapshot time of the evidence tests.
var evidenceNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) *time.Time {
	t := evidenceNow.Add(-d)
	return &t
}

var authDep = contract.Dependency{Name: "auth-service", Ref: "ghcr.io/x/auth-service:1.0.0", Required: true, Compatibility: "^1.0.0"}

// evidenceSnap holds auth-service, the given revisions and targets, and the
// observed edges, built at evidenceNow.
func evidenceSnap(t *testing.T, revs []fleet.RawRevision, targets []fleet.RawTarget, observed ...fleet.ObservedEdge) *fleet.FleetSnapshot {
	t.Helper()
	revs = append([]fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}}, revs...)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Targets: targets, Observed: observed}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func gwRev(deps ...contract.Dependency) fleet.RawRevision {
	return fleet.RawRevision{Bundle: mkBundle(svcContract("api-gateway", "1.0.0", contract.Owner{Team: "gateway-team"}, deps...))}
}

func gwTarget(at *time.Time) fleet.RawTarget {
	return fleet.RawTarget{Scope: "prod", Kind: "deployment", Name: "gw", Service: "api-gateway", Compliance: fleet.StatusCompliant, EvidenceAt: at}
}

// analyzeAuth analyses a BREAKING change of auth-service (an interface removed).
func analyzeAuth(snap *fleet.FleetSnapshot, opts Options) *Result {
	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	old.Interfaces = []contract.Interface{{Name: "auth-api", Type: contract.InterfaceTypeGRPC, Ref: "auth.proto"}}
	return Analyze(context.Background(), old, svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"}),
		fstest.MapFS{}, fstest.MapFS{}, snap, opts)
}

func consumerNamed(t *testing.T, res *Result, name string) AffectedConsumer {
	t.Helper()
	for _, c := range res.Consumers {
		if c.Service == name {
			return c
		}
	}
	t.Fatalf("no consumer %s in %+v", name, res.Consumers)
	return AffectedConsumer{}
}

func limitation(res *Result, code string) *fleet.Limitation {
	for i := range res.Limitations {
		if res.Limitations[i].Code == code {
			return &res.Limitations[i]
		}
	}
	return nil
}

func TestOwnerKeyDistinguishesSameDisplay(t *testing.T) {
	team := fleet.RawRevision{Bundle: mkBundle(svcContract("a", "1.0.0", contract.Owner{Team: "shop"}, authDep))}
	dri := fleet.RawRevision{Bundle: mkBundle(svcContract("b", "1.0.0", contract.Owner{DRI: "shop"}, authDep))}
	res := analyzeAuth(evidenceSnap(t, []fleet.RawRevision{team, dri}, nil), Options{})
	if !reflect.DeepEqual(res.Owners, []string{"dri:shop", "team:shop"}) {
		t.Errorf("owners = %v", res.Owners)
	}
	rows := res.ConsumerTallies.ByOwner
	if len(rows) != 2 || rows[0].OwnerKey != "dri:shop" || rows[1].OwnerKey != "team:shop" || rows[0].Owner != "shop" || res.ConsumerTallies.Owners != 2 {
		t.Errorf("byOwner = %+v", rows)
	}
}

func TestChangedOwnerKey(t *testing.T) {
	res := analyzeAuth(evidenceSnap(t, nil, nil), Options{})
	if res.ChangedOwnerKey != "team:platform" {
		t.Errorf("changedOwnerKey = %q", res.ChangedOwnerKey)
	}
}

func TestProductOwnersAreCanonicalKeys(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))})
	res := analyzeAuth(snap, Options{})
	svc := fleet.NewServiceKey("auth-service")
	rev := &fleet.ContractRevision{Key: "auth@1", ServiceKey: svc, Service: "auth-service", Version: "1.0.0"}
	p := BuildProduct(fleet.ProductMeta{}, snap, svc, rev, rev, res, 0, 0)
	want := fleet.EntityRef{Kind: fleet.KindOwner, Key: "team:gateway-team", Label: "gateway-team", Secondary: "Team"}
	if len(p.Owners.Items) != 1 || p.Owners.Items[0] != want {
		t.Errorf("owners = %+v", p.Owners.Items)
	}
	if c := p.Consumers.Items[0]; c.OwnerRef == nil || *c.OwnerRef != want || c.OwnerKey != "team:gateway-team" {
		t.Errorf("consumer owner = %+v %q", c.OwnerRef, c.OwnerKey)
	}
	if p.ChangedOwner == nil || p.ChangedOwner.Key != "team:platform" {
		t.Errorf("changedOwner = %+v", p.ChangedOwner)
	}
}

func TestStaleTargetLowersConfidence(t *testing.T) {
	res := analyzeAuth(evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(31 * time.Hour))}), Options{})
	c := consumerNamed(t, res, "api-gateway")
	if c.Confidence != ConfidenceUnknown || len(c.Evidence) != 1 || c.Evidence[0].Kind != EvidenceTarget || !c.Evidence[0].Stale ||
		c.Evidence[0].Message != "evidence 31h0m0s old, window 24h" || !c.Evidence[0].At.Equal(*ago(31 * time.Hour)) {
		t.Errorf("consumer = %+v", c)
	}
	l := limitation(res, "CONSUMER_EVIDENCE_STALE")
	if l == nil || !strings.HasPrefix(l.Message, "1 consumer has target or traffic evidence older than 24h") || res.Completeness != fleet.CompletenessComplete {
		t.Errorf("limitation = %+v, completeness %s", l, res.Completeness)
	}
}

func TestNilEvidenceAtIsStale(t *testing.T) {
	res := analyzeAuth(evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(nil)}), Options{})
	c := consumerNamed(t, res, "api-gateway")
	if c.Confidence != ConfidenceUnknown || c.Evidence[0].Message != "no evidence time" || !c.Evidence[0].Stale {
		t.Errorf("consumer = %+v", c)
	}
}

func TestFreshTargetKeepsConfidence(t *testing.T) {
	res := analyzeAuth(evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(23 * time.Hour))}), Options{})
	c := consumerNamed(t, res, "api-gateway")
	if c.Confidence != ConfidenceContractual || c.Evidence[0].Stale || limitation(res, "CONSUMER_EVIDENCE_STALE") != nil {
		t.Errorf("consumer = %+v, limitations %+v", c, res.Limitations)
	}
}

func TestStaleObservedCallDoesNotCorroborate(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))},
		fleet.ObservedEdge{From: "api-gateway", To: "auth-service", Count: 3, LastSeen: *ago(48 * time.Hour)})
	c := consumerNamed(t, analyzeAuth(snap, Options{IncludeObserved: true}), "api-gateway")
	obs := c.Evidence[len(c.Evidence)-1]
	if c.Confidence != ConfidenceUnknown || obs.Kind != EvidenceObservedCall || !obs.Stale || obs.Source != "test" ||
		obs.Message != "3 calls seen, last seen more than 24h before asOf" {
		t.Errorf("consumer = %+v", c)
	}
}

func TestStaleObservedOnlyCallerStaysListed(t *testing.T) {
	shadow := fleet.RawRevision{Bundle: mkBundle(svcContract("shadow", "1.0.0", contract.Owner{Team: "ops"}))}
	snap := evidenceSnap(t, []fleet.RawRevision{shadow}, nil,
		fleet.ObservedEdge{From: "shadow", To: "auth-service", Count: 1, LastSeen: *ago(48 * time.Hour)})
	res := analyzeAuth(snap, Options{IncludeObserved: true})
	c := consumerNamed(t, res, "shadow")
	if c.Confidence != ConfidenceUnknown || c.Provenance != fleet.ProvenanceObserved || !reflect.DeepEqual(c.Via, []string{fleet.ProvenanceObserved}) {
		t.Errorf("consumer = %+v", c)
	}
	if res.Completeness != fleet.CompletenessComplete || res.Gate == GateClear {
		t.Errorf("gate %s (%s) on %s", res.Gate, res.GateReason, res.Completeness)
	}
}

func TestStaleTraceFileDoesNotCorroborate(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))})
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{
		{Consumer: "api-gateway", Provider: "auth-service", LastSeen: ago(48 * time.Hour)},
	}})
	c := consumerNamed(t, res, "api-gateway")
	obs := c.Evidence[len(c.Evidence)-1]
	if c.Confidence != ConfidenceUnknown || obs.Source != tracesFileSource || !obs.Stale || obs.Kind != EvidenceObservedCall {
		t.Errorf("consumer = %+v", c)
	}
}

func TestObservedNoWindowCorroborates(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))})
	fresh := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{
		{Consumer: "api-gateway", Provider: "auth-service", LastSeen: ago(time.Hour)},
	}})
	if c := consumerNamed(t, fresh, "api-gateway"); c.Confidence != ConfidenceCorroborated {
		t.Errorf("a fresh trace edge corroborates: %+v", c)
	}
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: "api-gateway", Provider: "auth-service"}}})
	c := consumerNamed(t, res, "api-gateway")
	obs := c.Evidence[len(c.Evidence)-1]
	if c.Confidence != ConfidenceCorroborated || obs.Stale || obs.At != nil || obs.Message != "calls seen, no observation window" {
		t.Errorf("consumer = %+v", c)
	}
}

func TestPolicyReferrerOwnerKeyAndStaleness(t *testing.T) {
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: policyProvider("1.0.0"), FS: policySchema(openPolicy)},
			Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0"}, policyReferrer("orders", true)},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "orders", Service: "orders", Compliance: fleet.StatusCompliant, EvidenceAt: ago(30 * time.Hour)}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	res := analyzePolicy(snap, openPolicy)
	c := onlyConsumer(t, res)
	if c.OwnerKey != "team:orders-team" || !reflect.DeepEqual(res.Owners, []string{"team:orders-team"}) || c.Confidence != ConfidenceUnknown ||
		limitation(res, "CONSUMER_EVIDENCE_STALE") == nil {
		t.Errorf("consumer = %+v, owners %v", c, res.Owners)
	}
}

func TestUnreachableEvidenceKeyedToProvider(t *testing.T) {
	unreachable := func(name string) finding.Finding {
		return finding.Finding{Code: finding.CodeDependencyUnreachable, Subject: finding.SubjectRef{Kind: "dependency", Name: name}, Message: name + " unreachable"}
	}
	tg := gwTarget(ago(time.Hour))
	tg.Findings = []finding.Finding{unreachable("auth-service"), unreachable("billing"),
		{Code: finding.CodeDependencyUnreachable, Subject: finding.SubjectRef{Kind: "service", Name: "auth-service"}}}
	c := consumerNamed(t, analyzeAuth(evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{tg}), Options{}), "api-gateway")
	if len(c.Evidence) != 2 || c.Evidence[1].Kind != EvidenceUnreachable || c.Evidence[1].Message != "auth-service unreachable" ||
		c.Evidence[1].Provenance != fleet.ProvenanceObserved {
		t.Errorf("evidence = %+v", c.Evidence)
	}
}

func TestObservedIdentityUnresolvedPartial(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))})
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: "ghost", Provider: "auth-service"}}})
	if res.Completeness != fleet.CompletenessPartial || limitation(res, ObservedIdentityUnresolved) == nil || res.Gate == GateClear {
		t.Errorf("completeness %s, limitations %+v", res.Completeness, res.Limitations)
	}
}

func TestObservedEvidenceAbsent(t *testing.T) {
	snap := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep)}, []fleet.RawTarget{gwTarget(ago(time.Hour))})
	if l := limitation(analyzeAuth(snap, Options{IncludeObserved: true}), "OBSERVED_EVIDENCE_ABSENT"); l == nil ||
		!strings.Contains(l.Message, "call auth-service without declaring it cannot be found") {
		t.Errorf("limitation = %+v", l)
	}
	if limitation(analyzeAuth(snap, Options{}), "OBSERVED_EVIDENCE_ABSENT") != nil {
		t.Error("declared-only answers do not ask for observed evidence")
	}
	withEdge := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: "api-gateway", Provider: "auth-service"}}})
	if limitation(withEdge, "OBSERVED_EVIDENCE_ABSENT") != nil {
		t.Error("a trace edge is observed evidence")
	}
}

func TestResultJSONKeepsFields(t *testing.T) {
	res := analyzeAuth(buildChain(t), Options{})
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(b, &top)
	for _, k := range []string{"schemaVersion", "snapshotId", "asOf", "service", "oldVersion", "newVersion", "classification",
		"breakingChanges", "consumers", "activeTargets", "owners", "completeness", "gate", "gateReason", "consumerTallies"} {
		if _, ok := top[k]; !ok {
			t.Errorf("result lost %q", k)
		}
	}
	var consumers []map[string]json.RawMessage
	_ = json.Unmarshal(top["consumers"], &consumers)
	for _, k := range []string{"service", "depth", "direct", "path", "owner", "required", "compatibility", "compatibilityVerdict",
		"provenance", "confidence", "status", "targets", "via"} {
		if _, ok := consumers[0][k]; !ok {
			t.Errorf("consumer lost %q", k)
		}
	}
}

func TestProductConsumerFacts(t *testing.T) {
	targets := make([]string, MaxProductConsumerTargets+2)
	for i := range targets {
		targets[i] = "prod/deployment/gw-" + strconv.Itoa(i)
	}
	res := &Result{Consumers: []AffectedConsumer{
		{Service: "web", Path: []string{"web", "api"}, Provenance: fleet.ProvenanceDeclared, Required: true, Targets: targets,
			Via: []string{fleet.RelationshipDependency}, Evidence: []Evidence{{Kind: EvidenceTarget, Message: "m"}}},
		{Service: "10.0.0.7", Path: []string{"10.0.0.7", "api"}, Provenance: fleet.ProvenanceObserved, Uncontracted: true, Via: []string{fleet.ProvenanceObserved}},
	}}
	snap := productSnapshot()
	from := snap.Revisions["api@1"]
	items := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, from, res, 0, 0).Consumers.Items
	u, w := items[0], items[1]
	if w.Provenance != fleet.ProvenanceDeclared || !w.Required || len(w.Targets) != MaxProductConsumerTargets || w.TargetsTotal != MaxProductConsumerTargets+2 ||
		w.Targets[0].Kind != fleet.KindTarget || !reflect.DeepEqual(w.Via, []string{fleet.RelationshipDependency}) || len(w.Evidence) != 1 || w.OwnerRef != nil {
		t.Errorf("web = %+v", w)
	}
	if !u.Uncontracted || u.Service.Kind != fleet.KindService || u.Provenance != fleet.ProvenanceObserved {
		t.Errorf("uncontracted = %+v", u)
	}
}

func TestConsumerEvidenceEdges(t *testing.T) {
	// A target the snapshot does not hold, one a source marks stale and more
	// entries than the bound.
	snap := &fleet.FleetSnapshot{GeneratedAt: evidenceNow,
		Services: map[fleet.ServiceKey]*fleet.ServiceRecord{"web": {Targets: []fleet.TargetKey{"gone", "old"}}},
		Targets:  map[fleet.TargetKey]*fleet.TargetRecord{"old": {EvidenceAt: ago(time.Hour), Stale: true}},
	}
	var observed []resolvedObservedEdge
	for i := range MaxConsumerEvidence {
		observed = append(observed, resolvedObservedEdge{consumer: "web", provider: "api", lastSeen: ago(time.Duration(i) * time.Minute)})
	}
	ev, stale := consumerEvidence(snap, "web", "api", observed)
	last := ev[len(ev)-1]
	if !stale || len(ev) != MaxConsumerEvidence || !strings.HasSuffix(last.Message, "; 2 older entries not shown") {
		t.Fatalf("evidence = %+v", ev)
	}
	ev, _ = consumerEvidence(snap, "web", "api", nil)
	if ev[0].Message != "evidence 1h0m0s old, window 24h; the source marks it stale" || !ev[0].Stale ||
		ev[1].Message != "the target is not in the snapshot" || !ev[1].Stale {
		t.Errorf("evidence = %+v", ev)
	}
}

func TestObservedSource(t *testing.T) {
	if got := observedSource(fleet.Relationship{ObservedSources: []fleet.ObservedSourceStat{{Source: "otel"}, {Source: "mira"}}}); got != "otel, mira" {
		t.Errorf("observedSource = %q", got)
	}
}
