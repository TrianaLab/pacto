package ocireferrer

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
)

// tagged serves a registry under opts holding one artifact in team/app under
// every tag in tags, written in process so thousands cost no round trips.
func tagged(t *testing.T, opts testutil.ReferrersOptions, tags ...string) (*remote.Repository, Artifact) {
	t.Helper()
	h := testutil.NewReferrersRegistry(opts)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	repo, _ := Open(strings.TrimPrefix(srv.URL, "http://")+"/team/app", Options{})
	art := Build("application/x-subject", "application/x-layer", []byte("s"), nil, nil)
	if err := Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		req := httptest.NewRequest(http.MethodPut, "/v2/team/app/manifests/"+tag, bytes.NewReader(art.Manifest))
		req.Header.Set("Content-Type", ocispec.MediaTypeImageManifest)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("tag %s: %d", tag, rec.Code)
		}
	}
	return repo, art
}

// tagsServer answers every tag listing with tags and, when link, a Link
// header naming the same page again.
func tagsServer(t *testing.T, link bool, tags ...string) *remote.Repository {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if link {
			w.Header().Set("Link", `<`+r.URL.Path+`?n=1>; rel="next"`)
		}
		_, _ = fmt.Fprintf(w, `{"tags":["%s"]}`, strings.Join(tags, `","`))
	}))
	t.Cleanup(srv.Close)
	repo, _ := Open(strings.TrimPrefix(srv.URL, "http://")+"/team/app", Options{})
	return repo
}

func TestTagsFiltersByPrefix(t *testing.T) {
	repo, _ := tagged(t, testutil.ReferrersOptions{}, "a-1", "rec-2", "rec-1", "recx", "z")
	got, err := Tags(t.Context(), repo, "rec-")
	if err != nil || !slices.Equal(got, []string{"rec-1", "rec-2"}) {
		t.Fatalf("tags = %v %v", got, err)
	}
	if repo.TagListPageSize != tagPageSize {
		t.Fatalf("page size = %d", repo.TagListPageSize)
	}
}

func TestTagsEmptyPrefixListsAll(t *testing.T) {
	repo, _ := tagged(t, testutil.ReferrersOptions{TagsLink: true}, "a", "m", "z")
	repo.TagListPageSize = 1
	if got, err := Tags(t.Context(), repo, ""); err != nil || !slices.Equal(got, []string{"a", "m", "z"}) {
		t.Fatalf("tags = %v %v", got, err)
	}
}

func TestTagsFollowsEveryPage(t *testing.T) {
	var tags []string
	for i := range 7 {
		tags = append(tags, fmt.Sprintf("rec-%d", i))
	}
	repo, _ := tagged(t, testutil.ReferrersOptions{TagsLink: true}, tags...)
	repo.TagListPageSize = 2
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || !slices.Equal(got, tags) {
		t.Fatalf("tags = %v %v", got, err)
	}
}

// TestTagsSeeksToPrefix: with 10,000 other tags around the prefix and a page
// bound of two pages of two, the three tags still come back, which only a
// listing that starts at the prefix and stops past it can do.
func TestTagsSeeksToPrefix(t *testing.T) {
	tags := []string{"m-1", "m-2", "m-3"}
	for i := range 5000 {
		tags = append(tags, fmt.Sprintf("a-%05d", i), fmt.Sprintf("z-%05d", i))
	}
	repo, _ := tagged(t, testutil.ReferrersOptions{TagsLink: true}, tags...)
	repo.TagListPageSize = 2
	old := maxTagPages
	maxTagPages = 2
	t.Cleanup(func() { maxTagPages = old })
	if got, err := Tags(t.Context(), repo, "m-"); err != nil || !slices.Equal(got, tags[:3]) {
		t.Fatalf("tags = %v %v", got, err)
	}
}

func TestTagsFallsBackWhenUnsorted(t *testing.T) {
	repo := tagsServer(t, false, "z", "rec-2", "a", "rec-1")
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || !slices.Equal(got, []string{"rec-2", "rec-1"}) {
		t.Fatalf("tags = %v %v", got, err)
	}
}

// TestTagsFallsBackWhenPagesOverlap: each page is sorted but the second
// starts before the first ended, so the seek is not trusted and the full scan
// from the start answers.
func TestTagsFallsBackWhenPagesOverlap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("page") == "2":
			_, _ = fmt.Fprint(w, `{"tags":["rec-1"]}`)
		case r.URL.Query().Get("last") != "":
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_, _ = fmt.Fprint(w, `{"tags":["rec-2","rec-3"]}`)
		default:
			_, _ = fmt.Fprint(w, `{"tags":["a","rec-0","rec-1","rec-2","rec-3"]}`)
		}
	}))
	t.Cleanup(srv.Close)
	repo, _ := Open(strings.TrimPrefix(srv.URL, "http://")+"/team/app", Options{})
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || !slices.Equal(got, []string{"rec-0", "rec-1", "rec-2", "rec-3"}) {
		t.Fatalf("tags = %v %v", got, err)
	}
}

