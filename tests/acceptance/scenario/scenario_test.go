package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

const domain = "pacto-registry.pacto-system.svc.cluster.local:5000/demo"

// The point of this package is that the fixture is declared ONCE. That only
// holds if the declared identity and the literal bundle content cannot drift
// apart, so the whole bundle set is materialized and read back with the real
// contract parser: if a version is bumped in a heredoc-turned-literal and not in
// the Revision beside it, the gate would go looking for a revision nobody
// published, which is the failure this package exists to make impossible.
func TestMaterialize_TheBundlesAreTheDeclaredIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := OperationalGraph.Materialize(dir, domain); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	for _, svc := range OperationalGraph.Services {
		for _, rev := range svc.Revisions {
			c := parseBundle(t, dir, rev.Dir)
			if c.Service.Name != svc.Name {
				t.Errorf("%s: bundle declares service %q, scenario declares %q", rev.Dir, c.Service.Name, svc.Name)
			}
			if c.Service.Version != rev.Version {
				t.Errorf("%s: bundle declares version %q, scenario declares %q", rev.Dir, c.Service.Version, rev.Version)
			}
			for _, ref := range rev.Files {
				if strings.Contains(ref, "{{") && !strings.Contains(ref, "{{.Domain}}") {
					t.Errorf("%s: a template action other than .Domain is not rendered by Materialize", rev.Dir)
				}
			}
		}
	}
}

