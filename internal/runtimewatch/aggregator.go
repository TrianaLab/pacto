/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"cmp"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// Bounds caps what the aggregator keeps, so a busy host costs bounded memory and
// publishes a bounded object. The per-workload limits are the
// NodeRuntimeObservation schema's maxItems.
type Bounds struct {
	MaxWorkloads       int
	MaxListeners       int
	MaxConnections     int
	MaxNodeListeners   int
	MaxNodeConnections int
	// MaxDNSNamesPerWorkload caps the addresses a workload's DNS answers name;
	// the least recently answered address is evicted first.
	MaxDNSNamesPerWorkload int
	// MaxNodeHosts caps the connections on the host that carry a name.
	MaxNodeHosts int
	// Retention is how long a peer is kept after its last attempt.
	Retention time.Duration
}

// DefaultBounds are the limits from the design. The node-wide caps keep the
// worst-case status under MaxStatusBytes (1 MiB = 1,048,576 bytes), which the
// per-workload bounds alone would not (256 x 128 peers is 6.8 MB). Worst-case
// JSON sizes, every field at its longest (IPv6 addresses of 39 characters,
// int64 counters of 19 digits, both timestamps, a 15-byte comm, a 253-byte
// host on every named connection and the ambiguous flag on every other one):
//
//	named connection   472 bytes x  512 =  241,664
//	other connection   226 bytes x 2560 =  578,560
//	listener           111 bytes x 1024 =  113,664
//	workload          ~190 bytes x  256 =   48,640
//	node frame, collection and dns   ~   1,700
//	total, measured by the test         973,916
//
// Room for names costs connections: the node-wide connection cap is 3072
// (4096 before hosts were published).
//
// TestDefaultBoundsFitMaxStatusBytes builds that object and checks it. The
// publisher still measures and trims every status as a backstop.
func DefaultBounds() Bounds {
	return Bounds{
		MaxWorkloads:       256,
		MaxListeners:       64,
		MaxConnections:     128,
		MaxNodeListeners:   1024,
		MaxNodeConnections: 3072,
		// ponytail: 64 x 4 names per workload, a few KiB; raise with the budget table.
		MaxDNSNamesPerWorkload: 64,
		MaxNodeHosts:           512,
		Retention:              10 * time.Minute,
	}
}

// Collection reasons for a Degraded state.
const (
	// ReasonPodLimitExceeded keeps the name it has on the NodeRuntimeObservation
	// API, where every workload is a pod.
	ReasonPodLimitExceeded = "PodLimitExceeded"
	ReasonResyncFailed     = "ResyncFailed"
	ReasonEventsDropped    = "EventsDropped"
	// ReasonAwaitingBaseline: the program was (re)attached after the last /proc
	// baseline, so events before the attach were missed and nothing since has
	// filled the gap.
	ReasonAwaitingBaseline = "AwaitingBaseline"
	// ReasonObjectTooLarge: the status was trimmed to fit MaxStatusBytes.
	ReasonObjectTooLarge = "ObjectTooLarge"
)

// DNS capture reasons.
const (
	// ReasonDNSNotAttached: the DNS program is not attached, so no names.
	ReasonDNSNotAttached = "DNSNotAttached"
	// ReasonAnswersDropped: DNS answers were lost within the retention window.
	ReasonAnswersDropped = "AnswersDropped"
	// ReasonDNSUnattributed: an answer reached no workload within the
	// retention window, so a connection may lack its name.
	ReasonDNSUnattributed = "DNSUnattributed"
	// ReasonHostLimit: more named connections than the host publishes.
	ReasonHostLimit = "HostLimit"
)

// maxNamesPerAddress caps the names one address keeps in a workload's table.
const maxNamesPerAddress = 4

