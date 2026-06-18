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
const CurrentLockVersion = 1

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

// Reference is one resolved config/policy reference. Config/policy sources carry
// no compatibility constraint (unlike dependencies), so a reference has no
// Constraint field.
type Reference struct {
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

// Parse decodes pacto.lock bytes and validates the schema version.
func Parse(data []byte) (*Lock, error) {
	var l Lock
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse pacto.lock: %w", err)
	}
	if l.LockVersion != CurrentLockVersion {
		return nil, fmt.Errorf("unsupported lockVersion %d (want %d)", l.LockVersion, CurrentLockVersion)
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

// Reference returns the reference with the given kind and name.
func (l *Lock) Reference(kind, name string) (*Reference, bool) {
	for i := range l.References {
		if l.References[i].Kind == kind && l.References[i].Name == name {
			return &l.References[i], true
		}
	}
	return nil, false
}
