/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	personIssuer = "https://login.acme.example"
	alice        = "alice@acme.com"
	aliceUser    = "oidc:" + alice
)

// withSigners is p accepting keyless approvals from personIssuer's
// certificates for acme.com addresses, checked as oidc:<address>.
func withSigners(p delivery.ClassPolicy, ss *sigtest.Sigstore) delivery.ClassPolicy {
	p.Signers = []delivery.PersonSigner{{Issuer: personIssuer, UsernamePrefix: "oidc:", Subjects: `^[^@]+@acme\.com$`,
		TrustedRoot: string(ss.RootJSON)}}
	return p
}

// signedApprovalOf is an approval of op as a person's client signs it, with
// edit applied before signing.
func signedApprovalOf(t *testing.T, ss *sigtest.Sigstore, san string, op *dv1.Operation, stream string, edit ...func(*delivery.ApprovalStatement)) []byte {
	t.Helper()
	return signedApprovalWith(t, ss, sigtest.Token(san, personIssuer), op, stream, edit...)
}

// signedApprovalWith is signedApprovalOf signed with the OIDC token tok.
func signedApprovalWith(t *testing.T, ss *sigtest.Sigstore, tok string, op *dv1.Operation, stream string, edit ...func(*delivery.ApprovalStatement)) []byte {
	t.Helper()
	obj := delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: op.Name, Namespace: op.Namespace, UID: string(op.UID)}, Spec: op.Spec}
	st := delivery.NewApprovalRecord(delivery.RecordStage{}, deliveryrecord.Anchor(stream).ManifestDesc.Digest.String(), obj,
		delivery.ApprovalSpec{IntentDigest: op.Spec.IntentDigest, Decision: delivery.Approve, Note: "ship it"},
		delivery.ApprovalSigner{Kind: delivery.SignerPerson}, time.Now().UTC().Format(time.RFC3339))
	for _, f := range edit {
		f(&st)
	}
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := delivery.SignApproval(context.Background(), s, st)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pushApproval puts bundle on stream's anchor in the approvals repository
// path as a person's client does, and returns its reference.
func pushApproval(t *testing.T, path, stream string, op *dv1.Operation, bundle []byte) string {
	t.Helper()
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	if err := ocireferrer.Push(context.Background(), repo, deliveryrecord.Anchor(stream)); err != nil {
		t.Fatal(err)
	}
	ref, err := deliveryrecord.Push(context.Background(), repo, stream, capabilities.ReferrersNative, delivery.PredicateApproval, bundle,
		map[string]string{deliveryrecord.AnnUID: string(op.UID)})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// keylessRecEnv is heldRecEnv whose recorder views keyless approvals.
func keylessRecEnv(t *testing.T) (*recEnv, *ApprovalRecorder, *dv1.Operation, *sigtest.Sigstore) {
	t.Helper()
	re, ar, o := heldRecEnv(t)
	ss := sigtest.New(t)
	re.rec.Policy = withSigners(delivery.ClassPolicy{}, ss)
	re.reconcile() // with signers, the Stage anchors its approvals repository
	return re, ar, o, ss
}

const recStream = "cid/prod/payments-prod"

func (re *recEnv) pushSigned(ss *sigtest.Sigstore, san string, op *dv1.Operation, edit ...func(*delivery.ApprovalStatement)) string {
	re.t.Helper()
	return pushApproval(re.t, re.rec.Store.Approvals, recStream, op, signedApprovalOf(re.t, ss, san, op, recStream, edit...))
}

// views are the Approvals that stand for a keyless record.
func (re *recEnv) views() []dv1.Approval {
	re.t.Helper()
	list := &dv1.ApprovalList{}
	if err := re.c.List(context.Background(), list, client.InNamespace(ns)); err != nil {
		re.t.Fatal(err)
	}
	var out []dv1.Approval
	for _, a := range list.Items {
		if a.Spec.Record != "" {
			out = append(out, a)
		}
	}
	return out
}

// TestScanSignedCreatesView: a keyless approval record in the approvals
// repository on a held operation becomes a view naming the person the
// certificate names, owned by the operation; the promoter, with the recorder
// on, counts it once it verified the record itself.
func TestScanSignedCreatesView(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.recordApprovals(ar) // the intent first
	ref := re.pushSigned(ss, alice, o)
	if res := re.recordApprovals(ar); res.RequeueAfter != re.rec.Poll {
		t.Fatalf("a held operation is not looked at again: %+v", res)
	}
	views := re.views()
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	checkAliceView(t, views[0], ref, o)
	// Another pass creates nothing new.
	re.recordApprovals(ar)
	if len(re.views()) != 1 {
		t.Fatal("a record got a second view")
	}

	// The promoter counts the view only through its verified record.
	re.op.Records, re.op.Policy = true, withSigners(delivery.ClassPolicy{}, ss)
	got, why := re.op.approval(context.Background(), re.getOp("p1"), re.getStage())
	personCounted := got != nil && got.Signer != nil && got.Signer.Kind == delivery.SignerPerson
	if !personCounted || got.By != aliceUser || got.ClassifiedBy != delivery.ClassByUser || got.Record == "" ||
		got.AccessSource != delivery.AccessBySubjectReview {
		t.Fatalf("counted %+v (%q)", got, why)
	}
}

// checkAliceView fails unless a is the view of alice's keyless record ref on
// o: her mapped user, no groups or login time, owned by o.
func checkAliceView(t *testing.T, a dv1.Approval, ref string, o *dv1.Operation) {
	t.Helper()
	v := a.Spec
	if v.By != aliceUser || v.Record != ref || v.SignedAt == "" || v.Decision != delivery.Approve || v.Note != "ship it" {
		t.Fatalf("view = %+v", v)
	}
	if v.OperationUID != string(o.UID) || v.IntentDigest != o.Spec.IntentDigest || len(v.Groups) != 0 || v.AuthTime != "" {
		t.Fatalf("view = %+v", v)
	}
	if own := a.OwnerReferences; len(own) != 1 || own[0].UID != o.UID {
		t.Fatalf("owners = %+v", own)
	}
}

// TestScanSignedSetsStatusAfterCreate: the view's status.record is written
// after the create, since the apiserver drops status on create; a view whose
// status write failed is harmless and never viewed twice.
func TestScanSignedSetsStatusAfterCreate(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.recordApprovals(ar)
	ref := re.pushSigned(ss, alice, o)
	re.fails = fails{"status/Approval": true}
	defer func() {
		if v := re.views(); len(v) != 1 || v[0].Spec.Record != ref {
			t.Fatalf("views = %+v", v)
		}
	}()
	if res := re.recordApprovals(ar); res.RequeueAfter != re.rec.Poll {
		t.Fatalf("%+v", res)
	}
	if v := re.views(); len(v) != 1 || v[0].Status.Record != nil {
		t.Fatalf("views = %+v", v)
	}
	re.fails = fails{}
	re.recordApprovals(ar)
	if v := re.views(); len(v) != 1 {
		t.Fatalf("views = %+v", v)
	}
	// A fresh record gets its status in the same pass.
	re2, ar2, o2, ss2 := keylessRecEnv(t)
	re2.recordApprovals(ar2)
	ref2 := re2.pushSigned(ss2, alice, o2)
	re2.recordApprovals(ar2)
	v := re2.views()
	if len(v) != 1 || v[0].Status.Record == nil || v[0].Status.Record.Ref != ref2 || v[0].Status.Record.Digest == "" {
		t.Fatalf("view status = %+v", v)
	}
	// A refused create writes nothing and looks again.
	re3, ar3, o3, ss3 := keylessRecEnv(t)
	re3.recordApprovals(ar3)
	re3.pushSigned(ss3, alice, o3)
	re3.fails = fails{"create/Approval": true}
	if res := re3.recordApprovals(ar3); res.RequeueAfter != re3.rec.Poll || len(re3.views()) != 0 {
		t.Fatalf("%+v %+v", res, re3.views())
	}
}

// TestScanSignedSkipsUnverifiable: a record that does not verify (another
// Sigstore, garbage, a recorder-signed record) is never viewed, and is
// counted and logged once however many passes see it.
func TestScanSignedSkipsUnverifiable(t *testing.T) {
	re, ar, o, _ := keylessRecEnv(t)
	re.recordApprovals(ar)
	before := promtest.ToFloat64(UnverifiedApprovalRecords)
	re.pushSigned(sigtest.New(t), alice, o)
	pushApproval(t, re.rec.Store.Approvals, recStream, o, []byte("not a bundle"))
	re.recordApprovals(ar)
	re.recordApprovals(ar)
	if len(re.views()) != 0 {
		t.Fatalf("an unverifiable record was viewed: %+v", re.views())
	}
	if got := promtest.ToFloat64(UnverifiedApprovalRecords) - before; got != 2 {
		t.Fatalf("unverified counted %v times, want 2", got)
	}
}

// TestScanSignedCreatesNoViewForRefusedSigner: a SAN outside the signer's
// subjects is an agent, and a person the workload review refuses would not
// count: neither gets a view.
func TestScanSignedCreatesNoViewForRefusedSigner(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.recordApprovals(ar)
	re.pushSigned(ss, "mallory@evil.example", o)
	re.rights[aliceUser] = false
	re.pushSigned(ss, alice, o)
	re.recordApprovals(ar)
	if len(re.views()) != 0 {
		t.Fatalf("a refused signer was viewed: %+v", re.views())
	}
	// A record for another operation is not this one's view, even under
	// this one's uid annotation.
	re2, ar2, o2, ss2 := keylessRecEnv(t)
	re2.recordApprovals(ar2)
	other := opObj("p2", delivery.TypePromote, re2.reg.from, re2.reg.to, bot)
	pushApproval(t, re2.rec.Store.Approvals, recStream, o2, signedApprovalOf(t, ss2, alice, other, recStream))
	re2.recordApprovals(ar2)
	if len(re2.views()) != 0 {
		t.Fatalf("another operation's approval was viewed: %+v", re2.views())
	}
}

// TestScanSignedCapsViewsPerOperation: an operation gets at most
// MaxApprovalViews views of signed approvals.
func TestScanSignedCapsViewsPerOperation(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.recordApprovals(ar)
	defer func(old int) { MaxApprovalViews = old }(MaxApprovalViews)
	MaxApprovalViews = 1
	re.pushSigned(ss, alice, o)
	re.pushSigned(ss, "bob@acme.com", o)
	re.recordApprovals(ar)
	re.recordApprovals(ar)
	if len(re.views()) != 1 {
		t.Fatalf("views = %d, want the cap", len(re.views()))
	}
}

// TestScanSignedBounded: one pass reads at most MaxApprovalScan records of
// the operation and says so; a repository that cannot be read writes
// nothing and looks again.
func TestScanSignedBounded(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.recordApprovals(ar)
	defer func(old int) { MaxApprovalScan = old }(MaxApprovalScan)
	MaxApprovalScan = 1
	re.pushSigned(ss, alice, o)
	re.pushSigned(ss, "bob@acme.com", o)
	re.recordApprovals(ar)
	if len(re.views()) != 1 {
		t.Fatalf("views = %d, want one pass's bound", len(re.views()))
	}
	for name, setup := range map[string]func(*recEnv){
		"registry down": func(re *recEnv) {
			re.reg.fault = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/delivery/approvals/") }
		},
		"approvals invalid": func(re *recEnv) {
			re.setStage(func(s *delivery.StageStatus) { s.Stream.Approvals = "reg/other@sha256:00" })
		},
		"repository invalid": func(re *recEnv) {
			re.setStage(func(s *delivery.StageStatus) { s.Stream.Approvals = deliveryrecord.AnchorRef("bad repo", recStream) })
		},
		"approvals unlistable": func(re *recEnv) { re.fails = fails{"list/Approval": true} },
	} {
		t.Run(name, func(t *testing.T) {
			re, ar, o, ss := keylessRecEnv(t)
			re.recordApprovals(ar)
			re.pushSigned(ss, alice, o)
			setup(re)
			if res := re.recordApprovals(ar); res.RequeueAfter != re.rec.Poll {
				t.Fatalf("%+v", res)
			}
			re.fails = fails{}
			if len(re.views()) != 0 {
				t.Fatal("a failed pass created a view")
			}
		})
	}
}

// TestScanSignedFailureKeepsSigningOnBehalf: an approvals repository that
// cannot be read never stops the recorder signing the operation's cluster
// Approvals, which count only with their record.
func TestScanSignedFailureKeepsSigningOnBehalf(t *testing.T) {
	re, ar, o, _ := keylessRecEnv(t)
	re.recordApprovals(ar) // the guard is seen first
	re.reg.fault = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/delivery/approvals/") }
	re.approve(o, "ok", carol, delivery.Approve)
	if res := re.recordApprovals(ar); res.RequeueAfter != re.rec.Poll {
		t.Fatalf("%+v", res)
	}
	if a := re.getApproval("ok"); a.Status.Record == nil {
		t.Fatalf("a failing approvals repository stopped the on-behalf record: %+v", a.Status)
	}
}

// TestScanSignedRejectedTakeNoBound: records that do not verify are read
// once, so however many sit ahead of a person's record in the listing, a
// later pass reaches it. Write-once tags list in a stable order.
func TestScanSignedRejectedTakeNoBound(t *testing.T) {
	re, ar, o := heldRecEnv(t, true)
	ss := sigtest.New(t)
	re.rec.Policy = withSigners(delivery.ClassPolicy{}, ss)
	re.reconcile()
	re.recordApprovals(ar)
	defer func(old int) { MaxApprovalScan = old }(MaxApprovalScan)
	MaxApprovalScan = 1
	repo, _ := ocireferrer.Open(re.rec.Store.Approvals, ocireferrer.Options{})
	push := func(bundle []byte) string {
		ref, err := deliveryrecord.Push(context.Background(), repo, recStream, capabilities.ReferrersWriteOnceTags, delivery.PredicateApproval, bundle,
			map[string]string{deliveryrecord.AnnUID: string(o.UID)})
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	real := push(signedApprovalOf(t, ss, alice, o, recStream))
	// One junk record that lists ahead of the real one. Records list in
	// manifest digest order and the real digest is random, so pushing junk
	// until one happens to land first fails when the real digest is small;
	// pick the junk by its digest instead.
	anchor := deliveryrecord.Anchor(recStream).ManifestDesc
	ann := map[string]string{
		deliveryrecord.AnnBundleContent: "dsse-envelope", deliveryrecord.AnnPredicateType: delivery.PredicateApproval,
		deliveryrecord.AnnStream: recStream, deliveryrecord.AnnRegistryMode: string(capabilities.ReferrersWriteOnceTags),
		deliveryrecord.AnnUID: string(o.UID),
	}
	realDigest := real[strings.LastIndex(real, "@")+1:]
	for i := 0; ; i++ {
		junk := []byte(fmt.Sprintf("junk %d", i))
		if ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, junk, &anchor, ann).ManifestDesc.Digest.String() < realDigest {
			push(junk)
			break
		}
	}
	first, _, err := (&deliveryrecord.Reader{}).ListPredicate(context.Background(), repo, recStream, delivery.PredicateApproval, string(o.UID), 1)
	if err != nil || strings.HasSuffix(real, first[0].Manifest) {
		t.Fatalf("junk does not list ahead of the real record: %v", err)
	}
	for range 60 {
		re.recordApprovals(ar)
	}
	if v := re.views(); len(v) != 1 || v[0].Spec.By != aliceUser {
		t.Fatalf("views = %+v", v)
	}
}

// TestScanSignedOnlyHeldStages: only a held operation is scanned, and only
// when signers are configured and the Stage keeps an approvals repository.
func TestScanSignedOnlyHeldStages(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.recordApprovals(ar)
	re.pushSigned(ss, alice, o)
	o = re.getOp("p1")
	o.Status.Phase = delivery.PhasePending
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if res := re.recordApprovals(ar); res.RequeueAfter != 0 || len(re.views()) != 0 {
		t.Fatalf("a pending operation was scanned: %+v", res)
	}
	re2, ar2, o2 := heldRecEnv(t) // no signers
	re2.recordApprovals(ar2)
	re2.pushSigned(ss, alice, o2)
	if res := re2.recordApprovals(ar2); res.RequeueAfter != 0 || len(re2.views()) != 0 {
		t.Fatalf("with no signer a record was scanned: %+v", res)
	}
}

// heldKeylessEnv is heldEnv whose promoter accepts keyless approvals, with
// team narrowing off (a certificate names no team) and the recorder on.
func heldKeylessEnv(t *testing.T) (*env, *dv1.Operation, *sigtest.Sigstore, *testRegistry) {
	t.Helper()
	e, o := heldEnv(t)
	ss := sigtest.New(t)
	e.op.Policy = withSigners(e.op.Policy, ss)
	e.op.Policy.TeamNarrowing = false
	e.op.Records = true
	return e, o, ss, newTestRegistry(t)
}

// view creates the Approval the recorder would for bundle at ref.
func (e *env) view(name string, o *dv1.Operation, ref string, edit ...func(*delivery.ApprovalSpec)) {
	e.t.Helper()
	path, _, _ := strings.Cut(ref, "@")
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	_, manifest, _ := strings.Cut(ref, "@")
	bundle, err := deliveryrecord.Fetch(context.Background(), repo, manifest, delivery.PredicateApproval)
	if err != nil {
		e.t.Fatal(err)
	}
	signed, err := openSigned(bundle, e.op.Policy)
	if err != nil {
		e.t.Fatal(err)
	}
	p := signed.st.Predicate
	s := delivery.ApprovalSpec{Operation: o.Name, OperationUID: string(o.UID), IntentDigest: p.Operation.IntentDigest,
		Decision: p.Decision, Note: p.Note, By: signed.identity.User, Record: ref, SignedAt: ts(signed.at)}
	for _, f := range edit {
		f(&s)
	}
	e.create(&dv1.Approval{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, CreationTimestamp: metav1.NewTime(e.now)}, Spec: s})
}

