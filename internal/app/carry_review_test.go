package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// TestDeclarationSourceCitationNotADeclaration: a record citing a bundle
// with no declaration block (a forged tag citing a plain CI release) drops
// every field it names, with a reason, and never crashes the carry.
func TestDeclarationSourceCitationNotADeclaration(t *testing.T) {
	e := newDeclCIEnv(t)
	plain := e.de.publishBundle(t, e.repo, "0.1.0", map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: 0.1.0\n"}, nil)
	tags, _ := e.inner.Tags(t.Context(), e.repo)
	base := carryBase{repo: e.repo, version: "0.1.1", digest: "sha256:" + strings.Repeat("1", 64), b: citing(t, e.repo+"@"+plain)}
	d, dropped, _, err := e.trust(t).declarationSource(t.Context(), e.inner, wantService{Name: "invoices"}, tags, base)
	if d != nil || err != nil || len(dropped) != 1 || dropped[0].dropped != "it is not a registry declaration" {
		t.Fatalf("%v %+v %+v", err, d, dropped)
	}
	var res carryResult
	raw := []byte("service:\n  name: invoices\n")
	if fields := mergeCarry(&raw, &contract.Contract{}, nil, dropped, &res); len(fields) != 0 || res.Carried[0].State != ci.CarriedDropped {
		t.Fatalf("%+v", res.Carried)
	}
}

// TestMergeCarryNothingToCarry: a candidate with neither an owner nor an
// entry is dropped, never dereferenced.
func TestMergeCarryNothingToCarry(t *testing.T) {
	var res carryResult
	raw := []byte("service:\n  name: invoices\n")
	cands := []carryCandidate{{Field: "dependencies[ledger].compatibility", Value: "^2"}, {Field: "service.owner"}}
	if fields := mergeCarry(&raw, &contract.Contract{}, nil, cands, &res); len(fields) != 0 ||
		res.Carried[0].State != ci.CarriedDropped || res.Carried[1].Reason != "nothing to carry" {
		t.Fatalf("%+v", res.Carried)
	}
}

// TestDeclarationSourceErrorNamesCitedDeclaration: a check that fails on the
// declaration a CI version cites names that declaration, never the CI
// version, as the one that could not be checked.
func TestDeclarationSourceErrorNamesCitedDeclaration(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	ref := e.repo + "@" + d
	baseDigest := "sha256:" + strings.Repeat("1", 64)
	base := carryBase{repo: e.repo, version: "0.1.1", digest: baseDigest, b: citing(t, ref)}
	tags, _ := e.inner.Tags(t.Context(), e.repo)
	for name, reg := range map[string]publish.Registry{"pull": pullFails{e.inner}, "signatures": referrersFail{e.inner}} {
		_, _, _, err := e.trust(t).declarationSource(t.Context(), reg, wantService{Name: "invoices"}, tags, base)
		if err == nil || !strings.HasPrefix(err.Error(), "registry declaration "+ref+" could not be checked: ") || strings.Contains(err.Error(), baseDigest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestDeclarationSourceUnreadableRecordNoted: a baseline record this
// binary cannot read says so, never reads as citing nothing.
func TestDeclarationSourceUnreadableRecordNoted(t *testing.T) {
	b := bundleOf(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: 0.1.1\n",
		contract.InferredRecordFile: "format: " + contract.InferredRecordFormat + "\nfromANewerPacto: 1\n"})
	base := carryBase{repo: "r.io/acme/invoices", version: "0.1.1", digest: "sha256:" + strings.Repeat("1", 64), b: b}
	d, dropped, notes, err := declarationTrustFrom(t.Context(), nil).declarationSource(t.Context(), noRequests{nil, t}, wantService{Name: "invoices"}, nil, base)
	if d != nil || dropped != nil || err != nil || len(notes) != 1 || !strings.HasPrefix(notes[0], "baseline r.io/acme/invoices@sha256:1111") ||
		!strings.HasSuffix(notes[0], "; registry declarations not carried") {
		t.Fatalf("%v %q", err, notes)
	}
}

// TestPinAcceptedOverriddenValueIsPactoYAML: an acceptance pacto.yaml
// overrides records the value pacto.yaml publishes, the accepted one in
// the reason.
func TestPinAcceptedOverriddenValueIsPactoYAML(t *testing.T) {
	js := []delivery.JudgedAcceptance{accepting("0.1.0", "^2.3.0")}
	raw := []byte("dependencies:\n  - name: ledger\n    compatibility: \"^1.0.0\"\n")
	deps := []contract.Dependency{{Name: "ledger", Compatibility: "^1.0.0"}}
	var res carryResult
	pinAccepted(js, &raw, deps, &res, nil)
	if c := res.Carried[0]; c.State != ci.CarriedOverridden || c.Value != "^1.0.0" || c.From != carriedFromPactoYAML || c.Reason != "registry accepts ^2.3.0" {
		t.Fatalf("%+v", c)
	}
	// A ref pacto.yaml declares is the value it publishes, not the compatibility.
	js[0].Record.Ranges = []delivery.AcceptedRange{{Field: "dependencies[ledger].ref", Value: "oci://r.io/acme/ledger-v2"}}
	raw = []byte("dependencies:\n  - name: ledger\n    ref: oci://r.io/acme/ledger\n    compatibility: \"^1.0.0\"\n")
	deps = []contract.Dependency{{Name: "ledger", Ref: "oci://r.io/acme/ledger", Compatibility: "^1.0.0"}}
	res = carryResult{}
	pinAccepted(js, &raw, deps, &res, nil)
	if c := res.Carried[0]; c.State != ci.CarriedOverridden || c.Value != "oci://r.io/acme/ledger" || c.From != carriedFromPactoYAML {
		t.Fatalf("ref: %+v", c)
	}
}

// TestFillRowBaselineMovedDropsPlanDigest: once publish measured the
// version from another release than the plan read, the plan's digest no
// longer names the baseline.
func TestFillRowBaselineMovedDropsPlanDigest(t *testing.T) {
	r := ci.Row{Baseline: "0.1.0", BaselineDigest: "sha256:" + strings.Repeat("a", 64), Derived: true}
	fillRow(&r, nil, &publish.Result{Plan: publish.Plan{Version: "0.3.0", Baseline: "0.2.0"}}, errors.New("refused"))
	if r.Baseline != "0.2.0" || r.BaselineDigest != "" {
		t.Fatalf("%+v", r)
	}
	same := ci.Row{Baseline: "0.1.0", BaselineDigest: "sha256:" + strings.Repeat("a", 64), Derived: true}
	fillRow(&same, nil, &publish.Result{Plan: publish.Plan{Version: "0.2.0", Baseline: "0.1.0"}}, errors.New("refused"))
	if same.BaselineDigest == "" {
		t.Fatalf("%+v", same)
	}
}

// TestCIDeclaredVersionRerunAfterCarriedPublish: a declared version that
// published a carried owner reads unchanged on the next run, never changed
// against its own carried content.
func TestCIDeclaredVersionRerunAfterCarriedPublish(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.write(t, map[string]string{"invoices/pacto.yaml": e.contract("invoices", "0.2.0", "", "")})
	if r := e.publishCI(t, "r1"); r.State != ci.StatePublished || carriedField(t, r, "service.owner").State != ci.CarriedCarried {
		t.Fatalf("%+v", r)
	}
	r, _, _ := e.plan(t)
	if r.Changed != "no" || r.Baseline != "0.2.0" || carriedField(t, r, "service.owner").State != ci.CarriedCarried {
		t.Fatalf("%+v", r)
	}
	if r := e.publishCI(t, "r1"); r.State != ci.StateUnchanged {
		t.Fatalf("%+v", r)
	}
}

// TestCarryOwnerPolicyBesideBundleRepository: the owner policy of a
// carried owner is looked up beside the bundle repository, as declare and
// the acceptance store look, never one level above it.
func TestCarryOwnerPolicyBesideBundleRepository(t *testing.T) {
	e := newDeclCIEnv(t)
	e.ownerPolicy(t, "")
	pub, err := os.ReadFile(filepath.Join(filepath.Dir(e.key), "cosign.pub"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(TrustEnv, trustFile(t, delivery.TrustedKey{Name: "rec", PublicKey: string(pub), Streams: []string{"prod/*/*"}}))
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	r, _, _ := e.plan(t)
	if c := carriedField(t, r, "service.owner"); c.State != ci.CarriedDropped || !strings.Contains(c.Reason, "beside "+e.repo) {
		t.Fatalf("%+v", c)
	}
}

// TestDeclarationCandidatesUnjudgedOwnerSaysUnverified: a range whose owner
// was never judged is dropped as unverified, never as routed to a
// non-owner.
func TestDeclarationCandidatesUnjudgedOwnerSaysUnverified(t *testing.T) {
	d := &registryDeclaration{Ref: "r", Version: "0.1.0", Signer: fleet.VerifiedSigner{Subject: "ana@acme.com", Issuer: personIssuer, Class: contract.SignerClassHuman},
		Bundle: bundleOf(t, map[string]string{"pacto.yaml": "service:\n  name: invoices\n"}),
		Record: &contract.InferredRecord{Declaration: &contract.InferredDeclaration{Accepted: ledgerAccepted}}}
	got := declarationCandidates(d, ownerVerdict{Reason: "owner history longer than 32 releases"}, acceptNow)
	if len(got) != 1 || got[0].dropped != "owner unverified: owner history longer than 32 releases" {
		t.Fatalf("%+v", got)
	}
}

// ciRevision is a verified CI publish of invoices at version with owner.
func ciRevision(version string, owner contract.Owner) fleet.ContractRevision {
	ci := fleet.VerifiedSigner{Subject: "https://github.com/acme/invoices/.github/workflows/ci.yaml@refs/heads/main", Issuer: "https://token.actions.githubusercontent.com", Class: "ci"}
	return fleet.ContractRevision{Key: fleet.RevisionKey("sha256:" + version), Service: "invoices", Version: version, Owner: owner,
		Provenance: &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Signer: ci.Subject, Issuer: ci.Issuer}, Signers: []fleet.VerifiedSigner{ci}}}
}

// TestJudgeOwnerHistoryWindow: a declaration every carry re-cites ages out
// of the newest releases; a CI publish in them that already set the
// declared owner keeps it carried. Another owner, or no CI publish, is the
// spent cap; a declaration that is not among the releases says so.
func TestJudgeOwnerHistoryWindow(t *testing.T) {
	billing, payments := contract.Owner{Team: "billing"}, contract.Owner{Team: "payments"}
	d := &registryDeclaration{Ref: "r.io/acme/invoices@sha256:decl", Bundle: bundleOf(t, map[string]string{"pacto.yaml": "service:\n  name: invoices\n  owner: {team: billing}\n"})}
	policy := &delivery.OwnerPolicy{}
	judge := ciOwnerJudge(ownerJudge("invoices", policy, "", acceptNow))
	window := []fleet.ContractRevision{ciRevision("0.40.0", billing)}
	if v := judgeOwnerHistory(t.Context(), window, true, d, "sha256:decl", "r.io/acme/invoices", judge, policy, ""); !v.Allowed || !v.Owner.Claim.Owner.Equal(billing) {
		t.Fatalf("aged out: %+v", v)
	}
	if v := judgeOwnerHistory(t.Context(), []fleet.ContractRevision{ciRevision("0.40.0", payments)}, true, d, "sha256:decl", "r.io/acme/invoices", judge, policy, ""); v.Allowed ||
		v.Reason != "owner history longer than 32 releases" {
		t.Fatalf("other owner: %+v", v)
	}
	if v := judgeOwnerHistory(t.Context(), window, false, d, "sha256:decl", "r.io/acme/invoices", judge, policy, ""); v.Allowed ||
		v.Reason != "declaration r.io/acme/invoices@sha256:decl is not among the releases of r.io/acme/invoices" {
		t.Fatalf("not listed: %+v", v)
	}
}

// TestSignerRootsMissingIssuerNotChecked: a policy signer whose public-good
// root could not be fetched has its declarations not checked, never read
// under another signer's root and called unsigned.
func TestSignerRootsMissingIssuerNotChecked(t *testing.T) {
	ss := testutil.New(t)
	publicGoodIs(t, nil, errors.New("tuf unreachable"))
	rr := signerRoots([]delivery.PersonSigner{{Issuer: "https://private.example", TrustedRoot: string(ss.RootJSON)}, {Issuer: personIssuer}})
	if rr.noRoot || rr.err != nil || len(rr.roots) != 1 || !slices.Equal(rr.missing, []string{personIssuer}) {
		t.Fatalf("%+v", rr)
	}
	tr := &declarationTrust{roots: func() rootsRead { return rr }}
	_, why, err := tr.signers(t.Context(), nil, "r.io/acme/invoices", "sha256:d", personIssuer)
	if err != nil || why != "declaration r.io/acme/invoices@sha256:d not checked: no trusted root reachable for "+personIssuer {
		t.Fatalf("%v %q", err, why)
	}
}

// TestOwnerHistoryReadsEveryRoot: a declaration signed under the second
// policy signer's root is judged by who signed it, as it was counted.
func TestOwnerHistoryReadsEveryRoot(t *testing.T) {
	e := newDeclCIEnv(t)
	digest := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	tags, _ := e.inner.Tags(t.Context(), e.repo)
	d, _, _, err := e.trust(t).declarationSource(t.Context(), e.inner, wantService{Name: "invoices"}, tags,
		carryBase{repo: e.repo, version: firstDeclaration, digest: digest, b: bundleOf(t, e.declFiles(firstDeclaration, "ana@acme.com", ownerBilling, nil, nil))})
	if err != nil || d == nil {
		t.Fatal(err)
	}
	other := testutil.New(t)
	rootOf := func(tm root.TrustedMaterial) signerRoot { return signerRoot{tm, len(tm.RekorLogs()) > 0} }
	tr := &declarationTrust{roots: func() rootsRead { return rootsRead{roots: []signerRoot{rootOf(other.Root), rootOf(e.ss.Root)}} }}
	policy, _ := delivery.ReadOwnerPolicy(e.svc.OwnerPolicyPath)
	if v, err := tr.ownerHistory(t.Context(), e.inner, e.repo, tags, d, policy, "", acceptNow); err != nil || !v.Allowed {
		t.Fatalf("%+v %v", v, err)
	}
}

// TestCIPublishRefusesUncarriedDeclarationBaseline: a release the plan
// read but publish skips (pushed without the publisher's signature) hides
// the declaration publish measures from; the publish fails instead of
// dropping the declared owner without a word.
func TestCIPublishRefusesUncarriedDeclarationBaseline(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.unsignedRelease(t, "0.2.0", "")
	e.folder(t, "", nil)
	before := e.reg.writes.Load()
	r := e.publishCI(t, "r1")
	if r.State != ci.StateFailed || !strings.Contains(r.Reason, "the plan carried registry decisions from 0.2.0, the publish measures the version from "+firstDeclaration) ||
		!strings.Contains(r.Reason, "service.owner") || e.reg.writes.Load() != before {
		t.Fatalf("%+v", r)
	}
}

// TestCICarriesFromFinalBelowCandidate: a candidate above the declaration
// is never the release plan mode carries from, so the publish carries the
// declared owner it measures the version from.
func TestCICarriesFromFinalBelowCandidate(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.unsignedRelease(t, "0.2.0-rc.1", "")
	e.folder(t, "", nil)
	r := e.publishCI(t, "r1")
	raw, _ := e.published(t, r)
	if r.State != ci.StatePublished || carriedField(t, r, "service.owner").State != ci.CarriedCarried || !strings.Contains(raw, "team: billing") {
		t.Fatalf("%+v\n%s", r, raw)
	}
}
