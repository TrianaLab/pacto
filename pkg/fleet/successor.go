package fleet

import (
	"fmt"
	"path"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// LimitationSuccessorUnresolved is raised for each provider whose declared
// successor names no revision this fleet holds, so no consumer's range can be
// checked against it.
const LimitationSuccessorUnresolved = "SUCCESSOR_UNRESOLVED"

// CodeSuccessorOutOfRange is the lifecycle attention code for a consumer whose
// range excludes its deprecated provider's successor.
const CodeSuccessorOutOfRange = "SUCCESSOR_OUT_OF_RANGE"

// SuccessorInfo is a provider's declared successor and the revision it
// resolves to. A successor resolves by name inside the provider's registry
// domain, as a dependency does, so a resolved one is inferred; one that does
// not resolve is unknown and Reason says why.
type SuccessorInfo struct {
	Ref        string     `json:"ref"`
	Revision   *EntityRef `json:"revision,omitempty"`
	Provenance string     `json:"provenance" enum:"inferred,unknown"`
	Reason     string     `json:"reason,omitempty"`
}

// resolveSuccessor resolves l's successor for provider; nil when l declares
// none.
func (q *Query) resolveSuccessor(provider *ServiceRecord, l *contract.InterfaceLifecycle) *SuccessorInfo {
	if l == nil || l.Successor == "" {
		return nil
	}
	rev, reason := q.successorRevision(provider, l.Successor)
	if rev == nil {
		return &SuccessorInfo{Ref: l.Successor, Provenance: provenanceUnknown, Reason: reason}
	}
	ref := revisionEntityRef(rev)
	return &SuccessorInfo{Ref: l.Successor, Revision: &ref, Provenance: ProvenanceInferred}
}

// successorRevision is the one revision the successor ref names: the service
// of its last path segment in provider's domain, at its digest when the ref
// pins one, else at its tag. Otherwise nil and the reason.
func (q *Query) successorRevision(provider *ServiceRecord, ref string) (*ContractRevision, string) {
	if !strings.HasPrefix(ref, contract.OCIScheme) {
		return nil, "it is not an oci:// reference"
	}
	repo, tag, digest := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	if tag == "" && digest == "" {
		return nil, "it names no version or digest"
	}
	name := path.Base(repo)
	sk := lookupServiceInDomain(q.snap, provider.Domain, name)
	if sk == "" {
		return nil, fmt.Sprintf("no service %s in %s's registry domain is in this fleet", name, provider.Name)
	}
	var match []*ContractRevision
	for _, rk := range q.snap.Services[sk].Revisions {
		if r := q.snap.Revisions[rk]; (digest != "" && r.Digest == digest) || (digest == "" && r.Version == tag) {
			match = append(match, r)
		}
	}
	switch len(match) {
	case 0:
		return nil, "no revision of " + name + " in this fleet matches it"
	case 1:
		return match[0], ""
	}
	return nil, fmt.Sprintf("%d revisions of %s match it", len(match), name)
}

// latestSupported is the newest readable revision of provider that declares no
// service lifecycle, nil when every one is deprecated or unreadable.
func (q *Query) latestSupported(provider *ServiceRecord) *ContractRevision {
	var revs []*ContractRevision
	for _, rk := range provider.Revisions {
		if r := q.snap.Revisions[rk]; r.Contract != nil && r.Contract.Service.Lifecycle == nil {
			revs = append(revs, r)
		}
	}
	return newestReadable(revs)
}

// successorRange checks a consumer's range against rev: unknown for no range
// or no revision, so an unresolved successor is never satisfied.
func successorRange(rng string, rev *ContractRevision) string {
	if rng == "" || rev == nil {
		return RangeUnknown
	}
	return CheckRange(rng, rev.Version)
}

// fillSuccessorRanges grades a use of a deprecated provider against the
// provider's successor and its newest supported revision.
func (q *Query) fillSuccessorRanges(u *OfferingUse) {
	if u.ProviderLifecycle == nil {
		return
	}
	provider := q.snap.Services[ServiceKey(u.Provider.Key)]
	var rev *ContractRevision
	if u.ProviderLifecycle.Successor != "" {
		rev, _ = q.successorRevision(provider, u.ProviderLifecycle.Successor)
	}
	u.SuccessorRange = successorRange(u.Compatibility, rev)
	if sup := q.latestSupported(provider); sup != nil {
		u.SupportedRange = successorRange(u.Compatibility, sup)
	}
}

// successorLimitations is one SUCCESSOR_UNRESOLVED limitation per provider
// whose successor does not resolve.
func successorLimitations(providers []OfferingProvider) []Limitation {
	var out []Limitation
	for _, p := range providers {
		if s := p.Successor; s != nil && s.Revision == nil {
			out = append(out, Limitation{Code: LimitationSuccessorUnresolved,
				Message: fmt.Sprintf("%s's successor %s resolves to no revision in this fleet (%s), so no consumer's range is checked against it", p.Service.Label, s.Ref, s.Reason)})
		}
	}
	return out
}

// Text renders s as "<ref> (<revision>)" when it resolves, else
// "<ref> (unknown: <reason>)".
func (s *SuccessorInfo) Text() string {
	if s.Revision != nil {
		return s.Ref + " (" + s.Revision.Label + ")"
	}
	return s.Ref + " (unknown: " + s.Reason + ")"
}
