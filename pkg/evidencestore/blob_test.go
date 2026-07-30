package evidencestore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"gocloud.dev/blob"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func newRec(id, producer, target string, seq uint64, at time.Time) AcceptedRecord {
	env := evidenceenvelope.Envelope{
		ID:       id,
		Producer: evidenceenvelope.Producer{ID: producer},
		Sequence: seq,
	}
	return AcceptedRecord{
		Envelope:       env,
		TargetKey:      target,
		AcceptedAt:     at,
		SchemaVersion:  RecordSchemaVersion,
		EvidenceDigest: EvidenceDigest(env.EvidenceSet),
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

func TestReturnedState_IsDeepCopied(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	rec := newRec("e1", "prod", "t1", 1, time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	rec.Findings = []finding.Finding{{Code: "X"}}
	rec.Compliance = "NonCompliant"
	commit(t, s, rec)

	// Latest: mutating the returned record must never touch store-owned memory.
	got, _ := s.Latest(ctx, "t1")
	got.Findings[0].Code = "TAMPERED"
	got.Findings = append(got.Findings, finding.Finding{Code: "EXTRA"})
	got.Compliance = "Compliant"
	if again, _ := s.Latest(ctx, "t1"); len(again.Findings) != 1 || again.Findings[0].Code != "X" || again.Compliance != "NonCompliant" {
		t.Errorf("Latest leaked a caller mutation: %+v", again)
	}

	// ListLatest: same isolation.
	list := s.ListLatest(ctx, ListOptions{})
	list[0].Findings[0].Code = "TAMPERED"
	if l2 := s.ListLatest(ctx, ListOptions{}); l2[0].Findings[0].Code != "X" {
		t.Errorf("ListLatest leaked a caller mutation: %+v", l2[0])
	}

	// Recover's RecoveredState maps must be independent of the store's live index.
	st, err := s.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	delete(st.SeenEnvelopeIDs, "e1")
	st.MaxSequence["prod"] = 999
	delete(st.LatestByTarget, "t1")
	if err := s.Commit(ctx, rec); err != ErrAlreadyCommitted {
		t.Errorf("store seen map was mutated via RecoveredState: %v", err)
	}
	if _, ok := s.Latest(ctx, "t1"); !ok {
		t.Error("store latest map was mutated via RecoveredState")
	}
}

func TestRecover_TamperedRecordsAreCorruption(t *testing.T) {
	ctx := context.Background()
	valid := newRec("e1", "prod", "t1", 1, fixedClock()()) // schema + evidence digest set by newRec; ContractRef "" matches empty EvidenceSet

	keyOf := func(rec AcceptedRecord) string { return (&BlobStore{prefix: ""}).immutableKey(rec) }
	mustJSON := func(rec AcceptedRecord) []byte { d, _ := json.Marshal(rec); return d }

	// recoverInto writes one crafted object under envelopes/ and recovers a fresh
	// store over it, returning the reconstructed state.
	recoverInto := func(t *testing.T, key string, data []byte) RecoveredState {
		t.Helper()
		dir := t.TempDir()
		hb, err := blob.OpenBucket(ctx, "file://"+dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := hb.WriteAll(ctx, key, data, nil); err != nil {
			t.Fatal(err)
		}
		_ = hb.Close()
		s, err := Open(ctx, "file://"+dir, "", WithInstanceID("w"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		st, err := s.Recover(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	cases := []struct {
		name string
		key  string
		data []byte
	}{
		{"unknown schema version", keyOf(valid), func() []byte { r := valid; r.SchemaVersion = "bogus"; return mustJSON(r) }()},
		{"empty envelope id", keyOf(valid), func() []byte { r := valid; r.Envelope.ID = ""; return mustJSON(r) }()},
		{"object moved under another producer", "envelopes/" + hashID("other") + "/00000000000000000001-x.json", mustJSON(valid)},
		{"tampered evidence digest", keyOf(valid), func() []byte { r := valid; r.EvidenceDigest = "sha256:tampered"; return mustJSON(r) }()},
		{"tampered contract ref", keyOf(valid), func() []byte { r := valid; r.ContractRef = "oci://evil"; return mustJSON(r) }()},
		{"unknown field", keyOf(valid), []byte(`{"schemaVersion":"` + RecordSchemaVersion + `","evilField":true}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := recoverInto(t, tc.key, tc.data)
			if len(st.Corruptions) != 1 {
				t.Fatalf("want 1 corruption, got %d: %+v", len(st.Corruptions), st.Corruptions)
			}
			if len(st.SeenEnvelopeIDs) != 0 {
				t.Errorf("tampered record must not be indexed, seen=%v", st.SeenEnvelopeIDs)
			}
		})
	}
}

func TestRepairProjections(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	commit(t, s, newRec("e1", "p", "t1", 1, fixedClock()()))

	// Force projection writes to fail so the next commit degrades the store.
	orig := writeProjection
	defer func() { writeProjection = orig }()
	writeProjection = func(context.Context, *blob.Bucket, string, []byte) error {
		return errors.New("disk full")
	}
	commit(t, s, newRec("e2", "p", "t2", 2, fixedClock()())) // Commit returns nil; store degrades
	if st := s.Inspect(ctx); st.Phase != PhaseDegraded || !st.PendingRepair {
		t.Fatalf("want degraded+pendingRepair, got phase=%s pending=%v", st.Phase, st.PendingRepair)
	}
	// The immutable record is still accepted: replay stays blocked while degraded.
	if err := s.Commit(ctx, newRec("e2", "p", "t2", 2, fixedClock()())); err != ErrAlreadyCommitted {
		t.Errorf("replay must stay blocked while degraded, got %v", err)
	}
	// Repair while writes still fail -> stays degraded.
	if err := s.RepairProjections(ctx); err == nil {
		t.Error("repair should fail while projection writes fail")
	}
	if st := s.Inspect(ctx); st.Phase != PhaseDegraded || !st.PendingRepair {
		t.Errorf("still degraded expected, got phase=%s pending=%v", st.Phase, st.PendingRepair)
	}
	// Restore writes; repair succeeds -> ready + cleared.
	writeProjection = orig
	if err := s.RepairProjections(ctx); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if st := s.Inspect(ctx); st.Phase != PhaseReady || st.PendingRepair {
		t.Errorf("want ready+cleared, got phase=%s pending=%v", st.Phase, st.PendingRepair)
	}
	// Idempotent: repairing a healthy store is a no-op.
	if err := s.RepairProjections(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRecover_ImpossibleHistory(t *testing.T) {
	ctx := context.Background()
	keyOf := func(rec AcceptedRecord) string { return (&BlobStore{prefix: ""}).immutableKey(rec) }
	mustJSON := func(rec AcceptedRecord) []byte { d, _ := json.Marshal(rec); return d }
	at := fixedClock()()

	// write commits crafted objects DIRECTLY to a fresh bucket (bypassing Commit's
	// replay protection, which makes these states unreachable in normal operation)
	// then recovers a store over them.
	recover := func(t *testing.T, objs map[string][]byte) (RecoveredState, *BlobStore) {
		t.Helper()
		dir := t.TempDir()
		hb, err := blob.OpenBucket(ctx, "file://"+dir)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range objs {
			if err := hb.WriteAll(ctx, k, v, nil); err != nil {
				t.Fatal(err)
			}
		}
		_ = hb.Close()
		s, err := Open(ctx, "file://"+dir, "", WithInstanceID("w"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		st, err := s.Recover(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st, s
	}

	t.Run("forked producer sequence", func(t *testing.T) {
		a := newRec("secret-env-a", "secret-producer", "t1", 7, at)
		b := newRec("secret-env-b", "secret-producer", "t2", 7, at) // same producer+sequence, distinct envelope: a fork
		st, s := recover(t, map[string][]byte{keyOf(a): mustJSON(a), keyOf(b): mustJSON(b)})
		if len(st.Corruptions) != 1 {
			t.Fatalf("want 1 impossible-history corruption, got %+v", st.Corruptions)
		}
		if len(st.SeenEnvelopeIDs) != 1 {
			t.Errorf("the first record must stay indexed, seen=%v", st.SeenEnvelopeIDs)
		}
		if _, ok := st.TaintedProducers[hashID("secret-producer")]; !ok {
			t.Error("a forked producer must be tainted")
		}
		// The reason must NOT leak the raw producer or envelope id values.
		if r := st.Corruptions[0].Reason; !containsAll(r, "impossible history", "sequence") ||
			containsAny(r, "secret-producer", "secret-env-a", "secret-env-b") {
			t.Errorf("reason leaks ids or is unclear: %q", r)
		}
		if err := s.Commit(ctx, newRec("envC", "secret-producer", "t3", 99, at)); err != ErrProducerTainted {
			t.Errorf("tainted producer must be refused, got %v", err)
		}
	})

	t.Run("envelope committed more than once", func(t *testing.T) {
		a := newRec("envDup", "prod", "t1", 1, at)
		b := newRec("envDup", "prod", "t1", 2, at) // same envelope id re-committed at a new sequence
		st, _ := recover(t, map[string][]byte{keyOf(a): mustJSON(a), keyOf(b): mustJSON(b)})
		if len(st.Corruptions) != 1 || len(st.SeenEnvelopeIDs) != 1 {
			t.Fatalf("want 1 corruption + 1 indexed, got corruptions=%+v seen=%v", st.Corruptions, st.SeenEnvelopeIDs)
		}
	})
}

// driftStore writes one valid immutable record plus an optional manifest
// projection to a fresh bucket and returns an opened (not-yet-recovered) store, so
// the projection-drift tests can each recover it and assert the outcome.
func driftStore(t *testing.T, manifest []byte) *BlobStore {
	t.Helper()
	ctx := context.Background()
	rec := newRec("e1", "p", "t1", 1, fixedClock()())
	dir := t.TempDir()
	hb, err := blob.OpenBucket(ctx, "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	key := (&BlobStore{prefix: ""}).immutableKey(rec)
	body, _ := json.Marshal(rec)
	if err := hb.WriteAll(ctx, key, body, nil); err != nil {
		t.Fatal(err)
	}
	if manifest != nil {
		if err := hb.WriteAll(ctx, "materialized/manifest.json", manifest, nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = hb.Close()
	s, err := Open(ctx, "file://"+dir, "", WithInstanceID("w"), WithClock(fixedClock()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRecover_ProjectionDrift_MissingManifestRepairs(t *testing.T) {
	ctx := context.Background()
	s := driftStore(t, nil)
	st, err := s.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Corruptions) != 0 {
		t.Fatalf("drift is not a corruption: %+v", st.Corruptions)
	}
	if ins := s.Inspect(ctx); ins.Phase != PhaseDegraded || !ins.PendingRepair {
		t.Fatalf("want degraded+pendingRepair, got %+v", ins)
	}
	// A physical repair rewrites projections from the recovered index and restores
	// readiness; a re-recovery then sees a consistent manifest.
	if err := s.RepairProjections(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if ins := s.Inspect(ctx); ins.Phase != PhaseReady || ins.PendingRepair {
		t.Fatalf("re-recovery after physical repair must be clean, got %+v", ins)
	}
}

func TestRecover_ProjectionDrift_StaleCountRepairs(t *testing.T) {
	s := driftStore(t, []byte(`{"records":99,"generatedAt":"x"}`))
	if _, err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Inspect(context.Background()).PendingRepair {
		t.Error("a record-count mismatch must flag a repair")
	}
}

func TestRecover_ProjectionDrift_UnparsableRepairs(t *testing.T) {
	s := driftStore(t, []byte("not json"))
	if _, err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Inspect(context.Background()).PendingRepair {
		t.Error("an unparsable manifest must flag a repair")
	}
}

func TestRecover_ProjectionDrift_ConsistentStaysReady(t *testing.T) {
	s := driftStore(t, []byte(`{"records":1,"generatedAt":"x"}`))
	if _, err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ins := s.Inspect(context.Background()); ins.Phase != PhaseReady || ins.PendingRepair {
		t.Fatalf("a consistent manifest must stay ready, got %+v", ins)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestSameDeviceTempURL(t *testing.T) {
	cases := map[string]string{
		"file:///var/lib/pacto":        "file:///var/lib/pacto?no_tmp_dir=true",        // add with ?
		"file:///data?create_dir=true": "file:///data?create_dir=true&no_tmp_dir=true", // add with &
		"file:///data?no_tmp_dir=true": "file:///data?no_tmp_dir=true",                 // already set → unchanged
		"mem://":                       "mem://",                                       // non-file → unchanged
		"s3://bucket/prefix":           "s3://bucket/prefix",                           // non-file → unchanged
	}
	for in, want := range cases {
		if got := sameDeviceTempURL(in); got != want {
			t.Errorf("sameDeviceTempURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPhase(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	if s.Phase() != PhaseStarting {
		t.Errorf("initial phase = %s, want starting", s.Phase())
	}
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Phase() != PhaseReady {
		t.Errorf("recovered phase = %s, want ready", s.Phase())
	}
	// A zero-value store (before Open ever set the atomic) reports starting.
	if (&BlobStore{}).Phase() != PhaseStarting {
		t.Error("zero-value Phase should be starting")
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
	// Backend is the scheme only — never the raw URL, credentials or endpoint.
	if st.InstanceID != "my-id" || st.Backend != "mem" || st.Prefix != "p1" {
		t.Errorf("identity=%+v", st)
	}
	if st.Records != 1 || st.Targets != 1 || st.Producers != 1 {
		t.Errorf("counts=%+v", st)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
