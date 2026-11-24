package orgindex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// indexWith is an index repository on an in-process registry holding entry
// e under its tag, and the tag.
func indexWith(t *testing.T, e Entry, payload []byte) (*remote.Repository, string, string) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	repo := host + "/acme/" + RepositoryName
	r, err := ocireferrer.Open(repo, ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if payload == nil {
		e.Bundle = host + "/acme/payments@sha256:" + strings.Repeat("a", 64)
		e.Service = string(fleet.NewServiceKeyDomain(contract.OCIDomain(host+"/acme/payments"), e.Name))
		payload, _ = Encode(e)
	}
	tag, _ := Tag(fleet.ServiceKey(e.Service), e.Version)
	if err := ocireferrer.PushWriteOnce(t.Context(), r, ocireferrer.Build(ArtifactType, MediaType, payload, nil, nil), tag); err != nil {
		t.Fatal(err)
	}
	return r, repo, tag
}

func TestReadEntryFetchesAndBinds(t *testing.T) {
	e := Entry{SchemaVersion: SchemaVersion, Name: "payments", Version: "1.5.0"}
	r, repo, tag := indexWith(t, e, nil)
	got, err := ReadEntry(t.Context(), r, repo, tag, nil)
	if err != nil || got.Name != "payments" || got.Version != "1.5.0" {
		t.Fatalf("ReadEntry = %+v, %v", got, err)
	}
	// Bound to where it was read: another index refuses it.
	if _, err := ReadEntry(t.Context(), r, repo+"x", tag, nil); Reason(err) == "" {
		t.Fatalf("another index: %v", err)
	}
	// A missing tag, an undecodable layer and a failing layer reader are errors.
	if _, err := ReadEntry(t.Context(), r, repo, "nope", nil); err == nil {
		t.Fatal("missing tag read")
	}
	boom := errors.New("boom")
	if _, err := ReadEntry(t.Context(), r, repo, tag, func(context.Context, ocispec.Descriptor) ([]byte, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("layer error: %v", err)
	}
	r2, repo2, tag2 := indexWith(t, Entry{Name: "x", Version: "1.0.0", Service: "x"}, []byte("{"))
	if _, err := ReadEntry(t.Context(), r2, repo2, tag2, nil); Reason(err) != "the entry does not decode" {
		t.Fatalf("undecodable: %v", err)
	}
}

func TestReadEntryRefusesWrongManifests(t *testing.T) {
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	failGet := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if failGet && req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/manifests/") {
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	repo := strings.TrimPrefix(srv.URL, "http://") + "/acme/" + RepositoryName
	r, _ := ocireferrer.Open(repo, ocireferrer.Options{})
	// Another artifact type is refused before its layer is fetched.
	if err := ocireferrer.PushWriteOnce(t.Context(), r, ocireferrer.Build("application/x-other", MediaType, []byte("{}"), nil, nil), "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEntry(t.Context(), r, repo, "other", nil); err == nil || !strings.Contains(err.Error(), "artifactType") {
		t.Fatalf("other artifact: %v", err)
	}
	// A manifest over its bound is refused before it is fetched.
	big := map[string]string{"x": strings.Repeat("a", MaxManifestBytes)}
	if err := ocireferrer.PushWriteOnce(t.Context(), r, ocireferrer.Build(ArtifactType, MediaType, []byte("{}"), nil, big), "big"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEntry(t.Context(), r, repo, "big", nil); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("big manifest: %v", err)
	}
	// A manifest whose layer the registry does not hold is an error.
	art := ocireferrer.Build(ArtifactType, MediaType, []byte("{}"), nil, nil)
	if err := r.Blobs().Push(t.Context(), art.ConfigDesc, bytes.NewReader(art.Config)); err != nil {
		t.Fatal(err)
	}
	if err := r.Manifests().PushReference(t.Context(), art.ManifestDesc, bytes.NewReader(art.Manifest), "nolayer"); err != nil {
		t.Skip("the registry refuses a manifest with a missing layer:", err)
	}
	if _, err := ReadEntry(t.Context(), r, repo, "nolayer", nil); err == nil {
		t.Fatal("missing layer read")
	}
	// A manifest that resolves but cannot be fetched is an error.
	failGet = true
	if _, err := ReadEntry(t.Context(), r, repo, "other", nil); err == nil || strings.Contains(err.Error(), "artifactType") {
		t.Fatalf("manifest fetch: %v", err)
	}
}

// fakeRepo resolves every reference to desc and serves body, or fails the
// read with err.
type fakeRepo struct {
	desc ocispec.Descriptor
	body string
	err  error
}

func (f fakeRepo) Resolve(context.Context, string) (ocispec.Descriptor, error) { return f.desc, nil }

func (f fakeRepo) Fetch(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
	return io.NopCloser(io.MultiReader(strings.NewReader(f.body), errReader{f.err})), nil
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	return 0, io.EOF
}

func TestReadEntryRefusesContentThatIsNotItsDescriptor(t *testing.T) {
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: godigest.FromString("x"), Size: 1}
	if _, err := ReadEntry(t.Context(), fakeRepo{desc: desc, body: "y"}, "r", "t", nil); err == nil || !strings.Contains(err.Error(), "not the 1 bytes") {
		t.Fatalf("other content: %v", err)
	}
	boom := errors.New("reset")
	if _, err := ReadEntry(t.Context(), fakeRepo{desc: desc, err: boom}, "r", "t", nil); !errors.Is(err, boom) {
		t.Fatalf("read error: %v", err)
	}
}
