/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

var remoteTest = delivery.RemoteStage{Repository: "reg.example/delivery/streams", Stream: "cid-test/test/payments-test"}

// remoteStage is a prod Stage that takes its versions from a test Stage in
// another cluster, through that Stage's signed records.
func remoteStage(pointer string) *dv1.Stage {
	s := stageObj(pointer)
	s.Spec.Candidates = delivery.Candidates{UpstreamRecords: &remoteTest}
	return s
}

// upstreamReads makes the promoter read the remote upstream as status, or
// nothing with why.
func (e *env) upstreamReads(status *delivery.StageStatus, why string) {
	f := func(_ context.Context, r delivery.RemoteStage, _ delivery.RecordMark) (*delivery.StageStatus, string) {
		if r != remoteTest {
			e.t.Fatalf("read the wrong upstream: %+v", r)
		}
		return status, why
	}
	e.op.Cluster.Upstream, e.st.Cluster.Upstream = f, f
}

// TestRemoteUpstreamReady: a Stage fed by another cluster's records is not
// Ready while the promoter trusts no upstream key.
func TestRemoteUpstreamReady(t *testing.T) {
	e := newEnv(t, remoteStage(dA), ociRepo("payments", dA, "", false))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if c := conditionOf(e.getStage().Status.Conditions, "Ready"); c == nil || c.Reason != "UpstreamUntrusted" || !strings.Contains(c.Message, "promoter.upstreamTrust") {
		t.Fatalf("untrusted: %+v", c)
	}
	e.upstreamReads(&delivery.StageStatus{}, "")
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if c := conditionOf(e.getStage().Status.Conditions, "Ready"); c == nil || c.Reason != "Ready" {
		t.Fatalf("trusted: %+v", c)
	}
}

// TestRemoteUpstreamUnreadable: records that cannot be read make the Stage
// not Ready, naming why, instead of stalling silently; Ready returns once a
// read succeeds. The registry is read again only every UpstreamPoll, and the
// newest record read is remembered, so a stream cut at its tail is refused.
func TestRemoteUpstreamUnreadable(t *testing.T) {
	s := remoteStage(dA)
	e := newEnv(t, s, ociRepo("payments", dA, "", false))
	reads := 0
	// view is the upstream stream holding ids at seqs 1, 2 and so on, each a
	// verified and published promote of the next version in verified.
	var up []delivery.StoredRecord
	view := func(ids []string, verified ...string) []delivery.StoredRecord {
		recs := make([]delivery.StoredRecord, 0, len(ids))
		for i, id := range ids {
			parent := ""
			if i > 0 {
				parent = ids[i-1]
			}
			recs = append(recs, delivery.StoredRecord{ID: id, Statement: delivery.Statement{Predicate: delivery.RecordPredicate{
				Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: delivery.TypePromote},
				Intent:   delivery.RecordIntent{Params: &delivery.Params{To: verified[i%len(verified)]}},
				Verdicts: []delivery.Verdict{{Gate: delivery.GatePublished, Result: delivery.Pass}},
				Outcome:  &delivery.RecordOutcome{Result: delivery.Succeeded, Verification: delivery.Verified},
				Chain:    delivery.Chain{Stream: remoteTest.Stream, Seq: int64(i + 1), Parent: parent}}}})
		}
		return recs
	}
	why := "reading the records: registry down"
	f := func(_ context.Context, _ delivery.RemoteStage, seen delivery.RecordMark) (*delivery.StageStatus, string) {
		reads++
		if up == nil {
			return nil, why
		}
		return delivery.UpstreamFromRecords(up, seen)
	}
	e.op.Cluster.Upstream, e.st.Cluster.Upstream = f, f
	ready := func() delivery.Condition {
		t.Helper()
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatal(err)
		}
		return *conditionOf(e.getStage().Status.Conditions, "Ready")
	}
	if c := ready(); c.Reason != "UpstreamUnreadable" || !strings.Contains(c.Message, "registry down") || !strings.Contains(c.Message, remoteTest.Stream) {
		t.Fatalf("unreadable: %+v", c)
	}
	up = view([]string{"r1", "r2", "r3"}, dB)
	if c := ready(); c.Reason != "UpstreamUnreadable" || reads != 1 || len(e.ops()) != 0 {
		t.Fatalf("a read younger than UpstreamPoll is reused: %+v reads=%d", c, reads)
	}
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "Ready" || reads != 2 {
		t.Fatalf("readable again: %+v reads=%d", c, reads)
	}
	if got := e.getStage().Status.UpstreamSeen; got.Stream != remoteTest.Stream || got.Seq != 3 || got.Record != "r3" {
		t.Fatalf("the newest record read is remembered: %+v", got)
	}
	if ops := e.ops(); len(ops) != 1 || ops[0].Spec.Params.To != dB {
		t.Fatalf("auto-promotes what the read offers: %+v", ops)
	}
	// The newest records disappear: the shorter stream offers nothing, not
	// the version a removed record failed.
	up = view([]string{"r1", "r2"}, dB, dC)
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "UpstreamUnreadable" || !strings.Contains(c.Message, "newest records were removed") {
		t.Fatalf("a cut stream: %+v", c)
	}
	// Planning reads afresh and refuses the cut stream too.
	if s, why, err := (planSources{e.op.Cluster}).Upstream(context.Background(), stageObject(e.getStage())); s != nil || err != nil || !strings.Contains(why, "newest records were removed") {
		t.Fatalf("plan read of a cut stream: %+v %q %v", s, why, err)
	}
	// The same seq is not rewritten in place, and a read that extends it moves the mark.
	up = view([]string{"r1", "r2", "x3"}, dB)
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "UpstreamUnreadable" || !strings.Contains(c.Message, "rewritten") {
		t.Fatalf("a rewritten stream: %+v", c)
	}
	// Nor is a rewrite accepted once the stream grows past it.
	up = view([]string{"r1", "r2", "x3", "r4"}, dB)
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "UpstreamUnreadable" || !strings.Contains(c.Message, "rewritten") {
		t.Fatalf("a rewritten then extended stream: %+v", c)
	}
	up = view([]string{"r1", "r2", "r3", "r4"}, dB)
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "Ready" || e.getStage().Status.UpstreamSeen.Seq != 4 {
		t.Fatalf("extended: %+v %+v", c, e.getStage().Status.UpstreamSeen)
	}
	// A deliberate reset of the upstream stream blocks the follower until a
	// person clears the mark, as delivery-reach.md tells them to.
	up = view([]string{"n1"}, dC)
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "UpstreamUnreadable" {
		t.Fatalf("a reset stream: %+v", c)
	}
	reset := client.RawPatch(types.MergePatchType, []byte(`{"status":{"upstreamSeen":null}}`))
	if err := e.c.Status().Patch(context.Background(), e.getStage(), reset); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(UpstreamPoll)
	if c := ready(); c.Reason != "Ready" || e.getStage().Status.UpstreamSeen.Record != "n1" {
		t.Fatalf("after clearing the mark: %+v %+v", c, e.getStage().Status.UpstreamSeen)
	}
}