// CollectorStatus is the kernel side's health as the loader reports it.
type CollectorStatus struct {
	// Failed is true while the eBPF program is not attached.
	Failed  bool
	Reason  string
	Message string
	// Dropped is the cumulative ring buffer drop count.
	Dropped uint64
	// AttachedAt is when the program was last attached; zero while detached.
	AttachedAt time.Time
	// DNSState is the DNS program's state; empty reads as not attached.
	DNSState  runtimefacts.CollectorState
	DNSReason string
	// DNSDropped is the cumulative DNS ring buffer drop count.
	DNSDropped uint64
}

type peerKey struct {
	addr netip.Addr
	port uint16
}

type peer struct {
	successes, failures      int64
	lastSuccess, lastFailure time.Time
}

func (p *peer) last() time.Time {
	if p.lastSuccess.After(p.lastFailure) {
		return p.lastSuccess
	}
	return p.lastFailure
}

type listener struct {
	count   int
	process string
}

// dnsName is one name an answer gave an address, and when it was last seen.
type dnsName struct {
	name string
	seen time.Time
}

// peerSet is a bounded set of peers and the names the same workload's DNS
// answers gave their addresses. truncatedAt records the last time a new peer
// was turned away.
type peerSet struct {
	peers       map[peerKey]*peer
	names       map[netip.Addr][]dnsName
	truncatedAt time.Time
}

type workloadState struct {
	shared             bool
	observedSince      time.Time
	baselineAt         time.Time
	listeners          map[ListenerKey]*listener
	listenersTruncated bool
	peerSet
}

// Resolve maps a socket's cgroup id to the workload whose cgroup created it; ok
// is false when the id is unknown (0, cgroup v1, a deleted cgroup) or names no
// workload, and the event falls back to its network namespace.
type Resolve func(cgroupID uint64) (id runtimefacts.Identity, ok bool)

// noIdentity resolves nothing: attribution by network namespace only.
func noIdentity(uint64) (runtimefacts.Identity, bool) { return runtimefacts.Identity{}, false }

// Aggregator folds kernel events and /proc baselines into per-workload facts.
// It is not safe for concurrent use: the Runner owns it from one goroutine.
type Aggregator struct {
	bounds  Bounds
	resolve Resolve
	// byCgroup is set when events are attributed by cgroup: only then can a
	// workload sharing a namespace be told apart by its processes' sockets.
	byCgroup bool
	// owners maps every netns the last scan saw to its workload; the zero
	// identity marks a namespace whose events cannot be attributed (shared or
	// over the workload limit).
	owners map[uint32]runtimefacts.Identity
	// cookies is the netns cookie events last reported for each inode in owners
	// or pending, to notice an inode the kernel recycled for a new namespace.
	cookies   map[uint32]uint64
	workloads map[runtimefacts.Identity]*workloadState
	// pending holds connect outcomes from namespaces no scan has mapped yet, so a
	// workload that dials its dependencies the moment it starts is not missed. A
	// pending set only ever holds events of the namespace that currently has its
	// cookie (a cookie change drops it), so everything adopted from it happened
	// in the namespace the scan maps, never in an earlier holder of the inode.
	pending map[uint32]*peerSet
	// pendingIDs is the same for events whose cgroup named a workload no scan has
	// seen yet. Cgroup ids are never reused, so no cookie check is needed.
	pendingIDs     map[runtimefacts.Identity]*peerSet
	totalPeers     int
	totalListeners int
	// scanMono is the last baseline's Scan.Mono.
	scanMono uint64

	limitHit   bool
	lastResync time.Time
	resyncErr  error
	dropped    uint64
	lastDrop   time.Time

	dnsDropped       uint64
	lastDNSDrop      time.Time
	lastUnattributed time.Time
}

// NewAggregator returns an empty aggregator. A nil resolve attributes by
// network namespace only.
func NewAggregator(b Bounds, resolve Resolve) *Aggregator {
	byCgroup := resolve != nil
	if !byCgroup {
		resolve = noIdentity
	}
	return &Aggregator{
		bounds:     b,
		resolve:    resolve,
		byCgroup:   byCgroup,
		owners:     map[uint32]runtimefacts.Identity{},
		cookies:    map[uint32]uint64{},
		workloads:  map[runtimefacts.Identity]*workloadState{},
		pending:    map[uint32]*peerSet{},
		pendingIDs: map[runtimefacts.Identity]*peerSet{},
	}
}

