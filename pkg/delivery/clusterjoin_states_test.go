package delivery_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	hubUID    = "hub-0000"
	memberUID = "9f1c4e2a-0000"
)

// goodJoin is an approval of a fresh Ed25519 key for memberUID.
func goodJoin(t *testing.T) delivery.ClusterJoin {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, id := joinKey(t, pub)
	return delivery.ClusterJoin{Cluster: memberUID, Hub: hubUID, KeyID: id, PublicKey: key, Decision: delivery.JoinApprove,
		Approver: "oidc:alice@acme.com", At: "2026-11-20T10:00:00Z"}
}

func TestClusterJoinCheck(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*delivery.ClusterJoin)
		reject string
	}{
		{"good", func(*delivery.ClusterJoin) {}, ""},
		{"remove with a name", func(j *delivery.ClusterJoin) { j.Decision, j.Name = delivery.JoinRemove, "eu-prod" }, ""},
		{"bad uid", func(j *delivery.ClusterJoin) { j.Cluster = "a/b" }, "cluster"},
		{"empty hub", func(j *delivery.ClusterJoin) { j.Hub = "" }, "hub"},
		{"empty approver", func(j *delivery.ClusterJoin) { j.Approver = "" }, "approver"},
		{"empty key", func(j *delivery.ClusterJoin) { j.PublicKey, j.KeyID = "", "" }, "public key"},
		{"key id mismatch", func(j *delivery.ClusterJoin) { j.KeyID = strings.Repeat("0", 64) }, "key id"},
		{"name not a label", func(j *delivery.ClusterJoin) { j.Name = "Eu_Prod" }, "not a DNS label"},
		{"homoglyph", func(j *delivery.ClusterJoin) { j.Name = "eu-pr\u043ed" }, "not a DNS label"},
		{"bidi", func(j *delivery.ClusterJoin) { j.Name = "eu\u202eprod" }, "not a DNS label"},
		{"semicolon", func(j *delivery.ClusterJoin) { j.Name = "eu;rm" }, "not a DNS label"},
		{"unknown decision", func(j *delivery.ClusterJoin) { j.Decision = "maybe" }, "decision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := goodJoin(t)
			tc.edit(&j)
			err := j.Check()
			if (tc.reject == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.reject)) {
				t.Fatalf("Check = %v, want %q", err, tc.reject)
			}
		})
	}
}

// signJoin signs a statement carrying j with a fresh key and returns the
// bundle and that key's verifier.
func signJoin(t *testing.T, j delivery.ClusterJoin) ([]byte, func([]byte) error) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	payload, _ := json.Marshal(delivery.ClusterJoinStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateClusterJoin, Predicate: j})
	b, err := signing.Keyed(kp, "").Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	verify, err := signing.OpenKey(delivery.TrustedKey{PublicKey: pub})
	if err != nil {
		t.Fatal(err)
	}
	return b, verify
}

