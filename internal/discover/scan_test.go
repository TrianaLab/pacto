package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageRepo(t *testing.T) {
	for in, want := range map[string]string{"python:3.13": "docker.io/library/python", "acme/app": "docker.io/acme/app", "localhost:5000/app:1": "localhost:5000/app",
		"reg.io/a/b@sha256:abc": "reg.io/a/b"} {
		if got, _ := ImageRepo(in); got != want {
			t.Errorf("ImageRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestScanReadsInitContainerEnvKeys: an init container's environment is read
// like a container's, and its schema property holds a key and a type only.
func TestScanReadsInitContainerEnvKeys(t *testing.T) {
	dir := t.TempDir()
	m := `apiVersion: apps/v1
kind: Deployment
metadata: {name: checkout}
spec:
  template:
    spec:
      initContainers:
      - name: migrate
        env:
        - {name: MIGRATE_DSN, value: m1grate-only}
        - {name: LOG_LEVEL, value: debug}
      containers:
      - name: checkout
        image: reg.io/acme/checkout:1.0.0
        env:
        - {name: LOG_LEVEL, value: info}
`
	if err := os.WriteFile(filepath.Join(dir, "deploy.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EnvVar{}
	for _, e := range sc.Env {
		got[e.Name] = e
	}
	dsn, ok := got["MIGRATE_DSN"]
	if !ok || got["LOG_LEVEL"].Value != "info" {
		t.Fatalf("env %+v", sc.Env)
	}
	data, _ := json.Marshal(EnvProperty(dsn))
	if p := EnvProperty(dsn); p["type"] != "string" || strings.Contains(string(data), "m1grate-only") {
		t.Fatalf("MIGRATE_DSN property %s", data)
	}
}
