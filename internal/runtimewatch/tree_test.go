/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

var treeNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// treeProc is pacto (pid 100) with a child 101 and a grandchild 102 sharing one
// namespace, a zombie child 103, an unrelated process 200 and a process whose
// stat is unreadable. 101 listens on 0.0.0.0:8080 and has dialled
// 10.0.0.5:5432; 102 holds a connection accepted on 8080.
func treeProc() mapProc {
	stat := func(ppid int, state string) *fstest.MapFile {
		return &fstest.MapFile{Data: fmt.Appendf(nil, "1 (a b) c) %s %d 1 1 0 -1 4194560 0", state, ppid)}
	}
	table := tcpHeader +
		"   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 11 1\n" +
		"   1: 0100007F:1F90 0100007F:9C40 01 00000000:00000000 00:00000000 00000000  1000 0 13 1\n" +
		"   2: 0100007F:A000 0500000A:1538 01 00000000:00000000 00:00000000 00000000  1000 0 12 1\n" +
		"   3: 0100007F:A001 0600000A:1538 01 00000000:00000000 00:00000000 00000000  1000 0 99 1\n" +
		"   4: 0100007F:A002 zz:1538 01 00000000:00000000 00:00000000 00000000  1000 0 12 1\n"
	return mapProc{
		files: fstest.MapFS{
			"100/stat":    stat(1, "S"),
			"101/stat":    stat(100, "S"),
			"102/stat":    stat(101, "R"),
			"103/stat":    stat(100, "Z"),
			"200/stat":    stat(1, "S"),
			"300/cgroup":  {Data: []byte("0::/\n")},
			"self/stat":   stat(1, "S"),
			"101/fd/3":    {},
			"101/fd/4":    {},
			"101/fd/7":    {},
			"102/fd/5":    {},
			"101/net/tcp": {Data: []byte(table)},
			"101/net/tcp6": {Data: []byte(tcpHeader +
				"   0: 0000000000000000FFFF00000100007F:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 14 1\n")},
		},
		links: map[string]string{
			"101/fd/3": "socket:[11]", "101/fd/4": "socket:[12]", "101/fd/7": "socket:[14]", "102/fd/5": "socket:[13]",
			"101/ns/net": "net:[1]", "102/ns/net": "net:[1]",
		},
	}
}

func newTree(p ProcFS) *TreeWatcher {
	return &TreeWatcher{Proc: p, Root: 100, Identity: runtimefacts.Identity{Kind: runtimefacts.KindSession, ID: "pacto-run-100", Name: "pacto-run-100"},
		Info: HostInfo{PublishInterval: 5 * time.Second, StartedAt: treeNow}, Now: func() time.Time { return treeNow }}
}

func TestTreeWatcher_Poll(t *testing.T) {
	p := treeProc()
	w := newTree(p)
	w.Poll(treeNow)
	s := w.Snapshot(treeNow)
	wl := s.Workloads[0]
	if s.Collection.State != runtimefacts.StateRunning || s.Collection.Reason != ReasonUnprivileged || s.PublishIntervalSeconds != 5 ||
		wl.Truncated || !wl.BaselineAt.Equal(treeNow) || wl.Identity != w.Identity {
		t.Fatalf("snapshot = %+v", s)
	}
	if len(wl.Listeners) != 2 || wl.Listeners[0] != (runtimefacts.Listener{Port: 8080, Family: "IPv4", Address: "0.0.0.0"}) ||
		wl.Listeners[1].Address != "::ffff:127.0.0.1" || wl.Listeners[1].Family != "IPv6" {
		t.Fatalf("listeners = %+v", wl.Listeners)
	}
	// The accepted connection (local port 8080) and the socket no process of the tree holds are not dials.
	if len(wl.Connections) != 1 || wl.Connections[0] != (runtimefacts.Connection{Address: "10.0.0.5", Port: 5432, Successes: 1, LastSuccessAt: treeNow}) {
		t.Fatalf("connections = %+v", wl.Connections)
	}

}

func TestTreeWatcher_ConnectionsOverPolls(t *testing.T) {
	p := treeProc()
	w := newTree(p)
	w.Poll(treeNow)
	// The same socket at the next poll is the same connection, fresh while it stays open; a new socket to the
	// peer is another one.
	later := treeNow.Add(time.Second)
	w.Poll(later)
	if c := w.Snapshot(later).Workloads[0].Connections[0]; c.Successes != 1 || !c.LastSuccessAt.Equal(later) {
		t.Fatalf("same socket = %+v", c)
	}
	p.links["101/fd/4"] = "socket:[99]"
	w.Poll(later)
	cs := w.Snapshot(later).Workloads[0].Connections
	if len(cs) != 2 || cs[1].Address != "10.0.0.6" {
		t.Fatalf("new socket = %+v", cs)
	}
	// A peer not dialled for the retention window is forgotten.
	gone := later.Add(DefaultBounds().Retention + time.Second)
	p.links["101/fd/4"] = "pipe:[1]"
	w.Poll(gone)
	if cs := w.Snapshot(gone).Workloads[0].Connections; len(cs) != 0 {
		t.Fatalf("expired = %+v", cs)
	}
}

