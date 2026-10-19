// Package runtimefacts is the platform-neutral model of what a kernel collector saw on one host and the
// rules that turn those facts into verdicts about a target's interfaces and dependencies. It is pure: no
// clock, no I/O and no platform. A front-end (the Kubernetes operator today) joins its own resources into
// the inputs here, calls the verdict functions and maps the neutral verdicts onto its own evidence logic.
//
// A Snapshot's JSON shape is the status of the Kubernetes NodeRuntimeObservation resource field for field,
// so a collector can publish one as the other; the operator module pins that with a parity test. The
// resource carries timestamps at second precision.
package runtimefacts

import (
	"bytes"
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

const (
	// defaultPublishInterval is assumed when a snapshot does not state its publish interval.
	defaultPublishInterval = 30 * time.Second
	// stalenessIntervals is how many publish intervals a snapshot stays fresh by default.
	stalenessIntervals = 3
)

// Collector names the Kubernetes operator stamps into observation provenance, and so into every finding's
// evidenceRefs[].collector. They live here so the operator and the generated reference read one source.
const (
	// CollectorKubernetes produced an observation from the Kubernetes API alone.
	CollectorKubernetes = "k8s-observer"
	// CollectorKubernetesKernel produced a Kubernetes API result that node agent evidence changed or qualified.
	CollectorKubernetesKernel = CollectorKubernetes + "+ebpf"
	// CollectorKernel produced an observation only the node agent could make (an undeclared peer).
	CollectorKernel = "ebpf"
)

// CollectorState is the collector's ability to observe the kernel.
type CollectorState string

const (
	// StateRunning means the collector is attached and events flow.
	StateRunning CollectorState = "Running"
	// StateDegraded means events flow but some were lost or not attributed.
	StateDegraded CollectorState = "Degraded"
	// StateFailed means the collector could not attach: its facts must not be read as evidence of absence.
	StateFailed CollectorState = "Failed"
)

// Collection is the health of the kernel collection on one host.
type Collection struct {
	State         CollectorState `json:"state"`
	Reason        string         `json:"reason,omitempty"`
	Message       string         `json:"message,omitempty"`
	DroppedEvents int64          `json:"droppedEvents,omitempty"`
	LastDropAt    time.Time      `json:"lastDropAt,omitzero"`
	LastResyncAt  time.Time      `json:"lastResyncAt,omitzero"`
}

// Snapshot is one host's published view: the collector's health and the facts of every workload on it.
//
// On the wire pods and every other kind travel apart: pods under "pods" keyed by "uid" (their kind
// implied), the NodeRuntimeObservation status shape, and the rest under "workloads" with kind, id and
// name. A snapshot of pods alone is therefore exactly that status.
type Snapshot struct {
	NodeName               string     `json:"nodeName,omitempty"`
	AgentVersion           string     `json:"agentVersion,omitempty"`
	KernelRelease          string     `json:"kernelRelease,omitempty"`
	PublishIntervalSeconds int32      `json:"publishIntervalSeconds,omitempty"`
	LastPublished          time.Time  `json:"lastPublished,omitzero"`
	ObservingSince         time.Time  `json:"observingSince,omitzero"`
	Collection             Collection `json:"collection,omitzero"`
	Workloads              []Workload `json:"-"`
}

// snapshotFields is Snapshot without its JSON methods.
type snapshotFields Snapshot

// podWire is a pod on the wire: its ID as "uid" and no kind or name.
type podWire struct {
	UID string `json:"uid"`
	Workload
}

type snapshotWire struct {
	snapshotFields
	Pods      []podWire  `json:"pods,omitempty"`
	Workloads []Workload `json:"workloads,omitempty"`
}

// MarshalJSON splits the workloads into pods and the rest.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	w := snapshotWire{snapshotFields: snapshotFields(s)}
	for _, wl := range s.Workloads {
		if wl.Kind == KindPod {
			id := wl.ID
			wl.Identity = Identity{}
			w.Pods = append(w.Pods, podWire{UID: id, Workload: wl})
			continue
		}
		w.Workloads = append(w.Workloads, wl)
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads both lists back, pods first, and rejects unknown fields: the wire form is a
// contract with the NodeRuntimeObservation schema, and a field this side does not know is drift.
func (s *Snapshot) UnmarshalJSON(b []byte) error {
	var w snapshotWire
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return err
	}
	*s = Snapshot(w.snapshotFields)
	s.Workloads = nil
	for _, p := range w.Pods {
		p.Identity = Identity{Kind: KindPod, ID: p.UID}
		s.Workloads = append(s.Workloads, p.Workload)
	}
	s.Workloads = append(s.Workloads, w.Workloads...)
	return nil
}

// Identity kinds. A pod's ID is its UID, a container's its runtime id; a systemd unit and a session (the
// cgroup `pacto run` creates) are named by their cgroup directory, prefixed by the enclosing container's id
// when they run inside one; a process that matches none of them is identified by its cgroup path.
const (
	KindPod       = "pod"
	KindContainer = "container"
	KindUnit      = "unit"
	KindSession   = "session"
	KindProcess   = "process"
)

// Identity names a workload.
type Identity struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Workload is what the kernel showed for one workload on the host.
type Workload struct {
	Identity
	// Shared is set when the workload's network namespace is not its own, so its sockets cannot be told
	// apart from the host's and Listeners and Connections stay empty.
	Shared        bool         `json:"netnsShared,omitempty"`
	ObservedSince time.Time    `json:"observedSince,omitzero"`
	BaselineAt    time.Time    `json:"baselineAt,omitzero"`
	Truncated     bool         `json:"truncated,omitempty"`
	Listeners     []Listener   `json:"listeners,omitempty"`
	Connections   []Connection `json:"connections,omitempty"`
}

// Listener is a TCP socket in LISTEN state.
type Listener struct {
	Port    int32  `json:"port"`
	Family  string `json:"family"`
	Address string `json:"address,omitempty"`
	Process string `json:"process,omitempty"`
}

// Connection aggregates outbound TCP connection attempts to one peer.
type Connection struct {
	Address       string    `json:"address"`
	Port          int32     `json:"port"`
	Successes     int64     `json:"successes,omitempty"`
	Failures      int64     `json:"failures,omitempty"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitzero"`
	LastFailureAt time.Time `json:"lastFailureAt,omitzero"`
}

// Binding maps a target's contract names to the addresses the platform resolved them to.
type Binding struct {
	Interfaces   map[string][]Endpoint
	Dependencies map[string][]Address
}

// Endpoint is a port an interface is served on.
type Endpoint struct {
	Port     int32
	Protocol string
}

// Address is a peer address and port. Build one with NewAddress so equal peers compare equal.
type Address struct {
	IP   string
	Port int32
}

// NewAddress returns the canonical address: net.ParseIP folds IPv4-mapped IPv6 and spelling variants, and
// an unparseable address is kept as written.
func NewAddress(ip string, port int32) Address {
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	}
	return Address{IP: ip, Port: port}
}

