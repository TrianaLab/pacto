package mcp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v3/pkg/capability"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/openapi"
	"github.com/trianalab/pacto/v3/pkg/skills"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// CapabilityOptions configures capability-tool registration for a bundle.
type CapabilityOptions struct {
	BaseURL     string
	Creds       capability.Credentials
	AllowWrites bool
	HTTPClient  *http.Client
	// Resolver is passed through to the authoring tools this server also
	// registers, so pacto_check here runs the same recursive policy resolution
	// `pacto validate` runs. See NewServer.
	Resolver validation.BundleResolver
}

// NewCapabilityServer builds an MCP server that exposes both the authoring tools
// and the bundle's capability tools + pacto_skill. The server's instructions
// describe the capability tools so an agent knows they invoke the live service.
func NewCapabilityServer(bundle *contract.Bundle, opts CapabilityOptions, version string, stderr io.Writer) (*mcpsdk.Server, error) {
	server := newServer(version, baseInstructions+"\n\n"+capabilityInstructions(bundle, opts), opts.Resolver)
	if err := RegisterCapabilities(server, bundle, opts, stderr); err != nil {
		return nil, err
	}
	return server, nil
}

// capabilityInstructions is the server-level guidance describing how to use the
// bundle's generated tools. This is the "built-in capability" knowledge shared
// across all services — it lives in Pacto, not in per-bundle skill files.
func capabilityInstructions(bundle *contract.Bundle, opts CapabilityOptions) string {
	name := bundle.Contract.Service.Name
	var b strings.Builder
	fmt.Fprintf(&b, "This server also exposes the operations of the %q service as executable tools "+
		"derived from its OpenAPI interface: call a tool to invoke the live endpoint and read the "+
		"returned status and body.", name)
	if opts.AllowWrites {
		b.WriteString(" Both read and mutating (POST/PUT/PATCH/DELETE) operations are available.")
	} else {
		b.WriteString(" Only read-only (GET/HEAD) operations are available; mutating operations are " +
			"hidden unless the server was started with --allow-writes.")
	}
	b.WriteString(" Call pacto_skill with no arguments to list the service's domain skills " +
		"(workflows and business rules the interface cannot express), or with a skill name to read one.")
	return b.String()
}

// RegisterCapabilities registers one executable MCP tool per OpenAPI operation
// in the bundle's http interfaces, plus a pacto_skill tool exposing skills/*.md.
// Mutating operations (POST/PUT/PATCH/DELETE) are only registered when
// opts.AllowWrites is set.
func RegisterCapabilities(server *mcpsdk.Server, bundle *contract.Bundle, opts CapabilityOptions, stderr io.Writer) error {
	// Refuse to send operator credentials to a host chosen by (possibly
	// untrusted) bundle content: require an explicit --base-url alongside --auth.
	if len(opts.Creds) > 0 && opts.BaseURL == "" {
		return fmt.Errorf("--auth requires an explicit --base-url (refusing to send credentials to a bundle-declared server)")
	}
	ifaces := httpInterfaces(bundle.Contract)
	for _, iface := range ifaces {
		if err := registerInterface(server, bundle.FS, iface, len(ifaces) > 1, opts, stderr); err != nil {
			return err
		}
	}
	mcpsdk.AddTool(server, skillTool(), skillHandler(bundle.FS))
	return nil
}

func httpInterfaces(c *contract.Contract) []contract.Interface {
	var out []contract.Interface
	for _, iface := range c.Interfaces {
		if iface.Type == contract.InterfaceTypeOpenAPI && iface.Ref != "" {
			out = append(out, iface)
		}
	}
	return out
}

