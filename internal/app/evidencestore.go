package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/evidencestore"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// recoverEvidence recovers a durable store; a seam so the Collect recovery-error
// path is testable without a fault-injecting filesystem.
var recoverEvidence = func(ctx context.Context, s *evidencestore.BlobStore) error {
	_, err := s.Recover(ctx)
	return err
}

// DefaultEvidencePrefix is the key prefix the durable evidence store uses within
// its bucket. The ingestion host and the fleet source share it so evidence
// written by `pacto evidence serve` is read back by `pacto fleet`.
const DefaultEvidencePrefix = "pacto-evidence/v1"

// openEvidenceStore opens a durable evidence store. For a file:// bucket it first
// creates the target directory (fileblob refuses a missing one), matching the
// zero-infrastructure "just a PVC" deployment. Cloud buckets are opened as-is.
func openEvidenceStore(ctx context.Context, bucketURL, prefix string) (*evidencestore.BlobStore, error) {
	if dir, ok := strings.CutPrefix(bucketURL, "file://"); ok {
		if i := strings.IndexByte(dir, '?'); i >= 0 {
			dir = dir[:i]
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return evidencestore.Open(ctx, bucketURL, prefix)
}

// toBucketURL maps a bare directory to a file:// URL (absolute), passing an
// already-schemed value (file://, s3://, gs://, azblob://) through unchanged.
func toBucketURL(pathOrURL string) string {
	if strings.Contains(pathOrURL, "://") {
		return pathOrURL
	}
	// filepath.Abs only fails if the working directory is unreadable, which the
	// rest of the CLI already assumes it is not; an empty result still yields a
	// valid (if useless) file:// URL rather than a branch to test.
	abs, _ := filepath.Abs(pathOrURL)
	return "file://" + abs
}

// durableEvidenceStore adapts the durable [evidencestore.BlobStore] to the
// ingestion layer's [evidenceingest.Store] port: Commit maps the store's replay
// sentinels to the ingest ErrReplay so the handler answers 409, and List
// projects the latest accepted records back to ingest records.
type durableEvidenceStore struct {
	store *evidencestore.BlobStore
}

// Commit implements [evidenceingest.Store].
func (d durableEvidenceStore) Commit(ctx context.Context, rec evidenceingest.Record) error {
	err := d.store.Commit(ctx, acceptedRecordFromIngest(rec))
	if errors.Is(err, evidencestore.ErrAlreadyCommitted) || errors.Is(err, evidencestore.ErrOutOfSequence) {
		return evidenceingest.ErrReplay
	}
	return err
}

// List implements [evidenceingest.Store].
func (d durableEvidenceStore) List(ctx context.Context) ([]evidenceingest.Record, error) {
	latest := d.store.ListLatest(ctx, evidencestore.ListOptions{})
	out := make([]evidenceingest.Record, 0, len(latest))
	for _, ar := range latest {
		out = append(out, recordFromAccepted(ar))
	}
	return out, nil
}

// acceptedRecordFromIngest builds the immutable durable record from an accepted
// ingest record and its envelope. The target key uses the same rule the ingest
// layer projects with, so durable and in-memory targets are identical.
func acceptedRecordFromIngest(rec evidenceingest.Record) evidencestore.AcceptedRecord {
	env := rec.Envelope
	return evidencestore.AcceptedRecord{
		Envelope:       env,
		AcceptedAt:     rec.AcceptedAt,
		EvidenceDigest: contentID(env.EvidenceSet),
		Findings:       rec.Findings,
		Coverage:       rec.Coverage,
		Compliance:     rec.Compliance,
		TargetKey:      string(fleet.NewTargetKey(env.Producer.ID, "external", env.EvidenceSet.Subject.Name)),
		ContractRef:    env.EvidenceSet.ContractRef,
	}
}

// recordFromAccepted is the inverse projection used by the read-only source API.
func recordFromAccepted(ar evidencestore.AcceptedRecord) evidenceingest.Record {
	return evidenceingest.Record{
		Envelope:   ar.Envelope,
		Compliance: ar.Compliance,
		Findings:   ar.Findings,
		Coverage:   ar.Coverage,
		AcceptedAt: ar.AcceptedAt,
	}
}

// durableEvidenceSource is a read-only fleet source over a durable evidence
// bucket: it opens the store, recovers it (rebuilding the latest-per-target
// index from the immutable records) and projects each latest record into an
// external target. The store is opened per Collect and closed after, so a fleet
// snapshot never holds the bucket open.
type durableEvidenceSource struct {
	id        string
	bucketURL string
	prefix    string
}

// newDurableEvidenceSource returns a source over an evidence bucket dir or URL.
func newDurableEvidenceSource(id, bucketOrDir string) *durableEvidenceSource {
	if id == "" {
		id = "evidence-ingest"
	}
	return &durableEvidenceSource{id: id, bucketURL: toBucketURL(bucketOrDir), prefix: DefaultEvidencePrefix}
}

// ID implements [fleet.Source].
func (s *durableEvidenceSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *durableEvidenceSource) Kind() string { return "evidence-ingest" }

// Collect opens the durable store read-only and projects its latest records.
func (s *durableEvidenceSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	store, err := openEvidenceStore(ctx, s.bucketURL, s.prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	if err := recoverEvidence(ctx, store); err != nil {
		return nil, err
	}
	col := &fleet.Collection{}
	for _, ar := range store.ListLatest(ctx, evidencestore.ListOptions{}) {
		col.Targets = append(col.Targets, rawTargetFromAccepted(ar))
	}
	return col, nil
}

// rawTargetFromAccepted projects one durable record into a fleet target, matching
// the shape the old file-store source produced.
func rawTargetFromAccepted(ar evidencestore.AcceptedRecord) fleet.RawTarget {
	env := ar.Envelope
	at := env.EvidenceSet.ObservedAt
	return fleet.RawTarget{
		Scope:        env.Producer.ID,
		Kind:         "external",
		Name:         env.EvidenceSet.Subject.Name,
		Service:      env.EvidenceSet.Subject.Name,
		ResolvedRef:  ar.ContractRef,
		Compliance:   ar.Compliance,
		Findings:     ar.Findings,
		Coverage:     &fleet.Coverage{Evaluated: ar.Coverage.Evaluated, Required: ar.Coverage.Required},
		EvidenceAt:   &at,
		ReconciledAt: &ar.AcceptedAt,
	}
}
