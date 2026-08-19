package evidenceoci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
)

const testRepoPath = "team/contracts/orders"

// startRegistry runs a spec-faithful OCI 1.1 registry and returns its host:port.
// It is loopback, so the repository factory reaches it over plain HTTP exactly as
// it would a listed insecure registry.
func startRegistry(t *testing.T, opts testutil.ReferrersOptions) string {
	t.Helper()
	return serve(t, testutil.NewReferrersRegistry(opts))
}

// serve runs h on loopback and returns its host:port.
func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// seedContract pushes a contract revision into the registry and returns the
// subject naming it plus the descriptor referrers must be attached to.
func seedContract(t *testing.T, host string, opts RepositoryOptions) (Subject, ocispec.Descriptor) {
	t.Helper()
	return seedContractIn(t, host, testRepoPath, "orders", opts)
}

// seedContractIn is seedContract for a chosen repository and contract, so a test
// can configure a store with several subjects that are genuinely distinct
// revisions rather than the same one twice.
func seedContractIn(t *testing.T, host, repoPath, service string, opts RepositoryOptions) (Subject, ocispec.Descriptor) {
	t.Helper()
	repo := openRepoAt(t, host, repoPath, opts)
	config := []byte("{}")
	layer := []byte(`{"contract":"` + service + `"}`)
	pushBlob(t, repo, ocispec.MediaTypeEmptyJSON, config)
	pushBlob(t, repo, "application/vnd.pacto.contract.v1+json", layer)
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ocispec.Descriptor{MediaType: ocispec.MediaTypeEmptyJSON, Digest: digest.FromBytes(config), Size: int64(len(config))},
		Layers: []ocispec.Descriptor{{
			MediaType: "application/vnd.pacto.contract.v1+json",
			Digest:    digest.FromBytes(layer),
			Size:      int64(len(layer)),
		}},
	})
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	if err := repo.Push(t.Context(), desc, bytes.NewReader(manifest)); err != nil {
		t.Fatalf("push contract manifest: %v", err)
	}
	subj, err := ParseSubject(Scheme + host + "/" + repoPath + "@" + desc.Digest.String())
	if err != nil {
		t.Fatalf("ParseSubject: %v", err)
	}
	return subj, desc
}

func openRepo(t *testing.T, host string, opts RepositoryOptions) *remote.Repository {
	t.Helper()
	return openRepoAt(t, host, testRepoPath, opts)
}

func openRepoAt(t *testing.T, host, repoPath string, opts RepositoryOptions) *remote.Repository {
	t.Helper()
	repo, err := newRepository(Subject{Registry: host, Repository: repoPath}, opts)
	if err != nil {
		t.Fatalf("newRepository: %v", err)
	}
	return repo
}

func pushBlob(t *testing.T, repo *remote.Repository, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
	if err := repo.Push(t.Context(), desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("push %s: %v", mediaType, err)
	}
	return desc
}

// publish writes one built evidence artifact the way the store does — through
// the store's own push, so a test can never seed bytes the product cannot.
func publish(t *testing.T, repo *remote.Repository, art Artifact) {
	t.Helper()
	if err := pushArtifact(t.Context(), repo, art); err != nil {
		t.Fatalf("push evidence artifact: %v", err)
	}
}

// recordN is a distinct accepted record for the same subject: one producer, a
// strictly increasing sequence, a unique envelope id.
func recordN(s Subject, n int) evidenceingest.Record {
	rec := testRecord(s)
	rec.Envelope.ID = fmt.Sprintf("env-%d", n)
	rec.Envelope.Sequence = uint64(n)
	rec.Envelope.EvidenceSet.Subject.Name = fmt.Sprintf("orders-%d", n)
	return rec
}

// publishRecords seeds n evidence artifacts and returns the repository they live
// in, so a test can scan the same connection that wrote them.
func publishRecords(t *testing.T, host string, subj Subject, desc ocispec.Descriptor, n int, opts RepositoryOptions) *remote.Repository {
	t.Helper()
	repo := openRepo(t, host, opts)
	for i := 1; i <= n; i++ {
		art, err := BuildArtifact(recordN(subj, i), subj, desc)
		if err != nil {
			t.Fatalf("BuildArtifact: %v", err)
		}
		publish(t, repo, art)
	}
	return repo
}

