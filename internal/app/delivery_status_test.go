package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// statusStage is a Stage of service in env, fed by the Stage upNS/upName
// when one is named.
func statusStage(ns, name, service, env, upNS, upName string, status map[string]any) runtime.Object {
	spec := map[string]any{"service": map[string]any{"name": service}, "environment": env, "applyRef": map[string]any{"name": service}}
	if upName != "" {
		spec["candidates"] = map[string]any{"upstreamStage": map[string]any{"name": upName, "namespace": upNS}}
	}
	if status == nil {
		status = map[string]any{}
	}
	return obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", ns, name, map[string]any{"spec": spec, "status": status})
}

// statusFixture is the delivery fixture for alice, whose RBAC lets her
// create Operations everywhere.
func statusFixture(t *testing.T, objs ...runtime.Object) (*Delivery, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	d, c, reg := deliveryFixture(t, "alice", nil, nil, objs...)
	// Status describes digests at once: the fixture's registry records the
	// hosts it was opened with, which this test never reads.
	d.Registry = func(...string) DeliveryRegistry { return reg }
	allow(c, true, nil)
	return d, c
}

// allow answers every SelfSubjectAccessReview with ok, or fails it with err.
func allow(c *dynamicfake.FakeDynamicClient, ok bool, err error) {
	c.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		if err != nil {
			return true, nil, err
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": ok}}}, nil
	})
}

func forbid(resource string) error {
	return apierrors.NewForbidden(gvrStages.GroupResource(), "", errors.New("no "+resource))
}

// offer is the row's offer for action, or the zero offer.
func offer(row delivery.StageRow, action string) delivery.Offer {
	if i := slices.IndexFunc(row.Offers, func(o delivery.Offer) bool { return o.Action == action }); i >= 0 {
		return row.Offers[i]
	}
	return delivery.Offer{}
}

func envsOf(s *delivery.ServiceStatus) string {
	var envs []string
	for _, r := range s.Stages {
		envs = append(envs, r.Environment)
	}
	return strings.Join(envs, ",")
}

func TestStatusNeedsService(t *testing.T) {
	d, _ := statusFixture(t)
	_, err := d.Status(context.Background(), delivery.StatusQuery{})
	wantErrContaining(t, err, "name the service: pacto delivery status --service <name>")
}

// TestStatusRowsAndOffers: the shared row per Stage, with the version and
// contract it runs, offers narrowed by RBAC and where its candidates come
// from, complete when every namespace was read.
func TestStatusRowsAndOffers(t *testing.T) {
	d, _ := statusFixture(t)
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if err != nil || len(s.Stages) != 1 || s.Completeness != "complete" || s.AsOf == "" || s.Service != "payments" {
		t.Fatalf("status = %+v, %v", s, err)
	}
	r := s.Stages[0]
	if r.Stage != "payments-prod" || r.Version != "1.0.0" || !strings.HasSuffix(r.ContractRef, dlC) || r.State != "verified" || r.Held != 0 {
		t.Fatalf("row = %+v", r)
	}
	if o := offer(r, "freeze"); !o.Available {
		t.Fatalf("freeze is offered with a service: %+v", r.Offers)
	}
	if o := offer(r, "undo"); o.Action != "undo" || o.Available {
		t.Fatalf("no change put the running version in place: %+v", r.Offers)
	}
}

// TestStatusUpstreamAndNote: each row names where its candidates come from,
// the note says the order, and an unknown caller is an error.
func TestStatusUpstreamAndNote(t *testing.T) {
	d, c := statusFixture(t)
	s, _ := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	r := s.Stages[0]
	if u := r.Upstream; u == nil || u.Kind != "stage" || u.Namespace != "test" || u.Problem != "" {
		t.Fatalf("upstream = %+v", r.Upstream)
	}
	if !strings.HasPrefix(s.Note, "the Stages of payments you can see, in promotion order, from each Stage's spec.candidates") {
		t.Fatalf("note = %q", s.Note)
	}
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("who")
	})
	if _, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"}); err == nil {
		t.Fatal("want the identity error")
	}
}

