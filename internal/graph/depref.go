package graph

import "strings"

// Scheme represents the type of a dependency reference.
type Scheme int

const (
	// SchemeLocal indicates a local filesystem reference (no scheme or file://).
	SchemeLocal Scheme = iota
	// SchemeOCI indicates an OCI registry reference (oci://).
	SchemeOCI
)

// String returns the scheme as a human-readable string.
func (s Scheme) String() string {
	switch s {
	case SchemeOCI:
		return "oci"
	case SchemeLocal:
		return "local"
	default:
		return "unknown"
	}
}

const (
	ociPrefix  = "oci://"
	filePrefix = "file://"
)

// DependencyRef is a parsed, normalized dependency reference.
type DependencyRef struct {
	Scheme   Scheme
	Location string // Registry ref (OCI) or filesystem path (local)
	Original string // Original unparsed reference
}

// ParseDependencyRef parses a raw dependency reference string into a
// structured DependencyRef. The scheme is detected as follows:
//   - "oci://" prefix → OCI registry reference
//   - "file://" prefix → local filesystem reference
//   - no scheme → local filesystem reference
func ParseDependencyRef(raw string) DependencyRef {
	if loc, ok := strings.CutPrefix(raw, ociPrefix); ok {
		return DependencyRef{
			Scheme:   SchemeOCI,
			Location: loc,
			Original: raw,
		}
	}
	if loc, ok := strings.CutPrefix(raw, filePrefix); ok {
		return DependencyRef{
			Scheme:   SchemeLocal,
			Location: loc,
			Original: raw,
		}
	}
	return DependencyRef{
		Scheme:   SchemeLocal,
		Location: raw,
		Original: raw,
	}
}

// IsLocal reports whether the reference points to the local filesystem.
func (r DependencyRef) IsLocal() bool {
	return r.Scheme == SchemeLocal
}

// IsOCI reports whether the reference points to an OCI registry.
func (r DependencyRef) IsOCI() bool {
	return r.Scheme == SchemeOCI
}
