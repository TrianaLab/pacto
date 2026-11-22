package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// ledgerDeclared is a declaration's ledger dependency with the range it
// accepted.
const ledgerDeclared = "dependencies:\n  - name: ledger\n    ref: oci://r.io/acme/ledger\n    compatibility: \"^2.3.0\"\n"

// ledgerAccepted is the range a declaration accepted on ledger.
var ledgerAccepted = []contract.InferredAcceptance{{Field: "dependencies[ledger].compatibility", Range: "^2.3.0"}}

// declCIEnv is a repository holding invoices, whose bundle repository holds
// a declaration ana signed, judged by pacto ci under the test Sigstore and
// an owner policy that makes ana a default approver.
type declCIEnv struct {
	*ciPubEnv
	de   *declareEnv
	ss   *testutil.Sigstore
	name string // the service declared and planned, invoices unless set
	repo string
}

// service makes name the service the env declares and plans.
func (e *declCIEnv) service(name string) {
	e.name, e.repo = name, e.host+"/acme/"+name
}

func newDeclCIEnv(t *testing.T) *declCIEnv {
	t.Helper()
	noAmbient(t)
	noPublicGood(t)
	e := &declCIEnv{ciPubEnv: newCIPub(t), ss: testutil.New(t)}
	e.de = &declareEnv{host: e.host, reg: e.inner, ss: e.ss}
	e.service("invoices")
	e.ownerPolicy(t, `{"defaultApprovers":[{"name":"admins","issuer":"`+personIssuer+`","subject":"^ana@acme\\.com$"}]}`)
	return e
}

// ownerPolicy mounts body as the owner policy ("" mounts none).
func (e *declCIEnv) ownerPolicy(t *testing.T, body string) {
	t.Helper()
	e.svc.OwnerPolicyPath = ""
	if body == "" {
		return
	}
	p := filepath.Join(t.TempDir(), "owners.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	e.svc.OwnerPolicyPath = p
}

// declaration publishes asker's declaration of invoices whose pacto.yaml
// holds extra and whose record accepted accepted, at the version it
// derives over what the repository holds; it returns its digest.
func (e *declCIEnv) declaration(t *testing.T, asker, extra string, accepted []contract.InferredAcceptance) string {
	t.Helper()
	files := e.declFiles("0.0.0", asker, extra, accepted, nil)
	version, baseline := e.nextDeclaration(t, files)
	files = e.declFiles(version, asker, extra, accepted, baseline)
	s := e.de.signer(t, personToken(asker))
	return e.de.publishBundle(t, e.repo, version, files, &s)
}

// declFiles is asker's declaration of invoices at version.
func (e *declCIEnv) declFiles(version, asker, extra string, accepted []contract.InferredAcceptance, baseline *contract.InferredBaseline) map[string]string {
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Baseline: baseline,
		Asker: contract.InferredSigner{Subject: asker, Issuer: personIssuer, Class: contract.SignerClassHuman}, Accepted: accepted}}
	return map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: " + e.name + "\n  version: " + version + "\n" + extra,
		contract.InferredRecordFile: string(rec.Marshal())}
}

// nextDeclaration is the version a declaration of files derives, as pacto
// declare derives it, and the baseline it names.
func (e *declCIEnv) nextDeclaration(t *testing.T, files map[string]string) (string, *contract.InferredBaseline) {
	t.Helper()
	ctx := t.Context()
	tags, _ := e.inner.Tags(ctx, e.repo)
	if len(tags) == 0 {
		return firstDeclaration, nil
	}
	b := bundleOf(t, files)
	d, err := publish.DeriveVersion(ctx, e.inner, publish.DeriveInput{Repo: e.repo, Local: b, Unchanged: Unchanged,
		Publish: &contract.Publish{Bundle: "oci://" + e.repo}, Verify: acceptBelow})
	if err != nil {
		t.Fatal(err)
	}
	return d.Version, &contract.InferredBaseline{Version: d.Baseline, Bundle: e.repo + "@" + d.BaselineDigest}
}

// unsignedRelease pushes an unsigned invoices release at version holding
// extra.
func (e *declCIEnv) unsignedRelease(t *testing.T, version, extra string) {
	t.Helper()
	e.de.publishBundle(t, e.repo, version, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: " + version + "\n" + extra}, nil)
}

// folder writes the repository's invoices pacto.yaml with extra and the
// other files.
func (e *declCIEnv) folder(t *testing.T, extra string, files map[string]string) {
	t.Helper()
	all := map[string]string{e.name + "/pacto.yaml": e.contract(e.name, "", extra, "")}
	for p, c := range files {
		all[e.name+"/"+p] = c
	}
	e.write(t, all)
}

// ledgerManifest is a workload that calls ledger, so discovery infers it.
var ledgerManifest = map[string]string{"deploy/app.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: invoices\nspec:\n  template:\n    spec:\n" +
	"      containers:\n        - name: app\n          env:\n            - name: LEDGER_URL\n              value: http://ledger:8080\n"}

func (e *declCIEnv) opts(bundles map[string]*contract.Bundle) CIOptions {
	return CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles,
		Signers: &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: ".*", TrustedRoot: string(e.ss.RootJSON)}}}}
}

