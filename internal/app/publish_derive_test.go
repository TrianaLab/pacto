package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// writeDerivedBundle writes a 2.5 payments contract that publishes only its
// bundle to host, signed with a key, at version ("" leaves it undeclared).
func writeDerivedBundle(t *testing.T, dir, host, version, extra string) {
	t.Helper()
	y := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n"
	if version != "" {
		y += "  version: " + version + "\n"
	}
	y += extra + "publish:\n  bundle: oci://" + host + "/pacto/payments\n  sign: {key: cosign.key, transparencyLog: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
}

// countingRegistry counts the writes and tags that reach reg.
type countingRegistry struct {
	publish.Registry
	writes int
}

func (c *countingRegistry) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	c.writes++
	return c.Registry.Write(ctx, repo, a)
}

func (c *countingRegistry) Tag(ctx context.Context, repo, digest, tag string) error {
	c.writes++
	return c.Registry.Tag(ctx, repo, digest, tag)
}

// derivedFolder publishes 1.4.2 as declared, then leaves the version
// undeclared and adds a file, so the next publish derives 1.4.3.
func derivedFolder(t *testing.T) (dir, host string, reg *countingRegistry, svc *Service) {
	t.Helper()
	host, inner := publishRegistry(t)
	reg = &countingRegistry{Registry: inner}
	svc = publishService(&fakeRun{}, reg)
	dir = t.TempDir()
	signingKey(t, dir)
	writeDerivedBundle(t, dir, host, "1.4.2", "")
	if _, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	writeDerivedBundle(t, dir, host, "", "")
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# payments\n"), 0o644)
	return dir, host, reg, svc
}

// TestPublishDerivedVersionRecorded: the published pacto.yaml declares the
// derived version and its record says where it came from.
func TestPublishDerivedVersionRecorded(t *testing.T) {
	dir, host, reg, svc := derivedFolder(t)
	ctx := context.Background()
	base, err := reg.Head(ctx, host+"/pacto/payments:1.4.2")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2"})
	if err != nil || res.Plan.Version != "1.4.3" || res.Plan.Baseline != "1.4.2" {
		t.Fatalf("%v %+v", err, res)
	}
	b, err := reg.Pull(ctx, host+"/pacto/payments:1.4.3")
	if err != nil {
		t.Fatal(err)
	}
	if b.Contract.Service.Version != "1.4.3" || !strings.Contains(string(b.RawYAML), "  version: 1.4.3\n") {
		t.Fatalf("pinned pacto.yaml:\n%s", b.RawYAML)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil {
		t.Fatalf("record: %v", err)
	}
	if err := contract.VerifyInferredRecord(b.FS, rec); err != nil {
		t.Fatal(err)
	}
	// The fixture's baseline carries no revision: the run says the stale
	// check did not happen instead of passing it silently.
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "stale-run check not performed") {
		t.Fatalf("notes: %v", res.Notes)
	}
	want := []contract.Inferred{{Field: "service.version", Value: "1.4.3 (1.4.2 + patch, NON_BREAKING)", Source: "registry:" + host + "/pacto/payments@" + base.Digest,
		Confidence: contract.ConfidenceLikely, Declare: "service:\n  version: 1.4.3"}}
	if !reflect.DeepEqual(rec.Fields, want) || rec.Inputs.Commit != "r2" {
		t.Fatalf("record: %+v", rec)
	}
}

