package app

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// pushedBundle is the bundle at ref ("<repo>:<tag>@<digest>" or a tag) and
// its manifest's io.pacto.pactoVersion label.
func (e *ciPubEnv) pushedBundle(t *testing.T, ref string) (*contract.Bundle, string) {
	t.Helper()
	if repoTag, digest, ok := strings.Cut(ref, "@"); ok {
		ref = repoTag[:strings.LastIndex(repoTag, ":")] + "@" + digest
	}
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	img, err := remote.Image(r)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	b, err := oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host)).Pull(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return b, cfg.Config.Labels["io.pacto.pactoVersion"]
}

// ciPublishAt runs pacto ci --publish at revision, filling bundles.
func (e *ciPubEnv) ciPublishAt(t *testing.T, revision string, bundles map[string]*contract.Bundle) *ci.Plan {
	t.Helper()
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles}, Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPublishUsesGivenBundle: Publish with a Bundle packs that bundle, not
// the folder on disk, and its checks read the given contract.
func TestPublishUsesGivenBundle(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.0.0", "", "")})
	dir := filepath.Join(e.root, "payments")
	b, err := e.svc.ciBundle(dir, e.root, "r1", nil)
	if err != nil {
		t.Fatal(err)
	}
	given, _ := copyFSWithReplace(b.FS, "", nil)
	given["notes.md"] = &fstest.MapFile{Data: []byte("# only in memory\n")}
	b.FS = given
	res, err := e.svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1", Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	bundle := res.Plan.Items[len(res.Plan.Items)-1]
	pushed, _ := e.pushedBundle(t, bundle.Ref+"@"+res.Digests[publish.BundleItem])
	if data, err := fs.ReadFile(pushed.FS, "notes.md"); err != nil || string(data) != "# only in memory\n" {
		t.Fatalf("pushed notes.md %q %v", data, err)
	}
	// The folder on disk is valid; the given contract is not, and refuses.
	local := *b
	local.Contract = &contract.Contract{PactoVersion: b.Contract.PactoVersion, Service: b.Contract.Service, Publish: b.Contract.Publish,
		Dependencies: []contract.Dependency{{Name: "ledger", Ref: "../ledger"}}}
	if _, err := e.svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2", Plan: true, Bundle: &local}); err == nil || !strings.Contains(err.Error(), "contract validation failed") {
		t.Fatalf("the given contract was not validated: %v", err)
	}
}

func TestCIMigratedLabelMatchesBytes(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	r := pubRow(t, e.ciPublish(t, nil), "legacy")
	if r.State != ci.StatePublished {
		t.Fatalf("row %+v", r)
	}
	b, label := e.pushedBundle(t, r.Published)
	if label != contract.Current() || b.Contract.PactoVersion != label {
		t.Fatalf("label %s, pacto.yaml %s", label, b.Contract.PactoVersion)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil || len(rec.Migrations) != 1 || rec.Migrations[0].From != prevVersion {
		t.Fatalf("record %+v %v", rec, err)
	}
}

// TestCIKeptLabelMatchesBytes: raising this contract would add a finding,
// so it publishes as written.
func TestCIKeptLabelMatchesBytes(t *testing.T) {
	e := newCIPub(t)
	keptLegacy(t, e)
	r := pubRow(t, e.ciPublish(t, nil), "legacy")
	if r.State != ci.StatePublished {
		t.Fatalf("row %+v", r)
	}
	b, label := e.pushedBundle(t, r.Published)
	if label != keptFrom || b.Contract.PactoVersion != label {
		t.Fatalf("label %s, pacto.yaml %s", label, b.Contract.PactoVersion)
	}
	if rec, _ := contract.ReadInferredRecord(b.FS); rec != nil {
		t.Fatalf("a kept bundle carries a record: %+v", rec)
	}
}

func TestCIMigratedTwiceSameDigest(t *testing.T) {
	e := newE2ERepo(t)
	writeFiles(t, e.root, map[string]string{"ledger/pacto.yaml": "pactoVersion: " + strconv.Quote(prevVersion) + "\nservice:\n  name: ledger\n  version: 1.0.0\n"})
	digests := map[string]bool{}
	for range 2 {
		bundles := map[string]*contract.Bundle{}
		if _, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles}); err != nil {
			t.Fatal(err)
		}
		d, err := oci.BundleDigest(bundles["ledger"])
		if err != nil || bundles["ledger"].Contract.PactoVersion != contract.Current() {
			t.Fatalf("%v %s", err, bundles["ledger"].Contract.PactoVersion)
		}
		digests[d] = true
	}
	first := e.publish(t, "r1")
	e.emptyRegistry()
	second := e.publish(t, "r1")
	if len(digests) != 1 || first.digests["ledger bundle"] == "" || !maps.Equal(first.digests, second.digests) {
		t.Fatalf("plans %v, publishes\n%v\n%v\n%s", digests, first.digests, second.digests, first.text)
	}
}

