package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// ordersStore serves orders 1.0.0 (a service) and 2.0.0 (a job: a BREAKING
// change) at any oci:// ref ending in their version or its digest.
func ordersStore(t *testing.T) oci.BundleStore {
	t.Helper()
	bundles := map[string]*contract.Bundle{}
	for v, w := range map[string]string{"1.0.0": "service", "2.0.0": "job"} {
		dir := t.TempDir()
		testutil.WriteOrders(t, dir, v, w)
		b, err := app.NewService(nil, nil).ResolveBundle(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		bundles[v] = b
	}
	version := func(ref string) string {
		for v := range bundles {
			if strings.HasSuffix(ref, ":"+v) || strings.HasSuffix(ref, digest.FromString(v).String()) {
				return v
			}
		}
		return ""
	}
	return &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) {
			return digest.FromString(version(ref)).String(), nil
		},
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if b := bundles[version(ref)]; b != nil {
				return b, nil
			}
			return nil, &oci.ArtifactNotFoundError{Ref: ref}
		},
	}
}

// webappTargets publishes the snapshot of cluster c-6f1e, which runs webapp
// depending on orders at <host>/acme with compat, and returns the registry
// host and a trust config with the key scoped to that cluster as ci-demo.
func webappTargets(t *testing.T, compat string) (host, trust string) {
	t.Helper()
	t.Setenv(app.TrustEnv, "")
	host, key, _ := targetsEnv(t)
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "shop", "webapp"
	p.Spec.Target = &delivery.PactoTargetView{ServiceName: "webapp"}
	p.Status.StatusFormat = delivery.StatusFormatGuarantees
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", time.Now().UTC().Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: "webapp", Version: "1.0.0", ResolvedRef: "oci://" + host + "/acme/webapp@sha256:" + strings.Repeat("b", 64)}
	p.Status.Dependencies = []delivery.DependencyView{{Name: "orders", Ref: "oci://" + host + "/acme/orders", Required: true, Compatibility: compat}}
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, map[string]any{"pactos": []delivery.PactoView{p}}), "--registry", host+"/acme", "--key", key); err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(key + ".pub.pem")
	if err != nil {
		t.Fatal(err)
	}
	trust = filepath.Join(t.TempDir(), "trust.yaml")
	doc := "keys:\n- name: ci-demo\n  streams: [\"c-6f1e/*/*\"]\n  publicKey: |\n    " + strings.ReplaceAll(strings.TrimSpace(string(pub)), "\n", "\n    ") + "\n"
	if err := os.WriteFile(trust, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return host, trust
}

// runImpact runs pacto impact orders 1.0.0 -> 2.0.0 at host with args.
func runImpact(t *testing.T, host string, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCommand(app.NewService(ordersStore(t), nil), VersionInfo{Version: "dev"}, defaultDeps())
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := Execute(context.Background(), root, append([]string{"impact", "oci://" + host + "/acme/orders:1.0.0", "oci://" + host + "/acme/orders:2.0.0", "--local", ""}, args...))
	return stdout.String(), stderr.String(), err
}

func impactExit(err error) int {
	var exit *ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	}
	return 1
}

