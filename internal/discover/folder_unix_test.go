//go:build !windows

package discover

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestDiscoverySkipsSymlinkAndFIFO(t *testing.T) {
	outside := writeFiles(t, map[string]string{"secret.yaml": "openapi: 3.0.0\n"})
	dir := writeFiles(t, map[string]string{"api/README": ""})
	if err := os.Symlink(filepath.Join(outside, "secret.yaml"), filepath.Join(dir, "api", "openapi.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "Dockerfile"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan Reading, 1)
	go func() { done <- readFixture(t, dir) }()
	select {
	case r := <-done:
		want := []contract.InferredFinding{
			{Code: CodeUnreadable, Source: "Dockerfile", Message: "Dockerfile is not a regular file, so it is not read"},
			{Code: CodeUnreadable, Source: "api/openapi.yaml", Message: "api/openapi.yaml is not a regular file, so it is not read"},
		}
		if !slices.Equal(r.Findings, want) || len(r.Fields) != 0 {
			t.Errorf("findings = %+v, fields = %+v", r.Findings, r.Fields)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery opened the FIFO")
	}
}

func TestCodeownersSymlinkNotRead(t *testing.T) {
	outside := writeFiles(t, map[string]string{"owners": "* @acme/evil\n"})
	repo := writeFiles(t, map[string]string{".github/README": "", "CODEOWNERS": "* @acme/a\n", "services/payments/README": ""})
	if err := os.Symlink(filepath.Join(outside, "owners"), filepath.Join(repo, ".github", "CODEOWNERS")); err != nil {
		t.Fatal(err)
	}
	r := readIn(t, repo, "services/payments")
	want := []contract.InferredFinding{{Code: CodeUnreadable, Source: "repository:.github/CODEOWNERS", Message: "repository:.github/CODEOWNERS is not a regular file, so it is not read"}}
	if owner(r) != nil || !slices.Equal(r.Findings, want) {
		t.Errorf("owner = %v, findings = %+v", owner(r), r.Findings)
	}
}

// A repository root Pacto cannot open, though the folder inside it opens, is
// an error, never a read with no CODEOWNERS.
func TestReadDirUnreadableRepository(t *testing.T) {
	repo := writeFiles(t, map[string]string{"svc/Dockerfile": "FROM x\n"})
	if err := os.Chmod(repo, 0o100); err != nil { // search, no read
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
	if _, err := ReadDir(filepath.Join(repo, "svc"), repo); err == nil {
		t.Error("an unreadable repository root read")
	}
}
