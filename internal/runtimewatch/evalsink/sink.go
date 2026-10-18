/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package evalsink evaluates what the host agent sees against contracts on the host itself: for every
// target a binding source names (a bindings file, labelled Docker containers) it turns the agent's snapshot
// and its own probes into an evidence set, evaluates it and hands the result to an emitter (print it, or
// sign and send it to an Evidence Server).
//
// Only what this host can observe is observed: interface listeners and dependency connections from the
// kernel, health and metrics endpoints and the interface protocol from probes. Exposure, dependency version,
// persistence, configuration and workload kind have no source here and get no observation, so the engine
// reports a required one as missing evidence.
package evalsink

import (
	"cmp"
	"context"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/probe"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// Provenance collectors of this path: an observation the kernel's facts decided carries CollectorKernel,
// one from a probe or from the binding alone Collector.
const (
	Collector       = "pacto-agent"
	CollectorKernel = Collector + "+ebpf"
	// Source is the evidence set source.
	Source = "pacto-agent"
)

// resolveTimeout bounds the name resolution of one dependency address.
const resolveTimeout = 2 * time.Second

// Result is one target's evaluation.
type Result struct {
	Target   string              `json:"target"`
	Contract string              `json:"contract"`
	Status   string              `json:"status,omitempty"`
	Coverage validation.Coverage `json:"coverage"`
	Findings []finding.Finding   `json:"findings,omitempty"`
	// Error is set when the target could not be evaluated (its contract did not load).
	Error string `json:"error,omitempty"`
	// Notes say why some assertion could not be observed on this host.
	Notes []string `json:"notes,omitempty"`
	// Evidence is the set the result was evaluated from.
	Evidence evidence.EvidenceSet `json:"-"`
}

// Sink is a runtimewatch.Sink that evaluates every target on each published snapshot. Publish never blocks
// the agent's event loop: Run takes the latest snapshot and evaluates it, since evaluation dials probes,
// resolves names and loads contracts.
type Sink struct {
	// Sources list the targets anew on every evaluation.
	Sources []func(context.Context) ([]Target, error)
	// Load reads a contract bundle from a directory or an oci:// reference.
	Load func(ctx context.Context, ref string) (*contract.Bundle, error)
	// Emit receives every evaluation's results.
	Emit func(ctx context.Context, results []Result) error
	// Proc is the host /proc, read to name the workload of a pid selector.
	Proc   runtimewatch.ProcFS
	Prober *probe.Prober
	// Resolve looks a dependency host up; nil uses the system resolver.
	Resolve func(ctx context.Context, host string) ([]string, error)
	// Window is how long a negative must persist before it is reported (the operator's
	// --stabilization-window).
	Window time.Duration
	// Protocol enables the interface protocol probe.
	Protocol bool
	Now      func() time.Time
	// Name is the evidence source and the collector of probe and binding observations; empty is Collector.
	Name string
	// Kernel is the collector of observations the snapshot's facts decided; empty is CollectorKernel.
	Kernel string
	// ForeignNetns is set when this process runs in a network namespace other than the host's (an agent
	// container without --network=host). A target probed on a loopback address (a host-network container,
	// a bindings file entry) is then out of reach: its probes are Unsupported instead of dialling this
	// process's own loopback.
	ForeignNetns bool

	queue     chan runtimefacts.Snapshot
	queueOnce sync.Once
	// windows holds when each assertion's current negative streak started, keyed target/kind/subject.
	windows map[string]time.Time
	// contracts caches contracts loaded from a registry; a directory is read on every evaluation.
	contracts map[string]*contract.Contract
}

// Publish implements runtimewatch.Sink: it replaces any snapshot Run has not taken yet. The agent publishes
// from one goroutine, so after the drain the send always has room.
func (s *Sink) Publish(_ context.Context, snap runtimefacts.Snapshot) error {
	select {
	case <-s.q():
	default:
	}
	s.q() <- snap
	return nil
}

// q is the hand-off queue, made on first use by whichever of Publish and Run comes first.
func (s *Sink) q() chan runtimefacts.Snapshot {
	s.queueOnce.Do(func() { s.queue = make(chan runtimefacts.Snapshot, 1) })
	return s.queue
}

// Run evaluates every snapshot Publish hands over until ctx ends. Start it before the agent publishes.
func (s *Sink) Run(ctx context.Context) {
	queue := s.q()
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-queue:
			s.Evaluate(ctx, &snap)
		}
	}
}

