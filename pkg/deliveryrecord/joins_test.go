package deliveryrecord

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	jHub    = "hub00000-0000-4000-8000-000000000000"
	jIssuer = "https://idp.acme.example"
	jA      = "aaaa0000-0000-4000-8000-00000000000a"
	jB      = "bbbb0000-0000-4000-8000-00000000000b"
)

var (
	jNow         = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	errForbidden = errors.New("forbidden")
)

// joinsEnv is a hub reading a pacto-targets repository in an in-process
// registry. Reviews allow the users in allow; errs fails some.
type joinsEnv struct {
	path                     string
	mode                     capabilities.ReferrersMode
	repo                     *remote.Repository
	denyTags, failRefs, page atomic.Bool
	// deadOpen makes Open return an unreachable repository.
	deadOpen atomic.Bool
	mu       sync.Mutex
	allow    map[string]bool
	errs     map[string]error
	rec      Reader
}

func newJoinsEnv(t *testing.T, mode capabilities.ReferrersMode) *joinsEnv {
	t.Helper()
	old := ocireferrer.Probes
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = old })
	e := &joinsEnv{mode: mode, allow: map[string]bool{"oidc:alice@acme.com": true},
		errs: map[string]error{"oidc:carol@acme.com": errForbidden, "oidc:dave@acme.com": errors.New("hub down")}}
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags, TagsLink: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case e.denyTags.Load() && strings.HasSuffix(r.URL.Path, "/tags/list"):
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
			return
		case e.failRefs.Load() && strings.Contains(r.URL.Path, "/referrers/"):
			http.Error(w, "down", http.StatusInternalServerError)
			return
		case e.page.Load() && strings.HasSuffix(r.URL.Path, "/tags/list") && r.URL.Query().Get("n") == "":
			q := r.URL.Query()
			q.Set("n", "1")
			r.URL.RawQuery = q.Encode()
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e.path = strings.TrimPrefix(srv.URL, "http://") + "/acme/" + delivery.TargetSnapshotRepository
	e.repo, _ = ocireferrer.Open(e.path, ocireferrer.Options{})
	return e
}

// fakeVerifyPerson opens a record without checking its signature: the user
// is the approver it names, signed at its At. Mallory never verifies.
func fakeVerifyPerson(_ delivery.ClassPolicy, what string, open func(verify func([]byte) error) (string, error)) (string, time.Time, string) {
	var at time.Time
	approver, err := open(func(b []byte) error {
		var env struct {
			DSSEEnvelope struct {
				Payload []byte `json:"payload"`
			} `json:"dsseEnvelope"`
		}
		var st struct {
			Predicate struct {
				At time.Time `json:"at"`
			} `json:"predicate"`
		}
		_ = json.Unmarshal(b, &env)
		_ = json.Unmarshal(env.DSSEEnvelope.Payload, &st)
		at = st.Predicate.At
		return nil
	})
	switch {
	case err != nil:
		return "", at, "a " + what + " was not counted: " + err.Error()
	case strings.HasPrefix(approver, "oidc:mallory"):
		return approver, at, "a " + what + " claiming approver " + approver + " was not counted"
	}
	return approver, at, ""
}

func (e *joinsEnv) in() JoinRead {
	return JoinRead{Repository: e.path, Hub: jHub, Records: &e.rec,
		Authorize: func(_ context.Context, user, _ string) (bool, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.allow[user], e.errs[user]
		},
		Fetch: func(ctx context.Context, repo *remote.Repository, reference string) ([]byte, string, error) {
			b, d, err := FetchTargetSnapshot(ctx, repo, reference)
			return b, d.Digest.String(), err
		},
		Forbidden:    func(err error) bool { return errors.Is(err, errForbidden) },
		VerifyPerson: fakeVerifyPerson, OpenKey: signing.OpenKey,
		Open: func(p string) (*remote.Repository, error) {
			if e.deadOpen.Load() {
				p = "127.0.0.1:1/acme/" + delivery.TargetSnapshotRepository
			}
			return ocireferrer.Open(p, ocireferrer.Options{})
		},
		Now: jNow}
}

func (e *joinsEnv) read(t *testing.T, in JoinRead) delivery.ClusterJoinList {
	t.Helper()
	l, _ := ReadJoins(t.Context(), in)
	return l
}

