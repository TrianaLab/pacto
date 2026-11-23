package evidenceoci

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// acceptedAt is the fixed acceptance instant records are dated from, so every
// ordering assertion is about the data and never about the wall clock.
var acceptedAt = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// testDigest is a well-formed contract digest that names nothing, for subjects a
// test configures but never expects to resolve.
func testDigest(hexSeed string) string { return "sha256:" + strings.Repeat(hexSeed, 64) }

// storeRecord is one accepted record for subj: a producer, a global sequence, an
// envelope id, the operational target it reports on and when it was accepted.
func storeRecord(subj Subject, producer, id string, seq uint64, target string, at time.Time) evidenceingest.Record {
	rec := testRecord(subj)
	rec.Envelope.ID = id
	rec.Envelope.Producer.ID = producer
	rec.Envelope.Sequence = seq
	rec.Envelope.EvidenceSet.Subject.Name = target
	rec.AcceptedAt = at
	return rec
}

func newTestStore(t *testing.T, opts RepositoryOptions, subjects ...Subject) *Store {
	t.Helper()
	s, err := NewStore(SubjectList(subjects), nil, opts)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// mustCommit commits rec and fails the test if the store refuses it.
func mustCommit(t *testing.T, s *Store, rec evidenceingest.Record) {
	t.Helper()
	if err := s.Commit(t.Context(), rec); err != nil {
		t.Fatalf("commit %s: %v", rec.Envelope.ID, err)
	}
}

// oneSubjectStore is the common fixture: a spec-faithful registry, one seeded
// contract revision and a store configured with exactly that subject.
func oneSubjectStore(t *testing.T) (*Store, Subject) {
	t.Helper()
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, _ := seedContract(t, host, RepositoryOptions{})
	return newTestStore(t, RepositoryOptions{}, subj), subj
}

// A server with neither a subject nor a prefix has no durable store at all:
// there is no implicit catalog-wide discovery to fall back to.
func TestNewStoreNeedsSubjectOrPrefix(t *testing.T) {
	if _, err := NewStore(nil, nil, RepositoryOptions{}); !errors.Is(err, ErrNoSubjects) {
		t.Fatalf("NewStore(nil, nil) = %v, want ErrNoSubjects", err)
	}
	if _, err := NewStore(SubjectList{}, PrefixList{}, RepositoryOptions{}); !errors.Is(err, ErrNoSubjects) {
		t.Fatalf("NewStore({}, {}) = %v, want ErrNoSubjects", err)
	}
	if _, err := NewStore(nil, PrefixList{{Registry: "registry.example", Path: "acme"}}, RepositoryOptions{}); err != nil {
		t.Fatalf("NewStore with only a prefix: %v", err)
	}
}

// A subject whose repository ORAS cannot address is a configuration error at
// construction, not a mystery at the first write.
func TestNewStore_RejectsUnaddressableSubject(t *testing.T) {
	if _, err := NewStore(SubjectList{{Registry: "", Repository: ""}}, nil, RepositoryOptions{}); err == nil {
		t.Fatal("NewStore accepted an unaddressable subject")
	}
}

// Construction performs no network I/O: a registry that is briefly unreachable
// must make the server not-ready, never unstartable.
func TestNewStore_DoesNotTouchTheRegistry(t *testing.T) {
	subj := Subject{Registry: "127.0.0.1:1", Repository: "team/orders", Digest: testDigest("a")}
	s := newTestStore(t, RepositoryOptions{}, subj)
	if err := s.Ready(t.Context()); err == nil {
		t.Fatal("Ready against an unreachable registry succeeded; readiness is what must fail, not construction")
	}
}

// Configuration membership narrows producer authorization: an exact configured
// revision is accepted, and every near-miss — a sibling revision, the same digest
// in another repository, a mutable tag — is not.
func TestStore_AuthorizeSubject(t *testing.T) {
	s, subj := oneSubjectStore(t)
	if err := s.AuthorizeSubject(subj.Ref()); err != nil {
		t.Fatalf("the configured subject must be authorized: %v", err)
	}
	other := Subject{Registry: subj.Registry, Repository: subj.Repository, Digest: testDigest("b")}
	elsewhere := Subject{Registry: subj.Registry, Repository: "team/other", Digest: subj.Digest}
	for name, ref := range map[string]string{
		"sibling revision":                other.Ref(),
		"same digest, another repository": elsewhere.Ref(),
		"mutable tag":                     Scheme + subj.Path() + ":latest",
		"local path":                      "/tmp/bundle",
	} {
		if err := s.AuthorizeSubject(ref); !errors.Is(err, evidenceingest.ErrContractRefPolicy) {
			t.Errorf("%s: AuthorizeSubject(%q) = %v, want ErrContractRefPolicy", name, ref, err)
		}
	}
}

// Readiness is a live registry preflight: every subject must resolve AND have a
// placement (native Referrers or a covering prefix's ledger), because a store
// that cannot be enumerated cannot replay-protect a write.
func TestStore_Ready(t *testing.T) {
	s, subj := oneSubjectStore(t)
	if err := s.Ready(t.Context()); err != nil {
		t.Fatalf("Ready with no evidence yet: %v", err)
	}
	// A subject that already carries evidence walks a non-empty listing, and the
	// probe answer is cached per host, so the second check is cheap and stable.
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if err := s.Ready(t.Context()); err != nil {
		t.Fatalf("Ready with evidence present: %v", err)
	}
}

// A registry with no native Referrers endpoint fails readiness for an exact
// subject no prefix covers: there is no ledger to hold its records as write-once
// tags, and oras-go's legacy referrers tag — a mutable pointer no producer
// signed — is never used.
func TestStore_Ready_FailsWithoutReferrersAPI(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	subj, _ := seedContract(t, host, RepositoryOptions{})
	s := newTestStore(t, RepositoryOptions{}, subj)
	if err := s.Ready(t.Context()); err == nil {
		t.Fatal("Ready succeeded against a registry with no Referrers API; it must fail closed")
	}
}

// The load-bearing round trip: a committed record is discoverable through the
// same Referrers API a later scan uses, from a store with no local state.
func TestStore_CommitThenList(t *testing.T) {
	s, subj := oneSubjectStore(t)
	rec := storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt)
	mustCommit(t, s, rec)

	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || res.Health.Subjects != 1 {
		t.Errorf("health = %+v, want a complete read of 1 subject", res.Health)
	}
	if len(res.Records) != 1 {
		t.Fatalf("read %d records, want 1", len(res.Records))
	}
	if got := res.Records[0].Envelope.ID; got != "env-1" {
		t.Errorf("envelope id = %q, want env-1", got)
	}
}