// A declared dependency must name the repository the scenario says the provider
// publishes to, in the domain the harness brought up. Hand-writing that ref in
// the bundle is how the fixture used to point at a registry path nothing was
// ever pushed to.
func TestMaterialize_DeclaredDependenciesResolveToPublishedRepos(t *testing.T) {
	dir := t.TempDir()
	if err := OperationalGraph.Materialize(dir, domain); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	seen := 0
	for _, svc := range OperationalGraph.Services {
		for _, rev := range svc.Revisions {
			for _, dep := range parseBundle(t, dir, rev.Dir).Dependencies {
				seen++
				provider, ok := OperationalGraph.Service(dep.Name)
				if !ok {
					t.Fatalf("%s depends on %q, which the scenario does not publish", rev.Dir, dep.Name)
				}
				if want := "oci://" + domain + "/" + provider.Repo; dep.Ref != want {
					t.Errorf("%s depends on %q, want %q", rev.Dir, dep.Ref, want)
				}
				if !relationshipDeclared(svc.Name, dep.Name) {
					t.Errorf("%s -> %s is a contract dependency the scenario never declares as a relationship, so the gate would not prove it",
						svc.Name, dep.Name)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no bundle declares a dependency; the fixture no longer has a declared edge to reconcile")
	}
}

func TestMaterialize_RejectsAMissingDirOrDomain(t *testing.T) {
	if err := OperationalGraph.Materialize("", domain); err == nil {
		t.Error("materializing with no output directory should fail")
	}
	if err := OperationalGraph.Materialize(t.TempDir(), ""); err == nil {
		t.Error("materializing with no domain should fail: every dependency ref would name an empty registry")
	}
}

func TestMaterialize_ReportsABrokenTemplate(t *testing.T) {
	broken := Scenario{Name: "broken", Services: []Service{{
		Name: "s", Revisions: []Revision{{Dir: "s", Files: map[string]string{"pacto.yaml": "{{.Nope}}"}}},
	}}}
	err := broken.Materialize(t.TempDir(), domain)
	if err == nil || !strings.Contains(err.Error(), "s/pacto.yaml") {
		t.Errorf("want an error naming the offending file, got %v", err)
	}
}

// The observed half of the fixture is derived from the same relationships the
// gate later requires the backend to reconcile. A hand-written OTLP blob is how
// the two halves used to disagree: an export naming a service the contract never
// depends on reconciles to "observed only", and the fixture would be proving the
// opposite of what it claims.
func TestTraceExport_CarriesExactlyTheObservedRelationships(t *testing.T) {
	raw, err := OperationalGraph.TraceExport("orders-traces")
	if err != nil {
		t.Fatalf("TraceExport: %v", err)
	}
	got := map[string]bool{}
	for _, rs := range decodeExport(t, raw).ResourceSpans {
		caller := attrValue(t, rs.Resource.Attributes, "service.name")
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				if span.Kind != spanKindClient {
					t.Errorf("span %s -> ? has kind %d; only a CLIENT span makes peer.service the callee",
						caller, span.Kind)
				}
				got[caller+" -> "+attrValue(t, span.Attributes, "peer.service")] = true
			}
		}
	}
	want := map[string]bool{}
	for _, rel := range OperationalGraph.Relationships {
		if rel.ObservedBy == "orders-traces" {
			want[rel.From+" -> "+rel.To] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("the scenario declares no observed relationship, so the export proves nothing")
	}
	for edge := range want {
		if !got[edge] {
			t.Errorf("the export is missing the observed edge %s", edge)
		}
	}
	for edge := range got {
		if !want[edge] {
			t.Errorf("the export carries %s, which the scenario does not declare as observed", edge)
		}
	}
}

func TestTraceExport_RejectsAnUnknownSource(t *testing.T) {
	if _, err := OperationalGraph.TraceExport("no-such-source"); err == nil {
		t.Error("exporting an unobserved source should fail rather than mount an empty export")
	}
}

func TestTraceExport_GroupsEveryCallOfOneCaller(t *testing.T) {
	s := Scenario{Name: "multi", Relationships: []Relationship{
		{From: "a", To: "b", ObservedBy: "src"},
		{From: "a", To: "c", ObservedBy: "src"},
		{From: "d", To: "b", ObservedBy: "src"},
		{From: "d", To: "e", ObservedBy: "other"},
	}}
	raw, err := s.TraceExport("src")
	if err != nil {
		t.Fatalf("TraceExport: %v", err)
	}
	e := decodeExport(t, raw)
	if len(e.ResourceSpans) != 2 {
		t.Fatalf("got %d resourceSpans, want one per distinct caller (a, d)", len(e.ResourceSpans))
	}
	if n := len(e.ResourceSpans[0].ScopeSpans[0].Spans); n != 2 {
		t.Errorf("caller a exported %d spans, want 2", n)
	}
	if n := len(e.ResourceSpans[1].ScopeSpans[0].Spans); n != 1 {
		t.Errorf("caller d exported %d spans, want 1; the edge observed by another source belongs in that source's export", n)
	}
}

// The gate reports "N of M facts outstanding". M is derived from the scenario so
// it cannot quietly keep claiming a number the fixture stopped justifying — and
// 14 is the count section 8 requires, so this is also the pin on that gate.
func TestFactCount_IsTheFourteenTheGateOwes(t *testing.T) {
	if n := OperationalGraph.FactCount(); n != 14 {
		t.Errorf("FactCount() = %d, want 14 (3 sources + 1 service list + 3 revisions + 2 targets + 3 for the edge + 1 evidence target + 1 coherence)", n)
	}
}

func TestFactCount_TracksTheScenario(t *testing.T) {
	base := OperationalGraph.FactCount()
	for _, tc := range []struct {
		name  string
		apply func(s *Scenario)
		delta int
	}{
		{"an added observation source is an added source fact", func(s *Scenario) {
			s.Sources = append(s.Sources, Source{ID: "extra", Kind: SourceObservation})
		}, 1},
		{"the Evidence Server is proved by its target, not as a source fact", func(s *Scenario) {
			s.Sources = append(s.Sources, Source{ID: "extra", Kind: SourceEvidence})
		}, 0},
		{"an added relationship is declared, observed and reconciled", func(s *Scenario) {
			s.Relationships = append(s.Relationships, Relationship{From: "orders", To: "payments"})
		}, 3},
		{"an added evidence target is one fact", func(s *Scenario) {
			s.Evidence = append(s.Evidence, Evidence{Service: "orders", Via: "evidence-http"})
		}, 1},
		{"an evidence-only service carries no revision or target facts", func(s *Scenario) {
			s.Services = append(s.Services, Service{Name: "x", EvidenceOnly: true,
				Workload:  &Workload{Name: "x"},
				Revisions: []Revision{{Version: "1.0.0", Deployed: true}}})
		}, 0},
		{"a running workload owes a revision fact and a target fact", func(s *Scenario) {
			s.Services = append(s.Services, Service{Name: "x", Workload: &Workload{Name: "x"},
				Revisions: []Revision{{Version: "1.0.0", Deployed: true}}})
		}, 2},
		{"a service that runs nothing owes only its revision fact", func(s *Scenario) {
			s.Services = append(s.Services, Service{Name: "x",
				Revisions: []Revision{{Version: "1.0.0"}}})
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := OperationalGraph
			s.Services = append([]Service(nil), s.Services...)
			s.Sources = append([]Source(nil), s.Sources...)
			s.Relationships = append([]Relationship(nil), s.Relationships...)
			s.Evidence = append([]Evidence(nil), s.Evidence...)
			tc.apply(&s)
			if got := s.FactCount() - base; got != tc.delta {
				t.Errorf("fact count moved by %d, want %d", got, tc.delta)
			}
		})
	}
}

// Every name the gate and the browser suite reach for has to be in the scenario,
// or the surfaces are declaring the fixture again between themselves.
func TestScenario_IsSelfConsistent(t *testing.T) {
	s := OperationalGraph
	for _, kind := range []SourceKind{SourceRegistry, SourceCache, SourceObservation, SourceEvidence} {
		if s.SourceID(kind) == "" {
			t.Errorf("no data source plays the %q part", kind)
		}
	}
	for _, rel := range s.Relationships {
		mustExist(t, s, rel.From)
		mustExist(t, s, rel.To)
		if rel.ObservedBy != "" && !sourceExists(s, rel.ObservedBy) {
			t.Errorf("%s -> %s is observed by %q, which is not a declared data source", rel.From, rel.To, rel.ObservedBy)
		}
	}
	for _, ev := range s.Evidence {
		mustExist(t, s, ev.Service)
		if !sourceExists(s, ev.Via) {
			t.Errorf("evidence for %s arrives via %q, which is not a declared data source", ev.Service, ev.Via)
		}
	}
	// The browser journeys drive a change analysis, which needs a provider with
	// one deployed revision and one published-but-undeployed alternative.
	provider := mustExist(t, s, s.Journey.Provider)
	if _, err := provider.DeployedRevision(); err != nil {
		t.Errorf("journey provider %s has no operational target to open: %v", provider.Name, err)
	}
	if _, ok := provider.PublishedOnlyRevision(); !ok {
		t.Errorf("journey provider %s has no undeployed revision, so there is no change to analyse", provider.Name)
	}
	consumer := mustExist(t, s, s.Journey.Consumer)
	if !relationshipDeclared(consumer.Name, provider.Name) {
		t.Errorf("the journey consumer %s does not declare the provider %s", consumer.Name, provider.Name)
	}
	external := mustExist(t, s, s.Journey.External)
	if !external.EvidenceOnly {
		t.Errorf("the journey's external service %s is published in the fixture's own domain, so it proves nothing about evidence ingest", external.Name)
	}
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			if rev.Dir == "" || len(rev.Files) == 0 {
				t.Errorf("%s %s has no bundle to publish", svc.Name, rev.Version)
			}
		}
	}
}