// TestStatusNarrowedByRBAC: where RBAC refuses or the review fails, every
// available offer becomes unavailable with the reason.
func TestStatusNarrowedByRBAC(t *testing.T) {
	d, c := statusFixture(t)
	allow(c, false, nil)
	s, _ := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if o := offer(s.Stages[0], "freeze"); o.Available || !strings.Contains(o.Reason, "RBAC does not let you create Operations in prod") {
		t.Fatalf("freeze under RBAC refusal = %+v", o)
	}
	allow(c, true, errors.New("down"))
	s, _ = d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if o := offer(s.Stages[0], "freeze"); o.Available || !strings.Contains(o.Reason, "could not ask the apiserver") {
		t.Fatalf("freeze when the review fails = %+v", o)
	}
}

// TestStatusPartialWhenNamespaceForbidden: a namespace the caller cannot list
// is named with why, the answer is partial, and an upstream Stage there is
// unreadable, never missing. A walk past auditLimit is partial too.
func TestStatusPartialWhenNamespaceForbidden(t *testing.T) {
	d, c := statusFixture(t, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil))
	c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if ns := a.GetNamespace(); ns == "" || ns == "test" {
			return true, nil, forbid("stages")
		}
		return false, nil, nil
	})
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if err != nil || s.Completeness != "partial" || len(s.Unreadable) != 1 || !strings.Contains(s.Unreadable[0], "in test") {
		t.Fatalf("status = %+v, %v", s, err)
	}
	if u := s.Stages[0].Upstream; u.Problem != "unreadable" {
		t.Fatalf("an upstream in a namespace the caller cannot list = %+v", u)
	}
	if _, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Namespace: "test"}); !apierrors.IsForbidden(err) {
		t.Fatalf("a named namespace is not walked around: %v", err)
	}

	d, c = statusFixture(t)
	c.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		l := &unstructured.UnstructuredList{}
		for i := range auditLimit + 1 {
			l.Items = append(l.Items, *statusStage("x", fmt.Sprint("s", i), "payments", "prod", "", "", nil).(*unstructured.Unstructured))
		}
		return true, l, nil
	})
	s, err = d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if err != nil || s.Completeness != "partial" || len(s.Stages) != auditLimit || !strings.Contains(strings.Join(s.Unreadable, "|"), "one walk reads at most") {
		t.Fatalf("a truncated walk = %d rows, %v, %v", len(s.Stages), s.Unreadable, err)
	}
}

// TestStatusOpsForbiddenIsPartial: operations that cannot be read make the
// answer partial and the held count unknown.
func TestStatusOpsForbiddenIsPartial(t *testing.T) {
	d, c := statusFixture(t)
	c.PrependReactor("list", "operations", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbid("operations") })
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if err != nil || s.Completeness != "partial" || !s.Stages[0].HeldUnknown || !strings.Contains(strings.Join(s.Unreadable, "|"), "operations in prod: ") {
		t.Fatalf("status = %+v, %v", s, err)
	}
	if o := offer(s.Stages[0], "undo"); o.Available || !strings.Contains(o.Reason, "could not read its history") {
		t.Fatalf("an unread history gives the unavailable undo = %+v", o)
	}
}

// TestStatusOpsForbiddenStateUnknown: a verified Stage whose operations were
// refused is unknown, never verified; a frozen one is still frozen.
func TestStatusOpsForbiddenStateUnknown(t *testing.T) {
	d, c := statusFixture(t, statusStage("prod", "payments-frozen", "payments", "prod-2", "", "",
		map[string]any{"pointer": dlA, "verified": []any{dlA}, "frozen": true}))
	c.PrependReactor("list", "operations", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbid("operations") })
	s, _ := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	got := map[string]string{}
	for _, r := range s.Stages {
		got[r.Stage] = r.State
	}
	if got["payments-prod"] != "unknown" || got["payments-frozen"] != "frozen" {
		t.Fatalf("states = %v", got)
	}
}

// TestStatusUndoFromBlame: the change that put the running version in place
// gives the undo offer.
func TestStatusUndoFromBlame(t *testing.T) {
	d, _ := statusFixture(t, opFixture("ship", map[string]any{"type": delivery.TypePromote, "stage": "payments-prod",
		"params": map[string]any{"from": dlC, "to": dlA}, "actor": map[string]any{"user": "bot"}},
		map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(1), "pointer": map[string]any{"before": dlC, "after": dlA}}))
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if err != nil {
		t.Fatal(err)
	}
	if o := offer(s.Stages[0], "undo"); o.Operation != "ship" || o.To != dlC || !o.Available {
		t.Fatalf("undo = %+v", o)
	}
}