func (e *declCIEnv) plan(t *testing.T) (ci.Row, *ci.Plan, *contract.Bundle) {
	t.Helper()
	bundles := map[string]*contract.Bundle{}
	p, _, err := e.svc.CIPlan(t.Context(), e.opts(bundles))
	if err != nil {
		t.Fatal(err)
	}
	return pubRow(t, p, e.name), p, bundles[e.name]
}

func (e *declCIEnv) publishCI(t *testing.T, rev string) ci.Row {
	t.Helper()
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: e.opts(nil), Revision: rev})
	if err != nil {
		t.Fatal(err)
	}
	return pubRow(t, p, e.name)
}

// published is the pacto.yaml and record of the bundle r published.
func (e *declCIEnv) published(t *testing.T, r ci.Row) (string, *contract.InferredRecord) {
	t.Helper()
	_, dgst, _ := strings.Cut(r.Published, "@")
	b, err := e.inner.Pull(t.Context(), e.repo+"@"+dgst)
	if err != nil {
		t.Fatalf("%+v: %v", r, err)
	}
	raw, _ := fs.ReadFile(b.FS, "pacto.yaml")
	rec, _ := contract.ReadInferredRecord(b.FS)
	return string(raw), rec
}

// carriedField is r's Carried record of field.
func carriedField(t *testing.T, r ci.Row, field string) ci.Carried {
	t.Helper()
	for _, c := range r.Carried {
		if c.Field == field {
			return c
		}
	}
	t.Fatalf("no %s in %+v (notes %q)", field, r.Carried, r.Notes)
	return ci.Carried{}
}

const ownerBilling = "  owner:\n    team: billing\n    dri: ana@acme.com\n"

func TestCarryDeclaredOwner(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	_, p, _ := e.plan(t)
	out, _ := json.Marshal(p)
	if !strings.Contains(string(out), `"carried":[{"field":"service.owner","value":"billing","from":"registry","kind":"declared"`) {
		t.Fatalf("%s", out)
	}
	r := e.publishCI(t, "r1")
	raw, rec := e.published(t, r)
	c := carriedField(t, r, "service.owner")
	cited := rec.Inputs.Declaration
	if r.State != ci.StatePublished || !strings.Contains(raw, "  owner:\n    team: billing\n    dri: ana@acme.com\n") || c.State != ci.CarriedCarried ||
		c.Record != e.repo+"@"+d || cited == nil || cited.Fields != "service.owner" || cited.Record != e.repo+"@"+d || cited.Signer != "ana@acme.com ("+personIssuer+")" {
		t.Fatalf("%+v\n%s\n%+v", r, raw, cited)
	}
}

func TestCarryRepositoryOverridesDeclaredOwner(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "  owner:\n    team: payments\n", nil)
	r := e.publishCI(t, "r1")
	raw, rec := e.published(t, r)
	c := carriedField(t, r, "service.owner")
	if c.State != ci.CarriedOverridden || c.Value != "payments" || c.From != "pacto.yaml" || !strings.Contains(c.Reason, "billing") || c.Record != e.repo+"@"+d ||
		strings.Contains(raw, "billing") || rec.Inputs.Declaration != nil {
		t.Fatalf("%+v\n%s", c, raw)
	}
}

func TestCarryAcceptedRange(t *testing.T) {
	t.Run("declared by name", func(t *testing.T) {
		e := newDeclCIEnv(t)
		e.declaration(t, "ana@acme.com", ownerBilling+ledgerDeclared, ledgerAccepted)
		e.folder(t, "dependencies:\n  - name: ledger\n    required: true\n", nil)
		r := e.publishCI(t, "r1")
		raw, rec := e.published(t, r)
		if c := carriedField(t, r, "dependencies[ledger].compatibility"); c.State != ci.CarriedCarried || c.Kind != "declared" ||
			!strings.Contains(raw, "  - name: ledger\n    compatibility: \"^2.3.0\"\n") || rec.Inputs.Declaration.Fields != "dependencies[ledger].compatibility,service.owner" {
			t.Fatalf("%+v\n%s\n%+v", r, raw, rec.Inputs.Declaration)
		}
	})
	t.Run("whole entry", func(t *testing.T) {
		e := newDeclCIEnv(t)
		e.declaration(t, "ana@acme.com", ledgerDeclared, ledgerAccepted)
		e.folder(t, "", ledgerManifest)
		r := e.publishCI(t, "r1")
		raw, rec := e.published(t, r)
		c := carriedField(t, r, "dependencies[ledger]")
		if c.State != ci.CarriedCarried || c.Value != "^2.3.0 (oci://r.io/acme/ledger)" || !strings.Contains(raw, "compatibility: \"^2.3.0\"") ||
			rec.Inputs.Declaration.Fields != "dependencies[ledger]" || slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return strings.HasPrefix(f.Field, "dependencies[ledger]") }) {
			t.Fatalf("%+v\n%s\n%s", r, raw, rec.Marshal())
		}
	})
}

