package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// TestMemberPlans: one plan per environment placed on a member, the hub's
// environments first, read over one member list; a restricted reader's
// hidden members keep the environment.
func TestMemberPlans(t *testing.T) {
	eu, us := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, eu, us)
	e.publishTaking(t, us, e.clock, "prod", "qa")
	e.ask(t, eu, "alice@acme.com", planDigest)
	plans := e.svc.MemberPlans(t.Context(), "billing", []string{"staging", "production"}, e.planOpts())
	var envs []string
	for _, p := range plans {
		envs = append(envs, p.Environment)
	}
	if strings.Join(envs, ",") != "production,qa" || len(plans[0].Members) != 2 || memberOf(t, plans[0], eu.uid).State != delivery.MemberWaiting ||
		plans[0].OrderUnknown || !plans[1].OrderUnknown {
		t.Fatalf("%v %+v", envs, plans)
	}
	opts := e.planOpts()
	opts.Restrict = func(string, string) bool { return true }
	plans = e.svc.MemberPlans(t.Context(), "billing", nil, opts)
	if len(plans) != 2 || plans[0].Hidden != 2 || len(plans[0].Members) != 0 {
		t.Fatalf("%+v", plans)
	}
}

// TestMemberPlansUnreadIsPartial: members that cannot be read never read as
// a hub with none: the plans say partial and why, and with no plan to say
// it one partial plan does.
func TestMemberPlansUnreadIsPartial(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	e.tagPage.Store(true)
	e.deny.Store(true)
	plans := e.svc.MemberPlans(t.Context(), "billing", []string{"production"}, e.planOpts())
	if len(plans) == 0 || slices.ContainsFunc(plans, func(p delivery.MemberPlan) bool {
		return p.Completeness != fleet.CompletenessPartial || len(p.Notes) == 0
	}) {
		t.Fatalf("%+v", plans)
	}
	plans = e.svc.MemberPlans(t.Context(), "billing", nil, e.planOpts())
	if len(plans) != 1 || plans[0].Completeness != fleet.CompletenessPartial || len(plans[0].Notes) == 0 {
		t.Fatalf("%+v", plans)
	}
}

// refusalsEnv is a hub whose member eu-prod refused one request at intake,
// raised a second one whose gates rejected it, applied a third and holds a
// fourth; us-prod, with requests too, has not answered.
func refusalsEnv(t *testing.T) (*joinEnv, member, member) {
	t.Helper()
	eu, us := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e := newPlanEnv(t, eu, us)
	refused := e.ask(t, eu, "alice@acme.com", planDigest)
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: refused, Cluster: eu.uid, State: delivery.RequestRefused, Code: delivery.RefusalNotAllowedHere,
		Namespace: "billing", Reason: "alice may not create operations in namespace billing", Next: "kubectl create rolebinding x -n billing"})
	rejected := e.ask(t, eu, "alice@acme.com", otherDigest)
	op := &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-1", Stage: "billing-prod"}
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: rejected, Cluster: eu.uid, State: delivery.RequestRaised, Operation: op})
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: rejected, Cluster: eu.uid, State: delivery.RequestConcluded, Outcome: delivery.Rejected,
		Published: "pacto.published: no publisher signature in billing", Reason: "rejected in billing", Operation: op})
	applied := e.ask(t, eu, "alice@acme.com", fmt.Sprintf("sha256:%064x", 3))
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: applied, Cluster: eu.uid, State: delivery.RequestConcluded, Outcome: delivery.Succeeded, Operation: op})
	held := e.ask(t, eu, "alice@acme.com", fmt.Sprintf("sha256:%064x", 4))
	e.answer(t, eu.uid, eu.key, delivery.RequestOutcome{Request: held, Cluster: eu.uid, State: delivery.RequestRaised, Operation: op})
	e.ask(t, us, "alice@acme.com", planDigest)
	return e, eu, us
}

// TestMemberRefusals: the requests a member refused and those its gates
// rejected, never one it applied, holds or has not answered.
func TestMemberRefusals(t *testing.T) {
	e, eu, _ := refusalsEnv(t)
	r := e.svc.MemberRefusals(t.Context(), e.planOpts())
	if len(r.Refusals) != 2 || r.Hidden != 0 || len(r.Unreadable) != 0 {
		t.Fatalf("%+v", r)
	}
	slices.SortFunc(r.Refusals, func(a, b MemberRefusal) int { return strings.Compare(string(a.Outcome.State), string(b.Outcome.State)) })
	concluded, refused := r.Refusals[0], r.Refusals[1]
	if refused.Member.Name != "eu-prod" || refused.Member.UID != eu.uid || refused.Outcome.Code != delivery.RefusalNotAllowedHere || refused.Request.Digest != planDigest {
		t.Errorf("%+v", refused)
	}
	if concluded.Outcome.Outcome != delivery.Rejected || concluded.Outcome.Operation == nil || concluded.Request.Digest != otherDigest {
		t.Errorf("%+v", concluded)
	}
}

