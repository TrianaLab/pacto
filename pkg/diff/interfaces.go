package diff

import (
	"io/fs"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/validation"
)

// diffInterfaces compares interface lists and delegates to OpenAPI diff
// for openapi-type interfaces that reference spec files.
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
		if oldIface.Visibility != newIface.Visibility {
			changes = append(changes, newChange("interfaces.visibility", Modified, name+": "+oldIface.Visibility, name+": "+newIface.Visibility))
		}

		// Diff spec ref (renamed from Contract in v1)
		if oldIface.Ref != newIface.Ref {
			changes = append(changes, newChange("interfaces.ref", Modified, name+": "+oldIface.Ref, name+": "+newIface.Ref))
		}

		// Diff OpenAPI spec content if both are openapi type and both have refs (regardless of ref equality)
		if oldIface.Type == contract.InterfaceTypeOpenAPI && newIface.Type == contract.InterfaceTypeOpenAPI &&
			oldIface.Ref != "" && newIface.Ref != "" {
			changes = append(changes, diffOpenAPI(oldIface.Ref, newIface.Ref, oldFS, newFS)...)
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
// an optional schema and/or ref), used by the shared diff routine. values holds a
// configuration's inline values (nil for policies).
type refSource struct {
	name, schema, ref string
	values            map[string]any
}

func configRefSources(cfgs []contract.Configuration) []refSource {
	out := make([]refSource, len(cfgs))
	for i, c := range cfgs {
		out[i] = refSource{name: c.Name, schema: c.Schema, ref: c.Ref, values: c.Values}
	}
	return out
}

func policyRefSources(pols []contract.Policy) []refSource {
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
		// Diff inline configuration values (provider defaults; never consumer-facing).
		changes = append(changes, diffConfigValues(field, name, o.values, n.values)...)
	}

	for name, n := range newByName {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange(field, Added, nil, refSourceSummary(n)))
		}
	}
	return changes
}

// diffConfigValues diffs two inline configuration value maps, reusing the JSON
// structural diff but reclassifying every change as NonBreaking: values are the
// provider's own defaults, not part of the consumer-facing contract surface.
func diffConfigValues(field, name string, old, new map[string]any) []Change {
	changes := diffJSON(field+"["+name+"].values", old, new)
	for i := range changes {
		changes[i].Classification = NonBreaking
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
