/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

var (
	any4    = netip.IPv4Unspecified()
	any6    = netip.IPv6Unspecified()
	dbAddr  = netip.MustParseAddr("10.96.0.10")
	apiAddr = netip.MustParseAddr("10.96.0.20")
)

// oneScan maps pod A to netns 100 (listening on 8080), pod B to the host netns.
func oneScan() Scan {
	return Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		1:   {Workloads: podIDs(uidB), Shared: true},
		100: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{{Addr: any4, Port: 8080}: 1}},
	}}
}

func connect(ns uint32, addr netip.Addr, port uint16, ok bool) Event {
	k := EventConnectFail
	if ok {
		k = EventConnectOK
	}
	return Event{Kind: k, Netns: ns, RemoteAddr: addr, RemotePort: port}
}

func listen(ns uint32, kind EventKind, ipv6 bool, addr netip.Addr, port uint16, comm string) Event {
	return Event{Kind: kind, IPv6: ipv6, Netns: ns, LocalAddr: addr, LocalPort: port, Comm: comm}
}

func podByUID(t *testing.T, pods []runtimefacts.Workload, uid string) runtimefacts.Workload {
	t.Helper()
	for _, p := range pods {
		if p.ID == uid {
			return p
		}
	}
	t.Fatalf("pod %s not published", uid)
	return runtimefacts.Workload{}
}

func TestAggregator_BaselineEventsAndSnapshot(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)

	a.Apply(listen(100, EventListen, true, any6, 9090, "api"), at(time.Second))
	a.Apply(listen(100, EventListen, false, any4, 8080, "web"), at(time.Second)) // second socket on 8080
	a.Apply(connect(100, dbAddr, 5432, true), at(2*time.Second))
	a.Apply(connect(100, netip.MustParseAddr("::ffff:10.96.0.10"), 5432, false), at(3*time.Second)) // unmapped
	a.Apply(connect(100, apiAddr, 443, false), at(4*time.Second))
	// Host netns: unattributable, ignored for listeners and connects alike.
	a.Apply(listen(1, EventListen, false, any4, 22, "sshd"), at(time.Second))
	a.Apply(connect(1, dbAddr, 5432, true), at(time.Second))

	checkFirstSnapshot(t, a)

	// UNLISTEN: one of two sockets on 8080 leaves it listening; the second
	// removes it; an UNLISTEN for an unknown socket is a no-op.
	a.Apply(listen(100, EventUnlisten, false, any4, 8080, ""), at(6*time.Second))
	_, pods := a.Snapshot(at(6*time.Second), CollectorStatus{})
	if len(pods[0].Listeners) != 2 {
		t.Fatalf("after one unlisten: %+v", pods[0].Listeners)
	}
	a.Apply(listen(100, EventUnlisten, false, any4, 8080, ""), at(6*time.Second))
	a.Apply(listen(100, EventUnlisten, false, any4, 1, ""), at(6*time.Second))
	_, pods = a.Snapshot(at(6*time.Second), CollectorStatus{})
	if len(pods[0].Listeners) != 1 || pods[0].Listeners[0].Port != 9090 {
		t.Fatalf("after both unlistens: %+v", pods[0].Listeners)
	}
}

// checkFirstSnapshot checks TestAggregator_BaselineEventsAndSnapshot's state
// before any UNLISTEN.
func checkFirstSnapshot(t *testing.T, a *Aggregator) {
	t.Helper()
	col, pods := a.Snapshot(at(5*time.Second), CollectorStatus{})
	if col.State != runtimefacts.StateRunning || col.Reason != "" || col.LastResyncAt.IsZero() || !col.LastDropAt.IsZero() {
		t.Fatalf("collection = %+v", col)
	}
	if len(pods) != 2 || pods[0].ID != uidA || pods[1].ID != uidB {
		t.Fatalf("pods not sorted by UID: %+v", pods)
	}
	host := pods[1]
	if !host.Shared || len(host.Listeners) != 0 || len(host.Connections) != 0 {
		t.Fatalf("host-network pod = %+v", host)
	}
	a0 := pods[0]
	if a0.Truncated || a0.Shared || !a0.ObservedSince.Equal(t0) || !a0.BaselineAt.Equal(t0) {
		t.Fatalf("pod A = %+v", a0)
	}
	checkPodA(t, a0)
}