type jMember struct {
	uid, name string
	key       ed25519.PrivateKey
}

func newJMember(uid, name string) jMember {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	return jMember{uid: uid, name: name, key: key}
}

func (m jMember) join() *delivery.SnapshotJoin {
	der, _ := x509.MarshalPKIXPublicKey(m.key.Public())
	return &delivery.SnapshotJoin{Name: m.name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: signing.Hint(m.key.Public()),
		PublicKey: base64.StdEncoding.EncodeToString(der)}
}

// publish tags, as cluster-<tag>, a snapshot naming cluster signed by key.
func (e *joinsEnv) publish(t *testing.T, m jMember, tag, cluster string, key ed25519.PrivateKey, at time.Time) {
	t.Helper()
	s := delivery.TargetSnapshot{Cluster: cluster, AsOf: at.Format(time.RFC3339), Completeness: fleet.CompletenessComplete,
		Publisher: "pacto-system/pacto-operator-delivery-recorder", Join: m.join()}
	b, err := delivery.SignTargetSnapshot(t.Context(), edSigner(key), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PushTargetSnapshot(t.Context(), e.repo, tag, b); err != nil {
		t.Fatal(err)
	}
}

// record is m's record deciding dec on hub, by user, signed at minute min.
func (m jMember) record(hub, user string, dec delivery.JoinDecision, min int) delivery.ClusterJoin {
	j := m.join()
	return delivery.ClusterJoin{Cluster: m.uid, Hub: hub, KeyID: j.KeyID, PublicKey: j.PublicKey, Decision: dec,
		Approver: "oidc:" + user + "@acme.com", At: jNow.Add(time.Duration(min) * time.Minute).Format(time.RFC3339)}
}

func (e *joinsEnv) decide(t *testing.T, j delivery.ClusterJoin) {
	t.Helper()
	b, err := delivery.SignClusterJoin(t.Context(), edSigner(newJMember("", "").key), delivery.NewClusterJoinStatement(j))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PushClusterJoin(t.Context(), e.repo, b); err != nil {
		t.Fatal(err)
	}
}

func (e *joinsEnv) mapping(t *testing.T, uid, hub, issuer, user string, min int, rules ...string) {
	t.Helper()
	m := delivery.ClusterGroupMapping{Cluster: uid, Hub: hub, Issuer: issuer, Approver: "oidc:" + user + "@acme.com",
		At: jNow.Add(time.Duration(min) * time.Minute).Format(time.RFC3339)}
	for _, r := range rules {
		from, to, _ := strings.Cut(r, "=")
		m.Rules = append(m.Rules, delivery.GroupRule{From: from, To: to})
	}
	b, err := delivery.SignClusterGroupMapping(t.Context(), edSigner(newJMember("", "").key), delivery.NewClusterGroupMappingStatement(m))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PushClusterGroupMapping(t.Context(), e.repo, b); err != nil {
		t.Fatal(err)
	}
}

func jState(t *testing.T, l delivery.ClusterJoinList, uid string) delivery.ClusterJoinState {
	t.Helper()
	for _, c := range l.Clusters {
		if c.UID == uid {
			return c
		}
	}
	t.Fatalf("no cluster %s in %+v", uid, l)
	return delivery.ClusterJoinState{}
}

func says(l delivery.ClusterJoinList, text string) bool {
	return strings.Contains(fmt.Sprint(l.Limitations), text)
}

func TestPluralCountsClusters(t *testing.T) {
	if plural(1, "cluster") != "1 cluster" || plural(2, "cluster") != "2 clusters" {
		t.Fatal(plural(2, "cluster"))
	}
}

// TestReadJoinsPendingWithoutRecord: a member that signed its own snapshot
// and has no record is pending, fresh and self-signed, and the list says
// where it read.
func TestReadJoinsPendingWithoutRecord(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	m := newJMember(jA, "eu-prod")
	e.publish(t, m, jA, jA, m.key, jNow)
	l, repo := ReadJoins(t.Context(), e.in())
	st := jState(t, l, jA)
	if repo == nil || st.State != delivery.JoinPending || st.Reason != "no approval yet" || st.Name != "eu-prod" || l.Completeness != fleet.CompletenessComplete ||
		l.Hub.UID != jHub || l.Repository != e.path || l.AsOf != jNow.Format(time.RFC3339) {
		t.Fatalf("%+v %+v", st, l)
	}
}

// TestReadJoinsSkipsHub: the hub's own tag is never a cluster that asks to join.
func TestReadJoinsSkipsHub(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	h := newJMember(jHub, "hub")
	e.publish(t, h, jHub, jHub, h.key, jNow)
	if l := e.read(t, e.in()); len(l.Clusters) != 0 {
		t.Fatalf("%+v", l)
	}
}

// TestReadJoinsJudgesRecords: an approval by a user the hub allows counts
// and supersedes the older ones; one that does not verify, one by a user the
// hub refuses, one it may not ask about or that fails are not counted and
// say why; another hub's record is counted there.
func TestReadJoinsJudgesRecords(t *testing.T) {
	cases := []struct {
		user, want string
		dec        delivery.JoinDecision
		hub        string
		state      delivery.JoinState
		limitation string
	}{
		{user: "alice", dec: delivery.JoinApprove, hub: jHub, state: delivery.JoinApproved},
		{user: "alice", dec: delivery.JoinRemove, hub: jHub, state: delivery.JoinRemoved},
		{user: "bob", dec: delivery.JoinApprove, hub: jHub, state: delivery.JoinPending, want: "may not approve clusterjoins"},
		{user: "bob", dec: delivery.JoinRemove, hub: jHub, state: delivery.JoinPending, want: "a removal by oidc:bob@acme.com"},
		{user: "carol", dec: delivery.JoinApprove, hub: jHub, state: delivery.JoinPending, want: "may not create subjectaccessreviews", limitation: "grant create on subjectaccessreviews"},
		{user: "dave", dec: delivery.JoinApprove, hub: jHub, state: delivery.JoinPending, want: "hub down", limitation: "could not be checked"},
		{user: "mallory", dec: delivery.JoinApprove, hub: jHub, state: delivery.JoinPending, want: "no approval yet"},
		{user: "alice", dec: delivery.JoinApprove, hub: "other-hub", state: delivery.JoinPending, want: "no approval yet"},
	}
	for _, c := range cases {
		t.Run(c.user+"-"+string(c.dec)+"-"+c.hub, func(t *testing.T) {
			e := newJoinsEnv(t, capabilities.ReferrersNative)
			m := newJMember(jA, "eu-prod")
			e.publish(t, m, jA, jA, m.key, jNow)
			if c.dec == delivery.JoinRemove && c.user == "alice" {
				e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
			}
			e.decide(t, m.record(c.hub, c.user, c.dec, 1))
			l := e.read(t, e.in())
			st := jState(t, l, jA)
			if st.State != c.state || !strings.Contains(st.Reason+fmt.Sprint(st), c.want) || (c.limitation != "" && !says(l, c.limitation)) {
				t.Fatalf("%+v %+v", st, l)
			}
		})
	}
}

// TestReadJoinsSupersedes: once a newer record counts, the older one and a
// record naming another cluster are rejected, so they take no bound again.
func TestReadJoinsSupersedes(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	m, b := newJMember(jA, "eu-prod"), newJMember(jB, "us-prod")
	e.publish(t, m, jA, jA, m.key, jNow)
	e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
	e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 1))
	misplaced := b.record(jHub, "alice", delivery.JoinApprove, 2)
	b2, _ := delivery.SignClusterJoin(t.Context(), edSigner(m.key), delivery.NewClusterJoinStatement(misplaced))
	if _, err := Push(t.Context(), e.repo, delivery.ClusterJoinStream(jA), e.mode, delivery.PredicateClusterJoin, b2, nil); err != nil {
		t.Fatal(err)
	}
	if st := jState(t, e.read(t, e.in()), jA); st.State != delivery.JoinApproved {
		t.Fatalf("%+v", st)
	}
	got, _, _, err := e.rec.ListClusterJoins(t.Context(), e.repo, jA)
	if err != nil || len(got) != 1 {
		t.Fatalf("%d records left, %v", len(got), err)
	}
}

