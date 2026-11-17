package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

// AvailabilityState is whether a capability answers where it runs. It is
// derived from observed targets and consumer reports, never declared and
// never read from compliance. Unknown is the answer whenever Pacto cannot
// see every target or no fresh evidence exists.
type AvailabilityState string

// The availability states, and the bound on the consumer targets an
// environment names as unable to reach a capability.
const (
	AvailabilityAvailable   AvailabilityState = "available"
	AvailabilityDegraded    AvailabilityState = "degraded"
	AvailabilityUnavailable AvailabilityState = "unavailable"
	AvailabilityUnknown     AvailabilityState = "unknown"
	MaxAvailabilityEvidence                   = 5
)

// EnvironmentAvailability is a capability's state in one environment ("" is
// the targets with no environment label, and the one entry of a capability
// no target or report places anywhere).
type EnvironmentAvailability struct {
	Environment   string            `json:"environment" doc:"The environment label of the targets; empty for targets with no label"`
	State         AvailabilityState `json:"state" enum:"available,degraded,unavailable,unknown" doc:"Whether the capability answers here"`
	Provenance    string            `json:"provenance" enum:"observed,inferred,unknown" doc:"observed from the provider's own targets, inferred from consumer reports, unknown whenever the state is unknown"`
	Reason        string            `json:"reason" doc:"Why the state is what it is"`
	Partial       bool              `json:"partial,omitempty" doc:"Consumers or providers the viewer may not see are not counted"`
	TargetsHidden bool              `json:"targetsHidden,omitempty" doc:"Targets of the provider the viewer may not see are not counted"`
	Reporting     int               `json:"reporting" doc:"Provider targets with fresh evidence"`
	Targets       int               `json:"targets" doc:"Provider targets in this environment the viewer can see"`
	Unreachable   []EntityRef       `json:"unreachable,omitempty" doc:"Consumer targets that report they cannot reach it, bounded"`
	EvidenceAt    *time.Time        `json:"evidenceAt,omitempty" doc:"The newest fresh evidence the state reads"`
	Providers     []EntityRef       `json:"providers,omitempty" doc:"On an offering: the providers whose state this is"`
}

// Availability is a capability's state by environment and its roll-up.
type Availability struct {
	State         AvailabilityState         `json:"state" enum:"available,degraded,unavailable,unknown" doc:"available or unavailable when every environment is; degraded when an environment is degraded, or one is unavailable and another available; otherwise unknown"`
	ByEnvironment []EnvironmentAvailability `json:"byEnvironment" doc:"The state per environment, bounded"`
	Total         int                       `json:"total" doc:"Environments before the bound"`
	AsOf          time.Time                 `json:"asOf" doc:"The snapshot time the evidence is judged against"`
}

// Reasons an availability is unknown.
const (
	availabilityStaleReason   = "evidence older than 24h before asOf"
	availabilityUnseenReason  = "no fresh target shows an interface answering"
	availabilityNoneReason    = "no target reports it; a provider outside the observed clusters is not seen"
	availabilityHiddenSuffix  = "; consumers you may not see are not counted"
	availabilityPartialSource = "source %s is %s"
)

// availabilityRank orders states from best to worst for an offering: an
// unknown provider may be available, so it outranks a confirmed unavailable one.
var availabilityRank = map[AvailabilityState]int{AvailabilityAvailable: 0, AvailabilityDegraded: 1, AvailabilityUnknown: 2, AvailabilityUnavailable: 3}

// envEvidence is what one environment holds for a provider.
type envEvidence struct {
	targets, fresh      []*TargetRecord
	absent, partly      []*TargetRecord // fresh targets serving no declared interface, and some but not all
	serving             []*TargetRecord // fresh targets with no interface absent or unobserved
	reporting, reaching []*TargetRecord
}

// availIndex is what one answer reads to compute availability: the declared
// dependency edges by provider, built once, and each provider's environments,
// computed once. It keeps a list linear in the relationships.
type availIndex struct {
	q     *Query
	names map[ServiceKey]map[ServiceKey]map[string]bool // provider, consumer, dependency names
	envs  map[ServiceKey][]EnvironmentAvailability
}

