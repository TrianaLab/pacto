package fleet

import (
	"sort"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ProviderVersion is one contract revision of an offering provider: its
// version, whether a target runs it, the service lifecycle it declares and,
// when a range is given, whether its version satisfies the range.
type ProviderVersion struct {
	Revision EntityRef `json:"revision"`
	Version  string    `json:"version,omitempty"`
	// InUse says whether a target runs this revision. It is absent when the
	// caller's permissions hide the provider's targets, so "not in use" is never
	// derived from targets the caller cannot see.
	InUse         *bool                        `json:"inUse,omitempty"`
	Lifecycle     *contract.InterfaceLifecycle `json:"lifecycle,omitempty"`
	SunsetReached bool                         `json:"sunsetReached,omitempty"`
	// RangeStatus is present only when a range is given. It is unknown for a
	// version semver cannot read and for a revision that is invalid or unread.
	RangeStatus string `json:"rangeStatus,omitempty" enum:"satisfied,unsatisfied,unknown"`
}

// ProviderVersionsPreview is a bounded, newest-first preview of a provider's
// versions. LatestCompatible is the newest readable revision whose version
// satisfies Range. Completeness says where the list comes from: the contract
// revisions the fleet holds, which Note explains.
type ProviderVersionsPreview struct {
	Total            int               `json:"total"`
	Count            int               `json:"count"`
	Truncated        bool              `json:"truncated"`
	Items            []ProviderVersion `json:"items"`
	LatestCompatible *EntityRef        `json:"latestCompatible,omitempty"`
	Range            string            `json:"range,omitempty"`
	Completeness     string            `json:"completeness" enum:"fleet"`
	Note             string            `json:"note"`
}

// providerVersionsNote is ProviderVersionsPreview.Note.
const providerVersionsNote = "Versions come from the contract revisions this snapshot holds; the registry may publish others. " +
	"An OCI-sourced fleet lists what the registry holds, and pacto lock resolves a range against it."

// offeringProviderVersions lists s's revisions, newest first, checked against
// rng when it is set. rng must parse (see [Query.OfferingVersions]).
func (q *Query) offeringProviderVersions(s *ServiceRecord, rng string) ProviderVersionsPreview {
	running := map[RevisionKey]bool{}
	for _, tk := range s.Targets {
		running[q.snap.Targets[tk].ContractRevision] = true
	}
	revs := make([]*ContractRevision, 0, len(s.Revisions))
	for _, rk := range s.Revisions {
		revs = append(revs, q.snap.Revisions[rk])
	}
	sort.Slice(revs, func(i, j int) bool { return lessRevisionChrono(revs[j], revs[i]) })
	out := ProviderVersionsPreview{Range: rng, Completeness: "fleet", Note: providerVersionsNote}
	items := make([]ProviderVersion, 0, len(revs))
	for _, r := range revs {
		v := ProviderVersion{Revision: revisionEntityRef(r), Version: r.Version}
		if !q.snap.offeringOnly[s.Key] {
			inUse := running[r.Key]
			v.InUse = &inUse
		}
		if r.Contract != nil && r.Contract.Service.Lifecycle != nil {
			v.Lifecycle = r.Contract.Service.Lifecycle
			v.SunsetReached = v.Lifecycle.SunsetReached(q.snap.GeneratedAt)
		}
		if rng != "" {
			v.RangeStatus = RangeUnknown
			if readableRevision(r) {
				v.RangeStatus = CheckRange(rng, r.Version)
			}
			if v.RangeStatus == RangeSatisfied && out.LatestCompatible == nil {
				ref := v.Revision
				out.LatestCompatible = &ref
			}
		}
		items = append(items, v)
	}
	out.Items, out.Total, out.Truncated = boundSlice(items, MaxDetailPreview)
	out.Count = len(out.Items)
	return out
}

// OfferingProviderVersions is one provider's versions in an
// [OfferingVersionsResult].
type OfferingProviderVersions struct {
	Provider EntityRef               `json:"provider"`
	Versions ProviderVersionsPreview `json:"versions"`
}

// OfferingVersionsResult answers which versions each provider of an offering
// has and which satisfy a range. Providers is bounded like
// [OfferingDetailData.Providers].
type OfferingVersionsResult struct {
	Meta      ProductMeta                `json:"meta"`
	Offering  EntityRef                  `json:"offering"`
	Total     int                        `json:"total"`
	Truncated bool                       `json:"truncated"`
	Providers []OfferingProviderVersions `json:"providers"`
}

// OfferingVersions lists the versions of every provider of offering, or of the
// one provider (key or unique name) provider names, checked against rng when it
// is set. An unparsable range is an [InvalidQueryError]; an offering no service
// provides, or a provider outside it, is a [NotFoundError].
func (q *Query) OfferingVersions(offering, provider, rng string) (*OfferingVersionsResult, error) {
	if rng != "" {
		if _, err := contract.ParseRange(rng); err != nil {
			return nil, &InvalidQueryError{Field: "range", Value: rng, Reason: "not a semver range: " + err.Error()}
		}
	}
	var providers []*ServiceRecord
	for _, s := range q.snap.Services {
		if offering != "" && s.Offering == offering {
			providers = append(providers, s)
		}
	}
	if len(providers) == 0 {
		return nil, &NotFoundError{Kind: "offering", ID: offering}
	}
	if provider != "" {
		s, err := q.resolveService(provider)
		if err != nil {
			return nil, err
		}
		if s.Offering != offering {
			return nil, &NotFoundError{Kind: "provider of " + offering, ID: provider}
		}
		providers = []*ServiceRecord{s}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Key < providers[j].Key })
	shown, total, trunc := boundSlice(providers, MaxOfferingProviders)
	res := &OfferingVersionsResult{
		Meta: q.productMeta(), Offering: offeringEntityRef(offering, offeringProvenance(q.snap, offering)),
		Total: total, Truncated: trunc, Providers: make([]OfferingProviderVersions, 0, len(shown)),
	}
	for _, s := range shown {
		res.Providers = append(res.Providers, OfferingProviderVersions{Provider: serviceEntityRef(s), Versions: q.offeringProviderVersions(s, rng)})
	}
	// A deep copy severs the lifecycle pointers into the snapshot, as in EntityDetail.
	return jsonClone(res), nil
}
