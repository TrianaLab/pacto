package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	rTo   = "sha256:9f2c000000000000000000000000000000000000000000000000000000000000"
	rFrom = "sha256:41ab000000000000000000000000000000000000000000000000000000000000"
	rCtr  = "sha256:7b10000000000000000000000000000000000000000000000000000000000000"
	rAnc  = "sha256:a1c0000000000000000000000000000000000000000000000000000000000000"
)

func recordInput() RecordInput {
	return RecordInput{
		ClusterID: "6c0d",
		Stage: StageObject{Metadata: ObjectMeta{Name: "payments-prod", Namespace: "billing", UID: "88f1"},
			Spec: StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "prod"}},
		Operation: OperationObject{
			Metadata: ObjectMeta{Name: "ship", UID: "3b1e"},
			Spec: OperationSpec{Type: TypePromote, Stage: "payments-prod", Facet: FacetPointer,
				Params: Params{From: rFrom, To: rTo}, IntentDigest: "sha256:aa", Reason: "ship idempotency keys",
				Actor: Actor{User: "system:serviceaccount:agents:release-bot"}, OnBehalfOf: &Principal{User: "https://idp#u-4821"}},
			Status: OperationStatus{Phase: PhaseConcluded, Outcome: Succeeded, Verification: Verified, ActorClass: ClassAgent,
				Verdicts:  []Verdict{{Gate: GateContract, Phase: "verify", Result: Pass, Citation: map[string]string{"evidenceRecord": "sha256:e3"}}},
				Approvals: []ApprovalRecord{{Name: "ok", By: "https://idp#u-0117", Decision: AcceptUnknown, AuthTime: "2026-10-19T10:01:50Z", Note: "additive"}},
				Pointer:   PointerChange{Before: rFrom, After: rTo},
				Times:     Times{Finished: "2026-10-19T10:12:40Z"}, Seq: 42},
		},
		Approvals: map[string]ApprovalSpec{"ok": {OperationUID: "3b1e", IntentDigest: "sha256:aa"}},
		Subjects:  RecordSubjects{To: rTo, From: rFrom, Contract: rCtr, Anchor: rAnc},
		Chain:     Chain{Stream: "6c0d/billing/payments-prod", Seq: 42, Parent: "sha256:71c2"},
	}
}

func TestNewOperationRecord(t *testing.T) {
	in := recordInput()
	in.Operation.Status.Guarded = true
	st := NewOperationRecord(in)
	if st.Type != StatementType || st.PredicateType != PredicateOperation || len(st.Subject) != 4 {
		t.Fatalf("statement = %+v", st)
	}
	if st.Subject[0].Name != "to" || st.Subject[0].Digest["sha256"] != strings.TrimPrefix(rTo, "sha256:") {
		t.Fatalf("subjects = %+v", st.Subject)
	}
	p := st.Predicate
	pt, _ := TypeByID(TypePromote)
	for field, ok := range map[string]bool{
		"kind":       p.Kind == RecordOperation,
		"type":       p.Type.Digest == pt.Digest(),
		"cluster":    p.Intent.Stage.Cluster == "6c0d",
		"params":     p.Intent.Params.To == rTo,
		"onBehalfOf": p.Actor.OnBehalfOf.Asserted,
		"approval":   p.Approvals[0].IntentDigest == "sha256:aa" && p.Approvals[0].Operation == "3b1e",
		"outcome":    p.Outcome.Pointer.After == rTo,
		"time":       p.Time.Finished == "2026-10-19T10:12:40Z",
		"policy":     p.Policy.Environment == "prod",
		"guarded":    p.Guarded,
	} {
		if !ok {
			t.Errorf("predicate %s is wrong: %+v", field, p)
		}
	}
	if c, m := CheckRecord(in, st); c != "" {
		t.Fatalf("a clean record failed %s: %s", c, m)
	}
	// Reproducible: the same input gives the same payload.
	a, _ := json.Marshal(st)
	b, _ := json.Marshal(NewOperationRecord(in))
	if string(a) != string(b) {
		t.Fatal("the record payload is not reproducible")
	}

	in.Operation.Spec.Type, in.Operation.Spec.OnBehalfOf = "delivery.pacto.trianalab.io/scale", nil
	in.Subjects = RecordSubjects{Anchor: rAnc}
	st = NewOperationRecord(in)
	if got := st.Predicate; got.Type.ID != "delivery.pacto.trianalab.io/scale" || got.Type.Digest != "" || got.Actor.OnBehalfOf != nil {
		t.Fatalf("unknown type record = %+v", st)
	}
	if len(st.Subject) != 1 {
		t.Fatalf("subjects = %+v", st.Subject)
	}
}