func TestTreeWatcher_PeerBound(t *testing.T) {
	p := treeProc()
	var table strings.Builder
	table.WriteString(tcpHeader)
	for i := range DefaultBounds().MaxConnections + 1 {
		ino := 1000 + i
		fmt.Fprintf(&table, "   %d: 0100007F:A000 0500000A:%04X 01 0:0 0:0 0 0 0 %d 1\n", i, 1+i, ino)
		name := "101/fd/" + strconv.Itoa(ino)
		p.files[name] = &fstest.MapFile{}
		p.links[name] = "socket:[" + strconv.Itoa(ino) + "]"
	}
	p.files["101/net/tcp"] = &fstest.MapFile{Data: []byte(table.String())}
	w := newTree(p)
	w.Poll(treeNow)
	if wl := w.Snapshot(treeNow).Workloads[0]; len(wl.Connections) != DefaultBounds().MaxConnections || wl.Truncated {
		t.Fatalf("connections = %d, truncated %v", len(wl.Connections), wl.Truncated)
	}
}

// flakyProc makes the stat of the pids in gone readable once: the process exits during the poll.
type flakyProc struct {
	mapProc
	gone map[string]int
}

func (f flakyProc) ReadFile(name string) ([]byte, error) {
	if n, ok := f.gone[name]; ok {
		if n == 0 {
			return nil, errors.New("gone")
		}
		f.gone[name] = n - 1
	}
	return f.mapProc.ReadFile(name)
}

func TestTreeWatcher_Truncation(t *testing.T) {
	poll := func(p ProcFS) runtimefacts.Workload {
		w := newTree(p)
		w.Poll(treeNow)
		return w.Snapshot(treeNow).Workloads[0]
	}
	if wl := poll(treeProc()); wl.Truncated {
		t.Fatal("a complete tree is truncated")
	}
	cases := map[string]func(p mapProc){
		"unreadable descriptors": func(p mapProc) { delete(p.files, "102/fd/5") },
		"unreadable namespace":   func(p mapProc) { delete(p.links, "102/ns/net") },
		"unreadable table":       func(p mapProc) { delete(p.files, "101/net/tcp6") },
		"failed table": func(p mapProc) {
			p.files["101/net/tcp"] = &fstest.MapFile{Data: []byte(strings.Repeat("x\n", maxTableLines+1))}
		},
		"too many processes": func(p mapProc) {
			for i := range maxAttributedPids {
				pid := strconv.Itoa(1000 + i)
				p.files[pid+"/stat"] = &fstest.MapFile{Data: []byte("1 (x) S 101 1 1 0 -1 0")}
				p.files[pid+"/fd"] = &fstest.MapFile{Mode: fs.ModeDir}
				p.links[pid+"/ns/net"] = "net:[1]"
			}
		},
	}
	for name, mutate := range cases {
		p := treeProc()
		mutate(p)
		if wl := poll(p); !wl.Truncated {
			t.Errorf("%s: not truncated", name)
		}
	}
	// A process that exits during the poll hides nothing.
	p := treeProc()
	delete(p.files, "102/fd/5")
	delete(p.links, "102/ns/net")
	if wl := poll(flakyProc{mapProc: p, gone: map[string]int{"102/stat": 1}}); wl.Truncated {
		t.Fatal("an exited process truncated the view")
	}
	// Malformed stat lines are not processes.
	p = treeProc()
	p.files["101/stat"] = &fstest.MapFile{Data: []byte("101 (x) S 100 1 1 0")}
	p.files["102/stat"] = &fstest.MapFile{Data: []byte("102 (x) S nope 1 1 0 -1 0")}
	if wl := poll(p); len(wl.Listeners) != 0 || wl.Truncated {
		t.Fatalf("malformed = %+v", wl)
	}
}

func TestTreeWatcher_ListingFails(t *testing.T) {
	p := treeProc()
	w := newTree(&p)
	w.Poll(treeNow)
	p.readDirErr = errors.New("no proc")
	w.Poll(treeNow.Add(time.Second))
	s := w.Snapshot(treeNow.Add(time.Second))
	if s.Collection.State != runtimefacts.StateFailed || s.Collection.Message != "no proc" || len(s.Workloads[0].Listeners) != 2 {
		t.Fatalf("snapshot = %+v", s)
	}
}

