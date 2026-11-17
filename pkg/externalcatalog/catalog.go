// Package externalcatalog classifies the systems outside the fleet. A catalog
// maps the names, hosts, SDK modules, configuration key names and OpenTelemetry
// system values a service uses to one canonical [externalid.ID] and a class.
//
// Pacto embeds a default catalog and publishes the same bytes as a signed OCI
// artifact; an organization may add an extension, pinned by digest, whose
// entries override the default ones by identity.
package externalcatalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/externalid"
	"gopkg.in/yaml.v3"
)

// APIVersion is the only catalog format this package reads.
const APIVersion = "catalog.pacto.trianalab.io/v1"

// The classes a catalog entry may carry.
const (
	ClassVendorAPI    = "vendor-api"
	ClassManagedCloud = "managed-cloud"
	ClassLegacy       = "legacy"
	ClassUnclassified = "unclassified"
)

// Where a catalog came from.
const (
	OriginDefault      = "default"
	OriginOrganization = "organization"
)

// Bounds on one catalog file.
const (
	MaxEntries   = 2000
	MaxFileBytes = 1 << 20
)

// File is one parsed catalog file.
type File struct {
	APIVersion string  `yaml:"apiVersion"`
	Entries    []Entry `yaml:"entries"`
}

// Entry classifies one external system.
type Entry struct {
	Identity    externalid.ID `yaml:"identity"`
	Class       string        `yaml:"class"`
	Label       string        `yaml:"label,omitempty"`
	Aliases     []string      `yaml:"aliases,omitempty"`
	Hosts       []string      `yaml:"hosts,omitempty"`
	SDKModules  []string      `yaml:"sdkModules,omitempty"`
	KeyPatterns []string      `yaml:"keyPatterns,omitempty"`
	OTel        OTel          `yaml:"otel,omitempty"`
}

// OTel lists the OpenTelemetry system attribute values that name the entry.
type OTel struct {
	DBSystem        []string `yaml:"dbSystem,omitempty"`
	MessagingSystem []string `yaml:"messagingSystem,omitempty"`
	RPCSystem       []string `yaml:"rpcSystem,omitempty"`
}

