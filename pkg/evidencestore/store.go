// Package evidencestore is Pacto's durable store for accepted external evidence.
// It is deliberately infrastructure-light: the default deployment needs only a
// file:// bucket on a ReadWriteOnce PVC — no database, object-storage service,
// cloud account, cache or coordination service. When cloud storage is requested
// the same logic runs unchanged over s3://, gs:// or azblob:// via the Go Cloud
// Development Kit ([gocloud.dev/blob]).
//
// The correctness model is explicit and simple:
//
//   - There is ONE active writer per (bucket URL + prefix). This is enforced
//     operationally (Helm replicaCount 1, a schema that rejects more, startup
//     warnings, a unique instance id in diagnostics) — NOT with a fake
//     distributed lock built from blob writes.
//   - The only authoritative durable records are immutable accepted-evidence
//     records under <prefix>/envelopes/. Once written, a record is never
//     overwritten. Materialized projections under <prefix>/materialized/ are
//     rebuildable performance optimizations, never a source of truth.
//   - The immutable write is the commit point. Read-after-write within the
//     single writer is served from an in-memory index, never from bucket List.
//
// The [Store] interface expresses domain operations so ingestion never reasons
// about object keys, providers or bucket APIs. The gocloud-backed implementation
// lives behind it in this package.
package evidencestore

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// Phase is the store's lifecycle state, surfaced for readiness and diagnostics.
type Phase string

const (
	// PhaseStarting: the store is opening its bucket.
	PhaseStarting Phase = "starting"
	// PhaseRecovering: replaying immutable records to rebuild indexes.
	PhaseRecovering Phase = "recovering"
	// PhaseReady: recovery completed; ingestion may proceed.
	PhaseReady Phase = "ready"
	// PhaseDegraded: ready for reads, but a derived projection write failed or a
	// producer's history is partially corrupt; writes for affected producers are
	// refused until repair.
	PhaseDegraded Phase = "degraded"
	// PhaseFailed: recovery could not establish replay correctness at all.
	PhaseFailed Phase = "failed"
)

// AcceptedRecord is one immutable accepted-evidence record — the authoritative
// durable unit. Once committed it is never modified.
type AcceptedRecord struct {
	// Envelope is the original signed report.
	Envelope evidenceenvelope.Envelope `json:"envelope"`
	// AcceptedAt is when the record was committed.
	AcceptedAt time.Time `json:"acceptedAt"`
	// EvidenceDigest is a content digest over the carried EvidenceSet.
	EvidenceDigest string `json:"evidenceDigest"`
	// Findings, Coverage and Compliance are the evaluation the acceptor produced.
	Findings   []finding.Finding   `json:"findings,omitempty"`
	Coverage   validation.Coverage `json:"coverage"`
	Compliance string              `json:"compliance"`
	// TargetKey is the canonical operational-target identity this record reports.
	TargetKey string `json:"targetKey"`
	// ContractRef is the immutable contract reference the evidence was evaluated
	// against.
	ContractRef string `json:"contractRef"`
	// SchemaVersion identifies the record wire format. Recovery refuses (taints) a
	// record whose version it does not recognize rather than mis-parsing it.
	SchemaVersion string `json:"schemaVersion"`
}

// ProducerID is the reporting producer's id (from the envelope).
func (r AcceptedRecord) ProducerID() string { return r.Envelope.Producer.ID }

// Sequence is the producer-scoped monotonic sequence (from the envelope).
func (r AcceptedRecord) Sequence() uint64 { return r.Envelope.Sequence }

// EnvelopeID is the unique envelope id (from the envelope).
func (r AcceptedRecord) EnvelopeID() string { return r.Envelope.ID }

// Corruption reports one immutable record that could not be read or parsed
// during recovery. Corruptions are surfaced, never silently skipped.
type Corruption struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
	// Producer is the producer whose history the corrupt object belongs to, when
	// derivable from the key; its writes are refused until repaired.
	Producer string `json:"producer,omitempty"`
}

// RecoveredState is the replay- and read-critical state reconstructed from the
// immutable records at startup.
type RecoveredState struct {
	// SeenEnvelopeIDs are the ids already accepted (duplicate rejection).
	SeenEnvelopeIDs map[string]struct{}
	// MaxSequence is the highest accepted sequence per producer (ordering).
	MaxSequence map[string]uint64
	// LatestByTarget is the most recent accepted record per target.
	LatestByTarget map[string]AcceptedRecord
	// Corruptions are the records that could not be recovered.
	Corruptions []Corruption
	// TaintedProducers are producers whose replay correctness cannot be
	// guaranteed (a record in their history is corrupt); their writes are refused.
	TaintedProducers map[string]struct{}
}

