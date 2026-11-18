package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// ciRepoEnv is a repository root, an in-memory registry and a Service that
// reads its baselines from it.
type ciRepoEnv struct {
	root, host string
	reg        publish.Registry
	svc        *Service
}

func newCIRepo(t *testing.T) *ciRepoEnv {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	host, reg := publishRegistry(t)
	return &ciRepoEnv{root: t.TempDir(), host: host, reg: reg, svc: &Service{sys: sysDeps{publishRegistry: func() publish.Registry { return reg }}}}
}

// write writes files below the root.
func (e *ciRepoEnv) write(t *testing.T, files map[string]string) {
	t.Helper()
	for rel, data := range files {
		writeCIFile(t, e.root, rel, data)
	}
}

// publish pushes the folder rel as it is now, the way publish packs it, to
// repo:tag (repo below the registry host); edit, when set, changes the
// bundle first.
func (e *ciRepoEnv) publish(t *testing.T, rel, repo, tag string, edit func(*contract.Bundle)) string {
	t.Helper()
	dir := filepath.Join(e.root, rel)
	b, err := e.svc.ciBundle(dir, e.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, err = publishable(b); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(b)
	}
	img, err := oci.BundleImage(b)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := oci.BundleDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := v1.NewHash(digest)
	ctx := context.Background()
	d, err := e.reg.Write(ctx, e.host+"/"+repo, digested{img, h})
	if err == nil {
		err = e.reg.Tag(ctx, e.host+"/"+repo, d, tag)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// digested is a bundle image whose streamed layer's digest is known.
type digested struct {
	v1.Image
	h v1.Hash
}

func (d digested) Digest() (v1.Hash, error) { return d.h, nil }

// plan plans the root against the registry.
func (e *ciRepoEnv) plan(t *testing.T) *ci.Plan {
	t.Helper()
	p, _, err := e.svc.CIPlan(context.Background(), CIOptions{Root: e.root, Registry: e.host + "/acme"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// row is the plan's row for dir.
func row(t *testing.T, p *ci.Plan, dir string) ci.Row {
	t.Helper()
	for _, r := range p.Rows {
		if r.Dir == dir {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", dir, p.Rows)
	return ci.Row{}
}

const reportsV1 = "service:\n  name: reports\n  version: 1.0.0\n"

func TestUnchangedIgnoresManifestLabelsAndPin(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/openapi.yaml": "openapi: 3.0.0\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n# a comment\n"})
	r := row(t, e.plan(t), "reports")
	if r.Changed != "no" || r.Impact.Text != "-" || r.Reason != "same content as 1.0.0" || r.Next != "" || r.Baseline != "1.0.0" {
		t.Fatalf("row = %+v", r)
	}
}

func TestUnchangedOnlyRecordDiffers(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/compose.yaml": "services:\n  reports:\n    image: ghcr.io/acme/reports:1.0\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", func(b *contract.Bundle) {
		rec, err := contract.ReadInferredRecord(b.FS)
		if err != nil || rec == nil {
			t.Fatalf("record %v %v", rec, err)
		}
		rec.Inputs.Discovery = "1"
		b.FS = contract.WithInferredRecord(b.FS, rec.Marshal())
	})
	r := row(t, e.plan(t), "reports")
	if r.Changed != "no" || !strings.Contains(r.Reason, "only the inferred record differs (discovery 1 -> 3)") || r.Inferred == 0 {
		t.Fatalf("row = %+v", r)
	}
}

func TestChangedFileContent(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/notes.txt": "a\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/notes.txt": "b\n", "reports/pacto.yaml": "service: {name: reports}\n"})
	r := row(t, e.plan(t), "reports")
	if r.Changed != "yes" || !strings.HasPrefix(r.Reason, "content sha256:") || !strings.Contains(r.Reason, " != baseline 1.0.0 sha256:") {
		t.Fatalf("row = %+v", r)
	}
	if r.Classification != "NON_BREAKING" || r.Next != "1.0.1" || !r.Derived || r.Impact.Text != "unverified (no consumer source)" {
		t.Fatalf("row = %+v", r)
	}
}

const ledgerDep = "dependencies:\n  - name: ledger\n    ref: oci://ghcr.io/acme/ledger\n    compatibility: \"^1.0.0\"\n"

func TestChangedContractField(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n" + ledgerDep})
	r := row(t, e.plan(t), "reports")
	if r.Changed != "yes" || r.Reason != "contract differs from 1.0.0 (dependencies)" {
		t.Fatalf("row = %+v", r)
	}
}

func TestChangedRecordSources(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "CODEOWNERS": "/reports/ @acme/reporting\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"CODEOWNERS": "# owners\n/reports/ @acme/reporting\n", "reports/pacto.yaml": "service: {name: reports}\n"})
	r := row(t, e.plan(t), "reports")
	if r.Changed != "yes" || r.Reason != "inferred inputs differ from 1.0.0 (sources)" || r.Classification != "NON_BREAKING" ||
		!strings.HasPrefix(r.Impact.Text, "unverified (inferred fields changed: ") {
		t.Fatalf("row = %+v", r)
	}
}

func TestChangedStaleRecord(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "reports/compose.yaml": "services:\n  reports:\n    image: ghcr.io/acme/reports:1.0\n"})
	e.publish(t, "reports", "acme/reports", "1.0.0", func(b *contract.Bundle) {
		b.FS = contract.WithInferredRecord(b.FS, []byte("not: a record\n"))
	})
	if r := row(t, e.plan(t), "reports"); r.Changed != "yes" || r.Reason != "the inferred record differs from 1.0.0" {
		t.Fatalf("row = %+v", r)
	}
}

func TestCIUndeclaredVersionNoSpuriousChange(t *testing.T) {
	base := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "reports", Version: "1.0.0"}}
	local := &contract.Contract{Service: contract.Service{Name: "reports"}}
	for _, c := range diff.Compare(context.Background(), base, normalised(local, "1.0.0"), nil, nil).Changes {
		if c.Path == "service.version" || c.Path == "pactoVersion" {
			t.Fatalf("spurious change %+v", c)
		}
	}
}

func TestCIFirstPublish(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1, "billing/pacto.yaml": "service: {name: billing}\n"})
	p := e.plan(t)
	if r := row(t, p, "reports"); r.Changed != "yes" || r.Baseline != "" || r.Next != "1.0.0" || r.Derived || r.Impact.Text != "first publish, clear" || r.Repository != e.host+"/acme/reports" {
		t.Fatalf("row = %+v", r)
	}
	if r := row(t, p, "billing"); r.Next != "1.0.0" || !r.Derived {
		t.Fatalf("undeclared first publish = %+v", r)
	}
	if p.Exit != 0 {
		t.Fatalf("exit = %d", p.Exit)
	}
}