func (e *env) signedRef(reg *testRegistry, ss *sigtest.Sigstore, san string, o *dv1.Operation, edit ...func(*delivery.ApprovalStatement)) string {
	e.t.Helper()
	const stream = "cid/" + ns + "/payments-prod"
	return pushApproval(e.t, reg.host+"/delivery/approvals", stream, o, signedApprovalOf(e.t, ss, san, o, stream, edit...))
}

// TestViewApprovalCountsOnlyWhenItsRecordVerifies: a view counts after the
// promoter fetched and verified its record; one whose record is gone, does
// not verify or was never signed by the person it names does not.
func TestViewApprovalCountsOnlyWhenItsRecordVerifies(t *testing.T) {
	e, o, ss, reg := heldKeylessEnv(t)
	ref := e.signedRef(reg, ss, alice, o)
	e.view("v", o, ref)
	o = e.drive("p1")
	if len(o.Status.Approvals) != 1 || o.Status.Approvals[0].By != aliceUser || o.Status.Approvals[0].Signer.Kind != delivery.SignerPerson {
		t.Fatalf("a verified view did not count: %s %q", o.Status.Phase, o.Status.Message)
	}

	for name, tc := range map[string]struct {
		ref  func(*env, *testRegistry, *sigtest.Sigstore, *dv1.Operation) string
		want string
	}{
		"gone": {func(*env, *testRegistry, *sigtest.Sigstore, *dv1.Operation) string {
			return reg.host + "/delivery/approvals@sha256:" + strings.Repeat("0", 64)
		}, "cannot be read"},
		"bad repository": {func(*env, *testRegistry, *sigtest.Sigstore, *dv1.Operation) string {
			return "BAD REPO@sha256:" + strings.Repeat("0", 64)
		}, "cannot be read"},
		"another sigstore": {func(e *env, reg *testRegistry, _ *sigtest.Sigstore, o *dv1.Operation) string {
			return e.signedRef(reg, sigtest.New(t), alice, o)
		}, "does not verify"},
		"recorder-signed": {func(e *env, reg *testRegistry, ss *sigtest.Sigstore, o *dv1.Operation) string {
			return e.signedRef(reg, ss, alice, o, func(st *delivery.ApprovalStatement) {
				st.Predicate.Signer = delivery.ApprovalSigner{Kind: delivery.SignerRecorder}
			})
		}, "does not verify: the record does not say a person signed it"},
	} {
		t.Run(name, func(t *testing.T) {
			e, o, ss, reg := heldKeylessEnv(t)
			ref := tc.ref(e, reg, ss, o)
			e.create(&dv1.Approval{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "v", CreationTimestamp: metav1.NewTime(e.now)},
				Spec: delivery.ApprovalSpec{Operation: o.Name, OperationUID: string(o.UID), IntentDigest: o.Spec.IntentDigest,
					Decision: delivery.Approve, Note: "ship it", By: aliceUser, Record: ref, SignedAt: ts(time.Now())}})
			o = e.drive("p1")
			if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, tc.want) || !strings.HasPrefix(o.Status.Message, "Approval v by "+aliceUser+" does not count: ") {
				t.Fatalf("counted or unclear: %+v %q", o.Status.Approvals, o.Status.Message)
			}
		})
	}
	// With no signer configured, no record verifies.
	e2, o2, ss2, reg2 := heldKeylessEnv(t)
	e2.view("v", o2, e2.signedRef(reg2, ss2, alice, o2))
	e2.op.Policy.Signers = nil
	if o2 = e2.drive("p1"); len(o2.Status.Approvals) != 0 || !strings.Contains(o2.Status.Message, "no approver signer is configured") {
		t.Fatalf("%+v %q", o2.Status.Approvals, o2.Status.Message)
	}
}

