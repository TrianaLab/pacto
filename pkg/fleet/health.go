package fleet

import (
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// categoryHealth is the attention category of an operational target whose own
// health check answered and reports it is failing.
const categoryHealth = "health"

// OperationalHealthTally partitions operational targets by what their declared
// health check reports, one bucket per [AvailabilityState] plus NotObserved, so
// the buckets sum to the target population like [ComplianceTally]. A target is
// Available only when its revision declares health, a source evaluated it, its
// evidence is fresh and no finding names capability/health; anything Pacto cannot
// see is Unknown, never Available. No bucket rolls up into another.
type OperationalHealthTally struct {
	Available   int `json:"available" doc:"Targets whose declared health check was evaluated on fresh evidence with no health finding"`
	Degraded    int `json:"degraded" doc:"Targets reported as degraded; no source reports degradation yet, so it reads 0"`
	Unavailable int `json:"unavailable" doc:"Targets whose health check answered and reports it is failing (HEALTH_FAILING)"`
	Unknown     int `json:"unknown" doc:"Targets whose health Pacto cannot tell: no evaluation, stale or no evidence, an unresolved, inferred (mutable tag) or quarantined revision link, or another finding on the health capability"`
	NotObserved int `json:"notObserved" doc:"Targets whose contract revision declares no health capability"`
}

func (h *OperationalHealthTally) add(state AvailabilityState, observed bool) {
	switch {
	case !observed:
		h.NotObserved++
	case state == AvailabilityAvailable:
		h.Available++
	case state == AvailabilityDegraded:
		h.Degraded++
	case state == AvailabilityUnavailable:
		h.Unavailable++
	default:
		h.Unknown++
	}
}

// healthFindings returns the HEALTH_FAILING finding on capability/health, if
// any, and whether any finding names that subject.
func healthFindings(t *TargetRecord) (failing *finding.Finding, named bool) {
	for i := range t.Findings {
		f := &t.Findings[i]
		if f.Subject.Kind != "capability" || f.Subject.Name != contract.CapabilityHealth {
			continue
		}
		named = true
		if f.Code == finding.CodeHealthFailing {
			return f, true
		}
	}
	return nil, named
}

// failingHealth reports whether the target's health check answered and reports
// it is failing, the one state [Query.targetHealth] reads as unavailable.
func failingHealth(t *TargetRecord) bool {
	f, _ := healthFindings(t)
	return f != nil
}

// declaresHealth reports whether the target's linked revision resolves and
// declares the health capability, and whether it resolves at all.
func (q *Query) declaresHealth(t *TargetRecord) (declared, resolved bool) {
	r := q.snap.Revisions[t.ContractRevision]
	if r == nil || r.Contract == nil {
		return false, false
	}
	return slices.ContainsFunc(r.Contract.Capabilities, func(c contract.Capability) bool { return c.Type == contract.CapabilityHealth }), true
}

// freshEvidence is the source's own freshness rule, and with no window (the
// --freshness 0 build) the fixed RecentEvidenceWindow impact applies.
func (q *Query) freshEvidence(t *TargetRecord) bool {
	return !t.Stale && t.EvidenceAt != nil &&
		(q.snap.provisioningWindow > 0 || !t.EvidenceAt.Before(q.snap.GeneratedAt.Add(-RecentEvidenceWindow)))
}

// targetHealth returns what the target's health check reports and whether it
// applies at all (false only when the linked revision declares no health).
func (q *Query) targetHealth(t *TargetRecord) (AvailabilityState, bool) {
	failing, other := healthFindings(t)
	if failing != nil {
		return AvailabilityUnavailable, true
	}
	if other || t.Quarantined {
		return AvailabilityUnknown, true
	}
	declared, resolved := q.declaresHealth(t)
	switch {
	case t.RevisionMatch == revisionMatchInferred:
		// An inferred link is a mutable tag, not proof of what runs: the
		// revision it names may not be the one the source evaluated.
		return AvailabilityUnknown, true
	case resolved && !declared:
		return "", false
	case !resolved || t.Coverage == nil || !q.freshEvidence(t):
		return AvailabilityUnknown, true
	}
	return AvailabilityAvailable, true
}

// healthAttention reports each target whose health check is failing.
func (q *Query) healthAttention() []AttentionItem {
	var out []AttentionItem
	for _, t := range q.snap.Targets {
		if !q.inScope(t.ServiceKey) {
			continue
		}
		f, _ := healthFindings(t)
		if f == nil {
			continue
		}
		out = append(out, AttentionItem{
			Entity: targetEntityRef(t), Service: string(t.ServiceKey), Label: t.DisplayName(),
			Severity: severityWarning, Code: string(finding.CodeHealthFailing), Category: categoryHealth,
			Summary: "Health check failing on this operational target", Reason: f.Message, Source: t.Source,
			NextStep: "Open the target's findings and the workload's logs; its compliance stays unknown until the check passes",
		})
	}
	return out
}

// unknownCauses is the precedence of the Family-2 codes that name why a
// target's compliance is unknown, most telling first.
var unknownCauses = []struct {
	code   finding.Code
	reason func(subject string) string
}{
	{finding.CodeCollectionFailed, func(s string) string { return "the collector failed to read " + s }},
	{finding.CodeHealthFailing, func(string) string { return "the health check is failing" }},
	{finding.CodeEvidenceStale, func(string) string { return "the evidence is older than the source's freshness window" }},
	{finding.CodeObservationUnsupported, func(s string) string { return s + " cannot be observed in this environment" }},
	{finding.CodeEvidenceInsufficient, func(s string) string { return "the evidence cannot confirm or contradict " + s }},
	{finding.CodeEvidenceMissing, func(s string) string { return "no evidence was collected for " + s }},
}

const (
	noEvidenceReason         = "no evidence was ever collected"
	healthNotEvaluatedReason = "the source reported no evaluation of the declared health check"
)

// dominantUnknown names the cause of a target's unknown compliance: its most
// telling Family-2 finding, else EVIDENCE_MISSING when no evidence (or no
// evaluation of a declared health check) reached Pacto.
func (q *Query) dominantUnknown(t *TargetRecord) (code, reason string) {
	for _, c := range unknownCauses {
		for _, f := range t.Findings {
			if f.Code == c.code {
				return string(c.code), c.reason(f.Subject.Kind + " " + f.Subject.Name)
			}
		}
	}
	if t.EvidenceAt == nil {
		return string(finding.CodeEvidenceMissing), noEvidenceReason
	}
	if declared, _ := q.declaresHealth(t); declared && t.Coverage == nil {
		return string(finding.CodeEvidenceMissing), healthNotEvaluatedReason
	}
	return "UNKNOWN", "compliance is unknown because evidence is insufficient"
}
