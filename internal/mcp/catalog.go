package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v3/pkg/catalog"
)

// Resource URIs for the catalog discovery surface. They are fixed, so a client
// discovers them by listing rather than by constructing one, and no part of a
// revision identity is ever encoded into a path segment.
const (
	catalogURI        = "pacto://catalog"
	catalogClosureURI = "pacto://catalog/closure"
	catalogMIMEType   = "application/json"
)

// catalogInstructions describe the read-only catalog discovery surface and,
// just as importantly, what it is not: it is not the operational fleet, it is
// not an authorization decision and it is not a way to run anything.
//
// They stand alone rather than extending the authoring instructions, because
// this server registers no authoring tools. Instructions that named a tool the
// server does not have would be an invitation to a call that cannot succeed.
const catalogInstructions = "This Pacto server exposes exactly one thing: a READ-ONLY contract catalog — the " +
	"explicit contract roots it was started with, plus their dependency closure, resolved once at " +
	"startup and frozen. It has no contract-authoring tools, so nothing reachable here creates, edits " +
	"or writes anything. " +
	"Read the resource pacto://catalog for the schema version, catalog id, generation " +
	"time, the bounds that applied, the completeness of the whole answer and every requested root, " +
	"including roots that did not resolve and why. Read pacto://catalog/closure for the revisions, " +
	"resolved dependency edges, unresolved dependencies, conflicts and cycles. It is the cheaper read " +
	"first, not a precondition: both resources carry the same catalog metadata, so either one is safe " +
	"to read on its own. Call " +
	"pacto_catalog_revision to look one revision up by its full identity (service name, domain, " +
	"content scheme and content digest) instead of reading the whole closure. " +
	"The catalog is NOT the fleet: it describes contracts discovered from the roots you supplied, " +
	"never runtime targets, deployments or observed state — use the fleet tools for those. " +
	"Distinguish a requested reference from the immutable reference it resolved to from the content " +
	"identity; only the last is identity. Treat 'partial' as incomplete knowledge: a revision the " +
	"catalog does not hold is unknown, not proven absent. Nothing here is authorization and nothing " +
	"here executes anything; discovering a revision does not mean you may read, deploy or call it."

// NewCatalogServer builds a server whose entire surface is the read-only
// contract catalog discovery surface over cat: two resources and one lookup
// tool, and nothing else.
//
// It deliberately does not go through newServer. Catalog mode is a read-only
// knowledge surface, and the authoring tools are not read-only — pacto_create
// and pacto_edit write contract files to disk. Registering them here would hand
// a client that asked for discovery a way to modify the filesystem, so this
// mode starts from a bare server and adds only what discovery needs.
//
// cat is a frozen session: it was resolved once, before this server existed,
// and nothing below can resolve, refresh or rebuild it. Every handler is a pure
// read of a deep copy, so a registry tag that moves while this server runs does
// not move any answer it gives.
func NewCatalogServer(version string, cat *catalog.Catalog) *mcpsdk.Server {
	server := newBareServer(version, catalogInstructions)
	registerCatalogSurface(server, cat)
	return server
}

// registerCatalogSurface adds the two discovery resources and the one identity
// query.
//
// The split is by question, not by convenience: pacto://catalog answers "what
// is this catalog, what was asked for and how much of it is known", which is
// small and the cheaper first read; pacto://catalog/closure answers "what is in
// it", which is bounded but far larger. Serving the completeness only as part
// of the large half would make it expensive to check, and it is the part an
// agent must never skip.
//
// Both therefore carry the same catalog.Meta. The repetition is deliberate: a
// resource can be read on its own, in any order, so a payload that carried only
// data would be indistinguishable from an authoritative answer whenever the
// data happened to be empty. Every independently readable payload states its
// own epistemic standing instead of borrowing it from a read that may never
// happen.
//
// Everything else is a resource read, so the single tool has to earn its place:
// a revision's identity is four structured fields the catalog core deliberately
// never joins into a string, and a domain or service name may contain "/", ":",
// "%" or arbitrary UTF-8. A resource template would force exactly the ambiguous
// encoding and ad hoc re-parsing that identity discipline exists to prevent, so
// the identity-keyed lookup takes structured tool inputs instead.
func registerCatalogSurface(server *mcpsdk.Server, cat *catalog.Catalog) {
	server.AddResource(&mcpsdk.Resource{
		URI:      catalogURI,
		Name:     "pacto_catalog",
		Title:    "Pacto contract catalog",
		MIMEType: catalogMIMEType,
		Description: "Catalog metadata and every requested contract root. Carries the schema version, " +
			"catalog id, generation time, the bounds that applied, the completeness of the whole answer, " +
			"and each root with the reference requested, the immutable reference it resolved to, the " +
			"revision identity it became, or the structured reason it did not resolve. Read-only.",
	}, catalogOverviewHandler(cat))

	server.AddResource(&mcpsdk.Resource{
		URI:      catalogClosureURI,
		Name:     "pacto_catalog_closure",
		Title:    "Pacto contract catalog closure",
		MIMEType: catalogMIMEType,
		Description: "The dependency closure of the requested roots: every deduplicated revision with " +
			"its content identity, rank and every retained root-to-revision path, every resolved " +
			"dependency edge, every dependency that did not resolve, and the conflicts and cycles left " +
			"visible rather than resolved. Read-only.",
	}, catalogClosureHandler(cat))

	server.AddTool(catalogRevisionTool(), catalogRevisionHandler(cat))
}

