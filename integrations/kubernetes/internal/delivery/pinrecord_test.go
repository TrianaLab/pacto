/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// trustConcluded is a concluded trust operation for otherID that alice's
// Approval a1 answered; the Approval's signed record is at ref.
func (re *recEnv) trustConcluded(outcome delivery.Outcome, ref string) *dv1.Operation {
	re.t.Helper()
	name := delivery.TrustOperationName("payments-prod", otherID)
	o := re.concluded(name, 1, outcome, func(o *dv1.Operation) {
		o.Spec.Type, o.Spec.Facet = delivery.TypeTrust, delivery.FacetTrust
		o.Spec.Params = delivery.Params{To: re.reg.to, Publisher: &otherID}
		o.Spec.Actor.User = promoterSA
	})
	re.approve(o, "a1", "alice", delivery.Approve)
	a := re.getApproval("a1")
	a.Status.Record = &delivery.ApprovalRecordRef{Digest: "sha256:1", Ref: ref}
	must(re.t, re.c.Status().Update(context.Background(), a))
	o.Status.Approvals = []delivery.ApprovalRecord{{Name: "a1", By: "alice", Decision: delivery.Approve}}
	must(re.t, re.c.Status().Update(context.Background(), o))
	return o
}

// recorderApproval pushes alice's approval of op as the recorder signs it
// with key, on the Stage's stream in the streams repository, and returns
// where it is.
func (re *recEnv) recorderApproval(key ed25519.PrivateKey, op *dv1.Operation, edit ...func(*delivery.ApprovalStatement)) string {
	re.t.Helper()
	obj := delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: op.Name, Namespace: op.Namespace, UID: string(op.UID)}, Spec: op.Spec}
	st := delivery.NewApprovalRecord(delivery.RecordStage{Namespace: ns, Name: "payments-prod"}, deliveryrecord.Anchor(recStream).ManifestDesc.Digest.String(), obj,
		delivery.ApprovalSpec{IntentDigest: op.Spec.IntentDigest, Decision: delivery.Approve},
		delivery.ApprovalSigner{Kind: delivery.SignerRecorder, OnBehalfOf: &delivery.ApprovalBehalf{User: "alice", Verified: delivery.VerifiedByAdmission}},
		"2026-10-20T10:00:00Z")
	for _, f := range edit {
		f(&st)
	}
	b, _, err := delivery.SignApproval(context.Background(), edSigner(key), st)
	must(re.t, err)
	return pushApproval(re.t, re.rec.Store.Streams, recStream, op, b)
}

func pinSignerYAML(t *testing.T, pub ed25519.PublicKey, streams string) []byte {
	t.Helper()
	pem, err := delivery.PublicKeyPEM(pub)
	must(t, err)
	return []byte("keys:\n  - name: recorder\n    publicKey: |\n      " + strings.ReplaceAll(strings.TrimSpace(pem), "\n", "\n      ") + "\n" + streams)
}

// readPins reads the pins of payments as the promoter does, with pinSigner
// and recordTrust.
func (re *recEnv) readPins(pinSigner, recordTrust []byte) deliveryrecord.PinListing {
	re.t.Helper()
	verify, ok, err := PinVerifier(pinSigner, recordTrust)
	if err != nil || !ok {
		re.t.Fatalf("pin verifier: %v %v", ok, err)
	}
	l, err := PinReader(re.rec.Store.Streams, ocireferrer.Options{}, verify, delivery.ClassPolicy{})(context.Background(), ns, delivery.ServiceRef{Name: "payments"})
	must(re.t, err)
	return l
}

