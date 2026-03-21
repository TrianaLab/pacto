package diff

import (
	"strconv"

	"github.com/trianalab/pacto/pkg/contract"
)

// diffDependencies compares dependency lists between old and new contracts.
func diffDependencies(old, new *contract.Contract) []Change {
	var changes []Change

	oldByRef := indexDeps(old.Dependencies)
	newByRef := indexDeps(new.Dependencies)

	// Removed or modified dependencies.
	for ref, oldDep := range oldByRef {
		newDep, exists := newByRef[ref]
		if !exists {
			changes = append(changes, newChange("dependencies", Removed, ref, nil))
			continue
		}
		if oldDep.Compatibility != newDep.Compatibility {
			changes = append(changes, newChange("dependencies.compatibility", Modified,
				ref+": "+oldDep.Compatibility, ref+": "+newDep.Compatibility))
		}
		if oldDep.Required != newDep.Required {
			changes = append(changes, newChange("dependencies.required", Modified,
				ref+": required="+strconv.FormatBool(oldDep.Required), ref+": required="+strconv.FormatBool(newDep.Required)))
		}
	}

	// Added dependencies.
	for ref := range newByRef {
		if _, exists := oldByRef[ref]; !exists {
			changes = append(changes, newChange("dependencies", Added, nil, ref))
		}
	}

	return changes
}

func indexDeps(deps []contract.Dependency) map[string]contract.Dependency {
	m := make(map[string]contract.Dependency, len(deps))
	for _, d := range deps {
		m[d.Ref] = d
	}
	return m
}