// Evaluate lists the targets, evaluates each against the snapshot and emits the results. Failures are logged:
// a target that cannot be evaluated this time is reported with its error, never dropped silently.
func (s *Sink) Evaluate(ctx context.Context, snap *runtimefacts.Snapshot) {
	log := logging.LoggerFromContext(ctx)
	now := s.Now()
	var targets []Target
	for _, src := range s.Sources {
		ts, err := src(ctx)
		if err != nil {
			log.Error("Listing targets failed", "error", err)
		}
		targets = append(targets, ts...)
	}
	next := map[string]time.Time{}
	results := make([]Result, len(targets))
	var loaded []bound
	for i, t := range targets {
		c, err := s.contract(ctx, t.Contract)
		if err != nil {
			results[i] = Result{Target: t.Name, Contract: t.Contract, Error: err.Error()}
			continue
		}
		loaded = append(loaded, bound{t: t, c: c, i: i, addrs: s.addresses(ctx, t)})
	}
	for _, b := range loaded {
		results[b.i] = s.evaluate(ctx, b, loaded, snap, now, next)
	}
	// Assertions not seen this time (a target gone, a negative turned positive) close their window.
	s.windows = next
	if err := s.Emit(ctx, results); err != nil {
		log.Error("Emitting results failed", "error", err)
	}
}

// contract loads a target's contract, a registry one once per process.
func (s *Sink) contract(ctx context.Context, ref string) (*contract.Contract, error) {
	if c, ok := s.contracts[ref]; ok {
		return c, nil
	}
	b, err := s.Load(ctx, ref)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(ref, contract.OCIScheme) {
		if s.contracts == nil {
			s.contracts = map[string]*contract.Contract{}
		}
		s.contracts[ref] = b.Contract
	}
	return b.Contract, nil
}

// bound is a target whose contract loaded, at index i of the round's targets, with the addresses it
// serves its bound interfaces on.
type bound struct {
	t     Target
	c     *contract.Contract
	i     int
	addrs []runtimefacts.Address
}

// round is the state one target's evaluation shares.
type round struct {
	*Sink
	ctx context.Context
	t   Target
	// others are every target of the round whose contract loaded, this one included.
	others []bound
	view   runtimefacts.View
	now    time.Time
	next   map[string]time.Time
	notes  []string
}

// evaluate builds the target's evidence set and evaluates the contract against it.
func (s *Sink) evaluate(ctx context.Context, b bound, others []bound, snap *runtimefacts.Snapshot, now time.Time,
	next map[string]time.Time) Result {
	t, c := b.t, b.c
	r := &round{Sink: s, ctx: ctx, t: t, others: others, view: s.view(t, snap, now), now: now, next: next}
	var obs []evidence.Observation
	for _, iface := range c.Interfaces {
		obs = append(obs, r.observeInterface(iface))
	}
	for _, dep := range c.Dependencies {
		obs = append(obs, r.observeDependency(dep.Name))
	}
	obs = append(obs, r.observeUndeclared(c)...)
	for _, capability := range c.Capabilities {
		switch capability.Type {
		case contract.CapabilityHealth:
			obs = append(obs, r.observeHealth(capability))
		case contract.CapabilityMetrics:
			obs = append(obs, r.observeMetrics(capability))
		}
	}
	set := evidence.EvidenceSet{
		Subject:      evidence.SubjectRef{Kind: "service", Name: t.Name},
		ContractRef:  t.Contract,
		Source:       cmp.Or(s.Name, Source),
		ObservedAt:   now,
		Observations: obs,
	}
	// ponytail: runtime findings only, like evidence ingestion; structural warnings are `pacto validate`'s.
	findings, cov := validation.Evaluate(*c, set)
	return Result{Target: t.Name, Contract: t.Contract, Status: validation.DeriveStatus(findings, cov), Coverage: cov,
		Findings: findings, Notes: r.notes, Evidence: set}
}