func TestCarryMissingBaselineCarriesNothing(t *testing.T) {
	e := newDeclCIEnv(t)
	e.folder(t, "", nil)
	r := e.publishCI(t, "r1")
	_, rec := e.published(t, r)
	if r.State != ci.StatePublished || len(r.Carried) != 0 || rec != nil && rec.Inputs.Declaration != nil {
		t.Fatalf("%+v", r)
	}
}

func TestCarryExplicitUnknownOverrides(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ledgerDeclared, ledgerAccepted)
	e.folder(t, "dependencies:\n  - name: ledger\n    compatibility: unknown\n", nil)
	r, _, b := e.plan(t)
	if c := carriedField(t, r, "dependencies[ledger].compatibility"); c.State != ci.CarriedOverridden || c.Value != "unknown" || strings.Contains(string(b.RawYAML), "^2.3.0") {
		t.Fatalf("%+v\n%s", c, b.RawYAML)
	}
}

func TestCarryNeverCarriesProposal(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", "dependencies:\n  - name: ledger\n    compatibility: unknown\n",
		[]contract.InferredAcceptance{{Field: "dependencies[ledger].compatibility", Range: "unknown"}})
	e.folder(t, "dependencies:\n  - name: ledger\n", nil)
	r, _, b := e.plan(t)
	if len(r.Carried) != 0 || contract.Declares(b.RawYAML, "dependencies[ledger].compatibility") {
		t.Fatalf("%+v\n%s", r.Carried, b.RawYAML)
	}
}

func TestCarryOwnerRefusedByOwnerRuleDropped(t *testing.T) {
	e := newDeclCIEnv(t)
	e.ownerPolicy(t, `{"defaultApprovers":[{"name":"admins","issuer":"`+personIssuer+`","subject":"^admin@acme\\.com$"}]}`)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	r, _, b := e.plan(t)
	c := carriedField(t, r, "service.owner")
	if c.State != ci.CarriedDropped || c.Reason == "" || contract.Declares(b.RawYAML, "service.owner") ||
		!slices.ContainsFunc(r.Notes, func(n string) bool { return strings.HasPrefix(n, "service.owner from the registry declaration ") }) {
		t.Fatalf("%+v\n%q", c, r.Notes)
	}
}

func TestCarryOwnerUnverifiedDropped(t *testing.T) {
	e := newDeclCIEnv(t)
	e.ownerPolicy(t, "")
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	r, _, _ := e.plan(t)
	// The reason keeps why no policy was read.
	if c := carriedField(t, r, "service.owner"); c.State != ci.CarriedDropped || !strings.HasPrefix(c.Reason, "no owner policy readable from CI: trust missing: ") {
		t.Fatalf("%+v", c)
	}
}

// pullCounter counts the bundle pulls reaching a registry.
type pullCounter struct {
	publish.Registry
	n int
}

func (p *pullCounter) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	p.n++
	return p.Registry.Pull(ctx, ref)
}

func TestCarryOwnerRightsBoundedPulls(t *testing.T) {
	e := newDeclCIEnv(t)
	for i := 1; i <= 40; i++ {
		e.unsignedRelease(t, "0.0."+strconv.Itoa(i), "")
	}
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	pulls := &pullCounter{Registry: e.reg}
	e.svc.sys.publishRegistry = func() publish.Registry { return pulls }
	r, _, _ := e.plan(t)
	if c := carriedField(t, r, "service.owner"); c.State != ci.CarriedDropped || c.Reason != "owner history longer than 32 releases" || pulls.n > carryOwnerRevisionCap+4 {
		t.Fatalf("%+v %d", c, pulls.n)
	}
}

func TestCarryRangeWhenOwnerRefusedDropped(t *testing.T) {
	e := newDeclCIEnv(t)
	e.ownerPolicy(t, `{"defaultApprovers":[{"name":"admins","issuer":"`+personIssuer+`","subject":"^admin@acme\\.com$"}]}`)
	e.declaration(t, "ana@acme.com", ownerBilling+ledgerDeclared, ledgerAccepted)
	e.folder(t, "dependencies:\n  - name: ledger\n", nil)
	r, _, _ := e.plan(t)
	if c := carriedField(t, r, "dependencies[ledger].compatibility"); c.State != ci.CarriedDropped || c.Reason != "routed: ana@acme.com is not an owner of invoices" {
		t.Fatalf("%+v", c)
	}
}

// TestCarryRangeFromNonOwnerDropped: an unsigned release, pushed with
// registry write alone, never sets the owner CI carries. A declaration
// repeating its owner is judged as the first owner, and a range its signer
// accepted is matched against no owner.
func TestCarryRangeFromNonOwnerDropped(t *testing.T) {
	e := newDeclCIEnv(t)
	e.ownerPolicy(t, `{"defaultApprovers":[{"name":"admins","issuer":"`+personIssuer+`","subject":"^admin@acme\\.com$"}]}`)
	e.unsignedRelease(t, "1.0.0", "  owner:\n    team: payments\n    dri: ana@acme.com\n")
	e.declaration(t, "ana@acme.com", "  owner:\n    team: payments\n    dri: ana@acme.com\n"+ledgerDeclared, ledgerAccepted)
	e.folder(t, "dependencies:\n  - name: ledger\n", nil)
	r, _, b := e.plan(t)
	if c := carriedField(t, r, "dependencies[ledger].compatibility"); c.State != ci.CarriedDropped || c.Reason != "routed: ana@acme.com is not an owner of invoices" {
		t.Fatalf("%+v", c)
	}
	if c := carriedField(t, r, "service.owner"); c.State != ci.CarriedDropped || contract.Declares(b.RawYAML, "service.owner") {
		t.Fatalf("an unsigned release's owner set what CI signs: %+v", c)
	}
}

