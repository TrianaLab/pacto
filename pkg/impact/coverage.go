package impact

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// MaxUnknownRanges bounds the unknown ranges a coverage lists.
const MaxUnknownRanges = 20

// RangeCoverage is how many dependency edges declare a compatibility range,
// how many a signed acceptance made known and which stay unknown.
type RangeCoverage struct {
	Scope        string         `json:"scope" doc:"The edges counted, such as 'consumers of ledger' or 'dependencies of 12 services in .'"`
	Edges        int            `json:"edges" doc:"Dependency edges counted"`
	Declared     int            `json:"declared" doc:"Edges whose contract declares a compatibility range"`
	Accepted     int            `json:"accepted" doc:"Edges whose range a counted acceptance makes known, not yet declared in the contract"`
	Unknown      []UnknownRange `json:"unknown,omitempty" doc:"Edges whose range is unknown, at most 20"`
	UnknownTotal int            `json:"unknownTotal,omitempty" doc:"How many edges have an unknown range, including those beyond the bound"`
}

// UnknownRange is one dependency edge whose range is unknown, with the
// range Pacto proposes for it, if any.
type UnknownRange struct {
	From     string `json:"from" doc:"The consumer"`
	To       string `json:"to" doc:"The provider it depends on"`
	Proposed string `json:"proposed,omitempty" doc:"The range Pacto proposes from what runs, such as ^2.3.0; inferred, so it never counts"`
}

// Range policy values.
const (
	PolicyWarn = "warn"
	PolicyFail = "fail"
)

// Gate reason codes the range policy sets (blocked).
const (
	GateCodePolicyUnknownRange = "POLICY_UNKNOWN_RANGE"
	GateCodePolicyUncontracted = "POLICY_UNCONTRACTED"
)

// NotePolicyWarning starts a note a range policy set to warn adds.
const NotePolicyWarning = "POLICY_WARNING"

// Policy is the range policy a BREAKING answer is held to: whether an
// unknown range, and an uncontracted caller seen within the window, only
// warn or hold the change.
type Policy struct {
	UnknownRange       string `json:"unknownRange" enum:"warn,fail" doc:"fail holds a BREAKING change while a direct consumer's range is unknown and not accepted"`
	Uncontracted       string `json:"uncontracted" enum:"warn,fail" doc:"fail holds a BREAKING change while a caller no contract describes was seen within uncontractedWindow"`
	UncontractedWindow string `json:"uncontractedWindow,omitempty" doc:"How recent an uncontracted call holds the change, as a Go duration such as 168h0m0s"`
}

// ParsePolicy reads repeated key=value policy values: unknown-range=warn|fail
// and uncontracted=warn|fail:<window>, the window in days (7d) or a Go
// duration (12h). Unset keys default to warn.
func ParsePolicy(values []string) (Policy, error) {
	p := Policy{UnknownRange: PolicyWarn, Uncontracted: PolicyWarn}
	for _, v := range values {
		key, val, _ := strings.Cut(v, "=")
		switch key {
		case "unknown-range":
			if val != PolicyWarn && val != PolicyFail {
				return p, fmt.Errorf("policy %s: use unknown-range=warn or unknown-range=fail", v)
			}
			p.UnknownRange = val
		case "uncontracted":
			mode, window, _ := strings.Cut(val, ":")
			d, ok := policyWindow(window)
			switch {
			case mode == PolicyWarn && window == "":
				p.Uncontracted, p.UncontractedWindow = PolicyWarn, ""
			case mode == PolicyFail && ok:
				p.Uncontracted, p.UncontractedWindow = PolicyFail, d.String()
			default:
				return p, fmt.Errorf("policy %s: use uncontracted=warn or uncontracted=fail:<window>, such as fail:7d", v)
			}
		default:
			return p, fmt.Errorf("policy %s: use unknown-range=warn|fail or uncontracted=warn|fail:<window>", v)
		}
	}
	return p, nil
}

// policyWindow reads a positive window: days (7d) or a Go duration.
func policyWindow(s string) (time.Duration, bool) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		return time.Duration(days) * 24 * time.Hour, err == nil && days > 0 && days <= 36500
	}
	d, err := time.ParseDuration(s)
	return d, err == nil && d > 0
}

// rangeCoverage counts the direct declared consumers of the changed
// service: declared when the consumer declares a range, accepted when a
// counted acceptance of accs accepts its compatibility field, else unknown
// with the range its newest record proposes.
func (r *Result) rangeCoverage(snap *fleet.FleetSnapshot, accs func(fleet.ServiceKey) ([]Acceptance, bool)) {
	cov := &RangeCoverage{Scope: "consumers of " + r.Service}
	// ponytail: the field names the dependency by the provider's name; a
	// consumer that names it otherwise reads unknown until it is declared.
	field := "dependencies[" + r.Service + "].compatibility"
	for _, c := range r.Consumers {
		if !c.Direct || c.Uncontracted || !slices.Contains(c.Via, fleet.RelationshipDependency) {
			continue
		}
		cov.Edges++
		key := fleet.NewServiceKeyDomain(c.Domain, c.Service)
		switch {
		case contract.IsRange(c.Compatibility):
			cov.Declared++
		case rangeAccepted(accs, key, field):
			cov.Accepted++
		default:
			cov.AddUnknown(UnknownRange{From: c.Service, To: r.Service, Proposed: proposedRange(snap, key, field)})
			r.unknownRanges = append(r.unknownRanges, c.Service)
		}
	}
	r.Ranges = cov
}

