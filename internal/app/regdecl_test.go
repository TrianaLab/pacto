package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// declFiles is an invoices declaration at version by asker (state declared
// unless draft), naming baseline (nil for none), in domain.
func declFiles(version, asker, domain string, draft bool, baseline *contract.InferredBaseline) map[string]string {
	y := "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: " + version + "\n"
	if domain != "" {
		y += "  domain: " + domain + "\n"
	}
	y += "  owner:\n    team: billing\nworkload: service\n"
	d := &contract.InferredDeclaration{State: contract.DeclarationDeclared, Baseline: baseline,
		Asker: contract.InferredSigner{Subject: asker, Issuer: personIssuer, Class: contract.SignerClassHuman}}
	if draft {
		d.State, d.Asker.Class = contract.DeclarationDraft, contract.SignerClassAgent
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: d}
	return map[string]string{"pacto.yaml": y, contract.InferredRecordFile: string(rec.Marshal())}
}

// trustingAcme is a run trust whose installation signer policy lets people
// of acme.com declare under the env's private Sigstore.
func (e *declareEnv) trustingAcme(t *testing.T) *declarationTrust {
	t.Helper()
	return declarationTrustFrom(t.Context(), impactTrust(ImpactOptions{Signers: &delivery.ClassPolicy{Signers: []delivery.PersonSigner{
		{Issuer: personIssuer, Subjects: `.+@acme\.com`, TrustedRoot: string(e.ss.RootJSON)}}}}))
}

// noPublicGood fails the test when public-good Sigstore's root is fetched.
func noPublicGood(t *testing.T) {
	t.Helper()
	old := publicGoodRoot
	t.Cleanup(func() { publicGoodRoot = old })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { t.Fatal("public-good root fetched"); return nil, nil }
}

// publicGoodIs makes public-good Sigstore's root tm, or err.
func publicGoodIs(t *testing.T, tm root.TrustedMaterial, err error) {
	t.Helper()
	old := publicGoodRoot
	t.Cleanup(func() { publicGoodRoot = old })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return tm, err }
}

func unsignedBelow(context.Context, string, string) error { return publish.ErrNoSignature }

// counted asks t whether repo:tag (digest d) counts as an invoices declaration.
func (e *declareEnv) counted(t *testing.T, tr *declarationTrust, reg publish.Registry, tag, d string) (*registryDeclaration, string, error) {
	t.Helper()
	repo := e.host + "/acme/invoices"
	tags, _ := e.reg.Tags(t.Context(), repo)
	return tr.countedDeclaration(t.Context(), reg, wantService{Name: "invoices"}, repo, tags, tag, d, unsignedBelow)
}

// refCounter counts the referrer listings that reach a registry.
type refCounter struct {
	publish.Registry
	n int
}

func (r *refCounter) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	r.n++
	return r.Registry.Referrers(ctx, repo, digest, artifactType)
}

// layerFails answers 500 for every layer read.
type layerFails struct{ publish.Registry }

func (layerFails) Layer(context.Context, string, string) ([]byte, map[string]string, error) {
	return nil, nil, errors.New("500 Internal Server Error")
}

func TestCountedDeclarationCounts(t *testing.T) {
	noPublicGood(t)
	e := newDeclareEnv(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req("ledger"))
	_, digest, _ := strings.Cut(res.Published, "@")
	tr := e.trustingAcme(t)
	d, why, err := e.counted(t, tr, e.reg, res.Tag, digest)
	if err != nil || why != "" || d == nil || d.Signer.Subject != "ana@acme.com" || d.Version != firstDeclaration || d.Record.Declaration == nil {
		t.Fatalf("%v %q %+v", err, why, d)
	}
	again, _, _ := e.counted(t, tr, layerFails{e.reg}, res.Tag, digest)
	if again != d {
		t.Fatal("the answer was not remembered for the run")
	}
}

// signedDecl publishes files at tag in invoices signed by tok ("" unsigned).
func (e *declareEnv) signedDecl(t *testing.T, tag, tok string, files map[string]string) string {
	t.Helper()
	var s *signing.Signer
	if tok != "" {
		sg := e.signer(t, tok)
		s = &sg
	}
	return e.publishBundle(t, e.host+"/acme/invoices", tag, files, s)
}

func TestCountedDeclarationUnsignedIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", "", declFiles("0.1.0", "ana@acme.com", "", false, nil))
	if got, why, err := e.counted(t, e.trustingAcme(t), e.reg, "0.1.0", d); got != nil || err != nil || why != "declaration unsigned: no signature names the asker ana@acme.com" {
		t.Fatalf("%v %q %+v", err, why, got)
	}
}

func TestCountedDeclarationOtherSignerIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("bob@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	if got, why, err := e.counted(t, e.trustingAcme(t), e.reg, "0.1.0", d); got != nil || err != nil || why != "declaration signed by bob@acme.com, not the asker ana@acme.com" {
		t.Fatalf("%v %q %+v", err, why, got)
	}
}

func TestCountedDeclarationCISignerIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", acmeCIToken(), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	if got, why, err := e.counted(t, e.trustingAcme(t), e.reg, "0.1.0", d); got != nil || err != nil || !strings.Contains(why, "(ci), not the asker ana@acme.com") {
		t.Fatalf("%v %q %+v", err, why, got)
	}
}

func TestCountedDeclarationDraftIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", true, nil))
	if got, why, err := e.counted(t, e.trustingAcme(t), e.reg, "0.1.0", d); got != nil || err != nil || why != "a declaration draft is not a version" {
		t.Fatalf("%v %q %+v", err, why, got)
	}
}

func TestCountedDeclarationSignerNotAllowed(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("eve@evil.com"), declFiles("0.1.0", "eve@evil.com", "", false, nil))
	if got, why, err := e.counted(t, e.trustingAcme(t), e.reg, "0.1.0", d); got != nil || err != nil || !strings.HasPrefix(why, "declaration by eve@evil.com not counted: ") {
		t.Fatalf("%v %q %+v", err, why, got)
	}
	unread := declarationTrustFrom(t.Context(), func(context.Context) (Installation, error) { return Installation{}, errors.New("forbidden") })
	if got, why, err := e.counted(t, unread, e.reg, "0.1.0", d); got != nil || err != nil || why != "the installation signer policy could not be read (forbidden)" {
		t.Fatalf("%v %q %+v", err, why, got)
	}
}

func TestCountedDeclarationOtherServiceIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	files := declFiles("0.1.0", "ana@acme.com", "", false, nil)
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "name: invoices", "name: payments", 1)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), files)
	refs := &refCounter{Registry: e.reg}
	if got, why, err := e.counted(t, e.trustingAcme(t), refs, "0.1.0", d); got != nil || err != nil || why != "declares payments, not invoices" || refs.n != 0 {
		t.Fatalf("%v %q %+v %d", err, why, got, refs.n)
	}
	other := e.signedDecl(t, "0.2.0", personToken("ana@acme.com"), declFiles("0.2.0", "ana@acme.com", "payments", false, nil))
	if _, why, _ := e.counted(t, e.trustingAcme(t), e.reg, "0.2.0", other); why != "declares invoices (domain payments), not invoices" {
		t.Fatal(why)
	}
}

func TestCountedDeclarationRetaggedIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	if err := e.reg.Tag(t.Context(), e.host+"/acme/invoices", d, "9.0.0"); err != nil {
		t.Fatal(err)
	}
	tr := e.trustingAcme(t)
	refs := &refCounter{Registry: e.reg}
	if got, why, err := e.counted(t, tr, refs, "9.0.0", d); got != nil || err != nil || why != "declares version 0.1.0, read under tag 9.0.0" || refs.n != 0 {
		t.Fatalf("%v %q %+v %d", err, why, got, refs.n)
	}
	if got, why, err := e.counted(t, tr, e.reg, "0.1.0", d); got == nil || err != nil {
		t.Fatalf("%v %q", err, why)
	}
}

func TestCountedDeclarationVersionJumpIgnored(t *testing.T) {
	noPublicGood(t)
	e := newDeclareEnv(t)
	tok := personToken("ana@acme.com")
	first := e.signedDecl(t, "0.1.0", tok, declFiles("0.1.0", "ana@acme.com", "", false, nil))
	repo := e.host + "/acme/invoices"
	jump := e.signedDecl(t, "99.0.0", tok, declFiles("99.0.0", "ana@acme.com", "", false, &contract.InferredBaseline{Version: "0.1.0", Bundle: repo + "@" + first}))
	tr := e.trustingAcme(t)
	tags, _ := e.reg.Tags(t.Context(), repo)
	verify := func(ctx context.Context, tag, ref string) error {
		_, digest, _ := strings.Cut(ref, "@")
		d, why, err := tr.countedDeclaration(ctx, e.reg, wantService{Name: "invoices"}, repo, tags, tag, digest, unsignedBelow)
		if d == nil && err == nil {
			return publish.NotCounted{Reason: why}
		}
		return err
	}
	got, why, err := tr.countedDeclaration(t.Context(), e.reg, wantService{Name: "invoices"}, repo, tags, "99.0.0", jump, verify)
	if got != nil || err != nil || !strings.HasPrefix(why, "version 99.0.0 does not follow 0.1.0: the next version is 0.1.") {
		t.Fatalf("%v %q %+v", err, why, got)
	}
}

func TestCountedDeclarationPrivateRootCounts(t *testing.T) {
	noPublicGood(t)
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	tr := e.trustingAcme(t)
	if got, why, err := e.counted(t, tr, e.reg, "0.1.0", d); got == nil || err != nil || tr.publicGood {
		t.Fatalf("%v %q", err, why)
	}
}

