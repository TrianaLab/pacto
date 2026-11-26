package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// memberRecords is an in-process streams repository holding one promote
// record per stream, each signed by key.
func memberRecords(t *testing.T, key ed25519.PrivateKey, streams ...string) (string, map[string]string) {
	t.Helper()
	return memberRecordsAt(t, key, 1, streams...)
}

// memberRecordsAt is memberRecords with each record at seq.
func memberRecordsAt(t *testing.T, key ed25519.PrivateKey, seq int64, streams ...string) (string, map[string]string) {
	t.Helper()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	path := strings.TrimPrefix(srv.URL, "http://") + "/delivery/streams"
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{Insecure: []string{strings.TrimPrefix(srv.URL, "http://")}})
	ids := map[string]string{}
	ctx := context.Background()
	for _, stream := range streams {
		anchor := deliveryrecord.Anchor(stream)
		if err := ocireferrer.Push(ctx, repo, anchor); err != nil {
			t.Fatal(err)
		}
		st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation, Predicate: delivery.RecordPredicate{
			Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: delivery.TypePromote},
			Intent:  delivery.RecordIntent{UID: "uid-ship", Name: "ship", Params: &delivery.Params{To: cliA}},
			Actor:   &delivery.RecordActor{User: "bot", Class: delivery.ClassAgent},
			Outcome: &delivery.RecordOutcome{Result: delivery.Succeeded, Verification: delivery.Verified, Pointer: delivery.PointerChange{After: cliA}},
			Chain:   delivery.Chain{Stream: stream, Seq: seq}, Time: delivery.RecordTime{Finished: "2026-10-21T10:00:00Z"}}}
		b, id, err := delivery.SignRecord(ctx, edSigner(key), st)
		if err != nil {
			t.Fatal(err)
		}
		art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, b, &anchor.ManifestDesc,
			map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateOperation})
		if err := ocireferrer.Push(ctx, repo, art); err != nil {
			t.Fatal(err)
		}
		ids[stream] = id
	}
	return path, ids
}

// openRecords opens a streams repository over plain HTTP.
func openRecords(path string, _ ...string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Insecure: []string{strings.Split(path, "/")[0]}})
}

// recorderKey is pub as a member's advertised recorder key.
func recorderKey(pub ed25519.PublicKey) *delivery.TrustedKey {
	pem, _ := delivery.PublicKeyPEM(pub)
	return &delivery.TrustedKey{PublicKey: pem}
}

// recordedMember is an approved member eu-prod recording billing in the
// namespaces given, with records signed by signer, read under key.
func recordedMember(t *testing.T, key, signer ed25519.PrivateKey, namespaces ...string) (*memberRecordDelivery, map[string]string) {
	t.Helper()
	var streams []string
	var joins []delivery.JoinStream
	for _, ns := range namespaces {
		s := euProd + "/" + ns + "/billing-prod"
		streams = append(streams, s)
		joins = append(joins, delivery.JoinStream{Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Stream: s})
	}
	path, ids := memberRecords(t, signer, streams...)
	m := delivery.ClusterJoinState{UID: euProd, Name: "eu-prod", State: delivery.JoinApproved, RecorderKey: recorderKey(key.Public().(ed25519.PublicKey)),
		StreamsRepository: path, Streams: joins}
	verify, err := signing.TrustVerifier(*delivery.MemberRecordTrust([]delivery.ClusterJoinState{m}))
	if err != nil {
		t.Fatal(err)
	}
	return &memberRecordDelivery{member: m, registry: "zot.test/acme/pacto-targets", reader: &deliveryrecord.Reader{Verify: verify},
		records: openRecords, now: time.Now, hidden: map[string]bool{}}, ids
}

// TestMemberStagesFromVerifiedRecords: a member's Stage reads from the
// records its recorder signs and says the member recorded it.
func TestMemberStagesFromVerifiedRecords(t *testing.T) {
	t.Parallel()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	m, _ := recordedMember(t, key, key, "billing")
	ctx := t.Context()
	sts, err := m.Stages(ctx, "")
	if err != nil || len(sts) != 1 {
		t.Fatalf("%v %+v", err, sts)
	}
	st := sts[0]
	if st.Metadata.Namespace != "billing" || st.Metadata.Name != "billing-prod" || st.Spec.Service.Name != "billing" || st.Status.Pointer != cliA ||
		len(st.Status.Conditions) != 1 || !strings.Contains(st.Status.Conditions[0].Message, "recordedBy: member eu-prod") {
		t.Fatalf("%+v", st)
	}
	other, _ := m.Stages(ctx, "payments")
	if len(other) != 0 || len(m.Unreadable()) != 0 || m.HiddenNamespaces() != 0 {
		t.Errorf("%+v %v %d", other, m.Unreadable(), m.HiddenNamespaces())
	}
}

