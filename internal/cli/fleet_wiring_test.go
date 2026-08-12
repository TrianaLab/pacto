package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/dashboard"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// TestBuildMCPServer_WithFleet covers the --fleet branch of buildMCPServer: no
// bundle ref, fleet enabled, so a fleet-backed server is returned.
func TestBuildMCPServer_WithFleet(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("fleet", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("local", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	server, err := buildMCPServer(cmd, svc, "v", nil)
	if err != nil {
		t.Fatalf("buildMCPServer --fleet: %v", err)
	}
	if server == nil {
		t.Fatal("expected a fleet MCP server")
	}

	// A cancelled context makes the fleet snapshot build fail, exercising the
	// error branch of the --fleet path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	if _, err := buildMCPServer(cmd, svc, "v", nil); err == nil {
		t.Fatal("buildMCPServer --fleet with a cancelled context: expected an error")
	}
}

// TestMCPImpactProvider exercises the pacto_impact provider closure wired into
// the MCP fleet server: a nonexistent old revision makes svc.Impact fail, which
// covers the closure body and its fleet-options capture.
func TestMCPImpactProvider(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	provide := mcpImpactProvider(cmd, svc)
	if _, err := provide(context.Background(), "/nonexistent/old", "/nonexistent/new", true, ""); err == nil {
		t.Fatal("expected an error resolving nonexistent revisions")
	}
	// A nonexistent traces path is a read error before any resolution.
	if _, err := provide(context.Background(), "/old", "/new", false, "/nonexistent/traces.json"); err == nil {
		t.Fatal("expected a traces read error")
	}
	// A readable traces file is consumed (implying include-observed), then the
	// nonexistent revisions make Impact fail — covering the read-success path.
	tf := filepath.Join(t.TempDir(), "traces.json")
	if err := os.WriteFile(tf, []byte(`{"resourceSpans":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provide(context.Background(), "/nonexistent/old", "/nonexistent/new", false, tf); err == nil {
		t.Fatal("expected a resolution error after reading traces")
	}
}

// writeCLIBundle writes a minimal valid bundle so old/new/fleet revisions resolve
// as local paths in the wiring tests.
func writeCLIBundle(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestImpactProviderForFleet exercises the dashboard /api/fleet/impact provider
// closure: the resolve-error branch, and (section 2.2) that a successful impact answer
// binds to the Manager's PUBLISHED snapshot — same snapshotId the fleet endpoints
// serve, never a rebuilt one.
func TestImpactProviderForFleet(t *testing.T) {
	svc := app.NewService(nil, nil)
	fleetRoot := t.TempDir()
	writeCLIBundle(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")
	mgr := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{fleetRoot}})
	}, fleet.ManagerOptions{})
	provide := impactProviderForFleet(svc, mgr)

	if _, err := provide(context.Background(), "/nonexistent/old", "/nonexistent/new", false); err == nil {
		t.Fatal("expected an error resolving nonexistent revisions")
	}

	oldDir, newDir := t.TempDir(), t.TempDir()
	writeCLIBundle(t, oldDir, "orders", "1.0.0")
	writeCLIBundle(t, newDir, "orders", "2.0.0")
	res, err := provide(context.Background(), oldDir, newDir, false)
	if err != nil {
		t.Fatalf("impact against the published snapshot: %v", err)
	}
	fleetQ, err := managerFleetProvider(mgr)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.SnapshotID == "" || res.SnapshotID != fleetQ.SnapshotID() {
		t.Errorf("impact snapshotId %q must equal the dashboard's published snapshot %q", res.SnapshotID, fleetQ.SnapshotID())
	}

	// When the published snapshot is unavailable (first build fails on a cancelled
	// context), the provider surfaces that error rather than rebuilding.
	mgr2 := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{fleetRoot}})
	}, fleet.ManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := impactProviderForFleet(svc, mgr2)(ctx, oldDir, newDir, false); err == nil {
		t.Fatal("expected an error when the published snapshot is unavailable")
	}
}

// TestDashboardFleetOptions maps detected sources onto fleet options.
func TestDashboardFleetOptions(t *testing.T) {
	// Unset (empty) env → no evidence source added, unconfigured stays disabled.
	t.Setenv("PACTO_EVIDENCE_SOURCE_URL", "")
	// No sources -> disabled.
	if _, ok := dashboardFleetOptions("", nil, "", nil, &dashboard.DetectResult{}); ok {
		t.Error("expected fleet disabled with no sources")
	}
	// Every source active.
	dr := &dashboard.DetectResult{
		Local: &dashboard.LocalSource{},
		OCI:   &dashboard.OCISource{},
		Cache: &dashboard.CacheSource{},
		K8s:   &dashboard.K8sSource{},
	}
	fopts, ok := dashboardFleetOptions("./svc", []string{"ghcr.io/x/a"}, "prod", nil, dr)
	if !ok {
		t.Fatal("expected fleet enabled")
	}
	if len(fopts.LocalRoots) != 1 || fopts.LocalRoots[0] != "./svc" {
		t.Errorf("LocalRoots = %v", fopts.LocalRoots)
	}
	if len(fopts.OCIRefs) != 1 || fopts.OCIRefs[0] != "ghcr.io/x/a" {
		t.Errorf("OCIRefs = %v", fopts.OCIRefs)
	}
	if !fopts.IncludeCache || !fopts.IncludeK8s || fopts.K8sNamespace != "prod" {
		t.Errorf("cache/k8s wiring wrong: %+v", fopts)
	}
	if len(fopts.EvidenceURLs) != 0 {
		t.Errorf("expected no evidence URLs with env unset, got %v", fopts.EvidenceURLs)
	}
}

// TestDashboardFleetOptions_EvidenceURL proves the operator-wired env var adds a
// read-only evidence source and enables the fleet even with no other source.
func TestDashboardFleetOptions_EvidenceURL(t *testing.T) {
	t.Setenv("PACTO_EVIDENCE_SOURCE_URL", "http://evidence.internal:8080")
	fopts, ok := dashboardFleetOptions("", nil, "", nil, &dashboard.DetectResult{})
	if !ok {
		t.Fatal("expected fleet enabled by the evidence env var alone")
	}
	if len(fopts.EvidenceURLs) != 1 || fopts.EvidenceURLs[0] != "http://evidence.internal:8080" {
		t.Errorf("EvidenceURLs = %v", fopts.EvidenceURLs)
	}
}

// TestDashboardFleetOptions_Traces proves --traces / PACTO_DASHBOARD_TRACES wires
// an observation source into the normal dashboard and enables the fleet alone.
// The path-only form keeps its ad-hoc positional ids — that compatibility is the
// point, so a one-off command line never has to invent persistent identities.
func TestDashboardFleetOptions_Traces(t *testing.T) {
	t.Setenv("PACTO_EVIDENCE_SOURCE_URL", "")
	obs, err := observationSources([]string{"/tmp/a.json", "/tmp/b.json"}, nil)
	if err != nil {
		t.Fatalf("observationSources: %v", err)
	}
	fopts, ok := dashboardFleetOptions("", nil, "", obs, &dashboard.DetectResult{})
	if !ok {
		t.Fatal("expected fleet enabled by trace files alone")
	}
	want := []app.ObservationSourceSpec{
		{ID: "observation-1", Path: "/tmp/a.json"},
		{ID: "observation-2", Path: "/tmp/b.json"},
	}
	if !reflect.DeepEqual(fopts.ObservationSources, want) {
		t.Errorf("ObservationSources = %+v, want %+v", fopts.ObservationSources, want)
	}
}

// TestObservationSources_IdentitySurvivesReordering is the identity counterexample
// a declarative configuration must not fail: two named trace sources swap places
// and keep their ids, because the id is declared, not derived from list position.
func TestObservationSources_IdentitySurvivesReordering(t *testing.T) {
	forward, err := observationSources(nil, []string{"a=/mnt/a/traces.json", "b=/mnt/b/traces.json"})
	if err != nil {
		t.Fatalf("observationSources: %v", err)
	}
	reversed, err := observationSources(nil, []string{"b=/mnt/b/traces.json", "a=/mnt/a/traces.json"})
	if err != nil {
		t.Fatalf("observationSources reversed: %v", err)
	}
	byID := func(specs []app.ObservationSourceSpec) map[string]string {
		m := map[string]string{}
		for _, s := range specs {
			m[s.ID] = filepath.Join(s.Root, s.Path)
		}
		return m
	}
	if !reflect.DeepEqual(byID(forward), byID(reversed)) {
		t.Errorf("reordering rewrote identity: %+v vs %+v", forward, reversed)
	}
	if got := byID(forward)["a"]; got != "/mnt/a/traces.json" {
		t.Errorf("source a = %q", got)
	}
}

// TestObservationSources_SameBasenameStaysTwoSources proves identity does not come
// from the filesystem: two sources whose files share a basename are two Data
// Sources, not one.
func TestObservationSources_SameBasenameStaysTwoSources(t *testing.T) {
	got, err := observationSources(nil, []string{"eu=/mnt/eu/traces.json", "us=/mnt/us/traces.json"})
	if err != nil {
		t.Fatalf("observationSources: %v", err)
	}
	// Each source reads inside its own mount, so the two identical file names stay
	// two sources rather than one path shared by both.
	want := []app.ObservationSourceSpec{
		{ID: "eu", Root: "/mnt/eu", Path: "traces.json"},
		{ID: "us", Root: "/mnt/us", Path: "traces.json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %+v, want %+v", got, want)
	}
}

// TestObservationSources_DeclaresTheFilesOwnDirectoryAsItsRoot pins the rule the
// operator's mount layout depends on: a named source may read inside the
// directory its file sits in, and the root plus the file recompose exactly the
// path that was configured. Nothing else in the container is reachable, whatever
// the mounted volume happens to contain.
func TestObservationSources_DeclaresTheFilesOwnDirectoryAsItsRoot(t *testing.T) {
	const path = "/var/lib/pacto/observation/orders/traces.json"
	got, err := observationSources(nil, []string{"orders=" + path})
	if err != nil {
		t.Fatalf("observationSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sources = %+v, want 1", got)
	}
	if got[0].Root != "/var/lib/pacto/observation/orders" {
		t.Errorf("root = %q, want the file's own mount directory", got[0].Root)
	}
	if joined := filepath.Join(got[0].Root, got[0].Path); joined != path {
		t.Errorf("root+path = %q, want the configured %q", joined, path)
	}
}

// TestObservationSources_AdHocTracesDeclareNoRoot keeps the command line working
// as a command line: a path a person typed names whatever it names, including a
// symlink, because there is no declared mount for it to escape from.
func TestObservationSources_AdHocTracesDeclareNoRoot(t *testing.T) {
	got, err := observationSources([]string{"/tmp/a.json"}, nil)
	if err != nil {
		t.Fatalf("observationSources: %v", err)
	}
	if len(got) != 1 || got[0].Root != "" || got[0].Path != "/tmp/a.json" {
		t.Errorf("sources = %+v, want an unrooted /tmp/a.json", got)
	}
}

// TestObservationSources_Rejects covers the configuration counterexamples: a
// duplicate identity (including one colliding with a positional --traces id) and
// the malformed NAME=PATH forms. None of them may be silently repaired.
func TestObservationSources_Rejects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		traces []string
		named  []string
		want   string
	}{
		{"duplicate named", nil, []string{"a=/x.json", "a=/y.json"}, `duplicate observation source name "a"`},
		{"collides with positional", []string{"/x.json"}, []string{"observation=/y.json"}, `duplicate observation source name "observation"`},
		{"no separator", nil, []string{"/x.json"}, `invalid --trace-source "/x.json"`},
		{"empty name", nil, []string{"=/x.json"}, `invalid --trace-source "=/x.json"`},
		{"empty path", nil, []string{"a="}, `invalid --trace-source "a="`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := observationSources(tc.traces, tc.named)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestObservationSources_PathMayContainEquals proves the NAME=PATH split is on the
// FIRST separator, so a path carrying an "=" is configuration, not a parse error.
func TestObservationSources_PathMayContainEquals(t *testing.T) {
	got, err := observationSources(nil, []string{"a=/mnt/x=y/traces.json"})
	if err != nil {
		t.Fatalf("observationSources: %v", err)
	}
	if len(got) != 1 || got[0].Root != "/mnt/x=y" || got[0].Path != "traces.json" {
		t.Errorf("sources = %+v", got)
	}
}

// TestManagerFleetProvider covers the dashboard's Manager-backed fleet provider:
// the lazy first build (ErrNoSnapshot -> refresh -> query) and the served-cached
// path, plus the refresh-error path on a cancelled context.
func TestManagerFleetProvider(t *testing.T) {
	svc := app.NewService(nil, nil)
	dir := t.TempDir()
	mgr := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{dir}})
	}, fleet.ManagerOptions{})
	provider := managerFleetProvider(mgr)

	// First call: no snapshot yet -> coalesced build then query.
	q, err := provider(context.Background())
	if err != nil || q == nil {
		t.Fatalf("first call: q=%v err=%v", q, err)
	}
	// Second call: served from the published snapshot.
	q2, err := provider(context.Background())
	if err != nil || q2.SnapshotID() != q.SnapshotID() {
		t.Fatalf("second call should serve the same snapshot: %v", err)
	}

	// A fresh manager whose first build is triggered with a cancelled context
	// returns the refresh error.
	mgr2 := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{dir}})
	}, fleet.ManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := managerFleetProvider(mgr2)(ctx); err == nil {
		t.Fatal("cancelled first build should return an error")
	}
}

// TestClusterContractRefs proves the callback exists only when a cluster was
// detected: with no Kubernetes source there is nothing to ask.
func TestClusterContractRefs(t *testing.T) {
	if got := clusterContractRefs(&dashboard.DetectResult{}); got != nil {
		t.Error("expected no callback without a Kubernetes source")
	}
	if got := clusterContractRefs(&dashboard.DetectResult{K8s: &dashboard.K8sSource{}}); got == nil {
		t.Error("expected a callback when a Kubernetes source was detected")
	}
}

// TestWithClusterContractRefs proves each refresh asks the cluster again and
// merges what it reports behind the explicitly configured refs, without
// duplicating a ref both already name.
func TestWithClusterContractRefs(t *testing.T) {
	base := app.FleetOptions{OCIRefs: []string{"ghcr.io/x/a", "reg.svc:5000/demo/orders"}}
	ctx := context.Background()

	// No callback at all, and a cluster that reports nothing, both leave the
	// configured refs exactly as they are.
	for name, discover := range map[string]func(context.Context) []string{
		"no cluster":    nil,
		"empty cluster": func(context.Context) []string { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			got := withClusterContractRefs(ctx, base, discover)
			if !reflect.DeepEqual(got.OCIRefs, base.OCIRefs) {
				t.Errorf("OCIRefs = %v, want %v", got.OCIRefs, base.OCIRefs)
			}
		})
	}

	calls := 0
	discover := func(context.Context) []string {
		calls++
		return []string{"reg.svc:5000/demo/orders", "reg.svc:5000/demo/checkout@sha256:abc"}
	}
	got := withClusterContractRefs(ctx, base, discover)
	want := []string{
		"ghcr.io/x/a",
		"reg.svc:5000/demo/orders",
		"reg.svc:5000/demo/checkout@sha256:abc",
	}
	if !reflect.DeepEqual(got.OCIRefs, want) {
		t.Errorf("OCIRefs = %v, want %v", got.OCIRefs, want)
	}
	if base.OCIRefs[0] != "ghcr.io/x/a" || len(base.OCIRefs) != 2 {
		t.Errorf("the configured options were mutated: %v", base.OCIRefs)
	}
	// A second refresh re-asks rather than reusing the first answer.
	withClusterContractRefs(ctx, base, discover)
	if calls != 2 {
		t.Errorf("discover called %d times, want 2", calls)
	}
}
