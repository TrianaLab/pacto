package contract

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

// InferredRecordFile is the bundle file that holds what Pacto completed for a
// service: generated with the bundle, never committed and never part of
// pacto.yaml.
const InferredRecordFile = "pacto.inferred.yaml"

// InferredRecordFormat is the only format value an inferred record carries.
const InferredRecordFormat = "pacto.inferred/v1"

// ConfidenceLikely marks an inferred value a file in the repository states.
// "observed" stays reserved for runtime evidence.
const ConfidenceLikely = "likely"

// ConfidencePossible marks an inferred value read from a convention, such as
// a values-<env>.yaml file name, rather than stated by a file.
const ConfidencePossible = "possible"

// InferredClassOwnerFile marks an owner read from CODEOWNERS or
// catalog-info.yaml: which file it came from, never that it was verified.
const InferredClassOwnerFile = "ownerFile"

// InferredRecord is what Pacto inferred from a service's folder beside its
// declared contract, with the inputs it came from, the files discovery
// recognized but could not read (Findings, at most a bound, FindingsDropped
// counting the rest).
type InferredRecord struct {
	Format          string            `json:"format" yaml:"format"`
	Inputs          InferredInputs    `json:"inputs" yaml:"inputs"`
	Fields          []Inferred        `json:"fields" yaml:"fields"`
	Findings        []InferredFinding `json:"findings,omitempty" yaml:"findings,omitempty"`
	FindingsDropped int               `json:"findingsDropped,omitempty" yaml:"findingsDropped,omitempty"`
	// Migrations are the contracts pacto ci raised in memory before it
	// published them: never a contract field, and absent when none was.
	Migrations []InferredMigration `json:"migrations,omitempty" yaml:"migrations,omitempty"`
}

// InferredMigration is one contract file published at another pactoVersion
// than it was written with: the version it declared, the version it was
// published as, the bundle file and the Pacto that raised it.
type InferredMigration struct {
	From   string `json:"from" yaml:"from"`
	To     string `json:"to" yaml:"to"`
	Source string `json:"source" yaml:"source"`
	By     string `json:"by" yaml:"by"`
}

// InferredFinding is one thing discovery recognized but could not read, or
// read as ambiguous: a code, its "<path>[:<line>]" and a fixed message that
// never quotes the file.
type InferredFinding struct {
	Code    string `json:"code" yaml:"code"`
	Source  string `json:"source" yaml:"source"`
	Message string `json:"message" yaml:"message"`
}

// InferredFindingTruncated is the finding that says discovery stopped early:
// it orders first, since every other result is partial under it.
const InferredFindingTruncated = "DISCOVERY_TRUNCATED"

// InferredChartDependency is one Helm chart dependency Chart.lock pins.
type InferredChartDependency struct {
	Chart      string `json:"chart" yaml:"chart"`
	Name       string `json:"name" yaml:"name"`
	Version    string `json:"version" yaml:"version"`
	Repository string `json:"repository,omitempty" yaml:"repository,omitempty"`
	Digest     string `json:"digest" yaml:"digest"`
}

// InferredInputs names what a record was completed from: the commit (absent
// when unknown), the Pacto that completed it, the discovery version and every
// file a field cites, with its digest.
type InferredInputs struct {
	Commit    string               `json:"commit,omitempty" yaml:"commit,omitempty"`
	Pacto     string               `json:"pacto" yaml:"pacto"`
	Discovery string               `json:"discovery" yaml:"discovery"`
	Sources   []InferredSourceFile `json:"sources" yaml:"sources"`
	// ChartDependencies are the chart dependencies Chart.lock pins.
	ChartDependencies []InferredChartDependency `json:"chartDependencies,omitempty" yaml:"chartDependencies,omitempty"`
	// Catalog is each external catalog completion classified with, as
	// "<origin> <digest>": the default first, then the organization's.
	Catalog []string `json:"catalog,omitempty" yaml:"catalog,omitempty"`
}