// A brand-new store reading the same registry sees the same evidence: durability
// is the registry's, so a restart with no local directory loses nothing.
func TestStore_SurvivesRestart(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, _ := seedContract(t, host, RepositoryOptions{})
	mustCommit(t, newTestStore(t, RepositoryOptions{}, subj),
		storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))

	restarted := newTestStore(t, RepositoryOptions{}, subj)
	res := restarted.List(t.Context())
	if len(res.Records) != 1 || res.Records[0].Envelope.ID != "env-1" {
		t.Fatalf("a restarted store read %+v, want the committed record", res.Records)
	}
	// And it still refuses the replay, because the history it reconstructs is the
	// registry's, not a lost in-process index.
	if err := restarted.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-1", 9, "orders", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("replay after restart = %v, want ErrReplay", err)
	}
}

// Replay protection is global to the PRODUCER, not to the contract revision:
// aiming a replayed report at a different configured subject must not get it in.
func TestStore_ReplayIsProducerGlobalAcrossSubjects(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	orders, _ := seedContractIn(t, host, "team/contracts/orders", "orders", RepositoryOptions{})
	payments, _ := seedContractIn(t, host, "team/contracts/payments", "payments", RepositoryOptions{})
	s := newTestStore(t, RepositoryOptions{}, orders, payments)

	mustCommit(t, s, storeRecord(orders, "remote-eu", "env-1", 5, "orders", acceptedAt))

	// Same producer, a sequence already used, a DIFFERENT contract subject.
	err := s.Commit(t.Context(), storeRecord(payments, "remote-eu", "env-2", 5, "payments", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("cross-subject sequence reuse = %v, want ErrReplay", err)
	}
	// The same envelope id, likewise, is spent everywhere once it is spent anywhere.
	err = s.Commit(t.Context(), storeRecord(payments, "remote-eu", "env-1", 9, "payments", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("cross-subject duplicate envelope id = %v, want ErrReplay", err)
	}
	// A strictly newer sequence on the other subject is genuinely new evidence.
	mustCommit(t, s, storeRecord(payments, "remote-eu", "env-3", 6, "payments", acceptedAt))
	// A different producer has its own sequence space and starts fresh.
	mustCommit(t, s, storeRecord(payments, "remote-us", "env-4", 1, "payments", acceptedAt))

	if n := len(s.List(t.Context()).Records); n != 3 {
		t.Errorf("read %d targets, want 3", n)
	}
}

// Concurrent ingestion must be serialized: two requests racing on the same
// sequence would both pass a replay check read before either wrote.
func TestStore_ConcurrentCommitsSerialize(t *testing.T) {
	s, subj := oneSubjectStore(t)
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := storeRecord(subj, "remote-eu", fmt.Sprintf("env-%d", i), 5, fmt.Sprintf("orders-%d", i), acceptedAt)
			errs[i] = s.Commit(t.Context(), rec)
		}()
	}
	wg.Wait()

	accepted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, evidenceingest.ErrReplay):
		default:
			t.Errorf("commit %d failed for the wrong reason: %v", i, err)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of %d concurrent commits on the same sequence were accepted, want exactly 1", accepted, n)
	}
}

// Latest-per-target: a later acceptance supersedes an earlier one, and records
// accepted in the same instant are broken by the producer's own sequence, so the
// projection is deterministic whatever order the registry lists referrers in.
func TestStore_ListKeepsTheLatestPerTarget(t *testing.T) {
	s, subj := oneSubjectStore(t)
	// One target, three reports: the middle acceptance is superseded, and the two
	// sharing an instant are separated by sequence.
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-2", 2, "orders", acceptedAt.Add(time.Hour)))
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-3", 3, "orders", acceptedAt.Add(time.Hour)))
	// A second target of the same producer is independent.
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-4", 4, "billing", acceptedAt))

	res := s.List(t.Context())
	if len(res.Records) != 2 {
		t.Fatalf("read %d targets, want 2", len(res.Records))
	}
	got := map[string]string{}
	for _, rec := range res.Records {
		got[rec.Envelope.EvidenceSet.Subject.Name] = rec.Envelope.ID
	}
	if got["orders"] != "env-3" {
		t.Errorf("orders = %q, want env-3 (latest instant, highest sequence)", got["orders"])
	}
	if got["billing"] != "env-4" {
		t.Errorf("billing = %q, want env-4", got["billing"])
	}
	// Deterministic order, so a consumer diffing two reads sees real changes only.
	if evidenceingest.TargetKey(res.Records[0]) > evidenceingest.TargetKey(res.Records[1]) {
		t.Errorf("records are not in target-key order: %+v", res.Records)
	}
}

// The fold itself, driven directly: a registry lists referrers in an order of its
// own choosing, so the projection must be identical whichever order it arrives in.
func TestLatestPerTarget(t *testing.T) {
	subj := Subject{Registry: "r", Repository: "team/orders", Digest: testDigest("a")}
	first := storeRecord(subj, "p", "env-1", 1, "orders", acceptedAt)
	last := storeRecord(subj, "p", "env-2", 2, "orders", acceptedAt.Add(time.Hour))
	other := storeRecord(subj, "p", "env-3", 3, "billing", acceptedAt)

	for name, history := range map[string][]evidenceingest.Record{
		"oldest first": {first, last, other},
		"newest first": {last, first, other},
	} {
		got := latestPerTarget(history)
		if len(got) != 2 {
			t.Fatalf("%s: %d targets, want 2", name, len(got))
		}
		// Target-key order, and the newer report wins whichever way round it came.
		if got[0].Envelope.EvidenceSet.Subject.Name != "billing" {
			t.Errorf("%s: first target is %q, want billing", name, got[0].Envelope.EvidenceSet.Subject.Name)
		}
		if got[1].Envelope.ID != "env-2" {
			t.Errorf("%s: orders = %q, want env-2", name, got[1].Envelope.ID)
		}
	}
	if got := latestPerTarget(nil); len(got) != 0 {
		t.Errorf("an empty history projected to %+v, want nothing", got)
	}
}

func TestSupersedes(t *testing.T) {
	subj := Subject{Registry: "r", Repository: "team/orders", Digest: testDigest("a")}
	older := storeRecord(subj, "p", "a", 9, "orders", acceptedAt)
	newer := storeRecord(subj, "p", "b", 1, "orders", acceptedAt.Add(time.Hour))
	if !supersedes(newer, older) {
		t.Error("a later acceptance must win regardless of sequence")
	}
	if supersedes(older, newer) {
		t.Error("an earlier acceptance must not win")
	}
	tie := storeRecord(subj, "p", "c", 10, "orders", acceptedAt)
	if !supersedes(tie, older) {
		t.Error("same instant: the higher producer sequence must win")
	}
	if supersedes(older, tie) {
		t.Error("same instant: the lower producer sequence must lose")
	}
}

