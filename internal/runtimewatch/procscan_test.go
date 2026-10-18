/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"testing/iotest"

	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// mapProc is an in-memory /proc: files and directories from a MapFS, symlinks
// from a map.
type mapProc struct {
	files      fstest.MapFS
	links      map[string]string
	readDirErr error
	// opens overrides Open for a name.
	opens map[string]io.ReadCloser
}

func (m mapProc) ReadFile(name string) ([]byte, error) { return fs.ReadFile(m.files, name) }

func (m mapProc) ReadDir(name string) ([]fs.DirEntry, error) {
	if m.readDirErr != nil {
		return nil, m.readDirErr
	}
	return fs.ReadDir(m.files, name)
}

func (m mapProc) Open(name string) (io.ReadCloser, error) {
	if f, ok := m.opens[name]; ok {
		return f, nil
	}
	return m.files.Open(name)
}

// errDir is a directory whose listing fails.
type errDir struct{ io.ReadCloser }

func (errDir) ReadDir(int) ([]fs.DirEntry, error) { return nil, errors.New("boom") }
func (errDir) Stat() (fs.FileInfo, error)         { return nil, errors.New("boom") }

func (m mapProc) Readlink(name string) (string, error) {
	l, ok := m.links[name]
	if !ok {
		return "", fs.ErrNotExist
	}
	return l, nil
}

