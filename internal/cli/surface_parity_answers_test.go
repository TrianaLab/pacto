package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const parityState = "state:\n  type: stateless\n  persistence:\n    scope: local\n    durability: ephemeral\n  dataCriticality: low\n"

// parityFixture writes the inputs every surface reads: a bundle tree with an
// unowned service and an offering whose consumer has no provisioning report,
// targets in the prod, staging and secret namespaces, a trace file with an
// observed-only call, an Evidence Server that is down and one whose evidence
// is old. It returns the fleet source flags.
func parityFixture(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	write := func(dir, body string) {
		mustWriteFile(t, filepath.Join(root, dir, "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n"+body+"workload: service\n"+parityState)
	}
	write("postgresql", "  name: postgresql\n  version: \"16.1.0\"\n  offering: postgresql\n  owner:\n    team: infra-data\n")
	write("app", "  name: app\n  version: \"1.0.0\"\n  owner:\n    team: shop\ndependencies:\n  - name: postgresql\n    ref: oci://ghcr.io/acme/postgresql\n    required: true\n    compatibility: ^16.0.0\n")
	write("lonely", "  name: lonely\n  version: \"1.0.0\"\n")
	traces := filepath.Join(t.TempDir(), "traces.json")
	mustWriteFile(t, traces, `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"app"}}]},"scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"lonely"}}]}]}]}]}`)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":"pacto.dev/evidence-source/v2","health":{"status":"ready","subjects":2},"targets":[{"subject":"app","service":"app","producer":"prod","compliance":"Compliant","coverage":{"evaluated":3,"required":3},"evidenceAt":"2026-01-01T00:00:00Z","acceptedAt":"2026-01-01T00:00:00Z"},{"subject":"lonely","service":"lonely","producer":"prod","compliance":"Unknown","coverage":{"evaluated":0,"required":3},"evidenceAt":"2026-01-01T00:00:00Z","acceptedAt":"2026-01-01T00:00:00Z"}]}`))
	}))
	t.Cleanup(down.Close)
	t.Cleanup(old.Close)
	targets := filepath.Join(t.TempDir(), "targets.yaml")
	mustWriteFile(t, targets, "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n"+
		"  - {scope: prod, kind: kubernetes, name: app, service: app, compliance: Compliant}\n"+
		"  - {scope: staging, kind: kubernetes, name: postgresql, service: postgresql, compliance: Unknown}\n"+
		"  - {scope: secret, kind: kubernetes, name: lonely, service: lonely, compliance: NonCompliant}\n")
	return []string{"--local", root, "--target-state", targets, "--traces", traces, "--evidence-url", down.URL, "--evidence-url", old.URL, "--freshness", "1m"}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	expect(t, os.MkdirAll(filepath.Dir(path), 0o755) == nil && os.WriteFile(path, []byte(body), 0o644) == nil, "write %s", path)
}

