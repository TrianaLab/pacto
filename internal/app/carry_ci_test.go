package app

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// carryCIEnv is a repository whose reports, owned by ana and depending on
// ledger by name with no range, published 1.0.0; ana then accepted ledger's
// range ^2.3.0 on it.
type carryCIEnv struct {
	*ciPubEnv
	ss     *testutil.Sigstore
	repo   string
	record string // <repo>@<digest> of ana's acceptance
}

func newCarryCIEnv(t *testing.T) *carryCIEnv {
	t.Helper()
	e := &carryCIEnv{ciPubEnv: newCIPub(t), ss: testutil.New(t)}
	e.svc.sys.refusalRepo = func(path string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Keychain: anonymous})
	}
	e.svc.sys.now = func() time.Time { return acceptNow }
	e.repo = e.host + "/acme/reports"
	e.write(t, map[string]string{"reports/pacto.yaml": e.contract("reports", "", "  owner: {team: reporting, dri: ana@acme.com}\ndependencies:\n  - name: ledger\n    required: true\n", "")})
	e.publishAs(t, "reports", "r1")
	head, err := e.inner.Head(t.Context(), e.repo+":1.0.0")
	if err != nil || head == nil {
		t.Fatal(head, err)
	}
	r := delivery.AcceptanceRecord{Service: string(fleet.NewServiceKeyDomain(e.host+"/acme", "reports")), Version: "1.0.0", Kind: delivery.AcceptKindRanges,
		Ranges: []delivery.AcceptedRange{{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", Proposed: "^2.3.0", Source: snapSrc}}}
	e.record = e.attachTo(t, head.Digest, r, personTok("ana@acme.com"))
	return e
}

// attachTo signs r as tok's person and attaches it to reports@dgst as a
// write-once tag; it returns where the record is.
func (e *carryCIEnv) attachTo(t *testing.T, dgst string, r delivery.AcceptanceRecord, tok string) string {
	t.Helper()
	ctx := t.Context()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := delivery.SignAcceptance(ctx, s, delivery.NewAcceptanceStatement(e.repo+"@"+dgst, r))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := ocireferrer.Open(e.repo, ocireferrer.Options{Keychain: anonymous})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := repo.Resolve(ctx, dgst)
	if err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build(delivery.AcceptanceArtifactType, delivery.AcceptanceArtifactType, bundle, &subject, nil)
	if err := ocireferrer.PushWriteOnce(ctx, repo, art, delivery.AcceptanceTag(subject.Digest, art.ManifestDesc.Digest)); err != nil {
		t.Fatal(err)
	}
	return e.repo + "@" + art.ManifestDesc.Digest.String()
}

// opts plans the repository verifying acceptances under the test Sigstore.
func (e *carryCIEnv) opts(bundles map[string]*contract.Bundle) CIOptions {
	return CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles,
		Signers: &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: ".*", TrustedRoot: string(e.ss.RootJSON)}}}}
}

// publishCI runs pacto ci --publish and returns reports' row.
func (e *carryCIEnv) publishCI(t *testing.T, rev string) ci.Row {
	t.Helper()
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: e.opts(nil), Revision: rev})
	if err != nil {
		t.Fatal(err)
	}
	return pubRow(t, p, "reports")
}

// published is the pacto.yaml and record of the bundle row published.
func (e *carryCIEnv) published(t *testing.T, r ci.Row) (string, *contract.InferredRecord) {
	t.Helper()
	_, dgst, _ := strings.Cut(r.Published, "@")
	b, err := e.inner.Pull(t.Context(), e.repo+"@"+dgst)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := fs.ReadFile(b.FS, "pacto.yaml")
	return string(raw), carriedRecord(t, b)
}

