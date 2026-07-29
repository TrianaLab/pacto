package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLocalBundle(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestService_Fleet(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "svc-a"), "svc-a")

	evidence := filepath.Join(t.TempDir(), "evidence.yaml")
	body := "targets:\n  - scope: prod\n    kind: k8s\n    name: svc-a\n    service: svc-a\n    compliance: Compliant\n"
	if err := os.WriteFile(evidence, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		LocalRoots:    []string{root},
		EvidenceFiles: []string{evidence},
		Concurrency:   2,
		Now:           func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if !snap.GeneratedAt.Equal(fixed) {
		t.Errorf("GeneratedAt = %v, want injected clock %v", snap.GeneratedAt, fixed)
	}
	if snap.Service("svc-a") == nil {
		t.Error("expected service svc-a in snapshot")
	}
	if len(snap.Targets) != 1 {
		t.Errorf("got %d targets, want 1", len(snap.Targets))
	}
	// Single source of each kind → clean, unsuffixed ids.
	ids := map[string]bool{}
	for _, s := range snap.Sources {
		ids[s.ID] = true
	}
	if !ids["local"] || !ids["evidence"] {
		t.Errorf("source ids = %v, want local + evidence", ids)
	}
}

func TestSourceID(t *testing.T) {
	cases := []struct {
		kind     string
		i, total int
		want     string
	}{
		{"local", 0, 1, "local"},
		{"local", 0, 2, "local-1"},
		{"local", 1, 2, "local-2"},
		{"evidence", 0, 1, "evidence"},
		{"evidence", 1, 3, "evidence-2"},
	}
	for _, tc := range cases {
		if got := sourceID(tc.kind, tc.i, tc.total); got != tc.want {
			t.Errorf("sourceID(%q,%d,%d) = %q, want %q", tc.kind, tc.i, tc.total, got, tc.want)
		}
	}
}

func TestService_Fleet_MultipleSourcesGetSuffixedIDs(t *testing.T) {
	r1 := t.TempDir()
	writeLocalBundle(t, filepath.Join(r1, "svc-a"), "svc-a")
	r2 := t.TempDir()
	writeLocalBundle(t, filepath.Join(r2, "svc-b"), "svc-b")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{r1, r2}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range snap.Sources {
		ids[s.ID] = true
	}
	if !ids["local-1"] || !ids["local-2"] {
		t.Errorf("source ids = %v, want local-1 + local-2", ids)
	}
}

func TestService_Fleet_DisallowPartial(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Fleet(context.Background(), FleetOptions{
		EvidenceFiles:   []string{filepath.Join(t.TempDir(), "missing.yaml")},
		DisallowPartial: true,
	})
	if err == nil {
		t.Fatal("expected error with DisallowPartial and a missing evidence file")
	}
}