// TestRemoteUpstreamAutoPromote: auto-promotion ships the newest version the
// remote upstream verified and offered, and nothing when its records cannot be
// verified.
func TestRemoteUpstreamAutoPromote(t *testing.T) {
	s := remoteStage(dA)
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false))
	e.upstreamReads(nil, "records that could not be verified")
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 0 {
		t.Fatalf("unverifiable records raise nothing: %v %d", err, len(e.ops()))
	}
	e.upstreamReads(&delivery.StageStatus{Verified: []string{dB, dC}, Offered: []string{dB}}, "")
	e.now = e.now.Add(UpstreamPoll)
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 || e.ops()[0].Spec.Params.To != dB {
		t.Fatalf("remote candidate: %v %+v", err, e.ops())
	}
}

// TestRemoteUpstreamPublishedGate: a promote passes pacto.published only on a
// version the remote upstream's records show offered, and the refusal says
// why the records gave nothing.
func TestRemoteUpstreamPublishedGate(t *testing.T) {
	op := func() *dv1.Operation {
		o := opObj("p1", delivery.TypePromote, dA, dB, bot)
		o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, remoteStage("").Spec)
		return o
	}
	world := func() *env {
		return newEnv(t, remoteStage(dA), ociRepo("payments", dA, "latest@"+dA, true),
			kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+dA, "latest@"+dA),
			deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	}
	e := world()
	e.create(op())
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Rejected, "NotPublished")
	if !strings.Contains(o.Status.Message, "promoter.upstreamTrust") {
		t.Fatalf("names the missing trust: %q", o.Status.Message)
	}
	e = world()
	e.upstreamReads(&delivery.StageStatus{Verified: []string{dB}, Offered: []string{dB}}, "")
	e.create(op())
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("an offered version ships: %s %q", o.Status.Phase, o.Status.Message)
	}
}
