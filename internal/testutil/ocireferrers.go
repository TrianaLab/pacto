package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ReferrersOptions configures [NewReferrersRegistry].
type ReferrersOptions struct {
	// PageSize caps how many referrers one response carries when the client did
	// not ask for fewer. Zero means one unbounded page.
	PageSize int
	// LegacyArtifactType reports each descriptor's artifactType as the referring
	// manifest's CONFIG media type even when the manifest declares its own, which
	// is what go-containerregistry's registry and other pre-v1.1.1 servers do. A
	// client that trusts the listing descriptor sees no Pacto artifacts at all.
	LegacyArtifactType bool
	// Unsupported serves 404 from the referrers endpoint, as a registry that
	// implements no Referrers API does. It is how a client's fallback-forbidden
	// posture is proven.
	Unsupported bool
	// CatalogStatus serves that status on /v2/_catalog: 404 for a registry
	// without _catalog, 500 for one that fails. Zero serves the catalog.
	CatalogStatus int
	// NameUnknown serves 404 NAME_UNKNOWN from the referrers endpoint, an
	// answer that says the repository is missing, never that Referrers is.
	NameUnknown bool
	// ReferrersContentType serves a 200 with this content type and an HTML
	// body from the referrers endpoint, as a maintenance or captive-proxy page does.
	ReferrersContentType string
	// Challenge serves 401 with this WWW-Authenticate on GET /v2/ only.
	Challenge string
	// TagsLink adds the Link header go-containerregistry's tag listing never
	// sets: a next page whenever a response held exactly ?n= tags. Without
	// it a client stops after the first page.
	TagsLink bool
}

// NewReferrersRegistry returns an in-process OCI registry whose Referrers
// endpoint follows distribution-spec v1.1.1 where go-containerregistry's test
// registry does not: the descriptor artifactType is read from the referring
// MANIFEST (falling back to its config media type only when absent), results are
// ordered deterministically, and ?n= paginates with a Link header. No real
// registry Pacto can run against paginates referrers today, so this server is
// the only way to prove a client follows every page.
//
// Everything else — blob uploads, manifest storage, tag listing — is
// go-containerregistry's registry unchanged.
func NewReferrersRegistry(opts ReferrersOptions) http.Handler {
	inner := registry.New(
		registry.WithReferrersSupport(true),
		registry.Logger(log.New(io.Discard, "", 0)),
	)
	return &referrersRegistry{inner: inner, opts: opts}
}

type referrersRegistry struct {
	inner http.Handler
	opts  ReferrersOptions
}

// serveOverride answers the requests opts reshape outside the referrers
// endpoint, reporting whether it did.
func (r *referrersRegistry) serveOverride(w http.ResponseWriter, req *http.Request) bool {
	switch {
	case r.opts.Challenge != "" && req.URL.Path == "/v2/":
		w.Header().Set("WWW-Authenticate", r.opts.Challenge)
		w.WriteHeader(http.StatusUnauthorized)
	case r.opts.CatalogStatus != 0 && req.URL.Path == "/v2/_catalog":
		http.Error(w, `{"errors":[{"code":"UNSUPPORTED"}]}`, r.opts.CatalogStatus)
	case r.opts.TagsLink && req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/tags/list"):
		r.tagsWithLink(w, req)
	default:
		return false
	}
	return true
}

func (r *referrersRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.serveOverride(w, req) {
		return
	}
	repo, target, ok := parseReferrersPath(req.URL.Path)
	if !ok || req.Method != http.MethodGet {
		r.inner.ServeHTTP(w, req)
		return
	}
	if r.opts.Unsupported {
		http.Error(w, `{"errors":[{"code":"UNSUPPORTED"}]}`, http.StatusNotFound)
		return
	}
	if r.opts.NameUnknown {
		http.Error(w, `{"errors":[{"code":"NAME_UNKNOWN"}]}`, http.StatusNotFound)
		return
	}
	if r.opts.ReferrersContentType != "" {
		w.Header().Set("Content-Type", r.opts.ReferrersContentType)
		_, _ = w.Write([]byte("<html>maintenance</html>"))
		return
	}
	descs, err := r.referrersOf(repo, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	page, next := r.paginate(descs, req.URL.Query())
	if next != "" {
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, next))
	}
	body, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: page,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// tagsWithLink serves the inner, sorted tag listing paginated here: tags