// newAvailIndex indexes the declared dependency edges of q's snapshot.
func (q *Query) newAvailIndex() *availIndex {
	x := &availIndex{q: q, names: map[ServiceKey]map[ServiceKey]map[string]bool{}, envs: map[ServiceKey][]EnvironmentAvailability{}}
	for _, r := range q.snap.Relationships {
		if r.Type != RelationshipDependency || !ConsumesEdge(r) || r.FromService == r.ToService {
			continue
		}
		if x.names[r.ToService] == nil {
			x.names[r.ToService] = map[ServiceKey]map[string]bool{}
		}
		if x.names[r.ToService][r.FromService] == nil {
			x.names[r.ToService][r.FromService] = map[string]bool{}
		}
		x.names[r.ToService][r.FromService][r.To] = true
	}
	return x
}

// environments is s's state in every environment, computed once.
func (x *availIndex) environments(s *ServiceRecord) []EnvironmentAvailability {
	if envs, ok := x.envs[s.Key]; ok {
		return envs
	}
	envs := x.q.providerEnvironments(s, x.names[s.Key])
	x.envs[s.Key] = envs
	return envs
}

// providerAvailability is s's availability per environment, bounded.
func (x *availIndex) providerAvailability(s *ServiceRecord) Availability {
	return x.q.availability(slices.Clone(x.environments(s)))
}

// providerEnvironments is s's state in every environment: what its own fresh
// targets and the interface findings they carry say, then the
// DEPENDENCY_UNREACHABLE reports of the consumers that declare it (names: per
// consumer, the names of its dependencies resolved to s).
// The compliance verdict is never read.
func (q *Query) providerEnvironments(s *ServiceRecord, names map[ServiceKey]map[string]bool) []EnvironmentAvailability {
	byEnv := map[string]*envEvidence{}
	at := func(env string) *envEvidence {
		if byEnv[env] == nil {
			byEnv[env] = &envEvidence{}
		}
		return byEnv[env]
	}
	for _, tk := range s.Targets {
		if t := q.snap.Targets[tk]; t != nil {
			q.addProviderTarget(at(t.Environment), t)
		}
	}
	q.consumerReports(names, at)
	hiddenReason := q.availabilityHidden(s)
	partial := q.availabilityPartial(s)
	envs := make([]EnvironmentAvailability, 0, len(byEnv))
	for env, ev := range byEnv {
		if len(ev.targets) == 0 && len(ev.reporting) == 0 {
			continue // only consumers that reach it name this environment
		}
		e := q.environmentAvailability(ev, hiddenReason)
		e.Environment = env
		envs = append(envs, markPartial(e, partial))
	}
	if len(envs) == 0 {
		e := EnvironmentAvailability{State: AvailabilityUnknown, Provenance: provenanceUnknown, Reason: cmp.Or(hiddenReason, availabilityNoneReason), TargetsHidden: q.snap.targetsHidden[s.Key]}
		envs = append(envs, markPartial(e, partial))
	}
	return envs
}

// provenanceUnknown is the provenance of a state nothing supports: no fresh
// evidence for availability, no matching revision for a successor.
const provenanceUnknown = "unknown"

// markPartial flags e when the viewer may not see every consumer.
func markPartial(e EnvironmentAvailability, partial bool) EnvironmentAvailability {
	if partial {
		e.Partial, e.Reason = true, e.Reason+availabilityHiddenSuffix
	}
	return e
}

// availability sorts envs, rolls them up over the whole list and bounds them.
func (q *Query) availability(envs []EnvironmentAvailability) Availability {
	slices.SortFunc(envs, func(a, b EnvironmentAvailability) int {
		if a.Environment == "" || b.Environment == "" {
			return strings.Compare(b.Environment, a.Environment) // the unlabelled group sorts last
		}
		return NaturalCompare(a.Environment, b.Environment)
	})
	shown, total, _ := boundSlice(envs, MaxDetailPreview)
	return Availability{State: rollUp(envs), ByEnvironment: shown, Total: total, AsOf: q.snap.GeneratedAt}
}