func TestImpactPrintsRegistryAndTargets(t *testing.T) {
	host, trust := webappTargets(t, "^1.0.0")
	out, _, _ := runImpact(t, host, "--delivery-trust", trust)
	repo := host + "/acme/pacto-targets"
	for _, want := range []string{
		"Registry: " + host + "/acme (found " + repo + ", 1 cluster read)\n",
		"Targets: " + repo + "@sha256:",
		" (cluster c-6f1e, asOf 0m, complete, signed by ci-demo (publisher key:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestImpactExitBlockedWithVerifiedSnapshot(t *testing.T) {
	host, trust := webappTargets(t, "^1.0.0")
	out, _, err := runImpact(t, host, "--delivery-trust", trust)
	if impactExit(err) != 1 || !strings.Contains(out, "Release gate: blocked") {
		t.Fatalf("err %v:\n%s", err, out)
	}
}

func TestImpactExitWithUnverifiedSnapshot(t *testing.T) {
	host, _ := webappTargets(t, ">=1.0.0")
	out, _, err := runImpact(t, host)
	if impactExit(err) != 2 || !strings.Contains(out, "partial, unverified)") {
		t.Fatalf("err %v:\n%s", err, out)
	}
}

func TestImpactExitWithStaleSnapshot(t *testing.T) {
	host, trust := webappTargets(t, ">=1.0.0")
	prev := fleetNow
	fleetNow = func() time.Time { return time.Now().Add(20 * time.Minute) }
	t.Cleanup(func() { fleetNow = prev })
	out, _, err := runImpact(t, host, "--delivery-trust", trust)
	if impactExit(err) != 2 || !strings.Contains(out, ", stale, signed by ci-demo") {
		t.Fatalf("err %v:\n%s", err, out)
	}
}

func TestImpactPrintsRegistryNotFound(t *testing.T) {
	host, _, _ := targetsEnv(t)
	out, _, _ := runImpact(t, host)
	want := "Registry: no pacto-targets at " + host + "/acme/pacto-targets (not found) or " + host + "/pacto-targets (not found); consumers come only from the sources you passed\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

// TestImpactPrintsDeniedAndUnusedSnapshots: a denied root names the
// credentials to check; a snapshot that was not used and one signed by a key
// not scoped to its cluster say so.
func TestImpactPrintsDeniedAndUnusedSnapshots(t *testing.T) {
	var w bytes.Buffer
	cl := &fleet.Cluster{Name: "c-6f1e"}
	printTargetSnapshots(&w, &impact.Result{
		Registry: &impact.RegistryLookup{Tried: []impact.RegistryRoot{{Repository: "ghcr.io/acme/pacto-targets", Outcome: impact.RegistryDenied}, {Repository: "ghcr.io/pacto-targets", Outcome: impact.RegistryNoClusterTag}}},
		TargetSnapshots: []fleet.SourceState{
			{ID: "targets-c-6f1e", Status: fleet.SourceUnavailable, Cluster: cl},
			{ID: "targets", Status: fleet.SourceUnavailable},
			{ID: "targets-c2", Ref: "r@sha256:1", Signer: "key:ab", SignerUnscoped: true, Status: fleet.SourcePartial},
			{ID: "targets-c3", Ref: "r@sha256:2", Signer: "rec", Status: fleet.SourceAvailable},
		},
		// No limitation: a capped list can drop it, so the line reads the source.
	})
	want := "Registry: no pacto-targets at ghcr.io/acme/pacto-targets (denied (check registry read credentials)) or ghcr.io/pacto-targets (no cluster tag); consumers come only from the sources you passed\n" +
		"Targets: targets-c-6f1e (cluster c-6f1e, unavailable, not used)\n" +
		"Targets: targets (cluster -, unavailable, not used)\n" +
		"Targets: r@sha256:1 (cluster -, asOf -, partial, signed by key:ab (not scoped to this cluster))\n" +
		"Targets: r@sha256:2 (cluster -, asOf -, complete, signed by rec)\n"
	if w.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", w.String(), want)
	}
}

func TestImpactTargetSnapshotOff(t *testing.T) {
	host, trust := webappTargets(t, "^1.0.0")
	out, _, err := runImpact(t, host, "--delivery-trust", trust, "--target-snapshot", "off")
	// The test registry holds no organization index, so the BREAKING change
	// ends unverified rather than clear.
	if strings.Contains(out, "Registry:") || strings.Contains(out, "Targets:") || impactExit(err) != 2 {
		t.Fatalf("err %v:\n%s", err, out)
	}
	if _, _, err := runImpact(t, host, "--target-snapshot", "auto", "--target-snapshot", "oci://"+host+"/acme/pacto-targets"); err == nil || !strings.Contains(err.Error(), "not a mix") {
		t.Fatalf("mixed values: %v", err)
	}
}

func TestImpactJSONCarriesRegistryAndTargets(t *testing.T) {
	host, trust := webappTargets(t, "^1.0.0")
	out, _, _ := runImpact(t, host, "--delivery-trust", trust, "--output-format", "json")
	var res struct {
		Registry        *impact.RegistryLookup
		TargetSnapshots []fleet.SourceState
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.Registry == nil || res.Registry.Found != host+"/acme/pacto-targets" || len(res.TargetSnapshots) != 1 || res.TargetSnapshots[0].Signer != "ci-demo" {
		t.Fatalf("registry %+v, snapshots %+v", res.Registry, res.TargetSnapshots)
	}
}

func TestMCPImpactStdioDerivesSnapshot(t *testing.T) {
	host, trust := webappTargets(t, "^1.0.0")
	svc := app.NewService(ordersStore(t), nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"transport": "stdio", "fleet": "true", "local": "", "delivery-trust": trust} {
		_ = cmd.Flags().Set(k, v)
	}
	res, err := mcpImpactProvider(cmd, svc, defaultDeps(), true, true)(t.Context(), &fleet.FleetSnapshot{},
		pactomcp.ImpactRequest{OldRef: "oci://" + host + "/acme/orders:1.0.0", NewRef: "oci://" + host + "/acme/orders:2.0.0"})
	if err != nil || res.Registry == nil || len(res.TargetSnapshots) != 1 || res.Gate != impact.GateBlocked {
		t.Fatalf("res %+v, %v", res, err)
	}
}

// TestMCPImpactStdioTargetSnapshotOff: pacto mcp --target-snapshot off turns
// the derivation off as pacto impact does, and a mix fails at startup.
func TestMCPImpactStdioTargetSnapshotOff(t *testing.T) {
	host, trust := webappTargets(t, "^1.0.0")
	svc := app.NewService(ordersStore(t), nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"transport": "stdio", "fleet": "true", "local": "", "delivery-trust": trust, "target-snapshot": "off"} {
		_ = cmd.Flags().Set(k, v)
	}
	res, err := mcpImpactProvider(cmd, svc, defaultDeps(), true, true)(t.Context(), &fleet.FleetSnapshot{},
		pactomcp.ImpactRequest{OldRef: "oci://" + host + "/acme/orders:1.0.0", NewRef: "oci://" + host + "/acme/orders:2.0.0"})
	if err != nil || res.Registry != nil || len(res.TargetSnapshots) != 0 {
		t.Fatalf("res %+v, %v", res, err)
	}
	_ = cmd.Flags().Set("target-snapshot", "oci://"+host+"/acme/pacto-targets")
	if _, err := buildFleetMode(cmd, svc, defaultDeps()); err == nil || !strings.Contains(err.Error(), "not a mix") {
		t.Fatalf("mixed values: %v", err)
	}
}

// TestMCPImpactHTTPNeverDerives: an HTTP server reads no target snapshot for
// its callers (internal/mcp pins the tool description that says so).
func TestMCPImpactHTTPNeverDerives(t *testing.T) {
	svc := app.NewService(&testutil.MockBundleStore{PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
		return ordersStore(t).Pull(t.Context(), map[bool]string{true: ":1.0.0", false: ":2.0.0"}[strings.Contains(ref, "aaaa")])
	}}, nil)
	res, err := httpImpact(svc, false)(t.Context(), viewSnapshot(), pactomcp.ImpactRequest{
		OldRef: "oci://reg.example/orders@sha256:" + strings.Repeat("a", 64), NewRef: "oci://reg.example/orders@sha256:" + strings.Repeat("b", 64)})
	if err != nil || res.Registry != nil || len(res.TargetSnapshots) != 0 {
		t.Fatalf("res %+v, %v", res, err)
	}
}
