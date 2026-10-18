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
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

type recordingSink struct {
	out chan runtimefacts.Snapshot
	err error
	// deadline is the publish context's deadline, read after a receive from out.
	deadline time.Time
}

func (p *recordingSink) Publish(ctx context.Context, s runtimefacts.Snapshot) error {
	p.deadline, _ = ctx.Deadline()
	p.out <- s
	return p.err
}

// fakeCollector is a Collector driven by hand.
type fakeCollector struct {
	status   CollectorStatus
	attached chan struct{}
	events   []Event
}

func (c *fakeCollector) Run(ctx context.Context, out chan<- Event) {
	for _, ev := range c.events {
		out <- ev
	}
	<-ctx.Done()
}
func (c *fakeCollector) Status() CollectorStatus   { return c.status }
func (c *fakeCollector) Attached() <-chan struct{} { return c.attached }
func (c *fakeCollector) Mono() uint64              { return 42 }

func TestRunner_Run(t *testing.T) {
	now := t0
	scans := []error{nil, errors.New("proc gone"), nil, nil}
	events := make(chan Event)
	resync := make(chan time.Time)
	publish := make(chan time.Time)
	col := &fakeCollector{status: CollectorStatus{Failed: true, Reason: "AttachFailed", Message: "no BTF"}, attached: make(chan struct{})}
	pub := &recordingSink{out: make(chan runtimefacts.Snapshot), err: errors.New("apiserver down")}
	r := &Runner{
		Info: HostInfo{
			NodeName: "node-a", AgentVersion: "v1", KernelRelease: "6.12.0",
			PublishInterval: 30 * time.Second, StartedAt: t0,
		},
		Aggregator: NewAggregator(DefaultBounds(), nil),
		Scan: func() (Scan, error) {
			err := scans[0]
			scans = scans[1:]
			return oneScan(), err
		},
		Sink:        pub,
		Collector:   col,
		Events:      events,
		ResyncTick:  resync,
		PublishTick: publish,
		Now:         func() time.Time { return now },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()

	// Initial baseline and publish, even though the publish fails.
	checkFirstPublish(t, <-pub.out, pub.deadline)

	now = at(time.Second)
	events <- connect(100, dbAddr, 5432, true)
	resync <- now // fails: the previous baseline stays
	publish <- now
	st := <-pub.out
	if len(st.Workloads[0].Connections) != 1 || !st.LastPublished.Equal(now) || !st.Workloads[0].BaselineAt.Equal(t0) {
		t.Fatalf("second snapshot = %+v", st)
	}

	now = at(time.Minute)
	resync <- now
	publish <- now
	st = <-pub.out
	if !st.Workloads[0].BaselineAt.Equal(now) {
		t.Fatalf("third snapshot = %+v", st)
	}

	// A (re)attach records the collector's drops first, then baselines at once.
	col.status = CollectorStatus{Dropped: 2, AttachedAt: at(2 * time.Minute)}
	now = at(2 * time.Minute)
	col.attached <- struct{}{}
	publish <- now
	st = <-pub.out
	if st.Collection.State != runtimefacts.StateRunning || st.Collection.DroppedEvents != 2 ||
		!st.Collection.LastDropAt.Equal(at(2*time.Minute)) || !st.Workloads[0].BaselineAt.Equal(at(2*time.Minute)) {
		t.Fatalf("after attach = %+v", st)
	}

	cancel()
	<-done
}

// checkFirstPublish checks the snapshot Run publishes before any tick.
func checkFirstPublish(t *testing.T, st runtimefacts.Snapshot, deadline time.Time) {
	t.Helper()
	if st.NodeName != "node-a" || st.AgentVersion != "v1" || st.KernelRelease != "6.12.0" ||
		st.PublishIntervalSeconds != 30 || !st.ObservingSince.Equal(t0) || !st.LastPublished.Equal(t0) ||
		st.Collection.State != runtimefacts.StateFailed || st.Collection.Reason != "AttachFailed" || len(st.Workloads) != 2 {
		t.Fatalf("first snapshot = %+v", st)
	}
	// Every publish is bounded by one interval, so a hung sink cannot stall
	// event consumption.
	if deadline.IsZero() || time.Until(deadline) > 30*time.Second {
		t.Fatalf("publish deadline = %v", deadline)
	}
}

func maxConnection() runtimefacts.Connection {
	return runtimefacts.Connection{
		Address: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", Port: 65535,
		Successes: math.MaxInt64, Failures: math.MaxInt64, LastSuccessAt: t0, LastFailureAt: t0,
	}
}

// TestDefaultBoundsFitMaxStatusBytes builds the largest snapshot the default
// bounds allow and checks it fits without trimming.
func TestDefaultBoundsFitMaxStatusBytes(t *testing.T) {
	b := DefaultBounds()
	st := runtimefacts.Snapshot{
		NodeName: strings.Repeat("n", 253), AgentVersion: "v5.99.99-rc.1+0123456789ab", KernelRelease: strings.Repeat("k", 64),
		PublishIntervalSeconds: math.MaxInt32, LastPublished: t0, ObservingSince: t0,
		Collection: runtimefacts.Collection{
			State: runtimefacts.StateDegraded, Reason: strings.Repeat("R", 128), Message: strings.Repeat("m", 1024),
			DroppedEvents: math.MaxInt64, LastDropAt: t0, LastResyncAt: t0,
		},
	}
	conns, listeners := b.MaxNodeConnections, b.MaxNodeListeners
	for i := range b.MaxWorkloads {
		p := runtimefacts.Workload{
			Identity: pod(fmt.Sprintf("%08d-0000-0000-0000-000000000000", i)), Shared: true, Truncated: true,
			ObservedSince: t0, BaselineAt: t0,
		}
		for ; conns > 0 && len(p.Connections) < b.MaxConnections; conns-- {
			p.Connections = append(p.Connections, maxConnection())
		}
		for ; listeners > 0 && len(p.Listeners) < b.MaxListeners; listeners-- {
			p.Listeners = append(p.Listeners, runtimefacts.Listener{
				Port: 65535, Family: "IPv6", Address: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
				Process: sanitizeComm([]byte(strings.Repeat("<", 16))),
			})
		}
		st.Workloads = append(st.Workloads, p)
	}
	size := jsonSize(st)
	t.Logf("worst-case snapshot: %d bytes (limit %d)", size, MaxStatusBytes)
	if size > MaxStatusBytes {
		t.Fatalf("worst case at the default bounds is %d bytes, over %d", size, MaxStatusBytes)
	}
}

func TestFitSnapshot(t *testing.T) {
	old, mid := t0, at(time.Minute)
	c := func(addr string, ts time.Time, failed bool) runtimefacts.Connection {
		rc := runtimefacts.Connection{Address: addr, Port: 1, LastSuccessAt: ts}
		if failed {
			rc.LastSuccessAt, rc.LastFailureAt = old, ts
		}
		return rc
	}
	l := runtimefacts.Listener{Port: 80, Family: "IPv4"}
	st := runtimefacts.Snapshot{Workloads: []runtimefacts.Workload{
		{Identity: pod("a"), Listeners: []runtimefacts.Listener{l}},
		{Identity: pod("b"), Listeners: []runtimefacts.Listener{l}, Connections: []runtimefacts.Connection{
			c("10.0.0.1", mid, false), c("10.0.0.2", mid, true), c("10.0.0.3", old, false), c("10.0.0.4", time.Time{}, false),
		}},
	}}
	fitSnapshot(&st, jsonSize(st))
	if len(st.Workloads[1].Connections) != 4 || st.Workloads[1].Truncated {
		t.Fatal("a snapshot at the limit was trimmed")
	}
	// One connection over: workload b (the largest) loses its oldest
	// connection, the one with no attempt time at all.
	fitSnapshot(&st, jsonSize(st)-10)
	b := st.Workloads[1]
	if !b.Truncated || st.Workloads[0].Truncated || len(b.Connections) != 3 || b.Connections[2].Address != "10.0.0.3" {
		t.Fatalf("workload b = %+v", b)
	}
	if fitSnapshot(&st, jsonSize(st)-10); st.Workloads[1].Connections[1].Address != "10.0.0.2" || len(st.Workloads[1].Connections) != 2 {
		t.Fatalf("second trim dropped %+v", st.Workloads[1].Connections)
	}
	// A limit nothing can meet empties every list and gives up.
	fitSnapshot(&st, 10)
	for _, p := range st.Workloads {
		if len(p.Listeners)+len(p.Connections) != 0 || !p.Truncated {
			t.Fatalf("workload %+v not emptied", p)
		}
	}
}

func TestRunner_PublishTrimsOversizedSnapshot(t *testing.T) {
	b := DefaultBounds()
	b.MaxConnections, b.MaxNodeConnections = 10000, 10000
	agg := NewAggregator(b, nil)
	agg.Resync(oneScan(), t0)
	for i := range 9500 {
		addr := netip.AddrFrom16([16]byte{0xfd, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, byte(i >> 8), byte(i)})
		agg.Apply(connect(100, addr, 65535, true), t0)
	}
	pub := &recordingSink{out: make(chan runtimefacts.Snapshot, 1)}
	r := &Runner{Info: HostInfo{PublishInterval: time.Second}, Aggregator: agg, Sink: pub,
		Collector: &fakeCollector{}, Now: func() time.Time { return t0 }}
	r.publish(context.Background())
	st := <-pub.out
	a := podByUID(t, st.Workloads, uidA)
	if size := jsonSize(st); size > MaxStatusBytes || !a.Truncated || len(a.Connections) >= 9500 ||
		st.Collection.State != runtimefacts.StateDegraded || st.Collection.Reason != ReasonObjectTooLarge {
		t.Fatalf("size %d, pod A truncated=%v with %d connections, collection %+v", size, a.Truncated, len(a.Connections), st.Collection)
	}
}