// InferredExternal is a system outside the cluster a folder names: its
// identity, the origin of the catalog that classified it (none for an
// unclassified host), every place the folder names it and, when read, the
// client library and the managed resource.
type InferredExternal struct {
	Identity string                   `json:"identity" yaml:"identity"`
	Catalog  string                   `json:"catalog,omitempty" yaml:"catalog,omitempty"`
	Sources  []InferredExternalSource `json:"sources" yaml:"sources"`
	Client   *InferredClient          `json:"client,omitempty" yaml:"client,omitempty"`
	Resource *InferredResource        `json:"resource,omitempty" yaml:"resource,omitempty"`
}

// InferredExternalSource is one place a folder names an external: what kind
// of source, the file, the line (0 when unknown), the key it sits under and
// the host and port at most, never the value it was read from.
type InferredExternalSource struct {
	Kind string `json:"kind" yaml:"kind"`
	Path string `json:"path" yaml:"path"`
	Line int    `json:"line,omitempty" yaml:"line,omitempty"`
	Key  string `json:"key,omitempty" yaml:"key,omitempty"`
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
}

// Confidence is how strongly s names its external, whose field has the
// confidence field: a client library, a provisioned resource or a key name
// alone is possible; a host or a Crossplane object takes the field's.
func (s InferredExternalSource) Confidence(field string) string {
	if s.Kind == "sdk" || s.Kind == "terraform" || (s.Host == "" && s.Kind != "crossplane") {
		return ConfidencePossible
	}
	return field
}

// InferredClient is the client library a manifest lists for an external and
// its major version, nil when unknown: never guessed.
type InferredClient struct {
	SDK   string `json:"sdk" yaml:"sdk"`
	Major *int   `json:"major,omitempty" yaml:"major,omitempty"`
}

// InferredResource is the managed resource a folder provisions for an
// external, with the identity parts it could not read.
type InferredResource struct {
	Type    string   `json:"type" yaml:"type"`
	Name    string   `json:"name,omitempty" yaml:"name,omitempty"`
	Missing []string `json:"missing,omitempty" yaml:"missing,omitempty"`
}

// InferredExternalKinds are the kinds of source an external is read from.
var InferredExternalKinds = []string{"env", "configMap", "secretKeyRef", "secretRef", "helmValues", "compose", "kustomize", "sdk", "terraform", "crossplane"}

var (
	inferredHostRE    = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]{1,5})?$`)
	inferredKeyRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	inferredCatalogRE = regexp.MustCompile(`^(default|organization) sha256:[0-9a-f]{64}$`)
	// inferredIdentityRE is the shape of an external identity: one to five
	// lower-case segments. pkg/contract imports no other Pacto package, so
	// this is externalid's segment rule; the fleet parses it in full.
	inferredIdentityRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?(/[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?){0,4}$`)
)

// InferredSourceFile is one file a record cites, with its sha256: a bundle
// path, or "repository:<path>" for a repository file outside the bundle.
type InferredSourceFile struct {
	Path   string `json:"path" yaml:"path"`
	Digest string `json:"digest" yaml:"digest"`
}

var inferredDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseInferredRecord decodes a record strictly: an unknown key, another
// format, a field with no field name or source, a field named twice, a
// confidence other than likely or possible, a class other than ownerFile or
// on a field other than service.owner, a finding with no code or source, a
// source that is absolute, leaves its root or carries a prefix other than
// "repository:", or an input without a sha256 digest is an error naming it.
func ParseInferredRecord(data []byte) (*InferredRecord, error) {
	var r InferredRecord
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", InferredRecordFile, err)
	}
	if r.Format != InferredRecordFormat {
		return nil, fmt.Errorf("%s: format is %q, want %q", InferredRecordFile, r.Format, InferredRecordFormat)
	}
	seen := make(map[string]bool, len(r.Fields))
	for i, f := range r.Fields {
		if err := checkInferredField(f, seen); err != nil {
			return nil, fmt.Errorf("%s: fields[%d]%s", InferredRecordFile, i, err)
		}
		seen[f.Field] = true
	}
	for i, f := range r.Findings {
		if f.Code == "" || !validSource(f.Source) {
			return nil, fmt.Errorf("%s: findings[%d] needs a code and a relative source, has %q at %q", InferredRecordFile, i, f.Code, f.Source)
		}
	}
	for i, c := range r.Inputs.Catalog {
		if !inferredCatalogRE.MatchString(c) {
			return nil, fmt.Errorf("%s: inputs.catalog[%d] is %q, want \"<default|organization> sha256:<64 hex>\"", InferredRecordFile, i, c)
		}
	}
	seenSource := make(map[string]bool, len(r.Migrations))
	for i, m := range r.Migrations {
		if err := checkInferredMigration(m, seenSource); err != nil {
			return nil, fmt.Errorf("%s: migrations[%d]%s", InferredRecordFile, i, err)
		}
		seenSource[m.Source] = true
	}
	for i, s := range r.Inputs.Sources {
		if !inferredDigestRE.MatchString(s.Digest) {
			return nil, fmt.Errorf("%s: inputs.sources[%d].digest is %q, want sha256:<64 hex>", InferredRecordFile, i, s.Digest)
		}
		if !validPath(s.Path) {
			return nil, fmt.Errorf("%s: inputs.sources[%d].path %q is not a relative path", InferredRecordFile, i, s.Path)
		}
	}
	return &r, nil
}