// TestPublishedIsUnchangedAgainstFolder: right after a derived publish, plan
// mode's comparator reads the folder as what was published.
func TestPublishedIsUnchangedAgainstFolder(t *testing.T) {
	dir, host, reg, svc := derivedFolder(t)
	ctx := context.Background()
	if _, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2"}); err != nil {
		t.Fatal(err)
	}
	published, err := reg.Pull(ctx, host+"/pacto/payments:1.4.3")
	if err != nil {
		t.Fatal(err)
	}
	c, raw, fsys, err := loadAndValidateFull(ctx, dir, override.Overrides{}, nil, contract.InferredInputs{Commit: "r3"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if same, why := Unchanged(published, &contract.Bundle{Contract: c, RawYAML: raw, FS: fsys}); !same {
		t.Fatalf("published reads as changed: %s", why)
	}
}

// TestPublishUnchangedFromNewCommitWritesNothing: the same folder published
// again from a later commit is unchanged, with nothing written.
func TestPublishUnchangedFromNewCommitWritesNothing(t *testing.T) {
	dir, _, reg, svc := derivedFolder(t)
	ctx := context.Background()
	first, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2"})
	if err != nil {
		t.Fatal(err)
	}
	reg.writes = 0
	for _, plan := range []bool{true, false} {
		res, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r3", Plan: plan})
		if err != nil || res.Plan.Version != "1.4.3" || len(res.Actions) != 0 || !res.Plan.Ready ||
			res.Digests[publish.BundleItem] != first.Digests[publish.BundleItem] || res.Plan.Items[0].State != publish.Identical {
			t.Fatalf("plan=%v: %v %+v", plan, err, res)
		}
	}
	if reg.writes != 0 {
		t.Fatalf("%d writes", reg.writes)
	}
}

// TestPublishDeclaredBelowFloorStillFails: a declared version keeps today's
// rule and its refusal.
func TestPublishDeclaredBelowFloorStillFails(t *testing.T) {
	host, reg := publishRegistry(t)
	svc := publishService(&fakeRun{}, reg)
	dir := t.TempDir()
	signingKey(t, dir)
	writeDerivedBundle(t, dir, host, "1.4.2", "workload: service\n")
	if _, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	writeDerivedBundle(t, dir, host, "1.4.3", "workload: job\n")
	_, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r2"})
	if !errors.Is(err, publish.ErrNotReady) || !strings.Contains(err.Error(), "1.4.3 needs a major bump over 1.4.2: publish 2.0.0 or higher") {
		t.Fatalf("%v", err)
	}
}

// TestPublishDeclaredFirstVersionAsWritten: a declared first version
// publishes as written, with no record field.
func TestPublishDeclaredFirstVersionAsWritten(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := t.TempDir()
	signingKey(t, dir)
	writeDerivedBundle(t, dir, host, "0.3.0", "")
	res, err := publishService(&fakeRun{}, reg).Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil || res.Plan.Version != "0.3.0" || len(res.Notes) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
}

// TestPublishDerivedFirstVersion: an empty repository gives 1.0.0, recorded
// against the bare repository.
func TestPublishDerivedFirstVersion(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := t.TempDir()
	signingKey(t, dir)
	writeDerivedBundle(t, dir, host, "", "")
	res, err := publishService(&fakeRun{}, reg).Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1", Plan: true})
	if err != nil || res.Plan.Version != publish.FirstVersion {
		t.Fatalf("%v %+v", err, res)
	}
	f := versionField(host+"/p", publish.Derived{Version: "1.0.0"})
	if f.Value != "1.0.0 (no versions in "+host+"/p)" || f.Source != "registry:"+host+"/p" {
		t.Fatalf("%+v", f)
	}
}

// TestPublishDeriveFailures: no signer means no verified baseline, and a
// derive error is the publish's error.
func TestPublishDeriveFailures(t *testing.T) {
	host, reg := publishRegistry(t)
	ctx := context.Background()
	dir := t.TempDir()
	writeDerivedBundle(t, dir, host, "", "")
	_ = os.WriteFile(filepath.Join(dir, ".pactoignore"), []byte("cosign.key\n"), 0o600)
	if _, err := publishService(&fakeRun{}, reg).Publish(ctx, PublishOptions{Path: dir, Revision: "r1"}); err == nil ||
		!strings.Contains(err.Error(), "needs the publisher identity to verify the baseline") {
		t.Fatalf("no key: %v", err)
	}
	signingKey(t, dir)
	broken := publishService(&fakeRun{}, brokenTags{reg})
	if _, err := broken.Publish(ctx, PublishOptions{Path: dir, Revision: "r1"}); err == nil || !strings.Contains(err.Error(), "listing the versions") {
		t.Fatalf("unreadable registry: %v", err)
	}
	shallow := publishService(&fakeRun{fail: "git cat-file"}, reg)
	writeDerivedBundle(t, dir, host, "", "workload: service\n")
	if res, err := shallow.Publish(ctx, PublishOptions{Path: dir, Revision: "r0"}); err != nil || len(res.Notes) != 0 {
		t.Fatalf("first: %v %+v", err, res)
	}
	writeDerivedBundle(t, dir, host, "", "")
	if res, err := shallow.Publish(ctx, PublishOptions{Path: dir, Revision: "r1", Plan: true}); err != nil ||
		!reflect.DeepEqual(res.Notes, []string{"baseline revision r0 not in this checkout; fetch more history to check"}) {
		t.Fatalf("shallow: %v %+v", err, res)
	}
	stale := publishService(&fakeRun{fail: "git merge-base"}, reg)
	if _, err := stale.Publish(ctx, PublishOptions{Path: dir, Revision: "r1"}); err == nil || !strings.Contains(err.Error(), "superseded: payments 1.0.0 was published from r0") {
		t.Fatalf("stale: %v", err)
	}
}

// TestPinDerivedFailures: a contract the version cannot be pinned into, or
// whose record cannot be extended, is an error.
func TestPinDerivedFailures(t *testing.T) {
	host := "example.com"
	b := &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "payments"}, Publish: &contract.Publish{Bundle: "oci://" + host + "/x"}},
		RawYAML: []byte("pactoVersion: \"2.5\"\nservice: {name: payments}\n")}
	if _, err := pinDerived(&contract.Bundle{Contract: b.Contract, RawYAML: []byte("[a]")}, "reg/pacto/payments", publish.Derived{Version: "1.0.0"}, contract.InferredInputs{}); err == nil {
		t.Fatal("a sequence was pinned")
	}
	if _, err := pinDerived(&contract.Bundle{Contract: b.Contract, RawYAML: []byte("service: {name: payments}\n")}, "reg/pacto/payments", publish.Derived{Version: "1.0.0"}, contract.InferredInputs{}); err == nil ||
		!strings.Contains(err.Error(), "pinning service.version 1.0.0") {
		t.Fatalf("no pactoVersion: %v", err)
	}
	b.FS = fstest.MapFS{contract.InferredRecordFile: {Data: []byte("bogus: 1\n")}}
	if _, err := pinDerived(b, "reg/pacto/payments", publish.Derived{Version: "1.0.0"}, contract.InferredInputs{}); err == nil {
		t.Fatal("a broken record was extended")
	}
	b.FS = walkFailFS{}
	if _, err := pinDerived(b, "reg/pacto/payments", publish.Derived{Version: "1.0.0"}, contract.InferredInputs{}); err == nil || !strings.Contains(err.Error(), "pinned bundle FS") {
		t.Fatalf("unwalkable: %v", err)
	}
}

