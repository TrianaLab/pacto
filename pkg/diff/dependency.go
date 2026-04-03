package diff

import (
	"strconv"

	"github.com/trianalab/pacto/pkg/contract"
)

// diffDependencies compares dependency lists between old and new contracts by name.
func diffDependencies(old, new *contract.Contract) []Change {
	var changes []Change

	oldByName := indexDeps(old.Dependencies)
	newByName := indexDeps(new.Dependencies)

	// Removed or modified dependencies.
	for name, oldDep := range oldByName {
		newDep, exists := newByName[name]
		if !exists {
			changes = append(changes, newChange("dependencies", Removed, name, nil))
			continue
		}
		if oldDep.Ref != newDep.Ref {
			changes = append(changes, newChange("dependencies.ref", Modified,
				name+": "+oldDep.Ref, name+": "+newDep.Ref))
		}
		if oldDep.Compatibility != newDep.Compatibility {
			changes = append(changes, newChange("dependencies.compatibility", Modified,
				name+": "+oldDep.Compatibility, name+": "+newDep.Compatibility))
		}
		if oldDep.Required != newDep.Required {
			changes = append(changes, newChange("dependencies.required", Modified,
				name+": required="+strconv.FormatBool(oldDep.Required), name+": required="+strconv.FormatBool(newDep.Required)))
		}
	}

	// Added dependencies.
	for name := range newByName {
		if _, exists := oldByName[name]; !exists {
			changes = append(changes, newChange("dependencies", Added, nil, name))
		}
	}

	return changes
}

func indexDeps(deps []contract.Dependency) map[string]contract.Dependency {
	m := make(map[string]contract.Dependency, len(deps))
	for _, d := range deps {
		m[d.Name] = d
	}
	return m
}
