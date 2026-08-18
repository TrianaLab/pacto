package evidenceoci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
)

// Store persists accepted evidence records as OCI 1.1 Referrers of the contract
// revisions they report on. It holds no local state: the configured registry is
// the durable store, so the replay index and the operational projection are both
// reconstructed from the registry on every operation.
//
// ponytail: reconstructing the whole history per commit is O(accepted records)
// registry requests per write. It is correct, stateless and restart-proof, which
// is what Phase 10C is for; an in-process cache invalidated by the read-after-
// write confirmation is the upgrade path if write throughput ever matters.
type Store struct {
	subjects SubjectList
	repos    map[Subject]*subjectRepo

	// commit serializes the whole logical commit — enumerate, replay-check,
	// publish, confirm — so two concurrent requests cannot both pass the replay
	// check against the same history.
	//
	// ponytail: a process-wide mutex, because exactly one active writer is
	// supported. OCI offers no compare-and-set a second writer could use, and
	// emulating a lock in tags or a side service is the distributed-locking
	// machinery this phase exists to delete. The deployment pins one replica with
	// a Recreate strategy instead.
	commit sync.Mutex
}

// NewStore opens one repository per configured subject. Construction performs no
// network I/O: a registry that is briefly unreachable must make the server
// not-ready, not unstartable.
func NewStore(subjects SubjectList, opts RepositoryOptions) (*Store, error) {
	if len(subjects) == 0 {
		return nil, ErrNoSubjects
	}
	s := &Store{subjects: subjects, repos: make(map[Subject]*subjectRepo, len(subjects))}
	for _, subj := range subjects {
		repo, err := newRepository(subj, opts)
		if err != nil {
			return nil, err
		}
		s.repos[subj] = &subjectRepo{subj: subj, repo: repo}
	}
	return s, nil
}

// subjectRepo is one configured subject and the connection to the repository
// holding it.
type subjectRepo struct {
	subj Subject
	repo *remote.Repository

	// mu guards the resolved subject descriptor, memoized because a digest
	// reference names one immutable manifest forever. A failed resolve is not
	// memoized, so a subject that was unreachable at startup recovers by itself.
	mu   sync.Mutex
	desc ocispec.Descriptor
}

func (r *subjectRepo) resolve(ctx context.Context) (ocispec.Descriptor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.desc.Digest != "" {
		return r.desc, nil
	}
	// ORAS rejects a response whose digest is not the one requested, so a
	// successful resolve IS the exact-subject guarantee.
	desc, err := r.repo.Resolve(ctx, r.subj.Digest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("evidence oci: %s: %w", r.subj.Ref(), err)
	}
	r.desc = desc
	return desc, nil
}

// descriptor returns the memoized subject descriptor. Callers use it only after a
// scan has resolved every subject.
func (r *subjectRepo) descriptor() ocispec.Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.desc
}

// AuthorizeSubject implements [evidenceingest.Store]. A signed contract reference
// is accepted only when it is EXACTLY one of the configured subjects. Trust rules
// already authorize the producer, its operational subjects and its contract
// repositories; configuration membership narrows those permissions and never
// widens them, so an approved producer still cannot make the server write into a
// revision the operator did not configure.
func (s *Store) AuthorizeSubject(ref string) error {
	if _, ok := s.subjects.Lookup(ref); !ok {
		return fmt.Errorf("%w: %q is not a configured evidence subject", evidenceingest.ErrContractRefPolicy, ref)
	}
	return nil
}

// Ready implements the registry preflight behind the readiness probe: every
// configured subject must resolve to a real manifest and answer native Referrers
// discovery. A transient registry failure makes the server not-ready; it never
// makes it unhealthy, so a registry outage cannot restart the process.
func (s *Store) Ready(ctx context.Context) error {
	for _, subj := range s.subjects {
		sr := s.repos[subj]
		desc, err := sr.resolve(ctx)
		if err != nil {
			return err
		}
		if err := enumerate(ctx, sr.repo, desc); err != nil {
			return err
		}
	}
	return nil
}

// List implements [evidenceingest.Store]: it rebuilds the latest record per
// operational target from the registry, and reports how complete that read was.
// It never fails: an unreadable subject is reported as unavailable or partial
// health, because a read error rendered as an empty list would claim an
// environment has no evidence when the truth is that nobody could look.
func (s *Store) List(ctx context.Context) evidenceingest.ListResult {
	sc := s.scan(ctx)
	return evidenceingest.ListResult{Records: latestPerTarget(sc.records), Health: sc.health()}
}

// latestPerTarget projects an unordered history onto the current report for each
// operational target, in target-key order. The registry lists referrers in an
// order of its own choosing, so the winner must come from the records themselves
// and the output order must not.
func latestPerTarget(history []evidenceingest.Record) []evidenceingest.Record {
	latest := make(map[string]evidenceingest.Record, len(history))
	for _, rec := range history {
		key := evidenceingest.TargetKey(rec)
		if cur, ok := latest[key]; ok && !supersedes(rec, cur) {
			continue
		}
		latest[key] = rec
	}
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]evidenceingest.Record, 0, len(keys))
	for _, k := range keys {
		out = append(out, latest[k])
	}
	return out
}

// supersedes reports whether a is the newer report for a target: a later
// acceptance wins, and records accepted in the same instant are broken by the
// producer's own monotonic sequence, so the winner is deterministic.
func supersedes(a, b evidenceingest.Record) bool {
	if !a.AcceptedAt.Equal(b.AcceptedAt) {
		return a.AcceptedAt.After(b.AcceptedAt)
	}
	return a.Envelope.Sequence > b.Envelope.Sequence
}

