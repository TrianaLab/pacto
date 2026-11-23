package evidenceoci

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Store persists accepted evidence records as OCI 1.1 Referrers of the contract
// revisions they report on or, on a registry the probe finds without Referrers,
// as write-once tags in the covering prefix's ledger. It holds no local state: the configured registry is
// the durable store, so the replay index and the operational projection are both
// reconstructed from the registry on every operation.
//
// ponytail: reconstructing the whole history per commit is O(accepted records)
// registry requests per write. It is correct, stateless and restart-proof, which
// is the whole point of making the registry the only durable store; an in-process
// cache invalidated by the read-after-write confirmation is the upgrade path if
// write throughput ever matters.
type Store struct {
	subjects SubjectList
	prefixes PrefixList
	opts     RepositoryOptions
	ledgers  map[Prefix]*remote.Repository

	// mu guards repos, conns and anchors, which grow per request under a
	// prefix.
	//
	// ponytail: repos, conns and anchors grow with the subjects that ever
	// received evidence and are never evicted; the ledger bounds them.
	mu sync.Mutex
	// repos is keyed by subject: each entry memoizes its own resolved
	// descriptor, so two digests of one repository never share one.
	repos map[Subject]*subjectRepo
	// conns shares one connection between the digests of one repository.
	conns map[string]*remote.Repository
	// anchors memoizes decoded ledger anchors by ledger and tag.
	anchors map[string]Subject

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

	// cluster is the id of the cluster this server runs in, stamped on every
	// record it accepts.
	cluster string
}

// WithCluster stamps id, the cluster this server runs in, on every record the
// store commits.
func (s *Store) WithCluster(id string) *Store {
	s.cluster = id
	return s
}

// NewStore opens one repository per configured subject and one ledger
// repository per prefix; a subject a prefix covers is opened at its first
// request. Construction performs no network I/O: a registry that is briefly
// unreachable must make the server not-ready, not unstartable.
func NewStore(subjects SubjectList, prefixes PrefixList, opts RepositoryOptions) (*Store, error) {
	if len(subjects) == 0 && len(prefixes) == 0 {
		return nil, ErrNoSubjects
	}
	s := &Store{
		subjects: subjects, prefixes: prefixes, opts: opts,
		ledgers: make(map[Prefix]*remote.Repository, len(prefixes)),
		repos:   make(map[Subject]*subjectRepo, len(subjects)),
		conns:   make(map[string]*remote.Repository, len(subjects)),
		anchors: make(map[string]Subject),
	}
	for _, subj := range subjects {
		if _, err := s.repoFor(subj); err != nil {
			return nil, err
		}
	}
	for _, p := range prefixes {
		repo, err := ocireferrer.Open(p.Ledger(), opts)
		if err != nil {
			return nil, fmt.Errorf("evidence oci: %q: %w", p.Ledger(), err)
		}
		s.ledgers[p] = repo
	}
	return s, nil
}

// repoFor returns subj's entry, opening it on first use. It fails only for a
// subject [ParseSubject] did not produce: a parsed subject always opens.
func (s *Store) repoFor(subj Subject) (*subjectRepo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sr := s.repos[subj]; sr != nil {
		return sr, nil
	}
	conn := s.conns[subj.Path()]
	if conn == nil {
		repo, err := newRepository(subj, s.opts)
		if err != nil {
			return nil, err
		}
		conn = repo
		s.conns[subj.Path()] = conn
	}
	sr := &subjectRepo{subj: subj, repo: conn}
	s.repos[subj] = sr
	return sr, nil
}

