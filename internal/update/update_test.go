package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errWriter is an io.Writer that always returns an error.
type errWriter struct{}

func (*errWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write error") }

// sha256hex returns the hex-encoded SHA-256 of s, matching the checksums.txt
// format the updater verifies against.
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeChecksums writes a sha256sum-format line for the current platform binary
// asset to w (used by the test release servers).
func writeChecksums(w io.Writer, content string) {
	_, _ = fmt.Fprintf(w, "%s  %s\n", sha256hex(content), binaryAssetName())
}

// writePluginChecksums writes a sha256sum-format line for a plugin asset.
func writePluginChecksums(w io.Writer, name, content string) {
	ext := ""
	if runtimeGOOS == "windows" {
		ext = ".exe"
	}
	_, _ = fmt.Fprintf(w, "%s  %s_%s_%s%s\n", sha256hex(content), name, runtimeGOOS, runtimeGOARCH, ext)
}

func TestBuildDownloadURL(t *testing.T) {
	tests := []struct {
		name, tag, goos, goarch, expected string
	}{
		{"linux amd64", "v1.0.0", "linux", "amd64",
			githubDownloadURL + "/TrianaLab/pacto/releases/download/v1.0.0/pacto_linux_amd64"},
		{"darwin arm64", "v1.2.3", "darwin", "arm64",
			githubDownloadURL + "/TrianaLab/pacto/releases/download/v1.2.3/pacto_darwin_arm64"},
		{"windows amd64", "v1.0.0", "windows", "amd64",
			githubDownloadURL + "/TrianaLab/pacto/releases/download/v1.0.0/pacto_windows_amd64.exe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origGOOS, origGOARCH := runtimeGOOS, runtimeGOARCH
			runtimeGOOS, runtimeGOARCH = tt.goos, tt.goarch
			defer func() { runtimeGOOS, runtimeGOARCH = origGOOS, origGOARCH }()

			if got := buildDownloadURL(tt.tag); got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestValidateRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
		case "/repos/TrianaLab/pacto/releases/tags/v99.99.99":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	t.Run("existing release", func(t *testing.T) {
		if err := validateRelease("v1.0.0"); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := validateRelease("v99.99.99")
		if err == nil {
			t.Fatal("expected error for nonexistent release")
		}
		if err.Error() != "release v99.99.99 not found" {
			t.Errorf("unexpected error: %s", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		err := validateRelease("v0.0.1")
		if err == nil {
			t.Fatal("expected error for server error")
		}
	})
}

func TestDownloadBinary(t *testing.T) {
	content := "fake-binary-content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(content))
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-binary-*")
	if err != nil {
		t.Fatal(err)
	}

	if err := downloadBinary(server.URL+"/pacto_linux_amd64", tmpFile); err != nil {
		t.Fatalf("downloadBinary failed: %v", err)
	}
	_ = tmpFile.Close()

	got, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func TestDownloadBinary_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-binary-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmpFile.Close() }()

	if err := downloadBinary(server.URL+"/missing", tmpFile); err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestUpdate_LatestVersion(t *testing.T) {
	tmpDir := setupTestEnv(t)

	// Mock GitHub API: latest release + release validation + binary download
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v2.0.0"})
		case "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		case "/TrianaLab/pacto/releases/download/v2.0.0/checksums.txt":
			writeChecksums(w, "#!/bin/sh\necho updated")
		default:
			// Binary download
			_, _ = w.Write([]byte("#!/bin/sh\necho updated"))
		}
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	// Create a fake executable
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	result, err := Update("v1.0.0", "")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if result.PreviousVersion != "v1.0.0" {
		t.Errorf("expected previous v1.0.0, got %s", result.PreviousVersion)
	}
	if result.NewVersion != "v2.0.0" {
		t.Errorf("expected new v2.0.0, got %s", result.NewVersion)
	}

	// Verify the binary was replaced
	data, _ := os.ReadFile(execPath)
	if string(data) != "#!/bin/sh\necho updated" {
		t.Errorf("expected updated binary content, got %q", data)
	}

	// Verify cache was updated
	cacheData, _ := os.ReadFile(filepath.Join(tmpDir, "pacto", cacheFileName))
	var c cache
	if err := json.Unmarshal(cacheData, &c); err != nil {
		t.Fatal(err)
	}
	if c.LatestVersion != "v2.0.0" {
		t.Errorf("expected cache updated to v2.0.0, got %s", c.LatestVersion)
	}
}

func TestUpdate_SpecificVersion(t *testing.T) {
	setupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v1.5.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.5.0"})
		case "/TrianaLab/pacto/releases/download/v1.5.0/checksums.txt":
			writeChecksums(w, "new-binary")
		default:
			_, _ = w.Write([]byte("new-binary"))
		}
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	result, err := Update("v1.0.0", "1.5.0") // without v prefix
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if result.NewVersion != "v1.5.0" {
		t.Errorf("expected new v1.5.0, got %s", result.NewVersion)
	}
}

func TestUpdate_ReleaseNotFound(t *testing.T) {
	setupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	_, err := Update("v1.0.0", "v99.99.99")
	if err == nil {
		t.Fatal("expected error for nonexistent release")
	}
}

func TestUpdate_DownloadFailure(t *testing.T) {
	setupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	_, err := Update("v1.0.0", "v2.0.0")
	if err == nil {
		t.Fatal("expected error for download failure")
	}

	// Verify original binary is unchanged
	data, _ := os.ReadFile(execPath)
	if string(data) != "old" {
		t.Errorf("expected original binary to be unchanged, got %q", data)
	}
}

func TestUpdate_FetchLatestFailure(t *testing.T) {
	setupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	_, err := Update("v1.0.0", "")
	if err == nil {
		t.Fatal("expected error when fetching latest fails")
	}
}

func TestDownloadAndReplace_ExecutableError(t *testing.T) {
	origExec := osExecutable
	osExecutable = func() (string, error) { return "", fmt.Errorf("executable error") }
	defer func() { osExecutable = origExec }()

	err := downloadAndReplace("http://example.com/binary", "")
	if err == nil {
		t.Fatal("expected error from osExecutable")
	}
	if !strings.Contains(err.Error(), "failed to determine executable path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadAndReplace_EvalSymlinksError(t *testing.T) {
	origExec := osExecutable
	osExecutable = func() (string, error) { return "/nonexistent/path/pacto", nil }
	defer func() { osExecutable = origExec }()

	err := downloadAndReplace("http://example.com/binary", "")
	if err == nil {
		t.Fatal("expected error from EvalSymlinks")
	}
	if !strings.Contains(err.Error(), "failed to resolve executable path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadAndReplace_CreateTempError(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	err := downloadAndReplace("http://example.com/binary", "")
	if err == nil {
		t.Fatal("expected error from CreateTemp in read-only directory")
	}
	if !strings.Contains(err.Error(), "failed to create temp file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadAndReplace_ChmodError(t *testing.T) {
	origChmod := osChmod
	osChmod = func(string, fs.FileMode) error { return fmt.Errorf("chmod error") }
	defer func() { osChmod = origChmod }()

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new-binary"))
	}))
	t.Cleanup(server.Close)

	err := downloadAndReplace(server.URL+"/binary", sha256hex("new-binary"))
	if err == nil {
		t.Fatal("expected error from chmod")
	}
	if !strings.Contains(err.Error(), "failed to set executable permission") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadAndReplace_RenameError(t *testing.T) {
	origRename := osRename
	osRename = func(string, string) error { return fmt.Errorf("rename error") }
	defer func() { osRename = origRename }()

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new-binary"))
	}))
	t.Cleanup(server.Close)

	err := downloadAndReplace(server.URL+"/binary", sha256hex("new-binary"))
	if err == nil {
		t.Fatal("expected error from rename")
	}
	if !strings.Contains(err.Error(), "failed to replace binary") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRelease_InvalidURL(t *testing.T) {
	origURL := githubAPIBaseURL
	githubAPIBaseURL = string([]byte{0x7f})
	defer func() { githubAPIBaseURL = origURL }()

	err := validateRelease("v1.0.0")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestValidateRelease_TransportError(t *testing.T) {
	origClient := httpClient
	httpClient = &http.Client{Transport: &errorTransport{}}
	defer func() { httpClient = origClient }()

	err := validateRelease("v1.0.0")
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
	if !strings.Contains(err.Error(), "failed to check release") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadBinary_InvalidURL(t *testing.T) {
	var buf bytes.Buffer
	err := downloadBinary(string([]byte{0x7f}), &buf)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "failed to create download request") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDownloadBinary_TransportError(t *testing.T) {
	origClient := downloadClient
	downloadClient = &http.Client{Transport: &errorTransport{}}
	defer func() { downloadClient = origClient }()

	var buf bytes.Buffer
	err := downloadBinary("http://example.com/binary", &buf)
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
	if !strings.Contains(err.Error(), "failed to download binary") {
		t.Errorf("unexpected error: %v", err)
	}
}

// shortBodyTransport returns a response whose Content-Length is larger than the
// body actually delivered, simulating a truncated download.
type shortBodyTransport struct{}

func (*shortBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(strings.NewReader("short")),
		ContentLength: 100,
		Header:        make(http.Header),
	}, nil
}

func TestDownloadBinary_ContentLengthMismatch(t *testing.T) {
	origClient := downloadClient
	downloadClient = &http.Client{Transport: &shortBodyTransport{}}
	defer func() { downloadClient = origClient }()

	var buf bytes.Buffer
	err := downloadBinary("http://example.com/binary", &buf)
	if err == nil || !strings.Contains(err.Error(), "incomplete download") {
		t.Fatalf("expected incomplete download error, got %v", err)
	}
}

func TestParseChecksums(t *testing.T) {
	in := "abc123  pacto_linux_amd64\n" + // standard two-space form
		"def456 *pacto_windows_amd64.exe\n" + // binary-mode '*' prefix
		"\n" + // blank line ignored
		"garbage\n" // single field ignored
	got := parseChecksums(in)
	if got["pacto_linux_amd64"] != "abc123" {
		t.Errorf("linux: got %q", got["pacto_linux_amd64"])
	}
	if got["pacto_windows_amd64.exe"] != "def456" {
		t.Errorf("windows (*) prefix not stripped: got %q", got["pacto_windows_amd64.exe"])
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(path, sha256hex("hello")); err != nil {
		t.Errorf("expected match, got %v", err)
	}
	if err := verifyChecksum(path, "deadbeef"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected mismatch error, got %v", err)
	}
	if err := verifyChecksum(filepath.Join(dir, "nope"), "x"); err == nil || !strings.Contains(err.Error(), "failed to read downloaded file") {
		t.Errorf("expected read error, got %v", err)
	}
}

func TestFetchChecksums(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "abc  pacto_linux_amd64\n")
		}))
		t.Cleanup(server.Close)
		sums, err := fetchChecksums(server.URL + "/checksums.txt")
		if err != nil {
			t.Fatal(err)
		}
		if sums["pacto_linux_amd64"] != "abc" {
			t.Errorf("got %v", sums)
		}
	})

	t.Run("http error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(server.Close)
		if _, err := fetchChecksums(server.URL + "/checksums.txt"); err == nil {
			t.Fatal("expected error for 404")
		}
	})

	t.Run("invalid url", func(t *testing.T) {
		if _, err := fetchChecksums(string([]byte{0x7f})); err == nil {
			t.Fatal("expected error for invalid url")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		orig := httpClient
		httpClient = &http.Client{Transport: &errorTransport{}}
		defer func() { httpClient = orig }()
		if _, err := fetchChecksums("http://example.com/checksums.txt"); err == nil {
			t.Fatal("expected transport error")
		}
	})

	t.Run("body read error", func(t *testing.T) {
		orig := httpClient
		httpClient = &http.Client{Transport: &errorBodyTransport{}}
		defer func() { httpClient = orig }()
		if _, err := fetchChecksums("http://example.com/checksums.txt"); err == nil {
			t.Fatal("expected body read error")
		}
	})
}

