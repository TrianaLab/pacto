//go:build linux

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package ebpf

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// cgroupSession is a cgroup v2 directory and an open descriptor of it, which
// clone3(CLONE_INTO_CGROUP) starts the child in: the child is never outside
// the cgroup, not even between fork and exec (Linux 5.7 or later).
type cgroupSession struct {
	dir string
	fd  int
}

// NewSession creates the cgroup name under parent, a cgroup v2 directory the
// caller may write.
func NewSession(parent, name string) (Session, error) {
	if _, err := os.Stat(filepath.Join(parent, "cgroup.procs")); err != nil {
		return nil, fmt.Errorf("%s is not a cgroup v2 directory: %w", parent, err)
	}
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cgroup: %w", err)
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = os.Remove(dir)
		return nil, fmt.Errorf("open cgroup: %w", err)
	}
	return &cgroupSession{dir: dir, fd: fd}, nil
}

// Prepare implements Session.
func (s *cgroupSession) Prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = s.fd
}

// Close implements Session: a cgroup with processes left in it (a daemon the
// child started) cannot be removed, so they are killed (cgroup.kill, Linux
// 5.14 or later) and the removal retried for a second.
func (s *cgroupSession) Close() error {
	_ = unix.Close(s.fd)
	var err error
	for range 20 {
		if err = os.Remove(s.dir); err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		_ = os.WriteFile(filepath.Join(s.dir, "cgroup.kill"), []byte("1"), 0)
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("remove cgroup %s: %w", s.dir, err)
}

// SetSubreaper makes orphans of the calling process's descendants its
// children instead of init's, so a daemon the child forks stays in the tree
// the unprivileged mode of `pacto run` watches. It needs no privilege.
func SetSubreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}
