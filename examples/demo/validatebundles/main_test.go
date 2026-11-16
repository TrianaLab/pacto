package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An empty contract beside a chart is indexed under the name the chart gives.
func TestBuildIndexInfersName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newsletter")
	if err := os.MkdirAll(filepath.Join(dir, "chart"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chart", "Chart.yaml"), []byte("apiVersion: v2\nname: newsletter-service\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := buildIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if idx["newsletter-service"] == nil {
		t.Fatalf("index = %v", idx)
	}
}
