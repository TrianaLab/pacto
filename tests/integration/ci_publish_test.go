//go:build integration

package integration

import (
	"crypto/elliptic"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// ciPublishRepo writes a git repository with ledger and reports (reports
// depending on ledger), whose contracts declare a publish section signed
// with a key and kept out of the transparency log, and notes, which
// declares none and deploys the manifests in its folder.
func ciPublishRepo(t *testing.T, host, key string) string {
	t.Helper()
	root := t.TempDir()
	sign := "  sign: {key: " + key + ", transparencyLog: false}\n"
	files := map[string]string{
		// Folders in the reverse of the dependency order.
		"a/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: reports\n  version: 1.2.0\n" +
			"dependencies:\n  - name: ledger\n    ref: oci://" + host + "/acme/ledger\n    compatibility: \"^1\"\n" +
			"publish:\n  bundle: oci://" + host + "/acme/reports\n" + sign,
		"b/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n" +
			"publish:\n  bundle: oci://" + host + "/acme/ledger\n" + sign,
		"notes/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: notes\n",
		"notes/k8s/deploy.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: notes}\nspec:\n  template:\n    spec:\n" +
			"      containers:\n        - name: api\n          image: r.io/notes:1\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// cosignKey writes a cosign key pair outside every repository and returns
// the private key file.
func cosignKey(t *testing.T) string {
	t.Helper()
	priv, _, err := cryptoutils.GeneratePEMEncodedECDSAKeyPair(elliptic.P256(), cryptoutils.StaticPasswordFunc([]byte("pw")))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSIGN_PASSWORD", "pw")
	key := filepath.Join(t.TempDir(), "cosign.key")
	if err := os.WriteFile(key, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}

// TestCIPublishEndToEnd: pacto ci --publish publishes ledger before reports,
// derives the versions the contracts leave out, signs the section-less
// service with --sign-key, exits 0, and every bundle pulls back.
func TestCIPublishEndToEnd(t *testing.T) {
	// The run must not read the CI job this suite runs in: a pull request
	// pipeline is refused, and the plan would land in its job summary.
	for _, k := range []string{"GITHUB_ACTIONS", "GITHUB_BASE_REF", "GITHUB_STEP_SUMMARY", "GITHUB_REPOSITORY_OWNER", "ACTIONS_ID_TOKEN_REQUEST_URL",
		"GITLAB_CI", "CI_MERGE_REQUEST_DIFF_BASE_SHA", "SIGSTORE_ID_TOKEN"} {
		t.Setenv(k, "")
	}
	reg := newTestRegistry(t)
	key := cosignKey(t)
	root := ciPublishRepo(t, reg.host, key)
	image := reg.host + "/other/notes@sha256:" + strings.Repeat("a", 64)

	out, err := runCommand(t, reg, "ci", root, "--publish", "--registry", reg.host+"/acme", "--sign-key", key, "--no-transparency-log",
		"--image", "notes="+image, "--output-format", "json")
	if err != nil {
		t.Fatalf("pacto ci --publish: %v\n%s", err, out)
	}
	var plan struct {
		Exit int `json:"exit"`
		Rows []struct {
			Service, State, To, Identity, Published string
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []struct{ service, to, bundle string }{
		{"ledger", "1.0.0", "/acme/ledger"},
		{"notes", "1.0.0", "/acme/notes"},
		{"reports", "1.2.0", "/acme/reports"},
	}
	if plan.Exit != 0 || len(plan.Rows) != len(want) {
		t.Fatalf("plan:\n%s", out)
	}
	for i, w := range want {
		r := plan.Rows[i]
		ref := reg.host + w.bundle + ":" + w.to
		if r.Service != w.service || r.State != "published" || r.To != w.to || r.Identity != "key "+key || !strings.HasPrefix(r.Published, ref+"@sha256:") {
			t.Fatalf("row %d %+v, want %+v\n%s", i, r, w, out)
		}
		pulled := filepath.Join(t.TempDir(), w.service)
		if out, err := runCommand(t, reg, "pull", "oci://"+ref, "-o", pulled); err != nil {
			t.Fatalf("pull %s: %v\n%s", ref, err, out)
		}
		raw, err := os.ReadFile(filepath.Join(pulled, "pacto.yaml"))
		if err != nil || !strings.Contains(string(raw), "version: "+w.to) {
			t.Fatalf("pulled %s: %v\n%s", ref, err, raw)
		}
	}
}
