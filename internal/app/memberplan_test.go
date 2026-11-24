package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	planDigest  = "sha256:5e1f000000000000000000000000000000000000000000000000000000000000"
	otherDigest = "sha256:4d00000000000000000000000000000000000000000000000000000000000000"
)

// newPlanEnv is a hub whose registry holds the approved members ms, each
// taking requests for prod, published at the env's clock (now).
func newPlanEnv(t *testing.T, ms ...member) *joinEnv {
	t.Helper()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	e.clock = time.Now().UTC().Truncate(time.Second)
	for _, m := range ms {
		e.publishTaking(t, m, e.clock, "prod")
		e.decide(t, m, delivery.JoinApprove)
	}
	return e
}

// publishTaking publishes m's snapshot at at, its join block taking
// requests for envs.
func (e *joinEnv) publishTaking(t *testing.T, m member, at time.Time, envs ...string) {
	t.Helper()
	s := ledgerSnapshot(m.uid, at)
	s.Join = m.join()
	s.Join.Requests = &delivery.JoinRequests{Issuers: []string{joinIssuer}, Environments: envs}
	pushAt(t, e.repo, m.key, m.uid, s)
}

// planReq is a member request for billing in production.
func planReq(to string) delivery.Request {
	return delivery.Request{Service: "billing", Environment: "production", To: to}
}

// opRequest is email's request to promote billing to digest on uid.
func opRequest(uid, email, digest string) delivery.OperationRequest {
	at := time.Now().UTC().Truncate(time.Second)
	return delivery.OperationRequest{Cluster: uid, Hub: joinHub, Service: delivery.ServiceRef{Name: "billing"}, Environment: "production",
		Digest: digest, Requester: "oidc:" + email, At: at.Format(time.RFC3339), Expires: at.Add(time.Hour).Format(time.RFC3339),
		Placement: delivery.RequestPlacement{Source: delivery.RequestPlacedAdvertised, ConfirmedBy: "oidc:" + email, Members: []string{uid}}}
}