// cliJSON runs pacto args with the fixture flags and JSON output.
func cliJSON(t *testing.T, flags []string, args ...string) string {
	t.Helper()
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	root.SetArgs(append(append(args, flags...), "--output-format", "json"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	expect(t, root.Execute() == nil, "pacto %v", args)
	return out.String()
}

// parityQuery is one question asked of every surface.
type parityQuery struct {
	name  string
	cli   []string
	tool  string
	args  map[string]any
	route string
}

func parityQueries(revision string) []parityQuery {
	return []parityQuery{
		{"services", []string{"fleet", "entities", "--kind", "service"}, "pacto_fleet_entities", map[string]any{"kind": "service"}, "/api/fleet/entities?kinds=service"},
		{"offerings", []string{"fleet", "entities", "--kind", "offering"}, "pacto_fleet_entities", map[string]any{"kind": "offering"}, "/api/fleet/entities?kinds=offering"},
		{"owners", []string{"fleet", "entities", "--kind", "owner"}, "pacto_fleet_entities", map[string]any{"kind": "owner"}, "/api/fleet/entities?kinds=owner"},
		{"service app", []string{"fleet", "entity", "service", "app"}, "pacto_fleet_entity", map[string]any{"kind": "service", "key": "app"}, "/api/fleet/entities/service?key=app"},
		{"revision", []string{"fleet", "entity", "revision", revision}, "pacto_fleet_entity", map[string]any{"kind": "revision", "key": revision}, "/api/fleet/entities/revision?key=" + url.QueryEscape(revision)},
		{"offering postgresql", []string{"fleet", "entity", "offering", "postgresql"}, "pacto_fleet_entity", map[string]any{"kind": "offering", "key": "postgresql"}, "/api/fleet/entities/offering?key=postgresql"},
		{"attention", []string{"fleet", "attention"}, "pacto_fleet_attention", map[string]any{}, "/api/fleet/attention"},
		{"neighborhood app", []string{"fleet", "neighborhood", "app"}, "pacto_fleet_neighborhood", map[string]any{"service": "app"}, "/api/fleet/neighborhood?kind=service&key=app"},
		{"versions postgresql", []string{"fleet", "versions", "postgresql"}, "pacto_fleet_versions", map[string]any{"offering": "postgresql"}, "/api/fleet/versions?offering=postgresql"},
	}
}

// buildVolatile are the values each build stamps with its own clock: the
// three surfaces build three snapshots from the same inputs, so these differ
// by when each ran and are compared only for being present on every side.
var buildVolatile = []string{"asOf", "snapshotId", "lastSuccessfulSync", "observedAt"}

// knownShapeDifferences are the fields a surface answers differently on
// purpose, by query name, with the reason. Empty: href (dashboard links the
// CLI does not print) is the only difference, and normalise drops it.
var knownShapeDifferences = map[string]string{}

// normalise decodes one answer, drops every href and stamps each
// build-volatile value it holds.
func normalise(t *testing.T, what, body string) any {
	t.Helper()
	var v any
	expect(t, json.Unmarshal([]byte(body), &v) == nil, "%s: not JSON: %s", what, body)
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			delete(v, "href")
			for _, k := range buildVolatile {
				if x, ok := v[k]; ok && x != "" {
					v[k] = "<build>"
				}
			}
			for _, e := range v {
				walk(e)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(v)
	return v
}

// firstDifference names the first path where a and b differ, or "".
func firstDifference(path string, a, b any) string {
	switch a := a.(type) {
	case map[string]any:
		bm, ok := b.(map[string]any)
		if !ok {
			return path
		}
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		sorted := slices.Sorted(maps.Keys(keys))
		for _, k := range sorted {
			if d := firstDifference(path+"."+k, a[k], bm[k]); d != "" {
				return d
			}
		}
		return ""
	case []any:
		bs, ok := b.([]any)
		if !ok || len(a) != len(bs) {
			return path
		}
		for i := range a {
			if d := firstDifference(fmt.Sprintf("%s[%d]", path, i), a[i], bs[i]); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(a, b) {
		return path
	}
	return ""
}

// sameOnSurfaces fails naming the field where two surfaces' answers differ.
func sameOnSurfaces(t *testing.T, query string, answers map[string]string) {
	t.Helper()
	if _, ok := knownShapeDifferences[query]; ok {
		return
	}
	names := slices.Sorted(maps.Keys(answers))
	base := normalise(t, query+" "+names[0], answers[names[0]])
	for _, n := range names[1:] {
		if d := firstDifference("", base, normalise(t, query+" "+n, answers[n])); d != "" {
			t.Errorf("%s: %s and %s differ at %s\n%s: %s\n%s: %s", query, names[0], n, d, names[0], answers[names[0]], n, answers[n])
		}
	}
}

// paritySurfaces are the fixture's MCP sessions (without and with
// --dashboard-url) and its dashboard API, each built from flags as its
// command builds it.
func paritySurfaces(t *testing.T, flags []string) (plain, linked *mcpsdk.ClientSession, dash http.Handler) {
	t.Helper()
	svc := app.NewService(nil, nil)
	session := func(extra ...string) (*mcpsdk.ClientSession, *cobra.Command) {
		cmd := newMCPCommand(svc, "v", defaultDeps())
		cmd.SetContext(context.Background())
		cmd.SetErr(io.Discard)
		expect(t, cmd.ParseFlags(append(append([]string{"--fleet"}, flags...), extra...)) == nil, "flags")
		server, err := buildMCPServer(cmd, svc, "v", nil, defaultDeps())
		expect(t, err == nil, "server: %v", err)
		return sessionOn(t, server), cmd
	}
	plain, cmd := session()
	linked, _ = session("--dashboard-url", "https://dash.example")
	return plain, linked, dashboard.NewServer(dashboard.Options{Fleet: func(ctx context.Context) (*fleet.Query, error) {
		snap, err := svc.Fleet(ctx, fleetOptions(cmd))
		if err != nil {
			return nil, err
		}
		return fleet.NewQuery(snap), nil
	}}).APIHandler()
}

// TestSurfaceParityAnswers: one fixture, read through the CLI, MCP and the
// dashboard, answers the same, completeness, limitations and unknown tallies
// included; only dashboard links differ.
func TestSurfaceParityAnswers(t *testing.T) {
	flags := parityFixture(t)
	plain, linked, dash := paritySurfaces(t, flags)
	text, _ := callText(t, plain, "pacto_fleet_entity", map[string]any{"kind": "service", "key": "app"})
	revision := regexp.MustCompile(`app@sha256:[0-9a-f]{64}`).FindString(text)
	expect(t, revision != "", "app has no revision: %s", text)
	for _, q := range parityQueries(revision) {
		m, isErr := callText(t, plain, q.tool, q.args)
		expect(t, !isErr, "%s: %s", q.name, m)
		l, _ := callText(t, linked, q.tool, q.args)
		sameOnSurfaces(t, q.name, map[string]string{
			"cli": cliJSON(t, flags, q.cli...), "mcp": m, "mcp --dashboard-url": l,
			"dashboard": routeBody(t, dash, context.Background(), q.route),
		})
	}
	// The fixture is the hard case: a partial answer with its limitations and
	// unknowns, which every surface above had to carry.
	services := cliJSON(t, flags, "fleet", "entities", "--kind", "service")
	for _, want := range []string{`"completeness": "partial"`, `"SOURCE_UNAVAILABLE"`, `"unknown": 1`} {
		expect(t, strings.Contains(services, want), "the fixture lacks %s: %s", want, services)
	}
}

// aliceView narrows a fleet to the prod and staging namespaces: one Viewer
// over a Review that lets alice read those two.
func aliceView() func(context.Context, *fleet.Query) (*fleet.Query, error) {
	viewer := authn.NewViewer(func(_ context.Context, p authn.Person, ns string) (bool, error) {
		return p.User == "alice" && (ns == "prod" || ns == "staging"), nil
	})
	return func(ctx context.Context, q *fleet.Query) (*fleet.Query, error) {
		return viewer.Query(ctx, authn.Person{User: "alice"}, q)
	}
}

// fixtureSource builds the fixture's snapshot once from flags, as the CLI
// builds it.
func fixtureSource(t *testing.T, flags []string) pactomcp.FleetQuerySource {
	t.Helper()
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	expect(t, cmd.ParseFlags(flags) == nil, "flags")
	snap, err := svc.Fleet(context.Background(), fleetOptions(cmd))
	expect(t, err == nil, "fleet: %v", err)
	q := fleet.NewQuery(snap)
	return func(context.Context) (*fleet.Query, error) { return q, nil }
}

// TestSurfaceParityRestrictedView: the same caller's view narrows MCP and the
// dashboard identically, for fleet reads, delivery reads and impact refs. The
// CLI is the local user and is not narrowed.
func TestSurfaceParityRestrictedView(t *testing.T) {
	src, view := fixtureSource(t, parityFixture(t)), aliceView()
	fm := &pactomcp.FleetMode{Src: src, Opts: pactomcp.FleetOptions{View: func(ctx context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return view(ctx, q)
	}}}
	d := blindDeps()
	id := baseDashboardIdentity(&rest.Config{Host: "https://k"}, d, nil, delivery.PolicyNamespace)
	act, who := actFor(t, d), remoteCallerKey(authn.Person{User: "alice"})
	server, err := pactomcp.New("v", pactomcp.Authoring{Remote: true}, pactomcp.Modes{Fleet: fm, Delivery: &pactomcp.DeliveryMode{Reads: mcpDeliveryReads(id, fm, "")(act, who)}})
	expect(t, err == nil, "server: %v", err)
	session := sessionOn(t, server)
	dash := dashboard.NewServer(dashboard.Options{Fleet: src, View: view, Delivery: func(*http.Request) (dashboard.DeliveryAPI, error) {
		return newDashDelivery(id, act, who), nil
	}}).APIHandler()

	text, _ := callText(t, session, "pacto_fleet_entity", map[string]any{"kind": "service", "key": "app"})
	for _, q := range parityQueries(regexp.MustCompile(`app@sha256:[0-9a-f]{64}`).FindString(text)) {
		m, isErr := callText(t, session, q.tool, q.args)
		expect(t, !isErr, "%s: %s", q.name, m)
		dashBody := routeBody(t, dash, context.Background(), q.route)
		sameOnSurfaces(t, q.name+" for alice", map[string]string{"mcp": m, "dashboard": dashBody})
		expect(t, !strings.Contains(m, "lonely") && !strings.Contains(dashBody, "lonely"), "%s names the hidden service: %s", q.name, m)
	}
	services, _ := callText(t, session, "pacto_fleet_entities", map[string]any{"kind": "service"})
	expect(t, strings.Contains(services, fleet.LimitationHiddenByPermissions), "the view says it hid something: %s", services)

	for _, route := range []string{"inbox", "environments"} {
		m, isErr := callText(t, session, "pacto_delivery_"+route, map[string]any{})
		expect(t, !isErr, "%s: %s", route, m)
		sameOnSurfaces(t, route+" for alice", map[string]string{"mcp": m, "dashboard": routeBody(t, dash, context.Background(), "/api/delivery/"+route)})
		l := listingOf(t, m)
		expect(t, !slices.Contains(l.Unreadable, "secret") && !strings.Contains(m, "lonely"), "%s names the hidden namespace: %s", route, m)
	}
}

// impactViewFleet: orders runs in prod and vault in secret, each a pinned
// registry revision.
func impactViewFleet(t *testing.T) pactomcp.FleetQuerySource {
	t.Helper()
	rev := func(name, digest string) fleet.RawRevision {
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService,
			Service: contract.Service{Name: name, Version: "1.0.0"}}, FS: fstest.MapFS{}},
			RequestedRef: "oci://reg.example/" + name + ":1", ResolvedRef: "oci://reg.example/" + name + "@" + digest, Digest: digest}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Revisions: []fleet.RawRevision{rev("orders", "sha256:"+strings.Repeat("a", 64)), rev("vault", "sha256:"+strings.Repeat("b", 64))},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "orders", Service: "orders"},
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "vault", Service: "vault"}},
	}))
	expect(t, err == nil, "fleet: %v", err)
	q := fleet.NewQuery(snap)
	return func(context.Context) (*fleet.Query, error) { return q, nil }
}