// pushForeign attaches a non-Pacto artifact to the same contract revision, the
// way cosign or an SBOM tool would.
func pushForeign(t *testing.T, repo *remote.Repository, subjectDesc ocispec.Descriptor, artifactType string) {
	t.Helper()
	config := pushBlob(t, repo, ocispec.MediaTypeEmptyJSON, []byte("{}"))
	config.Data = []byte("{}")
	payload := pushBlob(t, repo, artifactType, []byte(`{"unrelated":true}`))
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       config,
		Layers:       []ocispec.Descriptor{payload},
		Subject:      &subjectDesc,
	})
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifest), Size: int64(len(manifest))}
	if err := repo.Push(t.Context(), desc, bytes.NewReader(manifest)); err != nil {
		t.Fatalf("push foreign manifest: %v", err)
	}
}

// pushMalformed attaches something that claims to be a Pacto evidence record but
// is not one.
func pushMalformed(t *testing.T, repo *remote.Repository, subjectDesc ocispec.Descriptor, payload []byte) ocispec.Descriptor {
	t.Helper()
	config := pushBlob(t, repo, ocispec.MediaTypeEmptyJSON, []byte("{}"))
	config.Data = []byte("{}")
	layer := pushBlob(t, repo, PayloadMediaType, payload)
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       config,
		Layers:       []ocispec.Descriptor{layer},
		Subject:      &subjectDesc,
	})
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifest), Size: int64(len(manifest))}
	if err := repo.Push(t.Context(), desc, bytes.NewReader(manifest)); err != nil {
		t.Fatalf("push malformed manifest: %v", err)
	}
	return desc
}

func mustScan(t *testing.T, repo *remote.Repository, subj Subject, desc ocispec.Descriptor) subjectScan {
	t.Helper()
	got, err := scanSubject(t.Context(), repo, subj, desc)
	if err != nil {
		t.Fatalf("scanSubject: %v", err)
	}
	return got
}

func TestScanSubject_ReadsEveryPublishedRecord(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, 3, RepositoryOptions{})

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 3 {
		t.Fatalf("read %d records, want 3", len(got.Found))
	}
	if got.Invalid != 0 {
		t.Errorf("invalid = %d, want 0", got.Invalid)
	}
	ids := map[string]bool{}
	for _, f := range got.Found {
		ids[f.Record.Envelope.ID] = true
		if f.Desc.Digest == "" {
			t.Error("a scanned record carries no manifest digest")
		}
	}
	for i := 1; i <= 3; i++ {
		if !ids[fmt.Sprintf("env-%d", i)] {
			t.Errorf("env-%d missing from the scan", i)
		}
	}
}

// Replay protection is only sound if the scan sees EVERY record. A registry that
// pages the referrers listing must be followed to the last page.
func TestScanSubject_FollowsEveryPage(t *testing.T) {
	const total = 7
	host := startRegistry(t, testutil.ReferrersOptions{PageSize: 2})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, total, RepositoryOptions{PageSize: 2})

	// Guard against a vacuous pass: the listing really is served in pages.
	resp, err := http.Get(fmt.Sprintf("http://%s/v2/%s/referrers/%s", host, testRepoPath, subj.Digest))
	if err != nil {
		t.Fatalf("GET referrers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Link") == "" {
		t.Fatal("the registry served every referrer in one page; pagination is not exercised")
	}

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != total {
		t.Fatalf("read %d records across pages, want %d", len(got.Found), total)
	}
}

