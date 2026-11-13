package registry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

func record() runrecord.Record {
	return runrecord.Record{
		Version: runrecord.Version, ID: runrecord.ID(runrecord.KindDiff, "payments", "a", "b"),
		Kind: runrecord.KindDiff, Service: "payments", OldRevision: "a", NewRevision: "b",
		Classification: "BREAKING", Outcome: runrecord.OutcomeFailed, PactoVersion: "dev",
		RecordedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}
}

// serve starts the referrers registry, wrapped by wrap when set, and returns the
// stats repository path on it.
func serve(t *testing.T, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	h := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://") + "/acme/pacto-stats"
}

func TestPublishNativeReferrers(t *testing.T) {
	repo, err := Open(serve(t, nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	desc, err := Publish(ctx, repo, record())
	if err != nil {
		t.Fatal(err)
	}
	anchor := Anchor("payments").ManifestDesc
	if got, err := repo.Resolve(ctx, runrecord.AnchorTag("payments")); err != nil || got.Digest != anchor.Digest {
		t.Fatalf("anchor tag = %v %v", got, err)
	}
	var listed []ocispec.Descriptor
	if err := repo.Referrers(ctx, anchor, "", func(p []ocispec.Descriptor) error { listed = append(listed, p...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Digest != desc.Digest || listed[0].ArtifactType != runrecord.ArtifactType {
		t.Fatalf("referrers = %+v", listed)
	}
	if listed[0].Annotations[runrecord.AnnRecordedAt] != "2026-11-13T09:00:00Z" {
		t.Fatalf("annotations = %v", listed[0].Annotations)
	}
	if got := fetchRecord(t, repo, desc); got.ID != record().ID {
		t.Fatalf("layer = %+v", got)
	}
}

// fetchRecord reads the record a referrer manifest carries back.
func fetchRecord(t *testing.T, repo *remote.Repository, desc ocispec.Descriptor) runrecord.Record {
	t.Helper()
	manifest, err := content.FetchAll(t.Context(), repo, desc)
	if err != nil {
		t.Fatal(err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil || m.Layers[0].MediaType != runrecord.LayerType {
		t.Fatalf("manifest = %s %v", manifest, err)
	}
	layer, err := content.FetchAll(t.Context(), repo.Blobs(), m.Layers[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := runrecord.Parse(layer)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPublishIdempotent(t *testing.T) {
	repo, _ := Open(serve(t, nil), nil, nil)
	ctx := t.Context()
	first, err := Publish(ctx, repo, record())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Publish(ctx, repo, record())
	if err != nil || second.Digest != first.Digest {
		t.Fatalf("second publish = %v %v", second.Digest, err)
	}
	n := 0
	_ = repo.Referrers(ctx, Anchor("payments").ManifestDesc, "", func(p []ocispec.Descriptor) error { n += len(p); return nil })
	if n != 1 {
		t.Fatalf("%d referrers, want 1", n)
	}
}

func TestPublishDenied(t *testing.T) {
	path := serve(t, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"errors":[{"code":"DENIED"}]}`)
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	repo, _ := Open(path, nil, nil)
	if _, err := Publish(t.Context(), repo, record()); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishRefusesInvalid(t *testing.T) {
	var requests atomic.Int32
	path := serve(t, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); inner.ServeHTTP(w, r) })
	})
	repo, _ := Open(path, nil, nil)
	r := record()
	r.Kind = "validate"
	if _, err := Publish(t.Context(), repo, r); err == nil || requests.Load() != 0 {
		t.Fatalf("err = %v after %d requests", err, requests.Load())
	}
}

func TestOpenRejectsBadReference(t *testing.T) {
	if _, err := Open("not a reference", nil, nil); err == nil {
		t.Fatal("opened a bad reference")
	}
}

// failPut fails every PUT fail matches and serves the rest.
func failPut(fail func(r *http.Request, body []byte) bool) func(http.Handler) http.Handler {
	return func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
				if fail(r, body) {
					http.Error(w, "denied", http.StatusForbidden)
					return
				}
			}
			inner.ServeHTTP(w, r)
		})
	}
}

func TestPublishTagFails(t *testing.T) {
	path := serve(t, failPut(func(r *http.Request, _ []byte) bool {
		return strings.Contains(r.URL.Path, "/manifests/pacto-stats-")
	}))
	repo, _ := Open(path, nil, nil)
	if _, err := Publish(t.Context(), repo, record()); err == nil {
		t.Fatal("a failed tag was not an error")
	}
}

func TestPublishAnchorPushFails(t *testing.T) {
	anchor := Anchor("payments").ManifestDesc.Digest.String()
	path := serve(t, failPut(func(r *http.Request, _ []byte) bool { return strings.HasSuffix(r.URL.Path, "/manifests/"+anchor) }))
	repo, _ := Open(path, nil, nil)
	if _, err := Publish(t.Context(), repo, record()); err == nil {
		t.Fatal("a failed anchor push was not an error")
	}
}

func TestPublishRecordPushFails(t *testing.T) {
	path := serve(t, failPut(func(r *http.Request, body []byte) bool {
		return strings.Contains(r.URL.Path, "/manifests/") && bytes.Contains(body, []byte(`"subject"`))
	}))
	repo, _ := Open(path, nil, nil)
	if _, err := Publish(t.Context(), repo, record()); err == nil {
		t.Fatal("a failed record push was not an error")
	}
	if _, err := repo.Resolve(t.Context(), runrecord.AnchorTag("payments")); err != nil {
		t.Fatalf("the anchor was not tagged before the record push: %v", err)
	}
}

// TestBundleDigestPinned proves this story changed no packaging: the digest a
// push would publish a fixed bundle under is the one it was before run
// records existed.
func TestBundleDigestPinned(t *testing.T) {
	const want = "sha256:efaa2dd406380eca840b11c3fbe79d382b467a7cb200b6b81251b26baa453a31"
	got, err := oci.BundleDigest(testutil.TestBundle())
	if err != nil || got != want {
		t.Fatalf("BundleDigest = %s %v, want %s", got, err, want)
	}
}
