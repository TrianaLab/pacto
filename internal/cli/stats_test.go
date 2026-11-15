package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

var statsTestClock = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

// statsRegistry starts a stats registry holding the named pkg/runrecord
// fixtures, fixes the stats clock and returns the repository path. Once
// setup is done, any write to the registry fails the test.
func statsRegistry(t *testing.T, fixtures ...string) string {
	t.Helper()
	return newStatsRegistry(t, true, fixtures...)
}

// newStatsRegistry is statsRegistry; sealed false allows writes after setup.
func newStatsRegistry(t *testing.T, seal bool, fixtures ...string) string {
	t.Helper()
	recs := make([]runrecord.Record, 0, len(fixtures))
	for _, name := range fixtures {
		recs = append(recs, fixtureRecord(t, name))
	}
	return statsRegistryOf(t, seal, recs...)
}

// fixtureRecord parses the named pkg/runrecord fixture.
func fixtureRecord(t *testing.T, name string) runrecord.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "pkg", "runrecord", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	r, err := runrecord.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// statsRegistryOf is newStatsRegistry over records.
func statsRegistryOf(t *testing.T, seal bool, recs ...runrecord.Record) string {
	t.Helper()
	var sealed atomic.Bool
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sealed.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead {
			t.Errorf("pacto stats wrote to the registry: %s %s", r.Method, r.URL.Path)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	path := host + "/acme/pacto-stats"
	repo, err := registry.Open(path, nil, []string{host})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if _, err := registry.Publish(t.Context(), repo, r); err != nil {
			t.Fatal(err)
		}
	}
	sealed.Store(seal)
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	t.Setenv("PACTO_STATS_REGISTRY", "")
	old := statsNow
	statsNow = func() time.Time { return statsTestClock }
	t.Cleanup(func() { statsNow = old })
	return path
}

func statsCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runRoot(t, defaultDeps(), nil, append([]string{"stats"}, args...)...)
}

var allFixtures = []string{"impact-blocked.json", "publish-refused.json", "operation.golden.json"}

func TestStatsCommandNeedsRegistry(t *testing.T) {
	statsRegistry(t)
	_, _, err := statsCmd(t)
	if err == nil || err.Error() != "no stats registry: pass --registry or set PACTO_STATS_REGISTRY" {
		t.Fatalf("err = %v", err)
	}
}

func decodeStats(t *testing.T, out string) stats.Stats {
	t.Helper()
	var st stats.Stats
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return st
}

