// Package reconcile compares declared dependencies (what a contract says a
// service depends on) against observed dependencies (what runtime telemetry
// proves it actually reached). It is source-agnostic: observed edges may come
// from OpenTelemetry, a service mesh or the operator. The result names three
// states — matched, declared-but-not-observed and observed-but-not-declared —
// so drift between intent and reality is explicit. It never treats absence of
// an observation as proof a dependency is dead; declared-not-observed is a
// prompt to investigate, not a verdict.
package reconcile

import "sort"

// Status classifies one dependency relationship after reconciliation.
type Status string

const (
	// StatusMatched: declared and observed in traffic.
	StatusMatched Status = "matched"
	// StatusDeclaredNotObserved: declared but no traffic observed (dormant, or
	// the window was too short — not proof of a dead dependency).
	StatusDeclaredNotObserved Status = "declared-not-observed"
	// StatusObservedNotDeclared: traffic observed to a dependency the contract
	// does not declare (a shadow/undeclared dependency).
	StatusObservedNotDeclared Status = "observed-not-declared"
)

// Declared is one contract-declared dependency of a service.
type Declared struct {
	Service    string
	Dependency string
	Required   bool
}

// Observed is one observed dependency edge (caller reached callee Count times).
type Observed struct {
	Service    string
	Dependency string
	Count      int
}

// Entry is one reconciled relationship.
type Entry struct {
	Service    string `json:"service"`
	Dependency string `json:"dependency"`
	Status     Status `json:"status"`
	Required   bool   `json:"required,omitempty"`
	Count      int    `json:"count,omitempty"`
}

// Unresolved is observed traffic whose endpoint name could not be mapped to a
// unique domain-qualified service (unknown, or ambiguous across domains). It is
// preserved as explicit unresolved knowledge rather than being forced into a
// default domain, so observed traffic can never be misattributed across domains.
type Unresolved struct {
	Service    string `json:"service"`
	Dependency string `json:"dependency"`
	Count      int    `json:"count"`
	Reason     string `json:"reason"` // "unknown" or "ambiguous"
}

// Summary counts entries by status.
type Summary struct {
	Matched             int `json:"matched"`
	DeclaredNotObserved int `json:"declaredNotObserved"`
	ObservedNotDeclared int `json:"observedNotDeclared"`
	Unresolved          int `json:"unresolved,omitempty"`
}

// Report is the full reconciliation result, entries sorted by service then
// dependency for deterministic output. Unresolved holds observed edges that
// could not be attributed to a unique domain-qualified identity.
type Report struct {
	Entries    []Entry      `json:"entries"`
	Unresolved []Unresolved `json:"unresolved,omitempty"`
	Summary    Summary      `json:"summary"`
}

type key struct{ service, dependency string }

// Reconcile compares declared against observed edges by (service, dependency)
// name. Duplicate declared entries collapse (last wins); duplicate observed
// edges sum their counts.
func Reconcile(declared []Declared, observed []Observed) Report {
	declMap := make(map[key]Declared, len(declared))
	for _, d := range declared {
		declMap[key{d.Service, d.Dependency}] = d
	}
	obsMap := make(map[key]int, len(observed))
	for _, o := range observed {
		obsMap[key{o.Service, o.Dependency}] += o.Count
	}

	var entries []Entry
	for k, d := range declMap {
		e := Entry{Service: d.Service, Dependency: d.Dependency, Required: d.Required, Status: StatusDeclaredNotObserved}
		if c, ok := obsMap[k]; ok {
			e.Status = StatusMatched
			e.Count = c
		}
		entries = append(entries, e)
	}
	for k, c := range obsMap {
		if _, ok := declMap[k]; ok {
			continue
		}
		entries = append(entries, Entry{Service: k.service, Dependency: k.dependency, Status: StatusObservedNotDeclared, Count: c})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Service != entries[j].Service {
			return entries[i].Service < entries[j].Service
		}
		return entries[i].Dependency < entries[j].Dependency
	})
	return Report{Entries: entries, Summary: summarize(entries)}
}

func summarize(entries []Entry) Summary {
	var s Summary
	for _, e := range entries {
		switch e.Status {
		case StatusMatched:
			s.Matched++
		case StatusDeclaredNotObserved:
			s.DeclaredNotObserved++
		case StatusObservedNotDeclared:
			s.ObservedNotDeclared++
		}
	}
	return s
}
