package fleet

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// contributionRefusals counts, per producer, the contributed bundles Build
// refused and the reason of the first.
type contributionRefusals map[string]*contributionRefusal

type contributionRefusal struct {
	n     int
	first string
}

func (r contributionRefusals) add(producer, reason string) {
	if r[producer] == nil {
		r[producer] = &contributionRefusal{first: reason}
	}
	r[producer].n++
}

// limitations is one CONTRIBUTION_REFUSED per producer of source.
func (r contributionRefusals) limitations(source string) []Limitation {
	var out []Limitation
	for _, p := range slices.Sorted(maps.Keys(r)) {
		out = append(out, Limitation{Code: LimitationContributionRefused, Source: source,
			Message: refusedMessage(p, r[p])})
	}
	return out
}

// downgradeRows makes the producer row of every producer r refused a bundle
// of read partial, saying why: the reader counted those bundles read.
func (r contributionRefusals) downgradeRows(rows []Provider) {
	for i, p := range rows {
		ref := r[strings.TrimPrefix(p.ID, ProducerRowPrefix)]
		if p.producer == nil || ref == nil {
			continue
		}
		rows[i], _ = rebuiltProducerRow(p, nil, func(f *ProducerFacts) {
			f.Problems = append(f.Problems, refusedMessage(f.Producer, ref))
		})
	}
}

// refusedMessage says how many bundles producer contributed were refused and why.
func refusedMessage(producer string, r *contributionRefusal) string {
	if r.n == 1 {
		return "1 bundle " + producer + " contributed was refused because " + r.first
	}
	return strconv.Itoa(r.n) + " bundles " + producer + " contributed were refused, the first because " + r.first
}

// contributionReason is why rev, read as producer's contribution, cannot be
// one, or "" when it can.
func contributionReason(rev *ContractRevision, producer string) string {
	if p, ok := contract.ContributionRef(rev.ResolvedRef); !ok || p != producer {
		return "the ref names no contribution of " + producer
	}
	return contract.ContributionRefusal(rev.bundle, rev.Record)
}

// ingestContribution reads rev, which a source marked contributed by c, as a
// producer's contribution: keyed in the contribution domain its ref names
// whatever domain the source gave it. A revision that declares anything, or
// whose ref names no contribution of c.Producer, is refused into refused. It
// reports whether rev was accepted.
func ingestContribution(rev *ContractRevision, c *Contributor, refused contributionRefusals) bool {
	if why := contributionReason(rev, c.Producer); why != "" {
		refused.add(c.Producer, why)
		return false
	}
	content := strings.TrimPrefix(string(rev.Key), string(rev.ServiceKey)+"@")
	rev.Domain = contract.OCIDomain(rev.ResolvedRef)
	rev.ServiceKey = NewServiceKeyDomain(rev.Domain, rev.Service)
	rev.Key = NewRevisionKey(rev.ServiceKey, content)
	cp := *c
	rev.contributor = &cp
	return true
}

// divertRevision keeps rev out of the declared services: a contribution
// Build refuses, or a revision read under pacto-inferred that is no
// contribution (see divertInferredProvider). A refusal says what the producer
// sent, never that the source failed. It reports whether rev was diverted.
func divertRevision(snap *FleetSnapshot, rev *ContractRevision, raw RawRevision, source string, refused contributionRefusals) bool {
	if raw.Contributor != nil {
		if !ingestContribution(rev, raw.Contributor, refused) {
			return true
		}
		recordHead(snap, rev)
		return false
	}
	return divertInferredProvider(snap, rev, source)
}

// reportedBy is the producer that reported a target with scope through src:
// its scope when src is an Evidence Server, else "".
func reportedBy(src Source, scope string) string {
	if src.Kind() != SourceKindEvidenceHTTP {
		return ""
	}
	return scope
}

