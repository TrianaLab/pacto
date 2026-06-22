package diff

import (
	"io/fs"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/validation"
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
	return diffRefSources("configurations",
		configRefSources(old.Configurations), configRefSources(new.Configurations), oldFS, newFS)
}

// refSource is the common shape of a configuration or policy source (a name with
// an optional schema and/or ref), used by the shared diff routine.
type refSource struct {
	name, schema, ref string
}

func configRefSources(cfgs []contract.ConfigurationSource) []refSource {
	out := make([]refSource, len(cfgs))
	for i, c := range cfgs {
		out[i] = refSource{name: c.Name, schema: c.Schema, ref: c.Ref}
	}
	return out
}

func policyRefSources(pols []contract.PolicySource) []refSource {
	out := make([]refSource, len(pols))
	for i, p := range pols {
		out[i] = refSource{name: p.Name, schema: p.Schema, ref: p.Ref}
	}
	return out
}

// diffRefSources diffs two sets of named schema/ref sources (configurations or
// policies) by name, including local schema-file content diffs. field is the
// change-path prefix ("configurations" or "policies").
func diffRefSources(field string, old, new []refSource, oldFS, newFS fs.FS) []Change {
	var changes []Change
	oldByName := indexRefSources(old)
	newByName := indexRefSources(new)

	for name, o := range oldByName {
		n, exists := newByName[name]
		if !exists {
			changes = append(changes, newChange(field, Removed, refSourceSummary(o), nil))
			continue
		}
		if o.schema != n.schema {
			changes = append(changes, newChange(field+".schema", Modified, name+": "+o.schema, name+": "+n.schema))
		}
		if o.ref != n.ref {
			ct := Modified
			if o.ref == "" {
				ct = Added
			} else if n.ref == "" {
				ct = Removed
			}
			changes = append(changes, newChange(field+".ref", ct, name+": "+o.ref, name+": "+n.ref))
		}
		// Diff schema file contents when both reference local schemas.
		if o.schema != "" && n.schema != "" {
			changes = append(changes, diffSchema(o.schema, n.schema, oldFS, newFS)...)
		}
	}

	for name, n := range newByName {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange(field, Added, nil, refSourceSummary(n)))
		}
	}
	return changes
}

func refSourceSummary(s refSource) string {
	if s.ref != "" {
		return s.name + ": " + s.ref
	}
	if s.schema != "" {
		return s.name + ": " + s.schema
	}
	return s.name
}

func indexRefSources(sources []refSource) map[string]refSource {
	m := make(map[string]refSource, len(sources))
	for _, s := range sources {
		m[s.name] = s
	}
	return m
}

// diffPolicy compares policies arrays by name.
func diffPolicy(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	changes := diffRefSources("policies",
		policyRefSources(old.Policies), policyRefSources(new.Policies), oldFS, newFS)

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

func indexInterfaces(ifaces []contract.Interface) map[string]contract.Interface {
	m := make(map[string]contract.Interface, len(ifaces))
	for _, i := range ifaces {
		m[i.Name] = i
	}
	return m
}