const (
	uidA = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
	uidB = "11111111-2222-3333-4444-555555555555"
	uidC = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	uidD = "99999999-8888-7777-6666-555555555555"
	uidE = "eeeeeeee-0000-0000-0000-000000000000"
)

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func TestParseNetnsLink(t *testing.T) {
	if n, err := parseNetnsLink("net:[4026531840]"); err != nil || n != 4026531840 {
		t.Fatalf("got %d, %v", n, err)
	}
	for _, bad := range []string{"mnt:[1]", "net:[1", "net:[x]", "net:[99999999999]"} {
		if _, err := parseNetnsLink(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestParseListeners(t *testing.T) {
	v4 := tcpHeader +
		"   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1\n" +
		"   1: 0100007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1\n" +
		"   2: 0700000A:A000 0800000A:1F90 01 00000000:00000000 00:00000000 00000000     0        0 3 1\n" +
		"   3: 0100007FXX 00000000:0000 0A\n" +
		"   4: 0100007F:ZZZZ 00000000:0000 0A\n" +
		"   5: 01007F:0050 00000000:0000 0A\n" +
		"   6: GG00007F:0050 00000000:0000 0A\n" +
		"short line\n"
	got, complete := ParseListeners(strings.NewReader(v4), false)
	if !complete {
		t.Fatal("v4: incomplete")
	}
	want := []ListenerKey{
		{Addr: netip.MustParseAddr("0.0.0.0"), Port: 8080},
		{Addr: netip.MustParseAddr("127.0.0.1"), Port: 53},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v4: got %+v, want %+v", got, want)
	}

	v6 := tcpHeader +
		"   0: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 0 0 0 0 0 1\n" +
		"   1: 00000000000000000000000001000000:0016 00000000000000000000000000000000:0000 0A 0 0 0 0 0 1\n" +
		"   2: 0000000000000000:0016 00000000000000000000000000000000:0000 0A 0 0 0 0 0 1\n" +
		"   3: 00000000000000000000000000000000:0000 00000000000000000000000000000000:0000 0A 0 0 0 0 0 1\n"
	got, _ = ParseListeners(strings.NewReader(v6), true)
	want = []ListenerKey{
		{IPv6: true, Addr: netip.IPv6Unspecified(), Port: 8080},
		{IPv6: true, Addr: netip.IPv6Loopback(), Port: 22},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v6: got %+v, want %+v", got, want)
	}
}

func cg(uid string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("0::/kubepods.slice/kubepods-pod" + strings.ReplaceAll(uid, "-", "_") + ".slice/x.scope\n")}
}

func TestScanProc(t *testing.T) {
	listen8080 := &fstest.MapFile{Data: []byte(tcpHeader +
		"   0: 00000000:1F90 00000000:0000 0A 0 0 0 0 0 1\n" +
		"   1: 00000000:1F90 00000000:0000 0A 0 0 0 0 0 2\n")}
	p := mapProc{
		files: fstest.MapFS{
			"1/cgroup":    {Data: []byte("0::/init.scope\n")},
			"self/cgroup": cg(uidA),
			"0/cgroup":    cg(uidA),
			"5/cgroup":    {Data: []byte("0::/system.slice/kubelet.service\n")},
			"6/stat":      {Data: []byte("no cgroup file")},
			"7/cgroup":    cg(uidA), // no ns link
			"8/cgroup":    cg(uidA), // malformed ns link
			"10/cgroup":   cg(uidA), // pod A, netns 100; visited before 9
			"10/net/tcp":  {Data: []byte("unread: 10 is not the representative")},
			"9/cgroup":    cg(uidA),
			"9/net/tcp":   listen8080,
			"20/cgroup":   cg(uidB), // hostNetwork pod
			"30/cgroup":   cg(uidC), // C and D claim one namespace
			"31/cgroup":   cg(uidD),
			"40/cgroup":   cg(uidE),
			"40/net/tcp6": {Data: []byte(tcpHeader + "   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 0 0 0 0 0 1\n")},
			"50/cgroup":   cg(uidB), // exits before its tables are read
		},
		links: map[string]string{
			"1/ns/net":  "net:[1]",
			"8/ns/net":  "bogus",
			"9/ns/net":  "net:[100]",
			"10/ns/net": "net:[100]",
			"20/ns/net": "net:[1]",
			"30/ns/net": "net:[300]",
			"31/ns/net": "net:[300]",
			"40/ns/net": "net:[400]",
			"50/ns/net": "net:[500]",
		},
	}
	scan, err := ScanProc(p, onlyPods)
	if err != nil {
		t.Fatal(err)
	}
	if scan.HostNetns != 1 {
		t.Fatalf("host netns = %d", scan.HostNetns)
	}
	want := map[uint32]*NetnsScan{
		1:   {Workloads: podIDs(uidB), Shared: true},
		100: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{{Addr: netip.IPv4Unspecified(), Port: 8080}: 2}},
		300: {Workloads: podIDs(uidD, uidC), Shared: true}, // sorted
		400: {Workloads: podIDs(uidE), Listeners: map[ListenerKey]int{{IPv6: true, Addr: netip.IPv6Unspecified(), Port: 80}: 1}},
		500: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
	}
	if !reflect.DeepEqual(scan.Netns, want) {
		for k, v := range scan.Netns {
			t.Logf("got %d: %+v", k, *v)
		}
		t.Fatal("scan mismatch")
	}
}

func TestScanProc_Errors(t *testing.T) {
	if _, err := ScanProc(mapProc{links: map[string]string{}}, nil); err == nil {
		t.Error("missing host link: want error")
	}
	if _, err := ScanProc(mapProc{links: map[string]string{"1/ns/net": "bad"}}, nil); err == nil {
		t.Error("malformed host link: want error")
	}
	boom := errors.New("boom")
	_, err := ScanProc(mapProc{links: map[string]string{"1/ns/net": "net:[1]"}, readDirErr: boom}, nil)
	if !errors.Is(err, boom) {
		t.Errorf("ReadDir failure: got %v", err)
	}
}

func TestOSProcFS(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "1", "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "1", "cgroup"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("net:[7]", filepath.Join(root, "1", "ns", "net")); err != nil {
		t.Fatal(err)
	}
	p := OSProcFS{Root: root}
	if b, err := p.ReadFile("1/cgroup"); err != nil || string(b) != "x" {
		t.Errorf("ReadFile = %q, %v", b, err)
	}
	if es, err := p.ReadDir("."); err != nil || len(es) != 1 || es[0].Name() != "1" {
		t.Errorf("ReadDir = %v, %v", es, err)
	}
	if l, err := p.Readlink("1/ns/net"); err != nil || l != "net:[7]" {
		t.Errorf("Readlink = %q, %v", l, err)
	}
	f, err := p.Open("1/cgroup")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "x" {
		t.Errorf("Open read %q", b)
	}
}

