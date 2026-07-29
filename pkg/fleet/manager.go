package fleet

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/trianalab/pacto/v3/pkg/logging"
)

// ErrNoSnapshot is returned by a [Manager] before any snapshot has been
// successfully built — distinct from an empty snapshot, which is a valid answer.
var ErrNoSnapshot = errors.New("fleet: no snapshot has been published yet")

// Builder produces a candidate snapshot from a manager's configured sources.
// It must honor context cancellation.
type Builder func(ctx context.Context) (*FleetSnapshot, error)

// ManagerOptions tunes a [Manager]. The zero value is valid.
type ManagerOptions struct {
	// Now is the clock used for refresh timestamps; defaults to time.Now.
	Now func() time.Time
}

// Manager owns the current published [FleetSnapshot] and refreshes it. It lets a
// long-running host (the dashboard, an MCP server) serve many pure queries from
// one coherent, immutable snapshot and swap it atomically, rather than rebuilding
// the whole graph per request. It is safe for concurrent use.
//
// Guarantees: publication is atomic (a half-built snapshot is never observable);
// a failed refresh retains the last good snapshot and records the degradation;
// concurrent Refresh calls coalesce into a single in-flight build (bounded
// concurrency of one); readers are race-safe.
type Manager struct {
	build Builder
	now   func() time.Time

	mu          sync.RWMutex
	current     *FleetSnapshot
	lastErr     error
	lastRefresh time.Time

	flightMu sync.Mutex
	inflight *refreshCall

	refreshes atomic.Int64
	failures  atomic.Int64
}

type refreshCall struct {
	done chan struct{}
	err  error
}

// NewManager returns a manager that builds snapshots with build. No snapshot is
// published until the first successful Refresh.
func NewManager(build Builder, opts ManagerOptions) *Manager {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	return &Manager{build: build, now: now}
}

// Current returns the current published snapshot, or [ErrNoSnapshot] if none has
// been built yet. The returned snapshot is immutable and safe to read
// concurrently; use [Query.Snapshot] to obtain a copy for serialization.
func (m *Manager) Current() (*FleetSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return nil, ErrNoSnapshot
	}
	return m.current, nil
}

// Query returns a pure query over the current snapshot, or [ErrNoSnapshot].
func (m *Manager) Query() (*Query, error) {
	snap, err := m.Current()
	if err != nil {
		return nil, err
	}
	return NewQuery(snap), nil
}

// Refresh builds a candidate snapshot and, on success, atomically publishes it.
// On failure the last good snapshot is retained and the error is returned.
// Concurrent Refresh calls share one in-flight build; a caller whose context is
// cancelled while waiting returns that context error without cancelling the
// shared build for the others.
func (m *Manager) Refresh(ctx context.Context) error {
	m.flightMu.Lock()
	if call := m.inflight; call != nil {
		m.flightMu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	m.inflight = call
	m.flightMu.Unlock()

	snap, err := m.build(ctx)

	m.flightMu.Lock()
	m.inflight = nil
	m.flightMu.Unlock()

	m.refreshes.Add(1)
	m.mu.Lock()
	m.lastRefresh = m.now()
	if err != nil {
		m.failures.Add(1)
		m.lastErr = err
		m.mu.Unlock()
		logging.LoggerFromContext(ctx).Debug("fleet snapshot refresh failed; retaining last good snapshot", "error", err)
		call.err = err
		close(call.done)
		return err
	}
	m.current = snap
	m.lastErr = nil
	m.mu.Unlock()
	logging.LoggerFromContext(ctx).Debug("fleet snapshot published", "snapshotId", snap.SnapshotID, "services", len(snap.Services))
	close(call.done)
	return nil
}

// Start refreshes immediately, then periodically at interval until ctx is
// cancelled. A non-positive interval runs a single initial refresh and returns.
// Periodic-refresh failures are retained (last good snapshot kept) and logged,
// never fatal. Start blocks; run it in a goroutine.
func (m *Manager) Start(ctx context.Context, interval time.Duration) {
	_ = m.Refresh(ctx)
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.Refresh(ctx)
		}
	}
}

// ManagerStatus reports the manager's lifecycle state for health/metrics.
type ManagerStatus struct {
	HasSnapshot bool         `json:"hasSnapshot"`
	SnapshotID  string       `json:"snapshotId,omitempty"`
	LastRefresh time.Time    `json:"lastRefresh"`
	Degraded    bool         `json:"degraded"`
	Error       *SourceError `json:"error,omitempty"`
	Refreshes   int64        `json:"refreshes"`
	Failures    int64        `json:"failures"`
}

// Status returns the current lifecycle state. Degraded is true when the last
// refresh failed but a previous good snapshot is still being served. The error
// is sanitized (no secrets).
func (m *Manager) Status() ManagerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := ManagerStatus{
		HasSnapshot: m.current != nil,
		LastRefresh: m.lastRefresh,
		Degraded:    m.current != nil && m.lastErr != nil,
		Refreshes:   m.refreshes.Load(),
		Failures:    m.failures.Load(),
	}
	if m.current != nil {
		st.SnapshotID = m.current.SnapshotID
	}
	if m.lastErr != nil {
		st.Error = sanitizeError(m.lastErr)
	}
	return st
}