// Apply folds one kernel event in. An event whose cgroup names a workload goes
// to that workload; any other goes to the owner of its network namespace.
//
// Residual windows: an inode recycled before its new namespace's first event
// is indistinguishable from its old one until the next scan sees the new owner
// (the scan cannot read cookies), and a listener closed between a scan's start
// and its read of that namespace's table is decremented twice, which can hide
// an SO_REUSEPORT sibling until the next baseline.
func (a *Aggregator) Apply(ev Event, now time.Time) {
	if id, ok := a.resolve(ev.CgroupID); ok {
		a.applyIdentified(id, ev, now)
		return
	}
	a.applyByNetns(ev, now)
}

// applyByNetns folds in an event its network namespace attributes.
func (a *Aggregator) applyByNetns(ev Event, now time.Time) {
	if old, ok := a.cookies[ev.Netns]; ok && ev.NetnsCookie != 0 && old != ev.NetnsCookie {
		// The kernel recycled the inode for a new namespace: what was learned
		// or mapped under it belongs to the old one. The next scan maps it anew.
		delete(a.cookies, ev.Netns)
		delete(a.pending, ev.Netns)
		delete(a.owners, ev.Netns)
	}
	id, known := a.owners[ev.Netns]
	if known && ev.NetnsCookie != 0 {
		a.cookies[ev.Netns] = ev.NetnsCookie
	}
	switch ev.Kind {
	case EventDNSAnswer:
		a.answerByNetns(id, known, ev, now)
	case EventListen, EventUnlisten:
		// Listeners in unmapped namespaces are left to the next baseline, as are
		// transitions from before the last scan started, which it already counts.
		// Port 0 is never a reachable listener; the program drops it too.
		if id == (runtimefacts.Identity{}) || ev.LocalPort == 0 || ev.Mono < a.scanMono {
			return
		}
		a.applyListener(a.workloads[id], ev)
	case EventConnectOK, EventConnectFail:
		if ev.RemotePort == 0 {
			return
		}
		key, ok := peerKeyOf(ev)
		switch {
		case id != (runtimefacts.Identity{}):
			a.record(&a.workloads[id].peerSet, key, ok, now, true)
		case known:
			// Shared namespace: unattributable.
		default:
			a.recordPending(ev, key, ok, now)
		}
	}
}

// recordPending holds a connect outcome of a namespace no scan has mapped yet.
func (a *Aggregator) recordPending(ev Event, key peerKey, ok bool, now time.Time) {
	ps := pendingSet(a, a.pending, ev.Netns)
	if ps == nil {
		return
	}
	if ev.NetnsCookie != 0 {
		a.cookies[ev.Netns] = ev.NetnsCookie
	}
	a.record(ps, key, ok, now, false)
}

// answerByNetns learns a DNS answer its network namespace attributes; a shared
// namespace's answers are dropped like its socket events.
func (a *Aggregator) answerByNetns(id runtimefacts.Identity, known bool, ev Event, now time.Time) {
	switch {
	case id != (runtimefacts.Identity{}):
		a.answer(&a.workloads[id].peerSet, ev, now)
	case !known:
		ps := pendingSet(a, a.pending, ev.Netns)
		if ps != nil && ev.NetnsCookie != 0 {
			a.cookies[ev.Netns] = ev.NetnsCookie
		}
		a.answer(ps, ev, now)
	}
}

// answer learns the names of one DNS answer into ps; a nil ps (the pending
// bound was hit) counts the answer as unattributed.
func (a *Aggregator) answer(ps *peerSet, ev Event, now time.Time) {
	name, addrs, ok := parseDNSAnswer(ev.DNS)
	if !ok {
		return
	}
	if ps == nil {
		a.lastUnattributed = now
		return
	}
	for _, addr := range addrs {
		a.learn(ps, addr, dnsName{name: name, seen: now})
	}
}