func TestCountedDeclarationNoPolicyNote(t *testing.T) {
	e := newDeclareEnv(t)
	publicGoodIs(t, e.ss.Root, nil)
	d := e.signedDecl(t, "0.1.0", personToken("ana@example.org"), declFiles("0.1.0", "ana@example.org", "", false, nil))
	tr := declarationTrustFrom(t.Context(), nil)
	if got, why, err := e.counted(t, tr, e.reg, "0.1.0", d); got == nil || err != nil || !tr.publicGood {
		t.Fatalf("%v %q", err, why)
	}
	none := declarationTrustFrom(t.Context(), func(context.Context) (Installation, error) { return Installation{}, ErrNoInstallationTrust })
	if !none.publicGood {
		t.Fatal("no installation trust is no policy")
	}
}

func TestCountedDeclarationNoRootReachableNotChecked(t *testing.T) {
	e := newDeclareEnv(t)
	publicGoodIs(t, nil, errors.New("tuf down"))
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	got, why, err := e.counted(t, declarationTrustFrom(t.Context(), nil), e.reg, "0.1.0", d)
	if got != nil || err != nil || why != "declaration "+e.host+"/acme/invoices@"+d+" not checked: no trusted root reachable" {
		t.Fatalf("%v %q", err, why)
	}
}

func TestCountedDeclarationConfiguredRootUnparsableIsError(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	tr := declarationTrustFrom(t.Context(), impactTrust(ImpactOptions{Signers: &delivery.ClassPolicy{Signers: []delivery.PersonSigner{
		{Issuer: personIssuer, Subjects: ".*", TrustedRoot: "not json"}}}}))
	if got, _, err := e.counted(t, tr, e.reg, "0.1.0", d); got != nil || err == nil || !strings.Contains(err.Error(), "the trusted root of the installation signer") {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestCountedDeclarationUnreadIsError(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	if got, _, err := e.counted(t, e.trustingAcme(t), layerFails{e.reg}, "0.1.0", d); got != nil || err == nil || !strings.Contains(err.Error(), "reading the signatures of") {
		t.Fatalf("%v %+v", err, got)
	}
	gone := "sha256:" + strings.Repeat("0", 64)
	if got, _, err := e.counted(t, e.trustingAcme(t), e.reg, "0.1.0", gone); got != nil || err == nil {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestCountedDeclarationNoRecordReadsNoSignature(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	d := e.signedDecl(t, "1.0.0", "", e.invoicesBaseline("1.0.0", ""))
	refs := &refCounter{Registry: e.reg}
	if got, why, err := e.counted(t, e.trustingAcme(t), refs, "1.0.0", d); got != nil || err != nil || why != "" || refs.n != 0 {
		t.Fatalf("%v %q %d", err, why, refs.n)
	}
	files := e.invoicesBaseline("1.1.0", "")
	files[contract.InferredRecordFile] = "format: nope\n"
	bad := e.signedDecl(t, "1.1.0", "", files)
	if _, why, _ := e.counted(t, e.trustingAcme(t), refs, "1.1.0", bad); !strings.HasPrefix(why, "its pacto.inferred.yaml does not parse: ") || refs.n != 0 {
		t.Fatalf("%q %d", why, refs.n)
	}
}

func TestCountedDeclarationChecksIdentityBeforeSignature(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	repo := e.host + "/acme/invoices"
	base := e.signedDecl(t, "0.1.0", "", e.invoicesBaseline("0.1.0", ""))
	// the history check fails before any signature of the declaration is read
	d := e.signedDecl(t, "0.2.0", personToken("ana@acme.com"), declFiles("0.2.0", "ana@acme.com", "", false, &contract.InferredBaseline{Version: "0.1.0", Bundle: repo + "@" + base}))
	refs := &refCounter{Registry: e.reg}
	if got, why, err := e.counted(t, e.trustingAcme(t), refs, "0.2.0", d); got != nil || err != nil || why != "no final release below 0.2.0 counts as its baseline" || refs.n != 0 {
		t.Fatalf("%v %q %d", err, why, refs.n)
	}
}

// TestCountedDeclarationSignerWithoutRootUsesPublicGood: an installation
// signer naming no trusted root is checked under public-good Sigstore's.
func TestCountedDeclarationSignerWithoutRootUsesPublicGood(t *testing.T) {
	e := newDeclareEnv(t)
	publicGoodIs(t, e.ss.Root, nil)
	d := e.signedDecl(t, "0.1.0", personToken("ana@acme.com"), declFiles("0.1.0", "ana@acme.com", "", false, nil))
	tr := declarationTrustFrom(t.Context(), impactTrust(ImpactOptions{Signers: &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: `.+@acme\.com`}}}}))
	if got, why, err := e.counted(t, tr, e.reg, "0.1.0", d); got == nil || err != nil {
		t.Fatalf("%v %q", err, why)
	}
}
