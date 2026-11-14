//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	statsregistry "github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

// statsEnvOff clears the stats env so only the flags a test passes record.
func statsEnvOff(t *testing.T) {
	t.Helper()
	t.Setenv("PACTO_STATS_REGISTRY", "")
	t.Setenv("PACTO_STATS_FILE", "")
}

// serveRegistry starts h and returns a testRegistry for its host.
func serveRegistry(t *testing.T, h http.Handler) *testRegistry {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testRegistry{server: srv, host: strings.TrimPrefix(srv.URL, "http://"),
		client: oci.NewClient(authn.NewMultiKeychain(authn.DefaultKeychain))}
}

// referrersRegistry is a registry with the native Referrers API.
func referrersRegistry(t *testing.T) *testRegistry {
	t.Helper()
	return serveRegistry(t, testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
}

// writePayments writes service payments at version with workload into a new dir.
func writePayments(t *testing.T, version, workload string) string {
	t.Helper()
	dir := t.TempDir()
	body := `pactoVersion: "2.0"
service:
  name: payments
  version: "` + version + `"
workload: ` + workload + `
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// openStats opens a fresh client for the repository at path, so a listing
// detects the registry's Referrers capability on its own.
func openStats(t *testing.T, path string) *remote.Repository {
	t.Helper()
	repo, err := statsregistry.Open(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// listRecords returns the referrers of service's anchor at path, each with its
// layer bytes.
func listRecords(t *testing.T, path, service string) [][]byte {
	t.Helper()
	repo := openStats(t, path)
	ctx := t.Context()
	anchor, err := repo.Resolve(ctx, runrecord.AnchorTag(service))
	if err != nil {
		t.Fatalf("anchor tag: %v", err)
	}
	var listed []ocispec.Descriptor
	if err := repo.Referrers(ctx, anchor, "", func(p []ocispec.Descriptor) error { listed = append(listed, p...); return nil }); err != nil {
		t.Fatal(err)
	}
	layers := make([][]byte, 0, len(listed))
	for _, d := range listed {
		raw, err := content.FetchAll(ctx, repo, d)
		if err != nil {
			t.Fatal(err)
		}
		var m ocispec.Manifest
		if err := json.Unmarshal(raw, &m); err != nil || m.ArtifactType != runrecord.ArtifactType || len(m.Layers) != 1 {
			t.Fatalf("manifest = %s %v", raw, err)
		}
		layer, err := content.FetchAll(ctx, repo.Blobs(), m.Layers[0])
		if err != nil {
			t.Fatal(err)
		}
		layers = append(layers, layer)
	}
	return layers
}

// onlyRecord asserts exactly one record of service is listed at path and parses it.
func onlyRecord(t *testing.T, path, service string) runrecord.Record {
	t.Helper()
	layers := listRecords(t, path, service)
	if len(layers) != 1 {
		t.Fatalf("%d records listed, want 1", len(layers))
	}
	rec, err := runrecord.Parse(layers[0])
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// breakingDiff runs a BREAKING diff recorded to path and returns its output.
func breakingDiff(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCommand(t, nil, append([]string{"diff", writePayments(t, "1.0.0", "service"), writePayments(t, "2.0.0", "job")}, args...)...)
	if err == nil {
		t.Fatalf("a BREAKING diff succeeded:\n%s", out)
	}
	return out
}

// checkDiffRecord asserts the record of a BREAKING payments diff.
func checkDiffRecord(t *testing.T, rec runrecord.Record) {
	t.Helper()
	if rec.Kind != runrecord.KindDiff || rec.Service != "payments" || rec.Outcome != runrecord.OutcomeFailed ||
		rec.Classification != "BREAKING" || !strings.HasPrefix(rec.OldRevision, "sha256:") ||
		!strings.HasPrefix(rec.NewRevision, "sha256:") || rec.OldRevision == rec.NewRevision {
		t.Fatalf("record = %+v", rec)
	}
}

func TestRunRecordNativeReferrers(t *testing.T) {
	statsEnvOff(t)
	path := referrersRegistry(t).host + "/acme/pacto-stats"
	out := breakingDiff(t, "--stats-registry", path)
	assertContains(t, out, "pacto: run record published to "+path+"@sha256:")
	checkDiffRecord(t, onlyRecord(t, path, "payments"))
}

func TestRunRecordTagFallback(t *testing.T) {
	statsEnvOff(t)
	reg := serveRegistry(t, registry.New(registry.Logger(log.New(io.Discard, "", 0)))) // no Referrers API
	path := reg.host + "/acme/pacto-stats"
	out := breakingDiff(t, "--stats-registry", path)
	assertContains(t, out, "pacto: run record published to "+path+"@sha256:")
	repo := openStats(t, path)
	anchor, err := repo.Resolve(t.Context(), runrecord.AnchorTag("payments"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(t.Context(), "sha256-"+anchor.Digest.Encoded()); err != nil {
		t.Fatalf("referrers tag: %v", err)
	}
	checkDiffRecord(t, onlyRecord(t, path, "payments"))
}

func TestRunRecordLeavesBundleDigest(t *testing.T) {
	statsEnvOff(t)
	reg := referrersRegistry(t)
	repoPath := reg.host + "/acme/payments"
	ref := "oci://" + repoPath + ":1.0.0"
	out, err := runCommand(t, reg, "--output-format", "json", "push", ref, "-p", writePayments(t, "1.0.0", "service"))
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	var pushed struct{ Digest string }
	if err := json.Unmarshal([]byte(out), &pushed); err != nil || pushed.Digest == "" {
		t.Fatalf("push output = %s %v", out, err)
	}
	repo := openStats(t, repoPath)
	ctx := t.Context()
	before, err := repo.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := content.FetchAll(ctx, repo, before)
	if err != nil {
		t.Fatal(err)
	}

	out, err = runCommand(t, reg, "diff", ref, writePayments(t, "2.0.0", "job"), "--stats-registry", repoPath)
	if err == nil {
		t.Fatalf("a BREAKING diff succeeded:\n%s", out)
	}
	assertContains(t, out, "pacto: run record published to "+repoPath+"@sha256:")
	checkDiffRecord(t, onlyRecord(t, repoPath, "payments"))

	after, err := repo.Resolve(ctx, "1.0.0")
	if err != nil || after.Digest.String() != pushed.Digest || after.Digest != before.Digest {
		t.Fatalf("bundle tag = %v %v, pushed %s", after.Digest, err, pushed.Digest)
	}
	manifestAfter, err := content.FetchAll(ctx, repo, after)
	if err != nil || !bytes.Equal(manifestAfter, manifestBefore) {
		t.Fatalf("bundle manifest changed: %v", err)
	}
	n := 0
	if err := repo.Referrers(ctx, after, "", func(p []ocispec.Descriptor) error { n += len(p); return nil }); err != nil || n != 0 {
		t.Fatalf("bundle has %d referrers (%v), want 0", n, err)
	}
}

func TestStatsPublishFromFile(t *testing.T) {
	statsEnvOff(t)
	file := filepath.Join(t.TempDir(), "record.json")
	out := breakingDiff(t, "--stats-file", file)
	assertContains(t, out, "pacto: run record written to "+file)
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	path := referrersRegistry(t).host + "/acme/pacto-stats"
	if out, err := runCommand(t, nil, "stats", "publish", file, "--registry", path); err != nil {
		t.Fatalf("stats publish: %v\n%s", err, out)
	}
	layers := listRecords(t, path, "payments")
	if len(layers) != 1 || !bytes.Equal(layers[0], written) {
		t.Fatalf("published %d records; layer differs from the file:\n%s\n%s", len(layers), layers, written)
	}
}

func TestImpactRecordFromFleet(t *testing.T) {
	statsEnvOff(t)
	oldDir, newDir := t.TempDir(), t.TempDir()
	testutil.WriteOrders(t, oldDir, "1.0.0", "service")
	testutil.WriteOrders(t, newDir, "2.0.0", "job")
	root, ev := testutil.ImpactFleet(t, "^1.0.0")
	cases := []struct {
		name     string
		extra    []string
		code     int
		verdict  string
		reason   string
		outcome  string
		blocking int
	}{
		{"blocked with target evidence", []string{"--target-state", ev}, 1,
			runrecord.VerdictBlocked, impact.GateCodeConsumersIncompatible, runrecord.OutcomeFailed, 1},
		{"unverified without target evidence", nil, 2,
			runrecord.VerdictUnverified, impact.GateCodeConsumersUnchecked, runrecord.OutcomeUnverified, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := referrersRegistry(t).host + "/acme/pacto-stats"
			args := append([]string{"impact", oldDir, newDir, "--local", root, "--stats-registry", path}, tc.extra...)
			out, err := runCommand(t, nil, args...)
			if code := exitCode(err); code != tc.code {
				t.Fatalf("exit %d (%v), want %d:\n%s", code, err, tc.code, out)
			}
			rec := onlyRecord(t, path, "orders")
			if rec.Kind != runrecord.KindImpact || rec.Gate == nil || rec.Gate.Verdict != tc.verdict || rec.Gate.Reason != tc.reason ||
				rec.Outcome != tc.outcome || rec.Consumers == nil || rec.Consumers.Blocking != tc.blocking ||
				rec.Completeness != "complete" || rec.AsOf == nil {
				t.Fatalf("record = %+v gate %+v consumers %+v", rec, rec.Gate, rec.Consumers)
			}
		})
	}
}

// exitCode is the process exit code a command error maps to.
func exitCode(err error) int {
	var exit *cli.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	default:
		return 1
	}
}