// TestCIPublishSignsMigratedDigest: the tag and the signature are the
// migrated bundle's: the next run verifies that signature on that digest
// and finds the service unchanged.
func TestCIPublishSignsMigratedDigest(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	bundles := map[string]*contract.Bundle{}
	r := pubRow(t, e.ciPublishAt(t, "r1", bundles), "legacy")
	want, err := oci.BundleDigest(bundles["legacy"])
	if err != nil || r.State != ci.StatePublished || !strings.HasSuffix(r.Published, "@"+want) {
		t.Fatalf("%v row %+v want %s", err, r, want)
	}
	head, err := e.inner.Head(t.Context(), e.host+"/acme/legacy:1.0.0")
	if err != nil || head.Digest != want {
		t.Fatalf("tag %+v %v", head, err)
	}
	if again := pubRow(t, e.ciPublishAt(t, "r2", nil), "legacy"); again.State != ci.StateUnchanged || again.BaselineDigest != want {
		t.Fatalf("second run %+v", again)
	}
}

func TestCILockSeesMigratedDigest(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	r := pubRow(t, e.ciPublishAt(t, "r1", nil), "legacy")
	consumer := t.TempDir()
	writeFiles(t, consumer, map[string]string{"pacto.yaml": "pactoVersion: " + strconv.Quote(contract.Current()) +
		"\nservice: {name: shop, version: 1.0.0}\ndependencies:\n  - {name: legacy, ref: oci://" + e.host + "/acme/legacy, compatibility: ^1}\n"})
	e.svc.BundleStore = oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host))
	res, err := e.svc.Lock(t.Context(), LockOptions{Path: consumer})
	if err != nil || len(res.Lock.Dependencies) != 1 || !strings.HasSuffix(r.Published, "@"+res.Lock.Dependencies[0].Digest) {
		t.Fatalf("%v lock %+v published %s", err, res, r.Published)
	}
}

// TestCIMigrationAloneUnchanged: a baseline an earlier CLI published as
// written reads unchanged after the CLI starts migrating; nothing is pushed.
func TestCIMigrationAloneUnchanged(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	e.publishAs(t, "legacy", "r1")
	before := e.reg.writes.Load()
	p := e.ciPublish(t, nil)
	if r := pubRow(t, p, "legacy"); r.State != ci.StateUnchanged || r.Changed != "no" || e.reg.writes.Load() != before || p.Exit != 0 {
		t.Fatalf("row %+v writes %d", r, e.reg.writes.Load()-before)
	}
}

// TestCIMigratedBaselineUnchanged: a baseline this path published, migrated
// bytes and record, reads unchanged at another commit.
func TestCIMigratedBaselineUnchanged(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	if r := pubRow(t, e.ciPublishAt(t, "r1", nil), "legacy"); r.State != ci.StatePublished {
		t.Fatalf("first run %+v", r)
	}
	before := e.reg.writes.Load()
	if r := pubRow(t, e.ciPublishAt(t, "r2", nil), "legacy"); r.State != ci.StateUnchanged || r.Changed != "no" || e.reg.writes.Load() != before {
		t.Fatalf("row %+v writes %d", r, e.reg.writes.Load()-before)
	}
}

