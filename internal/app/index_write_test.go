package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// indexReg is an in-memory registry that records every request and lets a
// test answer some itself.
type indexReg struct {
	host string
	mu   sync.Mutex
	reqs []string
	// hook answers a request when it returns true.
	hook func(w http.ResponseWriter, r *http.Request) bool
}

func newIndexReg(t *testing.T) *indexReg {
	t.Helper()
	ir := &indexReg{}
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	freshProbes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ir.mu.Lock()
		ir.reqs = append(ir.reqs, r.Method+" "+r.URL.Path)
		hook := ir.hook
		ir.mu.Unlock()
		switch {
		case hook != nil && hook(w, r):
		case r.Method == http.MethodGet && r.URL.Path == "/v2/_catalog":
			testutil.ServeCatalog(w, r, testutil.CatalogOf(inner))
		default:
			inner.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	ir.host = strings.TrimPrefix(srv.URL, "http://")
	return ir
}

// count is how many requests matched method and a path containing part.
func (ir *indexReg) count(method, part string) int {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	n := 0
	for _, r := range ir.reqs {
		if strings.HasPrefix(r, method+" ") && strings.Contains(r, part) {
			n++
		}
	}
	return n
}

// anonRepo opens repositories as the CLI does, with no credentials.
func anonRepo(path string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Keychain: anonymous})
}

// indexPublishService publishes to ir with every index read and write
// opened anonymously.
func indexPublishService(ir *indexReg) *Service {
	svc := publishService(&fakeRun{}, publish.NewClient(anonymous, ir.host))
	svc.sys.refusalRepo = anonRepo
	return svc
}

// readEntry reads the index entry under tag in repo.
func readEntry(t *testing.T, repo, tag string) orgindex.Entry {
	t.Helper()
	r, _ := anonRepo(repo)
	desc, raw, err := fetchTagged(t.Context(), r, tag)
	if err != nil {
		t.Fatalf("reading %s:%s: %v", repo, tag, err)
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m)
	if desc.ArtifactType != orgindex.ArtifactType || len(m.Layers) != 1 || m.Layers[0].MediaType != orgindex.MediaType {
		t.Fatalf("entry manifest: %+v %s", desc, raw)
	}
	layer, err := content.FetchAll(t.Context(), r, m.Layers[0])
	if err != nil {
		t.Fatal(err)
	}
	var e orgindex.Entry
	if err := json.Unmarshal(layer, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// fetchTagged resolves tag and reads its manifest, with its artifactType.
func fetchTagged(ctx context.Context, r *remote.Repository, tag string) (ocispec.Descriptor, []byte, error) {
	desc, err := r.Resolve(ctx, tag)
	if err != nil {
		return desc, nil, err
	}
	raw, err := content.FetchAll(ctx, r, desc)
	if err == nil {
		var m ocispec.Manifest
		_ = json.Unmarshal(raw, &m)
		desc.ArtifactType = m.ArtifactType
	}
	return desc, raw, err
}

const indexDeps = "dependencies: [{name: ledger, ref: \"oci://ghcr.io/acme/contracts/ledger:2.0.0\", required: true, compatibility: ^2.0.0}]\n"

// publishedIndex publishes the fixture bundle to ir and returns its result,
// with the index repository and tag the entry must be under.
func publishedIndex(t *testing.T, ir *indexReg, svc *Service, opts PublishOptions) (res *publish.Result, repo, tag string, err error) {
	t.Helper()
	dir := writePublishBundle(t, ir.host, indexDeps)
	signingKey(t, dir)
	opts.Path, opts.Revision = dir, "r1"
	res, err = svc.Publish(t.Context(), opts)
	tag, _ = orgindex.Tag(fleet.NewServiceKeyDomain(ir.host+"/pacto", "payments"), "1.0.0")
	return res, ir.host + "/pacto/pacto-index", tag, err
}

func TestPublishWritesIndexEntry(t *testing.T) {
	ir := newIndexReg(t)
	res, repo, tag, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{})
	if err != nil || res.Index != "indexed as "+repo+":"+tag {
		t.Fatalf("publish: %v %q", err, res.Index)
	}
	e := readEntry(t, repo, tag)
	want := orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(fleet.NewServiceKeyDomain(ir.host+"/pacto", "payments")), Name: "payments", Version: "1.0.0",
		Bundle: ir.host + "/pacto/payments@" + res.Digests[publish.BundleItem]}
	if e.Service != want.Service || e.Name != want.Name || e.Version != want.Version || e.Bundle != want.Bundle || e.SchemaVersion != want.SchemaVersion ||
		len(e.Dependencies) != 1 || e.Dependencies[0] != (orgindex.Dependency{Name: "ledger", Ref: "oci://ghcr.io/acme/contracts/ledger:2.0.0", Compatibility: "^2.0.0"}) {
		t.Fatalf("entry %+v", e)
	}
	if err := e.Validate(repo, tag); err != nil {
		t.Fatal(err)
	}
}

