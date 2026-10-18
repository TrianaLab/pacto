/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"bufio"
	"cmp"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

// ProcFS is the slice of the host /proc the scanner reads. fs.FS has no
// readlink, and the network namespace inode is only exposed as a symlink.
// Names are slash-separated and relative to the /proc root ("1/ns/net").
type ProcFS interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Readlink(name string) (string, error)
	// Open streams a file, so a namespace with a huge connection table is never
	// read into memory whole.
	Open(name string) (io.ReadCloser, error)
}

// OSProcFS reads a real /proc mounted at Root (the host /proc: hostPID in a pod, --pid=host in Docker).
type OSProcFS struct {
	Root string
}

func (p OSProcFS) path(name string) string { return filepath.Join(p.Root, filepath.FromSlash(name)) }

// ReadFile implements ProcFS.
func (p OSProcFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(p.path(name)) }

// ReadDir implements ProcFS.
func (p OSProcFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(p.path(name)) }

// Readlink implements ProcFS.
func (p OSProcFS) Readlink(name string) (string, error) { return os.Readlink(p.path(name)) }

// Open implements ProcFS.
func (p OSProcFS) Open(name string) (io.ReadCloser, error) { return os.Open(p.path(name)) }

// ListenerKey identifies a listening socket inside one network namespace.
type ListenerKey struct {
	IPv6 bool
	Addr netip.Addr
	Port uint16
}

// NetnsScan is what one /proc scan learned about one network namespace.
type NetnsScan struct {
	// Workloads are the workloads whose processes live in the namespace, sorted
	// by kind and ID.
	Workloads []runtimefacts.Identity
	// Shared is true for the host namespace and for any namespace more than one
	// workload claims: events in it cannot be attributed to a single workload.
	Shared bool
	// Listeners counts LISTEN sockets per key (SO_REUSEPORT allows several).
	// Nil for shared namespaces, which are never read.
	Listeners map[ListenerKey]int
	// ListenersTruncated is true when a table could not be read to its end, so
	// Listeners may be missing entries.
	ListenersTruncated bool
}

// Scan is one baseline of the host: every workload network namespace and its listeners.
type Scan struct {
	HostNetns uint32
	Netns     map[uint32]*NetnsScan
	// Attributed holds, for every workload other than a pod that lives in a
	// shared namespace (a systemd unit, a host-network container), the
	// listeners its own processes hold: their socket inodes matched against the
	// namespace's tables. Workloads is unset in these entries.
	Attributed map[runtimefacts.Identity]*NetnsScan
	// Mono is CLOCK_MONOTONIC in nanoseconds when the scan started (the clock
	// the kernel stamps events with), or 0 when unknown. The caller sets it.
	Mono uint64
}

// parseNetnsLink parses a "net:[4026531840]" namespace link.
func parseNetnsLink(link string) (uint32, error) {
	inner, ok := strings.CutPrefix(link, "net:[")
	if !ok || !strings.HasSuffix(inner, "]") {
		return 0, fmt.Errorf("not a network namespace link: %q", link)
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(inner, "]"), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("network namespace inode %q: %w", link, err)
	}
	return uint32(n), nil
}

func compareIdentities(x, y runtimefacts.Identity) int {
	return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.ID, y.ID))
}

// Bounds of the per-process listener attribution: each resync reads the
// descriptors of at most maxAttributedPids processes per workload and at most
// maxSocketFds descriptors per process. A workload that hits either bound, or
// whose descriptors cannot be read, is reported truncated: its view is then
// never complete, so silence cannot confirm an absence.
const (
	maxAttributedPids = 64
	maxSocketFds      = 4096
)

// ScanProc walks every process once and keeps those whose cgroup names a
// workload keep accepts (nil keeps every workload). Only the host namespace
// link is required: processes that exit or deny access mid-scan are skipped,
// because /proc is a moving target and one vanished pid must not void the
// baseline.
//
// A workload in a namespace it does not own (the host network) cannot be told
// apart in the namespace-wide tables. A pod stays Shared, as pods always were;
// any other workload gets its listeners from its own processes' socket
// descriptors (Scan.Attributed).
func ScanProc(p ProcFS, keep func(runtimefacts.Identity) bool) (Scan, error) {
	hostLink, err := p.Readlink("1/ns/net")
	if err != nil {
		return Scan{}, fmt.Errorf("host network namespace: %w", err)
	}
	host, err := parseNetnsLink(hostLink)
	if err != nil {
		return Scan{}, fmt.Errorf("host network namespace: %w", err)
	}
	entries, err := p.ReadDir(".")
	if err != nil {
		return Scan{}, fmt.Errorf("list processes: %w", err)
	}

	scan := Scan{HostNetns: host, Netns: map[uint32]*NetnsScan{}, Attributed: map[runtimefacts.Identity]*NetnsScan{}}
	pids := map[runtimefacts.Identity][]int{}
	rep := walkProcesses(p, entries, keep, scan.Netns, pids)
	for ns, n := range scan.Netns {
		slices.SortFunc(n.Workloads, compareIdentities)
		n.Shared = ns == host || len(n.Workloads) > 1
		if n.Shared {
			attributeListeners(p, rep[ns], n.Workloads, pids, scan.Attributed)
		} else {
			readListeners(p, rep[ns], n)
		}
	}
	return scan, nil
}