// AddUnknown lists u within the bound and counts it.
func (c *RangeCoverage) AddUnknown(u UnknownRange) {
	if len(c.Unknown) < MaxUnknownRanges {
		c.Unknown = append(c.Unknown, u)
	}
	c.UnknownTotal++
}

// rangeAccepted reports whether a counted acceptance of consumer accepts
// field. A listing that could not be read accepts nothing it did not show.
func rangeAccepted(accs func(fleet.ServiceKey) ([]Acceptance, bool), consumer fleet.ServiceKey, field string) bool {
	if accs == nil {
		return false
	}
	list, _ := accs(consumer)
	return slices.ContainsFunc(list, func(a Acceptance) bool { return a.State == AcceptCounted && slices.Contains(a.Fields, field) })
}

// proposedRange is the range consumer's newest revision's record proposes
// for field, "" when none.
func proposedRange(snap *fleet.FleetSnapshot, consumer fleet.ServiceKey, field string) string {
	if rev := newestRevision(snap, consumer); rev != nil {
		return rev.Record.Proposed(field)
	}
	return ""
}

// applyPolicy records the policy the answer is held to, defaults filled,
// and adds a POLICY_WARNING note for each warn policy a BREAKING change
// meets. A fail policy holds the gate through policyGate.
func (r *Result) applyPolicy(p Policy) {
	p.UnknownRange, p.Uncontracted = cmp.Or(p.UnknownRange, PolicyWarn), cmp.Or(p.Uncontracted, PolicyWarn)
	r.Policy = &p
	if r.Classification != "BREAKING" {
		return
	}
	if names := r.unknownNotAccepted(); p.UnknownRange == PolicyWarn && len(names) > 0 {
		r.Notes = append(r.Notes, NotePolicyWarning+": "+r.unknownRangeText(names)+" (policy unknown-range=warn)")
	}
	if names := r.uncontractedWithin(); p.Uncontracted == PolicyWarn && len(names) > 0 {
		r.Notes = append(r.Notes, NotePolicyWarning+": "+r.uncontractedText(names)+" (policy uncontracted=warn)")
	}
}

// policyGate is the code and reason of a fail policy that holds a BREAKING
// change, "" when none does. A policy never clears.
func (r *Result) policyGate() (code, why string) {
	p := r.Policy
	if p == nil || r.Classification != "BREAKING" {
		return "", ""
	}
	if names := r.unknownNotAccepted(); p.UnknownRange == PolicyFail && len(names) > 0 {
		return GateCodePolicyUnknownRange, r.unknownRangeText(names) + " FAIL (policy unknown-range=fail)"
	}
	if names := r.uncontractedWithin(); p.Uncontracted == PolicyFail && len(names) > 0 {
		return GateCodePolicyUncontracted, r.uncontractedText(names) + " FAIL (policy uncontracted=fail:" + p.UncontractedWindow + ")"
	}
	return "", ""
}

// unknownNotAccepted names the direct consumers whose range is unknown and
// who did not accept the break either.
func (r *Result) unknownNotAccepted() []string {
	var out []string
	for _, c := range r.Consumers {
		if slices.Contains(r.unknownRanges, c.Service) && (c.Break == nil || c.Break.State != BreakAccepted) {
			out = append(out, c.Service)
		}
	}
	return out
}

// uncontractedWithin names the uncontracted callers with an observed call
// inside the policy window. A call with no time cannot be shown to be
// older, so it counts as inside.
func (r *Result) uncontractedWithin() []string {
	window, _ := time.ParseDuration(r.Policy.UncontractedWindow) // ponytail: ParsePolicy wrote it; an unreadable one is 0
	since := r.AsOf.Add(-window)
	var out []string
	for _, c := range r.Consumers {
		if c.Uncontracted && slices.ContainsFunc(c.Evidence, func(e Evidence) bool {
			return e.Kind == EvidenceObservedCall && (e.At == nil || e.At.After(since))
		}) {
			out = append(out, c.Service)
		}
	}
	return out
}

// unknownRangeText says which consumers' ranges on the changed service are
// unknown: "ledger -> reports range unknown, 1 consumer".
func (r *Result) unknownRangeText(names []string) string {
	return r.Service + " -> " + strings.Join(names, ", ") + " range unknown, " + strconv.Itoa(len(names)) + " " + plural(len(names), "consumer", "consumers")
}

// uncontractedText says which callers no contract describes call the
// changed service.
func (r *Result) uncontractedText(names []string) string {
	return strconv.Itoa(len(names)) + " " + plural(len(names), "caller", "callers") + " no contract describes " +
		plural(len(names), "calls", "call") + " " + r.Service + ": " + strings.Join(names, ", ")
}