const listenLine = "   0: 00000000:1F90 00000000:0000 0A 0 0 0 0 0 1\n"

func TestParseListeners_Bounds(t *testing.T) {
	// Only listeners up to the cap: the rest may hold more of them.
	all := tcpHeader + strings.Repeat(listenLine, maxTableLines)
	if keys, complete := ParseListeners(strings.NewReader(all), false); complete || len(keys) != maxTableLines-1 {
		t.Fatalf("all listeners: %d keys, complete=%v", len(keys), complete)
	}
	// Past the listeners (the kernel prints them first), the cap loses nothing.
	est := "   1: 0700000A:A000 0800000A:1F90 01 0 0 0 0 0 1\n"
	past := tcpHeader + listenLine + strings.Repeat(est, maxTableLines)
	if keys, complete := ParseListeners(strings.NewReader(past), false); !complete || len(keys) != 1 {
		t.Fatalf("past listeners: %d keys, complete=%v", len(keys), complete)
	}
	// A read error mid-table leaves it incomplete.
	r := io.MultiReader(strings.NewReader(tcpHeader+listenLine), iotest.ErrReader(errors.New("boom")))
	if keys, complete := ParseListeners(r, false); complete || len(keys) != 1 {
		t.Fatalf("read error: %d keys, complete=%v", len(keys), complete)
	}
}

func TestScanProc_TruncatedTable(t *testing.T) {
	p := mapProc{
		files: fstest.MapFS{
			"9/cgroup":  cg(uidA),
			"9/net/tcp": {Data: []byte(tcpHeader + strings.Repeat(listenLine, maxTableLines))},
		},
		links: map[string]string{"1/ns/net": "net:[1]", "9/ns/net": "net:[100]"},
	}
	scan, err := ScanProc(p, onlyPods)
	if err != nil {
		t.Fatal(err)
	}
	if n := scan.Netns[100]; !n.ListenersTruncated || len(n.Listeners) != 1 {
		t.Fatalf("netns 100 = %+v", n)
	}
}

func pod(uid string) runtimefacts.Identity {
	return runtimefacts.Identity{Kind: runtimefacts.KindPod, ID: uid}
}

func podIDs(uids ...string) []runtimefacts.Identity {
	out := make([]runtimefacts.Identity, len(uids))
	for i, u := range uids {
		out[i] = pod(u)
	}
	return out
}

func onlyPods(id runtimefacts.Identity) bool { return id.Kind == runtimefacts.KindPod }

func unit(name string) runtimefacts.Identity {
	return runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: name, Name: name}
}

func unitCg(name string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("0::/system.slice/" + name + "\n")}
}

