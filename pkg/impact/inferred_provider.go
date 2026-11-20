package impact

import (
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// LimitationInferredProvider marks an answer about an inferred contract (see
// [Options.InferredProvider]): no range is checked against it, so the gate is
// unverified whatever the change.
const LimitationInferredProvider = fleet.RangeReasonInferredProvider

// inferredProviderNote qualifies every consumer verdict of an inferred provider.
const inferredProviderNote = "the provider is an inferred contract; Pacto never checks a range against it"

// inferredProviderConsumers are the services whose declared dependencies join
// the external system key, each once: its inferred contract is no fleet
// service, so they are its consumers; analyze makes their verdicts unknown.
func inferredProviderConsumers(snap *fleet.FleetSnapshot, key fleet.ExternalKey) []AffectedConsumer {
	out := []AffectedConsumer{}
	seen := map[fleet.ServiceKey]bool{}
	changed := fleet.ServiceKey(fleet.ExternalSubjectPrefix + string(key))
	for _, rel := range snap.Relationships {
		if rel.ToExternal != key || rel.Provenance != fleet.ProvenanceDeclared || rel.Type != fleet.RelationshipDependency || seen[rel.FromService] {
			continue
		}
		seen[rel.FromService] = true
		c, stale := consumerBase(snap, rel.FromService, changed, nil)
		c.Depth, c.Direct, c.Required, c.Compatibility = 1, true, rel.Required, rel.Compatibility
		c.Provenance, c.Confidence, c.Via = fleet.ProvenanceDeclared, ConfidenceDeclared, []string{fleet.RelationshipDependency}
		if stale {
			c.Confidence = ConfidenceUnknown
		}
		out = append(out, c)
	}
	return out
}

// forceUnknownVerdicts makes every consumer verdict unknown: the new revision
// is an inferred contract, so no range or check against it shows anything.
func (r *Result) forceUnknownVerdicts() {
	for i := range r.Consumers {
		forceUnknown(&r.Consumers[i])
	}
}

// forceUnknown makes c's verdicts unknown and says why.
func forceUnknown(c *AffectedConsumer) {
	c.CompatibilityVerdict, c.Note = CompatibilityUnknown, inferredProviderNote
	if c.rangeVerdict != "" {
		c.rangeVerdict = CompatibilityUnknown
	}
	if c.policyVerdict != "" {
		c.policyVerdict = CompatibilityUnknown
	}
}

// inferredProvider is the gate reason of an answer about an inferred
// contract, "" for any other answer. It comes before every other branch: an
// inferred contract never clears a gate, and no verdict against it blocks.
func (r *Result) inferredProvider() string {
	if !hasCode(r.Limitations, LimitationInferredProvider) {
		return ""
	}
	n := len(r.Consumers)
	return r.Service + " " + r.NewVersion + " is an inferred contract Pacto published from what it sees, so the " +
		strconv.Itoa(n) + " " + plural(n, "consumer that checks", "consumers that check") +
		" a range against it cannot be verified. Declare a contract for it with pacto declare and point the ranges at it"
}