// addProviderTarget files t, a target of the provider, into ev.
func (q *Query) addProviderTarget(ev *envEvidence, t *TargetRecord) {
	ev.targets = append(ev.targets, t)
	if !q.freshTarget(t) {
		return
	}
	ev.fresh = append(ev.fresh, t)
	switch q.targetServing(t) {
	case servingNone:
		ev.absent = append(ev.absent, t)
	case servingSome:
		ev.partly = append(ev.partly, t)
	case servingAll:
		ev.serving = append(ev.serving, t)
	}
}

// What a fresh target's interface findings say it serves.
const (
	servingAll     = iota // no declared interface is absent or unobserved
	servingSome           // some declared interfaces are absent
	servingNone           // every declared interface is absent
	servingUnknown        // none is absent and some interface was not observed
)

// targetServing reads t's interface findings: INTERFACE_ABSENT is the
// provider's own report that one interface is not served, an inconclusive
// finding on an interface means it was never seen answering. A target serves
// nothing only when every interface its contract declares is absent; when no
// readable contract says how many it declares, it serves some.
func (q *Query) targetServing(t *TargetRecord) int {
	absent, unseen := map[string]bool{}, false
	for _, f := range t.Findings {
		switch {
		case f.Code == finding.CodeInterfaceAbsent:
			absent[f.Subject.Name] = true
		case f.Subject.Kind == "interface" && finding.CategoryOf(f.Code) == finding.CategoryInconclusive:
			unseen = true
		}
	}
	switch n := q.declaredInterfaces(t); {
	case len(absent) > 0 && n > 0 && len(absent) >= n:
		return servingNone
	case len(absent) > 0:
		return servingSome
	case unseen:
		return servingUnknown
	}
	return servingAll
}

// declaredInterfaces is how many interfaces t's contract declares: the
// revision t runs, else its service's newest readable revision; 0 when no
// readable revision says.
func (q *Query) declaredInterfaces(t *TargetRecord) int {
	r := q.snap.Revisions[t.ContractRevision]
	if r == nil || r.Contract == nil {
		r = nil
		if s := q.snap.Services[t.ServiceKey]; s != nil {
			r = newestReadable(q.revisionsOf(s))
		}
	}
	if r == nil {
		return 0
	}
	return len(r.Contract.Interfaces)
}

// freshTarget reports whether t's evidence is recent and its record trusted.
func (q *Query) freshTarget(t *TargetRecord) bool {
	_, recent := q.recentEvidence(t)
	return recent && !t.Stale && !t.Quarantined
}

// consumerReports files the fresh targets of every consumer in names, the
// consumers declaring a dependency resolved to the provider: those reporting
// they cannot reach it under the dependency's name, and those that do not.
// The keying is the one impact's unreachable evidence uses.
func (q *Query) consumerReports(names map[ServiceKey]map[string]bool, at func(env string) *envEvidence) {
	for _, ck := range slices.Sorted(maps.Keys(names)) {
		c := q.snap.Services[ck]
		if c == nil {
			continue
		}
		for _, tk := range c.Targets {
			t := q.snap.Targets[tk]
			if t == nil || !q.freshTarget(t) {
				continue
			}
			ev := at(t.Environment)
			if reportsUnreachable(t, names[ck]) {
				ev.reporting = append(ev.reporting, t)
			} else {
				ev.reaching = append(ev.reaching, t)
			}
		}
	}
}

// reportsUnreachable reports whether t says it cannot reach a dependency
// named in deps.
func reportsUnreachable(t *TargetRecord, deps map[string]bool) bool {
	return slices.ContainsFunc(t.Findings, func(f finding.Finding) bool {
		return f.Code == finding.CodeDependencyUnreachable && f.Subject.Kind == "dependency" && deps[f.Subject.Name]
	})
}

