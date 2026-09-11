package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// errorTransport is an http.RoundTripper that always returns a connection error.
type errorTransport struct{}

func (*errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("connection refused")
}

// errorBodyTransport returns an HTTP 200 response with a body that errors on Read.
type errorBodyTransport struct{}

func (*errorBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(&errorReader{}),
		Header:     make(http.Header),
	}, nil
}

type errorReader struct{}

func (*errorReader) Read([]byte) (int, error) { return 0, fmt.Errorf("read error") }

// setupTestEnv sets up a temp config dir and returns its path.
func setupTestEnv(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	cacheDir := filepath.Join(tmpDir, "pacto")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

// writeFreshCache writes a cache with the given latest version and a recent timestamp.
func writeFreshCache(t *testing.T, tmpDir, version string) {
	t.Helper()
	c := cache{CheckedAt: time.Now(), LatestVersion: version}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(tmpDir, "pacto", cacheFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// writeStaleCache writes a cache with a timestamp older than cacheTTL.
func writeStaleCache(t *testing.T, tmpDir, version string) {
	t.Helper()
	c := cache{CheckedAt: time.Now().Add(-48 * time.Hour), LatestVersion: version}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(tmpDir, "pacto", cacheFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// startGitHubServer creates an httptest server that mocks the GitHub releases API.
func startGitHubServer(t *testing.T, latestTag string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: latestTag})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// testUpdater returns an Updater whose every endpoint points at server.
func testUpdater(server *httptest.Server) *Updater {
	u := New()
	u.APIBaseURL = server.URL
	u.DownloadBaseURL = server.URL
	u.Client = server.Client()
	u.DownloadClient = server.Client()
	return u
}

func TestCheckForUpdate_DevVersion(t *testing.T) {
	if result := CheckForUpdate("dev"); result != nil {
		t.Errorf("expected nil for dev version, got %+v", result)
	}
}

func TestCheckForUpdate_InvalidSemver(t *testing.T) {
	if result := CheckForUpdate("not-a-version"); result != nil {
		t.Errorf("expected nil for invalid semver, got %+v", result)
	}
}

func TestCheckForUpdate_FreshCacheSkipsHTTP(t *testing.T) {
	tmpDir := setupTestEnv(t)
	writeFreshCache(t, tmpDir, "v2.0.0")

	result := CheckForUpdate("v1.0.0")
	if result == nil {
		t.Fatal("expected update result, got nil")
	}
	if result.LatestVersion != "v2.0.0" {
		t.Errorf("expected latest v2.0.0, got %s", result.LatestVersion)
	}
}

func TestCheckForUpdate_StaleCacheFetchesFromGitHub(t *testing.T) {
	tmpDir := setupTestEnv(t)
	writeStaleCache(t, tmpDir, "v1.0.0")

	server := startGitHubServer(t, "v3.0.0")

	result := testUpdater(server).CheckForUpdate("v1.0.0")
	if result == nil {
		t.Fatal("expected update result, got nil")
	}
	if result.LatestVersion != "v3.0.0" {
		t.Errorf("expected latest v3.0.0, got %s", result.LatestVersion)
	}

	// Verify cache was updated
	data, err := os.ReadFile(filepath.Join(tmpDir, "pacto", cacheFileName))
	if err != nil {
		t.Fatal(err)
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.LatestVersion != "v3.0.0" {
		t.Errorf("expected cache to be updated to v3.0.0, got %s", c.LatestVersion)
	}
}

func TestCheckForUpdate_NoCacheFetchesFromGitHub(t *testing.T) {
	setupTestEnv(t)
	// No cache file written

	server := startGitHubServer(t, "v2.0.0")

	result := testUpdater(server).CheckForUpdate("v1.0.0")
	if result == nil {
		t.Fatal("expected update result, got nil")
	}
	if result.LatestVersion != "v2.0.0" {
		t.Errorf("expected latest v2.0.0, got %s", result.LatestVersion)
	}
}

func TestCheckForUpdate_AlreadyUpToDate(t *testing.T) {
	tmpDir := setupTestEnv(t)
	writeFreshCache(t, tmpDir, "v1.0.0")

	if result := CheckForUpdate("v1.0.0"); result != nil {
		t.Errorf("expected nil when already up-to-date, got %+v", result)
	}
}

func TestCheckForUpdate_NewerCurrentVersion(t *testing.T) {
	tmpDir := setupTestEnv(t)
	writeFreshCache(t, tmpDir, "v1.0.0")

	if result := CheckForUpdate("v2.0.0"); result != nil {
		t.Errorf("expected nil when current is newer, got %+v", result)
	}
}

func TestCheckForUpdate_NetworkErrorReturnsNil(t *testing.T) {
	setupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	if result := testUpdater(server).CheckForUpdate("v1.0.0"); result != nil {
		t.Errorf("expected nil on network error, got %+v", result)
	}
}

func TestCheckForUpdate_InvalidLatestSemver(t *testing.T) {
	tmpDir := setupTestEnv(t)
	writeFreshCache(t, tmpDir, "not-semver")

	if result := CheckForUpdate("v1.0.0"); result != nil {
		t.Errorf("expected nil for invalid latest semver, got %+v", result)
	}
}

func TestFetchLatestVersion(t *testing.T) {
	server := startGitHubServer(t, "v1.5.0")

	version, err := testUpdater(server).fetchLatestVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "v1.5.0" {
		t.Errorf("expected v1.5.0, got %s", version)
	}
}

func TestFetchLatestVersion_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	_, err := testUpdater(server).fetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestFetchLatestVersion_EmptyTagName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: ""})
	}))
	t.Cleanup(server.Close)

	_, err := testUpdater(server).fetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for empty tag_name")
	}
}