func registerInterface(server *mcpsdk.Server, fsys fs.FS, iface contract.Interface, prefixed bool, opts CapabilityOptions, stderr io.Writer) error {
	doc, err := openapi.ReadDoc(fsys, iface.Ref)
	if err != nil {
		return fmt.Errorf("interface %q: %w", iface.Name, err)
	}
	base := opts.BaseURL
	if base == "" && len(doc.Servers) > 0 {
		base = doc.Servers[0]
	}
	if base == "" {
		return fmt.Errorf("interface %q: no base URL (set --base-url or add servers[] to the spec)", iface.Name)
	}

	tools := capability.BuildTools(doc, opts.AllowWrites)
	if !opts.AllowWrites {
		if n := countMutating(doc); n > 0 {
			_, _ = fmt.Fprintf(stderr, "pacto mcp: skipped %d mutating operation(s) in interface %q (use --allow-writes to expose)\n", n, iface.Name)
		}
	}

	prefix := ""
	if prefixed {
		prefix = iface.Name + "_"
	}
	for _, tool := range tools {
		name := prefix + tool.Name
		// operationId is bundle content, and the SDK's AddTool REPLACES a tool of the
		// same name. A contract declaring `operationId: pacto_check` therefore used to
		// take over the authoring tool, so an agent asking Pacto to validate a
		// contract issued a bundle-chosen HTTP request to a bundle-chosen host while
		// the tool list still showed the trusted description. The operation is skipped
		// rather than renamed: a silently renamed tool is a capability nobody asked
		// for, and the bundle can simply pick a name of its own.
		if reservedToolNames[name] {
			_, _ = fmt.Fprintf(stderr, "pacto mcp: skipped operation %q in interface %q (that name belongs to a Pacto tool)\n", name, iface.Name)
			continue
		}
		decl := &mcpsdk.Tool{
			Name:        name,
			Description: toolDesc(tool),
			InputSchema: tool.InputSchema,
		}
		if err := addCapabilityTool(server, decl, capabilityHandler(tool.Op, doc, base, opts)); err != nil {
			_, _ = fmt.Fprintf(stderr, "pacto mcp: operation %q in interface %q is unavailable (%v)\n", name, iface.Name, err)
			registerUnavailable(server, decl, err)
		}
	}
	return nil
}

// addCapabilityTool registers a bundle-derived tool through the generic AddTool,
// the only form that validates a call against the declared schema before the
// handler runs. The raw Server.AddTool does not, so a required path parameter the
// agent omitted used to reach the live service with "{id}" left literal in the
// URL — a mutating request reported back as a success.
//
// The generic form panics when the declared schema does not resolve, and these
// schemas are built from bundle content, so any bundle could otherwise take the
// whole server down. Bundle-authored draft-4 bounds are translated first (see
// normalizeDraft4Bounds); anything still unresolvable is recovered and handed to
// registerUnavailable, since registering it raw is the defect above.
func addCapabilityTool(server *mcpsdk.Server, decl *mcpsdk.Tool, h mcpsdk.ToolHandlerFor[map[string]any, any]) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	normalizeDraft4Bounds(decl.InputSchema)
	mcpsdk.AddTool(server, decl, h)
	return nil
}

// normalizeDraft4Bounds rewrites draft-4's boolean exclusiveMinimum/
// exclusiveMaximum into the draft 2020-12 numeric form, in place, at any depth.
// The boolean form is not exotic: draft-4 is the only dialect OpenAPI 3.0
// allows, so it is how every 3.0 spec writes an exclusive bound — and the SDK
// unmarshals the declared schema into a 2020-12 struct, where the keyword is the
// bound itself, and rejects the boolean. Without this, a plain 3.0 bundle loses
// every operation that bounds a number.
//
// ponytail: the walk is dialect-blind — it rewrites the keyword wherever a
// boolean sits under it, including inside a literal `example`. That costs a
// cosmetic edit to an example and never misses a real bound; teach it the
// keyword vocabulary if a spec ever cares.
func normalizeDraft4Bounds(v any) {
	switch t := v.(type) {
	case map[string]any:
		convertExclusiveBound(t, "exclusiveMinimum", "minimum")
		convertExclusiveBound(t, "exclusiveMaximum", "maximum")
		for _, val := range t {
			normalizeDraft4Bounds(val)
		}
	case []any:
		for _, e := range t {
			normalizeDraft4Bounds(e)
		}
	}
}