// view is the kernel's facts for the target's workloads. A selector that matches no workload in the
// snapshot still classifies (an unmapped workload: partial, or the host's stale or failed collection).
// A target with no start of its own (a bindings file entry) starts with the earliest process of its
// workload, so a connection from before it started is ignored.
func (s *Sink) view(t Target, snap *runtimefacts.Snapshot, now time.Time) runtimefacts.View {
	want := s.selects(t.Match)
	var ids []string
	for _, w := range snap.Workloads {
		if want != nil && want(w.Identity) {
			ids = append(ids, w.ID)
		}
	}
	if len(ids) == 0 {
		ids = []string{""}
	}
	start := t.Start
	if start.IsZero() && want != nil && s.Proc != nil {
		start, _ = runtimewatch.StartTime(s.Proc, want)
	}
	v := runtimefacts.View{Now: now, Freshness: snap.StalenessBound(0)}
	for _, id := range ids {
		v.Workloads = append(v.Workloads, runtimefacts.Classify(snap, id, start, now, 0))
	}
	return v
}

// selects returns whether an identity is a workload the selector names; nil when a pid selector's process
// is gone.
func (s *Sink) selects(sel Selector) func(runtimefacts.Identity) bool {
	named := func(kind, v string) func(runtimefacts.Identity) bool {
		return func(id runtimefacts.Identity) bool { return id.Kind == kind && (id.Name == v || id.ID == v) }
	}
	exactly := func(want runtimefacts.Identity) func(runtimefacts.Identity) bool {
		return func(id runtimefacts.Identity) bool { return id.Kind == want.Kind && id.ID == want.ID }
	}
	var want func(runtimefacts.Identity) bool
	switch {
	case sel.Unit != "":
		want = named(runtimefacts.KindUnit, sel.Unit)
	case sel.Session != "":
		want = named(runtimefacts.KindSession, sel.Session)
	case sel.Container != "":
		v := sel.Container
		want = func(id runtimefacts.Identity) bool {
			return id.Kind == runtimefacts.KindContainer && (id.ID == v || (len(v) >= 12 && strings.HasPrefix(id.ID, v)))
		}
	case sel.Cgroup != "":
		want = exactly(runtimewatch.IdentityFromPath(sel.Cgroup))
	default:
		b, err := s.Proc.ReadFile(strconv.Itoa(sel.PID) + "/cgroup")
		if err != nil {
			return nil
		}
		want = exactly(runtimewatch.IdentityFromProcCgroup(b))
	}
	return want
}

// stabilize holds a negative back as Partial until it has persisted for the window, the operator's rule:
// the first negative opens the window and is never reported; any other verdict closes it.
func (r *round) stabilize(key string, v runtimefacts.Verdict) runtimefacts.Verdict {
	if v != runtimefacts.Negative {
		return v
	}
	key = r.t.Name + "/" + key
	first, open := r.windows[key]
	if !open {
		first = r.now
	}
	r.next[key] = first
	if !open || r.now.Sub(first) < r.Window {
		return runtimefacts.Partial
	}
	return v
}

// probed is the provenance of an observation a probe or the binding alone decided.
func (r *round) probed() evidence.Provenance {
	return evidence.Provenance{Collector: cmp.Or(r.Name, Collector), DetectedAt: r.now}
}

// kernel is the provenance of an observation the snapshot's facts decided.
func (r *round) kernel() evidence.Provenance {
	return evidence.Provenance{Collector: cmp.Or(r.Kernel, CollectorKernel), DetectedAt: r.now}
}

// unsupported is the observation of an assertion this target's binding gives no way to observe.
func (r *round) unsupported(kind evidence.ObservationKind, subjectKind, name string) evidence.Observation {
	obs, _ := evidence.NewUnobserved(kind, evidence.SubjectRef{Kind: subjectKind, Name: name}, evidence.Unsupported, r.probed())
	return obs
}

// observeInterface judges an interface from the listeners of the target's workloads on its bound port.
func (r *round) observeInterface(iface contract.Interface) evidence.Observation {
	port, bound := r.t.Interfaces[iface.Name]
	if !bound {
		return r.unsupported(evidence.InterfaceObserved, "interface", iface.Name)
	}
	backing := make([]runtimefacts.Backing, len(r.view.Workloads))
	for i, o := range r.view.Workloads {
		backing[i] = runtimefacts.Backing{Observed: o, Ready: true, Port: port, Resolved: true}
	}
	v := r.stabilize("interface/"+iface.Name, runtimefacts.InterfaceVerdict(backing, runtimefacts.CoverageComplete))
	p := evidence.InterfaceObservation{Type: iface.Type}
	if v == runtimefacts.Positive && r.Protocol {
		if host, ok := r.probeHost(); ok {
			p.Protocol = r.Prober.ProbeProtocol(r.ctx, hostPort(host, port))
		}
	}
	return runtimefacts.InterfaceObservation(iface.Name, p, v, r.kernel())
}

