package fleet

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// Source is a fleet-owned ingestion seam. A source produces raw revision and
// target records for [Build] to compose. The interface is framework-neutral: a
// Kubernetes-backed source, an OCI-backed source, a local source, or a
// dashboard adapter each implement it, and none of them leak their transport
// into pkg/fleet. This is the inversion the architecture requires — the fleet
// layer defines the contract; adapters (including the dashboard) depend inward
// on it.
type Source interface {
	// ID is the stable identifier of this source instance (e.g. "oci", "k8s",
	// "local", or a named environment like "production-eu"). Used as provenance.
	ID() string
	// Kind classifies the source (e.g. "oci", "kubernetes", "local", "cache").
	Kind() string
	// Collect gathers everything this source can observe right now. It must
	// honor ctx cancellation. A non-nil error means the source could not be
	// observed at all; Build records it as unavailable (sanitized) rather than
	// as an empty result.
	Collect(ctx context.Context) (*Collection, error)
}

// Collection is one source's contribution to a snapshot. A source that
// partially succeeded may return records AND set State so its degraded status
// reaches the snapshot instead of masquerading as complete data.
type Collection struct {
	Revisions []RawRevision
	Targets   []RawTarget
	// State, when non-nil, overrides Build's derived source state. Use it to
	// report a source that succeeded but is stale or partial. Its ID/Kind are
	// filled from the source if left blank.
	State *SourceState
	// Limitations report record-level problems a source hit while collecting
	// (an unreadable bundle, an invalid fixture entry). A non-empty Limitations
	// implies the source is partial: usable records are kept AND the problem is
	// surfaced, so "an invalid record exists" is never confused with "no record
	// exists". Messages must already be sanitized.
	Limitations []Limitation
}

// RawRevision is what a definition or baseline source knows about a resolved
// contract revision. Bundle carries the parsed contract and its FS; the
// resolved identity (requestedRef → resolvedRef + digest) is preserved.
type RawRevision struct {
	Bundle       *contract.Bundle
	RequestedRef string
	ResolvedRef  string
	Digest       string
	Lock         *lock.Lock
	FetchedAt    *time.Time
}

// RawTarget is what an observer source knows about a concrete deployed instance.
type RawTarget struct {
	Scope           string
	Kind            string
	Name            string
	Labels          map[string]string
	Service         string
	RequestedRef    string
	ResolvedRef     string
	Digest          string
	Compliance      string
	Findings        []finding.Finding
	Coverage        *Coverage
	Readiness       *readiness.Result
	ObservedRuntime map[string]any
	EvidenceAt      *time.Time
	ReconciledAt    *time.Time
	Limitations     []Limitation
}

// MemorySource is an in-memory [Source]. It is the substrate for tests, the
// demo fleet, and any caller that has already gathered records by other means.
type MemorySource struct {
	id      string
	kind    string
	col     *Collection
	err     error
	collect func(ctx context.Context) (*Collection, error)
}

// NewMemorySource returns a source that always yields the given collection.
func NewMemorySource(id, kind string, col *Collection) *MemorySource {
	return &MemorySource{id: id, kind: kind, col: col}
}

// NewFailingSource returns a source whose Collect always fails with err. It
// models an unavailable environment (unreachable registry, disconnected
// cluster) for testing partial-fleet semantics.
func NewFailingSource(id, kind string, err error) *MemorySource {
	return &MemorySource{id: id, kind: kind, err: err}
}

// WithCollectFunc overrides Collect with a custom function (e.g. to honor
// context cancellation in tests). The function receives the request context.
func (s *MemorySource) WithCollectFunc(fn func(ctx context.Context) (*Collection, error)) *MemorySource {
	s.collect = fn
	return s
}

// ID implements [Source].
func (s *MemorySource) ID() string { return s.id }

// Kind implements [Source].
func (s *MemorySource) Kind() string { return s.kind }

// Collect implements [Source].
func (s *MemorySource) Collect(ctx context.Context) (*Collection, error) {
	if s.collect != nil {
		return s.collect(ctx)
	}
	if s.err != nil {
		return nil, s.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.col == nil {
		return &Collection{}, nil
	}
	return s.col, nil
}
