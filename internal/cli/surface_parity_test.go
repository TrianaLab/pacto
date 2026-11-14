package cli

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// surfaceRow names one concept on the CLI (a command's flag, argument or the
// command itself), MCP (a tool's argument or the tool) and the dashboard API
// (an operation's query, path or body parameter, or the operation). An empty
// cell needs a Reason a reviewer can check; a Reason on a full row is a note.
type surfaceRow struct {
	CLI, MCP, HTTP, Reason string
}

// alike are the rows of parameters a command, a tool and a route spell alike:
// kebab-case on the command, snake_case on the tool and camelCase on the
// route. names are kebab-case; a blank surface takes reason.
func alike(cli, tool, route, reason string, names ...string) []surfaceRow {
	var out []surfaceRow
	for _, n := range names {
		r := surfaceRow{Reason: reason}
		if cli != "" {
			r.CLI = cli + " --" + n
		}
		if tool != "" {
			r.MCP = tool + "." + strings.ReplaceAll(n, "-", "_")
		}
		if route != "" {
			r.HTTP = route + "?" + camel(n)
		}
		out = append(out, r)
	}
	return out
}

// body is alike for the body properties of a POST, PUT or DELETE route.
func body(cli, tool, route, reason string, names ...string) []surfaceRow {
	out := alike(cli, tool, "", reason, names...)
	for i, n := range names {
		out[i].HTTP = route + " body." + camel(n)
	}
	return out
}

