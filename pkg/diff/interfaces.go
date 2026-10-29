package diff

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// diffInterfaces compares interface lists and delegates to OpenAPI diff
// for openapi-type interfaces that reference spec files.
func diffInterfaces(old, new *contract.Contract, oldFS, newFS fs.FS) []Change {
	var changes []Change

	oldByName := indexInterfaces(old.Interfaces)
	newByName := indexInterfaces(new.Interfaces)

	for _, name := range slices.Sorted(maps.Keys(oldByName)) {
		oldIface := oldByName[name]
		newIface, exists := newByName[name]
		if !exists {
			changes = append(changes, removedInterface(oldIface))
			continue
		}
		changes = append(changes, diffLifecycle(name, oldIface.Lifecycle, newIface.Lifecycle)...)

		if oldIface.Type != newIface.Type {
			changes = append(changes, newChange("interfaces.type", Modified, name+": "+oldIface.Type, name+": "+newIface.Type))
		}
		if oldIface.Visibility != newIface.Visibility {
			changes = append(changes, newChange("interfaces.visibility", Modified, name+": "+oldIface.Visibility, name+": "+newIface.Visibility))
		}

		changes = append(changes, diffGuarantees(name, oldIface.Guarantees, newIface.Guarantees)...)
		changes = append(changes, diffEpoch(name, oldIface.CompatibilityEpoch, newIface.CompatibilityEpoch)...)

		// Diff spec ref (renamed from Contract in v1)
		if oldIface.Ref != newIface.Ref {
			changes = append(changes, newChange("interfaces.ref", Modified, name+": "+oldIface.Ref, name+": "+newIface.Ref))
		}

		// Diff referenced spec content when both sides are the same interface type
		// and both have refs (regardless of ref equality — a spec can be rewritten
		// behind an unchanged ref).
		if oldIface.Type == newIface.Type && oldIface.Ref != "" && newIface.Ref != "" {
			switch oldIface.Type {
			case contract.InterfaceTypeOpenAPI:
				changes = append(changes, diffOpenAPI(oldIface.Ref, newIface.Ref, oldFS, newFS)...)
			case contract.InterfaceTypeAsyncAPI:
				changes = append(changes, diffAsyncAPI(oldIface.Ref, newIface.Ref, oldFS, newFS)...)
			case contract.InterfaceTypeGRPC:
				changes = append(changes, diffGRPC(oldIface.Ref, newIface.Ref, oldFS, newFS)...)
			case contract.InterfaceTypeMCP:
				changes = append(changes, diffMCP(oldIface.Ref, newIface.Ref, oldFS, newFS)...)
			}
		}
	}

	for _, name := range slices.Sorted(maps.Keys(newByName)) {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange("interfaces", Added, nil, name))
		}
	}

	return changes
}

// removedInterface is the Breaking removal of iface. Its reason says so when
// the interface is removed before the sunset it announced.
func removedInterface(iface contract.Interface) Change {
	c := newChange("interfaces", Removed, iface.Name, nil)
	if l := iface.Lifecycle; l != nil && l.Sunset != "" && !l.SunsetReached(now()) {
		c.Reason += fmt.Sprintf(": %s removed before its sunset %s, so consumers still inside the announced window break", iface.Name, l.Sunset)
	}
	return c
}

// now is the clock that decides whether a removed interface's sunset has been
// reached. Only the reason text reads it; no classification does.
var now = time.Now

// diffLifecycle compares the lifecycle of one interface. Deprecating, moving the
// deprecation date, adding or dropping a sunset and naming a successor are
// notices that change nothing a consumer calls, so they are NonBreaking. An
// earlier sunset shortens the window consumers were promised to migrate in and
// is PotentialBreaking; a later one only extends it.
func diffLifecycle(name string, o, n *contract.InterfaceLifecycle) []Change {
	switch {
	case o == nil && n == nil:
		return nil
	case o == nil:
		c := newChange("interfaces.lifecycle", Added, nil, name+": deprecated "+n.Deprecated)
		c.Reason += ": " + name + " " + n.String()
		return []Change{c}
	case n == nil:
		return []Change{newChange("interfaces.lifecycle", Removed, name+": deprecated "+o.Deprecated, nil)}
	}
	var changes []Change
	if o.Deprecated != n.Deprecated {
		changes = append(changes, newChange("interfaces.lifecycle.deprecated", Modified, name+": "+o.Deprecated, name+": "+n.Deprecated))
	}
	if o.Sunset != n.Sunset {
		c := newChange("interfaces.lifecycle.sunset", strChangeType(o.Sunset, n.Sunset), name+": "+o.Sunset, name+": "+n.Sunset)
		if c.Type == Modified {
			direction := "earlier"
			if n.Sunset > o.Sunset { // canonical dates compare as strings
				direction, c.Classification = "later", NonBreaking
			}
			c.Reason += fmt.Sprintf(": %s sunset moved %s, from %s to %s", name, direction, o.Sunset, n.Sunset)
		}
		changes = append(changes, c)
	}
	if o.Successor != n.Successor {
		changes = append(changes, newChange("interfaces.lifecycle.successor", strChangeType(o.Successor, n.Successor), name+": "+o.Successor, name+": "+n.Successor))
	}
	return changes
}

// diffEpoch reports a changed compatibilityEpoch on interface name. Either
// direction is Breaking (the rule table says why); the reason says which.
func diffEpoch(name string, o, n int) []Change {
	if o == n {
		return nil
	}
	c := newChange("interfaces.compatibilityEpoch", Modified, fmt.Sprintf("%s: %d", name, o), fmt.Sprintf("%s: %d", name, n))
	c.Reason = fmt.Sprintf("interface %s: compatibilityEpoch raised from %d to %d: its behavior changed incompatibly, so every consumer must review the change", name, o, n)
	if n < o {
		c.Reason = fmt.Sprintf("interface %s: compatibilityEpoch lowered from %d to %d: an epoch only goes up, and consumers that adapted to the higher one break", name, o, n)
	}
	return []Change{c}
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

	for _, name := range slices.Sorted(maps.Keys(oldByName)) {
		o := oldByName[name]
		n, exists := newByName[name]
		if !exists {
			changes = append(changes, newChange(field, Removed, refSourceSummary(o), nil))
			continue
		}
		// Both arms go through strChangeType so the empty-string transition is
		// classified as what it is. A source that DROPS its schema is a removal,
		// not a modification, and only the removal row reaches Breaking.
		if o.schema != n.schema {
			changes = append(changes, newChange(field+".schema", strChangeType(o.schema, n.schema), name+": "+o.schema, name+": "+n.schema))
		}
		if o.ref != n.ref {
			changes = append(changes, newChange(field+".ref", strChangeType(o.ref, n.ref), name+": "+o.ref, name+": "+n.ref))
		}
		// Diff schema file contents when both reference local schemas.
		if o.schema != "" && n.schema != "" {
			changes = append(changes, diffSchema(o.schema, n.schema, oldFS, newFS)...)
		}
		// Diff inline configuration values (provider defaults; never consumer-facing).
		changes = append(changes, diffConfigValues(field, name, o.values, n.values)...)
	}

	for _, name := range slices.Sorted(maps.Keys(newByName)) {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange(field, Added, nil, refSourceSummary(newByName[name])))
		}
	}
	return changes
}

// diffConfigValues diffs two inline configuration value maps, reusing the JSON
// structural diff but reclassifying every change as NonBreaking: values are the
// provider's own defaults, not part of the consumer-facing contract surface.
func diffConfigValues(field, name string, old, new map[string]any) []Change {
	changes := diffJSON(dirUnknown, field+"["+name+"].values", old, new)
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