// Ref records one catalog a [Catalog] was built from: where it came from, its
// digest and what is known about its signature.
type Ref struct {
	Origin     string `json:"origin"`
	Repository string `json:"repository,omitempty"`
	Digest     string `json:"digest"`
	// Copy is "registry" when the bytes in use are the registry artifact at
	// the recorded digest: pulled (an organization catalog) or the embedded
	// copy matched against the signed artifact (a verified default). It is
	// "embedded" when the compiled-in copy is used unchecked.
	Copy string `json:"copy"`
	// Signature is verified, unverified, unsigned or unknown.
	Signature string `json:"signature"`
	Signer    string `json:"signer,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Catalog is the merged set of entries a fleet build classifies with.
type Catalog struct {
	entries map[externalid.ID]sourced
	refs    []Ref
	// aliases maps a lower-cased alias to the entry that claims it.
	aliases map[string]externalid.ID
}

type sourced struct {
	entry Entry
	ref   int // index into refs
}

// ParseFile decodes one catalog file strictly: unknown keys are errors.
func ParseFile(b []byte) (*File, error) {
	if len(b) > MaxFileBytes {
		return nil, fmt.Errorf("external catalog: %d bytes, over the %d-byte limit", len(b), MaxFileBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("external catalog: empty file")
		}
		return nil, fmt.Errorf("external catalog: %w", err)
	}
	return &f, nil
}

var (
	hostPatternRE = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	keyPatternRE  = regexp.MustCompile(`^[A-Z0-9_]+\*?$`)
	classes       = []string{ClassVendorAPI, ClassManagedCloud, ClassLegacy, ClassUnclassified}
)

// claims tracks which entry of one file claimed each alias, host or key pattern.
type claims map[string]int

func (c claims) claim(errs *[]error, field, value string, i int) {
	key := foldKey(value)
	if j, ok := c[key]; ok {
		*errs = append(*errs, fmt.Errorf("entries[%d].%s: %q is already claimed by entries[%d]", i, field, value, j))
		return
	}
	c[key] = i
}

// Validate checks every rule of the format and joins every violation.
func (f *File) Validate() error {
	var errs []error
	if f.APIVersion != APIVersion {
		errs = append(errs, fmt.Errorf("apiVersion: %q, want %q", f.APIVersion, APIVersion))
	}
	if len(f.Entries) > MaxEntries {
		errs = append(errs, fmt.Errorf("entries: %d, over the limit of %d", len(f.Entries), MaxEntries))
	}
	ids := map[externalid.ID]int{}
	aliases, hosts, keys := claims{}, claims{}, claims{}
	for i, e := range f.Entries {
		errs = append(errs, validateEntry(i, e)...)
		if j, ok := ids[e.Identity]; ok {
			errs = append(errs, fmt.Errorf("entries[%d].identity: %q duplicates entries[%d]", i, e.Identity, j))
		}
		ids[e.Identity] = i
		for _, a := range e.Aliases {
			aliases.claim(&errs, "aliases", a, i)
		}
		for _, h := range e.Hosts {
			hosts.claim(&errs, "hosts", h, i)
		}
		for _, k := range e.KeyPatterns {
			keys.claim(&errs, "keyPatterns", k, i)
		}
	}
	return errors.Join(errs...)
}

// validateEntry checks the rules local to one entry.
func validateEntry(i int, e Entry) []error {
	var errs []error
	id, err := externalid.Parse(string(e.Identity))
	if err != nil {
		errs = append(errs, fmt.Errorf("entries[%d].identity: %w", i, err))
	}
	if !slices.Contains(classes, e.Class) {
		errs = append(errs, fmt.Errorf("entries[%d].class: %q, want one of %s", i, e.Class, strings.Join(classes, ", ")))
	}
	if err == nil && e.Class == ClassManagedCloud && len(id.Segments()) != 2 {
		errs = append(errs, fmt.Errorf("entries[%d].identity: a managed-cloud entry names exactly <provider>/<service>, got %q", i, e.Identity))
	}
	for _, a := range e.Aliases {
		if strings.TrimSpace(a) == "" {
			errs = append(errs, fmt.Errorf("entries[%d].aliases: empty alias", i))
		}
	}
	for _, h := range e.Hosts {
		if !hostPatternRE.MatchString(h) {
			errs = append(errs, fmt.Errorf("entries[%d].hosts: %q must be a lower-case DNS name with an optional leading \"*.\"", i, h))
		}
	}
	for _, k := range e.KeyPatterns {
		if !keyPatternRE.MatchString(k) {
			errs = append(errs, fmt.Errorf("entries[%d].keyPatterns: %q must be upper-case letters, digits or '_' with at most one trailing '*'", i, k))
		}
	}
	return errs
}

// Merge builds the catalog from the default file and an optional organization
// extension. An organization entry replaces the default entry with the same
// identity, and an alias, host or key pattern the organization claims is taken
// away from every default entry that also claims it.
func Merge(def *File, defRef Ref, org *File, orgRef *Ref) *Catalog {
	c := &Catalog{entries: map[externalid.ID]sourced{}, refs: []Ref{defRef}, aliases: map[string]externalid.ID{}}
	taken := struct{ aliases, hosts, keys map[string]bool }{map[string]bool{}, map[string]bool{}, map[string]bool{}}
	if org != nil {
		c.refs = append(c.refs, *orgRef)
		for _, e := range org.Entries {
			c.add(e, 1)
			mark(taken.aliases, e.Aliases)
			mark(taken.hosts, e.Hosts)
			mark(taken.keys, e.KeyPatterns)
		}
	}
	for _, e := range def.Entries {
		if _, replaced := c.entries[e.Identity]; replaced {
			continue
		}
		e.Aliases = without(e.Aliases, taken.aliases)
		e.Hosts = without(e.Hosts, taken.hosts)
		e.KeyPatterns = without(e.KeyPatterns, taken.keys)
		c.add(e, 0)
	}
	return c
}

// add records e, supplied by refs[ref], and indexes its aliases. Organization
// entries are added first and a default entry has already lost every alias the
// organization claims, so an alias is never indexed twice.
func (c *Catalog) add(e Entry, ref int) {
	c.entries[e.Identity] = sourced{entry: e, ref: ref}
	for _, a := range e.Aliases {
		c.aliases[foldKey(a)] = e.Identity
	}
}

// foldKey is how an alias, host or key pattern is compared everywhere:
// without case or surrounding spaces, so a claim, its index and a lookup
// always agree.
func foldKey(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

func mark(set map[string]bool, values []string) {
	for _, v := range values {
		set[foldKey(v)] = true
	}
}

func without(values []string, taken map[string]bool) []string {
	return slices.DeleteFunc(slices.Clone(values), func(v string) bool { return taken[foldKey(v)] })
}

// Refs returns a copy of the catalogs this catalog was built from, the default
// first.
func (c *Catalog) Refs() []Ref { return slices.Clone(c.refs) }

// Entry returns the entry for id and the catalog that supplied it. A managed
// resource ("aws/rds/123456789012/eu-west-1/orders-db") with no entry of its
// own takes the managed-cloud entry of its provider and service ("aws/rds").
func (c *Catalog) Entry(id externalid.ID) (Entry, Ref, bool) {
	s, ok := c.entries[id]
	if !ok {
		segs := id.Segments()
		if len(segs) != 5 {
			return Entry{}, Ref{}, false
		}
		if s, ok = c.entries[externalid.ID(segs[0]+"/"+segs[1])]; !ok || s.entry.Class != ClassManagedCloud {
			return Entry{}, Ref{}, false
		}
	}
	return s.entry, c.refs[s.ref], true
}

// Len returns the number of entries.
func (c *Catalog) Len() int { return len(c.entries) }

// ResolveDeclared returns the entry whose alias equals name, compared without
// case and surrounding spaces, and the catalog that supplied it. An
// organization alias wins over a default one (see [Merge]).
func (c *Catalog) ResolveDeclared(name string) (Entry, Ref, bool) {
	id, ok := c.aliases[foldKey(name)]
	if !ok {
		return Entry{}, Ref{}, false
	}
	return c.Entry(id)
}

// How [DeclaredIdentity] named a declared external dependency.
const (
	// MatchIdentity is a dependency that names its identity itself
	// (dependencies[].identity, pactoVersion 2.5).
	MatchIdentity       = "identity"
	MatchCatalogAlias   = "catalog-alias"
	MatchHost           = "host"
	MatchNormalizedName = "normalized-name"
)

// DeclaredIdentity names the external system a declared dependency with no
// contract reference calls: the identity it declares when that parses, else
// the catalog entry one of whose aliases is the name or its normalized form
// ("Stripe API" is the alias "stripe-api"), else the registrable
// domain when the name is a host name, else the normalized name. It returns
// how it matched, and the [externalid.Name] error when the name cannot be
// normalized (empty, too long or reserved). Validation refuses an identity
// that does not parse, so the name fallback only serves a contract read
// without it.
func DeclaredIdentity(c *Catalog, identity, name string) (externalid.ID, string, error) {
	if id, err := externalid.Parse(identity); identity != "" && err == nil {
		return id, MatchIdentity, nil
	}
	if e, _, ok := c.ResolveDeclared(name); ok {
		return e.Identity, MatchCatalogAlias, nil
	}
	id, err := externalid.Name(name)
	if err == nil {
		if e, _, ok := c.ResolveDeclared(string(id)); ok {
			return e.Identity, MatchCatalogAlias, nil
		}
	}
	if hid, ok := externalid.Host(name); ok {
		return hid, MatchHost, nil
	}
	if err != nil {
		return "", "", err
	}
	return id, MatchNormalizedName, nil
}
