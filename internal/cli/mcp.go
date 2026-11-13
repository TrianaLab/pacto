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
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/capability"
	"github.com/trianalab/pacto/v4/pkg/catalog"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/override"
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
			"With --root, the server exposes a read-only contract catalog: the given roots " +
			"plus their dependency closure, resolved once at startup and then frozen, so the session keeps " +
			"its startup view even when a registry tag moves. Roots that fail to resolve stay visible " +
			"as partial knowledge. The catalog grants no authorization and executes nothing.\n\n" +
			"With --fleet, the server exposes the read-only operational-graph query tools " +
			"over a fleet snapshot, rebuilt when a call arrives 30 seconds or more after the last rebuild attempt. " +
			"With --dashboard-url their answers link each entity to its page on that dashboard. The shared fleet source flags (--local, --target-state, " +
			"--evidence-url, --traces, --mira-source, --oci, --cache, --k8s, --namespace and --freshness) compose " +
			"that snapshot and are read in --fleet mode only.\n\n" +
			"With --delivery, the server exposes the Pacto delivery tools (pacto_delivery_*) over the " +
			"ambient cluster: operation types, plan, get, explain, history, blame, undo plans and record checks, plus propose and cancel only when the " +
			"apiserver says the caller is an agent (a service account, or an agent by the identity policy Pacto delivery publishes). It has " +
			"no approve tool.\n\n" +
			"Over HTTP on loopback without --auth-issuer, the server prints a bearer at launch (pmcp_..., this launch only) " +
			"and every request must carry it, so other local processes cannot call the server.\n\n" +
			"Over HTTP, --auth-issuer and --resource make every request carry a bearer token the server verifies " +
			"(issuer, audience, expiry) and publish protected-resource metadata (RFC 9728); a bind beyond loopback " +
			"needs them and refuses --auth and --allow-writes, which would lend this process's service credential to every caller. With --delivery the server then exchanges each caller's token for one the apiserver accepts " +
			"(RFC 8693, --exchange-client-id) and builds the tools for that caller on every request, so an agent " +
			"proposes as itself and a person gets the read-only tools. With --fleet the server exchanges each caller's token the same way, " +
			"asks the apiserver who the caller is (a SelfSubjectReview) and asks Kubernetes RBAC what that user may read.\n\n" +
			"With --publish, the authoring server also exposes pacto_publish_plan and pacto_publish, which publish " +
			"a contract's version exactly as `pacto publish` does, with this process's registry credentials.\n\n" +
			"Over HTTP the only contract tool is pacto_schema: pacto_create, pacto_edit and pacto_check read and write the server's folders and follow refs with its credentials, so they run beside the agent over stdio.\n\n" +
			"A bundle reference, --fleet and --delivery combine in one server; --root and --publish select servers of their own. " +
			"Call pacto_capabilities to learn what a server serves.",
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
			fleetMode, _ := cmd.Flags().GetBool("fleet")
			deliveryMode, _ := cmd.Flags().GetBool("delivery")
			publishMode, _ := cmd.Flags().GetBool("publish")
			// The mode is selected by --root being present, not by the values surviving:
			// pflag round-trips a repeated flag through CSV, so a lone `--root ""` comes
			// back as no roots at all. That has to fail, not turn into a different server.
			if err := checkMCPMode(len(args) > 0, cmd.Flags().Changed("root"), fleetMode, deliveryMode, publishMode); err != nil {
				return err
			}
			if !fleetMode && cmd.Flags().Changed("dashboard-url") {
				return errDashboardURLNeedsFleet
			}
			if publishMode && s.transport != "stdio" {
				return errPublishOverHTTP
			}
			if err := mcpRemote(cmd, d, &s); err != nil {
				return err
			}
			// Delivery over HTTP always names its callers (or refuses); fleet
			// does once callers sign in.
			if s.transport == "http" && (deliveryMode || fleetMode && s.issuer != "") {
				if err := remoteCallerFor(cmd, svc, version, args, d, &s); err != nil {
					return err
				}
				return runMCPServer(cmd.Context(), s, cmd.ErrOrStderr())
			}
			var err error
			if s.server, err = buildMCPServer(cmd, svc, version, args, d); err != nil {
				return err
			}
			// The catalog server registers no contract tools at all.
			s.remoteAuthoring = s.transport != "stdio" && !cmd.Flags().Changed("root")
			return runMCPServer(cmd.Context(), s, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().StringP("transport", "t", "stdio", "transport type: stdio or http")
	_ = cmd.RegisterFlagCompletionFunc("transport", staticCompletions(mcpTransports...))
	cmd.Flags().Int("port", 8585, "port for HTTP transport")
	cmd.Flags().String("host", "127.0.0.1", "bind address for HTTP transport; beyond loopback it needs --auth-issuer, and on loopback without it every request carries the bearer printed at launch")
	cmd.Flags().String("auth-issuer", "", "OpenID issuer whose bearer tokens every HTTP request must carry")
	cmd.Flags().String("resource", "", "with --auth-issuer: this endpoint's URL as clients reach it, served as protected-resource metadata (RFC 9728)")
	cmd.Flags().String("audience", "", "with --auth-issuer: the audience incoming tokens must carry (default --resource)")
	cmd.Flags().String("exchange-client-id", "", "with --fleet or --delivery over HTTP with --auth-issuer: the server's own OAuth client, which exchanges each caller's token for the cluster "+
		"(RFC 8693); its secret comes from PACTO_MCP_EXCHANGE_CLIENT_SECRET")
	cmd.Flags().String("exchange-audience", "pacto-cli", "with --fleet or --delivery over HTTP with --auth-issuer: the audience to exchange for, one the apiserver accepts")
	addClaimFlags(cmd)
	cmd.Flags().String("base-url", "", "base URL for live invocation (overrides the OpenAPI servers[] URL)")
	cmd.Flags().StringArray("auth", nil, "credential for a security scheme as name=value (repeatable)")
	cmd.Flags().Bool("allow-writes", false, "expose mutating operations (POST/PUT/PATCH/DELETE) as tools")
	cmd.Flags().Bool("fleet", false, "expose read-only operational-graph (fleet) query tools")
	cmd.Flags().String("dashboard-url", "", "with --fleet: base URL of a Pacto dashboard; fleet answers then carry an href to each entity's page there")
	cmd.Flags().Bool("delivery", false, "expose the Pacto delivery tools over the ambient cluster")
	cmd.Flags().Bool("publish", false, "expose pacto_publish_plan and pacto_publish beside the authoring tools")
	cmd.Flags().String("delivery-namespace", delivery.PolicyNamespace, "in --delivery mode: "+"namespace Pacto delivery runs in, whose published identity policy (agent, break-glass and human groups, team prefix) says who is an agent and who may answer")
	cmd.Flags().String("delivery-trust", "", "the trust config delivery records (in --delivery mode) and the golden paths pacto_create creates from are verified with "+
		"(the recorder's PEM public key or a YAML trust config; default $"+trustEnv+")")
	// Declared before the shared set so this spelling wins: here --root selects a
	// catalog to discover, not a closure to fold into a snapshot.
	cmd.Flags().StringArray("root", nil, "contract root to discover a read-only catalog from: a local bundle path or an oci:// reference (repeatable)")
	// The rest are the fleet source flags, from the one declaration `pacto fleet`
	// uses, and they apply in --fleet mode only.
	addFleetSourceFlags(cmd.Flags())

	return cmd
}

