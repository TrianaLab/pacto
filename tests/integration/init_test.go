//go:build integration

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

func TestInitCommand(t *testing.T) {
	t.Parallel()

	t.Run("scaffold structure", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		output, err := runCommand(t, nil, "init", "test-svc")
		if err != nil {
			t.Fatalf("init failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Created test-svc/")

		for _, sub := range []string{"", "interfaces", "configuration"} {
			p := filepath.Join(dir, "test-svc", sub)
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("expected %s to exist: %v", p, err)
			}
			if !info.IsDir() {
				t.Errorf("expected %s to be a directory", p)
			}
		}

		pactoPath := filepath.Join(dir, "test-svc", "pacto.yaml")
		if _, err := os.Stat(pactoPath); err != nil {
			t.Fatalf("pacto.yaml was not created: %v", err)
		}
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		output, err := runCommand(t, nil, "--output-format", "json", "init", "json-svc")
		if err != nil {
			t.Fatalf("init failed: %v\noutput: %s", err, output)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["dir"] != "json-svc" {
			t.Errorf("expected dir=json-svc, got %v", result["dir"])
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		output, err := runCommand(t, nil, "--output-format", "markdown", "init", "md-svc")
		if err != nil {
			t.Fatalf("init markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "md-svc")
	})

	t.Run("existing empty dir gets the blank contract", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		os.Mkdir(filepath.Join(dir, "existing-svc"), 0755)

		if _, err := runCommand(t, nil, "init", "existing-svc"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "existing-svc", "pacto.yaml")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("pactify a plain service, plan first", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "fraud")
		os.MkdirAll(filepath.Join(dir, "k8s"), 0755)
		os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0644)
		os.WriteFile(filepath.Join(dir, "k8s", "deployment.yaml"), []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fraud}\nspec: {template: {spec: {containers: [{name: fraud, image: 'registry.example/story/images/fraud:1.0.0'}]}}}\n"), 0644)

		output, err := runCommand(t, nil, "init", "--plan", "--delivery", "--owner", "risk", dir)
		if err != nil {
			t.Fatalf("init --plan: %v\n%s", err, output)
		}
		assertContains(t, output, "Plan for fraud")
		assertContains(t, output, "bundle: oci://registry.example/story/fraud")
		assertContains(t, output, "kind: Stage")
		if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
			t.Fatal("--plan wrote pacto.yaml")
		}

		if output, err = runCommand(t, nil, "init", "--delivery", "--owner", "risk", dir); err != nil {
			t.Fatalf("init: %v\n%s", err, output)
		}
		if output, err = runCommand(t, nil, "validate", dir); err != nil {
			t.Fatalf("validate: %v\n%s", err, output)
		}
		output, err = runCommand(t, nil, "init", "--delivery", dir)
		if err != nil {
			t.Fatal(err)
		}
		assertContains(t, output, "Nothing to add")
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "init", "--help")
		if err != nil {
			t.Fatalf("init --help failed: %v", err)
		}
		assertContains(t, output, "init")
		assertContains(t, output, "Usage")
	})
}

// writeGoldenPath writes a golden path bundle directory.
func writeGoldenPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "http-service")
	os.MkdirAll(filepath.Join(dir, "interfaces"), 0o755)
	os.MkdirAll(filepath.Join(dir, "configuration"), 0o755)
	os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "2.4"
service:
  name: http-service
  version: 1.0.0
  owner: {team: platform}
  summary: An HTTP API on the platform runtime
  template:
    plugins:
      - name: dockerfile
        description: A Dockerfile for a Go HTTP service
interfaces:
  - name: api
    type: openapi
    ref: interfaces/openapi.yaml
configurations:
  - name: default
    schema: configuration/schema.json
    required: true
    values:
      port: 8080