// resolveDomain is the domain rev's names resolve in: for a contribution, the
// installation registry its producer's repository lies under, else its own.
func resolveDomain(rev *ContractRevision) string {
	if rev.contributor == nil {
		return rev.Domain
	}
	return strings.TrimSuffix(rev.Domain, "/"+contract.InferredRepository+"/"+rev.contributor.Producer)
}

// supersedeContributions drops every contributed revision whose service name
// a declared revision or a target of its installation registry already uses:
// the contract wins. The winning keys are kept per producer for Restrict, and
// one CONTRIBUTION_SUPERSEDED per producer counts the services dropped.
func supersedeContributions(snap *FleetSnapshot) {
	declared := map[ServiceKey]bool{}
	for _, r := range snap.Revisions {
		if r.contributor == nil {
			declared[r.ServiceKey] = true
		}
	}
	for _, t := range snap.Targets {
		declared[NewServiceKeyDomain(t.Domain, t.Service)] = true
	}
	for k, r := range snap.Revisions {
		if r.contributor == nil {
			continue
		}
		if won := NewServiceKeyDomain(resolveDomain(r), r.Service); declared[won] {
			delete(snap.Revisions, k)
			if snap.supersededBy == nil {
				snap.supersededBy = map[string][]ServiceKey{}
			}
			if p := r.contributor.Producer; !slices.Contains(snap.supersededBy[p], won) {
				snap.supersededBy[p] = append(snap.supersededBy[p], won)
			}
		}
	}
	snap.Limitations = append(snap.Limitations, supersededLimitations(snap.supersededBy, func(ServiceKey) bool { return true })...)
}

// supersededLimitations is one CONTRIBUTION_SUPERSEDED per producer, counting
// the winning services visible says may be seen; none at zero.
func supersededLimitations(by map[string][]ServiceKey, visible func(ServiceKey) bool) []Limitation {
	var out []Limitation
	for _, p := range slices.Sorted(maps.Keys(by)) {
		if n := len(slices.DeleteFunc(slices.Clone(by[p]), func(k ServiceKey) bool { return !visible(k) })); n > 0 {
			out = append(out, Limitation{Code: LimitationContributionSuperseded, Source: "fleet",
				Message: supersededMessage(p, n)})
		}
	}
	return out
}

// serviceContribution is the contribution of a service every revision of
// which a producer contributed, from its newest one; nil for any other
// service. It is stale when its age is unknown, older than the freshness
// window (RecentEvidenceWindow when zero, as the producer row reads it) or a
// newer version was refused.
func serviceContribution(snap *FleetSnapshot, s *ServiceRecord) *ServiceContribution {
	var newest *ContractRevision
	for _, rk := range s.Revisions {
		r := snap.Revisions[rk]
		if r.contributor == nil {
			return nil
		}
		if newest == nil || LessRevisionChrono(newest, r) {
			newest = r
		}
	}
	if newest == nil {
		return nil
	}
	c := newest.contributor
	ref := contract.TrimOCIScheme(newest.ResolvedRef)
	repo, _, _ := contract.SplitOCIRef(ref)
	out := &ServiceContribution{Producer: c.Producer, Repository: repo, Ref: ref, KeyID: c.KeyID,
		ReadAt: optionalTime(c.ReadAt), AsOf: optionalTime(c.AsOf), NewerRefused: c.NewerRefused, Cluster: c.Cluster}
	out.Stale = c.AsOf.IsZero() || c.NewerRefused != "" || snap.GeneratedAt.Sub(c.AsOf) > cmp.Or(snap.provisioningWindow, RecentEvidenceWindow)
	return out
}

// optionalTime is t, or nil when it is zero.
func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// supersededMessage says how many services producer contributed have a contract.
func supersededMessage(producer string, n int) string {
	if n == 1 {
		return "1 service " + producer + " contributed has a contract; the contract wins"
	}
	return strconv.Itoa(n) + " services " + producer + " contributed have a contract; the contract wins"
}
