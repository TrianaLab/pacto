package ocireferrer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// probeServer is a registry under opts whose requests are counted, wrapped by
// wrap when set, holding one artifact; it returns the repository and the
// artifact's digest.
type probeServer struct {
	srv      *httptest.Server
	requests atomic.Int64
	repo     *remote.Repository
	subject  string
}

func newProbeServer(t *testing.T, opts testutil.ReferrersOptions, wrap func(http.Handler) http.Handler) *probeServer {
	t.Helper()
	ps := &probeServer{}
	h := testutil.NewReferrersRegistry(opts)
	if wrap != nil {
		h = wrap(h)
	}
	ps.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.requests.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ps.srv.Close)
	ps.repo = ps.open(t, "team/app")
	art := Build("application/x-subject", "application/x-layer", []byte("s"), nil, nil)
	if err := Push(t.Context(), ps.repo, art); err != nil {
		t.Fatal(err)
	}
	ps.subject = art.ManifestDesc.Digest.String()
	ps.requests.Store(0)
	return ps
}

func (ps *probeServer) open(t *testing.T, name string) *remote.Repository {
	t.Helper()
	repo, err := Open(strings.TrimPrefix(ps.srv.URL, "http://")+"/"+name, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func probeOf(t *testing.T, opts testutil.ReferrersOptions) (capabilities.Capabilities, error) {
	t.Helper()
	ps := newProbeServer(t, opts, nil)
	return (&Prober{}).Probe(t.Context(), ps.repo, ps.subject, false)
}

func TestProbeNative(t *testing.T) {
	c, err := probeOf(t, testutil.ReferrersOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Referrers != capabilities.ReferrersNative || c.Listing != capabilities.ListingCatalog || c.Evidence() != "Referrers, _catalog" ||
		c.Mode() != "referrers=native listing=catalog" || c.Auth != "anonymous" || c.Host == "" || c.CheckedAt.IsZero() {
		t.Fatalf("native probe = %+v", c)
	}
}

func TestProbeWithoutReferrers(t *testing.T) {
	c, err := probeOf(t, testutil.ReferrersOptions{Unsupported: true, CatalogStatus: http.StatusNotFound})
	if err != nil {
		t.Fatal(err)
	}
	if c.Referrers != capabilities.ReferrersWriteOnceTags || c.Listing != capabilities.ListingTags || c.Evidence() != "no Referrers, no _catalog" {
		t.Fatalf("probe without Referrers = %+v", c)
	}
}

func TestProbeCatalogRefusalIsTags(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed} {
		c, err := probeOf(t, testutil.ReferrersOptions{CatalogStatus: status})
		if err != nil || c.Listing != capabilities.ListingTags {
			t.Errorf("catalog %d: %+v %v, want tags", status, c, err)
		}
	}
}

// noRetry stops oras-go's retrying client from backing off on a 5xx.
func noRetry(repo *remote.Repository) { repo.Client = &auth.Client{Client: http.DefaultClient} }

func TestProbeCatalogServerErrorFails(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{CatalogStatus: http.StatusInternalServerError}, nil)
	noRetry(ps.repo)
	if c, err := (&Prober{}).Probe(t.Context(), ps.repo, ps.subject, false); err == nil {
		t.Fatalf("a catalog 500 gave a mode: %+v", c)
	}
}

func TestProbeNetworkErrorIsNotAMode(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	ps.srv.Close()
	if c, err := (&Prober{}).Probe(t.Context(), ps.repo, ps.subject, false); err == nil {
		t.Fatalf("an unreachable registry gave a mode: %+v", c)
	}
}

func TestProbeNameUnknownIsNotAMode(t *testing.T) {
	if c, err := probeOf(t, testutil.ReferrersOptions{NameUnknown: true}); err == nil || !strings.Contains(err.Error(), "NAME_UNKNOWN") {
		t.Fatalf("NAME_UNKNOWN gave %+v %v", c, err)
	}
}

func TestProbeNonIndexContentTypeIsNotAMode(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{ReferrersContentType: "text/html"}, nil)
	now := time.Unix(1000, 0)
	p := &Prober{Now: func() time.Time { return now }}
	if _, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err == nil || !strings.Contains(err.Error(), "text/html") {
		t.Fatalf("an HTML referrers answer gave no error: %v", err)
	}
	now = now.Add(probeRetryAfter)
	before := ps.requests.Load()
	if _, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err == nil || ps.requests.Load() == before {
		t.Fatalf("the HTML answer was cached as a success: %v, %d requests", err, ps.requests.Load()-before)
	}
}

