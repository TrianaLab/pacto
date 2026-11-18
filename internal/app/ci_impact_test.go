package app

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// planWith plans the root against the registry with the fleet sources fo.
func (e *ciRepoEnv) planWith(t *testing.T, fo FleetOptions) (*ci.Plan, map[string]*impact.Result) {
	t.Helper()
	p, res, err := e.svc.CIPlan(context.Background(), CIOptions{Root: e.root, Registry: e.host + "/acme", Fleet: fo})
	if err != nil {
		t.Fatal(err)
	}
	return p, res
}

// targets writes a target-state file running each service and returns it.
func targets(t *testing.T, services ...string) string {
	t.Helper()
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n"
	for _, s := range services {
		body += "  - scope: prod\n    kind: k8s\n    name: " + s + "\n    service: " + s + "\n    compliance: Compliant\n"
	}
	file := filepath.Join(t.TempDir(), "targets.yaml")
	writeCIFile(t, filepath.Dir(file), "targets.yaml", body)
	return file
}

// paymentsV1 is a service with an interface its next revision can drop.
const paymentsV1 = "service: {name: payments, version: 1.0.0}\n" + interfaceYAML

// consumerOf is a service that depends on host/acme/payments ^1.0.0, named
// only by that ref.
func consumerOf(name, host string) string {
	return "service: {name: " + name + ", version: 1.0.0}\ndependencies:\n  - ref: oci://" + host + "/acme/payments\n    compatibility: \"^1.0.0\"\n"
}

// breakingPayments publishes payments 1.0.0 and drops its interface.
func (e *ciRepoEnv) breakingPayments(t *testing.T) {
	t.Helper()
	e.write(t, map[string]string{"payments/pacto.yaml": paymentsV1, "payments/openapi.yaml": openAPIFile})
	e.publish(t, "payments", "acme/payments", "1.0.0", nil)
	e.write(t, map[string]string{"payments/pacto.yaml": "service: {name: payments}\n"})
	if err := os.Remove(filepath.Join(e.root, "payments/openapi.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestCIConsumerFoundByRepositoryDomain(t *testing.T) {
	e := newCIRepo(t)
	e.breakingPayments(t)
	e.write(t, map[string]string{"web/pacto.yaml": consumerOf("web", e.host)})
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "web")}})
	if r := row(t, p, "payments"); r.Impact.Text != "blocked (web)" || r.Next != "2.0.0" {
		t.Fatalf("row = %+v, result %+v", r, res["payments"])
	}
}

func TestCIBlockedNamesConsumers(t *testing.T) {
	e := newCIRepo(t)
	e.breakingPayments(t)
	names := []string{"web1", "web2", "web3", "web4"}
	for _, n := range names {
		e.write(t, map[string]string{n + "/pacto.yaml": consumerOf(n, e.host)})
	}
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, names...)}})
	blocking := res["payments"].BlockingServices()
	want := "blocked (" + strings.Join(blocking[:3], ", ") + ", +1)"
	if r := row(t, p, "payments"); len(blocking) != 4 || r.Impact.Text != want || len(r.Impact.Blocking) != 4 || p.Exit != 1 {
		t.Fatalf("row = %+v, want %s", r, want)
	}
}

func TestCIUnverifiedCarriesReason(t *testing.T) {
	e := newCIRepo(t)
	e.breakingPayments(t)
	e.write(t, map[string]string{"web/pacto.yaml": consumerOf("web", e.host)})
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}})
	if r := row(t, p, "payments"); res["payments"].Gate != "unverified" || r.Impact.Text != "unverified ("+res["payments"].GateReason+")" {
		t.Fatalf("row = %+v", r)
	}
}

// additiveReports publishes reports 1.0.0 and changes a file beside it.
func (e *ciRepoEnv) additiveReports(t *testing.T) {
	t.Helper()
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/notes.txt": "a\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/notes.txt": "b\n", "reports/pacto.yaml": "service: {name: reports}\n"})
}

