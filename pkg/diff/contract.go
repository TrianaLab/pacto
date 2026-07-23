package diff

import (
	"fmt"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

// diffContract compares root-level fields: service identity, workload, state, capabilities.
func diffContract(old, new *contract.Contract) []Change {
	var changes []Change

	// Schema version
	if old.PactoVersion != new.PactoVersion {
		changes = append(changes, newChange("pactoVersion", strChangeType(old.PactoVersion, new.PactoVersion), old.PactoVersion, new.PactoVersion))
	}

	// Service identity
	if old.Service.Name != new.Service.Name {
		changes = append(changes, newChange("service.name", Modified, old.Service.Name, new.Service.Name))
	}
	if old.Service.Version != new.Service.Version {
		changes = append(changes, newChange("service.version", Modified, old.Service.Version, new.Service.Version))
	}
	changes = append(changes, diffOwner(old.Service.Owner, new.Service.Owner)...)

	// Workload (top-level in v2)
	if old.Workload != new.Workload {
		changes = append(changes, newChange("workload", strChangeType(old.Workload, new.Workload), old.Workload, new.Workload))
	}

	// State (top-level in v2)
	changes = append(changes, diffState(old.State, new.State)...)

	// Capabilities
	changes = append(changes, diffCapabilities(old.Capabilities, new.Capabilities)...)

	return changes
}

// diffOwner emits granular per-subfield changes for the ownership block so that,
// e.g., adding a contact while the team is unchanged surfaces the contact change
// instead of an opaque "team -> team" modification.
func diffOwner(old, new contract.Owner) []Change {
	var changes []Change

	if old.Team != new.Team {
		changes = append(changes, newChange("service.owner.team", strChangeType(old.Team, new.Team), old.Team, new.Team))
	}
	if old.DRI != new.DRI {
		changes = append(changes, newChange("service.owner.dri", strChangeType(old.DRI, new.DRI), old.DRI, new.DRI))
	}
	changes = append(changes, diffContacts(old.Contacts, new.Contacts)...)

	return changes
}

// diffContacts compares ownership contacts keyed by type+value (a contact's
// natural identity); a purpose change on an existing contact is a modification.
func diffContacts(old, new []contract.OwnerContact) []Change {
	var changes []Change
	oldByKey := indexContacts(old)
	newByKey := indexContacts(new)

	for k, o := range oldByKey {
		n, exists := newByKey[k]
		if !exists {
			changes = append(changes, newChange(contactPath(k), Removed, formatContact(o), nil))
			continue
		}
		if o.Purpose != n.Purpose {
			changes = append(changes, newChange(contactPath(k), Modified, formatContact(o), formatContact(n)))
		}
	}
	for k, n := range newByKey {
		if _, exists := oldByKey[k]; !exists {
			changes = append(changes, newChange(contactPath(k), Added, nil, formatContact(n)))
		}
	}
	return changes
}

func indexContacts(contacts []contract.OwnerContact) map[string]contract.OwnerContact {
	m := make(map[string]contract.OwnerContact, len(contacts))
	for _, c := range contacts {
		m[c.Type+":"+c.Value] = c
	}
	return m
}

func contactPath(key string) string {
	return "service.owner.contacts[" + key + "]"
}

func formatContact(c contract.OwnerContact) string {
	if c.Purpose != "" {
		return c.Type + ":" + c.Value + " (" + c.Purpose + ")"
	}
	return c.Type + ":" + c.Value
}

func diffState(old, new *contract.State) []Change {
	var changes []Change

	if old == nil && new == nil {
		return nil
	}
	if old == nil {
		old = &contract.State{}
	}
	if new == nil {
		new = &contract.State{}
	}

	if old.Type != new.Type {
		changes = append(changes, newChange("state.type", strChangeType(old.Type, new.Type), old.Type, new.Type))
	}
	if old.Persistence.Scope != new.Persistence.Scope {
		changes = append(changes, newChange("state.persistence.scope", strChangeType(old.Persistence.Scope, new.Persistence.Scope), old.Persistence.Scope, new.Persistence.Scope))
	}
	if old.Persistence.Durability != new.Persistence.Durability {
		changes = append(changes, newChange("state.persistence.durability", strChangeType(old.Persistence.Durability, new.Persistence.Durability), old.Persistence.Durability, new.Persistence.Durability))
	}
	if old.DataCriticality != new.DataCriticality {
		changes = append(changes, newChange("state.dataCriticality", strChangeType(old.DataCriticality, new.DataCriticality), old.DataCriticality, new.DataCriticality))
	}

	return changes
}

func diffCapabilities(old, new []contract.Capability) []Change {
	var changes []Change
	oldByKey := indexCapabilities(old)
	newByKey := indexCapabilities(new)

	for key, o := range oldByKey {
		if _, exists := newByKey[key]; !exists {
			changes = append(changes, newChange("capabilities", Removed, formatCapability(o), nil))
		}
	}
	for key, n := range newByKey {
		if _, exists := oldByKey[key]; !exists {
			changes = append(changes, newChange("capabilities", Added, nil, formatCapability(n)))
		}
	}

	return changes
}

func indexCapabilities(caps []contract.Capability) map[string]contract.Capability {
	m := make(map[string]contract.Capability, len(caps))
	for _, c := range caps {
		m[capabilityKey(c)] = c
	}
	return m
}

func capabilityKey(c contract.Capability) string {
	if c.Ref != "" {
		return c.Type + ":" + c.Ref
	}
	return c.Type
}

func formatCapability(c contract.Capability) string {
	if c.Ref != "" {
		return c.Type + " (" + c.Ref + ")"
	}
	return c.Type
}

// newChange creates a Change with classification looked up from the rules table.
func newChange(path string, ct ChangeType, oldVal, newVal any) Change {
	cls := classify(path, ct)
	return Change{
		Path:           path,
		Type:           ct,
		OldValue:       oldVal,
		NewValue:       newVal,
		Classification: cls,
		Reason:         fmt.Sprintf("%s %s", path, ct),
	}
}

// diffStringSet compares two string-keyed boolean maps and emits Added/Removed
// changes. pathPrefix is used for the classification rule lookup (e.g.
// "openapi.paths"), and entityName for human-readable reasons (e.g. "API path").
func diffStringSet(oldSet, newSet map[string]bool, pathPrefix, entityName string) []Change {
	var changes []Change

	for key := range oldSet {
		if !newSet[key] {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("%s[%s]", pathPrefix, key),
				Type:           Removed,
				OldValue:       key,
				Classification: classify(pathPrefix, Removed),
				Reason:         fmt.Sprintf("%s %s removed", entityName, key),
			})
		}
	}

	for key := range newSet {
		if !oldSet[key] {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("%s[%s]", pathPrefix, key),
				Type:           Added,
				NewValue:       key,
				Classification: classify(pathPrefix, Added),
				Reason:         fmt.Sprintf("%s %s added", entityName, key),
			})
		}
	}

	return changes
}

// strChangeType classifies a string field change as Added (was empty), Removed
// (now empty), or Modified.
func strChangeType(old, new string) ChangeType {
	if old == "" {
		return Added
	}
	if new == "" {
		return Removed
	}
	return Modified
}