// keyless is email's keyless signer under e's private Sigstore.
func (e *joinEnv) keyless(t *testing.T, email string) delivery.Signer {
	t.Helper()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token(email, joinIssuer), nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// pushRequest signs r with s, pushes it and returns its manifest digest.
func (e *joinEnv) pushRequest(t *testing.T, s delivery.Signer, r delivery.OperationRequest) string {
	t.Helper()
	b, err := delivery.SignOperationRequest(t.Context(), s, delivery.NewOperationRequestStatement(r))
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	ref, _, err := deliveryrecord.PushOperationRequest(t.Context(), repo, b)
	if err != nil {
		t.Fatal(err)
	}
	_, dg, _ := strings.Cut(ref, "@")
	return dg
}

// ask pushes email's request on m for digest and returns its manifest.
func (e *joinEnv) ask(t *testing.T, m member, email, digest string) string {
	t.Helper()
	return e.pushRequest(t, e.keyless(t, email), opRequest(m.uid, email, digest))
}

// answer pushes o, signed by key, on stream uid keyed by o.Request.
func (e *joinEnv) answer(t *testing.T, uid string, key ed25519.PrivateKey, o delivery.RequestOutcome) {
	t.Helper()
	kp, _ := signing.FromPrivateKey(key)
	o.At = time.Now().UTC().Format(time.RFC3339)
	b, err := delivery.SignRequestOutcome(t.Context(), signing.Keyed(kp, ""), delivery.NewRequestOutcomeStatement(o))
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	if _, err := deliveryrecord.Push(t.Context(), repo, delivery.ClusterRequestStream(uid), capabilities.ReferrersNative,
		delivery.PredicateRequestOutcome, b, map[string]string{deliveryrecord.AnnUID: o.Request}); err != nil {
		t.Fatal(err)
	}
}

func raisedOp() *delivery.RequestedOperation {
	return &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-0123456789"}
}

func (e *joinEnv) planOpts() MemberPlanOptions {
	return MemberPlanOptions{Repository: e.repo, Hub: joinHub, Authorize: e.authorize, Delivery: &Delivery{Policy: e.policy}}
}

func (e *joinEnv) memberPlan(t *testing.T, req delivery.Request, opts MemberPlanOptions) delivery.MemberPlan {
	t.Helper()
	p, err := e.svc.MemberPlan(t.Context(), req, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func memberOf(t *testing.T, p delivery.MemberPlan, uid string) delivery.MemberState {
	t.Helper()
	for _, m := range p.Members {
		if m.UID == uid {
			return m
		}
	}
	t.Fatalf("no member %s in %+v", uid, p)
	return delivery.MemberState{}
}

// stateCase is a member in one state, and the records that put it there.
type stateCase struct {
	name, state, reason, next, op string
	outcome                       *delivery.RequestOutcome
	asked                         bool
}

// stateCases are one member per state.
func stateCases() []stateCase {
	return []stateCase{
		{name: "no request", state: delivery.MemberNoRequest},
		{name: "waiting", state: delivery.MemberWaiting, reason: "no answer yet", asked: true},
		{name: "refused", state: delivery.MemberRefused, reason: "NotAllowedHere: alice may not", next: "kubectl create rolebinding", asked: true,
			outcome: &delivery.RequestOutcome{State: delivery.RequestRefused, Code: delivery.RefusalNotAllowedHere, Reason: "alice may not", Next: "kubectl create rolebinding"}},
		{name: "raised", state: delivery.MemberRaised, op: "billing/billing-prod-req-0123456789", asked: true,
			outcome: &delivery.RequestOutcome{State: delivery.RequestRaised, Operation: raisedOp(), Reason: "this member keeps no intent records"}},
		{name: "held", state: delivery.MemberHeld, next: "pacto delivery approve --intent zot/acme/intents@sha256:1", asked: true,
			outcome: &delivery.RequestOutcome{State: delivery.RequestRaised, Operation: &delivery.RequestedOperation{Namespace: "billing", Name: "n", IntentRecord: "zot/acme/intents@sha256:1"}}},
		{name: "applied", state: delivery.MemberApplied, reason: "Succeeded", asked: true,
			outcome: &delivery.RequestOutcome{State: delivery.RequestConcluded, Operation: raisedOp(), Outcome: delivery.Succeeded}},
		{name: "failed", state: delivery.MemberFailed, reason: "Failed: the applier", asked: true,
			outcome: &delivery.RequestOutcome{State: delivery.RequestConcluded, Operation: raisedOp(), Outcome: delivery.Failed, Reason: "the applier"}},
		{name: "concluded", state: delivery.MemberConcluded, reason: "Expired", asked: true,
			outcome: &delivery.RequestOutcome{State: delivery.RequestConcluded, Operation: raisedOp(), Outcome: delivery.Expired}},
	}
}

// checkState fails unless got is what c says.
func checkState(t *testing.T, c stateCase, got delivery.MemberState) {
	t.Helper()
	ok := got.State == c.state && strings.Contains(got.Reason, c.reason) && strings.HasPrefix(got.Next, c.next) && got.AsOf != ""
	if !ok || (c.op != "" && got.Operation != c.op) || (c.asked && got.Digest != planDigest) {
		t.Errorf("%s: %+v", c.name, got)
	}
}

// TestMemberPlanStates: one member per state, each from signed records.
func TestMemberPlanStates(t *testing.T) {
	cases := stateCases()
	var ms []member
	for i := range cases {
		ms = append(ms, newMember(fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1), "m"+fmt.Sprint(i)+"-prod"))
	}
	e := newPlanEnv(t, ms...)
	var uids []string
	for _, m := range ms {
		uids = append(uids, m.uid)
	}
	alice := e.keyless(t, "alice@acme.com")
	for i, c := range cases {
		if !c.asked {
			continue
		}
		r := opRequest(ms[i].uid, "alice@acme.com", planDigest)
		r.Placement.Members = uids // one propose confirmed them all
		req := e.pushRequest(t, alice, r)
		if c.outcome != nil {
			o := *c.outcome
			o.Request, o.Cluster = req, ms[i].uid
			e.answer(t, ms[i].uid, ms[i].key, o)
		}
	}
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if p.Completeness != fleet.CompletenessComplete || len(p.Members) != len(cases) || p.Placement.Source != delivery.RequestPlacedAdvertised ||
		p.Placement.ConfirmedBy != "oidc:alice@acme.com" || !strings.HasPrefix(p.Next, "pacto delivery propose --service billing") {
		t.Fatalf("%+v", p)
	}
	for i, c := range cases {
		checkState(t, c, memberOf(t, p, ms[i].uid))
	}
}

// TestMemberPlanNoTarget: with no digest the newest request for the
// service and environment is read, whatever it asked for, and the confirm
// command is offered with the digest to fill in.
func TestMemberPlanNoTarget(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	e.ask(t, eu, "alice@acme.com", otherDigest)
	time.Sleep(1100 * time.Millisecond) // a later signing time
	e.ask(t, eu, "bob@acme.com", planDigest)
	p := e.memberPlan(t, planReq(""), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberWaiting || got.Digest != planDigest || !strings.Contains(p.Next, "--to '<sha256 digest>'") && !strings.Contains(p.Next, "--to <sha256 digest>") ||
		p.Placement.ConfirmedBy != "oidc:bob@acme.com" {
		t.Fatalf("%+v %+v", got, p)
	}
}

// TestMemberPlanDigestRequired: member mode refuses a version.
func TestMemberPlanDigestRequired(t *testing.T) {
	e := newPlanEnv(t)
	if _, err := e.svc.MemberPlan(t.Context(), planReq("1.5.0"), e.planOpts()); err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalDigestRequired+":") {
		t.Fatal(err)
	}
}

// TestMemberPlanForgedReadsRefused: a request the member's pacto.published
// gate rejected reads refused, with the gate's reason and the Operation.
func TestMemberPlanForgedReadsRefused(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	req := e.ask(t, eu, "alice@acme.com", planDigest)
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: req, Cluster: eu.uid, State: delivery.RequestConcluded, Operation: raisedOp(),
		Outcome: delivery.Rejected, Reason: "Rejected", Published: "pacto.published: no publisher signature for billing"})
	got := memberOf(t, e.memberPlan(t, planReq(planDigest), e.planOpts()), eu.uid)
	if got.State != delivery.MemberRefused || got.Reason != "Rejected: pacto.published: no publisher signature for billing" || got.Operation != "billing/billing-prod-req-0123456789" {
		t.Fatalf("%+v", got)
	}
}