// TestMemberRefusalsRestricted: a restricted reader does not read a member
// no live channel reviews for (counted), nor the namespace of an Operation
// that member's review hides from them.
func TestMemberRefusalsRestricted(t *testing.T) {
	e, eu, _ := refusalsEnv(t)
	opts := e.planOpts()
	opts.Restrict = func(_, ns string) bool { return ns != "billing" }
	opts.Reviewable = func(uid string) bool { return uid == eu.uid }
	r := e.svc.MemberRefusals(t.Context(), opts)
	if len(r.Refusals) != 2 || r.Hidden != 1 {
		t.Fatalf("%+v", r)
	}
	for _, x := range r.Refusals {
		if o := x.Outcome; o.Operation != nil || o.Namespace != "" || strings.Contains(o.Reason+o.Published+o.Next, "billing") {
			t.Errorf("namespace kept: %+v", x.Outcome)
		}
	}
	opts.Restrict = func(string, string) bool { return true }
	if r := e.svc.MemberRefusals(t.Context(), opts); len(r.Refusals) != 2 || r.Refusals[0].Outcome.Operation == nil && r.Refusals[1].Outcome.Operation == nil {
		t.Fatalf("readable namespace: %+v", r)
	}
	opts.Reviewable = nil
	if r := e.svc.MemberRefusals(t.Context(), opts); len(r.Refusals) != 0 || r.Hidden != 2 {
		t.Fatalf("no live channel: %+v", r)
	}
}

// TestMemberRefusalsUnread: requests and outcomes that do not verify, an
// outcome listing or a stream that cannot be read, a cut stream, signers
// that cannot be read and a partial member list are each said.
func TestMemberRefusalsUnread(t *testing.T) {
	eu := newMember(memberA, "eu-prod")
	e := newPlanEnv(t, eu)
	kp, _ := signing.FromPrivateKey(eu.key)
	e.pushRequest(t, signing.Keyed(kp, ""), opRequest(eu.uid, "alice@acme.com", planDigest))
	req := e.ask(t, eu, "alice@acme.com", planDigest)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	e.answer(t, eu.uid, stranger, delivery.RequestOutcome{Request: req, Cluster: eu.uid, State: delivery.RequestRefused, Code: "NoStage", Reason: "r", Next: "n"})
	r := e.svc.MemberRefusals(t.Context(), e.planOpts())
	if len(r.Refusals) != 0 || len(r.Unreadable) != 1 || r.Unreadable[0] != "eu-prod: 1 request did not verify under the hub's approver signers and was not read; 1 outcome did not verify and was not read" {
		t.Fatalf("%+v", r)
	}
	anchor := "/referrers/" + deliveryrecord.Anchor(delivery.ClusterRequestStream(eu.uid)).ManifestDesc.Digest.String()
	calls := 0
	outcomes := func(r *http.Request) bool {
		if !strings.Contains(r.URL.Path, anchor) {
			return false
		}
		calls++
		return calls > 1
	}
	e.fail.Store(&outcomes)
	if r := e.svc.MemberRefusals(t.Context(), e.planOpts()); len(r.Unreadable) != 1 || !strings.Contains(r.Unreadable[0], "the outcomes of a request could not be read") {
		t.Fatalf("outcomes: %+v", r)
	}
	all := func(r *http.Request) bool { return strings.Contains(r.URL.Path, anchor) }
	e.fail.Store(&all)
	if r := e.svc.MemberRefusals(t.Context(), e.planOpts()); len(r.Unreadable) != 1 || !strings.HasPrefix(r.Unreadable[0], "eu-prod: its request stream could not be read") {
		t.Fatalf("requests: %+v", r)
	}
	e.fail.Store(nil)
	// Signers read for the members, then not for their requests.
	opts := e.planOpts()
	pd := policyDelivery(e, "", "")
	gets := 0
	pd.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		return gets > 1, nil, errors.New("gone")
	})
	opts.Delivery = pd
	if r := e.svc.MemberRefusals(t.Context(), opts); len(r.Unreadable) != 1 || !strings.HasPrefix(r.Unreadable[0], "the hub's approver signers could not be read") {
		t.Fatalf("signers: %+v", r)
	}
	fewRequestPages(t)
	s := e.keyless(t, "alice@acme.com")
	for i := 0; i <= delivery.MaxOperationRequests; i++ {
		e.pushRequest(t, s, opRequest(eu.uid, "alice@acme.com", fmt.Sprintf("sha256:%064x", i)))
	}
	if r := e.svc.MemberRefusals(t.Context(), e.planOpts()); !strings.Contains(strings.Join(r.Unreadable, ";"), "its stream holds more records than a read takes") {
		t.Fatalf("cut: %+v", r)
	}
	e.tagPage.Store(true)
	e.deny.Store(true)
	if r := e.svc.MemberRefusals(t.Context(), e.planOpts()); len(r.Unreadable) == 0 {
		t.Fatalf("joins: %+v", r)
	}
}