func TestStatsCommandEnvFallback(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	t.Setenv("PACTO_STATS_REGISTRY", path)
	out, _, err := statsCmd(t, "--output-format", "json")
	if st := decodeStats(t, out); err != nil || st.Registry != path || st.Runs != 3 {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
}

func TestStatsCommandFlagBeatsEnv(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	t.Setenv("PACTO_STATS_REGISTRY", "127.0.0.1:1/nowhere")
	out, _, err := statsCmd(t, "--registry", path, "--output-format", "json")
	if st := decodeStats(t, out); err != nil || st.Registry != path {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
}

func TestStatsCommandJSON(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	out, _, err := statsCmd(t, "--registry", path, "--by", "service", "--output-format", "json")
	if err != nil {
		t.Fatal(err)
	}
	st := decodeStats(t, out)
	if st.Provenance != "unsigned statistics" || st.RecordsRead != 3 || st.Runs != 3 || st.DistinctChanges != 1 ||
		!st.AsOf.Equal(statsTestClock) || len(st.Groups) != 1 || st.Totals.BreakingCaught.Distinct != 1 || st.Totals.Gates.Blocked != 1 {
		t.Fatalf("stats = %s", out)
	}
}

func TestStatsCommandKindFilter(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	out, _, err := statsCmd(t, "--registry", path, "--kind", "impact,publish", "--kind", "diff", "--output-format", "json")
	if st := decodeStats(t, out); err != nil || st.Runs != 2 {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
}

func TestStatsCommandText(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	out, stderr, err := statsCmd(t, "--registry", path)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "stats.txt")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Fatalf("text output differs from %s:\n%s", golden, out)
	}
	if stderr != "" {
		t.Fatalf("a complete answer warned: %q", stderr)
	}
}

func TestStatsCommandTextSaysAttribution(t *testing.T) {
	path := statsRegistry(t, "impact-blocked.json")
	out, _, err := statsCmd(t, "--registry", path, "--by", "owner", "--local", t.TempDir())
	if err != nil || !strings.Contains(out, "unsigned statistics; owner inferred as of ") || !strings.Contains(out, "\nOWNER ") {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

func TestStatsCommandNoRecordsPrintsSentence(t *testing.T) {
	path := statsRegistry(t)
	out, stderr, err := statsCmd(t, "--registry", path)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing was ever published, so the repository itself is not found.
	if !strings.Contains(out, "the stats repository does not exist or holds no run records yet\n") ||
		strings.Contains(out, "BREAKING CAUGHT") || !strings.Contains(stderr, "answer is empty") {
		t.Fatalf("out = %q stderr = %q", out, stderr)
	}
}

func TestStatsCommandBuildsFleetOnlyForOwnerOrDomain(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	if _, _, err := statsCmd(t, "--registry", path, "--by", "service", "--mira-source", "ftp://x"); err != nil {
		t.Fatalf("--by service built the fleet: %v", err)
	}
	for _, by := range []string{"owner", "domain"} {
		if _, _, err := statsCmd(t, "--registry", path, "--by", by, "--mira-source", "ftp://x"); err == nil || !strings.Contains(err.Error(), `mira source: scheme "ftp"`) {
			t.Fatalf("--by %s: err = %v", by, err)
		}
	}
}

func TestStatsCommandRejectsBadBy(t *testing.T) {
	path := statsRegistry(t)
	_, _, err := statsCmd(t, "--registry", path, "--by", "team")
	if err == nil || err.Error() != `unsupported by "team": give one of service, owner, domain, repository, environment` {
		t.Fatalf("err = %v", err)
	}
}

func TestStatsCommandRejectsBadKind(t *testing.T) {
	path := statsRegistry(t)
	_, _, err := statsCmd(t, "--registry", path, "--kind", "impact, ,validate")
	if err == nil || err.Error() != `unsupported kind "validate": give one of diff, impact, publish, operation, coverage` {
		t.Fatalf("err = %v", err)
	}
}

func TestStatsCommandRejectsBadWindow(t *testing.T) {
	path := statsRegistry(t)
	if _, _, err := statsCmd(t, "--registry", path, "--since", "yesterday"); err == nil || !strings.Contains(err.Error(), `since "yesterday"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatsCommandReadError(t *testing.T) {
	statsRegistry(t)
	t.Setenv("PACTO_INSECURE_REGISTRIES", "")
	if _, _, err := statsCmd(t, "--registry", "Not A Reference"); err == nil || !strings.Contains(err.Error(), "reading run records from Not A Reference") {
		t.Fatalf("err = %v", err)
	}
}

func TestStatsCommandIsReadOnly(t *testing.T) {
	// statsRegistry fails the test on any write after setup.
	path := statsRegistry(t, allFixtures...)
	for _, args := range [][]string{{}, {"--by", "environment"}, {"--service", "payments", "--output-format", "json"}} {
		if _, _, err := statsCmd(t, append([]string{"--registry", path}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStatsPublishStillWorks(t *testing.T) {
	path := newStatsRegistry(t, false)
	file := fixture(t, "impact-blocked.json")
	out, _, err := runRoot(t, defaultDeps(), nil, "stats", "--registry", path, "publish", file)
	if err != nil || !strings.HasPrefix(out, "published "+file+" (impact payments ") {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

func TestStatsRatioAndLeadText(t *testing.T) {
	seconds := int64(90)
	for got, want := range map[string]string{
		ratioText(stats.Ratio{Count: 3, Of: 41}):                  "3/41 (7%)",
		ratioText(stats.Ratio{Unknown: true}):                     "unknown",
		leadText(stats.LeadTime{Joined: 1}, &seconds):             "1m30s",
		leadText(stats.LeadTime{Unjoined: 2, Unknown: true}, nil): "unknown (2 unjoined)",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// Records name the contract's service.name, so a fleet key resolves to it
// and, with no snapshot to resolve it, is refused rather than read as an
// anchor that holds nothing.
func TestStatsServiceResolvesFleetKey(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "orders", Version: "1.0.0"}}
	src := fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:shoporders", Domain: "shop"},
	}})
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	full := fleet.NewQuery(snap)
	for in, want := range map[string]string{"orders": "orders", "shop/orders": "orders", "": ""} {
		if got, err := statsService(in, full); err != nil || got != want {
			t.Fatalf("%q = %q, %v", in, got, err)
		}
	}
	if _, err := statsService("payments/orders", full); err == nil {
		t.Fatal("an unknown key resolved")
	}
	if got, err := statsService("orders", nil); err != nil || got != "orders" {
		t.Fatalf("bare name without a snapshot = %q, %v", got, err)
	}
	if _, err := statsService("shop/orders", nil); err == nil || !strings.Contains(err.Error(), "service.name") {
		t.Fatalf("key without a snapshot: %v", err)
	}
	statsRegistry(t)
	if _, _, err := statsCmd(t, "--registry", "r.example/s", "--service", "shop/orders"); err == nil || !strings.Contains(err.Error(), "fleet key") {
		t.Fatalf("CLI key without a snapshot: %v", err)
	}
}

func TestStatsCommandTextShowsCoverage(t *testing.T) {
	path := statsRegistry(t, "impact-blocked.json", "coverage.golden.json")
	out, _, err := statsCmd(t, "--registry", path)
	if err != nil || !strings.Contains(out, "\ncoverage 12 services, 5 described, 8 observed, 3 unknown as of 2026-11-13T08:59:00Z (partial)\n") {
		t.Fatalf("out = %q err = %v", out, err)
	}
	only, _, err := statsCmd(t, "--registry", path, "--kind", "coverage", "--output-format", "json")
	if st := decodeStats(t, only); err != nil || st.Runs != 0 || st.Totals != nil || st.Coverage == nil || st.Coverage.Services != 12 {
		t.Fatalf("--kind coverage = %s err = %v", only, err)
	}
	cov := fixtureRecord(t, "coverage.golden.json")
	cov.Coverage.Knowledge = nil
	if got := coverageText(cov.Coverage); !strings.Contains(got, "12 services, unknown described, unknown observed as of") || strings.Contains(got, " 0 ") {
		t.Fatalf("a missing tally prints %q", got)
	}
}