// Another tool's artifact on the same contract revision is irrelevant, not
// malformed: it must neither appear as evidence nor degrade the read.
func TestScanSubject_IgnoresUnrelatedArtifacts(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, 1, RepositoryOptions{})
	pushForeign(t, repo, desc, "application/vnd.dev.cosign.simplesigning.v1+json")
	pushForeign(t, repo, desc, "application/spdx+json")

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 1 {
		t.Fatalf("read %d records, want only the Pacto one", len(got.Found))
	}
	if got.Invalid != 0 {
		t.Errorf("invalid = %d, want 0: a foreign artifact type is irrelevant, not corrupt", got.Invalid)
	}
}

// A registry that reports the config media type instead of the manifest's own
// artifactType is not conformant, but it must not make Pacto evidence invisible:
// invisible evidence would be served as an authoritative empty graph.
func TestScanSubject_ClassifiesByManifestNotListingDescriptor(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{LegacyArtifactType: true})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, 2, RepositoryOptions{})

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 2 {
		t.Fatalf("read %d records, want 2 even though the listing mislabels them", len(got.Found))
	}
}

// A Pacto-typed artifact that is not a Pacto record is counted, never silently
// dropped and never trusted.
func TestScanSubject_CountsMalformedPactoArtifacts(t *testing.T) {
	cases := map[string][]byte{
		"not json":       []byte("not json"),
		"trailing json":  []byte(`{"schemaVersion":"` + RecordSchemaVersion + `","record":{}}{}`),
		"unknown schema": []byte(`{"schemaVersion":"pacto.dev/evidence-record/v9","record":{}}`),
		"other subject":  []byte(`{"schemaVersion":"` + RecordSchemaVersion + `","record":{"envelope":{"id":"x"}}}`),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			host := startRegistry(t, testutil.ReferrersOptions{})
			subj, desc := seedContract(t, host, RepositoryOptions{})
			repo := publishRecords(t, host, subj, desc, 1, RepositoryOptions{})
			pushMalformed(t, repo, desc, payload)

			got := mustScan(t, repo, subj, desc)
			if len(got.Found) != 1 {
				t.Errorf("read %d records, want the 1 valid one", len(got.Found))
			}
			if got.Invalid != 1 {
				t.Errorf("invalid = %d, want 1", got.Invalid)
			}
		})
	}
}

// An artifact whose manifest is Pacto-typed but structurally wrong is malformed
// too, and is rejected before its payload is fetched.
func TestScanSubject_CountsMalformedPactoManifests(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, 1, RepositoryOptions{})

	config := pushBlob(t, repo, ocispec.MediaTypeEmptyJSON, []byte("{}"))
	config.Data = []byte("{}") // canonical, so the two layers below are what makes this malformed
	layer := pushBlob(t, repo, PayloadMediaType, []byte(`{"schemaVersion":"x"}`))
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       config,
		Layers:       []ocispec.Descriptor{layer, layer}, // two payload layers
		Subject:      &desc,
	})
	md := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifest), Size: int64(len(manifest))}
	if err := repo.Push(t.Context(), md, bytes.NewReader(manifest)); err != nil {
		t.Fatalf("push: %v", err)
	}

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 1 || got.Invalid != 1 {
		t.Fatalf("read %d records with %d invalid, want 1 and 1", len(got.Found), got.Invalid)
	}
}

// A registry without the native Referrers endpoint must fail the read. oras-go
// would otherwise fall back to the legacy referrers tag, which is a mutable
// pointer no producer signed.
func TestScanSubject_FailsClosedWithoutReferrersAPI(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})

	blind := startRegistry(t, testutil.ReferrersOptions{Unsupported: true})
	repo := openRepo(t, blind, RepositoryOptions{})
	if _, err := scanSubject(t.Context(), repo, subj, desc); err == nil {
		t.Fatal("scanning a registry with no Referrers API succeeded; it must fail closed")
	}
}

// An unreachable registry is unknown, not empty.
func TestScanSubject_FailsClosedWhenUnreachable(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := openRepo(t, "127.0.0.1:1", RepositoryOptions{})
	if _, err := scanSubject(context.Background(), repo, subj, desc); err == nil {
		t.Fatal("scanning an unreachable registry succeeded; it must fail closed")
	}
}

