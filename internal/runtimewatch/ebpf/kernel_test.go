//go:build ebpf && linux

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package ebpf

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/runtimewatch"
	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

// cgroupRoot is where the test reads its own cgroup: in a container with a
// private cgroup namespace (Docker's default on cgroup v2) the container's
// cgroup is mounted here and /proc/self/cgroup reads "0::/".
const cgroupRoot = "/sys/fs/cgroup"

// ownCgroupDir is the directory of the cgroup this process is in.
func ownCgroupDir(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join(cgroupRoot, p)
		}
	}
	t.Skip("not on cgroup v2")
	return ""
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

// moveSelf moves this whole process (every thread) into the cgroup at dir.
func moveSelf(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		t.Fatalf("move into %s: %v", dir, err)
	}
}

// kernelHarness reads the attached program's events and checks the stamps
// every event of this process's namespace must carry.
type kernelHarness struct {
	events     chan runtimewatch.Event
	ns         uint32
	start      uint64
	cookie     uint64
	wantCgroup uint64
}

// checkStamp: one non-zero cookie for the namespace (net_cookie exists on
// every kernel this runs on: >= 5.12), a kernel timestamp on the clock of
// monotonicNs and the cgroup the socket was created in.
func (h *kernelHarness) checkStamp(t *testing.T, ev runtimewatch.Event) {
	t.Helper()
	if ev.NetnsCookie == 0 {
		t.Errorf("event %+v has no netns cookie", ev)
	}
	if h.cookie == 0 {
		h.cookie = ev.NetnsCookie
	} else if ev.NetnsCookie != h.cookie {
		t.Errorf("netns cookie changed: %d then %d", h.cookie, ev.NetnsCookie)
	}
	if now := monotonicNs(); ev.Mono < h.start || ev.Mono > now {
		t.Errorf("event ktime %d outside [%d, %d]", ev.Mono, h.start, now)
	}
	if ev.CgroupID != h.wantCgroup {
		t.Errorf("event cgroup id %d, want %d (the inode of the socket owner's cgroup)", ev.CgroupID, h.wantCgroup)
	}
}

// await returns the first event of the kind in this namespace that matches.
func (h *kernelHarness) await(t *testing.T, kind runtimewatch.EventKind, match func(runtimewatch.Event) bool) runtimewatch.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-h.events:
			if ev.Kind == kind && ev.Netns == h.ns && match(ev) {
				t.Logf("event %+v", ev)
				h.checkStamp(t, ev)
				return ev
			}
			if ev.Netns == h.ns {
				t.Logf("skipped %+v", ev)
			}
		case <-deadline:
			t.Fatalf("no kind=%d event within 5s", kind)
		}
	}
}

// transitions drives LISTEN, CONNECT_OK, UNLISTEN and CONNECT_FAIL for IPv4
// and (when the namespace has it) IPv6.
func (h *kernelHarness) transitions(t *testing.T) {
	for _, tc := range []struct {
		network, host string
		ipv6          bool
	}{
		{"tcp4", "127.0.0.1", false},
		{"tcp6", "::1", true},
	} {
		t.Run(tc.network, func(t *testing.T) { h.transition(t, tc.network, tc.host, tc.ipv6) })
	}
}

func acceptAll(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
	}
}

func (h *kernelHarness) transition(t *testing.T, network, host string, ipv6 bool) {
	ln, err := net.Listen(network, net.JoinHostPort(host, "0"))
	if err != nil {
		if ipv6 {
			t.Skipf("no IPv6 loopback here: %v", err)
		}
		t.Fatal(err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	addr := netip.MustParseAddr(host)
	go acceptAll(ln)

	ev := h.await(t, runtimewatch.EventListen, func(e runtimewatch.Event) bool { return e.LocalPort == port })
	if ev.IPv6 != ipv6 || ev.LocalAddr != addr || ev.Comm == "" {
		t.Errorf("LISTEN = %+v, want ipv6=%v addr=%s and a comm", ev, ipv6, addr)
	}
	checkProcListener(t, runtimewatch.ListenerKey{IPv6: ipv6, Addr: addr, Port: port})

	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	c, err := net.Dial(network, target)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	ev = h.await(t, runtimewatch.EventConnectOK, func(e runtimewatch.Event) bool { return e.RemotePort == port })
	if ev.RemoteAddr != addr {
		t.Errorf("CONNECT_OK remote = %s, want %s", ev.RemoteAddr, addr)
	}

	_ = ln.Close()
	h.await(t, runtimewatch.EventUnlisten, func(e runtimewatch.Event) bool { return e.LocalPort == port })

	if c, err := net.Dial(network, target); err == nil {
		_ = c.Close()
		t.Fatalf("dial %s succeeded after the listener closed", target)
	}
	ev = h.await(t, runtimewatch.EventConnectFail, func(e runtimewatch.Event) bool { return e.RemotePort == port })
	if ev.RemoteAddr != addr {
		t.Errorf("CONNECT_FAIL remote = %s, want %s", ev.RemoteAddr, addr)
	}
}

// checkProcListener: the /proc baseline must see the listener the kernel
// reported.
func checkProcListener(t *testing.T, want runtimewatch.ListenerKey) {
	t.Helper()
	table := "/proc/self/net/tcp"
	if want.IPv6 {
		table += "6"
	}
	f, err := os.Open(table)
	if err != nil {
		t.Fatal(err)
	}
	keys, complete := runtimewatch.ParseListeners(f, want.IPv6)
	_ = f.Close()
	if !complete || !slices.Contains(keys, want) {
		t.Errorf("%s has no listener %+v (complete=%v)", table, want, complete)
	}
}

func netnsInode(t *testing.T) uint32 {
	t.Helper()
	link, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "net:["), "]"), 10, 32)
	if err != nil {
		t.Fatalf("parse %q: %v", link, err)
	}
	return uint32(n)
}