func TestFetchLatestVersion_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(server.Close)

	_, err := testUpdater(server).fetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteCache(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, cacheFileName)

	writeCache(path, "v1.2.3")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}

	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("failed to parse cache: %v", err)
	}
	if c.LatestVersion != "v1.2.3" {
		t.Errorf("expected v1.2.3, got %s", c.LatestVersion)
	}
}

func TestWriteCache_EmptyPath(t *testing.T) {
	writeCache("", "v1.0.0") // should not panic
}

func TestWriteCache_CreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", cacheFileName)

	writeCache(path, "v1.0.0")

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected cache file to be created: %v", err)
	}
}

func TestReadCache_InvalidJSON(t *testing.T) {
	tmpDir := setupTestEnv(t)
	if err := os.WriteFile(filepath.Join(tmpDir, "pacto", cacheFileName), []byte("{bad"), 0600); err != nil {
		t.Fatal(err)
	}

	c, path := readCache()
	if c != nil {
		t.Errorf("expected nil cache for invalid JSON, got %+v", c)
	}
	if path == "" {
		t.Error("expected non-empty path even on invalid JSON")
	}
}

func TestCachePath(t *testing.T) {
	setupTestEnv(t)
	p := cachePath()
	if p == "" {
		t.Error("expected non-empty cache path")
	}
	if !filepath.IsAbs(p) {
		t.Errorf("expected absolute path, got %s", p)
	}
}

func TestCachePath_ConfigDirError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "") // no XDG dir and no home: there is nowhere to put a cache

	if p := cachePath(); p != "" {
		t.Errorf("expected empty path, got %s", p)
	}
}

func TestReadCache_NoCachePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	c, path := readCache()
	if c != nil {
		t.Errorf("expected nil cache, got %+v", c)
	}
	if path != "" {
		t.Errorf("expected empty path, got %s", path)
	}
}

func TestFetchLatestVersion_InvalidURL(t *testing.T) {
	u := New()
	u.APIBaseURL = string([]byte{0x7f})

	_, err := u.fetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestFetchLatestVersion_TransportError(t *testing.T) {
	u := New()
	u.Client = &http.Client{Transport: &errorTransport{}}

	_, err := u.fetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
}

func TestFetchLatestVersion_ReadBodyError(t *testing.T) {
	u := New()
	u.Client = &http.Client{Transport: &errorBodyTransport{}}

	_, err := u.fetchLatestVersion()
	if err == nil {
		t.Fatal("expected error for body read failure")
	}
}