// catalogOverview is the cheap half of the catalog: what it is and what was
// asked for. It is an envelope over the accepted catalog model, not a second
// weaker model of it.
type catalogOverview struct {
	Meta  catalog.Meta   `json:"meta"`
	Roots []catalog.Root `json:"roots"`
}

// catalogClosure is the bounded half: what the roots turned out to contain,
// including everything that is known to be missing or contradictory. Meta is
// the same metadata the overview carries, so an empty closure can be told apart
// from a complete one without a second read.
type catalogClosure struct {
	Meta       catalog.Meta         `json:"meta"`
	Revisions  []catalog.Revision   `json:"revisions"`
	Edges      []catalog.Edge       `json:"edges"`
	Unresolved []catalog.Unresolved `json:"unresolved"`
	Conflicts  []catalog.Conflict   `json:"conflicts"`
	Cycles     []catalog.Cycle      `json:"cycles"`
}

// catalogRevisionAnswer is one identity lookup. Found and Completeness are both
// present on every answer because a miss means two different things: in a
// complete catalog the revision is not there, and in a partial one it is
// unknown. Reporting only the miss would let a caller read the first meaning
// into the second.
type catalogRevisionAnswer struct {
	Found        bool                 `json:"found"`
	Completeness catalog.Completeness `json:"completeness"`
	Requested    catalog.RevisionID   `json:"requested"`
	Revision     *catalog.Revision    `json:"revision,omitempty"`
}

func catalogOverviewHandler(cat *catalog.Catalog) mcpsdk.ResourceHandler {
	return func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return jsonResource(req.Params.URI, catalogOverview{
			Meta:  cat.Meta(),
			Roots: orEmpty(cat.Roots()),
		})
	}
}

func catalogClosureHandler(cat *catalog.Catalog) mcpsdk.ResourceHandler {
	return func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return jsonResource(req.Params.URI, catalogClosure{
			Meta:       cat.Meta(),
			Revisions:  orEmpty(cat.Revisions()),
			Edges:      orEmpty(cat.Edges()),
			Unresolved: orEmpty(cat.Unresolved()),
			Conflicts:  orEmpty(cat.Conflicts()),
			Cycles:     orEmpty(cat.Cycles()),
		})
	}
}

func catalogRevisionTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_catalog_revision",
		Description: "Look up one contract revision in the catalog by its full identity: the " +
			"domain-qualified service it belongs to and its immutable content identity. Returns the " +
			"revision with every requested and resolved reference, its rank and every retained path, " +
			"or a miss qualified by the catalog's completeness. Read-only; resolves nothing.",
		InputSchema: inputSchema(map[string]property{
			"name":   {Type: "string", Description: "Service name exactly as the revision reports it"},
			"domain": {Type: "string", Description: "Domain qualifying the service name; omit for a local revision, which has none"},
			"scheme": {Type: "string", Description: "Content identity scheme", Enum: []string{string(catalog.SchemeOCI), string(catalog.SchemeLocal)}},
			"digest": {Type: "string", Description: "Content digest as <algorithm>:<hex>; a tag or a version is not a content identity"},
		}, []string{"name", "scheme", "digest"}),
	}
}

func catalogRevisionHandler(cat *catalog.Catalog) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		// The content identity is validated rather than trusted, so a tag or a
		// version arriving here is refused instead of quietly missing.
		content, err := catalog.NewContentID(
			catalog.ContentScheme(parseInput(req, "scheme")),
			parseInput(req, "digest"),
		)
		if err != nil {
			return errorResult(err), nil
		}
		id := catalog.RevisionID{
			Service: catalog.ServiceID{Domain: parseInput(req, "domain"), Name: parseInput(req, "name")},
			Content: content,
		}
		answer := catalogRevisionAnswer{Completeness: cat.Meta().Completeness, Requested: id}
		if rev, ok := cat.Revision(id); ok {
			answer.Found, answer.Revision = true, &rev
		}
		return jsonResult(answer)
	}
}

// jsonResource marshals v as the JSON body of one resource read.
func jsonResource(uri string, v any) (*mcpsdk.ReadResourceResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling resource %s: %w", uri, err)
	}
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{
			{URI: uri, MIMEType: catalogMIMEType, Text: string(data)},
		},
	}, nil
}

// orEmpty renders an absent collection as [] rather than null, so "the catalog
// holds none of these" reads the same as any other list to a client iterating
// it. Absence inside the catalog model keeps that model's own encoding.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
