// These tests are the MUTATION CHECK for the gate. An acceptance assertion that
// silently stops asserting is worse than no assertion: the cluster run goes
// green and the claim in the harness comment is now a lie. So every check below
// is exercised twice — once against a payload that must pass, and once against
// the same payload with exactly that fact broken, which must fail and say why.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

const mountRoot = "/var/lib/pacto/observation"

// deploymentJSON is the shape `kubectl get deployment pacto-dashboard -o json`
// serves for the four sources observation.sh declares: two PVC-backed, two
// ConfigMap-backed. Only the fields the gate reads are kept.
const deploymentJSON = `{
  "spec": {
    "template": {
      "spec": {
        "volumes": [
          {"name": "obs-orders-traces", "persistentVolumeClaim": {"claimName": "orders-trace-export", "readOnly": true}},
          {"name": "obs-escaping-traces", "persistentVolumeClaim": {"claimName": "escaping-trace-export", "readOnly": true}},
          {"name": "obs-broken-traces", "configMap": {"name": "broken-trace-export"}},
          {"name": "obs-fixture-traces", "configMap": {"name": "fixture-trace-export"}}
        ],
        "containers": [
          {
            "volumeMounts": [
              {"name": "obs-orders-traces", "mountPath": "/var/lib/pacto/observation/orders-traces", "readOnly": true},
              {"name": "obs-escaping-traces", "mountPath": "/var/lib/pacto/observation/escaping-traces", "readOnly": true},
              {"name": "obs-broken-traces", "mountPath": "/var/lib/pacto/observation/broken-traces", "readOnly": true},
              {"name": "obs-fixture-traces", "mountPath": "/var/lib/pacto/observation/fixture-traces", "readOnly": true}
            ],
            "env": [
              {"name": "PACTO_DASHBOARD_WATCH_NAMESPACE", "value": ""},
              {"name": "PACTO_DASHBOARD_TRACE_SOURCES", "value": "broken-traces=/var/lib/pacto/observation/broken-traces/traces.json escaping-traces=/var/lib/pacto/observation/escaping-traces/escape.json fixture-traces=/var/lib/pacto/observation/fixture-traces/traces.json orders-traces=/var/lib/pacto/observation/orders-traces/traces.json"}
            ]
          }
        ]
      }
    }
  }
}`

func declaredSources() []source {
	return []source{
		{name: "orders-traces", kind: "pvc", backing: "orders-trace-export", file: "traces.json"},
		{name: "broken-traces", kind: "configMap", backing: "broken-trace-export", file: "traces.json"},
		{name: "fixture-traces", kind: "configMap", backing: "fixture-trace-export", file: "traces.json"},
		{name: "escaping-traces", kind: "pvc", backing: "escaping-trace-export", file: "escape.json"},
	}
}

func decodeDeployment(t *testing.T, raw string) deployment {
	t.Helper()
	var d deployment
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("decode the fixture deployment: %v", err)
	}
	return d
}

func TestCheckWiring_AcceptsTheRealChartOutput(t *testing.T) {
	if errs := checkWiring(decodeDeployment(t, deploymentJSON), declaredSources(), nil, mountRoot); len(errs) != 0 {
		t.Fatalf("the real chart output must pass, got: %v", errs)
	}
}