func TestCIFirstPublishInferredNameUnverified(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {version: 1.0.0}\n"})
	r := row(t, e.plan(t), "reports")
	if r.Impact.Text != "unverified (first publish of "+e.host+"/acme/reports: name inferred; declare service.name)" {
		t.Fatalf("row = %+v", r)
	}
}

// fakeCIRegistry answers the reads a plan makes; nil funcs answer nothing.
type fakeCIRegistry struct {
	publish.Registry
	tags  func(string) ([]string, error)
	head  func(string) (*publish.Remote, error)
	pull  func(string) (*contract.Bundle, error)
	calls []string
}

func (f *fakeCIRegistry) Tags(_ context.Context, repo string) ([]string, error) {
	f.calls = append(f.calls, "tags "+repo)
	return f.tags(repo)
}

func (f *fakeCIRegistry) Head(_ context.Context, ref string) (*publish.Remote, error) {
	f.calls = append(f.calls, "head "+ref)
	return f.head(ref)
}

func (f *fakeCIRegistry) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	f.calls = append(f.calls, "pull "+ref)
	return f.pull(ref)
}

// fakePlan plans root with reg and the CI environment env.
func fakePlan(t *testing.T, root string, reg publish.Registry, env map[string]string) *ci.Plan {
	t.Helper()
	svc := &Service{sys: sysDeps{publishRegistry: func() publish.Registry { return reg }}}
	p, _, err := svc.CIPlan(context.Background(), CIOptions{Root: root, Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func noTags(string) ([]string, error) { return nil, nil }

func TestCIFirstPublishGuessedRegistryUnverified(t *testing.T) {
	root := t.TempDir()
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	reg := &fakeCIRegistry{tags: noTags}
	p := fakePlan(t, root, reg, map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY_OWNER": "Acme"})
	r := row(t, p, "reports")
	if r.Impact.Text != "unverified (first publish of ghcr.io/acme/reports: registry from GITHUB_REPOSITORY_OWNER; pass --registry)" {
		t.Fatalf("row = %+v", r)
	}
	// GitLab's CI_REGISTRY_IMAGE is declared by the platform.
	p = fakePlan(t, root, reg, map[string]string{"GITLAB_CI": "true", "CI_REGISTRY_IMAGE": "registry.gitlab.com/acme/repo"})
	if r := row(t, p, "reports"); r.Impact.Text != "first publish, clear" {
		t.Fatalf("gitlab row = %+v", r)
	}
}

// countingHost is a server that counts every request it gets.
func countingHost(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &n
}

func TestCIPublishBundleOutsideRegistryUnverified(t *testing.T) {
	e := newCIRepo(t)
	other, hits := countingHost(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1 + "publish:\n  bundle: oci://" + other + "/x\n"})
	r := row(t, e.plan(t), "reports")
	want := "unverified (publish.bundle names " + other + ", outside the registry " + e.host + "/acme; pass --registry " + other + " to trust it)"
	if r.Impact.Text != want || hits.Load() != 0 {
		t.Fatalf("row = %+v, %d requests", r, hits.Load())
	}
}

func TestCIPublishBundleOnRegistryIsRead(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1 + "publish:\n  bundle: oci://" + e.host + "/pacto/reports\n"})
	e.publish(t, "reports", "pacto/reports", "1.0.0", nil)
	if r := row(t, e.plan(t), "reports"); r.Repository != e.host+"/pacto/reports" || r.Changed != "no" {
		t.Fatalf("row = %+v", r)
	}
}

func TestCIImageRegistryNeverContacted(t *testing.T) {
	root := t.TempDir()
	img, hits := countingHost(t)
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	writeCIFile(t, root, "reports/compose.yaml", "services:\n  reports:\n    image: "+img+"/acme/reports:1.0\n")
	reg := &fakeCIRegistry{}
	p := fakePlan(t, root, reg, nil)
	if r := row(t, p, "reports"); r.Impact.Text != "unverified (registry inferred from images; pass --registry)" || hits.Load() != 0 || len(reg.calls) != 0 {
		t.Fatalf("row = %+v, %d requests, calls %v", r, hits.Load(), reg.calls)
	}
}

func TestCIPolicyRefOutsideRegistryNotFetched(t *testing.T) {
	e := newCIRepo(t)
	other, hits := countingHost(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1 + "policies:\n  - name: p\n    ref: oci://" + other + "/policy:1.0.0\n"})
	r := row(t, e.plan(t), "reports")
	if r.Impact.Text != "unverified (policy ref on "+other+" not fetched: outside the registry "+e.host+"/acme)" || hits.Load() != 0 {
		t.Fatalf("row = %+v, %d requests", r, hits.Load())
	}
	// A contract invalid on its own stays blocked.
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1 + "workload: nope\npolicies:\n  - ref: oci://" + other + "/policy:1.0.0\n"})
	if r := row(t, e.plan(t), "reports"); r.Impact.Gate != "blocked" || hits.Load() != 0 {
		t.Fatalf("row = %+v", r)
	}
}