`), 0o644)
	os.WriteFile(filepath.Join(dir, "interfaces", "openapi.yaml"), []byte("openapi: 3.0.0\ninfo: {title: api, version: \"1\"}\npaths: {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "configuration", "schema.json"), []byte(`{"type":"object","properties":{"port":{"type":"integer"}}}`), 0o644)
	return dir
}

// pushGoldenPath pushes the golden path and returns its oci:// ref.
func pushGoldenPath(t *testing.T, reg *testRegistry) string {
	t.Helper()
	ref := "oci://" + reg.host + "/paths/http-service:1.0.0"
	if out, err := runCommand(t, reg, "push", ref, "-p", writeGoldenPath(t)); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	return ref
}

func originOf(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	origin, _ := m["service"].(map[string]any)["origin"].(map[string]any)
	return origin
}

func contentHash(t *testing.T, dir string) string {
	t.Helper()
	d := os.DirFS(dir)
	m, _ := ignore.Load(d)
	h, err := lock.HashFS(ignore.FS(d, m))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestInitFromGoldenPath_Directory(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	path := writeGoldenPath(t)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runCommand(t, nil, "init", dir, "--from", path, "--owner", "checkout", "--domain", "commerce")
	if err != nil {
		t.Fatalf("init --from: %v\n%s", err, out)
	}
	assertContains(t, out, "a directory carries no signature")
	if out, err = runCommand(t, nil, "validate", dir); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	origin := originOf(t, dir)
	if origin["digest"] != contentHash(t, path) || strings.HasPrefix(origin["ref"].(string), "/") {
		t.Errorf("origin %v, content hash %s", origin, contentHash(t, path))
	}
}

func TestInitFromGoldenPath_OCI(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	reg := newTestRegistry(t)
	ref := pushGoldenPath(t, reg)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runCommand(t, reg, "init", dir, "--from", ref, "--owner", "checkout", "--plan")
	if err != nil {
		t.Fatalf("init --from --plan: %v\n%s", err, out)
	}
	assertContains(t, out, "Status")
	assertContains(t, out, "signature not checked")
	if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		t.Fatal("--plan wrote pacto.yaml")
	}
	if out, err = runCommand(t, reg, "init", dir, "--from", ref, "--owner", "checkout"); err != nil {
		t.Fatalf("init --from: %v\n%s", err, out)
	}
	if origin := originOf(t, dir); origin["ref"] != ref || !strings.HasPrefix(origin["digest"].(string), "sha256:") {
		t.Errorf("origin %v", origin)
	}
	if out, err = runCommand(t, reg, "validate", dir); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
}

func TestInitFromGoldenPath_TrustRefuses(t *testing.T) {
	reg := newTestRegistry(t)
	ref := pushGoldenPath(t, reg)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pem, _ := delivery.PublicKeyPEM(pub)
	trust := filepath.Join(t.TempDir(), "platform.pub")
	os.WriteFile(trust, []byte(pem), 0o644)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runCommand(t, reg, "init", dir, "--from", ref, "--owner", "checkout", "--trust", trust)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("an unsigned path was created from under a template key: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		t.Fatal("a refused creation wrote pacto.yaml")
	}
}

func TestInitFromGoldenPath_PinnedRef(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	reg := newTestRegistry(t)
	ref := pushGoldenPath(t, reg)
	digest, err := reg.client.Resolve(t.Context(), strings.TrimPrefix(ref, "oci://"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := "oci://" + reg.host + "/paths/http-service@" + digest
	dir := filepath.Join(t.TempDir(), "gift-cards")
	if out, err := runCommand(t, reg, "init", dir, "--from", pinned, "--owner", "checkout"); err != nil {
		t.Fatalf("init --from a pinned ref: %v\n%s", err, out)
	}
	if origin := originOf(t, dir); origin["ref"] != pinned {
		t.Errorf("origin %v", origin)
	}
}

func TestInitFromGoldenPath_ValuesAndSet(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	path := writeGoldenPath(t)
	values := filepath.Join(t.TempDir(), "values.yaml")
	os.WriteFile(values, []byte("configurations:\n  - name: default\n    schema: configuration/schema.json\n    required: true\n    values:\n      port: 9090\n"), 0o644)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runCommand(t, nil, "init", dir, "--from", path, "--owner", "checkout", "--values", values, "--set", "service.version=0.3.0")
	if err != nil {
		t.Fatalf("init --from: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	for _, s := range []string{"port: 9090", "version: 0.3.0"} {
		if !strings.Contains(string(data), s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, data)
		}
	}
}

func TestInitFromGoldenPath_RunsTestPlugin(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	path := writeGoldenPath(t)
	raw, _ := os.ReadFile(filepath.Join(path, "pacto.yaml"))
	os.WriteFile(filepath.Join(path, "pacto.yaml"), []byte(strings.Replace(string(raw), "    plugins:\n", "    plugins:\n      - name: test\n", 1)), 0o644)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runCommand(t, nil, "init", dir, "--from", path, "--owner", "checkout", "--run-plugins", "test", "--plan")
	if err != nil {
		t.Fatalf("init --from --plan: %v\n%s", err, out)
	}
	bin := filepath.Join(testPluginDir, "pacto-plugin-test")
	assertContains(t, out, "would run "+bin)
	if out, err = runCommand(t, nil, "init", dir, "--from", path, "--owner", "checkout", "--run-plugins", "test"); err != nil {
		t.Fatalf("init --from --run-plugins: %v\n%s", err, out)
	}
	assertContains(t, out, "ran "+bin+": added 2 files, kept 0")
	assertContains(t, out, "not installed: install pacto-plugin-dockerfile")
	for _, f := range []string{"deployment.yaml", "service.yaml"} {
		if data, err := os.ReadFile(filepath.Join(dir, f)); err != nil || !strings.Contains(string(data), "gift-cards") {
			t.Errorf("%s: %v", f, err)
		}
	}
	if out, err = runCommand(t, nil, "validate", dir); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
}
