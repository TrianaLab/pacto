//go:build !windows

package cli

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// foregroundTerminal returns the descriptor of stdin and whether it is a terminal this process's group
// is the foreground of: only then may the command's group take the terminal over.
func foregroundTerminal(stdin io.Reader) (int, bool) {
	f, ok := stdin.(*os.File)
	if !ok {
		return -1, false
	}
	fd := int(f.Fd())
	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return fd, err == nil && pgrp == syscall.Getpgrp()
}

// ownGroup starts the command in a process group of its own, so a terminal's Ctrl-C reaches it once: from
// the terminal itself when its group is made the foreground one, else from pacto run passing it on.
func ownGroup(c *exec.Cmd, tty int, foreground bool) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
	if foreground {
		c.SysProcAttr.Foreground, c.SysProcAttr.Ctty = true, tty
	}
}

// holdTerminal readies pacto run for a terminal its command's group owns: SIGTTOU is ignored, so neither a
// round written to it nor taking it back stops pacto run in the background. The returned function takes it
// back once the command is gone.
func holdTerminal(tty int, foreground bool) func() {
	if !foreground {
		return func() {}
	}
	signal.Ignore(syscall.SIGTTOU)
	return func() {
		_ = unix.IoctlSetPointerInt(tty, unix.TIOCSPGRP, syscall.Getpgrp())
		signal.Reset(syscall.SIGTTOU)
	}
}

// signalGroup passes a signal on to the command's whole process group.
func signalGroup(p *os.Process, s os.Signal) error {
	return syscall.Kill(-p.Pid, s.(syscall.Signal))
}
