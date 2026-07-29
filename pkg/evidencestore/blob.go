package evidencestore

import (
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
// when the object cannot be read or parsed.
func (b *BlobStore) recoverOne(ctx context.Context, key, listPrefix string) {
	data, err := readAll(ctx, b.bucket, key)
	if err != nil {
		b.taint(key, listPrefix, err)
		return
	}
	var rec AcceptedRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		b.taint(key, listPrefix, err)
		return
	}
	b.index(rec)
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

// snapshotState exposes the current index as RecoveredState. The maps are shared
// with the store, so the value is a startup snapshot consumed once by readiness.
func (b *BlobStore) snapshotState() RecoveredState {
	return RecoveredState{
		SeenEnvelopeIDs:  b.seen,
		MaxSequence:      b.maxSeq,
		LatestByTarget:   b.latest,
		Corruptions:      b.corruptions,
		TaintedProducers: b.tainted,
	}
}

// Commit writes the immutable record (the commit point), reserves its id and
// sequence in memory, then best-effort writes the derived projections.
func (b *BlobStore) Commit(ctx context.Context, rec AcceptedRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase == PhaseStarting || b.phase == PhaseFailed {
		return ErrNotReady
	}
	if _, ok := b.seen[rec.EnvelopeID()]; ok {
		return ErrAlreadyCommitted
	}
	if _, ok := b.tainted[hashID(rec.ProducerID())]; ok {
		return ErrProducerTainted
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

// Latest returns the most recent accepted record for a target from memory.
func (b *BlobStore) Latest(_ context.Context, targetKey string) (AcceptedRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.latest[targetKey]
	return rec, ok
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
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TargetKey < out[j].TargetKey })
	return out
}

// Inspect returns a point-in-time diagnostic view of the store.
func (b *BlobStore) Inspect(_ context.Context) StoreStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return StoreStatus{
		Phase:         b.phase,
		InstanceID:    b.instanceID,
		BucketURL:     b.bucketURL,
		Prefix:        b.prefix,
		Records:       len(b.seen),
		Targets:       len(b.latest),
		Producers:     len(b.maxSeq),
		Corruptions:   b.corruptions,
		PendingRepair: b.pendingRepair,
	}
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
