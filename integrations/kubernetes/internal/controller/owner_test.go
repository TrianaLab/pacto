/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/sigstore/sigstore-go/pkg/root"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	ownerPersonIssuer = "https://accounts.example"
	ownerGHIssuer     = "https://token.actions.githubusercontent.com"
	ownerAcmeRepo     = "https://github.com/acme/payments"
	ownerAcmeCI       = ownerAcmeRepo + "/.github/workflows/release.yml@refs/heads/main"
	ownerCommit       = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
)

var (
	teamPayments = contract.Owner{Team: "payments"}
	teamBilling  = contract.Owner{Team: "billing"}
	ownerPolicy  = &core.OwnerPolicy{
		DefaultApprovers: []core.SignerRule{{Name: "platform-admins", Issuer: ownerPersonIssuer, Subject: `^admin@acme\.example$`}},
		Teams:            map[string][]core.SignerRule{"payments": {{Name: "alice", Issuer: ownerPersonIssuer, Subject: `^alice@acme\.example$`}}},
	}
)

// ownerFixture is one Pacto over bundles in an in-process registry, signed
// by an in-process Sigstore, with the reconciler's owner check wired to them.
type ownerFixture struct {
	t       *testing.T
	ss      *testutil.Sigstore
	repo    string
	reg     *publish.Client
	r       *PactoReconciler
	bundles map[string]*loader.LoadResult
	current string
}

func newOwnerFixture(t *testing.T) *ownerFixture {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	f := &ownerFixture{t: t, ss: testutil.New(t), repo: host + "/payments", reg: publish.NewClient(authn.NewMultiKeychain(), host),
		bundles: map[string]*loader.LoadResult{}}
	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{OCI: "oci://" + f.repo + ":1.0.0"}}}
	f.r = newReconciler(pacto)
	f.r.Loader = &mockLoader{loadFn: func(_ context.Context, ref, _ string) (*loader.LoadResult, error) {
		d := f.current
		if _, at, ok := strings.Cut(ref, "@"); ok {
			d = at
		}
		if lr, ok := f.bundles[d]; ok {
			return lr, nil
		}
		return nil, errors.New("manifest unknown: " + ref)
	}}
	rule, why := core.DeriveRule("ghcr.io/acme", "main")
	if why != "" {
		t.Fatal(why)
	}
	f.r.Owners = &OwnerCheck{Policy: ownerPolicy, Root: func() (root.TrustedMaterial, error) { return f.ss.Root, nil },
		Rules: []core.PublisherRule{rule}, Registry: func(*authn.AuthConfig) publish.Registry { return f.reg }, Now: time.Now}
	return f
}

func (f *ownerFixture) signer(token string) signing.Signer {
	f.t.Helper()
	s, err := signing.Keyless(f.ss.Fulcio, f.ss.TSA, "", func(context.Context) (string, error) { return token, nil }, f.ss.Root)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *ownerFixture) person(email string) signing.Signer {
	return f.signer(testutil.Token(email, ownerPersonIssuer))
}

func (f *ownerFixture) ci() signing.Signer {
	return f.signer(testutil.CITokenAt(ownerAcmeCI, ownerGHIssuer, ownerAcmeRepo, "refs/heads/main", ownerCommit))
}

func (f *ownerFixture) agent() signing.Signer {
	return f.signer(testutil.Token("https://kubernetes.io/namespaces/agents/serviceaccounts/bot", ownerPersonIssuer))
}

// publish pushes a bundle with owner and record, signed by signers, and
// returns its digest.
func (f *ownerFixture) publish(owner contract.Owner, rec *contract.InferredRecord, signers ...signing.Signer) string {
	f.t.Helper()
	img, _ := random.Image(64, 1)
	d, err := f.reg.Write(f.t.Context(), f.repo, img)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, s := range signers {
		if err := publish.SignDigest(f.t.Context(), f.reg, s, f.repo, d); err != nil {
			f.t.Fatal(err)
		}
	}
	fsys := fstest.MapFS{"pacto.yaml": {Data: []byte(validContract)}}
	if rec != nil {
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
		if got, err := contract.ReadInferredRecord(fsys); err != nil || got == nil {
			f.t.Fatalf("the fixture record does not read: %v", err)
		}
	}
	f.bundles[d] = &loader.LoadResult{Contract: &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0", Owner: owner},
		Workload: "service"}, RawYAML: []byte(validContract), BundleFS: fsys, ResolvedRef: f.repo + ":1.0.0", ResolvedDigest: d}
	return d
}