// An unreadable subject makes the whole write fail closed: replay protection is
// reconstructed from the registry, so a history nobody could read cannot answer
// "has this already been accepted?".
func TestStore_CommitFailsClosedWhenASubjectIsUnreadable(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	orders, _ := seedContract(t, host, RepositoryOptions{})
	gone := Subject{Registry: "127.0.0.1:1", Repository: "team/contracts/payments", Digest: testDigest("b")}
	s := newTestStore(t, RepositoryOptions{}, orders, gone)

	err := s.Commit(t.Context(), storeRecord(orders, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryUnavailable) {
		t.Fatalf("commit with an unreadable subject = %v, want ErrRegistryUnavailable", err)
	}
	// The read is partial, not empty: what WAS read is still returned, and the
	// health says absence is no longer authoritative.
	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthPartial || res.Health.FailedSubjects != 1 || res.Health.Subjects != 2 {
		t.Errorf("health = %+v, want partial with 1 of 2 subjects failed", res.Health)
	}
}

// A registry that resolves the contract but answers no Referrers API is readable
// and still useless: reading it as an empty history would both erase the evidence
// and disarm replay protection, so it is unavailable and writes fail closed.
func TestStore_FailsClosedWithoutReferrersAPI(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	subj, _ := seedContract(t, host, RepositoryOptions{})
	s := newTestStore(t, RepositoryOptions{}, subj)

	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthUnavailable || res.Health.FailedSubjects != 1 {
		t.Errorf("health = %+v, want unavailable with the subject failed", res.Health)
	}
	err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryUnavailable) {
		t.Fatalf("commit without a Referrers API = %v, want ErrRegistryUnavailable", err)
	}
}

// Nothing readable at all is unavailable, never an authoritative empty graph.
func TestStore_ListUnavailableWhenNoSubjectCanBeRead(t *testing.T) {
	s := newTestStore(t, RepositoryOptions{},
		Subject{Registry: "127.0.0.1:1", Repository: "team/orders", Digest: testDigest("a")})
	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthUnavailable {
		t.Fatalf("health = %+v, want unavailable", res.Health)
	}
	if len(res.Records) != 0 {
		t.Errorf("records = %+v, want none", res.Records)
	}
}

// Something published as Pacto evidence that cannot be read as a record makes the
// read incomplete, and an incomplete history cannot protect a write either.
func TestStore_CommitFailsClosedOnMalformedEvidence(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	pushMalformed(t, openRepo(t, host, RepositoryOptions{}), desc, []byte("not a record"))
	s := newTestStore(t, RepositoryOptions{}, subj)

	err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryIncomplete) {
		t.Fatalf("commit over malformed evidence = %v, want ErrRegistryIncomplete", err)
	}
	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthPartial || res.Health.InvalidArtifacts != 1 {
		t.Errorf("health = %+v, want partial with 1 invalid artifact", res.Health)
	}
}

// An artifact whose stored envelope the ingestion boundary would have refused is
// invalid on the read path too — the envelope rules do not weaken because the
// bytes arrived from a registry instead of from a producer. It is counted, kept
// out of the projection, and because an incomplete history cannot answer "has
// this already been accepted?", it blocks the next write.
func TestStore_CommitFailsClosedOnInvalidStoredEnvelope(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	rec := storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt)
	rec.Envelope.Producer.KeyID = "" // no key id: nothing could ever have verified it
	pushMalformed(t, openRepo(t, host, RepositoryOptions{}), desc, mustPayload(t, rec))
	s := newTestStore(t, RepositoryOptions{}, subj)

	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthPartial || res.Health.InvalidArtifacts != 1 {
		t.Errorf("health = %+v, want partial with 1 invalid artifact", res.Health)
	}
	if len(res.Records) != 0 {
		t.Errorf("read %+v, want the invalid envelope kept out of the projection", res.Records)
	}
	err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-2", 2, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryIncomplete) {
		t.Fatalf("commit over an invalid stored envelope = %v, want ErrRegistryIncomplete", err)
	}
}

// An artifact that keeps the configured revision digest but restates the subject
// descriptor the registry resolved is not a referrer of the revision this scan is
// enumerating. It is counted invalid, kept out of the projection and blocks the
// next write, because a history with a member nobody could read cannot answer
// "has this already been accepted?".
func TestStore_CommitFailsClosedOnSubjectDescriptorMismatch(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	rec := storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt)
	pushSubjectMismatch(t, openRepo(t, host, RepositoryOptions{}), subj, desc, rec)
	s := newTestStore(t, RepositoryOptions{}, subj)

	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthPartial || res.Health.InvalidArtifacts != 1 {
		t.Errorf("health = %+v, want partial with 1 invalid artifact", res.Health)
	}
	if len(res.Records) != 0 {
		t.Errorf("read %+v, want the mismatched subject kept out of the projection", res.Records)
	}
	err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-2", 2, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryIncomplete) {
		t.Fatalf("commit over a subject-mismatched artifact = %v, want ErrRegistryIncomplete", err)
	}
}

// The store is the operator's configuration boundary: a record about a revision
// nobody configured is refused even though the producer signed it.
func TestStore_CommitRejectsUnconfiguredSubject(t *testing.T) {
	s, subj := oneSubjectStore(t)
	other := Subject{Registry: subj.Registry, Repository: subj.Repository, Digest: testDigest("b")}
	err := s.Commit(t.Context(), storeRecord(other, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrContractRefPolicy) {
		t.Fatalf("commit on an unconfigured subject = %v, want ErrContractRefPolicy", err)
	}
}

// A record that is not a well-formed evidence record never becomes an artifact.
func TestStore_CommitRejectsUnbuildableRecord(t *testing.T) {
	s, subj := oneSubjectStore(t)
	rec := storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt)
	rec.Service = "" // no logical identity to attach the evidence to
	err := s.Commit(t.Context(), rec)
	if !errors.Is(err, evidenceingest.ErrStoreWrite) || !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("commit of an identity-less record = %v, want a wrapped ErrInvalidArtifact", err)
	}
}

// gatedRegistry serves an inner registry until writes are closed off, as a
// credential with pull-only permission on the contract repository does.
type gatedRegistry struct {
	inner    http.Handler
	mu       sync.Mutex
	readOnly bool
}

