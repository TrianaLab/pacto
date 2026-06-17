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
	LockVersion  int         `yaml:"lockVersion"`
	Pacto        PactoInfo   `yaml:"pacto"`
	Root         RootInfo    `yaml:"root"`
	Dependencies []Entry     `yaml:"dependencies,omitempty"`
	References   []Reference `yaml:"references,omitempty"`
}

// PactoInfo records the CLI version that produced the lock.
type PactoInfo struct {
	Version string `yaml:"version"`
}

// RootInfo identifies the contract the lock belongs to.
type RootInfo struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Entry is one resolved dependency in the closure.
type Entry struct {
	Name        string   `yaml:"name"`
	Source      string   `yaml:"source"` // "oci" or "local"
	Ref         string   `yaml:"ref,omitempty"`
	Path        string   `yaml:"path,omitempty"`
	Constraint  string   `yaml:"constraint,omitempty"`
	Version     string   `yaml:"version,omitempty"`
	Digest      string   `yaml:"digest,omitempty"`
	ContentHash string   `yaml:"contentHash,omitempty"`
	DependsOn   []string `yaml:"dependsOn,omitempty"`
}

// Reference is one resolved config/policy reference.
type Reference struct {
	Kind        string `yaml:"kind"` // "config" or "policy"
	Name        string `yaml:"name"`
	Source      string `yaml:"source"`
	Ref         string `yaml:"ref,omitempty"`
	Path        string `yaml:"path,omitempty"`
	Constraint  string `yaml:"constraint,omitempty"`
	Version     string `yaml:"version,omitempty"`
	Digest      string `yaml:"digest,omitempty"`
	ContentHash string `yaml:"contentHash,omitempty"`
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
		if out.References[i].Kind != out.References[j].Kind {
			return out.References[i].Kind < out.References[j].Kind
		}
		return out.References[i].Name < out.References[j].Name
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
