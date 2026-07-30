package evidencestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"gocloud.dev/blob"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// Seams, overridable in tests to exercise the write error paths without a real
// fault-injecting bucket. jsonMarshal mirrors filestore.go's fsMarshal; the two
// write seams wrap bucket.WriteAll so the immutable commit and the derived
// projection can be failed independently.
var (
	jsonMarshal = func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

	readAll = func(ctx context.Context, bucket *blob.Bucket, key string) ([]byte, error) {
		return bucket.ReadAll(ctx, key)
	}
	writeImmutable = func(ctx context.Context, bucket *blob.Bucket, key string, data []byte) error {
		return bucket.WriteAll(ctx, key, data, nil)
	}
	writeProjection = func(ctx context.Context, bucket *blob.Bucket, key string, data []byte) error {
		return bucket.WriteAll(ctx, key, data, nil)
	}

	// newInstanceID is the default instance-id generator. It is a seam so tests
	// can be deterministic; production callers may also pass WithInstanceID.
	newInstanceID = func() string {
		var buf [8]byte
		// ponytail: the id is a diagnostic marker, not a secret; a rand miss just
		// yields the zero id, so the error is ignored rather than seamed.
		_, _ = rand.Read(buf[:])
		return "instance-" + hex.EncodeToString(buf[:])
	}
)

// Option configures a BlobStore at Open time.
type Option func(*BlobStore)

// WithClock injects the time source used for projection timestamps.
func WithClock(clock func() time.Time) Option {
	return func(b *BlobStore) { b.now = clock }
}

// WithInstanceID sets the store's unique instance id (single-writer diagnostics).
func WithInstanceID(id string) Option {
	return func(b *BlobStore) { b.instanceID = id }
}

// BlobStore is the gocloud.dev/blob-backed [Store]. Immutable accepted-evidence
// records under <prefix>/envelopes/ are the sole source of truth; materialized
// projections under <prefix>/materialized/ are rebuildable optimizations. A
// single writer owns the in-memory index and serves every read from it.
type BlobStore struct {
	bucket     *blob.Bucket
	prefix     string
	bucketURL  string
	instanceID string
	now        func() time.Time

	mu     sync.Mutex
	phase  Phase
	seen   map[string]struct{}       // accepted envelope ids
	maxSeq map[string]uint64         // producer id -> highest sequence
	latest map[string]AcceptedRecord // target key -> most recent record
	// tainted holds hashed producer ids whose history is partially corrupt; keyed
	// by hash because a corrupt record's original producer id cannot be read back.
	tainted       map[string]struct{}
	corruptions   []Corruption
	pendingRepair bool
}

// Open opens the bucket, normalizes the prefix and returns a not-yet-recovered
// store. Recover must run before it accepts writes.
func Open(ctx context.Context, bucketURL, prefix string, opts ...Option) (*BlobStore, error) {
	normalized, err := NormalizePrefix(prefix)
	if err != nil {
		return nil, err
	}
	bucket, err := blob.OpenBucket(ctx, bucketURL)
	if err != nil {
		return nil, err
	}
	b := &BlobStore{
		bucket:     bucket,
		prefix:     normalized,
		bucketURL:  bucketURL,
		instanceID: newInstanceID(),
		now:        time.Now,
		phase:      PhaseStarting,
	}
	b.resetIndex()
	for _, opt := range opts {
		opt(b)
	}
	return b, nil
}

// resetIndex clears the in-memory index to its empty starting state.
func (b *BlobStore) resetIndex() {
	b.seen = map[string]struct{}{}
	b.maxSeq = map[string]uint64{}
	b.latest = map[string]AcceptedRecord{}
	b.tainted = map[string]struct{}{}
	b.corruptions = nil
	b.pendingRepair = false
}

// Recover rebuilds the in-memory index from the immutable records alone.
func (b *BlobStore) Recover(ctx context.Context) (RecoveredState, error) {
	// ponytail: rebuild-from-immutable is the whole model this ships. A checkpoint
	// fast-path that trusts a materialized projection is deliberately future work.
	b.mu.Lock()
	defer b.mu.Unlock()
	b.phase = PhaseRecovering
	b.resetIndex()
	listPrefix := path.Join(b.prefix, "envelopes") + "/"
	it := b.bucket.List(&blob.ListOptions{Prefix: listPrefix})
	for {
		obj, err := it.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			b.phase = PhaseFailed
			return RecoveredState{}, err
		}
		b.recoverOne(ctx, obj.Key, listPrefix)
	}
	if len(b.corruptions) == 0 {
		b.phase = PhaseReady
	} else {
		b.phase = PhaseDegraded
	}
	return b.snapshotState(), nil
}