func TestHostFence(t *testing.T) {
	inner := &bundleResolverAdapter{svc: &Service{}, base: "/repo"}
	f := &hostFence{inner: inner, host: "r.example"}
	if f.RootBase() != "/repo" {
		t.Fatal("RootBase is forwarded")
	}
	if _, err := f.ResolveBundle(context.Background(), "oci://other.example/p:1"); err == nil || len(f.refused) != 1 || f.refused[0] != "other.example" {
		t.Fatalf("err %v, refused %v", err, f.refused)
	}
	// The allowed host goes to the inner resolver (here without a store).
	if _, _, err := f.ResolveBundleFrom(context.Background(), "/repo", "oci://r.example/p:1"); err == nil || len(f.refused) != 1 {
		t.Fatalf("err %v, refused %v", err, f.refused)
	}
	if _, err := (&hostFence{inner: inner}).ResolveBundle(context.Background(), "oci://r.example/p:1"); err == nil {
		t.Fatal("no allowed host refuses every registry")
	}
}

func TestCIBaselineSelection(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.4.2}\n"})
	e.publish(t, "reports", "acme/reports", "1.4.2", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.5.0}\n"})
	d15 := e.publish(t, "reports", "acme/reports", "1.5.0", nil)
	e.publish(t, "reports", "acme/reports", "latest", nil)
	for _, tc := range []struct{ yaml, baseline string }{
		{"service: {name: reports, version: 1.5.0}\n", "1.5.0"},
		{"service: {name: reports, version: 1.6.0}\n", "1.5.0"},
		{"service: {name: reports}\n", "1.5.0"},
		{"service: {name: reports, version: 1.0.0}\n", "1.5.0"},
	} {
		e.write(t, map[string]string{"reports/pacto.yaml": tc.yaml})
		if r := row(t, e.plan(t), "reports"); r.Baseline != tc.baseline || r.BaselineDigest != d15 {
			t.Errorf("%q: row = %+v, want %s %s", tc.yaml, r, tc.baseline, d15)
		}
	}
}