// learn records that n names addr, within the per-address and per-workload
// bounds: a name (see evictName), then the least recently seen address, is
// evicted.
func (a *Aggregator) learn(ps *peerSet, addr netip.Addr, n dnsName) {
	if ps.names == nil {
		ps.names = map[netip.Addr][]dnsName{}
	}
	list, had := ps.names[addr]
	if i := slices.IndexFunc(list, func(o dnsName) bool { return o.name == n.name }); i >= 0 {
		list[i].seen = laterOf(list[i].seen, n.seen)
		return
	}
	if !had && len(ps.names) >= a.bounds.MaxDNSNamesPerWorkload {
		delete(ps.names, oldestAddr(ps.names))
	}
	list = append(list, n)
	if len(list) > maxNamesPerAddress {
		i := evictName(list)
		list = slices.Delete(list, i, i+1)
	}
	ps.names[addr] = list
}

func lastSeen(list []dnsName) time.Time {
	return list[newestName(list)].seen
}

// evictName is the name a full list drops: the least recently seen one whose
// registrable domain another name keeps, so every domain the address answered
// for stays and hostOf still reads ambiguous; the least recently seen when
// every domain is distinct.
func evictName(list []dnsName) int {
	i := -1
	for j, n := range list {
		d := registrableDomain(n.name)
		shared := slices.ContainsFunc(list, func(o dnsName) bool { return o.name != n.name && registrableDomain(o.name) == d })
		if shared && (i < 0 || n.seen.Before(list[i].seen)) {
			i = j
		}
	}
	if i < 0 {
		return oldestName(list)
	}
	return i
}

func oldestName(list []dnsName) int {
	return slices.IndexFunc(list, func(n dnsName) bool {
		return !slices.ContainsFunc(list, func(o dnsName) bool { return o.seen.Before(n.seen) })
	})
}

func newestName(list []dnsName) int {
	return slices.IndexFunc(list, func(n dnsName) bool {
		return !slices.ContainsFunc(list, func(o dnsName) bool { return o.seen.After(n.seen) })
	})
}

// oldestAddr is the address whose names were seen least recently, the lowest
// address on a tie.
func oldestAddr(names map[netip.Addr][]dnsName) netip.Addr {
	keys := slices.SortedFunc(maps.Keys(names), netip.Addr.Compare)
	return slices.MinFunc(keys, func(x, y netip.Addr) int {
		return lastSeen(names[x]).Compare(lastSeen(names[y]))
	})
}

// applyIdentified folds in an event its cgroup attributed. A workload the last
// scan found shared stays empty, exactly as by namespace.
func (a *Aggregator) applyIdentified(id runtimefacts.Identity, ev Event, now time.Time) {
	w := a.workloads[id]
	switch ev.Kind {
	case EventDNSAnswer:
		switch {
		case w == nil:
			a.answer(pendingSet(a, a.pendingIDs, id), ev, now)
		case !w.shared:
			a.answer(&w.peerSet, ev, now)
		}
	case EventListen, EventUnlisten:
		if w == nil || w.shared || ev.LocalPort == 0 || ev.Mono < a.scanMono {
			return
		}
		a.applyListener(w, ev)
	case EventConnectOK, EventConnectFail:
		if ev.RemotePort == 0 {
			return
		}
		key, ok := peerKeyOf(ev)
		switch {
		case w == nil:
			if ps := pendingSet(a, a.pendingIDs, id); ps != nil {
				a.record(ps, key, ok, now, false)
			}
		case !w.shared:
			a.record(&w.peerSet, key, ok, now, true)
		}
	}
}

func peerKeyOf(ev Event) (peerKey, bool) {
	return peerKey{addr: ev.RemoteAddr.Unmap(), port: ev.RemotePort}, ev.Kind == EventConnectOK
}