// TestKernelProbeReportsSocketTransitions loads and attaches the real program and
// drives each transition it reports from this process: LISTEN, CONNECT_OK,
// UNLISTEN and CONNECT_FAIL, for IPv4 and (when the namespace has it) IPv6,
// first in this process's own cgroup, then in a child cgroup it creates and
// moves into: every event must carry the cgroup id the socket was created in,
// which is the inode of that cgroup's directory. Run as root on Linux >= 5.12
// with BTF and cgroup v2 (the netns cookie): `make test-ebpf`.
func TestKernelProbeReportsSocketTransitions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (BPF, PERFMON)")
	}
	p, reason, err := attach(0)
	if err != nil {
		t.Fatalf("attach (%s): %v", reason, err)
	}
	defer p.close()

	ctx := t.Context()
	h := &kernelHarness{events: make(chan runtimewatch.Event, 1024), ns: netnsInode(t), start: monotonicNs()}
	stop := context.AfterFunc(ctx, func() { _ = p.reader.Close() })
	defer stop()
	go func() { _ = p.read(ctx, h.events) }()

	own := ownCgroupDir(t)
	h.wantCgroup = inode(t, own)
	t.Logf("netns inode %d, own cgroup %s, id %d", h.ns, own, h.wantCgroup)
	t.Run("own cgroup", h.transitions)

	// A child cgroup: the events move with the socket's creator, and the
	// resolver names it from the id alone.
	child := filepath.Join(own, "pacto-run-kerneltest")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("create child cgroup: %v", err)
	}
	moveSelf(t, child)
	defer func() {
		moveSelf(t, own)
		if err := os.Remove(child); err != nil {
			t.Errorf("remove child cgroup: %v", err)
		}
	}()
	h.wantCgroup = inode(t, child)
	t.Logf("child cgroup %s, id %d", child, h.wantCgroup)
	t.Run("child cgroup", h.transitions)

	r := &runtimewatch.CgroupResolver{FS: os.DirFS(cgroupRoot), Now: time.Now}
	if id, ok := r.Identify(h.wantCgroup); !ok || id.Kind != runtimefacts.KindSession || id.Name != "pacto-run-kerneltest" {
		t.Errorf("resolver named cgroup %d %+v (ok=%v), want the pacto-run-kerneltest session", h.wantCgroup, id, ok)
	}

	n, err := p.drops()
	if err != nil {
		t.Fatalf("read drop counter: %v", err)
	}
	t.Logf("dropped events: %d", n)
}

// TestScanProcOnThisHost runs the /proc baseline against the real /proc. With
// --pid=host on a host that runs workloads it shows every workload namespace; without
// CAP_SYS_PTRACE the namespace links of other processes are unreadable and the
// scan finds none of them.
func TestScanProcOnThisHost(t *testing.T) {
	scan, err := runtimewatch.ScanProc(runtimewatch.OSProcFS{Root: "/proc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scan.HostNetns == 0 {
		t.Fatal("host netns inode is 0")
	}
	for ns, n := range scan.Netns {
		t.Logf("netns %d shared=%v workloads=%v listeners=%d", ns, n.Shared, n.Workloads, len(n.Listeners))
	}
	t.Logf("host netns %d, %d workload namespaces", scan.HostNetns, len(scan.Netns))
}