// String is the address in host:port form.
func (a Address) String() string {
	return net.JoinHostPort(a.IP, strconv.Itoa(int(a.Port)))
}

// Coverage is how far one workload's kernel view can be trusted. Higher is worse, in the order a mixed set
// resolves: stale beats failed, failed beats partial.
type Coverage int

const (
	CoverageComplete Coverage = iota // fresh, running, attributable and complete
	CoveragePartial                  // readable, but degraded, shared, truncated, unmapped or not yet baselined
	CoverageFailed                   // the host's collection failed
	CoverageStale                    // no snapshot, or none within the staleness bound
)

// Verdict is the kernel's answer for one assertion.
type Verdict int

const (
	Silent   Verdict = iota // nothing to say: the platform's own result stands
	Positive                // a listener or a successful connection was seen
	Negative                // a confirmed absence: complete silence or failures only
	Partial                 // cannot confirm and cannot rule out
	Failed                  // the host's collection failed
	Stale                   // the host's evidence is too old
)

// Outcome maps an inconclusive verdict to the evidence outcome it stands for.
func (v Verdict) Outcome() evidence.Outcome {
	switch v {
	case Failed:
		return evidence.Failed
	case Stale:
		return evidence.Stale
	default:
		return evidence.Insufficient
	}
}

// StalenessBound is how old the snapshot may be: the configured bound, or when that is zero three publish
// intervals of the snapshot itself.
func (s *Snapshot) StalenessBound(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	interval := time.Duration(s.PublishIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultPublishInterval
	}
	return stalenessIntervals * interval
}

// Observed is one workload of a target with the coverage of its facts. Facts is set only when the snapshot
// is fresh, its collection has not failed and the workload's sockets are attributable to it.
type Observed struct {
	Coverage Coverage
	Facts    *Workload
	// Start is when the workload started; a timestamp before it belongs to a previous occupant of a reused
	// network namespace.
	Start time.Time
}