// buildMCPServer constructs the MCP server the invocation selected (RunE
// checked the combination): the catalog from explicit roots, the publish
// server, or the contract tools composed with any of fleet, delivery and a
// bundle's operations.
func buildMCPServer(cmd *cobra.Command, svc *app.Service, version string, args []string, d deps) (*mcpsdk.Server, error) {
	roots, _ := cmd.Flags().GetStringArray("root")
	deliveryMode, _ := cmd.Flags().GetBool("delivery")
	publishMode, _ := cmd.Flags().GetBool("publish")
	switch {
	case publishMode:
		return pactomcp.NewPublishServer(version, mcpPublish(svc, d), mcpAuthoring(cmd, svc)), nil
	case cmd.Flags().Changed("root"):
		cat, err := buildCatalog(cmd, svc, roots)
		if err != nil {
			return nil, err
		}
		return pactomcp.NewCatalogServer(version, cat), nil
	}
	m, err := mcpModes(cmd, svc, version, args, d)
	if err == nil && deliveryMode {
		m.Delivery, err = buildDeliveryMode(cmd, d)
	}
	if err != nil {
		return nil, err
	}
	return pactomcp.New(version, mcpAuthoring(cmd, svc), m)
}

// mcpModes builds the modes every request shares: the fleet over one
// refreshing snapshot and the bundle's operations. Delivery is per caller.
func mcpModes(cmd *cobra.Command, svc *app.Service, version string, args []string, d deps) (pactomcp.Modes, error) {
	transport, _ := cmd.Flags().GetString("transport")
	fleetMode, _ := cmd.Flags().GetBool("fleet")
	m := pactomcp.Modes{Transport: transport}
	var err error
	if fleetMode {
		if m.Fleet, err = buildFleetMode(cmd, svc, d); err != nil {
			return m, err
		}
	}
	if len(args) > 0 {
		m.Bundle, err = buildBundleMode(cmd, svc, version, args[0])
	}
	return m, err
}