// recoverOne reads one immutable object into the index, or taints its producer
// when the object cannot be read, parsed or validated.
func (b *BlobStore) recoverOne(ctx context.Context, key, listPrefix string) {
	data, err := readAll(ctx, b.bucket, key)
	if err != nil {
		b.taint(key, listPrefix, err)
		return
	}
	rec, err := decodeRecord(data)
	if err != nil {
		b.taint(key, listPrefix, err)
		return
	}
	if err := b.validateRecovered(rec, key); err != nil {
		b.taint(key, listPrefix, err)
		return
	}
	b.index(rec)
}

// decodeRecord strictly decodes an immutable record, rejecting unknown fields so a
// schema change or an injected field is reported as corruption, not silently
// mis-parsed.
func decodeRecord(data []byte) (AcceptedRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec AcceptedRecord
	if err := dec.Decode(&rec); err != nil {
		return AcceptedRecord{}, err
	}
	return rec, nil
}

// validateRecovered re-verifies a recovered record's structural and identity
// integrity before it re-enters the index, so a syntactically valid but tampered
// or degenerate object is reported as corruption instead of poisoning the replay
// and latest-target state. It checks everything re-derivable WITHOUT trusting the
// bucket: the record schema version; non-empty producer and envelope ids (a "{}"
// object can no longer be indexed as a record); the object-key binding
// (recomputing the immutable key ties the object to its producer, envelope id and
// sequence, so a moved, re-producered, re-sequenced or re-identified object is
// rejected); the evidence digest recomputed over the carried EvidenceSet; and the
// contract reference matching the one inside the evidence set.
//
// The producer's envelope SIGNATURE is verified at INGEST and the immutable write
// is the commit point. Recovery does NOT re-verify the signature (that needs the
// trust store — see the evidence protocol) nor re-derive the acceptor's own
// evaluation (findings/coverage/compliance) or the target key: those are trusted
// from the single active writer's bucket, a private ReadWriteOnce PVC by default
// or an IAM-scoped bucket for cloud backends. A full bucket-compromise adversary
// is out of that trust model.
func (b *BlobStore) validateRecovered(rec AcceptedRecord, key string) error {
	if rec.SchemaVersion != RecordSchemaVersion {
		return fmt.Errorf("unknown record schema version %q", rec.SchemaVersion)
	}
	if rec.EnvelopeID() == "" || rec.ProducerID() == "" {
		return fmt.Errorf("record has an empty envelope or producer id")
	}
	if key != b.immutableKey(rec) {
		return fmt.Errorf("record body does not bind its object key")
	}
	if EvidenceDigest(rec.Envelope.EvidenceSet) != rec.EvidenceDigest {
		return fmt.Errorf("evidence digest does not match the carried evidence set")
	}
	if rec.ContractRef != rec.Envelope.EvidenceSet.ContractRef {
		return fmt.Errorf("contract reference does not match the evidence set")
	}
	return nil
}