// TestMemberPlanStaleMemberPartial: a stale member stays placed, reads
// stale and makes the answer partial.
func TestMemberPlanStaleMemberPartial(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t)
	e.publishTaking(t, eu, e.clock.Add(-2*delivery.TargetSnapshotFreshness), "prod")
	e.decide(t, eu, delivery.JoinApprove)
	e.ask(t, eu, "alice@acme.com", planDigest)
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	got := memberOf(t, p, eu.uid)
	if !got.Stale || !p.Placement.Placed[0].Stale || got.Reason != "stale: its request waits until it reads the registry" || p.Completeness != fleet.CompletenessPartial ||
		!slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasPrefix(n, "eu-prod is stale") }) {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanWaitingBecomesUnknown: a request unanswered past freshness
// reads unknown and the answer partial.
func TestMemberPlanWaitingBecomesUnknown(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	e.ask(t, eu, "alice@acme.com", planDigest)
	e.clock = e.clock.Add(2 * delivery.TargetSnapshotFreshness)
	e.publishTaking(t, eu, e.clock, "prod")
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberUnknown || !strings.HasSuffix(got.Reason, "no answer") || p.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanUnverifiedOutcomeCounted: an outcome no approved key signed
// is not read, and is counted.
func TestMemberPlanUnverifiedOutcomeCounted(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	req := e.ask(t, eu, "alice@acme.com", planDigest)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	e.answer(t, eu.uid, stranger, delivery.RequestOutcome{Request: req, Cluster: eu.uid, State: delivery.RequestConcluded, Operation: raisedOp(), Outcome: delivery.Succeeded})
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberWaiting || p.Completeness != fleet.CompletenessPartial ||
		!slices.Contains(p.Notes, "1 outcome from eu-prod did not verify and was not read") {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanOutcomeSignedByOtherMemberIgnored: member B's key on A's
// stream, and an outcome naming B pushed on A's stream, are ignored and
// counted.
func TestMemberPlanOutcomeSignedByOtherMemberIgnored(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, a, b)
	req := e.ask(t, a, "alice@acme.com", planDigest)
	e.answer(t, a.uid, b.key, delivery.RequestOutcome{Request: req, Cluster: a.uid, State: delivery.RequestConcluded, Operation: raisedOp(), Outcome: delivery.Succeeded})
	e.answer(t, a.uid, b.key, delivery.RequestOutcome{Request: req, Cluster: b.uid, State: delivery.RequestConcluded, Operation: raisedOp(), Outcome: delivery.Succeeded})
	// An outcome the member signed for another request is not this one's.
	e.answer(t, a.uid, a.key, delivery.RequestOutcome{Request: otherDigest, Cluster: a.uid, State: delivery.RequestRefused, Code: "X", Next: "y"})
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, a.uid); got.State != delivery.MemberWaiting || !slices.Contains(p.Notes, "2 outcomes from eu-prod did not verify and were not read") {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlacementUnverifiedRequestNotConfirmation: a request signed with
// a key, by someone the hub does not accept, or by one person naming
// another never confirms the placement nor reads waiting.
func TestMemberPlacementUnverifiedRequestNotConfirmation(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	kp, _ := signing.FromPrivateKey(eu.key)
	e.pushRequest(t, signing.Keyed(kp, ""), opRequest(eu.uid, "alice@acme.com", planDigest))
	e.pushRequest(t, e.keyless(t, "mallory@evil.example"), opRequest(eu.uid, "mallory@evil.example", planDigest))
	e.pushRequest(t, e.keyless(t, "bob@acme.com"), opRequest(eu.uid, "alice@acme.com", planDigest))
	e.pushRequest(t, e.keyless(t, "build@proj.iam.gserviceaccount.com"), opRequest(eu.uid, "build@proj.iam.gserviceaccount.com", planDigest))
	other := opRequest(memberB, "alice@acme.com", planDigest) // names another member, pushed on eu's stream
	b, _ := delivery.SignOperationRequest(t.Context(), e.keyless(t, "alice@acme.com"), delivery.NewOperationRequestStatement(other))
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	if _, err := deliveryrecord.Push(t.Context(), repo, delivery.ClusterRequestStream(eu.uid), capabilities.ReferrersNative, delivery.PredicateOperationRequest, b, nil); err != nil {
		t.Fatal(err)
	}
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberNoRequest || p.Placement.Source != delivery.PlacementUnconfirmed || p.Placement.ConfirmedBy != "" ||
		!slices.Contains(p.Notes, "5 requests on the stream of eu-prod did not verify under the hub's approver signers and were not read") ||
		p.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v", p)
	}
	// The hub rejects none: its signers may change, so every read counts
	// them again and stays partial.
	if p := e.memberPlan(t, planReq(planDigest), e.planOpts()); len(p.Notes) != 1 || p.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v", p.Notes)
	}
}

// TestMemberPlanNoLiveChannelHiddenForRestricted: a restricted reader does
// not see a member no live channel reviews for (D5), counted; the
// unauthenticated reader sees every one.
func TestMemberPlanNoLiveChannelHiddenForRestricted(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	c := newMember("cccc0000-0000-4000-8000-00000000000c", "ap-prod")
	e := newPlanEnv(t, a, b)
	e.publish(t, c, c.key, e.clock) // takes no requests: unplaced
	e.decide(t, c, delivery.JoinApprove)
	open := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if len(open.Members) != 2 || open.Hidden != 0 || len(open.Placement.Unplaced) != 1 {
		t.Fatalf("%+v", open)
	}
	opts := e.planOpts()
	opts.Restrict = func(string, string) bool { return true }
	opts.Reviewable = func(uid string) bool { return uid == a.uid }
	p := e.memberPlan(t, planReq(planDigest), opts)
	if len(p.Members) != 1 || p.Members[0].UID != a.uid || len(p.Placement.Placed) != 1 || !p.Placement.Placed[0].Reviewable ||
		len(p.Placement.Unplaced) != 0 || p.Hidden != 2 || p.HiddenReason == "" {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanNamespaceHiddenForRestricted: a reader who may not read the
// Operation's namespace on that member keeps the member but not the
// namespace or the Operation.
func TestMemberPlanNamespaceHiddenForRestricted(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	req := e.ask(t, eu, "alice@acme.com", planDigest)
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: req, Cluster: eu.uid, State: delivery.RequestRaised,
		Operation: &delivery.RequestedOperation{Namespace: "billing", Name: "n", IntentRecord: "r@sha256:1"}, Reason: "raised in billing"})
	opts := e.planOpts()
	opts.Restrict = func(cluster, ns string) bool { return cluster == eu.uid && ns != "billing" }
	opts.Reviewable = func(string) bool { return true }
	got := memberOf(t, e.memberPlan(t, planReq(planDigest), opts), eu.uid)
	if got.State != delivery.MemberHeld || got.Operation != "" || got.Next != "" || strings.Contains(got.Reason, "billing") {
		t.Fatalf("%+v", got)
	}
}

// policyDelivery is a hub Delivery whose published identities name policy
// signers and a delivery policy at policy.
func policyDelivery(e *joinEnv, policy, signers string) *Delivery {
	pol, _ := json.Marshal(e.policy)
	pubs, _ := json.Marshal(delivery.PublisherConfig{Policy: policy, PolicySigners: signers})
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.PolicyConfigMap},
		"data":     map[string]any{delivery.PolicyKey: string(pol), delivery.PublishersKey: string(pubs)}}}
	return &Delivery{Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm), PolicyNamespace: delivery.PolicyNamespace}
}