// TestReadJoinsReviewErrorKeepsPending: no approval counts while the hub's
// signers are unknown, the list says why, and its reason keeps what the
// snapshot says when the snapshot is not one the member signed.
func TestReadJoinsReviewErrorKeepsPending(t *testing.T) {
	for _, unread := range []bool{false, true} {
		t.Run(fmt.Sprint(unread), func(t *testing.T) {
			e := newJoinsEnv(t, capabilities.ReferrersNative)
			m, b := newJMember(jA, "eu-prod"), newJMember(jB, "us-prod")
			e.publish(t, m, jA, jA, m.key, jNow)
			e.publish(t, b, jB, jB, m.key, jNow) // not its own key
			e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
			e.mapping(t, jA, jHub, jIssuer, "alice", 1, "platform=readers")
			in := e.in()
			in.PolicyWhy, in.PolicyUnread = "no signers", unread
			l := e.read(t, in)
			a, bs := jState(t, l, jA), jState(t, l, jB)
			// An approval that cannot be judged may be a member's: partial.
			want := fleet.CompletenessPartial
			if a.State != delivery.JoinPending || a.Reason != "no signers" || a.Mappings != nil || !strings.HasPrefix(bs.Reason, "no signers; ") ||
				l.Completeness != want || !says(l, "no signers") {
				t.Fatalf("%+v %+v %+v", a, bs, l)
			}
		})
	}
}