// pushOversized attaches a manifest deliberately larger than the reader's bound.
// A registry cannot be trusted to keep manifests small, so the bound is applied
// from the descriptor before any body is read.
func pushOversized(t *testing.T, repo *remote.Repository, subjectDesc ocispec.Descriptor, artifactType string) {
	t.Helper()
	config := pushBlob(t, repo, ocispec.MediaTypeEmptyJSON, []byte("{}"))
	config.Data = []byte("{}") // canonical, so the padding below is what makes this malformed
	layer := pushBlob(t, repo, PayloadMediaType, []byte("{}"))
	manifest, _ := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       config,
		Layers:       []ocispec.Descriptor{layer},
		Subject:      &subjectDesc,
		Annotations:  map[string]string{"pad": strings.Repeat("p", maxManifestBytes)},
	})
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(manifest), Size: int64(len(manifest))}
	if err := repo.Push(t.Context(), desc, bytes.NewReader(manifest)); err != nil {
		t.Fatalf("push oversized manifest: %v", err)
	}
}

// A manifest too large to be a Pacto record but claiming to be one is malformed:
// it is refused on the descriptor, before anything is downloaded.
func TestScanSubject_CountsOversizedPactoManifests(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, 1, RepositoryOptions{})
	pushOversized(t, repo, desc, ArtifactType)

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 1 || got.Invalid != 1 {
		t.Fatalf("read %d records with %d invalid, want 1 and 1", len(got.Found), got.Invalid)
	}
}

// The same bound must not turn somebody else's large artifact into a Pacto
// problem: an unrelated type is skipped whatever its size.
func TestScanSubject_IgnoresOversizedForeignManifests(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := publishRecords(t, host, subj, desc, 1, RepositoryOptions{})
	pushOversized(t, repo, desc, "application/spdx+json")

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 1 || got.Invalid != 0 {
		t.Fatalf("read %d records with %d invalid, want 1 and 0", len(got.Found), got.Invalid)
	}
}

// brokenRegistry serves 404 for any request naming a chosen digest, the way a
// garbage-collected or half-replicated registry does. The referrers listing still
// names the artifact, because the shim reads its inner registry directly.
type brokenRegistry struct {
	inner  http.Handler
	mu     sync.Mutex
	broken map[digest.Digest]bool
}

func (b *brokenRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	for d := range b.broken {
		if strings.Contains(r.URL.Path, d.String()) && !strings.Contains(r.URL.Path, "/referrers/") {
			b.mu.Unlock()
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
			return
		}
	}
	b.mu.Unlock()
	b.inner.ServeHTTP(w, r)
}

func (b *brokenRegistry) breakDigest(d digest.Digest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.broken[d] = true
}

// An artifact the listing promises but the registry then refuses to serve makes
// the read partial. It is never silently dropped, because a dropped record is
// indistinguishable from a record that was never written.
func TestScanSubject_CountsArtifactsTheRegistryCannotServe(t *testing.T) {
	broken := &brokenRegistry{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), broken: map[digest.Digest]bool{}}
	host := serve(t, broken)
	subj, desc := seedContract(t, host, RepositoryOptions{})
	repo := openRepo(t, host, RepositoryOptions{})

	var arts []Artifact
	for i := 1; i <= 3; i++ {
		art, err := BuildArtifact(recordN(subj, i), subj, desc)
		if err != nil {
			t.Fatalf("BuildArtifact: %v", err)
		}
		publish(t, repo, art)
		arts = append(arts, art)
	}
	broken.breakDigest(arts[0].ManifestDesc.Digest) // the manifest itself is gone
	broken.breakDigest(arts[1].PayloadDesc.Digest)  // the manifest survives, its payload does not

	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 1 || got.Invalid != 2 {
		t.Fatalf("read %d records with %d invalid, want 1 and 2", len(got.Found), got.Invalid)
	}
	if got.Found[0].Record.Envelope.ID != "env-3" {
		t.Errorf("surviving record is %q, want env-3", got.Found[0].Record.Envelope.ID)
	}
}