// buildFleetMode serves the fleet tools over a snapshot that rebuilds after
// the dashboard's refresh interval. The first build fails startup; later ones
// run on the command's lifetime context, never a request's, so a cancelled call
// cannot abort the build other sessions wait on.
func buildFleetMode(cmd *cobra.Command, svc *app.Service, d deps) (*pactomcp.FleetMode, error) {
	fopts := fleetOptions(cmd)
	src, err := mcpFleetSource(cmd.Context(), func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, fopts)
	}, fleetRefreshInterval, time.Now)
	if err != nil {
		return nil, err
	}
	view := mcpFleetView(cmd, d)
	url, _ := cmd.Flags().GetString("dashboard-url")
	// view is nil for an HTTP server without --auth-issuer too, so only the
	// transport says whether the caller is this process's own user.
	transport, _ := cmd.Flags().GetString("transport")
	return &pactomcp.FleetMode{Src: src, Opts: pactomcp.FleetOptions{
		// Under a view the new revision is never added: the rebuild would read
		// the unrestricted sources.
		Impact: mcpImpactProvider(cmd, svc, d, view == nil, transport == "stdio"), View: view, DashboardURL: url,
	}}, nil
}

// mcpFleetSource builds the first snapshot (its error fails startup) and
// returns a source that rebuilds when a call arrives at least interval after
// the last attempt, successful or not, so a broken source is retried once per
// interval, never per call. A failed rebuild keeps the last good snapshot.
// ponytail: lazy, not a ticker, so an idle stdio session never polls a cluster.
func mcpFleetSource(life context.Context, build fleet.Builder, interval time.Duration, now func() time.Time) (pactomcp.FleetQuerySource, error) {
	mgr := fleet.NewManager(build)
	if err := mgr.Refresh(life); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	last := now()
	return func(ctx context.Context) (*fleet.Query, error) {
		mu.Lock()
		due := now().Sub(last) >= interval
		if due {
			last = now()
		}
		mu.Unlock()
		if due {
			// The build runs on life; the call waits for it only as long as its
			// own context lives, then answers from the last good snapshot.
			done := make(chan struct{})
			go func() {
				_ = mgr.Refresh(life) // a failure keeps the last good snapshot
				close(done)
			}()
			select {
			case <-done:
			case <-ctx.Done():
			}
		}
		return mgr.Query()
	}, nil
}

// errDashboardURLNeedsFleet: only the fleet tools answer with entities to link.
var errDashboardURLNeedsFleet = errors.New("--dashboard-url needs --fleet: only the fleet tools link answers to a dashboard")

// checkMCPMode rejects --root or --publish beside any other mode: each
// selects a server of its own, so honouring one and dropping the rest would
// hand a client a server it did not ask for. A bundle reference, --fleet and
// --delivery combine in one server.
func checkMCPMode(hasBundle, hasRoots, fleetMode, deliveryMode, publishMode bool) error {
	var selected []string
	for _, m := range []struct {
		on   bool
		name string
	}{{hasBundle, "a bundle reference"}, {hasRoots, "--root"}, {fleetMode, "--fleet"}, {deliveryMode, "--delivery"}, {publishMode, "--publish"}} {
		if m.on {
			selected = append(selected, m.name)
		}
	}
	if len(selected) > 1 && (hasRoots || publishMode) {
		return fmt.Errorf("%s cannot be combined: --root and --publish each select an MCP server of their own, so pass them alone",
			strings.Join(selected, ", "))
	}
	return nil
}