// StoreStatus is a point-in-time diagnostic view of the store. It NEVER carries
// the raw bucket URL: only the backend scheme is exposed, so credentials, a
// private endpoint host, a path or query parameters can never leak through
// status, logs or CLI output.
type StoreStatus struct {
	Phase      Phase  `json:"phase"`
	InstanceID string `json:"instanceId"`
	// Backend is the bucket scheme only (file, s3, gs, azblob, mem) — never the
	// full URL, credentials, host or query.
	Backend     string       `json:"backend"`
	Prefix      string       `json:"prefix"`
	Records     int          `json:"records"`
	Targets     int          `json:"targets"`
	Producers   int          `json:"producers"`
	Corruptions []Corruption `json:"corruptions,omitempty"`
	// PendingRepair is true when a derived projection write failed and the store
	// is serving from memory until [BlobStore.RepairProjections] rewrites it.
	PendingRepair bool `json:"pendingRepair"`
}

// ListOptions bounds a [Store.ListLatest] query.
type ListOptions struct {
	// Producer, when set, restricts results to targets reported by that producer.
	Producer string
}

// Store persists accepted evidence durably and serves the latest per target.
// Implementations own their in-memory index; the ingestion workflow never sees
// object keys or bucket APIs.
type Store interface {
	// Recover opens the bucket, replays immutable records and reconstructs the
	// replay and latest-target indexes. It must complete before the store accepts
	// writes. A store whose replay correctness cannot be established returns an
	// error (the caller stays not-ready).
	Recover(ctx context.Context) (RecoveredState, error)
	// Commit writes an immutable record. The immutable write is the commit point:
	// on success the record is durably accepted and its id/sequence are reserved
	// in memory, even if a subsequent derived-projection write fails (which marks
	// the store degraded, never re-accepts). It returns [ErrAlreadyCommitted] if
	// the envelope id was already committed, and [ErrProducerTainted] if the
	// producer's history is corrupt.
	Commit(ctx context.Context, rec AcceptedRecord) error
	// Latest returns the most recent accepted record for a target, served from
	// the in-memory index.
	Latest(ctx context.Context, targetKey string) (AcceptedRecord, bool)
	// ListLatest returns the latest record per target, in deterministic order.
	ListLatest(ctx context.Context, opts ListOptions) []AcceptedRecord
	// Inspect returns the current diagnostic status.
	Inspect(ctx context.Context) StoreStatus
	// Close releases the bucket.
	Close() error
}

// Sentinel errors.
var (
	// ErrAlreadyCommitted means the envelope id is already durably accepted.
	ErrAlreadyCommitted = errors.New("evidence store: envelope already committed")
	// ErrOutOfSequence means the envelope's sequence is not greater than the
	// producer's highest accepted sequence — an old or replayed report. Replay
	// protection is enforced here, atomically with the immutable write, so it
	// survives process and pod restarts (Recover rebuilds the sequence high-water
	// mark from the immutable records).
	ErrOutOfSequence = errors.New("evidence store: envelope sequence is not newer than the producer's latest")
	// ErrProducerTainted means the producer's history is partially corrupt, so
	// replay correctness cannot be guaranteed and new writes are refused.
	ErrProducerTainted = errors.New("evidence store: producer history is corrupt; writes refused")
	// ErrNotReady means the store has not finished recovery.
	ErrNotReady = errors.New("evidence store: not ready")
)

// DefaultBucketURL is the zero-infrastructure default: a file bucket on a PVC.
const DefaultBucketURL = "file:///var/lib/pacto/evidence"

// RecordSchemaVersion is the current immutable-record wire format. Recovery
// rejects (taints) a record carrying any other version.
const RecordSchemaVersion = "pacto.dev/evidence-record/v1"

// NormalizePrefix validates and normalizes a logical key prefix. Every object
// key is scoped below it, so it must be a safe relative path: no absolute paths,
// no parent traversal, no empty or "." components. The normalized form has no
// leading or trailing slash and collapses internal separators.
func NormalizePrefix(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("evidence store: prefix %q must be relative, not absolute", raw)
	}
	// Reject Windows-style and scheme-like values outright.
	if strings.Contains(p, "\\") || strings.Contains(p, "://") {
		return "", fmt.Errorf("evidence store: prefix %q is malformed", raw)
	}
	parts := strings.Split(p, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			// Empty (from a double slash or trailing slash) and "." are dropped;
			// an all-empty result is caught below.
			continue
		case "..":
			return "", fmt.Errorf("evidence store: prefix %q must not contain parent traversal", raw)
		default:
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("evidence store: prefix %q has no usable path component", raw)
	}
	// clean holds only non-empty, non-"." and non-".." components (".." already
	// returned an error above), so path.Join cannot reintroduce traversal here.
	return path.Join(clean...), nil
}