// checkPodA checks pod A's listeners and connections in that snapshot.
func checkPodA(t *testing.T, a0 runtimefacts.Workload) {
	t.Helper()
	wantL := []runtimefacts.Listener{
		{Port: 8080, Family: "IPv4", Address: "0.0.0.0", Process: "web"},
		{Port: 9090, Family: "IPv6", Address: "::", Process: "api"},
	}
	if len(a0.Listeners) != 2 || a0.Listeners[0] != wantL[0] || a0.Listeners[1] != wantL[1] {
		t.Fatalf("listeners = %+v", a0.Listeners)
	}
	if len(a0.Connections) != 2 {
		t.Fatalf("connections = %+v", a0.Connections)
	}
	db, api := a0.Connections[0], a0.Connections[1]
	if db.Address != "10.96.0.10" || db.Port != 5432 || db.Successes != 1 || db.Failures != 1 ||
		!db.LastSuccessAt.Equal(at(2*time.Second)) || !db.LastFailureAt.Equal(at(3*time.Second)) {
		t.Fatalf("db = %+v", db)
	}
	if api.Address != "10.96.0.20" || api.Successes != 0 || api.Failures != 1 || !api.LastSuccessAt.IsZero() {
		t.Fatalf("api = %+v", api)
	}
}

func TestAggregator_ResyncHealsListenersAndKeepsProcess(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(listen(100, EventListen, false, any4, 8080, "web"), at(time.Second))
	a.Apply(listen(100, EventListen, false, any4, 7000, "lost"), at(time.Second)) // gone by the next scan

	a.Resync(oneScan(), at(time.Minute))
	_, pods := a.Snapshot(at(time.Minute), CollectorStatus{})
	p := podByUID(t, pods, uidA)
	if len(p.Listeners) != 1 || p.Listeners[0].Process != "web" || !p.BaselineAt.Equal(at(time.Minute)) ||
		!p.ObservedSince.Equal(t0) {
		t.Fatalf("pod A after resync = %+v", p)
	}
}

func TestAggregator_PodLifecycle(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(connect(100, dbAddr, 5432, true), at(time.Second))

	// Pod A gone, pod B moves to its own namespace: A and its peers are dropped.
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		200: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
	}}, at(time.Minute))
	if a.totalPeers != 0 {
		t.Fatalf("totalPeers = %d after the pod left", a.totalPeers)
	}
	a.Apply(connect(200, dbAddr, 5432, true), at(61*time.Second))
	_, pods := a.Snapshot(at(61*time.Second), CollectorStatus{})
	if len(pods) != 1 || pods[0].ID != uidB || pods[0].Shared || len(pods[0].Connections) != 1 {
		t.Fatalf("pods = %+v", pods)
	}

	// B moves back to the host namespace: its history is no longer attributable.
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{1: {Workloads: podIDs(uidB), Shared: true}}}, at(2*time.Minute))
	_, pods = a.Snapshot(at(2*time.Minute), CollectorStatus{})
	if !pods[0].Shared || len(pods[0].Connections) != 0 || a.totalPeers != 0 {
		t.Fatalf("pods = %+v, totalPeers = %d", pods, a.totalPeers)
	}
}

func TestAggregator_PendingAdoptedOnResync(t *testing.T) {
	b := DefaultBounds()
	b.MaxConnections = 2
	a := NewAggregator(b, nil)
	a.Resync(oneScan(), t0)

	// Netns 300 is not mapped yet: its connects are held, not lost.
	a.Apply(connect(300, dbAddr, 5432, true), at(time.Second))
	a.Apply(connect(300, dbAddr, 5432, false), at(2*time.Second))
	a.Apply(connect(300, apiAddr, 443, true), at(2*time.Second))
	a.Apply(connect(300, netip.MustParseAddr("10.0.0.3"), 80, true), at(3*time.Second)) // over the per-set bound
	// Listener events in an unmapped netns are left to the baseline.
	a.Apply(listen(300, EventListen, false, any4, 80, "x"), at(time.Second))
	// Netns 400 stays unmapped.
	a.Apply(connect(400, dbAddr, 5432, true), at(time.Second))

	scan := oneScan()
	scan.Netns[300] = &NetnsScan{Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}}
	a.Resync(scan, at(10*time.Second))
	if _, ok := a.pending[300]; ok {
		t.Fatal("adopted pending set not removed")
	}
	if _, ok := a.pending[400]; !ok {
		t.Fatal("unmapped pending set dropped early")
	}
	_, pods := a.Snapshot(at(10*time.Second), CollectorStatus{})
	c := podByUID(t, pods, uidC)
	if !c.Truncated || len(c.Connections) != 2 || c.Connections[0].Successes != 1 || c.Connections[0].Failures != 1 {
		t.Fatalf("pod C = %+v", c)
	}
	if len(c.Listeners) != 0 {
		t.Fatalf("pod C listeners = %+v", c.Listeners)
	}
}