// observeDependency judges a dependency from the target's connections to the addresses its binding
// resolves to now.
func (r *round) observeDependency(name string) evidence.Observation {
	addr, bound := r.t.Dependencies[name]
	if !bound {
		return r.unsupported(evidence.DependencyReachable, "dependency", name)
	}
	peers, err := r.peers(addr)
	if err != nil {
		obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, evidence.SubjectRef{Kind: "dependency", Name: name},
			evidence.Failed, r.probed())
		return obs
	}
	v := r.stabilize("dependency/"+name, r.view.DependencyVerdict(peers))
	return runtimefacts.DependencyObservation(name, v, r.kernel())
}

// lookup resolves a host, bounded by resolveTimeout.
func (s *Sink) lookup(ctx context.Context, host string) ([]string, error) {
	resolve := s.Resolve
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	return resolve(ctx, host)
}

// peers resolves a host:port the target validated to every address it stands for.
func (r *round) peers(addr string) ([]runtimefacts.Address, error) {
	host, port, _ := splitAddress(addr)
	ips, err := r.lookup(r.ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]runtimefacts.Address, len(ips))
	for i, ip := range ips {
		out[i] = runtimefacts.NewAddress(ip, port)
	}
	return out, nil
}

// capabilityURL is the probe URL of a capability on the address of its binding's interface; ok is false
// when the capability has no http binding, the interface no port or the target no probe host.
func (r *round) capabilityURL(c contract.Capability, fallbackPath string) (string, bool) {
	if c.Binding == nil || c.Binding.Type != contract.CapabilityBindingHTTP {
		return "", false
	}
	port, bound := r.t.Interfaces[c.Binding.Interface]
	host, reachable := r.probeHost()
	if !bound || !reachable {
		return "", false
	}
	path := c.Binding.Path
	if path == "" {
		path = fallbackPath
	}
	u, err := probe.URL(hostPort(host, port), path)
	return u, err == nil
}

// hold keeps an open window open without counting the round as a negative: the operator's rule for an answer
// that neither confirms nor refutes (a 5xx, no answer), which only a positive may close.
func (r *round) hold(key string) {
	key = r.t.Name + "/" + key
	if first, open := r.windows[key]; open {
		r.next[key] = first
	}
}

// observeHealth probes the health endpoint: a 2xx or 3xx is present, a 404 that persists past the window
// absent, anything else (no answer, an error status) inconclusive and leaves the window as it was.
func (r *round) observeHealth(c contract.Capability) evidence.Observation {
	subj := evidence.SubjectRef{Kind: "capability", Name: c.AssertionKey()}
	u, ok := r.capabilityURL(c, "/")
	if !ok {
		return r.unsupported(evidence.CapabilityObserved, subj.Kind, subj.Name)
	}
	res := r.Prober.Probe(r.ctx, u)
	switch {
	case res.Reachable && res.StatusCode >= 200 && res.StatusCode < 400:
		return evidence.NewCapabilityObserved(subj, true, r.probed())
	case res.Reachable && res.StatusCode == 404 && r.stabilize("capability/"+subj.Name, runtimefacts.Negative) == runtimefacts.Negative:
		return evidence.NewCapabilityObserved(subj, false, r.probed())
	case !res.Reachable || res.StatusCode != 404:
		r.hold("capability/" + subj.Name)
	}
	obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, r.probed())
	return obs
}

// observeMetrics probes the metrics endpoint: a 200 that parses as Prometheus text is present, no answer a
// failed collection, anything else inconclusive (metrics have no reliable negative).
func (r *round) observeMetrics(c contract.Capability) evidence.Observation {
	subj := evidence.SubjectRef{Kind: "capability", Name: c.AssertionKey()}
	u, ok := r.capabilityURL(c, "/metrics")
	if !ok {
		return r.unsupported(evidence.CapabilityObserved, subj.Kind, subj.Name)
	}
	res := r.Prober.Probe(r.ctx, u)
	outcome := evidence.Insufficient
	switch {
	case !res.Reachable:
		outcome = evidence.Failed
	case res.StatusCode == 200 && res.PrometheusParsed:
		return evidence.NewCapabilityObserved(subj, true, r.probed())
	}
	obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, outcome, r.probed())
	return obs
}

