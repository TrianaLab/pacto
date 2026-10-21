package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/capability"
	"github.com/trianalab/pacto/v4/pkg/catalog"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

func newMCPCommand(svc *app.Service, version string, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp [bundle-ref]",
		Short: "Start an MCP server",
		Long: "Starts a Model Context Protocol (MCP) server exposing Pacto tools for AI agents. " +
			"Supports stdio (default) and HTTP transports.\n\n" +
			"When a bundle reference (local directory or oci:// ref) is given, the server also " +
			"exposes one executable tool per OpenAPI operation in the bundle's openapi interfaces, " +
			"plus a pacto_skill tool for any skills/*.md domain knowledge. Read-only (GET/HEAD) " +
			"operations are exposed by default; pass --allow-writes to expose mutating operations.\n\n" +
			"With --root, the server instead exposes a read-only contract catalog: the given roots " +
			"plus their dependency closure, resolved once at startup and then frozen, so a registry " +
			"tag that moves later does not change the session. Roots that do not resolve stay visible " +
			"as partial knowledge. Discovery is not authorization and nothing in the catalog executes.\n\n" +
			"With --fleet, the server instead exposes the read-only operational-graph query tools " +
			"over one snapshot. The shared fleet source flags — --local, --target-state, " +
			"--evidence-url, --traces, --oci, --cache, --k8s, --namespace and --freshness — compose " +
			"that snapshot and are read in --fleet mode only.\n\n" +
			"With --delivery, the server instead exposes the Pacto delivery tools (pacto_delivery_*) over the " +
			"ambient cluster: operation types, describe, plan and get, plus propose and cancel only when the " +
			"apiserver says the caller is an agent (a service account or an --agent-groups member). There is " +
			"no approve tool.\n\n" +
			"Over HTTP, --auth-issuer and --resource make every request carry a bearer token the server verifies " +
			"(issuer, audience, expiry) and publish protected-resource metadata (RFC 9728); a bind beyond loopback " +
			"needs them and refuses --auth and --allow-writes, which would lend this process's service credential to every caller. With --delivery the server then exchanges each caller's token for one the apiserver accepts " +
			"(RFC 8693, --exchange-client-id) and builds the tools for that caller on every request, so an agent " +
			"proposes as itself and a person gets the read-only tools.\n\n" +
			"With --publish, the authoring server also exposes pacto_publish_plan and pacto_publish, which publish " +
			"a contract's version exactly as `pacto publish` does, with this process's registry credentials.\n\n" +
			"A bundle reference, --root, --fleet, --delivery and --publish select different servers and cannot be combined.",
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
			s := mcpServe{}
			s.transport, _ = cmd.Flags().GetString("transport")
			s.port, _ = cmd.Flags().GetInt("port")
			if err := checkMCPTransport(s.transport); err != nil {
				return err
			}
			if pub, _ := cmd.Flags().GetBool("publish"); pub && s.transport != "stdio" {
				return errPublishOverHTTP
			}
			if err := mcpRemote(cmd, d, &s); err != nil {
				return err
			}
			if dm, _ := cmd.Flags().GetBool("delivery"); dm && s.transport != "stdio" {
				fleetMode, _ := cmd.Flags().GetBool("fleet")
				if err := checkMCPMode(len(args) > 0, cmd.Flags().Changed("root"), fleetMode, true, false); err != nil {
					return err
				}
				if err := remoteDeliveryFor(cmd, svc, version, d, &s); err != nil {
					return err
				}
				return runMCPServer(cmd.Context(), s, cmd.ErrOrStderr())
			}
			var err error
			if s.server, err = buildMCPServer(cmd, svc, version, args, d); err != nil {
				return err
			}
			return runMCPServer(cmd.Context(), s, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().StringP("transport", "t", "stdio", "transport type: stdio or http")
	_ = cmd.RegisterFlagCompletionFunc("transport", staticCompletions(mcpTransports...))
	cmd.Flags().Int("port", 8585, "port for HTTP transport")
	cmd.Flags().String("host", "127.0.0.1", "bind address for HTTP transport; beyond loopback it needs --auth-issuer")
	cmd.Flags().String("auth-issuer", "", "OpenID issuer whose bearer tokens every HTTP request must carry")
	cmd.Flags().String("resource", "", "with --auth-issuer: this endpoint's URL as clients reach it, served as protected-resource metadata (RFC 9728)")
	cmd.Flags().String("audience", "", "with --auth-issuer: the audience incoming tokens must carry (default --resource)")
	cmd.Flags().String("exchange-client-id", "", "with --delivery over HTTP: the server's own OAuth client, which exchanges each caller's token for the cluster "+
		"(RFC 8693); its secret comes from PACTO_MCP_EXCHANGE_CLIENT_SECRET")
	cmd.Flags().String("exchange-audience", "pacto-cli", "with --delivery over HTTP: the audience to exchange for, one the apiserver accepts")
	cmd.Flags().String("base-url", "", "base URL for live invocation (overrides the OpenAPI servers[] URL)")
	cmd.Flags().StringArray("auth", nil, "credential for a security scheme as name=value (repeatable)")
	cmd.Flags().Bool("allow-writes", false, "expose mutating operations (POST/PUT/PATCH/DELETE) as tools")
	cmd.Flags().Bool("fleet", false, "expose read-only operational-graph (fleet) query tools")
	cmd.Flags().Bool("delivery", false, "expose the Pacto delivery tools over the ambient cluster")
	cmd.Flags().Bool("publish", false, "expose pacto_publish_plan and pacto_publish beside the authoring tools")
	cmd.Flags().StringSlice("agent-groups", []string{"pacto:agents"}, "in --delivery mode: groups whose members are agents")
	cmd.Flags().StringSlice("break-glass-groups", []string{"pacto:break-glass"}, "in --delivery mode: groups that may override")
	cmd.Flags().String("delivery-trust", "", "in --delivery mode: the trust config delivery records are verified with "+
		"(the recorder's PEM public key or a YAML trust config; default $"+trustEnv+")")
	// Declared before the shared set so this spelling wins: here --root selects a
	// catalog to discover, not a closure to fold into a snapshot.
	cmd.Flags().StringArray("root", nil, "contract root to discover a read-only catalog from: a local bundle path or an oci:// reference (repeatable)")
	// The rest are the fleet source flags, from the one declaration `pacto fleet`
	// uses, and they apply in --fleet mode only.
	addFleetSourceFlags(cmd.Flags())

	return cmd
}

// buildMCPServer constructs the MCP server for the one mode the invocation
// selected: catalog discovery from explicit roots, fleet queries, a bundle's
// capability tools, or the authoring tools when nothing else is asked for.
func buildMCPServer(cmd *cobra.Command, svc *app.Service, version string, args []string, d deps) (*mcpsdk.Server, error) {
	roots, _ := cmd.Flags().GetStringArray("root")
	fleetMode, _ := cmd.Flags().GetBool("fleet")
	deliveryMode, _ := cmd.Flags().GetBool("delivery")
	publishMode, _ := cmd.Flags().GetBool("publish")
	// The mode is selected by --root being present, not by the values surviving:
	// pflag round-trips a repeated flag through CSV, so a lone `--root ""` comes
	// back as no roots at all. That has to fail, not turn into a different server.
	catalogMode := cmd.Flags().Changed("root")
	if err := checkMCPMode(len(args) > 0, catalogMode, fleetMode, deliveryMode, publishMode); err != nil {
		return nil, err
	}

	switch {
	case publishMode:
		return pactomcp.NewPublishServer(version, mcpPublish(svc, d), svc.ValidationReport), nil
	case deliveryMode:
		return buildDeliveryServer(cmd, svc, version, d)
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
		return pactomcp.NewFleetServer(version, q, mcpImpactProvider(cmd, svc), svc.ValidationReport), nil
	case len(args) > 0:
		return buildCapabilityServer(cmd, svc, version, args[0])
	}
	return pactomcp.NewServer(svc.ValidationReport, version), nil
}

// checkMCPMode rejects an invocation that selects more than one server. The
// modes expose disjoint surfaces, so honouring one and dropping the rest would
// hand a client a server it did not ask for; saying so is the only safe answer.
func checkMCPMode(hasBundle, hasRoots, fleetMode, deliveryMode, publishMode bool) error {
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
	if deliveryMode {
		selected = append(selected, "--delivery")
	}
	if publishMode {
		selected = append(selected, "--publish")
	}
	if len(selected) > 1 {
		return fmt.Errorf("%s cannot be combined: each selects a different MCP server, so pass only one",
			strings.Join(selected, ", "))
	}
	return nil
}

// buildDeliveryServer exposes the delivery tools. Write tools register only when
// the apiserver says the caller is an agent: an agent must run under its own
// identity, and a person's credentials get the read-only surface.
func buildDeliveryServer(cmd *cobra.Command, svc *app.Service, version string, d deps) (*mcpsdk.Server, error) {
	c, _, err := deliveryClient(cmd, d)
	if err != nil {
		return nil, err
	}
	caller, err := c.WhoAmI(cmd.Context())
	if err != nil {
		return nil, err
	}
	return pactomcp.NewDeliveryServer(version, mcpDelivery{c}, caller.Class == delivery.ClassAgent, svc.ValidationReport), nil
}

// mcpDelivery adapts app.Delivery to the MCP tools' interface.
type mcpDelivery struct{ *app.Delivery }

func (m mcpDelivery) Plan(ctx context.Context, req delivery.Request) (delivery.Plan, error) {
	p, _, err := m.Delivery.Plan(ctx, req)
	return p, err
}

// mcpTransports is the closed --transport vocabulary. checkMCPTransport
// validates against it and shell completion offers it, so the two cannot drift.
var mcpTransports = []string{"stdio", "http"}

// checkMCPTransport rejects an unknown -t value. Falling back to stdio would
// hand the client a transport it never asked for and report success doing it,
// so a typo has to fail before anything is resolved or served.
func checkMCPTransport(transport string) error {
	if slices.Contains(mcpTransports, transport) {
		return nil
	}
	return fmt.Errorf("unsupported transport %q: only \"stdio\" and \"http\" are supported", transport)
}

// errDeliveryOverHTTP refuses to serve the delivery tools to unauthenticated
// remote callers: every caller would act as this process's own identity, so a
// person's or an agent's request could not be told apart from the server's.
// With --auth-issuer and token exchange each request acts as its caller.
var errDeliveryOverHTTP = errors.New("--delivery over HTTP needs --auth-issuer, --resource and token exchange, so every request " +
	"acts as its own caller; without them run `pacto mcp --delivery` over stdio beside the agent, under the agent's own credentials")

// errPublishOverHTTP refuses to serve the publish tools to remote callers:
// every caller would publish with this process's registry credentials.
var errPublishOverHTTP = errors.New("--publish serves stdio only: over HTTP every caller would publish with this process's " +
	"registry credentials; run `pacto mcp --publish` beside the agent, under the agent's own credentials")

// mcpPublish adapts Service.Publish to the publish tools.
func mcpPublish(svc *app.Service, d deps) pactomcp.PublishFunc {
	return func(ctx context.Context, path, revision string, plan bool) (*publish.Result, error) {
		return d.publish(svc, ctx, app.PublishOptions{Path: path, Revision: revision, Plan: plan})
	}
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
		Validator:   svc.ValidationReport,
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

func runMCPServer(ctx context.Context, s mcpServe, stderr io.Writer) error {
	switch s.transport {
	case "http":
		addr := net.JoinHostPort(cmp.Or(s.host, "127.0.0.1"), strconv.Itoa(s.port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "MCP server listening on http://%s/mcp\n", addr)
		return serveHTTP(ctx, mcpHandler(s), listener)
	case "stdio":
		_, _ = fmt.Fprintln(stderr, "MCP server running on stdio")
		return s.server.Run(ctx, &mcpsdk.StdioTransport{})
	}
	return checkMCPTransport(s.transport)
}

func serveHTTP(ctx context.Context, handler http.Handler, listener net.Listener) error {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}
