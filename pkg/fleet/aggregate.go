package fleet

import (
	"sort"
	"time"

	"github.com/trianalab/pacto/v3/pkg/finding"
)

// Backend-authoritative aggregates over a COMPLETE population.
//
// Every distribution a product surface draws must come from here, never from a
// bounded preview: a preview is capped at MaxDetailPreview, so counting its Items
// would silently turn "the first 200 targets" into "the fleet". These tallies are
// computed over the whole snapshot slice and their buckets are exhaustive, so a
// consumer can render a proportional visual without inventing a remainder.

// ComplianceTally is the COMPLETE compliance distribution over a target
// population: the four Compliance 2.0 states plus one explicit catch-all, so the
// buckets always sum to the population size. Without the catch-all a consumer
// drawing a distribution has to guess whether the missing slice is a legacy
// status or a bug.
type ComplianceTally struct {
	Compliant    int `json:"compliant"`
	NonCompliant int `json:"nonCompliant"`
	Unknown      int `json:"unknown"`
	Invalid      int `json:"invalid"`
	Other        int `json:"other"`
}

func (c *ComplianceTally) add(status string) {
	switch status {
	case StatusCompliant:
		c.Compliant++
	case StatusNonCompliant:
		c.NonCompliant++
	case StatusUnknown:
		c.Unknown++
	case StatusInvalid:
		c.Invalid++
	default:
		c.Other++
	}
}

// Total is the population the tally covers.
func (c ComplianceTally) Total() int {
	return c.Compliant + c.NonCompliant + c.Unknown + c.Invalid + c.Other
}

// LinkTally is the COMPLETE revision-match certainty distribution over a target
// population. It answers "how confidently does the fleet know which revision each
// target is running" and is orthogonal to compliance and to content
// retrievability (see [RevisionIdentity]).
type LinkTally struct {
	Exact      int `json:"exact"`
	Inferred   int `json:"inferred"`
	Ambiguous  int `json:"ambiguous"`
	Unresolved int `json:"unresolved"`
}

// add buckets one target by the SAME rule targetLinkState uses, so a distribution
// and a per-target badge can never disagree.
func (l *LinkTally) add(t *TargetRecord) {
	switch targetLinkState(t) {
	case "exact":
		l.Exact++
	case "inferred":
		l.Inferred++
	case "ambiguous":
		l.Ambiguous++
	default:
		l.Unresolved++
	}
}

// Total is the population the tally covers.
func (l LinkTally) Total() int { return l.Exact + l.Inferred + l.Ambiguous + l.Unresolved }

// SeverityTally is the COMPLETE finding-severity distribution over a population
// of findings.
type SeverityTally struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
	Unknown  int `json:"unknown"`
}

func (s *SeverityTally) add(sev finding.Severity) {
	switch sev {
	case finding.SeverityError:
		s.Errors++
	case finding.SeverityWarning:
		s.Warnings++
	case finding.SeverityInfo:
		s.Infos++
	default:
		s.Unknown++
	}
}

// Total is the population the tally covers.
func (s SeverityTally) Total() int { return s.Errors + s.Warnings + s.Infos + s.Unknown }

// EvidenceWindow is the freshness envelope of a target population: how many
// targets carry evidence at all, how many are stale, and the oldest and newest
// evidence timestamps. Targets that carry NO evidence are counted separately and
// never folded into "stale": absence of evidence is not evidence of staleness.
//
// Stale is a strict SUBSET of WithEvidence (staleness is only ever set from an
// evidence timestamp older than the freshness window, so a target with no evidence
// is never stale), which makes fresh / stale / no-evidence a partition of the
// population: (WithEvidence-Stale) + Stale + WithoutEvidence == the whole.
type EvidenceWindow struct {
	WithEvidence    int        `json:"withEvidence"`
	WithoutEvidence int        `json:"withoutEvidence"`
	Stale           int        `json:"stale"`
	Quarantined     int        `json:"quarantined"`
	Oldest          *time.Time `json:"oldest,omitempty"`
	Newest          *time.Time `json:"newest,omitempty"`
}

func (e *EvidenceWindow) add(t *TargetRecord) {
	if t.Stale {
		e.Stale++
	}
	if t.Quarantined {
		e.Quarantined++
	}
	if t.EvidenceAt == nil {
		e.WithoutEvidence++
		return
	}
	e.WithEvidence++
	if e.Oldest == nil || t.EvidenceAt.Before(*e.Oldest) {
		e.Oldest = copyTime(t.EvidenceAt)
	}
	if e.Newest == nil || t.EvidenceAt.After(*e.Newest) {
		e.Newest = copyTime(t.EvidenceAt)
	}
}

