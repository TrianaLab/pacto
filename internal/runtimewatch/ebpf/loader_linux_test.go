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
	"strings"
	"syscall"
	"testing"

	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

func TestLoaderStatus(t *testing.T) {
	l := newLoader(0)
	if st := l.Status(); !st.Failed || st.Reason != ReasonAttaching || !st.AttachedAt.IsZero() {
		t.Fatalf("before the first attach = %+v", st)
	}

	l.set(&probe{}, "", nil)
	select {
	case <-l.attached:
	default:
		t.Fatal("attach not signalled")
	}
	if l.status.Failed || l.status.AttachedAt.IsZero() || l.status.DNSState != runtimefacts.StateRunning {
		t.Fatalf("attached = %+v", l.status)
	}
	l.set(&probe{}, "", nil) // a second signal while the first is unread never blocks
	l.set(&probe{}, "", nil)

	// A probe with no kernel objects cannot report drops; detach it by hand.
	l.current = nil
	l.set(nil, reasonReaderFailed, errors.New("read: EBADF"))
	st := l.Status()
	if !st.Failed || st.Reason != reasonReaderFailed || st.Dropped != 1 || !st.AttachedAt.IsZero() ||
		st.DNSState != runtimefacts.StateRunning {
		t.Fatalf("after a reader failure = %+v (the DNS fields outlive a detach)", st)
	}
}

// TestLoaderStatusDNSBeforeAttach: until the first attach DNS is never Running.
func TestLoaderStatusDNSBeforeAttach(t *testing.T) {
	if st := newLoader(0).Status(); st.DNSState != runtimefacts.StateFailed || st.DNSReason != runtimewatch.ReasonDNSNotAttached {
		t.Fatalf("before the first attach = %+v", st)
	}
}

// TestLoaderStatusDNSAttachFailed: a probe whose DNS step failed runs sockets
// and reports why DNS is down.
func TestLoaderStatusDNSAttachFailed(t *testing.T) {
	l := newLoader(0)
	l.set(&probe{dnsReason: reasonDNSAttachFailed, dnsErr: errors.New("no BTF for skb_consume_udp")}, "", nil)
	if l.status.Failed || l.status.DNSState != runtimefacts.StateFailed || l.status.DNSReason != reasonDNSAttachFailed {
		t.Fatalf("DNS attach failed = %+v", l.status)
	}
}

func TestLoadError(t *testing.T) {
	eperm := fmt.Errorf("map create: %w", syscall.EPERM)
	memlock := errors.New("operation not permitted")
	for name, tc := range map[string]struct {
		err, memlock error
		floor        bool
	}{
		"EPERM, memlock stuck":  {eperm, memlock, true},
		"ENOMEM, memlock stuck": {fmt.Errorf("x: %w", syscall.ENOMEM), memlock, true},
		"EPERM, memlock lifted": {eperm, nil, false},
		"other error":           {errors.New("no BTF"), memlock, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := loadError(tc.err, tc.memlock)
			if !errors.Is(got, tc.err) || strings.Contains(got.Error(), "Linux >= 5.11") != tc.floor {
				t.Fatalf("loadError = %v", got)
			}
		})
	}
}

func TestMonotonicNs(t *testing.T) {
	if a, b := monotonicNs(), monotonicNs(); a == 0 || b < a {
		t.Fatalf("monotonicNs = %d then %d", a, b)
	}
}
