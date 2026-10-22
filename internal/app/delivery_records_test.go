package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const recStream = "c/prod/payments-prod"

// recordEnv is a registry holding a Stage's records, and a delivery client
// that reads them.
type recordEnv struct {
	t      *testing.T
	d      *Delivery
	c      *dynamicfake.FakeDynamicClient
	srv    *httptest.Server
	host   string
	key    ed25519.PrivateKey
	anchor ocispec.Descriptor
}

func newRecordEnv(t *testing.T, objs ...runtime.Object) *recordEnv {
	t.Helper()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, _ := delivery.PublicKeyPEM(pub)
	trust, err := delivery.ParseTrust([]byte(pemKey))
	if err != nil {
		t.Fatal(err)
	}
	d, c, _ := deliveryFixture(t, "alice", nil, nil, objs...)
	d.Records = func(path string, insecure ...string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Insecure: insecure})
	}
	d.Trust = &trust
	e := &recordEnv{t: t, d: d, c: c, srv: srv, host: strings.TrimPrefix(srv.URL, "http://"), key: key}
	anchor := deliveryrecord.Anchor(recStream)
	if err := ocireferrer.Push(context.Background(), e.repo("delivery/streams"), anchor); err != nil {
		t.Fatal(err)
	}
	e.anchor = anchor.ManifestDesc
	return e
}

func (e *recordEnv) repo(path string) *remote.Repository {
	r, _ := ocireferrer.Open(e.host+"/"+path, ocireferrer.Options{})
	return r
}

// attach signs p and attaches it to subject in repo, as the recorder does.
func (e *recordEnv) attach(repo string, subject ocispec.Descriptor, p delivery.RecordPredicate) string {
	e.t.Helper()
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation, Predicate: p}
	b, id, err := delivery.SignRecord(context.Background(), edSigner(e.key), st)
	if err != nil {
		e.t.Fatal(err)
	}
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, b, &subject,
		map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateOperation})
	if err := ocireferrer.Push(context.Background(), e.repo(repo), art); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// move is the predicate of an operation that moved the Stage before → after.
func move(seq int64, parent, name, before, after string) delivery.RecordPredicate {
	return delivery.RecordPredicate{Kind: delivery.RecordOperation,
		Type:    &delivery.RecordType{ID: delivery.TypePromote},
		Intent:  delivery.RecordIntent{UID: "uid-" + name, Name: name, Params: &delivery.Params{From: before, To: after}, Reason: "ship " + name},
		Actor:   &delivery.RecordActor{User: "bot", Class: delivery.ClassAgent},
		Outcome: &delivery.RecordOutcome{Result: delivery.Succeeded, Verification: delivery.Verified, Pointer: delivery.PointerChange{Before: before, After: after}},
		Chain:   delivery.Chain{Stream: recStream, Seq: seq, Parent: parent},
		Time:    delivery.RecordTime{Finished: "2026-10-21T10:00:00Z"}}
}