// checkInferredMigration is why m cannot be in a record whose migrations
// seen holds by source, as a suffix of "migrations[i]", or nil.
func checkInferredMigration(m InferredMigration, seen map[string]bool) error {
	switch {
	case m.From == "" || !Known(m.From):
		return fmt.Errorf(".from %q is not a supported pactoVersion", m.From)
	case m.To == "" || !Known(m.To):
		return fmt.Errorf(".to %q is not a supported pactoVersion", m.To)
	case slices.Index(SupportedVersions, m.From) >= slices.Index(SupportedVersions, m.To):
		return fmt.Errorf(".from %q is not older than to %q", m.From, m.To)
	case !validPath(m.Source):
		return fmt.Errorf(".source %q is not a relative path", m.Source)
	case seen[m.Source]:
		return fmt.Errorf(".source %q is listed twice", m.Source)
	case m.By == "":
		return errors.New(".by is empty")
	}
	return nil
}

// checkInferredField is why f cannot be in a record whose fields seen holds,
// as a suffix of "fields[i]", or nil.
func checkInferredField(f Inferred, seen map[string]bool) error {
	switch {
	case seen[f.Field]:
		return fmt.Errorf(".field %q is listed twice", f.Field)
	case f.Field == "":
		return errors.New(".field is empty")
	case f.Source == "":
		return errors.New(".source is empty")
	case !validFieldSource(f.Source):
		return fmt.Errorf(".source %q is not a relative path or a registry: or flag: source", f.Source)
	case f.Confidence != ConfidenceLikely && f.Confidence != ConfidencePossible:
		return fmt.Errorf(".confidence is %q, want likely or possible", f.Confidence)
	case f.Class != "" && (f.Class != InferredClassOwnerFile || f.Field != "service.owner"):
		return fmt.Errorf(".class is %q, want none or ownerFile on service.owner", f.Class)
	}
	for _, o := range f.Outranks {
		if !validSource(o) {
			return fmt.Errorf(".outranks %q is not a relative path", o)
		}
	}
	if f.External != nil {
		if !strings.HasPrefix(f.Field, "dependencies[") {
			return fmt.Errorf(".external is on %q, want a dependencies[<name>] field", f.Field)
		}
		if err := checkInferredExternal(f.External); err != nil {
			return fmt.Errorf(".external%s", err)
		}
	}
	return nil
}

// checkInferredExternal is why x cannot be in a record, as a suffix of
// "fields[i].external", or nil. A host with userinfo, a path that leaves the
// folder or a key that is a URL is refused, so a hand-edited record never
// carries a credential, and no error quotes the refused value: the fleet
// shows it in a limitation.
func checkInferredExternal(x *InferredExternal) error {
	if len(x.Identity) > 253 || !inferredIdentityRE.MatchString(x.Identity) || strings.Contains(x.Identity, "..") {
		return errors.New(".identity is not an external identity")
	}
	if x.Catalog != "" && x.Catalog != "default" && x.Catalog != "organization" {
		return fmt.Errorf(".catalog is %q, want default or organization", x.Catalog)
	}
	if len(x.Sources) == 0 {
		return errors.New(".sources is empty")
	}
	for i, s := range x.Sources {
		if err := checkExternalSource(s); err != nil {
			return fmt.Errorf(".sources[%d]%s", i, err)
		}
	}
	return checkClientResource(x.Client, x.Resource)
}