func TestAggregator_AdoptMergesAndBounds(t *testing.T) {
	b := DefaultBounds()
	b.MaxConnections = 1
	a := NewAggregator(b, nil)
	a.Resync(oneScan(), t0)
	a.Apply(connect(100, dbAddr, 5432, true), at(time.Second))

	// Pod A's sandbox is recreated into netns 101 before any scan sees it: the
	// known peer merges, the new one does not fit.
	a.Apply(connect(101, dbAddr, 5432, false), at(2*time.Second))
	a.Apply(connect(101, apiAddr, 443, true), at(3*time.Second))

	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		101: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{}},
	}}, at(10*time.Second))
	p := a.workloads[pod(uidA)]
	db := p.peers[peerKey{addr: dbAddr, port: 5432}]
	if len(p.peers) != 1 || db.successes != 1 || db.failures != 1 || !db.lastFailure.Equal(at(2*time.Second)) ||
		!db.lastSuccess.Equal(at(time.Second)) {
		t.Fatalf("peers = %+v, db = %+v", p.peers, db)
	}
	if !p.truncatedAt.Equal(at(3 * time.Second)) {
		t.Fatalf("truncatedAt = %v", p.truncatedAt)
	}
}

func TestAggregator_Bounds(t *testing.T) {
	b := Bounds{MaxWorkloads: 2, MaxListeners: 1, MaxConnections: 1, MaxNodeListeners: 10, MaxNodeConnections: 1, Retention: time.Minute}
	a := NewAggregator(b, nil)
	scan := Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		100: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{{Addr: any4, Port: 1}: 1, {Addr: any4, Port: 2}: 1}},
		200: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
		300: {Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}}, // third pod: over MaxWorkloads
	}}
	a.Resync(scan, t0)
	col, pods := a.Snapshot(t0, CollectorStatus{})
	if col.State != runtimefacts.StateDegraded || col.Reason != ReasonPodLimitExceeded || len(pods) != 2 {
		t.Fatalf("collection = %+v, %d pods", col, len(pods))
	}
	if !pods[0].Truncated || len(pods[0].Listeners) != 1 || pods[0].Listeners[0].Port != 1 {
		t.Fatalf("pod A = %+v", pods[0])
	}
	// The untracked pod's namespace is known but unattributable: not pending.
	a.Apply(connect(300, dbAddr, 1, true), at(time.Second))
	if len(a.pending) != 0 {
		t.Fatal("over-limit pod's connects went to pending")
	}

	// Listener over the per-pod bound.
	a.Apply(listen(200, EventListen, false, any4, 1, ""), at(time.Second))
	a.Apply(listen(200, EventListen, false, any4, 2, ""), at(time.Second))
	// Per-pod and node-wide peer bounds.
	a.Apply(connect(100, dbAddr, 1, true), at(time.Second))
	a.Apply(connect(100, dbAddr, 2, true), at(time.Second)) // per-pod
	a.Apply(connect(200, dbAddr, 3, true), at(time.Second)) // node-wide
	_, pods = a.Snapshot(at(time.Second), CollectorStatus{})
	if !pods[1].Truncated || len(pods[1].Listeners) != 1 || len(pods[1].Connections) != 0 {
		t.Fatalf("pod B = %+v", pods[1])
	}

	// Pending bound: one more unmapped namespace than MaxWorkloads is ignored.
	a.Apply(connect(900, dbAddr, 1, true), at(time.Second))
	a.Apply(connect(901, dbAddr, 1, true), at(time.Second))
	a.Apply(connect(902, dbAddr, 1, true), at(time.Second))
	if len(a.pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(a.pending))
	}

	// Adoption hits the node-wide bound too.
	scan.Netns = map[uint32]*NetnsScan{
		100: scan.Netns[100],
		900: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
	}
	a.Resync(scan, at(2*time.Second))
	if len(a.workloads[pod(uidB)].peers) != 0 || !a.workloads[pod(uidB)].truncatedAt.Equal(at(time.Second)) {
		t.Fatalf("pod B after adoption = %+v", a.workloads[pod(uidB)])
	}
}

