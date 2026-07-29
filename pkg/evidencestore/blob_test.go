package evidencestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"gocloud.dev/blob"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func newRec(id, producer, target string, seq uint64, at time.Time) AcceptedRecord {
	return AcceptedRecord{
		Envelope: evidenceenvelope.Envelope{
			ID:       id,
			Producer: evidenceenvelope.Producer{ID: producer},
			Sequence: seq,
		},
		TargetKey:  target,
		AcceptedAt: at,
	}
}

func openMem(t *testing.T) *BlobStore {
	t.Helper()
	s, err := Open(context.Background(), "mem://", "", WithClock(fixedClock()), WithInstanceID("test-instance"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func commit(t *testing.T, s *BlobStore, rec AcceptedRecord) {
	t.Helper()
	if err := s.Commit(context.Background(), rec); err != nil {
		t.Fatalf("commit %s: %v", rec.EnvelopeID(), err)
	}
}

func TestOpen_Prefix(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, "mem://", "app/ev")
	if err != nil {
		t.Fatalf("valid prefix: %v", err)
	}
	if got := s.Inspect(ctx).Prefix; got != "app/ev" {
		t.Errorf("prefix=%q", got)
	}
	_ = s.Close()
	if _, err := Open(ctx, "mem://", "../bad"); err == nil {
		t.Fatal("want prefix error")
	}
}

func TestOpen_BadBucket(t *testing.T) {
	if _, err := Open(context.Background(), "bogus://x", ""); err == nil {
		t.Fatal("want open error")
	}
}

func TestOpen_InvalidPrefixes(t *testing.T) {
	for _, bad := range []string{"/abs", "a\\b", "a://b", "a/../b", "."} {
		if _, err := Open(context.Background(), "mem://", bad); err == nil {
			t.Errorf("prefix %q: want error", bad)
		}
	}
}

func TestRecover_ReadError(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	hb, err := blob.OpenBucket(ctx, "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.WriteAll(ctx, "envelopes/"+hashID("p")+"/rec.json", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	if err := hb.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, "file://"+dir, "", WithInstanceID("w1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	orig := readAll
	readAll = func(context.Context, *blob.Bucket, string) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { readAll = orig }()
	state, err := s.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Corruptions) != 1 || s.Inspect(ctx).Phase != PhaseDegraded {
		t.Errorf("want one corruption and degraded, got %d / %s", len(state.Corruptions), s.Inspect(ctx).Phase)
	}
}

func TestRecover_Empty(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	state, err := s.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SeenEnvelopeIDs) != 0 || len(state.LatestByTarget) != 0 {
		t.Error("expected empty state")
	}
	if s.Inspect(ctx).Phase != PhaseReady {
		t.Error("want PhaseReady")
	}
}

func TestCommit_ReadAfterWrite(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	commit(t, s, newRec("e1", "p", "t1", 1, t0))
	got, ok := s.Latest(ctx, "t1")
	if !ok || got.EnvelopeID() != "e1" {
		t.Fatalf("latest=%q ok=%v", got.EnvelopeID(), ok)
	}
	// A newer record for the same target supersedes it (moreRecent by AcceptedAt).
	commit(t, s, newRec("e2", "p", "t1", 2, t0.Add(time.Minute)))
	if got, _ = s.Latest(ctx, "t1"); got.EnvelopeID() != "e2" {
		t.Fatalf("want e2, got %s", got.EnvelopeID())
	}
	list := s.ListLatest(ctx, ListOptions{})
	if len(list) != 1 || list[0].TargetKey != "t1" {
		t.Fatalf("list=%v", list)
	}
	if _, ok := s.Latest(ctx, "missing"); ok {
		t.Error("missing target must be absent")
	}
}

func TestCommit_BeforeRecover(t *testing.T) {
	s := openMem(t)
	if err := s.Commit(context.Background(), newRec("e1", "p", "t1", 1, time.Now())); err != ErrNotReady {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
}

func TestCommit_Duplicate(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	commit(t, s, newRec("e1", "p", "t1", 1, time.Now()))
	if err := s.Commit(ctx, newRec("e1", "p", "t1", 1, time.Now())); err != ErrAlreadyCommitted {
		t.Fatalf("want ErrAlreadyCommitted, got %v", err)
	}
}

func TestRecover_RebuildsFromDisk(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, "file://"+dir, "", WithInstanceID("w1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	commit(t, s, newRec("e1", "p", "t1", 1, at))
	commit(t, s, newRec("e2", "p", "t2", 2, at))
	commit(t, s, newRec("e3", "q", "t3", 1, at))
	commit(t, s, newRec("e4", "q", "t3", 2, at)) // same target and time, higher sequence wins
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(ctx, "file://"+dir, "", WithInstanceID("w1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	state, err := s2.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SeenEnvelopeIDs) != 4 {
		t.Errorf("seen=%d", len(state.SeenEnvelopeIDs))
	}
	if state.MaxSequence["p"] != 2 || state.MaxSequence["q"] != 2 {
		t.Errorf("maxSeq=%v", state.MaxSequence)
	}
	if got, _ := s2.Latest(ctx, "t3"); got.Sequence() != 2 {
		t.Errorf("tie-break seq=%d", got.Sequence())
	}
	if s2.Inspect(ctx).Phase != PhaseReady {
		t.Error("want PhaseReady")
	}
}

func TestRecover_Corruption(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	// Write unparseable objects directly through a helper bucket: one under a
	// producer folder, one stray at the top of envelopes/.
	hb, err := blob.OpenBucket(ctx, "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	phash := hashID("env-a")
	garbage := []byte("not json {")
	if err := hb.WriteAll(ctx, "envelopes/"+phash+"/00000000000000000001-x.json", garbage, nil); err != nil {
		t.Fatal(err)
	}
	if err := hb.WriteAll(ctx, "envelopes/stray.json", garbage, nil); err != nil {
		t.Fatal(err)
	}
	if err := hb.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, "file://"+dir, "", WithInstanceID("w1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	state, err := s.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Corruptions) != 2 {
		t.Fatalf("corruptions=%d", len(state.Corruptions))
	}
	if s.Inspect(ctx).Phase != PhaseDegraded {
		t.Error("want PhaseDegraded")
	}
	if _, ok := state.TaintedProducers[phash]; !ok {
		t.Error("producer folder not tainted")
	}
	if _, ok := state.TaintedProducers["stray.json"]; !ok {
		t.Error("stray object not tainted")
	}
	// The tainted producer is refused.
	if err := s.Commit(ctx, newRec("e1", "env-a", "t1", 1, time.Now())); err != ErrProducerTainted {
		t.Errorf("want ErrProducerTainted, got %v", err)
	}
	// A clean producer still commits while degraded.
	if err := s.Commit(ctx, newRec("e2", "clean", "t2", 1, time.Now())); err != nil {
		t.Errorf("clean commit: %v", err)
	}
}

func TestCommit_ImmutableWriteFails(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	orig := writeImmutable
	writeImmutable = func(context.Context, *blob.Bucket, string, []byte) error { return errors.New("boom") }
	defer func() { writeImmutable = orig }()
	if err := s.Commit(ctx, newRec("e1", "p", "t1", 1, time.Now())); err == nil {
		t.Fatal("want write error")
	}
	if _, ok := s.Latest(ctx, "t1"); ok {
		t.Error("failed write must not be accepted")
	}
	if s.Inspect(ctx).Records != 0 {
		t.Error("index must be unchanged")
	}
}

func TestCommit_ProjectionWriteFails(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	orig := writeProjection
	writeProjection = func(context.Context, *blob.Bucket, string, []byte) error { return errors.New("boom") }
	defer func() { writeProjection = orig }()
	if err := s.Commit(ctx, newRec("e1", "p", "t1", 1, time.Now())); err != nil {
		t.Fatalf("want nil (immutable accepted), got %v", err)
	}
	st := s.Inspect(ctx)
	if st.Phase != PhaseDegraded || !st.PendingRepair {
		t.Errorf("status=%+v", st)
	}
	if _, ok := s.Latest(ctx, "t1"); !ok {
		t.Error("record must remain retrievable")
	}
}

func TestCommit_MarshalFails(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	orig := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { jsonMarshal = orig }()
	if err := s.Commit(ctx, newRec("e1", "p", "t1", 1, time.Now())); err == nil {
		t.Fatal("want marshal error")
	}
}

func TestListLatest_FilterAndOrder(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	commit(t, s, newRec("e1", "p", "z", 1, now))
	commit(t, s, newRec("e2", "q", "a", 1, now))
	commit(t, s, newRec("e3", "p", "m", 2, now)) // producer p advances to seq 2
	all := s.ListLatest(ctx, ListOptions{})
	if len(all) != 3 || all[0].TargetKey != "a" || all[1].TargetKey != "m" || all[2].TargetKey != "z" {
		t.Fatalf("unsorted: %v", all)
	}
	only := s.ListLatest(ctx, ListOptions{Producer: "p"})
	if len(only) != 2 || only[0].TargetKey != "m" || only[1].TargetKey != "z" {
		t.Fatalf("filter failed: %v", only)
	}
}

func TestCommit_OutOfSequence(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	commit(t, s, newRec("e1", "p", "t", 5, now))
	// A newer sequence for the same producer is accepted.
	commit(t, s, newRec("e2", "p", "t", 6, now))
	// An equal or older sequence (a replay) is rejected, even with a fresh id.
	if err := s.Commit(ctx, newRec("e3", "p", "t", 6, now)); err != ErrOutOfSequence {
		t.Errorf("equal seq: got %v, want ErrOutOfSequence", err)
	}
	if err := s.Commit(ctx, newRec("e4", "p", "t", 3, now)); err != ErrOutOfSequence {
		t.Errorf("older seq: got %v, want ErrOutOfSequence", err)
	}
}

func TestRecover_ListError(t *testing.T) {
	s, err := Open(context.Background(), "mem://", "", WithInstanceID("w1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Recover(ctx); err == nil {
		t.Fatal("want error from a closed bucket")
	}
	if s.Inspect(ctx).Phase != PhaseFailed {
		t.Error("want PhaseFailed")
	}
	if err := s.Commit(ctx, newRec("e1", "p", "t1", 1, time.Now())); err != ErrNotReady {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
}

func TestInspect(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, "mem://", "p1", WithInstanceID("my-id"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	commit(t, s, newRec("e1", "p", "t1", 1, time.Now()))
	st := s.Inspect(ctx)
	if st.InstanceID != "my-id" || st.BucketURL != "mem://" || st.Prefix != "p1" {
		t.Errorf("identity=%+v", st)
	}
	if st.Records != 1 || st.Targets != 1 || st.Producers != 1 {
		t.Errorf("counts=%+v", st)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