// checkClientResource is why an external's client c or resource r cannot be
// in a record, or nil.
func checkClientResource(c *InferredClient, r *InferredResource) error {
	switch {
	case c != nil && (c.SDK == "" || (c.Major != nil && *c.Major < 0)):
		return errors.New(".client needs an sdk and a major that is not negative")
	case r == nil:
		return nil
	case r.Type == "":
		return errors.New(".resource.type is empty")
	case slices.ContainsFunc(r.Missing, func(m string) bool { return m != "account" && m != "region" && m != "id" }):
		return fmt.Errorf(".resource.missing is %q, want account, region or id", r.Missing)
	}
	return nil
}

// checkExternalSource is why s cannot be an external's source, or nil.
func checkExternalSource(s InferredExternalSource) error {
	switch {
	case !slices.Contains(InferredExternalKinds, s.Kind):
		return errors.New(".kind is not a source kind")
	case !validPath(s.Path) || strings.ContainsAny(s.Path, "?#"):
		return errors.New(".path is not a relative path with no query")
	case s.Line < 0:
		return errors.New(".line is negative")
	case s.Key != "" && !inferredKeyRE.MatchString(s.Key):
		return errors.New(".key is not a key name")
	case s.Host != "" && !inferredHostRE.MatchString(s.Host):
		return errors.New(".host is not a host and port")
	}
	return nil
}

// externalPaths are the files the external on f cites, none when f has none.
func externalPaths(f Inferred) []string {
	if f.External == nil {
		return nil
	}
	out := make([]string, 0, len(f.External.Sources))
	for _, s := range f.External.Sources {
		out = append(out, s.Path)
	}
	return out
}

// sourceForms are the field sources outside the folder, which
// inputs.sources never lists: the verified registry baseline a derived value
// came from (a repository, pinned by digest when it holds a version) and the
// command-line flag that supplied one. Any other value under one of these
// prefixes is invalid.
var sourceForms = map[string]*regexp.Regexp{
	"registry:": regexp.MustCompile(`^registry:[a-z0-9][a-z0-9._:/-]*[a-z0-9](@sha256:[0-9a-f]{64})?$`),
	"flag:":     regexp.MustCompile(`^flag:--[a-z][a-z-]*$`),
}

// sourceForm reports whether s carries one of the sourceForms prefixes, and
// whether it is valid under it.
func sourceForm(s string) (valid, outside bool) {
	for prefix, re := range sourceForms {
		if strings.HasPrefix(s, prefix) {
			return re.MatchString(s), true
		}
	}
	return false, false
}

// validFieldSource reports whether s is a valid field source: one of the
// sourceForms, or "<path>[:<line>]".
func validFieldSource(s string) bool {
	if valid, outside := sourceForm(s); outside {
		return valid
	}
	return validSource(s)
}

// validSource reports whether s is "<path>[:<line>]" with a valid path.
func validSource(s string) bool { return validPath(sourceFile(s)) }

// validPath reports whether p is a clean relative slash path ("." for the
// folder itself), prefixed by "repository:" or by nothing.
func validPath(p string) bool {
	p = strings.TrimPrefix(p, repositoryPrefix)
	return fs.ValidPath(p) && !strings.Contains(p, ":")
}

// repositoryPrefix marks a source read from the repository root, outside the
// bundle.
const repositoryPrefix = "repository:"