// TestTrustOperationApprovedWritesPin: a succeeded trust operation gets its
// pin, naming the approver and citing the approval's record, before its
// operation record; the promoter's pin reader accepts it.
func TestTrustOperationApprovedWritesPin(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	name := delivery.TrustOperationName("payments-prod", otherID)
	ref := re.recorderApproval(re.key, &dv1.Operation{ObjectMeta: objMeta(name), Spec: delivery.OperationSpec{}})
	o := re.trustConcluded(delivery.Succeeded, ref)
	re.reconcile()
	if recs := re.records(); len(recs) != 1 || re.getOp(o.Name).Status.Record == nil {
		t.Fatalf("the operation record: %d", len(recs))
	}
	l := re.readPins(pinSignerYAML(t, re.key.Public().(ed25519.PublicKey), "    streams: ['pins/*/*']\n"), nil)
	if len(l.Pins) != 1 || len(l.Unreadable) != 0 {
		t.Fatalf("pins %+v", l)
	}
	p := l.Pins[0].Pin
	if p.Namespace != ns || p.Approver != "alice" || p.Approval != ref || p.Operation != string(o.UID) || p.Subject != otherSub || p.At != "2026-10-20T10:00:00Z" {
		t.Fatalf("pin %+v", p)
	}
}

func objMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID("uid-" + name)}
}

// TestTrustOperationRejectedWritesNoPin: only a succeeded trust operation
// is pinned; one whose Approval is gone records without a pin; a signer or
// registry failure holds its record back.
func TestTrustOperationRejectedWritesNoPin(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.trustConcluded(delivery.Rejected, "")
	re.reconcile()
	pub := re.key.Public().(ed25519.PublicKey)
	if l := re.readPins(pinSignerYAML(t, pub, ""), nil); len(l.Pins) != 0 || len(re.records()) != 1 {
		t.Fatalf("pins %+v", l)
	}

	re = newRecEnv(t)
	re.stage(1)
	re.trustConcluded(delivery.Succeeded, "")
	must(t, re.c.Delete(context.Background(), re.getApproval("a1")))
	re.reconcile()
	if l := re.readPins(pinSignerYAML(t, pub, ""), nil); len(l.Pins) != 0 || len(re.records()) != 1 {
		t.Fatalf("an Approval gone: %+v", l)
	}

	re = newRecEnv(t)
	re.stage(1)
	re.trustConcluded(delivery.Succeeded, "")
	re.rec.Signer = failingSigner{}
	if _, err := re.rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf("payments-prod")}); err != nil {
		t.Fatal(err) // the recorder reports a store failure on the Stage, and backs off
	}
	if s := re.getStage(); condition(s).Status == statusTrue {
		t.Fatalf("a failed pin left the store ready: %+v", condition(s))
	}
	re.rec.Signer = edSigner(re.key)
	re.setFail("get/Approval", true)
	if _, err := re.rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf("payments-prod")}); err != nil {
		t.Fatal(err)
	}
	if recs := re.records(); len(recs) != 0 {
		t.Fatalf("recorded past an unreadable Approval: %d", len(recs))
	}
	re.setFail("get/Approval", false)
	re.reg.fault = func(r *http.Request) bool {
		return strings.Contains(r.URL.Path, "/manifests/"+deliveryrecord.AnchorTag(delivery.PinStream(delivery.ServiceRef{Name: "payments"})))
	}
	if _, err := re.rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf("payments-prod")}); err != nil {
		t.Fatal(err)
	}
	if recs := re.records(); len(recs) != 0 {
		t.Fatalf("recorded past an unwritable pin stream: %d", len(recs))
	}
}

// TestPinWrongSignerIgnored, TestPinRemoteRecorderKeyIgnored and
// TestPinEmptyStreamsKeyIgnored: a pin verifies only under this cluster's
// recorder key or a recorder.trust key that lists an explicit pins/ stream.
func TestPinWrongSignerIgnored(t *testing.T) {
	re, pub := pinnedRecEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if l := re.readPins(pinSignerYAML(t, other.Public().(ed25519.PublicKey), ""), nil); len(l.Pins) != 0 || len(l.Unreadable) != 1 {
		t.Fatalf("another key: %+v", l)
	}
	if l := re.readPins(pinSignerYAML(t, pub, ""), nil); len(l.Pins) != 1 {
		t.Fatalf("the recorder's key: %+v", l)
	}
}