// pendingSet returns the pending set under k, creating it while fewer than
// MaxWorkloads exist; nil when the bound is hit.
func pendingSet[K comparable](a *Aggregator, m map[K]*peerSet, k K) *peerSet {
	ps := m[k]
	if ps == nil {
		if len(m) >= a.bounds.MaxWorkloads {
			return nil
		}
		ps = &peerSet{peers: map[peerKey]*peer{}}
		m[k] = ps
	}
	return ps
}

func (a *Aggregator) applyListener(p *workloadState, ev Event) {
	key := ListenerKey{IPv6: ev.IPv6, Addr: ev.LocalAddr, Port: ev.LocalPort}
	l := p.listeners[key]
	if ev.Kind == EventUnlisten {
		if l != nil {
			if l.count--; l.count <= 0 {
				delete(p.listeners, key)
				a.totalListeners--
			}
		}
		return
	}
	switch {
	case l != nil:
		l.count++
		l.process = ev.Comm
	case len(p.listeners) >= a.bounds.MaxListeners || a.totalListeners >= a.bounds.MaxNodeListeners:
		p.listenersTruncated = true
	default:
		p.listeners[key] = &listener{count: 1, process: ev.Comm}
		a.totalListeners++
	}
}

// record counts one connect outcome. Workload peers also count against the
// host-wide budget; pending peers are bounded per set only.
func (a *Aggregator) record(ps *peerSet, key peerKey, ok bool, now time.Time, counted bool) {
	p := ps.peers[key]
	if p == nil {
		if len(ps.peers) >= a.bounds.MaxConnections || (counted && a.totalPeers >= a.bounds.MaxNodeConnections) {
			ps.truncatedAt = now
			return
		}
		p = &peer{}
		ps.peers[key] = p
		if counted {
			a.totalPeers++
		}
	}
	if ok {
		p.successes++
		p.lastSuccess = now
	} else {
		p.failures++
		p.lastFailure = now
	}
}

// Resync replaces the workload set and every listener table with a /proc
// baseline. Listener counts and connection history survive for workloads that
// remain. Workloads are visited in kind and ID order, so the same scan always
// yields the same state.
func (a *Aggregator) Resync(scan Scan, now time.Time) {
	netnsOf := map[runtimefacts.Identity][]uint32{}
	for ns, n := range scan.Netns {
		for _, id := range n.Workloads {
			netnsOf[id] = append(netnsOf[id], ns)
		}
	}
	for id, w := range a.workloads {
		if _, ok := netnsOf[id]; !ok {
			a.totalPeers -= len(w.peers)
			delete(a.workloads, id)
		}
	}

	prev := a.owners
	a.owners = map[uint32]runtimefacts.Identity{}
	a.limitHit = false
	a.totalListeners = 0
	for _, id := range slices.SortedFunc(maps.Keys(netnsOf), compareIdentities) {
		a.place(id, netnsOf[id], scan, prev, now)
	}
	for ns := range a.pending {
		if _, ok := a.owners[ns]; ok {
			delete(a.pending, ns)
		}
	}
	for ns := range a.cookies {
		if _, ok := a.owners[ns]; !ok && a.pending[ns] == nil {
			delete(a.cookies, ns)
		}
	}
	a.scanMono = scan.Mono
	a.lastResync = now
	a.resyncErr = nil
}