// Marshal writes the record with its fields sorted by field then source, its
// findings truncation first then by code and source, its sources by path, its
// migrations by source then from, and no timestamp, so the same inputs give
// the same bytes.
func (r *InferredRecord) Marshal() []byte {
	out := *r
	out.Fields = slices.Clone(r.Fields)
	slices.SortFunc(out.Fields, func(a, b Inferred) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Source, b.Source))
	})
	for i, f := range out.Fields {
		if f.External != nil {
			x := *f.External
			x.Sources = slices.Clone(x.Sources)
			slices.SortFunc(x.Sources, func(a, b InferredExternalSource) int {
				return cmp.Or(strings.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), strings.Compare(a.Key, b.Key),
					strings.Compare(a.Kind, b.Kind), strings.Compare(a.Host, b.Host))
			})
			out.Fields[i].External = &x
		}
	}
	out.Findings = slices.Clone(r.Findings)
	slices.SortFunc(out.Findings, func(a, b InferredFinding) int {
		return cmp.Or(-cmp.Compare(boolInt(a.Code == InferredFindingTruncated), boolInt(b.Code == InferredFindingTruncated)),
			strings.Compare(a.Code, b.Code), strings.Compare(a.Source, b.Source))
	})
	out.Inputs.Sources = slices.Clone(r.Inputs.Sources)
	slices.SortFunc(out.Inputs.Sources, func(a, b InferredSourceFile) int { return strings.Compare(a.Path, b.Path) })
	out.Migrations = slices.Clone(r.Migrations)
	slices.SortFunc(out.Migrations, func(a, b InferredMigration) int {
		return cmp.Or(strings.Compare(a.Source, b.Source), strings.Compare(a.From, b.From))
	})
	data, _ := yaml.Marshal(&out) // strings and slices of them always encode
	return data
}

// ReadInferredRecord reads and parses the record in fsys: nil and no error
// when fsys is nil or holds none.
func ReadInferredRecord(fsys fs.FS) (*InferredRecord, error) {
	if fsys == nil {
		return nil, nil
	}
	data, err := fs.ReadFile(fsys, InferredRecordFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", InferredRecordFile, err)
	}
	return ParseInferredRecord(data)
}

// VerifyInferredRecord checks the record against the bundle it sits in: every
// source exists in fsys with its digest (the record itself is never a source),
// and every field cites, in its source and its outranks, files the sources
// list, and every migration names a file in fsys that declares the
// pactoVersion it was published as. A repository file outside
// the bundle (a "repository:" source) cannot be checked here: its digest only
// tells two records' inputs apart. A stale or hand-edited record fails here
// instead of reading as authoritative.
func VerifyInferredRecord(fsys fs.FS, rec *InferredRecord) error {
	listed := make(map[string]bool, len(rec.Inputs.Sources))
	for i, s := range rec.Inputs.Sources {
		if s.Path == InferredRecordFile {
			return fmt.Errorf("inputs.sources[%d] cites %s itself", i, InferredRecordFile)
		}
		listed[s.Path] = true
		if strings.HasPrefix(s.Path, repositoryPrefix) {
			continue
		}
		data, err := fs.ReadFile(fsys, s.Path)
		if err != nil {
			return fmt.Errorf("inputs.sources[%d]: %s is not in the bundle", i, s.Path)
		}
		sum := sha256.Sum256(data)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != s.Digest {
			return fmt.Errorf("inputs.sources[%d]: %s has digest %s, the record says %s", i, s.Path, got, s.Digest)
		}
	}
	for i, f := range rec.Fields {
		for _, src := range append(append([]string{f.Source}, f.Outranks...), externalPaths(f)...) {
			if _, outside := sourceForm(src); outside {
				continue
			}
			if file := sourceFile(src); !listed[file] {
				return fmt.Errorf("fields[%d] (%s) cites %s, which inputs.sources does not list", i, f.Field, file)
			}
		}
	}
	for i, m := range rec.Migrations {
		if err := verifyMigration(fsys, m); err != nil {
			return fmt.Errorf("migrations[%d]: %s %w", i, m.Source, err)
		}
	}
	return nil
}

// verifyMigration is why m does not match the bundle fsys, or nil: its source
// must be in fsys and declare m.To.
func verifyMigration(fsys fs.FS, m InferredMigration) error {
	data, err := fs.ReadFile(fsys, m.Source)
	if err != nil {
		return errors.New("is not in the bundle")
	}
	node, err := versionNode(data)
	if err != nil {
		return fmt.Errorf("is unreadable: %w", err)
	}
	var got string
	if node != nil {
		got = node.Value
	}
	if got != m.To {
		return fmt.Errorf("declares pactoVersion %q, the record says it was published as %q", got, m.To)
	}
	return nil
}

// sourceFile is the file a "<path>:<line>" or "<path>" source names.
func sourceFile(source string) string {
	i := strings.LastIndexByte(source, ':')
	if i < 0 || i == len(source)-1 || strings.Trim(source[i+1:], "0123456789") != "" {
		return source
	}
	return source[:i]
}

