package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/evidencestore"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestService_InspectEvidence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// Seed one record via a store on the same bucket+prefix InspectEvidence reads.
	store, err := openEvidenceStore(ctx, "file://"+dir, DefaultEvidencePrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	rec := evidencestore.AcceptedRecord{
		Envelope:   evidenceenvelope.Envelope{ID: "e1", Producer: evidenceenvelope.Producer{ID: "prod"}, Sequence: 1},
		TargetKey:  "t1",
		AcceptedAt: time.Unix(1, 0),
	}
	if err := store.Commit(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := NewService(nil, nil).InspectEvidence(ctx, "file://"+dir, DefaultEvidencePrefix)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if st.Backend != "file" || st.Records != 1 || st.Phase != evidencestore.PhaseReady {
		t.Errorf("status = %+v", st)
	}
	// An unopenable bucket surfaces as an error.
	if _, err := NewService(nil, nil).InspectEvidence(ctx, "bogus://x", ""); err == nil {
		t.Error("expected error for unopenable bucket")
	}
}

func TestToBucketURL(t *testing.T) {
	// A schemed value passes through unchanged.
	for _, u := range []string{"s3://bucket", "gs://bucket", "azblob://c", "file:///abs"} {
		if got := toBucketURL(u); got != u {
			t.Errorf("toBucketURL(%q) = %q, want unchanged", u, got)
		}
	}
	// A bare directory becomes an absolute file:// URL.
	got := toBucketURL("relative/dir")
	if got[:7] != "file://" || !filepath.IsAbs(got[len("file://"):]) {
		t.Errorf("toBucketURL(dir) = %q, want an absolute file:// URL", got)
	}
}

func TestNewDurableEvidenceSource_Defaults(t *testing.T) {
	if s := newDurableEvidenceSource("", t.TempDir()); s.ID() != "evidence-ingest" {
		t.Errorf("default id = %q, want evidence-ingest", s.ID())
	}
	s := newDurableEvidenceSource("prod-eu", t.TempDir())
	if s.ID() != "prod-eu" || s.Kind() != "evidence-ingest" {
		t.Errorf("id=%q kind=%q", s.ID(), s.Kind())
	}
}

func TestOpenEvidenceStore_QueryStrip(t *testing.T) {
	// A file:// URL with a query is created after stripping the query.
	dir := filepath.Join(t.TempDir(), "bucket")
	store, err := openEvidenceStore(context.Background(), "file://"+dir+"?create_dir=true", DefaultEvidencePrefix)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("expected the bucket dir to be created (query stripped): %v", err)
	}
}

func TestOpenEvidenceStore_MkdirError(t *testing.T) {
	// A file:// dir beneath a regular file cannot be created, so MkdirAll fails.
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openEvidenceStore(context.Background(), "file://"+filepath.Join(file, "bucket"), DefaultEvidencePrefix); err == nil {
		t.Fatal("expected a mkdir error under a regular file")
	}
}

func TestDurableEvidenceSource_Collect_DegradedIsPartial(t *testing.T) {
	// A recovered-but-degraded store (a stray/garbage object under envelopes/)
	// still serves usable targets, but the contribution must be marked partial
	// rather than presented as a complete graph.
	ctx := context.Background()
	dir := t.TempDir()
	stray := filepath.Join(dir, DefaultEvidencePrefix, "envelopes", "stray.json")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("{not a record}"), 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := newDurableEvidenceSource("evidence-store", dir).Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if col.State == nil || col.State.Status != fleet.SourcePartial {
		t.Errorf("degraded store must be SourcePartial: %+v", col.State)
	}
	found := false
	for _, l := range col.Limitations {
		if l.Code == fleet.LimitationSourcePartial {
			found = true
		}
	}
	if !found {
		t.Errorf("degraded store must surface SOURCE_PARTIAL: %+v", col.Limitations)
	}
}

func TestDurableEvidenceSource_Collect_RecoverError(t *testing.T) {
	// A recovery failure (e.g. a corrupt or unreadable bucket) surfaces as a
	// source error, not an empty collection.
	orig := recoverEvidence
	recoverEvidence = func(context.Context, *evidencestore.BlobStore) error {
		return errors.New("recover failed")
	}
	t.Cleanup(func() { recoverEvidence = orig })
	s := newDurableEvidenceSource("evidence-store", t.TempDir())
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected a recover error to surface from Collect")
	}
}