func TestScanProc_HostNetworkAttribution(t *testing.T) {
	files := fstest.MapFS{
		"1/cgroup": unitCg("init.scope"), // the representative; its fds stay unread
		"1/net/tcp": {Data: []byte(tcpHeader +
			"   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 111 1\n" + // a.service
			"   1: 0100007F:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 222 1\n" + // nobody's
			"   2: 00000000:0016 00000000:0000 0A 0 0 0 0 0 1\n")}, // no inode column
		"1/net/tcp6": {Data: []byte(tcpHeader)},
		"5/cgroup":   unitCg("a.service"),
		"5/fd/3":     {}, "5/fd/4": {}, "5/fd/5": {}, "5/fd/6": {},
		"6/cgroup":  unitCg("b.service"),
		"6/fd":      {Data: []byte("a file, not a directory")},
		"7/cgroup":  unitCg("c.service"), // no fd directory
		"8/cgroup":  unitCg("e.service"),
		"9/cgroup":  unitCg("f.service"),
		"20/cgroup": cg(uidB), // a hostNetwork pod stays shared and unattributed
	}
	links := map[string]string{
		"1/ns/net": "net:[1]", "5/ns/net": "net:[1]", "6/ns/net": "net:[1]", "7/ns/net": "net:[1]",
		"8/ns/net": "net:[1]", "9/ns/net": "net:[1]", "20/ns/net": "net:[1]",
		"5/fd/3": "socket:[111]", "5/fd/4": "pipe:[9]", "5/fd/5": "socket:[bad]", // 5/fd/6 closed mid-scan
	}
	for i := range maxSocketFds + 1 {
		files["8/fd/"+strconv.Itoa(i)] = &fstest.MapFile{}
	}
	for i := range maxAttributedPids + 1 {
		pid := strconv.Itoa(1000 + i)
		files[pid+"/cgroup"] = unitCg("d.service")
		files[pid+"/fd"] = &fstest.MapFile{Mode: fs.ModeDir}
		links[pid+"/ns/net"] = "net:[1]"
	}
	scan, err := ScanProc(mapProc{files: files, links: links, opens: map[string]io.ReadCloser{"9/fd": errDir{io.NopCloser(nil)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := scan.Netns[1]; !n.Shared || n.Listeners != nil {
		t.Fatalf("host netns = %+v", n)
	}
	if _, ok := scan.Attributed[pod(uidB)]; ok {
		t.Fatal("a pod got per-process listeners")
	}
	want := map[runtimefacts.Identity]*NetnsScan{
		unit("a.service"):  {Listeners: map[ListenerKey]int{{Addr: netip.IPv4Unspecified(), Port: 8080}: 1}},
		unit("b.service"):  {Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
		unit("c.service"):  {Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
		unit("d.service"):  {Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
		unit("e.service"):  {Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
		unit("f.service"):  {Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
		unit("init.scope"): {Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
	}
	if !reflect.DeepEqual(scan.Attributed, want) {
		for k, v := range scan.Attributed {
			t.Logf("got %v: %+v", k, *v)
		}
		t.Fatal("attribution mismatch")
	}

	// An unreadable table leaves every attributed workload of the namespace truncated.
	delete(files, "1/net/tcp6")
	scan, _ = ScanProc(mapProc{files: files, links: links}, func(id runtimefacts.Identity) bool {
		return id == unit("a.service") || id == unit("init.scope") // init.scope keeps pid 1 the representative
	})
	if a := scan.Attributed[unit("a.service")]; !a.ListenersTruncated || len(a.Listeners) != 1 {
		t.Fatalf("a.service = %+v", a)
	}
	// Pods only: nothing to attribute, no table read.
	if scan, _ = ScanProc(mapProc{files: files, links: links}, onlyPods); len(scan.Attributed) != 0 {
		t.Fatalf("pods only: %+v", scan.Attributed)
	}
}

func TestForeignNetns(t *testing.T) {
	host := mapProc{links: map[string]string{"1/ns/net": "net:[1]"}}
	for name, tc := range map[string]struct {
		host, self mapProc
		want       bool
	}{
		"same":            {host, mapProc{links: map[string]string{"self/ns/net": "net:[1]"}}, false},
		"other":           {host, mapProc{links: map[string]string{"self/ns/net": "net:[2]"}}, true},
		"self unreadable": {host, mapProc{}, false},
		"host unreadable": {mapProc{}, mapProc{links: map[string]string{"self/ns/net": "net:[2]"}}, false},
	} {
		if got := ForeignNetns(tc.host, tc.self); got != tc.want {
			t.Errorf("%s: ForeignNetns = %v", name, got)
		}
	}
}
