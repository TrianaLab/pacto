/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
)

func TestRecorderNamesTheAnchorAndCitesTrust(t *testing.T) {
	re := newRecEnv(t)
	re.rec.Trust = "sha256:trust"
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	if s := re.getStage(); s.Status.Stream.Anchor != deliveryrecord.AnchorRef(re.rec.Store.Streams, "cid/prod/payments-prod") {
		t.Fatalf("anchor = %q", s.Status.Stream.Anchor)
	}
	if recs := re.records(); len(recs) != 1 || recs[0].Statement.Predicate.Trust != "sha256:trust" {
		t.Fatalf("records = %+v", recs)
	}
	// A gap cites it too.
	re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
	re.rec.GapGrace = 0
	re.reconcile()
	for _, r := range re.records() {
		if r.Statement.Predicate.Trust != "sha256:trust" {
			t.Fatalf("record %+v cites no trust config", r.Statement.Predicate)
		}
	}
}

func TestRecorderNotReadyTwiceWritesOnce(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reg.unsupported = true
	re.reconcile()
	rv := re.getStage().ResourceVersion
	re.reconcile()
	if re.getStage().ResourceVersion != rv {
		t.Fatal("the same condition was written twice")
	}
}

func TestRecorderAdvanceFailsAfterTheWrite(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.setStage(func(s *delivery.StageStatus) {
		s.Stream.Anchor = deliveryrecord.AnchorRef(re.rec.Store.Streams, "cid/prod/payments-prod")
		s.Conditions = []delivery.Condition{{Type: ConditionRecordStoreReady, Status: "True", Reason: "ReferrersSupported",
			Message: "records are written to " + re.rec.Store.Streams}}
	})
	re.concluded("ship", 1, delivery.Succeeded)
	re.fails = fails{"status/Stage": true}
	if _, err := re.rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf("payments-prod")}); err == nil {
		t.Fatal("a failed stream advance was not an error")
	}
	if len(re.records()) != 1 {
		t.Fatal("the record was not written before the advance")
	}
}