// interfaceYAML is an OpenAPI interface the next version can drop: a breaking change.
const interfaceYAML = "interfaces:\n  - name: api\n    type: openapi\n    ref: openapi.yaml\n"

// openAPIFile is the file interfaceYAML names.
const openAPIFile = "openapi: 3.0.0\ninfo: {title: reports, version: \"1\"}\npaths: {}\n"

func TestCIBaselinesMatchPublish(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.5.0}\n" + interfaceYAML, "reports/openapi.yaml": openAPIFile})
	e.publish(t, "reports", "acme/reports", "1.5.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.6.0-rc.1}\n"})
	e.publish(t, "reports", "acme/reports", "1.6.0-rc.1", nil)
	// Against the candidate the fix is a patch; against the final release
	// it drops an interface, so publish refuses 1.6.0 and so does the plan.
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.6.0}\n" + ledgerDep})
	r := row(t, e.plan(t), "reports")
	if r.Baseline != "1.6.0-rc.1" || r.Impact.Text != "blocked (1.6.0 needs a major bump over 1.5.0: publish 2.0.0 or higher)" {
		t.Fatalf("declared row = %+v", r)
	}
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n" + ledgerDep})
	r = row(t, e.plan(t), "reports")
	if r.Baseline != "1.6.0-rc.1" || r.Next != "2.0.0" || !r.Derived {
		t.Fatalf("derived row = %+v", r)
	}
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 2.0.0}\n" + ledgerDep})
	if r = row(t, e.plan(t), "reports"); r.Next != "2.0.0" || r.Impact.Text != "unverified (no consumer source)" {
		t.Fatalf("accepted row = %+v", r)
	}
}

