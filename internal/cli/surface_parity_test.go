package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
	whyRecordRef     = "a registry reference reads with the local machine's registry credentials, so only the CLI and the local MCP server take one; " +
		"the remote MCP server and the dashboard read records through a Stage the caller can see"
	whyStatsWindow  = "the Metrics page takes a closed window that ends now (GET /api/stats?window), with the same totals"
	whyStatsPage    = "the Metrics page answers every service and environment at once and reads the stats repository pacto dashboard --stats-registry names"
	whyStatsRecords = "the records behind one number of the Metrics page; pacto stats and pacto_stats answer totals"
	whyMigrateLocal = "a repository-wide rewrite of pacto.yaml files is a local CLI step; pacto_edit raises the version of the contract it edits and the dashboard writes no contract files"
)

// surfaceRows maps every CLI flag, MCP argument and dashboard parameter of
// the fleet, delivery, impact and contract surfaces to its concept. A story
// that adds one adds its row here in the same commit.
var surfaceRows = slices.Concat(fleetRows(), fleetDetailRows(), impactRows(), deliveryRows(), deliveryWriteRows(), deliveryViewRows(), authoringRows(), statsRows(), publishRows(), evidenceRows(), ciRows())

func fleetRows() []surfaceRow {
	return slices.Concat(
		alike("fleet entities", "pacto_fleet_entities", "GET /api/fleet/entities", "", "business-domain", "consumes", "created-from", "environment",
			"has-non-compliant", "limit", "offering", "offset", "owner", "owner-key", "ownership", "phase", "sort", "source-kind", "status", "template",
			"declaration", "group", "capability", "domain", "readiness", "scope", "service", "source", "source-health", "failed", "lifecycle", "gap", "class"),
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
		alike("fleet search", "pacto_fleet_search", "GET /api/fleet/services", "", "declaration", "limit", "offset", "owner", "scope", "source", "status", "template"),
		[]surfaceRow{{"fleet search [text]", "pacto_fleet_search.text", "GET /api/fleet/services?text",
			"the CLI and MCP also list the external systems the text names; the dashboard finds them through GET /api/fleet/entities?kinds=external&text"}},
		alike("fleet search", "pacto_fleet_search", "", whySearchRoute, "business-domain", "compliance", "has-capability", "has-dependency", "not-ready", "offering", "ready", "workload"),
		[]surfaceRow{{"fleet search --label", "pacto_fleet_search.label", "", whySearchRoute}},
		alike("fleet status", "pacto_fleet_status", "", whyStatusRoute, "golden-path-deployed", "invalid", "lifecycle", "limit", "missing-readiness", "needs-attention",
			"non-compliant", "stale", "undeclared", "unknown", "unowned", "unresolved-deps", "unsatisfied-ranges"),
		alike("fleet status", "", "", "a coverage run record is a statistic a scheduled CLI job writes; MCP and the dashboard record nothing",
			"stats-file", "stats-registry"),
		[]surfaceRow{
			{"fleet status", "pacto_fleet_status", "GET /api/fleet/status", ""},
			{"fleet overview", "pacto_fleet_overview", "GET /api/fleet/overview", ""},
			{"fleet providers", "", "GET /api/fleet/providers", "MCP answers it as pacto_fleet_status.providers (no new tool, map X4)"},
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
			{"fleet entity --reached", "pacto_fleet_entity.reached", "GET /api/fleet/entities/{kind}?reached", ""},
			{"fleet entity --trust", "", "", whyAtServerStart},
			{"fleet versions <offering>", "pacto_fleet_versions.offering", "GET /api/fleet/versions?offering", ""},
			{"fleet get [service]", "pacto_fleet_get.service", "GET /api/fleet/service?key",
				"the CLI and MCP also take external:<identity>; the dashboard opens an external on its entity page (GET /api/fleet/entities/{kind}?key)"},
			{"fleet get --target", "pacto_fleet_get.target", "GET /api/fleet/target?key", ""},
			{"fleet graph <service>", "pacto_fleet_graph.service", "GET /api/fleet/services/{name}/graph?name",
				"the CLI and MCP also root at external:<identity>; the dashboard focuses an external in its neighborhood graph"},
			{"fleet graph --max-depth", "pacto_fleet_graph.max_depth", "GET /api/fleet/services/{name}/graph?maxDepth", ""},
			{"", "", "GET /api/fleet/services/{name}/graph?maxNodes", "the dashboard bounds the graph it draws; the CLI and MCP answer the whole tree"},
			{"fleet explain <subject>", "pacto_fleet_explain.subject", "", "the dashboard explains a subject, an external:<identity> included, on its entity pages"},
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
			{"", "pacto_impact.offering", "POST /api/fleet/impact body.offeringKey", "the CLI takes the offering as offering:<name> in --unavailable or --removed"},
		},
		alike("impact", "", "", "a run record is a CI statistic the CLI writes; MCP and the dashboard record no runs",
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
			{"delivery record get <id-or-ref>", "pacto_delivery_record_get.record", "GET /api/delivery/record?id", ""},
			{"", "pacto_delivery_record_get.ref", "", whyRecordRef},
			{"delivery record verify <ref>", "pacto_delivery_record_verify.ref", "", whyRecordRef},
			{"delivery record get --signers", "", "", whyAtServerStart + "; the local MCP server reads the published identity policy"},
			{"delivery record verify --signers", "", "", whyAtServerStart + "; the local MCP server reads the published identity policy"},
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
		alike("delivery history", "pacto_delivery_history", "GET /api/delivery/history", "", "at", "cause", "domain", "service", "since", "stage", "until"),
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
		alike("delivery approve", "", "", "the CLI signs the person's decision keyless itself; the dashboard records it through the recorder, which signs on the person's behalf",
			"fulcio-url", "keyless", "timestamp-url"),
		alike("delivery approve", "", "", "only the CLI approves from an intent record, signed keyless with the person's fresh login and no kubeconfig; MCP reads approval records and never writes one",
			"intent", "signers"),
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
		view("trust", "domain", "limit", "offset", "q", "service"),
		[]surfaceRow{
			{"delivery refusals", "pacto_delivery_refusals", "GET /api/delivery/refusals", ""},
			{"delivery caller", "pacto_delivery_caller", "GET /api/delivery/caller", ""},
			{"delivery types", "pacto_delivery_operation_types", "", "the dashboard's propose panel lists the types it offers"},
		},
	)
}

func authoringRows() []surfaceRow {
	return slices.Concat(
		alike("init", "pacto_create", "", whyCreateOnly, "call-window", "cluster", "continue", "delivery", "describe-only", "domain", "from", "insecure-registry", "max-items", "name",
			"no-transparency-log", "owner", "registry", "registry-secret", "run-plugins", "set", "values"),
		[]surfaceRow{
			{"init [dir...]", "pacto_create.path", "", whyCreateOnly},
			{"init --env", "pacto_create.environments", "", whyCreateOnly},
			{"init --fleet", "pacto_create.fleet_roots", "", whyCreateOnly},
			{"init --namespace", "pacto_create.namespaces", "", whyCreateOnly},
			{"init --plan", "pacto_create.dry_run", "", whyCreateOnly},
			{"init --out", "", "", "with --cluster the CLI writes one folder per workload; pacto_create writes at path"},
			{"init --trust", "", "", "pacto_create verifies a golden path with the trust config the server was started with"},
			{"init --external-catalog", "", "", "pacto_create classifies with the external catalog the server was started with, never one a caller names"},
			{"init", "pacto_create.pactify", "", "pacto init always reads the service folder; pacto_create does so with pactify"},
			{"", "pacto_create.paths", "", "the CLI takes several folders as arguments (init [dir...])"},
			{"validate [dir | oci://ref]", "pacto_check.path", "", whyCreateOnly},
			{"migrate [dir...]", "", "", whyMigrateLocal},
			{"migrate --to", "", "", whyMigrateLocal},
			{"migrate --dry-run", "", "", whyMigrateLocal},
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
		[]surfaceRow{
			{"stats --since", "pacto_stats.since", "GET /api/stats?window", "the Metrics page takes a closed 7d, 30d or 90d window that ends now"},
			{"stats --until", "pacto_stats.until", "", whyStatsWindow},
			{"stats --kind", "pacto_stats.kinds", "", whyStatsPage + "; the CLI repeats --kind, MCP takes comma-separated kinds"},
			{"stats --registry", "pacto_stats.registry", "", whyStatsPage + "; pacto_stats honours it over stdio only"},
		},
		alike("stats", "pacto_stats", "", whyStatsPage, "by", "service"),
		alike("", "", "GET /api/stats", whyDashboardPage, "services-offset", "services-limit"),
		alike("", "", "GET /api/stats/records", whyStatsRecords, "window", "metric", "environment", "offset", "limit"),
	)
}

// publishRows map pacto publish. Its stdio twins, pacto_publish and
// pacto_publish_plan, are outside the composed server the harness reads, and
// --propose is the promote pacto_delivery_operation_propose and
// POST /api/delivery/operations make, with the same on-behalf-of and correlation id.
func publishRows() []surfaceRow {
	const why = "pacto_publish and pacto_publish_plan take path, revision, image, registry, sign_key and no_transparency_log on pacto mcp --publish over stdio; the dashboard publishes nothing"
	const whyPropose = "publish proposes the promote itself; MCP and the dashboard propose it as a separate step (pacto_delivery_operation_propose, POST /api/delivery/operations)"
	return slices.Concat(
		alike("publish", "", "", why, "path", "plan", "revision", "image", "registry", "sign-key", "no-transparency-log"),
		alike("publish", "", "", whyPropose, "propose", "on-behalf-of", "correlation-id", "propose-timeout"),
		alike("publish", "", "", "overrides the CLI applies to the local contract before publishing; pacto_publish publishes the contract as it is", "set", "values"),
		alike("publish", "", "", "a run record is a CI statistic the CLI writes; MCP and the dashboard record no runs", "stats-context", "stats-file", "stats-registry"),
	)
}

// evidenceRows map pacto evidence usage: a provider adapter's scripted step
// that writes the usage EvidenceSet the service page's consumedOfferings
// consumption reads back on every surface.
func evidenceRows() []surfaceRow {
	return alike("evidence usage", "", "", "an adapter writes usage evidence with the CLI and signs it; MCP and the dashboard read it back as consumedOfferings consumption and write none",
		"collector", "configuration", "contract", "cost", "dependency", "environment", "external-ref", "metric", "observed-at", "outcome", "provider", "subject")
}

// ciRows map pacto ci, the CI entry point. Its shared fleet source flags are
// the ones every fleet command takes, so only its own flags are named.
func ciRows() []surfaceRow {
	const whyCI = "pacto ci is the CI entry point over a repository and a registry baseline; MCP has no repository-wide plan, pacto_impact answers one revision pair without ci's consumer-source and inferred rules, and the dashboard publishes nothing"
	return slices.Concat(
		alike("ci", "", "", whyCI, "registry", "explain"),
		alike("ci", "", "", "publishing runs in CI on the merged commit, signed as the CI identity; MCP never publishes and the dashboard publishes nothing", "publish", "image", "sign-key", "no-transparency-log"),
		alike("ci", "", "", "a run record is a CI statistic the CLI writes; MCP and the dashboard record no runs", "stats-context", "stats-file", "stats-registry"),
	)
}

// excludedFromSurfaces are the commands no other surface mirrors, each with
// the reason. Every other command has rows in surfaceRows, so a new command
// forces a choice: map it, or add it here with a reason.
var excludedFromSurfaces = map[string]string{
	"agent":                "a long-running host daemon that reports what runs on its node; the other surfaces read what it reports",
	"dashboard":            "starts the dashboard server, which is itself the surface the routes belong to",
	"diff":                 "compares two contracts in a pipeline without the fleet; MCP and the dashboard compare revisions with their consumers (pacto_impact, GET /api/fleet/impact)",
	"doc":                  "renders a contract's documentation to a file or a local site, a build step with no fleet to read",
	"evidence keygen":      "creates key material, which belongs at a shell prompt",
	"evidence provisioned": "an adapter's scripted step that writes provisioning evidence to sign",
	"evidence send":        "signs with a local key and sends evidence, which belongs at a shell prompt",
	"evidence serve":       "starts the long-running Evidence Server",
	"evidence sign":        "signs with a local key, which belongs at a shell prompt",
	"evidence verify":      "verifies a signed evidence file on disk",
	"export backstage":     "writes a catalog file for a repository; the dashboard serves the same entities at GET /api/backstage/entities outside the OpenAPI document",
	"generate":             "runs a code generation plugin over a local contract, a build step",
	"graph":                "resolves the dependency tree of a bundle path or ref outside any fleet; the fleet's tree is pacto fleet graph, which pacto_fleet_graph and GET /api/fleet/services/{name}/graph mirror",
	"lock":                 "writes pacto.lock next to a local contract, a build step",
	"login":                "stores registry credentials on this machine",
	"logout":               "removes registry credentials from this machine",
	"mcp":                  "starts the MCP server, which is itself the surface the tools belong to",
	"otel observe":         "a long-running collector of OpenTelemetry traces",
	"pack":                 "builds a bundle archive from a local folder, a build step",
	"pull":                 "downloads a bundle to a local folder",
	"push":                 "writes a bundle to a registry, a pipeline step the other surfaces never take",
	"run":                  "wraps a local process and lives as long as it does",
	"stats publish":        "writes unsigned run records to a registry, a pipeline step the other surfaces never take",
	"tui":                  "starts the terminal UI, a surface of its own over the same fleet",
	"update":               "replaces the running binary",
	"version":              "reports the running binary's own version",
}

// excludedRoutes are the dashboard operations no CLI command or MCP tool
// mirrors, each with the reason.
const whyOfflineRoute = "answered only by the offline static export pacto doc --format html builds for one bundle; a live dashboard serves /api/fleet/*, which the rows map"

var excludedRoutes = map[string]string{
	"GET /api/diff":                               whyOfflineRoute,
	"GET /api/graph":                              whyOfflineRoute,
	"GET /api/services":                           whyOfflineRoute,
	"GET /api/services/{name}":                    whyOfflineRoute,
	"GET /api/services/{name}/dependents":         whyOfflineRoute,
	"GET /api/services/{name}/refs":               whyOfflineRoute,
	"GET /api/services/{name}/sources":            whyOfflineRoute,
	"GET /api/services/{name}/versions":           whyOfflineRoute,
	"GET /api/services/{name}/versions/{version}": whyOfflineRoute,
	"GET /api/session":                            "the sign-in state and CSRF token of the dashboard's own pages; the CLI and MCP authenticate on every call",
	"GET /api/sources":                            "the dashboard's Data sources page; the CLI and MCP report each source's status in every fleet answer",
	"POST /api/refresh":                           "rebuilds the dashboard's long-lived snapshot; the CLI and MCP build a fresh snapshot on every call",
}

// ownFlags are the flags of c the mapping covers: its non-inherited flags
// except help and the fleet source flags every fleet command shares.
func ownFlags(c *cobra.Command) []string {
	shared := pflag.NewFlagSet("shared", pflag.ContinueOnError)
	addFleetSourceFlags(shared)
	var out []string
	c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		if sf := shared.Lookup(f.Name); f.Name != "help" && (sf == nil || sf.Usage != f.Usage) {
			out = append(out, f.Name)
		}
	})
	return out
}