// convertExclusiveBound rewrites one boolean bound: true adopts its sibling's
// value and consumes it, while false — or a boolean with no sibling to point at,
// which draft-4 does not define — means there is no exclusive bound at all.
func convertExclusiveBound(m map[string]any, key, sibling string) {
	b, isBool := m[key].(bool)
	if !isBool {
		return
	}
	if bound, ok := m[sibling]; b && ok {
		m[key] = bound
		delete(m, sibling)
		return
	}
	delete(m, key)
}

// registerUnavailable keeps an operation Pacto could not expose visible to the
// agent that has to reason about it. A tool that simply disappears is
// indistinguishable from an operation the service never had, and the stderr line
// reaches whoever launched the server, not the caller. The stand-in takes no
// arguments and never touches the network: every call answers with the reason.
func registerUnavailable(server *mcpsdk.Server, decl *mcpsdk.Tool, cause error) {
	reason := fmt.Errorf("operation %q is declared by the contract but Pacto could not expose it: %w", decl.Name, cause)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        decl.Name,
		Description: "UNAVAILABLE (" + decl.Description + "): Pacto could not register this operation's schema. Calling it reports why; it does not reach the service.",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
		return errorResult(reason), nil, nil
	})
}

// reservedToolNames are the tool names Pacto itself registers across every server
// variant (authoring, fleet, impact, catalog, skills). Bundle-derived capability
// tools may not use them. TestReservedToolNames_CoversEveryPactoTool reads a
// fully-loaded server back and fails if this set falls behind.
var reservedToolNames = map[string]bool{
	"pacto_create": true, "pacto_edit": true, "pacto_check": true, "pacto_schema": true,
	"pacto_fleet_search": true, "pacto_fleet_get": true, "pacto_fleet_graph": true,
	"pacto_fleet_status": true, "pacto_fleet_explain": true,
	"pacto_impact": true, "pacto_catalog_revision": true, "pacto_skill": true,
}

func countMutating(doc *openapi.Doc) int {
	n := 0
	for _, op := range doc.Operations {
		if capability.IsMutating(op.Method) {
			n++
		}
	}
	return n
}

func toolDesc(tool capability.Tool) string {
	switch {
	case tool.Summary != "":
		return tool.Summary
	case tool.Description != "":
		return tool.Description
	default:
		return tool.Method + " " + tool.Path
	}
}

// capabilityHandler invokes the live operation. Arguments arrive already
// validated against the operation's declared schema (see addCapabilityTool), so
// a missing required parameter or a wrong-typed one never reaches the service.
func capabilityHandler(op openapi.Operation, doc *openapi.Doc, baseURL string, opts CapabilityOptions) mcpsdk.ToolHandlerFor[map[string]any, any] {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, args map[string]any) (*mcpsdk.CallToolResult, any, error) {
		res, err := capability.Invoke(ctx, opts.HTTPClient, op, doc, baseURL, args, opts.Creds)
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
	}
}

func skillTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_skill",
		Description: "Lists the bundle's domain skills (skills/*.md) when called with no arguments, " +
			"or returns one skill's markdown content when given its name.",
		InputSchema: inputSchema(map[string]property{
			"name": {Type: "string", Description: "Skill file name (e.g. refund_customer.md). Omit to list all skills."},
		}, nil),
	}
}

type skillArgs struct {
	Name string `json:"name"`
}

func skillHandler(fsys fs.FS) mcpsdk.ToolHandlerFor[skillArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a skillArgs) (*mcpsdk.CallToolResult, any, error) {
		if a.Name == "" {
			names, _ := skills.List(fsys)
			r, err := jsonResult(names)
			return r, nil, err
		}
		content, err := skills.Read(fsys, a.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(content), nil, nil
	}
}