// place applies one workload of a baseline: tracked or not, shared, told apart
// by its processes' sockets, or the owner of its one namespace. prev is the
// previous baseline's owners.
func (a *Aggregator) place(id runtimefacts.Identity, nss []uint32, scan Scan, prev map[uint32]runtimefacts.Identity, now time.Time) {
	// A workload in more than one namespace (one of its processes unshared its
	// own) has no namespace that is unambiguously its own: it is treated like a
	// shared one, whatever order the scan listed them in.
	shared := len(nss) > 1 || scan.Netns[nss[0]].Shared
	// One namespace it shares, its listeners read from its own descriptors and
	// its events attributed by cgroup: its facts are its own after all.
	own := scan.Attributed[id]
	byProcess := len(nss) == 1 && shared && own != nil && a.byCgroup
	w := a.workloads[id]
	if w == nil {
		if len(a.workloads) >= a.bounds.MaxWorkloads {
			a.limitHit = true
			a.disown(nss)
			return
		}
		w = &workloadState{observedSince: now, peerSet: peerSet{peers: map[peerKey]*peer{}}}
		a.workloads[id] = w
	}
	w.baselineAt = now
	w.shared = shared && !byProcess
	switch {
	case byProcess:
		// The namespace stays unattributable: only cgroup-attributed events
		// reach this workload.
		a.disown(nss)
		a.baselineListeners(w, own)
	case shared:
		a.disown(nss)
		a.totalPeers -= len(w.peers)
		w.peers = map[peerKey]*peer{}
		w.names = nil
		w.listeners = nil
		w.listenersTruncated = false
		delete(a.pendingIDs, id)
		return
	default:
		ns := nss[0]
		a.owners[ns] = id
		if old, had := prev[ns]; had && old != id {
			// The inode changed hands since the last scan: its recorded cookie is
			// the previous namespace's, and would otherwise unmap the new owner on
			// its first event. (Nothing is pending under it: an owned
			// inode's events go to its workload, and a cookie change both drops the
			// stale pending set and removes the inode from the owners.)
			delete(a.cookies, ns)
		}
		a.baselineListeners(w, scan.Netns[ns])
		if ps := a.pending[ns]; ps != nil {
			a.adopt(w, ps)
		}
	}
	if ps := a.pendingIDs[id]; ps != nil {
		a.adopt(w, ps)
	}
	delete(a.pendingIDs, id)
}

// disown marks namespaces as known but unattributable.
func (a *Aggregator) disown(nss []uint32) {
	for _, ns := range nss {
		a.owners[ns] = runtimefacts.Identity{}
	}
}

// baselineListeners replaces a workload's listeners with the scan's, within the
// per-workload and remaining host-wide bounds.
func (a *Aggregator) baselineListeners(p *workloadState, n *NetnsScan) {
	old := p.listeners
	p.listeners = map[ListenerKey]*listener{}
	keys := slices.SortedFunc(maps.Keys(n.Listeners), compareListenerKeys)
	room := min(a.bounds.MaxListeners, a.bounds.MaxNodeListeners-a.totalListeners)
	p.listenersTruncated = n.ListenersTruncated || len(keys) > room
	for _, k := range keys[:min(len(keys), room)] {
		l := &listener{count: n.Listeners[k]}
		if o := old[k]; o != nil {
			l.process = o.process
		}
		p.listeners[k] = l
	}
	a.totalListeners += len(p.listeners)
}

func (a *Aggregator) adopt(p *workloadState, ps *peerSet) {
	for k, src := range ps.peers {
		if dst := p.peers[k]; dst != nil {
			dst.successes += src.successes
			dst.failures += src.failures
			dst.lastSuccess = laterOf(dst.lastSuccess, src.lastSuccess)
			dst.lastFailure = laterOf(dst.lastFailure, src.lastFailure)
			continue
		}
		if len(p.peers) >= a.bounds.MaxConnections || a.totalPeers >= a.bounds.MaxNodeConnections {
			p.truncatedAt = laterOf(p.truncatedAt, src.last())
			continue
		}
		p.peers[k] = src
		a.totalPeers++
	}
	p.truncatedAt = laterOf(p.truncatedAt, ps.truncatedAt)
	for addr, list := range ps.names {
		for _, n := range list {
			a.learn(&p.peerSet, addr, n)
		}
	}
}

func laterOf(x, y time.Time) time.Time {
	if y.After(x) {
		return y
	}
	return x
}