// buildDeliveryMode serves the delivery tools for the kubeconfig's caller.
// Write tools register only when the apiserver says the caller is an agent:
// an agent must run under its own identity, and a person's credentials get
// the read-only surface.
func buildDeliveryMode(cmd *cobra.Command, d deps) (*pactomcp.DeliveryMode, error) {
	c, _, err := deliveryClient(cmd, d)
	if err != nil {
		return nil, err
	}
	caller, err := c.WhoAmI(cmd.Context())
	if err != nil {
		return nil, err
	}
	return &pactomcp.DeliveryMode{API: mcpDelivery{c}, Writes: caller.Class == delivery.ClassAgent, Caller: caller}, nil
}

// mcpDelivery adapts app.Delivery to the MCP tools' interface.
type mcpDelivery struct{ *app.Delivery }

func (m mcpDelivery) Plan(ctx context.Context, req delivery.Request) (delivery.PlanAnswer, error) {
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

// mcpAuthoring is what every server's authoring tools get. pacto_create,
// pacto_edit and pacto_check serve stdio only: over HTTP every caller would
// read and write this process's folders, read its kubeconfig and follow refs
// with its registry credentials, so Remote leaves only pacto_schema there.
func mcpAuthoring(cmd *cobra.Command, svc *app.Service) pactomcp.Authoring {
	a := pactomcp.Authoring{Validate: svc.ValidationReport}
	transport, _ := cmd.Flags().GetString("transport")
	a.Remote = transport != "stdio"
	if !a.Remote {
		trust, _ := cmd.Flags().GetString("delivery-trust")
		a.Init = mcpInit(svc, trust)
	}
	return a
}

// mcpInit adapts Service.InitFrom to pacto_create from. The trust config is
// read on each call, so a broken one fails that call and not the server.
func mcpInit(svc *app.Service, trustPath string) pactomcp.Initializer {
	return func(ctx context.Context, opts app.InitOptions, req pactomcp.FromRequest) (*app.InitResult, error) {
		trust, err := deliveryTrust(trustPath)
		if err != nil {
			return nil, err
		}
		from := app.InitFrom{From: req.Ref, Trust: trust, RunPlugins: req.RunPlugins, Overrides: override.Overrides{SetValues: req.Set}}
		if req.Values != nil {
			from.Overrides.Values = []map[string]any{req.Values}
		}
		return svc.InitFrom(ctx, opts, from)
	}
}

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

// buildBundleMode serves one bundle's OpenAPI operations and skills.
func buildBundleMode(cmd *cobra.Command, svc *app.Service, version, ref string) (*pactomcp.BundleMode, error) {
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
	return &pactomcp.BundleMode{Bundle: bundle, Stderr: cmd.ErrOrStderr(), Opts: pactomcp.CapabilityOptions{
		BaseURL:     baseURL,
		Creds:       creds,
		AllowWrites: allowWrites,
		Version:     version,
	}}, nil
}

// mcpImpactProvider builds the impact provider for pacto_impact. It analyses
// over the snapshot the fleet tools answer from; with addMissing it adds the new
// revision, rebuilt from the same fleet source flags, when no source holds the
// changed service. Only a trusted server (stdio: the caller is this process's
// own user, as mcpAuthoring decides) reads a traces file, a local ref or
// delivery: over HTTP every caller would read them as this process.
// Extracted so the wiring is directly testable.
func mcpImpactProvider(cmd *cobra.Command, svc *app.Service, d deps, addMissing, trusted bool) func(ctx context.Context, snap *fleet.FleetSnapshot, req pactomcp.ImpactRequest) (*impact.Result, error) {
	fopts := fleetOptions(cmd)
	return func(ctx context.Context, snap *fleet.FleetSnapshot, req pactomcp.ImpactRequest) (*impact.Result, error) {
		req, err := refuseUntrustedImpact(req, trusted, snap)
		if err != nil {
			return nil, err
		}
		var traces []byte
		if req.TracesPath != "" {
			var err error
			if traces, err = os.ReadFile(req.TracesPath); err != nil {
				return nil, err
			}
			req.IncludeObserved = true
		}
		if req.Event != nil {
			opts := app.ImpactEventOptions{Event: *req.Event, Traces: traces}
			opts.ReadRegression = func(ctx context.Context, ev impact.Event) *impact.Regression {
				return mcpRegression(ctx, cmd, d, ev, trusted)
			}
			return svc.ImpactEventOnSnapshot(ctx, opts, snap)
		}
		opts := app.ImpactOptions{OldPath: req.OldRef, NewPath: req.NewRef, Fleet: fopts, IncludeObserved: req.IncludeObserved, Traces: traces}
		return svc.ImpactOnSnapshot(ctx, opts, snap, addMissing)
	}
}

// refuseUntrustedImpact keeps an HTTP caller from naming a file path, a
// local ref or a revision outside their fleet view: the server would read
// them with its own access. Over HTTP the returned request carries each
// revision's pinned ResolvedRef in place of the caller's string.
func refuseUntrustedImpact(req pactomcp.ImpactRequest, trusted bool, snap *fleet.FleetSnapshot) (pactomcp.ImpactRequest, error) {
	switch {
	case trusted:
		return req, nil
	case req.TracesPath != "":
		return req, errors.New("this server answers over HTTP: it reads no file path a caller names")
	case req.Event != nil:
		return req, nil
	case graph.ParseDependencyRef(req.OldRef).IsLocal() || graph.ParseDependencyRef(req.NewRef).IsLocal():
		return req, errors.New("this server answers over HTTP: pass oci:// references")
	}
	var err error
	req.OldRef, req.NewRef, err = pinVisibleRefs(snap, req.OldRef, req.NewRef, "old_ref", "new_ref")
	return req, err
}

// mcpRegression reads delivery about a regressed revision for a trusted
// caller, with the kubeconfig the server runs under, which is the caller's.
func mcpRegression(ctx context.Context, cmd *cobra.Command, d deps, ev impact.Event, trusted bool) *impact.Regression {
	if !trusted {
		return &impact.Regression{Unavailable: "this server answers over HTTP and reads no delivery for its callers; " +
			"run pacto mcp --fleet over stdio beside the agent, under its own cluster credentials, to read delivery evidence"}
	}
	return readRegression(ctx, cmd, d, ev)
}

// mcpFleetView narrows the fleet tools of a server whose callers sign in
// (HTTP with --auth-issuer) to what each caller's Kubernetes RBAC lets them
// read, asked with this process's own credentials about the person the
// apiserver reviewed for the request (remoteCaller). Other servers answer
// from the whole snapshot, as their single local user would read it.
func mcpFleetView(cmd *cobra.Command, d deps) pactomcp.FleetView {
	transport, _ := cmd.Flags().GetString("transport")
	issuer, _ := cmd.Flags().GetString("auth-issuer")
	if transport != "http" || issuer == "" {
		return nil
	}
	cluster, _ := d.clusterConfig()
	viewer := authn.NewViewer(clusterReview(cluster, d.dynamicFor))
	return func(ctx context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		p, ok := personFrom(ctx)
		if !ok {
			return nil, errNobodyReviewed
		}
		r, err := viewer.Query(ctx, p, q)
		if err != nil {
			return nil, fmt.Errorf("checking your Kubernetes permissions failed: %w", err)
		}
		return r, nil
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
		if s.launch != "" {
			_, _ = fmt.Fprintf(stderr, "MCP server listening on http://%s/mcp   bearer: %s (this launch only)\n", addr, s.launch)
		} else {
			_, _ = fmt.Fprintf(stderr, "MCP server listening on http://%s/mcp\n", addr)
		}
		if s.remoteAuthoring {
			_, _ = fmt.Fprintln(stderr, "contract tools: pacto_schema only over HTTP; pacto_create, pacto_edit and pacto_check run beside the agent with pacto mcp over stdio")
		}
		return serveHTTP(ctx, mcpHandler(s), listener)
	case "stdio":
		_, _ = fmt.Fprintln(stderr, "MCP server running on stdio")
		return s.server.Run(ctx, &mcpsdk.StdioTransport{})
	}
	return checkMCPTransport(s.transport)
}

func serveHTTP(ctx context.Context, handler http.Handler, listener net.Listener) error {
	// Requests log through the command's logger, not the process default.
	base := logging.WithLogger(context.Background(), logging.LoggerFromContext(ctx))
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}
