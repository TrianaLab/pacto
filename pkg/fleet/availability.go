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
	Environment string            `json:"environment" doc:"The environment label of the targets; empty for targets with no label"`
	State       AvailabilityState `json:"state" enum:"available,degraded,unavailable,unknown" doc:"Whether the capability answers here"`
	Provenance  string            `json:"provenance" enum:"observed,inferred,unknown" doc:"observed from the provider's own targets, inferred from consumer reports, unknown when nothing fresh is seen"`
	Reason      string            `json:"reason" doc:"Why the state is what it is"`
	Partial     bool              `json:"partial,omitempty" doc:"Consumers or providers the viewer may not see are not counted"`
	Reporting   int               `json:"reporting" doc:"Provider targets with fresh evidence"`
	Targets     int               `json:"targets" doc:"Provider targets in this environment the viewer can see"`
	Unreachable []EntityRef       `json:"unreachable,omitempty" doc:"Consumer targets that report they cannot reach it, bounded"`
	EvidenceAt  *time.Time        `json:"evidenceAt,omitempty" doc:"The newest fresh evidence the state reads"`
	Providers   []EntityRef       `json:"providers,omitempty" doc:"On an offering: the providers whose state this is"`
}

// Availability is a capability's state by environment and its roll-up.
type Availability struct {
	State         AvailabilityState         `json:"state" enum:"available,degraded,unavailable,unknown" doc:"available when every environment is, unavailable when every one is, unknown when every one is or none is listed, else degraded"`
	ByEnvironment []EnvironmentAvailability `json:"byEnvironment" doc:"The state per environment, bounded"`
	Total         int                       `json:"total" doc:"Environments before the bound"`
	AsOf          time.Time                 `json:"asOf" doc:"The snapshot time the evidence is judged against"`
}

// Reasons an availability is unknown.
const (
	availabilityStaleReason   = "evidence older than 24h before asOf"
	availabilityNoneReason    = "no target reports it; a provider outside the observed clusters is not seen"
	availabilityHiddenSuffix  = "; consumers you may not see are not counted"
	availabilityPartialSource = "source %s is %s"
)

// availabilityRank orders states from best to worst for an offering: an
// unknown provider may be available, so it outranks a confirmed unavailable one.
var availabilityRank = map[AvailabilityState]int{AvailabilityAvailable: 0, AvailabilityDegraded: 1, AvailabilityUnknown: 2, AvailabilityUnavailable: 3}

// envEvidence is what one environment holds for a provider.
type envEvidence struct {
	targets, fresh, absent []*TargetRecord
	reporting, reaching    []*TargetRecord
}

// providerAvailability is s's availability per environment, bounded.
func (q *Query) providerAvailability(s *ServiceRecord) Availability {
	return q.availability(q.providerEnvironments(s))
}

// providerEnvironments is s's state in every environment: what its own fresh
// targets and the INTERFACE_ABSENT finding they carry say, then the
// DEPENDENCY_UNREACHABLE reports of the consumers that declare it.
// Compliance is never read.
func (q *Query) providerEnvironments(s *ServiceRecord) []EnvironmentAvailability {
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
	q.consumerReports(s, at)
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
		e := EnvironmentAvailability{State: AvailabilityUnknown, Provenance: availabilityProvenanceUnknown, Reason: cmp.Or(hiddenReason, availabilityNoneReason)}
		envs = append(envs, markPartial(e, partial))
	}
	return envs
}

// availabilityProvenanceUnknown is the provenance of a state nothing fresh supports.
const availabilityProvenanceUnknown = "unknown"

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
	if _, ok := interfaceAbsent(t); ok {
		ev.absent = append(ev.absent, t)
	}
}

// freshTarget reports whether t's evidence is recent and its record trusted.
func (q *Query) freshTarget(t *TargetRecord) bool {
	_, recent := q.recentEvidence(t)
	return recent && !t.Stale && !t.Quarantined
}