// TestProbeIndexWithParameterIsNotAMode: oras-go compares the content type
// exactly, so the probe does too and never says native for a listing the
// reader then refuses.
func TestProbeIndexWithParameterIsNotAMode(t *testing.T) {
	if c, err := probeOf(t, testutil.ReferrersOptions{ReferrersContentType: "application/vnd.oci.image.index.v1+json; charset=utf-8"}); err == nil {
		t.Fatalf("a parameterised content type gave %+v", c)
	}
}

// TestProbeFailureCarriesTheAttemptTime: a failure answered from the cache
// says when the probe was tried, not when it was asked again.
func TestProbeFailureCarriesTheAttemptTime(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{NameUnknown: true}, nil)
	tried := time.Unix(1000, 0)
	now := tried
	p := &Prober{Now: func() time.Time { return now }}
	if c, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err == nil || !c.CheckedAt.Equal(tried) {
		t.Fatalf("fresh failure = %+v %v", c, err)
	}
	now = tried.Add(probeRetryAfter / 2)
	if c, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err == nil || !c.CheckedAt.Equal(tried) {
		t.Fatalf("cached failure = %+v %v", c, err)
	}
}

func TestProbeReferrersServerErrorIsNotAMode(t *testing.T) {
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/referrers/") {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			h.ServeHTTP(w, r)
		})
	}
	ps := newProbeServer(t, testutil.ReferrersOptions{}, wrap)
	noRetry(ps.repo)
	if _, err := (&Prober{}).Probe(t.Context(), ps.repo, ps.subject, false); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("a referrers 502 gave %v", err)
	}
}

func TestProbeMissingSubjectFails(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	if _, err := (&Prober{}).Probe(t.Context(), ps.repo, "missing", false); err == nil {
		t.Fatal("a missing subject gave a mode")
	}
}

func TestProbeAuthScheme(t *testing.T) {
	cases := map[string]string{"": "anonymous", `Bearer realm="https://auth"`: "bearer", `Basic realm="r"`: "basic", "Negotiate": "unknown"}
	for challenge, want := range cases {
		c, err := probeOf(t, testutil.ReferrersOptions{Challenge: challenge})
		if err != nil || c.Auth != want {
			t.Errorf("challenge %q: %+v %v, want %s", challenge, c, err, want)
		}
	}
}

func TestProbeAuthClients(t *testing.T) {
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	defer srv.Close()
	repo := &remote.Repository{PlainHTTP: true, Reference: registry.Reference{Registry: strings.TrimPrefix(srv.URL, "http://")}}
	for _, c := range []remote.Client{&auth.Client{}, http.DefaultClient} {
		if got := probeAuth(t.Context(), c, repo); got != "anonymous" {
			t.Errorf("probeAuth through %T = %s", c, got)
		}
	}
	srv.Close()
	if got := probeAuth(t.Context(), http.DefaultClient, repo); got != "unknown" {
		t.Errorf("probeAuth of a closed server = %s", got)
	}
	if _, err := probeReferrers(t.Context(), http.DefaultClient, repo, Descriptor("x", nil)); err == nil {
		t.Error("probeReferrers of a closed server succeeded")
	}
	bad := &remote.Repository{Reference: registry.Reference{Registry: "%zz"}}
	if got := probeAuth(t.Context(), http.DefaultClient, bad); got != "unknown" {
		t.Errorf("probeAuth of a bad URL = %s", got)
	}
	if _, err := probeReferrers(t.Context(), http.DefaultClient, bad, Descriptor("x", nil)); err == nil {
		t.Error("probeReferrers of a bad URL succeeded")
	}
}

func TestProbeWithoutAClient(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	repo := &remote.Repository{PlainHTTP: true, Reference: ps.repo.Reference}
	if c, err := (&Prober{}).Probe(t.Context(), repo, ps.subject, false); err != nil || c.Referrers != capabilities.ReferrersNative {
		t.Fatalf("probe through the default client = %+v %v", c, err)
	}
}

func TestProbeReadOnlyArtifactTypeUnknown(t *testing.T) {
	c, err := probeOf(t, testutil.ReferrersOptions{})
	if err != nil || c.ArtifactType != capabilities.ArtifactTypeUnknown {
		t.Fatalf("read-only probe artifactType = %q %v", c.ArtifactType, err)
	}
}

