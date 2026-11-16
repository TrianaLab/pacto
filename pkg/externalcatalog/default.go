package externalcatalog

import (
	_ "embed"
	"sync"
)

// DefaultRepository is where the release pipeline publishes the default catalog.
const DefaultRepository = "ghcr.io/trianalab/pacto/external-catalog"

// DefaultSigner and DefaultIssuer are the keyless identity the release
// workflow signs the default catalog with.
const (
	DefaultSigner = "https://github.com/TrianaLab/pacto/.github/workflows/release.yml@refs/heads/main"
	DefaultIssuer = "https://token.actions.githubusercontent.com"
)

// CopyEmbedded is the [Ref] copy of bytes compiled into this binary.
const CopyEmbedded = "embedded"

// CopyRegistry is the [Ref] copy of bytes the registry holds at the recorded
// digest.
const CopyRegistry = "registry"

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

// Default returns the embedded default catalog.
var Default = sync.OnceValue(func() *Catalog { return Merge(defaultFile(), DefaultRef(), nil, nil) })

// defaultFile is the embedded default catalog file. It is validated by
// TestDefaultCatalogValid, so the panic cannot ship.
var defaultFile = sync.OnceValue(func() *File { return mustLoad(defaultBytes) })

func mustLoad(b []byte) *File {
	f, err := load(b)
	if err != nil {
		panic("embedded external catalog: " + err.Error())
	}
	return f
}

// Assemble merges the organization catalog org, when it is not nil, over the
// embedded default catalog. defRef and orgRef record what is known about each;
// an org that fails to parse or validate is an error.
func Assemble(defRef Ref, org []byte, orgRef Ref) (*Catalog, error) {
	if org == nil {
		return Merge(defaultFile(), defRef, nil, nil), nil
	}
	f, err := load(org)
	if err != nil {
		return nil, err
	}
	return Merge(defaultFile(), defRef, f, &orgRef), nil
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
