package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const atApprovals = "delivery/streams-approvals"

var atStage = delivery.RecordStage{Cluster: "c", Namespace: "prod", Name: "payments-prod"}

// atEnv is a record stream with its approvals repository beside it under
// the chart's default name, and a keyless signer configured for acme.
type atEnv struct {
	*recordEnv
	ss   *sigtest.Sigstore
	mode capabilities.ReferrersMode
}

func newAtEnv(t *testing.T, opts testutil.ReferrersOptions) *atEnv {
	t.Helper()
	e := &atEnv{recordEnv: newRecordEnvOn(t, opts), ss: sigtest.New(t), mode: capabilities.ReferrersNative}
	if opts.Unsupported {
		e.mode = capabilities.ReferrersWriteOnceTags
	}
	e.d.Policy = delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: acmeIssuer, UsernamePrefix: "oidc:",
		Subjects: `^[^@]+@acme\.example$`, TrustedRoot: string(e.ss.RootJSON)}}}
	if err := ocireferrer.Push(context.Background(), e.repo(atApprovals), deliveryrecord.Anchor(recStream)); err != nil {
		t.Fatal(err)
	}
	return e
}

// keyless is a keyless signer whose certificate names san.
func (e *atEnv) keyless(san string) delivery.Signer {
	e.t.Helper()
	tok := sigtest.Token(san, acmeIssuer)
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// push signs st with s and pushes it to path, returning its reference and id.
func (e *atEnv) push(path string, s delivery.Signer, predicate string, st any) (string, string) {
	e.t.Helper()
	var b []byte
	var id string
	var err error
	switch x := st.(type) {
	case delivery.ApprovalStatement:
		b, id, err = delivery.SignApproval(context.Background(), s, x)
	case delivery.IntentStatement:
		b, id, err = delivery.SignIntent(context.Background(), s, x)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	ref, err := deliveryrecord.Push(context.Background(), e.repo(path), recStream, e.mode, predicate, b, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return ref, id
}

func shipOp() delivery.OperationObject {
	return delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "ship", UID: "uid-ship"}}
}

// personApproval is alice's keyless approval of ship with note.
func (e *atEnv) personApproval(san, note string) (string, string) {
	st := delivery.NewApprovalRecord(atStage, e.anchor.Digest.String(), shipOp(),
		delivery.ApprovalSpec{IntentDigest: "sha256:i", Decision: delivery.Approve, Note: note}, delivery.ApprovalSigner{Kind: delivery.SignerPerson}, "2026-11-18T00:00:00Z")
	return e.push(atApprovals, e.keyless(san), delivery.PredicateApproval, st)
}

// recorderApproval is the recorder's approval of ship on alice's behalf.
func (e *atEnv) recorderApproval(key ed25519.PrivateKey) (string, string) {
	st := delivery.NewApprovalRecord(atStage, e.anchor.Digest.String(), shipOp(), delivery.ApprovalSpec{Decision: delivery.Approve},
		delivery.ApprovalSigner{Kind: delivery.SignerRecorder, OnBehalfOf: &delivery.ApprovalBehalf{User: "alice", Verified: delivery.VerifiedByAdmission}}, "2026-11-18T00:00:00Z")
	return e.push("delivery/streams", edSigner(key), delivery.PredicateApproval, st)
}

// intent is the recorder's intent record of ship, naming approvals.
func (e *atEnv) intent(key ed25519.PrivateKey, approvals string) string {
	op := shipOp()
	op.Spec = delivery.OperationSpec{IntentDigest: "sha256:i", Stage: "payments-prod", Actor: delivery.Actor{User: "bob"}}
	ref, _ := e.push("delivery/streams", edSigner(key), delivery.PredicateIntent,
		delivery.NewIntentRecord(atStage, e.anchor.Digest.String(), approvals, op, "2026-11-18T00:00:00Z"))
	return ref
}

// concluded records ship's operation record listing the approval records ids.
func (e *atEnv) concluded(ids ...string) {
	p := move(1, "", "ship", "", dlA)
	p.Intent.Stage = atStage
	for _, id := range ids {
		p.Approvals = append(p.Approvals, delivery.RecordApproval{By: "oidc:alice@acme.example", Decision: delivery.Approve, Record: id})
	}
	e.attach("delivery/streams", e.anchor, p)
}

func TestRecordAtApprovalPersonVerified(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, _ := e.personApproval("alice@acme.example", "ok")
	at, err := e.d.RecordAt(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Verified || at.Predicate != delivery.PredicateApproval || at.Signer != "alice@acme.example (keyless, issuer "+acmeIssuer+")" ||
		at.Approval == nil || at.Approval.Predicate.Note != "ok" || at.Ref != ref || at.AsOf == "" ||
		at.Note != "decision recorded (signature verified); it counted only if the operation record lists it" {
		t.Fatalf("%+v", at)
	}
}

func TestRecordAtApprovalSaysWhetherItCounted(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, id := e.personApproval("alice@acme.example", "ok")
	at, _ := e.d.RecordAt(context.Background(), ref)
	if at.Counted != delivery.CountedUnknown || !strings.Contains(at.CountedWhy, "holds no record of operation ship yet") {
		t.Fatalf("before the operation concluded: %+v", at)
	}
	other, _ := e.personApproval("alice@acme.example", "second thoughts")
	e.concluded(id)
	if at, _ := e.d.RecordAt(context.Background(), ref); at.Counted != delivery.Counted || at.CountedWhy != "" {
		t.Fatalf("listed: %+v", at)
	}
	if at, _ := e.d.RecordAt(context.Background(), other); at.Counted != delivery.NotCounted {
		t.Fatalf("not listed: %+v", at)
	}
	records := e.d.Records
	e.d.Records = func(path string, insecure ...string) (*remote.Repository, error) {
		if strings.HasSuffix(path, "/delivery/streams") {
			return nil, errors.New("denied")
		}
		return records(path, insecure...)
	}
	if at, _ := e.d.RecordAt(context.Background(), ref); at.Counted != delivery.CountedUnknown || !strings.Contains(at.CountedWhy, "could not be read: denied") {
		t.Fatalf("unreadable stream: %+v", at)
	}
	e.d.Records, e.d.Trust = records, nil
	if at, _ := e.d.RecordAt(context.Background(), ref); !at.Verified || at.Counted != delivery.CountedUnknown || !strings.Contains(at.CountedWhy, "no trust config") {
		t.Fatalf("no trust config: %+v", at)
	}
}

func TestRecordAtApprovalUnknownIssuerUnverified(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, _ := e.personApproval("alice@acme.example", "ok")
	e.d.Policy.Signers[0].Issuer = "https://other.example"
	at, err := e.d.RecordAt(context.Background(), ref)
	if err != nil || at.Verified || at.Counted != "" || !strings.HasPrefix(at.Note, "unverified: ") || !strings.Contains(at.Note, "--signers") {
		t.Fatalf("%+v %v", at, err)
	}
	e.d.Policy.Signers = nil
	if at, _ := e.d.RecordAt(context.Background(), ref); at.Verified ||
		at.Note != "unverified: no approver signer was given to verify it with; pass --signers with the installation's identity policy" {
		t.Fatalf("no signers given: %+v", at)
	}
	e.d.PolicyNamespace = "pacto-delivery"
	if at, _ := e.d.RecordAt(context.Background(), ref); at.Verified || !strings.Contains(at.Note, "signers could not be read") {
		t.Fatalf("%+v", at)
	}
}

func TestRecordAtApprovalPublishedPolicyRefusesSigner(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, _ := e.personApproval("alice@acme.example", "ok")
	published := e.d.Policy
	published.Signers[0].Issuer = "https://other.example"
	raw, _ := json.Marshal(published)
	cluster, _, _ := deliveryFixture(t, "carol", nil, nil, obj("v1", "ConfigMap", "pacto-delivery", delivery.PolicyConfigMap,
		map[string]any{"data": map[string]any{delivery.PolicyKey: string(raw)}}))
	e.d.Dynamic, e.d.PolicyNamespace = cluster.Dynamic, "pacto-delivery"
	at, err := e.d.RecordAt(context.Background(), ref)
	if err != nil || at.Verified || strings.Contains(at.Note, "--signers") ||
		!strings.HasPrefix(at.Note, "unverified: the installation's published identity policy does not accept this signer: ") {
		t.Fatalf("%+v %v", at, err)
	}
}

func TestRecordAtApprovalCustomApprovalsRepository(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	if err := ocireferrer.Push(context.Background(), e.repo("delivery/decisions"), deliveryrecord.Anchor(recStream)); err != nil {
		t.Fatal(err)
	}
	st := delivery.NewApprovalRecord(atStage, e.anchor.Digest.String(), shipOp(),
		delivery.ApprovalSpec{Decision: delivery.Approve}, delivery.ApprovalSigner{Kind: delivery.SignerPerson}, "2026-11-18T00:00:00Z")
	ref, _ := e.push("delivery/decisions", e.keyless("alice@acme.example"), delivery.PredicateApproval, st)
	at, err := e.d.RecordAt(context.Background(), ref)
	if err != nil || !at.Verified || at.Counted != delivery.CountedUnknown || strings.Contains(at.CountedWhy, "yet") ||
		!strings.Contains(at.CountedWhy, "operation records' repository is not known") {
		t.Fatalf("%+v %v", at, err)
	}
}

func TestRecordAtIntentWithTrust(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	approvals := deliveryrecord.AnchorRef(e.host+"/"+atApprovals, recStream)
	ref := e.intent(e.key, approvals)
	at, err := e.d.RecordAt(context.Background(), ref)
	if err != nil || !at.Verified || at.Intent == nil || at.Intent.Predicate.Approvals != approvals || !strings.HasPrefix(at.Note, "signature verified with trust config ") {
		t.Fatalf("%+v %v", at, err)
	}
	if in, err := e.d.IntentAt(context.Background(), ref); err != nil || in.Predicate.Intent.UID != "uid-ship" {
		t.Fatalf("%+v %v", in, err)
	}
	approval, _ := e.recorderApproval(e.key)
	if at, _ := e.d.RecordAt(context.Background(), approval); !at.Verified || at.Signer != "the recorder on behalf of alice (verified by admission)" {
		t.Fatalf("recorder approval: %+v", at)
	}
	if _, err := e.d.IntentAt(context.Background(), approval); err == nil || !strings.Contains(err.Error(), "is not an intent record") {
		t.Fatal(err)
	}
}

func TestRecordAtOperationRecord(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.concluded()
	descs, _, _ := ocireferrer.Attached(context.Background(), e.repo("delivery/streams"), e.anchor, "")
	at, err := e.d.RecordAt(context.Background(), e.host+"/delivery/streams@"+descs[0].Digest.String())
	if err != nil || at.Predicate != delivery.PredicateOperation || !at.Verified || at.Operation == nil || at.Operation.Stage != "payments-prod" || !at.Operation.Verified {
		t.Fatalf("operation record: %+v %v", at, err)
	}
}

func TestRecordAtNoTrustSaysUnverified(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref := e.intent(e.key, "")
	approval, _ := e.recorderApproval(e.key)
	e.concluded()
	trust := e.d.Trust
	e.d.Trust = nil
	for _, r := range []string{ref, approval} {
		if at, err := e.d.RecordAt(context.Background(), r); err != nil || at.Verified || at.Note != noTrust {
			t.Fatalf("%s: %+v %v", r, at, err)
		}
	}
	if _, err := e.d.IntentAt(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatal(err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.d.Trust = trust
	forged, _ := e.recorderApproval(other)
	if at, _ := e.d.RecordAt(context.Background(), forged); at.Verified || !strings.HasPrefix(at.Note, "unverified: ") || at.Counted != "" {
		t.Fatalf("a forged record: %+v", at)
	}
	e.d.Trust = nil
	descs, _, _ := ocireferrer.Attached(context.Background(), e.repo("delivery/streams"), e.anchor, "")
	for _, d := range descs {
		if at, _ := e.d.RecordAt(context.Background(), e.host+"/delivery/streams@"+d.Digest.String()); at.Predicate == delivery.PredicateOperation &&
			(at.Verified || at.Operation == nil || at.Operation.Verified) {
			t.Fatalf("operation record: %+v", at)
		}
	}
}

func TestRecordAtBounded(t *testing.T) {
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, _ := e.personApproval("alice@acme.example", "ok")
	old := deliveryrecord.MaxBytes
	deliveryrecord.MaxBytes = 16
	_, err := e.d.RecordAt(context.Background(), ref)
	deliveryrecord.MaxBytes = old
	if err == nil || !strings.Contains(err.Error(), "is not a delivery record of at most 16 bytes") {
		t.Fatal(err)
	}
}

func TestRecordAtReferenceErrors(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, _ := e.personApproval("alice@acme.example", "ok")
	for _, bad := range []string{"reg", "reg/x", "reg/x:", "@sha256:x"} {
		if _, err := e.d.RecordAt(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "is not a record reference") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := e.d.IntentAt(context.Background(), "reg"); err == nil {
		t.Fatal("read an intent from no reference")
	}
	if repo, tag, err := SplitRecordRef("localhost:5000/a/b:stream-x"); err != nil || repo != "localhost:5000/a/b" || tag != "stream-x" {
		t.Fatal(repo, tag, err)
	}
	e.d.Records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("bad repo") }
	if _, err := e.d.RecordAt(context.Background(), ref); err == nil || err.Error() != "bad repo" {
		t.Fatal(err)
	}
	e.d.Records = nil
	if _, err := e.d.RecordAt(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "reads no registry") {
		t.Fatal(err)
	}
}

// TestRecordAtBrokenBundle: a bundle that is no statement fails to open,
// under every predicate.
func TestRecordAtBrokenBundle(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	for _, p := range []string{delivery.PredicateOperation, delivery.PredicateApproval, delivery.PredicateIntent} {
		r, err := deliveryrecord.Push(context.Background(), e.repo("delivery/streams"), recStream+p, capabilities.ReferrersNative, p, []byte("{}"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.d.RecordAt(context.Background(), r); err == nil {
			t.Errorf("%s: a broken bundle opened", p)
		}
	}
}

func TestApproveIntentPushesToApprovals(t *testing.T) {
	t.Parallel()
	for _, opts := range []testutil.ReferrersOptions{{}, {Unsupported: true}} {
		e := newAtEnv(t, opts)
		approvals := deliveryrecord.AnchorRef(e.host+"/"+atApprovals, recStream)
		in, err := e.d.IntentAt(context.Background(), e.intent(e.key, approvals))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.d.ApproveIntent(context.Background(), in, delivery.Approve, "ok", e.keyless("alice@acme.example"), acmeIssuer, "alice@acme.example")
		if err != nil || !strings.HasPrefix(ref.Ref, e.host+"/"+atApprovals+"@sha256:") {
			t.Fatalf("%+v: %+v %v", opts, ref, err)
		}
		at, err := e.d.RecordAt(context.Background(), ref.Ref)
		if err != nil || !at.Verified || at.Approval.Predicate.Operation.UID != "uid-ship" || at.Approval.Predicate.Operation.IntentDigest != "sha256:i" {
			t.Fatalf("%+v %v", at, err)
		}
		if opts.Unsupported {
			tags, _ := ocireferrer.Tags(context.Background(), e.repo(atApprovals), deliveryrecord.AnchorTag(recStream)+"-"+deliveryrecord.KindApproval+"-")
			if len(tags) != 1 {
				t.Fatalf("write-once tags: %v", tags)
			}
		}
	}
}

func TestApproveIntentRefusals(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	approvals := deliveryrecord.AnchorRef(e.host+"/"+atApprovals, recStream)
	in, _ := e.d.IntentAt(context.Background(), e.intent(e.key, approvals))
	approve := func(in *delivery.IntentStatement, issuer, san string) error {
		_, err := e.d.ApproveIntent(context.Background(), in, delivery.Approve, "", e.keyless("alice@acme.example"), issuer, san)
		return err
	}
	none := *in
	none.Predicate.Approvals = ""
	if err := approve(&none, acmeIssuer, "alice@acme.example"); err == nil || !strings.Contains(err.Error(), "names no approvals repository") {
		t.Fatal(err)
	}
	none.Predicate.Approvals = deliveryrecord.AnchorRef(e.host+"/x", "other/stream")
	if err := approve(&none, acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("pushed to another stream's anchor")
	}
	if err := approve(in, acmeIssuer, "https://github.com/acme/app/.github/workflows/ci.yml@refs/heads/main"); err == nil || !strings.HasPrefix(err.Error(), "AgentsMayNotApprove") {
		t.Fatal(err)
	}
	if err := approve(in, "https://other.example", "alice@acme.example"); err == nil {
		t.Fatal("an unconfigured issuer approved")
	}
	e.d.PolicyNamespace = "pacto-delivery"
	if err := approve(in, acmeIssuer, "alice@acme.example"); err == nil {
		t.Fatal("approved with an unreadable identity policy")
	}
	e.d.PolicyNamespace = ""
}

func TestApproveIntentRegistryFailures(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	approvals := deliveryrecord.AnchorRef(e.host+"/"+atApprovals, recStream)
	in, _ := e.d.IntentAt(context.Background(), e.intent(e.key, approvals))
	approve := func(in *delivery.IntentStatement, issuer, san string) error {
		_, err := e.d.ApproveIntent(context.Background(), in, delivery.Approve, "", e.keyless("alice@acme.example"), issuer, san)
		return err
	}
	fail := signerFunc(func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("no login") })
	if _, err := e.d.ApproveIntent(context.Background(), in, delivery.Approve, "", fail, acmeIssuer, "alice@acme.example"); err == nil || !strings.Contains(err.Error(), "no login") {
		t.Fatal(err)
	}
	records := e.d.Records
	e.d.Records = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("bad repo") }
	if err := approve(in, acmeIssuer, "alice@acme.example"); err == nil || err.Error() != "bad repo" {
		t.Fatal(err)
	}
	e.d.Records = records
	if err := approve(in, acmeIssuer, "alice@acme.example"); err != nil {
		t.Fatal(err)
	}
	// A registry whose probe cannot tell gets nothing.
	unanchored := newAtEnv(t, testutil.ReferrersOptions{})
	gone := *in
	gone.Predicate.Approvals = deliveryrecord.AnchorRef(unanchored.host+"/delivery/none", recStream)
	if err := approve(&gone, acmeIssuer, "alice@acme.example"); err == nil || !strings.Contains(err.Error(), "probing registry") {
		t.Fatal(err)
	}
	e.srv.Close()
	if err := approve(in, acmeIssuer, "alice@acme.example"); err == nil || !strings.Contains(err.Error(), "pushing the approval") {
		t.Fatal(err)
	}
}

// TestRecordAtCarriesClassifiedBy: a verified keyless approval says whether
// a person, an agent or a CI workload signed it and what said so; an agent's
// is a proposal that never counted.
func TestRecordAtCarriesClassifiedBy(t *testing.T) {
	t.Parallel()
	e := newAtEnv(t, testutil.ReferrersOptions{})
	ref, _ := e.personApproval("alice@acme.example", "ok")
	at, err := e.d.RecordAt(context.Background(), ref)
	if err != nil || at.SignerClass != delivery.ClassHuman || !strings.HasPrefix(at.ClassifiedBy, "default:") {
		t.Fatalf("a person: %+v %v", at, err)
	}
	const sa = "https://kubernetes.io/namespaces/agents/serviceaccounts/reviewer"
	ref, id := e.personApproval(sa, "ok")
	e.concluded(id)
	at, err = e.d.RecordAt(context.Background(), ref)
	if err != nil || !at.Verified || at.SignerClass != delivery.ClassAgent || !strings.HasPrefix(at.ClassifiedBy, "default:") ||
		at.Counted != delivery.NotCounted || at.CountedWhy != "not counted (signed by agent "+sa+": a proposal)" || !strings.Contains(at.Note, "proposal") {
		t.Fatalf("a workload: %+v %v", at, err)
	}
}