// setStream writes the Stage's stream status.
func (e *recordEnv) setStream(fields map[string]any) {
	e.t.Helper()
	ri := e.c.Resource(gvrStages).Namespace("prod")
	u, _ := ri.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	for k, v := range fields {
		_ = unstructured.SetNestedField(u.Object, v, "status", k)
	}
	if _, err := ri.Update(context.Background(), u, metav1.UpdateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

// twoRecords records boot ("" → C) and ship (C → A), the version the Stage
// runs, with an approval and an onBehalfOf on ship.
func (e *recordEnv) twoRecords() (string, string) {
	r1 := e.attach("delivery/streams", e.anchor, move(1, "", "boot", "", dlC))
	ship := move(2, r1, "ship", dlC, dlA)
	ship.Actor.OnBehalfOf = &delivery.OnBehalfOf{User: "carol", Asserted: true}
	ship.Approvals = []delivery.RecordApproval{{By: "alice", Decision: delivery.Approve}}
	ship.Verdicts = []delivery.Verdict{{Gate: delivery.GateContract, Result: delivery.Pass}}
	r2 := e.attach("delivery/streams", e.anchor, ship)
	e.setStream(map[string]any{"lastOp": "uid-ship", "stream": map[string]any{"id": recStream, "seq": int64(2),
		"recordedSeq": int64(2), "head": r2, "anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream)}})
	return r1, r2
}

func shipSpec(to string) map[string]any {
	return map[string]any{"type": delivery.TypePromote, "stage": "payments-prod", "params": map[string]any{"from": dlC, "to": to},
		"actor": map[string]any{"user": "bot"}}
}

func TestHistoryFromRecords(t *testing.T) {
	e := newRecordEnv(t,
		opFixture("ship", shipSpec(dlA), map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(2)}),
		opFixture("next", shipSpec(dlB), map[string]any{"phase": "Verifying"}))
	_, r2 := e.twoRecords()
	h, err := e.d.History(context.Background(), "prod", "payments-prod")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, en := range h.Entries {
		lines = append(lines, en.Operation+":"+en.Record)
	}
	if h.Source != delivery.SourceRecords || h.Completeness != "complete" || h.Head != r2 || len(h.Problems) != 0 ||
		len(lines) != 3 || lines[1] != "ship:"+r2 || lines[2] != "next:" || !strings.Contains(h.Note, "verified with trust config") {
		t.Fatalf("history = %+v (%v)", h, lines)
	}

	// With no trust config the records are listed and the answer is partial.
	e.d.Trust = nil
	h, _ = e.d.History(context.Background(), "prod", "payments-prod")
	if h.Completeness != "partial" || !strings.Contains(h.Note, "no signature was checked") || len(h.Entries) != 3 {
		t.Fatalf("unverified history = %+v", h)
	}
}

// TestHistoryNamesASeqNothingHolds: a seq the Stage concluded that has
// neither a record nor an Operation is a hole, never nothing having happened.
// A seq whose conclusion is still being written, or held by a live Operation,
// is not.
func TestHistoryNamesASeqNothingHolds(t *testing.T) {
	e := newRecordEnv(t,
		opFixture("late", shipSpec(dlB), map[string]any{"phase": "Verifying"}),
		opFixture("live", shipSpec(dlB), map[string]any{"phase": "Concluded", "seq": int64(5)}),
		opFixture("old", shipSpec(dlB), map[string]any{"phase": "Concluded", "seq": int64(6), "stageUID": "an-earlier-stage"}))
	_, r2 := e.twoRecords()
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "seq": int64(6), "recordedSeq": int64(2), "head": r2,
		"anchor":    deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream),
		"concluded": []any{map[string]any{"uid": "uid-late", "seq": int64(4)}, map[string]any{"uid": "uid-gone", "seq": int64(3)}}}})
	h, _ := e.d.History(context.Background(), "prod", "payments-prod")
	want := []string{"local seq 3 concluded, but neither its record nor its Operation can be read",
		"local seq 6 concluded, but neither its record nor its Operation can be read"}
	if h.Completeness != "partial" || fmt.Sprint(h.Problems) != fmt.Sprint(want) {
		t.Fatalf("history = %s %v", h.Completeness, h.Problems)
	}
}

// TestHistoryNamesARecordThatDidNotVerify: an Operation whose record exists
// but could not be verified says so, never that it is not recorded yet.
func TestHistoryNamesARecordThatDidNotVerify(t *testing.T) {
	e := newRecordEnv(t, opFixture("live", shipSpec(dlB), map[string]any{"phase": "Concluded", "seq": int64(3),
		"record": map[string]any{"digest": "sha256:unverified", "seq": int64(3)}}))
	e.twoRecords()
	h, _ := e.d.History(context.Background(), "prod", "payments-prod")
	if last := h.Entries[len(h.Entries)-1]; last.Operation != "live" || last.Record != "" || last.UnverifiedRecord != "sha256:unverified" {
		t.Fatalf("history = %+v", h.Entries)
	}
}