// availabilityHidden is why the viewer cannot see every target of s, or "".
func (q *Query) availabilityHidden(s *ServiceRecord) string {
	if q.snap.targetsHidden[s.Key] {
		return targetsIncompleteReason
	}
	if id, status := firstIncompleteTargetSource(q.snap); id != "" {
		return fmt.Sprintf(availabilityPartialSource, id, status)
	}
	return ""
}

// availabilityPartial reports whether a consumer of s the viewer may not see,
// or a target of a declared consumer, could change the answer.
func (q *Query) availabilityPartial(s *ServiceRecord) bool {
	if s.HiddenDependents > 0 {
		return true
	}
	return slices.ContainsFunc(q.snap.reverseDeps[s.Key], func(k ServiceKey) bool { return q.snap.targetsHidden[k] })
}

// environmentAvailability applies the availability rules to one environment.
func (q *Query) environmentAvailability(ev *envEvidence, hiddenReason string) EnvironmentAvailability {
	e := EnvironmentAvailability{Reporting: len(ev.fresh), Targets: len(ev.targets), Unreachable: unreachableRefs(ev.reporting), EvidenceAt: newestEvidence(ev)}
	switch {
	case hiddenReason != "":
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, provenanceUnknown, hiddenReason
		e.TargetsHidden = hiddenReason == targetsIncompleteReason
	case len(ev.absent)+len(ev.partly)+len(ev.serving) > 0:
		e.State, e.Provenance, e.Reason = q.servingAvailability(ev)
	case len(ev.reporting) > 0:
		e.State, e.Provenance, e.Reason = reportedAvailability(ev)
	case len(ev.fresh) > 0:
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, provenanceUnknown, availabilityUnseenReason
	default:
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, provenanceUnknown, availabilityStaleReason
	}
	return e
}

// servingAvailability is the state of an environment where a fresh provider
// target says what it serves: what they report of themselves, then what
// consumers report. It is unavailable only when every fresh target serves no
// declared interface.
func (q *Query) servingAvailability(ev *envEvidence) (AvailabilityState, string, string) {
	trouble := append(slices.Clip(ev.absent), ev.partly...)
	msg := absentMessage(trouble)
	switch {
	case len(ev.absent) == len(ev.fresh):
		return AvailabilityUnavailable, ProvenanceObserved, msg
	case len(trouble) > 0:
		return AvailabilityDegraded, ProvenanceObserved, fmt.Sprintf("%d of %d targets report %s", len(trouble), len(ev.targets), msg)
	case len(ev.reporting) > 0:
		return AvailabilityDegraded, ProvenanceInferred, fmt.Sprintf("%d targets reporting; %s cannot reach it", len(ev.serving), reporterNames(ev.reporting))
	}
	age := q.snap.GeneratedAt.Sub(*newestEvidence(ev)).Round(time.Minute)
	return AvailabilityAvailable, ProvenanceObserved, fmt.Sprintf("%d of %d targets reporting, evidence %s old", len(ev.serving), len(ev.targets), shortAge(age))
}

// Age writes d rounded to the minute the way a person would: 1h, 1h30m,
// 15m. Every surface that prints an age of a fleet fact uses it.
// A negative age (a clock behind the API server's) reads as 0m.
func Age(d time.Duration) string { return shortAge(max(d, 0).Round(time.Minute)) }