// EvidenceDigest is the canonical content digest over an EvidenceSet. The acceptor
// stamps it on each record and recovery recomputes it, so a tampered EvidenceSet
// is detected. Deterministic: encoding/json sorts map keys.
func EvidenceDigest(set evidence.EvidenceSet) string {
	raw, _ := json.Marshal(set)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// taint records a corruption and refuses future writes for the affected producer
// (identified by the hashed producer segment of the key).
func (b *BlobStore) taint(key, listPrefix string, cause error) {
	producer := producerFromKey(key, listPrefix)
	b.corruptions = append(b.corruptions, Corruption{Key: key, Reason: cause.Error(), Producer: producer})
	b.tainted[producer] = struct{}{}
}

// index folds one accepted record into the seen, max-sequence and latest maps.
func (b *BlobStore) index(rec AcceptedRecord) {
	b.seen[rec.EnvelopeID()] = struct{}{}
	if cur, ok := b.maxSeq[rec.ProducerID()]; !ok || rec.Sequence() > cur {
		b.maxSeq[rec.ProducerID()] = rec.Sequence()
	}
	if cur, ok := b.latest[rec.TargetKey]; !ok || moreRecent(rec, cur) {
		b.latest[rec.TargetKey] = rec
	}
}

// snapshotState exposes the current index as RecoveredState. Every map and record
// is deep-copied, so a caller can retain and mutate the returned state without
// ever touching store-owned memory (defends against data races and accidental
// mutation, even though records are immutable in the store).
func (b *BlobStore) snapshotState() RecoveredState {
	seen := make(map[string]struct{}, len(b.seen))
	for k := range b.seen {
		seen[k] = struct{}{}
	}
	maxSeq := make(map[string]uint64, len(b.maxSeq))
	for k, v := range b.maxSeq {
		maxSeq[k] = v
	}
	latest := make(map[string]AcceptedRecord, len(b.latest))
	for k, v := range b.latest {
		latest[k] = cloneRecord(v)
	}
	tainted := make(map[string]struct{}, len(b.tainted))
	for k := range b.tainted {
		tainted[k] = struct{}{}
	}
	return RecoveredState{
		SeenEnvelopeIDs:  seen,
		MaxSequence:      maxSeq,
		LatestByTarget:   latest,
		Corruptions:      append([]Corruption(nil), b.corruptions...),
		TaintedProducers: tainted,
	}
}

// cloneRecord returns a fully independent copy of rec so a returned value can
// never alias store-owned nested slices/maps (Findings, Coverage, the envelope's
// observations). ponytail: a JSON round-trip is the simple provably-isolated
// clone; if read throughput ever dominates, replace with field-wise deep copies.
func cloneRecord(rec AcceptedRecord) AcceptedRecord {
	data, _ := json.Marshal(rec)
	var out AcceptedRecord
	_ = json.Unmarshal(data, &out)
	return out
}

// Commit writes the immutable record (the commit point), reserves its id and
// sequence in memory, then best-effort writes the derived projections.
func (b *BlobStore) Commit(ctx context.Context, rec AcceptedRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase == PhaseStarting || b.phase == PhaseFailed {
		return ErrNotReady
	}
	// The store owns record metadata: stamp the schema version and (re)compute the
	// evidence digest so every committed record is self-consistent and passes the
	// same integrity checks recovery applies. A caller can never commit a record
	// that would later fail to recover on these fields.
	rec.SchemaVersion = RecordSchemaVersion
	rec.EvidenceDigest = EvidenceDigest(rec.Envelope.EvidenceSet)
	if _, ok := b.seen[rec.EnvelopeID()]; ok {
		return ErrAlreadyCommitted
	}
	if _, ok := b.tainted[hashID(rec.ProducerID())]; ok {
		return ErrProducerTainted
	}
	// Replay protection: the sequence must be strictly newer than the producer's
	// high-water mark. This is atomic with the immutable write below and survives
	// restarts because Recover rebuilds maxSeq from the immutable records.
	if cur, ok := b.maxSeq[rec.ProducerID()]; ok && rec.Sequence() <= cur {
		return ErrOutOfSequence
	}
	data, err := jsonMarshal(rec)
	if err != nil {
		return err
	}
	if err := writeImmutable(ctx, b.bucket, b.immutableKey(rec), data); err != nil {
		return err // not accepted; the index is untouched
	}
	b.index(rec) // commit point reached: reserve id and sequence
	if err := b.writeProjections(ctx, rec, data); err != nil {
		b.phase = PhaseDegraded
		b.pendingRepair = true
	}
	return nil
}

// writeProjections writes the per-target latest record and the manifest summary.
func (b *BlobStore) writeProjections(ctx context.Context, rec AcceptedRecord, data []byte) error {
	if err := writeProjection(ctx, b.bucket, b.targetKeyPath(rec.TargetKey), data); err != nil {
		return err
	}
	manifest := fmt.Sprintf(`{"records":%d,"generatedAt":%q}`, len(b.seen), b.now().UTC().Format(time.RFC3339Nano))
	return writeProjection(ctx, b.bucket, b.manifestKeyPath(), []byte(manifest))
}

// projection is one materialized-projection write (key + bytes).
type projection struct {
	key  string
	data []byte
}

// RepairProjections rewrites every materialized projection from the in-memory
// index and, on full success, clears the pending-repair flag and restores
// readiness (unless recovery found corruptions). It is the explicit, idempotent
// repair for a store that went degraded because a derived-projection write failed
// AFTER an accepted immutable write: the immutable record was never lost — only
// its rebuildable projection — so replay protection and the latest-target index
// stay correct in memory throughout, and a repeated envelope is still rejected
// while the store is degraded. If a rewrite fails the store stays degraded.
func (b *BlobStore) RepairProjections(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.pendingRepair {
		return nil
	}
	writes := make([]projection, 0, len(b.latest)+1)
	for _, rec := range b.latest {
		data, _ := jsonMarshal(rec)
		writes = append(writes, projection{key: b.targetKeyPath(rec.TargetKey), data: data})
	}
	manifest := fmt.Sprintf(`{"records":%d,"generatedAt":%q}`, len(b.seen), b.now().UTC().Format(time.RFC3339Nano))
	writes = append(writes, projection{key: b.manifestKeyPath(), data: []byte(manifest)})
	for _, w := range writes {
		if err := writeProjection(ctx, b.bucket, w.key, w.data); err != nil {
			return err // still degraded; pendingRepair stays set
		}
	}
	b.pendingRepair = false
	if len(b.corruptions) == 0 {
		b.phase = PhaseReady
	}
	return nil
}

// Latest returns the most recent accepted record for a target from memory.
func (b *BlobStore) Latest(_ context.Context, targetKey string) (AcceptedRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.latest[targetKey]
	if !ok {
		return AcceptedRecord{}, false
	}
	return cloneRecord(rec), true
}

// ListLatest returns the latest record per target in target-key order, optionally
// filtered to one producer.
func (b *BlobStore) ListLatest(_ context.Context, opts ListOptions) []AcceptedRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]AcceptedRecord, 0, len(b.latest))
	for _, rec := range b.latest {
		if opts.Producer != "" && rec.ProducerID() != opts.Producer {
			continue
		}
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TargetKey < out[j].TargetKey })
	return out
}