func TestAggregator_RetentionExpiry(t *testing.T) {
	a := NewAggregator(Bounds{MaxWorkloads: 4, MaxListeners: 4, MaxConnections: 1, MaxNodeListeners: 10, MaxNodeConnections: 10, Retention: time.Minute}, nil)
	a.Resync(oneScan(), t0)
	a.Apply(connect(100, dbAddr, 5432, true), t0)
	a.Apply(connect(100, apiAddr, 443, true), t0) // over the bound: truncated
	a.Apply(connect(500, dbAddr, 5432, true), t0) // pending, will expire
	a.Apply(connect(600, dbAddr, 5432, true), t0)
	a.Apply(connect(600, apiAddr, 443, true), t0) // pending over its bound

	_, pods := a.Snapshot(at(30*time.Second), CollectorStatus{})
	if !podByUID(t, pods, uidA).Truncated {
		t.Fatal("truncation should hold within the retention window")
	}
	a.Apply(connect(600, apiAddr, 443, true), at(50*time.Second)) // keeps 600's set alive
	a.Apply(connect(600, dbAddr, 5432, true), at(50*time.Second))

	_, pods = a.Snapshot(at(61*time.Second), CollectorStatus{})
	p := podByUID(t, pods, uidA)
	if p.Truncated || len(p.Connections) != 0 || a.totalPeers != 0 {
		t.Fatalf("pod A after retention = %+v (totalPeers %d)", p, a.totalPeers)
	}
	if _, ok := a.pending[500]; ok {
		t.Fatal("expired pending set kept")
	}
	if ps := a.pending[600]; ps == nil || len(ps.peers) != 1 {
		t.Fatalf("pending 600 = %+v", a.pending[600])
	}
}

func TestAggregator_CollectionStates(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)

	// Drops after the last resync degrade; the counter is cumulative.
	col, _ := a.Snapshot(at(time.Second), CollectorStatus{Dropped: 3})
	if col.State != runtimefacts.StateDegraded || col.Reason != ReasonEventsDropped ||
		col.DroppedEvents != 3 || !col.LastDropAt.Equal(at(time.Second)) {
		t.Fatalf("drops = %+v", col)
	}
	// A count that does not grow keeps the old drop time; a resync clears the state.
	a.Resync(oneScan(), at(time.Minute))
	col, _ = a.Snapshot(at(61*time.Second), CollectorStatus{Dropped: 3})
	if col.State != runtimefacts.StateRunning || !col.LastDropAt.Equal(at(time.Second)) {
		t.Fatalf("after resync = %+v", col)
	}

	a.ResyncFailed(errors.New("open /proc: permission denied"))
	col, _ = a.Snapshot(at(62*time.Second), CollectorStatus{})
	if col.State != runtimefacts.StateDegraded || col.Reason != ReasonResyncFailed ||
		!strings.Contains(col.Message, "permission denied") {
		t.Fatalf("resync failed = %+v", col)
	}

	// Failed wins, and its free-text fields are bounded to the schema.
	col, _ = a.Snapshot(at(63*time.Second), CollectorStatus{
		Failed: true, Reason: strings.Repeat("R", 200), Message: strings.Repeat("m", 2000),
	})
	if col.State != runtimefacts.StateFailed || len(col.Reason) != 128 || len(col.Message) != 1024 {
		t.Fatalf("failed = %+v", col)
	}
}

func TestHelpers(t *testing.T) {
	if !seconds(time.Time{}).IsZero() {
		t.Error("zero time must render absent")
	}
	if laterOf(at(2), at(1)) != at(2) || laterOf(at(1), at(2)) != at(2) {
		t.Error("laterOf")
	}
	p := &peer{lastSuccess: at(2), lastFailure: at(1)}
	if p.last() != at(2) {
		t.Error("peer.last success")
	}
	p.lastFailure = at(3)
	if p.last() != at(3) {
		t.Error("peer.last failure")
	}
	v4 := ListenerKey{Addr: any4, Port: 1}
	v6 := ListenerKey{IPv6: true, Addr: any6, Port: 1}
	if compareListenerKeys(v4, v6) >= 0 || compareListenerKeys(v6, v4) <= 0 || compareListenerKeys(v4, v4) != 0 {
		t.Error("compareListenerKeys orders IPv4 first")
	}
}

func withCookie(ev Event, cookie uint64) Event {
	ev.NetnsCookie = cookie
	return ev
}