func TestCIClearNeedsConsumerSource(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	p, _ := e.planWith(t, FleetOptions{})
	if r := row(t, p, "reports"); r.Impact.Text != "unverified (no consumer source)" || p.Exit != 2 || len(p.ConsumerSources) != 0 || r.Impact.Sources == nil || len(r.Impact.Sources) != 0 {
		t.Fatalf("row = %+v", r)
	}
	file := targets(t, "other")
	p, _ = e.planWith(t, FleetOptions{TargetStateFiles: []string{file}})
	if r := row(t, p, "reports"); r.Impact.Text != "clear" || p.Exit != 0 || !slices.Equal(r.Impact.Sources, []string{"target-state"}) {
		t.Fatalf("row = %+v", r)
	}
	want := []ci.SourceLine{{Name: "target-state", Status: "available"}}
	if !slices.Equal(p.Snapshot.Sources, want) || p.Snapshot.AsOf.IsZero() || p.Snapshot.Completeness != "complete" || !slices.Equal(p.ConsumerSources, []string{"--target-state " + file}) {
		t.Fatalf("snapshot = %+v, sources %q", p.Snapshot, p.ConsumerSources)
	}
}

func TestCIFailedConsumerSourceNeverClear(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(srv.Close)
	p, _ := e.planWith(t, FleetOptions{EvidenceURLs: []string{srv.URL}})
	r := row(t, p, "reports")
	if len(p.Snapshot.Sources) != 1 || r.Impact.Text != "unverified (consumer source "+p.Snapshot.Sources[0].Name+" unavailable)" {
		t.Fatalf("row = %+v, snapshot %+v", r, p.Snapshot)
	}
}

// cacheOnlyStore is a store whose disk cache is dir and that reads nothing.
type cacheOnlyStore struct {
	oci.BundleStore
	dir string
}

func (c cacheOnlyStore) CacheDir() string { return c.dir }

func TestCIEmptyCacheIsNoConsumerSource(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	e.svc.BundleStore = cacheOnlyStore{dir: t.TempDir()}
	p, _ := e.planWith(t, FleetOptions{IncludeCache: true})
	if r := row(t, p, "reports"); r.Impact.Text != "unverified (consumer source cache empty)" {
		t.Fatalf("row = %+v", r)
	}
}

func TestCIInferredChangeNeverClear(t *testing.T) {
	e := newCIRepo(t)
	compose := "services:\n  reports:\n    image: " + e.host + "/acme/reports:1.0\n"
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/compose.yaml": compose + "    ports: [\"8080:8080\"]\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n", "reports/compose.yaml": compose})
	p, _ := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}})
	r := row(t, p, "reports")
	if r.Changed != "yes" || r.Classification != "NON_BREAKING" || r.Next != "1.0.1" || !strings.HasPrefix(r.Impact.Text, "unverified (inferred fields changed: ") {
		t.Fatalf("row = %+v", r)
	}
}

func TestCIServiceNameInferredNeverClear(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/notes.txt": "a\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/notes.txt": "b\n", "reports/pacto.yaml": "service: {version: 1.0.1}\n"})
	p, _ := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}})
	if r := row(t, p, "reports"); r.Impact.Text != "unverified (service.name inferred; declare it)" {
		t.Fatalf("row = %+v", r)
	}
}

func TestCIBaselinePinnedByDigest(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/notes.txt": "a\n"})
	d1 := e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 9.9.9}\n"})
	d9 := e.publish(t, "reports", "acme/reports", "scratch", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n", "reports/notes.txt": "b\n"})
	ctx, repo := context.Background(), e.host+"/acme/reports"
	reg := &fakeCIRegistry{
		tags: func(string) ([]string, error) { return e.reg.Tags(ctx, repo) },
		// The tag moves right after it is read.
		head: func(ref string) (*publish.Remote, error) {
			h, err := e.reg.Head(ctx, ref)
			if err == nil {
				err = e.reg.Tag(ctx, repo, d9, "1.0.0")
			}
			return h, err
		},
		pull: func(ref string) (*contract.Bundle, error) { return e.reg.Pull(ctx, ref) },
	}
	e.svc.sys.publishRegistry = func() publish.Registry { return reg }
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}})
	r := row(t, p, "reports")
	if r.BaselineDigest != d1 || res["reports"].OldVersion != "1.0.0" || r.Next != "1.0.1" || !slices.Contains(reg.calls, "pull "+repo+"@"+d1) {
		t.Fatalf("row = %+v, old %s, calls %v", r, res["reports"].OldVersion, reg.calls)
	}
}