// TestViewApprovalRecordMismatchSkipped: a view that says anything its
// record does not (who, when, which operation or intent, the decision or
// the note) never counts.
func TestViewApprovalRecordMismatchSkipped(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*delivery.ApprovalSpec)
		want string
	}{
		"by":       {func(s *delivery.ApprovalSpec) { s.By = "oidc:bob@acme.com" }, "its record is signed by " + aliceUser},
		"signedAt": {func(s *delivery.ApprovalSpec) { s.SignedAt = "2099-01-01T00:00:00Z" }, "signing time"},
		"note":     {func(s *delivery.ApprovalSpec) { s.Note = "something else" }, "does not say what its signed record says"},
		"decision": {func(s *delivery.ApprovalSpec) { s.Decision = delivery.AcceptUnknown }, "does not say what its signed record says"},
		"digest":   {func(s *delivery.ApprovalSpec) { s.IntentDigest = "sha256:" + strings.Repeat("1", 64) }, "does not say what its signed record says"},
	} {
		t.Run(name, func(t *testing.T) {
			e, o, ss, reg := heldKeylessEnv(t)
			e.view("v", o, e.signedRef(reg, ss, alice, o), tc.edit)
			o = e.drive("p1")
			if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, tc.want) {
				t.Fatalf("a mismatched view: %+v %q", o.Status.Approvals, o.Status.Message)
			}
		})
	}
	// A record that answers another operation, even in a view of this one.
	e, o, ss, reg := heldKeylessEnv(t)
	e.view("v", o, e.signedRef(reg, ss, alice, o, func(st *delivery.ApprovalStatement) { st.Predicate.Operation.UID = "uid-other" }))
	if o = e.drive("p1"); len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "does not say what its signed record says") {
		t.Fatalf("uid: %+v %q", o.Status.Approvals, o.Status.Message)
	}
}

