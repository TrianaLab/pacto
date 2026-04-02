package diff

import (
	"fmt"
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

// diffConfiguration compares configuration objects and delegates to JSON Schema diff.
func diffConfiguration(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	var changes []Change

	oldCfg := old.Configuration
	newCfg := new.Configuration

	// Both nil — no changes
	if oldCfg == nil && newCfg == nil {
		return nil
	}

	// One nil — entire configuration added/removed
	if oldCfg == nil {
		changes = append(changes, newChange("configuration", Added, nil, configSummary(newCfg)))
		return changes
	}
	if newCfg == nil {
		changes = append(changes, newChange("configuration", Removed, configSummary(oldCfg), nil))
		return changes
	}

	// Compare legacy fields (schema, ref, values)
	if oldCfg.Schema != newCfg.Schema {
		changes = append(changes, newChange("configuration.schema", Modified, oldCfg.Schema, newCfg.Schema))
	}
	if oldCfg.Ref != newCfg.Ref {
		ct := Modified
		if oldCfg.Ref == "" {
			ct = Added
		} else if newCfg.Ref == "" {
			ct = Removed
		}
		changes = append(changes, newChange("configuration.ref", ct, oldCfg.Ref, newCfg.Ref))
	}

	// Diff schema files if both have legacy schema
	if oldCfg.Schema != "" && newCfg.Schema != "" {
		changes = append(changes, diffSchema(oldCfg.Schema, newCfg.Schema, oldFS, newFS)...)
	}

	// Compare configs array (positional)
	changes = append(changes, diffNamedConfigs(oldCfg.Configs, newCfg.Configs, oldFS, newFS)...)

	return changes
}

func configSummary(cfg *contract.Configuration) string {
	if cfg == nil {
		return ""
	}
	if cfg.Ref != "" {
		return cfg.Ref
	}
	if cfg.Schema != "" {
		return cfg.Schema
	}
	if len(cfg.Configs) > 0 {
		return fmt.Sprintf("%d configs", len(cfg.Configs))
	}
	return ""
}

func namedConfigSummary(cfg *contract.NamedConfigSource) string {
	if cfg.Ref != "" {
		return cfg.Name + ": " + cfg.Ref
	}
	return cfg.Name + ": " + cfg.Schema
}

// diffNamedConfigs compares the configs[] arrays positionally.
func diffNamedConfigs(oldConfigs, newConfigs []contract.NamedConfigSource, oldFS, newFS fs.FS) []Change {
	var changes []Change
	maxLen := len(oldConfigs)
	if len(newConfigs) > maxLen {
		maxLen = len(newConfigs)
	}
	for i := 0; i < maxLen; i++ {
		prefix := fmt.Sprintf("configuration.configs[%d]", i)
		if i >= len(oldConfigs) {
			changes = append(changes, newChange(prefix, Added, nil, namedConfigSummary(&newConfigs[i])))
			continue
		}
		if i >= len(newConfigs) {
			changes = append(changes, newChange(prefix, Removed, namedConfigSummary(&oldConfigs[i]), nil))
			continue
		}
		oldNamed := &oldConfigs[i]
		newNamed := &newConfigs[i]

		if oldNamed.Name != newNamed.Name {
			changes = append(changes, newChange(prefix+".name", Modified, oldNamed.Name, newNamed.Name))
		}
		if oldNamed.Schema != newNamed.Schema {
			changes = append(changes, newChange(prefix+".schema", Modified, oldNamed.Schema, newNamed.Schema))
		}
		if oldNamed.Ref != newNamed.Ref {
			ct := Modified
			if oldNamed.Ref == "" {
				ct = Added
			} else if newNamed.Ref == "" {
				ct = Removed
			}
			changes = append(changes, newChange(prefix+".ref", ct, oldNamed.Ref, newNamed.Ref))
		}
		if oldNamed.Schema != "" && newNamed.Schema != "" {
			changes = append(changes, diffSchema(oldNamed.Schema, newNamed.Schema, oldFS, newFS)...)
		}
	}
	return changes
}

// diffPolicy compares policies arrays.
func diffPolicy(old, new *contract.Contract) []Change {
	var changes []Change

	maxLen := len(old.Policies)
	if len(new.Policies) > maxLen {
		maxLen = len(new.Policies)
	}

	for i := 0; i < maxLen; i++ {
		prefix := fmt.Sprintf("policies[%d]", i)
		if i >= len(old.Policies) {
			changes = append(changes, newChange(prefix, Added, nil, policySummary(&new.Policies[i])))
			continue
		}
		if i >= len(new.Policies) {
			changes = append(changes, newChange(prefix, Removed, policySummary(&old.Policies[i]), nil))
			continue
		}
		oldPol := &old.Policies[i]
		newPol := &new.Policies[i]

		if oldPol.Schema != newPol.Schema {
			changes = append(changes, newChange(prefix+".schema", Modified, oldPol.Schema, newPol.Schema))
		}
		if oldPol.Ref != newPol.Ref {
			ct := Modified
			if oldPol.Ref == "" {
				ct = Added
			} else if newPol.Ref == "" {
				ct = Removed
			}
			changes = append(changes, newChange(prefix+".ref", ct, oldPol.Ref, newPol.Ref))
		}
	}

	return changes
}

func policySummary(p *contract.PolicySource) string {
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
