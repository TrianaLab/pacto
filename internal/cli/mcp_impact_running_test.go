package cli

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

var (
	runDigest1 = "sha256:" + strings.Repeat("1", 64)
	runDigest2 = "sha256:" + strings.Repeat("2", 64)
	runNewRef  = "oci://reg.example/acme/payments@" + runDigest2
)

// runningPay is payments at version; 1.x has an interface 2.x drops, so
// judging a running 1.x against 2.x is BREAKING.
func runningPay(version string) *contract.Bundle {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: version}}
	if !strings.HasPrefix(version, "1.") {
		return &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
	}
	c.Interfaces = []contract.Interface{{Name: "api", Type: "openapi", Ref: "openapi.yaml"}}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{"openapi.yaml": {Data: []byte("openapi: 3.0.0\ninfo: {title: p, version: \"1\"}\npaths: {}\n")}}}
}

// runningSnap is a fleet where payments 1.0.0, published on runHost, runs on
// a target, payments 2.0.0 is published on reg.example and ledger, which runs
// too, depends on payments with ^1.
func runningSnap(t *testing.T, runHost string) *fleet.FleetSnapshot {
	t.Helper()
	now := time.Now()
	ledger := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://reg.example/acme/payments", Required: true, Compatibility: "^1"}}}
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: runningPay("1.0.0"), Domain: "reg.example/acme", ResolvedRef: "oci://" + runHost + "/acme/payments@" + runDigest1, Digest: runDigest1},
			{Bundle: runningPay("2.0.0"), Domain: "reg.example/acme", ResolvedRef: runNewRef, Digest: runDigest2},
			{Bundle: &contract.Bundle{Contract: ledger, FS: fstest.MapFS{}}, Domain: "reg.example/acme", Digest: "sha256:" + strings.Repeat("3", 64)},
		},
		Targets: []fleet.RawTarget{
			{Scope: "prod", Kind: "deployment", Name: "pay", Service: "payments", Domain: "reg.example/acme", Digest: runDigest1, Compliance: fleet.StatusCompliant, EvidenceAt: &now},
			{Scope: "prod", Kind: "deployment", Name: "ledger", Service: "ledger", Domain: "reg.example/acme", Digest: "sha256:" + strings.Repeat("3", 64), Compliance: fleet.StatusCompliant, EvidenceAt: &now},
		},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("mem", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// runningStore serves payments by the digest a ref carries and fails the
// test when asked for a ref on a host it must never reach.
func runningStore(t *testing.T, never string) *app.Service {
	t.Helper()
	return app.NewService(&testutil.MockBundleStore{PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
		switch {
		case never != "" && strings.Contains(ref, never):
			t.Errorf("pulled %s", ref)
		case strings.Contains(ref, runDigest1):
			return runningPay("1.0.0"), nil
		case strings.Contains(ref, runDigest2):
			return runningPay("2.0.0"), nil
		}
		return nil, errors.New("not served")
	}}, nil)
}

// runningProvider is the pacto_impact provider of a server over svc.
func runningProvider(svc *app.Service, addMissing, trusted bool) func(context.Context, *fleet.FleetSnapshot, pactomcp.ImpactRequest) (*impact.Result, error) {
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	// The snapshot each test passes is the server's whole fleet: no index
	// rebuilds it from the (empty) source flags.
	_ = cmd.Flags().Set("index", "none")
	return mcpImpactProvider(cmd, svc, defaultDeps(), addMissing, trusted)
}

// newPaymentsDir is a local payments folder at 2.0.0.
func newPaymentsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: payments, version: 2.0.0}\n"})
	return dir
}

func TestMCPImpactRunningMatchesCLI(t *testing.T) {
	t.Setenv("PACTO_RUNNING_REGISTRIES", "reg.example")
	snap, dir := runningSnap(t, "reg.example"), newPaymentsDir(t)
	svc := runningStore(t, "")
	got, err := runningProvider(svc, true, true)(context.Background(), snap, pactomcp.ImpactRequest{Running: true, NewRef: dir})
	if err != nil {
		t.Fatal(err)
	}
	want, err := svc.ImpactRunning(context.Background(), app.ImpactOptions{NewPath: dir, RunningRegistries: []string{"reg.example"}, Fleet: app.FleetOptions{Index: app.IndexNone}}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gate != impact.GateBlocked || got.Gate != want.Gate || got.GateReason != want.GateReason || !reflect.DeepEqual(got.Running, want.Running) ||
		got.Running.Judged != "oci://reg.example/acme/payments@"+runDigest1 {
		t.Fatalf("mcp %s %q %+v, cli %s %q %+v", got.Gate, got.GateReason, got.Running, want.Gate, want.GateReason, want.Running)
	}
}

func TestMCPImpactRunningRefusedWhenRestricted(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		svc, pulled := pullRecorder()
		_, err := runningProvider(svc, false, trusted)(context.Background(), runningSnap(t, "reg.example"), pactomcp.ImpactRequest{Running: true, NewRef: runNewRef})
		if !errors.Is(err, errRunningRestricted) || len(*pulled) != 0 {
			t.Errorf("trusted %v: %v (pulled %v)", trusted, err, *pulled)
		}
	}
}