func TestHistoryRecordsProblems(t *testing.T) {
	e := newRecordEnv(t)
	e.twoRecords()
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "head": "sha256:gone",
		"anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream)}})
	h, _ := e.d.History(context.Background(), "prod", "payments-prod")
	if h.Completeness != "partial" || len(h.Problems) != 1 || !strings.Contains(h.Problems[0], "lost history") ||
		!strings.Contains(h.Note, "show problems") {
		t.Fatalf("history = %+v", h)
	}

	// An anchor for another stream, then a registry that is gone: history falls
	// back to the cluster and says the records could not be read.
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "anchor": deliveryrecord.AnchorRef(e.host+"/x", "other")}})
	h, _ = e.d.History(context.Background(), "prod", "payments-prod")
	if h.Source != delivery.SourceStatus || len(h.Unreadable) != 1 || !strings.Contains(h.Note, "could not be read") {
		t.Fatalf("history with a foreign anchor = %+v", h)
	}
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream)}})
	e.srv.Close()
	h, _ = e.d.History(context.Background(), "prod", "payments-prod")
	if h.Source != delivery.SourceStatus || len(h.Unreadable) != 1 || !strings.Contains(h.Unreadable[0], "reading the records") {
		t.Fatalf("history with the registry down = %+v", h)
	}
	e.d.Records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("bad path") }
	h, _ = e.d.History(context.Background(), "prod", "payments-prod")
	if len(h.Unreadable) != 1 || !strings.Contains(h.Unreadable[0], "bad path") {
		t.Fatalf("history with no repository = %+v", h)
	}
	if _, err := e.d.History(context.Background(), "prod", "missing"); err == nil {
		t.Fatal("history of a missing Stage")
	}
}

func TestBlame(t *testing.T) {
	e := newRecordEnv(t,
		opFixture("live", shipSpec(dlB), map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(3),
			"pointer": map[string]any{"before": dlA, "after": dlB}, "verdicts": []any{map[string]any{"gate": "health", "result": "Pass"}},
			"approvals": []any{map[string]any{"name": "a1", "by": "dave", "decision": "approve"}}}))
	e.twoRecords()
	ctx := context.Background()
	b, err := e.d.Blame(ctx, "prod", "payments-prod", "")
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(b.Digest, b.Change.Operation, b.OnBehalfOf, b.IntentReason, b.Approvals, len(b.Verdicts), b.Source)
	if want := fmt.Sprint(dlA, "ship", "carol", "ship ship", []delivery.RecordApproval{{By: "alice", Decision: delivery.Approve}}, 1,
		delivery.SourceRecords); got != want {
		t.Fatalf("blame = %s, want %s", got, want)
	}
	// B was put in place by an operation not recorded yet: read from its status.
	b, _ = e.d.Blame(ctx, "prod", "payments-prod", dlB)
	if b.Change == nil || b.Change.Operation != "live" || b.Change.Record != "" || len(b.Approvals) != 1 || b.Approvals[0].By != "dave" ||
		b.Completeness != "partial" || len(b.Unsigned) == 0 || !strings.Contains(b.Note, "unsigned status") {
		t.Fatalf("blame of an unrecorded change = %+v", b)
	}
	if b, _ := e.d.Blame(ctx, "prod", "payments-prod", ""); b.Completeness != "complete" || len(b.Unsigned) != 0 {
		t.Fatalf("blame from a signed record = %+v", b)
	}
}

func TestBlameOfAVersionNeverMovedTo(t *testing.T) {
	e := newRecordEnv(t)
	e.twoRecords()
	ctx := context.Background()
	b, _ := e.d.Blame(ctx, "prod", "payments-prod", dlE)
	if b.Change != nil || !strings.Contains(b.Note, "no change in the stream") {
		t.Fatalf("blame of a version never moved to = %+v", b)
	}
	e.d.Trust = nil
	b, _ = e.d.Blame(ctx, "prod", "payments-prod", dlE)
	if !strings.Contains(b.Note, "unknown, not nobody") {
		t.Fatalf("blame on a partial history = %+v", b)
	}
	if _, err := e.d.Blame(ctx, "prod", "missing", ""); err == nil {
		t.Fatal("blame of a missing Stage")
	}
}