func TestCheckWiring_CatchesEveryBrokenFact(t *testing.T) {
	for _, tc := range []struct {
		name, old, new, want string
	}{
		{
			"a PVC volume points at another claim",
			`"claimName": "orders-trace-export"`, `"claimName": "someone-elses-export"`,
			"obs-orders-traces is not backed by the declared PVC orders-trace-export",
		},
		{
			"a PVC volume is writable",
			`{"claimName": "orders-trace-export", "readOnly": true}`, `{"claimName": "orders-trace-export"}`,
			"obs-orders-traces is not readOnly",
		},
		{
			"a ConfigMap volume points at another ConfigMap",
			`"configMap": {"name": "fixture-trace-export"}`, `"configMap": {"name": "other-export"}`,
			"obs-fixture-traces is not backed by the declared ConfigMap fixture-trace-export",
		},
		{
			"a source loses its volume",
			`{"name": "obs-broken-traces", "configMap": {"name": "broken-trace-export"}},`, ``,
			"obs-broken-traces has no volume",
		},
		{
			"a source loses its mount",
			`{"name": "obs-broken-traces", "mountPath": "/var/lib/pacto/observation/broken-traces", "readOnly": true},`, ``,
			"missing mount obs-broken-traces",
		},
		{
			"a mount is writable",
			`{"name": "obs-fixture-traces", "mountPath": "/var/lib/pacto/observation/fixture-traces", "readOnly": true}`,
			`{"name": "obs-fixture-traces", "mountPath": "/var/lib/pacto/observation/fixture-traces"}`,
			"obs-fixture-traces is mounted writable",
		},
		{
			"a mount moves out of the declared root",
			`"mountPath": "/var/lib/pacto/observation/escaping-traces"`, `"mountPath": "/tmp/escaping-traces"`,
			"obs-escaping-traces mounted at /tmp/escaping-traces",
		},
		{
			"the configured sources drop one",
			` orders-traces=/var/lib/pacto/observation/orders-traces/traces.json`, ``,
			"PACTO_DASHBOARD_TRACE_SOURCES=",
		},
		{
			"a configured source points at the wrong file",
			`escaping-traces=/var/lib/pacto/observation/escaping-traces/escape.json`,
			`escaping-traces=/var/lib/pacto/observation/escaping-traces/traces.json`,
			"PACTO_DASHBOARD_TRACE_SOURCES=",
		},
		{
			// Rendered in declaration order the value still contains every
			// expected fragment; only a whole-value comparison catches it.
			"the configured sources are no longer deterministically ordered",
			`"value": "broken-traces=/var/lib/pacto/observation/broken-traces/traces.json escaping-traces=/var/lib/pacto/observation/escaping-traces/escape.json fixture-traces=/var/lib/pacto/observation/fixture-traces/traces.json orders-traces=/var/lib/pacto/observation/orders-traces/traces.json"`,
			`"value": "orders-traces=/var/lib/pacto/observation/orders-traces/traces.json broken-traces=/var/lib/pacto/observation/broken-traces/traces.json fixture-traces=/var/lib/pacto/observation/fixture-traces/traces.json escaping-traces=/var/lib/pacto/observation/escaping-traces/escape.json"`,
			"PACTO_DASHBOARD_TRACE_SOURCES=",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(deploymentJSON, tc.old, tc.new, 1)
			if mutated == deploymentJSON {
				t.Fatalf("the mutation did not apply; %q is not in the fixture", tc.old)
			}
			errs := checkWiring(decodeDeployment(t, mutated), declaredSources(), nil, mountRoot)
			if !containsSubstring(errs, tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, errs)
			}
		})
	}
}