func camel(kebab string) string {
	parts := strings.Split(kebab, "-")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

const (
	whyNamespace     = "every delivery command takes the namespace from the delivery group's -n (delivery --namespace)"
	whyDashboardPage = "paging and narrowing of a dashboard table; the CLI and MCP answer the whole list"
	whyNoEdit        = "the CLI has no edit command: people edit pacto.yaml and run pacto validate"
	whyNoCreateFlag  = "pacto init reads it from the service folder (Dockerfile, manifests, workload labels) instead of a flag"
	whyStatusRoute   = "GET /api/fleet/status answers the whole summary with no filters; the dashboard narrows it in the page"
	whySearchRoute   = "GET /api/fleet/services takes text, owner, scope, source, status and template only; the dashboard's Services page narrows with /api/fleet/entities"
	whyNoApprove     = "MCP has no approve tool: people answer an operation, an agent never does"
	whyCreateOnly    = "the dashboard writes and checks no contract files, and an MCP server over HTTP serves only pacto_schema of the contract tools"
	whyImpactRefs    = "over HTTP and on a shared dashboard, both resolve only refs of revisions in the caller's view, pinned to their digest"
	whyKeyedImpact   = "the dashboard's impact page compares two revisions of its published snapshot by key; the CLI and MCP name refs (GET /api/fleet/impact)"
	whyAtServerStart = "the MCP server and the dashboard take it once at startup (pacto mcp and pacto dashboard flags)"
	whyNoStatsRoute  = "the dashboard has no stats route yet; pacto stats and pacto_stats total the same run records"
)

// surfaceRows maps every CLI flag, MCP argument and dashboard parameter of
// the fleet, delivery, impact and contract surfaces to its concept. A story
// that adds one adds its row here in the same commit.
var surfaceRows = slices.Concat(fleetRows(), fleetDetailRows(), impactRows(), deliveryRows(), deliveryWriteRows(), deliveryViewRows(), authoringRows(), statsRows())

func fleetRows() []surfaceRow {
	return slices.Concat(
		alike("fleet entities", "pacto_fleet_entities", "GET /api/fleet/entities", "", "business-domain", "consumes", "created-from", "environment",
			"has-non-compliant", "limit", "offering", "offset", "owner", "owner-key", "ownership", "phase", "sort", "source-kind", "status", "template",
			"capability", "domain", "readiness", "scope", "service", "source", "source-health"),
		[]surfaceRow{
			{"fleet entities --kind", "pacto_fleet_entities.kind", "GET /api/fleet/entities?kinds", "the route takes several kinds, the CLI and MCP one"},
			{"fleet entities [text]", "pacto_fleet_entities.text", "GET /api/fleet/entities?text", ""},
		},
		alike("fleet attention", "pacto_fleet_attention", "GET /api/fleet/attention", "", "business-domain", "category", "limit", "offset", "owner", "owner-key", "service", "severity",
			"key", "kind", "source", "stale-only", "status"),
		[]surfaceRow{
			{"fleet neighborhood <service>", "pacto_fleet_neighborhood.service", "GET /api/fleet/neighborhood?key", ""},
			{"", "", "GET /api/fleet/neighborhood?kind", "the CLI and MCP focus a service; the dashboard's graph also focuses a revision or an owner"},
			{"", "", "GET /api/fleet/neighborhood?perspective", "the dashboard's graph switches perspective; the CLI and MCP walk the service perspective"},
		},
		alike("fleet neighborhood", "pacto_fleet_neighborhood", "GET /api/fleet/neighborhood", "", "depth", "direction", "max-edges", "max-nodes", "views"),
		alike("fleet search", "pacto_fleet_search", "GET /api/fleet/services", "", "limit", "offset", "owner", "scope", "source", "status", "template"),
		[]surfaceRow{{"fleet search [text]", "pacto_fleet_search.text", "GET /api/fleet/services?text", ""}},
		alike("fleet search", "pacto_fleet_search", "", whySearchRoute, "business-domain", "compliance", "has-capability", "has-dependency", "not-ready", "offering", "ready", "workload"),
		[]surfaceRow{{"fleet search --label", "pacto_fleet_search.label", "", whySearchRoute}},
		alike("fleet status", "pacto_fleet_status", "", whyStatusRoute, "golden-path-deployed", "invalid", "limit", "missing-readiness", "needs-attention",
			"non-compliant", "stale", "undeclared", "unknown", "unowned", "unresolved-deps", "unsatisfied-ranges"),
		[]surfaceRow{
			{"fleet status", "pacto_fleet_status", "GET /api/fleet/status", ""},
			{"", "", "GET /api/fleet/overview", "the CLI has no overview command and MCP has no overview tool yet (#460 adds pacto_fleet_overview)"},
			{"fleet snapshot", "", "GET /api/fleet/snapshot", "MCP serves the snapshot only through the fleet tools' answers"},
			{"fleet reconcile --traces", "", "", "reconcile compares one local trace file with the local contracts; it reads no fleet source"},
		},
	)
}

func fleetDetailRows() []surfaceRow {
	return slices.Concat(
		[]surfaceRow{
			{"fleet entity <kind>", "pacto_fleet_entity.kind", "GET /api/fleet/entities/{kind}?kind", ""},
			{"fleet entity <key>", "pacto_fleet_entity.key", "GET /api/fleet/entities/{kind}?key", ""},
			{"fleet versions <offering>", "pacto_fleet_versions.offering", "GET /api/fleet/versions?offering", ""},
			{"fleet get [service]", "pacto_fleet_get.service", "GET /api/fleet/service?key", ""},
			{"fleet get --target", "pacto_fleet_get.target", "GET /api/fleet/target?key", ""},
			{"fleet graph <service>", "pacto_fleet_graph.service", "GET /api/fleet/services/{name}/graph?name", ""},
			{"fleet graph --max-depth", "pacto_fleet_graph.max_depth", "GET /api/fleet/services/{name}/graph?maxDepth", ""},
			{"", "", "GET /api/fleet/services/{name}/graph?maxNodes", "the dashboard bounds the graph it draws; the CLI and MCP answer the whole tree"},
			{"fleet explain <subject>", "pacto_fleet_explain.subject", "", "the dashboard explains a subject on its entity pages"},
			{"", "pacto_fleet_revision_document.key", "GET /api/fleet/revisions/document?key", "the CLI reads a bundle's documents with pacto doc on the bundle itself"},
			{"", "pacto_fleet_revision_document.path", "GET /api/fleet/revisions/document?path", "the CLI reads a bundle's documents with pacto doc on the bundle itself"},
		},
		alike("fleet versions", "pacto_fleet_versions", "GET /api/fleet/versions", "", "provider", "range"),
		alike("fleet graph", "pacto_fleet_graph", "GET /api/fleet/services/{name}/graph", "", "direction", "revision", "target", "transitive"),
	)
}

func impactRows() []surfaceRow {
	return slices.Concat(
		[]surfaceRow{
			{"impact <old>", "pacto_impact.old_ref", "GET /api/fleet/impact?old", whyImpactRefs},
			{"impact <new>", "pacto_impact.new_ref", "GET /api/fleet/impact?new", whyImpactRefs},
			{"impact --include-observed", "pacto_impact.include_observed", "GET /api/fleet/impact?includeObserved", ""},
			{"", "", "POST /api/fleet/impact body.includeObserved", whyKeyedImpact},
			{"impact --traces", "pacto_impact.traces", "", "the dashboard reads the trace sources it was started with"},
			{"impact --unavailable", "pacto_impact.event", "POST /api/fleet/impact body.event", "each CLI event flag names the event and its service at once"},
			{"impact --removed", "", "", "pacto_impact.event and the dashboard's body.event take removed as a value"},
			{"impact --regressed", "", "", "pacto_impact.event and the dashboard's body.event take regressed as a value"},
			{"", "pacto_impact.service", "POST /api/fleet/impact body.serviceKey", "the CLI takes the service as the event flag's value"},
			{"", "pacto_impact.revision", "POST /api/fleet/impact body.revision", "the CLI takes the revision after @ in --regressed"},
		},
		alike("impact", "", "", "the CLI records an unsigned run record for CI statistics; MCP and the dashboard never record",
			"stats-context", "stats-file", "stats-registry"),
		alike("impact", "", "", "overrides the CLI applies to local contracts before comparing; MCP and the dashboard compare published revisions as they are",
			"new-set", "new-values", "old-set", "old-values"),
		body("", "", "POST /api/fleet/impact", whyKeyedImpact, "from-revision-key", "to-revision-key"),
		body("", "", "POST /api/fleet/impact", whyDashboardPage, "limit", "offset", "outcome"),
		body("", "", "POST /api/fleet/impact", "the dashboard refuses an answer for a snapshot it no longer publishes; the CLI and MCP build their own", "snapshot-id"),
	)
}

func deliveryRows() []surfaceRow {
	return slices.Concat(
		[]surfaceRow{
			{"delivery --namespace", "", "", "each delivery tool and route takes namespace as an argument; its row follows the tool"},
			{"delivery --delivery-namespace", "", "", whyAtServerStart},
			{"delivery --trust", "", "", whyAtServerStart},
			{"delivery status --why", "pacto_delivery_status.why", "GET /api/delivery/progress", "the dashboard's progress page plans the candidates of one Stage"},
			{"", "", "GET /api/delivery/progress?stage", "pacto delivery status --why and pacto_delivery_status with why plan every Stage of a service"},
			{"", "", "GET /api/delivery/progress?namespace", "pacto delivery status --why and pacto_delivery_status with why plan every Stage of a service"},
			{"", "pacto_delivery_status.namespace", "", whyNamespace + "; the dashboard's progress page names its Stage"},
			{"delivery history --correlation-id", "pacto_delivery_history.correlation_id", "GET /api/delivery/history?correlationID", ""},
			{"delivery history --all", "", "", "pacto_delivery_history and the history route walk every Stage when stage is empty"},
			{"", "pacto_delivery_history.namespace", "GET /api/delivery/history?namespace", whyNamespace},
			{"", "pacto_delivery_blame.namespace", "GET /api/delivery/blame?namespace", whyNamespace},
			{"", "pacto_delivery_candidates.namespace", "", whyNamespace + "; the dashboard lists candidates in its propose panel through plan"},
			{"delivery record get <id>", "pacto_delivery_record_get.record", "GET /api/delivery/record?id", ""},
			{"", "pacto_delivery_record_get.namespace", "GET /api/delivery/record?namespace", whyNamespace},
			{"delivery record verify --all", "", "", "pacto_delivery_record_verify walks every Stage when stage is empty"},
			{"", "pacto_delivery_record_verify.namespace", "", whyNamespace + "; the dashboard verifies a record on its record page"},
			{"delivery get <operation>", "pacto_delivery_get.name", "GET /api/delivery/operations/{namespace}/{name}?name", ""},
			{"", "pacto_delivery_get.namespace", "GET /api/delivery/operations/{namespace}/{name}?namespace", whyNamespace},
			{"delivery explain <operation>", "pacto_delivery_explain.name", "", "the dashboard's operation page explains it"},
			{"", "pacto_delivery_explain.namespace", "", whyNamespace + "; the dashboard's operation page explains it"},
			{"delivery undo <operation>", "pacto_delivery_undo_plan.name", "GET /api/delivery/undo?name", ""},
			{"", "pacto_delivery_undo_plan.namespace", "GET /api/delivery/undo?namespace", whyNamespace},
			{"", "pacto_delivery_lifecycle.namespace", "", whyNamespace + "; the dashboard's lifecycle route reads every namespace the caller can list"},
			{"delivery describe <type>", "pacto_delivery_operation_types.type", "", "the dashboard's propose panel lists the types it offers"},
		},
		alike("delivery status", "pacto_delivery_status", "", "the dashboard shows a service's Stages on the environments board (GET /api/delivery/environments?service)", "domain", "service"),
		alike("delivery history", "pacto_delivery_history", "GET /api/delivery/history", "", "cause", "domain", "service", "since", "stage", "until"),
		alike("", "", "GET /api/delivery/history", whyDashboardPage, "limit", "offset", "q", "timed"),
		alike("delivery blame", "pacto_delivery_blame", "GET /api/delivery/blame", "", "digest", "stage"),
		alike("delivery candidates", "pacto_delivery_candidates", "", "the dashboard lists candidates in its propose panel through plan", "stage"),
		alike("delivery record get", "pacto_delivery_record_get", "GET /api/delivery/record", "", "stage"),
		alike("delivery record verify", "pacto_delivery_record_verify", "", "the dashboard verifies a record on its record page (GET /api/delivery/record)", "stage"),
		alike("delivery undo", "pacto_delivery_undo_plan", "GET /api/delivery/undo", "", "stage"),
		alike("delivery lifecycle", "pacto_delivery_lifecycle", "GET /api/delivery/lifecycle", "", "contract", "digest", "domain", "service", "version"),
	)
}

func deliveryWriteRows() []surfaceRow {
	const plan, propose = "POST /api/delivery/plan", "POST /api/delivery/operations"
	return slices.Concat(
		body("delivery plan", "pacto_delivery_operation_plan", plan, "", "from", "on-behalf-of", "reason", "stage", "to", "type", "undoes"),
		body("delivery propose", "pacto_delivery_operation_propose", propose, "", "from", "intent-digest", "name", "on-behalf-of", "reason", "stage", "to", "type", "undoes"),
		[]surfaceRow{
			{"delivery plan --correlation-id", "pacto_delivery_operation_plan.correlation_id", plan + " body.correlationID", ""},
			{"delivery propose --correlation-id", "pacto_delivery_operation_propose.correlation_id", propose + " body.correlationID", ""},
			{"delivery plan --cause-verdict", "pacto_delivery_operation_plan.cause_verdict", plan + " body.cause", ""},
			{"delivery propose --cause-verdict", "pacto_delivery_operation_propose.cause_verdict", propose + " body.cause", ""},
			{"delivery plan --record", "", plan + " body.record", "acknowledge operations take a record id, which agents never propose"},
			{"delivery propose --record", "", propose + " body.record", "acknowledge operations take a record id, which agents never propose"},
			{"", "pacto_delivery_operation_plan.namespace", plan + " body.namespace", whyNamespace},
			{"", "pacto_delivery_operation_propose.namespace", propose + " body.namespace", whyNamespace},
			{"", "", plan + " body.intentDigest", "the plan body is the request propose sends; intentDigest and name matter only on propose"},
			{"", "", plan + " body.name", "the plan body is the request propose sends; intentDigest and name matter only on propose"},
			{"delivery cancel <operation>", "pacto_delivery_cancel.name", "DELETE /api/delivery/operations/{namespace}/{name}?name", ""},
			{"", "pacto_delivery_cancel.namespace", "DELETE /api/delivery/operations/{namespace}/{name}?namespace", whyNamespace},
			{"delivery approve <operation>", "", "POST /api/delivery/operations/{namespace}/{name}/decision?name", whyNoApprove},
			{"", "", "POST /api/delivery/operations/{namespace}/{name}/decision?namespace", whyNamespace + "; " + whyNoApprove},
		},
		body("delivery approve", "", "POST /api/delivery/operations/{namespace}/{name}/decision", whyNoApprove, "decision", "note"),
		alike("delivery approve", "", "", "the CLI signs in to the identity provider itself to answer as a person; the dashboard signs people in",
			"oidc-ca-file", "oidc-client-id", "oidc-extra-scopes", "oidc-issuer-url", "oidc-redirect-port"),
	)
}

// deliveryViewRows are the dashboard's delivery views: the command's flags,
// the tool's arguments and the route's parameters carry one name.
func deliveryViewRows() []surfaceRow {
	view := func(route string, names ...string) []surfaceRow {
		return alike("delivery "+route, "pacto_delivery_"+route, "GET /api/delivery/"+route, "", names...)
	}
	return slices.Concat(
		view("inbox", "domain", "environment", "errored", "except", "frozen", "held", "limit", "q", "service"),
		view("environments", "domain", "environment", "except", "limit", "offset", "q", "service", "state", "tier"),
		view("autonomy", "blockers", "domain", "level", "limit", "offset", "q", "service"),
		view("published", "domain", "limit", "offset", "q", "service", "signed"),
		[]surfaceRow{
			{"delivery refusals", "pacto_delivery_refusals", "GET /api/delivery/refusals", ""},
			{"delivery caller", "pacto_delivery_caller", "GET /api/delivery/caller", ""},
		},
	)
}

func authoringRows() []surfaceRow {
	return slices.Concat(
		alike("init", "pacto_create", "", whyCreateOnly, "cluster", "delivery", "describe-only", "domain", "from", "insecure-registry", "name",
			"no-transparency-log", "owner", "registry", "registry-secret", "run-plugins", "set", "values"),
		[]surfaceRow{
			{"init [dir...]", "pacto_create.path", "", whyCreateOnly},
			{"init --env", "pacto_create.environments", "", whyCreateOnly},
			{"init --fleet", "pacto_create.fleet_roots", "", whyCreateOnly},
			{"init --namespace", "pacto_create.namespaces", "", whyCreateOnly},
			{"init --plan", "pacto_create.dry_run", "", whyCreateOnly},
			{"init --out", "", "", "with --cluster the CLI writes one folder per workload; pacto_create writes at path"},
			{"init --trust", "", "", "pacto_create verifies a golden path with the trust config the server was started with"},
			{"init", "pacto_create.pactify", "", "pacto init always reads the service folder; pacto_create does so with pactify"},
			{"", "pacto_create.paths", "", "the CLI takes several folders as arguments (init [dir...])"},
			{"validate [dir | oci://ref]", "pacto_check.path", "", whyCreateOnly},
			{"explain --environments", "pacto_check.environments", "", "pacto explain compares environments; pacto_check adds the comparison to its validation"},
			{"", "pacto_schema", "", "the CLI has no schema command; the JSON Schema is published with the docs"},
			{"", "pacto_capabilities", "GET /api/capabilities", "the CLI is the local user with every command; MCP and the dashboard report what they serve"},
		},
		alike("", "pacto_create", "", whyNoCreateFlag, "config-properties", "data-loss-impact", "data-shared-across-instances", "data-survives-restart",
			"dependencies", "description", "interfaces", "metadata", "stores-data", "version", "workload"),
		alike("validate", "", "", "checks of pacto validate that pacto_check runs through its suggestions; the dashboard validates nothing", "closure", "readiness", "set", "values"),
		alike("explain", "", "", "overrides the CLI applies to a local contract before explaining it; pacto_check reads the contract as it is", "set", "values"),
		alike("", "pacto_edit", "", whyNoEdit, "add-config-properties", "add-configuration-refs", "add-dependencies", "add-environments", "add-interfaces",
			"add-policy-refs", "data-loss-impact", "data-shared-across-instances", "data-survives-restart", "domain", "dry-run", "lifecycle", "name",
			"offering", "owner", "path", "remove-configuration-refs", "remove-dependencies", "remove-environments", "remove-interfaces", "remove-metadata",
			"set-environment-values", "set-metadata", "stores-data", "summary", "template", "version", "workload"),
	)
}

func statsRows() []surfaceRow {
	return slices.Concat(
		alike("stats", "pacto_stats", "", whyNoStatsRoute, "by", "service", "since", "until"),
		[]surfaceRow{
			{"stats --kind", "pacto_stats.kinds", "", whyNoStatsRoute + "; the CLI repeats --kind, MCP takes comma-separated kinds"},
			{"stats --registry", "pacto_stats.registry", "", whyNoStatsRoute + "; pacto_stats honours it over stdio only"},
		},
	)
}

// parityCommands are the commands whose own flags the mapping covers.
var parityCommands = []string{
	"fleet entities", "fleet entity", "fleet attention", "fleet neighborhood", "fleet versions", "fleet search",
	"fleet get", "fleet graph", "fleet status", "fleet explain", "fleet reconcile", "fleet snapshot",
	"delivery", "delivery status", "delivery history", "delivery blame", "delivery lifecycle", "delivery candidates",
	"delivery record get", "delivery record verify", "delivery get", "delivery explain", "delivery undo", "delivery plan",
	"delivery propose", "delivery cancel", "delivery describe", "delivery types", "delivery approve",
	"delivery caller", "delivery inbox", "delivery environments", "delivery autonomy", "delivery published", "delivery refusals",
	"impact", "init", "validate", "explain", "stats",
}

// surfaces is what the three surfaces expose at HEAD.
type surfaces struct {
	cli, mcp, http map[string]bool
	commands       map[string]string // command path -> Use
}

func collectSurfaces(t *testing.T) surfaces {
	t.Helper()
	s := surfaces{cli: map[string]bool{}, commands: map[string]string{}}
	shared := pflag.NewFlagSet("shared", pflag.ContinueOnError)
	addFleetSourceFlags(shared)
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	for _, path := range parityCommands {
		cmd, _, err := root.Find(strings.Fields(path))
		expect(t, err == nil && cmd.CommandPath() == "pacto "+path, "no command %q", path)
		s.commands[path] = cmd.Use
		cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			if sf := shared.Lookup(f.Name); f.Name == "help" || (sf != nil && sf.Usage == f.Usage) {
				return // help, and the fleet source flags every fleet command shares
			}
			s.cli[path+" --"+f.Name] = true
		})
	}
	s.mcp, s.http = mcpNames(t), httpNames(t)
	return s
}