// TestRecordSaysWhetherItWasGuarded: a record says admission checked who
// asked and who approved only when the promoter found its policies enforcing.
func TestRecordSaysWhetherItWasGuarded(t *testing.T) {
	in := recordInput()
	if NewOperationRecord(in).Predicate.Guarded {
		t.Fatal("an unguarded operation's record says it was guarded")
	}
	in.Operation.Status.Guarded = true
	if !NewOperationRecord(in).Predicate.Guarded {
		t.Fatal("a guarded operation's record does not say so")
	}
}

func TestUnrecordableAndGap(t *testing.T) {
	in := recordInput()
	u := NewUnrecordable(in, CheckSensitive, "a secret")
	p := u.Predicate
	if p.Kind != RecordUnrecordable || p.Intent.Params != nil || p.Intent.Digest != "sha256:aa" || p.Intent.UID != "3b1e" ||
		p.Unrecordable.Check != CheckSensitive || p.Chain.Seq != 42 || len(u.Subject) != 1 || u.Subject[0].Name != "anchor" {
		t.Fatalf("unrecordable = %+v", u)
	}
	g := NewGap("6c0d", in.Stage.Metadata, 7, Chain{Stream: "s", Seq: 43, Parent: "sha256:p"}, rAnc, "2026-10-20T00:00:00Z")
	if g.Predicate.Kind != RecordGap || g.Predicate.Gap.Seq != 7 || g.Predicate.Chain.Seq != 43 || g.Predicate.Intent.Stage.Name != "payments-prod" {
		t.Fatalf("gap = %+v", g)
	}
}

func TestCheckRecord(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*RecordInput)
		check string
	}{
		{"no seq", func(in *RecordInput) { in.Chain.Seq = 0 }, CheckSchema},
		{"no uid", func(in *RecordInput) { in.Operation.Metadata.UID = "" }, CheckSchema},
		{"no finished time", func(in *RecordInput) { in.Operation.Status.Times.Finished = "" }, CheckSchema},
		{"no outcome", func(in *RecordInput) { in.Operation.Status.Outcome = "" }, CheckSchema},
		{"approval gone", func(in *RecordInput) { in.Approvals = nil }, CheckApproval},
		{"approval of another intent", func(in *RecordInput) {
			in.Approvals["ok"] = ApprovalSpec{OperationUID: "3b1e", IntentDigest: "sha256:bb"}
		}, CheckApproval},
		{"private key", func(in *RecordInput) { in.Operation.Spec.Reason = "-----BEGIN OPENSSH PRIVATE KEY-----" }, CheckSensitive},
		{"bearer", func(in *RecordInput) { in.Operation.Spec.Reason = "Bearer abcdefghijklmnopqrstuvwxyz" }, CheckSensitive},
		{"jwt", func(in *RecordInput) { in.Operation.Spec.Reason = "eyJhbGciOiJI.eyJzdWIiOiIx.c2lnbmF0dXJl" }, CheckSensitive},
		{"aws key", func(in *RecordInput) { in.Operation.Status.Message = "AKIAABCDEFGHIJKLMNOP" }, CheckSensitive},
		{"github token", func(in *RecordInput) { in.Operation.Spec.Reason = "ghp_" + strings.Repeat("a", 36) }, CheckSensitive},
		{"password pair", func(in *RecordInput) { in.Operation.Spec.Reason = "password=hunter22" }, CheckSensitive},
		{"gate param", func(in *RecordInput) {
			in.Stage.Spec.Gates.Verify.Checks = []GateCheck{{Name: "m", Provider: "mira", Params: map[string]string{"apiKey": "s3cr3tvalue"}}}
		}, CheckSensitive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := recordInput()
			tc.edit(&in)
			if c, m := CheckRecord(in, NewOperationRecord(in)); c != tc.check || m == "" {
				t.Fatalf("check = %q (%s), want %q", c, m, tc.check)
			}
		})
	}
	in := recordInput()
	st := NewOperationRecord(in)
	st.Subject = nil
	if c, _ := CheckRecord(in, st); c != CheckSchema {
		t.Fatalf("no subject passed: %q", c)
	}
	in.Operation.Spec.Reason = "the bearer of bad news, see the token budget"
	if c, m := CheckRecord(in, NewOperationRecord(in)); c != "" {
		t.Fatalf("plain words failed %s: %s", c, m)
	}
}

