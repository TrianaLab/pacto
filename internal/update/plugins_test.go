package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPluginDownloadURL(t *testing.T) {
	tests := []struct {
		name, tag, plugin, goos, goarch, expected string
	}{
		{"linux amd64", "v1.0.0", "pacto-plugin-foo", "linux", "amd64",
			githubDownloadURL + "/TrianaLab/pacto-plugins/releases/download/v1.0.0/pacto-plugin-foo_linux_amd64"},
		{"darwin arm64", "v1.2.3", "pacto-plugin-bar", "darwin", "arm64",
			githubDownloadURL + "/TrianaLab/pacto-plugins/releases/download/v1.2.3/pacto-plugin-bar_darwin_arm64"},
		{"windows amd64", "v1.0.0", "pacto-plugin-baz", "windows", "amd64",
			githubDownloadURL + "/TrianaLab/pacto-plugins/releases/download/v1.0.0/pacto-plugin-baz_windows_amd64.exe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origGOOS, origGOARCH := runtimeGOOS, runtimeGOARCH
			runtimeGOOS, runtimeGOARCH = tt.goos, tt.goarch
			defer func() { runtimeGOOS, runtimeGOARCH = origGOOS, origGOARCH }()

			if got := buildPluginDownloadURL(tt.tag, tt.plugin); got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestDiscoverInstalledPlugins(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create plugin binaries
	for _, name := range []string{"pacto-plugin-alpha", "pacto-plugin-beta"} {
		if err := os.WriteFile(filepath.Join(execDir, name), []byte("plugin"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Create a non-plugin file (should be ignored)
	if err := os.WriteFile(filepath.Join(execDir, "other-binary"), []byte("other"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create a directory with plugin-like name (should be ignored)
	if err := os.Mkdir(filepath.Join(execDir, "pacto-plugin-dir"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	plugins, dir, err := discoverInstalledPlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// EvalSymlinks may resolve /var -> /private/var on macOS
	resolvedExecDir, _ := filepath.EvalSymlinks(execDir)
	if dir != resolvedExecDir {
		t.Errorf("expected dir %s, got %s", resolvedExecDir, dir)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d: %v", len(plugins), plugins)
	}
	if plugins[0] != "pacto-plugin-alpha" || plugins[1] != "pacto-plugin-beta" {
		t.Errorf("unexpected plugins: %v", plugins)
	}
}

func TestDiscoverInstalledPlugins_WindowsExe(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo.exe"), []byte("plugin"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	plugins, _, err := discoverInstalledPlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 1 || plugins[0] != "pacto-plugin-foo" {
		t.Errorf("expected [pacto-plugin-foo], got %v", plugins)
	}
}

func TestDiscoverInstalledPlugins_NoPlugins(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	plugins, _, err := discoverInstalledPlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("expected no plugins, got %v", plugins)
	}
}

func TestDiscoverInstalledPlugins_ExecutableError(t *testing.T) {
	origExec := osExecutable
	osExecutable = func() (string, error) { return "", fmt.Errorf("exec error") }
	defer func() { osExecutable = origExec }()

	_, _, err := discoverInstalledPlugins()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverInstalledPlugins_EvalSymlinksError(t *testing.T) {
	origExec := osExecutable
	osExecutable = func() (string, error) { return "/nonexistent/path/pacto", nil }
	defer func() { osExecutable = origExec }()

	_, _, err := discoverInstalledPlugins()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverInstalledPlugins_ReadDirError(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	origReadDir := osReadDir
	osReadDir = func(string) ([]os.DirEntry, error) { return nil, fmt.Errorf("read dir error") }
	defer func() { osReadDir = origReadDir }()

	_, _, err := discoverInstalledPlugins()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdatePlugins_Success(t *testing.T) {
	setupTestEnv(t)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo"), []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

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
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	results, err := UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "pacto-plugin-foo" || results[0].Version != "v1.0.0" {
		t.Errorf("unexpected result: %+v", results[0])
	}

	data, _ := os.ReadFile(filepath.Join(execDir, "pacto-plugin-foo"))
	if string(data) != "new-plugin-binary" {
		t.Errorf("expected updated binary, got %q", data)
	}
}

func TestUpdatePlugins_NoPlugins(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	results, err := UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil, got %v", results)
	}
}

func TestUpdatePlugins_DiscoverError(t *testing.T) {
	origExec := osExecutable
	osExecutable = func() (string, error) { return "", fmt.Errorf("exec error") }
	defer func() { osExecutable = origExec }()

	_, err := UpdatePlugins()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdatePlugins_FetchVersionError(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo"), []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	_, err := UpdatePlugins()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdatePlugins_DownloadError(t *testing.T) {
	setupTestEnv(t)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo"), []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

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
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	_, err := UpdatePlugins()
	if err == nil {
		t.Fatal("expected error")
	}

	// Verify original plugin is unchanged
	data, _ := os.ReadFile(filepath.Join(execDir, "pacto-plugin-foo"))
	if string(data) != "old" {
		t.Errorf("expected original plugin to be unchanged, got %q", data)
	}
}

func TestUpdatePlugins_ChecksumsFetchError(t *testing.T) {
	setupTestEnv(t)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo"), []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

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
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	if _, err := UpdatePlugins(); err == nil || !strings.Contains(err.Error(), "failed to fetch plugin checksums") {
		t.Fatalf("expected checksums fetch error, got %v", err)
	}
}

func TestUpdatePlugins_MissingChecksum(t *testing.T) {
	setupTestEnv(t)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo"), []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

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
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	if _, err := UpdatePlugins(); err == nil || !strings.Contains(err.Error(), "no checksum published for plugin") {
		t.Fatalf("expected missing-checksum error, got %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(execDir, "pacto-plugin-foo")); string(data) != "old" {
		t.Errorf("expected plugin unchanged, got %q", data)
	}
}

func TestUpdatePlugins_WindowsExt(t *testing.T) {
	setupTestEnv(t)

	origGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	defer func() { runtimeGOOS = origGOOS }()

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "pacto-plugin-foo.exe"), []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

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
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	results, err := UpdatePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Name != "pacto-plugin-foo" {
		t.Errorf("unexpected results: %+v", results)
	}

	data, _ := os.ReadFile(filepath.Join(execDir, "pacto-plugin-foo.exe"))
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
	overrideGitHubAPI(t, server)

	version, err := fetchLatestRepoVersion("TrianaLab/pacto-plugins")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", version)
	}
}
