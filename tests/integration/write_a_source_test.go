//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// sourceScript is the first bash fence of docs/write-a-source.md: the whole
// producer the page shows.
func sourceScript(t *testing.T) string {
	t.Helper()
	page, err := os.ReadFile(filepath.Join(repoRoot, "docs", "write-a-source.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(page), "```bash\n")
	script, _, closed := strings.Cut(rest, "\n```")
	if !ok || !closed {
		t.Fatal("docs/write-a-source.md has no bash fence")
	}
	return script
}

// TestWriteASourceScriptRuns runs the page's producer, as written, against a
// real Evidence Server that grants my-idp and a real registry that holds the
// declared ledger, with the shipped binary on PATH. The fleet then shows the
// contributed checkout, which the reader only shows once its signature
// verifies with my-idp's advertised key, and ledger's target reported by
// my-idp; the pushed layer holds no key.
func TestWriteASourceScriptRuns(t *testing.T) {
	script := sourceScript(t)
	if n := strings.Count(script, "\n") + 1; n > 40 {
		t.Fatalf("the producer script is %d lines, at most 40", n)
	}

	bin := buildPactoBinary(t)
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	home, work := t.TempDir(), t.TempDir()
	env := append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home, "XDG_CACHE_HOME="+t.TempDir(), "PACTO_INSECURE_REGISTRIES="+host, "PACTO_NO_UPDATE_CHECK=1")
	pacto := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pacto %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	ledger := t.TempDir()
	if err := os.WriteFile(filepath.Join(ledger, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice:\n  name: ledger\n  version: 1.0.0\n  owner:\n    team: ledger\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var pushed struct{ Digest string }
	if err := json.Unmarshal([]byte(pacto("push", "oci://"+host+"/acme/ledger", "-p", ledger, "--index", "none", "--output-format", "json")), &pushed); err != nil || pushed.Digest == "" {
		t.Fatalf("push ledger: %v", err)
	}
	contractRef := "oci://" + host + "/acme/ledger@" + pushed.Digest

	// The platform holds the producer's public key before it reports: the
	// script's keygen line finds the key and keeps it.
	keys := filepath.Join(home, ".pacto", "keys")
	pacto("evidence", "keygen", "--out", keys, "--producer", "my-idp", "--key-id", "my-idp-2026")
	trust := filepath.Join(keys, "trust.yaml")
	if err := os.WriteFile(trust, []byte("apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n"+
		"  - keyId: my-idp-2026\n    producerId: my-idp\n    publicKeyFile: my-idp__my-idp-2026.pub\n"+
		"    allowedContractRepos: ["+host+"/acme/ledger, "+host+"/acme/pacto-inferred/my-idp]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidenceURL := serveEvidence(t, host, trust, contractRef)

	run := exec.Command("bash", "-euo", "pipefail", "-c", script)
	run.Dir = work
	run.Env = append(env, "REGISTRY="+host+"/acme", "EVIDENCE_URL="+evidenceURL, "CONTRACT_REF="+contractRef)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("the producer script: %v\n%s", err, out)
	}

	fleetArgs := []string{"--local", "", "--evidence-url", evidenceURL, "--oci", contractRef}
	if out := pacto(append([]string{"fleet", "get", "checkout"}, fleetArgs...)...); !strings.Contains(out, "Contributed by my-idp from "+host+"/acme/pacto-inferred/my-idp/services@") {
		t.Errorf("checkout:\n%s", out)
	}
	var view struct {
		Targets []struct{ ReportedBy string } `json:"targets"`
	}
	if err := json.Unmarshal([]byte(pacto(append([]string{"fleet", "get", "ledger", "--output-format", "json"}, fleetArgs...)...)), &view); err != nil ||
		len(view.Targets) != 1 || view.Targets[0].ReportedBy != "my-idp" {
		t.Errorf("ledger's targets = %+v, %v", view.Targets, err)
	}

	store := oci.NewClient(authn.NewMultiKeychain(), oci.WithInsecureRegistries(host))
	b, err := store.Pull(context.Background(), host+"/acme/pacto-inferred/my-idp/services:checkout_0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.WalkDir(b.FS, ".", func(path string, _ fs.DirEntry, err error) error {
		if strings.HasSuffix(path, ".key") {
			t.Errorf("the pushed bundle holds %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// serveEvidence starts an Evidence Server on the registry at host that loads
// trust and stores evidence on subject, and returns its base URL.
func serveEvidence(t *testing.T, host, trust, subject string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	store := oci.NewClient(authn.NewMultiKeychain(), oci.WithInsecureRegistries(host))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.NewService(store, nil).ServeEvidenceOnListener(ctx, ln, app.ServeOptions{
			TrustPath: trust, Subjects: []string{subject}, Producers: []string{"my-idp"}})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("evidence server: %v", err)
		}
	})
	return "http://" + ln.Addr().String()
}
