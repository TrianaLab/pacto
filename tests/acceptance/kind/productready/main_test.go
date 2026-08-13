package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/tests/acceptance/scenario"
)

// These tests hold the gate to the rule that makes its twelve facts mean anything:
// they must all come from ONE snapshot.
//
// The dashboard Manager swaps its snapshot on a refresh interval, and each of the
// gate's ~17 requests is answered by whichever snapshot is current when it lands.
// A gate that reads the service list from one snapshot and the neighborhood from
// the next has proved a system view that never existed — and it can do so while
// every individual fact looks true. The Product tells the gate which snapshot
// answered, on every response; the only way to be wrong is not to check.
//
// The server below is a controlled Product: a complete, passing fixture whose
// snapshot ids can be perturbed one response at a time.

const (
	fxDomain    = "reg.example/demo"
	fxCheckout  = "svc:checkout"
	fxOrders    = "svc:orders"
	fxPayments  = "svc:payments"
	fxRevA      = "rev:checkout-a"
	fxRevB      = "rev:checkout-b"
	fxRevOrders = "rev:orders"
	fxTgtC      = "tgt:checkout"
	fxTgtO      = "tgt:orders"
	fxTgtP      = "tgt:payments"
	fxOCI       = "oci"
	fxCache     = "cache"
	fxObs       = "orders-traces"
	fxEvidence  = "evidence-http"
)

func meta(id string) fleet.ProductMeta {
	return fleet.ProductMeta{SchemaVersion: "1", SnapshotID: id, AsOf: time.Unix(0, 0).UTC()}
}

func svcRef(key, label, domain string) fleet.EntityRef {
	return fleet.EntityRef{Kind: "service", Key: key, Label: label, Domain: domain}
}

func revRef(key, version string) fleet.EntityRef {
	return fleet.EntityRef{Kind: "revision", Key: key, Label: key, Version: version}
}

// fakeProduct serves a complete, passing fixture. Each round starts when the gate
// lists the data sources, which is how it mirrors a live dashboard: every request
// of a round is answered by the snapshot current at the round's start, and with
// rotate set, a new round means a new snapshot.
type fakeProduct struct {
	mu      sync.Mutex
	round   int
	current string
	rotate  bool
	// perturb overrides the snapshot id of the requests it matches, standing in for
	// a refresh that lands between two of the gate's requests.
	perturb func(r *http.Request) (string, bool)
	// extraRevisions are appended to the checkout revision list, so a test can add
	// a second record for an already-published version.
	extraRevisions []fleet.EntityRef
	// preCache is the dashboard BEFORE its OCI cache has been populated: the cache
	// source is absent and every revision was contributed by the registry alone.
	// Everything else about the fixture is complete and coherent.
	preCache bool
	// truncatedProvenance serves a bounded provenance list that dropped a source,
	// so the answer cannot say which sources contributed.
	truncatedProvenance bool
}

// revisionSources is the provenance the fake serves for every revision.
func (f *fakeProduct) revisionSources() fleet.StringsPreview {
	if f.preCache {
		return fleet.StringsPreview{Total: 1, Count: 1, Items: []string{fxOCI}}
	}
	if f.truncatedProvenance {
		return fleet.StringsPreview{Total: 2, Count: 1, Truncated: true, Items: []string{fxOCI}}
	}
	return fleet.StringsPreview{Total: 2, Count: 2, Items: []string{fxOCI, fxCache}}
}

func (f *fakeProduct) idFor(r *http.Request) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/fleet/entities" && r.URL.Query().Get("kinds") == "source" {
		f.round++
		if f.rotate || f.current == "" {
			f.current = fmt.Sprintf("snap-%d", f.round)
		}
	}
	id := f.current
	if f.perturb != nil {
		if alt, ok := f.perturb(r); ok {
			return alt
		}
	}
	return id
}

func (f *fakeProduct) rounds() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.round
}