// TestTagsFallsBackWhenSeekRefused: zot answers 404 with an empty body to a
// tag listing whose last names no tag it holds (seen on zot v2.1.20 by the
// registry conformance test), so the seek falls back to the full scan.
func TestTagsFallsBackWhenSeekRefused(t *testing.T) {
	held := []string{"a", "rec-1", "rec-2", "z"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last := r.URL.Query().Get("last")
		if last != "" && !slices.Contains(held, last) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tags":["%s"]}`, strings.Join(held, `","`))
	}))
	t.Cleanup(srv.Close)
	repo, _ := Open(strings.TrimPrefix(srv.URL, "http://")+"/team/app", Options{})
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || !slices.Equal(got, []string{"rec-1", "rec-2"}) {
		t.Fatalf("tags = %v %v", got, err)
	}
}

// TestTagsGHCRUnknownSeek: GHCR answers a listing from a tag it does not
// hold with 200 and null tags. Seeking from the anchor tag (the prefix
// without its '-') is honoured, and an empty seek is never a confident empty
// listing: the full scan answers.
func TestTagsGHCRUnknownSeek(t *testing.T) {
	opts := testutil.ReferrersOptions{TagsUnknownLast: true}
	tags := []string{"rec-1", "rec-2"}
	for i := range 5000 {
		tags = append(tags, fmt.Sprintf("a-%05d", i), fmt.Sprintf("z-%05d", i))
	}
	repo, _ := tagged(t, opts, append([]string{"rec"}, tags...)...)
	repo.TagListPageSize = 2
	old := maxTagPages
	maxTagPages = 2
	t.Cleanup(func() { maxTagPages = old })
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || !slices.Equal(got, tags[:2]) {
		t.Fatalf("seek from the anchor = %v %v", got, err)
	}
	maxTagPages = old
	repo, _ = tagged(t, opts, "a", "rec-1", "z")
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || !slices.Equal(got, []string{"rec-1"}) {
		t.Fatalf("seek from a tag GHCR does not hold = %v %v", got, err)
	}
	repo, _ = tagged(t, opts, "a", "rec")
	if got, err := Tags(t.Context(), repo, "rec-"); err != nil || len(got) != 0 {
		t.Fatalf("anchor with no records = %v %v", got, err)
	}
}

func TestSeekFrom(t *testing.T) {
	for prefix, want := range map[string]string{"": "", "stream-ab-": "stream-ab", "rec": "reb"} {
		if got := seekFrom(prefix); got != want {
			t.Errorf("seekFrom(%q) = %q, want %q", prefix, got, want)
		}
	}
}

func TestTagsFailsPastPageBound(t *testing.T) {
	old := maxTagPages
	maxTagPages = 3
	t.Cleanup(func() { maxTagPages = old })
	_, err := Tags(t.Context(), tagsServer(t, true, "rec-1"), "rec-")
	if err == nil || !strings.Contains(err.Error(), `from "rec": more than 3 pages`) {
		t.Fatalf("sorted path: %v", err)
	}
	_, err = Tags(t.Context(), tagsServer(t, true, "z", "rec-1"), "rec-")
	if err == nil || !strings.Contains(err.Error(), "the registry does not sort tags, and the full scan passed 3 pages") {
		t.Fatalf("full scan: %v", err)
	}
}

func TestTagsRegistryError(t *testing.T) {
	repo, _ := tagged(t, testutil.ReferrersOptions{})
	other, _ := Open(repo.Reference.Registry+"/team/missing", Options{})
	if _, err := Tags(t.Context(), other, "rec-"); err == nil {
		t.Fatal("a repository that does not exist listed no tags without an error")
	}
}

