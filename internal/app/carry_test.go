package app

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// bundleOf is files as a local bundle.
func bundleOf(t *testing.T, files map[string]string) *contract.Bundle {
	t.Helper()
	fsys := fstest.MapFS{}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c), Mode: 0o644}
	}
	c, err := contract.Parse(strings.NewReader(files["pacto.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	return &contract.Bundle{Contract: c, RawYAML: []byte(files["pacto.yaml"]), FS: fsys}
}

// base is reports 1.6.0 as published, the baseline a carry reads.
func (e *acceptEnv) base(t *testing.T) carryBase {
	t.Helper()
	b, err := e.reg.Pull(t.Context(), e.subject)
	if err != nil {
		t.Fatal(err)
	}
	return carryBase{repo: e.repo, version: "1.6.0", digest: e.digest, b: b}
}

// carry carries reports 1.6.0's acceptances into its next checkout.
func (e *acceptEnv) carry(t *testing.T, st *acceptanceStore) carryResult {
	t.Helper()
	res, err := carryForward(t.Context(), st, e.base(t), bundleOf(t, e.reportsFiles("1.6.1", false)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// carriedRecord is b's inferred record.
func carriedRecord(t *testing.T, b *contract.Bundle) *contract.InferredRecord {
	t.Helper()
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil {
		t.Fatalf("%v %v", rec, err)
	}
	return rec
}

func TestCarryPinsAcceptedRange(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	r := e.rangesRecord()
	r.Ranges = append(r.Ranges, delivery.AcceptedRange{Field: "dependencies[ledger].ref", Value: "oci://" + e.host + "/acme/ledger", Source: indexSrc})
	ref := e.repo + "@" + e.attach(t, attachment{record: r, token: personTok("ana@acme.com"), tag: true})
	local := bundleOf(t, e.reportsFiles("1.6.1", true))
	res, err := carryForward(t.Context(), e.store(t), e.base(t), local, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := fs.ReadFile(res.Bundle.FS, "pacto.yaml")
	rec := carriedRecord(t, res.Bundle)
	dep := res.Bundle.Contract.Dependencies[0]
	if res.Pinned != 2 || !strings.Contains(string(raw), "  - name: ledger\n    ref: \"oci://"+e.host+"/acme/ledger\"\n    compatibility: \"^2.3.0\"\n") ||
		string(res.Bundle.RawYAML) != string(raw) || dep.Compatibility != "^2.3.0" || dep.Ref != "oci://"+e.host+"/acme/ledger" || len(rec.Inputs.Acceptances) != 1 ||
		rec.Inputs.Acceptances[0] != (contract.CitedAcceptance{Record: ref, Signer: "ana@acme.com", Fields: "dependencies[ledger].compatibility, dependencies[ledger].ref"}) {
		t.Fatalf("%+v\n%s\n%+v", res, raw, rec.Inputs)
	}
	// The pinned fields leave the record; the other proposal stays.
	if len(rec.Fields) != 1 || rec.Fields[0].Field != "dependencies[billing].compatibility" || local.Contract.Dependencies[0].Compatibility != contract.CompatibilityUnknown {
		t.Fatalf("%+v", rec.Fields)
	}
	if res.Bundle.Contract == e.base(t).b.Contract || len(res.Notes) != 0 {
		t.Fatalf("%+v", res.Notes)
	}
}

func TestCarryRepositoryWins(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	r := e.rangesRecord()
	r.Ranges = []delivery.AcceptedRange{{Field: "dependencies[billing].compatibility", Value: "^1.4.0"}, {Field: "dependencies[ghost].ref", Value: "oci://x/y"},
		{Field: "service.owner", Value: "x"}}
	ref := e.repo + "@" + e.attach(t, attachment{record: r, token: personTok("ana@acme.com"), tag: true})
	files := e.reportsFiles("1.6.1", false)
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "compatibility: unknown", `compatibility: "^1.0.0"`, 1)
	res, err := carryForward(t.Context(), e.store(t), e.base(t), bundleOf(t, files), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pacto.yaml declares dependencies[billing].compatibility; acceptance " + ref + " not applied",
		"pin dependencies[ghost].ref: the contract declares no dependency ghost; acceptance " + ref + " not applied",
		"service.owner is no dependency ref or compatibility; acceptance " + ref + " not applied"}
	if res.Pinned != 0 || len(res.Cited) != 0 || strings.Join(res.Notes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%+v", res)
	}
}

func TestCarryCitesBreakAcceptance(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	brk := delivery.AcceptanceRecord{Service: e.key("reports"), Version: "1.6.0", Kind: delivery.AcceptKindBreak,
		Break: &delivery.AcceptedBreak{Provider: e.key("ledger"), Version: "3.0.0", Announcement: "abc"}}
	ref := e.repo + "@" + e.attach(t, attachment{record: brk, token: personTok("ana@acme.com"), tag: true})
	res := e.carry(t, e.store(t))
	if res.Pinned != 0 || len(res.Cited) != 1 || res.Cited[0].Record != ref || res.Cited[0].Fields != "break "+e.key("ledger")+"@3.0.0" {
		t.Fatalf("%+v", res)
	}
	// A local bundle with no record gets one holding the citation.
	local := bundleOf(t, e.reportsFiles("1.6.1", false))
	local.FS = contract.WithoutInferredRecord(local.FS)
	got, err := carryForward(t.Context(), e.store(t), e.base(t), local, nil)
	if err != nil || carriedRecord(t, got.Bundle).Inputs.Acceptances[0].Record != ref {
		t.Fatal(err)
	}
}

func TestCarryIgnoresRoutedAndProposal(t *testing.T) {
	e := newAcceptEnv(t, false)
	swapPublicGood(t, e.ss.Root, nil)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("bob@acme.com"), tag: true})
	e.attach(t, attachment{record: e.rangesRecord(), tag: true,
		token: testutil.CIToken("https://github.com/acme/reports/.github/workflows/ci.yml@refs/heads/main", "https://token.actions.githubusercontent.com", "https://github.com/acme/reports", "refs/heads/main")})
	res := e.carry(t, newAcceptanceStore(t.Context(), e.svc.env(), nil))
	if res.Pinned != 0 || len(res.Cited) != 0 || len(res.Notes) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestCarryLatestAcceptanceWins(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	time.Sleep(1100 * time.Millisecond) // the test Sigstore stamps whole seconds
	newer := e.rangesRecord()
	newer.Ranges[0].Value = "^2.4.0"
	ref := e.repo + "@" + e.attach(t, attachment{record: newer, token: personTok("ana@acme.com"), tag: true})
	res := e.carry(t, e.store(t))
	if res.Bundle.Contract.Dependencies[0].Compatibility != "^2.4.0" || len(res.Cited) != 1 || res.Cited[0].Record != ref {
		t.Fatalf("%+v", res)
	}
}

func TestCarryDropsRecordThatNoLongerVerifies(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	ref := e.repo + "@" + e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	// reports 1.6.1 published the carried range and cites the record.
	files := e.reportsFiles("1.6.1", false)
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "  - name: ledger\n", "  - name: ledger\n    compatibility: \"^2.3.0\"\n", 1)
	rec := acceptRecordOf()
	rec.Inputs.Acceptances = []contract.CitedAcceptance{{Record: ref, Signer: "ana@acme.com", Fields: "dependencies[ledger].compatibility"}}
	files[contract.InferredRecordFile] = string(rec.Marshal())
	d := e.publishBundle(t, e.repo, "1.6.1", files, nil)
	b, _ := e.reg.Pull(t.Context(), e.repo+"@"+d)
	base := carryBase{repo: e.repo, version: "1.6.1", digest: d, b: b}
	local := bundleOf(t, e.reportsFiles("1.6.2", false))
	res, err := carryForward(t.Context(), e.store(t), base, local, nil)
	if err != nil || res.Pinned != 1 || res.Cited[0].Record != ref {
		t.Fatalf("%v %+v", err, res)
	}
	// Under another root the cited record no longer verifies: it stops carrying.
	other := testutil.New(t)
	st := newAcceptanceStore(t.Context(), e.svc.env(), func(context.Context) (Installation, error) {
		return Installation{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: ".*", TrustedRoot: string(other.RootJSON)}}}, nil
	})
	res, err = carryForward(t.Context(), st, base, local, nil)
	if err != nil || res.Pinned != 0 || res.Bundle != local || !hasNote(res.Notes, "acceptance "+ref+" no longer carried: it does not verify") {
		t.Fatalf("%v %+v", err, res)
	}
	// A cited record that cannot be read is an error: dropping it would
	// publish the decision away. A listing that fails carries nothing new.
	e.failWhen(func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/manifests/"+strings.TrimPrefix(ref, e.repo+"@"))
	})
	if _, err := carryForward(t.Context(), e.store(t), base, local, nil); err == nil || !strings.Contains(err.Error(), "the acceptance "+ref+" 1.6.1 cites") {
		t.Fatal(err)
	}
	e.failWhen(func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/tags/list") })
	if res := e.carry(t, e.store(t)); res.Bundle.Contract.Dependencies[0].Compatibility != contract.CompatibilityUnknown || !hasNote(res.Notes, "acceptances of 1.6.0 not read, none carried") {
		t.Fatalf("%+v", res)
	}
}

func TestCarryUnwritableBundle(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	local := bundleOf(t, e.reportsFiles("1.6.1", false))
	local.FS = failFS{}
	if _, err := carryForward(t.Context(), e.store(t), e.base(t), local, nil); err == nil || !strings.Contains(err.Error(), "carrying registry decisions into pacto.yaml") {
		t.Fatal(err)
	}
}

func TestReadCited(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	ref := e.repo + "@" + e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	st := e.store(t)
	if j, why, err := st.readCited(t.Context(), ref, e.host, e.key("reports"), nil); err != nil || why != "" || j.State != impact.AcceptCounted || j.Ref != ref {
		t.Fatalf("%v %q %+v", err, why, j)
	}
	other := e.publishBundle(t, e.repo, "9.0.0", map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: billing, version: 9.0.0}\n"}, nil)
	for name, tc := range map[string]struct{ ref, host, why string }{
		"no digest":     {e.repo, e.host, "it names no manifest digest"},
		"other host":    {ref, "elsewhere.io", "it is on " + e.host + ", read only on elsewhere.io"},
		"gone":          {e.repo + "@sha256:" + strings.Repeat("0", 64), e.host, "the record is gone"},
		"not a record":  {e.subject, e.host, "it is no acceptance record"},
		"other service": {e.repo + "@" + e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), manifest: other, tag: true, tagOf: other}), e.host, "it is attached to " + e.key("billing")},
	} {
		if _, why, err := st.readCited(t.Context(), tc.ref, tc.host, e.key("reports"), nil); err != nil || !strings.HasPrefix(why, tc.why) {
			t.Fatalf("%s: %v %q", name, err, why)
		}
	}
	e.failWhen(func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/manifests/") })
	if _, _, err := st.readCited(t.Context(), ref, e.host, e.key("reports"), nil); err == nil {
		t.Fatal("a failed resolve read")
	}
	e.failWhen(func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/manifests/"+e.digest)
	})
	if _, _, err := st.readCited(t.Context(), ref, e.host, e.key("reports"), nil); err == nil || !strings.Contains(err.Error(), "reading "+e.subject) {
		t.Fatal(err)
	}
	e.failWhen(nil)
	broken := *st
	broken.open = func(string) (*remote.Repository, error) { return nil, errors.New("no registry") }
	if _, _, err := broken.readCited(t.Context(), ref, e.host, e.key("reports"), nil); err == nil {
		t.Fatal("an unopened repository read")
	}
}

