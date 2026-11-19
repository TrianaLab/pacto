package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// publishedTargets publishes a snapshot of cluster c-6f1e to an in-process
// registry and returns the repository ref and a trust config with the
// signing key scoped to that cluster, named ci-demo.
func publishedTargets(t *testing.T) (repo, trust string) {
	t.Helper()
	t.Setenv(app.TrustEnv, "")
	host, key, _ := targetsEnv(t)
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, nil), "--registry", host+"/acme", "--key", key); err != nil {
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
	return "oci://" + host + "/acme/pacto-targets", trust
}

func TestFleetTargetSnapshotFlag(t *testing.T) {
	repo, trust := publishedTargets(t)
	out, _, err := runRoot(t, defaultDeps(), nil, "fleet", "status", "--target-snapshot", repo, "--delivery-trust", trust, "--local", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "Source targets-c-6f1e (target-snapshot): available, ref " + strings.TrimPrefix(repo, "oci://") + "@sha256:"
	if !strings.Contains(out, want) || !strings.Contains(out, ", signed by ci-demo\n") {
		t.Fatalf("out = %q", out)
	}
	out, _, _ = runRoot(t, defaultDeps(), nil, "fleet", "snapshot", "--target-snapshot", repo, "--local", "")
	if !strings.Contains(out, "targets-c-6f1e (target-snapshot): partial revs=0 targets=0, ref ") || !strings.Contains(out, ", unverified\n") {
		t.Fatalf("unverified out = %q", out)
	}
}

func TestSourceRefLine(t *testing.T) {
	s := fleet.SourceState{ID: "targets-c1", Ref: "reg/acme/pacto-targets@sha256:1", Signer: "key:ab"}
	lims := []fleet.Limitation{{Code: fleet.LimitationTargetSnapshotUnscoped, Source: "targets-c1"}}
	if got := sourceRef(s, lims); got != ", ref reg/acme/pacto-targets@sha256:1, signed by key:ab (not scoped to this cluster)" {
		t.Fatal(got)
	}
	if got := sourceRef(fleet.SourceState{ID: "oci"}, nil); got != "" {
		t.Fatal(got)
	}
}

// TestImpactReadsDeliveryTrustFlag: pacto impact takes --delivery-trust, and
// the snapshot it names verifies with no $PACTO_DELIVERY_TRUST set; auto and
// off name no snapshot.
func TestImpactReadsDeliveryTrustFlag(t *testing.T) {
	repo, trust := publishedTargets(t)
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "dev"}, defaultDeps())
	cmd, _, err := root.Find([]string{"impact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--target-snapshot", repo, "--target-snapshot", "auto", "--delivery-trust", trust, "--local", ""}); err != nil {
		t.Fatal(err)
	}
	opts := fleetOptions(cmd)
	if len(opts.TargetSnapshot) != 1 || opts.TargetSnapshotTrustFile != trust {
		t.Fatalf("options %+v", opts)
	}
	snap, err := app.NewService(nil, nil).Fleet(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snap.Sources {
		if s.ID == "targets-c-6f1e" && s.Signer == "ci-demo" && s.Status == fleet.SourceAvailable {
			return
		}
	}
	t.Fatalf("sources %+v", snap.Sources)
}

func TestMCPHTTPRefusesTargetSnapshot(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", deps{})
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"transport": "http", "fleet": "true", "target-snapshot": "oci://example.invalid/pacto-targets"} {
		_ = cmd.Flags().Set(k, v)
	}
	if _, err := buildFleetMode(cmd, svc, deps{}); err != errMCPTargetSnapshotHTTP {
		t.Fatalf("err = %v", err)
	}
}

func TestMCPStdioReadsTargetSnapshot(t *testing.T) {
	repo, trust := publishedTargets(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", deps{})
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"transport": "stdio", "fleet": "true", "local": "", "target-snapshot": repo, "delivery-trust": trust} {
		_ = cmd.Flags().Set(k, v)
	}
	m, err := buildFleetMode(cmd, svc, deps{})
	if err != nil {
		t.Fatal(err)
	}
	q, err := m.Src(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range q.Snapshot().Sources {
		if s.ID == "targets-c-6f1e" && s.Signer == "ci-demo" {
			return
		}
	}
	t.Fatalf("sources %+v", q.Snapshot().Sources)
}

// TestDeliveryTrustMovedKeepsEnv: pacto delivery history still reads the
// trust config $PACTO_DELIVERY_TRUST names.
func TestDeliveryTrustMovedKeepsEnv(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	t.Setenv(app.TrustEnv, missing)
	_, err := runDelivery(t, deliveryDeps("alice", nil), "text", "history", "-n", "prod", "--stage", "payments-prod")
	if err == nil || !strings.Contains(err.Error(), "reading the delivery trust config") {
		t.Fatalf("err = %v", err)
	}
}