// unrecordableEnv: live moved the Stage to B, but its approval was gone
// before the recorder ran, so its record says only that it could not be
// recorded. It returns that record's id.
func unrecordableEnv(t *testing.T) (*recordEnv, string) {
	t.Helper()
	e := newRecordEnv(t,
		opFixture("live", shipSpec(dlB), map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(3),
			"pointer":   map[string]any{"before": dlA, "after": dlB},
			"approvals": []any{map[string]any{"name": "a1", "by": "dave", "decision": "approve"}}}))
	_, r2 := e.twoRecords()
	// The approval was gone before the recorder ran: the record of live says
	// only that it could not be recorded, and the Stage runs B.
	r3 := e.attach("delivery/streams", e.anchor, delivery.RecordPredicate{Kind: delivery.RecordUnrecordable,
		Intent: delivery.RecordIntent{UID: "uid-live", Name: "live"}, Chain: delivery.Chain{Stream: recStream, Seq: 3, Parent: r2},
		Unrecordable: &delivery.Refusal{Check: delivery.CheckApproval, Message: "approval a1 is gone"}})
	e.setStream(map[string]any{"pointer": dlB, "stream": map[string]any{"id": recStream, "seq": int64(3), "recordedSeq": int64(3),
		"head": r3, "anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream)}})
	return e, r3
}

func TestBlameOfAnUnrecordableMove(t *testing.T) {
	e, r3 := unrecordableEnv(t)
	ctx := context.Background()
	h, _ := e.d.History(ctx, "prod", "payments-prod")
	last := h.Entries[len(h.Entries)-1]
	if len(h.Entries) != 3 || last.Record != r3 || last.To != dlB || last.Pointer == nil || !strings.HasPrefix(last.Reason, "Unrecordable") {
		t.Fatalf("history = %+v", h.Entries)
	}
	b, _ := e.d.Blame(ctx, "prod", "payments-prod", "")
	// The record refused to vouch for the move: what blame read from the
	// Operation is labelled unsigned, and the answer is not complete.
	if b.Change == nil || b.Change.Operation != "live" || len(b.Approvals) != 1 || b.Approvals[0].By != "dave" ||
		b.Completeness != "partial" || len(b.Unsigned) == 0 || !strings.Contains(b.Note, "could not vouch for it") {
		t.Fatalf("blame = %+v", b)
	}
}

// TestBlameOfAnUnrecordableMoveWithItsOperationGone: what the unrecordable
// change moved is unknown, never nobody.
func TestBlameOfAnUnrecordableMoveWithItsOperationGone(t *testing.T) {
	e, _ := unrecordableEnv(t)
	ctx := context.Background()
	if err := e.c.Resource(gvrOperations).Namespace("prod").Delete(ctx, "live", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	b, _ := e.d.Blame(ctx, "prod", "payments-prod", "")
	if b.Change != nil || b.Completeness != "partial" || !strings.Contains(b.Note, "unrecordable record hides") {
		t.Fatalf("blame with the Operation gone = %+v", b)
	}
}

func TestHistorySaysWhetherItWasVerified(t *testing.T) {
	e := newRecordEnv(t)
	e.twoRecords()
	if h, _ := e.d.History(context.Background(), "prod", "payments-prod"); !h.Verified {
		t.Fatal("a history read with a trust config is not marked verified")
	}
	e.d.Trust = nil
	if h, _ := e.d.History(context.Background(), "prod", "payments-prod"); h.Verified {
		t.Fatal("a history read with no trust config is marked verified")
	}
}

func TestBlameFromStatusAndEmptyStage(t *testing.T) {
	d, c, _ := deliveryFixture(t, "alice", nil, nil,
		opFixture("ship", map[string]any{"type": delivery.TypePromote, "stage": "payments-prod", "params": map[string]any{"to": dlA},
			"actor": map[string]any{"user": "bot"}, "onBehalfOf": map[string]any{"user": "carol"}, "reason": "why"},
			map[string]any{"phase": "Concluded", "seq": int64(1), "pointer": map[string]any{"after": dlA}}))
	b, err := d.Blame(context.Background(), "prod", "payments-prod", "")
	if err != nil || b.Change == nil || b.OnBehalfOf != "carol" || b.IntentReason != "why" || b.Source != delivery.SourceStatus {
		t.Fatalf("blame = %+v %v", b, err)
	}
	ri := c.Resource(gvrStages).Namespace("prod")
	u, _ := ri.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	unstructured.RemoveNestedField(u.Object, "status", "pointer")
	_, _ = ri.Update(context.Background(), u, metav1.UpdateOptions{})
	if b, _ := d.Blame(context.Background(), "prod", "payments-prod", ""); b.Note != "the Stage runs nothing yet" {
		t.Fatalf("blame of an empty Stage = %+v", b)
	}
}

func TestUndo(t *testing.T) {
	applied := func(before, after string) map[string]any {
		return map[string]any{"phase": "Concluded", "outcome": "Succeeded", "pointer": map[string]any{"before": before, "after": after}}
	}
	e := newRecordEnv(t,
		opFixture("ship", shipSpec(dlA), applied(dlC, dlA)),
		opFixture("older", shipSpec(dlC), applied(dlB, dlC)),
		opFixture("first", shipSpec(dlC), applied("", dlC)),
		opFixture("same", shipSpec(dlE), applied(dlA, dlE)),
		opFixture("refused", shipSpec(dlB), map[string]any{"phase": "Concluded", "outcome": "Rejected"}))
	e.twoRecords()
	ctx := context.Background()
	u, err := e.d.Undo(ctx, "prod", "ship", "")
	if err != nil || u.Kind != "rollback" || u.Plan == nil || !strings.Contains(u.Command, "--type rollback --undoes ship") ||
		!strings.Contains(u.Command, "--intent-digest "+u.Plan.IntentDigest) {
		t.Fatalf("undo of the running change = %+v %v", u, err)
	}
	u, err = e.d.Undo(ctx, "prod", "older", "")
	if err != nil || u.Kind != "promote" || !strings.Contains(u.Command, "--to "+dlB) || !strings.Contains(u.Note, "every gate runs") {
		t.Fatalf("undo of an older change = %+v %v", u, err)
	}
	for name, want := range map[string]string{"first": "first version", "same": "already runs", "refused": "moved nothing"} {
		if u, err := e.d.Undo(ctx, "prod", name, ""); err != nil || u.Kind != "none" || !strings.Contains(u.Note, want) {
			t.Errorf("undo of %s = %+v %v", name, u, err)
		}
	}
}

func TestUndoRefusals(t *testing.T) {
	applied := map[string]any{"phase": "Concluded", "outcome": "Succeeded", "pointer": map[string]any{"before": dlC, "after": dlB}}
	e := newRecordEnv(t, opFixture("older", shipSpec(dlC), applied))
	e.twoRecords()
	ctx := context.Background()
	// boot is gone from the cluster; its record says what it did.
	u, err := e.d.Undo(ctx, "prod", "boot", "payments-prod")
	if err != nil || u.Kind != "none" || !strings.Contains(u.Note, "first version") {
		t.Fatalf("undo of a recorded, deleted operation = %+v %v", u, err)
	}
	// ship is the change the Stage runs, and its Operation is gone: going
	// back is a promote, and the note says the Stage did not change.
	u, err = e.d.Undo(ctx, "prod", "ship", "payments-prod")
	if err != nil || u.Kind != "promote" || !strings.Contains(u.Note, "still runs this change") || !strings.Contains(u.Command, "--to "+dlC) {
		t.Fatalf("undo of the running change whose Operation is gone = %+v %v", u, err)
	}
	// A later rollback put the same version back: the Stage runs that
	// change, not ship's, so the note must not say it still runs ship.
	e.setStream(map[string]any{"lastOp": "uid-rollback"})
	u, err = e.d.Undo(ctx, "prod", "ship", "payments-prod")
	if err != nil || u.Kind != "promote" || strings.Contains(u.Note, "still runs this change") || !strings.Contains(u.Note, "changed since") {
		t.Fatalf("undo of a deleted change a later one replaced with the same version = %+v %v", u, err)
	}
	e.setStream(map[string]any{"lastOp": "uid-ship"})
	_, err = e.d.Undo(ctx, "prod", "boot", "")
	wantErrContaining(t, err, "name its Stage")
	_, err = e.d.Undo(ctx, "prod", "nowhere", "payments-prod")
	wantErrContaining(t, err, "no record of Stage payments-prod names it")
	_, err = e.d.Undo(ctx, "prod", "nowhere", "missing")
	wantErrContaining(t, err, "missing")

	gone := opFixture("orphan", map[string]any{"type": delivery.TypePromote, "stage": "missing"}, applied)
	_, _ = e.c.Resource(gvrOperations).Namespace("prod").Create(ctx, gone.(*unstructured.Unstructured), metav1.CreateOptions{})
	_, err = e.d.Undo(ctx, "prod", "orphan", "")
	wantErrContaining(t, err, "missing")
	e.c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("who knows")
	})
	_, err = e.d.Undo(ctx, "prod", "older", "")
	wantErrContaining(t, err, "who knows")
}

func TestVerifyRecords(t *testing.T) {
	e := newRecordEnv(t)
	ctx := context.Background()
	_, err := e.d.VerifyRecords(ctx, "prod", "payments-prod")
	wantErrContaining(t, err, "has no records")
	_, r2 := e.twoRecords()
	v, err := e.d.VerifyRecords(ctx, "prod", "payments-prod")
	if err != nil || !v.OK || v.Head != r2 || len(v.Records) != 2 || v.Records[1].Operation != "ship" || v.Records[0].Manifest == "" ||
		!strings.Contains(v.Cosign, "cosign verify-blob-attestation") || !strings.Contains(v.Cosign, v.Records[0].Manifest) {
		t.Fatalf("verification = %+v %v", v, err)
	}
	// A record signed by a key the trust config does not list.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, _ := delivery.PublicKeyPEM(other)
	trust, _ := delivery.ParseTrust([]byte(pemKey))
	e.d.Trust = &trust
	v, _ = e.d.VerifyRecords(ctx, "prod", "payments-prod")
	if v.OK || len(v.Unreadable) != 2 || len(v.Records) != 0 || !strings.Contains(v.Cosign, "<record manifest>") {
		t.Fatalf("verification under another key = %+v", v)
	}
	e.srv.Close()
	_, err = e.d.VerifyRecords(ctx, "prod", "payments-prod")
	wantErrContaining(t, err, "reading the records")
	_, err = e.d.VerifyRecords(ctx, "prod", "missing")
	wantErrContaining(t, err, "missing")
	e.d.Trust = nil
	_, err = e.d.VerifyRecords(ctx, "prod", "payments-prod")
	wantErrContaining(t, err, "needs a trust config")
}

// precedentEnv is a recordEnv whose apply repository holds one version, with
// six records of other Stages moving to it and one moving away from it.
func precedentEnv(t *testing.T) (*recordEnv, string) {
	t.Helper()
	e := newRecordEnv(t)
	e.twoRecords()
	ctx := context.Background()
	// The apply repository lives in the test registry, with one version in it.
	ri := e.c.Resource(gvrOCIRepository).Namespace("prod")
	u, _ := ri.Get(ctx, "payments", metav1.GetOptions{})
	_ = unstructured.SetNestedField(u.Object, "oci://"+e.host+"/apps/payments", "spec", "url")
	_, _ = ri.Update(ctx, u, metav1.UpdateOptions{})
	version := ocireferrer.Build("application/x-app", "application/x-app", []byte("v"), nil, nil)
	if err := ocireferrer.Push(ctx, e.repo("apps/payments"), version); err != nil {
		t.Fatal(err)
	}
	to := version.ManifestDesc.Digest.String()
	for i, outcome := range []delivery.Outcome{delivery.Failed, delivery.Succeeded, delivery.Failed, delivery.Succeeded, delivery.Failed, delivery.Failed} {
		p := move(int64(i+1), "", "t", dlA, to)
		p.Chain.Stream, p.Outcome.Result = "c/test/payments-test", outcome
		p.Time.Finished = "2026-10-2" + string(rune('0'+i)) + "T00:00:00Z"
		e.attach("apps/payments", version.ManifestDesc, p)
	}
	from := move(9, "", "f", to, dlB) // names the version as from, not to: not precedent
	e.attach("apps/payments", version.ManifestDesc, from)
	return e, to
}

func TestPlanPrecedent(t *testing.T) {
	e, to := precedentEnv(t)
	ctx := context.Background()
	plan, _, err := e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: to})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Precedent
	if p == nil || p.Completeness != "complete" || len(p.Entries) != precedentLimit || p.Entries[0].Finished != "2026-10-25T00:00:00Z" ||
		p.Entries[0].Stream != "c/test/payments-test" || p.Entries[0].Type != delivery.TypePromote {
		t.Fatalf("precedent = %+v", p)
	}

	e.d.Trust = nil
	plan, _, _ = e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: to})
	if plan.Precedent.Completeness != "partial" || !strings.Contains(plan.Precedent.Note, "no signature") {
		t.Fatalf("unverified precedent = %+v", plan.Precedent)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, _ := delivery.PublicKeyPEM(other)
	trust, _ := delivery.ParseTrust([]byte(pemKey))
	e.d.Trust = &trust
	plan, _, _ = e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: to})
	if plan.Precedent.Completeness != "partial" || !strings.Contains(plan.Precedent.Note, "could not be verified") {
		t.Fatalf("precedent under another key = %+v", plan.Precedent)
	}
}

