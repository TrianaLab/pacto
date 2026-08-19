package catalog

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
)

// ErrInvalidContentID is returned when a content identity is not a closed-enum
// scheme paired with a well-formed digest.
var ErrInvalidContentID = errors.New("catalog: invalid content identity")

// ContentScheme names how a revision's immutable content identity was derived.
// It is a closed enum, so a scheme can never be confused with user text.
type ContentScheme string

const (
	// SchemeOCI identifies content by the exact manifest digest a registry
	// resolved a reference to. A tag is never a content identity: two tags may
	// name one digest, and one tag names different digests over time.
	SchemeOCI ContentScheme = "oci"
	// SchemeLocal identifies content by a deterministic hash of the bundle's
	// files. Two byte-identical directories therefore share one identity, and a
	// path is never a content identity.
	SchemeLocal ContentScheme = "local"
)

// ContentID is the immutable content identity of one contract revision, and the
// only thing the catalog treats as identity. It is a comparable struct of a
// closed-enum scheme and a validated `<algorithm>:<hex>` digest, so it is usable
// as a map key without ever joining user-controlled text with a delimiter: a
// service, domain or reference containing "/", ":", "%" or arbitrary UTF-8
// cannot reach it, let alone collide inside it.
type ContentID struct {
	Scheme ContentScheme `json:"scheme"`
	Digest string        `json:"digest"`
}

// NewContentID validates a scheme/digest pair into a content identity.
func NewContentID(scheme ContentScheme, dgst string) (ContentID, error) {
	if scheme != SchemeOCI && scheme != SchemeLocal {
		return ContentID{}, fmt.Errorf("%w: unknown scheme %q", ErrInvalidContentID, scheme)
	}
	// digest.Parse returns fixed sentinel errors, so a malformed digest never
	// echoes back into the message.
	if _, err := digest.Parse(dgst); err != nil {
		return ContentID{}, fmt.Errorf("%w: %w", ErrInvalidContentID, err)
	}
	return ContentID{Scheme: scheme, Digest: dgst}, nil
}

// Zero reports whether the identity is unset.
func (c ContentID) Zero() bool { return c == ContentID{} }

// String renders the identity for display and logs. Both halves are constrained
// -- a closed enum and a validated digest -- so the join is unambiguous. It is
// never used to derive identity; comparisons go field by field.
func (c ContentID) String() string { return string(c.Scheme) + ":" + c.Digest }

func compareContentID(a, b ContentID) int {
	if v := cmp.Compare(a.Scheme, b.Scheme); v != 0 {
		return v
	}
	return cmp.Compare(a.Digest, b.Digest)
}

// ServiceID is the domain-qualified service identity a revision claims. Two
// revisions may share a ServiceID and even a version while being different
// content, and that disagreement is reported as a conflict rather than resolved
// by picking one.
type ServiceID struct {
	Domain string `json:"domain,omitempty"`
	Name   string `json:"name"`
}

func compareServiceID(a, b ServiceID) int {
	if v := cmp.Compare(a.Domain, b.Domain); v != 0 {
		return v
	}
	return cmp.Compare(a.Name, b.Name)
}

// RevisionID names one contract revision: the domain-qualified service it
// belongs to, and the immutable content it is. Content alone is not enough,
// because mirroring publishes one bundle into two places: the manifest digest
// is identical by construction, and the two services are still not each other.
// A revision belongs to exactly one service, so both halves are identity, and
// every declaration, edge, path step, cycle and conflict is keyed on the pair.
//
// Both halves stay comparable structs, so the pair is a map key without ever
// joining user-controlled text with a delimiter.
type RevisionID struct {
	Service ServiceID `json:"service"`
	Content ContentID `json:"content"`
}

// compareRevisionID orders by content first, so revisions still sort by content
// identity, and by service only to break the tie that mirroring creates.
func compareRevisionID(a, b RevisionID) int {
	if v := compareContentID(a.Content, b.Content); v != 0 {
		return v
	}
	return compareServiceID(a.Service, b.Service)
}

// RootID is the ordinal of a requested root within the request. It is an input
// position, not a name, so it cannot collide and cannot be forged by a hostile
// reference. It is deliberately excluded from the catalog fingerprint.
type RootID int

// DeclarationID identifies one dependency declaration: the revision that
// declares it, plus its index in that contract's declared order. Both halves
// are structural, so the same declared name in two contracts -- or twice in one
// -- stays distinct without any string joining. The declaring half is a
// [RevisionID] rather than a content identity, so two mirrors of one bundle
// declare their dependencies separately even at the same index.
type DeclarationID struct {
	From  RevisionID `json:"from"`
	Index int        `json:"index"`
}

func compareDeclaration(a, b DeclarationID) int {
	if v := compareRevisionID(a.From, b.From); v != 0 {
		return v
	}
	return cmp.Compare(a.Index, b.Index)
}

// Path is one retained route from a root to a revision: the root it started at
// and the ordered declarations traversed. A path of zero steps is the root
// itself. Paths are structured steps, never a rendered string, so no name can
// forge or split one.
type Path struct {
	Root  RootID          `json:"root"`
	Steps []DeclarationID `json:"steps,omitempty"`
}

func comparePath(a, b Path) int {
	if v := cmp.Compare(a.Root, b.Root); v != 0 {
		return v
	}
	return slices.CompareFunc(a.Steps, b.Steps, compareDeclaration)
}

func clonePath(p Path) Path {
	p.Steps = slices.Clone(p.Steps)
	return p
}

// Rank is a revision's best deterministic standing across every retained path.
// It is derived from the shortest retained path, so a revision reachable both
// directly and transitively ranks as direct while keeping both paths.
type Rank string

const (
	// RankRoot means the revision was itself requested as a root.
	RankRoot Rank = "root"
	// RankDirect means the shortest retained path is one declaration long.
	RankDirect Rank = "direct"
	// RankTransitive means every retained path is at least two declarations long.
	RankTransitive Rank = "transitive"
)

func rankForDepth(depth int) Rank {
	switch depth {
	case 0:
		return RankRoot
	case 1:
		return RankDirect
	default:
		return RankTransitive
	}
}
