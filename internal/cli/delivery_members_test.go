package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	usProd       = "bbbb0000-0000-4000-8000-00000000000b"
	memberDigest = "sha256:5e1f000000000000000000000000000000000000000000000000000000000000"
)

// publishJoin signs m's snapshot, its join block edited by edit, and tags it.
func (e *clusterEnv) publishJoin(t *testing.T, m joinMember, edit func(*delivery.SnapshotJoin)) {
	t.Helper()
	der, _ := x509.MarshalPKIXPublicKey(m.key.Public())
	j := &delivery.SnapshotJoin{Name: m.name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: m.keyID(), PublicKey: base64.StdEncoding.EncodeToString(der),
		Requests: &delivery.JoinRequests{Issuers: []string{clusterIssuer}, Environments: []string{"prod"}}}
	if edit != nil {
		edit(j)
	}
	s := delivery.TargetSnapshot{Cluster: m.uid, AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "pacto/pacto-operator", Join: j}
	kp, _ := signing.FromPrivateKey(m.key)
	b, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), e.registry(), m.uid, b); err != nil {
		t.Fatal(err)
	}
}

// newRequestHub is a hub whose approved members ms take requests for prod.
func newRequestHub(t *testing.T, ms ...joinMember) *clusterEnv {
	t.Helper()
	e := newClusterEnv(t, capabilities.ReferrersNative)
	for _, m := range ms {
		e.publishJoin(t, m, nil)
		if _, errOut, err := e.approve(t, m); err != nil {
			t.Fatalf("%v %s", err, errOut)
		}
	}
	return e
}

// delivery runs pacto delivery with args against the hub's registry.
func (e *clusterEnv) delivery(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runRoot(t, e.d, nil, append(append([]string{"delivery"}, args...), "--registry", e.repo)...)
}

// memberArgs plan billing in production at digest.
func memberArgs(verb, digest string, extra ...string) []string {
	return append([]string{verb, "--service", "billing", "--environment", "production", "--to", digest}, extra...)
}

