package fleet

import "slices"

// CreatablePath is a declared golden path a caller may create a service
// from: its digest-pinned reference, the signature of the revision that
// reference names and the offerings it requests.
type CreatablePath struct {
	Key       ServiceKey
	Name      string
	Version   string
	Ref       string
	Signature *BundleSignature
	Requests  []string
}

// The reasons CreatablePath gives. A key the caller's view hides reads as a
// missing one, so a restricted caller learns nothing about it.
const (
	noCreatablePath       = "no golden path with this key"
	inferredCreatablePath = "an inferred golden path is accepted first"
	unpinnedCreatablePath = "no digest-pinned reference: publish the golden path to create a service from it"
)

// CreatablePath is the declared golden path key names in this view, or why a
// service cannot be created from it. It reads only service records, so an
// inferred catalog template (never a service record) reads as missing, and it
// answers only a digest-pinned registry reference, so a caller never names
// what is pulled.
func (q *Query) CreatablePath(key ServiceKey) (CreatablePath, string) {
	s := q.snap.Services[key]
	if s == nil || !s.Template {
		return CreatablePath{}, noCreatablePath
	}
	if s.Inferred() {
		return CreatablePath{}, inferredCreatablePath
	}
	rev := newestReadable(serviceRevisions(q.snap, s))
	if rev == nil || pinnedRef(rev) == "" {
		return CreatablePath{}, unpinnedCreatablePath
	}
	sig := revisionSignature(rev)
	edges, _ := q.revisionTemplateEdges(rev)
	return CreatablePath{
		Key: key, Name: rev.Contract.Service.Name, Version: rev.Version, Ref: rev.ResolvedRef,
		Signature: &sig, Requests: requestedOfferings(q.templateRequirements(s, edges)),
	}, ""
}

// requestedOfferings are the distinct offering names uses name, sorted; a
// use of a provider that offers nothing names none.
func requestedOfferings(uses []OfferingUse) []string {
	out := []string{}
	for _, u := range uses {
		if u.Offering != "" && !slices.Contains(out, u.Offering) {
			out = append(out, u.Offering)
		}
	}
	slices.Sort(out)
	return out
}