// commandTree is every runnable command path of the tree (a command with no
// subcommand) plus every parent with flags of its own, such as delivery.
func commandTree(root *cobra.Command) map[string]*cobra.Command {
	out := map[string]*cobra.Command{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c != root && (!c.HasSubCommands() || len(ownFlags(c)) > 0) {
			out[strings.TrimPrefix(c.CommandPath(), "pacto ")] = c
		}
		for _, k := range c.Commands() {
			if k.Name() != "help" {
				walk(k)
			}
		}
	}
	walk(root)
	return out
}

// rowCommand is the command path a CLI cell names: the words before its
// first flag or argument.
func rowCommand(cell string) string {
	if i := strings.IndexAny(cell, "-<["); i > 0 {
		return strings.TrimSpace(cell[:i])
	}
	return cell
}

// rowOperation is the dashboard operation an HTTP cell names.
func rowOperation(cell string) string {
	op, _, _ := strings.Cut(cell, "?")
	op, _, _ = strings.Cut(op, " body.")
	return op
}

// mappedBy is the set of what rows name, keyed by f.
func mappedBy(rows []surfaceRow, f func(surfaceRow) string) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		if k := f(r); k != "" {
			out[k] = true
		}
	}
	return out
}

// surfaces is what the three surfaces expose at HEAD.
type surfaces struct {
	cli, mcp, http map[string]bool
	commands       map[string]string // command path -> Use
}

