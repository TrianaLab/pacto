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
// 2 added Reference.From, naming the contract that declared each reference by
// the closure PATH through which it was reached ("config:foo/policy:limits").
// That path was built by joining names with "/" and ":", and configuration and
// policy names accept any non-empty string — so a scope legitimately named
// "a/policy:b" produced the same path as a policy "b" declared by the bundle
// reached through a scope named "a". The encoding was therefore not injective
// over the contracts the schema accepts, and a v2 lock could file two different
// transitive references under one identity.
//
// 3 replaces that path with the declaring contract's own CONTENT identity, which
// is never assembled out of user-controlled text. See Reference.From.
const CurrentLockVersion = 3

// MinLockVersion is the oldest schema version this build can still read.
const MinLockVersion = 1

// RootOccurrenceLockVersion is the first schema version from which a reference
// declared by the ROOT contract can be told apart from a transitive namesake.
//
// It is 2, not CurrentLockVersion, and that is deliberate. The root's own
// entries are the one part of a v2 lock the delimiter flaw cannot reach: v2 wrote
// From == "" for the root and a non-empty path for everything else, so "the root
// declared this" stayed decidable whatever a name contained. Only TRANSITIVE
// attribution was unsound in v2, and no reader may rely on it.
const RootOccurrenceLockVersion = 2

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

// Reference is one resolved config/policy DECLARATION. Config/policy sources
// carry no compatibility constraint (unlike dependencies), so a reference has no
// Constraint field.
//
// The lock holds the TRANSITIVE closure, so (Kind, Name) is a label, not an
// identity: a configuration scope called "settings" declared by the root
// contract and another called "settings" declared by a bundle the root reached
// through some other reference are two different references that both resolved
// to something authoritative. From is what tells them apart.
//
// Three things are deliberately kept apart here:
//
//   - The DECLARING CONTRACT is From: which immutable bundle contains the
//     declaration.
//   - The DECLARATION is (From, Kind, Name), i.e. Occurrence. A declaration
//     exists once, inside one immutable contract; the lock holds one entry per
//     declaration, never one per route taken to it.
//   - TRAVERSAL PROVENANCE — the routes by which the root reaches a declaration —
//     is not a field. Every entry's From equals some other entry's DestinationID,
//     so the entries are a DAG rooted at the root contract and every route is
//     derivable from the set. Recording one chosen route instead would privilege
//     whichever the walk happened to take first.
type Reference struct {
	// From is the CONTENT IDENTITY of the contract that declared this reference:
	// "" for the root contract, otherwise "oci:<digest>" or "local:<contentHash>"
	// — the same string DestinationID returns for the entry that reached it.
	//
	// It is never assembled out of names. Kind and Name are separate fields, so
	// no delimiter has to survive a name containing one, and a content identity
	// is a hash the author cannot choose. (From, Kind, Name) is therefore
	// injective over every contract the schema accepts, which the joined closure
	// path of lockVersion 2 was not.
	//
	// Because it is content-addressed it is also route-independent: a bundle
	// reachable by two paths has one identity, so its declarations land in the
	// same place whichever path the walk took first.
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

// Occurrence is the identity of one declared reference: the contract that
// declared it, plus the kind and name it was declared under.
//
// It is a comparable struct rather than a string on purpose. Names accept any
// non-empty value, so any encoding that joins them with delimiters can be forged
// by a legal name that contains the delimiters; keeping the parts as separate
// fields makes the identity injective by construction and usable directly as a
// map key.
type Occurrence struct {
	From string
	Kind string
	Name string
}

// String renders an occurrence for a human reader — error messages, diagnostics.
// It is NOT an identity: names are arbitrary text, so two occurrences can render
// alike. Use the struct itself as the key.
func (o Occurrence) String() string {
	who := "the root contract"
	if o.From != "" {
		who = o.From
	}
	return fmt.Sprintf("%s %q declared by %s", o.Kind, o.Name, who)
}

// Occurrence returns this entry's declaration identity.
func (r Reference) Occurrence() Occurrence {
	return Occurrence{From: r.From, Kind: r.Kind, Name: r.Name}
}

// DestinationID is the content identity of the bundle this reference resolved
// to, in the same namespace as From. An entry whose From equals another entry's
// DestinationID was declared by that entry's target: this is the edge that makes
// the reference set a graph, and it is how traversal provenance is recovered
// without storing any route.
//
// It is "" only for an entry that pinned nothing, which the closure builder
// refuses to produce.
func (r Reference) DestinationID() string {
	switch r.Source {
	case "local":
		if r.ContentHash != "" {
			return "local:" + r.ContentHash
		}
	case "oci":
		if r.Digest != "" {
			return "oci:" + r.Digest
		}
	}
	return ""
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
	if l.LockVersion < RootOccurrenceLockVersion {
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