func TestAggregator_NetnsInodeReuse(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(withCookie(connect(100, dbAddr, 5432, true), 7), at(time.Second))

	// Pod A's namespace dies and the kernel hands inode 100 to a new one: its
	// events must not land on A, and listeners wait for the next baseline.
	a.Apply(withCookie(connect(100, apiAddr, 443, true), 8), at(2*time.Second))
	a.Apply(withCookie(listen(100, EventListen, false, any4, 9000, "new"), 8), at(2*time.Second))
	p := a.workloads[pod(uidA)]
	if len(p.peers) != 1 || len(p.listeners) != 1 || a.pending[100] == nil {
		t.Fatalf("pod A = %+v, pending = %+v", p, a.pending[100])
	}

	// The next scan maps 100 to pod C, which adopts only the new namespace's peer.
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		100: {Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}},
	}}, at(time.Minute))
	c := a.workloads[pod(uidC)]
	if a.workloads[pod(uidA)] != nil || len(c.peers) != 1 || c.peers[peerKey{addr: apiAddr, port: 443}] == nil {
		t.Fatalf("pods = %+v", a.workloads)
	}
	a.Apply(withCookie(connect(100, dbAddr, 5432, true), 8), at(61*time.Second))
	if len(c.peers) != 2 || a.cookies[100] != 8 {
		t.Fatalf("pod C peers = %+v, cookie = %d", c.peers, a.cookies[100])
	}

	// A pending set is dropped when its inode is recycled before any scan.
	a.Apply(withCookie(connect(300, dbAddr, 5432, true), 1), at(62*time.Second))
	a.Apply(withCookie(connect(300, apiAddr, 443, true), 2), at(63*time.Second))
	if ps := a.pending[300]; len(ps.peers) != 1 || ps.peers[peerKey{addr: apiAddr, port: 443}] == nil {
		t.Fatalf("pending 300 = %+v", ps)
	}
}

func TestAggregator_OwnerChangeForgetsCookie(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(withCookie(connect(100, dbAddr, 5432, true), 7), at(time.Second))
	a.Apply(withCookie(connect(400, dbAddr, 5432, true), 9), at(time.Second)) // pending, stays unmapped

	// Without any event from the new namespace, the scan alone shows inode 100
	// changed hands: the old cookie must not unmap pod C on its first event.
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		100: {Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}},
	}}, at(time.Minute))
	if _, ok := a.cookies[100]; ok {
		t.Fatal("cookie of the previous owner kept")
	}
	if a.cookies[400] != 9 {
		t.Fatal("pending namespace's cookie dropped")
	}
	a.Apply(withCookie(connect(100, apiAddr, 443, true), 8), at(61*time.Second))
	if len(a.workloads[pod(uidC)].peers) != 1 || a.pending[100] != nil {
		t.Fatalf("pod C = %+v, pending = %+v", a.workloads[pod(uidC)], a.pending[100])
	}

	// An inode no longer mapped or pending keeps no cookie past a scan.
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{}}, at(2*time.Minute))
	if len(a.cookies) != 1 || a.cookies[400] != 9 {
		t.Fatalf("cookies = %v", a.cookies)
	}
}

func TestAggregator_ListenerEventsBeforeBaselineSkipped(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	scan := oneScan()
	scan.Netns[100].Listeners = map[ListenerKey]int{{Addr: any4, Port: 8080}: 2} // SO_REUSEPORT pair
	scan.Mono = 1000
	a.Resync(scan, t0)

	// Both closes happened before the scan started (one sibling closed, one
	// reopened): the scan already counts them.
	ev := listen(100, EventUnlisten, false, any4, 8080, "")
	ev.Mono = 500
	a.Apply(ev, at(time.Second))
	a.Apply(ev, at(time.Second))
	if l := a.workloads[pod(uidA)].listeners[ListenerKey{Addr: any4, Port: 8080}]; l == nil || l.count != 2 {
		t.Fatalf("listener = %+v", l)
	}
	ev.Mono = 1500
	a.Apply(ev, at(time.Second))
	if l := a.workloads[pod(uidA)].listeners[ListenerKey{Addr: any4, Port: 8080}]; l == nil || l.count != 1 {
		t.Fatalf("listener after a later close = %+v", l)
	}
}

func TestAggregator_PortZeroNeverRendered(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	a.Apply(listen(100, EventListen, false, any4, 0, "x"), at(time.Second))
	a.Apply(listen(100, EventUnlisten, false, any4, 0, "x"), at(time.Second))
	a.Apply(connect(100, dbAddr, 0, false), at(time.Second))
	a.Apply(connect(300, dbAddr, 0, false), at(time.Second))
	_, pods := a.Snapshot(at(time.Second), CollectorStatus{})
	p := podByUID(t, pods, uidA)
	if len(p.Listeners) != 1 || p.Listeners[0].Port != 8080 || len(p.Connections) != 0 || len(a.pending) != 0 {
		t.Fatalf("pod A = %+v, pending = %v", p, a.pending)
	}
}

