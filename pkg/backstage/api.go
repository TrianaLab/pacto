package backstage

import (
	"cmp"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// apiID is one interface of one service.
type apiID struct {
	service fleet.ServiceKey
	iface   string
}

func apiRef(name string) string { return "api:default/" + name }

// readable is r's contract, nil when r or its contract is missing.
func readable(r *fleet.ContractRevision) *contract.Contract {
	if r == nil {
		return nil
	}
	return r.Contract
}

// declares reports whether c declares an interface named name.
func declares(c *contract.Contract, name string) bool {
	return c != nil && slices.ContainsFunc(c.Interfaces, func(i contract.Interface) bool { return i.Name == name })
}

// currentContract is s's current revision's contract (see
// [fleet.Query.ProviderCurrentRevision]), nil when it is unreadable.
func (e *exporter) currentContract(s fleet.ServiceKey) *contract.Contract {
	return readable(e.q.ProviderCurrentRevision(s))
}

// consumedAPIs are the interfaces rel, an exported edge, consumes: those a
// declared, resolved dependency requires and the provider revision it resolves
// to declares. That revision is the one the edge names, else the provider's
// current one; an interface it does not declare is left out (the fleet reports
// it as unmet). An interface the provider's current revision does not declare
// is left out too: only the current revision's interfaces become API entities,
// and a ref to an entity the file does not hold would show as a missing
// relation in Backstage (spec.dependsOn still names the provider).
func (e *exporter) consumedAPIs(rel *fleet.Relationship) []apiID {
	if !fleet.ConsumesEdge(*rel) || rel.Type != fleet.RelationshipDependency || len(rel.Requires) == 0 {
		return nil
	}
	current := e.currentContract(rel.ToService)
	provider := current
	if rel.ResolvedRevision != "" {
		provider = readable(e.snap.Revisions[rel.ResolvedRevision])
	}
	var out []apiID
	for _, req := range rel.Requires {
		if declares(provider, req.Interface) && declares(current, req.Interface) {
			out = append(out, apiID{rel.ToService, req.Interface})
		}
	}
	return out
}

// apiNames names every API the export holds: each interface of each service's
// current revision (a consumed interface is always one of them, see
// consumedAPIs), named `<component>-<interface>` and told apart by service key
// and interface.
func (e *exporter) apiNames(services []*fleet.ServiceRecord) map[apiID]string {
	var ids []apiID
	for _, s := range services {
		if c := e.currentContract(s.Key); c != nil {
			for _, i := range c.Interfaces {
				ids = append(ids, apiID{s.Key, i.Name})
			}
		}
	}
	slices.SortFunc(ids, func(a, b apiID) int { return cmp.Or(cmp.Compare(a.service, b.service), cmp.Compare(a.iface, b.iface)) })
	reqs := make([]nameRequest, len(ids))
	for i, id := range ids {
		reqs[i] = nameRequest{base: e.components[id.service] + "-" + id.iface, identity: string(id.service) + "#" + id.iface}
	}
	out := make(map[apiID]string, len(ids))
	for i, n := range assignNames(reqs, sha256Hex) {
		out[ids[i]] = n
	}
	return out
}

// ownerAnnotations are the Component annotations an API copies with its owner.
var ownerAnnotations = []string{AnnotationOwnerProvenance, AnnotationOwnerConflict, AnnotationOwner, AnnotationOwnerUnmapped}

// apiEntities is one API per interface of s's current revision, none when it
// is unreadable. Owner, its annotations and system are the Component's; an interface's own
// deprecation or its Component's makes it deprecated.
func (e *exporter) apiEntities(s *fleet.ServiceRecord, comp Entity) []Entity {
	r := e.q.ProviderCurrentRevision(s.Key)
	c := readable(r)
	if c == nil {
		return nil
	}
	in := cmp.Or(r.ResolvedRef, string(r.Key))
	out := make([]Entity, 0, len(c.Interfaces))
	for _, i := range c.Interfaces {
		a := entity(kindAPI, e.base.annotations(provenanceDeclared))
		a.Metadata.Annotations[AnnotationServiceKey] = string(s.Key)
		a.Metadata.Name = e.apis[apiID{s.Key, i.Name}]
		a.Spec.Type = i.Type
		a.Spec.Owner = comp.Spec.Owner
		// The owner annotations say why spec.owner reads as it does.
		for _, k := range ownerAnnotations {
			if v, ok := comp.Metadata.Annotations[k]; ok {
				a.Metadata.Annotations[k] = v
			}
		}
		a.Spec.System = comp.Spec.System
		a.Spec.Lifecycle = comp.Spec.Lifecycle
		if i.Lifecycle != nil && i.Lifecycle.Deprecated != "" {
			a.Spec.Lifecycle = "deprecated"
		}
		a.Spec.Definition = "Defined by " + i.Ref + " in " + in + ". Read it with pacto pull."
		out = append(out, a)
	}
	return out
}