// TestSurfaceParityImpactRefs: pacto_impact over HTTP and a shared
// dashboard's impact route refuse the ref of a revision the caller cannot see
// and resolve a visible one to the same pinned ref.
func TestSurfaceParityImpactRefs(t *testing.T) {
	src, view := impactViewFleet(t), aliceView()
	mcpSvc, mcpPulled := pullRecorder()
	dashSvc, dashPulled := pullRecorder()
	server, err := pactomcp.New("v", pactomcp.Authoring{Remote: true}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{Src: src, Opts: pactomcp.FleetOptions{
		Impact: httpImpact(mcpSvc, false),
		View: func(ctx context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
			return view(ctx, q)
		},
	}}})
	expect(t, err == nil, "server: %v", err)
	session := sessionOn(t, server)
	dash := dashboard.NewServer(dashboard.Options{Fleet: src, View: view, Impact: impactProviderForFleet(dashSvc, false)}).APIHandler()
	ask := func(old string) (string, string) {
		m, _ := callText(t, session, "pacto_impact", map[string]any{"old_ref": old, "new_ref": "oci://reg.example/orders:1"})
		return m, routeBody(t, dash, context.Background(), "/api/fleet/impact?old="+url.QueryEscape(old)+"&new="+url.QueryEscape("oci://reg.example/orders:1"))
	}
	m, d := ask("oci://reg.example/vault:1")
	expect(t, strings.Contains(m, "is not a revision in your fleet view") && strings.Contains(d, "is not a revision in your fleet view"),
		"a hidden revision's ref is refused on both:\nmcp: %s\ndashboard: %s", m, d)
	expect(t, len(*mcpPulled) == 0 && len(*dashPulled) == 0, "a refused ref resolved: %v %v", *mcpPulled, *dashPulled)
	_, _ = ask("oci://attacker.example/x@sha256:" + strings.Repeat("a", 64))
	expect(t, len(*mcpPulled) == 1 && slices.Equal(*mcpPulled, *dashPulled) && strings.Contains((*mcpPulled)[0], "reg.example/orders@sha256:"),
		"a visible ref resolves to the same pinned ref: mcp %v, dashboard %v", *mcpPulled, *dashPulled)
}