// brokenTags fails every listing.
type brokenTags struct{ publish.Registry }

func (brokenTags) Tags(context.Context, string) ([]string, error) { return nil, errors.New("down") }

// walkFailFS holds no record and cannot be walked.
type walkFailFS struct{}

func (walkFailFS) Open(name string) (fs.File, error) {
	if name == contract.InferredRecordFile {
		return nil, fs.ErrNotExist
	}
	return nil, fmt.Errorf("cannot open %s", name)
}

// TestSuperseded: a checkout must contain the baseline's revision; in a
// shallow clone the commit times decide, and with no baseline time a note
// says what was not checked.
func TestSuperseded(t *testing.T) {
	ctx := context.Background()
	d := publish.Derived{Baseline: "1.4.2", BaselineRevision: "abc", BaselineCreated: "2026-11-01T00:00:00Z"}
	run := func(answers map[string]string) runner {
		return func(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
			call := strings.Join(append([]string{name}, args...), " ")
			for prefix, out := range answers {
				if strings.HasPrefix(call, prefix) {
					if out == "fail" {
						return nil, errors.New("exit 1")
					}
					return []byte(out), nil
				}
			}
			return nil, errors.New("unexpected " + call)
		}
	}
	unknown := "baseline revision abc not in this checkout; fetch more history to check"
	for name, tc := range map[string]struct {
		d       publish.Derived
		answers map[string]string
		note    string
		refused bool
	}{
		"first":        {publish.Derived{}, nil, "", false},
		"no revision":  {publish.Derived{Baseline: "1.4.0"}, nil, "baseline 1.4.0 names no revision (no deploy artifact annotation, no inferred record): stale-run check not performed", false},
		"contained":    {d, map[string]string{"git cat-file": "", "git merge-base": ""}, "", false},
		"not an anc.":  {d, map[string]string{"git cat-file": "", "git merge-base": "fail"}, "", true},
		"no time":      {publish.Derived{BaselineRevision: "abc"}, map[string]string{"git cat-file": "fail"}, unknown, false},
		"no HEAD time": {d, map[string]string{"git cat-file": "fail", "git show": "fail"}, unknown, false},
		"bad HEAD":     {d, map[string]string{"git cat-file": "fail", "git show": "soon"}, unknown, false},
		"HEAD older":   {d, map[string]string{"git cat-file": "fail", "git show": "1000\n"}, "", true},
		"HEAD newer":   {d, map[string]string{"git cat-file": "fail", "git show": "4000000000\n"}, "", false},
	} {
		note, err := superseded(ctx, run(tc.answers), ".", "payments", tc.d)
		if note != tc.note || (err != nil) != tc.refused || tc.refused && !strings.Contains(err.Error(), "superseded: payments 1.4.2 was published from abc, which this checkout does not contain") {
			t.Errorf("%s: %q %v", name, note, err)
		}
	}
}