func TestSourceID_AndServiceLookupsMiss(t *testing.T) {
	if got := OperationalGraph.SourceID("no-such-kind"); got != "" {
		t.Errorf("SourceID of an unknown kind = %q, want empty", got)
	}
	if _, ok := OperationalGraph.Service("no-such-service"); ok {
		t.Error("Service found a service the scenario does not declare")
	}
	empty := Service{Revisions: []Revision{{Version: "1.0.0", Deployed: true}}}
	if _, ok := empty.PublishedOnlyRevision(); ok {
		t.Error("a fully deployed service reported an undeployed revision")
	}
}

// Deployed is only meaningful against a Workload, and a workload runs EXACTLY
// one revision. Both halves fail here, in the one accessor every surface reads,
// rather than in whichever of them happens to consume the fixture first.
func TestDeployedRevision_IsExactlyOneOrAnError(t *testing.T) {
	runs := &Workload{Name: "x"}
	for _, tc := range []struct {
		name string
		svc  Service
		want string
	}{{
		name: "one deployed revision is the one that runs",
		svc: Service{Name: "x", Workload: runs, Revisions: []Revision{
			{Version: "1.0.0", Deployed: true}, {Version: "1.1.0"}}},
	}, {
		name: "the flag is read, not the position",
		svc: Service{Name: "x", Workload: runs, Revisions: []Revision{
			{Version: "0.9.0"}, {Version: "1.0.0", Deployed: true}}},
	}, {
		name: "a workload that deploys nothing",
		svc:  Service{Name: "x", Workload: runs, Revisions: []Revision{{Version: "1.0.0"}}},
		want: "deploys no revision",
	}, {
		name: "two deployed revisions name both, and choose neither",
		svc: Service{Name: "x", Workload: runs, Revisions: []Revision{
			{Version: "1.0.0", Deployed: true}, {Version: "1.1.0", Deployed: true}}},
		want: "1.0.0, 1.1.0",
	}, {
		name: "a service that runs nothing has no deployed revision to return",
		svc:  Service{Name: "x", Revisions: []Revision{{Version: "1.0.0", Deployed: true}}},
		want: "runs no workload",
	}, {
		name: "and neither has an empty one",
		svc:  Service{Name: "x"},
		want: "runs no workload",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			rev, err := tc.svc.DeployedRevision()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("DeployedRevision: %v", err)
				}
				if rev.Version != "1.0.0" {
					t.Errorf("deployed revision = %q, want 1.0.0", rev.Version)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error mentioning %q", err, tc.want)
			}
			if rev.Version != "" {
				t.Errorf("a refused declaration still yielded revision %q", rev.Version)
			}
		})
	}
}

