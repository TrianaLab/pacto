//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestProcessGroupTerminal(t *testing.T) {
	if fd, fg := foregroundTerminal(strings.NewReader("")); fd != -1 || fg {
		t.Fatalf("a reader that is no file: %d %v", fd, fg)
	}
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, fg := foregroundTerminal(f); fg {
		t.Fatal("a regular file is no terminal")
	}
	c := exec.Command("true")
	ownGroup(c, 7, true)
	if !c.SysProcAttr.Setpgid || !c.SysProcAttr.Foreground || c.SysProcAttr.Ctty != 7 {
		t.Fatalf("attrs = %+v", c.SysProcAttr)
	}
	holdTerminal(-1, false)()
	// Taking back a descriptor that is no terminal fails quietly.
	holdTerminal(int(f.Fd()), true)()
}