func TestImpactReadsCitedAcceptances(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	rd := e.reader(t, &fleet.FleetSnapshot{})
	brk := delivery.AcceptanceRecord{Service: e.key("reports"), Version: "1.6.0", Kind: delivery.AcceptKindBreak,
		Break: &delivery.AcceptedBreak{Provider: e.key("ledger"), Version: "3.0.0", Announcement: rd.in.Announced["reports"].Announcement}}
	m := e.attach(t, attachment{record: brk, token: personTok("ana@acme.com"), tag: true})
	ref := e.repo + "@" + m
	// reports 1.7.0 is the newest version; its record carries the break
	// acceptance attached to 1.6.0, which is no longer newest.
	files := e.reportsFiles("1.7.0", false)
	rec := acceptRecordOf()
	cite := contract.CitedAcceptance{Record: ref, Signer: "ana@acme.com", Fields: "break " + e.key("ledger") + "@3.0.0"}
	rec.Inputs.Acceptances = []contract.CitedAcceptance{cite, cite, {Record: e.repo + "@sha256:" + strings.Repeat("0", 64), Signer: "ana@acme.com", Fields: "x"}}
	files[contract.InferredRecordFile] = string(rec.Marshal())
	d17 := e.publishBundle(t, e.repo, "1.7.0", files, nil)
	// An unsigned record attached to 1.7.0 is ignored, with a note.
	e.attach(t, attachment{record: brk, manifest: d17, tag: true})
	key := fleet.ServiceKey(e.key("reports"))
	accs, complete := rd.read(t.Context(), key)
	if !complete || len(accs) != 1 || accs[0].Record != ref || accs[0].State != impact.AcceptCounted || accs[0].Break == nil {
		t.Fatalf("%v %+v", complete, accs)
	}
	e.failWhen(func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/manifests/"+m) })
	if _, complete := e.reader(t, &fleet.FleetSnapshot{}).read(t.Context(), key); complete {
		t.Fatal("a cited record that could not be read read complete")
	}
}

// TestCarryPinsOverDeclaredUnknown: compatibility: unknown, as pactify and
// pacto_create write it, declares no range, so the accepted one replaces it.
func TestCarryPinsOverDeclaredUnknown(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	r := e.rangesRecord()
	r.Ranges = []delivery.AcceptedRange{{Field: "dependencies[billing].compatibility", Value: "^1.4.0", Proposed: "^1.4.0", Source: snapSrc}}
	e.attach(t, attachment{record: r, token: personTok("ana@acme.com"), tag: true})
	res := e.carry(t, e.store(t))
	raw, _ := fs.ReadFile(res.Bundle.FS, "pacto.yaml")
	if res.Pinned != 1 || len(res.Cited) != 1 || !strings.Contains(string(raw), "    compatibility: \"^1.4.0\"\n") || strings.Contains(string(raw), "unknown") {
		t.Fatalf("%+v\n%s", res, raw)
	}
}

// TestCarryRefOnlyStaysValid: a ref accepted alone pins compatibility:
// unknown beside it, so the published pacto.yaml validates when re-read.
func TestCarryRefOnlyStaysValid(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	r := e.rangesRecord()
	r.Ranges = []delivery.AcceptedRange{{Field: "dependencies[ledger].ref", Value: "oci://" + e.host + "/acme/ledger", Source: indexSrc}}
	e.attach(t, attachment{record: r, token: personTok("ana@acme.com"), tag: true})
	res := e.carry(t, e.store(t))
	raw, _ := fs.ReadFile(res.Bundle.FS, "pacto.yaml")
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil || res.Pinned != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if vr := validation.Validate(c, raw, res.Bundle.FS); !vr.IsValid() || c.Dependencies[0].Compatibility != contract.CompatibilityUnknown {
		t.Fatalf("%+v\n%s", vr.Errors, raw)
	}
}

// TestCarryNotesPublicGoodJudgement: what carries under no installation
// signer policy says it was judged with public-good issuers.
func TestCarryNotesPublicGoodJudgement(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	st := newAcceptanceStore(t.Context(), e.svc.env(), nil)
	st.verify = e.store(t).verify // the test Sigstore stands in for public-good
	if res := e.carry(t, st); len(res.Cited) != 1 || !slices.Contains(res.Notes, publicGoodNote) {
		t.Fatalf("%+v", res)
	}
	if res := e.carry(t, e.store(t)); slices.Contains(res.Notes, publicGoodNote) {
		t.Fatalf("an installation policy noted public-good: %+v", res.Notes)
	}
}