func TestPublishIndexEntryCarriesPublisherAndMode(t *testing.T) {
	ir := newIndexReg(t)
	dir := writePublishBundle(t, ir.host, "")
	pub := signingKey(t, dir)
	res, err := indexPublishService(ir).Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(ir.host+"/pacto", "payments"), "1.0.0")
	e := readEntry(t, ir.host+"/pacto/pacto-index", tag)
	r, _ := anonRepo(ir.host + "/pacto/payments")
	mode := "unknown"
	if caps, err := ocireferrer.Probes.Probe(t.Context(), r, res.Digests[publish.BundleItem], false); err == nil {
		mode = caps.Mode()
	}
	if e.Publisher != (orgindex.Publisher{Key: signing.Hint(pub)}) || e.RegistryMode != mode || mode == "" {
		t.Fatalf("publisher %+v mode %q, want key %s mode %q", e.Publisher, e.RegistryMode, signing.Hint(pub), mode)
	}
}

func TestPublishIndexEntryKeylessFromToken(t *testing.T) {
	privateSigstore(t, ciWorkflow)
	ir := newIndexReg(t)
	pinned := ir.host + "/acme/payments@" + digest64
	dir := sectionless(t, "1.0.0", "", map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment(pinned)})
	svc := indexPublishService(ir).withRun(&buildRun{})
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1", Registry: ir.host + "/acme"})
	if err != nil || !strings.HasPrefix(res.Index, "indexed as "+ir.host+"/acme/pacto-index:") {
		t.Fatalf("keyless derived publish: %v %+v", err, res)
	}
	tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(ir.host+"/acme/contracts", "payments"), "1.0.0")
	if e := readEntry(t, ir.host+"/acme/pacto-index", tag); e.Publisher != (orgindex.Publisher{Identity: ciIdentity, Issuer: ghIssuer}) {
		t.Fatalf("publisher %+v", e.Publisher)
	}
}

// deniedWrites answers every write to the index with a 403 whose message
// carries a digest.
func deniedWrites(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || !strings.Contains(r.URL.Path, "/pacto-index/") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, `{"errors":[{"code":"DENIED","message":"writing sha256:`+strings.Repeat("b", 64)+` is denied"}]}`)
	return true
}

func TestPublishIndexWriteFailureWarnsAndSucceeds(t *testing.T) {
	ir := newIndexReg(t)
	ir.hook = deniedWrites
	res, repo, _, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{})
	if err != nil || !strings.HasPrefix(res.Index, "not indexed in "+repo+": ") || len(res.Actions) == 0 {
		t.Fatalf("a failed index write: %v %q", err, res.Index)
	}
}

func TestIndexTextNeverCarriesDigest(t *testing.T) {
	ir := newIndexReg(t)
	ir.hook = deniedWrites
	res, _, _, _ := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{})
	if regexp.MustCompile(`[0-9a-f]{64}`).MatchString(res.Index) || !strings.Contains(res.Index, "sha256:…") {
		t.Fatalf("index text %q", res.Index)
	}
}

func TestPublishIndexEntryNeverOverwritten(t *testing.T) {
	ir := newIndexReg(t)
	repo := ir.host + "/pacto/pacto-index"
	tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(ir.host+"/pacto", "payments"), "1.0.0")
	r, _ := anonRepo(repo)
	other := ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, []byte(`{"other":true}`), nil, nil)
	if err := ocireferrer.PushWriteOnce(t.Context(), r, other, tag); err != nil {
		t.Fatal(err)
	}
	res, _, _, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{})
	if err != nil || res.Index != "not indexed in "+repo+": "+repo+":"+tag+" names another entry; index entries are never overwritten" {
		t.Fatalf("taken tag: %v %q", err, res.Index)
	}
	if desc, _ := r.Resolve(t.Context(), tag); desc.Digest != other.ManifestDesc.Digest {
		t.Fatalf("the entry moved to %s", desc.Digest)
	}
}

func TestPublishRerunKeepsTheEntry(t *testing.T) {
	ir := newIndexReg(t)
	svc := indexPublishService(ir)
	dir := writePublishBundle(t, ir.host, "")
	signingKey(t, dir)
	first, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil || again.Index != first.Index || len(again.Actions) != 0 || ir.count("PUT", "/pacto-index/manifests/") != 1 {
		t.Fatalf("re-run: %v %q %q puts %d", err, first.Index, again.Index, ir.count("PUT", "/pacto-index/manifests/"))
	}
}