func TestAggregator_NodeListenerBound(t *testing.T) {
	b := DefaultBounds()
	b.MaxNodeListeners = 2
	a := NewAggregator(b, nil)
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		100: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{{Addr: any4, Port: 1}: 1, {Addr: any4, Port: 2}: 1}},
		200: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{{Addr: any4, Port: 3}: 1}},
		300: {Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}, ListenersTruncated: true},
	}}, t0)
	// UIDs sort B (1111...), A (0f1e... sorts first). A takes the node budget.
	if len(a.workloads[pod(uidA)].listeners) != 2 || a.workloads[pod(uidA)].listenersTruncated ||
		len(a.workloads[pod(uidB)].listeners) != 0 || !a.workloads[pod(uidB)].listenersTruncated ||
		!a.workloads[pod(uidC)].listenersTruncated || a.totalListeners != 2 {
		t.Fatalf("A = %+v, B = %+v, total = %d", a.workloads[pod(uidA)], a.workloads[pod(uidB)], a.totalListeners)
	}
	a.Apply(listen(100, EventListen, false, any4, 9, "x"), at(time.Second))
	if len(a.workloads[pod(uidA)].listeners) != 2 || !a.workloads[pod(uidA)].listenersTruncated {
		t.Fatalf("A over the node bound = %+v", a.workloads[pod(uidA)])
	}
	a.Apply(listen(100, EventUnlisten, false, any4, 1, ""), at(time.Second))
	a.Apply(listen(200, EventListen, false, any4, 3, "y"), at(time.Second))
	if a.totalListeners != 2 || len(a.workloads[pod(uidB)].listeners) != 1 {
		t.Fatalf("B after room freed = %+v, total = %d", a.workloads[pod(uidB)], a.totalListeners)
	}
}

func TestAggregator_PodInSeveralNetnsIsShared(t *testing.T) {
	for range 20 { // map order differs per run; the result must not
		a := NewAggregator(DefaultBounds(), nil)
		a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
			100: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{{Addr: any4, Port: 1}: 1}},
			101: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{{Addr: any4, Port: 2}: 1}},
			200: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
		}}, t0)
		a.Apply(connect(100, dbAddr, 1, true), at(time.Second))
		a.Apply(connect(101, dbAddr, 1, true), at(time.Second))
		_, pods := a.Snapshot(at(time.Second), CollectorStatus{})
		p := podByUID(t, pods, uidA)
		if !p.Shared || len(p.Listeners) != 0 || len(p.Connections) != 0 || len(a.pending) != 0 ||
			a.owners[100] != (runtimefacts.Identity{}) || a.owners[101] != (runtimefacts.Identity{}) || a.owners[200] != pod(uidB) {
			t.Fatalf("pod A = %+v, owners = %v", p, a.owners)
		}
	}

	// Over the pod limit, every namespace of the untracked pod is disowned.
	a := NewAggregator(Bounds{MaxWorkloads: 1, MaxListeners: 1, MaxConnections: 1, MaxNodeListeners: 1, MaxNodeConnections: 1}, nil)
	a.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{
		100: {Workloads: podIDs(uidA), Listeners: map[ListenerKey]int{}},
		200: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
		201: {Workloads: podIDs(uidB), Listeners: map[ListenerKey]int{}},
	}}, t0)
	if v, ok := a.owners[200]; !ok || v != (runtimefacts.Identity{}) {
		t.Fatalf("owners = %v", a.owners)
	}
	if v, ok := a.owners[201]; !ok || v != (runtimefacts.Identity{}) {
		t.Fatalf("owners = %v", a.owners)
	}
}

func TestAggregator_AwaitingBaselineAfterAttach(t *testing.T) {
	a := NewAggregator(DefaultBounds(), nil)
	a.Resync(oneScan(), t0)
	col, _ := a.Snapshot(at(2*time.Second), CollectorStatus{AttachedAt: at(time.Second)})
	if col.State != runtimefacts.StateDegraded || col.Reason != ReasonAwaitingBaseline {
		t.Fatalf("before the post-attach scan = %+v", col)
	}
	a.Resync(oneScan(), at(3*time.Second))
	col, _ = a.Snapshot(at(4*time.Second), CollectorStatus{AttachedAt: at(time.Second)})
	if col.State != runtimefacts.StateRunning {
		t.Fatalf("after the post-attach scan = %+v", col)
	}
}