func TestCIOnResolvedGetsLoadedBundles(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	var got []string
	opts := CIOptions{Root: e.root, Registry: e.host + "/acme", OnResolved: func(service string) func(old, new *contract.Bundle) {
		return func(old, new *contract.Bundle) {
			got = append(got, service+" "+old.Contract.Service.Name+" "+old.Contract.Service.Version+" -> "+new.Contract.Service.Name+" "+new.Contract.Service.Version)
		}
	}}
	if _, _, err := e.svc.CIPlan(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"reports reports 1.0.0 -> reports 1.0.1"}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestCIOneSnapshot(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	e.write(t, map[string]string{"billing/pacto.yaml": "service: {name: billing, version: 1.0.0}\n", "billing/notes.txt": "a\n"})
	e.publish(t, "billing", "acme/billing", "1.0.0", nil)
	e.write(t, map[string]string{"billing/notes.txt": "b\n", "billing/pacto.yaml": "service: {name: billing}\n"})
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	fo := FleetOptions{EvidenceURLs: []string{srv.URL}}
	if _, err := e.svc.Fleet(context.Background(), fo); err != nil {
		t.Fatal(err)
	}
	perBuild := n.Swap(0)
	_, res := e.planWith(t, fo)
	if len(res) != 2 || n.Load() != perBuild || res["reports"].SnapshotID != res["billing"].SnapshotID {
		t.Fatalf("%d results, %d requests for %d per build", len(res), n.Load(), perBuild)
	}
}

func TestCIUnchangedRunsNoImpact(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}})
	if res != nil || !p.Snapshot.AsOf.IsZero() || row(t, p, "reports").Changed != "no" {
		t.Fatalf("results %v, snapshot %+v", res, p.Snapshot)
	}
}

func TestCIImpactErrorUnverified(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	// A view that hides services refuses a revision it cannot see: here the
	// local one, since --local names a folder without it.
	fo := FleetOptions{LocalRoots: []string{t.TempDir()}, extraSources: []fleet.Source{fleet.NewMemorySource("hidden", "argument",
		&fleet.Collection{Limitations: []fleet.Limitation{{Code: fleet.LimitationHiddenByPermissions}}})}}
	p, res := e.planWith(t, fo)
	if r := row(t, p, "reports"); len(res) != 0 || !strings.HasPrefix(r.Impact.Text, "unverified (impact: ") {
		t.Fatalf("row = %+v", r)
	}
}

func TestRowImpact(t *testing.T) {
	local := &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "s"}}}
	for _, tc := range []struct {
		name     string
		res      impact.Result
		evidence string
		want     string
	}{
		// A blocked answer stands without a consumer source: a policy
		// failure is evidence.
		{"blocked policy", impact.Result{Gate: "blocked", GateReason: "the policy fails"}, "no consumer source", "blocked (the policy fails)"},
		{"unverified", impact.Result{Gate: "unverified", GateReason: "why"}, "", "unverified (why)"},
		{"partial", impact.Result{Gate: "clear", Completeness: fleet.CompletenessPartial}, "", "unverified (consumer sources partial)"},
		{"no source", impact.Result{Gate: "clear", Completeness: fleet.CompletenessComplete}, "no consumer source", "unverified (no consumer source)"},
		{"inferred", impact.Result{Gate: "clear", BreakingChanges: []diff.Change{{Path: "inferred.ports"}}, PotentiallyBreakingChanges: []diff.Change{{Path: "inferred.image"}, {Path: "interfaces"}}}, "", "unverified (inferred fields changed: ports, image; declare them to classify)"},
		{"clear", impact.Result{Gate: "clear", Completeness: fleet.CompletenessComplete}, "", "clear"},
	} {
		if got := rowImpact(&tc.res, local, tc.evidence); got.Text != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got.Text, tc.want)
		}
	}
}