func TestPublishPlanWritesNoIndexEntry(t *testing.T) {
	ir := newIndexReg(t)
	res, _, _, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{Plan: true})
	if err != nil || res.Index != "" || ir.count("", "pacto-index") != 0 {
		t.Fatalf("plan: %v %q requests %d", err, res.Index, ir.count("", "pacto-index"))
	}
}

func TestPublishRefusedWritesNoIndexEntry(t *testing.T) {
	ir := newIndexReg(t)
	svc := indexPublishService(ir)
	svc.sys.now = func() time.Time { return time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC) }
	dir := writePublishBundle(t, ir.host, "workload: service\n")
	signingKey(t, dir)
	if _, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	before := ir.count("", "pacto-index")
	bumpVersion(t, dir, "1.0.1", "job")
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2"})
	if !errors.Is(err, publish.ErrNotReady) || res.Index != "" || ir.count("", "pacto-index") != before {
		t.Fatalf("refused: %v %q", err, res.Index)
	}
}

func TestPublishIndexNone(t *testing.T) {
	ir := newIndexReg(t)
	res, _, _, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{Index: IndexNone})
	if err != nil || res.Index != "" || ir.count("", "pacto-index") != 0 {
		t.Fatalf("--index none: %v %q", err, res.Index)
	}
}

// seedIndex writes one tag into repo so it exists.
func seedIndex(t *testing.T, repo string) {
	t.Helper()
	r, _ := anonRepo(repo)
	if err := ocireferrer.PushWriteOnce(t.Context(), r, ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, []byte(`{}`), nil, nil), "seed"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexRepositoryPrefersFirstRoot(t *testing.T) {
	ir := newIndexReg(t)
	seedIndex(t, ir.host+"/acme/pacto-index")
	seedIndex(t, ir.host+"/pacto-index")
	svc := &Service{sys: sysDeps{refusalRepo: anonRepo}}
	repo, note, _, err := svc.indexRepository(t.Context(), indexCandidates("", ir.host+"/acme/contracts/ledger"))
	if err != nil || repo != ir.host+"/acme/pacto-index" || note != "" {
		t.Fatalf("%q %q %v", repo, note, err)
	}
}

func TestIndexRepositoryFallsThroughOnNotFound(t *testing.T) {
	ir := newIndexReg(t)
	svc := &Service{sys: sysDeps{refusalRepo: anonRepo}}
	cands := indexCandidates("", ir.host+"/acme/contracts/ledger")
	repo, _, _, err := svc.indexRepository(t.Context(), cands)
	if !errors.Is(err, errIndexNotFound) || repo != ir.host+"/acme/pacto-index" {
		t.Fatalf("every candidate absent: %q %v", repo, err)
	}
	seedIndex(t, ir.host+"/pacto-index")
	repo, note, _, err := svc.indexRepository(t.Context(), cands)
	if err != nil || repo != ir.host+"/pacto-index" || note != "after "+ir.host+"/acme/pacto-index: not found" {
		t.Fatalf("fallback: %q %q %v", repo, note, err)
	}
}

func TestIndexRepositoryStopsOnAuthFailure(t *testing.T) {
	ir := newIndexReg(t)
	seedIndex(t, ir.host+"/pacto-index")
	ir.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/acme/pacto-index/") {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`)
		return true
	}
	svc := &Service{sys: sysDeps{refusalRepo: anonRepo}}
	repo, _, _, err := svc.indexRepository(t.Context(), indexCandidates("", ir.host+"/acme/contracts/ledger"))
	if err == nil || errors.Is(err, errIndexNotFound) || repo != ir.host+"/acme/pacto-index" {
		t.Fatalf("a denied candidate fell through: %q %v", repo, err)
	}
	// A candidate that cannot be opened stops too.
	if _, _, _, err := svc.indexRepository(t.Context(), []string{"Bad Host/pacto-index"}); err == nil || errors.Is(err, errIndexNotFound) {
		t.Fatalf("unopenable: %v", err)
	}
}

func TestIndexWriteRefusesBundleOutsideIndexRoot(t *testing.T) {
	ir, elsewhere := newIndexReg(t), newIndexReg(t)
	res, _, _, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{Index: "oci://" + elsewhere.host + "/pacto-index"})
	if err != nil || !strings.HasPrefix(res.Index, "not indexed in "+elsewhere.host+"/pacto-index: ") || !strings.Contains(res.Index, "not the index host") ||
		elsewhere.count("PUT", "") != 0 || elsewhere.count("POST", "") != 0 || ir.count("", "pacto-index") != 0 {
		t.Fatalf("foreign --index: %v %q", err, res.Index)
	}
}

