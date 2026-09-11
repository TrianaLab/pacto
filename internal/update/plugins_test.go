package update

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pluginsOnPath writes fake plugin binaries into a temp dir and makes that dir
// the whole of $PATH, so plugin.Installed sees exactly these and nothing the
// developer happens to have installed.
func pluginsOnPath(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestBuildPluginDownloadURL(t *testing.T) {
	tests := []struct {
		name, tag, plugin, goos, goarch, expected string
	}{
		{"linux amd64", "v1.0.0", "foo", "linux", "amd64",
			"/TrianaLab/pacto-plugins/releases/download/v1.0.0/pacto-plugin-foo_linux_amd64"},
		{"darwin arm64", "v1.2.3", "bar", "darwin", "arm64",
			"/TrianaLab/pacto-plugins/releases/download/v1.2.3/pacto-plugin-bar_darwin_arm64"},
		{"windows amd64", "v1.0.0", "baz", "windows", "amd64",
			"/TrianaLab/pacto-plugins/releases/download/v1.0.0/pacto-plugin-baz_windows_amd64.exe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origGOOS, origGOARCH := runtimeGOOS, runtimeGOARCH
			runtimeGOOS, runtimeGOARCH = tt.goos, tt.goarch
			t.Cleanup(func() { runtimeGOOS, runtimeGOARCH = origGOOS, origGOARCH })

			u := New()
			if got := u.buildPluginDownloadURL(tt.tag, tt.plugin); got != u.DownloadBaseURL+tt.expected {
				t.Errorf("expected %s, got %s", u.DownloadBaseURL+tt.expected, got)
			}
		})
	}
}

func TestUpdatePlugins_Success(t *testing.T) {
	setupTestEnv(t)
	dir := pluginsOnPath(t, "pacto-plugin-foo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
		case "/TrianaLab/pacto-plugins/releases/download/v1.0.0/checksums.txt":
			writePluginChecksums(w, "pacto-plugin-foo", "new-plugin-binary")
		default:
			_, _ = w.Write([]byte("new-plugin-binary"))
		}
	}))
	t.Cleanup(server.Close)

	results, err := testUpdater(server).UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "pacto-plugin-foo" || results[0].Version != "v1.0.0" {
		t.Errorf("unexpected result: %+v", results[0])
	}

	data, _ := os.ReadFile(filepath.Join(dir, "pacto-plugin-foo"))
	if string(data) != "new-plugin-binary" {
		t.Errorf("expected updated binary, got %q", data)
	}
}

// The runner searches $PATH before ~/.config/pacto/plugins/, so a plugin
// present in both must be updated where it actually runs. Rewriting the
// shadowed copy and reporting success is the failure this pins.
func TestUpdatePlugins_UpdatesTheCopyTheRunnerExecutes(t *testing.T) {
	tmpDir := setupTestEnv(t)
	pathDir := pluginsOnPath(t, "pacto-plugin-foo")
	configPlugins := filepath.Join(tmpDir, "pacto", "plugins")
	if err := os.MkdirAll(configPlugins, 0755); err != nil {
		t.Fatal(err)
	}
	shadowed := filepath.Join(configPlugins, "pacto-plugin-foo")
	if err := os.WriteFile(shadowed, []byte("shadowed"), 0755); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
		case "/TrianaLab/pacto-plugins/releases/download/v1.0.0/checksums.txt":
			writePluginChecksums(w, "pacto-plugin-foo", "fresh")
		default:
			_, _ = w.Write([]byte("fresh"))
		}
	}))
	t.Cleanup(server.Close)

	results, err := testUpdater(server).UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the plugin to be reported once, got %+v", results)
	}
	if data, _ := os.ReadFile(filepath.Join(pathDir, "pacto-plugin-foo")); string(data) != "fresh" {
		t.Errorf("PATH copy not updated, got %q", data)
	}
	if data, _ := os.ReadFile(shadowed); string(data) != "shadowed" {
		t.Errorf("shadowed copy should be left alone, got %q", data)
	}
}

func TestUpdatePlugins_NoPlugins(t *testing.T) {
	setupTestEnv(t)
	pluginsOnPath(t)

	results, err := New().UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil, got %v", results)
	}
}

func TestUpdatePlugins_FetchVersionError(t *testing.T) {
	setupTestEnv(t)
	pluginsOnPath(t, "pacto-plugin-foo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	if _, err := testUpdater(server).UpdatePlugins(); err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdatePlugins_DownloadError(t *testing.T) {
	setupTestEnv(t)
	dir := pluginsOnPath(t, "pacto-plugin-foo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
		case "/TrianaLab/pacto-plugins/releases/download/v1.0.0/checksums.txt":
			writePluginChecksums(w, "pacto-plugin-foo", "new-plugin-binary")
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	if _, err := testUpdater(server).UpdatePlugins(); err == nil {
		t.Fatal("expected error")
	}

	// Verify original plugin is unchanged
	data, _ := os.ReadFile(filepath.Join(dir, "pacto-plugin-foo"))
	if string(data) != "old" {
		t.Errorf("expected original plugin to be unchanged, got %q", data)
	}
}

func TestUpdatePlugins_ChecksumsFetchError(t *testing.T) {
	setupTestEnv(t)
	pluginsOnPath(t, "pacto-plugin-foo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
		default:
			// checksums.txt (and anything else) errors.
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	if _, err := testUpdater(server).UpdatePlugins(); err == nil || !strings.Contains(err.Error(), "failed to fetch plugin checksums") {
		t.Fatalf("expected checksums fetch error, got %v", err)
	}
}

func TestUpdatePlugins_MissingChecksum(t *testing.T) {
	setupTestEnv(t)
	dir := pluginsOnPath(t, "pacto-plugin-foo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
		case "/TrianaLab/pacto-plugins/releases/download/v1.0.0/checksums.txt":
			// Checksums published, but not for our plugin.
			_, _ = io.WriteString(w, "abc  some-other-plugin_linux_amd64\n")
		default:
			_, _ = w.Write([]byte("new-plugin-binary"))
		}
	}))
	t.Cleanup(server.Close)

	if _, err := testUpdater(server).UpdatePlugins(); err == nil || !strings.Contains(err.Error(), "no checksum published for plugin") {
		t.Fatalf("expected missing-checksum error, got %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "pacto-plugin-foo")); string(data) != "old" {
		t.Errorf("expected plugin unchanged, got %q", data)
	}
}

func TestUpdatePlugins_WindowsExt(t *testing.T) {
	setupTestEnv(t)

	origGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	t.Cleanup(func() { runtimeGOOS = origGOOS })

	dir := pluginsOnPath(t, "pacto-plugin-foo.exe")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
		case "/TrianaLab/pacto-plugins/releases/download/v1.0.0/checksums.txt":
			writePluginChecksums(w, "pacto-plugin-foo", "new-plugin")
		default:
			_, _ = w.Write([]byte("new-plugin"))
		}
	}))
	t.Cleanup(server.Close)

	results, err := testUpdater(server).UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Name != "pacto-plugin-foo" {
		t.Errorf("unexpected results: %+v", results)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "pacto-plugin-foo.exe"))
	if string(data) != "new-plugin" {
		t.Errorf("expected updated binary, got %q", data)
	}
}

func TestFetchLatestRepoVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/TrianaLab/pacto-plugins/releases/latest" {
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v2.0.0"})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	version, err := testUpdater(server).fetchLatestRepoVersion("TrianaLab/pacto-plugins")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", version)
	}
}