func TestConsumerEvidence(t *testing.T) {
	st := func(id, kind string, status fleet.SourceStatus, n int, advisory bool) fleet.SourceState {
		return fleet.SourceState{ID: id, Kind: kind, Status: status, RevisionCount: n, Advisory: advisory}
	}
	for _, tc := range []struct {
		sources []fleet.SourceState
		want    string
	}{
		{nil, "no consumer source"},
		{[]fleet.SourceState{st("local", "local", fleet.SourceAvailable, 3, false), st(ciChangesSource, "argument", fleet.SourceAvailable, 1, false), st("detection", "detection", fleet.SourceAvailable, 1, true)}, "no consumer source"},
		{[]fleet.SourceState{st("a", "oci", fleet.SourceStale, 1, false), st("b", "oci", fleet.SourceUnavailable, 0, false)}, "consumer source a stale"},
		{[]fleet.SourceState{st("a", "oci", fleet.SourceUnavailable, 0, false), st("b", "oci", fleet.SourceAvailable, 1, false)}, ""},
	} {
		if got := consumerEvidence(&fleet.FleetSnapshot{Sources: tc.sources}); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.sources, got, tc.want)
		}
	}
}

// methodRecorder counts the requests a server gets by method.
type methodRecorder struct {
	mu      sync.Mutex
	methods map[string]int
}

func (m *methodRecorder) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.methods[r.Method]++
		m.mu.Unlock()
		h.ServeHTTP(w, r)
	})
}

// writes is every method other than GET and HEAD the server got.
func (m *methodRecorder) writes() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.methods {
		if k != http.MethodGet && k != http.MethodHead {
			out[k] = v
		}
	}
	return out
}

func (m *methodRecorder) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.methods = map[string]int{}
}

// newRecordedCIRepo is newCIRepo over a registry whose requests are counted.
func newRecordedCIRepo(t *testing.T) (*ciRepoEnv, *methodRecorder) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rec := &methodRecorder{methods: map[string]int{}}
	srv := httptest.NewServer(rec.wrap(registry.New(registry.Logger(log.New(io.Discard, "", 0)))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := publish.NewClient(anonymous, host)
	return &ciRepoEnv{root: t.TempDir(), host: host, reg: reg, svc: &Service{sys: sysDeps{publishRegistry: func() publish.Registry { return reg }}}}, rec
}

func TestCIPlanMakesNoWrites(t *testing.T) {
	e, rec := newRecordedCIRepo(t)
	e.additiveReports(t)
	e.write(t, map[string]string{"ledger/pacto.yaml": "service: {name: ledger, version: 1.0.0}\n", "billing/pacto.yaml": "service: {name: billing}\n"})
	e.publish(t, "ledger", "acme/ledger", "1.0.0", nil)
	token, tokenHits := countingHost(t)
	stats, statsHits := countingHost(t)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://"+token+"?x=1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "t")
	t.Setenv("PACTO_STATS_REGISTRY", stats+"/stats")
	rec.reset()
	p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{targets(t, "other")}})
	if row(t, p, "reports").Impact.Text != "clear" || row(t, p, "ledger").Changed != "no" || row(t, p, "billing").Impact.Text != "first publish, clear" || len(res) != 1 {
		t.Fatalf("rows = %+v", p.Rows)
	}
	if w := rec.writes(); len(w) != 0 || tokenHits.Load() != 0 || statsHits.Load() != 0 {
		t.Fatalf("writes %v, token %d, stats %d", w, tokenHits.Load(), statsHits.Load())
	}
}

