package diff

import (
	"io/fs"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffEnvironments reports environments added and removed by name and, for an
// environment on both sides, every value that changed in its file, at
// environments[name].<configuration>.<path>. A value change is NonBreaking:
// the values are this service's own inputs, and only the service reads them.
// A file that cannot be read on one side compares as empty.
func diffEnvironments(old, new []contract.Environment, oldFS, newFS fs.FS) []Change {
	var changes []Change
	for _, o := range old {
		i := slices.IndexFunc(new, func(n contract.Environment) bool { return n.Name == o.Name })
		if i < 0 {
			changes = append(changes, newChange("environments", Removed, o.Name, nil))
			continue
		}
		valueChanges := diffJSON(dirUnknown, nil, "environments["+o.Name+"]", environmentValues(oldFS, o.File), environmentValues(newFS, new[i].File))
		for j := range valueChanges {
			valueChanges[j].Classification = NonBreaking
		}
		changes = append(changes, valueChanges...)
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