// mcpNames are "<tool>.<argument>", or the tool's name when it takes none,
// for every tool of a composed fleet and delivery server that may write, with
// every contract tool.
func mcpNames(t *testing.T) map[string]bool {
	t.Helper()
	q := fleet.NewQuery(&fleet.FleetSnapshot{})
	svc := app.NewService(nil, nil)
	server, err := pactomcp.New("v", mcpAuthoring(newMCPCommand(svc, "v", defaultDeps()), svc), pactomcp.Modes{
		Fleet: &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return q, nil }, Opts: pactomcp.FleetOptions{Impact: signedInImpact(t, defaultDeps()),
			Stats: mcpStatsProvider(newMCPCommand(svc, "v", defaultDeps()), svc, true)}},
		Delivery: &pactomcp.DeliveryMode{Writes: true, Reads: func(context.Context, string, url.Values) (json.RawMessage, error) { return nil, nil }},
	})
	expect(t, err == nil, "server: %v", err)
	res, err := sessionOn(t, server).ListTools(context.Background(), nil)
	expect(t, err == nil, "tools: %v", err)
	out := map[string]bool{}
	for _, tool := range res.Tools {
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		if len(props) == 0 {
			out[tool.Name] = true
		}
		for p := range props {
			out[tool.Name+"."+p] = true
		}
	}
	return out
}