func TestCINeverContactsRepoNamedHost(t *testing.T) {
	e, rec := newRecordedCIRepo(t)
	bundleHost, bundleHits := countingHost(t)
	imageHost, imageHits := countingHost(t)
	policyHost, policyHits := countingHost(t)
	depHost, depHits := countingHost(t)
	e.write(t, map[string]string{"reports/compose.yaml": "services:\n  reports:\n    image: " + imageHost + "/acme/reports:1.0\n"})
	e.additiveReports(t)
	e.write(t, map[string]string{
		"bundled/pacto.yaml": "service: {name: bundled}\npublish:\n  bundle: oci://" + bundleHost + "/x\n",
		"policy/pacto.yaml":  "service: {name: policy}\npolicies:\n  - name: p\n    ref: oci://" + policyHost + "/p:1.0.0\n",
	})
	// A consumer root whose dependency names another host: the closure is
	// followed through the plan's store, which reads only the hosts named.
	consumers := t.TempDir()
	writeCIFile(t, consumers, "pacto.yaml", "service: {name: web, version: 1.0.0}\ndependencies:\n  - name: dep\n    ref: oci://"+depHost+"/acme/dep\n    compatibility: \"^1.0.0\"\n")
	e.svc.BundleStore = oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host, bundleHost, imageHost, policyHost, depHost))
	rec.reset()
	p, _ := e.planWith(t, FleetOptions{CatalogRoots: []string{consumers}, TargetStateFiles: []string{targets(t, "other")}})
	if row(t, p, "bundled").Impact.Gate != "unverified" || row(t, p, "policy").Impact.Gate != "unverified" || row(t, p, "reports").Changed != "yes" {
		t.Fatalf("rows = %+v", p.Rows)
	}
	if n := bundleHits.Load() + imageHits.Load() + policyHits.Load() + depHits.Load(); n != 0 || len(rec.writes()) != 0 {
		t.Fatalf("%d requests to repository-named hosts, registry writes %v", n, rec.writes())
	}
}

func TestFencedStore(t *testing.T) {
	inner := cacheOnlyStore{BundleStore: fakeStore{}, dir: "/cache"}
	f := fencedStore{inner: inner, hosts: []string{"r.example"}}
	ctx := context.Background()
	if _, err := f.Push(ctx, "oci://r.example/a:1", nil); err == nil {
		t.Fatal("a plan writes nothing")
	}
	for _, ref := range []string{"oci://other.example/a:1", "a"} {
		if _, err := f.Pull(ctx, ref); err == nil {
			t.Fatalf("pull %s outside the fence", ref)
		}
		if _, err := f.Resolve(ctx, ref); err == nil {
			t.Fatalf("resolve %s outside the fence", ref)
		}
		if _, err := f.ListTags(ctx, ref); err == nil {
			t.Fatalf("tags %s outside the fence", ref)
		}
	}
	b, err1 := f.Pull(ctx, "oci://r.example/a:1")
	d, err2 := f.Resolve(ctx, "r.example/a:1")
	tags, err3 := f.ListTags(ctx, "r.example/a")
	if b == nil || d != "sha256:d" || len(tags) != 1 || errors.Join(err1, err2, err3) != nil || f.CacheDir() != "/cache" {
		t.Fatalf("inside the fence: %v %q %v %v %v %v", b, d, tags, err1, err2, err3)
	}
	if s := (&Service{}).fencedTo(nil); s.BundleStore != nil {
		t.Fatal("no store stays no store")
	}
}

// fakeStore answers every read.
type fakeStore struct{}

func (fakeStore) Push(context.Context, string, *contract.Bundle) (string, error) { return "", nil }
func (fakeStore) Pull(context.Context, string) (*contract.Bundle, error) {
	return &contract.Bundle{}, nil
}
func (fakeStore) Resolve(context.Context, string) (string, error) { return "sha256:d", nil }
func (fakeStore) ListTags(context.Context, string) ([]string, error) {
	return []string{"1.0.0"}, nil
}

func TestFenceHosts(t *testing.T) {
	got := fenceHosts(FleetOptions{CatalogRoots: []string{"./consumers", "oci://r.example/acme/web:1.0.0"}, OCIRefs: []string{"o.example/acme/api:1.0.0"}})
	if !slices.Equal(got, []string{"r.example", "o.example"}) {
		t.Fatalf("hosts = %q", got)
	}
}

