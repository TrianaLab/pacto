package fleet

import (
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Bounded projections of what a contract revision DECLARES.
//
// The product API previously reduced interfaces, configurations, policies and
// capabilities to four integers, which made a revision page a dead end: a user
// could see "3 interfaces" and never learn what they were. These types carry the
// declared content itself, bounded exactly like every other product preview.
//
// They project the declaration only. Anything that lives in the bundle FILES
// rather than in pacto.yaml (raw OpenAPI/AsyncAPI documents, JSON Schema bodies,
// documentation bodies, skill bodies, SBOM documents) is NOT retained by the
// snapshot and is therefore never claimed here: an interface reports the path it
// declares plus the operations that were actually derived from the bundle at
// build time, and says explicitly when those operations are unknown.

// InterfaceSummary is one declared interface plus the API operations derived
// from its referenced document at build time. OperationsKnown distinguishes "this
// interface has no operations" from "this interface's document was never read",
// so an empty Operations list is never presented as evidence of absence.
type InterfaceSummary struct {
	Name            string       `json:"name"`
	Type            string       `json:"type,omitempty"`
	Ref             string       `json:"ref,omitempty"`
	Visibility      string       `json:"visibility,omitempty"`
	OperationsKnown bool         `json:"operationsKnown"`
	Operations      ToolsPreview `json:"operations"`
}

// InterfacesPreview is a bounded preview of declared interfaces.
type InterfacesPreview struct {
	Total     int                `json:"total"`
	Count     int                `json:"count"`
	Truncated bool               `json:"truncated"`
	Items     []InterfaceSummary `json:"items"`
}

// ConfigurationSummary is one declared configuration scope. Values is the
// bounded flattening of the inline values map (the same bounded walk used for
// observed runtime facts), so an arbitrarily wide or deep values block can never
// make a detail response unbounded.
type ConfigurationSummary struct {
	Name     string         `json:"name"`
	Schema   string         `json:"schema,omitempty"`
	Ref      string         `json:"ref,omitempty"`
	Required bool           `json:"required"`
	Values   RuntimePreview `json:"values"`
}

// ConfigurationsPreview is a bounded preview of declared configuration scopes.
type ConfigurationsPreview struct {
	Total     int                    `json:"total"`
	Count     int                    `json:"count"`
	Truncated bool                   `json:"truncated"`
	Items     []ConfigurationSummary `json:"items"`
}

// PolicySummary is one declared policy: its identity, the schema it declares
// inline or the contract reference it resolves against, and its target.
type PolicySummary struct {
	Name   string `json:"name"`
	Schema string `json:"schema,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Target string `json:"target,omitempty"`
}

// PoliciesPreview is a bounded preview of declared policies.
type PoliciesPreview struct {
	Total     int             `json:"total"`
	Count     int             `json:"count"`
	Truncated bool            `json:"truncated"`
	Items     []PolicySummary `json:"items"`
}

// CapabilityBindingSummary is how a standard capability is reached: the binding
// protocol, the declared interface it binds to and the path on it.
type CapabilityBindingSummary struct {
	Type      string `json:"type,omitempty"`
	Interface string `json:"interface,omitempty"`
	Path      string `json:"path,omitempty"`
}

// CapabilitySummary is one declared capability (health, metrics or a namespaced
// extension) with its binding. The binding is the part the legacy dashboard DTO
// silently discarded.
type CapabilitySummary struct {
	Type    string                    `json:"type,omitempty"`
	Ref     string                    `json:"ref,omitempty"`
	Binding *CapabilityBindingSummary `json:"binding,omitempty"`
}

// CapabilitiesPreview is a bounded preview of declared capabilities.
type CapabilitiesPreview struct {
	Total     int                 `json:"total"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
	Items     []CapabilitySummary `json:"items"`
}

// StateSummary is the declared state block, flattened one level so a consumer
// does not have to know the nested persistence shape.
type StateSummary struct {
	Type                  string `json:"type,omitempty"`
	PersistenceScope      string `json:"persistenceScope,omitempty"`
	PersistenceDurability string `json:"persistenceDurability,omitempty"`
	DataCriticality       string `json:"dataCriticality,omitempty"`
}

// RevisionProvenance records where a revision's content came from and when it
// was fetched, so identity questions ("is this what the registry has?") can be
// answered on the revision page instead of only in the sources workspace.
type RevisionProvenance struct {
	Source    string         `json:"source,omitempty"`
	Sources   StringsPreview `json:"sources"`
	FetchedAt *time.Time     `json:"fetchedAt,omitempty"`
}

func interfacesPreview(ifaces []contract.Interface, tools []ToolSummary, specsRead []string) InterfacesPreview {
	read := make(map[string]bool, len(specsRead))
	for _, n := range specsRead {
		read[n] = true
	}
	byIface := map[string][]ToolSummary{}
	for _, t := range tools {
		byIface[t.Interface] = append(byIface[t.Interface], t)
	}
	src, total, trunc := boundSlice(ifaces, MaxDetailPreview)
	items := make([]InterfaceSummary, 0, len(src))
	for _, i := range src {
		items = append(items, InterfaceSummary{
			Name: i.Name, Type: i.Type, Ref: i.Ref, Visibility: i.Visibility,
			OperationsKnown: read[i.Name], Operations: toolsPreview(byIface[i.Name]),
		})
	}
	return InterfacesPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
}

func configurationsPreview(cfgs []contract.Configuration) ConfigurationsPreview {
	src, total, trunc := boundSlice(cfgs, MaxDetailPreview)
	items := make([]ConfigurationSummary, 0, len(src))
	for _, c := range src {
		items = append(items, ConfigurationSummary{
			Name: c.Name, Schema: c.Schema, Ref: c.Ref, Required: c.Required,
			Values: runtimePreview(c.Values),
		})
	}
	return ConfigurationsPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
}

func policiesPreview(ps []contract.Policy) PoliciesPreview {
	src, total, trunc := boundSlice(ps, MaxDetailPreview)
	items := make([]PolicySummary, 0, len(src))
	for _, p := range src {
		items = append(items, PolicySummary{Name: p.Name, Schema: p.Schema, Ref: p.Ref, Target: p.Target})
	}
	return PoliciesPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
}

func capabilitiesPreview(cs []contract.Capability) CapabilitiesPreview {
	src, total, trunc := boundSlice(cs, MaxDetailPreview)
	items := make([]CapabilitySummary, 0, len(src))
	for _, c := range src {
		item := CapabilitySummary{Type: c.Type, Ref: c.Ref}
		if c.Binding != nil {
			item.Binding = &CapabilityBindingSummary{
				Type: c.Binding.Type, Interface: c.Binding.Interface, Path: c.Binding.Path,
			}
		}
		items = append(items, item)
	}
	return CapabilitiesPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
}

func stateSummary(s *contract.State) *StateSummary {
	if s == nil {
		return nil
	}
	return &StateSummary{
		Type:                  s.Type,
		PersistenceScope:      s.Persistence.Scope,
		PersistenceDurability: s.Persistence.Durability,
		DataCriticality:       s.DataCriticality,
	}
}

func revisionProvenance(rev *ContractRevision) RevisionProvenance {
	return RevisionProvenance{
		Source: rev.Source, Sources: stringsPreview(rev.Sources), FetchedAt: rev.FetchedAt,
	}
}
