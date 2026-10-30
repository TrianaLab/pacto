package diff

import (
	"io/fs"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffEnvironments reports environments added and removed by name and, for an
// environment on both sides, every value that changed in its values file. A
// value change is NonBreaking: the values are this service's own deploy
// inputs, not surface its consumers read. A values file that cannot be read on
// one side compares as empty.
func diffEnvironments(old, new []contract.Environment, oldFS, newFS fs.FS) []Change {
	var changes []Change
	for _, o := range old {
		i := slices.IndexFunc(new, func(n contract.Environment) bool { return n.Name == o.Name })
		if i < 0 {
			changes = append(changes, newChange("environments", Removed, o.Name, nil))
			continue
		}
		changes = append(changes, diffConfigValues("environments", o.Name, environmentValues(oldFS, o.Values), environmentValues(newFS, new[i].Values))...)
	}
	for _, n := range new {
		if !slices.ContainsFunc(old, func(o contract.Environment) bool { return o.Name == n.Name }) {
			changes = append(changes, newChange("environments", Added, nil, n.Name))
		}
	}
	return changes
}

// environmentValues is the values file at path in fsys, or nil when it cannot
// be read.
func environmentValues(fsys fs.FS, path string) map[string]any {
	if fsys == nil {
		return nil
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	values, _ := contract.ParseEnvironmentValues(data)
	return values
}