// Commit implements [evidenceingest.Store] as one serialized logical commit:
// reconstruct the accepted history from the registry, refuse a replay, publish
// the record, and report success only once the same Referrers API a later scan
// uses can see it.
func (s *Store) Commit(ctx context.Context, rec evidenceingest.Record) error {
	subj, ok := s.subjects.Lookup(rec.Envelope.EvidenceSet.ContractRef)
	if !ok {
		return fmt.Errorf("%w: %q is not a configured evidence subject",
			evidenceingest.ErrContractRefPolicy, rec.Envelope.EvidenceSet.ContractRef)
	}
	sr := s.repos[subj]

	s.commit.Lock()
	defer s.commit.Unlock()

	// The replay index must cover EVERY subject: a producer's sequence is global
	// to the producer, so a history missing one revision would let a replayed
	// report through by aiming it at another one.
	sc := s.scan(ctx)
	switch {
	case sc.failed > 0:
		return fmt.Errorf("%w: %d of %d contract subjects could not be read",
			evidenceingest.ErrRegistryUnavailable, sc.failed, sc.subjects)
	case sc.invalid > 0:
		return fmt.Errorf("%w: %d published evidence artifact(s) could not be read",
			evidenceingest.ErrRegistryIncomplete, sc.invalid)
	}
	if err := checkReplay(rec, sc.records); err != nil {
		return err
	}

	art, err := BuildArtifact(rec, subj, sr.descriptor())
	if err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrStoreWrite, err)
	}
	if err := pushArtifact(ctx, sr.repo, art); err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrStoreWrite, err)
	}
	// The push is the registry's commit point, but a record no later scan can
	// discover is not accepted evidence. Confirming through the same API those
	// scans use is what makes "202 Accepted" mean it.
	if err := confirmDiscoverable(ctx, sr.repo, sr.descriptor(), art.ManifestDesc); err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrStoreWrite, err)
	}
	return nil
}

// registryScan is one read of every configured subject.
type registryScan struct {
	records  []evidenceingest.Record
	subjects int
	failed   int
	invalid  int
}

func (s *Store) scan(ctx context.Context) registryScan {
	out := registryScan{subjects: len(s.subjects)}
	for _, subj := range s.subjects {
		sr := s.repos[subj]
		desc, err := sr.resolve(ctx)
		if err != nil {
			out.failed++
			continue
		}
		found, err := scanSubject(ctx, sr.repo, subj, desc)
		if err != nil {
			out.failed++
			continue
		}
		out.invalid += found.Invalid
		for _, f := range found.Found {
			out.records = append(out.records, f.Record)
		}
	}
	return out
}

// health states what the read is worth. Absence of evidence is only authoritative
// when every subject was read completely; anything less is partial, and nothing
// readable at all is unavailable, never an empty operational graph.
func (r registryScan) health() evidenceingest.SourceHealth {
	h := evidenceingest.SourceHealth{
		Status:           evidenceingest.HealthReady,
		Subjects:         r.subjects,
		FailedSubjects:   r.failed,
		InvalidArtifacts: r.invalid,
	}
	switch {
	case r.failed == r.subjects:
		h.Status = evidenceingest.HealthUnavailable
	case r.failed > 0 || r.invalid > 0:
		h.Status = evidenceingest.HealthPartial
	}
	return h
}

// checkReplay rejects a record the accepted history already answers for. Both
// rules are global to the producer across every configured subject: an envelope
// id is usable once, and a sequence must be strictly greater than the highest
// that producer has ever had accepted — so aiming a replayed report at a
// different contract revision does not get it in.
func checkReplay(rec evidenceingest.Record, history []evidenceingest.Record) error {
	env := rec.Envelope
	var maxSeq uint64
	var seen bool
	for _, h := range history {
		if h.Envelope.ID == env.ID {
			return evidenceingest.ErrReplay
		}
		if h.Envelope.Producer.ID == env.Producer.ID && (!seen || h.Envelope.Sequence > maxSeq) {
			maxSeq, seen = h.Envelope.Sequence, true
		}
	}
	if seen && env.Sequence <= maxSeq {
		return evidenceingest.ErrReplay
	}
	return nil
}

// pushArtifact writes one evidence artifact: blobs first, then the manifest that
// references them and the contract revision it is a referrer of.
func pushArtifact(ctx context.Context, repo *remote.Repository, art Artifact) error {
	blobs := []struct {
		desc ocispec.Descriptor
		data []byte
	}{{art.ConfigDesc, art.Config}, {art.PayloadDesc, art.Payload}}
	for _, b := range blobs {
		if err := repo.Push(ctx, b.desc, bytes.NewReader(b.data)); err != nil {
			return err
		}
	}
	return repo.Push(ctx, art.ManifestDesc, bytes.NewReader(art.Manifest))
}

// confirmDiscoverable checks that the Referrers API already lists want among the
// referrers of subjectDesc. A listing that cannot be enumerated and a listing
// that omits the record are the same answer: it is not discoverable.
func confirmDiscoverable(ctx context.Context, repo *remote.Repository, subjectDesc, want ocispec.Descriptor) error {
	found := false
	err := repo.Referrers(ctx, subjectDesc, "", func(page []ocispec.Descriptor) error {
		for _, d := range page {
			found = found || d.Digest == want.Digest
		}
		return nil
	})
	if err == nil && !found {
		err = errors.New("the Referrers API did not list it")
	}
	if err != nil {
		return fmt.Errorf("the published record %s is not discoverable: %w", want.Digest, err)
	}
	return nil
}

// enumerate walks a subject's Referrers listing without fetching anything: the
// cheapest proof that native discovery works, which is all readiness needs.
func enumerate(ctx context.Context, repo *remote.Repository, subjectDesc ocispec.Descriptor) error {
	return repo.Referrers(ctx, subjectDesc, "", func([]ocispec.Descriptor) error { return nil })
}
