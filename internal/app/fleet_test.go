package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/fleet"
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

	targetState := filepath.Join(t.TempDir(), "targets.yaml")
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n  - scope: prod\n    kind: k8s\n    name: svc-a\n    service: svc-a\n    compliance: Compliant\n"
	if err := os.WriteFile(targetState, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		LocalRoots:       []string{root},
		TargetStateFiles: []string{targetState},
		Concurrency:      2,
		Now:              func() time.Time { return fixed },
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
	if !ids["local"] || !ids["target-state"] {
		t.Errorf("source ids = %v, want local + target-state", ids)
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
		{"target-state", 0, 1, "target-state"},
		{"target-state", 1, 3, "target-state-2"},
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

func TestService_Fleet_EvidenceStores(t *testing.T) {
	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	storeDir := t.TempDir()
	store, err := evidenceingest.NewFileStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	rec := evidenceingest.Record{
		Envelope: evidenceenvelope.Envelope{
			Producer: evidenceenvelope.Producer{ID: "prod-eu"},
			EvidenceSet: evidence.EvidenceSet{
				Subject:     evidence.SubjectRef{Kind: "service", Name: "svc-a"},
				ContractRef: "oci://ghcr.io/acme/svc:1.0.0",
				ObservedAt:  fixed,
			},
		},
		Compliance: fleet.StatusCompliant,
		AcceptedAt: fixed,
	}
	if err := store.Put(context.Background(), "svc-a", rec); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		EvidenceStores: []string{storeDir},
		Now:            func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(snap.Targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(snap.Targets))
	}
	found := false
	for _, tgt := range snap.Targets {
		if tgt.Scope == "prod-eu" && tgt.Name == "svc-a" && tgt.Kind == "external" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an external target prod-eu/svc-a, got %+v", snap.Targets)
	}
	ids := map[string]bool{}
	for _, s := range snap.Sources {
		ids[s.ID] = true
	}
	if !ids["evidence-store"] {
		t.Errorf("source ids = %v, want evidence-store", ids)
	}
}

func TestService_Fleet_EvidenceStore_OpenError(t *testing.T) {
	// A regular file cannot host a store directory beneath it, so NewFileStore
	// fails and the store becomes a failing source (a limitation), not a build
	// abort.
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		EvidenceStores: []string{filepath.Join(file, "store")},
	})
	if err != nil {
		t.Fatalf("Fleet returned a hard error, want partial snapshot: %v", err)
	}
	found := false
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceUnavailable && l.Source == "evidence-store" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a SOURCE_UNAVAILABLE limitation for evidence-store, got %+v", snap.Limitations)
	}
}

func TestService_Fleet_DisallowPartial(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Fleet(context.Background(), FleetOptions{
		TargetStateFiles: []string{filepath.Join(t.TempDir(), "missing.yaml")},
		DisallowPartial:  true,
	})
	if err == nil {
		t.Fatal("expected error with DisallowPartial and a missing target-state file")
	}
}
