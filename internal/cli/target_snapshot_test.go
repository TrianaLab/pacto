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
	if !strings.Contains(out, want) || !strings.Contains(out, ", signed by ci-demo, asOf ") {
		t.Fatalf("out = %q", out)
	}
	out, _, _ = runRoot(t, defaultDeps(), nil, "fleet", "snapshot", "--target-snapshot", repo, "--local", "")
	if !strings.Contains(out, "targets-c-6f1e (target-snapshot): partial revs=0 targets=0, ref ") || !strings.Contains(out, ", unverified\n") {
		t.Fatalf("unverified out = %q", out)
	}
}

// TestFleetStatusPrintsSnapshotSections: every section of a snapshot prints
// on its own line with its own source, asOf and completeness, and a partial
// one names its limitation. The publisher fills a section's empty asOf.
func TestFleetStatusPrintsSnapshotSections(t *testing.T) {
	t.Setenv(app.TrustEnv, "")
	host, key, _ := targetsEnv(t)
	evidence := fleet.Limitation{Code: fleet.LimitationSourcePartial, Message: "the Evidence Server answered HTTP 500"}
	doc := snapshotDoc(t, map[string]any{"completeness": "partial", "sources": []any{
		map[string]any{"kind": "kubernetes", "asOf": "", "completeness": "complete"},
		map[string]any{"kind": "kubernetes-workloads", "asOf": "", "completeness": "complete"},
		map[string]any{"kind": "evidence-http", "asOf": "", "completeness": "partial", "limitations": []any{evidence}}}})
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", doc, "--registry", host+"/acme", "--key", key); err != nil {
		t.Fatal(err)
	}
	pub, _ := os.ReadFile(key + ".pub.pem")
	trust := filepath.Join(t.TempDir(), "trust.yaml")
	_ = os.WriteFile(trust, []byte("keys:\n- name: ci-demo\n  streams: [\"c-6f1e/*/*\"]\n  publicKey: |\n    "+strings.ReplaceAll(strings.TrimSpace(string(pub)), "\n", "\n    ")+"\n"), 0o600)
	out, _, _ := runRoot(t, defaultDeps(), nil, "fleet", "status", "--target-snapshot", "oci://"+host+"/acme/pacto-targets", "--delivery-trust", trust, "--local", "")
	for _, want := range []string{
		"Source targets-c-6f1e (target-snapshot): available, ref ",
		"Source targets-c-6f1e-workloads (kubernetes-workloads): available, ref ",
		"Source targets-c-6f1e-evidence (evidence-http): partial, ref ",
		", signed by ci-demo, asOf 20",
		": " + evidence.Message + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in %q", want, out)
		}
	}
}

func TestSourceRefLine(t *testing.T) {
	// The scope rides on the source, so a capped limitation list never drops it.
	s := fleet.SourceState{ID: "targets-c1", Ref: "reg/acme/pacto-targets@sha256:1", Signer: "key:ab", SignerUnscoped: true}
	if got := sourceRef(s); got != ", ref reg/acme/pacto-targets@sha256:1, signed by key:ab (not scoped to this cluster)" {
		t.Fatal(got)
	}
	if got := sourceRef(fleet.SourceState{ID: "oci"}); got != "" {
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

// TestPrintPinnedSourcesReason: a degraded source names its own limitation,
// past the line Build adds for it, else that line; one with no sync prints
// no asOf.
func TestPrintPinnedSourcesReason(t *testing.T) {
	var b strings.Builder
	lims := []fleet.Limitation{
		{Source: "a", Message: "source a returned a partial result"},
		{Source: "b", Message: "source b is stale; its records may be out of date"},
		{Source: "b", Message: "the snapshot is old"},
	}
	printPinnedSources(&b, []fleet.SourceState{{ID: "a", Kind: "k", Status: fleet.SourcePartial, Ref: "r"}, {ID: "b", Kind: "k", Status: fleet.SourceStale, Ref: "r"}}, lims)
	want := "Source a (k): partial, ref r, unverified: source a returned a partial result\nSource b (k): stale, ref r, unverified: the snapshot is old\n"
	if b.String() != want {
		t.Fatalf("got %q", b.String())
	}
}