// attachedTo pushes a referrer of subject into repo.
func attachedTo(t *testing.T, repo *remote.Repository, subject ocispec.Descriptor) Artifact {
	t.Helper()
	ref := Build("application/x-ref", "application/x-ref", []byte("r"), &subject, nil)
	if err := Push(t.Context(), repo, ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestAttachedNativeOnly(t *testing.T) {
	repo, art := tagged(t, testutil.ReferrersOptions{}, "rec-1")
	ref := attachedTo(t, repo, art.ManifestDesc)
	descs, tags, err := Attached(t.Context(), repo, art.ManifestDesc, "")
	if err != nil || len(descs) != 1 || descs[0].Digest != ref.ManifestDesc.Digest || tags != nil {
		t.Fatalf("attached = %v %v %v", descs, tags, err)
	}
	descs, tags, err = Attached(t.Context(), repo, art.ManifestDesc, "rec-")
	if err != nil || len(descs) != 1 || !slices.Equal(tags, []string{"rec-1"}) {
		t.Fatalf("attached with tags = %v %v %v", descs, tags, err)
	}
}

func TestAttachedUnsupportedWithPrefixReadsTags(t *testing.T) {
	repo, art := tagged(t, testutil.ReferrersOptions{Unsupported: true}, "rec-1", "rec-2", "other")
	descs, tags, err := Attached(t.Context(), repo, art.ManifestDesc, "rec-")
	if err != nil || len(descs) != 0 || !slices.Equal(tags, []string{"rec-1", "rec-2"}) {
		t.Fatalf("attached = %v %v %v", descs, tags, err)
	}
}

// TestAttachedRefusesWhatIsNotAMissingAPI: oras-go answers ErrUnsupported
// for a maintenance page, an index with a content-type parameter and a 404 on
// a later page alike. Only a registry that does not serve the API reads as
// tags; the rest fail, so a native stream is never read as empty or short.
func TestAttachedRefusesWhatIsNotAMissingAPI(t *testing.T) {
	for _, ct := range []string{"text/html", ocispec.MediaTypeImageIndex + "; charset=utf-8"} {
		repo, art := tagged(t, testutil.ReferrersOptions{ReferrersContentType: ct}, "rec-1")
		if descs, tags, err := Attached(t.Context(), repo, art.ManifestDesc, "rec-"); err == nil {
			t.Errorf("%s: a non-index answer read as %v %v", ct, descs, tags)
		}
	}
	h := testutil.NewReferrersRegistry(testutil.ReferrersOptions{PageSize: 1})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/referrers/") && r.URL.Query().Get("last") != "" {
			http.Error(w, `{"errors":[{"code":"UNSUPPORTED"}]}`, http.StatusNotFound)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	repo, _ := Open(strings.TrimPrefix(srv.URL, "http://")+"/team/app", Options{})
	subject := Build("application/x-subject", "application/x-layer", []byte("s"), nil, nil)
	if err := Push(t.Context(), repo, subject); err != nil {
		t.Fatal(err)
	}
	attachedTo(t, repo, subject.ManifestDesc)
	second := Build("application/x-ref", "application/x-ref", []byte("r2"), &subject.ManifestDesc, nil)
	if err := Push(t.Context(), repo, second); err != nil {
		t.Fatal(err)
	}
	if descs, _, err := Attached(t.Context(), repo, subject.ManifestDesc, "rec-"); err == nil || !strings.Contains(err.Error(), "stopped after 1 referrers") {
		t.Fatalf("a listing cut on page 2 read as %v, %v", descs, err)
	}
}

func TestAttachedUnsupportedWithoutPrefixFails(t *testing.T) {
	repo, art := tagged(t, testutil.ReferrersOptions{Unsupported: true}, "rec-1")
	if _, _, err := Attached(t.Context(), repo, art.ManifestDesc, ""); err == nil {
		t.Fatal("a registry without Referrers listed nothing without an error")
	}
}

func TestAttachedFailures(t *testing.T) {
	repo, art := tagged(t, testutil.ReferrersOptions{NameUnknown: true}, "rec-1")
	if _, _, err := Attached(t.Context(), repo, art.ManifestDesc, "rec-"); err == nil || errors.Is(err, errTagsDone) {
		t.Fatalf("a NAME_UNKNOWN referrers answer read as no Referrers API: %v", err)
	}
	repo, art = tagged(t, testutil.ReferrersOptions{Unsupported: true})
	missing, _ := Open(repo.Reference.Registry+"/team/missing", Options{})
	if _, _, err := Attached(t.Context(), missing, art.ManifestDesc, "rec-"); err == nil {
		t.Fatal("a failed tag listing was not an error")
	}
}

// writeOnceRepo serves a registry without the Referrers API through wrap
// (nil: none) and returns team/app and a record attached to an anchor there.
func writeOnceRepo(t *testing.T, wrap func(http.Handler) http.Handler) (*remote.Repository, Artifact) {
	t.Helper()
	h := testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: true})
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	repo, _ := Open(strings.TrimPrefix(srv.URL, "http://")+"/team/app", Options{})
	noRetry(repo)
	anchor := Build("application/x-anchor", "application/x-anchor", []byte("a"), nil, nil)
	if err := Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	return repo, Build("application/x-rec", "application/x-rec", []byte("r"), &anchor.ManifestDesc, nil)
}

