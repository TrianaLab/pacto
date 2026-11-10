package impact

import (
	"context"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// policyReferrers groups, by referring service, the declared, resolved and
// visible policy references to changed. A policy reference is never a
// runtime consumption edge (see [fleet.ConsumesEdge]), so the dependents walk
// never reaches these services.
func policyReferrers(snap *fleet.FleetSnapshot, changed fleet.ServiceKey) map[fleet.ServiceKey][]fleet.Relationship {
	out := map[fleet.ServiceKey][]fleet.Relationship{}
	for _, r := range snap.Relationships {
		if r.Type != fleet.RelationshipPolicyRef || r.Provenance != fleet.ProvenanceDeclared || !r.Resolved || r.Hidden ||
			r.ToService != changed || r.FromService == changed {
			continue
		}
		out[r.FromService] = append(out[r.FromService], r)
	}
	return out
}

// policyRefVerdict checks each referring revision's contract against the
// policy the provider's new revision publishes. The revision that accepts it
// least speaks for the referrer, as with dependency ranges.
func policyRefVerdict(snap *fleet.FleetSnapshot, refs []fleet.Relationship, provider *contract.Bundle) (verdict, note string) {
	byRev := map[fleet.RevisionKey][]string{}
	var order []fleet.RevisionKey
	for _, r := range refs {
		if byRev[r.FromRevision] == nil {
			order = append(order, r.FromRevision)
		}
		byRev[r.FromRevision] = append(byRev[r.FromRevision], r.To)
	}
	slices.Sort(order)
	for i, rk := range order {
		v, n := revisionPolicyVerdict(snap.Revisions[rk], byRev[rk], provider)
		if i == 0 || verdictRank(v) > verdictRank(verdict) {
			verdict, note = v, n
		}
	}
	return verdict, note
}

// revisionPolicyVerdict checks rev's contract document against the policies
// it names that reference provider, resolved to provider's new revision. It
// is unknown when the document or the policy cannot be read, and says why.
func revisionPolicyVerdict(rev *fleet.ContractRevision, names []string, provider *contract.Bundle) (string, string) {
	who := provider.Contract.Service.Name + "@" + provider.Contract.Service.Version
	if rev == nil || rev.Contract == nil || rev.BundleFS() == nil {
		return CompatibilityUnknown, "the referrer's contract cannot be read, so it cannot be checked against " + who
	}
	fsys := rev.BundleFS()
	raw, err := fs.ReadFile(fsys, "pacto.yaml")
	if err != nil {
		return CompatibilityUnknown, "the referrer's contract cannot be read (" + err.Error() + "), so it cannot be checked against " + who
	}
	// Only the policies that name the changed service: what the referrer's
	// other policies say is no business of this change.
	c := *rev.Contract
	c.Policies = nil
	byRef := providerByRef{}
	for _, pol := range rev.Contract.Policies {
		if pol.Ref != "" && slices.Contains(names, pol.Name) {
			c.Policies = append(c.Policies, pol)
			byRef[pol.Ref] = provider
		}
	}
	policies, res := validation.ResolvePoliciesWithResolver(context.Background(), &c, fsys, byRef)
	if why := messages(res.Errors, ""); why != "" {
		return CompatibilityUnknown, why
	}
	if why := messages(validation.EnforcePolicies(raw, policies).Errors, " against "+who); why != "" {
		return CompatibilityIncompatible, why
	}
	return CompatibilityCompatible, "the contract satisfies the policy of " + who
}

// messages joins the messages of errs, each followed by suffix.
func messages(errs []contract.ValidationError, suffix string) string {
	why := make([]string, 0, len(errs))
	for _, e := range errs {
		why = append(why, e.Message+suffix)
	}
	return strings.Join(why, "; ")
}

// addPolicyReferrers adds the services that reference changed through a
// policy reference as consumers, each checked against the new revision's
// policy. A referrer that is already a consumer keeps one row: the verdict
// that accepts least speaks and Via lists both edge types.
func (r *Result) addPolicyReferrers(snap *fleet.FleetSnapshot, changed fleet.ServiceKey, provider *contract.Bundle) {
	refs := policyReferrers(snap, changed)
	keys := make([]fleet.ServiceKey, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	unchecked := 0
	for _, k := range keys {
		c := consumerBase(snap, k, changed)
		c.Depth, c.Direct, c.Provenance, c.Confidence = 1, true, fleet.ProvenanceDeclared, ConfidenceDeclared
		c.Via = []string{fleet.RelationshipPolicyRef}
		c.CompatibilityVerdict, c.Note = policyRefVerdict(snap, refs[k], provider)
		c.policyVerdict = c.CompatibilityVerdict
		if c.policyVerdict == CompatibilityUnknown {
			unchecked++
		}
		r.mergeConsumer(c)
	}
	if unchecked > 0 {
		r.Limitations = append(r.Limitations, fleet.Limitation{Code: "POLICY_REFERRERS_UNCHECKED", Source: limitationSource,
			Message: strconv.Itoa(unchecked) + " " + plural(unchecked, "service that references", "services that reference") + " " + string(changed) +
				" could not be checked against its new policy, so whether the change breaks " + plural(unchecked, "it", "them") + " is unknown"})
	}
}

// mergeConsumer adds a policy referrer, or folds it into the consumer row
// for the same service. A referrer reached before only through other
// services references the changed one itself, so it becomes direct.
func (r *Result) mergeConsumer(c AffectedConsumer) {
	i := slices.IndexFunc(r.Consumers, func(e AffectedConsumer) bool { return e.Service == c.Service && e.Domain == c.Domain })
	if i < 0 {
		r.Consumers = append(r.Consumers, c)
		return
	}
	e := &r.Consumers[i]
	if e.Depth > 1 {
		e.Depth, e.Direct, e.Path, e.Provenance, e.Confidence = c.Depth, c.Direct, c.Path, c.Provenance, c.Confidence
	}
	if verdictRank(c.CompatibilityVerdict) > verdictRank(e.CompatibilityVerdict) {
		e.CompatibilityVerdict = c.CompatibilityVerdict
	}
	e.Note = strings.TrimPrefix(e.Note+"; "+c.Note, "; ")
	e.Via = append(e.Via, c.Via...)
	e.policyVerdict = c.policyVerdict
}
