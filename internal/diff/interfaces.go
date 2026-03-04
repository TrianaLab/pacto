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
			ct := Modified
			if oldIface.Port == nil {
				ct = Added
			} else if newIface.Port == nil {
				ct = Removed
			}
			changes = append(changes, newChange("interfaces.port", ct, intPtrVal(oldIface.Port), intPtrVal(newIface.Port)))
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
		changes = append(changes, newChange("configuration", Added, nil, new.Configuration.Schema))
		return changes
	}
	if new.Configuration == nil {
		changes = append(changes, newChange("configuration", Removed, old.Configuration.Schema, nil))
		return changes
	}

	if old.Configuration.Schema != new.Configuration.Schema {
		changes = append(changes, newChange("configuration.schema", Modified, old.Configuration.Schema, new.Configuration.Schema))
	}

	// Diff the JSON Schema files.
	oldSchema := old.Configuration.Schema
	newSchema := new.Configuration.Schema
	if oldSchema != "" && newSchema != "" {
		changes = append(changes, diffSchema(oldSchema, newSchema, oldFS, newFS)...)
	}

	return changes
}

func indexInterfaces(ifaces []contract.Interface) map[string]contract.Interface {
	m := make(map[string]contract.Interface, len(ifaces))
	for _, i := range ifaces {
		m[i.Name] = i
	}
	return m
}