func TestCIPlanShowsCarriedAcceptance(t *testing.T) {
	t.Parallel()
	e := newCarryCIEnv(t)
	p, _, err := e.svc.CIPlan(t.Context(), e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	r := pubRow(t, p, "reports")
	if r.Changed != "yes" || r.Accepted != 1 || !strings.Contains(r.Reason, "contract differs") {
		t.Fatalf("%+v", r)
	}
}

func TestCIPublishAcceptedRangeBecomesDeclared(t *testing.T) {
	t.Parallel()
	e := newCarryCIEnv(t)
	r := e.publishCI(t, "r2")
	raw, rec := e.published(t, r)
	if r.State != ci.StatePublished || !strings.Contains(raw, "  - name: ledger\n    compatibility: \"^2.3.0\"\n") ||
		len(rec.Inputs.Acceptances) != 1 || rec.Inputs.Acceptances[0].Record != e.record || rec.Inputs.Acceptances[0].Signer != "ana@acme.com" {
		t.Fatalf("%+v\n%s\n%+v", r, raw, rec.Inputs)
	}
	for _, f := range rec.Fields {
		if f.Field == "dependencies[ledger].compatibility" {
			t.Fatalf("the declared range is still listed: %+v", f)
		}
	}
}

// TestCarryKeepsCitedRecordsAcrossPublishes: the next run reads the
// acceptance through the citation of the version it published, so the
// service stays unchanged with the range declared.
func TestCarryKeepsCitedRecordsAcrossPublishes(t *testing.T) {
	t.Parallel()
	e := newCarryCIEnv(t)
	first := e.publishCI(t, "r2")
	before := e.reg.writes.Load()
	again := e.publishCI(t, "r3")
	if first.State != ci.StatePublished || again.State != ci.StateUnchanged || again.Accepted != 1 || again.Baseline != first.To || e.reg.writes.Load() != before {
		t.Fatalf("first %+v\nagain %+v", first, again)
	}
}

func TestCIPlanAndPublishCarrySameBytes(t *testing.T) {
	t.Parallel()
	e := newCarryCIEnv(t)
	bundles := map[string]*contract.Bundle{}
	if _, _, err := e.svc.CIPlan(t.Context(), e.opts(bundles)); err != nil {
		t.Fatal(err)
	}
	planned := bundles["reports"]
	plannedRec := carriedRecord(t, planned)
	r := e.publishCI(t, "r2")
	raw, rec := e.published(t, r)
	want, err := contract.PinServiceVersion(planned.RawYAML, r.To)
	if err != nil || raw != string(want) || len(plannedRec.Inputs.Acceptances) != 1 || plannedRec.Inputs.Acceptances[0] != rec.Inputs.Acceptances[0] {
		t.Fatalf("%v\nplanned %s\npublished %s", err, want, raw)
	}
}

func TestCIPlanUnreadableCitationIsUnverified(t *testing.T) {
	t.Parallel()
	e := newCarryCIEnv(t)
	e.publishCI(t, "r2")
	// The next run cannot read the record its baseline cites: dropping it
	// would publish the range away, so the row reads unverified.
	repo, err := ocireferrer.Open(e.repo, ocireferrer.Options{Keychain: anonymous})
	if err != nil {
		t.Fatal(err)
	}
	_, dgst, _ := strings.Cut(e.record, "@")
	e.svc.sys.refusalRepo = func(path string) (*remote.Repository, error) {
		if _, err := repo.Resolve(t.Context(), dgst); err != nil {
			t.Fatal(err)
		}
		return nil, errors.New("registry down")
	}
	p, _, err := e.svc.CIPlan(t.Context(), e.opts(nil))
	if r := pubRow(t, p, "reports"); err != nil || r.Impact.Gate != "unverified" || !strings.Contains(r.Impact.Text, "carried acceptances unreadable: ") {
		t.Fatalf("%v %+v", err, r)
	}
}

// TestCIPlanDeclaredPublishedVersionCarriesNothing: a declared version that
// is already published cannot change content, so an acceptance never turns
// its unchanged row blocked; it carries with the next version.
func TestCIPlanDeclaredPublishedVersionCarriesNothing(t *testing.T) {
	t.Parallel()
	e := newCarryCIEnv(t)
	e.write(t, map[string]string{"reports/pacto.yaml": e.contract("reports", "1.0.0", "  owner: {team: reporting, dri: ana@acme.com}\ndependencies:\n  - name: ledger\n    required: true\n", "")})
	p, _, err := e.svc.CIPlan(t.Context(), e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	r := pubRow(t, p, "reports")
	if r.Impact.Gate == impact.GateBlocked || r.Changed != "no" || r.Accepted != 0 ||
		!slices.Contains(r.Notes, "registry decisions carry with the next version: bump service.version") ||
		len(r.Carried) != 1 || r.Carried[0].State != ci.CarriedDropped || r.Carried[0].Reason != "carries with the next version: bump service.version" {
		t.Fatalf("%+v", r)
	}
}

// TestCIPlanBehindCarriedBaselineUnchanged: an earlier CLI published the
// behind contract as written with the acceptance carried in. The CLI that
// raises it in memory carries the same decision into the contract as
// written, so the row reads unchanged and keeps the behind pactoVersion,
// whether the version is derived or declared as the one published.
func TestCIPlanBehindCarriedBaselineUnchanged(t *testing.T) {
	t.Parallel()
	for _, declared := range []bool{false, true} {
		t.Run(strconv.FormatBool(declared), func(t *testing.T) { behindCarriedBaselineUnchanged(t, declared) })
	}
}

func behindCarriedBaselineUnchanged(t *testing.T, declared bool) {
	e := newCarryCIEnv(t)
	first := e.publishCI(t, "r2")
	raw, _ := e.published(t, first)
	_, dgst, _ := strings.Cut(first.Published, "@")
	b, err := e.inner.Pull(t.Context(), e.repo+"@"+dgst)
	if err != nil {
		t.Fatal(err)
	}
	behind := []byte(asBehind(raw))
	c, _, err := contract.ParseDocument(behind)
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := copyFSWithReplace(b.FS, DefaultContractPath, behind)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host)).Push(t.Context(), e.repo+":"+first.To,
		&contract.Bundle{Contract: c, RawYAML: behind, FS: fsys}); err != nil {
		t.Fatal(err)
	}
	version := ""
	if declared {
		version = first.To
	}
	e.write(t, map[string]string{"reports/pacto.yaml": asBehind(e.contract("reports", version, "  owner: {team: reporting, dri: ana@acme.com}\ndependencies:\n  - name: ledger\n    required: true\n", ""))})
	p, _, err := e.svc.CIPlan(t.Context(), e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	r := pubRow(t, p, "reports")
	note := "pacto.yaml is " + prevVersion + ", published as " + prevVersion + " (raises to " + contract.Current() + " when it next changes)"
	if r.Changed != "no" || r.Baseline != first.To || r.Impact.Gate != impact.GateClear || !slices.Contains(r.Notes, note) {
		t.Fatalf("%+v", r)
	}
}
