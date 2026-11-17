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

// ConfidenceLikely marks an inferred value read from a file in the
// repository. "observed" stays reserved for runtime evidence.
const ConfidenceLikely = "likely"

// InferredRecord is what Pacto inferred from a service's folder beside its
// declared contract, with the inputs it came from.
type InferredRecord struct {
	Format string         `json:"format" yaml:"format"`
	Inputs InferredInputs `json:"inputs" yaml:"inputs"`
	Fields []Inferred     `json:"fields" yaml:"fields"`
}

// InferredInputs names what a record was completed from: the commit (absent
// when unknown), the Pacto that completed it, the discovery version and every
// file a field cites, with its digest.
type InferredInputs struct {
	Commit    string               `json:"commit,omitempty" yaml:"commit,omitempty"`
	Pacto     string               `json:"pacto" yaml:"pacto"`
	Discovery string               `json:"discovery" yaml:"discovery"`
	Sources   []InferredSourceFile `json:"sources" yaml:"sources"`
}

// InferredSourceFile is one bundle file a record cites, with its sha256.
type InferredSourceFile struct {
	Path   string `json:"path" yaml:"path"`
	Digest string `json:"digest" yaml:"digest"`
}

var inferredDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseInferredRecord decodes a record strictly: an unknown key, another
// format, a field with no field name or source, a field named twice, a
// confidence other than likely or possible, or a source without a sha256
// digest is an error naming it.
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
		switch {
		case seen[f.Field]:
			return nil, fmt.Errorf("%s: fields[%d].field %q is listed twice", InferredRecordFile, i, f.Field)
		case f.Field == "":
			return nil, fmt.Errorf("%s: fields[%d].field is empty", InferredRecordFile, i)
		case f.Source == "":
			return nil, fmt.Errorf("%s: fields[%d].source is empty", InferredRecordFile, i)
		case f.Confidence != ConfidenceLikely && f.Confidence != "possible":
			return nil, fmt.Errorf("%s: fields[%d].confidence is %q, want likely or possible", InferredRecordFile, i, f.Confidence)
		}
		seen[f.Field] = true
	}
	for i, s := range r.Inputs.Sources {
		if !inferredDigestRE.MatchString(s.Digest) {
			return nil, fmt.Errorf("%s: inputs.sources[%d].digest is %q, want sha256:<64 hex>", InferredRecordFile, i, s.Digest)
		}
	}
	return &r, nil
}

// Marshal writes the record with its fields sorted by field then source and
// its sources by path, and no timestamp, so the same inputs give the same bytes.
func (r *InferredRecord) Marshal() []byte {
	out := *r
	out.Fields = slices.Clone(r.Fields)
	slices.SortFunc(out.Fields, func(a, b Inferred) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Source, b.Source))
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
// and every field cites a file the sources list. A stale or hand-edited record
// fails here instead of reading as authoritative.
func VerifyInferredRecord(fsys fs.FS, rec *InferredRecord) error {
	listed := make(map[string]bool, len(rec.Inputs.Sources))
	for i, s := range rec.Inputs.Sources {
		if s.Path == InferredRecordFile {
			return fmt.Errorf("inputs.sources[%d] cites %s itself", i, InferredRecordFile)
		}
		data, err := fs.ReadFile(fsys, s.Path)
		if err != nil {
			return fmt.Errorf("inputs.sources[%d]: %s is not in the bundle", i, s.Path)
		}
		sum := sha256.Sum256(data)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != s.Digest {
			return fmt.Errorf("inputs.sources[%d]: %s has digest %s, the record says %s", i, s.Path, got, s.Digest)
		}
		listed[s.Path] = true
	}
	for i, f := range rec.Fields {
		if file := sourceFile(f.Source); !listed[file] {
			return fmt.Errorf("fields[%d] (%s) cites %s, which inputs.sources does not list", i, f.Field, file)
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

// shortCommit is a commit as git abbreviates it.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