// TestReadJoinsOnceApprovedPendingIsPartial: a member a person approved
// that is pending again (its key changed, or its approver may no longer
// approve) leaves the list partial, so its consumers read unknown.
func TestReadJoinsOnceApprovedPendingIsPartial(t *testing.T) {
	for _, c := range []string{"key", "approver"} {
		t.Run(c, func(t *testing.T) {
			e := newJoinsEnv(t, capabilities.ReferrersNative)
			m := newJMember(jA, "eu-prod")
			e.publish(t, m, jA, jA, m.key, jNow)
			e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
			if c == "key" {
				again := newJMember(jA, "eu-prod")
				e.publish(t, again, jA, jA, again.key, jNow.Add(time.Minute))
			} else {
				e.allow["oidc:alice@acme.com"] = false
			}
			l := e.read(t, e.in())
			if st := jState(t, l, jA); st.State != delivery.JoinPending || l.Completeness != fleet.CompletenessPartial ||
				!says(l, "pending again (a key changed or the approval no longer counts): 1 cluster") {
				t.Fatalf("%+v %+v", st, l)
			}
		})
	}
}

// TestReadJoinsListingDeniedNamesCredential: a registry that refuses the
// listing names the credential to add; one that cannot be opened or listed
// says so; both leave the list partial.
func TestReadJoinsListingDeniedNamesCredential(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	e.denyTags.Store(true)
	l, repo := ReadJoins(t.Context(), e.in())
	if repo != nil || l.Completeness != fleet.CompletenessPartial || !says(l, "no pull credential reads "+e.path) {
		t.Fatalf("%+v", l)
	}
	in := e.in()
	in.Open = func(string) (*remote.Repository, error) { return nil, errors.New("bad path") }
	if l := e.read(t, in); l.Completeness != fleet.CompletenessPartial || !says(l, "could not be listed: bad path") {
		t.Fatalf("%+v", l)
	}
}

// TestReadJoinsBounds: past the tag pages the list is partial; past the
// snapshots a read takes the pending ones are counted; a cluster a person
// decides on is read even with no tag.
func TestReadJoinsBounds(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersWriteOnceTags)
	for i := range delivery.MaxTargetSnapshots + 1 {
		tag := fmt.Sprintf("cluster-%02d", i)
		tagManifest(t, e.repo, []byte(`{"schemaVersion":2,"mediaType":"`+ocispec.MediaTypeImageManifest+`","annotations":{"t":"`+tag+`"}}`), tag)
	}
	in := e.in()
	in.Include = jA
	l := e.read(t, in)
	if st := jState(t, l, jA); !strings.Contains(st.Reason+fmt.Sprint(st), "could not be read") || !says(l, "1 pending clusters were not read") {
		t.Fatalf("%+v %+v", st, l)
	}
	in.Include = "00" // a listed cluster: not added again, and read whatever the bound
	if l := e.read(t, in); len(l.Clusters) != delivery.MaxTargetSnapshots+1 || says(l, "pending clusters were not read") {
		t.Fatalf("%d clusters", len(l.Clusters))
	}
	e.page.Store(true)
	if l := e.read(t, e.in()); l.Completeness != fleet.CompletenessPartial || !says(l, "holds more tags than a read lists") {
		t.Fatalf("%+v", l)
	}
}