// parsedRepo is repoFor for a subject [ParseSubject] produced, which always
// opens.
func (s *Store) parsedRepo(subj Subject) *subjectRepo {
	sr, _ := s.repoFor(subj) // ParseSubject validated the reference ORAS opens
	return sr
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
	if desc := r.descriptor(); desc.Digest != "" {
		return desc, nil
	}
	// The registry call runs OUTSIDE the lock. A mutex honours no deadline, so
	// holding one across a request a stalled registry never answers would make a
	// concurrent caller wait past its own budget. Two callers racing here resolve
	// the same immutable digest, so the duplicate request is the cheap half of the
	// trade.
	//
	// ORAS rejects a response whose digest is not the one requested, so a
	// successful resolve IS the exact-subject guarantee.
	desc, err := r.repo.Resolve(ctx, r.subj.Digest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("evidence oci: %s: %w", r.subj.Ref(), err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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
// is accepted only when it is EXACTLY one of the configured subjects, or an
// exact revision under a configured prefix outside Pacto's own repositories.
// Trust rules already authorize the producer, its operational subjects and its
// contract repositories; configuration narrows those permissions and never
// widens them, so an approved producer still cannot make the server write into a
// revision the operator did not configure. It performs no I/O.
func (s *Store) AuthorizeSubject(ref string) error {
	_, _, err := s.lookup(ref)
	return err
}

// lookup resolves ref to the subject it names and the prefix covering it, if
// any (an exact subject a prefix covers is anchored too).
func (s *Store) lookup(ref string) (Subject, *Prefix, error) {
	subj, ok := s.subjects.Lookup(ref)
	if !ok {
		parsed, err := ParseSubject(ref)
		if err != nil {
			return Subject{}, nil, fmt.Errorf("%w: %q is not a configured evidence subject", evidenceingest.ErrContractRefPolicy, ref)
		}
		subj = parsed
	}
	if p, covered := s.prefixes.Covering(subj); covered {
		return subj, &p, nil
	}
	if !ok {
		return Subject{}, nil, fmt.Errorf("%w: %q is not a configured evidence subject or a revision under a subject prefix", evidenceingest.ErrContractRefPolicy, ref)
	}
	return subj, nil, nil
}

// Ready implements the registry preflight behind the readiness probe: every
// configured subject must resolve, live, to a real manifest on a registry whose
// placement the probe can tell, and every prefix's ledger must list (an absent
// ledger is empty). The probe runs once per registry host, cached, so readiness
// costs one Referrers request per host however many subjects it holds. A
// transient registry failure makes the server not-ready; it never makes it
// unhealthy, so a registry outage cannot restart the process.
func (s *Store) Ready(ctx context.Context) error {
	for _, subj := range s.subjects {
		sr := s.parsedRepo(subj)
		// A live request, never the memoized descriptor: readiness must see
		// an outage.
		if _, err := sr.repo.Resolve(ctx, subj.Digest); err != nil {
			return fmt.Errorf("evidence oci: %s: %w", subj.Ref(), err)
		}
		if _, err := s.placement(ctx, sr); err != nil {
			return err
		}
	}
	for _, p := range s.prefixes {
		if err := s.readyLedger(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// readyLedger lists p's ledger and probes its host with the first ledger
// subject the registry still holds. An empty ledger has nothing to probe yet:
// the first commit probes.
func (s *Store) readyLedger(ctx context.Context, p Prefix) error {
	tags, err := s.ledgerTags(ctx, p)
	if err != nil {
		return err
	}
	for _, tag := range tags {
		if len(tag) != anchorTagLen {
			continue
		}
		subj, err := s.anchorSubject(ctx, p, tag)
		if err != nil {
			continue // List and Commit report it; readiness is reachability
		}
		sr := s.parsedRepo(subj)
		if _, err := sr.resolve(ctx); err != nil {
			if notFound(err) {
				continue
			}
			return err
		}
		_, err = s.placement(ctx, sr)
		return err
	}
	return nil
}

// placement asks the registry probe, once per host, where a record on sr goes:
// a native referrer, or a write-once tag in the covering prefix's ledger. A
// probe that cannot tell is an error, never a mode, and so is a registry
// without Referrers for a subject no prefix covers, which has no ledger.
func (s *Store) placement(ctx context.Context, sr *subjectRepo) (capabilities.ReferrersMode, error) {
	caps, err := ocireferrer.Probes.Probe(ctx, sr.repo, sr.subj.Digest, false)
	if err != nil {
		return "", fmt.Errorf("%w: %w", evidenceingest.ErrRegistryUnavailable, err)
	}
	if _, covered := s.prefixes.Covering(sr.subj); caps.Referrers == capabilities.ReferrersWriteOnceTags && !covered {
		return "", fmt.Errorf("%w: registry %s serves no OCI Referrers: add a subject prefix covering %s to store its evidence as write-once tags in <prefix>%s",
			evidenceingest.ErrRegistryUnavailable, sr.subj.Registry, sr.subj.Ref(), oci.EvidenceRepository)
	}
	return caps.Referrers, nil
}

// notFound reports whether the registry definitively answered that a revision
// or its repository does not exist.
func notFound(err error) bool {
	return errors.Is(err, errdef.ErrNotFound) || ocireferrer.RepositoryNotFound(err)
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
// resolve the revision, reconstruct the accepted history from the registry,
// refuse a replay, ask the probe where the record goes, anchor a
// prefix-covered subject in its ledger, publish the record, and report success
// only once the same API a later scan uses can see it. The anchor goes first: a
// crash before the record leaves a subject with no evidence, never a record no
// scan can find.
func (s *Store) Commit(ctx context.Context, rec evidenceingest.Record) error {
	subj, prefix, err := s.lookup(rec.Envelope.EvidenceSet.ContractRef)
	if err != nil {
		return err
	}
	sr := s.parsedRepo(subj)

	s.commit.Lock()
	defer s.commit.Unlock()

	if _, err := sr.resolve(ctx); err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrRegistryUnavailable, err)
	}
	// The replay index must cover EVERY subject: a producer's sequence is global
	// to the producer, so a history missing one revision would let a replayed
	// report through by aiming it at another one.
	sc := s.scan(ctx)
	switch {
	case sc.failed > 0:
		return fmt.Errorf("%w: %d evidence subject(s) or ledger(s) could not be read: %w",
			evidenceingest.ErrRegistryUnavailable, sc.failed, sc.firstErr)
	case sc.invalid > 0:
		return fmt.Errorf("%w: %d published evidence artifact(s) could not be read",
			evidenceingest.ErrRegistryIncomplete, sc.invalid)
	}
	if err := checkReplay(rec, sc.records); err != nil {
		return err
	}
	// The probe runs before the anchor: a registry it cannot classify leaves
	// nothing in the ledger.
	mode, err := s.placement(ctx, sr)
	if err != nil {
		return err
	}

	rec.Cluster = s.cluster
	art, err := BuildArtifact(rec, subj, sr.descriptor())
	if err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrStoreWrite, err)
	}
	if prefix != nil {
		if err := s.writeLedger(ctx, *prefix, subj, mode, art); err != nil {
			return fmt.Errorf("%w: ledger %s: %w", evidenceingest.ErrStoreWrite, prefix.Ledger(), err)
		}
	}
	if mode == capabilities.ReferrersWriteOnceTags {
		return nil // placement refuses this mode for a subject no prefix covers
	}
	if err := ocireferrer.Push(ctx, sr.repo, art); err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrStoreWrite, err)
	}
	// The push is the registry's commit point, but a record no later scan can
	// discover is not accepted evidence. Confirming through the same API those
	// scans use is what makes "202 Accepted" mean it.
	if err := ocireferrer.Confirm(ctx, sr.repo, sr.descriptor(), art.ManifestDesc); err != nil {
		return fmt.Errorf("%w: %w", evidenceingest.ErrStoreWrite, err)
	}
	return nil
}

// registryScan is one read of every configured subject, every ledger and
// every ledger subject.
type registryScan struct {
	records []evidenceingest.Record
	// subjects counts the read units: exact subjects, ledger subjects and one
	// per ledger listing.
	subjects int
	failed   int
	invalid  int
	// pruned counts ledger subjects whose revision the registry definitively
	// no longer holds (retention, or an anchor naming a missing digest): their
	// referrers went with them, so they are neither failed nor counted.
	pruned int
	// firstErr is why the first failed unit failed.
	firstErr error
}

// fail counts one failed read unit.
func (r *registryScan) fail(err error) {
	r.failed++
	if r.firstErr == nil {
		r.firstErr = err
	}
}

// scan reads the exact subjects, then every ledger and the subjects it names,
// each subject once, with the record tags each ledger holds for it.
func (s *Store) scan(ctx context.Context) registryScan {
	var out registryScan
	units := slices.Clone(s.subjects)
	fromLedger := map[Subject]bool{}
	records := map[string][]string{}
	native := map[string]bool{}
	for _, p := range s.prefixes {
		out.subjects++
		read, err := s.ledgerSubjects(ctx, p)
		if err != nil {
			out.fail(err)
			continue
		}
		out.invalid += read.invalid
		maps.Copy(records, read.records)
		maps.Copy(native, read.native)
		for _, subj := range read.subjects {
			if !slices.Contains(units, subj) {
				units = append(units, subj)
				fromLedger[subj] = true
			}
		}
	}
	for _, subj := range units {
		tag := anchorTag(subj.Ref())
		s.scanOne(ctx, subj, fromLedger[subj], native[tag], records[tag], &out)
	}
	return out
}

// scanOne reads subj's records into out: its referrers where the registry
// serves them, and in every mode its record tags in the ledger, so a registry
// whose probe answer changed across a restart keeps every earlier record. A
// ledger subject the registry definitively no longer holds is pruned, its
// record tags still read; an exact subject fails closed. A subject marked as
// holding native referrers on a registry that no longer serves them fails:
// those records can no longer be listed, and reading it as empty would reopen
// the replay check.
func (s *Store) scanOne(ctx context.Context, subj Subject, fromLedger, native bool, tags []string, out *registryScan) {
	sr := s.parsedRepo(subj)
	desc, err := sr.resolve(ctx)
	if err != nil && fromLedger && notFound(err) {
		out.pruned++
		s.readTagRecords(ctx, subj, ocispec.Descriptor{}, tags, nil, out)
		return
	}
	out.subjects++
	if err != nil {
		out.fail(err)
		return
	}
	mode, err := s.placement(ctx, sr)
	if err != nil {
		out.fail(err)
		return
	}
	if native && mode != capabilities.ReferrersNative {
		out.fail(fmt.Errorf("evidence oci: %s holds OCI referrers and registry %s now serves no Referrers: its records cannot be read", subj.Ref(), subj.Registry))
		return
	}
	var seen map[digest.Digest]bool
	if mode == capabilities.ReferrersNative {
		found, err := scanSubject(ctx, sr.repo, subj, desc)
		if err != nil {
			out.fail(err)
			return
		}
		out.invalid += found.Invalid
		out.records = append(out.records, found.Found...)
		seen = found.seen
	}
	s.readTagRecords(ctx, subj, desc, tags, seen, out)
}

// readTagRecords reads subj's write-once record tags from its ledger, skipping
// records already read as referrers. A tag that does not resolve, is not named
// after its manifest or is not a valid record on subj is invalid.
func (s *Store) readTagRecords(ctx context.Context, subj Subject, subjectDesc ocispec.Descriptor, tags []string, seen map[digest.Digest]bool, out *registryScan) {
	if len(tags) == 0 {
		return
	}
	p, _ := s.prefixes.Covering(subj) // tags come from the covering ledger
	repo := s.ledgers[p]
	for _, tag := range tags {
		desc, err := repo.Resolve(ctx, tag)
		switch {
		case err != nil || recordTag(subj.Ref(), desc.Digest) != tag:
			out.invalid++
			continue
		case seen[desc.Digest]:
			continue
		}
		rec, state := readReferrer(ctx, repo, subj, subjectDesc, desc)
		if state != referrerRecord {
			out.invalid++
			continue
		}
		out.records = append(out.records, rec)
	}
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
	case r.failed > 0 && r.failed == r.subjects:
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
