package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v3/internal/app"
	pactomcp "github.com/trianalab/pacto/v3/internal/mcp"
	"github.com/trianalab/pacto/v3/pkg/capability"
	"github.com/trianalab/pacto/v3/pkg/catalog"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

func newMCPCommand(svc *app.Service, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp [bundle-ref]",
		Short: "Start an MCP server",
		Long: "Starts a Model Context Protocol (MCP) server exposing Pacto tools for AI agents. " +
			"Supports stdio (default) and HTTP transports.\n\n" +
			"When a bundle reference (local directory or oci:// ref) is given, the server also " +
			"exposes one executable tool per OpenAPI operation in the bundle's http interfaces, " +
			"plus a pacto_skill tool for any skills/*.md domain knowledge. Read-only (GET/HEAD) " +
			"operations are exposed by default; pass --allow-writes to expose mutating operations.\n\n" +
			"With --root, the server instead exposes a read-only contract catalog: the given roots " +
			"plus their dependency closure, resolved once at startup and then frozen, so a registry " +
			"tag that moves later does not change the session. Roots that do not resolve stay visible " +
			"as partial knowledge. Discovery is not authorization and nothing in the catalog executes.\n\n" +
			"A bundle reference, --root and --fleet select different servers and cannot be combined.",
		Example: `  # Start MCP server over stdio (default)
  pacto mcp

  # Start MCP server over HTTP
  pacto mcp -t http

  # Expose a bundle's OpenAPI operations as agent tools
  pacto mcp ./my-service --base-url https://api.example.com

  # Include mutating operations and an auth credential
  pacto mcp oci://ghcr.io/acme/svc:1.0.0 --base-url https://api.example.com \
    --auth bearerAuth=$TOKEN --allow-writes

  # Expose a contract catalog discovered from explicit roots
  pacto mcp --root oci://ghcr.io/acme/platform:1.4.0 --root ./experimental-platform`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			transport, _ := cmd.Flags().GetString("transport")
			port, _ := cmd.Flags().GetInt("port")
			server, err := buildMCPServer(cmd, svc, version, args)
			if err != nil {
				return err
			}
			return runMCPServer(cmd.Context(), server, transport, port, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().StringP("transport", "t", "stdio", "transport type: stdio or http")
	cmd.Flags().Int("port", 8585, "port for HTTP transport")
	cmd.Flags().String("base-url", "", "base URL for live invocation (overrides the OpenAPI servers[] URL)")
	cmd.Flags().StringArray("auth", nil, "credential for a security scheme as name=value (repeatable)")
	cmd.Flags().Bool("allow-writes", false, "expose mutating operations (POST/PUT/PATCH/DELETE) as tools")
	cmd.Flags().Bool("fleet", false, "expose read-only operational-graph (fleet) query tools")
	cmd.Flags().StringArray("root", nil, "contract root to discover a read-only catalog from: a local bundle path or an oci:// reference (repeatable)")
	cmd.Flags().StringArray("local", []string{"."}, "local bundle root(s) for --fleet (repeatable)")
	cmd.Flags().StringArray("target-state", nil, "offline target-state fixture file(s) for --fleet — a demo/test adapter (repeatable)")
	cmd.Flags().StringArray("evidence-url", nil, "base URL of an Evidence Server to consume over HTTP for --fleet (repeatable)")
	cmd.Flags().StringArray("oci", nil, "registry reference to include as a published-baseline revision for --fleet (repeatable)")
	cmd.Flags().Bool("cache", false, "include the local OCI cache as offline baseline revisions (--fleet)")
	cmd.Flags().Bool("k8s", false, "include live Pacto CRs from the current Kubernetes cluster (--fleet)")
	cmd.Flags().String("namespace", "", "namespace for --k8s (empty = all namespaces)")
	cmd.Flags().Duration("freshness", 0, "mark target evidence older than this as stale (--fleet)")

	return cmd
}

// buildMCPServer constructs the MCP server for the one mode the invocation
// selected: catalog discovery from explicit roots, fleet queries, a bundle's
// capability tools, or the authoring tools when nothing else is asked for.
func buildMCPServer(cmd *cobra.Command, svc *app.Service, version string, args []string) (*mcpsdk.Server, error) {
	roots, _ := cmd.Flags().GetStringArray("root")
	fleetMode, _ := cmd.Flags().GetBool("fleet")
	// The mode is selected by --root being present, not by the values surviving:
	// pflag round-trips a repeated flag through CSV, so a lone `--root ""` comes
	// back as no roots at all. That has to fail, not turn into a different server.
	catalogMode := cmd.Flags().Changed("root")
	if err := checkMCPMode(len(args) > 0, catalogMode, fleetMode); err != nil {
		return nil, err
	}

	switch {
	case catalogMode:
		cat, err := buildCatalog(cmd, svc, roots)
		if err != nil {
			return nil, err
		}
		return pactomcp.NewCatalogServer(version, cat), nil
	case fleetMode:
		q, err := buildQuery(cmd, svc)
		if err != nil {
			return nil, err
		}
		return pactomcp.NewFleetServer(version, q, mcpImpactProvider(cmd, svc)), nil
	case len(args) > 0:
		return buildCapabilityServer(cmd, svc, version, args[0])
	}
	return pactomcp.NewServer(svc, version), nil
}

// checkMCPMode rejects an invocation that selects more than one server. The
// modes expose disjoint surfaces, so honouring one and dropping the rest would
// hand a client a server it did not ask for; saying so is the only safe answer.
func checkMCPMode(hasBundle, hasRoots, fleetMode bool) error {
	var selected []string
	if hasBundle {
		selected = append(selected, "a bundle reference")
	}
	if hasRoots {
		selected = append(selected, "--root")
	}
	if fleetMode {
		selected = append(selected, "--fleet")
	}
	if len(selected) > 1 {
		return fmt.Errorf("%s cannot be combined: each selects a different MCP server, so pass only one",
			strings.Join(selected, ", "))
	}
	return nil
}

// errRootNeedsRef is returned when catalog mode was selected without a usable
// reference. An empty root set fails closed: an empty catalog would otherwise
// read as an authoritative "there is nothing here".
var errRootNeedsRef = errors.New("--root needs a reference: a local bundle path or an oci:// reference")

// buildCatalog resolves the requested roots into a frozen catalog session,
// exactly once, before any client can connect.
//
// Resolution goes through the service's catalog resolver, so reference parsing,
// registry credentials, the OCI cache, digest pinning and local hashing are the
// ones the rest of the CLI already uses. Nothing here re-implements them and
// nothing reaches a registry again once this returns.
func buildCatalog(cmd *cobra.Command, svc *app.Service, roots []string) (*catalog.Catalog, error) {
	if len(roots) == 0 {
		return nil, errRootNeedsRef
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, errRootNeedsRef
		}
	}
	return catalog.Build(cmd.Context(), catalog.Request{
		Roots:    roots,
		Resolver: svc.CatalogResolver(),
	})
}