type failingSigner struct{}

func (failingSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("kms down")
}

// plainSigner writes the bundle shape without a real signature; pkg/signing
// tests the crypto.
type plainSigner struct{}

func (plainSigner) Sign(_ context.Context, payloadType string, payload []byte) ([]byte, error) {
	return json.Marshal(map[string]any{"mediaType": BundleMediaType, "dsseEnvelope": map[string]any{
		"payload": payload, "payloadType": payloadType, "signatures": []map[string]any{{"sig": "c2ln"}}}})
}

func accept(string, int64, []byte) error { return nil }

func TestSignAndOpenRecord(t *testing.T) {
	st := NewOperationRecord(recordInput())
	b, id, err := SignRecord(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(st)
	if sum := sha256.Sum256(payload); id != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("id %s is not the payload digest", id)
	}
	var stream string
	var seq int64
	got, gotID, err := OpenRecord(b, func(s string, n int64, bundle []byte) error { stream, seq = s, n; return nil })
	if err != nil || gotID != id || got.Predicate.Intent.UID != "3b1e" || stream != st.Predicate.Chain.Stream || seq != st.Predicate.Chain.Seq {
		t.Fatalf("open = %+v %s %q %v", got.Predicate.Intent, gotID, stream, err)
	}
	if _, _, err := SignRecord(t.Context(), failingSigner{}, st); err == nil {
		t.Fatal("a failing signer signed")
	}
	if _, _, err := OpenRecord(b, func(string, int64, []byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("opened a record the verifier refused")
	}
	if hist, histID, err := OpenRecord(b, func(string, int64, []byte) error { return &HistoryOnly{Until: "r9"} }); err == nil ||
		!strings.Contains(err.Error(), "r9") || histID != gotID || hist.Predicate.Chain.Seq != got.Predicate.Chain.Seq {
		t.Fatalf("a revoked key's record comes back with its statement and a HistoryOnly error: %v", err)
	}
	edit := func(f func(*envelope)) []byte {
		var x envelope
		_ = json.Unmarshal(b, &x)
		f(&x)
		out, _ := json.Marshal(x)
		return out
	}
	for name, bad := range map[string][]byte{
		"not json":     []byte("{"),
		"media type":   edit(func(x *envelope) { x.MediaType = "x" }),
		"payload type": edit(func(x *envelope) { x.DSSEEnvelope.PayloadType = "x" }),
		"two signatures": edit(func(x *envelope) {
			x.DSSEEnvelope.Signatures = append(x.DSSEEnvelope.Signatures, x.DSSEEnvelope.Signatures[0])
		}),
		"payload not json": edit(func(x *envelope) { x.DSSEEnvelope.Payload = []byte(`not json`) }),
		"unknown field": edit(func(x *envelope) {
			x.DSSEEnvelope.Payload = []byte(`{"_type":"https://in-toto.io/Statement/v1","predicateType":"delivery.pacto.trianalab.io/operation/v1","extra":1}`)
		}),
		"other predicate": edit(func(x *envelope) {
			x.DSSEEnvelope.Payload = []byte(`{"_type":"https://in-toto.io/Statement/v1","subject":[],"predicateType":"x","predicate":{"kind":"","intent":{"stage":{"cluster":"","namespace":"","name":""}},"chain":{"stream":"","seq":0},"time":{"finished":""}}}`)
		}),
	} {
		if _, _, err := OpenRecord(bad, accept); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}