func TestPinRemoteRecorderKeyIgnored(t *testing.T) {
	re, pub := pinnedRecEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	remote := pinSignerYAML(t, pub, "    streams: ['other/*/*']\n")
	if _, ok, err := PinVerifier(nil, remote); ok || err != nil {
		t.Fatalf("a remote recorder key is no pin key: %v %v", ok, err)
	}
	l := re.readPins(pinSignerYAML(t, other.Public().(ed25519.PublicKey), ""), remote)
	if len(l.Pins) != 0 {
		t.Fatalf("a remote recorder key verified a pin: %+v", l)
	}
	if l := re.readPins(nil, pinSignerYAML(t, pub, "    streams: ['pins/*/*']\n")); len(l.Pins) != 1 {
		t.Fatalf("a recorder.trust key scoped to pins/: %+v", l)
	}
}

func TestPinEmptyStreamsKeyIgnored(t *testing.T) {
	re, pub := pinnedRecEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if l := re.readPins(pinSignerYAML(t, other.Public().(ed25519.PublicKey), ""), pinSignerYAML(t, pub, "")); len(l.Pins) != 0 {
		t.Fatalf("a recorder.trust key for every stream verified a pin: %+v", l)
	}
	if _, _, err := PinVerifier([]byte("nope: 1"), nil); err == nil {
		t.Fatal("an unreadable pin signer was accepted")
	}
	if _, ok, err := PinVerifier(nil, nil); ok || err != nil {
		t.Fatalf("no keys: %v %v", ok, err)
	}
}

// pinnedRecEnv is a recorder env whose recorder pinned otherID for payments
// on alice's recorder-signed approval, and the recorder's public key.
func pinnedRecEnv(t *testing.T) (*recEnv, ed25519.PublicKey) {
	t.Helper()
	re := newRecEnv(t)
	re.stage(1)
	name := delivery.TrustOperationName("payments-prod", otherID)
	re.trustConcluded(delivery.Succeeded, re.recorderApproval(re.key, &dv1.Operation{ObjectMeta: objMeta(name)}))
	re.reconcile()
	return re, re.key.Public().(ed25519.PublicKey)
}

