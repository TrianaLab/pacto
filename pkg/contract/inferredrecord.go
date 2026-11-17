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
}

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
	case !validSource(f.Source):
		return fmt.Errorf(".source %q is not a relative path", f.Source)
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
	return nil
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
// findings truncation first then by code and source, its sources by path, and no timestamp, so the same inputs give the same bytes.
func (r *InferredRecord) Marshal() []byte {
	out := *r
	out.Fields = slices.Clone(r.Fields)
	slices.SortFunc(out.Fields, func(a, b Inferred) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Source, b.Source))
	})
	out.Findings = slices.Clone(r.Findings)
	slices.SortFunc(out.Findings, func(a, b InferredFinding) int {
		return cmp.Or(-cmp.Compare(boolInt(a.Code == InferredFindingTruncated), boolInt(b.Code == InferredFindingTruncated)),
			strings.Compare(a.Code, b.Code), strings.Compare(a.Source, b.Source))
	})
	out.Inputs.Sources = slices.Clone(r.Inputs.Sources)
	slices.SortFunc(out.Inputs.Sources, func(a, b InferredSourceFile) int { return strings.Compare(a.Path, b.Path) })
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
// list. A repository file outside
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
		for _, src := range append([]string{f.Source}, f.Outranks...) {
			if file := sourceFile(src); !listed[file] {
				return fmt.Errorf("fields[%d] (%s) cites %s, which inputs.sources does not list", i, f.Field, file)
			}
		}
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
	return recordFS{base: fsys, record: fstest.MapFS{InferredRecordFile: {Data: data, Mode: 0o644}}}
}

// WithoutInferredRecord is fsys with no pacto.inferred.yaml: what a local
// hash reads, since the record names the commit and would move it on every
// commit.
func WithoutInferredRecord(fsys fs.FS) fs.FS {
	return recordFS{base: fsys, record: fstest.MapFS{}}
}

// recordFS is base with its root pacto.inferred.yaml taken from record.
type recordFS struct {
	base   fs.FS
	record fstest.MapFS // holds the record, or nothing to hide it
}

func (r recordFS) Open(name string) (fs.File, error) {
	if name == InferredRecordFile {
		return r.record.Open(name)
	}
	return r.base.Open(name)
}

func (r recordFS) Stat(name string) (fs.FileInfo, error) {
	if name == InferredRecordFile {
		return r.record.Stat(name)
	}
	return fs.Stat(r.base, name)
}

func (r recordFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(r.base, name)
	if name != "." || err != nil {
		return entries, err
	}
	entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return e.Name() == InferredRecordFile })
	if info, err := r.record.Stat(InferredRecordFile); err == nil {
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