// The removal half of the scenario: after the upgrade only orders-traces is
// declared and the other three must have taken all their wiring with them.
func TestCheckWiring_AbsentSourcesLeaveNothingBehind(t *testing.T) {
	const removedJSON = `{
  "spec": {"template": {"spec": {
    "volumes": [{"name": "obs-orders-traces", "persistentVolumeClaim": {"claimName": "orders-trace-export", "readOnly": true}}],
    "containers": [{
      "volumeMounts": [{"name": "obs-orders-traces", "mountPath": "/var/lib/pacto/observation/orders-traces", "readOnly": true}],
      "env": [{"name": "PACTO_DASHBOARD_TRACE_SOURCES", "value": "orders-traces=/var/lib/pacto/observation/orders-traces/absent.json"}]
    }]
  }}}
}`
	kept := []source{{name: "orders-traces", kind: "pvc", backing: "orders-trace-export", file: "absent.json"}}
	gone := []string{"broken-traces", "fixture-traces", "escaping-traces"}

	if errs := checkWiring(decodeDeployment(t, removedJSON), kept, gone, mountRoot); len(errs) != 0 {
		t.Fatalf("a clean removal must pass, got: %v", errs)
	}

	for _, tc := range []struct {
		name, old, new, want string
	}{
		{
			"an orphaned volume survives",
			`"volumes": [`, `"volumes": [{"name": "obs-broken-traces", "configMap": {"name": "broken-trace-export"}},`,
			"broken-traces left an orphaned volume behind",
		},
		{
			"an orphaned mount survives",
			`"volumeMounts": [`, `"volumeMounts": [{"name": "obs-fixture-traces", "mountPath": "/var/lib/pacto/observation/fixture-traces", "readOnly": true},`,
			"fixture-traces left an orphaned mount behind",
		},
		{
			"a removed source is still configured",
			`"value": "orders-traces=`, `"value": "escaping-traces=/var/lib/pacto/observation/escaping-traces/escape.json orders-traces=`,
			"escaping-traces is still configured",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(removedJSON, tc.old, tc.new, 1)
			if mutated == removedJSON {
				t.Fatalf("the mutation did not apply; %q is not in the fixture", tc.old)
			}
			errs := checkWiring(decodeDeployment(t, mutated), kept, gone, mountRoot)
			if !containsSubstring(errs, tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestCheckWiring_RejectsAContainerlessDeployment(t *testing.T) {
	errs := checkWiring(decodeDeployment(t, `{"spec":{"template":{"spec":{}}}}`), declaredSources(), nil, mountRoot)
	if !containsSubstring(errs, "no containers") {
		t.Fatalf("want a no-containers error, got %v", errs)
	}
}

func TestParseSource(t *testing.T) {
	got, err := parseSource("orders-traces:pvc:orders-trace-export:traces.json")
	if err != nil {
		t.Fatalf("a well-formed spec must parse: %v", err)
	}
	if want := (source{"orders-traces", "pvc", "orders-trace-export", "traces.json"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for _, bad := range []string{"", "a:pvc:b", "a:pvc:b:c:d", ":pvc:b:c", "a:secret:b:c", "a:pvc::c", "a:pvc:b:"} {
		if _, err := parseSource(bad); err == nil {
			t.Errorf("parseSource(%q) must fail", bad)
		}
	}
}

// --- snapshot ----------------------------------------------------------------

// goodSnapshot is what the dashboard reports once the scenario is fully true:
// the mounted export and the projected ConfigMap read, the malformed source and
// the escaping source explicitly unavailable, and the observed orders->checkout
// edge attributed to the source that actually witnessed it.
func goodSnapshot() *fleet.FleetSnapshot {
	return &fleet.FleetSnapshot{
		Services: map[fleet.ServiceKey]*fleet.ServiceRecord{
			"demo/checkout": {Key: "demo/checkout", Name: "checkout"},
			"demo/orders":   {Key: "demo/orders", Name: "orders"},
		},
		Sources: []fleet.SourceState{
			{ID: "orders-traces", Kind: "observation", Status: fleet.SourceAvailable},
			{ID: "fixture-traces", Kind: "observation", Status: fleet.SourceAvailable},
			{ID: "broken-traces", Kind: "observation", Status: fleet.SourceUnavailable},
			{ID: "escaping-traces", Kind: "observation", Status: fleet.SourceUnavailable},
			{ID: "kubernetes", Kind: "kubernetes", Status: fleet.SourceAvailable},
		},
		Limitations: []fleet.Limitation{
			{Code: fleet.LimitationSourceUnavailable, Source: "broken-traces", Message: "unreadable"},
			{Code: fleet.LimitationSourceUnavailable, Source: "escaping-traces", Message: "unreadable"},
		},
		Relationships: []fleet.Relationship{
			{
				FromService: "demo/orders", ToService: "demo/checkout",
				Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved,
				ObservedSources: []fleet.ObservedSourceStat{{Source: "orders-traces", Count: 1}},
			},
			{
				FromService: "demo/checkout", ToService: "demo/orders",
				Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved,
				ObservedSources: []fleet.ObservedSourceStat{{Source: "fixture-traces", Count: 1}},
			},
		},
	}
}

func installedWant() snapshotWant {
	return snapshotWant{
		available:   []string{"orders-traces", "fixture-traces"},
		unavailable: []string{"broken-traces", "escaping-traces"},
		services:    []string{"checkout"},
		observed:    []string{"orders:checkout"},
		attributed:  []string{"orders-traces"},
		silent:      []string{"escaping-traces"},
	}
}

func TestCheckSnapshot_AcceptsTheFullyAssembledScenario(t *testing.T) {
	if errs := checkSnapshot(goodSnapshot(), installedWant()); len(errs) != 0 {
		t.Fatalf("the assembled scenario must pass, got: %v", errs)
	}
}

func TestCheckSnapshot_CatchesEveryBrokenFact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*fleet.FleetSnapshot)
		want   string
	}{
		{
			"a source that must be available is not",
			func(s *fleet.FleetSnapshot) { s.Sources[0].Status = fleet.SourceUnavailable },
			"orders-traces status=unavailable, want available",
		},
		{
			"the projected ConfigMap source broke (a rooted read that follows no symlink)",
			func(s *fleet.FleetSnapshot) { s.Sources[1].Status = fleet.SourceUnavailable },
			"fixture-traces status=unavailable, want available",
		},
		{
			"a source vanished from the snapshot entirely",
			func(s *fleet.FleetSnapshot) { s.Sources = s.Sources[1:] },
			"orders-traces is not a source in the snapshot",
		},
		{
			"a failing source silently reports itself healthy",
			func(s *fleet.FleetSnapshot) { s.Sources[2].Status = fleet.SourceAvailable },
			"broken-traces status=available, want unavailable",
		},
		{
			"a failing source is a silent gap, with no limitation naming it",
			func(s *fleet.FleetSnapshot) { s.Limitations = s.Limitations[:1] },
			"no SOURCE_UNAVAILABLE limitation naming escaping-traces",
		},
		{
			"a limitation names a different source",
			func(s *fleet.FleetSnapshot) { s.Limitations[0].Source = "orders-traces" },
			"no SOURCE_UNAVAILABLE limitation naming broken-traces",
		},
		{
			"the observed edge never reached the fleet",
			func(s *fleet.FleetSnapshot) { s.Relationships = s.Relationships[1:] },
			"no observed orders->checkout edge reached the fleet",
		},
		{
			"the edge is there but only as a declaration",
			func(s *fleet.FleetSnapshot) { s.Relationships[0].Provenance = fleet.ProvenanceDeclared },
			"no observed orders->checkout edge reached the fleet",
		},
		{
			"the observed edge is attributed to the wrong source",
			func(s *fleet.FleetSnapshot) {
				s.Relationships[0].ObservedSources[0].Source = "fixture-traces"
			},
			"the observed orders->checkout edge is not attributed to orders-traces",
		},
		{
			// The whole point of the escaping source: had the symlink out of the
			// mount been followed, it would have read the very export this edge
			// came from and would be counted here alongside orders-traces.
			"the escaping source read outside its mount and joined the expected edge",
			func(s *fleet.FleetSnapshot) {
				s.Relationships[0].ObservedSources = append(s.Relationships[0].ObservedSources,
					fleet.ObservedSourceStat{Source: "escaping-traces", Count: 1})
			},
			"escaping-traces contributed evidence to the snapshot",
		},
		{
			"the escaping source contributed to some OTHER edge nobody asserted",
			func(s *fleet.FleetSnapshot) {
				s.Relationships[1].ObservedSources[0].Source = "escaping-traces"
			},
			"escaping-traces contributed evidence to the snapshot",
		},
		{
			"the reconciled services are not in the snapshot",
			func(s *fleet.FleetSnapshot) { delete(s.Services, "demo/checkout") },
			"service checkout is not in the snapshot",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := goodSnapshot()
			tc.break_(s)
			errs := checkSnapshot(s, installedWant())
			if !containsSubstring(errs, tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, errs)
			}
		})
	}
}

// The second half of the scenario: one source is repointed at a file that is not
// there, and the rest of the fleet must be untouched by it.
func TestCheckSnapshot_DegradedSourceLeavesTheFleetIntact(t *testing.T) {
	want := snapshotWant{
		unavailable: []string{"orders-traces"},
		kinds:       []string{"kubernetes"},
		services:    []string{"checkout"},
	}
	degraded := func() *fleet.FleetSnapshot {
		s := goodSnapshot()
		s.Sources[0].Status = fleet.SourceUnavailable
		s.Limitations = append(s.Limitations,
			fleet.Limitation{Code: fleet.LimitationSourceUnavailable, Source: "orders-traces", Message: "no such file"})
		return s
	}
	if errs := checkSnapshot(degraded(), want); len(errs) != 0 {
		t.Fatalf("the degraded scenario must pass, got: %v", errs)
	}

	s := degraded()
	s.Sources[4].Status = fleet.SourceUnavailable // the live kubernetes source
	if errs := checkSnapshot(s, want); !containsSubstring(errs, "no available source of kind kubernetes") {
		t.Fatalf("a failing observation source must not be allowed to take the live source with it, got %v", errs)
	}

	s = degraded()
	s.Sources[0].Status = fleet.SourceAvailable
	if errs := checkSnapshot(s, want); !containsSubstring(errs, "orders-traces status=available, want unavailable") {
		t.Fatalf("a missing trace file must surface as unavailable, got %v", errs)
	}
}

func TestCheckSnapshot_MatchesDomainQualifiedServiceKeys(t *testing.T) {
	s := goodSnapshot()
	// A real fleet key is domain-qualified; the registry host is not the fact
	// under test, so the edge must still be found by its service-name suffix.
	s.Relationships[0].FromService = "pacto-registry.pacto-system.svc.cluster.local/demo/orders"
	s.Relationships[0].ToService = "pacto-registry.pacto-system.svc.cluster.local/demo/checkout"
	if errs := checkSnapshot(s, snapshotWant{observed: []string{"orders:checkout"}, attributed: []string{"orders-traces"}}); len(errs) != 0 {
		t.Fatalf("a domain-qualified key must still match, got %v", errs)
	}
}

func TestRunSnapshot_PollsUntilTheFactsBecomeTrue(t *testing.T) {
	// The dashboard rebuilds its snapshot on an interval, so the first answers
	// are legitimately not-yet. Only a poll distinguishes that from a failure.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/fleet/snapshot" {
			http.NotFound(w, r)
			return
		}
		calls++
		s := goodSnapshot()
		if calls < 3 {
			s.Sources[0].Status = fleet.SourceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	}))
	defer srv.Close()

	args := []string{"-base", srv.URL, "-available", "orders-traces", "-timeout", "5s", "-interval", "10ms"}
	errs, err := runSnapshot(args)
	if err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("the facts became true on the third round, got %v", errs)
	}
	if calls < 3 {
		t.Fatalf("want at least 3 polls, got %d", calls)
	}
}