func TestExpectedChecksum_MissingAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "abc  some-other-asset\n")
	}))
	t.Cleanup(server.Close)
	if _, err := expectedChecksum(server.URL+"/checksums.txt", "pacto_linux_amd64"); err == nil ||
		!strings.Contains(err.Error(), "no checksum published") {
		t.Fatalf("expected missing-asset error, got %v", err)
	}
}

func TestExpectedChecksum_FetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	if _, err := expectedChecksum(server.URL+"/checksums.txt", "x"); err == nil ||
		!strings.Contains(err.Error(), "failed to fetch release checksums") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestUpdate_ChecksumMismatch(t *testing.T) {
	setupTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			// Publish a checksum that does NOT match the served binary.
			_, _ = fmt.Fprintf(w, "%s  %s\n", sha256hex("the-real-binary"), binaryAssetName())
		default:
			_, _ = w.Write([]byte("a-tampered-binary"))
		}
	}))
	t.Cleanup(server.Close)
	overrideGitHubAPI(t, server)

	origDownload := githubDownloadURL
	githubDownloadURL = server.URL
	defer func() { githubDownloadURL = origDownload }()

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	origExec := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	defer func() { osExecutable = origExec }()

	_, err := Update("v1.0.0", "v2.0.0")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	// The running binary must be untouched.
	if data, _ := os.ReadFile(execPath); string(data) != "old" {
		t.Errorf("expected binary unchanged on checksum mismatch, got %q", data)
	}
}