// TestClusterJoinOpenRoundTrip: a signed record opens under its key, and is
// refused under another key, as another predicate or when Check refuses it.
func TestClusterJoinOpenRoundTrip(t *testing.T) {
	j := goodJoin(t)
	b, verify := signJoin(t, j)
	st, err := delivery.OpenClusterJoin(b, verify)
	if err != nil || st.Predicate != j {
		t.Fatalf("opened %+v, %v", st.Predicate, err)
	}
	if _, err := delivery.OpenClusterJoin(b, func([]byte) error { return errors.New("no") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a bad signature opened: %v", err)
	}
	snap, _ := signedSnapshot(t)
	if _, err := delivery.OpenClusterJoin(snap, verify); err == nil {
		t.Fatal("a target snapshot opened as a join record")
	}
	j.Hub = ""
	bad, verify := signJoin(t, j)
	if _, err := delivery.OpenClusterJoin(bad, verify); err == nil || !strings.Contains(err.Error(), "hub") {
		t.Fatalf("a record Check refuses opened: %v", err)
	}
	if got := delivery.ClusterJoinStream(memberUID); got != memberUID+"/pacto-targets/join" {
		t.Fatal(got)
	}
}

var day = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// cand is a self-signed candidate for uid named name (node-label) with key.
func cand(uid, name string, j delivery.ClusterJoin) delivery.JoinCandidate {
	return delivery.JoinCandidate{UID: uid, SelfSigned: true, AsOf: "2026-11-20T09:00:00Z", Status: fleet.SourceAvailable,
		Join: &delivery.SnapshotJoin{Name: name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: j.KeyID, PublicKey: j.PublicKey}}
}

// rec is j counted, decided as dec, signed at day plus h hours.
func rec(j delivery.ClusterJoin, dec delivery.JoinDecision, h int) delivery.VerifiedJoin {
	j.Decision = dec
	return delivery.VerifiedJoin{Record: j, User: j.Approver, Ref: "r@" + string(dec), SignedAt: day.Add(time.Duration(h) * time.Hour), Counted: true}
}

func one(t *testing.T, c delivery.JoinCandidate, rs delivery.JoinRecords) delivery.ClusterJoinState {
	t.Helper()
	states, _ := delivery.JoinStates(hubUID, []delivery.JoinCandidate{c}, map[string]delivery.JoinRecords{c.UID: rs})
	if len(states) != 1 {
		t.Fatalf("states %+v", states)
	}
	return states[0]
}

func TestJoinStatesPendingWithoutRecord(t *testing.T) {
	j := goodJoin(t)
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{})
	if st.State != delivery.JoinPending || st.Reason != "no approval yet" || st.Name != "eu-prod" || st.KeyID != j.KeyID || st.Status != fleet.SourceAvailable {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesApprovedNewestCountedWins(t *testing.T) {
	j := goodJoin(t)
	old := rec(j, delivery.JoinRemove, 1)
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 2), old}})
	if st.State != delivery.JoinApproved || st.Approver != "oidc:alice@acme.com" || st.ApprovedAt != "2026-11-20T12:00:00Z" || st.Record != "r@approve" || st.NameSource != fleet.ClusterNameFromNodeLabel {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesRemoveAfterApprove(t *testing.T) {
	j := goodJoin(t)
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 1), rec(j, delivery.JoinRemove, 2)}})
	if st.State != delivery.JoinRemoved || st.Record != "r@remove" {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesTieRemoves(t *testing.T) {
	j := goodJoin(t)
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 1), rec(j, delivery.JoinRemove, 1), rec(j, delivery.JoinApprove, 1)}})
	if st.State != delivery.JoinRemoved {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesApproveAfterRemove(t *testing.T) {
	j := goodJoin(t)
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinRemove, 1), rec(j, delivery.JoinApprove, 2)}})
	if st.State != delivery.JoinApproved {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesUncountedRecordNeverDecides(t *testing.T) {
	j := goodJoin(t)
	bad := rec(j, delivery.JoinApprove, 3)
	bad.Counted, bad.Why = false, "an approval by oidc:bob@acme.com was not counted: oidc:bob@acme.com may not approve clusterjoins"
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{bad}})
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "oidc:bob@acme.com may not approve") {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesKeyChangedIsPending(t *testing.T) {
	j := goodJoin(t)
	rotated := goodJoin(t)
	st := one(t, cand(memberUID, "eu-prod", rotated), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 1)}})
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "the key changed from sha256:"+j.KeyID[:12]+" since oidc:alice@acme.com approved it on 2026-11-20") {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesMayReplaceByName(t *testing.T) {
	a, b, c := goodJoin(t), goodJoin(t), goodJoin(t)
	a.Cluster = "uid-a"
	byUID := cand("uid-c", "eu-prod", c)
	byUID.Join.NameSource = fleet.ClusterNameFromUID
	states, _ := delivery.JoinStates(hubUID, []delivery.JoinCandidate{cand("uid-a", "eu-prod", a), cand("uid-b", "eu-prod", b), byUID},
		map[string]delivery.JoinRecords{"uid-a": {Verified: []delivery.VerifiedJoin{rec(a, delivery.JoinApprove, 1)}}})
	if states[0].State != delivery.JoinApproved || states[0].MayReplace != nil {
		t.Fatalf("approved %+v", states[0])
	}
	if mr := states[1].MayReplace; mr == nil || *mr != (delivery.ClusterRef{UID: "uid-a", Name: "eu-prod"}) {
		t.Fatalf("rebuilt %+v", states[1])
	}
	if states[2].MayReplace != nil {
		t.Fatalf("a uid name replaced: %+v", states[2])
	}
}

func TestJoinStatesOverrideNameWins(t *testing.T) {
	j := goodJoin(t)
	j.Name = "eu-west"
	st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 1)}})
	if st.Name != "eu-west" || st.NameSource != fleet.ClusterNameFromApproval {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesSkipsHub(t *testing.T) {
	j := goodJoin(t)
	if states, _ := delivery.JoinStates(hubUID, []delivery.JoinCandidate{cand(hubUID, "hub", j)}, nil); len(states) != 0 {
		t.Fatalf("%+v", states)
	}
}

func TestJoinStatesOtherHubIgnoredAndCounted(t *testing.T) {
	j := goodJoin(t)
	other := rec(j, delivery.JoinApprove, 1)
	other.Record.Hub = "other-hub"
	states, n := delivery.JoinStates(hubUID, []delivery.JoinCandidate{cand(memberUID, "eu-prod", j)},
		map[string]delivery.JoinRecords{memberUID: {Verified: []delivery.VerifiedJoin{other}}})
	if n != 1 || states[0].State != delivery.JoinPending {
		t.Fatalf("%d %+v", n, states)
	}
	wrong := rec(j, delivery.JoinApprove, 1)
	wrong.Record.Cluster = "someone-else"
	if st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: []delivery.VerifiedJoin{wrong}}); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "names cluster someone-else") {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesSelfSignatureMismatchNeverApprovable(t *testing.T) {
	j := goodJoin(t)
	approved := delivery.JoinRecords{Verified: []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 1)}}
	c := cand(memberUID, "eu-prod", j)
	c.SelfSigned = false
	if st := one(t, c, approved); st.State != delivery.JoinPending || !strings.HasPrefix(st.Reason, "the snapshot is not signed by the key it names") {
		t.Fatalf("%+v", st)
	}
	c = delivery.JoinCandidate{UID: memberUID}
	if st := one(t, c, approved); st.State != delivery.JoinPending || !strings.HasPrefix(st.Reason, "the snapshot carries no join block") {
		t.Fatalf("%+v", st)
	}
	c.Err = "its snapshot could not be read: boom"
	if st := one(t, c, approved); st.State != delivery.JoinPending || !strings.HasPrefix(st.Reason, c.Err) {
		t.Fatalf("%+v", st)
	}
}