// interfaceAbsent is the message of t's first INTERFACE_ABSENT finding: the
// provider's own report that an interface is not being served.
func interfaceAbsent(t *TargetRecord) (string, bool) {
	for _, f := range t.Findings {
		if f.Code == finding.CodeInterfaceAbsent {
			return f.Message, true
		}
	}
	return "", false
}

// consumerReports files the fresh targets of every consumer declaring a
// dependency resolved to s: those reporting they cannot reach it under the
// dependency's name, and those that do not. The keying is the one impact's
// unreachable evidence uses.
func (q *Query) consumerReports(s *ServiceRecord, at func(env string) *envEvidence) {
	names := map[ServiceKey]map[string]bool{}
	for _, r := range q.snap.Relationships {
		if r.Type == RelationshipDependency && ConsumesEdge(r) && r.ToService == s.Key && r.FromService != s.Key {
			if names[r.FromService] == nil {
				names[r.FromService] = map[string]bool{}
			}
			names[r.FromService][r.To] = true
		}
	}
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
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, availabilityProvenanceUnknown, hiddenReason
	case len(ev.fresh) > 0:
		e.State, e.Provenance, e.Reason = q.servingAvailability(ev)
	case len(ev.reporting) > 0:
		e.State, e.Provenance, e.Reason = reportedAvailability(ev)
	default:
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, ProvenanceObserved, availabilityStaleReason
	}
	return e
}

// servingAvailability is the state of an environment where the provider has
// fresh targets: what they report of themselves, then what consumers report.
func (q *Query) servingAvailability(ev *envEvidence) (AvailabilityState, string, string) {
	msg, _ := firstInterfaceAbsent(ev.absent)
	switch {
	case len(ev.absent) == len(ev.fresh):
		return AvailabilityUnavailable, ProvenanceObserved, msg
	case len(ev.absent) > 0:
		return AvailabilityDegraded, ProvenanceObserved, fmt.Sprintf("%d of %d targets report %s", len(ev.absent), len(ev.targets), msg)
	case len(ev.reporting) > 0:
		return AvailabilityDegraded, ProvenanceInferred, fmt.Sprintf("%d targets reporting; %s cannot reach it", len(ev.fresh), reporterNames(ev.reporting))
	}
	age := q.snap.GeneratedAt.Sub(*newestEvidence(ev)).Round(time.Minute)
	return AvailabilityAvailable, ProvenanceObserved, fmt.Sprintf("%d of %d targets reporting, evidence %s old", len(ev.fresh), len(ev.targets), age)
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

// firstInterfaceAbsent is the INTERFACE_ABSENT message of the first target.
func firstInterfaceAbsent(ts []*TargetRecord) (string, bool) {
	if len(ts) == 0 {
		return "", false
	}
	return interfaceAbsent(ts[0])
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

// rollUp is the state over every environment: available or unavailable when
// every one is, unknown when every one is or none is listed, else degraded.
func rollUp(envs []EnvironmentAvailability) AvailabilityState {
	if len(envs) == 0 {
		return AvailabilityUnknown
	}
	first := envs[0].State
	for _, e := range envs[1:] {
		if e.State != first {
			return AvailabilityDegraded
		}
	}
	return first
}

// offeringAvailability is an offering's availability: per environment, the
// best state of its providers there (available, degraded, unknown, then
// unavailable). When the viewer may not see some providers, no environment
// reads unavailable.
func (q *Query) offeringAvailability(name string, providers []*ServiceRecord) Availability {
	best := map[string]*EnvironmentAvailability{}
	for _, p := range providers {
		for _, e := range q.providerEnvironments(p) {
			mergeBest(best, e, serviceEntityRef(p))
		}
	}
	hidden := q.snap.hiddenProviders[name]
	envs := make([]EnvironmentAvailability, 0, len(best))
	for _, e := range best {
		sortEntityRefs(e.Providers)
		envs = append(envs, floorHidden(*e, hidden))
	}
	return q.availability(envs)
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
		cur.Unreachable = boundRefs(append(cur.Unreachable, e.Unreachable...))
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
		e.State, e.Provenance, e.Reason = AvailabilityUnknown, availabilityProvenanceUnknown, note
		return e
	}
	e.Reason += "; " + note
	return e
}
