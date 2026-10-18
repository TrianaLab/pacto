package cli

import (
	"io"
	"os"
	"os/exec"
)

// Windows has no process groups or terminal ownership to arrange: the command shares the console and
// signals go to it alone.

func foregroundTerminal(io.Reader) (int, bool) { return -1, false }

func ownGroup(*exec.Cmd, int, bool) {}

func holdTerminal(int, bool) func() { return func() {} }

func signalGroup(p *os.Process, s os.Signal) error { return p.Signal(s) }
