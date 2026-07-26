//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/plugin"
)

// chdirMu serialises tests that need to change the working directory so they
// are safe to run alongside t.Parallel() subtests.
var chdirMu sync.Mutex

// cliExecMu serialises root.Execute() across parallel tests. The --no-anim
// decision is now per-command (carried on cmd.Context(), no longer a package
// global), but the CLI still writes ONE process-global during a run: the slog
// default logger. internal/cli.NewRootCommand's PersistentPreRunE calls
// logger.Setup -> slog.SetDefault, pointing the global logger at THIS command's
// stderr, and the app layer logs through slog.Default() (~40 call sites, e.g.
// internal/app.Service.Validate). Two commands executing concurrently — one with
// --verbose (which raises the global level to Debug) and another whose app code
// calls slog.Debug — race on the first command's output buffer. Holding this
// mutex for the duration of each Execute keeps the t.Parallel() tests race-free
// while their setup/IO still run in parallel. De-globalling slog (per-command
// logger threaded through context to every app call site) is a larger follow-up.
// Always acquired inside chdirMu/xdgMu (never the reverse), so no lock-ordering
// cycle exists.
var cliExecMu sync.Mutex

// inDir changes the working directory to dir for the duration of the current
// test/subtest. It acquires chdirMu so that parallel tests never race on CWD.
func inDir(t *testing.T, dir string) {
	t.Helper()
	chdirMu.Lock()
	orig, err := os.Getwd()
	if err != nil {
		chdirMu.Unlock()
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		chdirMu.Unlock()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chdir(orig)
		chdirMu.Unlock()
	})
}

// testRegistry wraps an ephemeral OCI registry for testing.
type testRegistry struct {
	server *httptest.Server
	host   string
	client *oci.Client
}

// newTestRegistry starts an ephemeral OCI registry and returns a testRegistry.
func newTestRegistry(t *testing.T) *testRegistry {
	t.Helper()

	handler := registry.New()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	host := strings.TrimPrefix(server.URL, "http://")

	client := oci.NewClient(
		authn.NewMultiKeychain(authn.DefaultKeychain),
		oci.WithNameOptions(name.Insecure),
	)

	return &testRegistry{
		server: server,
		host:   host,
		client: client,
	}
}

// runCommand executes a pacto CLI command with the default test version.
func runCommand(t *testing.T, reg *testRegistry, args ...string) (string, error) {
	t.Helper()
	return runCommandWithVersion(t, reg, "test-e2e", args...)
}

// runCommandWithVersion executes a pacto CLI command with the given version string.
func runCommandWithVersion(t *testing.T, reg *testRegistry, version string, args ...string) (string, error) {
	t.Helper()

	var store oci.BundleStore
	if reg != nil {
		store = reg.client
	}

	svc := app.NewService(store, &plugin.SubprocessRunner{})
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: version})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	cliExecMu.Lock()
	err := root.Execute()
	cliExecMu.Unlock()
	return out.String(), err
}

// runCommandWithCancelledCtx executes a pacto CLI command with a pre-cancelled
// context, useful for testing commands that start servers (--serve, --ui).
func runCommandWithCancelledCtx(t *testing.T, reg *testRegistry, args ...string) (string, error) {
	t.Helper()

	var store oci.BundleStore
	if reg != nil {
		store = reg.client
	}

	svc := app.NewService(store, &plugin.SubprocessRunner{})
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test-e2e"})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cliExecMu.Lock()
	err := root.ExecuteContext(ctx)
	cliExecMu.Unlock()
	return out.String(), err
}

// assertContains asserts that output contains the expected substring.
func assertContains(t *testing.T, output, expected string) {
	t.Helper()
	if !strings.Contains(output, expected) {
		t.Errorf("expected output to contain %q, got:\n%s", expected, output)
	}
}

// assertNotContains asserts that output does not contain the unexpected substring.
func assertNotContains(t *testing.T, output, unexpected string) {
	t.Helper()
	if strings.Contains(output, unexpected) {
		t.Errorf("expected output to NOT contain %q, got:\n%s", unexpected, output)
	}
}

// archiveEntries returns the list of file names contained in a tar.gz archive.
func archiveEntries(t *testing.T, archivePath string) []string {
	t.Helper()

	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive %s: %v", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer func() { _ = gr.Close() }()

	var names []string
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("error reading tar: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

// verifyArchiveContains checks that a tar.gz file at archivePath contains a file named expectedFile.
func verifyArchiveContains(t *testing.T, archivePath, expectedFile string) {
	t.Helper()
	for _, name := range archiveEntries(t, archivePath) {
		if name == expectedFile {
			return
		}
	}
	t.Errorf("expected %s in archive %s", expectedFile, archivePath)
}

// assertArchiveExcludes checks that a tar.gz file at archivePath does NOT contain
// a file named unexpectedFile. Mirrors verifyArchiveContains for ignored paths.
func assertArchiveExcludes(t *testing.T, archivePath, unexpectedFile string) {
	t.Helper()
	entries := archiveEntries(t, archivePath)
	for _, name := range entries {
		if name == unexpectedFile {
			t.Errorf("expected %s to be EXCLUDED from archive %s, entries: %v",
				unexpectedFile, archivePath, entries)
			return
		}
	}
}