func (f *fakeProduct) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := f.idFor(r)
	q := r.URL.Query()
	switch {
	case r.URL.Path == "/api/fleet/entities":
		writeJSON(w, f.list(id, q))
	case strings.HasPrefix(r.URL.Path, "/api/fleet/entities/"):
		d, ok := f.detail(id, strings.TrimPrefix(r.URL.Path, "/api/fleet/entities/"), q.Get("key"))
		if !ok {
			http.Error(w, "no such entity", http.StatusNotFound)
			return
		}
		writeJSON(w, d)
	case r.URL.Path == "/api/fleet/neighborhood":
		writeJSON(w, f.neighborhood(id))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeProduct) list(id string, q url.Values) fleet.EntityList {
	var refs []fleet.EntityRef
	switch service := q.Get("service"); q.Get("kinds") {
	case "source":
		refs = []fleet.EntityRef{
			{Kind: "source", Key: fxOCI, Label: fxOCI},
			{Kind: "source", Key: fxObs, Label: fxObs},
			{Kind: "source", Key: fxEvidence, Label: fxEvidence},
		}
		if !f.preCache {
			refs = append(refs, fleet.EntityRef{Kind: "source", Key: fxCache, Label: fxCache})
		}
	case "service":
		refs = []fleet.EntityRef{
			svcRef(fxCheckout, "checkout", fxDomain),
			svcRef(fxOrders, "orders", fxDomain),
			svcRef(fxPayments, "payments", fxDomain),
		}
	case "revision":
		switch service {
		case fxCheckout:
			refs = append([]fleet.EntityRef{revRef(fxRevA, "1.0.0"), revRef(fxRevB, "1.1.0")}, f.extraRevisions...)
		case fxOrders:
			refs = []fleet.EntityRef{revRef(fxRevOrders, "1.0.0")}
		}
	case "target":
		switch service {
		case fxCheckout:
			refs = []fleet.EntityRef{{Kind: "target", Key: fxTgtC, Label: "checkout"}}
		case fxOrders:
			refs = []fleet.EntityRef{{Kind: "target", Key: fxTgtO, Label: "orders"}}
		case fxPayments:
			refs = []fleet.EntityRef{{Kind: "target", Key: fxTgtP, Label: "payments"}}
		}
	}
	return fleet.EntityList{
		Meta: meta(id), Total: len(refs), Count: len(refs), Limit: 200, Entities: refs,
	}
}

func (f *fakeProduct) detail(id, kind, key string) (fleet.EntityDetail, bool) {
	d := fleet.EntityDetail{Meta: meta(id), Entity: fleet.EntityRef{Kind: fleet.EntityKind(kind), Key: key}}
	exact := fleet.RevisionIdentity{
		Digest: "sha256:f1", ResolvedRef: "oci://" + fxDomain + "/x@sha256:f1",
		Retrievable: true, IdentityClass: "exact",
	}
	switch kind {
	case "source":
		d.Source = &fleet.SourceDetailData{Kind: "oci", Health: "available"}
	case "revision":
		version := map[string]string{fxRevA: "1.0.0", fxRevB: "1.1.0", fxRevOrders: "1.0.0"}[key]
		if version == "" {
			return d, false
		}
		d.Revision = &fleet.RevisionDetailData{
			Version: version, Identity: exact,
			Provenance: fleet.RevisionProvenance{Source: fxOCI, Sources: f.revisionSources()},
		}
	case "target":
		switch key {
		case fxTgtC:
			d.Target = &fleet.TargetDetailData{
				LinkState: "exact", Revision: &fleet.EntityRef{Kind: "revision", Key: fxRevA}, Source: "k8s",
			}
		case fxTgtO:
			d.Target = &fleet.TargetDetailData{
				LinkState: "exact", Revision: &fleet.EntityRef{Kind: "revision", Key: fxRevOrders}, Source: "k8s",
			}
		case fxTgtP:
			d.Target = &fleet.TargetDetailData{LinkState: "exact", Source: fxEvidence}
		default:
			return d, false
		}
	default:
		return d, false
	}
	return d, true
}

func (f *fakeProduct) neighborhood(id string) fleet.Neighborhood {
	return fleet.Neighborhood{
		Meta:        meta(id),
		Perspective: "service",
		Edges: []fleet.NeighborhoodEdge{{
			ID:       "e1",
			From:     svcRef(fxOrders, "orders", fxDomain),
			To:       svcRef(fxCheckout, "checkout", fxDomain),
			Relation: "dependency", Expected: true, Observed: true,
			Provenance: "declared+observed", Difference: "matched",
			ObservationSources: fleet.ObservationSourcesPreview{
				Total: 1, Count: 1, Items: []fleet.ObservedSourceStat{{Source: fxObs}},
			},
		}},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(err)
	}
}

func newProber(t *testing.T, f *fakeProduct, snapshots int) *prober {
	t.Helper()
	return newProberFor(t, f, snapshots, scenario.OperationalGraph)
}