// Classify computes the coverage of the workload with the given ID in the snapshot, which may be nil.
func Classify(s *Snapshot, id string, start, now time.Time, staleness time.Duration) Observed {
	o := Observed{Coverage: CoverageStale, Start: start}
	if s == nil || s.LastPublished.IsZero() || now.Sub(s.LastPublished) > s.StalenessBound(staleness) {
		return o
	}
	if s.Collection.State == StateFailed {
		o.Coverage = CoverageFailed
		return o
	}
	o.Coverage = CoveragePartial
	i := slices.IndexFunc(s.Workloads, func(w Workload) bool { return w.ID == id })
	if i < 0 || s.Workloads[i].Shared {
		// Not mapped yet, or a workload whose sockets cannot be told apart from the host's.
		return o
	}
	o.Facts = &s.Workloads[i]
	if s.Collection.State == StateRunning && complete(o.Facts, start, s.Collection.LastDropAt) {
		o.Coverage = CoverageComplete
	}
	return o
}

// complete reports whether the view of the workload has no gap: a baseline taken after it started (so no
// listener predates the collector's knowledge), no dropped event since that baseline and no bound hit.
func complete(w *Workload, start, lastDropAt time.Time) bool {
	if w.Truncated || w.BaselineAt.IsZero() || !w.BaselineAt.After(start) {
		return false
	}
	return lastDropAt.IsZero() || lastDropAt.Before(w.BaselineAt)
}

// Observable reports whether the kernel collector can judge an interface on the protocol: it sees TCP
// sockets only, so a UDP or SCTP port gets no kernel opinion. Empty means TCP.
func Observable(protocol string) bool {
	return protocol == "" || protocol == "TCP"
}

// Backing is one workload the platform routes an interface's traffic to.
type Backing struct {
	Observed
	Ready bool
	// Port is the port the workload serves the interface on; Resolved is false when the platform could not
	// resolve it for this workload (a named port it does not declare).
	Port     int32
	Resolved bool
}

// InterfaceVerdict judges the interface against the listeners of the ready backing workloads, no better than
// floor: positive when one listens on its port, negative when every one is completely observed and none
// listens, otherwise as inconclusive as the worst coverage. No ready backing workload is silence.
func InterfaceVerdict(backing []Backing, floor Coverage) Verdict {
	worst, seen := floor, false
	for _, b := range backing {
		if !b.Ready {
			continue
		}
		seen = true
		if b.Resolved && b.Facts != nil && listensOn(b.Facts, b.Port) {
			return Positive
		}
		c := b.Coverage
		if !b.Resolved {
			c = max(c, CoveragePartial) // a port the workload does not declare cannot be judged
		}
		worst = max(worst, c)
	}
	if !seen {
		return Silent
	}
	switch worst {
	case CoverageComplete:
		return Negative
	case CoveragePartial:
		return Partial
	case CoverageFailed:
		return Failed
	default:
		return Stale
	}
}

// listensOn reports whether the workload has a listener on the port that serves traffic sent to its own
// address. A loopback-only listener, IPv4-mapped included, does not.
func listensOn(w *Workload, port int32) bool {
	for _, l := range w.Listeners {
		if l.Port != port {
			continue
		}
		if ip := net.ParseIP(l.Address); ip == nil || !ip.IsLoopback() {
			return true
		}
	}
	return false
}

// View is the kernel's facts for one target's workloads at one instant. A connection timestamp older than
// Freshness before Now is history, not evidence.
type View struct {
	Workloads []Observed
	Now       time.Time
	Freshness time.Duration
}

// peerTimes is the most recent success and failure the target saw towards one peer.
type peerTimes struct {
	success, failure time.Time
}

// peerHistory folds every workload's connection entries into one peerTimes per peer, dropping a timestamp
// from before the workload started.
func (v View) peerHistory() map[Address]peerTimes {
	out := map[Address]peerTimes{}
	for _, w := range v.Workloads {
		if w.Facts == nil {
			continue
		}
		for _, c := range w.Facts.Connections {
			key := NewAddress(c.Address, c.Port)
			t := out[key]
			if !c.LastSuccessAt.Before(w.Start) && c.LastSuccessAt.After(t.success) {
				t.success = c.LastSuccessAt
			}
			if !c.LastFailureAt.Before(w.Start) && c.LastFailureAt.After(t.failure) {
				t.failure = c.LastFailureAt
			}
			out[key] = t
		}
	}
	return out
}