func collectSurfaces(t *testing.T) surfaces {
	t.Helper()
	s := surfaces{cli: map[string]bool{}, commands: map[string]string{}}
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	for path, cmd := range commandTree(root) {
		if _, ok := excludedFromSurfaces[path]; ok {
			continue
		}
		s.commands[path] = cmd.Use
		for _, f := range ownFlags(cmd) {
			s.cli[path+" --"+f] = true
		}
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
		Delivery: &pactomcp.DeliveryMode{Writes: true, Reads: func(context.Context, string, url.Values) (json.RawMessage, error) { return nil, nil }, Refs: &app.Delivery{}},
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
// every dashboard operation under /api/ that excludedRoutes does not name, or
// the operation when it takes none.
func httpNames(t *testing.T) map[string]bool {
	t.Helper()
	doc := openAPIDoc(t)
	out := map[string]bool{}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			at := strings.ToUpper(method) + " " + path
			if _, ok := excludedRoutes[at]; ok || !strings.HasPrefix(path, "/api/") {
				continue
			}
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

// openAPIDocument is the part of the dashboard's OpenAPI document the mapping reads.
type openAPIDocument struct {
	Paths      map[string]map[string]openAPIOp `json:"paths"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]any `json:"properties"`
		} `json:"schemas"`
	} `json:"components"`
}

func openAPIDoc(t *testing.T) openAPIDocument {
	t.Helper()
	raw, err := dashboard.ExportOpenAPI()
	expect(t, err == nil, "openapi: %v", err)
	var doc openAPIDocument
	expect(t, json.Unmarshal(raw, &doc) == nil, "openapi json")
	return doc
}

// apiOperations are "<METHOD> <path>" for every dashboard operation under /api/.
func apiOperations(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for path, ops := range openAPIDoc(t).Paths {
		for method := range ops {
			if strings.HasPrefix(path, "/api/") {
				out[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return out
}

// unclassified lists every name of all that rows do not name and excluded
// does not list, sorted.
func unclassified(all map[string]bool, mapped map[string]bool, excluded map[string]string) []string {
	var out []string
	for n := range all {
		if _, ok := excluded[n]; !ok && !mapped[n] {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// TestEveryCommandIsEitherMappedOrExcluded: every command of the tree has rows
// in surfaceRows or a reason in excludedFromSurfaces, so a new command is
// mirrored on MCP and the dashboard or excluded on the record.
func TestEveryCommandIsEitherMappedOrExcluded(t *testing.T) {
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	all := map[string]bool{}
	for path := range commandTree(root) {
		all[path] = true
	}
	mapped := mappedBy(surfaceRows, func(r surfaceRow) string { return rowCommand(r.CLI) })
	for _, path := range unclassified(all, mapped, excludedFromSurfaces) {
		t.Errorf("pacto %s is in no row of surfaceRows and not in excludedFromSurfaces", path)
	}
	// The check itself: a new command is a named failure.
	all["throwaway"] = true
	expect(t, slices.Contains(unclassified(all, mapped, excludedFromSurfaces), "throwaway"), "a new command goes unnoticed")
}

// TestEveryRouteIsEitherMappedOrExcluded: every /api/ operation of the
// dashboard has rows in surfaceRows or a reason in excludedRoutes.
func TestEveryRouteIsEitherMappedOrExcluded(t *testing.T) {
	all := apiOperations(t)
	mapped := mappedBy(surfaceRows, func(r surfaceRow) string { return rowOperation(r.HTTP) })
	for _, op := range unclassified(all, mapped, excludedRoutes) {
		t.Errorf("%s is in no row of surfaceRows and not in excludedRoutes", op)
	}
	all["GET /api/throwaway"] = true
	expect(t, slices.Contains(unclassified(all, mapped, excludedRoutes), "GET /api/throwaway"), "a new route goes unnoticed")
}

// TestNoStaleSurfaceExclusion: an exclusion names a command or route that
// exists, has a reason and has no rows, since a row would make it a false claim.
func TestNoStaleSurfaceExclusion(t *testing.T) {
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	commands := commandTree(root)
	cmdRows := mappedBy(surfaceRows, func(r surfaceRow) string { return rowCommand(r.CLI) })
	for path, reason := range excludedFromSurfaces {
		_, ok := commands[path]
		expect(t, ok, "excludedFromSurfaces names %q, which is no command", path)
		expect(t, !cmdRows[path], "excludedFromSurfaces names %q, which has rows", path)
		expect(t, reason != "", "the exclusion of %q has no reason", path)
	}
	ops := apiOperations(t)
	opRows := mappedBy(surfaceRows, func(r surfaceRow) string { return rowOperation(r.HTTP) })
	for op, reason := range excludedRoutes {
		expect(t, ops[op], "excludedRoutes names %q, which is no operation", op)
		expect(t, !opRows[op], "excludedRoutes names %q, which has rows", op)
		expect(t, reason != "", "the exclusion of %q has no reason", op)
	}
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

// TestUsageErrorsEmitOneJSONDocument: a failure cobra reports before RunE
// (a wrong argument count, an unknown flag before --output-format) still
// answers one exit document under --output-format json.
func TestUsageErrorsEmitOneJSONDocument(t *testing.T) {
	statsOff(t)
	ciEnvOff(t)
	for _, args := range [][]string{
		{"ci", "a", "b", "--output-format", "json"},
		{"ci", "--registy", "x", "--output-format", "json"},
		{"ci", "--registy", "x", "--output-format=json"},
	} {
		out, _, err := runRoot(t, defaultDeps(), nil, args...)
		exit, _ := decode(t, out)["exit"].(map[string]any)
		if err == nil || exit["code"] != 1.0 || exit["reason"] != err.Error() {
			t.Errorf("%v: err %v, stdout %q", args, err, out)
		}
	}
	t.Setenv("PACTO_OUTPUT_FORMAT", "json")
	if out, _, _ := runRoot(t, defaultDeps(), nil, "ci", "a", "b"); decode(t, out)["exit"] == nil {
		t.Errorf("PACTO_OUTPUT_FORMAT=json: stdout %q", out)
	}
	t.Setenv("PACTO_OUTPUT_FORMAT", "")
	if out, _, err := runRoot(t, defaultDeps(), nil, "ci", "a", "b"); err == nil || out != "" {
		t.Errorf("text: err %v, stdout %q", err, out)
	}
}

// jsonRuns are the arguments TestEverySurfaceCommandEmitsOneJSONDocument runs
// each mapped command with, offline: most of them fail (no cluster, no
// registry), which is the case under test. A mapped command without an entry
// fails the test, so the table is deny-by-default too.
var jsonRuns = map[string][]string{
	"ci":                     {"."},
	"delivery approve":       {"-n", "prod", "held"},
	"delivery autonomy":      {},
	"delivery blame":         {"--stage", "prod"},
	"delivery caller":        {},
	"delivery cancel":        {"-n", "prod", "op"},
	"delivery candidates":    {"--stage", "prod"},
	"delivery describe":      {"promote"},
	"delivery environments":  {},
	"delivery explain":       {"-n", "prod", "op"},
	"delivery get":           {"-n", "prod", "op"},
	"delivery history":       {"--stage", "prod"},
	"delivery inbox":         {},
	"delivery lifecycle":     {"--service", "payments"},
	"delivery plan":          {"--stage", "prod", "--type", "promote", "--to", "1.0.0"},
	"delivery propose":       {"--stage", "prod", "--type", "promote", "--to", "1.0.0"},
	"delivery published":     {},
	"delivery record get":    {"--stage", "prod", "rec"},
	"delivery record verify": {"--stage", "prod"},
	"delivery refusals":      {},
	"delivery status":        {"--service", "payments"},
	"delivery trust":         {},
	"delivery types":         {},
	"delivery undo":          {"-n", "prod", "op"},
	"evidence usage":         {"--contract", "does-not-exist"},
	"explain":                {"does-not-exist"},
	"fleet attention":        {},
	"fleet entities":         {},
	"fleet entity":           {"service", "payments"},
	"fleet explain":          {"payments"},
	"fleet get":              {"payments"},
	"fleet graph":            {"payments"},
	"fleet neighborhood":     {"payments"},
	"fleet overview":         {},
	"fleet providers":        {},
	"fleet reconcile":        {"--traces", "does-not-exist.json"},
	"fleet search":           {},
	"fleet snapshot":         {},
	"fleet status":           {},
	"fleet versions":         {"payments"},
	"impact":                 {"does-not-exist", "does-not-exist-either"},
	"init":                   {"--plan", "."},
	"migrate":                {"--dry-run", "does-not-exist"},
	"publish":                {"does-not-exist"},
	"stats":                  {"--registry", "127.0.0.1:1/acme/pacto-stats"},
	"validate":               {"does-not-exist"},
}

// TestEverySurfaceCommandEmitsOneJSONDocument: every mapped command answers
// --output-format json with exactly one JSON document whatever its exit, and a
// failed one carries its exit code and reason, so an agent never parses prose.
func TestEverySurfaceCommandEmitsOneJSONDocument(t *testing.T) {
	statsOff(t)
	ciEnvOff(t)
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	for path, cmd := range commandTree(root) {
		if _, excluded := excludedFromSurfaces[path]; excluded || !cmd.Runnable() {
			continue
		}
		args, ok := jsonRuns[path]
		if !ok {
			t.Errorf("pacto %s is mapped but has no jsonRuns entry", path)
			continue
		}
		t.Run(path, func(t *testing.T) {
			out, stderr, err := runRoot(t, defaultDeps(), nil, slices.Concat(strings.Fields(path), args, []string{"--output-format", "json"})...)
			dec := json.NewDecoder(strings.NewReader(out))
			var doc any
			if derr := dec.Decode(&doc); derr != nil {
				t.Fatalf("stdout is no JSON document (%v): %q (err %v, stderr %q)", derr, out, err, stderr)
			}
			if derr := dec.Decode(&doc); derr != io.EOF {
				t.Fatalf("stdout holds more than one JSON document: %q", out)
			}
			if obj, _ := doc.(map[string]any); err != nil && obj["exit"] == nil {
				t.Errorf("failed (%v) but the document carries no exit: %q", err, out)
			}
		})
	}
}
