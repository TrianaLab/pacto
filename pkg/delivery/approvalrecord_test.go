package delivery

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var recStage = RecordStage{Cluster: "6c0d", Namespace: "billing", Name: "payments-prod", UID: "88f1"}

func recorderSigner(user string) ApprovalSigner {
	return ApprovalSigner{Kind: SignerRecorder, OnBehalfOf: &ApprovalBehalf{User: user, Verified: VerifiedByAdmission}}
}

// TestApprovalRecordRoundTrip: an approval record signs and opens back to
// the same statement, bound to the operation's uid and intent digest.
func TestApprovalRecordRoundTrip(t *testing.T) {
	op := recordInput().Operation
	op.Spec.CorrelationID = "corr-7"
	a := ApprovalSpec{Operation: "ship", OperationUID: "3b1e", IntentDigest: "sha256:aa", Decision: Approve, Note: "ok", By: "dana"}
	st := NewApprovalRecord(recStage, rAnc, op, a, recorderSigner("dana"), "2026-10-19T10:01:50Z")
	b, id, err := SignApproval(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	var got ApprovalStatement
	payload, err := openStatement(b, PredicateApproval, &got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, st) || id != sha256Hex(payload) {
		t.Fatalf("opened %+v (id %s), want %+v", got, id, st)
	}
	p := got.Predicate
	if p.Operation.UID != "3b1e" || p.Operation.IntentDigest != "sha256:aa" || p.CorrelationID != "corr-7" ||
		len(got.Subject) != 1 || got.Subject[0].Name != "anchor" {
		t.Fatalf("approval record = %+v", got)
	}
	if _, err := openStatement(b, PredicateOperation, &Statement{}); err == nil {
		t.Fatal("an approval record opened as an operation record")
	}
	if _, _, err := SignApproval(t.Context(), failingSigner{}, st); err == nil {
		t.Fatal("a failing signer signed")
	}
}

// TestIntentRecordCarriesAdmissionActor: the intent record names the user
// admission bound, the asserted principal and where approvals go; an intent
// that looks like it holds a secret is recorded without params or reason.
func TestIntentRecordCarriesAdmissionActor(t *testing.T) {
	op := recordInput().Operation
	op.Status.Guarded, op.Status.ActorClassifiedBy = true, ClassByServiceAccount
	st := NewIntentRecord(recStage, rAnc, "reg/approvals@"+rAnc, op, "2026-10-19T10:00:00Z")
	p := st.Predicate
	for field, ok := range map[string]bool{
		"actor":      p.Actor.User == "system:serviceaccount:agents:release-bot" && p.Actor.Class == ClassAgent,
		"onBehalfOf": p.Actor.OnBehalfOf != nil && p.Actor.OnBehalfOf.Asserted,
		"guarded":    p.Guarded,
		"approvals":  p.Approvals == "reg/approvals@"+rAnc,
		"intent":     p.Intent.Params.To == rTo && p.Intent.Reason != "" && p.Intent.Stage == recStage,
		"subjects":   len(st.Subject) == 2 && st.Subject[1].Name == "to",
	} {
		if !ok {
			t.Errorf("intent record %s is wrong: %+v", field, st)
		}
	}
	b, _, err := SignIntent(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	var got IntentStatement
	if _, err := openStatement(b, PredicateIntent, &got); err != nil || !reflect.DeepEqual(got, st) {
		t.Fatalf("opened %+v, %v", got, err)
	}

	op.Spec.Reason, op.Spec.Params.To, op.Spec.OnBehalfOf = "password=hunter22", "v2", nil
	st = NewIntentRecord(recStage, rAnc, "", op, "2026-10-19T10:00:00Z")
	if st.Predicate.Intent.Params != nil || st.Predicate.Intent.Reason != "" || len(st.Subject) != 1 || st.Predicate.Actor.OnBehalfOf != nil {
		t.Fatalf("a secret-looking intent = %+v", st)
	}
}

// TestOperationRecordNamesApprovalRecords: the operation record names each
// approval's own record and who signed it.
func TestOperationRecordNamesApprovalRecords(t *testing.T) {
	in := recordInput()
	in.Operation.Status.Approvals[0].Record = "sha256:ab"
	in.Operation.Status.Approvals[0].Signer = &ApprovalSigner{Kind: SignerRecorder,
		OnBehalfOf: &ApprovalBehalf{User: "https://idp#u-0117", Verified: VerifiedByAdmission}}
	a := NewOperationRecord(in).Predicate.Approvals[0]
	if a.Record != "sha256:ab" || a.Signer.Kind != SignerRecorder || a.Signer.OnBehalfOf.Verified != VerifiedByAdmission {
		t.Fatalf("record approval = %+v", a)
	}
}

// TestOperationRecordWithoutApprovalRecordStillOpens: a record with neither
// record nor signer on its approvals opens, and marshals with no new key, so
// a recorder that writes no approval records writes the same bytes as before.
func TestOperationRecordWithoutApprovalRecordStillOpens(t *testing.T) {
	st := NewOperationRecord(recordInput())
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), `"record"`) || strings.Contains(string(raw), `"signer"`) {
		t.Fatalf("a record without approval records gained keys: %s", raw)
	}
	b, _, err := SignRecord(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenRecord(b, func(string, int64, []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

// TestOpenApprovalRefusesOtherPredicate: an approval record opens through
// its verifier, and an intent record, a refused signature or a broken bundle
// never opens as an approval.
func TestOpenApprovalRefusesOtherPredicate(t *testing.T) {
	op := recordInput().Operation
	a := ApprovalSpec{OperationUID: "3b1e", IntentDigest: "sha256:aa", Decision: Approve}
	st := NewApprovalRecord(recStage, rAnc, op, a, ApprovalSigner{Kind: SignerPerson}, "2026-10-19T10:01:50Z")
	b, id, _ := SignApproval(t.Context(), plainSigner{}, st)
	ok := func([]byte) error { return nil }
	if got, gotID, err := OpenApproval(b, ok); err != nil || gotID != id || !reflect.DeepEqual(got, st) {
		t.Fatalf("OpenApproval = %+v %s %v", got, gotID, err)
	}
	intent, _, _ := SignIntent(t.Context(), plainSigner{}, NewIntentRecord(recStage, rAnc, "", op, "2026-10-19T10:01:50Z"))
	if _, _, err := OpenApproval(intent, ok); err == nil {
		t.Fatal("an intent record opened as an approval")
	}
	if _, _, err := OpenApproval(b, func([]byte) error { return errors.New("no") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a refused signature opened: %v", err)
	}
}

// TestOpenIntentRefusesOtherPredicate: an intent record opens through its
// verifier as a record of its Stage's stream, and an approval record or a
// refused signature never opens as an intent.
func TestOpenIntentRefusesOtherPredicate(t *testing.T) {
	op := recordInput().Operation
	st := NewIntentRecord(recStage, rAnc, "reg/approvals@"+rAnc, op, "2026-10-19T10:00:00Z")
	b, id, _ := SignIntent(t.Context(), plainSigner{}, st)
	var stream string
	var seq int64 = -1
	ok := func(s string, n int64, _ []byte) error { stream, seq = s, n; return nil }
	if got, gotID, err := OpenIntent(b, ok); err != nil || gotID != id || !reflect.DeepEqual(got, st) || stream != recStage.Stream() || seq != 0 {
		t.Fatalf("OpenIntent = %+v %s %v (%s %d)", got, gotID, err, stream, seq)
	}
	approval, _, _ := SignApproval(t.Context(), plainSigner{}, NewApprovalRecord(recStage, rAnc, op, ApprovalSpec{}, ApprovalSigner{Kind: SignerPerson}, ""))
	if _, _, err := OpenIntent(approval, ok); err == nil {
		t.Fatal("an approval record opened as an intent")
	}
	if _, _, err := OpenIntent(b, func(string, int64, []byte) error { return errors.New("no") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a refused signature opened: %v", err)
	}
	if recStage.Stream() != recStage.Cluster+"/"+recStage.Namespace+"/"+recStage.Name {
		t.Fatal(recStage.Stream())
	}
}
