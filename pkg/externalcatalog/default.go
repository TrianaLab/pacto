package externalcatalog

import (
	_ "embed"
	"sync"
)

// DefaultRepository is where the release pipeline publishes the default catalog.
const DefaultRepository = "ghcr.io/trianalab/pacto/external-catalog"

// CopyEmbedded is the [Ref] copy of bytes compiled into this binary.
const CopyEmbedded = "embedded"

// SignatureUnknown is a [Ref] signature nobody has checked.
const SignatureUnknown = "unknown"

//go:embed default/catalog.yaml
var defaultBytes []byte

// DefaultBytes returns a copy of the embedded default catalog.
func DefaultBytes() []byte { return append([]byte(nil), defaultBytes...) }

// DefaultDigest returns the digest of the default catalog artifact, computed
// from the embedded bytes; the published artifact has the same digest.
var DefaultDigest = sync.OnceValue(func() string { return Digest(defaultBytes) })

// DefaultRef describes the embedded default catalog before anyone has checked
// it against the registry.
func DefaultRef() Ref {
	return Ref{
		Origin:     OriginDefault,
		Repository: DefaultRepository,
		Digest:     DefaultDigest(),
		Copy:       CopyEmbedded,
		Signature:  SignatureUnknown,
		Reason:     "the embedded copy has not been checked against the registry",
	}
}

// Default returns the embedded default catalog. The embedded file is validated
// by TestDefaultCatalogValid, so the panic cannot ship.
var Default = sync.OnceValue(func() *Catalog { return mustDefault(defaultBytes) })

func mustDefault(b []byte) *Catalog {
	f, err := load(b)
	if err != nil {
		panic("embedded external catalog: " + err.Error())
	}
	return Merge(f, DefaultRef(), nil, nil)
}

// load parses and validates one catalog file.
func load(b []byte) (*File, error) {
	f, err := ParseFile(b)
	if err != nil {
		return nil, err
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return f, nil
}