type recordSink struct {
	got chan runtimefacts.Snapshot
	err error
}

func (r recordSink) Publish(_ context.Context, s runtimefacts.Snapshot) error {
	r.got <- s
	return r.err
}

func TestTreeWatcher_Run(t *testing.T) {
	sink := recordSink{got: make(chan runtimefacts.Snapshot, 4), err: errors.New("logged")}
	w := newTree(treeProc())
	w.Sink = sink
	poll, publish := make(chan time.Time), make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx, poll, publish)
	}()
	if s := <-sink.got; len(s.Workloads[0].Listeners) != 2 {
		t.Fatalf("first = %+v", s)
	}
	poll <- treeNow
	publish <- treeNow
	<-sink.got
	cancel()
	<-done
}

func TestRunning(t *testing.T) {
	p := mapProc{files: fstest.MapFS{
		"1/stat": {Data: []byte("1 (init) S 0 1 1 0 -1 4194560 0")},
		"2/stat": {Data: []byte("2 (exiting) R 1 1 1 0 -1 4194564 0")},
		"3/stat": {Data: []byte("3 (zombie) Z 1 1 1 0 -1 4194560 0")},
		"4/stat": {Data: []byte("4 (dead) X 1 1 1 0 -1 4194560 0")},
		"5/stat": {Data: []byte("5 (bad) S 1 1 1 0 -1 nope 0")},
		"6/stat": {Data: []byte("6 (short) S 1")},
	}}
	for pid, want := range map[int]bool{1: true, 2: false, 3: false, 4: false, 5: false, 6: false, 7: false} {
		if got := Running(p, pid); got != want {
			t.Errorf("Running(%d) = %v", pid, got)
		}
		if got := Exiting(p, pid); got != (!want && pid != 7) {
			t.Errorf("Exiting(%d) = %v", pid, got)
		}
	}
	// An exiting process is not part of the tree, and what it no longer shows truncates nothing.
	tp := treeProc()
	tp.files["102/stat"] = &fstest.MapFile{Data: []byte("102 (x) R 101 1 1 0 -1 4194564 0")}
	delete(tp.files, "102/fd/5")
	w := newTree(tp)
	w.Poll(treeNow)
	if wl := w.Snapshot(treeNow).Workloads[0]; wl.Truncated || len(wl.Listeners) != 2 {
		t.Fatalf("exiting = %+v", wl)
	}
}

func TestStartTime(t *testing.T) {
	stat := func(pid string, ticks string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(pid + " (a b) S 1 1 1 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 " + ticks + " 0 0")}
	}
	files := fstest.MapFS{
		"stat":      {Data: []byte("cpu  1 2 3\nbtime 1760000000\nprocesses 9\n")},
		"self/stat": stat("1", "1"), // not a pid
		"10/cgroup": unitCg("api.service"),
		"10/stat":   stat("10", "250"), // 2.5s after boot
		"11/cgroup": unitCg("api.service"),
		"11/stat":   stat("11", "900"),
		"12/cgroup": unitCg("api.service"),
		"12/stat":   {Data: []byte("12 (short) S 1")},
		"13/cgroup": unitCg("api.service"),
		"13/stat":   stat("13", "nope"),
		"14/cgroup": unitCg("api.service"), // gone mid-scan: no stat
		"15/cgroup": unitCg("other.service"),
		"15/stat":   stat("15", "1"),
		"16/stat":   stat("16", "1"), // no cgroup
		"0/cgroup":  unitCg("api.service"),
		"0/stat":    stat("0", "1"),
	}
	isAPI := func(id runtimefacts.Identity) bool { return id == unit("api.service") }
	got, ok := StartTime(mapProc{files: files}, isAPI)
	if want := time.Unix(1760000002, 0); !ok || !got.Equal(want) {
		t.Fatalf("StartTime = %v %v, want %v", got, ok, want)
	}
	if _, ok := StartTime(mapProc{files: files}, func(runtimefacts.Identity) bool { return false }); ok {
		t.Fatal("no match must not report a start")
	}
	for name, data := range map[string]string{"no btime": "cpu 1\n", "bad btime": "btime x\n"} {
		files["stat"] = &fstest.MapFile{Data: []byte(data)}
		if _, ok := StartTime(mapProc{files: files}, isAPI); ok {
			t.Errorf("%s: want no start", name)
		}
	}
	delete(files, "stat")
	if _, ok := StartTime(mapProc{files: files}, isAPI); ok {
		t.Error("unreadable /proc/stat: want no start")
	}
}
