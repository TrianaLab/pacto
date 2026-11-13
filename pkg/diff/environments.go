package diff

import (
	"io/fs"
	"reflect"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffEnvironments reports environments added and removed by name and, for an
// environment on both sides, every declared field that changed (file,
// lifetime, expires and owner, at environments[name].<field>) and every value
// that changed in its file, at environments[name].<configuration>.<path>.
// Both are NonBreaking: the declaration and the values are this service's own,
// and only the service reads them. A file that cannot be read on one side
// compares as empty.
func diffEnvironments(old, new []contract.Environment, oldFS, newFS fs.FS) []Change {
	var changes []Change
	for _, o := range old {
		i := slices.IndexFunc(new, func(n contract.Environment) bool { return n.Name == o.Name })
		if i < 0 {
			changes = append(changes, newChange("environments", Removed, o.Name, nil))
			continue
		}
		changes = append(changes, environmentFieldChanges(o, new[i])...)
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

// environmentFieldChanges is every declared field of one environment that
// changed between o and n; owner values are the owners' display strings.
func environmentFieldChanges(o, n contract.Environment) []Change {
	var changes []Change
	at := "environments[" + o.Name + "]."
	for _, f := range [][3]string{{"file", o.File, n.File}, {"lifetime", o.Lifetime, n.Lifetime}, {"expires", o.Expires, n.Expires}} {
		if f[1] != f[2] {
			changes = append(changes, newChange(at+f[0], strChangeType(f[1], f[2]), f[1], f[2]))
		}
	}
	if !reflect.DeepEqual(o.Owner, n.Owner) {
		changes = append(changes, newChange(at+"owner", ownerChangeType(o.Owner, n.Owner), ownerDisplay(o.Owner), ownerDisplay(n.Owner)))
	}
	return changes
}

// ownerChangeType is Added or Removed by nil, else Modified.
func ownerChangeType(o, n *contract.Owner) ChangeType {
	switch {
	case o == nil:
		return Added
	case n == nil:
		return Removed
	}
	return Modified
}

// ownerDisplay is o's display string, or nil when there is no owner.
func ownerDisplay(o *contract.Owner) any {
	if o == nil {
		return nil
	}
	return o.DisplayString()
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