// TestCapabilitiesKeysMatchDashboard: every key pacto_capabilities shares with
// GET /api/capabilities has the same JSON type and value on both, for a fleet
// server and for a fleet and delivery server.
func TestCapabilitiesKeysMatchDashboard(t *testing.T) {
	flags := parityFixture(t)
	for _, withDelivery := range []bool{false, true} {
		d, args := defaultDeps(), append([]string{"--fleet"}, flags...)
		opts := dashboard.Options{Fleet: fixtureSource(t, flags), Impact: impactProviderForFleet(app.NewService(nil, nil), true)}
		if withDelivery {
			d, args = readDeps(nil), append(args, "--delivery")
			opts.Auth = &dashboard.Auth{Local: true, State: func(*http.Request) dashboard.AuthState { return dashboard.AuthState{} }}
			opts.Delivery = func(*http.Request) (dashboard.DeliveryAPI, error) { return nil, nil }
		}
		svc := app.NewService(nil, nil)
		cmd := newMCPCommand(svc, "v", d)
		cmd.SetContext(context.Background())
		cmd.SetErr(io.Discard)
		expect(t, cmd.ParseFlags(args) == nil, "flags")
		server, err := buildMCPServer(cmd, svc, "v", nil, d)
		expect(t, err == nil, "server: %v", err)
		m, _ := callText(t, sessionOn(t, server), "pacto_capabilities", map[string]any{})
		var mcp, dash map[string]any
		expect(t, json.Unmarshal([]byte(m), &mcp) == nil, "capabilities: %s", m)
		body := routeBody(t, dashboard.NewServer(opts).APIHandler(), context.Background(), "/api/capabilities")
		expect(t, json.Unmarshal([]byte(body), &dash) == nil, "route: %s", body)
		var shared []string
		for k, v := range mcp {
			if dv, ok := dash[k]; ok {
				shared = append(shared, k)
				expect(t, reflect.TypeOf(v) == reflect.TypeOf(dv) && reflect.DeepEqual(v, dv), "delivery=%v: %s is %#v on MCP and %#v on the dashboard", withDelivery, k, v, dv)
			}
		}
		slices.Sort(shared)
		expect(t, slices.Equal(shared, []string{"delivery", "fleet", "impact", "observed"}), "shared keys: %v", shared)
		expect(t, mcp["delivery"] == withDelivery && mcp["observed"] == true, "delivery=%v: %s", withDelivery, m)
	}
}