// TestLoadAndValidateFullDeriveVersion: publish may leave service.version
// to derive, and pactoVersion is still pinned; push may not.
func TestLoadAndValidateFullDeriveVersion(t *testing.T) {
	dir := t.TempDir()
	writeDerivedBundle(t, dir, "example.com", "", "")
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service:\n  name: payments\n"), 0o644)
	c, raw, _, err := loadAndValidateFull(context.Background(), dir, override.Overrides{}, nil, contract.InferredInputs{}, nil, true)
	if err != nil || c.Service.Version != "" || c.PactoVersion != contract.Current() || !strings.Contains(string(raw), `pactoVersion: "`+contract.Current()+`"`) {
		t.Fatalf("%v %+v\n%s", err, c, raw)
	}
	if _, _, _, err := loadAndValidateFull(context.Background(), dir, override.Overrides{}, nil, contract.InferredInputs{}, nil, false); !errors.Is(err, ErrPublishNeedsServiceVersion) {
		t.Fatalf("%v", err)
	}
}

// TestPublishUnchangedNamesTheDeployArtifact: an unchanged folder still names
// the deploy artifact its version published, once it verifies, so --propose
// has a digest to propose; one missing or unsigned gives a note instead.
func TestPublishUnchangedNamesTheDeployArtifact(t *testing.T) {
	host, reg := publishRegistry(t)
	svc := publishService(&fakeRun{}, reg)
	ctx := context.Background()
	dir := t.TempDir()
	signingKey(t, dir)
	write := func(version string) {
		y := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n" + version + "publish:\n  bundle: oci://" + host + "/pacto/payments\n  deploy: manifests\n" +
			"  artifacts:\n    - {name: manifests, type: manifests, ref: \"oci://" + host + "/deploy/payments\", build: {command: [./render.sh]}}\n" +
			"  sign: {key: cosign.key, transparencyLog: false}\n"
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("  version: 1.4.2\n")
	first, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r1"})
	if err != nil || first.Deploy == "" {
		t.Fatalf("%v %+v", err, first)
	}
	write("")
	res, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2"})
	if err != nil || res.Plan.Version != "1.4.2" || res.Deploy != first.Deploy || len(res.Notes) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	unsigned := pushImage(t, reg, host+"/unsigned")
	repo, digest, _ := strings.Cut(unsigned, "@")
	if err := reg.Tag(ctx, repo, digest, "1.4.2"); err != nil {
		t.Fatal(err)
	}
	no := false
	tools := newExecTools(dir, nil, reg)
	for ref, want := range map[string]string{host + "/missing": "not found", repo: "no deploy artifact to propose: " + repo + ":1.4.2"} {
		pub := &contract.Publish{Deploy: "manifests", Sign: &contract.PublishSign{Key: "cosign.key", TransparencyLog: &no},
			Artifacts: []contract.PublishArtifact{{Name: "other", Ref: "oci://" + host + "/other"}, {Name: "manifests", Ref: "oci://" + ref}}}
		if got, notes := unchangedDeploy(ctx, reg, tools, pub, "1.4.2"); got != "" || len(notes) != 1 || !strings.Contains(notes[0], want) {
			t.Errorf("%s: %q %v", ref, got, notes)
		}
	}
}

// paymentsDeclaration is ana's declaration of payments at version in the
// bundle repository writeDerivedBundle publishes to, naming baseline (nil
// for a first declaration).
func paymentsDeclaration(host, version string, baseline *contract.InferredBaseline) map[string]string {
	y := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n  version: " + version + "\n" +
		"publish:\n  bundle: oci://" + host + "/pacto/payments\n  sign: {key: cosign.key, transparencyLog: false}\n"
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared,
		Asker: contract.InferredSigner{Subject: "ana@acme.com", Issuer: personIssuer, Class: contract.SignerClassHuman}, Baseline: baseline}}
	return map[string]string{"pacto.yaml": y, contract.InferredRecordFile: string(rec.Marshal())}
}