func TestCarryWholeEntryDroppedWhenNoLongerDiscovered(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ledgerDeclared+"  - name: tax\n    compatibility: \"^1.0.0\"\n",
		append(slices.Clone(ledgerAccepted), contract.InferredAcceptance{Field: "dependencies[tax].compatibility", Range: "^1.0.0"}))
	e.folder(t, "dependencies:\n  - name: tax\n", nil)
	r, _, b := e.plan(t)
	c := carriedField(t, r, "dependencies[ledger].compatibility")
	if c.State != ci.CarriedDropped || c.Reason != "ledger is no longer discovered; write it in pacto.yaml to keep it" || contract.Declares(b.RawYAML, "dependencies[ledger]") ||
		carriedField(t, r, "dependencies[tax].compatibility").State != ci.CarriedCarried {
		t.Fatalf("%+v\n%s", r.Carried, b.RawYAML)
	}
}

func TestCICarriedOwnerIsUnchanged(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	if r := e.publishCI(t, "r1"); r.State != ci.StatePublished {
		t.Fatalf("%+v", r)
	}
	r, _, _ := e.plan(t)
	if r.Changed != "no" || carriedField(t, r, "service.owner").State != ci.CarriedCarried {
		t.Fatalf("%+v", r)
	}
}

func TestCIPublishRecordsDeclarationInput(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	r := e.publishCI(t, "r1")
	_, rec := e.published(t, r)
	version := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "service.version" })
	if rec.Inputs.Declaration.Record != e.repo+"@"+d || version < 0 || rec.Fields[version].Source != "registry:"+e.repo+"@"+d {
		t.Fatalf("%s", rec.Marshal())
	}
}

// TestCIPublishCarriesOnceFromAsWritten: publish pushes the bytes plan mode
// carried over the bundle as written, never a second carry over them.
func TestCIPublishCarriesOnceFromAsWritten(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling+ledgerDeclared, ledgerAccepted)
	e.folder(t, "dependencies:\n  - name: ledger\n", nil)
	_, _, planned := e.plan(t)
	r := e.publishCI(t, "r1")
	raw, rec := e.published(t, r)
	want, err := contract.PinServiceVersion(planned.RawYAML, r.To)
	if err != nil || raw != string(want) || carriedField(t, r, "service.owner").State != ci.CarriedCarried || *rec.Inputs.Declaration != *carriedRecord(t, planned).Inputs.Declaration {
		t.Fatalf("%v\n%+v\nplanned %s\npublished %s", err, r.Carried, want, raw)
	}
}

func TestDeclarationSourceFollowsCitation(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	first := e.publishCI(t, "r1")
	e.folder(t, "", map[string]string{"notes.md": "# invoices\n"})
	r, _, b := e.plan(t)
	if r.Baseline != first.To || carriedField(t, r, "service.owner").Record != e.repo+"@"+d || carriedRecord(t, b).Inputs.Declaration.Record != e.repo+"@"+d {
		t.Fatalf("%+v", r)
	}
}

// TestCarryReCitesOriginalDeclaration: the second CI publish after one
// declaration cites the declaration, never the first CI version.
func TestCarryReCitesOriginalDeclaration(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	e.publishCI(t, "r1")
	e.folder(t, "", map[string]string{"notes.md": "# invoices\n"})
	r := e.publishCI(t, "r2")
	raw, rec := e.published(t, r)
	if r.State != ci.StatePublished || rec.Inputs.Declaration.Record != e.repo+"@"+d || !strings.Contains(raw, "team: billing") {
		t.Fatalf("%+v\n%s", r, raw)
	}
}

func TestCarryUncheckableDeclarationStopsPublish(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	e.reg.Registry = referrersFail{e.inner}
	r, _, _ := e.plan(t)
	if r.Impact.Gate != "unverified" || !strings.Contains(r.Impact.Text, "registry declaration "+e.repo+"@") || !strings.Contains(r.Impact.Text, "could not be checked") {
		t.Fatalf("%+v", r)
	}
	before := e.reg.writes.Load()
	if r := e.publishCI(t, "r1"); r.State != ci.StateFailed || !strings.Contains(r.Reason, "could not be checked") || e.reg.writes.Load() != before {
		t.Fatalf("%+v", r)
	}
}

// citing is a CI version of invoices whose record cites the declaration
// at ref.
func citing(t *testing.T, ref string) *contract.Bundle {
	t.Helper()
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Declaration: &contract.CitedDeclaration{Record: ref, Signer: "ana@acme.com (" + personIssuer + ")", Fields: "service.owner"}}}
	return bundleOf(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: 0.1.1\n  owner: {team: billing}\n",
		contract.InferredRecordFile: string(rec.Marshal())})
}

// noRequests fails the test on any registry request.
type noRequests struct {
	publish.Registry
	t *testing.T
}

