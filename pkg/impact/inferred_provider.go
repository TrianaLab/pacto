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

// forceUnknown makes c's verdicts unknown and says why. An inferred bundle
// declares no guarantees, so none it lacks is unmet either: tallies,
// Blocking and delivery read the same unknown as the verdict.
func forceUnknown(c *AffectedConsumer) {
	c.CompatibilityVerdict, c.Note, c.Unmet = CompatibilityUnknown, inferredProviderNote, nil
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
	n, k := len(r.Consumers), 0
	for _, c := range r.Consumers {
		if c.Compatibility != "" {
			k++
		}
	}
	next := "Declare a contract for it with pacto declare and point the ranges at it"
	if r.inferredExternal != "" {
		next = fleet.InferredExternalNextStep(r.inferredExternal)
	}
	return r.Service + " " + r.NewVersion + " is an inferred contract, so no range against it can be verified: " +
		strconv.Itoa(k) + " of " + strconv.Itoa(n) + " " + plural(n, "consumer pins", "consumers pin") + " a range on it. " + next
}