func TestCIVersionDerived(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.4.2}\n" + interfaceYAML, "reports/openapi.yaml": openAPIFile})
	e.publish(t, "reports", "acme/reports", "1.4.2", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n" + interfaceYAML + ledgerDep})
	if r := row(t, e.plan(t), "reports"); r.Next != "1.4.3" || !r.Derived || r.Classification != "NON_BREAKING" {
		t.Fatalf("additive row = %+v", r)
	}
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports}\n"})
	if r := row(t, e.plan(t), "reports"); r.Next != "2.0.0" || !r.Derived || r.Classification != "BREAKING" {
		t.Fatalf("breaking row = %+v", r)
	}
}

func TestCIVersionRefused(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.4.2}\n" + interfaceYAML, "reports/openapi.yaml": openAPIFile})
	e.publish(t, "reports", "acme/reports", "1.4.2", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.4.3}\n"})
	if r := row(t, e.plan(t), "reports"); r.Impact.Text != "blocked (1.4.3 needs a major bump over 1.4.2: publish 2.0.0 or higher)" {
		t.Fatalf("row = %+v", r)
	}
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.4.2}\n"})
	p := e.plan(t)
	if r := row(t, p, "reports"); r.Impact.Text != "blocked (1.4.2 is published with other content: publish a higher service.version)" || p.Exit != 1 {
		t.Fatalf("row = %+v, exit %d", r, p.Exit)
	}
}

func TestCICollisionBlocksBoth(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"services/pay/pacto.yaml": "service: {version: 1.0.0}\n", "apps/pay/pacto.yaml": "service: {version: 1.0.0}\n"})
	p := e.plan(t)
	if row(t, p, "apps/pay").Impact.Text != "blocked (name collision with services/pay)" || row(t, p, "services/pay").Impact.Text != "blocked (name collision with apps/pay)" {
		t.Fatalf("rows = %+v", p.Rows)
	}
	want := "error: apps/pay and services/pay both infer service.name 'pay' (directory pay). Declare service.name in one of them."
	if !slices.Contains(p.Notes, want) {
		t.Fatalf("notes = %q", p.Notes)
	}
}

func TestCIDeclaredDuplicateBlocks(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"services/pay/pacto.yaml": "service: {name: pay}\n", "apps/pay/pacto.yaml": "service: {name: pay}\n", "billing/pacto.yaml": "service: {name: billing}\n"})
	p := e.plan(t)
	for _, d := range []string{"apps/pay", "services/pay"} {
		if r := row(t, p, d); r.Impact.Text != "blocked (two folders declare service.name 'pay')" {
			t.Fatalf("%s = %+v", d, r)
		}
	}
	if r := row(t, p, "billing"); r.Impact.Gate == "blocked" {
		t.Fatalf("billing = %+v", r)
	}
}

func TestCIInvalidBlocks(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"bad/pacto.yaml": "service: {name: bad}\nworkload: nope\n", "reports/pacto.yaml": reportsV1})
	p := e.plan(t)
	if r := row(t, p, "bad"); r.Impact.Text != "blocked (invalid: 1 errors)" || len(r.Errors) != 1 {
		t.Fatalf("bad = %+v", r)
	}
	if r := row(t, p, "reports"); r.Impact.Text != "first publish, clear" {
		t.Fatalf("reports = %+v", r)
	}
}

func TestCINoRegistryUnverified(t *testing.T) {
	root := t.TempDir()
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	writeCIFile(t, root, "billing/pacto.yaml", "service: {name: billing}\n")
	p := fakePlan(t, root, &fakeCIRegistry{}, nil)
	for _, r := range p.Rows {
		if r.Impact.Text != "unverified (no registry)" {
			t.Fatalf("row = %+v", r)
		}
	}
	if p.Exit != 2 {
		t.Fatalf("exit = %d", p.Exit)
	}
}