// TestCIChangedServicePublishesMigrated: a declared change publishes the
// migrated bytes; the version rule judges the interface the change adds,
// and the pactoVersion change adds no bump of its own.
func TestCIChangedServicePublishesMigrated(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	e.publishAs(t, "legacy", "r1")
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.1.0", "interfaces:\n  - {name: api, type: openapi, ref: openapi.yaml}\n", "")),
		"legacy/openapi.yaml": "openapi: 3.0.0\ninfo: {title: legacy, version: \"1\"}\npaths: {}\n"})
	r := pubRow(t, e.ciPublishAt(t, "r2", nil), "legacy")
	if r.State != ci.StatePublished || r.Baseline != "1.0.0" || r.To != "1.1.0" {
		t.Fatalf("row %+v", r)
	}
	if b, label := e.pushedBundle(t, r.Published); label != contract.Current() || b.Contract.PactoVersion != label {
		t.Fatalf("label %s, pacto.yaml %s", label, b.Contract.PactoVersion)
	}
}

func TestCIPlanModeMigrationNoWrites(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	e.publishAs(t, "legacy", "r1")
	e.write(t, map[string]string{"legacy/notes.md": "# legacy\n"})
	token, tokenHits := countingHost(t)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://"+token+"?x=1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "t")
	before := e.reg.writes.Load()
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme"})
	if err != nil || pubRow(t, p, "legacy").Changed != "yes" || e.reg.writes.Load() != before || tokenHits.Load() != 0 {
		t.Fatalf("%v rows %+v writes %d tokens %d", err, p.Rows, e.reg.writes.Load()-before, tokenHits.Load())
	}
}

// TestCIMigrationLeavesFolderUntouched: neither a plan nor a publish writes
// the folder it migrates.
func TestCIMigrationLeavesFolderUntouched(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", "")), "legacy/k8s/deploy.yaml": deployment("ghcr.io/acme/legacy:1")})
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	snapshot := func() map[string]string {
		out := map[string]string{}
		_ = filepath.WalkDir(e.root, func(p string, d fs.DirEntry, _ error) error {
			if info, err := d.Info(); err == nil && !d.IsDir() {
				data, _ := os.ReadFile(p)
				out[p] = info.ModTime().String() + " " + string(data)
			}
			return nil
		})
		return out
	}
	for _, f := range []string{"legacy/pacto.yaml", "legacy/k8s/deploy.yaml"} {
		if err := os.Chtimes(filepath.Join(e.root, f), old, old); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshot()
	if _, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme"}); err != nil {
		t.Fatal(err)
	}
	if r := pubRow(t, e.ciPublish(t, nil), "legacy"); r.State != ci.StatePublished {
		t.Fatalf("row %+v", r)
	}
	if after := snapshot(); !maps.Equal(before, after) {
		t.Fatalf("the folder changed:\n%v\n%v", before, after)
	}
}

// TestCIPublishUnchangedBehindWithoutSectionFails: a pre-2.5 contract with no
// publish section, pushed as written by an earlier CLI, reads unchanged
// against that baseline and goes back to its as-written bundle. Nothing
// verifies that baseline as the publisher's, so the row fails with the fix
// (raise pactoVersion, which derives a section) and never reads unchanged.
func TestCIPublishUnchangedBehindWithoutSectionFails(t *testing.T) {
	e := newCIPub(t)
	y := "pactoVersion: \"2.4\"\nservice:\n  name: legacy\n  version: 1.0.0\n"
	e.write(t, map[string]string{"legacy/pacto.yaml": y})
	c, _, err := contract.ParseDocument([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host)).Push(t.Context(), e.host+"/acme/legacy:1.0.0",
		&contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(y)}}}); err != nil {
		t.Fatal(err)
	}
	before := e.reg.writes.Load()
	p := e.ciPublish(t, nil)
	r := pubRow(t, p, "legacy")
	cur := contract.Current()
	if r.Changed != "no" || r.State != ci.StateFailed || p.Exit != 1 || e.reg.writes.Load() != before ||
		r.Reason != "declares no publish section; Pacto derives one from pactoVersion 2.5: set pactoVersion: "+strconv.Quote(cur) {
		t.Fatalf("row %+v exit %d writes %d", r, p.Exit, e.reg.writes.Load()-before)
	}
}