// ResyncFailed records a failed baseline; the previous one stays in effect.
func (a *Aggregator) ResyncFailed(err error) { a.resyncErr = err }

// ObserveDrops records the cumulative ring buffer drop count.
func (a *Aggregator) ObserveDrops(total uint64, now time.Time) {
	if total > a.dropped {
		a.dropped = total
		a.lastDrop = now
	}
}

// expire drops peers whose last attempt is older than the retention window.
func (a *Aggregator) expire(now time.Time) {
	cutoff := now.Add(-a.bounds.Retention)
	for _, w := range a.workloads {
		for k, pr := range w.peers {
			if pr.last().Before(cutoff) {
				delete(w.peers, k)
				a.totalPeers--
			}
		}
		expireNames(w.names, cutoff)
	}
	expirePending(a.pending, cutoff)
	expirePending(a.pendingIDs, cutoff)
}

func expirePending[K comparable](m map[K]*peerSet, cutoff time.Time) {
	for k, ps := range m {
		for pk, pr := range ps.peers {
			if pr.last().Before(cutoff) {
				delete(ps.peers, pk)
			}
		}
		expireNames(ps.names, cutoff)
		if len(ps.peers) == 0 && len(ps.names) == 0 {
			delete(m, k)
		}
	}
}

// expireNames drops names last seen before cutoff, and addresses left with none.
func expireNames(names map[netip.Addr][]dnsName, cutoff time.Time) {
	for addr, list := range names {
		list = slices.DeleteFunc(list, func(n dnsName) bool { return n.seen.Before(cutoff) })
		if len(list) == 0 {
			delete(names, addr)
			continue
		}
		names[addr] = list
	}
}

// Snapshot expires old peers and renders the collection health and workloads,
// sorted so that the same state always renders the same object. Timestamps are
// at whole seconds, the NodeRuntimeObservation API's precision.
func (a *Aggregator) Snapshot(now time.Time, c CollectorStatus) (runtimefacts.Collection, []runtimefacts.Workload, runtimefacts.DNSCollection) {
	a.ObserveDrops(c.Dropped, now)
	if c.DNSDropped > a.dnsDropped {
		a.dnsDropped, a.lastDNSDrop = c.DNSDropped, now
	}
	a.expire(now)

	col := runtimefacts.Collection{
		State:         runtimefacts.StateRunning,
		DroppedEvents: int64(a.dropped),
		LastDropAt:    seconds(a.lastDrop),
		LastResyncAt:  seconds(a.lastResync),
	}
	switch {
	case c.Failed:
		col.State, col.Reason, col.Message = runtimefacts.StateFailed, c.Reason, c.Message
	case c.AttachedAt.After(a.lastResync):
		col.State, col.Reason = runtimefacts.StateDegraded, ReasonAwaitingBaseline
		col.Message = "the eBPF program was attached after the last /proc baseline; events before the attach were missed"
	case a.limitHit:
		col.State, col.Reason = runtimefacts.StateDegraded, ReasonPodLimitExceeded
		col.Message = "more workloads on the host than the agent tracks; untracked workloads are absent from this snapshot"
	case a.resyncErr != nil:
		col.State, col.Reason = runtimefacts.StateDegraded, ReasonResyncFailed
		col.Message = a.resyncErr.Error()
	case a.lastDrop.After(a.lastResync):
		col.State, col.Reason = runtimefacts.StateDegraded, ReasonEventsDropped
		col.Message = "kernel events were dropped since the last /proc baseline"
	}
	col.Reason, col.Message = truncate(col.Reason, 128), truncate(col.Message, 1024)

	out := make([]runtimefacts.Workload, 0, len(a.workloads))
	hosts := 0
	for _, id := range slices.SortedFunc(maps.Keys(a.workloads), compareIdentities) {
		w := a.workloads[id]
		o := runtimefacts.Workload{
			Identity:      id,
			Shared:        w.shared,
			ObservedSince: seconds(w.observedSince),
			BaselineAt:    seconds(w.baselineAt),
			Truncated: w.listenersTruncated ||
				(!w.truncatedAt.IsZero() && now.Sub(w.truncatedAt) < a.bounds.Retention),
		}
		for _, k := range slices.SortedFunc(maps.Keys(w.listeners), compareListenerKeys) {
			o.Listeners = append(o.Listeners, runtimefacts.Listener{
				Port:    int32(k.Port),
				Family:  family(k.IPv6),
				Address: k.Addr.String(),
				Process: w.listeners[k].process,
			})
		}
		for _, k := range slices.SortedFunc(maps.Keys(w.peers), comparePeerKeys) {
			pr := w.peers[k]
			host, ambiguous := hostOf(w.names[k.addr])
			host = a.budgetHost(host, &hosts)
			o.Connections = append(o.Connections, runtimefacts.Connection{
				Address:       k.addr.String(),
				Host:          host,
				Ambiguous:     ambiguous,
				Port:          int32(k.port),
				Successes:     pr.successes,
				Failures:      pr.failures,
				LastSuccessAt: seconds(pr.lastSuccess),
				LastFailureAt: seconds(pr.lastFailure),
			})
		}
		out = append(out, o)
	}
	return col, out, a.dnsCollection(now, c, hosts > a.bounds.MaxNodeHosts)
}