// Validate is the rule every surface reads the fixture through. The canonical
// fixture must satisfy it, and a fixture that cannot mean one thing must not
// reach any projection — including the surfaces that never call DeployedRevision
// for the offending service, which is how a workload-less "deployment" used to
// pass the projections and still oblige the gate to find a target for it.
func TestValidate_TheFixtureMeansOneThing(t *testing.T) {
	if err := OperationalGraph.Validate(); err != nil {
		t.Fatalf("the canonical fixture is not valid: %v", err)
	}
	for _, tc := range []struct {
		name string
		svc  Service
		want string
	}{
		{"two deployed revisions", Service{Name: "x", Workload: &Workload{Name: "x"},
			Revisions: []Revision{{Version: "1.0.0", Deployed: true}, {Version: "1.1.0", Deployed: true}}},
			"exactly one"},
		{"nothing deployed", Service{Name: "x", Workload: &Workload{Name: "x"},
			Revisions: []Revision{{Version: "1.0.0"}}}, "deploys no revision"},
		{"a deployment nothing runs", Service{Name: "x",
			Revisions: []Revision{{Version: "1.0.0", Deployed: true}}}, "runs no workload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := OperationalGraph
			s.Services = append(append([]Service(nil), s.Services...), tc.svc)
			err := s.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want an error mentioning %q", err, tc.want)
			}
			if err != nil && !strings.Contains(err.Error(), s.Name) {
				t.Errorf("the error does not name the scenario it rejects: %v", err)
			}
		})
	}
}

func parseBundle(t *testing.T, dir, bundle string) *contract.Contract {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, bundle, "pacto.yaml")) //nolint:gosec // a path this test just wrote
	if err != nil {
		t.Fatalf("opening the materialized bundle: %v", err)
	}
	defer func() { _ = f.Close() }()
	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("%s/pacto.yaml is not a contract the real parser accepts: %v", bundle, err)
	}
	return c
}

func decodeExport(t *testing.T, raw []byte) otlpExport {
	t.Helper()
	var e otlpExport
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("the export is not JSON the collector could read: %v", err)
	}
	return e
}

func attrValue(t *testing.T, attrs []otlpAttr, key string) string {
	t.Helper()
	for _, a := range attrs {
		if a.Key == key {
			return a.Value.StringValue
		}
	}
	t.Fatalf("the export carries no %q attribute", key)
	return ""
}

func relationshipDeclared(from, to string) bool {
	for _, rel := range OperationalGraph.Relationships {
		if rel.From == from && rel.To == to && rel.Declared {
			return true
		}
	}
	return false
}

func sourceExists(s Scenario, id string) bool {
	for _, src := range s.Sources {
		if src.ID == id {
			return true
		}
	}
	return false
}

func mustExist(t *testing.T, s Scenario, name string) Service {
	t.Helper()
	svc, ok := s.Service(name)
	if !ok {
		t.Fatalf("the scenario refers to service %q but never declares it", name)
	}
	return svc
}