// walkProcesses records the network namespace of every kept workload process in
// netns and returns the lowest pid of each namespace: any member reads the same
// namespace-wide tables. pids collects the processes of every workload other
// than a pod, one past maxAttributedPids at most, so an overflow shows.
func walkProcesses(p ProcFS, entries []fs.DirEntry, keep func(runtimefacts.Identity) bool, netns map[uint32]*NetnsScan,
	pids map[runtimefacts.Identity][]int) map[uint32]int {
	rep := map[uint32]int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		cg, err := p.ReadFile(e.Name() + "/cgroup")
		if err != nil {
			continue
		}
		id := IdentityFromProcCgroup(cg)
		if id.Kind == runtimefacts.KindProcess || (keep != nil && !keep(id)) {
			continue
		}
		ns, err := procNetns(p, e.Name())
		if err != nil {
			continue
		}
		n := netns[ns]
		if n == nil {
			n = &NetnsScan{}
			netns[ns] = n
		}
		if !slices.Contains(n.Workloads, id) {
			n.Workloads = append(n.Workloads, id)
		}
		if r, ok := rep[ns]; !ok || pid < r {
			rep[ns] = pid
		}
		if id.Kind != runtimefacts.KindPod && len(pids[id]) <= maxAttributedPids {
			pids[id] = append(pids[id], pid)
		}
	}
	return rep
}

// procNetns is the network namespace inode of a process.
func procNetns(p ProcFS, pid string) (uint32, error) {
	link, err := p.Readlink(pid + "/ns/net")
	if err != nil {
		return 0, err
	}
	return parseNetnsLink(link)
}

// attributeListeners gives every workload other than a pod in a shared
// namespace the listeners whose socket inode one of its processes holds. The
// namespace's tables are streamed once through its representative pid.
func attributeListeners(p ProcFS, rep int, workloads []runtimefacts.Identity, pids map[runtimefacts.Identity][]int,
	out map[runtimefacts.Identity]*NetnsScan) {
	owner := map[uint64]*NetnsScan{}
	var attributed []*NetnsScan
	for _, id := range workloads {
		if id.Kind == runtimefacts.KindPod {
			continue
		}
		a := out[id]
		if a == nil {
			a = &NetnsScan{Listeners: map[ListenerKey]int{}}
			out[id] = a
		}
		attributed = append(attributed, a)
		ps := pids[id]
		if len(ps) > maxAttributedPids {
			ps, a.ListenersTruncated = ps[:maxAttributedPids], true
		}
		for _, pid := range ps {
			if !socketInodes(p, pid, func(ino uint64) { owner[ino] = a }) {
				a.ListenersTruncated = true
			}
		}
	}
	if len(attributed) == 0 {
		return
	}
	dir := strconv.Itoa(rep) + "/net/"
	for _, file := range []string{"tcp", "tcp6"} {
		f, err := p.Open(dir + file)
		complete := err == nil
		if complete {
			complete = scanListeners(f, file == "tcp6", func(k ListenerKey, ino uint64) {
				if a := owner[ino]; a != nil {
					a.Listeners[k]++
				}
			})
			_ = f.Close()
		}
		if !complete {
			for _, a := range attributed {
				a.ListenersTruncated = true
			}
		}
	}
}

// socketInodes reports the inode of every socket descriptor of the process, at
// most maxSocketFds descriptors. It returns false when the descriptors could
// not all be read: a bound hit or an unreadable fd directory.
func socketInodes(p ProcFS, pid int, fn func(uint64)) bool {
	dir := strconv.Itoa(pid) + "/fd"
	f, err := p.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	d, ok := f.(fs.ReadDirFile)
	if !ok {
		return false
	}
	// One more than the bound, to tell a full directory from an overflowing one.
	entries, err := d.ReadDir(maxSocketFds + 1)
	if err != nil && err != io.EOF {
		return false
	}
	for _, e := range entries[:min(len(entries), maxSocketFds)] {
		link, err := p.Readlink(dir + "/" + e.Name())
		if err != nil {
			continue // closed since the listing
		}
		inner, ok := strings.CutPrefix(link, "socket:[")
		if !ok {
			continue
		}
		if ino, err := strconv.ParseUint(strings.TrimSuffix(inner, "]"), 10, 64); err == nil {
			fn(ino)
		}
	}
	return len(entries) <= maxSocketFds
}