// TestViewApprovalSignedBeforeReholdDoesNotCount: a view is timed by its
// verified signing time, never its creation, so a decision signed before the
// operation was last held does not answer the new hold.
func TestViewApprovalSignedBeforeReholdDoesNotCount(t *testing.T) {
	e, o, ss, reg := heldKeylessEnv(t)
	e.view("v", o, e.signedRef(reg, ss, alice, o))
	o = e.getOp("p1")
	o.Status.Times.Held = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if err := e.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	o = e.drive("p1")
	if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "before the question was last asked") {
		t.Fatalf("a decision signed before the hold counted: %+v %q", o.Status.Approvals, o.Status.Message)
	}
}

// TestKeylessApprovalNeverCountsOnFreshnessStage: a certificate shows no
// login time, so where approving needs a fresh login a view never counts.
func TestKeylessApprovalNeverCountsOnFreshnessStage(t *testing.T) {
	e, o, ss, reg := heldKeylessEnv(t)
	e.op.Freshness = 15 * time.Minute
	e.view("v", o, e.signedRef(reg, ss, alice, o))
	o = e.drive("p1")
	if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "this Stage asks for a fresh login, which a signed approval cannot show") {
		t.Fatalf("%+v %q", o.Status.Approvals, o.Status.Message)
	}
	// The recorder predicts it from the published freshness: no view.
	re, ar, ro, rss := keylessRecEnv(t)
	re.recordApprovals(ar)
	re.setStage(func(s *delivery.StageStatus) { s.AuthFreshness = "15m" })
	re.pushSigned(rss, alice, ro)
	re.recordApprovals(ar)
	if len(re.views()) != 0 {
		t.Fatal("a keyless approval was viewed on a freshness Stage")
	}
}

