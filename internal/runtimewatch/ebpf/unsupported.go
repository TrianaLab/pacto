//go:build !linux

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package ebpf is the kernel side of `pacto agent`. eBPF is Linux only: on
// every other platform New reports the collector unsupported.
package ebpf

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/trianalab/pacto/v4/internal/runtimewatch"
)

// ErrUnsupported is the collector's answer on a platform without eBPF.
var ErrUnsupported = errors.New("unsupported")

// New fails: kernel collection needs Linux.
func New(uint32) (runtimewatch.Collector, error) {
	return nil, fmt.Errorf("kernel collection is %w on %s: pacto agent needs Linux 5.11 or later with BTF, or 5.8 or later with root or CAP_SYS_RESOURCE",
		ErrUnsupported, runtime.GOOS)
}

// NewSession fails: cgroups are Linux only.
func NewSession(string, string) (Session, error) {
	return nil, fmt.Errorf("cgroup sessions are %w on %s", ErrUnsupported, runtime.GOOS)
}

// SetSubreaper fails: child subreapers are Linux only.
func SetSubreaper() error {
	return fmt.Errorf("child subreapers are %w on %s", ErrUnsupported, runtime.GOOS)
}