// peerVerdict judges one peer from its timestamps, never from lifetime counters: positive when the latest
// success is fresh and not older than the latest failure, negative when the latest failure is fresh and
// newer than the latest success, silent otherwise.
func (v View) peerVerdict(t peerTimes) Verdict {
	fresh := func(at time.Time) bool { return !at.IsZero() && v.Now.Sub(at) <= v.Freshness }
	switch {
	case fresh(t.success) && !t.success.Before(t.failure):
		return Positive
	case fresh(t.failure) && t.failure.After(t.success):
		return Negative
	default:
		return Silent
	}
}

// DependencyVerdict judges the target's outbound connections to the provider's addresses: any positive peer
// makes it positive, otherwise any negative peer makes it negative. With neither, a stale or failed workload
// says so and no recent connection is not a contradiction.
func (v View) DependencyVerdict(peers []Address) Verdict {
	worst := CoverageComplete
	for _, w := range v.Workloads {
		worst = max(worst, w.Coverage)
	}
	verdict := Silent
	for key, t := range v.peerHistory() {
		if !slices.Contains(peers, key) {
			continue
		}
		switch v.peerVerdict(t) {
		case Positive:
			return Positive
		case Negative:
			verdict = Negative
		}
	}
	switch {
	case verdict == Negative:
		return Negative
	case worst == CoverageStale:
		return Stale
	case worst == CoverageFailed:
		return Failed
	default:
		return Silent
	}
}

// Undeclared returns, sorted by name, a reachable undeclared dependency observation for every candidate in
// the binding's dependencies the target reached under the same timestamp rule as a declared dependency.
// Only a kernel sees these, so prov names the kernel collector.
func (v View) Undeclared(candidates Binding, prov evidence.Provenance) []evidence.Observation {
	history := v.peerHistory()
	var names []string
	for name, addrs := range candidates.Dependencies {
		if slices.ContainsFunc(addrs, func(a Address) bool { t, ok := history[a]; return ok && v.peerVerdict(t) == Positive }) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	out := make([]evidence.Observation, 0, len(names))
	for _, name := range names {
		out = append(out, evidence.NewDependencyObservation(evidence.SubjectRef{Kind: "dependency", Name: name},
			evidence.DependencyObservation{Reachable: true, Undeclared: true}, prov))
	}
	return out
}

// InterfaceObservation is the observation an interface verdict stands for where the kernel is the only
// source (a host agent: no platform verdict to merge): a listener makes the interface present with the facets
// in p, a confirmed absence makes it absent without them, and anything else carries no payload but the
// outcome that says why. Silence is Insufficient there: nothing was learned. A caller applies its
// stabilization window first, turning a negative still inside it into Partial.
func InterfaceObservation(name string, p evidence.InterfaceObservation, v Verdict, prov evidence.Provenance) evidence.Observation {
	subj := evidence.SubjectRef{Kind: "interface", Name: name}
	switch v {
	case Positive:
		p.Present = true
		return evidence.NewInterfaceObservation(subj, p, prov)
	case Negative:
		return evidence.NewInterfaceObservation(subj, evidence.InterfaceObservation{Type: p.Type}, prov)
	}
	return unobserved(evidence.InterfaceObserved, subj, v, prov)
}

// DependencyObservation is the observation a dependency verdict stands for where the kernel is the only
// source, with InterfaceObservation's rules: a fresh success is reachable, a fresh failure newer than any
// success unreachable, and silence (no recent connection) Insufficient.
func DependencyObservation(name string, v Verdict, prov evidence.Provenance) evidence.Observation {
	subj := evidence.SubjectRef{Kind: "dependency", Name: name}
	if v == Positive || v == Negative {
		return evidence.NewDependencyReachable(subj, v == Positive, prov)
	}
	return unobserved(evidence.DependencyReachable, subj, v, prov)
}

// unobserved is the payload-less observation of an inconclusive verdict. The kind and subject always pair
// and the outcome is never Observed, so the constructor cannot fail.
func unobserved(kind evidence.ObservationKind, subj evidence.SubjectRef, v Verdict, prov evidence.Provenance) evidence.Observation {
	obs, _ := evidence.NewUnobserved(kind, subj, v.Outcome(), prov)
	return obs
}