// buildCapabilityServer exposes one bundle's OpenAPI operations and skills.
func buildCapabilityServer(cmd *cobra.Command, svc *app.Service, version, ref string) (*mcpsdk.Server, error) {
	bundle, err := svc.ResolveBundle(cmd.Context(), ref)
	if err != nil {
		return nil, err
	}
	authPairs, _ := cmd.Flags().GetStringArray("auth")
	creds, err := parseAuthFlags(authPairs)
	if err != nil {
		return nil, err
	}
	baseURL, _ := cmd.Flags().GetString("base-url")
	allowWrites, _ := cmd.Flags().GetBool("allow-writes")
	opts := pactomcp.CapabilityOptions{
		BaseURL:     baseURL,
		Creds:       creds,
		AllowWrites: allowWrites,
	}
	return pactomcp.NewCapabilityServer(bundle, opts, version, cmd.ErrOrStderr())
}

// mcpImpactProvider builds the impact provider for pacto_impact from svc.Impact,
// using the same fleet source flags as the fleet query. Extracted so the wiring
// is directly testable. It resolves the old/new refs and builds the snapshot on
// each call, so the tool always analyzes against fresh contract and graph state.
func mcpImpactProvider(cmd *cobra.Command, svc *app.Service) func(ctx context.Context, oldRef, newRef string, includeObserved bool, tracesPath string) (*impact.Result, error) {
	fopts := fleetOptions(cmd)
	return func(ctx context.Context, oldRef, newRef string, includeObserved bool, tracesPath string) (*impact.Result, error) {
		var traces []byte
		if tracesPath != "" {
			var err error
			if traces, err = os.ReadFile(tracesPath); err != nil {
				return nil, err
			}
			includeObserved = true
		}
		return svc.Impact(ctx, app.ImpactOptions{
			OldPath: oldRef, NewPath: newRef, Fleet: fopts, IncludeObserved: includeObserved, Traces: traces,
		})
	}
}

// parseAuthFlags parses repeated name=value credential flags.
func parseAuthFlags(pairs []string) (capability.Credentials, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	creds := capability.Credentials{}
	for _, p := range pairs {
		name, value, ok := strings.Cut(p, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid --auth %q: expected name=value", p)
		}
		creds[name] = value
	}
	return creds, nil
}

func runMCPServer(ctx context.Context, server *mcpsdk.Server, transport string, port int, stderr io.Writer) error {
	if transport == "http" {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "MCP server listening on http://%s/mcp\n", addr)
		return serveHTTP(ctx, server, listener)
	}
	_, _ = fmt.Fprintln(stderr, "MCP server running on stdio")
	return server.Run(ctx, &mcpsdk.StdioTransport{})
}

func serveHTTP(ctx context.Context, server *mcpsdk.Server, listener net.Listener) error {
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcpsdk.Server { return server },
		nil,
	)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			handler.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}
