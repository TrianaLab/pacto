package delivery_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	euUID = "aaaa0000-0000-4000-8000-00000000000a"
	usUID = "bbbb0000-0000-4000-8000-00000000000b"
	apUID = "cccc0000-0000-4000-8000-00000000000c"
)

var billing = delivery.ServiceRef{Name: "billing"}

func approvedMember(uid, name string, envs ...string) delivery.ClusterJoinState {
	return delivery.ClusterJoinState{UID: uid, Name: name, State: delivery.JoinApproved, Status: fleet.SourceAvailable,
		Requests: &delivery.JoinRequests{Issuers: []string{"https://idp.acme.example"}, Environments: envs}}
}

func members() []delivery.ClusterJoinState {
	ap := approvedMember(apUID, "ap-dev", "prod")
	ap.State = delivery.JoinPending
	return []delivery.ClusterJoinState{approvedMember(euUID, "eu-prod", "prod"), approvedMember(usUID, "us-prod", "production"), ap}
}

func placedUIDs(p delivery.MemberPlacementPlan) string {
	var out []string
	for _, c := range p.Placed {
		out = append(out, c.Name)
	}
	return strings.Join(out, ",")
}

func TestOperationRequestRoundTrip(t *testing.T) {
	r := memberRequest()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	b, err := delivery.SignOperationRequest(t.Context(), signing.Keyed(kp, ""), delivery.NewOperationRequestStatement(r))
	if err != nil {
		t.Fatal(err)
	}
	st, err := delivery.OpenOperationRequest(b, func([]byte) error { return nil })
	if err != nil || st.Predicate.Digest != reqDigest || st.Subject[0].Name != delivery.ClusterRequestStream(reqCluster) {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := delivery.SignOperationRequest(t.Context(), failSigner{}, delivery.NewOperationRequestStatement(r)); err == nil {
		t.Fatal("a failing signer signed")
	}
}

func TestMemberPlacementFromAdvertised(t *testing.T) {
	p := delivery.MemberPlacement(nil, members(), billing, "production", nil)
	if placedUIDs(p) != "eu-prod,us-prod" || p.Source != delivery.PlacementUnconfirmed || p.ConfirmedBy != "" || len(p.Unplaced) != 0 ||
		p.Placed[0].Evidence != "takes requests for prod" {
		t.Fatalf("%+v", p)
	}
	if p := delivery.MemberPlacement(nil, members(), billing, "staging", nil); len(p.Placed) != 0 || len(p.Unplaced) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestMemberPlacementNoAdvertisedIsUnplaced(t *testing.T) {
	p := delivery.MemberPlacement(nil, []delivery.ClusterJoinState{approvedMember(euUID, "eu-prod")}, billing, "production", nil)
	if len(p.Placed) != 0 || len(p.Unplaced) != 1 || !strings.Contains(p.Unplaced[0].Reason, "advertises no environment") {
		t.Fatalf("%+v", p)
	}
}

func TestMemberPlacementIntakeOffUnplaced(t *testing.T) {
	m := approvedMember(euUID, "eu-prod", "prod")
	m.Requests = nil
	p := delivery.MemberPlacement(nil, []delivery.ClusterJoinState{m}, billing, "production", nil)
	if len(p.Placed) != 0 || len(p.Unplaced) != 1 || !strings.Contains(p.Unplaced[0].Reason, "does not take requests") {
		t.Fatalf("%+v", p)
	}
}

func TestMemberPlacementPolicyOverrides(t *testing.T) {
	pol := &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{
		{Name: "any", Environments: []string{"production"}, Namespace: "x"},
		{Name: "eu-only", Services: []string{"bill*"}, Environments: []string{"production"}, Namespace: "billing", ClusterUIDs: []string{euUID, apUID}},
	}}
	p := delivery.MemberPlacement(pol, members(), billing, "production",
		[]delivery.VerifiedRequest{{Request: delivery.OperationRequest{Service: billing, Environment: "production", Requester: "alice"}}})
	if placedUIDs(p) != "eu-prod" || p.Source != delivery.PlacementFromPolicy || p.ConfirmedBy != "" || p.Placed[0].Evidence != "placement rule eu-only" {
		t.Fatalf("%+v", p)
	}
}

func TestMemberPlacementRemovedNeverPlaced(t *testing.T) {
	m := approvedMember(euUID, "eu-prod", "prod")
	m.State = delivery.JoinRemoved
	if p := delivery.MemberPlacement(nil, []delivery.ClusterJoinState{m}, billing, "production", nil); len(p.Placed) != 0 || len(p.Unplaced) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestMemberPlacementConfirmedBy(t *testing.T) {
	reqs := []delivery.VerifiedRequest{
		{Request: delivery.OperationRequest{Service: billing, Environment: "prod", Requester: "oidc:bob@acme.com"}, SignedAt: reqSigned},
		{Request: delivery.OperationRequest{Service: billing, Environment: "production", Requester: "oidc:alice@acme.com"}, SignedAt: reqSigned.Add(time.Minute)},
		{Request: delivery.OperationRequest{Service: billing, Environment: "staging", Requester: "oidc:carol@acme.com"}, SignedAt: reqSigned.Add(time.Hour)},
	}
	p := delivery.MemberPlacement(nil, members(), billing, "production", reqs)
	if p.Source != delivery.RequestPlacedAdvertised || p.ConfirmedBy != "oidc:alice@acme.com" {
		t.Fatalf("%+v", p)
	}
}

func TestMemberPlacementStaleStaysPlaced(t *testing.T) {
	m := approvedMember(euUID, "eu-prod", "prod")
	m.Status = fleet.SourceStale
	p := delivery.MemberPlacement(nil, []delivery.ClusterJoinState{m}, billing, "production", nil)
	if len(p.Placed) != 1 || !p.Placed[0].Stale {
		t.Fatalf("%+v", p)
	}
}

func recorderKey(t *testing.T) (ed25519.PrivateKey, delivery.TrustedKey) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	return priv, delivery.TrustedKey{Name: "claimed", PublicKey: pub, Streams: []string{"*/*/*"}, Role: "x", UntilSeq: 3, UntilRecord: reqRecord}
}

func TestMemberRecordTrustScopesToMember(t *testing.T) {
	_, key := recorderKey(t)
	eu := approvedMember(euUID, "eu-prod", "prod")
	eu.RecorderKey, eu.Streams = &key, []delivery.JoinStream{{Service: billing, Environment: "staging", Stream: euUID + "/billing/billing-staging"}}
	noKey, pending, removed, noStream := eu, eu, eu, eu
	noKey.RecorderKey = nil
	pending.State, removed.State = delivery.JoinPending, delivery.JoinRemoved
	noStream.Streams = nil
	tr := delivery.MemberRecordTrust([]delivery.ClusterJoinState{eu, noKey, pending, removed, noStream})
	if len(tr.Keys) != 1 {
		t.Fatalf("%+v", tr.Keys)
	}
	k := tr.Keys[0]
	if k.Name != "member-recorder:eu-prod" || k.Role != "" || k.UntilSeq != 0 || k.UntilRecord != "" ||
		len(k.Streams) != 1 || k.Streams[0] != euUID+"/billing/billing-staging" {
		t.Fatalf("%+v", k)
	}
	if key.Name != "claimed" || len(key.Streams) != 1 {
		t.Fatalf("the member's key was changed: %+v", key)
	}
}

// TestMemberRecorderKeyNeverVerifiesSnapshotOrOutcome: merged with the
// approved installation keys, a recorder key still never verifies a target
// snapshot or a request outcome.
func TestMemberRecorderKeyNeverVerifiesSnapshotOrOutcome(t *testing.T) {
	priv, key := recorderKey(t)
	inst, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(inst)
	eu := approvedMember(euUID, "eu-prod", "prod")
	eu.PublicKey = base64.StdEncoding.EncodeToString(der)
	eu.RecorderKey, eu.Streams = &key, []delivery.JoinStream{{Service: billing, Environment: "staging", Stream: euUID + "/billing/billing-staging"}}
	states := []delivery.ClusterJoinState{eu}
	trust := delivery.JoinTrust(states)
	trust.Keys = append(trust.Keys, delivery.MemberRecordTrust(states).Keys...)
	kp, _ := signing.FromPrivateKey(priv)
	s := delivery.TargetSnapshot{Cluster: euUID, AsOf: reqSigned.Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "pacto/pacto-operator"}
	sb, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenTargetSnapshot(sb, trust, signing.OpenKey); err == nil {
		t.Fatal("a recorder key verified a snapshot")
	}
	o := outcome(delivery.RequestRefused)
	o.Cluster = euUID
	ob, err := delivery.SignRequestOutcome(t.Context(), signing.Keyed(kp, ""), delivery.NewRequestOutcomeStatement(o))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := delivery.OpenRequestOutcome(ob, trust, euUID, signing.OpenKey); err == nil {
		t.Fatal("a recorder key verified an outcome")
	}
}

func TestMemberMode(t *testing.T) {
	for _, tc := range []struct {
		r    delivery.Request
		want bool
	}{
		{delivery.Request{Service: "billing", Environment: "production"}, true},
		{delivery.Request{Stage: "s", Service: "billing", Environment: "production"}, false},
		{delivery.Request{Service: "billing"}, false},
		{delivery.Request{Stage: "s"}, false},
	} {
		if got := tc.r.MemberMode(); got != tc.want {
			t.Fatalf("%+v: %v", tc.r, got)
		}
	}
}

func TestHubRefusal(t *testing.T) {
	req := delivery.Request{Service: "billing", Environment: "production", To: "1.5.0", Members: []string{euUID}}
	if got := delivery.ConfirmCommand(req, "zot/acme"); got != "pacto delivery propose --service billing --environment production --to 1.5.0 --member "+euUID+" --registry zot/acme --confirm" {
		t.Fatal(got)
	}
	if got := delivery.ConfirmCommand(delivery.Request{Service: "s", Environment: "e"}, ""); !strings.Contains(got, "--to <sha256 digest>") || !strings.Contains(got, "--registry <registry>") {
		t.Fatal(got)
	}
	for _, code := range []string{delivery.RefusalMemberRequestNeedsPerson, delivery.RefusalNotAMember, delivery.RefusalStageOrMembers,
		delivery.RefusalMemberRecorderUnknown, delivery.RefusalDigestRequired, delivery.RefusalMemberTakesNoRequests, delivery.RefusalIssuerNotAccepted} {
		err := delivery.HubRefusal(code, "why", req, "zot/acme")
		if !strings.HasPrefix(err.Error(), code+": why; next: ") || strings.HasSuffix(err.Error(), "next: ") {
			t.Fatalf("%s: %v", code, err)
		}
	}
}
