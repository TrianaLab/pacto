package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// serveWith starts the referrers registry with opts, wrapped by wrap when set,
// and returns the stats repository opened on it.
func serveWith(t *testing.T, opts testutil.ReferrersOptions, wrap func(http.Handler) http.Handler) *remote.Repository {
	t.Helper()
	h := testutil.NewReferrersRegistry(opts)
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	repo, err := Open(host+"/acme/pacto-stats", nil, []string{host})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// runOf is a valid diff record of service with its own id per revision.
func runOf(service, rev string) runrecord.Record {
	r := record()
	r.Service, r.NewRevision = service, rev
	r.ID = runrecord.ID(r.Kind, service, r.OldRevision, rev)
	return r
}

func publish(t *testing.T, repo *remote.Repository, records ...runrecord.Record) []ocispec.Descriptor {
	t.Helper()
	var out []ocispec.Descriptor
	for _, r := range records {
		d, err := Publish(t.Context(), repo, r)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// anchorAt pushes service's anchor and tags it tag.
func anchorAt(t *testing.T, repo *remote.Repository, service, tag string) ocispec.Descriptor {
	t.Helper()
	a := Anchor(service)
	if err := ocireferrer.Push(t.Context(), repo, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(t.Context(), a.ManifestDesc, tag); err != nil {
		t.Fatal(err)
	}
	return a.ManifestDesc
}

// attach pushes a run-type manifest with these layers onto service's anchor.
func attach(t *testing.T, repo *remote.Repository, service string, layers ...[]byte) ocispec.Descriptor {
	t.Helper()
	ctx := t.Context()
	subject := anchorAt(t, repo, service, runrecord.AnchorTag(service))
	cfg := ocispec.DescriptorEmptyJSON
	push := func(d ocispec.Descriptor, data []byte) {
		if err := repo.Push(ctx, d, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	push(cfg, cfg.Data)
	m := ocispec.Manifest{MediaType: ocispec.MediaTypeImageManifest, ArtifactType: runrecord.ArtifactType, Config: cfg, Subject: &subject}
	m.SchemaVersion = 2
	for _, l := range layers {
		d := ocireferrer.Descriptor(runrecord.LayerType, l)
		push(d, l)
		m.Layers = append(m.Layers, d)
	}
	raw, _ := json.Marshal(m)
	d := ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, raw)
	push(d, raw)
	return d
}

// fresh reopens repo as a reader does: a repository that published has
// already settled how it lists referrers.
func fresh(t *testing.T, repo *remote.Repository) *remote.Repository {
	t.Helper()
	host := repo.Reference.Registry
	r, err := Open(host+"/"+repo.Reference.Repository, nil, []string{host})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustRead(t *testing.T, repo *remote.Repository, anchors ...string) stats.RecordSet {
	t.Helper()
	set, err := Read(t.Context(), fresh(t, repo), anchors)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func services(set stats.RecordSet) []string {
	var out []string
	for _, e := range set.Entries {
		out = append(out, e.Record.Service)
	}
	slices.Sort(out)
	return out
}

func TestReadListsEveryAnchorByTagPrefix(t *testing.T) {
	var recording atomic.Bool
	var mu sync.Mutex
	methods := map[string]bool{}
	repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if recording.Load() {
				mu.Lock()
				methods[r.Method] = true
				mu.Unlock()
			}
			inner.ServeHTTP(w, r)
		})
	})
	publish(t, repo, runOf("payments", "b"), runOf("orders", "b"), runOf("orders", "c"))
	// Aval's own prefix and an unrelated tag are never read.
	ghost := anchorAt(t, repo, "ghost", "stats-ghost")
	_ = anchorAt(t, repo, "other", "latest")
	payload, _ := runrecord.Encode(runOf("ghost", "b"))
	if err := ocireferrer.Push(t.Context(), repo, ocireferrer.Build(runrecord.ArtifactType, runrecord.LayerType, payload, &ghost, nil)); err != nil {
		t.Fatal(err)
	}
	recording.Store(true)
	set := mustRead(t, repo)
	if got := services(set); !slices.Equal(got, []string{"orders", "orders", "payments"}) {
		t.Fatalf("services = %v", got)
	}
	if len(set.Skipped) != 0 || set.Truncated != "" {
		t.Fatalf("set = %+v", set)
	}
	for m := range methods {
		if m != http.MethodGet && m != http.MethodHead {
			t.Fatalf("the read sent %s", m)
		}
	}
}

func TestReadOneServiceUsesItsAnchor(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	publish(t, repo, runOf("payments", "b"), runOf("orders", "b"))
	set := mustRead(t, repo, runrecord.AnchorTag("payments"))
	if got := services(set); !slices.Equal(got, []string{"payments"}) {
		t.Fatalf("services = %v", got)
	}
}

func TestReadMissingServiceAnchorIsEmpty(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	publish(t, repo, runOf("payments", "b"))
	set := mustRead(t, repo, runrecord.AnchorTag("nobody"))
	if len(set.Entries) != 0 || len(set.Skipped) != 0 || set.Truncated != "" {
		t.Fatalf("set = %+v", set)
	}
}

func TestReadEmptyRepository(t *testing.T) {
	set := mustRead(t, serveWith(t, testutil.ReferrersOptions{}, nil))
	if len(set.Entries) != 0 || len(set.Skipped) != 0 {
		t.Fatalf("set = %+v", set)
	}
}

func TestReadSkipsMalformedOversizedUnreadable(t *testing.T) {
	broken := []byte(`{"version":"pacto.trianalab.io/run-record/v1","id":"x","kind":"diff"}`)
	var brokenDigest atomic.Value
	brokenDigest.Store("none")
	repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/blobs/"+brokenDigest.Load().(string)) {
				http.Error(w, "boom", http.StatusNotFound)
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	good := publish(t, repo, runOf("payments", "b"))
	attach(t, repo, "payments", []byte("not json"))
	two, _ := runrecord.Encode(runOf("payments", "c"))
	attach(t, repo, "payments", two, broken)
	attach(t, repo, "payments", bytes.Repeat([]byte(" "), runrecord.MaxRecordBytes+1))
	unreadable, _ := runrecord.Encode(runOf("payments", "d"))
	brokenDigest.Store(ocireferrer.Descriptor(runrecord.LayerType, unreadable).Digest.String())
	attach(t, repo, "payments", unreadable)

	tag := runrecord.AnchorTag("payments")
	set := mustRead(t, repo)
	if len(set.Entries) != 1 || set.Entries[0].Digest != good[0].Digest.String() {
		t.Fatalf("entries = %+v", set.Entries)
	}
	want := map[string]stats.Skipped{tag: {Malformed: 2, Oversized: 1, Unreadable: 1}}
	if len(set.Skipped) != 1 || set.Skipped[tag] != want[tag] {
		t.Fatalf("skipped = %+v", set.Skipped)
	}
}

func TestReadSkipsUnreadableManifest(t *testing.T) {
	var target atomic.Value
	target.Store("none")
	repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/manifests/"+target.Load().(string)) {
				http.Error(w, "gone", http.StatusNotFound)
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	desc := publish(t, repo, runOf("payments", "b"))
	target.Store(desc[0].Digest.String())
	set := mustRead(t, repo)
	if len(set.Entries) != 0 || set.Skipped[runrecord.AnchorTag("payments")] != (stats.Skipped{Unreadable: 1}) {
		t.Fatalf("set = %+v", set)
	}
}

func TestReadSkipsOversizedManifest(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	// A manifest descriptor over the bound is skipped before it is fetched.
	big := strings.Repeat("x", runrecord.MaxRecordBytes)
	subject := anchorAt(t, repo, "payments", runrecord.AnchorTag("payments"))
	payload, _ := runrecord.Encode(runOf("payments", "b"))
	art := ocireferrer.Build(runrecord.ArtifactType, runrecord.LayerType, payload, &subject, map[string]string{"pad": big})
	if err := ocireferrer.Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	set := mustRead(t, repo)
	if len(set.Entries) != 0 || set.Skipped[runrecord.AnchorTag("payments")] != (stats.Skipped{Oversized: 1}) {
		t.Fatalf("set = %+v", set)
	}
}

func TestReadSkipsRecordOnWrongAnchor(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	orders, _ := runrecord.Encode(runOf("orders", "b"))
	attach(t, repo, "payments", orders)
	set := mustRead(t, repo)
	if len(set.Entries) != 0 || set.Skipped[runrecord.AnchorTag("payments")] != (stats.Skipped{Malformed: 1}) {
		t.Fatalf("set = %+v", set)
	}
}

func TestReadIgnoresOtherArtifactTypes(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		repo := serveWith(t, testutil.ReferrersOptions{LegacyArtifactType: legacy}, nil)
		publish(t, repo, runOf("payments", "b"))
		subject := Anchor("payments").ManifestDesc
		sig := ocireferrer.Build("application/vnd.dev.sigstore.bundle.v0.3+json", "application/vnd.dev.sigstore.bundle.v0.3+json", []byte(`{}`), &subject, nil)
		if err := ocireferrer.Push(t.Context(), repo, sig); err != nil {
			t.Fatal(err)
		}
		set := mustRead(t, repo)
		if len(set.Entries) != 1 || len(set.Skipped) != 0 {
			t.Fatalf("legacy=%v set = %+v", legacy, set)
		}
	}
}

func TestReadKeepsManifestDigest(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	desc := publish(t, repo, runOf("payments", "b"))
	set := mustRead(t, repo)
	e := set.Entries[0]
	if e.Digest != desc[0].Digest.String() || e.Anchor != runrecord.AnchorTag("payments") || e.Record.ID != runOf("payments", "b").ID {
		t.Fatalf("entry = %+v", e)
	}
}

func setBound(t *testing.T, p *int, v int) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func TestReadTruncatesAtMaxRecords(t *testing.T) {
	setBound(t, &MaxRecords, 3)
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	publish(t, repo, runOf("payments", "a1"), runOf("payments", "a2"), runOf("payments", "a3"), runOf("payments", "a4"), runOf("payments", "a5"))
	set := mustRead(t, repo)
	if len(set.Entries) != 3 || set.Truncated != "more than 3 run records" || set.TruncatedAnchor != runrecord.AnchorTag("payments") {
		t.Fatalf("set = %d entries, %q %q", len(set.Entries), set.Truncated, set.TruncatedAnchor)
	}
	// The bound spans services: a later anchor reads nothing more.
	publish(t, repo, runOf("zeta", "a1"))
	if set = mustRead(t, repo, runrecord.AnchorTag("payments"), runrecord.AnchorTag("zeta")); len(set.Entries) != 3 {
		t.Fatalf("read %d entries past the bound", len(set.Entries))
	}
}

func TestReadTruncatesAtMaxPages(t *testing.T) {
	setBound(t, &MaxPages, 2)
	repo := serveWith(t, testutil.ReferrersOptions{PageSize: 1}, nil)
	publish(t, repo, runOf("payments", "a1"), runOf("payments", "a2"), runOf("payments", "a3"), runOf("orders", "a1"))
	set := mustRead(t, repo)
	if len(set.Entries) != 3 || set.Truncated != "more than 2 pages listing one service's records" ||
		strings.Contains(set.Truncated, runrecord.AnchorTagPrefix) || set.TruncatedAnchor != runrecord.AnchorTag("payments") {
		t.Fatalf("set = %+v %q %q", services(set), set.Truncated, set.TruncatedAnchor)
	}
}

func TestReadTruncatesAnchorScan(t *testing.T) {
	// The test registry serves every tag in one page, so the bound is set
	// below one page to cut the scan.
	setBound(t, &MaxPages, 0)
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	publish(t, repo, runOf("payments", "a1"))
	set := mustRead(t, repo)
	if len(set.Entries) != 0 || set.Truncated != "more than 0 pages listing anchors" || set.TruncatedAnchor != "" {
		t.Fatalf("set = %+v %q", services(set), set.Truncated)
	}
}

func TestReadDeadlineIsAnError(t *testing.T) {
	var block atomic.Bool
	repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if block.Load() && strings.Contains(r.URL.Path, "/referrers/") {
				<-r.Context().Done()
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	publish(t, repo, runOf("payments", "b"))
	block.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	set, err := Read(ctx, fresh(t, repo), nil)
	var anchorErr *AnchorError
	if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &anchorErr) || len(set.Skipped) != 0 || len(set.Entries) != 0 {
		t.Fatalf("set = %+v err = %v", set, err)
	}
}

func TestReadFetchDeadlineIsAnError(t *testing.T) {
	var block atomic.Bool
	repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if block.Load() && strings.Contains(r.URL.Path, "/blobs/") {
				<-r.Context().Done()
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	publish(t, repo, runOf("payments", "b"))
	block.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if set, err := Read(ctx, fresh(t, repo), nil); !errors.Is(err, context.DeadlineExceeded) || len(set.Skipped) != 0 {
		t.Fatalf("set = %+v err = %v", set, err)
	}
}

func TestReadCancelledBeforeAnchorScan(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Read(ctx, fresh(t, repo), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadListingFailureIsAnAnchorError(t *testing.T) {
	for _, path := range []string{"/referrers/", "/manifests/" + runrecord.AnchorTagPrefix} {
		repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut && strings.Contains(r.URL.Path, path) {
					http.Error(w, "boom", http.StatusForbidden)
					return
				}
				inner.ServeHTTP(w, r)
			})
		})
		publish(t, repo, runOf("payments", "b"))
		tag := runrecord.AnchorTag("payments")
		_, err := Read(t.Context(), fresh(t, repo), []string{tag})
		var anchorErr *AnchorError
		if !errors.As(err, &anchorErr) || anchorErr.Tag != tag || !strings.Contains(err.Error(), tag) || anchorErr.Unwrap() == nil {
			t.Fatalf("%s: err = %v", path, err)
		}
	}
}

func TestReadTagScanFailureIsAnError(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{}, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/tags/list") {
				http.Error(w, "boom", http.StatusForbidden)
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	if _, err := Read(t.Context(), fresh(t, repo), nil); err == nil {
		t.Fatal("a failed tag scan read as no records")
	}
}

func TestReadLegacyArtifactTypeRegistry(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{LegacyArtifactType: true}, nil)
	publish(t, repo, runOf("payments", "b"), runOf("orders", "b"))
	if got := services(mustRead(t, repo)); !slices.Equal(got, []string{"orders", "payments"}) {
		t.Fatalf("services = %v", got)
	}
}

func TestReadTagFallbackRegistry(t *testing.T) {
	repo := serveWith(t, testutil.ReferrersOptions{Unsupported: true}, nil)
	publish(t, repo, runOf("payments", "b"), runOf("payments", "c"))
	if got := services(mustRead(t, repo)); !slices.Equal(got, []string{"payments", "payments"}) {
		t.Fatalf("services = %v", got)
	}
}

func TestAnchorErrorUnwraps(t *testing.T) {
	inner := errors.New("boom")
	err := error(&AnchorError{Tag: "pacto-stats-x", Err: inner})
	if !errors.Is(err, inner) || err.Error() != "reading the run records under pacto-stats-x: boom" {
		t.Fatalf("err = %v", err)
	}
}
