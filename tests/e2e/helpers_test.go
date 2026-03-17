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
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/cli"
	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/internal/plugin"
)

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
	root := cli.NewRootCommand(svc, version)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	err := root.Execute()
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
	root := cli.NewRootCommand(svc, "test-e2e")

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := root.ExecuteContext(ctx)
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

// verifyArchiveContains checks that a tar.gz file at archivePath contains a file named expectedFile.
func verifyArchiveContains(t *testing.T, archivePath, expectedFile string) {
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
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("error reading tar: %v", err)
		}
		if hdr.Name == expectedFile {
			return
		}
	}
	t.Errorf("expected %s in archive %s", expectedFile, archivePath)
}
