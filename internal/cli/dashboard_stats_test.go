package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// countingCache is a statsCache over a read that counts its calls, on a
// clock the test moves.
func countingCache(err error) (*statsCache, *atomic.Int64, *time.Time) {
	var calls atomic.Int64
	now := time.Unix(1000, 0)
	c := &statsCache{now: func() time.Time { return now }, read: func(context.Context) (stats.RecordSet, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond) // long enough for the concurrent gets to join
		return stats.RecordSet{Truncated: "records"}, err
	}}
	return c, &calls, &now
}

func TestStatsCacheSharesOneRead(t *testing.T) {
	c, calls, _ := countingCache(nil)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if set, err := c.get(t.Context()); err != nil || set.Truncated != "records" {
				t.Errorf("set = %+v err = %v", set, err)
			}
		})
	}
	wg.Wait()
	expect(t, calls.Load() == 1, "10 concurrent gets read %d times", calls.Load())
}

func TestStatsCacheExpires(t *testing.T) {
	c, calls, now := countingCache(nil)
	_, _ = c.get(t.Context())
	*now = now.Add(statsTTL - time.Second)
	_, _ = c.get(t.Context())
	expect(t, calls.Load() == 1, "a read within statsTTL is served: %d", calls.Load())
	*now = now.Add(time.Second)
	_, _ = c.get(t.Context())
	expect(t, calls.Load() == 2, "a read past statsTTL reads again: %d", calls.Load())
}

func TestStatsCacheKeepsFailureBriefly(t *testing.T) {
	c, calls, now := countingCache(errors.New("registry down"))
	if _, err := c.get(t.Context()); err == nil {
		t.Fatal("the failure is returned")
	}
	*now = now.Add(failureTTL - time.Second)
	if _, err := c.get(t.Context()); err == nil || calls.Load() != 1 {
		t.Fatalf("a failure is kept within failureTTL: err = %v, reads %d", err, calls.Load())
	}
	*now = now.Add(time.Second)
	_, _ = c.get(t.Context())
	expect(t, calls.Load() == 2, "a failure is asked again after failureTTL: %d", calls.Load())
}

func TestStatsCacheDetachedFromRequest(t *testing.T) {
	c := &statsCache{now: time.Now, read: func(ctx context.Context) (stats.RecordSet, error) { return stats.RecordSet{}, ctx.Err() }}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.get(ctx)
	expect(t, err == nil, "a closed tab does not fail the read every viewer waits on: %v", err)
}

// Each set of services read has its own cached read: a restricted view never
// shares, or depends on, the read of every record.
func TestStatsCachesKeyedByServices(t *testing.T) {
	var reads []string
	c := &statsCaches{now: time.Now, byKey: map[string]*statsCache{}, read: func(_ context.Context, services []string) (stats.RecordSet, error) {
		reads = append(reads, fmt.Sprint(services == nil, services))
		if services == nil {
			return stats.RecordSet{}, errors.New("a hidden anchor failed")
		}
		return stats.RecordSet{Truncated: strings.Join(services, ",")}, nil
	}}
	if _, err := c.get(t.Context(), nil); err == nil {
		t.Fatal("the whole read's failure is returned")
	}
	set, err := c.get(t.Context(), []string{"orders"})
	expect(t, err == nil && set.Truncated == "orders", "a view's read is its own: %+v %v", set, err)
	_, _ = c.get(t.Context(), []string{"orders"})
	expect(t, len(reads) == 2, "one read per set: %v", reads)
	for i := range maxStatsReads {
		_, _ = c.get(t.Context(), []string{fmt.Sprint(i)})
	}
	expect(t, len(c.byKey) == maxStatsReads && len(c.order) == maxStatsReads, "kept %d reads", len(c.byKey))
	_, _ = c.get(t.Context(), nil)
	expect(t, len(reads) == 2+maxStatsReads+1, "an evicted read is read again: %v", reads)
}

// statsFlagCmd is a command with the dashboard's --stats-registry flag,
// parsed from args.
func statsFlagCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := newDashboardCommand(app.NewService(nil, nil), viper.New(), "test", defaultDeps())
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestStatsRegistryFlagBeatsEnv(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "env.example/stats")
	read, repo := dashboardStats(statsFlagCmd(t, "--stats-registry", "flag.example/stats"), app.NewService(nil, nil))
	expect(t, read != nil && repo == "flag.example/stats", "repo = %q", repo)
}

func TestStatsRegistryEnvFallback(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "env.example/stats")
	_, repo := dashboardStats(statsFlagCmd(t), app.NewService(nil, nil))
	expect(t, repo == "env.example/stats", "repo = %q", repo)
	t.Setenv("PACTO_STATS_REGISTRY", "")
	read, repo := dashboardStats(statsFlagCmd(t), app.NewService(nil, nil))
	expect(t, read == nil && repo == "", "no registry, no Metrics reader: %q", repo)
}