func TestPlanPrecedentUnreadable(t *testing.T) {
	e, _ := precedentEnv(t)
	ctx := context.Background()
	ri := e.c.Resource(gvrOCIRepository).Namespace("prod")
	// A version the registry does not hold.
	plan, _, _ := e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlE})
	if !strings.Contains(plan.Precedent.Note, "could not be read") {
		t.Fatalf("precedent of a missing version = %+v", plan.Precedent)
	}
	e.d.Records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("bad path") }
	plan, _, _ = e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlE})
	if !strings.Contains(plan.Precedent.Note, "bad path") {
		t.Fatalf("precedent with no repository = %+v", plan.Precedent)
	}
	_ = ri.Delete(ctx, "payments", metav1.DeleteOptions{})
	plan, _, _ = e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", Type: delivery.TypeOverride, To: dlE})
	if plan.Precedent == nil || !strings.Contains(plan.Precedent.Note, "apply OCIRepository") {
		t.Fatalf("precedent with no apply repository = %+v", plan.Precedent)
	}
	e.d.Records = nil
	if plan, _, _ := e.d.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", Type: delivery.TypeOverride, To: dlE}); plan.Precedent != nil {
		t.Fatalf("precedent with no records = %+v", plan.Precedent)
	}
}

// edSigner signs records with key through sigstore-go.
func edSigner(key ed25519.PrivateKey) delivery.Signer {
	kp, _ := signing.FromPrivateKey(key)
	return signing.Keyed(kp, "")
}