func TestRunSnapshot_ReportsTheLastRoundOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s := goodSnapshot()
		s.Sources[0].Status = fleet.SourceUnavailable
		_ = json.NewEncoder(w).Encode(s)
	}))
	defer srv.Close()

	start := time.Now()
	errs, err := runSnapshot([]string{"-base", srv.URL, "-available", "orders-traces", "-timeout", "60ms", "-interval", "10ms"})
	if err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}
	if !containsSubstring(errs, "orders-traces status=unavailable") {
		t.Fatalf("a timeout must report what never became true, got %v", errs)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the timeout was not honoured: %s", elapsed)
	}
}

func TestRunSnapshot_ReportsAnUnreachableDashboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	errs, err := runSnapshot([]string{"-base", srv.URL, "-timeout", "30ms", "-interval", "10ms"})
	if err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}
	if !containsSubstring(errs, "500") {
		t.Fatalf("want the HTTP status reported, got %v", errs)
	}
}

func TestRunSnapshot_RejectsAMalformedEdge(t *testing.T) {
	if _, err := runSnapshot([]string{"-observed", "orders"}); err == nil {
		t.Fatal("an edge without a colon must be rejected")
	}
}

func TestRunWiring_ParsesFlagsAndReadsStdin(t *testing.T) {
	args := []string{
		"-mount-root", mountRoot,
		"-source", "orders-traces:pvc:orders-trace-export:traces.json",
		"-source", "broken-traces:configMap:broken-trace-export:traces.json",
		"-source", "fixture-traces:configMap:fixture-trace-export:traces.json",
		"-source", "escaping-traces:pvc:escaping-trace-export:escape.json",
	}
	errs, err := runWiring(args, strings.NewReader(deploymentJSON))
	if err != nil {
		t.Fatalf("runWiring: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("the real chart output must pass, got %v", errs)
	}

	if _, err := runWiring([]string{"-source", "orders-traces:pvc:claim"}, strings.NewReader(deploymentJSON)); err == nil {
		t.Fatal("a malformed -source must be rejected")
	}
	if _, err := runWiring(nil, strings.NewReader("not json")); err == nil {
		t.Fatal("an undecodable deployment must be rejected")
	}
}

func containsSubstring(errs []string, want string) bool {
	for _, e := range errs {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}