// TestKeepIndexEntryRefusals: what keepIndexEntry says when it cannot write.
func TestKeepIndexEntryRefusals(t *testing.T) {
	ir := newIndexReg(t)
	svc := &Service{sys: sysDeps{refusalRepo: anonRepo}}
	b := &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0"}}}
	repo := ir.host + "/pacto/payments"
	for ref, want := range map[string]string{
		repo + ":latest":      "not indexed: pushed as latest; the index names versions",
		repo + "@" + digest64: "not indexed: pushed as a digest; the index names versions",
	} {
		if got := svc.keepIndexEntry(t.Context(), b, ref, digest64, orgindex.Publisher{}, ""); got != want {
			t.Errorf("%s: %q", ref, got)
		}
	}
	long := "1.0.0-" + strings.Repeat("x", 115)
	b.Contract.Service.Version = long
	if got := svc.keepIndexEntry(t.Context(), b, repo+":"+long, digest64, orgindex.Publisher{}, ""); !strings.Contains(got, "over the 128 OCI allows") {
		t.Errorf("overlong: %q", got)
	}
	b.Contract.Service.Version = "1.0.0"
	b.Contract.Dependencies = []contract.Dependency{{Name: strings.Repeat("x", orgindex.MaxEntryBytes)}}
	if got := svc.keepIndexEntry(t.Context(), b, repo+":1.0.0", digest64, orgindex.Publisher{}, ""); !strings.Contains(got, "over the 65536-byte bound") {
		t.Errorf("oversized: %q", got)
	}
	// The index repository opens for the lookup but not for the write.
	b.Contract.Dependencies = nil
	opened := 0
	svc.sys.refusalRepo = func(path string) (*remote.Repository, error) {
		if strings.HasSuffix(path, "/pacto/pacto-index") {
			if opened++; opened > 1 {
				return nil, errors.New("gone")
			}
		}
		return anonRepo(path)
	}
	if got := svc.keepIndexEntry(t.Context(), b, repo+":1.0.0", digest64, orgindex.Publisher{}, ""); got != "not indexed in "+ir.host+"/pacto/pacto-index: gone" {
		t.Errorf("unopened: %q", got)
	}
	// A lookup that stops writes nothing and says where it stopped.
	svc.sys.refusalRepo = func(path string) (*remote.Repository, error) {
		if strings.HasSuffix(path, "/pacto-index") {
			return nil, errors.New("denied")
		}
		return anonRepo(path)
	}
	if got := svc.keepIndexEntry(t.Context(), b, repo+":1.0.0", digest64, orgindex.Publisher{}, ""); got != "not indexed in "+ir.host+"/pacto/pacto-index: denied" {
		t.Errorf("stopped lookup: %q", got)
	}
}

func TestRegistryModeUnknown(t *testing.T) {
	ir := newIndexReg(t)
	svc := &Service{sys: sysDeps{refusalRepo: func(string) (*remote.Repository, error) { return nil, errors.New("no") }}}
	if got := svc.registryMode(t.Context(), ir.host+"/pacto/payments", digest64); got != "unknown" {
		t.Fatalf("unopened: %q", got)
	}
	svc.sys.refusalRepo = anonRepo
	if got := svc.registryMode(t.Context(), ir.host+"/pacto/payments", digest64); got != "unknown" {
		t.Fatalf("a subject the registry lacks: %q", got)
	}
}

// pushIndexService pushes bundles to ir and opens every index read and
// write anonymously.
func pushIndexService(ir *indexReg) *Service {
	svc := NewService(oci.NewClient(anonymous, oci.WithInsecureRegistries(ir.host)), nil)
	svc.sys.refusalRepo = anonRepo
	return svc
}

// pushTag is the index tag of test-svc 1.0.0 pushed under host/acme.
func pushTag(ir *indexReg) string {
	tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(ir.host+"/acme", "test-svc"), "1.0.0")
	return tag
}

func TestPushWritesIndexEntry(t *testing.T) {
	ir := newIndexReg(t)
	res, err := pushIndexService(ir).Push(t.Context(), PushOptions{Ref: "oci://" + ir.host + "/acme/svc", Path: writeTestBundle(t)})
	repo := ir.host + "/acme/pacto-index"
	if err != nil || res.Index != "indexed as "+repo+":"+pushTag(ir) {
		t.Fatalf("push: %v %+v", err, res)
	}
	e := readEntry(t, repo, pushTag(ir))
	if e.Publisher != (orgindex.Publisher{}) || e.Bundle != ir.host+"/acme/svc@"+res.Digest || e.Version != "1.0.0" {
		t.Fatalf("entry %+v", e)
	}
}

