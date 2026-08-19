package evidenceoci

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
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
	s, err := NewStore(SubjectList(subjects), opts)
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

func TestNewStore_RequiresAtLeastOneSubject(t *testing.T) {
	if _, err := NewStore(nil, RepositoryOptions{}); !errors.Is(err, ErrNoSubjects) {
		t.Fatalf("NewStore(nil) = %v, want ErrNoSubjects", err)
	}
}

// A subject whose repository ORAS cannot address is a configuration error at
// construction, not a mystery at the first write.
func TestNewStore_RejectsUnaddressableSubject(t *testing.T) {
	if _, err := NewStore(SubjectList{{Registry: "", Repository: ""}}, RepositoryOptions{}); err == nil {
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

// Readiness is a live registry preflight: every subject must resolve AND answer
// native Referrers discovery, because a store that cannot be enumerated cannot
// replay-protect a write.
func TestStore_Ready(t *testing.T) {
	s, subj := oneSubjectStore(t)
	if err := s.Ready(t.Context()); err != nil {
		t.Fatalf("Ready with no evidence yet: %v", err)
	}
	// A subject that already carries evidence walks a non-empty listing, and the
	// resolved descriptor is memoized, so the second probe is cheap and stable.
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if err := s.Ready(t.Context()); err != nil {
		t.Fatalf("Ready with evidence present: %v", err)
	}
}

// A registry with no native Referrers endpoint must fail readiness. oras-go would
// otherwise fall back to the legacy referrers tag — a mutable pointer no producer
// signed — and the server would advertise a store it cannot trust.
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