func TestInstallFile_WindowsFallback(t *testing.T) {
	origGOOS, origRename, origRemove := runtimeGOOS, osRename, osRemove
	t.Cleanup(func() { runtimeGOOS, osRename, osRemove = origGOOS, origRename, origRemove })
	runtimeGOOS = "windows"
	osRemove = func(string) error { return nil }

	t.Run("fallback succeeds", func(t *testing.T) {
		var calls int
		osRename = func(_, _ string) error {
			calls++
			if calls == 1 {
				return fmt.Errorf("in use") // direct rename fails
			}
			return nil // move-aside + final rename succeed
		}
		if err := installFile("tmp", "target"); err != nil {
			t.Fatalf("expected fallback success, got %v", err)
		}
	})

	t.Run("move-aside fails", func(t *testing.T) {
		var calls int
		osRename = func(_, _ string) error {
			calls++
			return fmt.Errorf("fail %d", calls) // every rename fails
		}
		if err := installFile("tmp", "target"); err == nil ||
			!strings.Contains(err.Error(), "failed to move current executable aside") {
			t.Fatalf("expected move-aside error, got %v", err)
		}
	})

	t.Run("final rename fails and restores", func(t *testing.T) {
		var calls int
		osRename = func(_, _ string) error {
			calls++
			switch calls {
			case 1:
				return fmt.Errorf("in use") // direct rename fails
			case 2:
				return nil // move-aside succeeds
			default:
				return fmt.Errorf("swap failed") // final rename + restore
			}
		}
		if err := installFile("tmp", "target"); err == nil ||
			!strings.Contains(err.Error(), "failed to replace binary") {
			t.Fatalf("expected replace error, got %v", err)
		}
	})
}

func TestDownloadBinary_WriterError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("binary-content"))
	}))
	t.Cleanup(server.Close)

	err := downloadBinary(server.URL+"/binary", &errWriter{})
	if err == nil {
		t.Fatal("expected error from writer")
	}
	if !strings.Contains(err.Error(), "failed to write binary") {
		t.Errorf("unexpected error: %v", err)
	}
}
