// Package lock models pacto.lock: the committed, deterministic record of the
// resolved dependency + reference closure.
package lock

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// FileName is the lockfile name, alongside pacto.yaml.
const FileName = "pacto.lock"

// CurrentLockVersion is the schema version this build writes.
//
// 2 added Reference.From, the occurrence identity of the contract that declared
// a reference. Version 1 locks still parse, but carry no occurrence identity, so
// nothing in them can be attributed to a particular declared reference.
const CurrentLockVersion = 2

// MinLockVersion is the oldest schema version this build can still read.
const MinLockVersion = 1

// OccurrenceLockVersion is the first schema version that records which contract
// declared each reference (Reference.From). Below it, a reference lookup cannot
// distinguish a contract's own reference from a transitive namesake.
const OccurrenceLockVersion = 2

// Lock is the root document of pacto.lock.
type Lock struct {
	LockVersion  int         `yaml:"lockVersion" json:"lockVersion"`
	Pacto        PactoInfo   `yaml:"pacto" json:"pacto"`
	Root         RootInfo    `yaml:"root" json:"root"`
	Dependencies []Entry     `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	References   []Reference `yaml:"references,omitempty" json:"references,omitempty"`
}

// PactoInfo records the CLI version that produced the lock.
type PactoInfo struct {
	Version string `yaml:"version" json:"version"`
}

// RootInfo identifies the contract the lock belongs to.
type RootInfo struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
}

// Entry is one resolved dependency in the closure.
type Entry struct {
	Name        string   `yaml:"name" json:"name"`
	Source      string   `yaml:"source" json:"source"` // "oci" or "local"
	Ref         string   `yaml:"ref,omitempty" json:"ref,omitempty"`
	Path        string   `yaml:"path,omitempty" json:"path,omitempty"`
	Constraint  string   `yaml:"constraint,omitempty" json:"constraint,omitempty"`
	Version     string   `yaml:"version,omitempty" json:"version,omitempty"`
	Digest      string   `yaml:"digest,omitempty" json:"digest,omitempty"`
	ContentHash string   `yaml:"contentHash,omitempty" json:"contentHash,omitempty"`
	DependsOn   []string `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
}

// Reference is one resolved config/policy reference OCCURRENCE. Config/policy
// sources carry no compatibility constraint (unlike dependencies), so a
// reference has no Constraint field.
//
// The lock holds the TRANSITIVE closure, so (Kind, Name) is a label, not an
// identity: a configuration scope called "settings" declared by the root
// contract and another called "settings" declared by a bundle the root reached
// through some other reference are two different references that both resolved
// to something authoritative. From is what tells them apart.
type Reference struct {
	// From is the closure path of the contract that DECLARED this reference:
	// "" for the root contract, otherwise the ReferencePath of the reference
	// occurrence through which the declaring bundle was reached (for example
	// "config:foo" or "config:foo/policy:limits"). Together with Kind and Name
	// it identifies exactly one declared reference occurrence.
	From        string `yaml:"from,omitempty" json:"from,omitempty"`
	Kind        string `yaml:"kind" json:"kind"` // "config" or "policy"
	Name        string `yaml:"name" json:"name"`
	Source      string `yaml:"source" json:"source"`
	Ref         string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Path        string `yaml:"path,omitempty" json:"path,omitempty"`
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`
	Digest      string `yaml:"digest,omitempty" json:"digest,omitempty"`
	ContentHash string `yaml:"contentHash,omitempty" json:"contentHash,omitempty"`
}

// Marshal serializes the lock deterministically (sorted entries, sorted edges).
func (l *Lock) Marshal() ([]byte, error) {
	out := *l
	out.LockVersion = CurrentLockVersion
	out.Dependencies = append([]Entry(nil), l.Dependencies...)
	out.References = append([]Reference(nil), l.References...)
	sort.Slice(out.Dependencies, func(i, j int) bool { return out.Dependencies[i].Name < out.Dependencies[j].Name })
	for i := range out.Dependencies {
		d := append([]string(nil), out.Dependencies[i].DependsOn...)
		sort.Strings(d)
		out.Dependencies[i].DependsOn = d
	}
	sort.Slice(out.References, func(i, j int) bool {
		a, b := out.References[i], out.References[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Path < b.Path
	})
	return yaml.Marshal(&out)
}

// Parse decodes pacto.lock bytes and validates the schema version. Older
// readable versions are accepted as-is: LockVersion keeps the value the file
// declared, so callers can see what the lock does and does not record rather
// than reinterpreting an old lock under new semantics.
func Parse(data []byte) (*Lock, error) {
	var l Lock
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse pacto.lock: %w", err)
	}
	if l.LockVersion < MinLockVersion || l.LockVersion > CurrentLockVersion {
		return nil, fmt.Errorf("unsupported lockVersion %d (want %d..%d)", l.LockVersion, MinLockVersion, CurrentLockVersion)
	}
	return &l, nil
}

// Dependency returns the entry with the given name.
func (l *Lock) Dependency(name string) (*Entry, bool) {
	for i := range l.Dependencies {
		if l.Dependencies[i].Name == name {
			return &l.Dependencies[i], true
		}
	}
	return nil, false
}

// ReferencePath is the closure path of a reference occurrence: the path of the
// contract that declared it, then this occurrence's own kind and name. The root
// contract's path is "".
func ReferencePath(from, kind, name string) string {
	seg := kind + ":" + name
	if from == "" {
		return seg
	}
	return from + "/" + seg
}

// RootReference returns the entry recording what the ROOT contract's own
// declared (kind, name) reference resolved to.
//
// It deliberately does NOT fall back to a transitive namesake. The lock holds
// the whole closure, so a matching (kind, name) declared by some other contract
// in it is a real, authoritative resolution OF A DIFFERENT REFERENCE; projecting
// it onto this one produces a confident link to a bundle the root never
// referenced. Two entries claiming the same occurrence contradict each other,
// and a lock written before occurrence identity existed records nothing that can
// answer at all. Both return false: unknown beats a plausible wrong link.
func (l *Lock) RootReference(kind, name string) (*Reference, bool) {
	if l.LockVersion < OccurrenceLockVersion {
		return nil, false
	}
	var found *Reference
	for i := range l.References {
		r := &l.References[i]
		if r.From != "" || r.Kind != kind || r.Name != name {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = r
	}
	return found, found != nil
}
