package sbom

// ChangeType describes how an SBOM package changed.
type ChangeType string

const (
	PackageAdded    ChangeType = "added"
	PackageRemoved  ChangeType = "removed"
	PackageModified ChangeType = "modified"
)

// Change represents a single SBOM package change.
type Change struct {
	Package  string     `json:"package"`
	Type     ChangeType `json:"type"`
	Field    string     `json:"field,omitempty"`
	OldValue string     `json:"oldValue,omitempty"`
	NewValue string     `json:"newValue,omitempty"`
}

// Result holds the SBOM diff output.
type Result struct {
	OldFormat string   `json:"oldFormat,omitempty"`
	NewFormat string   `json:"newFormat,omitempty"`
	Changes   []Change `json:"changes"`
}

// Diff compares two SBOM documents and returns changes.
// Packages are matched by name. Returns nil if both documents are nil.
func Diff(old, new *Document) *Result {
	if old == nil && new == nil {
		return nil
	}

	result := &Result{}

	if old == nil {
		result.NewFormat = new.Format
		for _, p := range new.Packages {
			result.Changes = append(result.Changes, Change{
				Package:  p.Name,
				Type:     PackageAdded,
				Field:    "package",
				NewValue: p.Version,
			})
		}
		return result
	}

	if new == nil {
		result.OldFormat = old.Format
		for _, p := range old.Packages {
			result.Changes = append(result.Changes, Change{
				Package:  p.Name,
				Type:     PackageRemoved,
				Field:    "package",
				OldValue: p.Version,
			})
		}
		return result
	}

	result.OldFormat = old.Format
	result.NewFormat = new.Format

	oldByName := indexPackages(old.Packages)
	newByName := indexPackages(new.Packages)

	for name, oldPkg := range oldByName {
		newPkg, exists := newByName[name]
		if !exists {
			result.Changes = append(result.Changes, Change{
				Package:  name,
				Type:     PackageRemoved,
				Field:    "package",
				OldValue: oldPkg.Version,
			})
			continue
		}
		if oldPkg.Version != newPkg.Version {
			result.Changes = append(result.Changes, Change{
				Package:  name,
				Type:     PackageModified,
				Field:    "version",
				OldValue: oldPkg.Version,
				NewValue: newPkg.Version,
			})
		}
		if oldPkg.License != newPkg.License {
			result.Changes = append(result.Changes, Change{
				Package:  name,
				Type:     PackageModified,
				Field:    "license",
				OldValue: oldPkg.License,
				NewValue: newPkg.License,
			})
		}
	}

	for name, newPkg := range newByName {
		if _, exists := oldByName[name]; !exists {
			result.Changes = append(result.Changes, Change{
				Package:  name,
				Type:     PackageAdded,
				Field:    "package",
				NewValue: newPkg.Version,
			})
		}
	}

	return result
}

func indexPackages(pkgs []Package) map[string]Package {
	m := make(map[string]Package, len(pkgs))
	for _, p := range pkgs {
		m[p.Name] = p
	}
	return m
}