// openAPIOp is the part of a dashboard operation the mapping reads.
type openAPIOp struct {
	Parameters []struct {
		Name string `json:"name"`
		In   string `json:"in"`
	} `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			} `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
}

// httpNames are "<METHOD> <path>?<param>" for every query and path parameter
// and "<METHOD> <path> body.<property>" for every request-body property of
// every dashboard operation under /api/fleet/ and /api/delivery/, or the
// operation when it takes none, plus GET /api/capabilities.
func httpNames(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := dashboard.ExportOpenAPI()
	expect(t, err == nil, "openapi: %v", err)
	var doc struct {
		Paths      map[string]map[string]openAPIOp `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	expect(t, json.Unmarshal(raw, &doc) == nil, "openapi json")
	out := map[string]bool{"GET /api/capabilities": true}
	for path, ops := range doc.Paths {
		if !strings.HasPrefix(path, "/api/fleet/") && !strings.HasPrefix(path, "/api/delivery/") {
			continue
		}
		for method, op := range ops {
			at := strings.ToUpper(method) + " " + path
			names := []string{}
			for _, p := range op.Parameters {
				if p.In == "query" || p.In == "path" {
					names = append(names, at+"?"+p.Name)
				}
			}
			for _, c := range op.RequestBody.Content {
				for prop := range doc.Components.Schemas[strings.TrimPrefix(c.Schema.Ref, "#/components/schemas/")].Properties {
					if prop != "$schema" {
						names = append(names, at+" body."+prop)
					}
				}
			}
			if len(names) == 0 {
				names = []string{at}
			}
			for _, n := range names {
				out[n] = true
			}
		}
	}
	return out
}

// exists says whether a row's cell names something the surface has: a
// collected name, or for a CLI cell a command or one of its arguments, for an
// MCP cell a tool and for an HTTP cell an operation.
func (s surfaces) exists(kind, cell string) bool {
	switch kind {
	case "CLI":
		if s.cli[cell] {
			return true
		}
		path, arg, _ := strings.Cut(cell, " <")
		if arg != "" {
			return strings.Contains(s.commands[path], "<"+arg)
		}
		path, arg, _ = strings.Cut(cell, " [")
		if arg != "" {
			return strings.Contains(s.commands[path], "["+arg)
		}
		_, ok := s.commands[cell]
		return ok
	case "MCP":
		return s.mcp[cell] || hasPrefixKey(s.mcp, cell+".")
	default:
		return s.http[cell] || hasPrefixKey(s.http, cell+"?") || hasPrefixKey(s.http, cell+" body.")
	}
}

func hasPrefixKey(m map[string]bool, prefix string) bool {
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// mappingProblems lists, by surface, every collected name no row or more than
// one row maps, every cell that names nothing and every empty cell without a
// reason.
func mappingProblems(s surfaces, rows []surfaceRow) []string {
	seen := map[string]int{}
	var problems []string
	for _, r := range rows {
		for kind, cell := range map[string]string{"CLI": r.CLI, "MCP": r.MCP, "HTTP": r.HTTP} {
			switch {
			case cell == "" && r.Reason == "":
				problems = append(problems, kind+": the row "+r.CLI+"|"+r.MCP+"|"+r.HTTP+" has an empty cell and no reason")
			case cell != "" && !s.exists(kind, cell):
				problems = append(problems, kind+": "+cell+" names nothing at HEAD")
			case cell != "":
				seen[kind+" "+cell]++
			}
		}
	}
	for kind, names := range map[string]map[string]bool{"CLI": s.cli, "MCP": s.mcp, "HTTP": s.http} {
		for n := range names {
			switch seen[kind+" "+n] {
			case 0:
				problems = append(problems, kind+": "+n+" is in no row")
			case 1:
			default:
				problems = append(problems, kind+": "+n+" is in more than one row")
			}
		}
	}
	slices.Sort(problems)
	return problems
}

// filterSurfaces name, for each fleet filter, the surfaces whose rows reach
// its fields.
var filterSurfaces = []struct {
	filter  any
	markers []string
}{
	{fleet.EntityFilter{}, []string{"fleet entities", "pacto_fleet_entities.", "/api/fleet/entities?"}},
	{fleet.AttentionFilter{}, []string{"fleet attention", "pacto_fleet_attention.", "/api/fleet/attention?"}},
	{fleet.NeighborhoodQuery{}, []string{"fleet neighborhood", "pacto_fleet_neighborhood.", "/api/fleet/neighborhood?"}},
}

// unreachableFields lists every filter field no row of its surfaces names.
func unreachableFields(rows []surfaceRow) []string {
	var out []string
	for _, f := range filterSurfaces {
		reached := map[string]bool{}
		for _, r := range rows {
			for _, cell := range []string{r.CLI, r.MCP, r.HTTP} {
				if slices.ContainsFunc(f.markers, func(m string) bool { return strings.Contains(cell, m) }) {
					reached[paramKey(cell)] = true
				}
			}
		}
		typ := reflect.TypeOf(f.filter)
		for i := range typ.NumField() {
			name := strings.ToLower(typ.Field(i).Name)
			if !reached[name] && !reached[strings.TrimSuffix(name, "s")] {
				out = append(out, typ.Name()+"."+typ.Field(i).Name)
			}
		}
	}
	return out
}

// paramKey is a cell's parameter, lower-case without separators.
func paramKey(cell string) string {
	i := strings.LastIndexAny(cell, "?.<[-")
	p := strings.Trim(cell[i+1:], "<>[]. -")
	if strings.Contains(cell, " --") {
		p = cell[strings.Index(cell, " --")+3:]
	}
	return strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(p))
}

// TestSurfaceParameterMapping: every CLI flag, MCP argument and dashboard
// parameter of the fleet, delivery, impact and contract surfaces is in
// exactly one row of surfaceRows, every row names what exists and every fleet
// filter field is reachable from a row.
func TestSurfaceParameterMapping(t *testing.T) {
	s := collectSurfaces(t)
	for _, p := range mappingProblems(s, surfaceRows) {
		t.Error(p)
	}
	for _, f := range unreachableFields(surfaceRows) {
		t.Errorf("%s is reachable from no row", f)
	}
	// The check itself: one row less is a named failure.
	dropped := slices.DeleteFunc(slices.Clone(surfaceRows), func(r surfaceRow) bool { return r.MCP == "pacto_fleet_entities.owner_key" })
	expect(t, slices.Contains(mappingProblems(s, dropped), "MCP: pacto_fleet_entities.owner_key is in no row"), "a dropped row goes unnoticed")
}

// TestFleetToolsDocListsSurfaceRows: the "One question, three surfaces" table
// in docs/fleet-tools.md names every MCP tool and dashboard route a row of
// surfaceRows names, so the hand-written table follows the harness.
func TestFleetToolsDocListsSurfaceRows(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "fleet-tools.md"))
	expect(t, err == nil, "read: %v", err)
	_, table, ok := strings.Cut(string(raw), "## One question, three surfaces")
	expect(t, ok, "docs/fleet-tools.md has no One question, three surfaces section")
	table, _, _ = strings.Cut(table, "\n## ")
	for _, r := range surfaceRows {
		tool, _, _ := strings.Cut(r.MCP, ".")
		route, _, _ := strings.Cut(r.HTTP, "?")
		route, _, _ = strings.Cut(route, " body.")
		for _, name := range []string{tool, route} {
			if name != "" && !strings.Contains(table, "`"+name+"`") {
				t.Errorf("the surfaces table in docs/fleet-tools.md does not name `%s`", name)
			}
		}
	}
}