// TestReadJoinsRecordsUnreadCutOrForked: records that cannot be read, more
// than a read takes, or a moved tag are counted per cluster.
func TestReadJoinsRecordsUnreadCutOrForked(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	m := newJMember(jA, "eu-prod")
	e.publish(t, m, jA, jA, m.key, jNow)
	e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
	e.failRefs.Store(true)
	if l := e.read(t, e.in()); jState(t, l, jA).State != delivery.JoinPending || !says(l, "the join records of 1 cluster could not be read") {
		t.Fatalf("%+v", l)
	}
	e.failRefs.Store(false)
	for i := range delivery.MaxJoinRecords + 1 {
		e.decide(t, m.record(jHub, "bob", delivery.JoinApprove, i+1))
	}
	if l := e.read(t, e.in()); l.Completeness != fleet.CompletenessPartial || !says(l, "join records and stay pending") {
		t.Fatalf("%+v", l)
	}

	w := newJoinsEnv(t, capabilities.ReferrersWriteOnceTags)
	w.publish(t, m, jA, jA, m.key, jNow)
	w.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
	stream := delivery.ClusterJoinStream(jA)
	moved := KindTag(stream, KindJoin, digest.FromString("another manifest"))
	tagAs(t, w.path, Anchor(stream).ManifestDesc, []byte(`{"moved":1}`), delivery.PredicateClusterJoin, func(digest.Digest) string { return moved })
	if l := w.read(t, w.in()); !says(l, "have tags that do not name their record") {
		t.Fatalf("%+v", l)
	}
}

// TestReadJoinsSnapshots: an approved member whose snapshot cannot be read
// stays pending and is counted; a snapshot that does not open, names another
// cluster or is old says so.
func TestReadJoinsSnapshots(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	m, b := newJMember(jA, "eu-prod"), newJMember(jB, "us-prod")
	e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
	in := e.in()
	in.Include = jA
	if l := e.read(t, in); jState(t, l, jA).State != delivery.JoinPending || !says(l, "the snapshots of 1 cluster an approval names") {
		t.Fatalf("%+v", l)
	}
	e.publish(t, b, jB, jA, b.key, jNow)
	if st := jState(t, e.read(t, e.in()), jB); !strings.Contains(fmt.Sprint(st), "names cluster "+jA) {
		t.Fatalf("%+v", st)
	}
	e.publish(t, b, jB, jB, b.key, jNow.Add(-time.Hour))
	if st := jState(t, e.read(t, e.in()), jB); st.Status != fleet.SourceStale {
		t.Fatalf("%+v", st)
	}
	in = e.in()
	in.Fetch = func(context.Context, *remote.Repository, string) ([]byte, string, error) {
		return []byte("{}"), "ref", nil
	}
	if st := jState(t, e.read(t, in), jB); !strings.Contains(fmt.Sprint(st), "was not used") {
		t.Fatalf("%+v", st)
	}
}