func (n noRequests) Head(context.Context, string) (*publish.Remote, error) {
	n.t.Fatal("head")
	return nil, nil
}

func (n noRequests) Pull(context.Context, string) (*contract.Bundle, error) {
	n.t.Fatal("pull")
	return nil, nil
}

// anyHeadFails answers 503 to every manifest read; pullFails to every pull.
type anyHeadFails struct{ publish.Registry }

func (anyHeadFails) Head(context.Context, string) (*publish.Remote, error) {
	return nil, errors.New("503")
}

type pullFails struct{ publish.Registry }

func (pullFails) Pull(context.Context, string) (*contract.Bundle, error) {
	return nil, errors.New("503")
}

func TestDeclarationSourceForeignCitationIgnored(t *testing.T) {
	e := newDeclCIEnv(t)
	base := carryBase{repo: e.repo, version: "0.1.1", digest: "sha256:" + strings.Repeat("1", 64), b: citing(t, "other.io/acme/invoices@sha256:"+strings.Repeat("2", 64))}
	d, dropped, notes, err := declarationTrustFrom(t.Context(), nil).declarationSource(t.Context(), noRequests{e.inner, t}, wantService{Name: "invoices"}, nil, base)
	if d != nil || dropped != nil || err != nil || len(notes) != 1 || !strings.Contains(notes[0], "it is outside "+e.repo) {
		t.Fatalf("%v %q %+v", err, notes, d)
	}
}

func TestDeclarationSourceCitationGoneDropped(t *testing.T) {
	e := newDeclCIEnv(t)
	gone := e.repo + "@sha256:" + strings.Repeat("2", 64)
	base := carryBase{repo: e.repo, version: "0.1.1", digest: "sha256:" + strings.Repeat("1", 64), b: citing(t, gone)}
	tr := e.trust(t)
	d, dropped, notes, err := tr.declarationSource(t.Context(), e.inner, wantService{Name: "invoices"}, nil, base)
	want := carryCandidate{Field: "service.owner", Value: "billing", Kind: carriedKindDeclared, Record: gone, Signer: "ana@acme.com (" + personIssuer + ")", dropped: "it is gone"}
	if d != nil || err != nil || notes != nil || len(dropped) != 1 || dropped[0] != want {
		t.Fatalf("%v %q %+v %+v", err, notes, d, dropped)
	}
}

// trust is a run trust over the env's Sigstore.
func (e *declCIEnv) trust(t *testing.T) *declarationTrust {
	t.Helper()
	return declarationTrustFrom(t.Context(), impactTrust(ImpactOptions{Signers: e.opts(nil).Signers}))
}

func TestDeclarationSourceCitationFailures(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	ref := e.repo + "@" + d
	base := carryBase{repo: e.repo, version: "0.1.1", digest: "sha256:" + strings.Repeat("1", 64), b: citing(t, ref)}
	tags, _ := e.inner.Tags(t.Context(), e.repo)
	for name, reg := range map[string]publish.Registry{"head": anyHeadFails{e.inner}, "pull": pullFails{e.inner}, "signatures": referrersFail{e.inner}} {
		if got, _, _, err := e.trust(t).declarationSource(t.Context(), reg, wantService{Name: "invoices"}, tags, base); got != nil || err == nil {
			t.Errorf("%s: %v %+v", name, err, got)
		}
	}
	got, dropped, notes, err := e.trust(t).declarationSource(t.Context(), e.inner, wantService{Name: "payments"}, tags, base)
	if got != nil || err != nil || notes != nil || len(dropped) != 1 || !strings.HasPrefix(dropped[0].dropped, "declares invoices, not payments") {
		t.Fatalf("%v %q %+v", err, notes, dropped)
	}
	if got, _, _, err := e.trust(t).declarationSource(t.Context(), e.inner, wantService{Name: "invoices"}, tags, base); got == nil || err != nil || got.Ref != ref {
		t.Fatalf("%v %+v", err, got)
	}
}

// TestDeclarationSourceBaselineNotCountedNoted: a baseline that is itself a
// declaration carried nothing yet, so one that does not count is a note.
func TestDeclarationSourceBaselineNotCountedNoted(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	tags, _ := e.inner.Tags(t.Context(), e.repo)
	got, dropped, notes, err := e.trust(t).declarationSource(t.Context(), e.inner, wantService{Name: "payments"}, tags, carryBase{repo: e.repo, version: firstDeclaration, digest: d,
		b: bundleOf(t, e.declFiles(firstDeclaration, "ana@acme.com", ownerBilling, nil, nil))})
	if got != nil || dropped != nil || err != nil || len(notes) != 1 || !strings.HasPrefix(notes[0], "registry declaration "+e.repo+"@"+d+" not carried: declares invoices, not payments") {
		t.Fatalf("%v %q", err, notes)
	}
}