// TestFleetAudit walks every Stage: complete only when every Stage was read
// and every answer is complete.
func TestFleetAudit(t *testing.T) {
	e := newRecordEnv(t)
	e.twoRecords()
	ctx := context.Background()
	hs, err := e.d.HistoryAll(ctx, "")
	if err != nil || len(hs.Histories) != 2 || hs.Completeness != "partial" || len(hs.Unreadable) != 0 {
		t.Fatalf("history of every Stage (payments-test has no records) = %+v %v", hs, err)
	}
	if hs, _ = e.d.HistoryAll(ctx, "prod"); len(hs.Histories) != 1 || hs.Histories[0].Namespace != "prod" || hs.Completeness != "complete" {
		t.Fatalf("history of every Stage in prod = %+v", hs)
	}
}

func TestFleetVerify(t *testing.T) {
	e := newRecordEnv(t)
	e.twoRecords()
	ctx := context.Background()
	vs, err := e.d.VerifyAll(ctx, "prod")
	if err != nil || !vs.OK || len(vs.Streams) != 1 || vs.Completeness != "complete" || vs.Trust != e.d.Trust.Digest() {
		t.Fatalf("verify of every Stage in prod = %+v %v", vs, err)
	}
	vs, _ = e.d.VerifyAll(ctx, "")
	if vs.OK || len(vs.Streams) != 1 || vs.Completeness != "partial" || len(vs.Unreadable) != 1 ||
		!strings.Contains(vs.Unreadable[0], "test/payments-test: ") || !strings.Contains(vs.Unreadable[0], "has no records") {
		t.Fatalf("a Stage with no records does not verify: %+v", vs)
	}
}