// declaredFolder is a payments folder that leaves service.version
// undeclared (signed with a key when keyed) over a registry whose only
// release is ana's 0.1.0 declaration, signed by her when signed. It returns
// the declaration's digest.
func declaredFolder(t *testing.T, keyed, signed bool) (*declareEnv, string, *Service, string) {
	t.Helper()
	return declaredFolderWith(t, keyed, signed, "")
}

// declaredFolderWith is declaredFolder whose declaration also declares
// extra under service.
func declaredFolderWith(t *testing.T, keyed, signed bool, extra string) (*declareEnv, string, *Service, string) {
	t.Helper()
	e := newDeclareEnv(t)
	dir := t.TempDir()
	if keyed {
		signingKey(t, dir)
	}
	writeDerivedBundle(t, dir, e.host, "", "")
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# payments\n"), 0o644)
	var s *signing.Signer
	if signed {
		sg := e.signer(t, personToken("ana@acme.com"))
		s = &sg
	}
	files := paymentsDeclaration(e.host, "0.1.0", nil)
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "  version: 0.1.0\n", "  version: 0.1.0\n"+extra, 1)
	d := e.publishBundle(t, e.host+"/pacto/payments", "0.1.0", files, s)
	return e, dir, publishService(&fakeRun{}, e.reg), d
}

func TestPublishNotesDeclarationNotCarried(t *testing.T) {
	noPublicGood(t)
	e, dir, svc, d := declaredFolderWith(t, true, true, "  owner:\n    team: billing\n")
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true, declTrust: e.trustingAcme(t)})
	want := "pacto publish does not carry registry declarations: service.owner declared in " + e.host + "/pacto/payments@" + d +
		" are not in this version; run pacto ci --publish or write them in pacto.yaml"
	if err != nil || !slices.Contains(res.Notes, want) {
		t.Fatalf("%v %q", err, res.Notes)
	}
	// A field pacto.yaml writes is in this version.
	writeDerivedBundle(t, dir, e.host, "", "  owner:\n    team: payments\n")
	res, err = svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true, declTrust: e.trustingAcme(t)})
	if err != nil || notCarried(res.Notes) {
		t.Fatalf("%v %q", err, res.Notes)
	}
}

func TestPublishNoNoteWithoutDeclaration(t *testing.T) {
	noPublicGood(t)
	e, dir, svc, _ := declaredFolder(t, true, true) // a declaration that declares no carried field
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true, declTrust: e.trustingAcme(t)})
	if err != nil || res.Plan.Baseline != "0.1.0" || notCarried(res.Notes) {
		t.Fatalf("%v %q", err, res.Notes)
	}
}