// WithInferredRecord is fsys serving data as pacto.inferred.yaml, in place
// of any copy fsys holds, without copying the rest: a local read overlays the
// record it computed on the folder it read.
func WithInferredRecord(fsys fs.FS, data []byte) fs.FS {
	return recordFS{base: fsys, name: InferredRecordFile, record: fstest.MapFS{InferredRecordFile: {Data: data, Mode: 0o644}}}
}

// WithoutInferredRecord is fsys with no pacto.inferred.yaml: what a local
// hash reads, since the record names the commit and would move it on every
// commit.
func WithoutInferredRecord(fsys fs.FS) fs.FS { return WithoutFile(fsys, InferredRecordFile) }

// WithoutFile is fsys with no root file name, without copying the rest.
func WithoutFile(fsys fs.FS, name string) fs.FS {
	return recordFS{base: fsys, name: name, record: fstest.MapFS{}}
}

// recordFS is base with its root file name taken from record.
type recordFS struct {
	base   fs.FS
	name   string
	record fstest.MapFS // holds the file, or nothing to hide it
}

func (r recordFS) Open(name string) (fs.File, error) {
	if name == r.name {
		return r.record.Open(name)
	}
	return r.base.Open(name)
}

func (r recordFS) Stat(name string) (fs.FileInfo, error) {
	if name == r.name {
		return r.record.Stat(name)
	}
	return fs.Stat(r.base, name)
}

func (r recordFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(r.base, name)
	if name != "." || err != nil {
		return entries, err
	}
	entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return e.Name() == r.name })
	if info, err := r.record.Stat(r.name); err == nil {
		entries = append(entries, fs.FileInfoToDirEntry(info))
		slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	}
	return entries, nil
}

// InferredUnchanged reports whether next differs from prev only in its
// inferred record with the same cited inputs, so it needs no republish. what
// names the inputs that differ ("discovery 1 -> 2", "commit 4e1a9c0 ->
// 77c0b2e") whenever the declared parts match, and is empty when they do not.
// Both records are read from the bundles' files and verified against them, so
// a stale or forged record never reads as a record-only change; digest
// computes a bundle's digest, here over its files without the record.
func InferredUnchanged(prev, next *Bundle, digest func(*Bundle) (string, error)) (unchanged bool, what string) {
	declared := func(b *Bundle) (string, *InferredRecord, error) {
		rec, err := ReadInferredRecord(b.FS)
		if err == nil && rec != nil {
			err = VerifyInferredRecord(b.FS, rec)
		}
		if err != nil {
			return "", nil, err
		}
		d, err := digest(&Bundle{Contract: b.Contract, FS: WithoutInferredRecord(b.FS)})
		return d, cmp.Or(rec, &InferredRecord{}), err
	}
	pd, pr, perr := declared(prev)
	nd, nr, nerr := declared(next)
	if perr != nil || nerr != nil || pd != nd {
		return false, ""
	}
	var diffs []string
	for _, in := range [][3]string{
		{"discovery", pr.Inputs.Discovery, nr.Inputs.Discovery},
		{"pacto", pr.Inputs.Pacto, nr.Inputs.Pacto},
		{"commit", shortCommit(pr.Inputs.Commit), shortCommit(nr.Inputs.Commit)},
	} {
		if in[1] != in[2] {
			diffs = append(diffs, in[0]+" "+cmp.Or(in[1], "none")+" -> "+cmp.Or(in[2], "none"))
		}
	}
	if a, b := strings.Join(pr.Inputs.Catalog, ", "), strings.Join(nr.Inputs.Catalog, ", "); a != b {
		diffs = append(diffs, "catalog "+cmp.Or(a, "none")+" -> "+cmp.Or(b, "none"))
	}
	unchanged = slices.Equal(pr.Inputs.Sources, nr.Inputs.Sources)
	if !unchanged {
		diffs = append(diffs, "sources")
	}
	return unchanged, cmp.Or(strings.Join(diffs, ", "), "fields")
}

// boolInt is 1 for true.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// shortCommit is a commit as git abbreviates it.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