// TestKeylessApprovalRunsSARWithoutGroups: the workload review asks as the
// identity the certificate names, with no groups, and a refusal never
// counts.
func TestKeylessApprovalRunsSARWithoutGroups(t *testing.T) {
	e, o, ss, reg := heldKeylessEnv(t)
	e.view("v", o, e.signedRef(reg, ss, alice, o))
	e.rights[aliceUser] = false
	o = e.drive("p1")
	if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, aliceUser+" cannot update") {
		t.Fatalf("%+v %q", o.Status.Approvals, o.Status.Message)
	}
	asked := false
	for _, r := range e.reviews {
		if r.User == aliceUser {
			asked = true
			if len(r.Groups) != 0 {
				t.Fatalf("review groups = %v", r.Groups)
			}
		}
	}
	if !asked {
		t.Fatal("no review asked as the keyless identity")
	}
}

// TestKeylessURISignerIsAgent: a certificate naming a workload (a URI SAN)
// is an agent, refused although IsHuman alone would call it a person: its
// record reads as a proposal, and ApproverIdentity calls it an agent too.
func TestKeylessURISignerIsAgent(t *testing.T) {
	e, o, ss, reg := heldKeylessEnv(t)
	e.op.Policy.Signers[0].Subjects = ".*"
	const uri = "https://github.com/acme/app/.github/workflows/ship.yml@refs/heads/main"
	ref := e.signedRef(reg, ss, uri, o)
	e.view("v", o, ref, func(s *delivery.ApprovalSpec) { s.By = "oidc:" + uri })
	if !e.op.Policy.IsHuman(delivery.Identity{User: "oidc:" + uri}) {
		t.Fatal("the premise: IsHuman calls the URI a person")
	}
	o = e.drive("p1")
	if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "it is a proposal: signed by agent "+uri) {
		t.Fatalf("%+v %q", o.Status.Approvals, o.Status.Message)
	}
	_, why := e.op.verifySigned(context.Background(), e.getApproval("v").Spec)
	signed, _ := openSigned(func() []byte {
		repo, _ := ocireferrer.Open(reg.host+"/delivery/approvals", ocireferrer.Options{})
		_, m, _ := strings.Cut(ref, "@")
		b, _ := deliveryrecord.Fetch(context.Background(), repo, m, delivery.PredicateApproval)
		return b
	}(), e.op.Policy)
	if signed.source != delivery.ClassBySignerURI || why == "" {
		t.Fatalf("source = %s, why %q", signed.source, why)
	}
}
