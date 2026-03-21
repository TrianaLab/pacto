package diff

import (
	"io/fs"

	"github.com/trianalab/pacto/pkg/contract"
)

// diffInterfaces compares interface lists and delegates to OpenAPI diff
// for interfaces that reference contract files.
func diffInterfaces(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	var changes []Change

	oldByName := indexInterfaces(old.Interfaces)
	newByName := indexInterfaces(new.Interfaces)

	for name, oldIface := range oldByName {
		newIface, exists := newByName[name]
		if !exists {
			changes = append(changes, newChange("interfaces", Removed, name, nil))
			continue
		}

		if oldIface.Type != newIface.Type {
			changes = append(changes, newChange("interfaces.type", Modified, name+": "+oldIface.Type, name+": "+newIface.Type))
		}
		if intPtrChanged(oldIface.Port, newIface.Port) {
			changes = append(changes, newChange("interfaces.port", intPtrChangeType(oldIface.Port, newIface.Port), intPtrVal(oldIface.Port), intPtrVal(newIface.Port)))
		}
		if oldIface.Visibility != newIface.Visibility {
			changes = append(changes, newChange("interfaces.visibility", Modified, name+": "+oldIface.Visibility, name+": "+newIface.Visibility))
		}

		// Diff OpenAPI contract files if both reference one.
		if oldIface.Contract != "" && newIface.Contract != "" {
			if oldIface.Contract != newIface.Contract {
				changes = append(changes, newChange("interfaces.contract", Modified, name+": "+oldIface.Contract, name+": "+newIface.Contract))
			}
			changes = append(changes, diffOpenAPI(oldIface.Contract, newIface.Contract, oldFS, newFS)...)
		} else if oldIface.Contract != newIface.Contract {
			changes = append(changes, newChange("interfaces.contract", Modified, oldIface.Contract, newIface.Contract))
		}
	}

	for name := range newByName {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange("interfaces", Added, nil, name))
		}
	}

	return changes
}

// diffConfiguration compares configuration fields and delegates to JSON Schema diff.
func diffConfiguration(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	var changes []Change

	if old.Configuration == nil && new.Configuration == nil {
		return nil
	}
	if old.Configuration == nil {
		changes = append(changes, newChange("configuration", Added, nil, configSummary(new.Configuration)))
		return changes
	}
	if new.Configuration == nil {
		changes = append(changes, newChange("configuration", Removed, configSummary(old.Configuration), nil))
		return changes
	}

	if old.Configuration.Schema != new.Configuration.Schema {
		changes = append(changes, newChange("configuration.schema", Modified, old.Configuration.Schema, new.Configuration.Schema))
	}

	if old.Configuration.Ref != new.Configuration.Ref {
		ct := Modified
		if old.Configuration.Ref == "" {
			ct = Added
		} else if new.Configuration.Ref == "" {
			ct = Removed
		}
		changes = append(changes, newChange("configuration.ref", ct, old.Configuration.Ref, new.Configuration.Ref))
	}

	// Diff the JSON Schema files.
	oldSchema := old.Configuration.Schema
	newSchema := new.Configuration.Schema
	if oldSchema != "" && newSchema != "" {
		changes = append(changes, diffSchema(oldSchema, newSchema, oldFS, newFS)...)
	}

	return changes
}

func configSummary(cfg *contract.Configuration) string {
	if cfg.Ref != "" {
		return cfg.Ref
	}
	return cfg.Schema
}

// diffPolicy compares policy fields.
func diffPolicy(old, new *contract.Contract) []Change {
	var changes []Change

	if old.Policy == nil && new.Policy == nil {
		return nil
	}
	if old.Policy == nil {
		changes = append(changes, newChange("policy", Added, nil, policySummary(new.Policy)))
		return changes
	}
	if new.Policy == nil {
		changes = append(changes, newChange("policy", Removed, policySummary(old.Policy), nil))
		return changes
	}

	if old.Policy.Schema != new.Policy.Schema {
		changes = append(changes, newChange("policy.schema", Modified, old.Policy.Schema, new.Policy.Schema))
	}
	if old.Policy.Ref != new.Policy.Ref {
		changes = append(changes, newChange("policy.ref", Modified, old.Policy.Ref, new.Policy.Ref))
	}

	return changes
}

func policySummary(p *contract.Policy) string {
	if p.Ref != "" {
		return p.Ref
	}
	return p.Schema
}

func indexInterfaces(ifaces []contract.Interface) map[string]contract.Interface {
	m := make(map[string]contract.Interface, len(ifaces))
	for _, i := range ifaces {
		m[i.Name] = i
	}
	return m
}