// requests are the requests on uid's stream.
func (e *clusterEnv) requests(t *testing.T, uid string) []deliveryrecord.Fetched {
	t.Helper()
	recs, _, err := (&deliveryrecord.Reader{}).ListOperationRequests(t.Context(), e.registry(), uid)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

// ask pushes alice's request on uid and returns its manifest.
func (e *clusterEnv) ask(t *testing.T, uid string) string {
	t.Helper()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token(e.email, clusterIssuer), nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := delivery.OperationRequest{Cluster: uid, Hub: clusterHubUID, Service: delivery.ServiceRef{Name: "billing"}, Environment: "production", Digest: memberDigest,
		Requester: alice, Placement: delivery.RequestPlacement{Source: delivery.RequestPlacedAdvertised, ConfirmedBy: alice},
		At: now.Format(time.RFC3339), Expires: now.Add(time.Hour).Format(time.RFC3339)}
	b, err := delivery.SignOperationRequest(t.Context(), s, delivery.NewOperationRequestStatement(r))
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := deliveryrecord.PushOperationRequest(t.Context(), e.registry(), b)
	if err != nil {
		t.Fatal(err)
	}
	_, dg, _ := strings.Cut(ref, "@")
	return dg
}

// answer pushes m's outcome o.
func (e *clusterEnv) answer(t *testing.T, m joinMember, o delivery.RequestOutcome) {
	t.Helper()
	kp, _ := signing.FromPrivateKey(m.key)
	o.Cluster, o.At = m.uid, time.Now().UTC().Format(time.RFC3339)
	b, err := delivery.SignRequestOutcome(t.Context(), signing.Keyed(kp, ""), delivery.NewRequestOutcomeStatement(o))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := deliveryrecord.PushRequestOutcome(t.Context(), e.registry(), b); err != nil {
		t.Fatal(err)
	}
}

var timestamps = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ`)

// TestDeliveryPlanMemberMode: the placement, its source, each member's
// state with its uid and the confirming command.
func TestDeliveryPlanMemberMode(t *testing.T) {
	eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
	e := newRequestHub(t, eu, us)
	e.answer(t, eu, delivery.RequestOutcome{Request: e.ask(t, eu.uid), State: delivery.RequestConcluded, Outcome: delivery.Succeeded,
		Operation: &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-0123456789"}})
	e.answer(t, us, delivery.RequestOutcome{Request: e.ask(t, us.uid), State: delivery.RequestConcluded, Outcome: delivery.Rejected,
		Published: "pacto.published: no publisher signature for billing", Operation: &delivery.RequestedOperation{Namespace: "billing", Name: "billing-req-77"}})
	out, _, err := e.delivery(t, memberArgs("plan", memberDigest)...)
	want := "placement: eu-prod, us-prod (confirmed by " + alice + ")\n" +
		"eu-prod (aaaa0000): applied sha256:5e1f00000000 billing/billing-prod-req-0123456789 (Succeeded) as of <t>\n" +
		"us-prod (bbbb0000): refused sha256:5e1f00000000 billing/billing-req-77 (Rejected: pacto.published: no publisher signature for billing) as of <t>\n" +
		"as of <t> (complete)\n" +
		"confirm with: pacto delivery propose --service billing --environment production --to " + memberDigest + " --registry " + e.repo + " --confirm\n"
	if got := timestamps.ReplaceAllString(out, "<t>"); err != nil || got != want {
		t.Fatalf("%v\n%s\nwant\n%s", err, got, want)
	}
	if out, _, err := e.delivery(t, append(memberArgs("plan", memberDigest), "--output-format", "json")...); err != nil || !strings.Contains(out, `"members": {`) {
		t.Fatalf("%v %s", err, out)
	}
}

// TestPrintMemberPlanEveryPart: a policy placement, an unplaced member, a
// stale member with its next command, hidden members and notes.
func TestPrintMemberPlanEveryPart(t *testing.T) {
	var b bytes.Buffer
	printMemberPlan(&b, delivery.MemberPlan{Placement: delivery.MemberPlacementPlan{Source: delivery.PlacementFromPolicy,
		Placed:   []delivery.PlacedMember{{UID: euProd, Name: "eu-prod", Evidence: "placement rule eu"}},
		Unplaced: []delivery.UnplacedMember{{UID: usProd, Name: "us-prod", Reason: "does not take requests"}}},
		Members: []delivery.MemberState{{UID: euProd, Name: "eu-prod", State: delivery.MemberHeld, Stale: true, Next: "pacto delivery approve --intent r"}},
		Hidden:  1, HiddenReason: "no live channel", Notes: []string{"n"}, AsOf: "t", Completeness: fleet.CompletenessPartial})
	want := "placement: eu-prod (placement rule eu)\neu-prod (aaaa0000): stale, held\n  next: pacto delivery approve --intent r\n" +
		"not placed: us-prod (bbbb0000): does not take requests\n1 member hidden: no live channel\nnote: n\nas of t (partial)\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	printMemberPlan(&b, delivery.MemberPlan{Placement: delivery.MemberPlacementPlan{Source: delivery.PlacementUnconfirmed}, Hidden: 2, AsOf: "t"})
	if !strings.HasPrefix(b.String(), "placement: none (proposed from the environments they take requests for)\n2 members hidden") {
		t.Fatalf("%q", b.String())
	}
}

// TestDeliveryPlanMemberModeNeedsDigest: member mode refuses a version, and
// a member request needs a registry.
func TestDeliveryPlanMemberModeNeedsDigest(t *testing.T) {
	e := newRequestHub(t)
	if _, _, err := e.delivery(t, memberArgs("plan", "1.5.0")...); err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalDigestRequired+":") {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, e.d, nil, append([]string{"delivery"}, memberArgs("plan", memberDigest)...)...); err == nil || !strings.Contains(err.Error(), "pass --registry") {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, e.d, nil, append([]string{"delivery"}, memberArgs("propose", memberDigest)...)...); err == nil || !strings.Contains(err.Error(), "pass --registry") {
		t.Fatal(err)
	}
}

// TestRequestFlagsStageOrMembers: a Stage and members, neither, or a member
// request of another type are refused.
func TestRequestFlagsStageOrMembers(t *testing.T) {
	e := newRequestHub(t)
	for name, args := range map[string][]string{
		"both":    memberArgs("plan", memberDigest, "--stage", "billing-prod"),
		"member":  {"plan", "--stage", "billing-prod", "--member", euProd},
		"neither": {"plan", "--to", memberDigest},
		"service": {"plan", "--service", "billing", "--to", memberDigest},
		"type":    memberArgs("plan", memberDigest, "--type", "rollback"),
	} {
		if _, _, err := e.delivery(t, args...); err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalStageOrMembers+":") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestProposeMemberModeNeedsPerson: propose in member mode without
// --confirm signs nothing and says what a person runs.
func TestProposeMemberModeNeedsPerson(t *testing.T) {
	eu := newJoinMember(euProd, "eu-prod")
	e := newRequestHub(t, eu)
	logins := e.logins
	_, _, err := e.delivery(t, memberArgs("propose", memberDigest)...)
	if err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalMemberRequestNeedsPerson+":") || !strings.Contains(err.Error(), "--confirm") ||
		e.logins != logins || len(e.requests(t, eu.uid)) != 0 {
		t.Fatalf("%v, %d logins", err, e.logins-logins)
	}
}

// TestProposeMemberRequiresConfirm: without --confirm the plan and the
// confirming command are printed.
func TestProposeMemberRequiresConfirm(t *testing.T) {
	eu := newJoinMember(euProd, "eu-prod")
	e := newRequestHub(t, eu)
	out, _, err := e.delivery(t, memberArgs("propose", memberDigest)...)
	if err == nil || !strings.HasPrefix(out, "placement: eu-prod (proposed from the environments they take requests for)\neu-prod (aaaa0000): no request") ||
		!strings.Contains(out, "confirm with: pacto delivery propose") {
		t.Fatalf("%v %q", err, out)
	}
}

// TestDeliveryProposeMemberSignsPerMember: with --confirm the person logs in
// once and one request per placed member is signed and pushed.
func TestDeliveryProposeMemberSignsPerMember(t *testing.T) {
	for _, mode := range []capabilities.ReferrersMode{capabilities.ReferrersNative, capabilities.ReferrersWriteOnceTags} {
		t.Run(string(mode), func(t *testing.T) {
			eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
			e := newClusterEnv(t, mode)
			for _, m := range []joinMember{eu, us} {
				e.publishJoin(t, m, nil)
				if _, errOut, err := e.approve(t, m); err != nil {
					t.Fatalf("%v %s", err, errOut)
				}
			}
			logins := e.logins
			out, errOut, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm", "--reason", "ship")...)
			if err != nil || e.logins != logins+1 || len(e.requests(t, eu.uid)) != 1 || len(e.requests(t, us.uid)) != 1 ||
				!strings.Contains(out, "requested sha256:5e1f00000000 on eu-prod (aaaa0000) as "+alice+"; recorded "+e.repo+"@sha256:") ||
				!strings.Contains(errOut, "To sign a member request on billing in production") ||
				(mode == capabilities.ReferrersWriteOnceTags) != strings.Contains(out, "(write-once tag; registry has no Referrers)") {
				t.Fatalf("%v\n%s\n%s", err, out, errOut)
			}
			out, _, _ = e.delivery(t, memberArgs("plan", memberDigest)...)
			if !strings.Contains(out, "(confirmed by "+alice+")") || !strings.Contains(out, "eu-prod (aaaa0000): waiting") {
				t.Fatalf("%s", out)
			}
		})
	}
}

// TestDeliveryProposeMemberUnknownMember: a --member that is no member, a
// pending one, one that takes no requests and a placement with no member
// get nothing signed and no login.
func TestDeliveryProposeMemberUnknownMember(t *testing.T) {
	eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
	ap := newJoinMember("cccc0000-0000-4000-8000-00000000000c", "ap-dev")
	e := newRequestHub(t, eu)
	e.publishJoin(t, us, func(j *delivery.SnapshotJoin) { j.Requests = nil })
	if _, errOut, err := e.approve(t, us); err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	e.publishJoin(t, ap, nil)
	logins := e.logins
	for want, member := range map[string]string{
		delivery.RefusalNotAMember + ": no cluster dddd0000":          "dddd0000",
		delivery.RefusalNotAMember + ": ap-dev (cccc0000) is pending": ap.uid,
		delivery.RefusalMemberTakesNoRequests + ": us-prod":           us.uid,
	} {
		if _, _, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm", "--member", member)...); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, _, err := e.delivery(t, "propose", "--service", "billing", "--environment", "staging", "--to", memberDigest, "--confirm"); err == nil ||
		!strings.HasPrefix(err.Error(), delivery.RefusalNotAMember+": no member is placed") {
		t.Error(err)
	}
	if e.logins != logins || len(e.requests(t, us.uid)) != 0 {
		t.Fatalf("%d logins", e.logins-logins)
	}
}

// TestProposeMemberIssuerNotAccepted: a member that takes requests through
// another issuer gets nothing signed; neither does a hub with no signer for
// the issuer asked, an unreadable policy, a login as another user or a
// kubeconfig whose user cannot be read.
func TestProposeMemberIssuerNotAccepted(t *testing.T) {
	eu := newJoinMember(euProd, "eu-prod")
	e := newClusterEnv(t, capabilities.ReferrersNative)
	e.publishJoin(t, eu, func(j *delivery.SnapshotJoin) { j.Requests.Issuers = []string{"https://other.example"} })
	if _, errOut, err := e.approve(t, eu); err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	logins := e.logins
	if _, _, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm")...); err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalIssuerNotAccepted+": eu-prod") || e.logins != logins {
		t.Fatal(err)
	}
	if _, _, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm", "--issuer", "https://nope.example")...); err == nil || !strings.Contains(err.Error(), "--issuer") {
		t.Fatal(err)
	}
	e.publishJoin(t, eu, nil)
	e.email = "bob@acme.com"
	if _, _, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm")...); err == nil || !strings.Contains(err.Error(), "ApproverMismatch") || len(e.requests(t, eu.uid)) != 0 {
		t.Fatal(err)
	}
	e.email = "alice@acme.com"
	newDynamic, calls := e.d.newDynamic, 0
	e.d.newDynamic = func() (dynamic.Interface, error) {
		if calls++; calls > 1 {
			return nil, errors.New("no kubeconfig")
		}
		return newDynamic()
	}
	if _, _, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm")...); err == nil || !strings.Contains(err.Error(), "asking the hub who you are") {
		t.Fatal(err)
	}
}

// TestApproveIntentTrustFromRegistry: with --registry and no --trust an
// intent of a member verifies under the recorder key that member
// advertises, and the decision is signed with no kubeconfig of the member.
func TestApproveIntentTrustFromRegistry(t *testing.T) {
	r, eu, intent, forged := memberIntentEnv(t, true)
	out, errOut, err := r.run(t, "text", "approve", "--intent", intent, "--signers", r.signers, "--registry", eu.repo)
	if err != nil || !strings.HasPrefix(out, "approval signed by alice@acme.example (keyless) on prod/held") {
		t.Fatalf("%v\n%s\n%s", err, out, errOut)
	}
	// An intent the advertised key did not sign does not verify.
	if _, _, err := r.run(t, "text", "approve", "--intent", forged, "--signers", r.signers, "--registry", eu.repo); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatal(err)
	}
	// No hub to read the members from.
	hubDyn := r.d.newDynamic
	r.d.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	if _, _, err := r.run(t, "text", "approve", "--intent", intent, "--signers", r.signers, "--registry", eu.repo); err == nil || !strings.Contains(err.Error(), "no kubeconfig") {
		t.Fatal(err)
	}
	r.d.newDynamic = hubDyn
	// Without --signers the hub's approver signers sign, and they do not
	// accept this login's subject.
	if _, _, err := r.run(t, "text", "approve", "--intent", intent, "--registry", eu.repo); err == nil {
		t.Fatal("signed under the hub's signers with another root")
	}
	eu.noPolicy = true
	if _, _, err := r.run(t, "text", "approve", "--intent", intent, "--registry", eu.repo); err == nil || !strings.Contains(err.Error(), "approver signers could not be read") {
		t.Fatal(err)
	}
	if _, _, err := r.run(t, "text", "approve", "--intent", intent, "--registry", ""); err == nil {
		t.Fatal("verified with no trust")
	}
}

// TestApproveIntentMemberWithoutRecorderKey: a member that advertises no
// recorder key gets MemberRecorderUnknown and nothing signed.
func TestApproveIntentMemberWithoutRecorderKey(t *testing.T) {
	r, eu, intent, _ := memberIntentEnv(t, false)
	logins := r.logins
	if _, _, err := r.run(t, "text", "approve", "--intent", intent, "--signers", r.signers, "--registry", eu.repo); err == nil ||
		!strings.HasPrefix(err.Error(), delivery.RefusalMemberRecorderUnknown+": eu-prod") || r.logins != logins {
		t.Fatal(err)
	}
	if _, _, err := r.run(t, "text", "approve", "--intent", r.host+"/nothing@sha256:"+strings.Repeat("0", 64), "--signers", r.signers, "--registry", eu.repo); err == nil {
		t.Fatal("approved an intent that is not there")
	}
}

// memberIntentEnv is a refEnv whose intent of held sits on member eu-prod's
// Stage stream, signed by the recorder key eu-prod advertises when
// advertise is set, and a hub that approved eu-prod.
func memberIntentEnv(t *testing.T, advertise bool) (*refEnv, *clusterEnv, string, string) {
	t.Helper()
	r := newRefEnv(t, "alice@acme.example")
	hub := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	stage := delivery.RecordStage{Cluster: m.uid, Namespace: "prod", Name: "payments-prod"}
	stream := stage.Stream()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	pem, _ := delivery.PublicKeyPEM(pub)
	hub.publishJoin(t, m, func(j *delivery.SnapshotJoin) {
		j.StreamsRepository, j.Streams = r.host+"/delivery/streams", []delivery.JoinStream{{Service: delivery.ServiceRef{Name: "payments"}, Environment: "production", Stream: stream}}
		if advertise {
			j.RecorderKey = &delivery.TrustedKey{PublicKey: pem}
		}
	})
	if _, errOut, err := hub.approve(t, m); err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	approvals := deliveryrecord.AnchorRef(r.host+"/"+refApprovals, stream)
	if err := ocireferrer.Push(t.Context(), r.repo(refApprovals), deliveryrecord.Anchor(stream)); err != nil {
		t.Fatal(err)
	}
	op := delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "held", UID: "uid-held"},
		Spec: delivery.OperationSpec{IntentDigest: "sha256:i", Actor: delivery.Actor{User: "bob"}}}
	st := delivery.NewIntentRecord(stage, deliveryrecord.Anchor(stream).ManifestDesc.Digest.String(), approvals, op, "2026-11-18T00:00:00Z")
	b, _, err := delivery.SignIntent(t.Context(), edSigner(key), st)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := deliveryrecord.Push(t.Context(), r.repo("delivery/streams"), stream, capabilities.ReferrersNative, delivery.PredicateIntent, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	op.Metadata.UID = "uid-forged"
	fb, _, _ := delivery.SignIntent(t.Context(), edSigner(stranger), delivery.NewIntentRecord(stage, deliveryrecord.Anchor(stream).ManifestDesc.Digest.String(), approvals, op, "2026-11-18T00:00:00Z"))
	forged, err := deliveryrecord.Push(t.Context(), r.repo("delivery/streams"), stream, capabilities.ReferrersNative, delivery.PredicateIntent, fb, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.d.newDynamic = hub.d.newDynamic
	return r, hub, intent, forged
}

// TestProposeMemberKeyChangedDuringLogin: a member whose key changes while
// the person logs in gets nothing signed.
func TestProposeMemberKeyChangedDuringLogin(t *testing.T) {
	eu := newJoinMember(euProd, "eu-prod")
	e := newRequestHub(t, eu)
	e.onLogin = func() { e.publishJoin(t, newJoinMember(euProd, "eu-prod"), nil) }
	if _, _, err := e.delivery(t, memberArgs("propose", memberDigest, "--confirm")...); err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalNotAMember+":") ||
		len(e.requests(t, eu.uid)) != 0 {
		t.Fatal(err)
	}
}

// TestMemberRequestsCarryPolicyPlacement: a placement a signed rule made is
// signed as the policy's, by digest, confirmed by no one.
func TestMemberRequestsCarryPolicyPlacement(t *testing.T) {
	mr := &memberRequests{user: alice, opts: app.MemberPlanOptions{Hub: clusterHubUID}, req: delivery.Request{Service: "billing", Environment: "production", To: memberDigest},
		plan: delivery.MemberPlan{Placement: delivery.MemberPlacementPlan{Source: delivery.PlacementFromPolicy, Policy: "sha256:p"}}}
	reqs := mr.requests([]delivery.ClusterJoinState{{UID: euProd}, {UID: usProd}})
	if len(reqs) != 2 || reqs[1].Placement.Source != delivery.RequestPlacedPolicy || reqs[1].Placement.Policy != "sha256:p" ||
		reqs[1].Placement.ConfirmedBy != "" || len(reqs[0].Placement.Clusters) != 2 || reqs[0].Check() != nil {
		t.Fatalf("%+v", reqs)
	}
}