// byCgroup resolves cgroup id n to pod uids[n-1]; anything else names no
// workload.
func byCgroup(uids ...string) Resolve {
	return func(cg uint64) (runtimefacts.Identity, bool) {
		if cg == 0 || cg > uint64(len(uids)) {
			return runtimefacts.Identity{}, false
		}
		return pod(uids[cg-1]), true
	}
}

func inCgroup(ev Event, cg uint64) Event {
	ev.CgroupID = cg
	return ev
}

// TestAggregator_CgroupAttribution: an event whose cgroup names a workload goes
// to it whatever its namespace says, a shared workload stays empty, and an
// unknown cgroup falls back to the namespace.
func TestAggregator_CgroupAttribution(t *testing.T) {
	a := NewAggregator(DefaultBounds(), byCgroup(uidA, uidB, uidC))
	a.Resync(oneScan(), t0)

	// Pod A by cgroup, from a namespace inode nobody mapped.
	a.Apply(inCgroup(connect(999, dbAddr, 5432, true), 1), at(time.Second))
	a.Apply(inCgroup(listen(999, EventListen, false, any4, 9000, "web"), 1), at(time.Second))
	a.Apply(inCgroup(listen(999, EventListen, false, any4, 0, "web"), 1), at(time.Second))
	a.Apply(inCgroup(connect(999, dbAddr, 0, true), 1), at(time.Second))
	// Pod B is host network: shared, so nothing is attributed to it.
	a.Apply(inCgroup(connect(1, dbAddr, 5432, true), 2), at(time.Second))
	a.Apply(inCgroup(listen(1, EventListen, false, any4, 22, "sshd"), 2), at(time.Second))
	// Cgroup 9 names no workload: the namespace decides (100 is pod A).
	a.Apply(inCgroup(connect(100, apiAddr, 443, false), 9), at(time.Second))
	// Pod C is unknown to the scan: held under its identity, listeners left to
	// the baseline.
	a.Apply(inCgroup(connect(300, apiAddr, 443, true), 3), at(time.Second))
	a.Apply(inCgroup(listen(300, EventListen, false, any4, 80, "c"), 3), at(time.Second))

	if len(a.pending) != 0 || a.pendingIDs[pod(uidC)] == nil {
		t.Fatalf("pending = %v, pendingIDs = %v", a.pending, a.pendingIDs)
	}
	_, ws := a.Snapshot(at(time.Second), CollectorStatus{})
	pa, pb := podByUID(t, ws, uidA), podByUID(t, ws, uidB)
	if len(pa.Connections) != 2 || len(pa.Listeners) != 2 || len(pb.Connections)+len(pb.Listeners) != 0 {
		t.Fatalf("pod A = %+v, pod B = %+v", pa, pb)
	}

	// The scan finds pod C in its own namespace: it adopts what was held.
	scan := oneScan()
	scan.Netns[300] = &NetnsScan{Workloads: podIDs(uidC), Listeners: map[ListenerKey]int{}}
	a.Resync(scan, at(time.Minute))
	if len(a.pendingIDs) != 0 || len(a.workloads[pod(uidC)].peers) != 1 {
		t.Fatalf("pod C = %+v, pendingIDs = %v", a.workloads[pod(uidC)], a.pendingIDs)
	}

	// Held events of a workload the scan then finds shared are dropped.
	a.Apply(inCgroup(connect(1, apiAddr, 443, true), 4), at(time.Minute)) // uid 4 does not exist: namespace 1, shared
	b := NewAggregator(DefaultBounds(), byCgroup(uidA))
	b.Apply(inCgroup(connect(5, dbAddr, 5432, true), 1), t0)
	b.Resync(Scan{HostNetns: 1, Netns: map[uint32]*NetnsScan{1: {Workloads: podIDs(uidA), Shared: true}}}, at(time.Second))
	if len(b.pendingIDs) != 0 || len(b.workloads[pod(uidA)].peers) != 0 {
		t.Fatalf("shared pod adopted held events: %+v", b.workloads[pod(uidA)])
	}

	// The identity pending bound, and retention, match the namespace's.
	c := NewAggregator(Bounds{MaxWorkloads: 1, MaxConnections: 1, Retention: time.Minute}, byCgroup(uidA, uidB))
	c.Apply(inCgroup(connect(5, dbAddr, 5432, true), 1), t0)
	c.Apply(inCgroup(connect(5, dbAddr, 5432, true), 2), t0)
	if len(c.pendingIDs) != 1 {
		t.Fatalf("pendingIDs = %v", c.pendingIDs)
	}
	c.Snapshot(at(2*time.Minute), CollectorStatus{})
	if len(c.pendingIDs) != 0 {
		t.Fatal("expired identity pending set kept")
	}
}