// ServiceSummary is the complete, backend-authoritative aggregate a service page
// draws its operational picture from. Every counter covers ALL of the service's
// targets, revisions and edges — the sibling previews on ServiceDetailData are
// bounded and must never be counted instead.
type ServiceSummary struct {
	Targets    int             `json:"targets"`
	Compliance ComplianceTally `json:"compliance"`
	Links      LinkTally       `json:"links"`
	Evidence   EvidenceWindow  `json:"evidence"`
	Findings   SeverityTally   `json:"findings"`
	// Revisions is every known revision of the service; RevisionsInUse is how many
	// of them at least one target is linked to (exact or inferred). The gap between
	// them is the "how much of what we know is actually running" question the old
	// version list answered and the entity model dropped.
	Revisions        int `json:"revisions"`
	InvalidRevisions int `json:"invalidRevisions"`
	RevisionsInUse   int `json:"revisionsInUse"`
	// Declared/Observed/Reconciled describe this service's OUTGOING dependency
	// edges. DeclaredNotObserved and ObservedNotDeclared are the two drift
	// directions, kept apart because they mean opposite things: a declared edge
	// nobody exercises versus runtime traffic nobody declared.
	DeclaredDependencies int `json:"declaredDependencies"`
	UnresolvedDeclared   int `json:"unresolvedDeclared"`
	ReconciledMatched    int `json:"reconciledMatched"`
	DeclaredNotObserved  int `json:"declaredNotObserved"`
	ObservedNotDeclared  int `json:"observedNotDeclared"`
}

// serviceSummary aggregates the service's complete target, revision and edge
// populations. inUse is the set of revisions at least one target links to.
func (q *Query) serviceSummary(view *ServiceView) (ServiceSummary, map[RevisionKey]bool) {
	sum := ServiceSummary{Targets: len(view.Targets), Revisions: len(view.Revisions)}
	inUse := map[RevisionKey]bool{}
	for _, t := range view.Targets {
		sum.Compliance.add(t.Compliance)
		sum.Links.add(t)
		sum.Evidence.add(t)
		for _, f := range t.Findings {
			sum.Findings.add(f.Severity)
		}
		if t.ContractRevision != "" {
			inUse[t.ContractRevision] = true
		}
	}
	for _, r := range view.Revisions {
		// A ServiceView carries CLONES, and a clone is produced through JSON, so it
		// loses the unexported "was this revision actually validated" flag: read
		// validity from the authoritative snapshot record instead. Counting it off the
		// clone silently reported every service as having zero invalid revisions.
		if rec := q.snap.Revisions[r.Key]; rec != nil && revisionStatus(rec) == StatusInvalid {
			sum.InvalidRevisions++
		}
		if inUse[r.Key] {
			sum.RevisionsInUse++
		}
	}
	q.tallyServiceEdges(&sum, view.Service.Key)
	return sum, inUse
}

// tallyServiceEdges counts the service's outgoing declared dependencies and the
// two drift directions. An observed edge is "observed, not declared" iff no
// declared edge exists for the same (from, to) service pair — the same
// declared/observed split the engine keeps.
func (q *Query) tallyServiceEdges(sum *ServiceSummary, key ServiceKey) {
	declared := map[ServiceKey]bool{}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromService != key || rel.Type != RelationshipDependency {
			continue
		}
		if rel.Provenance == ProvenanceObserved {
			continue
		}
		declared[rel.ToService] = true
		sum.DeclaredDependencies++
		if !rel.Resolved {
			sum.UnresolvedDeclared++
		}
		switch rel.Reconciliation {
		case ReconciliationMatched:
			sum.ReconciledMatched++
		case ReconciliationDeclaredNotObserved:
			sum.DeclaredNotObserved++
		}
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromService != key || rel.Provenance != ProvenanceObserved {
			continue
		}
		if !declared[rel.ToService] {
			sum.ObservedNotDeclared++
		}
	}
}

// labelsPreview projects observed workload labels into the bounded fact preview
// shape. Labels come from a runtime source, so the emitted list, each key and each
// value are capped exactly like observed runtime facts.
func labelsPreview(m map[string]string) RuntimePreview {
	keys, all := keysBounded(m, MaxDetailPreview)
	items := make([]RuntimeFact, 0, len(keys))
	for _, k := range keys {
		items = append(items, RuntimeFact{Key: capLen(k, maxRuntimeKeyLen), Value: capLen(m[k], maxRuntimeValueLen)})
	}
	rp := RuntimePreview{Count: len(items), Scanned: len(items), Truncated: !all, Items: items}
	if all {
		total := len(items)
		rp.Total = &total
	}
	return rp
}

// chronologicalRevisionRefs returns the service's revisions NEWEST FIRST in
// canonical revision chronology (see siblingRevisions), not in RevisionKey order.
// Key order is content-digest order, which is semantically arbitrary; a version
// list sorted by digest is the regression that made revision history unreadable.
func chronologicalRevisionRefs(revs []*ContractRevision) []EntityRef {
	ordered := append([]*ContractRevision{}, revs...)
	sort.Slice(ordered, func(i, j int) bool { return lessRevisionChrono(ordered[j], ordered[i]) })
	return revisionRefs(ordered)
}
