package evidenceingest

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore is an in-memory [Store]: it enforces replay protection (duplicate
// id and per-producer non-increasing sequence) atomically inside Commit, then
// keeps the latest record per target key. It is the substrate for tests. It configures no
// subjects, so it authorizes every policy-valid contract reference; narrowing to
// an operator-configured subject set is the durable store's job.
type MemoryStore struct {
	mu      sync.Mutex
	seenID  map[string]bool
	maxSeq  map[string]uint64
	haveSeq map[string]bool
	recs    map[string]Record
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{seenID: map[string]bool{}, maxSeq: map[string]uint64{}, haveSeq: map[string]bool{}, recs: map[string]Record{}}
}

// AuthorizeSubject implements [Store]. An in-memory store configures no subject
// set, so the contract-ref policy [Acceptor.Accept] already enforced is the only
// gate.
func (m *MemoryStore) AuthorizeSubject(string) error { return nil }

// Commit implements [Store]: a repeated id, or a producer sequence not strictly
// greater than its last committed one, is rejected with [ErrReplay]; otherwise
// the record becomes the latest for its target.
func (m *MemoryStore) Commit(_ context.Context, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	env := rec.Envelope
	if m.seenID[env.ID] {
		return ErrReplay
	}
	if m.haveSeq[env.Producer.ID] && env.Sequence <= m.maxSeq[env.Producer.ID] {
		return ErrReplay
	}
	m.seenID[env.ID] = true
	m.maxSeq[env.Producer.ID] = env.Sequence
	m.haveSeq[env.Producer.ID] = true
	m.recs[TargetKey(rec)] = rec
	return nil
}

// List implements [Store] in deterministic target-key order. Memory cannot be
// half-read, so the health is always ready.
func (m *MemoryStore) List(_ context.Context) ListResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.recs))
	for k := range m.recs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Record, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.recs[k])
	}
	return ListResult{Records: out, Health: SourceHealth{Status: HealthReady}}

}