func TestCISnapshotErrorFailsTheRun(t *testing.T) {
	e := newCIRepo(t)
	e.additiveReports(t)
	if _, _, err := e.svc.CIPlan(context.Background(), CIOptions{Root: e.root, Registry: e.host + "/acme", Fleet: FleetOptions{Detected: "k8s:nope"}}); err == nil {
		t.Fatal("a source refused before any read fails the run")
	}
}

// policyFailure publishes the policy service 1.0.0 with an open policy,
// tightens it to require metadata, and adds web, a repository-local service
// whose contract references the policy and carries no metadata.
// policyFailure publishes the policy service 1.0.0 with an open policy,
// tightens it to require metadata, and adds web, a repository-local service
// whose contract references the published policy by digest and carries no
// metadata, so it fails the new policy.
func (e *ciRepoEnv) policyFailure(t *testing.T) {
	t.Helper()
	e.write(t, map[string]string{
		"policy/pacto.yaml":         "service: {name: policy, version: 1.0.0}\n",
		"policy/policy/schema.json": `{"type":"object"}`,
	})
	d := e.publish(t, "policy", "acme/policy", "1.0.0", nil)
	e.write(t, map[string]string{
		"policy/pacto.yaml":         "service: {name: policy}\n",
		"policy/policy/schema.json": `{"type":"object","required":["metadata"]}`,
		"web/pacto.yaml":            "service: {name: web, version: 1.0.0}\npolicies:\n  - name: http\n    ref: oci://" + e.host + "/acme/policy@" + d + "\n",
	})
}

// TestCIBlockedSurvivesNoConsumerSource runs the real pipeline: a blocked
// answer needs no consumer source that answered, while the evidence a clear
// answer needs is still missing.
func TestCIBlockedSurvivesNoConsumerSource(t *testing.T) {
	t.Run("running consumer, no source answered", func(t *testing.T) {
		e := newCIRepo(t)
		e.breakingPayments(t)
		e.write(t, map[string]string{"web/pacto.yaml": consumerOf("web", e.host)})
		// The only consumer source reports itself partial, so none answered.
		body := "schemaVersion: pacto.dev/fleet-targets/v1\nstate: {status: partial}\ntargets:\n  - scope: prod\n    kind: k8s\n    name: web\n    service: web\n    compliance: Compliant\n"
		file := filepath.Join(t.TempDir(), "targets.yaml")
		writeCIFile(t, filepath.Dir(file), "targets.yaml", body)
		p, res := e.planWith(t, FleetOptions{TargetStateFiles: []string{file}})
		want := []ci.SourceLine{{Name: "target-state", Status: "partial"}}
		if r := row(t, p, "payments"); r.Impact.Text != "blocked (web)" || p.Exit != 1 || !slices.Equal(p.Snapshot.Sources, want) {
			t.Fatalf("row = %+v, snapshot %+v, result %+v", r, p.Snapshot, res["payments"])
		}
	})
	t.Run("repository-local policy referrer, no consumer source", func(t *testing.T) {
		e := newCIRepo(t)
		e.svc.BundleStore = oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host))
		e.policyFailure(t)
		p, res := e.planWith(t, FleetOptions{})
		// The repository alone finds the referrer and its failure; a policy
		// failure blocks only a referrer that runs on a known target, which
		// no source here can show, so the row names it and is never clear.
		got := res["policy"]
		if len(got.Consumers) != 1 || got.Consumers[0].Service != "web" || got.Consumers[0].CompatibilityVerdict != impact.CompatibilityIncompatible {
			t.Fatalf("consumers = %+v", got.Consumers)
		}
		r := row(t, p, "policy")
		if r.Classification != "BREAKING" || r.Impact.Gate != impact.GateUnverified || !strings.Contains(r.Impact.Text, "fails its new policy: web") || len(p.ConsumerSources) != 0 {
			t.Fatalf("row = %+v", r)
		}
	})
}