// after ?last=, at most ?n= of them, and a next-page Link header when more
// remain. The inner handler returns every tag when ?last= sorts past them all.
func (r *referrersRegistry) tagsWithLink(w http.ResponseWriter, req *http.Request) {
	rec := httptest.NewRecorder()
	r.inner.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, req.URL.Path, nil))
	var page struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	q := req.URL.Query()
	if last := q.Get("last"); last != "" {
		i := sort.SearchStrings(page.Tags, last)
		if i < len(page.Tags) && page.Tags[i] == last {
			i++
		}
		page.Tags = page.Tags[i:]
	}
	if n, err := strconv.Atoi(q.Get("n")); err == nil && n > 0 && len(page.Tags) > n {
		page.Tags = page.Tags[:n]
		next := url.Values{"n": {strconv.Itoa(n)}, "last": {page.Tags[n-1]}}
		w.Header().Set("Link", fmt.Sprintf(`<%s?%s>; rel="next"`, req.URL.Path, next.Encode()))
	}
	body, _ := json.Marshal(page)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// paginate applies ?n= and ?last= over the deterministically ordered result and
// returns the page plus the next-page URL, empty when the page is the last.
func (r *referrersRegistry) paginate(descs []ocispec.Descriptor, q url.Values) ([]ocispec.Descriptor, string) {
	if last := q.Get("last"); last != "" {
		for i, d := range descs {
			if d.Digest.String() == last {
				descs = descs[i+1:]
				break
			}
		}
	}
	size := r.opts.PageSize
	if n, err := strconv.Atoi(q.Get("n")); err == nil && n > 0 && (size == 0 || n < size) {
		size = n
	}
	if size <= 0 || len(descs) <= size {
		return descs, ""
	}
	page := descs[:size]
	nextQuery := url.Values{"last": {page[len(page)-1].Digest.String()}}
	if n := q.Get("n"); n != "" {
		nextQuery.Set("n", n)
	}
	return page, "?" + nextQuery.Encode()
}

// referrersOf asks the inner registry which manifests point at target, then
// re-derives each descriptor from the referring manifest's own bytes.
func (r *referrersRegistry) referrersOf(repo, target string) ([]ocispec.Descriptor, error) {
	var raw ocispec.Index
	if err := r.getJSON(fmt.Sprintf("/v2/%s/referrers/%s", repo, target), &raw); err != nil {
		return nil, err
	}
	out := make([]ocispec.Descriptor, 0, len(raw.Manifests))
	for _, d := range raw.Manifests {
		var m struct {
			MediaType    string `json:"mediaType"`
			ArtifactType string `json:"artifactType"`
			Config       struct {
				MediaType string `json:"mediaType"`
			} `json:"config"`
			Annotations map[string]string `json:"annotations"`
		}
		if err := r.getJSON(fmt.Sprintf("/v2/%s/manifests/%s", repo, d.Digest), &m); err != nil {
			return nil, err
		}
		artifactType := m.ArtifactType
		if artifactType == "" || r.opts.LegacyArtifactType {
			artifactType = m.Config.MediaType
		}
		out = append(out, ocispec.Descriptor{
			MediaType:    d.MediaType,
			Digest:       d.Digest,
			Size:         d.Size,
			ArtifactType: artifactType,
			Annotations:  m.Annotations,
		})
	}
	// The inner registry iterates a map, so order is otherwise random and
	// pagination would drop or repeat entries between pages.
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	return out, nil
}

func (r *referrersRegistry) getJSON(path string, dst any) error {
	rec := httptest.NewRecorder()
	r.inner.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		return fmt.Errorf("inner registry GET %s: %d", path, rec.Code)
	}
	return json.Unmarshal(rec.Body.Bytes(), dst)
}

// parseReferrersPath matches /v2/<name>/referrers/<digest>, where <name> may
// itself contain slashes.
func parseReferrersPath(path string) (repo, target string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/v2/")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, "/referrers/")
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+len("/referrers/"):], true
}
