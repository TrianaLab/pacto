package contract

import (
	"errors"
	"io/fs"
)

// Bundle represents a contract bundled with its referenced files.
type Bundle struct {
	Contract *Contract
	RawYAML  []byte // Original YAML bytes; populated for local reads.
	FS       fs.FS
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