// TestStatusReadsStageOnce: the listed Stage is never read again, and its
// stream is read once.
func TestStatusReadsStageOnce(t *testing.T) {
	e := newRecordEnv(t)
	e.twoRecords()
	allow(e.c, true, nil)
	open := e.d.Records
	var opened atomic.Int64
	e.d.Records = func(path string, insecure ...string) (*remote.Repository, error) {
		opened.Add(1)
		return open(path, insecure...)
	}
	e.c.PrependReactor("get", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the listed Stage was read again")
	})
	s, err := e.d.Status(context.Background(), delivery.StatusQuery{Service: "payments"})
	if err != nil || opened.Load() != 1 {
		t.Fatalf("status = %+v, %v, the stream opened %d times", s, err, opened.Load())
	}
	if o := offer(s.Stages[0], "undo"); o.Operation != "ship" {
		t.Fatalf("undo from the records = %+v", o)
	}
}

// TestStatusWhyLimit: one walk plans at most statusWhyLimit Stages and names
// the rest in its note.
func TestStatusWhyLimit(t *testing.T) {
	var objs []runtime.Object
	for i := range statusWhyLimit + 1 {
		objs = append(objs, statusStage("o", fmt.Sprintf("s%02d", i), "orders", "prod", "", "", nil))
	}
	d, _ := statusFixture(t, objs...)
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "orders", Why: true})
	if err != nil || len(s.Stages) != statusWhyLimit+1 {
		t.Fatalf("status = %+v, %v", s, err)
	}
	for i, r := range s.Stages {
		if (r.Progress != nil) != (i < statusWhyLimit) {
			t.Fatalf("row %d progress = %+v", i, r.Progress)
		}
	}
	if !strings.Contains(s.Note, "why is answered for the first 16 of 17 Stages only") {
		t.Fatalf("note = %q", s.Note)
	}
}

// TestProgressPlansCandidate: with why each Stage's candidate is planned for
// the caller, not proposed; unread operations and a failed review make it
// unknown, never a refusal.
func TestProgressPlansCandidate(t *testing.T) {
	d, c := statusFixture(t)
	ri := c.Resource(gvrStages).Namespace("prod")
	u, _ := ri.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	_ = unstructured.SetNestedField(u.Object, dlB, "status", "candidate")
	_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "Ready", "status": "True", "reason": "Ready"}}, "status", "conditions")
	if _, err := ri.Update(context.Background(), u, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Why: true})
	if err != nil {
		t.Fatal(err)
	}
	p := s.Stages[0].Progress
	if p == nil || p.Plan == nil || p.Plan.To != dlB || p.Candidate != dlB || p.AsOf != s.AsOf {
		t.Fatalf("progress = %+v", p)
	}
	allow(c, true, errors.New("down"))
	c.PrependReactor("list", "operations", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbid("operations") })
	s, _ = d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Why: true})
	if p := s.Stages[0].Progress; p.State != "unknown" {
		t.Fatalf("progress with operations and RBAC unread = %+v", p)
	}
	c.PrependReactor("get", "stages", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("stage down") })
	s, _ = d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Why: true})
	if p := s.Stages[0].Progress; p.Plan != nil || p.State != "unknown" {
		t.Fatalf("progress with the plan failing = %+v", p)
	}
}

// TestStatusTopologicalOrder: rows follow the upstream links, whatever the
// names say.
func TestStatusTopologicalOrder(t *testing.T) {
	d, _ := statusFixture(t,
		statusStage("a", "o-prod", "orders", "prod", "b", "o-staging", nil),
		statusStage("b", "o-staging", "orders", "staging", "c", "o-test", nil),
		statusStage("c", "o-test", "orders", "test", "", "", nil))
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "orders"})
	if err != nil || envsOf(s) != "test,staging,prod" || strings.Contains(s.Note, "placed by name") {
		t.Fatalf("order = %s, note %q, %v", envsOf(s), s.Note, err)
	}
	if u := s.Stages[2].Upstream; u.Environment != "staging" {
		t.Fatalf("prod's upstream = %+v", u)
	}
}