func newProberFor(t *testing.T, f *fakeProduct, snapshots int, scn scenario.Scenario) *prober {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &prober{
		base: srv.URL,
		http: srv.Client(),
		// Short and impatient: these tests are about verdicts, not waiting. A case
		// that cannot pass must reach its deadline in milliseconds.
		interval:  time.Millisecond,
		timeout:   150 * time.Millisecond,
		snapshots: snapshots,
		// The real scenario, not a restatement of it: the fake product above serves
		// exactly what tests/acceptance/scenario declares, so a fixture change that
		// the gate would mishandle fails here rather than in a Kind shard.
		scn:    scn,
		domain: fxDomain,
		facts:  scn.FactCount(),
	}
}

// The gate reads WHAT must hold out of the scenario. If any of it were still
// written down inside the gate, a scenario asking for something the product does
// not serve would sail through — and the fixture the harness materializes could
// drift away from the fixture the gate proves, which is exactly the failure the
// shared declaration exists to prevent. Each case perturbs ONE declared fact and
// requires the gate to notice; TestGateCoherentSnapshotPasses is the control.
func TestGateProvesWhatTheScenarioDeclares(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(s *scenario.Scenario)
		want  string
	}{{
		name: "a renamed observation source",
		apply: func(s *scenario.Scenario) {
			s.Sources[2].ID = "other-traces"
			s.Relationships[0].ObservedBy = "other-traces"
		},
		want: `observation is not attributed to "other-traces"`,
	}, {
		name:  "a service the fixture does not publish",
		apply: func(s *scenario.Scenario) { s.Services[1].Name = "checkout-renamed" },
		want:  `service "checkout-renamed" is not in the snapshot`,
	}, {
		name:  "a revision at a version nobody published",
		apply: func(s *scenario.Scenario) { s.Services[1].Revisions[1].Version = "9.9.9" },
		want:  "has no revision 9.9.9",
	}, {
		name:  "a different reconciliation verdict",
		apply: func(s *scenario.Scenario) { s.Relationships[0].Reconciliation = "observed-only" },
		want:  `reconciliation is "matched", want observed-only`,
	}, {
		name: "the other revision is the deployed one",
		apply: func(s *scenario.Scenario) {
			s.Services[1].Revisions[0].Deployed = false
			s.Services[1].Revisions[1].Deployed = true
		},
		want: "runs revision " + fxRevA + ", want " + fxRevB,
	}, {
		name:  "evidence arriving over a different source",
		apply: func(s *scenario.Scenario) { s.Evidence[0].Via = "evidence-grpc" },
		want:  `no target attributed to "evidence-grpc"`,
	}, {
		name:  "a data source the product does not serve",
		apply: func(s *scenario.Scenario) { s.Sources[0].ID = "registry-v2" },
		want:  `data source "registry-v2" is not in the snapshot`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			scn := cloneScenario(scenario.OperationalGraph)
			tc.apply(&scn)
			_, problems := newProberFor(t, &fakeProduct{}, 1, scn).run(io.Discard)
			if len(problems) == 0 {
				t.Fatal("the gate passed a scenario the product does not satisfy; it is not reading the scenario")
			}
			if !containsSubstring(problems, tc.want) {
				t.Errorf("problems do not name the perturbed fact (want a mention of %q): %v", tc.want, problems)
			}
		})
	}
}

func cloneScenario(s scenario.Scenario) scenario.Scenario {
	s.Sources = append([]scenario.Source(nil), s.Sources...)
	s.Relationships = append([]scenario.Relationship(nil), s.Relationships...)
	s.Evidence = append([]scenario.Evidence(nil), s.Evidence...)
	s.Services = append([]scenario.Service(nil), s.Services...)
	for i := range s.Services {
		s.Services[i].Revisions = append([]scenario.Revision(nil), s.Services[i].Revisions...)
	}
	return s
}

func TestGateCoherentSnapshotPasses(t *testing.T) {
	p := newProber(t, &fakeProduct{}, 1)
	keys, problems := p.run(io.Discard)
	if len(problems) != 0 {
		t.Fatalf("a coherent product answer must pass, got: %v", problems)
	}
	if keys.SnapshotID != "snap-1" {
		t.Errorf("SnapshotID = %q, want the id that proved the facts (snap-1)", keys.SnapshotID)
	}
	// The keys handed to the browser are the ones the product published.
	if keys.CheckoutRevisionA != fxRevA || keys.CheckoutRevisionB != fxRevB ||
		keys.OrdersRevision != fxRevOrders || keys.CheckoutTarget != fxTgtC ||
		keys.OrdersTarget != fxTgtO || keys.EvidenceTarget != fxTgtP {
		t.Errorf("discovered keys = %+v", keys)
	}
}