// TestScanProc_AllKinds: with no filter every workload kind is tracked, and a
// process that matches none is not.
func TestScanProc_AllKinds(t *testing.T) {
	p := mapProc{
		files: fstest.MapFS{
			"1/cgroup":  {Data: []byte("0::/init.scope\n")},
			"2/cgroup":  {Data: []byte("0::/\n")},
			"3/cgroup":  {Data: []byte("0::/docker/" + strings.Repeat("ab", 32) + "\n")},
			"4/cgroup":  {Data: []byte("0::/pacto-run-1\n")},
			"9/cgroup":  cg(uidA),
			"10/cgroup": cg(uidA),
		},
		links: map[string]string{
			"1/ns/net": "net:[1]", "2/ns/net": "net:[1]", "3/ns/net": "net:[300]", "4/ns/net": "net:[1]",
			"9/ns/net": "net:[100]", "10/ns/net": "net:[100]",
		},
	}
	scan, err := ScanProc(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	host := scan.Netns[1]
	if !host.Shared || len(host.Workloads) != 2 || host.Workloads[0].Kind != runtimefacts.KindSession ||
		host.Workloads[1].Kind != runtimefacts.KindUnit {
		t.Fatalf("host netns = %+v", host)
	}
	if c := scan.Netns[300]; c.Shared || len(c.Workloads) != 1 || c.Workloads[0].Kind != runtimefacts.KindContainer {
		t.Fatalf("container netns = %+v", c)
	}
	if a := scan.Netns[100]; a.Shared || len(a.Workloads) != 1 {
		t.Fatalf("pod netns = %+v", a)
	}
}

// TestAggregator_HostNetworkByProcess: a unit sharing the host namespace whose
// listeners the scan read from its own descriptors gets its own facts once
// events are attributed by cgroup, while the namespace stays unattributable.
// Without cgroup attribution, or in more than one namespace, it stays shared.
func TestAggregator_HostNetworkByProcess(t *testing.T) {
	svc := unit("a.service")
	resolve := func(cg uint64) (runtimefacts.Identity, bool) { return svc, cg == 7 }
	scan := Scan{HostNetns: 1,
		Netns:      map[uint32]*NetnsScan{1: {Workloads: []runtimefacts.Identity{svc}, Shared: true}},
		Attributed: map[runtimefacts.Identity]*NetnsScan{svc: {Listeners: map[ListenerKey]int{{Addr: any4, Port: 8080}: 1}}},
	}
	a := NewAggregator(DefaultBounds(), resolve)
	a.Apply(inCgroup(connect(1, dbAddr, 5432, true), 7), t0) // before the scan: held under its identity
	a.Resync(scan, at(time.Second))
	a.Apply(inCgroup(connect(1, apiAddr, 443, false), 7), at(2*time.Second))
	a.Apply(inCgroup(listen(1, EventListen, false, any4, 9000, "a"), 7), at(2*time.Second))
	a.Apply(connect(1, apiAddr, 80, true), at(2*time.Second)) // by namespace: the host's, unattributable
	_, ws := a.Snapshot(at(3*time.Second), CollectorStatus{})
	if len(ws) != 1 || ws[0].Shared || len(ws[0].Listeners) != 2 || len(ws[0].Connections) != 2 {
		t.Fatalf("unit = %+v", ws)
	}
	if id, ok := a.owners[1]; !ok || id != (runtimefacts.Identity{}) {
		t.Fatalf("host namespace owner = %v, %v", id, ok)
	}

	for name, agg := range map[string]*Aggregator{
		"no cgroup attribution": NewAggregator(DefaultBounds(), nil),
		"several namespaces":    NewAggregator(DefaultBounds(), resolve),
	} {
		s := scan
		if name == "several namespaces" {
			s.Netns = map[uint32]*NetnsScan{1: scan.Netns[1], 2: {Workloads: []runtimefacts.Identity{svc}}}
		}
		agg.Resync(s, t0)
		if _, ws := agg.Snapshot(t0, CollectorStatus{}); !ws[0].Shared || len(ws[0].Listeners) != 0 {
			t.Errorf("%s: unit = %+v", name, ws)
		}
	}
}