// run reconciles with the CR resolving to digest d and returns the Pacto.
func (f *ownerFixture) run(d string) *pactov1alpha1.Pacto {
	f.t.Helper()
	f.current = d
	if _, err := f.r.Reconcile(f.t.Context(), reconcileReq("pay", "default")); err != nil {
		f.t.Fatal(err)
	}
	return getPacto(f.t, f.r, "pay", "default")
}

func declarationRecord() *contract.InferredRecord {
	return &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared,
		Asker: contract.InferredSigner{Subject: "alice@acme.example", Issuer: ownerPersonIssuer, Class: contract.SignerClassHuman}}}
}

func attestedOwnerRecord(team string) *contract.InferredRecord {
	return &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Commit: ownerCommit,
		Sources: []contract.InferredSourceFile{{Path: "repository:CODEOWNERS", Digest: "sha256:" + strings.Repeat("ab", 32)}}},
		Fields: []contract.Inferred{{Field: "service.owner", Value: team, Source: "repository:CODEOWNERS:7", Class: contract.InferredClassOwnerFile,
			Confidence: contract.ConfidenceLikely, Declare: "service:\n  owner:\n    team: " + team + "\n"}}}
}

func ownerCondition(p *pactov1alpha1.Pacto) *metav1.Condition {
	return meta.FindStatusCondition(p.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
}

func wantOwnerVerified(t *testing.T, p *pactov1alpha1.Pacto, status metav1.ConditionStatus, reason string) {
	t.Helper()
	c := ownerCondition(p)
	if c == nil || c.Status != status || c.Reason != reason {
		t.Fatalf("OwnerVerified = %+v, want %s/%s", c, status, reason)
	}
}

func statusOwnerTeam(p *pactov1alpha1.Pacto) string {
	if p.Status.Contract == nil || p.Status.Contract.Owner == nil {
		return ""
	}
	return p.Status.Contract.Owner.Team
}

func TestOwnerVerifiedTrueForDefaultApprover(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	p := f.run(f.publish(teamBilling, declarationRecord(), f.person("admin@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionTrue, pactov1alpha1.ReasonOwnerSignerAllowed)
	if statusOwnerTeam(p) != "billing" || p.Status.Contract.OwnerDisplay == "" {
		t.Fatalf("an allowed owner is shown: %+v", p.Status.Contract)
	}
	// The verdict is kept while the bundle is unchanged.
	if p = f.run(f.current); ownerCondition(p).Status != metav1.ConditionTrue || statusOwnerTeam(p) != "billing" {
		t.Fatalf("kept verdict: %+v", ownerCondition(p))
	}
}

func TestOwnerVerifiedTrueForAttestedOwnerMember(t *testing.T) {
	f := newOwnerFixture(t)
	p := f.run(f.publish(contract.Owner{}, attestedOwnerRecord("payments"), f.ci()))
	if ownerCondition(p) != nil {
		t.Fatalf("an accepted CI publish is outside the rule: %+v", ownerCondition(p))
	}
	p = f.run(f.publish(teamBilling, declarationRecord(), f.person("alice@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionTrue, pactov1alpha1.ReasonOwnerSignerAllowed)
	if !strings.Contains(ownerCondition(p).Message, "attested-owner") || statusOwnerTeam(p) != "billing" {
		t.Fatalf("a member of the attested owner sets it: %+v", ownerCondition(p))
	}
	// Without the publisher rule, the CI identity is accepted by nothing:
	// no owner is attested, and setting the first one needs an approver.
	f = newOwnerFixture(t)
	f.r.Owners.Rules = nil
	f.run(f.publish(contract.Owner{}, attestedOwnerRecord("payments"), f.ci()))
	p = f.run(f.publish(teamBilling, declarationRecord(), f.person("alice@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
}

func TestOwnerVerifiedFalseWithholdsOwner(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	p := f.run(f.publish(teamBilling, declarationRecord(), f.person("eve@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
	if p.Status.Contract == nil || p.Status.Contract.Owner != nil || p.Status.Contract.OwnerDisplay != "" {
		t.Fatalf("a refused owner is withheld: %+v", p.Status.Contract)
	}
	if !strings.Contains(ownerCondition(p).Message, "declared as team payments") {
		t.Fatalf("message: %s", ownerCondition(p).Message)
	}
	// Kept while the bundle is unchanged, and still withheld.
	if p = f.run(f.current); ownerCondition(p).Status != metav1.ConditionFalse || p.Status.Contract.Owner != nil {
		t.Fatalf("kept refusal: %+v", ownerCondition(p))
	}
}

// TestOwnerVerifiedFirstOwnerNeedsDefaultApprover: a first bundle has no
// reference, so only a default approver sets its owner.
func TestOwnerVerifiedFirstOwnerNeedsDefaultApprover(t *testing.T) {
	f := newOwnerFixture(t)
	p := f.run(f.publish(teamBilling, declarationRecord(), f.person("alice@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
	if judgedReference(ownerCondition(p)) != "" || !strings.HasSuffix(ownerCondition(p).Message, "judged against none]") {
		t.Fatalf("message: %s", ownerCondition(p).Message)
	}
	// A condition another writer left without the tail names no bundle: judged again.
	c := ownerCondition(p)
	c.Message = "edited"
	if judgedBundle(c) != "" || judgedReference(c) != "" {
		t.Fatal("a tail-less message names a bundle")
	}
}

func TestOwnerVerifiedFalseForAgentSigner(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	p := f.run(f.publish(teamBilling, declarationRecord(), f.agent()))
	wantOwnerVerified(t, p, metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotPerson)
	if statusOwnerTeam(p) != "" {
		t.Fatal("an agent's owner is withheld")
	}
}

func TestOwnerVerifiedUnknownWhenRootUnreachable(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	f.r.Owners.Root = func() (root.TrustedMaterial, error) { return nil, errors.New("tuf mirror unreachable") }
	d := f.publish(teamBilling, declarationRecord(), f.person("admin@acme.example"))
	p := f.run(d)
	wantOwnerVerified(t, p, metav1.ConditionUnknown, pactov1alpha1.ReasonOwnerSignerUnverified)
	if statusOwnerTeam(p) != "" || !strings.Contains(ownerCondition(p).Message, "tuf mirror unreachable") {
		t.Fatalf("unknown withholds the owner and says why: %+v", ownerCondition(p))
	}
	// Unknown is judged again, against the same reference, once the root is back.
	f.r.Owners.Root = func() (root.TrustedMaterial, error) { return f.ss.Root, nil }
	p = f.run(d)
	wantOwnerVerified(t, p, metav1.ConditionTrue, pactov1alpha1.ReasonOwnerSignerAllowed)
	// A reference that cannot be read is unknown too.
	f = newOwnerFixture(t)
	gone := f.publish(teamPayments, nil)
	f.run(gone)
	delete(f.bundles, gone)
	p = f.run(f.publish(teamBilling, declarationRecord(), f.person("admin@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionUnknown, pactov1alpha1.ReasonOwnerSignerUnverified)
	if !strings.Contains(ownerCondition(p).Message, "manifest unknown") {
		t.Fatalf("message: %s", ownerCondition(p).Message)
	}
}

// TestOwnerVerifiedStrippedKeysChecked: a person-signed bundle without the
// declaration keys is still checked.
func TestOwnerVerifiedStrippedKeysChecked(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	p := f.run(f.publish(teamBilling, nil, f.person("eve@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
}

// TestOwnerVerifiedBaselineLaunderingRefused: a refused declaration never
// becomes the reference of the next bundle, whatever baseline it names.
func TestOwnerVerifiedBaselineLaunderingRefused(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	refused := f.publish(teamBilling, declarationRecord(), f.person("eve@acme.example"))
	wantOwnerVerified(t, f.run(refused), metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
	rec := declarationRecord()
	rec.Declaration.Baseline = &contract.InferredBaseline{Version: "1.0.0", Bundle: f.repo + "@" + refused}
	p := f.run(f.publish(teamBilling, rec, f.person("eve@acme.example")))
	wantOwnerVerified(t, p, metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
	if statusOwnerTeam(p) != "" || !strings.Contains(ownerCondition(p).Message, "declared as team payments") {
		t.Fatalf("judged against the refused claim: %+v", ownerCondition(p))
	}
}

func TestOwnerVerifiedAbsentForOrdinaryBundle(t *testing.T) {
	f := newOwnerFixture(t)
	f.run(f.publish(teamPayments, nil))
	if p := f.run(f.current); ownerCondition(p) != nil || statusOwnerTeam(p) != "payments" {
		t.Fatalf("an unchanged ordinary bundle: %+v", ownerCondition(p))
	}
	p := f.run(f.publish(teamBilling, nil))
	if ownerCondition(p) != nil || statusOwnerTeam(p) != "billing" {
		t.Fatalf("an unsigned bundle without declaration keys is outside the rule: %+v", ownerCondition(p))
	}
	// A declaration that sets the same owner changes nothing.
	p = f.run(f.publish(teamBilling, declarationRecord(), f.person("eve@acme.example")))
	if ownerCondition(p) != nil || statusOwnerTeam(p) != "billing" {
		t.Fatalf("an unchanged owner is not checked: %+v", ownerCondition(p))
	}
	// A refusal clears once an ordinary bundle replaces it.
	wantOwnerVerified(t, f.run(f.publish(teamPayments, declarationRecord(), f.person("eve@acme.example"))),
		metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed)
	if p = f.run(f.publish(teamBilling, nil)); ownerCondition(p) != nil {
		t.Fatalf("stale condition: %+v", ownerCondition(p))
	}
	// Without an owner check nothing is set.
	f.r.Owners = nil
	if p = f.run(f.publish(teamPayments, declarationRecord(), f.person("eve@acme.example"))); ownerCondition(p) != nil {
		t.Fatalf("no check: %+v", ownerCondition(p))
	}
}

func TestOwnerVerifiedAbsentForInlineContract(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{Inline: validContract}}}
	r := newReconciler(pacto)
	r.Loader = loader.New()
	r.Owners = &OwnerCheck{Policy: ownerPolicy, Root: func() (root.TrustedMaterial, error) { t.Fatal("no root is read"); return nil, nil }}
	if _, err := r.Reconcile(t.Context(), reconcileReq("pay", "default")); err != nil {
		t.Fatal(err)
	}
	if p := getPacto(t, r, "pay", "default"); ownerCondition(p) != nil || statusOwnerTeam(p) != "team-a" {
		t.Fatalf("inline: %+v %+v", ownerCondition(p), p.Status.Contract)
	}
}

func TestOwnerVerifiedDoesNotChangeContractStatus(t *testing.T) {
	statusOf := func(check bool) *pactov1alpha1.Pacto {
		f := newOwnerFixture(t)
		if !check {
			f.r.Owners = nil
		}
		f.run(f.publish(teamPayments, nil))
		return f.run(f.publish(teamBilling, declarationRecord(), f.person("eve@acme.example")))
	}
	with, without := statusOf(true), statusOf(false)
	if with.Status.ContractStatus != without.Status.ContractStatus || with.Status.ContractStatus == "" ||
		meta.FindStatusCondition(with.Status.Conditions, pactov1alpha1.ConditionContractValid).Status != metav1.ConditionTrue {
		t.Fatalf("contract status %q with the check, %q without", with.Status.ContractStatus, without.Status.ContractStatus)
	}
}
