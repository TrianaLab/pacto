package release

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// legShard is one dashboard-e2e matrix entry.
type legShard struct {
	Leg   string `yaml:"leg"`
	Shard int    `yaml:"shard"`
	Of    int    `yaml:"of"`
}

// TestDashboardE2EShardsEveryLegOnce: dashboard-e2e shards the browser suite by
// weight, as two legs (PW_LEG in playwright.config.ts): the layout matrix and the
// rest. A leg missing from the matrix, or a leg whose shards do not cover 1..n,
// silently stops running part of the suite while the job stays green.
func TestDashboardE2EShardsEveryLegOnce(t *testing.T) {
	root := repoRoot(t)
	var wf struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []legShard `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []wfStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github", "workflows", "ci.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["dashboard-e2e"]
	if !ok {
		t.Fatal("ci.yml has no dashboard-e2e job")
	}

	checkLegShards(t, job.Strategy.Matrix.Include)

	run := ""
	for _, s := range job.Steps {
		run += s.Run
	}
	if !strings.Contains(run, "PW_LEG=${{ matrix.leg }}") || !strings.Contains(run, "PW_SHARD=${{ matrix.shard }}/${{ matrix.of }}") {
		t.Errorf("dashboard-e2e does not pass the matrix leg and shard to make test-browser: %q", run)
	}

	// The legs must partition the suite: layout selects exactly what rest ignores.
	cfg := readFile(t, root, "pkg", "dashboard", "frontend", "playwright.config.ts")
	for _, want := range []string{"testMatch: LAYOUT", "[MOBILE, LAYOUT]"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("playwright.config.ts lost %q, so the layout and rest legs may no longer partition the suite", want)
		}
	}
}

// checkLegShards: the matrix has exactly the layout and rest legs, each running
// shards 1..n once under one shard total n.
func checkLegShards(t *testing.T, include []legShard) {
	t.Helper()
	shards := map[string][]int{}
	of := map[string]int{}
	for _, e := range include {
		shards[e.Leg] = append(shards[e.Leg], e.Shard)
		if of[e.Leg] != 0 && of[e.Leg] != e.Of {
			t.Errorf("leg %s mixes shard totals %d and %d", e.Leg, of[e.Leg], e.Of)
		}
		of[e.Leg] = e.Of
	}
	for _, leg := range []string{"layout", "rest"} {
		got := slices.Sorted(slices.Values(shards[leg]))
		want := make([]int, of[leg])
		for i := range want {
			want[i] = i + 1
		}
		if len(got) == 0 || !slices.Equal(got, want) {
			t.Errorf("leg %s runs shards %v of %d; want exactly 1..%d once each", leg, got, of[leg], of[leg])
		}
	}
	if len(shards) != 2 {
		t.Errorf("dashboard-e2e legs %v; want exactly layout and rest, the two halves PW_LEG defines", slices.Sorted(maps.Keys(shards)))
	}
}