func TestProbePushedUpgradesArtifactType(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	p := &Prober{}
	if c, _ := p.Probe(t.Context(), ps.repo, ps.subject, false); c.ArtifactType != capabilities.ArtifactTypeUnknown {
		t.Fatalf("read-only probe = %+v", c)
	}
	before := ps.requests.Load()
	c, err := p.Probe(t.Context(), ps.repo, ps.subject, true)
	if err != nil || c.ArtifactType != capabilities.ArtifactTypeAccepted || ps.requests.Load() != before {
		t.Fatalf("pushed probe = %+v %v after %d requests", c, err, ps.requests.Load()-before)
	}
	if c, _ := p.Probe(t.Context(), ps.repo, ps.subject, false); c.ArtifactType != capabilities.ArtifactTypeAccepted {
		t.Fatalf("the upgrade was not kept: %+v", c)
	}
	// A first probe that pushed is accepted straight away.
	if c, _ := (&Prober{}).Probe(t.Context(), ps.repo, ps.subject, true); c.ArtifactType != capabilities.ArtifactTypeAccepted {
		t.Fatalf("first pushed probe = %+v", c)
	}
}

func TestProbeCachedPerHost(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	p := &Prober{}
	if _, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err != nil {
		t.Fatal(err)
	}
	first := ps.requests.Load()
	if _, err := p.Probe(t.Context(), ps.open(t, "team/other"), "missing", false); err != nil {
		t.Fatal(err)
	}
	if ps.requests.Load() != first {
		t.Fatalf("a second repository on the host probed again: %d requests", ps.requests.Load()-first)
	}
}

// TestProbeFailureIsPerRepository: a probe that fails in one repository
// (a subject the caller cannot read there) never fails a probe of another
// repository on the host.
func TestProbeFailureIsPerRepository(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	p := &Prober{}
	if _, err := p.Probe(t.Context(), ps.open(t, "team/other"), "missing", false); err == nil {
		t.Fatal("a missing subject probed")
	}
	if c, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err != nil || c.Referrers != capabilities.ReferrersNative {
		t.Fatalf("another repository's failure was reused: %+v %v", c, err)
	}
}

func TestProbeErrorRetriedAfterAMinute(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{NameUnknown: true}, nil)
	now := time.Unix(1000, 0)
	p := &Prober{Now: func() time.Time { return now }}
	_, _ = p.Probe(t.Context(), ps.repo, ps.subject, false)
	before := ps.requests.Load()
	now = now.Add(probeRetryAfter - time.Second)
	if _, err := p.Probe(t.Context(), ps.repo, ps.subject, true); err == nil || ps.requests.Load() != before {
		t.Fatalf("a remembered error probed again: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err == nil || ps.requests.Load() == before {
		t.Fatalf("an error a minute old was not probed again: %v", err)
	}
}

func TestProbeCancelledIsNotRemembered(t *testing.T) {
	ps := newProbeServer(t, testutil.ReferrersOptions{}, nil)
	p := &Prober{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Probe(ctx, ps.repo, ps.subject, false); err == nil {
		t.Fatal("a cancelled probe succeeded")
	}
	if c, err := p.Probe(t.Context(), ps.repo, ps.subject, false); err != nil || c.Referrers != capabilities.ReferrersNative {
		t.Fatalf("the cancelled probe was remembered: %+v %v", c, err)
	}
}

func TestProbeUsesTheRepositoryClient(t *testing.T) {
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u, pw, ok := r.BasicAuth(); !ok || u != "u" || pw != "p" {
				w.Header().Set("WWW-Authenticate", `Basic realm="r"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(w, r)
		})
	}
	ps := &probeServer{}
	inner := wrap(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	ps.srv = httptest.NewServer(inner)
	t.Cleanup(ps.srv.Close)
	ps.repo = ps.open(t, "team/app")
	ps.repo.Client = &auth.Client{Credential: auth.StaticCredential(ps.repo.Reference.Registry, auth.Credential{Username: "u", Password: "p"})}
	art := Build("application/x-subject", "application/x-layer", []byte("s"), nil, nil)
	if err := Push(t.Context(), ps.repo, art); err != nil {
		t.Fatal(err)
	}
	c, err := (&Prober{}).Probe(t.Context(), ps.repo, art.ManifestDesc.Digest.String(), false)
	if err != nil || c.Referrers != capabilities.ReferrersNative || c.Auth != "basic" {
		t.Fatalf("probe with credentials = %+v %v", c, err)
	}
}