// Inspect returns a point-in-time diagnostic view of the store. The bucket URL is
// reduced to its scheme so credentials or a private endpoint can never leak.
func (b *BlobStore) Inspect(_ context.Context) StoreStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return StoreStatus{
		Phase:         b.phase,
		InstanceID:    b.instanceID,
		Backend:       bucketScheme(b.bucketURL),
		Prefix:        b.prefix,
		Records:       len(b.seen),
		Targets:       len(b.latest),
		Producers:     len(b.maxSeq),
		Corruptions:   append([]Corruption(nil), b.corruptions...),
		PendingRepair: b.pendingRepair,
	}
}

// bucketScheme returns just the URL scheme (file, s3, gs, azblob, mem) — never the
// host, path, credentials or query — so a diagnostic can never leak them.
func bucketScheme(url string) string {
	return strings.SplitN(url, "://", 2)[0]
}

// Close releases the underlying bucket.
func (b *BlobStore) Close() error { return b.bucket.Close() }

// immutableKey builds the immutable record key: producer and envelope ids are
// hashed so arbitrary characters never escape the layout, and the sequence is
// zero-padded so keys sort in commit order.
func (b *BlobStore) immutableKey(rec AcceptedRecord) string {
	name := fmt.Sprintf("%020d-%s.json", rec.Sequence(), hashID(rec.EnvelopeID()))
	return path.Join(b.prefix, "envelopes", hashID(rec.ProducerID()), name)
}

// targetKeyPath is the per-target latest-projection key.
func (b *BlobStore) targetKeyPath(targetKey string) string {
	return path.Join(b.prefix, "materialized", "targets", hashID(targetKey), "latest.json")
}

// manifestKeyPath is the manifest-projection key.
func (b *BlobStore) manifestKeyPath() string {
	return path.Join(b.prefix, "materialized", "manifest.json")
}

// producerFromKey extracts the hashed producer segment from an immutable key,
// falling back to the trailing name when a stray object has no producer folder.
func producerFromKey(key, listPrefix string) string {
	rest := strings.TrimPrefix(key, listPrefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// moreRecent reports whether a supersedes b as the latest for a target: later
// AcceptedAt wins, ties break to the higher sequence.
func moreRecent(a, b AcceptedRecord) bool {
	if a.AcceptedAt.Equal(b.AcceptedAt) {
		return a.Sequence() > b.Sequence()
	}
	return a.AcceptedAt.After(b.AcceptedAt)
}

// hashID hex-encodes the sha256 of s for use as a safe key segment.
func hashID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