// TestMemberHistoryBlameRecordFromRecords: a member Stage's history, blame
// and records read from the same verified stream; what it does not record
// is the same 404 as an absent object.
func TestMemberHistoryBlameRecordFromRecords(t *testing.T) {
	t.Parallel()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	m, ids := recordedMember(t, key, key, "billing")
	ctx := t.Context()
	id := ids[euProd+"/billing/billing-prod"]
	h, herr := m.History(ctx, "billing", "billing-prod", nil)
	b, berr := m.Blame(ctx, "billing", "billing-prod", "")
	r, rerr := m.Record(ctx, "billing", "billing-prod", id)
	if herr != nil || len(h.Entries) != 1 || berr != nil || b.Digest != cliA || rerr != nil || r.Record != id || r.Trust != "member-recorder:eu-prod" {
		t.Fatalf("%v %v %v %+v %+v %+v", herr, berr, rerr, h, b, r)
	}
	for name, err := range map[string]error{
		"unknown record": func() error { _, err := m.Record(ctx, "billing", "billing-prod", "sha256:nope"); return err }(),
		"stage":          func() error { _, err := m.Stage(ctx, "billing", "nope"); return err }(),
		"blame":          func() error { _, err := m.Blame(ctx, "billing", "nope", ""); return err }(),
		"record":         func() error { _, err := m.Record(ctx, "billing", "nope", id); return err }(),
		"get":            func() error { _, err := m.Get(ctx, "billing", "ship"); return err }(),
		"why":            func() error { _, err := m.Explain(ctx, "billing", "ship"); return err }(),
	} {
		if code(err) != "NotFound" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestMemberReadsNothingElse: a hub reads no Operation, revision, member
// plan, namespace list, ladder or refusal of a member, and asks no access.
func TestMemberReadsNothingElse(t *testing.T) {
	t.Parallel()
	m := &memberRecordDelivery{member: delivery.ClusterJoinState{Name: "eu-prod"}}
	ctx := t.Context()
	ops, oerr := m.Operations(ctx, "")
	ns, nerr := m.StageNamespaces(ctx)
	v, rows, readable, eerr := m.Environments(ctx)
	refusals, perr := m.PublishRefusals(ctx, "")
	got := []bool{oerr == nil && len(ops) == 0, reflect.DeepEqual(m.Describe(ctx, &delivery.StageObject{}, cliA), delivery.Revision{}),
		m.Members(ctx, "billing", nil) == nil, ns == nil && nerr == nil, v == nil && rows == nil && !readable && eerr == nil, refusals == nil && perr == nil,
		strings.Contains(m.WorkloadAccess(ctx, "billing", delivery.WorkloadRef{Name: "billing"}).Unverified, "eu-prod")}
	for i, ok := range got {
		if !ok {
			t.Errorf("check %d", i)
		}
	}
}

// TestUnverifiedMemberRecordCountedNotShown: a record the member's recorder
// key does not verify is counted, never shown, and the Stage reads as one
// whose records could not be read.
func TestUnverifiedMemberRecordCountedNotShown(t *testing.T) {
	t.Parallel()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	_, forger, _ := ed25519.GenerateKey(rand.Reader)
	m, _ := recordedMember(t, key, forger, "billing")
	ctx := t.Context()
	if sts, err := m.Stages(ctx, ""); err != nil || len(sts) != 0 {
		t.Fatalf("%v %+v", err, sts)
	}
	if u := m.Unreadable(); len(u) != 1 || !strings.Contains(u[0], "member-recorder:eu-prod") {
		t.Fatalf("%v", u)
	}
	if _, err := m.Stage(ctx, "billing", "billing-prod"); code(err) != "RecordsUnreadable" {
		t.Errorf("%v", err)
	}
	// A stream that is not whole (its first record is missing) says why.
	gap, _ := recordedMember(t, key, key, "billing")
	path, _ := memberRecordsAt(t, key, 2, euProd+"/billing/billing-prod")
	gap.member.StreamsRepository = path
	if sts, _ := gap.Stages(ctx, ""); len(sts) != 0 || !strings.Contains(strings.Join(gap.Unreadable(), ";"), "not whole") {
		t.Errorf("gap: %+v %v", sts, gap.Unreadable())
	}
	gap.member.StreamsRepository = "127.0.0.1:1/delivery/streams"
	if sts, _ := gap.Stages(ctx, ""); len(sts) != 0 || !strings.Contains(strings.Join(gap.Unreadable(), ";"), "reading the records") {
		t.Errorf("unreachable: %+v %v", sts, gap.Unreadable())
	}
	m.records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("registry down") }
	if sts, _ := m.Stages(ctx, ""); len(sts) != 0 || !strings.Contains(strings.Join(m.Unreadable(), ";"), "registry down") {
		t.Errorf("%+v %v", sts, m.Unreadable())
	}
}

// TestMemberStreamsOwnAndVisibleOnly: a stream that is not the member's own
// is skipped, and one in a namespace the reader may not read is counted.
func TestMemberStreamsOwnAndVisibleOnly(t *testing.T) {
	t.Parallel()
	m := &memberRecordDelivery{member: delivery.ClusterJoinState{UID: euProd, Name: "eu-prod", Streams: []delivery.JoinStream{
		{Stream: usProd + "/billing/billing-prod"}, {Stream: "malformed"}, {Stream: euProd + "/payments/payments-prod"}, {Stream: euProd + "/billing/billing-prod"},
	}}, sees: func(ns string) bool { return ns == "billing" }, hidden: map[string]bool{}}
	got := m.streams("")
	if len(got) != 1 || got[0].namespace != "billing" || got[0].name != "billing-prod" || m.HiddenNamespaces() != 1 {
		t.Fatalf("%+v %d", got, m.HiddenNamespaces())
	}
}

// TestMemberWritesRefuseWithIntentCommand: a member only reads here; every
// write says a person decides its held request on the hub, by intent.
func TestMemberWritesRefuseWithIntentCommand(t *testing.T) {
	t.Parallel()
	m := &memberRecordDelivery{member: delivery.ClusterJoinState{Name: "eu-prod"}, registry: "zot.test/acme/pacto-targets"}
	ctx := t.Context()
	for name, err := range map[string]error{
		"whoami":  func() error { _, err := m.WhoAmI(ctx); return err }(),
		"plan":    func() error { _, err := m.Plan(ctx, delivery.Request{}); return err }(),
		"propose": func() error { _, _, err := m.Propose(ctx, delivery.Request{}); return err }(),
		"approve": func() error { _, err := m.Approve(ctx, "billing", "ship", delivery.Approve, ""); return err }(),
		"cancel":  m.Cancel(ctx, "billing", "ship"),
		"undo":    func() error { _, err := m.Undo(ctx, "billing", "ship", ""); return err }(),
		"trust":   func() error { _, err := m.Trust(ctx, nil, nil); return err }(),
	} {
		if code(err) != "RemoteClusterReadOnly" || !strings.Contains(err.Error(), "pacto delivery approve --intent <intent record> --registry zot.test/acme/pacto-targets") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if ok, err := m.Can(ctx, "create", "operations", "billing"); ok || err != nil {
		t.Error("can")
	}
}

// TestMemberRemotesFromTheHub: the hub reads each approved member no
// context reaches from its records; a member with no recorder key is a
// partial cluster saying so, one a context reads live is skipped, a stale
// snapshot is said, and members that cannot be read add none.
func TestMemberRemotesFromTheHub(t *testing.T) {
	// newClusterEnv sets the environment: not parallel.
	eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
	e := newRequestHub(t, eu, us)
	_, rec, _ := ed25519.GenerateKey(rand.Reader)
	stream := euProd + "/billing/billing-prod"
	path, _ := memberRecords(t, rec, stream)
	e.publishJoin(t, eu, func(j *delivery.SnapshotJoin) {
		j.RecorderKey, j.StreamsRepository = recorderKey(rec.Public().(ed25519.PublicKey)), path
		j.Streams = []delivery.JoinStream{{Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Stream: stream}}
	})
	id := &dashboardIdentity{members: e.hubMembers(), records: openRecords, now: time.Now}
	ctx := t.Context()
	rs := id.memberRemotes(ctx, authn.Person{}, nil)
	byName := map[string]dashboard.RemoteDelivery{}
	for _, r := range rs {
		byName[r.Cluster.Name] = r
	}
	euR, usR := byName["eu-prod"], byName["us-prod"]
	if len(rs) != 2 || euR.API == nil || euR.AsOf == nil || !strings.Contains(euR.Act, "--intent") {
		t.Fatalf("%+v", rs)
	}
	if usR.Err == nil || !strings.Contains(usR.Err.Error(), delivery.RefusalMemberRecorderUnknown) {
		t.Fatalf("no recorder key: %+v", usR)
	}
	if sts, err := euR.API.Stages(ctx, ""); err != nil || len(sts) != 1 {
		t.Fatalf("%v %+v", err, sts)
	}
	if got := id.memberRemotes(ctx, authn.Person{}, map[string]bool{euProd: true, usProd: true}); len(got) != 0 {
		t.Errorf("live members read again: %+v", got)
	}
	later := &dashboardIdentity{members: e.hubMembers(), records: openRecords, now: func() time.Time { return time.Now().Add(time.Hour) }}
	if stale := later.memberRemotes(ctx, authn.Person{}, map[string]bool{usProd: true}); len(stale) != 1 ||
		!strings.Contains(strings.Join(stale[0].API.(*memberRecordDelivery).Unreadable(), ";"), "past") {
		t.Errorf("stale: %+v", stale)
	}
	// With members and no other context the hub's own cluster is named.
	id.homeUID = func(context.Context) (string, bool) { return clusterHubUID, true }
	home, all := id.remotesFor(ctx, authn.Person{})
	if home.ID != clusterHubUID || len(all) != 2 {
		t.Errorf("%+v %d", home, len(all))
	}
}

// TestMemberRemotesNoneUnread: members that cannot be read, or a host that
// reads none, add no remote.
func TestMemberRemotesNoneUnread(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if got := (&dashboardIdentity{members: failingMembers(errors.New("hub down"))}).memberRemotes(ctx, authn.Person{}, nil); got != nil {
		t.Errorf("unreadable members: %+v", got)
	}
	if got := (&dashboardIdentity{}).memberRemotes(ctx, authn.Person{}, nil); got != nil {
		t.Errorf("no members: %+v", got)
	}
}

// TestMemberRemoteReviewedThroughLink: behind a viewer a member with no
// live channel is hidden, and a namespace there shows only when the
// member's own review lets the reader read Pactos in it.
func TestMemberRemoteReviewedThroughLink(t *testing.T) {
	// newClusterEnv sets the environment: not parallel.
	eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
	e := newRequestHub(t, eu, us)
	_, rec, _ := ed25519.GenerateKey(rand.Reader)
	billing, payments := euProd+"/billing/billing-prod", euProd+"/payments/billing-prod"
	path, _ := memberRecords(t, rec, billing, payments)
	e.publishJoin(t, eu, func(j *delivery.SnapshotJoin) {
		j.RecorderKey, j.StreamsRepository = recorderKey(rec.Public().(ed25519.PublicKey)), path
		j.Streams = []delivery.JoinStream{{Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Stream: billing},
			{Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Stream: payments}}
	})
	viewer := authn.NewViewer(func(context.Context) map[string]authn.Review {
		return map[string]authn.Review{euProd: func(_ context.Context, _ authn.Person, ns string, res authn.ResourceAttributes) (bool, error) {
			return ns == "billing" && res.Resource == "pactos", nil
		}}
	})
	id := &dashboardIdentity{members: e.hubMembers(), records: openRecords, now: time.Now, viewer: viewer}
	ctx := t.Context()
	var seen bool
	for _, r := range id.memberRemotes(ctx, authn.Person{User: "alice"}, nil) {
		switch r.Cluster.ID {
		case usProd:
			if !r.Hidden || r.API != nil {
				t.Errorf("us-prod has no live channel: %+v", r)
			}
		case euProd:
			seen = true
			sts, err := r.API.Stages(ctx, "")
			if err != nil || len(sts) != 1 || sts[0].Metadata.Namespace != "billing" || r.API.(*memberRecordDelivery).HiddenNamespaces() != 1 {
				t.Errorf("%v %+v", err, sts)
			}
		}
	}
	if !seen {
		t.Fatal("eu-prod not read")
	}
}
