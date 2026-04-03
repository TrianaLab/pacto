package diff

import (
	"io/fs"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/validation"
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

// diffConfiguration compares configuration slices by name and delegates to JSON Schema diff.
func diffConfiguration(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	var changes []Change

	oldByName := indexConfigurations(old.Configurations)
	newByName := indexConfigurations(new.Configurations)

	for name, oldCfg := range oldByName {
		newCfg, exists := newByName[name]
		if !exists {
			changes = append(changes, newChange("configurations", Removed, configSummary(&oldCfg), nil))
			continue
		}

		if oldCfg.Schema != newCfg.Schema {
			changes = append(changes, newChange("configurations.schema", Modified, name+": "+oldCfg.Schema, name+": "+newCfg.Schema))
		}
		if oldCfg.Ref != newCfg.Ref {
			ct := Modified
			if oldCfg.Ref == "" {
				ct = Added
			} else if newCfg.Ref == "" {
				ct = Removed
			}
			changes = append(changes, newChange("configurations.ref", ct, name+": "+oldCfg.Ref, name+": "+newCfg.Ref))
		}

		// Diff schema file contents when both reference local schemas.
		if oldCfg.Schema != "" && newCfg.Schema != "" {
			changes = append(changes, diffSchema(oldCfg.Schema, newCfg.Schema, oldFS, newFS)...)
		}
	}

	for name, newCfg := range newByName {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange("configurations", Added, nil, configSummary(&newCfg)))
		}
	}

	return changes
}

func configSummary(cfg *contract.ConfigurationSource) string {
	if cfg == nil {
		return ""
	}
	if cfg.Ref != "" {
		return cfg.Name + ": " + cfg.Ref
	}
	if cfg.Schema != "" {
		return cfg.Name + ": " + cfg.Schema
	}
	return cfg.Name
}

func indexConfigurations(cfgs []contract.ConfigurationSource) map[string]contract.ConfigurationSource {
	m := make(map[string]contract.ConfigurationSource, len(cfgs))
	for _, c := range cfgs {
		m[c.Name] = c
	}
	return m
}

// diffPolicy compares policies arrays by name.
func diffPolicy(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	var changes []Change

	oldByName := indexPolicies(old.Policies)
	newByName := indexPolicies(new.Policies)

	for name, oldPol := range oldByName {
		newPol, exists := newByName[name]
		if !exists {
			changes = append(changes, newChange("policies", Removed, policySummary(&oldPol), nil))
			continue
		}

		if oldPol.Schema != newPol.Schema {
			changes = append(changes, newChange("policies.schema", Modified, name+": "+oldPol.Schema, name+": "+newPol.Schema))
		}
		if oldPol.Ref != newPol.Ref {
			ct := Modified
			if oldPol.Ref == "" {
				ct = Added
			} else if newPol.Ref == "" {
				ct = Removed
			}
			changes = append(changes, newChange("policies.ref", ct, name+": "+oldPol.Ref, name+": "+newPol.Ref))
		}

		// Diff schema file contents when both policies reference local schemas.
		if oldPol.Schema != "" && newPol.Schema != "" {
			changes = append(changes, diffSchema(oldPol.Schema, newPol.Schema, oldFS, newFS)...)
		}
	}

	for name, newPol := range newByName {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange("policies", Added, nil, policySummary(&newPol)))
		}
	}

	// Auto-detect: compare policy/schema.json when bundles ship it but
	// the contract has no policies field (policy-provider bundles).
	if len(old.Policies) == 0 && len(new.Policies) == 0 {
		changes = append(changes, diffPolicySchemaFile(oldFS, newFS)...)
	}

	return changes
}

// diffPolicySchemaFile compares policy/schema.json between two bundles
// that ship the file but don't declare policies in the contract.
func diffPolicySchemaFile(oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil {
		return nil
	}
	oldExists := fileExists(oldFS, validation.PolicySchemaPath)
	newExists := fileExists(newFS, validation.PolicySchemaPath)

	if !oldExists && !newExists {
		return nil
	}
	if !oldExists && newExists {
		return []Change{newChange(validation.PolicySchemaPath, Added, nil, validation.PolicySchemaPath)}
	}
	if oldExists && !newExists {
		return []Change{newChange(validation.PolicySchemaPath, Removed, validation.PolicySchemaPath, nil)}
	}
	return diffSchema(validation.PolicySchemaPath, validation.PolicySchemaPath, oldFS, newFS)
}

func fileExists(fsys fs.FS, path string) bool {
	_, err := fs.Stat(fsys, path)
	return err == nil
}

func policySummary(p *contract.PolicySource) string {
	if p.Ref != "" {
		return p.Name + ": " + p.Ref
	}
	if p.Schema != "" {
		return p.Name + ": " + p.Schema
	}
	return p.Name
}

func indexPolicies(policies []contract.PolicySource) map[string]contract.PolicySource {
	m := make(map[string]contract.PolicySource, len(policies))
	for _, p := range policies {
		m[p.Name] = p
	}
	return m
}

func indexInterfaces(ifaces []contract.Interface) map[string]contract.Interface {
	m := make(map[string]contract.Interface, len(ifaces))
	for _, i := range ifaces {
		m[i.Name] = i
	}
	return m
}