func TestMCPImpactRunningHTTPNeedsRefInView(t *testing.T) {
	req, err := refuseUntrustedImpact(pactomcp.ImpactRequest{Running: true, NewRef: "oci://reg.example/orders:2"}, false, viewSnapshot())
	if err != nil || req.NewRef != "oci://reg.example/orders@sha256:"+strings.Repeat("b", 64) || req.OldRef != "" {
		t.Fatalf("visible ref: %+v %v", req, err)
	}
	if _, err := refuseUntrustedImpact(pactomcp.ImpactRequest{Running: true, NewRef: "oci://reg.example/ledger:2"}, false, viewSnapshot()); err == nil || err.Error() != "new_ref "+outsideView {
		t.Fatalf("hidden ref: %v", err)
	}
	if _, err := refuseUntrustedImpact(pactomcp.ImpactRequest{Running: true, NewRef: "./payments"}, false, viewSnapshot()); err == nil || !strings.Contains(err.Error(), "pass an oci:// reference") {
		t.Fatalf("local ref: %v", err)
	}
}

// TestMCPImpactRunningForeignHostIsUnread: a running revision on a host
// outside the allowlist is never pulled and withholds a clear, over stdio
// and over HTTP, where PACTO_RUNNING_REGISTRIES widens nothing.
func TestMCPImpactRunningForeignHostIsUnread(t *testing.T) {
	t.Setenv("PACTO_RUNNING_REGISTRIES", "")
	stdio, err := runningProvider(runningStore(t, "evil.example"), true, true)(context.Background(), runningSnap(t, "evil.example"),
		pactomcp.ImpactRequest{Running: true, NewRef: newPaymentsDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PACTO_RUNNING_REGISTRIES", "evil.example")
	http, err := runningProvider(runningStore(t, "evil.example"), true, false)(context.Background(), runningSnap(t, "evil.example"),
		pactomcp.ImpactRequest{Running: true, NewRef: runNewRef})
	if err != nil {
		t.Fatal(err)
	}
	for name, res := range map[string]*impact.Result{"stdio": stdio, "http": http} {
		if res.Gate == impact.GateClear || res.Running == nil || !slices.ContainsFunc(res.Running.Unread, func(s string) bool {
			return strings.Contains(s, "evil.example") && strings.HasSuffix(s, "registry not allowed")
		}) {
			t.Errorf("%s: gate %s %q running %+v", name, res.Gate, res.GateReason, res.Running)
		}
	}
}

// TestRunningRegistriesMatchCLIAndMCP: pacto mcp reads the running
// registries root read, the config file included, as pacto impact does.
func TestRunningRegistriesMatchCLIAndMCP(t *testing.T) {
	t.Setenv("PACTO_RUNNING_REGISTRIES", "")
	cfg := filepath.Join(t.TempDir(), "pacto.yaml")
	writeFiles(t, filepath.Dir(cfg), map[string]string{"pacto.yaml": "running-registries: cfg.example\n"})
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "dev"}, defaultDeps())
	_ = root.PersistentFlags().Set("config", cfg)
	for _, name := range []string{"impact", "mcp"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		cmd.SetContext(context.Background())
		if err := root.PersistentPreRunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
		if got := runningRegistries(cmd); !slices.Equal(got, []string{"cfg.example"}) {
			t.Errorf("%s: %v", name, got)
		}
	}
	t.Setenv("PACTO_RUNNING_REGISTRIES", "env.example")
	if got := runningRegistries(newMCPCommand(nil, "v", defaultDeps())); !slices.Equal(got, []string{"env.example"}) {
		t.Errorf("outside root: %v", got)
	}
}

// TestMCPImpactRunningLocalDomain: over stdio a local new_ref takes its
// domain from the snapshot as pacto impact --running does: a name two
// domains hold is refused, a name no domain holds is unverified.
func TestMCPImpactRunningLocalDomain(t *testing.T) {
	two := &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: runningPay("1.0.0"), Domain: "reg.example/acme", Digest: runDigest1},
		{Bundle: runningPay("1.0.0"), Domain: "other.example/team", Digest: runDigest2},
	}}
	ambiguous, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("mem", "memory", two))
	if err != nil {
		t.Fatal(err)
	}
	provide := runningProvider(runningStore(t, ""), true, true)
	if _, err := provide(context.Background(), ambiguous, pactomcp.ImpactRequest{Running: true, NewRef: newPaymentsDir(t)}); !errors.As(err, new(*fleet.AmbiguousError)) {
		t.Fatalf("ambiguous: %v", err)
	}
	res, err := provide(context.Background(), &fleet.FleetSnapshot{}, pactomcp.ImpactRequest{Running: true, NewRef: newPaymentsDir(t)})
	if err != nil || res.Gate != impact.GateUnverified || !strings.Contains(res.GateReason, "payments is in no fleet domain") {
		t.Fatalf("no domain: %+v %v", res, err)
	}
}
