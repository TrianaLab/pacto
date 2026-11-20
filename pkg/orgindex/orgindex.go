// Package orgindex is the organization index: one write-once entry per
// published contract version under <registry>/pacto-index, so impact finds
// every declared consumer of a service without a hand-kept list. Entries are
// unsigned pointers; a reader validates each entry against its tag and index,
// confirms it against the service's own repository and pulls the bundle it
// names by digest.
package orgindex

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	// RepositoryName is the fixed index repository under a registry root
	// (#452 decision 11).
	RepositoryName = "pacto-index"
	// SchemaVersion names the entry format.
	SchemaVersion = "pacto.dev/index-entry/v1"
	// ArtifactType is the entry manifest's artifactType.
	ArtifactType = "application/vnd.pacto.index.entry.v1"
	// MediaType is the entry's single layer.
	MediaType = ArtifactType + "+json"
	// MaxEntryBytes bounds one entry layer.
	MaxEntryBytes = 64 << 10
	// MaxManifestBytes bounds one entry manifest.
	MaxManifestBytes = 4 << 10
	// PrefixLen is the length of the service-key hash in a tag.
	PrefixLen = 16
	// MaxTagLen is the longest tag OCI allows.
	MaxTagLen = 128
)

// Dependency is one dependency the published contract declares.
type Dependency struct {
	Name          string `json:"name"`
	Ref           string `json:"ref,omitempty"`
	Compatibility string `json:"compatibility,omitempty"`
}

// Publisher is who the publish claims signed the version: claimed and
// unverified, since the entry itself is unsigned. The bundle's signature,
// checked by digest, is the evidence. Readers never show it as a verified
// actor. Empty when the version was pushed unsigned.
type Publisher struct {
	Identity string `json:"identity,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	Key      string `json:"key,omitempty"`
}

// Entry is one published version in the index.
type Entry struct {
	SchemaVersion string `json:"schemaVersion"`
	// Service is the fleet service key: the escaped domain/name.
	Service string `json:"service"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Bundle is the published bundle as repo@sha256:<hex>.
	Bundle       string       `json:"bundle"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
	Publisher    Publisher    `json:"publisher"`
	// RegistryMode is the registry probe's mode where the bundle lives.
	RegistryMode string `json:"registryMode,omitempty"`
}

// NewEntry is the entry for c published as bundle (repo@digest) under key,
// with its dependencies sorted by name then ref.
func NewEntry(key fleet.ServiceKey, c *contract.Contract, bundle string, pub Publisher, mode string) Entry {
	e := Entry{SchemaVersion: SchemaVersion, Service: string(key), Name: c.Service.Name, Version: c.Service.Version,
		Bundle: bundle, Publisher: pub, RegistryMode: mode}
	for _, d := range c.Dependencies {
		e.Dependencies = append(e.Dependencies, Dependency{Name: d.Name, Ref: d.Ref, Compatibility: d.Compatibility})
	}
	slices.SortFunc(e.Dependencies, func(a, b Dependency) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Ref, b.Ref))
	})
	return e
}

// Encode is e's deterministic layer: no time, no annotation, so the same
// publish yields the same bytes.
func Encode(e Entry) ([]byte, error) {
	b, _ := json.Marshal(e) // fields are strings and slices of strings
	if len(b) > MaxEntryBytes {
		return nil, fmt.Errorf("the index entry is %d bytes, over the %d-byte bound", len(b), MaxEntryBytes)
	}
	return b, nil
}

// Prefix is the tag prefix of a service: the first PrefixLen hex of the
// sha256 of its key.
func Prefix(key fleet.ServiceKey) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:PrefixLen]
}

// Tag is the entry tag of key at version: <prefix>-<version>, with build
// metadata's "+" as "_" since OCI tags have no "+". A tag past MaxTagLen is
// refused, never truncated, so two versions never share one.
func Tag(key fleet.ServiceKey, version string) (string, error) {
	t := Prefix(key) + "-" + strings.ReplaceAll(version, "+", "_")
	if len(t) > MaxTagLen {
		return "", fmt.Errorf("the index tag for version %s is %d characters, over the %d OCI allows", version, len(t), MaxTagLen)
	}
	return t, nil
}

// ParseTag splits an entry tag into its prefix and version; ok is false for a
// tag outside the scheme.
func ParseTag(tag string) (prefix, version string, ok bool) {
	prefix, version, found := strings.Cut(tag, "-")
	if !found || len(prefix) != PrefixLen || version == "" || strings.Trim(prefix, "0123456789abcdef") != "" {
		return "", "", false
	}
	return prefix, strings.ReplaceAll(version, "_", "+"), true
}

// Validate binds e to where it was read: its bundle is pinned by digest on
// the index host under the index root, its service key is its bundle's
// domain and its name, and tag is that key's prefix and e's version. An
// entry that fails is never used, so it can neither send credentials to
// another host nor speak for another service.
func (e Entry) Validate(indexRepo, tag string) error {
	repo, digest, _ := strings.Cut(e.Bundle, "@")
	hexd, isSHA := strings.CutPrefix(digest, "sha256:")
	if !isSHA || len(hexd) != 64 || strings.Trim(hexd, "0123456789abcdef") != "" {
		return refused("the bundle is not pinned by a sha256 digest", fmt.Sprintf("the index entry's bundle %s is not pinned by a sha256 digest", e.Bundle))
	}
	host, _, _ := strings.Cut(repo, "/")
	indexHost, _, _ := strings.Cut(indexRepo, "/")
	root, inIndex := strings.CutSuffix(indexRepo, "/"+RepositoryName)
	switch {
	case host != indexHost:
		return refused("the bundle is on another host", fmt.Sprintf("the index entry's bundle %s is on %s, not the index host %s", repo, host, indexHost))
	case !inIndex || !strings.HasPrefix(repo, root+"/"):
		return refused("the bundle is outside the index root", fmt.Sprintf("the index entry's bundle %s is outside %s", repo, root))
	case string(fleet.NewServiceKeyDomain(contract.OCIDomain(repo), e.Name)) != e.Service:
		return refused("the service does not match its bundle and name", fmt.Sprintf("the index entry names service %s, but its bundle %s is %s", e.Service, repo, e.Name))
	}
	prefix, version, ok := ParseTag(tag)
	if !ok || prefix != Prefix(fleet.ServiceKey(e.Service)) || version != e.Version {
		return refused("the tag names another service or version", "the index entry for "+e.Service+" "+e.Version+" is under tag "+tag+", which names another service or version")
	}
	return nil
}

// refusal is an entry refused by Validate or Decode: its Error is the detail
// a writer prints about its own entry, its reason the fixed text a reader
// prints, since a reader never echoes the content of an entry it refused.
type refusal struct{ reason, detail string }

func (r *refusal) Error() string { return r.detail }

func refused(reason, detail string) error { return &refusal{reason: reason, detail: detail} }

// Reason is the fixed reason of a Validate or Decode refusal, which carries
// no entry content; "" for any other error.
func Reason(err error) string {
	var r *refusal
	if errors.As(err, &r) {
		return r.reason
	}
	return ""
}