// TestPinApprovalAtChecks: the approval a pin cites is fetched and
// verified: a recorder-signed one under the pin keys, naming whom it was
// signed for, a person's keyless one against the identity policy; anything
// else leaves the pin unreadable.
func TestPinApprovalAtChecks(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	pub := re.key.Public().(ed25519.PublicKey)
	verify, _, _ := PinVerifier(pinSignerYAML(t, pub, ""), nil)
	v := func(b []byte) error { return verify(delivery.PinStream(delivery.ServiceRef{Name: "payments"}), 0, b) }
	name := delivery.TrustOperationName("payments-prod", otherID)
	op := &dv1.Operation{ObjectMeta: objMeta(name)}
	check := approvalAt(ocireferrer.Options{}, delivery.ClassPolicy{}, v)

	if _, who, err := check(context.Background(), re.recorderApproval(re.key, op)); err != nil || who != "alice" {
		t.Fatalf("recorder-signed: %s %v", who, err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := check(context.Background(), re.recorderApproval(other, op)); err == nil {
		t.Fatal("an approval another key signed verified")
	}
	nobody := re.recorderApproval(re.key, op, func(st *delivery.ApprovalStatement) { st.Predicate.Signer.OnBehalfOf = nil })
	if _, _, err := check(context.Background(), nobody); err == nil || !strings.Contains(err.Error(), "for nobody") {
		t.Fatalf("signed for nobody: %v", err)
	}
	if _, _, err := check(context.Background(), re.reg.host+"/delivery/streams@sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("a missing record verified")
	}
	if _, _, err := check(context.Background(), "UPPER/bad@sha256:0"); err == nil {
		t.Fatal("an unopenable repository verified")
	}
	ss := sigtest.New(t)
	keyless := pushApproval(t, re.rec.Store.Streams, recStream, op, signedApprovalOf(t, ss, alice, op, recStream))
	if _, who, err := approvalAt(ocireferrer.Options{}, withSigners(delivery.ClassPolicy{}, ss), v)(context.Background(), keyless); err != nil || who != aliceUser {
		t.Fatalf("keyless: %s %v", who, err)
	}
	if _, _, err := check(context.Background(), keyless); err == nil {
		t.Fatal("a keyless approval verified with no approver signer")
	}
	if _, err := PinReader("bad ref::", ocireferrer.Options{}, verify, delivery.ClassPolicy{})(context.Background(), ns, delivery.ServiceRef{Name: "payments"}); err == nil {
		t.Fatal("an unopenable streams repository read")
	}
}

// TestPinPushedOnce: a retry of a trust operation whose pin was pushed but
// whose operation record was not pushes no second pin.
func TestPinPushedOnce(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	name := delivery.TrustOperationName("payments-prod", otherID)
	o := re.trustConcluded(delivery.Succeeded, re.recorderApproval(re.key, &dv1.Operation{ObjectMeta: objMeta(name), Spec: delivery.OperationSpec{}}))
	stage := &dv1.Stage{}
	must(t, re.c.Get(context.Background(), keyOf("payments-prod"), stage))
	w := &writer{Recorder: re.rec, stage: stage}
	must(t, w.pin(context.Background(), o))
	var puts atomic.Int32
	re.reg.fault = func(r *http.Request) bool {
		if r.Method == http.MethodPut {
			puts.Add(1)
		}
		return false
	}
	must(t, (&writer{Recorder: re.rec, stage: stage}).pin(context.Background(), o))
	if puts.Load() != 0 {
		t.Fatalf("a retry pushed %d writes", puts.Load())
	}
	// Another process pushes it again, and a push that fails is an error.
	re.rec.pinned = nil
	re.reg.fault = func(r *http.Request) bool { return r.Method == http.MethodPut }
	if err := w.pin(context.Background(), o); err == nil {
		t.Fatal("a failed pin push was not an error")
	}
	if l := re.readPins(pinSignerYAML(t, re.key.Public().(ed25519.PublicKey), "    streams: ['pins/*/*']\n"), nil); len(l.Pins) != 1 {
		t.Fatalf("pins %+v", l)
	}
}

// TestPinFromOtherNamespaceIgnored: a trust operation confirmed on prod's
// Stage of payments never lets a Stage of payments in another namespace
// accept the identity: its pin reader reads no pin.
func TestPinFromOtherNamespaceIgnored(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	name := delivery.TrustOperationName("payments-prod", otherID)
	re.trustConcluded(delivery.Succeeded, re.recorderApproval(re.key, &dv1.Operation{ObjectMeta: objMeta(name), Spec: delivery.OperationSpec{}}))
	re.reconcile()
	verify, _, err := PinVerifier(pinSignerYAML(t, re.key.Public().(ed25519.PublicKey), "    streams: ['pins/*/*']\n"), nil)
	must(t, err)
	read := PinReader(re.rec.Store.Streams, ocireferrer.Options{}, verify, delivery.ClassPolicy{})
	if l, err := read(context.Background(), "team-x", delivery.ServiceRef{Name: "payments"}); err != nil || len(l.Pins) != 0 {
		t.Fatalf("team-x read prod's pin: %+v %v", l, err)
	}
	if l, err := read(context.Background(), ns, delivery.ServiceRef{Name: "payments"}); err != nil || len(l.Pins) != 1 {
		t.Fatalf("prod lost its pin: %+v %v", l, err)
	}
}