// sink is a CloudEvents receiver.
func sink(t *testing.T, status int) (*httptest.Server, chan map[string]any) {
	t.Helper()
	got := make(chan map[string]any, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &ev)
		if r.Header.Get("Content-Type") != "application/cloudevents+json" {
			ev["badContentType"] = true
		}
		got <- ev
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func receive(t *testing.T, got chan map[string]any) map[string]any {
	t.Helper()
	select {
	case ev := <-got:
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no CDEvent arrived")
		return nil
	}
}

func TestRecorderEmitsCDEvents(t *testing.T) {
	srv, got := sink(t, http.StatusAccepted)
	re := newRecEnv(t)
	re.rec.Events = &Events{Sink: srv.URL, Source: "/pacto-delivery/cid"}
	re.stage(1)
	o := re.concluded("ship", 1, delivery.Succeeded)
	o.Status.Applied = true
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.reconcile()
	id := re.records()[0].ID
	byType := map[string]map[string]any{}
	for range 2 {
		ev := receive(t, got)
		byType[ev["type"].(string)] = ev
	}
	up, concluded := byType[eventUpgraded], byType[eventConcluded]
	if up == nil || concluded == nil || up["id"] != id || concluded["id"] != id || up["source"] == concluded["source"] ||
		up["badContentType"] != nil {
		t.Fatalf("events = %v", byType)
	}
	subject := up["data"].(map[string]any)["subject"].(map[string]any)
	if subject["id"] != "payments" || !strings.HasSuffix(subject["content"].(map[string]any)["artifactId"].(string), re.reg.to) {
		t.Fatalf("service subject = %v", subject)
	}
}

func TestCDEventKinds(t *testing.T) {
	stage := stageObj("")
	st := func(typ, before, after string) delivery.Statement {
		return delivery.Statement{Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: typ},
			Outcome: &delivery.RecordOutcome{Result: delivery.Succeeded, Pointer: delivery.PointerChange{Before: before, After: after}}}}
	}
	kind := func(evs []map[string]any) string {
		if len(evs) < 2 {
			return ""
		}
		return evs[1]["type"].(string)
	}
	for name, c := range map[string]struct {
		st      delivery.Statement
		applied bool
		want    string
	}{
		"first version": {st(delivery.TypePromote, "", dA), true, eventDeployed},
		"rollback":      {st(delivery.TypeRollback, dB, dA), true, eventRolledBack},
		"not applied":   {st(delivery.TypePromote, dA, dB), false, ""},
		"moved nothing": {st(delivery.TypePromote, "", ""), true, ""},
	} {
		if got := kind(cdEvents("src", stage, c.st, "sha256:r", c.applied)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	untyped := st(delivery.TypePromote, "", "")
	untyped.Predicate.Type = nil
	if evs := cdEvents("src", stage, untyped, "sha256:r", false); len(evs) != 1 {
		t.Fatalf("events = %v", evs)
	}
	// Only operation records emit, and a nil Events emits nothing.
	var none *Events
	none.emit(context.Background(), stage, st(delivery.TypePromote, "", dA), "x", true)
	(&Events{}).emit(context.Background(), stage, delivery.Statement{Predicate: delivery.RecordPredicate{Kind: delivery.RecordGap}}, "x", true)
}

func TestCDEventDeliveryFailuresAreLogged(t *testing.T) {
	srv, got := sink(t, http.StatusInternalServerError)
	logged := make(chan string, 4)
	ctx := ctrl.LoggerInto(context.Background(), funcr.New(func(_, args string) { logged <- args }, funcr.Options{}))
	e := &Events{Sink: srv.URL, Source: "src"}
	e.emit(ctx, stageObj(""), delivery.Statement{Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation,
		Outcome: &delivery.RecordOutcome{}}}, "sha256:r", false)
	receive(t, got)
	select {
	case msg := <-logged:
		if !strings.Contains(msg, "500") {
			t.Fatalf("logged %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a failed delivery was not logged")
	}
	if err := (&Events{Sink: "http://bad\x7f"}).post(map[string]any{}); err == nil {
		t.Fatal("posted to an unparseable sink")
	}
	srv.Close()
	if err := (&Events{Sink: srv.URL, Client: http.DefaultClient}).post(map[string]any{}); err == nil {
		t.Fatal("posted to a sink that is gone")
	}
}

func TestPromoterExpiresRecordedOperations(t *testing.T) {
	recorded := func(e *env, name string, finished time.Time, edit ...func(*dv1.Operation)) {
		o := opObj(name, delivery.TypePromote, "", dA, bot)
		o.Finalizers = nil
		e.create(o)
		o.Status = delivery.OperationStatus{Phase: delivery.PhaseConcluded, Seq: 1, Times: delivery.Times{Finished: ts(finished)},
			Record: &delivery.RecordRef{Digest: "sha256:r", Seq: 1, Kind: delivery.RecordOperation}}
		for _, f := range edit {
			f(o)
		}
		if err := e.c.Status().Update(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	gone := func(e *env, name string) bool {
		return e.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &dv1.Operation{}) != nil
	}
	e := newEnv(t, stageObj(dA))
	e.op.RecordedTTL = time.Hour
	recorded(e, "old", e.now.Add(-2*time.Hour))
	recorded(e, "fresh", e.now.Add(-time.Minute))
	recorded(e, "running", e.now.Add(-2*time.Hour))
	recorded(e, "unrecorded", e.now.Add(-2*time.Hour), func(o *dv1.Operation) { o.Status.Record = nil })
	recorded(e, "no-time", e.now, func(o *dv1.Operation) { o.Status.Times.Finished = "" })
	e.setStage(func(s *delivery.StageStatus) { s.LastOp = "uid-running" })
	e.reconcileOp("old")
	if !gone(e, "old") {
		t.Fatal("a recorded operation past its TTL was kept")
	}
	if res := e.reconcileOp("fresh"); res.RequeueAfter != 59*time.Minute || gone(e, "fresh") {
		t.Fatalf("a fresh operation: %+v", res)
	}
	for _, name := range []string{"running", "unrecorded", "no-time"} {
		e.reconcileOp(name)
		if gone(e, name) {
			t.Errorf("%s was deleted", name)
		}
	}
	e.fails = fails{"get/Stage": true}
	if err := e.reconcileOpErr("running"); err == nil {
		t.Fatal("a failed Stage read was not an error")
	}
	e.fails = fails{}
	e.op.RecordedTTL = 0
	recorded(e, "kept", e.now.Add(-48*time.Hour))
	e.reconcileOp("kept")
	if gone(e, "kept") {
		t.Fatal("with no TTL an operation was deleted")
	}

	// The Stage is gone: nothing can cite the operation any more.
	e2 := newEnv(t)
	e2.op.RecordedTTL = time.Hour
	recorded(e2, "orphan", e2.now.Add(-2*time.Hour))
	e2.reconcileOp("orphan")
	if !gone(e2, "orphan") {
		t.Fatal("a recorded operation of a deleted Stage was kept")
	}
}

func TestPromoterFinalizerReleaseFailure(t *testing.T) {
	e := newEnv(t, stageObj(""))
	o := opObj("done", delivery.TypePromote, "", dA, bot)
	e.create(o)
	o.Status = delivery.OperationStatus{Phase: delivery.PhaseConcluded}
	_ = e.c.Status().Update(context.Background(), o)
	_ = e.c.Delete(context.Background(), o)
	e.fails = fails{"update/Operation": true}
	if err := e.reconcileOpErr("done"); err == nil {
		t.Fatal("a failed finalizer release was not an error")
	}
}

func TestStreamConflictFreezesTheStage(t *testing.T) {
	conflict := delivery.Condition{Type: ConditionRecordStoreReady, Status: "False", Reason: ReasonStreamConflict, Message: "records a and b both claim seq 2"}
	e := newEnv(t, stageObj(dA))
	e.setStage(func(s *delivery.StageStatus) { s.Conditions = []delivery.Condition{conflict} })
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	s := e.getStage()
	if !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "both claim seq 2") || !strings.Contains(s.Status.FrozenReason, "then unfreeze") {
		t.Fatalf("frozen %v: %s", s.Status.Frozen, s.Status.FrozenReason)
	}
	// Already frozen for another reason: the reason stays.
	e.setStage(func(s *delivery.StageStatus) { s.FrozenReason = "a person froze it" })
	_ = e.reconcileStage("payments-prod")
	if e.getStage().Status.FrozenReason != "a person froze it" {
		t.Fatal("the freeze reason was overwritten")
	}
	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason = false, "" })
	e.fails = fails{"status/Stage": true}
	if err := e.reconcileStage("payments-prod"); err == nil {
		t.Fatal("a failed freeze was not an error")
	}
	e.fails = fails{}
	e.setStage(func(s *delivery.StageStatus) { s.Conditions = nil })
	_ = e.reconcileStage("payments-prod")
	if e.getStage().Status.Frozen {
		t.Fatal("a Stage with a sound stream froze")
	}
}