// signPlacement pushes a placement policy of rules signed by key to
// host/policy:placement and returns its digest.
func signPlacement(t *testing.T, reg *provRegistry, key ed25519.PrivateKey, rules ...delivery.PlacementRule) string {
	t.Helper()
	payload, _ := json.Marshal(delivery.PlacementStatement{Type: delivery.StatementType, Subject: []delivery.Subject{},
		PredicateType: delivery.PredicatePlacement, Predicate: delivery.PlacementPolicy{Rules: rules, At: "2026-11-23T00:00:00Z"}})
	bundle, err := edSigner(key).Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := mutate.AppendLayers(empty.Image, static.NewLayer(bundle, types.MediaType(delivery.BundleMediaType)))
	d, err := reg.client.Write(t.Context(), reg.host+"/policy", mutate.MediaType(img, types.OCIManifestSchema1))
	if err == nil {
		err = reg.client.Tag(t.Context(), reg.host+"/policy", d, delivery.PlacementPolicyTag)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestMemberPlanPolicyPlaces: a signed placement rule naming a member by
// uid places on it alone, with the policy's digest.
func TestMemberPlanPolicyPlaces(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, a, b)
	reg := provenanceTestRegistry(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	d := signPlacement(t, reg, key, delivery.PlacementRule{Name: "eu", Environments: []string{"production"}, Namespace: "billing", ClusterUIDs: []string{a.uid}})
	opts := e.planOpts()
	opts.Delivery = policyDelivery(e, reg.host+"/policy:v1", policySigners(t, pub))
	p := e.memberPlan(t, planReq(planDigest), opts)
	if p.Placement.Source != delivery.PlacementFromPolicy || p.Placement.Policy != d || len(p.Members) != 1 || p.Members[0].UID != a.uid || p.Completeness != fleet.CompletenessComplete {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanPolicyUnreadablePartial: a placement policy that cannot be
// read or verified places from what the members take, partial.
func TestMemberPlanPolicyUnreadablePartial(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	opts := e.planOpts()
	opts.Delivery = policyDelivery(e, "127.0.0.1:1/policy:v1", "")
	p := e.memberPlan(t, planReq(planDigest), opts)
	if p.Completeness != fleet.CompletenessPartial || p.Placement.Source != delivery.PlacementUnconfirmed || len(p.Members) != 1 || p.Next != "" ||
		!slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasPrefix(n, "the placement policy could not be verified") }) {
		t.Fatalf("an unverified policy offers nothing to confirm: %+v", p)
	}
}

// TestMemberPlanUnreadable: the hub's signers or a member's stream that
// cannot be read, an outcome listing that fails and a partial join list
// make the answer partial and say why.
func TestMemberPlanUnreadable(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	e.ask(t, eu, "alice@acme.com", planDigest)
	opts := e.planOpts()
	opts.Delivery = &Delivery{Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), PolicyNamespace: delivery.PolicyNamespace}
	if p := e.memberPlan(t, planReq(planDigest), opts); p.Completeness != fleet.CompletenessPartial || len(p.Members) != 0 {
		t.Fatalf("no signers: %+v", p)
	}
	// Signers read for the members, then not for their requests.
	pd := policyDelivery(e, "", "")
	gets := 0
	pd.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		return gets > 1, nil, errors.New("gone")
	})
	opts.Delivery = pd
	if p := e.memberPlan(t, planReq(planDigest), opts); p.Completeness != fleet.CompletenessPartial || memberOf(t, p, eu.uid).State != delivery.MemberNoRequest ||
		!slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasPrefix(n, "the hub's approver signers could not be read") }) {
		t.Fatalf("signers gone: %+v", p)
	}
	anchor := "/referrers/" + deliveryrecord.Anchor(delivery.ClusterRequestStream(eu.uid)).ManifestDesc.Digest.String()
	calls := 0
	fail := func(r *http.Request) bool {
		if !strings.Contains(r.URL.Path, anchor) {
			return false
		}
		calls++
		return calls > 1 // the requests read, their outcomes do not
	}
	e.fail.Store(&fail)
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberUnknown || p.Completeness != fleet.CompletenessPartial {
		t.Fatalf("outcomes: %+v", p)
	}
	all := func(r *http.Request) bool { return strings.Contains(r.URL.Path, anchor) }
	e.fail.Store(&all)
	p = e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberUnknown || !strings.HasPrefix(got.Reason, "its request stream could not be read") ||
		!slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasPrefix(n, "the request stream of eu-prod could not be read") }) {
		t.Fatalf("requests: %+v", p)
	}
	e.fail.Store(nil)
	e.tagPage.Store(true)
	e.deny.Store(true)
	if p := e.memberPlan(t, planReq(planDigest), e.planOpts()); p.Completeness != fleet.CompletenessPartial || len(p.Notes) == 0 {
		t.Fatalf("joins: %+v", p)
	}
}