// spliceCases are the ways one refresh mid-round can splice two system views.
// Each one leaves EVERY semantic fact satisfiable — the only thing wrong is that
// they did not all come from the same snapshot.
func TestGateRejectsSplicedRounds(t *testing.T) {
	cases := []struct {
		name    string
		perturb func(r *http.Request) (string, bool)
		want    string
	}{{
		name: "a list answered by a different snapshot",
		perturb: func(r *http.Request) (string, bool) {
			q := r.URL.Query()
			return "snap-other", r.URL.Path == "/api/fleet/entities" &&
				q.Get("kinds") == "revision" && q.Get("service") == fxCheckout
		},
		want: "adopted",
	}, {
		name: "a detail answered by a different snapshot",
		perturb: func(r *http.Request) (string, bool) {
			return "snap-other", r.URL.Path == "/api/fleet/entities/revision" &&
				r.URL.Query().Get("key") == fxRevB
		},
		want: "adopted",
	}, {
		name: "the neighborhood answered by a different snapshot",
		perturb: func(r *http.Request) (string, bool) {
			return "snap-other", r.URL.Path == "/api/fleet/neighborhood"
		},
		want: "adopted",
	}, {
		name: "a response that names no snapshot at all",
		perturb: func(r *http.Request) (string, bool) {
			return "", r.URL.Path == "/api/fleet/entities/target" &&
				r.URL.Query().Get("key") == fxTgtO
		},
		want: "no snapshot id",
	}, {
		// The FIRST response setting the round's id is the one case where a wrong id
		// cannot be detected by comparison — so the round must be rejected by what
		// follows disagreeing with it, not by trusting whichever came first.
		name: "the first response of the round is the odd one out",
		perturb: func(r *http.Request) (string, bool) {
			return "snap-other", r.URL.Path == "/api/fleet/entities" &&
				r.URL.Query().Get("kinds") == "source"
		},
		want: "adopted",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newProber(t, &fakeProduct{perturb: tc.perturb}, 1)
			keys, problems := p.run(io.Discard)
			if len(problems) == 0 {
				t.Fatal("a round spliced from two snapshots must never pass")
			}
			if !containsSubstring(problems, tc.want) {
				t.Errorf("problems do not explain the splice (want a mention of %q): %v", tc.want, problems)
			}
			// A failed round hands on nothing: PW_FIXTURE must never be written from
			// facts that were never shown to describe one system state.
			if keys != (discovered{}) {
				t.Errorf("a failed round emitted fixture keys: %+v", keys)
			}
		})
	}
}

// The verdict is over the ROUND. Every fact can be individually true and the round
// still fails, which is the whole difference between this gate and its predecessor.
func TestGateSplicedRoundFailsEvenThoughEveryFactHolds(t *testing.T) {
	coherent := newProber(t, &fakeProduct{}, 1)
	if _, problems := coherent.run(io.Discard); len(problems) != 0 {
		t.Fatalf("the same fixture must pass when it is coherent: %v", problems)
	}

	spliced := newProber(t, &fakeProduct{perturb: func(r *http.Request) (string, bool) {
		return "snap-other", r.URL.Path == "/api/fleet/entities" &&
			r.URL.Query().Get("kinds") == "service"
	}}, 1)
	keys, problems := spliced.run(io.Discard)
	if len(problems) == 0 {
		t.Fatal("the identical fixture, spliced across two snapshots, must fail")
	}
	if keys.SnapshotID != "" {
		t.Errorf("a failed round reported snapshot %q; it must report none", keys.SnapshotID)
	}
}

// -snapshots N is how the Kind vertical reaches the state that only exists AFTER
// the first refresh has populated the dashboard's OCI cache. It is satisfied by
// observing N distinct snapshots prove the fixture, never by waiting a fixed time.
func TestGateRequiresDistinctSnapshots(t *testing.T) {
	t.Run("a dashboard that refreshes proves it twice", func(t *testing.T) {
		f := &fakeProduct{rotate: true}
		p := newProber(t, f, 2)
		keys, problems := p.run(io.Discard)
		if len(problems) != 0 {
			t.Fatalf("two refreshes must satisfy -snapshots 2: %v", problems)
		}
		if keys.SnapshotID != fmt.Sprintf("snap-%d", f.rounds()) {
			t.Errorf("SnapshotID = %q, want the LAST snapshot that proved the fixture", keys.SnapshotID)
		}
		if f.rounds() < 2 {
			t.Errorf("passed after %d rounds; two distinct snapshots cannot be observed in fewer", f.rounds())
		}
	})

	t.Run("one snapshot repeated is not two", func(t *testing.T) {
		// The gate must not accept the same snapshot twice: re-reading a cached
		// system view proves nothing about the state after the next refresh.
		p := newProber(t, &fakeProduct{}, 2)
		if _, problems := p.run(io.Discard); len(problems) == 0 {
			t.Fatal("a dashboard stuck on one snapshot must not satisfy -snapshots 2")
		}
	})
}