// probeHost is the address probes dial; ok is false when the target has none or it is out of this process's
// reach, which a note on the result then says.
func (r *round) probeHost() (string, bool) {
	if r.t.Host == "" {
		return "", false
	}
	if r.ForeignNetns && isLoopback(r.t.Host) {
		note := "probes unsupported: " + r.t.Host + " is a loopback address of the host's network namespace and this " +
			"process runs in another one (run the agent with --network=host)"
		if !slices.Contains(r.notes, note) {
			r.notes = append(r.notes, note)
		}
		return "", false
	}
	return r.t.Host, true
}

// isLoopback reports whether a probe host names this network namespace's loopback.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// observeUndeclared reports every other target of the round the workload connected to successfully that
// its contract does not declare, named by that target's contract service: the operator's rule off
// Kubernetes, with a target's probe host and bound ports for a Service's endpoints. A declared dependency
// matches a target by service name, contract reference or bound address; when one matches none, it may be
// the very target reached, so nothing is reported at all: drift goes unreported, never invented.
func (r *round) observeUndeclared(c *contract.Contract) []evidence.Observation {
	// Each declared dependency's binding, resolved; one that does not resolve matches by name or reference only.
	resolved := map[string][]runtimefacts.Address{}
	for name, addr := range r.t.Dependencies {
		resolved[name], _ = r.peers(addr)
	}
	declares := func(dep contract.Dependency, o bound) bool {
		return dep.Name == o.c.Service.Name || sameContract(dep.Ref, o.t.Contract) ||
			slices.ContainsFunc(resolved[dep.Name], func(a runtimefacts.Address) bool { return slices.Contains(o.addrs, a) })
	}
	others := slices.DeleteFunc(slices.Clone(r.others), func(o bound) bool { return o.t.Name == r.t.Name })
	candidates := runtimefacts.Binding{Dependencies: map[string][]runtimefacts.Address{}}
	for _, dep := range c.Dependencies {
		if !slices.ContainsFunc(others, func(o bound) bool { return declares(dep, o) }) {
			return nil
		}
	}
	for _, o := range others {
		name := o.c.Service.Name
		if name == "" || name == c.Service.Name || slices.ContainsFunc(c.Dependencies, func(d contract.Dependency) bool { return declares(d, o) }) {
			continue
		}
		for _, a := range o.addrs {
			// ponytail: a loopback address is the host's only for a workload on the host's network, which a
			// loopback probe host stands for; a bridge container's loopback is its own.
			if !isLoopback(a.IP) || isLoopback(r.t.Host) {
				candidates.Dependencies[name] = append(candidates.Dependencies[name], a)
			}
		}
	}
	return r.view.Undeclared(candidates, r.kernel())
}

// addresses is every address a target serves its bound interfaces on: its probe host, resolved once, at each
// bound port. A host that does not resolve gives none.
func (s *Sink) addresses(ctx context.Context, t Target) []runtimefacts.Address {
	if t.Host == "" || len(t.Interfaces) == 0 {
		return nil
	}
	ips, err := s.lookup(ctx, t.Host)
	if err != nil {
		return nil
	}
	var out []runtimefacts.Address
	for _, port := range t.Interfaces {
		for _, ip := range ips {
			out = append(out, runtimefacts.NewAddress(ip, port))
		}
	}
	return out
}

// sameContract reports whether a dependency reference names a target's contract: the same reference with
// any oci:// scheme, tag or digest dropped. A local path never matches.
func sameContract(depRef, targetRef string) bool {
	if !strings.HasPrefix(depRef, contract.OCIScheme) || !strings.HasPrefix(targetRef, contract.OCIScheme) {
		return false
	}
	dep, _, _ := contract.SplitOCIRef(depRef)
	target, _, _ := contract.SplitOCIRef(targetRef)
	return dep == target
}

// hostPort joins a probe host and port.
func hostPort(host string, port int32) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}