// budgetHost counts a named connection against MaxNodeHosts and drops the
// name past it.
func (a *Aggregator) budgetHost(host string, hosts *int) string {
	if host == "" {
		return ""
	}
	*hosts++
	if *hosts > a.bounds.MaxNodeHosts {
		return ""
	}
	return host
}

// dnsCollection is the health of DNS capture. The zero collector status is
// never Running: no attached program means no names.
func (a *Aggregator) dnsCollection(now time.Time, c CollectorStatus, hostLimit bool) runtimefacts.DNSCollection {
	d := runtimefacts.DNSCollection{State: runtimefacts.StateRunning, DroppedAnswers: int64(a.dnsDropped)}
	recent := func(t time.Time) bool { return !t.IsZero() && now.Sub(t) < a.bounds.Retention }
	switch {
	case c.Failed || c.DNSState == "":
		d.State, d.Reason = runtimefacts.StateFailed, ReasonDNSNotAttached
	case c.DNSState == runtimefacts.StateFailed:
		d.State, d.Reason = runtimefacts.StateFailed, truncate(c.DNSReason, 128)
	case recent(a.lastDNSDrop):
		d.State, d.Reason = runtimefacts.StateDegraded, ReasonAnswersDropped
	case recent(a.lastUnattributed):
		d.State, d.Reason = runtimefacts.StateDegraded, ReasonDNSUnattributed
	case hostLimit:
		d.State, d.Reason = runtimefacts.StateDegraded, ReasonHostLimit
	}
	return d
}

// hostOf names an address from one workload's live answers: the most
// recently seen name when every name shares one registrable domain, otherwise
// no name and ambiguous.
func hostOf(list []dnsName) (host string, ambiguous bool) {
	if len(list) == 0 {
		return "", false
	}
	domain := registrableDomain(list[0].name)
	for _, n := range list[1:] {
		if registrableDomain(n.name) != domain {
			return "", true
		}
	}
	return list[newestName(list)].name, false
}

func family(ipv6 bool) string {
	if ipv6 {
		return "IPv6"
	}
	return "IPv4"
}

func compareListenerKeys(x, y ListenerKey) int {
	if x.IPv6 != y.IPv6 {
		if x.IPv6 {
			return 1
		}
		return -1
	}
	return cmp.Or(cmp.Compare(x.Port, y.Port), x.Addr.Compare(y.Addr))
}

func comparePeerKeys(x, y peerKey) int {
	return cmp.Or(x.addr.Compare(y.addr), cmp.Compare(x.port, y.port))
}

// seconds renders a time at the API's one-second precision in UTC; zero stays
// zero.
func seconds(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Second)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