// TestStatusReportsCycle: two Stages naming each other are both listed,
// placed by name, and the note names the cycle; so are environments
// different Stages order both ways.
func TestStatusReportsCycle(t *testing.T) {
	d, _ := statusFixture(t,
		statusStage("x", "o-us", "orders", "us", "x", "o-eu", nil),
		statusStage("x", "o-eu", "orders", "eu", "x", "o-us", nil))
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "orders"})
	if err != nil || envsOf(s) != "eu,us" || !strings.Contains(s.Note, "; eu, us sit on a cycle of upstream Stages, so they are placed by name") {
		t.Fatalf("order = %s, note %q, %v", envsOf(s), s.Note, err)
	}
	d, _ = statusFixture(t,
		statusStage("x", "o-eu", "orders", "eu", "", "", nil), statusStage("x", "o-us", "orders", "us", "x", "o-eu", nil),
		statusStage("y", "o-us", "orders", "us", "", "", nil), statusStage("y", "o-eu", "orders", "eu", "y", "o-us", nil))
	s, _ = d.Status(context.Background(), delivery.StatusQuery{Service: "orders"})
	if !strings.Contains(s.Note, "; eu, us are ordered both ways by different Stages, so they are placed by name") {
		t.Fatalf("note = %q", s.Note)
	}
}

// TestStatusUpstreamOtherService: an upstream Stage of another service in a
// namespace read in full names its environment, never missing.
func TestStatusUpstreamOtherService(t *testing.T) {
	d, _ := statusFixture(t,
		statusStage("o", "orders", "orders", "prod", "p", "other", nil),
		statusStage("p", "other", "other", "qa", "", "", nil))
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "orders", Namespace: "o"})
	if err != nil || s.Stages[0].Upstream.Problem != "unreadable" {
		t.Fatalf("with -n o the namespace p was not read: %+v, %v", s.Stages[0].Upstream, err)
	}
	s, _ = d.Status(context.Background(), delivery.StatusQuery{Service: "orders"})
	if u := s.Stages[0].Upstream; len(s.Stages) != 1 || u.Environment != "qa" || u.Problem != "" {
		t.Fatalf("upstream = %+v", u)
	}
}

// walk is a walk's shape: how many Stages it listed, how many of the
// service, whether it read every namespace and which it read in full.
func walk(w auditWalk) string {
	return fmt.Sprint(len(w.All), " ", len(w.Stages), " ", w.Everywhere, " ", w.Listed, " ", len(w.Forbidden))
}

// TestAuditStagesWalk: All keeps every listed Stage, Stages only the
// service's, and the walk says which namespaces it read in full.
func TestAuditStagesWalk(t *testing.T) {
	d, c := statusFixture(t, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil),
		statusStage("test", "orders", "orders", "test", "", "", nil))
	ctx, payments := context.Background(), delivery.ServiceRef{Name: "payments"}
	w, err := d.auditStages(ctx, "", payments)
	if walk(w) != "3 1 true [] 0" || err != nil || !w.read("anywhere") {
		t.Fatalf("cluster-wide walk = %s, %v", walk(w), err)
	}
	if w, _ = d.auditStages(ctx, "test", payments); walk(w) != "2 0 false [test] 0" || w.read("prod") {
		t.Fatalf("one namespace = %s", walk(w))
	}
	c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if ns := a.GetNamespace(); ns == "" || ns == "test" {
			return true, nil, forbid("stages")
		}
		return false, nil, nil
	})
	if w, _ = d.auditStages(ctx, "", payments); walk(w) != "1 1 false [prod] 1" || w.read("test") {
		t.Fatalf("per namespace = %s", walk(w))
	}
}

// TestStatusCountsHeldPerStage: held counts only the Stage's own changes
// waiting for a person, and why reads them.
func TestStatusCountsHeldPerStage(t *testing.T) {
	spec := func(stage string) map[string]any {
		return map[string]any{"type": delivery.TypePromote, "stage": stage, "params": map[string]any{"from": dlA, "to": dlB}, "actor": map[string]any{"user": "bot"}}
	}
	d, _ := statusFixture(t, opFixture("mine", spec("payments-prod"), map[string]any{"phase": "Held"}),
		opFixture("live", spec("payments-prod"), map[string]any{"phase": "Verifying"}),
		opFixture("theirs", spec("payments-other"), map[string]any{"phase": "Held"}))
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Why: true})
	if err != nil || s.Stages[0].Held != 1 || s.Stages[0].State != "waiting" {
		t.Fatalf("status = %+v, %v", s, err)
	}
	if p := s.Stages[0].Progress; !slices.ContainsFunc(p.Reasons, func(r delivery.ProgressReason) bool { return r.Code == "Held" }) {
		t.Fatalf("progress = %+v", p)
	}
}