func TestCarryOwnerHistoryUnread(t *testing.T) {
	e := newDeclCIEnv(t)
	digest := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	tags, _ := e.inner.Tags(t.Context(), e.repo)
	tr := e.trust(t)
	d, _, _, err := tr.declarationSource(t.Context(), e.inner, wantService{Name: "invoices"}, tags, carryBase{repo: e.repo, version: firstDeclaration, digest: digest, b: bundleOf(t, e.declFiles(firstDeclaration, "ana@acme.com", ownerBilling, nil, nil))})
	if err != nil || d == nil {
		t.Fatal(err)
	}
	// An unread history is unknown: an error, never a dropped owner.
	if v, err := tr.ownerHistory(t.Context(), pullFails{e.inner}, e.repo, tags, d, nil, "", acceptNow); err == nil || v.Allowed || !strings.HasPrefix(err.Error(), "the owner history could not be read: ") {
		t.Fatalf("%+v %v", v, err)
	}
	noRoot := &declarationTrust{roots: func() rootsRead { return rootsRead{noRoot: true} }}
	if v, err := noRoot.ownerHistory(t.Context(), e.inner, e.repo, tags, d, nil, "", acceptNow); err != nil || v.Allowed || v.Reason != "no trusted root reads the owner history" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestCarryNotesPublicGoodDeclarations(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	publicGoodIs(t, e.ss.Root, nil)
	o := e.opts(nil)
	o.Signers = nil
	p, _, err := e.svc.CIPlan(t.Context(), o)
	if r := pubRow(t, p, "invoices"); err != nil || !slices.Contains(r.Notes, declarationsPublicGoodNote) || carriedField(t, r, "service.owner").State != ci.CarriedCarried {
		t.Fatalf("%v %+v", err, r)
	}
}

// policyGone removes the owner policy once a bundle of then's repository
// is pulled.
type policyGone struct {
	publish.Registry
	path, then string
}

func (p policyGone) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if strings.HasPrefix(ref, p.then) {
		_ = os.Remove(p.path)
	}
	return p.Registry.Pull(ctx, ref)
}

// TestCarryOwnerPolicyReadOncePerRun: the policy goes away after the first
// row read it; the later rows still judge with that one read.
func TestCarryOwnerPolicyReadOncePerRun(t *testing.T) {
	e := newDeclCIEnv(t)
	for _, name := range []string{"svc-a", "svc-b", "svc-c"} {
		e.service(name)
		e.declaration(t, "ana@acme.com", ownerBilling, nil)
		e.folder(t, "", nil)
	}
	e.svc.sys.publishRegistry = func() publish.Registry {
		return policyGone{Registry: e.reg, path: e.svc.OwnerPolicyPath, then: e.host + "/acme/svc-b"}
	}
	p, _, err := e.svc.CIPlan(t.Context(), e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.svc.OwnerPolicyPath); err == nil {
		t.Fatal("the policy was never removed: the test proves nothing")
	}
	for _, name := range []string{"svc-a", "svc-b", "svc-c"} {
		if c := carriedField(t, pubRow(t, p, name), "service.owner"); c.State != ci.CarriedCarried {
			t.Errorf("%s: %+v", name, c)
		}
	}
}

// accepting is ana's counted acceptance of ledger's range at version.
func accepting(version, value string) delivery.JudgedAcceptance {
	return delivery.JudgedAcceptance{Ref: "r.io/acme/invoices@sha256:" + strings.Repeat("a", 64), Signer: "ana@acme.com",
		Record: delivery.AcceptanceRecord{Version: version, Ranges: []delivery.AcceptedRange{{Field: "dependencies[ledger].compatibility", Value: value}}}}
}

// declaringLedger is a declaration at version that accepted ledger's range.
func declaringLedger(version string) *declarationCarry {
	return &declarationCarry{d: &registryDeclaration{Ref: "r.io/acme/invoices@sha256:" + strings.Repeat("d", 64), Version: version},
		cands: []carryCandidate{{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", Kind: carriedKindDeclared, AtVersion: version, entry: &contract.Dependency{Name: "ledger", Compatibility: "^2.3.0"}}}}
}

// TestCarryAcceptanceAtOrAboveDeclarationWins: an acceptance on the
// revision a CI version citing the declaration published, or on the
// declaration itself, outranks it.
func TestCarryAcceptanceAtOrAboveDeclarationWins(t *testing.T) {
	for _, at := range []string{"0.2.0", "0.1.0"} {
		dc := declaringLedger("0.1.0")
		js := []delivery.JudgedAcceptance{accepting(at, "^2.5.0")}
		cands := dc.outranking(js)
		var res carryResult
		raw := []byte("dependencies:\n  - name: ledger\n")
		deps := []contract.Dependency{{Name: "ledger"}}
		pinAccepted(js, &raw, deps, &res, declaredFields(cands))
		if len(cands) != 0 || !strings.Contains(string(raw), `"^2.5.0"`) || res.Carried[0].Kind != carriedKindAccepted {
			t.Fatalf("%s: %+v %s", at, cands, raw)
		}
	}
}

// TestCarryDeclarationNewerThanAcceptanceWins: a declaration on a newer
// revision than the acceptance outranks it, whatever was signed last.
func TestCarryDeclarationNewerThanAcceptanceWins(t *testing.T) {
	dc := declaringLedger("0.2.0")
	js := []delivery.JudgedAcceptance{accepting("0.1.0", "^2.5.0")}
	cands := dc.outranking(js)
	var res carryResult
	raw := []byte("dependencies:\n  - name: ledger\n")
	c := contract.Contract{Dependencies: []contract.Dependency{{Name: "ledger"}}}
	pinAccepted(js, &raw, c.Dependencies, &res, declaredFields(cands))
	fields := mergeCarry(&raw, &c, nil, cands, &res)
	if len(cands) != 1 || !slices.Equal(fields, []string{"dependencies[ledger].compatibility"}) || !strings.Contains(string(raw), `"^2.3.0"`) ||
		c.Dependencies[0].Compatibility != "^2.3.0" || len(res.Cited) != 0 {
		t.Fatalf("%+v %s %+v", cands, raw, res)
	}
	if (*declarationCarry)(nil).outranking(js) != nil {
		t.Fatal("no declaration names nothing")
	}
}

func TestCarryInferredOwnerDiffersNote(t *testing.T) {
	o := contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "ana@acme.com"}}}
	cands := []carryCandidate{{Field: "service.owner", Value: carriedOwner(o), Kind: carriedKindDeclared, owner: &o}}
	rec := &contract.InferredRecord{Fields: []contract.Inferred{{Field: "service.owner", Value: "payments", Source: "repository:CODEOWNERS:7"}}}
	var res carryResult
	raw, c := []byte("service:\n  name: invoices\n"), contract.Contract{}
	if fields := mergeCarry(&raw, &c, rec, cands, &res); len(fields) != 1 || c.Service.Owner.Contacts[0].Value != "ana@acme.com" ||
		!slices.Contains(res.Notes, "service.owner inferred payments from repository:CODEOWNERS:7 differs from the registry declaration") {
		t.Fatalf("%+v %q", fields, res.Notes)
	}
}