func TestPushWriteOnceWritesAndConfirms(t *testing.T) {
	repo, art := writeOnceRepo(t, nil)
	if err := PushWriteOnce(t.Context(), repo, art, "rec-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Resolve(t.Context(), "rec-1"); err != nil || got.Digest != art.ManifestDesc.Digest {
		t.Fatalf("rec-1 = %v %v", got.Digest, err)
	}
}

func TestPushWriteOnceIsIdempotent(t *testing.T) {
	var puts int
	repo, art := writeOnceRepo(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/") {
				puts++
			}
			h.ServeHTTP(w, r)
		})
	})
	for range 2 {
		if err := PushWriteOnce(t.Context(), repo, art, "rec-1"); err != nil {
			t.Fatal(err)
		}
	}
	if puts != 2 { // the anchor and the record, once
		t.Fatalf("manifest puts = %d", puts)
	}
}

func TestPushWriteOnceRefusesTakenTag(t *testing.T) {
	repo, art := writeOnceRepo(t, nil)
	other := Build("application/x-rec", "application/x-rec", []byte("other"), nil, nil)
	if err := PushWriteOnce(t.Context(), repo, other, "rec-1"); err != nil {
		t.Fatal(err)
	}
	if err := PushWriteOnce(t.Context(), repo, art, "rec-1"); !errors.Is(err, ErrTagTaken) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := repo.Resolve(t.Context(), "rec-1"); got.Digest != other.ManifestDesc.Digest {
		t.Fatal("a taken tag moved")
	}
}

// TestPushWriteOnceFailsWhenTagNamesAnotherManifest: a registry that moves
// the tag right after the push is caught by the confirming resolve.
func TestPushWriteOnceFailsWhenTagNamesAnotherManifest(t *testing.T) {
	other := Build("application/x-rec", "application/x-rec", []byte("other"), nil, nil)
	repo, art := writeOnceRepo(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r)
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/manifests/rec-1") {
				req := httptest.NewRequest(http.MethodPut, r.URL.Path, bytes.NewReader(other.Manifest))
				req.Header.Set("Content-Type", ocispec.MediaTypeImageManifest)
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	})
	err := PushWriteOnce(t.Context(), repo, art, "rec-1")
	if err == nil || !strings.Contains(err.Error(), "names "+other.ManifestDesc.Digest.String()+" after the push") {
		t.Fatalf("err = %v", err)
	}
}

func TestPushWriteOnceFailures(t *testing.T) {
	cases := map[string]func(*http.Request, int) bool{
		"resolve": func(r *http.Request, n int) bool { return r.Method == http.MethodHead && n == 1 },
		"blob": func(r *http.Request, _ int) bool {
			return strings.Contains(r.URL.Path, "/blobs/") && r.Method != http.MethodHead
		},
		"manifest": func(r *http.Request, _ int) bool {
			return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/rec-1")
		},
		"confirm": func(r *http.Request, n int) bool { return r.Method == http.MethodHead && n == 2 },
	}
	for name, fail := range cases {
		armed, heads := false, 0
		repo, art := writeOnceRepo(t, func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if armed && strings.HasSuffix(r.URL.Path, "/manifests/rec-1") && r.Method != http.MethodPut {
					heads++
				}
				if armed && fail(r, heads) {
					http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
					return
				}
				h.ServeHTTP(w, r)
			})
		})
		armed = true
		if err := PushWriteOnce(t.Context(), repo, art, "rec-1"); err == nil {
			t.Errorf("%s: a failed write succeeded", name)
		}
	}
}

func TestWriteOnceNeverWritesReferrersTagIndex(t *testing.T) {
	repo, _ := writeOnceRepo(t, nil)
	anchor := Build("application/x-anchor", "application/x-anchor", []byte("a"), nil, nil)
	for i := range 3 {
		art := Build("application/x-rec", "application/x-rec", fmt.Appendf(nil, "r%d", i), &anchor.ManifestDesc, nil)
		if err := PushWriteOnce(t.Context(), repo, art, fmt.Sprintf("rec-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	tags, err := Tags(t.Context(), repo, "")
	if err != nil || len(tags) != 3 {
		t.Fatalf("tags = %v %v", tags, err)
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, "sha256-") {
			t.Fatalf("a write-once stream wrote the referrers-tag index %s", tag)
		}
	}
}
