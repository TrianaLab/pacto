package diff

import (
	"encoding/json"
	"io/fs"

	"github.com/trianalab/pacto/pkg/contract"
)

// Classification represents the severity of a change.
type Classification int

const (
	NonBreaking       Classification = iota // Consumers are not affected.
	PotentialBreaking                       // Consumers may be affected.
	Breaking                                // Consumers are definitely affected.
)

func (c Classification) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

func (c Classification) String() string {
	switch c {
	case NonBreaking:
		return "NON_BREAKING"
	case PotentialBreaking:
		return "POTENTIAL_BREAKING"
	case Breaking:
		return "BREAKING"
	default:
		return "UNKNOWN"
	}
}

// ChangeType describes how a field changed.
type ChangeType int

const (
	Added ChangeType = iota
	Removed
	Modified
)

func (t ChangeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t ChangeType) String() string {
	switch t {
	case Added:
		return "added"
	case Removed:
		return "removed"
	case Modified:
		return "modified"
	default:
		return "unknown"
	}
}

// Change represents a single detected change between two contracts.
type Change struct {
	Path           string         `json:"path"`
	Type           ChangeType     `json:"type"`
	OldValue       interface{}    `json:"oldValue,omitempty"`
	NewValue       interface{}    `json:"newValue,omitempty"`
	Classification Classification `json:"classification"`
	Reason         string         `json:"reason"`
}

// Result holds the output of comparing two contracts.
type Result struct {
	Classification Classification `json:"classification"`
	Changes        []Change       `json:"changes"`
}

// fileSetReader reads a file from an FS and returns a set of string keys.
type fileSetReader func(fsys fs.FS, path string) (map[string]bool, error)

// diffFileSet is a shared helper for diffOpenAPI and diffSchema.
// It reads old/new key sets using the provided reader, then delegates to diffStringSet.
func diffFileSet(oldPath, newPath string, oldFS, newFS fs.FS, reader fileSetReader, setName, itemLabel string) []Change {
	if oldFS == nil || newFS == nil || oldPath == "" || newPath == "" {
		return nil
	}

	oldSet, oldErr := reader(oldFS, oldPath)
	newSet, newErr := reader(newFS, newPath)

	if oldErr != nil && newErr != nil {
		return nil
	}
	if oldErr != nil {
		return nil
	}
	if newErr != nil {
		return nil
	}

	return diffStringSet(oldSet, newSet, setName, itemLabel)
}

// Compare compares two contracts and produces a classified diff result.
// oldFS and newFS provide access to referenced files (OpenAPI specs, JSON Schemas)
// within each contract's bundle. Either may be nil if file-level diffs are not needed.
func Compare(old, new *contract.Contract, oldFS, newFS fs.FS) *Result {
	var changes []Change

	changes = append(changes, diffContract(old, new)...)
	changes = append(changes, diffRuntime(old, new)...)
	changes = append(changes, diffDependencies(old, new)...)
	changes = append(changes, diffInterfaces(old, new, oldFS, newFS)...)
	changes = append(changes, diffConfiguration(old, new, oldFS, newFS)...)

	overall := NonBreaking
	for i := range changes {
		if changes[i].Classification > overall {
			overall = changes[i].Classification
		}
	}

	return &Result{
		Classification: overall,
		Changes:        changes,
	}
}