func TestJoinStatesCutOrErrorIsPending(t *testing.T) {
	j := goodJoin(t)
	approve := []delivery.VerifiedJoin{rec(j, delivery.JoinApprove, 1)}
	if st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: approve, Cut: true}); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "more than 20 join records") {
		t.Fatalf("%+v", st)
	}
	if st := one(t, cand(memberUID, "eu-prod", j), delivery.JoinRecords{Verified: approve, Err: "registry down"}); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "registry down") {
		t.Fatalf("%+v", st)
	}
}

// memberSnapshot signs a snapshot of uid with priv carrying its join block.
func memberSnapshot(t *testing.T, uid string, priv ed25519.PrivateKey) ([]byte, delivery.ClusterJoinState) {
	t.Helper()
	key, id := joinKey(t, priv.Public())
	kp, _ := signing.FromPrivateKey(priv)
	s := delivery.TargetSnapshot{Cluster: uid, AsOf: "2026-11-20T10:00:00Z", Completeness: fleet.CompletenessComplete, Publisher: "pacto/pacto-operator",
		Join: &delivery.SnapshotJoin{Name: "m-" + uid[:2], NameSource: fleet.ClusterNameFromUID, KeyID: id, PublicKey: key}}
	b, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	return b, delivery.ClusterJoinState{UID: uid, Name: s.Join.Name, PublicKey: key, State: delivery.JoinApproved}
}

// TestJoinTrustScopedPerMember: each approved member's snapshot verifies
// scoped under its own key only; a pending member gets no key.
func TestJoinTrustScopedPerMember(t *testing.T) {
	_, ka, _ := ed25519.GenerateKey(rand.Reader)
	_, kb, _ := ed25519.GenerateKey(rand.Reader)
	ba, sa := memberSnapshot(t, "aa-1", ka)
	bb, sb := memberSnapshot(t, "bb-2", kb)
	pending := sb
	pending.UID, pending.State = "cc-3", delivery.JoinPending
	trust := delivery.JoinTrust([]delivery.ClusterJoinState{sa, sb, pending})
	if len(trust.Keys) != 2 || trust.Keys[0].Name != "member:m-aa" || trust.Keys[0].Streams[0] != "aa-1/*/*" {
		t.Fatalf("%+v", trust.Keys)
	}
	for _, b := range [][]byte{ba, bb} {
		if _, v, err := delivery.OpenTargetSnapshot(b, trust, signing.OpenKey); err != nil || !v.Scoped {
			t.Fatalf("verdict %+v, %v", v, err)
		}
	}
	onlyA := delivery.JoinTrust([]delivery.ClusterJoinState{sa})
	if _, _, err := delivery.OpenTargetSnapshot(bb, onlyA, signing.OpenKey); err == nil {
		t.Fatal("another member's key verified a snapshot")
	}
	// Signed by a's key under b's uid: b's approved key refuses it.
	forged, _ := memberSnapshot(t, "bb-2", ka)
	if _, _, err := delivery.OpenTargetSnapshot(forged, delivery.JoinTrust([]delivery.ClusterJoinState{sb}), signing.OpenKey); err == nil {
		t.Fatal("a snapshot signed by another key verified")
	}
}

// TestClusterJoinSignOpenRoundTrip: a statement NewClusterJoinStatement
// builds and SignClusterJoin signs opens under the signer's key, its subject
// the member's snapshot tag by the sha256 of the member key's DER.
func TestClusterJoinSignOpenRoundTrip(t *testing.T) {
	j := goodJoin(t)
	st := delivery.NewClusterJoinStatement(j)
	der, _ := base64.StdEncoding.DecodeString(j.PublicKey)
	sum := sha256.Sum256(der)
	if len(st.Subject) != 1 || st.Subject[0].Name != "pacto-targets:cluster-"+memberUID || st.Subject[0].Digest["sha256"] != hex.EncodeToString(sum[:]) ||
		st.PredicateType != delivery.PredicateClusterJoin || st.Type != delivery.StatementType {
		t.Fatalf("%+v", st)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	b, err := delivery.SignClusterJoin(t.Context(), signing.Keyed(kp, ""), st)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	verify, _ := signing.OpenKey(delivery.TrustedKey{PublicKey: pub})
	if got, err := delivery.OpenClusterJoin(b, verify); err != nil || got.Predicate != j || got.Subject[0].Digest["sha256"] != st.Subject[0].Digest["sha256"] {
		t.Fatalf("%+v %v", got, err)
	}
}
