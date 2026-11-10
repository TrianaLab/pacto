/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	cevent "sigs.k8s.io/controller-runtime/pkg/event"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

var stageCreated = time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

// TestStageEventsOnlyOnTransitions: seeding the stream says EnvironmentCreated
// once, a later pass says nothing, a status-loss reseed says it again with the
// original creationTimestamp, the delete handler says EnvironmentDeleted and a
// nil recorder says nothing.
func TestStageEventsOnlyOnTransitions(t *testing.T) {
	s := stageObj(dA)
	s.Status.Stream.ID = ""
	s.CreationTimestamp = metav1.NewTime(stageCreated)
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), verifiedRepo("payments-candidates", "latest@"+dA))
	rec := record.NewFakeRecorder(100)
	e.st.Events = rec
	want := "Normal EnvironmentCreated Stage prod/payments-prod for payments in prod was created at 2025-03-04T05:06:07Z; other Stages for the same environment are unaffected"
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if ev := drain(rec); len(ev) != 1 || ev[0] != want {
		t.Fatalf("seed: %v", ev)
	}
	for range 2 {
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatal(err)
		}
	}
	if ev := drain(rec); hasEvent(ev, "Normal EnvironmentCreated") {
		t.Fatalf("a later pass re-emits: %v", ev)
	}
	e.setStage(func(st *delivery.StageStatus) { st.Stream.ID = "" })
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if ev := drain(rec); len(ev) != 1 || ev[0] != want {
		t.Fatalf("reseed: %v", ev)
	}

	e.st.stageDeleted(context.Background(), cevent.DeleteEvent{Object: e.getStage(), DeleteStateUnknown: true}, nil)
	if ev := drain(rec); len(ev) != 1 || ev[0] != "Normal EnvironmentDeleted Stage prod/payments-prod for payments in prod was deleted; other Stages for the same environment are unaffected" {
		t.Fatalf("delete: %v", ev)
	}
	(&StageReconciler{}).stageDeleted(context.Background(), cevent.DeleteEvent{Object: s}, nil) // nil recorder: nothing
}

func envStage(ns, name, domain, uid string) *dv1.Stage {
	s := stageObj(dA)
	s.Namespace, s.Name, s.UID = ns, name, types.UID(uid)
	s.Spec.Service.Domain = domain
	s.Status.Stream.ID = "cid/" + ns + "/" + name
	s.CreationTimestamp = metav1.NewTime(stageCreated)
	return s
}

func subjectOf(ev map[string]any) map[string]any {
	return ev["data"].(map[string]any)["subject"].(map[string]any)
}

func customOf(ev map[string]any) map[string]any {
	return ev["data"].(map[string]any)["customData"].(map[string]any)
}

// TestEnvironmentEventShape pins the CDEvents v0.5.1 environment types and the
// event fields; distinct Stages get distinct subjects.
func TestEnvironmentEventShape(t *testing.T) {
	if eventEnvironmentCreated != "dev.cdevents.environment.created.0.3.0" || eventEnvironmentDeleted != "dev.cdevents.environment.deleted.0.3.0" {
		t.Fatal("environment event types are not the v0.5.1 ones")
	}
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	s := envStage("prod", "payments-prod", "acme", "u1")
	c := environmentEvent("/pacto-delivery/cid", s, true, now)
	if c["type"] != eventEnvironmentCreated || c["id"] != "u1/created" || c["time"] != "2025-03-04T05:06:07Z" || c["source"] != "/pacto-delivery/cid" {
		t.Fatalf("created = %v", c)
	}
	sub := subjectOf(c)
	if sub["id"] != "cid/prod/payments-prod" || sub["content"].(map[string]any)["name"] != "prod" || len(sub["content"].(map[string]any)) != 1 {
		t.Fatalf("subject = %v", sub)
	}
	cd := customOf(c)
	if cd["service"] != "payments" || cd["domain"] != "acme" || cd["stage"] != "prod/payments-prod" || cd["stageUID"] != "u1" ||
		cd["createdAt"] != "2025-03-04T05:06:07Z" || cd["observedAt"] != nil {
		t.Fatalf("customData = %v", cd)
	}
	other := subjectOf(environmentEvent("", envStage("prod2", "payments-prod", "other", "u2"), true, now))["id"]
	sibling := subjectOf(environmentEvent("", envStage("prod", "payments-prod-b", "acme", "u3"), true, now))["id"]
	if other == sub["id"] || sibling == sub["id"] || other == sibling {
		t.Fatalf("subjects collide: %v %v %v", sub["id"], other, sibling)
	}

	d := environmentEvent("/pacto-delivery/cid", s, false, now)
	if d["type"] != eventEnvironmentDeleted || d["id"] != "u1/deleted" || d["time"] != nil || customOf(d)["observedAt"] != "2026-10-20T10:00:00Z" {
		t.Fatalf("deleted without deletionTimestamp = %v", d)
	}
	gone := metav1.NewTime(now.Add(-time.Minute))
	s.DeletionTimestamp = &gone
	d = environmentEvent("/pacto-delivery/cid", s, false, now)
	if d["time"] != "2026-10-20T09:59:00Z" || customOf(d)["observedAt"] != nil {
		t.Fatalf("deleted with deletionTimestamp = %v", d)
	}
}

// TestRecorderEnvironmentEventsOnlyOnTransitions: the recorder posts created
// only when the stream id is first set, deleted on a delete, nothing with no
// sink.
func TestRecorderEnvironmentEventsOnlyOnTransitions(t *testing.T) {
	srv, got := sink(t, http.StatusAccepted)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	r := &Recorder{Events: &Events{Sink: srv.URL, Source: "/pacto-delivery/cid"}, Now: func() time.Time { return now }}
	seeded := envStage("prod", "payments-prod", "", "u1")
	unseeded := seeded.DeepCopy()
	unseeded.Status.Stream.ID = ""
	ctx := context.Background()
	r.stageStreamSeeded(ctx, cevent.UpdateEvent{ObjectOld: unseeded, ObjectNew: seeded}, nil)
	if ev := receive(t, got); ev["type"] != eventEnvironmentCreated {
		t.Fatalf("seeded = %v", ev)
	}
	r.stageStreamSeeded(ctx, cevent.UpdateEvent{ObjectOld: seeded, ObjectNew: seeded}, nil)
	r.stageGone(ctx, cevent.DeleteEvent{Object: seeded, DeleteStateUnknown: true}, nil)
	if ev := receive(t, got); ev["type"] != eventEnvironmentDeleted {
		t.Fatalf("an update with the stream already set posted, or the delete did not: %v", ev)
	}
	(&Recorder{Now: time.Now}).stageGone(ctx, cevent.DeleteEvent{Object: seeded}, nil) // nil Events: nothing
	srvDown, _ := sink(t, http.StatusInternalServerError)
	(&Events{Sink: srvDown.URL}).emitEnvironment(ctx, seeded, true, now) // a failing sink is only logged
	select {
	case ev := <-got:
		t.Fatalf("unexpected event %v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
