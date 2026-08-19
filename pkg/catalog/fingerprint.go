package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"slices"

	"github.com/opencontainers/go-digest"
)

// fingerprint derives [Meta.CatalogID] from what the catalog FOUND: resolved
// content and topology.
//
// Three exclusions make the identifier mean that and nothing else:
//
//   - Generation time is excluded, so rebuilding an unchanged catalog a day
//     later produces the same identifier.
//   - Root ordinals and retained paths are excluded, so permuting the requested
//     roots does not change it. Paths are derivable from the roots' contents and
//     the edges anyway, so nothing is lost.
//   - Requested references and local base directories are excluded wherever the
//     catalog resolved them, so the same content resolved from a different path
//     -- or through a tag instead of a digest -- fingerprints the same. The one
//     exception is a root that did NOT resolve: it has no content to be
//     identified by, so the reference it asked for is the only thing telling one
//     gap from another. It is a hashed input, never a rendered one.
//
// Every field is length-prefixed before hashing, so no two different field
// sequences can ever produce one byte stream. A service, domain or reference
// containing "/", ":", "%" or arbitrary UTF-8 therefore cannot collide with its
// neighbours, and no delimiter needs escaping.
func fingerprint(c *Catalog) string {
	w := &fpWriter{h: sha256.New()}
	w.str("pacto.catalog.fingerprint")
	w.str(SchemaVersion)
	w.str(string(c.meta.Completeness))
	w.bounds(c.meta.Bounds)

	// Root outcomes, order-independent: what each root resolved to, never which
	// slot it occupied or what text asked for it.
	outcomes := make([]string, 0, len(c.roots))
	for _, r := range c.roots {
		fields := append([]string{boolStr(r.Resolved)}, revFields(r.Revision)...)
		fields = append(fields, r.Reason.Code)
		if !r.Resolved {
			fields = append(fields, r.RequestedRef)
		}
		outcomes = append(outcomes, encode(fields...))
	}
	slices.Sort(outcomes)
	w.list(outcomes)

	revs := make([]string, 0, len(c.revisions))
	for _, r := range c.revisions {
		fields := []string{
			r.Content.String(), r.Service.Domain, r.Service.Name, r.Version, r.PactoVersion,
			r.Owner.Team, r.Owner.DRI, itoa(r.MinDepth), string(r.Rank),
		}
		for _, ct := range r.Owner.Contacts {
			fields = append(fields, ct.Type, ct.Value, ct.Purpose)
		}
		fields = append(fields, r.ResolvedRefs...)
		revs = append(revs, encode(fields...))
	}
	slices.Sort(revs)
	w.list(revs)

	edges := make([]string, 0, len(c.edges))
	for _, e := range c.edges {
		fields := append(declFields(e.Declaration), e.Name, e.Ref, e.Constraint, boolStr(e.Required))
		edges = append(edges, encode(append(fields, revFields(e.To)...)...))
	}
	slices.Sort(edges)
	w.list(edges)

	unresolved := make([]string, 0, len(c.unresolved))
	for _, u := range c.unresolved {
		fields := append(declFields(u.Declaration), u.Name, u.Ref, u.Constraint, boolStr(u.Required), u.Reason.Code)
		unresolved = append(unresolved, encode(fields...))
	}
	slices.Sort(unresolved)
	w.list(unresolved)

	conflicts := make([]string, 0, len(c.conflicts))
	for _, cf := range c.conflicts {
		fields := []string{string(cf.Kind), cf.Service.Domain, cf.Service.Name, cf.Version}
		fields = append(fields, declFields(cf.Declaration)...)
		fields = append(fields, cf.Versions...)
		for _, id := range cf.Revisions {
			fields = append(fields, revFields(id)...)
		}
		conflicts = append(conflicts, encode(fields...))
	}
	slices.Sort(conflicts)
	w.list(conflicts)

	cycles := make([]string, 0, len(c.cycles))
	for _, cy := range c.cycles {
		fields := make([]string, 0, 3*len(cy.Revisions))
		for _, id := range cy.Revisions {
			fields = append(fields, revFields(id)...)
		}
		cycles = append(cycles, encode(fields...))
	}
	slices.Sort(cycles)
	w.list(cycles)

	// Limitation codes only: the human message and the reference it names are
	// prose and provenance, not content.
	codes := make([]string, 0, len(c.meta.Limitations))
	for _, l := range c.meta.Limitations {
		codes = append(codes, l.Code)
	}
	slices.Sort(codes)
	codes = slices.Compact(codes)
	w.list(codes)

	return string(digest.NewDigest(digest.SHA256, w.h))
}

// revFields frames one revision identity as fingerprint fields. Content alone
// would let two mirrors of one bundle, published by two different services,
// hash alike.
func revFields(id RevisionID) []string {
	return []string{id.Content.String(), id.Service.Domain, id.Service.Name}
}

func declFields(d DeclarationID) []string {
	return append(revFields(d.From), itoa(d.Index))
}

type fpWriter struct{ h hash.Hash }

// str writes one length-prefixed field. hash.Hash never fails a write.
func (w *fpWriter) str(s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	_, _ = w.h.Write(n[:])
	_, _ = w.h.Write([]byte(s))
}

func (w *fpWriter) list(items []string) {
	w.str(itoa(len(items)))
	for _, s := range items {
		w.str(s)
	}
}

func (w *fpWriter) bounds(b Bounds) {
	for _, v := range []int{b.MaxRoots, b.MaxRevisions, b.MaxEdges, b.MaxDepth,
		b.MaxPaths, b.MaxPathLength, b.MaxUnresolved, b.MaxConflicts, b.MaxLimitations} {
		w.str(itoa(v))
	}
}

// encode packs several fields into one sortable string using the same
// length-prefixed framing, so grouping for a sort never introduces the
// delimiter ambiguity that plain joining would.
func encode(fields ...string) string {
	out := make([]byte, 0, 64)
	var n [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		out = append(out, n[:]...)
		out = append(out, f...)
	}
	return string(out)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
