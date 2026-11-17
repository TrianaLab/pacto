package contract

import (
	"errors"
	"io/fs"
	"strings"
)

// Bundle represents a contract bundled with its referenced files.
type Bundle struct {
	Contract *Contract
	RawYAML  []byte // Original YAML bytes; populated for local reads.
	FS       fs.FS
	// Inferred lists the values a local read implied rather than read from
	// pacto.yaml, with their sources. Complete fills it.
	Inferred []Inferred
}

// Complete records what a local read of the bundle implies: an absent
// pactoVersion is this CLI's, and an absent service.name is inferred from the
// bundle's files or, failing those, dirName. Call it once, on the final
// contract.
func (b *Bundle) Complete(dirName string) {
	if raw, _ := b.Raw(); !declaresVersion(raw) { // unreadable declares nothing
		b.Inferred = append(b.Inferred, Inferred{Field: "pactoVersion", Value: Current(), Source: "this CLI"})
	}
	if b.Contract.Service.Name != "" {
		return
	}
	if inf, ok := InferName(b.FS, dirName); ok {
		b.Contract.Service.Name = inf.Value
		b.Inferred = append(b.Inferred, inf)
	}
}

// NameInferred returns the inferred service.name entry, if any.
func (b *Bundle) NameInferred() (Inferred, bool) {
	for _, inf := range b.Inferred {
		if inf.Field == "service.name" {
			return inf, true
		}
	}
	return Inferred{}, false
}

// Raw returns the bundle's contract document as bytes.
//
// RawYAML is only populated for local reads, so a bundle fetched from a
// registry has the same document sitting in FS and nowhere else. Every caller
// that needs the bytes needs both places, and the three that open-coded the
// choice disagreed about the fallback: one treated a missing document as fatal,
// one skipped validation silently, one left the service Unknown. Ask here
// instead.
func (b *Bundle) Raw() ([]byte, error) {
	if len(b.RawYAML) > 0 {
		return b.RawYAML, nil
	}
	if b.FS == nil {
		return nil, errors.New("bundle has no raw YAML or filesystem")
	}
	return fs.ReadFile(b.FS, "pacto.yaml")
}

// DocsDir is the bundle directory that holds Markdown documentation.
const DocsDir = "docs"

// WalkDocs calls fn with the path of every docs/**/*.md file in fsys, in lexical
// order, until fn returns false. A missing docs/ or an unreadable subtree is
// skipped rather than reported: documentation is optional. A nil fsys has none.
func WalkDocs(fsys fs.FS, fn func(path string) bool) {
	if fsys == nil {
		return
	}
	_ = fs.WalkDir(fsys, DocsDir, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback never fails
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if !fn(p) {
			return fs.SkipAll
		}
		return nil
	})
}
