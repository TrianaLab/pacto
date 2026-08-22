package catalog

import (
	"slices"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Root is one requested root and what became of it. Every requested root that
// was within [Bounds.MaxRoots] appears here whether or not it resolved, so an
// invalid root is reported rather than silently dropped.
type Root struct {
	ID RootID `json:"id"`
	// RequestedRef is the reference the caller asked for, preserved verbatim.
	RequestedRef string `json:"requestedRef"`
	Resolved     bool   `json:"resolved"`
	// Revision is the identity the root resolved to; zero when it did not
	// resolve.
	Revision RevisionID `json:"revision,omitzero"`
	// ResolvedRef is the immutable reference the request resolved to, when the
	// resolver produced one.
	ResolvedRef string `json:"resolvedRef,omitempty"`
	// Reason says why the root did not resolve; zero when it did.
	Reason Reason `json:"reason,omitzero"`
}

// Revision is one immutable contract revision, canonically deduplicated by
// [RevisionID]. The same bytes reached through two roots, two references or two
// routes are one Revision carrying all of that provenance; the same bytes
// published by two different services are two.
type Revision struct {
	Content ContentID `json:"content"`
	Service ServiceID `json:"service"`
	Version string    `json:"version,omitempty"`
	// PactoVersion and Owner are the durable contract metadata the catalog
	// preserves. Free-form contract metadata is deliberately not projected here.
	PactoVersion string         `json:"pactoVersion,omitempty"`
	Owner        contract.Owner `json:"owner,omitzero"`
	// RequestedRefs are every distinct reference text that reached this content:
	// a moving tag and a digest pin that resolve to the same bytes both appear.
	RequestedRefs []string `json:"requestedRefs,omitempty"`
	// ResolvedRefs are every distinct immutable reference the resolver returned
	// for this content.
	ResolvedRefs []string `json:"resolvedRefs,omitempty"`
	// Roots are the requested roots this revision is reachable from, ascending.
	Roots []RootID `json:"roots,omitempty"`
	// Paths are the retained root-to-revision routes, including every branch of
	// a diamond and every root of a shared revision.
	Paths []Path `json:"paths,omitempty"`
	// PathsTruncated reports that [Bounds.MaxPaths] stopped further routes from
	// being retained, so Paths is a valid subset rather than the whole set.
	PathsTruncated bool `json:"pathsTruncated,omitempty"`
	// MinDepth is the shortest retained path length, and Rank is its name.
	MinDepth int  `json:"minDepth"`
	Rank     Rank `json:"rank"`
}

// ID is the revision's identity: its service and its content together.
func (r Revision) ID() RevisionID { return RevisionID{Service: r.Service, Content: r.Content} }

// Shared reports whether more than one requested root reaches this revision.
func (r Revision) Shared() bool { return len(r.Roots) > 1 }

func cloneRevision(r Revision) Revision {
	r.Owner.Contacts = slices.Clone(r.Owner.Contacts)
	r.RequestedRefs = slices.Clone(r.RequestedRefs)
	r.ResolvedRefs = slices.Clone(r.ResolvedRefs)
	r.Roots = slices.Clone(r.Roots)
	paths := make([]Path, len(r.Paths))
	for i, p := range r.Paths {
		paths[i] = clonePath(p)
	}
	r.Paths = paths
	return r
}

// Edge is one resolved dependency: the declaration that produced it and the
// revision it resolved to. The declaration is kept separate from the revision
// because one revision can be declared by many contracts, under many names, at
// many constraints.
type Edge struct {
	Declaration DeclarationID `json:"declaration"`
	Name        string        `json:"name,omitempty"`
	Ref         string        `json:"ref,omitempty"`
	Constraint  string        `json:"constraint,omitempty"`
	Required    bool          `json:"required,omitempty"`
	To          RevisionID    `json:"to"`
}

// Unresolved is a declared dependency that did not resolve. It is knowledge
// about a gap, not the absence of a dependency.
type Unresolved struct {
	Declaration DeclarationID `json:"declaration"`
	Name        string        `json:"name,omitempty"`
	Ref         string        `json:"ref,omitempty"`
	Constraint  string        `json:"constraint,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Reason      Reason        `json:"reason"`
}

// ConflictKind names the shape of a disagreement the catalog refuses to resolve
// on the caller's behalf.
type ConflictKind string

const (
	// ConflictVersion: one service appears at more than one version.
	ConflictVersion ConflictKind = "version"
	// ConflictContent: one service AND version appears as more than one content
	// identity. Same name, same version, different bytes.
	ConflictContent ConflictKind = "content"
	// ConflictDeclaration: one declaration resolved to more than one content,
	// which happens when byte-identical contracts sit in different places and
	// their relative references therefore point elsewhere.
	ConflictDeclaration ConflictKind = "declaration"
)

// Conflict is a disagreement left visible. Nothing is selected away.
type Conflict struct {
	Kind        ConflictKind  `json:"kind"`
	Service     ServiceID     `json:"service,omitzero"`
	Version     string        `json:"version,omitempty"`
	Declaration DeclarationID `json:"declaration,omitzero"`
	Versions    []string      `json:"versions,omitempty"`
	Revisions   []RevisionID  `json:"revisions,omitempty"`
}

func cloneConflict(c Conflict) Conflict {
	c.Versions = slices.Clone(c.Versions)
	c.Revisions = slices.Clone(c.Revisions)
	return c
}

// Cycle is a dependency loop, recorded as the revisions on the loop rotated so
// the smallest identity comes first. The walk terminates at a cycle instead of
// following it, and the cycle stays visible instead of disappearing.
type Cycle struct {
	Revisions []RevisionID `json:"revisions"`
}

func cloneCycle(c Cycle) Cycle {
	c.Revisions = slices.Clone(c.Revisions)
	return c
}

// Meta is the catalog's self-description.
type Meta struct {
	SchemaVersion string `json:"schemaVersion"`
	// CatalogID fingerprints the resolved content and topology. It is stable for
	// the same resolved catalog regardless of generation time, root ordering or
	// where a local bundle happens to live on disk.
	CatalogID   string    `json:"catalogId"`
	GeneratedAt time.Time `json:"generatedAt"`
	// Completeness is the honest standing of the whole answer.
	Completeness Completeness `json:"completeness"`
	// Bounds are the bounds that actually applied, after defaults and ceilings.
	Bounds Bounds `json:"bounds"`
	// RequestedRoots is how many roots the caller supplied. It is always known
	// exactly, so it is always truthful, even when MaxRoots dropped some.
	RequestedRoots int          `json:"requestedRoots"`
	Limitations    []Limitation `json:"limitations,omitempty"`
}

// Catalog is an immutable session. Every accessor returns a deep copy, and no
// method mutates state, so concurrent readers are safe and a caller cannot
// reach in through a returned slice, map or struct.
type Catalog struct {
	meta       Meta
	roots      []Root
	revisions  []Revision
	byRevision map[RevisionID]int
	edges      []Edge
	unresolved []Unresolved
	conflicts  []Conflict
	cycles     []Cycle
}

// Meta returns the catalog's self-description.
func (c *Catalog) Meta() Meta {
	m := c.meta
	m.Limitations = slices.Clone(m.Limitations)
	return m
}

// Roots returns every requested root that was resolved or reported, in request
// order.
func (c *Catalog) Roots() []Root { return slices.Clone(c.roots) }

// Revisions returns every retained revision, ordered by content identity and
// then by service, so mirrors of one bundle sit together in a stable order.
func (c *Catalog) Revisions() []Revision {
	out := make([]Revision, len(c.revisions))
	for i, r := range c.revisions {
		out[i] = cloneRevision(r)
	}
	return out
}

// Revision returns one revision by identity.
func (c *Catalog) Revision(id RevisionID) (Revision, bool) {
	i, ok := c.byRevision[id]
	if !ok {
		return Revision{}, false
	}
	return cloneRevision(c.revisions[i]), true
}

// Edges returns every resolved dependency edge, deterministically ordered.
func (c *Catalog) Edges() []Edge { return slices.Clone(c.edges) }

// Unresolved returns every declared dependency that did not resolve. Roots that
// did not resolve are reported by [Catalog.Roots] instead, so each gap has one
// home.
func (c *Catalog) Unresolved() []Unresolved { return slices.Clone(c.unresolved) }

// Conflicts returns every disagreement left visible.
func (c *Catalog) Conflicts() []Conflict {
	out := make([]Conflict, len(c.conflicts))
	for i, cf := range c.conflicts {
		out[i] = cloneConflict(cf)
	}
	return out
}

// Cycles returns every dependency loop the walk terminated at.
func (c *Catalog) Cycles() []Cycle {
	out := make([]Cycle, len(c.cycles))
	for i, cy := range c.cycles {
		out[i] = cloneCycle(cy)
	}
	return out
}
