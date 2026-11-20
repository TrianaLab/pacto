package oci_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/oci"
)

const (
	subjectManifest  = `{"schemaVersion":2,"subject":{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:aa","size":1}}`
	plainManifest    = `{"schemaVersion":2}`
	referrerDigest   = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	installationRoot = "/acme/pacto"
)

// scoped runs a recording registry and returns a client whose transport is the
// scope guard over the registry's host, plus the host and a hit counter.
func scoped(t *testing.T, repos ...string) (*http.Client, string, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method == http.MethodPost {
			w.Header().Set("Location", "/v2/acme/app/blobs/uploads/abc")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	scope := oci.NewPushScope(host+installationRoot, repos...)
	return &http.Client{Transport: scope.Guard(nil)}, host, &hits
}

func send(t *testing.T, c *http.Client, method, url, body string) error {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestPushScopeInstallationPrefixes(t *testing.T) {
	s := oci.NewPushScope("Europe-Docker.pkg.dev/acme/pacto", "", "reg.example/streams", "reg.example/streams")
	want := []string{
		"europe-docker.pkg.dev/acme/pacto/pacto-index",
		"europe-docker.pkg.dev/acme/pacto/pacto-inferred",
		"europe-docker.pkg.dev/acme/pacto/pacto-targets",
		"reg.example/streams",
	}
	if got := s.Prefixes(); !slices.Equal(got, want) {
		t.Fatalf("prefixes = %v, want %v", got, want)
	}
	if got := oci.InstallationRepository("ghcr.io/acme/", oci.TargetsRepository); got != "ghcr.io/acme/pacto-targets" {
		t.Errorf("InstallationRepository = %q", got)
	}
}

func TestPushScopeAllowsExactAndNested(t *testing.T) {
	s := oci.NewPushScope("index.docker.io/acme")
	cases := map[string]bool{
		"docker.io/acme/pacto-inferred":        true,
		"docker.io/acme/pacto-inferred/stripe": true,
		"DOCKER.IO/acme/pacto-targets":         true,
		"docker.io/acme/pacto-inferredx":       false,
		"docker.io/acme/app":                   false,
		"ghcr.io/acme/pacto-index":             false,
	}
	for repo, want := range cases {
		if got := s.Allows(repo); got != want {
			t.Errorf("Allows(%q) = %v, want %v", repo, got, want)
		}
	}
}

func TestPushScopeRefusesTagOutside(t *testing.T) {
	c, host, hits := scoped(t)
	err := send(t, c, http.MethodPut, "http://"+host+"/v2/acme/app/manifests/v1", subjectManifest)
	if !errors.Is(err, oci.ErrOutsidePushScope) || *hits != 0 {
		t.Fatalf("tag outside: err=%v hits=%d", err, *hits)
	}
}

func TestPushScopeAllowsTagInside(t *testing.T) {
	c, host, hits := scoped(t)
	if err := send(t, c, http.MethodPut, "http://"+host+"/v2/acme/pacto/pacto-targets/manifests/latest", plainManifest); err != nil || *hits != 1 {
		t.Fatalf("tag inside: err=%v hits=%d", err, *hits)
	}
}

func TestPushScopeAllowsReferrerByDigestOutside(t *testing.T) {
	c, host, hits := scoped(t)
	if err := send(t, c, http.MethodPut, "http://"+host+"/v2/acme/app/manifests/"+referrerDigest, subjectManifest); err != nil || *hits != 1 {
		t.Fatalf("referrer outside: err=%v hits=%d", err, *hits)
	}
}

func TestPushScopeRefusesManifestWithoutSubjectOutside(t *testing.T) {
	c, host, _ := scoped(t)
	for name, body := range map[string]string{
		"no subject":     plainManifest,
		"null subject":   `{"subject":null}`,
		"empty subject":  `{"subject":{}}`,
		"subject number": `{"subject":5}`,
		"not json":       `{`,
	} {
		if err := send(t, c, http.MethodPut, "http://"+host+"/v2/acme/app/manifests/"+referrerDigest, body); !errors.Is(err, oci.ErrOutsidePushScope) {
			t.Errorf("%s: err = %v, want ErrOutsidePushScope", name, err)
		}
	}
}

// The guard cannot read a body it cannot replay, or one that fails to read.
func TestPushScopeRefusesUnreadableManifestOutside(t *testing.T) {
	c, host, _ := scoped(t)
	url := "http://" + host + "/v2/acme/app/manifests/" + referrerDigest
	noReplay, _ := http.NewRequest(http.MethodPut, url, io.NopCloser(strings.NewReader(subjectManifest)))
	getBodyFails, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(subjectManifest))
	getBodyFails.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("gone") }
	readFails, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(subjectManifest))
	readFails.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(failingReader{}), nil }
	for name, req := range map[string]*http.Request{"no GetBody": noReplay, "GetBody fails": getBodyFails, "read fails": readFails} {
		if _, err := c.Do(req); !errors.Is(err, oci.ErrOutsidePushScope) {
			t.Errorf("%s: err = %v, want ErrOutsidePushScope", name, err)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestPushScopeRefusesOversizedManifestOutside(t *testing.T) {
	c, host, hits := scoped(t)
	body := `{"subject":{"digest":"sha256:aa"},"pad":"` + strings.Repeat("x", 4<<20) + `"}`
	err := send(t, c, http.MethodPut, "http://"+host+"/v2/acme/app/manifests/"+referrerDigest, body)
	if !errors.Is(err, oci.ErrOutsidePushScope) || *hits != 0 {
		t.Fatalf("oversized: err=%v hits=%d", err, *hits)
	}
}

func TestPushScopeRefusesDeleteOutside(t *testing.T) {
	c, host, hits := scoped(t)
	for _, path := range []string{"/v2/acme/app/manifests/" + referrerDigest, "/v2/acme/app/blobs/" + referrerDigest} {
		if err := send(t, c, http.MethodDelete, "http://"+host+path, ""); !errors.Is(err, oci.ErrOutsidePushScope) {
			t.Errorf("DELETE %s: err = %v, want ErrOutsidePushScope", path, err)
		}
	}
	if err := send(t, c, http.MethodDelete, "http://"+host+"/v2/acme/pacto/pacto-inferred/x/blobs/"+referrerDigest, ""); err != nil {
		t.Errorf("DELETE inside: %v", err)
	}
	if *hits != 1 {
		t.Errorf("registry hits = %d, want only the in-scope delete", *hits)
	}
}

func TestPushScopeAllowsBlobUploadOutside(t *testing.T) {
	c, host, hits := scoped(t)
	base := "http://" + host
	for _, step := range []struct{ method, path string }{
		{http.MethodPost, "/v2/acme/app/blobs/uploads/"},
		{http.MethodPatch, "/v2/acme/app/blobs/uploads/abc"},
		{http.MethodPut, "/v2/acme/app/blobs/uploads/abc?digest=" + referrerDigest},
	} {
		if err := send(t, c, step.method, base+step.path, "blob"); err != nil {
			t.Fatalf("%s %s: %v", step.method, step.path, err)
		}
	}
	if *hits != 3 {
		t.Errorf("registry hits = %d, want 3", *hits)
	}
}

func TestPushScopeAllowsUploadContinuationOnLocationHost(t *testing.T) {
	c, _, _ := scoped(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	t.Cleanup(other.Close)
	if err := send(t, c, http.MethodPut, other.URL+"/storage/upload/abc?digest="+referrerDigest, "blob"); err != nil {
		t.Fatalf("continuation on the Location host: %v", err)
	}
}

func TestPushScopeAllowsReads(t *testing.T) {
	c, host, hits := scoped(t)
	for _, path := range []string{"/v2/", "/v2/acme/app/manifests/v1", "/v2/acme/app/tags/list", "/v2/acme/app/referrers/" + referrerDigest} {
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			if err := send(t, c, m, "http://"+host+path, ""); err != nil {
				t.Errorf("%s %s: %v", m, path, err)
			}
		}
	}
	// A PUT outside /v2/ and a manifest path with no name name nothing.
	for _, path := range []string{"/token", "/v2/manifests/v1", "/v2/acme/app/manifests/"} {
		if err := send(t, c, http.MethodPut, "http://"+host+path, "x"); err != nil {
			t.Errorf("PUT %s: %v", path, err)
		}
	}
	if *hits != 11 {
		t.Errorf("registry hits = %d, want 11", *hits)
	}
}

func TestPushScopeRefusesTagOnOtherHost(t *testing.T) {
	c, _, _ := scoped(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	t.Cleanup(other.Close)
	err := send(t, c, http.MethodPut, other.URL+"/v2/acme/pacto/pacto-targets/manifests/latest", plainManifest)
	if !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("tag on another host: err = %v, want ErrOutsidePushScope", err)
	}
}

func TestPushScopeErrorNamesPrefixes(t *testing.T) {
	c, host, _ := scoped(t, "reg.example/streams")
	err := send(t, c, http.MethodPut, "http://"+host+"/v2/acme/app/manifests/v1", plainManifest)
	want := "outside the installation push scope: PUT " + host + "/acme/app (allowed: " +
		host + "/acme/pacto/pacto-index, " + host + "/acme/pacto/pacto-inferred, " + host + "/acme/pacto/pacto-targets, reg.example/streams)"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("err = %v, want it to end with %q", err, want)
	}
}

func TestPushScopeEmptyRegistryIsReferrerOnly(t *testing.T) {
	s := oci.NewPushScope("")
	if len(s.Prefixes()) != 0 {
		t.Fatalf("prefixes = %v, want none", s.Prefixes())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	t.Cleanup(srv.Close)
	c := &http.Client{Transport: s.Guard(http.DefaultTransport)}
	err := send(t, c, http.MethodPut, srv.URL+"/v2/acme/pacto/pacto-targets/manifests/latest", plainManifest)
	if !errors.Is(err, oci.ErrOutsidePushScope) || !strings.HasSuffix(err.Error(), "(allowed: none)") {
		t.Fatalf("tag under an empty scope: err = %v", err)
	}
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v2/acme/app/manifests/"+referrerDigest, bytes.NewReader([]byte(subjectManifest)))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("referrer under an empty scope: %v", err)
	}
	_ = resp.Body.Close()
}