func (g *gatedRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	closed := g.readOnly
	g.mu.Unlock()
	if closed && r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, `{"errors":[{"code":"DENIED","message":"pull access only"}]}`, http.StatusForbidden)
		return
	}
	g.inner.ServeHTTP(w, r)
}

func (g *gatedRegistry) closeWrites() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.readOnly = true
}

// A registry that refuses the write fails the commit; nothing is reported as
// accepted that was never stored.
func TestStore_CommitFailsWhenTheRegistryRefusesTheWrite(t *testing.T) {
	gate := &gatedRegistry{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})}
	host := serve(t, gate)
	subj, _ := seedContract(t, host, RepositoryOptions{})
	s := newTestStore(t, RepositoryOptions{}, subj)
	gate.closeWrites()

	err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrStoreWrite) {
		t.Fatalf("commit against a pull-only credential = %v, want ErrStoreWrite", err)
	}
	if n := len(s.List(t.Context()).Records); n != 0 {
		t.Errorf("read %d records after a refused write, want 0", n)
	}
}

// hidingRegistry stores everything but always lists no referrers, as a registry
// with a broken or eventually-consistent referrers index does.
type hidingRegistry struct{ inner http.Handler }

func (h hidingRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/referrers/") {
		w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
		_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"` + ocispec.MediaTypeImageIndex + `","manifests":[]}`))
		return
	}
	h.inner.ServeHTTP(w, r)
}

// A push the Referrers API does not then list is not accepted evidence: a later
// scan would never find it, so reporting 202 would be a lie. Confirming through
// the same API those scans use is what makes acceptance mean it.
func TestStore_CommitFailsWhenTheRecordIsNotDiscoverable(t *testing.T) {
	host := serve(t, hidingRegistry{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})})
	subj, _ := seedContract(t, host, RepositoryOptions{})
	s := newTestStore(t, RepositoryOptions{}, subj)

	err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrStoreWrite) {
		t.Fatalf("commit an undiscoverable record = %v, want ErrStoreWrite", err)
	}
	if got := err.Error(); !strings.Contains(got, "not discoverable") {
		t.Errorf("error = %q, want it to name the discoverability failure", got)
	}
}

// Every Referrers page must be read before a write is allowed: stopping at the
// first page would let a replayed report through the moment the history outgrows
// one page.
func TestStore_ReplayProtectionSpansEveryPage(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{PageSize: 2})
	subj, _ := seedContract(t, host, RepositoryOptions{PageSize: 2})
	s := newTestStore(t, RepositoryOptions{PageSize: 2}, subj)

	const history = 7
	for i := 1; i <= history; i++ {
		mustCommit(t, s, storeRecord(subj, "remote-eu", fmt.Sprintf("env-%d", i), uint64(i), fmt.Sprintf("orders-%d", i), acceptedAt))
	}
	if n := len(s.List(t.Context()).Records); n != history {
		t.Fatalf("read %d records across pages, want %d", n, history)
	}
	// The oldest envelope id lives on the FIRST page; the highest sequence only
	// becomes visible on the last one.
	if err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-1", 99, "orders-1", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("duplicate id from page one = %v, want ErrReplay", err)
	}
	if err := s.Commit(t.Context(), storeRecord(subj, "remote-eu", "env-new", history, "orders-new", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("sequence equal to the last page's maximum = %v, want ErrReplay", err)
	}
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-new", history+1, "orders-new", acceptedAt))
}

// recorder serves inner and remembers every manifest PUT path in order.
type recorder struct {
	inner http.Handler
	mu    sync.Mutex
	puts  []string
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/manifests/") {
		r.mu.Lock()
		r.puts = append(r.puts, req.URL.Path)
		r.mu.Unlock()
	}
	r.inner.ServeHTTP(w, req)
}

func TestStorePrefixCommitWritesAnchorThenRecord(t *testing.T) {
	rec := &recorder{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})}
	host := serve(t, rec)
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	rec.puts = nil
	s := prefixStore(t, host)
	mustCommit(t, s, storeRecord(subj, "crossplane", "env-1", 1, "invoices-db", acceptedAt))

	if len(rec.puts) != 3 || !strings.HasSuffix(rec.puts[0], "/acme/pacto-evidence/manifests/"+anchorTag(subj.Ref())) ||
		!strings.HasSuffix(rec.puts[1], "/acme/pacto-evidence/manifests/"+nativeTag(subj.Ref())) ||
		!strings.Contains(rec.puts[2], "/acme/invoices/manifests/sha256:") {
		t.Fatalf("manifest writes = %v, want the anchor tag, the native marker then the record referrer", rec.puts)
	}
	// A store with no state reads it back from the ledger.
	res := prefixStore(t, host).List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || res.Health.Subjects != 2 || len(res.Records) != 1 {
		t.Fatalf("restarted List = %+v (%d records), want the record over 1 ledger and 1 subject", res.Health, len(res.Records))
	}
}

func TestStoreTwoDigestsOneRepositoryUnderPrefix(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	v1, d1 := seedContractIn(t, host, "acme/invoices", "invoices-0.1.0", RepositoryOptions{})
	v2, d2 := seedContractIn(t, host, "acme/invoices", "invoices-0.2.0", RepositoryOptions{})
	s := prefixStore(t, host)
	mustCommit(t, s, storeRecord(v1, "crossplane", "env-1", 1, "db-1", acceptedAt))
	mustCommit(t, s, storeRecord(v2, "crossplane", "env-2", 2, "db-2", acceptedAt))
	if s.repos[v1].repo != s.repos[v2].repo {
		t.Error("two digests of one repository must share one connection")
	}
	if s.repos[v1].descriptor().Digest != d1.Digest || s.repos[v2].descriptor().Digest != d2.Digest {
		t.Error("each digest must keep its own resolved descriptor")
	}
	res := prefixStore(t, host).List(t.Context())
	if len(res.Records) != 2 || res.Health.Subjects != 3 {
		t.Fatalf("List = %+v (%d records), want both revisions", res.Health, len(res.Records))
	}
	for _, r := range res.Records {
		want := map[string]string{"db-1": v1.Digest, "db-2": v2.Digest}[r.Envelope.EvidenceSet.Subject.Name]
		if r.Digest != want {
			t.Errorf("%s read with digest %s, want %s", r.Envelope.EvidenceSet.Subject.Name, r.Digest, want)
		}
	}
}

func TestStoreEmptyLedgerIsReadyAndEmpty(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	s := prefixStore(t, host)
	if err := s.Ready(t.Context()); err != nil {
		t.Fatalf("Ready over an absent ledger: %v", err)
	}
	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || res.Health.Subjects != 1 || res.Health.FailedSubjects != 0 || len(res.Records) != 0 {
		t.Fatalf("List = %+v, want an authoritative empty store", res.Health)
	}
}

func TestStoreUnreadableLedgerIsPartial(t *testing.T) {
	host := serve(t, failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusForbidden, fail: ledgerPath})
	exact, _ := seedContractIn(t, host, "team/orders", "orders", RepositoryOptions{})

	withExact := prefixStore(t, host, exact)
	if h := withExact.List(t.Context()).Health; h.Status != evidenceingest.HealthPartial || h.Subjects != 2 || h.FailedSubjects != 1 {
		t.Errorf("with an exact subject: health = %+v, want partial, 1 of 2 failed", h)
	}
	if err := withExact.Ready(t.Context()); err == nil {
		t.Error("Ready over an unreadable ledger succeeded")
	}
	if err := withExact.Commit(t.Context(), storeRecord(exact, "p", "env-1", 1, "orders", acceptedAt)); !errors.Is(err, evidenceingest.ErrRegistryUnavailable) {
		t.Errorf("commit over an unreadable ledger = %v, want ErrRegistryUnavailable", err)
	}
	if h := prefixStore(t, host).List(t.Context()).Health; h.Status != evidenceingest.HealthUnavailable || h.Subjects != 1 || h.FailedSubjects != 1 {
		t.Errorf("prefix only: health = %+v, want unavailable, 1 of 1 failed", h)
	}
}

func TestStoreReplayAcrossExactAndLedgerSubjects(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	exact, _ := seedContractIn(t, host, "team/orders", "orders", RepositoryOptions{})
	covered, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host, exact), storeRecord(covered, "p", "env-1", 5, "db", acceptedAt))

	restarted := prefixStore(t, host, exact)
	if err := restarted.Commit(t.Context(), storeRecord(exact, "p", "env-2", 5, "orders", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("sequence reuse aimed at the exact subject = %v, want ErrReplay", err)
	}
	if err := restarted.Commit(t.Context(), storeRecord(exact, "p", "env-1", 9, "orders", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("envelope id reuse aimed at the exact subject = %v, want ErrReplay", err)
	}
	mustCommit(t, restarted, storeRecord(exact, "p", "env-3", 6, "orders", acceptedAt))
}

func TestStoreReplayAfterPrefixAdded(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	mustCommit(t, newTestStore(t, RepositoryOptions{}, subj), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))

	both := prefixStore(t, host, subj)
	if err := both.Commit(t.Context(), storeRecord(subj, "p", "env-1", 2, "db", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Fatalf("replay after the prefix was added = %v, want ErrReplay", err)
	}
	mustCommit(t, both, storeRecord(subj, "p", "env-2", 2, "db", acceptedAt.Add(time.Hour)))
	// The subject is now both exact and in the ledger: read once.
	mustCommit(t, both, storeRecord(subj, "p", "env-3", 3, "db-2", acceptedAt))
	if h := both.List(t.Context()).Health; h.Subjects != 2 {
		t.Errorf("health = %+v, want the subject counted once beside the ledger", h)
	}
	res := prefixStore(t, host).List(t.Context())
	if len(res.Records) != 2 {
		t.Fatalf("prefix-only store read %d targets, want 2 (the anchor written after the prefix makes the subject's history visible)", len(res.Records))
	}
}

// deleteManifest removes repoPath@dgst as a retention policy would.
func deleteManifest(t *testing.T, host, repoPath, dgst string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, "http://"+host+"/v2/"+repoPath+"/manifests/"+dgst, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("DELETE %s@%s: %d", repoPath, dgst, resp.StatusCode)
	}
}

func TestStoreDeletedLedgerSubjectIsPruned(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	gone, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	kept, _ := seedContractIn(t, host, "acme/billing", "billing", RepositoryOptions{})
	s := prefixStore(t, host)
	mustCommit(t, s, storeRecord(gone, "p", "env-1", 1, "db-1", acceptedAt))
	mustCommit(t, s, storeRecord(kept, "p", "env-2", 2, "db-2", acceptedAt))
	deleteManifest(t, host, "acme/invoices", gone.Digest)

	restarted := prefixStore(t, host)
	res := restarted.List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || res.Health.Subjects != 2 || len(res.Records) != 1 {
		t.Fatalf("List = %+v (%d records), want ready over the ledger and the surviving subject", res.Health, len(res.Records))
	}
	if err := restarted.Commit(t.Context(), storeRecord(kept, "p", "env-2", 9, "db-2", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("replay of a surviving envelope = %v, want ErrReplay", err)
	}
	mustCommit(t, restarted, storeRecord(kept, "p", "env-3", 3, "db-2", acceptedAt))
}

func TestStorePlantedAnchorToMissingDigestDoesNotBlockWrites(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	real, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	missing, _ := ParseSubject(Scheme + host + "/acme/ghost@" + testDigest("c"))
	plantAnchor(t, host, buildAnchor(missing), anchorTag(missing.Ref()))
	s := prefixStore(t, host)
	mustCommit(t, s, storeRecord(real, "p", "env-1", 1, "db", acceptedAt))
	if h := s.List(t.Context()).Health; h.Status != evidenceingest.HealthReady {
		t.Errorf("health = %+v, want ready with the planted anchor pruned", h)
	}
}

// A ledger subject the registry cannot answer for (as opposed to one it no
// longer holds) is a failed read: writes stay refused.
func TestStoreLedgerSubjectTransportErrorFails(t *testing.T) {
	var broken atomic.Bool
	host := serve(t, failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusForbidden, fail: func(r *http.Request) bool {
		return broken.Load() && r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/acme/invoices/manifests/")
	}})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
	broken.Store(true)
	if h := prefixStore(t, host).List(t.Context()).Health; h.Status != evidenceingest.HealthPartial || h.FailedSubjects != 1 {
		t.Errorf("health = %+v, want partial with the ledger subject failed", h)
	}
}

func TestStoreCommitUnresolvableCoveredSubject(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	missing, _ := ParseSubject(Scheme + host + "/acme/ghost@" + testDigest("c"))
	err := prefixStore(t, host).Commit(t.Context(), storeRecord(missing, "p", "env-1", 1, "db", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryUnavailable) {
		t.Fatalf("commit on a missing revision = %v, want ErrRegistryUnavailable", err)
	}
}

func TestStoreCommitAnchorWriteRefused(t *testing.T) {
	host := serve(t, failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusForbidden, fail: func(r *http.Request) bool {
		return r.Method == http.MethodPut && ledgerPath(r)
	}})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	s := prefixStore(t, host)
	if err := s.Commit(t.Context(), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt)); !errors.Is(err, evidenceingest.ErrStoreWrite) {
		t.Fatalf("commit with a refused anchor = %v, want ErrStoreWrite", err)
	}
	if n := len(s.List(t.Context()).Records); n != 0 {
		t.Errorf("read %d records, want none: the record is never written without its anchor", n)
	}
}

func TestStoreRepoForRejectsAnUnparsedSubject(t *testing.T) {
	s := prefixStore(t, "registry.example")
	if _, err := s.repoFor(Subject{Registry: "bad host", Repository: "x", Digest: digestA}); err == nil {
		t.Fatal("repoFor opened an unaddressable subject")
	}
}

func TestNewStoreRejectsUnaddressableLedger(t *testing.T) {
	if _, err := NewStore(nil, PrefixList{{Registry: "bad host"}}, RepositoryOptions{}); err == nil {
		t.Fatal("NewStore opened an unaddressable ledger")
	}
}

// probesOwner is the test that holds a fresh prober now.
var probesOwner *testing.T

// freshProbes gives the test its own registry prober, once per test: the
// process prober remembers each host with no TTL (a failure for a minute),
// and a test server can reuse the port of an earlier one with other
// capabilities. serve calls it, so every test that starts a registry has one.
// The tests in this package are not parallel.
func freshProbes(t *testing.T) {
	if probesOwner == t {
		return
	}
	old, oldOwner := ocireferrer.Probes, probesOwner
	ocireferrer.Probes, probesOwner = &ocireferrer.Prober{}, t
	t.Cleanup(func() { ocireferrer.Probes, probesOwner = old, oldOwner })
}

// flipRegistry serves inner, answering 404 on the referrers endpoint while
// noReferrers is set, as a registry without the Referrers API does.
type flipRegistry struct {
	inner       http.Handler
	noReferrers atomic.Bool
	referrers   atomic.Int32 // referrers GETs that reached the registry
}

func (f *flipRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/referrers/") {
		f.referrers.Add(1)
		if f.noReferrers.Load() {
			http.NotFound(w, r)
			return
		}
	}
	f.inner.ServeHTTP(w, r)
}

func newFlipRegistry(t *testing.T, noReferrers bool) (*flipRegistry, string) {
	t.Helper()
	f := &flipRegistry{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})}
	f.noReferrers.Store(noReferrers)
	return f, serve(t, f)
}

// repoTags lists every tag of repoPath on host.
func repoTags(t *testing.T, host, repoPath string) []string {
	t.Helper()
	repo, _ := ocireferrer.Open(host+"/"+repoPath, RepositoryOptions{})
	tags, err := ocireferrer.Tags(t.Context(), repo, "")
	if err != nil && !ocireferrer.RepositoryNotFound(err) {
		t.Fatal(err)
	}
	return tags
}

// noReferrersTag fails when any tag uses oras's shared referrers-tag scheme.
func noReferrersTag(t *testing.T, tags []string) {
	t.Helper()
	for _, tag := range tags {
		if strings.HasPrefix(tag, "sha256-") {
			t.Errorf("tag %s uses the shared referrers-tag scheme", tag)
		}
	}
}

func TestStoreNativePlacement(t *testing.T) {
	rec := &recorder{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})}
	host := serve(t, rec)
	exact, _ := seedContractIn(t, host, "team/orders", "orders", RepositoryOptions{})
	covered, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	rec.puts = nil

	mustCommit(t, newTestStore(t, RepositoryOptions{}, exact), storeRecord(exact, "p", "env-1", 1, "orders", acceptedAt))
	for _, put := range rec.puts {
		if !strings.Contains(put, "/manifests/sha256:") {
			t.Errorf("exact subject on a native registry wrote %s, want only referrers by digest", put)
		}
	}
	mustCommit(t, prefixStore(t, host), storeRecord(covered, "p", "env-2", 2, "db", acceptedAt))
	if got := repoTags(t, host, "acme/pacto-evidence"); len(got) != 2 || got[0] != anchorTag(covered.Ref()) || got[1] != nativeTag(covered.Ref()) {
		t.Errorf("ledger tags = %v, want only the anchor and its native marker", got)
	}
	if got := repoTags(t, host, "acme/invoices"); len(got) != 0 {
		t.Errorf("subject repository tags = %v, want none", got)
	}
}

func TestStoreWriteOnceTagPlacement(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	s := prefixStore(t, host)
	mustCommit(t, s, storeRecord(subj, "crossplane", "env-1", 1, "db", acceptedAt))

	ledger := repoTags(t, host, "acme/pacto-evidence")
	if len(ledger) != 2 || ledger[0] != anchorTag(subj.Ref()) || !strings.HasPrefix(ledger[1], anchorTag(subj.Ref())+"-") || len(ledger[1]) != recordTagLen {
		t.Fatalf("ledger tags = %v, want the anchor and one record tag", ledger)
	}
	noReferrersTag(t, ledger)
	if got := repoTags(t, host, "acme/invoices"); len(got) != 0 {
		t.Errorf("subject repository tags = %v, want none", got)
	}
	if err := s.Ready(t.Context()); err != nil {
		t.Errorf("Ready: %v", err)
	}
	res := prefixStore(t, host).List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || res.Health.Subjects != 2 || len(res.Records) != 1 || res.Records[0].Digest != subj.Digest {
		t.Fatalf("restarted List = %+v (%+v), want the tag-stored record", res.Health, res.Records)
	}
}

func TestStoreWriteOnceReplayAfterRestart(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	a, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	b, _ := seedContractIn(t, host, "acme/billing", "billing", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host), storeRecord(a, "p", "env-1", 5, "db", acceptedAt))

	restarted := prefixStore(t, host)
	if err := restarted.Commit(t.Context(), storeRecord(b, "p", "env-1", 9, "db-2", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("envelope id replayed at another subject = %v, want ErrReplay", err)
	}
	if err := restarted.Commit(t.Context(), storeRecord(b, "p", "env-2", 5, "db-2", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("sequence reuse = %v, want ErrReplay", err)
	}
	mustCommit(t, restarted, storeRecord(b, "p", "env-2", 6, "db-2", acceptedAt))
}

func TestStoreProbeErrorRefusesWrite(t *testing.T) {
	host := serve(t, failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusInternalServerError,
		fail: func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") }})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	err := prefixStore(t, host).Commit(t.Context(), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryUnavailable) {
		t.Fatalf("commit with a failing probe = %v, want ErrRegistryUnavailable", err)
	}
	if got := repoTags(t, host, "acme/pacto-evidence"); len(got) != 0 {
		t.Errorf("ledger tags after a refused write = %v, want none", got)
	}
	exact := newTestStore(t, RepositoryOptions{}, subj)
	if err := exact.Ready(t.Context()); err == nil {
		t.Error("Ready with a failing probe succeeded")
	}
	if h := exact.List(t.Context()).Health; h.Status != evidenceingest.HealthUnavailable {
		t.Errorf("health with a failing probe = %+v, want unavailable", h)
	}
}

func TestStoreUncoveredExactSubjectOnTagsRegistryNotReady(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	exact, _ := seedContractIn(t, host, "team/orders", "orders", RepositoryOptions{})
	covered, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	err := prefixStore(t, host, exact).Ready(t.Context())
	if err == nil || !strings.Contains(err.Error(), "registry "+host+" serves no OCI Referrers: add a subject prefix covering "+exact.Ref()+" to store its evidence as write-once tags in <prefix>pacto-evidence") {
		t.Fatalf("Ready = %v, want the actionable message", err)
	}
	if err := prefixStore(t, host, covered).Ready(t.Context()); err != nil {
		t.Errorf("Ready with the exact subject under the prefix: %v", err)
	}
}

func TestStoreReadyProbesOncePerHost(t *testing.T) {
	reg, host := newFlipRegistry(t, false)
	a, _ := seedContractIn(t, host, "team/orders", "orders", RepositoryOptions{})
	b, _ := seedContractIn(t, host, "team/payments", "payments", RepositoryOptions{})
	c, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host), storeRecord(c, "p", "env-1", 1, "db", acceptedAt))

	ocireferrer.Probes = &ocireferrer.Prober{} // a restarted process
	reg.referrers.Store(0)
	s := prefixStore(t, host, a, b)
	for range 2 {
		if err := s.Ready(t.Context()); err != nil {
			t.Fatalf("Ready: %v", err)
		}
	}
	if n := reg.referrers.Load(); n != 1 {
		t.Errorf("Ready made %d referrers requests, want 1 for the one host", n)
	}
}

func TestStoreReadsBothPlacementsAfterModeFlip(t *testing.T) {
	reg, host := newFlipRegistry(t, true)
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	first := storeRecord(subj, "p", "env-1", 1, "db", acceptedAt)
	mustCommit(t, prefixStore(t, host), first)

	// The registry now serves Referrers, and a restarted process probes again.
	reg.noReferrers.Store(false)
	ocireferrer.Probes = &ocireferrer.Prober{}
	s := prefixStore(t, host)
	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || len(res.Records) != 1 {
		t.Fatalf("List after the flip = %+v (%d records), want the tag-stored record", res.Health, len(res.Records))
	}
	if err := s.Commit(t.Context(), storeRecord(subj, "p", "env-1", 2, "db", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("replay after the flip = %v, want ErrReplay", err)
	}
	mustCommit(t, s, storeRecord(subj, "p", "env-2", 2, "db-2", acceptedAt))
	if n := len(s.List(t.Context()).Records); n != 2 {
		t.Errorf("read %d records, want the tag record and the referrer", n)
	}

	// A record held in both placements counts once.
	sr := s.parsedRepo(subj)
	art, err := BuildArtifact(first, subj, sr.descriptor())
	if err != nil {
		t.Fatal(err)
	}
	if err := ocireferrer.Push(t.Context(), sr.repo, art); err != nil {
		t.Fatal(err)
	}
	if sc := s.scan(t.Context()); len(sc.records) != 2 || sc.invalid != 0 {
		t.Errorf("scan = %d records, %d invalid, want each record once", len(sc.records), sc.invalid)
	}
}

// TestStoreFailsClosedOnNativeRecordsAfterReferrersStop: records stored as
// referrers cannot be listed once the registry stops serving Referrers. The
// subject's native marker makes the read partial and every write a 503, so a
// replay never passes against a history missing them.
func TestStoreFailsClosedOnNativeRecordsAfterReferrersStop(t *testing.T) {
	reg, host := newFlipRegistry(t, false)
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	other, _ := seedContractIn(t, host, "acme/orders", "orders", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host), storeRecord(subj, "p", "env-1", 7, "db", acceptedAt))

	// The registry stops serving Referrers, and a restarted process probes again.
	reg.noReferrers.Store(true)
	ocireferrer.Probes = &ocireferrer.Prober{}
	s := prefixStore(t, host)
	if h := s.List(t.Context()).Health; h.Status != evidenceingest.HealthPartial || h.FailedSubjects != 1 {
		t.Fatalf("List after the flip = %+v, want partial with the marked subject failed", h)
	}
	for name, rec := range map[string]evidenceingest.Record{
		"replayed id":   storeRecord(other, "p", "env-1", 8, "db-2", acceptedAt),
		"older seq":     storeRecord(subj, "p", "env-2", 3, "db", acceptedAt),
		"unrelated new": storeRecord(other, "q", "env-3", 1, "db-3", acceptedAt),
	} {
		if err := s.Commit(t.Context(), rec); !errors.Is(err, evidenceingest.ErrRegistryUnavailable) || !strings.Contains(err.Error(), "serves no Referrers") {
			t.Errorf("%s after the flip = %v, want 503 naming the unreadable referrers", name, err)
		}
	}
}

// endlessTags serves inner, except that the ledger's tag listing always holds
// one evidence tag and always links a next page.
type endlessTags struct{ inner http.Handler }

func (e endlessTags) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v2/acme/pacto-evidence/tags/list" {
		w.Header().Set("Link", `</v2/acme/pacto-evidence/tags/list?n=100&last=x>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"acme/pacto-evidence","tags":["evidence-` + strings.Repeat("0", 64) + `"]}`))
		return
	}
	e.inner.ServeHTTP(w, r)
}

func TestStoreLedgerOverTagCapIsUnavailable(t *testing.T) {
	host := serve(t, endlessTags{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	s := prefixStore(t, host)
	if h := s.List(t.Context()).Health; h.Status != evidenceingest.HealthUnavailable {
		t.Errorf("health = %+v, want unavailable", h)
	}
	err := s.Commit(t.Context(), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrRegistryUnavailable) || !strings.Contains(err.Error(), host+"/acme/pacto-evidence") || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("commit past the cap = %v, want 503 naming the ledger and the cap", err)
	}
}

func TestStoreNativeMarkerWriteRefused(t *testing.T) {
	rec := &recorder{inner: failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusForbidden, fail: func(r *http.Request) bool {
		return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, nativeSuffix)
	}}}
	host := serve(t, rec)
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	rec.puts = nil
	err := prefixStore(t, host).Commit(t.Context(), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrStoreWrite) {
		t.Fatalf("commit with a refused native marker = %v, want ErrStoreWrite", err)
	}
	for _, put := range rec.puts {
		if strings.Contains(put, "/acme/invoices/") {
			t.Errorf("wrote %s: a referrer is never pushed without its native marker", put)
		}
	}
}

func TestStoreWriteOnceRecordWriteRefused(t *testing.T) {
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: true})
	host := serve(t, failing{inner: inner, status: http.StatusForbidden, fail: func(r *http.Request) bool {
		return r.Method == http.MethodPut && ledgerPath(r) && len(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]) == recordTagLen
	}})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	err := prefixStore(t, host).Commit(t.Context(), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
	if !errors.Is(err, evidenceingest.ErrStoreWrite) {
		t.Fatalf("commit with a refused record tag = %v, want ErrStoreWrite", err)
	}
}

func TestStorePrunedSubjectKeepsItsTagRecords(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	gone, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	kept, _ := seedContractIn(t, host, "acme/billing", "billing", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host), storeRecord(gone, "p", "env-1", 1, "db", acceptedAt))
	deleteManifest(t, host, "acme/invoices", gone.Digest)

	s := prefixStore(t, host)
	res := s.List(t.Context())
	if res.Health.Status != evidenceingest.HealthReady || len(res.Records) != 1 || res.Records[0].Digest != gone.Digest {
		t.Fatalf("List = %+v (%+v), want the pruned subject's record", res.Health, res.Records)
	}
	if err := s.Commit(t.Context(), storeRecord(kept, "p", "env-1", 2, "db-2", acceptedAt)); !errors.Is(err, evidenceingest.ErrReplay) {
		t.Errorf("replay of the pruned subject's envelope = %v, want ErrReplay", err)
	}
	if err := s.Ready(t.Context()); err != nil {
		t.Errorf("Ready over a pruned ledger subject: %v", err)
	}

	// A record tag of the pruned subject whose manifest names no subject is
	// bound to nothing: invalid.
	bare := ocireferrer.Build(ArtifactType, PayloadMediaType, []byte("{}"), nil, nil)
	plantAnchor(t, host, bare, recordTag(gone.Ref(), bare.ManifestDesc.Digest))
	if sc := prefixStore(t, host).scan(t.Context()); sc.invalid != 1 || sc.pruned != 1 {
		t.Errorf("scan = %d invalid, %d pruned, want the subject-less record invalid", sc.invalid, sc.pruned)
	}
}

// A registry that stops serving Referrers inside one process (the probe answer
// is cached) is a failed read, never an empty history.
func TestStoreReferrersGoneInProcessFailsClosed(t *testing.T) {
	reg, host := newFlipRegistry(t, false)
	subj, _ := seedContract(t, host, RepositoryOptions{})
	s := newTestStore(t, RepositoryOptions{}, subj)
	mustCommit(t, s, storeRecord(subj, "p", "env-1", 1, "orders", acceptedAt))
	reg.noReferrers.Store(true)
	if h := s.List(t.Context()).Health; h.Status != evidenceingest.HealthUnavailable {
		t.Errorf("health = %+v, want unavailable", h)
	}
}

// Every record tag that cannot be read as a record on its subject makes the
// read partial and writes fail closed.
func TestStoreRejectsBadRecordTags(t *testing.T) {
	for name, plant := range map[string]func(Subject, ocireferrer.Artifact) string{
		// The anchor manifest under a record tag named after another digest.
		"misnamed": func(s Subject, _ ocireferrer.Artifact) string { return anchorTag(s.Ref()) + "-000000000000" },
		// The anchor manifest under a tag named after it: not a record.
		"foreign": func(s Subject, a ocireferrer.Artifact) string { return recordTag(s.Ref(), a.ManifestDesc.Digest) },
		// A record tag with no anchor.
		"orphan": func(Subject, ocireferrer.Artifact) string {
			return ledgerTagPrefix + strings.Repeat("0", 64) + "-0123456789ab"
		},
	} {
		t.Run(name, func(t *testing.T) {
			host := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
			subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
			mustCommit(t, prefixStore(t, host), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
			anchor := buildAnchor(subj)
			plantAnchor(t, host, anchor, plant(subj, anchor))
			if sc := prefixStore(t, host).scan(t.Context()); sc.invalid != 1 || len(sc.records) != 1 {
				t.Errorf("scan = %d invalid, %d records, want the bad tag counted once", sc.invalid, len(sc.records))
			}
		})
	}
	t.Run("unresolvable", func(t *testing.T) {
		var broken atomic.Bool
		inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: true})
		host := serve(t, failing{inner: inner, status: http.StatusForbidden, fail: func(r *http.Request) bool {
			return broken.Load() && r.Method == http.MethodHead && len(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]) == recordTagLen
		}})
		subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
		mustCommit(t, prefixStore(t, host), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
		broken.Store(true)
		if sc := prefixStore(t, host).scan(t.Context()); sc.invalid != 1 || len(sc.records) != 0 {
			t.Errorf("scan = %d invalid, %d records, want the unresolvable tag invalid", sc.invalid, len(sc.records))
		}
	})
}

// A ledger whose anchors are all unusable has nothing to probe: readiness
// skips a record tag, an invalid anchor and a pruned subject.
func TestStoreReadySkipsUnusableAnchors(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	missing, _ := ParseSubject(Scheme + host + "/acme/ghost@" + testDigest("c"))
	other, _ := ParseSubject(Scheme + host + "/acme/other@" + testDigest("d"))
	plantAnchor(t, host, buildAnchor(missing), anchorTag(missing.Ref()))
	plantAnchor(t, host, buildAnchor(other), anchorTag(missing.Ref())[:anchorTagLen-1]+"0")
	plantAnchor(t, host, buildAnchor(other), ledgerTagPrefix+strings.Repeat("0", 64)+"-0123456789ab")
	if err := prefixStore(t, host).Ready(t.Context()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}

func TestStoreReadyFailsOnUnreadableLedgerSubject(t *testing.T) {
	var broken atomic.Bool
	host := serve(t, failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusForbidden, fail: func(r *http.Request) bool {
		return broken.Load() && r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/acme/invoices/manifests/")
	}})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	mustCommit(t, prefixStore(t, host), storeRecord(subj, "p", "env-1", 1, "db", acceptedAt))
	broken.Store(true)
	if err := prefixStore(t, host).Ready(t.Context()); err == nil {
		t.Fatal("Ready with an unreadable ledger subject succeeded")
	}
}