func TestMergeCarryUnwritable(t *testing.T) {
	o := contract.Owner{Team: "billing"}
	cands := []carryCandidate{{Field: "service.owner", Value: "billing", owner: &o},
		{Field: "dependencies[ledger].compatibility", Value: "^2", entry: &contract.Dependency{Name: "ledger", Compatibility: "^2"}}}
	rec := &contract.InferredRecord{Fields: []contract.Inferred{{Field: "dependencies[ledger]"}}}
	var res carryResult
	raw := []byte("[1]\n")
	if fields := mergeCarry(&raw, &contract.Contract{}, rec, cands, &res); len(fields) != 0 || res.Carried[0].State != ci.CarriedDropped || res.Carried[1].State != ci.CarriedDropped {
		t.Fatalf("%+v", res.Carried)
	}
}

func TestDeclarationCandidatesSkipsNonRange(t *testing.T) {
	d := &registryDeclaration{Ref: "r", Version: "0.1.0", Bundle: bundleOf(t, map[string]string{"pacto.yaml": "service:\n  name: invoices\n"}),
		Record: &contract.InferredRecord{Declaration: &contract.InferredDeclaration{Accepted: []contract.InferredAcceptance{{Field: "service.owner", Range: "^1"}}}}}
	if got := declarationCandidates(d, ownerVerdict{}, acceptNow); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// notCarried reports whether notes hold the note a plain pacto publish adds
// for registry declarations it does not carry.
func notCarried(notes []string) bool {
	return slices.ContainsFunc(notes, func(n string) bool {
		return strings.HasPrefix(n, "pacto publish does not carry registry declarations: ")
	})
}

func TestPublishNotesCitedDeclaration(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	if r := e.publishCI(t, "r1"); r.State != ci.StatePublished {
		t.Fatalf("%+v", r)
	}
	// The baseline is now the CI version citing the declaration; the folder
	// changes, so a plain publish derives the next version from it.
	e.write(t, map[string]string{"invoices/notes.md": "# invoices\n"})
	res, err := e.svc.Publish(t.Context(), PublishOptions{Path: filepath.Join(e.root, "invoices"), Revision: "r2", Plan: true})
	want := "pacto publish does not carry registry declarations: service.owner declared in " + e.repo + "@" + d +
		" are not in this version; run pacto ci --publish or write them in pacto.yaml"
	if err != nil || !slices.Contains(res.Notes, want) {
		t.Fatalf("%v %q", err, res.Notes)
	}
}

func TestCIPublishDoesNotPrintPublishNote(t *testing.T) {
	e := newDeclCIEnv(t)
	e.ownerPolicy(t, "") // the owner is dropped: the carry pins nothing
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "", nil)
	path := filepath.Join(e.root, "invoices")
	res, err := e.svc.Publish(t.Context(), PublishOptions{Path: path, Revision: "r1", Plan: true, declTrust: e.trust(t)})
	if err != nil || !notCarried(res.Notes) {
		t.Fatalf("plain publish: %v %q", err, res.Notes)
	}
	r := e.publishCI(t, "r1")
	if r.State != ci.StatePublished || carriedField(t, r, "service.owner").State != ci.CarriedDropped || notCarried(r.Notes) {
		t.Fatalf("%+v", r)
	}
}