func TestDeriveCountsDeclarationAsBaseline(t *testing.T) {
	noPublicGood(t)
	e, dir, svc, d := declaredFolder(t, true, true)
	ctx := t.Context()
	res, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2", declTrust: e.trustingAcme(t)})
	if err != nil || res.Plan.Baseline != "0.1.0" || res.Plan.Version != "0.1.1" {
		t.Fatalf("%v %+v", err, res)
	}
	b, err := e.reg.Pull(ctx, e.host+"/pacto/payments:0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := contract.ReadInferredRecord(b.FS)
	if rec == nil || len(rec.Fields) != 1 || rec.Fields[0].Source != "registry:"+e.host+"/pacto/payments@"+d {
		t.Fatalf("%+v", rec)
	}
}

func TestDeriveCountsDeclarationNoPolicyNote(t *testing.T) {
	e, dir, svc, _ := declaredFolder(t, true, true)
	publicGoodIs(t, e.ss.Root, nil)
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true})
	if err != nil || res.Plan.Baseline != "0.1.0" || !slices.Contains(res.Notes, declarationsPublicGoodNote) {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestDeriveSkipsForgedDeclaration(t *testing.T) {
	e, dir, svc, _ := declaredFolder(t, true, false)
	_, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", declTrust: e.trustingAcme(t)})
	if err == nil || !strings.Contains(err.Error(), "no verifiable baseline in ") {
		t.Fatal(err)
	}
}

func TestDeriveDeclarationNeedsPublisherIdentity(t *testing.T) {
	e, dir, svc, _ := declaredFolder(t, false, true)
	refs := &refCounter{Registry: e.reg}
	svc.sys.publishRegistry = func() publish.Registry { return refs }
	_, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", declTrust: e.trustingAcme(t)})
	if err == nil || !strings.Contains(err.Error(), "needs the publisher identity") || refs.n != 0 {
		t.Fatal(err, refs.n)
	}
}

func TestForgedDeclarationWithoutRootDoesNotBlockPublish(t *testing.T) {
	e := newDeclareEnv(t)
	publicGoodIs(t, nil, errors.New("tuf down"))
	svc := publishService(&fakeRun{}, e.reg)
	dir := t.TempDir()
	signingKey(t, dir)
	writeDerivedBundle(t, dir, e.host, "0.1.0", "")
	if _, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	repo := e.host + "/pacto/payments"
	base, _ := e.reg.Head(t.Context(), repo+":0.1.0")
	forged := e.publishBundle(t, repo, "0.1.1", paymentsDeclaration(e.host, "0.1.1", &contract.InferredBaseline{Version: "0.1.0", Bundle: repo + "@" + base.Digest}), nil)
	writeDerivedBundle(t, dir, e.host, "", "")
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# payments\n"), 0o644)
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true})
	want := "ignored 0.1.1: declaration " + repo + "@" + forged + " not checked: no trusted root reachable"
	if err != nil || res.Plan.Baseline != "0.1.0" || !slices.Contains(res.Notes, want) {
		t.Fatalf("%v %+v", err, res)
	}
}

// deriveFaults fails Pull, or every Tags listing after the first.
type deriveFaults struct {
	publish.Registry
	pull bool
	tags int
}

func (f *deriveFaults) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if f.pull {
		return nil, errors.New("503")
	}
	return f.Registry.Pull(ctx, ref)
}

func (f *deriveFaults) Tags(ctx context.Context, repo string) ([]string, error) {
	if f.tags++; f.tags > 1 {
		return nil, errors.New("503")
	}
	return f.Registry.Tags(ctx, repo)
}

func TestDeriveDeclarationCheckUnreadIsError(t *testing.T) {
	for _, f := range []*deriveFaults{{pull: true, tags: -1 << 20}, {}} {
		e, dir, svc, _ := declaredFolder(t, true, true)
		f.Registry = e.reg
		svc.sys.publishRegistry = func() publish.Registry { return f }
		_, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true, declTrust: e.trustingAcme(t)})
		if err == nil || !strings.Contains(err.Error(), "cannot verify") {
			t.Fatal(err)
		}
	}
}