// fewRequestPages lowers how many bounds a hub read takes, for t.
func fewRequestPages(t *testing.T) {
	t.Helper()
	maxRequestPages = 1
	t.Cleanup(func() { maxRequestPages = 10 })
}

// TestMemberPlanStreamCut: a stream holding more requests than a read
// takes is partial.
func TestMemberPlanStreamCut(t *testing.T) {
	fewRequestPages(t)
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	s := e.keyless(t, "alice@acme.com")
	for i := 0; i <= delivery.MaxOperationRequests; i++ {
		e.pushRequest(t, s, opRequest(eu.uid, "alice@acme.com", fmt.Sprintf("sha256:%064x", i)))
	}
	if p := e.memberPlan(t, planReq(planDigest), e.planOpts()); p.Completeness != fleet.CompletenessPartial ||
		!slices.Contains(p.Notes, "the stream of eu-prod holds more records than a read takes, so newer ones may be missing") {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanReadsPastTheBound: more requests on a stream than one bound
// takes are read a bound at a time, so the newest request is read however
// many older ones sit before it.
func TestMemberPlanReadsPastTheBound(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	s := e.keyless(t, "alice@acme.com")
	for i := 0; i <= delivery.MaxOperationRequests+5; i++ {
		e.pushRequest(t, s, opRequest(eu.uid, "alice@acme.com", fmt.Sprintf("sha256:%064x", i+10)))
	}
	e.ask(t, eu, "bob@acme.com", planDigest)
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberWaiting || got.Digest != planDigest || p.Completeness != fleet.CompletenessComplete {
		t.Fatalf("%+v %+v", got, p)
	}
}

// TestMemberPlanSynonymEnvironment: a request signed for prod is the
// request of a plan for production, as the member reads it.
func TestMemberPlanSynonymEnvironment(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	r := opRequest(eu.uid, "alice@acme.com", planDigest)
	r.Environment = "prod"
	e.pushRequest(t, e.keyless(t, "alice@acme.com"), r)
	p := e.memberPlan(t, planReq(planDigest), e.planOpts())
	if got := memberOf(t, p, eu.uid); got.State != delivery.MemberWaiting || p.Placement.ConfirmedBy != "oidc:alice@acme.com" {
		t.Fatalf("%+v %+v", got, p)
	}
}

// TestMemberPlanNarrowsToMembers: a plan for some members answers for them
// alone; a uid that is no approved member is refused, and a named member
// the plan does not place leaves nothing to confirm.
func TestMemberPlanNarrowsToMembers(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	c := newMember("cccc0000-0000-4000-8000-00000000000c", "ap-prod")
	e := newPlanEnv(t, a, b)
	e.publishTaking(t, c, e.clock, "prod")
	req := planReq(planDigest)
	req.Members = []string{a.uid}
	p := e.memberPlan(t, req, e.planOpts())
	if len(p.Members) != 1 || p.Members[0].UID != a.uid || len(p.Placement.Placed) != 1 || !strings.Contains(p.Next, "--member "+a.uid) {
		t.Fatalf("%+v", p)
	}
	for _, uid := range []string{"dddd0000-0000-4000-8000-00000000000d", c.uid} {
		req.Members = []string{uid}
		if _, err := e.svc.MemberPlan(t.Context(), req, e.planOpts()); err == nil || !strings.HasPrefix(err.Error(), delivery.RefusalNotAMember+":") {
			t.Errorf("%s: %v", uid, err)
		}
	}
	e.publish(t, b, b.key, e.clock) // takes no requests now: unplaced
	req.Members = []string{a.uid, b.uid}
	if p := e.memberPlan(t, req, e.planOpts()); len(p.Placement.Placed) != 1 || p.Next != "" {
		t.Fatalf("%+v", p)
	}
}

// TestMemberPlanRefusalNamespaceHiddenForRestricted: an intake refusal
// carries no Operation, yet its reason and next command name the Stage's
// namespace: a reader whose review of the member hides that namespace
// reads neither, and an AmbiguousStage reason, which lists namespaces, is
// replaced.
func TestMemberPlanRefusalNamespaceHiddenForRestricted(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, a, b)
	ra, rb := e.ask(t, a, "alice@acme.com", planDigest), e.ask(t, b, "alice@acme.com", planDigest)
	e.answer(t, a.uid, a.key, delivery.RequestOutcome{Request: ra, Cluster: a.uid, State: delivery.RequestRefused, Code: delivery.RefusalNotAllowedHere,
		Namespace: "payments-secure", Reason: "alice may not create operations in namespace payments-secure on this member", Next: "kubectl create rolebinding x -n payments-secure"})
	e.answer(t, b.uid, b.key, delivery.RequestOutcome{Request: rb, Cluster: b.uid, State: delivery.RequestRefused, Code: delivery.RefusalAmbiguousStage,
		Reason: "2 Stages of billing in production: team-a/billing, secret/billing", Next: "keep one Stage"})
	opts := e.planOpts()
	opts.Restrict = func(_, ns string) bool { return ns == "team-a" }
	opts.Reviewable = func(string) bool { return true }
	p := e.memberPlan(t, planReq(planDigest), opts)
	if got := memberOf(t, p, a.uid); got.State != delivery.MemberRefused || got.Next != "" || strings.Contains(got.Reason, "payments-secure") {
		t.Errorf("%+v", got)
	}
	if got := memberOf(t, p, b.uid); got.State != delivery.MemberRefused || strings.Contains(got.Reason, "secret") || got.Next != "keep one Stage" {
		t.Errorf("%+v", got)
	}
}

// TestVerifiedPlacementAbsentIsNone: no delivery policy, or none at the
// placement tag, is no placement policy and no error.
func TestVerifiedPlacementAbsentIsNone(t *testing.T) {
	reg := provenanceTestRegistry(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	for _, cfg := range []delivery.PublisherConfig{{}, {Policy: reg.host + "/policy:v1", PolicySigners: policySigners(t, pub)}} {
		if p, d, err := verifiedPlacement(t.Context(), reg.client, cfg); p != nil || d != "" || err != nil {
			t.Fatalf("%+v: %+v %q %v", cfg, p, d, err)
		}
	}
}

// TestVerifiedPlacementUnverifiedErrors: a placement policy no policy key
// signed, bad policy signers, a bad reference and a layer that cannot be
// read are errors.
func TestVerifiedPlacementUnverifiedErrors(t *testing.T) {
	reg := provenanceTestRegistry(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	signPlacement(t, reg, stranger, delivery.PlacementRule{Name: "r", Environments: []string{"production"}, Namespace: "billing"})
	signers := policySigners(t, pub)
	for name, cfg := range map[string]delivery.PublisherConfig{
		"unsigned":    {Policy: reg.host + "/policy:v1", PolicySigners: signers},
		"bad signers": {Policy: reg.host + "/policy:v1", PolicySigners: "keys: nope"},
		"bad ref":     {Policy: "UPPER/policy:v1", PolicySigners: signers},
	} {
		if p, _, err := verifiedPlacement(t.Context(), reg.client, cfg); p != nil || err == nil {
			t.Errorf("%s: %+v %v", name, p, err)
		}
	}
	blobs := func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/blobs/") }
	reg.fail.Store(&blobs)
	if _, _, err := verifiedPlacement(t.Context(), reg.client, delivery.PublisherConfig{Policy: reg.host + "/policy:v1", PolicySigners: signers}); err == nil {
		t.Fatal("an unreadable layer read")
	}
}

// TestWriteOperationRequestsOnePerMember: one signed request per member,
// each on its own stream.
func TestWriteOperationRequestsOnePerMember(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, a, b)
	refs, mode, err := e.svc.WriteOperationRequests(t.Context(), []delivery.OperationRequest{opRequest(a.uid, "alice@acme.com", planDigest),
		opRequest(b.uid, "alice@acme.com", planDigest)}, e.keyless(t, "alice@acme.com"), e.planOpts())
	if err != nil || len(refs) != 2 || mode != capabilities.ReferrersNative {
		t.Fatalf("%v %s %v", refs, mode, err)
	}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	for _, m := range []member{a, b} {
		if recs, _, err := (&deliveryrecord.Reader{}).ListOperationRequests(t.Context(), repo, m.uid); err != nil || len(recs) != 1 {
			t.Fatalf("%s: %d %v", m.name, len(recs), err)
		}
	}
	if got := memberOf(t, e.memberPlan(t, planReq(planDigest), e.planOpts()), a.uid); got.State != delivery.MemberWaiting {
		t.Fatalf("%+v", got)
	}
}

type downSigner struct{}

func (downSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("down")
}

// TestWriteOperationRequestsRefusesChangedKey: a member whose key changed
// since its approval, one that takes no requests, one that is no member
// and a request Check refuses get nothing signed; a signer or push that
// fails is an error.
func TestWriteOperationRequestsRefusesChangedKey(t *testing.T) {
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, a)
	e.publish(t, b, b.key, e.clock)
	e.decide(t, b, delivery.JoinApprove)
	rotated := newMember(memberA, "eu-prod")
	e.publishTaking(t, rotated, e.clock, "prod")
	bad := opRequest(b.uid, "alice@acme.com", planDigest)
	bad.Hub = ""
	for want, r := range map[string]delivery.OperationRequest{
		delivery.RefusalNotAMember + ": eu-prod":              opRequest(a.uid, "alice@acme.com", planDigest),
		delivery.RefusalMemberTakesNoRequests + ": us-prod":   opRequest(b.uid, "alice@acme.com", planDigest),
		delivery.RefusalNotAMember + ": no approved member c": opRequest("cccc0000-0000-4000-8000-00000000000c", "alice@acme.com", planDigest),
		"no hub": bad,
	} {
		if _, _, err := e.svc.WriteOperationRequests(t.Context(), []delivery.OperationRequest{r}, e.keyless(t, "alice@acme.com"), e.planOpts()); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	if recs, _, _ := (&deliveryrecord.Reader{}).ListOperationRequests(t.Context(), repo, a.uid); len(recs) != 0 {
		t.Fatal("a refused request was written")
	}
	c := newMember("cccc0000-0000-4000-8000-00000000000c", "ap-prod")
	e.publishTaking(t, c, e.clock, "prod")
	e.decide(t, c, delivery.JoinApprove)
	ok := []delivery.OperationRequest{opRequest(c.uid, "alice@acme.com", planDigest)}
	if _, _, err := e.svc.WriteOperationRequests(t.Context(), ok, downSigner{}, e.planOpts()); err == nil {
		t.Fatal("a failing signer wrote")
	}
	put := func(r *http.Request) bool { return r.Method == http.MethodPut }
	e.fail.Store(&put)
	if _, _, err := e.svc.WriteOperationRequests(t.Context(), ok, e.keyless(t, "alice@acme.com"), e.planOpts()); err == nil {
		t.Fatal("a failing push wrote")
	}
}

// TestMemberTrust: the approved members' recorder keys, scoped to their
// streams.
func TestMemberTrust(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t)
	s := ledgerSnapshot(eu.uid, e.clock)
	s.Join = eu.join()
	pem, _ := delivery.PublicKeyPEM(eu.key.Public().(ed25519.PublicKey))
	s.Join.RecorderKey = &delivery.TrustedKey{PublicKey: pem}
	s.Join.StreamsRepository, s.Join.Streams = "zot/acme/streams", []delivery.JoinStream{{Service: delivery.ServiceRef{Name: "billing"}, Environment: "staging", Stream: eu.uid + "/billing/billing-staging"}}
	pushAt(t, e.repo, eu.key, eu.uid, s)
	e.decide(t, eu, delivery.JoinApprove)
	trust, states := e.svc.MemberTrust(t.Context(), e.planOpts())
	if len(states) != 1 || len(trust.Keys) != 1 || trust.Keys[0].Streams[0] != eu.uid+"/billing/billing-staging" {
		t.Fatalf("%+v %+v", trust, states)
	}
}

// TestPlanStageModeUnchanged: a request naming a Stage plans as before and
// carries no member plan.
func TestPlanStageModeUnchanged(t *testing.T) {
	req := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Reason: "ship", OnBehalfOf: "alice"}
	d, _, _ := deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"pacto:agents"}, nil)
	plan, _, err := d.Plan(context.Background(), req)
	if err != nil || plan.Members != nil || req.MemberMode() || plan.To == "" {
		t.Fatalf("%+v %v", plan, err)
	}
}