// The state the Kind vertical must reach is the POST-CACHE one: the pod's OCI
// cache starts empty, the first refresh's registry pulls fill it, and only then do
// the registry source and the disk-cache source offer the same published artifacts
// — the pairing that used to publish one artifact as two revisions.
//
// Counting distinct snapshots cannot establish that. SnapshotID hashes the
// generation time, so a dashboard that never populates a cache still emits an
// unlimited supply of distinct, individually coherent snapshots. These tests hold
// the gate to proving the state directly, from the provenance of the revisions.
func TestGateRequiresCacheContribution(t *testing.T) {
	t.Run("no number of refreshes substitutes for the cache", func(t *testing.T) {
		// Every fact except the cache holds, on a fresh snapshot every round, for as
		// many rounds as the deadline allows. Under -snapshots 1 — the weakest
		// possible demand — it must still never pass.
		f := &fakeProduct{rotate: true, preCache: true}
		p := newProber(t, f, 1)
		keys, problems := p.run(io.Discard)
		if len(problems) == 0 {
			t.Fatal("a fleet whose cache never contributed must not pass, however many snapshots it serves")
		}
		if f.rounds() < 3 {
			t.Fatalf("only %d rounds ran; the case must be shown to survive repeated distinct snapshots", f.rounds())
		}
		if !containsSubstring(problems, "the registry and the disk cache must reach the SAME canonical revision") {
			t.Errorf("problems do not name the missing cache contribution: %v", problems)
		}
		if keys != (discovered{}) {
			t.Errorf("a failed round emitted fixture keys: %+v", keys)
		}
	})

	t.Run("the cache source must itself be usable", func(t *testing.T) {
		p := newProber(t, &fakeProduct{preCache: true}, 1)
		if _, problems := p.run(io.Discard); !containsSubstring(problems, `data source "cache" is not in the snapshot`) {
			t.Fatalf("an absent cache source must be reported: %v", problems)
		}
	})

	t.Run("a truncated provenance list cannot answer the question", func(t *testing.T) {
		// The list is bounded, so "cache is not in the first N" is not "cache did not
		// contribute". An unanswerable question is a failure, not a pass.
		p := newProber(t, &fakeProduct{truncatedProvenance: true}, 1)
		if _, problems := p.run(io.Discard); !containsSubstring(problems, "truncated list cannot show which contributed") {
			t.Fatalf("a truncated provenance list must be reported: %v", problems)
		}
	})

	t.Run("one coherent post-cache snapshot passes", func(t *testing.T) {
		// The positive control for all three: the SAME fixture, with the cache source
		// present and both sources on every revision, passes on a single snapshot.
		keys, problems := newProber(t, &fakeProduct{}, 1).run(io.Discard)
		if len(problems) != 0 {
			t.Fatalf("a coherent post-cache snapshot must pass: %v", problems)
		}
		if keys.CacheSource != fxCache {
			t.Errorf("CacheSource = %q, want the discovered cache source %q", keys.CacheSource, fxCache)
		}
	})
}

// An unresolved duplicate of a published artifact must FAIL the gate, not be
// stepped over in favour of the sibling that happens to look retrievable.
func TestGateRejectsShadowRevision(t *testing.T) {
	// The same published version, present a second time under a derived content
	// identity: the shape the cache/registry double-identity produced. Its sibling
	// is perfectly exact and retrievable, so a gate that scanned for the first
	// acceptable match would step straight over it.
	p := newProber(t, &fakeProduct{
		extraRevisions: []fleet.EntityRef{revRef("rev:checkout-a-shadow", "1.0.0")},
	}, 1)
	_, problems := p.run(io.Discard)
	if !containsSubstring(problems, "one published artifact must be one canonical revision") {
		t.Fatalf("a shadow revision must fail the gate, got: %v", problems)
	}
}

func containsSubstring(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}