func TestCIBaselineUnreadableUnverified(t *testing.T) {
	root := t.TempDir()
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	env := map[string]string{"GITLAB_CI": "true", "CI_REGISTRY_IMAGE": "r.example/acme"}
	boom := errors.New("boom")
	tags := func(string) ([]string, error) { return []string{"1.0.0"}, nil }
	for _, tc := range []struct {
		name string
		reg  *fakeCIRegistry
		want string
	}{
		{"tags", &fakeCIRegistry{tags: func(string) ([]string, error) { return nil, boom }}, "unverified (baseline unreadable: boom)"},
		{"head", &fakeCIRegistry{tags: tags, head: func(string) (*publish.Remote, error) { return nil, boom }}, "unverified (baseline unreadable: boom)"},
		{"head absent", &fakeCIRegistry{tags: tags, head: func(string) (*publish.Remote, error) { return nil, nil }}, "unverified (baseline unreadable: r.example/acme/reports:1.0.0 is listed but not found)"},
		{"pull", &fakeCIRegistry{tags: tags, head: func(string) (*publish.Remote, error) { return &publish.Remote{Digest: "sha256:aa"}, nil },
			pull: func(string) (*contract.Bundle, error) { return nil, boom }}, "unverified (baseline unreadable: boom)"},
	} {
		if r := row(t, fakePlan(t, root, tc.reg, env), "reports"); r.Impact.Text != tc.want {
			t.Errorf("%s: row = %+v", tc.name, r)
		}
	}
}

func TestCIBaseUnreadableInVersionCheck(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.5.0}\n"})
	e.publish(t, "reports", "acme/reports", "1.5.0", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.6.0-rc.1}\n"})
	e.publish(t, "reports", "acme/reports", "1.6.0-rc.1", nil)
	e.write(t, map[string]string{"reports/pacto.yaml": "service: {name: reports, version: 1.6.0}\n"})
	// The final release cannot be read: the version rule cannot be judged.
	reg := &fakeCIRegistry{
		tags: func(string) ([]string, error) { return e.reg.Tags(context.Background(), e.host+"/acme/reports") },
		head: func(ref string) (*publish.Remote, error) {
			if strings.HasSuffix(ref, ":1.5.0") {
				return nil, errors.New("gone")
			}
			return e.reg.Head(context.Background(), ref)
		},
		pull: func(ref string) (*contract.Bundle, error) { return e.reg.Pull(context.Background(), ref) },
	}
	e.svc.sys.publishRegistry = func() publish.Registry { return reg }
	if r := row(t, e.plan(t), "reports"); r.Impact.Text != "unverified (baseline unreadable: gone)" || r.Baseline != "1.6.0-rc.1" {
		t.Fatalf("row = %+v", r)
	}
}

func TestCIInferredCount(t *testing.T) {
	e := newCIRepo(t)
	e.write(t, map[string]string{
		"reports/pacto.yaml":   reportsV1,
		"reports/compose.yaml": "services:\n  reports:\n    image: " + e.host + "/acme/reports:1.0\n    ports: [\"8080:8080\"]\n",
		"plain/pacto.yaml":     "service: {name: plain}\n",
	})
	p := e.plan(t)
	if row(t, p, "reports").Inferred == 0 || row(t, p, "plain").Inferred != 0 {
		t.Fatalf("rows = %+v", p.Rows)
	}
}

func TestCIExternalCatalogRefMalformed(t *testing.T) {
	root := t.TempDir()
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	if _, _, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: root, Fleet: FleetOptions{ExternalCatalogRef: "not a ref"}}); err == nil {
		t.Fatal("a malformed catalog reference must fail the run")
	}
}

func TestShortDigest(t *testing.T) {
	if shortDigest("") != "" || shortDigest("sha256:9a1f77") != "sha256:9a1f…" {
		t.Fatal(shortDigest("sha256:9a1f77"))
	}
}

func TestCIExternalCatalogLimitationNoted(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, "apiVersion: v0\n")
	root := t.TempDir()
	writeCIFile(t, root, "reports/pacto.yaml", reportsV1)
	p, _, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: root, Fleet: FleetOptions{ExternalCatalogRef: ref}})
	if err != nil || len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "is not a valid catalog") {
		t.Fatalf("err %v, notes %q", err, p.Notes)
	}
}