// TestReadJoinsMappingNotesFlushed: each issuer's newest mapping whose
// approver the hub allows is in force; a clear or a refused, unasked or
// failed review leaves none; a mapping that does not verify is rejected and
// counted; a listing that fails or is cut uses none.
func TestReadJoinsMappingNotesFlushed(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	m := newJMember(jA, "eu-prod")
	e.publish(t, m, jA, jA, m.key, jNow)
	e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
	e.mapping(t, jA, jHub, "https://one.example", "alice", 1, "a=old")
	e.mapping(t, jA, jHub, "https://one.example", "alice", 2, "a=new")
	e.mapping(t, jA, jHub, "https://two.example", "bob", 1, "b=x")
	e.mapping(t, jA, jHub, "https://three.example", "carol", 1, "c=x")
	e.mapping(t, jA, jHub, "https://four.example", "dave", 1, "d=x")
	e.mapping(t, jA, jHub, "https://five.example", "alice", 1)
	e.mapping(t, jA, jHub, "https://six.example", "bob", 1)
	e.mapping(t, jA, jHub, "https://seven.example", "mallory", 1, "m=x")
	e.mapping(t, jA, jHub, "https://eight.example", "alice", 3, "z=x")
	e.mapping(t, jA, jHub, "https://eight.example", "alice", 1, "z=y")
	l := e.read(t, e.in())
	got := jState(t, l, jA).Mappings
	if len(got) != 2 || got[0].Issuer != "https://eight.example" || got[1].Rules[0].To != "new" || got[1].ApprovedBy != "oidc:alice@acme.com" ||
		!says(l, "group mapping records that were not counted") || !says(l, "grant create on subjectaccessreviews") || !says(l, "could not be checked") {
		t.Fatalf("%+v %+v", got, l)
	}

	for range delivery.MaxJoinRecords {
		e.mapping(t, jA, jHub, jIssuer, "alice", 4, "x=y")
	}
	if l := e.read(t, e.in()); jState(t, l, jA).Mappings != nil || !says(l, "group mapping records and use none") {
		t.Fatalf("%+v", l)
	}

	in := e.in()
	fetch := in.Fetch
	in.Fetch = func(ctx context.Context, repo *remote.Repository, ref string) ([]byte, string, error) {
		defer e.deadOpen.Store(true) // the records were read; the mappings fail
		return fetch(ctx, repo, ref)
	}
	if l := e.read(t, in); jState(t, l, jA).Mappings != nil || !says(l, "the group mappings of 1 cluster could not be read") {
		t.Fatalf("%+v", l)
	}
}

// TestReadJoinsOtherHubsCountsMappings: another hub's records, joins and
// mappings, are counted in OtherHubs and never decide here.
func TestReadJoinsOtherHubsCountsMappings(t *testing.T) {
	e := newJoinsEnv(t, capabilities.ReferrersNative)
	m := newJMember(jA, "eu-prod")
	e.publish(t, m, jA, jA, m.key, jNow)
	e.decide(t, m.record(jHub, "alice", delivery.JoinApprove, 0))
	e.mapping(t, jA, "other-hub", jIssuer, "alice", 1, "a=b")
	l := e.read(t, e.in())
	if st := jState(t, l, jA); st.State != delivery.JoinApproved || st.Mappings != nil || l.OtherHubs != 1 {
		t.Fatalf("%+v %+v", st, l)
	}
}

// TestOpenMember: a member's snapshot opens only under its approved key and
// only when it names the member.
func TestOpenMember(t *testing.T) {
	a, b := newJMember(jA, "eu-prod"), newJMember(jB, "us-prod")
	sign := func(m jMember, cluster string) []byte {
		s := delivery.TargetSnapshot{Cluster: cluster, AsOf: jNow.Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "p"}
		bundle, err := delivery.SignTargetSnapshot(t.Context(), edSigner(m.key), delivery.NewTargetSnapshotStatement(s))
		if err != nil {
			t.Fatal(err)
		}
		return bundle
	}
	trust := delivery.JoinTrust([]delivery.ClusterJoinState{{UID: a.uid, Name: a.name, PublicKey: a.join().PublicKey, State: delivery.JoinApproved}})
	if st, err := OpenMember(sign(a, a.uid), trust, signing.OpenKey, a.uid); err != nil || st.Cluster != a.uid {
		t.Fatalf("%v %+v", err, st)
	}
	if _, err := OpenMember(sign(b, a.uid), trust, signing.OpenKey, a.uid); err == nil {
		t.Fatal("a snapshot no approved key signed")
	}
	both := delivery.JoinTrust([]delivery.ClusterJoinState{{UID: a.uid, Name: a.name, PublicKey: a.join().PublicKey, State: delivery.JoinApproved},
		{UID: b.uid, Name: b.name, PublicKey: a.join().PublicKey, State: delivery.JoinApproved}})
	if _, err := OpenMember(sign(a, b.uid), both, signing.OpenKey, a.uid); err == nil || !strings.Contains(err.Error(), "holds the snapshot of cluster "+b.uid) {
		t.Fatalf("a snapshot of another cluster: %v", err)
	}
}