func TestDashboardStatsNotViperBound(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "")
	file := filepath.Join(t.TempDir(), "pacto.yaml")
	if err := os.WriteFile(file, []byte("stats-registry: contract.example/stats\ndashboard:\n  stats-registry: contract.example/stats\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	cmd := newDashboardCommand(app.NewService(nil, nil), v, "test", defaultDeps())
	if err := cmd.ParseFlags([]string{"--stats-registry", "flag.example/stats"}); err != nil {
		t.Fatal(err)
	}
	expect(t, !v.IsSet("dashboard.stats-registry"), "the flag binds no viper key")
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	cmd = newDashboardCommand(app.NewService(nil, nil), v, "test", defaultDeps())
	_, repo := dashboardStats(cmd, app.NewService(nil, nil))
	expect(t, repo == "", "a ./pacto.yaml key changes nothing: %q", repo)
}

// statsAPI is the dashboard's API over the stats repository repo, through
// the reader pacto dashboard builds, on the clock now.
func statsAPI(t *testing.T, repo string, now time.Time) http.Handler {
	t.Helper()
	t.Setenv("PACTO_STATS_REGISTRY", repo)
	read, reg := dashboardStats(statsFlagCmd(t), app.NewService(nil, nil))
	mux := http.NewServeMux()
	dashboard.NewServer(dashboard.Options{Stats: read, StatsRegistry: reg, Now: func() time.Time { return now }}).
		RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	return mux
}

func getStatsAnswer(t *testing.T, h http.Handler, target string) (int, stats.Stats) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var st stats.Stats
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("%v\n%s", err, rec.Body.String())
		}
	}
	return rec.Code, st
}

// The registry fails the test on any write after setup (statsRegistryOf).
func TestDashboardStatsIsReadOnly(t *testing.T) {
	h := statsAPI(t, statsRegistry(t, allFixtures...), statsTestClock)
	for _, target := range []string{"/api/stats", "/api/stats?window=90d", "/api/stats/records?metric=breaking"} {
		code, _ := getStatsAnswer(t, h, target)
		expect(t, code == http.StatusOK, "%s answered %d", target, code)
	}
}

// The test registry asks for no credentials: a public repository answers.
func TestDashboardStatsAnonymousPublicRepository(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	t.Setenv("PACTO_REGISTRY_USERNAME", "")
	t.Setenv("PACTO_REGISTRY_PASSWORD", "")
	t.Setenv("PACTO_REGISTRY_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	code, st := getStatsAnswer(t, statsAPI(t, path, statsTestClock), "/api/stats")
	expect(t, code == http.StatusOK && st.RecordsRead == 3 && st.Registry == path, "code %d stats %+v", code, st)
}

// For one record set and one clock, the Metrics page's totals are pacto
// stats' totals.
func TestStatsDashboardMatchesCLI(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	t.Setenv("PACTO_STATS_REGISTRY", path)
	out, _, err := statsCmd(t, "--since", "30d", "--output-format", "json")
	if err != nil {
		t.Fatal(err)
	}
	cli := decodeStats(t, out)
	code, dash := getStatsAnswer(t, statsAPI(t, path, statsTestClock), "/api/stats?window=30d")
	if code != http.StatusOK || cli.Totals == nil {
		t.Fatalf("code %d, cli totals %+v", code, cli.Totals)
	}
	want, _ := json.Marshal(cli.Totals)
	got, _ := json.Marshal(dash.Totals)
	t.Logf("totals: %s", got)
	if string(want) != string(got) || cli.Runs != dash.Runs || cli.DistinctChanges != dash.DistinctChanges ||
		cli.RecordsRead != dash.RecordsRead || cli.Completeness != dash.Completeness || !cli.Window.Since.Equal(dash.Window.Since) {
		t.Fatalf("pacto stats:\n%s\n/api/stats:\n%s", out, got)
	}
}

// The Metrics page reviews operation records in the sign-in cluster alone:
// its cached id is the one cluster records may come from, and an id that
// cannot be read leaves none, so every record naming a cluster is hidden.
func TestDashboardOperationAccessHomeCluster(t *testing.T) {
	id, full, _ := operationAccessWorld(t)
	id.homeUID = func(context.Context) (string, bool) { return "c1", true }
	access, err := id.operationAccess(full)(t.Context(), []string{"shop"})
	expect(t, err == nil && access.Visible("shop") && slices.Equal(access.Clusters, []string{"c1"}), "home known: %+v %v", access, err)
	id.homeUID = func(context.Context) (string, bool) { return "", false }
	access, err = id.operationAccess(full)(t.Context(), []string{"shop"})
	expect(t, err == nil && access.Visible("shop") && access.Clusters != nil && len(access.Clusters) == 0, "home unknown: %+v %v", access, err)

	pairs := []fleet.ClusterNamespace{{Cluster: "c1", Namespace: "shop"}, {Cluster: "c2", Namespace: "vault"}, {Namespace: "plain"}, {Cluster: "c1", Namespace: "shop"}}
	for _, tc := range []struct {
		home           func(context.Context) (string, bool)
		known, cluster []string
	}{
		{func(context.Context) (string, bool) { return "c1", true }, []string{"plain", "shop"}, []string{"c1"}},
		{func(context.Context) (string, bool) { return "c2", false }, []string{"plain"}, []string{}},
		{nil, []string{"plain"}, []string{}},
	} {
		known, clusters := homeNamespaces(t.Context(), tc.home, pairs)
		expect(t, slices.Equal(known, tc.known) && slices.Equal(clusters, tc.cluster) && clusters != nil, "known %v clusters %v, want %v %v", known, clusters, tc.known, tc.cluster)
	}
}