func TestFleetAuditUnreadable(t *testing.T) {
	e := newRecordEnv(t)
	ctx := context.Background()
	e.c.PrependReactor("get", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("gone")
	})
	if hs, _ := e.d.HistoryAll(ctx, ""); len(hs.Histories) != 0 || len(hs.Unreadable) != 2 || hs.Completeness != "partial" ||
		!strings.Contains(hs.Note, "may still have happened") {
		t.Fatalf("unreadable Stages = %+v", hs)
	}
	e.c.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	_, err := e.d.HistoryAll(ctx, "")
	wantErrContaining(t, err, "forbidden")
	_, err = e.d.VerifyAll(ctx, "")
	wantErrContaining(t, err, "forbidden")
	e.d.Trust = nil
	_, err = e.d.VerifyAll(ctx, "")
	wantErrContaining(t, err, "needs a trust config")
}

// TestFleetAuditPerNamespace: a caller who may not list Stages cluster-wide
// gets every namespace it can read, and the answer names the others and is
// partial.
func TestFleetAuditPerNamespace(t *testing.T) {
	ctx := context.Background()
	e := newRecordEnv(t, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil))
	e.twoRecords()
	e.c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if ns := a.GetNamespace(); ns == "" || ns == "test" {
			return true, nil, apierrors.NewForbidden(gvrStages.GroupResource(), "", errors.New("no"))
		}
		return false, nil, nil
	})
	hs, err := e.d.HistoryAll(ctx, "")
	if err != nil || len(hs.Histories) != 1 || hs.Histories[0].Namespace != "prod" || hs.Completeness != "partial" ||
		len(hs.Unreadable) != 1 || !strings.Contains(hs.Unreadable[0], "in test") || !strings.Contains(hs.Note, "namespaces listed as unreadable") {
		t.Fatalf("history with Stages listable per namespace only = %+v %v", hs, err)
	}
	vs, err := e.d.VerifyAll(ctx, "")
	if err != nil || vs.OK || len(vs.Streams) != 1 || vs.Completeness != "partial" || len(vs.Unreadable) != 1 {
		t.Fatalf("verify with Stages listable per namespace only = %+v %v", vs, err)
	}
	if _, err := e.d.HistoryAll(ctx, "test"); !apierrors.IsForbidden(err) {
		t.Fatalf("a namespace named with -n is not walked around: %v", err)
	}
	e.c.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(gvrNamespaces.GroupResource(), "", errors.New("no"))
	})
	_, err = e.d.HistoryAll(ctx, "")
	wantErrContaining(t, err, "name one with -n")
}

func TestAuditIsBounded(t *testing.T) {
	stages := make([]delivery.StageObject, auditLimit+1)
	out, unreadable := audit(context.Background(), stages, func(ctx context.Context, _ delivery.StageObject) (*int, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("no time limit")
		}
		return new(int), nil
	})
	if len(out) != auditLimit || len(unreadable) != 1 || !strings.Contains(unreadable[0], "1 Stages were not read") {
		t.Fatalf("out = %d, unreadable = %v", len(out), unreadable)
	}
}
