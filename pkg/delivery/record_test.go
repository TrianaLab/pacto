package delivery

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
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

	in.Operation.Spec.Type, in.Operation.Spec.OnBehalfOf = "pacto.dev/scale", nil
	in.Subjects = RecordSubjects{Anchor: rAnc}
	st = NewOperationRecord(in)
	if got := st.Predicate; got.Type.ID != "pacto.dev/scale" || got.Type.Digest != "" || got.Actor.OnBehalfOf != nil {
		t.Fatalf("unknown type record = %+v", st)
	}
	if len(st.Subject) != 1 {
		t.Fatalf("subjects = %+v", st.Subject)
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

func (failingSigner) Sign(context.Context, []byte) ([]byte, VerificationMaterial, error) {
	return nil, VerificationMaterial{}, errors.New("kms down")
}

func TestSignAndOpenRecord(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	st := NewOperationRecord(recordInput())
	b, id, err := SignRecord(t.Context(), Ed25519Signer(key), st)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(st)
	if sum := sha256.Sum256(payload); id != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("id %s is not the payload digest", id)
	}
	got, gotID, err := OpenRecord(b, PublicKeyVerifier(key.Public()))
	if err != nil || gotID != id || got.Predicate.Intent.UID != "3b1e" {
		t.Fatalf("open = %+v %s %v", got.Predicate.Intent, gotID, err)
	}
	if _, _, err := SignRecord(t.Context(), failingSigner{}, st); err == nil {
		t.Fatal("a failing signer signed")
	}

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := OpenRecord(b, PublicKeyVerifier(other.Public())); err == nil {
		t.Fatal("verified under another key")
	}
	edit := func(f func(*Bundle)) []byte {
		var x Bundle
		_ = json.Unmarshal(b, &x)
		f(&x)
		out, _ := json.Marshal(x)
		return out
	}
	resign := func(payload []byte) func(*Bundle) {
		return func(x *Bundle) {
			x.DSSEEnvelope.Payload = payload
			x.DSSEEnvelope.Signatures[0].Sig = ed25519.Sign(key, PAE(PayloadType, payload))
		}
	}
	for name, bad := range map[string][]byte{
		"not json":        []byte("{"),
		"media type":      edit(func(x *Bundle) { x.MediaType = "x" }),
		"payload type":    edit(func(x *Bundle) { x.DSSEEnvelope.PayloadType = "x" }),
		"two signatures":  edit(func(x *Bundle) { x.DSSEEnvelope.Signatures = append(x.DSSEEnvelope.Signatures, Signature{}) }),
		"tampered":        edit(func(x *Bundle) { x.DSSEEnvelope.Payload = append(x.DSSEEnvelope.Payload, ' ') }),
		"unknown field":   edit(resign([]byte(`{"_type":"x","extra":1}`))),
		"other predicate": edit(resign([]byte(`{"_type":"https://in-toto.io/Statement/v1","subject":[],"predicateType":"x","predicate":{"kind":"","intent":{"stage":{"cluster":"","namespace":"","name":""}},"chain":{"stream":"","seq":0},"time":{"finished":""}}}`))),
	} {
		if _, _, err := OpenRecord(bad, PublicKeyVerifier(key.Public())); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

func TestPublicKeyVerifierKeyTypes(t *testing.T) {
	pae := PAE(PayloadType, []byte("{}"))
	digest := sha256.Sum256(pae)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecSig, _ := ecdsa.SignASN1(rand.Reader, ec, digest[:])
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaSig, _ := rsa.SignPKCS1v15(rand.Reader, rk, crypto.SHA256, digest[:])
	for name, c := range map[string]struct {
		pub any
		sig []byte
	}{"ecdsa": {&ec.PublicKey, ecSig}, "rsa": {&rk.PublicKey, rsaSig}} {
		v := PublicKeyVerifier(c.pub)
		m := VerificationMaterial{PublicKey: &KeyHint{Hint: KeyHintOf(c.pub)}}
		if err := v("", pae, c.sig, m); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if err := v("", pae, []byte("bad"), m); err == nil {
			t.Errorf("%s: a bad signature verified", name)
		}
		if err := v("", pae, c.sig, VerificationMaterial{}); err == nil {
			t.Errorf("%s: verified with no key named", name)
		}
	}
	if KeyHintOf(struct{}{}) != "" {
		t.Fatal("an unsupported key has a hint")
	}
	if err := verifyWith(struct{}{}, pae, nil); err == nil {
		t.Fatal("verified with an unsupported key type")
	}
}

func selfSigned(t *testing.T, identity string, ext []pkix.Extension) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	u, _ := url.Parse(identity)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
		URIs: []*url.URL{u}, EmailAddresses: []string{"ops@acme.example"}, ExtraExtensions: ext}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

func TestCertificateVerifier(t *testing.T) {
	const id, iss = "https://kubernetes.io/namespaces/pacto/serviceaccounts/recorder", "https://issuer.example"
	v2, _ := asn1.Marshal(iss)
	key, der := selfSigned(t, id, []pkix.Extension{{Id: oidIssuerV2, Value: v2}})
	pae := PAE(PayloadType, []byte("{}"))
	digest := sha256.Sum256(pae)
	sig, _ := ecdsa.SignASN1(rand.Reader, key, digest[:])
	m := VerificationMaterial{Certificate: &Certificate{RawBytes: der}}
	// Without roots a self-signed certificate naming the right identity and
	// issuer proves nothing, so it does not verify.
	if err := CertificateVerifier(id, iss, nil)("", pae, sig, m); err == nil || !strings.Contains(err.Error(), "no trusted roots") {
		t.Fatalf("verified a certificate with no roots: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	if err := CertificateVerifier(id, iss, roots)("", pae, sig, m); err != nil {
		t.Fatal(err)
	}
	if err := CertificateVerifier("ops@acme.example", iss, roots)("", pae, sig, m); err != nil {
		t.Fatalf("email identity: %v", err)
	}
	if err := CertificateVerifier("someone-else", iss, roots)("", pae, sig, m); err == nil {
		t.Fatal("verified another identity")
	}
	if err := CertificateVerifier(id, iss, roots)("", pae, sig, VerificationMaterial{}); err == nil {
		t.Fatal("verified with no certificate")
	}
	if err := CertificateVerifier(id, iss, roots)("", pae, sig, VerificationMaterial{Certificate: &Certificate{RawBytes: []byte("x")}}); err == nil {
		t.Fatal("verified with a broken certificate")
	}
	_, v1der := selfSigned(t, id, []pkix.Extension{{Id: oidIssuerV1, Value: []byte(iss)}})
	c1, _ := x509.ParseCertificate(v1der)
	_, badDer := selfSigned(t, id, []pkix.Extension{{Id: oidIssuerV2, Value: []byte{0xff}}})
	c2, _ := x509.ParseCertificate(badDer)
	if certIssuer(c1) != iss || certIssuer(c2) != "" {
		t.Fatalf("issuers %q %q", certIssuer(c1), certIssuer(c2))
	}
}