// shortAge writes a whole-minute age the way a person would: 1h, 1h30m, 15m.
func shortAge(d time.Duration) string {
	s := strings.TrimSuffix(d.String(), "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return cmp.Or(s, "0m")
}

// reportedAvailability is the state of an environment with no fresh provider
// target and some consumer reporting it cannot reach the provider. A provider
// outside the observed clusters is not seen, so it is unavailable only when
// every consumer there reports it.
func reportedAvailability(ev *envEvidence) (AvailabilityState, string, string) {
	all := len(ev.reporting) + len(ev.reaching)
	if len(ev.reaching) == 0 {
		return AvailabilityUnavailable, ProvenanceInferred, fmt.Sprintf("all %d consumers cannot reach it and no target reports it", all)
	}
	return AvailabilityDegraded, ProvenanceInferred, fmt.Sprintf("%d of %d consumers cannot reach it", len(ev.reporting), all)
}

// absentMessage is the first INTERFACE_ABSENT message of the targets in ts.
func absentMessage(ts []*TargetRecord) string {
	for _, t := range ts {
		for _, f := range t.Findings {
			if f.Code == finding.CodeInterfaceAbsent {
				return f.Message
			}
		}
	}
	return ""
}

// reporterNames names the first reporting consumer target and counts the rest.
func reporterNames(ts []*TargetRecord) string {
	name := targetEntityRef(ts[0]).Label
	if len(ts) > 1 {
		name += fmt.Sprintf(" and %d more", len(ts)-1)
	}
	return name
}

// unreachableRefs is the bounded references of the reporting consumer targets.
func unreachableRefs(ts []*TargetRecord) []EntityRef {
	refs := make([]EntityRef, 0, len(ts))
	for _, t := range ts {
		refs = append(refs, targetEntityRef(t))
	}
	sortEntityRefs(refs)
	shown, _, _ := boundSlice(refs, MaxAvailabilityEvidence)
	return shown
}

// newestEvidence is the newest evidence time of the fresh provider targets,
// else of the reporting consumer targets, else of any provider target.
func newestEvidence(ev *envEvidence) *time.Time {
	for _, ts := range [][]*TargetRecord{ev.fresh, ev.reporting, ev.targets} {
		var newest *time.Time
		for _, t := range ts {
			if t.EvidenceAt != nil && (newest == nil || t.EvidenceAt.After(*newest)) {
				newest = t.EvidenceAt
			}
		}
		if newest != nil {
			return copyTime(newest)
		}
	}
	return nil
}

// rollUp is the state over every environment: the state every one holds;
// degraded when confirmed trouble sits beside an environment that answers
// (one degraded, or one unavailable and another available); otherwise
// unknown, so an unknown environment never reads as trouble nor as service.
func rollUp(envs []EnvironmentAvailability) AvailabilityState {
	has := map[AvailabilityState]bool{}
	for _, e := range envs {
		has[e.State] = true
	}
	switch {
	case len(has) == 1:
		return envs[0].State
	case has[AvailabilityDegraded] || (has[AvailabilityUnavailable] && has[AvailabilityAvailable]):
		return AvailabilityDegraded
	}
	return AvailabilityUnknown
}

// offeringAvailability is an offering's availability: per environment, the
// best state of its providers there (available, degraded, unknown, then
// unavailable). When the viewer may not see some providers, or a provider's
// state is tied to no environment (some of its targets are hidden or no target
// places it), no environment reads unavailable: that provider may serve it.
// Its nowhere entry is left out when another provider places the offering.
func (x *availIndex) offeringAvailability(name string, providers []*ServiceRecord) Availability {
	best := map[string]*EnvironmentAvailability{}
	unseen := 0
	byProvider := make([][]EnvironmentAvailability, len(providers))
	placed := false
	for i, p := range providers {
		byProvider[i] = x.environments(p)
		nowhere := isNowhere(byProvider[i])
		if nowhere || x.q.availabilityHidden(p) != "" {
			unseen++
		}
		placed = placed || !nowhere
	}
	for i, p := range providers {
		if placed && isNowhere(byProvider[i]) {
			continue
		}
		for _, e := range byProvider[i] {
			mergeBest(best, e, serviceEntityRef(p))
		}
	}
	hidden := x.q.snap.hiddenProviders[name]
	envs := make([]EnvironmentAvailability, 0, len(best))
	for _, e := range best {
		sortEntityRefs(e.Providers)
		envs = append(envs, floorUnseen(floorHidden(*e, hidden), unseen))
	}
	return x.q.availability(envs)
}

// isNowhere reports whether envs is the one entry of a provider no target or
// report places anywhere.
func isNowhere(envs []EnvironmentAvailability) bool {
	return len(envs) == 1 && envs[0].Environment == "" && envs[0].Targets == 0 && len(envs[0].Unreachable) == 0
}

// mergeBest keeps, in best, the better of its entry and e, a provider's state
// in one environment; providers holding the same state share the entry.
func mergeBest(best map[string]*EnvironmentAvailability, e EnvironmentAvailability, provider EntityRef) {
	cur := best[e.Environment]
	switch {
	case cur == nil || availabilityRank[e.State] < availabilityRank[cur.State]:
		e.Providers = []EntityRef{provider}
		best[e.Environment] = &e
	case e.State == cur.State:
		cur.Providers = append(cur.Providers, provider)
		cur.Reporting, cur.Targets, cur.Partial = cur.Reporting+e.Reporting, cur.Targets+e.Targets, cur.Partial || e.Partial
		cur.Unreachable = boundRefs(append(slices.Clip(cur.Unreachable), e.Unreachable...))
		if e.EvidenceAt != nil && (cur.EvidenceAt == nil || e.EvidenceAt.After(*cur.EvidenceAt)) {
			cur.EvidenceAt = e.EvidenceAt
		}
	}
}

// boundRefs sorts refs and keeps MaxAvailabilityEvidence of them.
func boundRefs(refs []EntityRef) []EntityRef {
	sortEntityRefs(refs)
	shown, _, _ := boundSlice(refs, MaxAvailabilityEvidence)
	return shown
}

// floorHidden marks e partial when hidden providers of the offering exist,
// and turns an unavailable state unknown: a hidden provider may serve it.
func floorHidden(e EnvironmentAvailability, hidden int) EnvironmentAvailability {
	if hidden == 0 {
		return e
	}
	noun := "providers"
	if hidden == 1 {
		noun = "provider"
	}
	note := fmt.Sprintf("%d %s you may not see", hidden, noun)
	e.Partial = true
	if e.State == AvailabilityUnavailable {
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, provenanceUnknown, note
		return e
	}
	e.Reason += "; " + note
	return e
}

// floorUnseen turns e unknown and partial when it reads unavailable while n
// providers of the offering are not seen in every environment.
func floorUnseen(e EnvironmentAvailability, n int) EnvironmentAvailability {
	if n == 0 || e.State != AvailabilityUnavailable {
		return e
	}
	noun := "providers are"
	if n == 1 {
		noun = "provider is"
	}
	e.State, e.Provenance, e.Partial = AvailabilityUnknown, provenanceUnknown, true
	e.Reason = fmt.Sprintf("%s; %d %s not seen in every environment and may serve it", e.Reason, n, noun)
	return e
}

// Lines renders a for text surfaces: one line per environment, then how many
// the bound left out. The CLI and the TUI share the wording.
func (a Availability) Lines() []string {
	out := make([]string, 0, len(a.ByEnvironment))
	for _, e := range a.ByEnvironment {
		out = append(out, e.Line())
	}
	if n := a.Total - len(a.ByEnvironment); n > 0 {
		out = append(out, fmt.Sprintf("%d more environments", n))
	}
	return out
}

// label names e's environment: "(no target you may see)" for the entry of a
// provider whose targets are all hidden from the viewer, "(nowhere observed)"
// for the entry that keeps the reason when no target or report places the
// provider anywhere, "(no environment label)" for targets that carry no
// environment label.
func (e EnvironmentAvailability) label() string {
	if e.Environment != "" {
		return e.Environment
	}
	if e.Targets == 0 && len(e.Unreachable) == 0 {
		if e.TargetsHidden {
			return "(no target you may see)"
		}
		return "(nowhere observed)"
	}
	return "(no environment label)"
}

// Line renders e as "prod: available (observed, <reason>)", marked partial
// when the viewer may not see every consumer or provider.
func (e EnvironmentAvailability) Line() string {
	s := fmt.Sprintf("%s: %s (%s, %s)", e.label(), e.State, e.Provenance, e.Reason)
	if e.Partial {
		s += " [partial]"
	}
	return s
}
