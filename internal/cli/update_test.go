package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/update"
)

// platformAsset returns the release asset name for the current platform, matching
// the updater's naming (prefix_<os>_<arch>[.exe]).
func platformAsset(prefix string) string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("%s_%s_%s%s", prefix, runtime.GOOS, runtime.GOARCH, ext)
}

// updateChecksums renders a sha256sum-format checksums.txt body covering the
// given assets, all with the SHA-256 of content.
func updateChecksums(content string, assets ...string) string {
	sum := sha256.Sum256([]byte(content))
	h := hex.EncodeToString(sum[:])
	var b strings.Builder
	for _, a := range assets {
		fmt.Fprintf(&b, "%s  %s\n", h, a)
	}
	return b.String()
}

// updaterFor points a real Updater at a test server. The command takes its
// updater as an argument, so a test hands it one instead of mutating package
// globals through an exported setter.
func updaterFor(server *httptest.Server) *update.Updater {
	u := update.New()
	u.APIBaseURL = server.URL
	u.DownloadBaseURL = server.URL
	u.Client = server.Client()
	u.DownloadClient = server.Client()
	return u
}

// runUpdate executes the update command against updater and returns everything
// it wrote to either stream, plus the error it exited with.
func runUpdate(t *testing.T, updater *update.Updater, args ...string) (string, error) {
	t.Helper()
	cmd := newUpdateCommand("v0.0.1", updater)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

// installedBinary writes a stand-in pacto binary and returns the path the
// updater should replace.
func installedBinary(t *testing.T) string {
	t.Helper()
	execPath := filepath.Join(t.TempDir(), "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	return execPath
}

// pinPluginDiscovery points plugin discovery at a temporary config directory
// holding exactly the named plugins. Discovery reads PATH and the user plugin
// directory, so both have to be pinned -- otherwise the developer's own
// installed plugins join the update run and the test reads their machine.
func pinPluginDiscovery(t *testing.T, names ...string) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("PATH", "")
	dir := filepath.Join(configHome, "pacto", "plugins")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, "pacto-plugin-"+n), []byte("old"), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateCommand_DevBuildRefuses(t *testing.T) {
	t.Parallel()

	cmd := newUpdateCommand("dev", update.New())
	cmd.SetArgs(nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for dev build")
	}
	if !strings.Contains(err.Error(), "cannot update a dev build") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdateCommand_Success(t *testing.T) {
	pinPluginDiscovery(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		case "/TrianaLab/pacto/releases/download/v2.0.0/checksums.txt":
			_, _ = w.Write([]byte(updateChecksums("new-binary", platformAsset("pacto"))))
		default:
			_, _ = w.Write([]byte("new-binary"))
		}
	}))
	t.Cleanup(server.Close)

	execPath := installedBinary(t)
	updater := updaterFor(server)
	updater.Executable = func() (string, error) { return execPath, nil }

	out, err := runUpdate(t, updater, "v2.0.0")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !strings.Contains(out, "Updated pacto v0.0.1 -> v2.0.0") {
		t.Errorf("expected success message, got: %s", out)
	}
}

func TestUpdateCommand_WithPlugins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
		case "/TrianaLab/pacto/releases/download/v2.0.0/checksums.txt":
			_, _ = w.Write([]byte(updateChecksums("new-binary", platformAsset("pacto"))))
		case "/TrianaLab/pacto-plugins/releases/download/v1.0.0/checksums.txt":
			_, _ = w.Write([]byte(updateChecksums("new-binary", platformAsset("pacto-plugin-foo"))))
		default:
			_, _ = w.Write([]byte("new-binary"))
		}
	}))
	t.Cleanup(server.Close)

	pinPluginDiscovery(t, "foo")
	execPath := installedBinary(t)
	updater := updaterFor(server)
	updater.Executable = func() (string, error) { return execPath, nil }

	out, err := runUpdate(t, updater, "v2.0.0")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !strings.Contains(out, "Updated pacto v0.0.1 -> v2.0.0") {
		t.Errorf("expected pacto update message, got: %s", out)
	}
	if !strings.Contains(out, "Updated plugin pacto-plugin-foo -> v1.0.0") {
		t.Errorf("expected plugin update message, got: %s", out)
	}
}

func TestUpdateCommand_PluginFailureIsWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		case "/repos/TrianaLab/pacto-plugins/releases/latest":
			w.WriteHeader(http.StatusInternalServerError)
		case "/TrianaLab/pacto/releases/download/v2.0.0/checksums.txt":
			_, _ = w.Write([]byte(updateChecksums("new-binary", platformAsset("pacto"))))
		default:
			_, _ = w.Write([]byte("new-binary"))
		}
	}))
	t.Cleanup(server.Close)

	pinPluginDiscovery(t, "foo")
	execPath := installedBinary(t)
	updater := updaterFor(server)
	updater.Executable = func() (string, error) { return execPath, nil }

	// A failing plugin update is a warning, not a failed pacto update.
	out, err := runUpdate(t, updater, "v2.0.0")
	if err != nil {
		t.Fatalf("update should succeed even if plugin update fails: %v", err)
	}
	if !strings.Contains(out, "Warning: plugin update failed") {
		t.Errorf("expected plugin warning, got: %s", out)
	}
}

func TestUpdateCommand_Error(t *testing.T) {
	pinPluginDiscovery(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	if _, err := runUpdate(t, updaterFor(server), "v99.99.99"); err == nil {
		t.Fatal("expected error for nonexistent release")
	}
}