// readListeners reads a namespace's listeners through its representative pid.
func readListeners(p ProcFS, pid int, n *NetnsScan) {
	n.Listeners = map[ListenerKey]int{}
	dir := strconv.Itoa(pid) + "/net/"
	for _, file := range []string{"tcp", "tcp6"} {
		// A process that exited since the walk leaves the namespace with no
		// listeners this round; the next resync heals it.
		f, err := p.Open(dir + file)
		if err != nil {
			continue
		}
		keys, complete := ParseListeners(f, file == "tcp6")
		_ = f.Close()
		n.ListenersTruncated = n.ListenersTruncated || !complete
		for _, k := range keys {
			n.Listeners[k]++
		}
	}
}

// tcpListen is the kernel's TCP_LISTEN state as /proc/net/tcp prints it.
const tcpListen = "0A"

// maxTableLines caps the lines read from one tcp or tcp6 table (about 2.4 MB
// of text at 150 bytes a line), so a proxy with 100k connections costs bounded
// time every resync. The kernel prints every listening socket before the
// established hash, so once a non-LISTEN line has gone by the rest of the table
// holds no listeners; only a table whose first maxTableLines lines are all
// listeners is reported incomplete.
const maxTableLines = 16384

// ParseListeners streams a /proc/<pid>/net/tcp or tcp6 table and returns its
// LISTEN sockets. Malformed lines and port 0 are skipped. complete is false
// when the read failed or stopped at maxTableLines while still in listeners.
func ParseListeners(table io.Reader, ipv6 bool) (out []ListenerKey, complete bool) {
	complete = scanListeners(table, ipv6, func(k ListenerKey, _ uint64) { out = append(out, k) })
	return out, complete
}

// scanListeners calls fn with every LISTEN socket of a table and its socket
// inode (0 when the line has none), with ParseListeners' rules and result.
func scanListeners(table io.Reader, ipv6 bool, fn func(ListenerKey, uint64)) bool {
	return scanTable(table, ipv6, func(s tableSocket) {
		if s.state == tcpListen && s.local.Port() != 0 {
			fn(ListenerKey{IPv6: ipv6, Addr: s.local.Addr(), Port: s.local.Port()}, s.inode)
		}
	})
}

// tableSocket is one parsed line of a tcp or tcp6 table.
type tableSocket struct {
	state         string
	local, remote netip.AddrPort
	inode         uint64
}

// scanTable calls fn with every well-formed socket line of a table, reading at
// most maxTableLines lines. It returns false when the read failed or stopped at
// the bound while still in listeners, so a caller can trust the listeners it
// saw to be all of them.
func scanTable(table io.Reader, ipv6 bool, fn func(tableSocket)) bool {
	sc := bufio.NewScanner(table)
	pastListeners := false
	for lines := 0; sc.Scan(); lines++ {
		if lines == maxTableLines {
			return pastListeners
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		if f[3] != tcpListen {
			pastListeners = pastListeners || f[0] != "sl"
		}
		local, ok := parseProcAddrPort(f[1], ipv6)
		if !ok {
			continue
		}
		remote, ok := parseProcAddrPort(f[2], ipv6)
		if !ok {
			continue
		}
		s := tableSocket{state: f[3], local: local, remote: remote}
		if len(f) > 9 {
			s.inode, _ = strconv.ParseUint(f[9], 10, 64)
		}
		fn(s)
	}
	return sc.Err() == nil
}

// parseProcAddrPort decodes a /proc/net/tcp "ADDR:PORT" field.
func parseProcAddrPort(s string, ipv6 bool) (netip.AddrPort, bool) {
	addrHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	addr, ok := parseProcAddr(addrHex, ipv6)
	return netip.AddrPortFrom(addr, uint16(port)), ok
}

// parseProcAddr decodes a /proc/net/tcp address: the raw in_addr/in6_addr
// printed as 32-bit words in host byte order. Every architecture the agent
// ships for (amd64, arm64) is little endian, so each word is byte-reversed.
func parseProcAddr(s string, ipv6 bool) (netip.Addr, bool) {
	b, err := hex.DecodeString(s)
	if err != nil || (ipv6 && len(b) != 16) || (!ipv6 && len(b) != 4) {
		return netip.Addr{}, false
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	if ipv6 {
		return netip.AddrFrom16([16]byte(b)), true
	}
	return netip.AddrFrom4([4]byte(b)), true
}
