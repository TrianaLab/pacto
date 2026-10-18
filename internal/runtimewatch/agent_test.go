//go:build unix

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestJSONSink(t *testing.T) {
	var buf bytes.Buffer
	snap := runtimefacts.Snapshot{NodeName: "n", Workloads: []runtimefacts.Workload{{Identity: pod(uidA)}}}
	if err := (JSONSink{W: &buf}).Publish(t.Context(), snap); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != `{"nodeName":"n","pods":[{"uid":"`+uidA+`"}]}`+"\n" {
		t.Fatalf("line = %q", got)
	}
	if err := (JSONSink{W: failingWriter{}}).Publish(t.Context(), snap); err == nil {
		t.Fatal("a write error must surface")
	}
}

// chanSink hands every snapshot to the test.
type chanSink chan runtimefacts.Snapshot

func (c chanSink) Publish(_ context.Context, s runtimefacts.Snapshot) error {
	c <- s
	return nil
}

// TestRun wires the whole agent: a /proc with pod A in netns 100, a cgroupfs
// where cgroup 7 is a unit kept out by the filter and cgroup 8 is pod A, and a
// collector that delivers one event of each before the first publish tick.
func TestRun(t *testing.T) {
	proc := mapProc{
		files: fstest.MapFS{
			"sys/kernel/osrelease": {Data: []byte("6.12.0\n")},
			"9/cgroup":             cg(uidA),
		},
		links: map[string]string{"1/ns/net": "net:[1]", "9/ns/net": "net:[100]"},
	}
	cgfs := fstest.MapFS{
		"cgroup.controllers":         {},
		"system.slice/web.service":   dir(7),
		"kubepods/pod" + uidA + "/c": dir(8),
	}
	col := &fakeCollector{attached: make(chan struct{}), events: []Event{
		inCgroup(connect(100, dbAddr, 5432, true), 7), // a unit: filtered, then namespace 100 is pod A
		inCgroup(connect(555, apiAddr, 443, true), 8), // pod A by cgroup
	}}
	sink := make(chanSink, 8)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, Config{
			NodeName: "node-a", AgentVersion: "v1", PublishInterval: 20 * time.Millisecond, ResyncInterval: time.Hour,
			Proc: proc, Cgroups: cgfs, Collector: col, Sink: sink,
			Keep: func(id runtimefacts.Identity) bool { return id.Kind == runtimefacts.KindPod },
		})
	}()
	deadline := time.After(10 * time.Second)
	for {
		var s runtimefacts.Snapshot
		select {
		case s = <-sink:
		case <-deadline:
			t.Fatal("no snapshot with both connections")
		}
		if s.KernelRelease != "6.12.0" || s.NodeName != "node-a" || len(s.Workloads) != 1 {
			t.Fatalf("snapshot = %+v", s)
		}
		if len(s.Workloads[0].Connections) == 2 {
			break
		}
	}
	cancel()
	<-done

	// Without a cgroupfs or a filter: namespace attribution only.
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	Run(ctx, Config{PublishInterval: time.Hour, ResyncInterval: time.Hour, Proc: proc,
		Collector: &fakeCollector{attached: make(chan struct{})}, Sink: JSONSink{W: &bytes.Buffer{}}})
}

// TestRunOnThisHost reads the real /proc and cgroupfs where there are any, as a
// smoke test of the OS adapters; it asserts only that a snapshot is produced.
func TestRunOnThisHost(t *testing.T) {
	if _, err := os.Stat("/proc/1"); err != nil {
		t.Skip("no /proc here")
	}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	Run(ctx, Config{PublishInterval: time.Hour, ResyncInterval: time.Hour, Proc: OSProcFS{Root: "/proc"},
		Cgroups: os.DirFS("/sys/fs/cgroup"), Collector: &fakeCollector{attached: make(chan struct{})}, Sink: JSONSink{W: &buf}})
	var s runtimefacts.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &s); err != nil {
		t.Fatal(err)
	}
}