// TestCIPublishRefusesForgedPlanBaseline: a tag pushed without the
// publisher's signature that cites an old declaration the genuine history
// stopped carrying makes plan mode carry it, and the publish refuses those
// bytes instead of signing them.
func TestCIPublishRefusesForgedPlanBaseline(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.folder(t, "  owner:\n    team: payments\n", nil)
	if r := e.publishCI(t, "r1"); r.State != ci.StatePublished {
		t.Fatalf("%+v", r)
	}
	forged := citing(t, e.repo+"@"+d)
	files := map[string]string{"pacto.yaml": strings.Replace(string(forged.RawYAML), "0.1.1", "0.9.0", 1)}
	rec, _ := fs.ReadFile(forged.FS, contract.InferredRecordFile)
	files[contract.InferredRecordFile] = string(rec)
	e.de.publishBundle(t, e.repo, "0.9.0", files, nil)
	e.folder(t, "", nil)
	if r, _, _ := e.plan(t); carriedField(t, r, "service.owner").State != ci.CarriedCarried {
		t.Fatalf("%+v", r)
	}
	before := e.reg.writes.Load()
	r := e.publishCI(t, "r2")
	if r.State != ci.StateFailed || !strings.Contains(r.Reason, "the plan carried service.owner from "+e.repo+":0.9.0, which is neither signed") || e.reg.writes.Load() != before {
		t.Fatalf("%+v", r)
	}
}

// TestCarryCitedDeclarationGoneDropsItsFields: once the declaration a CI
// version cites is gone, the plan returns each field it carried as dropped,
// valued as that version published it.
func TestCarryCitedDeclarationGoneDropsItsFields(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling+ledgerDeclared, ledgerAccepted)
	e.folder(t, "dependencies:\n  - name: ledger\n", nil)
	if r := e.publishCI(t, "r1"); r.State != ci.StatePublished {
		t.Fatalf("%+v", r)
	}
	e.reg.Registry = headGone{e.inner, d}
	e.folder(t, "dependencies:\n  - name: ledger\n", map[string]string{"notes.md": "# invoices\n"})
	r, _, _ := e.plan(t)
	owner, ledger := carriedField(t, r, "service.owner"), carriedField(t, r, "dependencies[ledger].compatibility")
	if owner.State != ci.CarriedDropped || owner.Value != "billing" || owner.Reason != "it is gone" || owner.Record != e.repo+"@"+d ||
		ledger.State != ci.CarriedDropped || ledger.Value != "^2.3.0" || !slices.Contains(r.Notes, "service.owner from the registry declaration "+e.repo+"@"+d+" not carried: it is gone") {
		t.Fatalf("%+v\n%q", r.Carried, r.Notes)
	}
}

// headGone answers every manifest read of digest as not found.
type headGone struct {
	publish.Registry
	digest string
}

func (h headGone) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if strings.HasSuffix(ref, "@"+h.digest) {
		return nil, nil
	}
	return h.Registry.Head(ctx, ref)
}

// tagHeadFails answers 503 to the manifest read of one tag.
type tagHeadFails struct {
	publish.Registry
	tag string
}

func (h tagHeadFails) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if strings.HasSuffix(ref, ":"+h.tag) {
		return nil, errors.New("503")
	}
	return h.Registry.Head(ctx, ref)
}

// A release in the owner history that cannot be read leaves the owner
// history unknown: the row is unverified and nothing is carried.
func TestCarryOwnerHistoryReleaseUnreadable(t *testing.T) {
	e := newDeclCIEnv(t)
	e.declaration(t, "ana@acme.com", ownerBilling, nil)
	e.unsignedRelease(t, "0.2.0", "")
	e.write(t, map[string]string{e.name + "/pacto.yaml": e.contract(e.name, firstDeclaration, "", "")})
	e.reg.Registry = tagHeadFails{e.inner, "0.2.0"}
	r, _, b := e.plan(t)
	if r.Impact.Gate != "unverified" || !strings.Contains(r.Impact.Text, "could not be checked: the owner history could not be read: 503") ||
		len(r.Carried) != 0 || contract.Declares(b.RawYAML, "service.owner") {
		t.Fatalf("%+v", r)
	}
}

// TestHistoryRevisionEveryRoot: a declaration in the owner history keeps the
// signers its first root verified, adds those every other root verifies
// once, and a second root's signatures that cannot be read fail the read.
func TestHistoryRevisionEveryRoot(t *testing.T) {
	e := newDeclCIEnv(t)
	d := e.declaration(t, "ana@acme.com", ownerBilling, nil)
	root := delivery.PersonSigner{Issuer: personIssuer, Subjects: ".*", TrustedRoot: string(e.ss.RootJSON)}
	rr := signerRoots([]delivery.PersonSigner{root, root})
	r, err := e.trust(t).historyRevision(t.Context(), e.inner, rr.roots, e.repo, "invoices", firstDeclaration)
	if err != nil || r == nil || r.Digest != d || len(r.Provenance.Signers) != 1 || r.Provenance.Signers[0].Subject != "ana@acme.com" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := e.trust(t).historyRevision(t.Context(), referrersFail{e.inner}, rr.roots, e.repo, "invoices", firstDeclaration); err == nil ||
		!strings.HasPrefix(err.Error(), "reading the signatures of "+e.repo+"@"+d+": ") {
		t.Fatalf("unread signatures under a second root: %v", err)
	}
}