func TestPushAlreadyPublishedBackfillsEntry(t *testing.T) {
	ir := newIndexReg(t)
	svc, dir := pushIndexService(ir), writeTestBundle(t)
	ref := "oci://" + ir.host + "/acme/svc"
	if res, err := svc.Push(t.Context(), PushOptions{Ref: ref, Path: dir, Index: IndexNone}); err != nil || res.Index != "" {
		t.Fatalf("first push: %v %+v", err, res)
	}
	res, err := svc.Push(t.Context(), PushOptions{Ref: ref, Path: dir})
	if err != nil || !res.Unchanged || res.Index != "indexed as "+ir.host+"/acme/pacto-index:"+pushTag(ir) {
		t.Fatalf("backfill: %v %+v", err, res)
	}
	if e := readEntry(t, ir.host+"/acme/pacto-index", pushTag(ir)); e.Bundle != ir.host+"/acme/svc@"+res.Digest {
		t.Fatalf("entry %+v", e)
	}
}

func TestPushForceKeepsTheOldEntry(t *testing.T) {
	ir := newIndexReg(t)
	svc, dir := pushIndexService(ir), writeTestBundle(t)
	ref := "oci://" + ir.host + "/acme/svc"
	first, err := svc.Push(t.Context(), PushOptions{Ref: ref, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	forced, err := svc.Push(t.Context(), PushOptions{Ref: ref, Path: dir, Force: true})
	repo := ir.host + "/acme/pacto-index"
	if err != nil || forced.Digest == first.Digest || !strings.HasSuffix(forced.Index, "names another entry; index entries are never overwritten") {
		t.Fatalf("forced push: %v %+v", err, forced)
	}
	if e := readEntry(t, repo, pushTag(ir)); e.Bundle != ir.host+"/acme/svc@"+first.Digest {
		t.Fatalf("the entry moved: %+v", e)
	}
}

func TestPushLatestTagWritesNoEntry(t *testing.T) {
	ir := newIndexReg(t)
	res, err := pushIndexService(ir).Push(t.Context(), PushOptions{Ref: "oci://" + ir.host + "/acme/svc:latest", Path: writeTestBundle(t)})
	if err != nil || res.Index != "not indexed: pushed as latest; the index names versions" || ir.count("", "pacto-index") != 0 {
		t.Fatalf("latest: %v %+v requests %d", err, res, ir.count("", "pacto-index"))
	}
}

func TestPushDigestRefWritesNoEntry(t *testing.T) {
	store := &mockBundleStore{ResolveFn: func(context.Context, string) (string, error) { return "", &oci.ArtifactNotFoundError{} }}
	res, err := offlineService(store).Push(t.Context(), PushOptions{Ref: "oci://ghcr.io/acme/svc@" + digest64, Path: writeTestBundle(t)})
	if err != nil || res.Index != "not indexed: pushed as a digest; the index names versions" {
		t.Fatalf("digest ref: %v %+v", err, res)
	}
}

func TestIndexFlagOverridesRepository(t *testing.T) {
	ir := newIndexReg(t)
	repo := ir.host + "/pacto-index"
	pushed, err := pushIndexService(ir).Push(t.Context(), PushOptions{Ref: "oci://" + ir.host + "/acme/svc", Path: writeTestBundle(t), Index: "oci://" + repo})
	if err != nil || pushed.Index != "indexed as "+repo+":"+pushTag(ir) {
		t.Fatalf("push --index: %v %+v", err, pushed)
	}
	published, _, tag, err := publishedIndex(t, ir, indexPublishService(ir), PublishOptions{Index: repo})
	if err != nil || published.Index != "indexed as "+repo+":"+tag || ir.count("", "/acme/pacto-index/") != 0 || ir.count("", "/pacto/pacto-index/") != 0 {
		t.Fatalf("publish --index: %v %q", err, published.Index)
	}
}

func TestIndexFlagNoneWritesNothing(t *testing.T) {
	ir := newIndexReg(t)
	res, err := pushIndexService(ir).Push(t.Context(), PushOptions{Ref: "oci://" + ir.host + "/acme/svc", Path: writeTestBundle(t), Index: IndexNone})
	if err != nil || res.Index != "" || ir.count("", "pacto-index") != 0 {
		t.Fatalf("--index none: %v %+v", err, res)
	}
}
