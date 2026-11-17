package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const billingName = "k8s:shop/Deployment/billing"

// inferredLeakStrings are the strings of the Decision 9 object
// (internal/testutil/testdata/leak-deployment.json) no answer may carry, and
// the escape of the pwn workload's image.
var inferredLeakStrings = []string{"DB_PASSWORD", "hunter2", "API_KEY", "billing-creds", "token", "billing-config",
	"s3cr3t-hdr", "s3cr3t-query", "s3cr3t-exec", "s3cr3t-arg", "last-applied", "\x1b"}

// inferredQuery is a fleet read from a fake cluster whose Deployments are the
// Decision 9 object (billing) and pwn, whose image is a terminal escape,
// beside orders, a declared service in shop. With called set, a kernel
// observer saw billing call orders.
func inferredQuery(t *testing.T, called bool) *fleet.Query {
	t.Helper()
	obj, err := os.ReadFile("../testutil/testdata/leak-deployment.json")
	expect(t, err == nil, "fixture: %v", err)
	pwn := strings.ReplaceAll(strings.ReplaceAll(string(obj), `"registry.example/billing:1.4.2"`, `"\u001b]0;pwn\u0007"`), `"name": "billing",`, `"name": "pwn",`)
	client := &fakeClusterClient{workloads: map[string]string{"deployments": `{"items":[` + string(obj) + "," + pwn + `]}`}}
	srcs := []fleet.Source{
		fleetsrc.NewWorkloadSource("k8s-workloads", nil, client, nil),
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{
			Targets: []fleet.RawTarget{{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "orders", Service: "orders"}}}),
	}
	if called {
		srcs = append(srcs, fleet.NewMemorySource("kernel", "observation", &fleet.Collection{Observed: []fleet.ObservedEdge{{From: billingName, To: "orders", Count: 3}}}))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, srcs...)
	expect(t, err == nil, "build: %v", err)
	return fleet.NewQuery(snap)
}

// printed runs print over a fresh command and returns its output.
func printed(t *testing.T, print func(*cobra.Command) error) string {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	expect(t, print(cmd) == nil, "print")
	return out.String()
}

func inferredDetail(t *testing.T, q *fleet.Query) *fleet.EntityDetail {
	t.Helper()
	d, err := q.EntityDetail(fleet.KindService, string(fleet.NewServiceKey(billingName)))
	expect(t, err == nil, "detail: %v", err)
	return d
}

func TestFleetEntitiesDeclarationFlag(t *testing.T) {
	cmd := newFleetEntitiesCommand(app.NewService(nil, nil), viper.New())
	expect(t, cmd.ParseFlags([]string{"--declaration", "inferred"}) == nil, "flags")
	f := entityFilterFromCmd(cmd, "")
	expect(t, f.Declaration == fleet.DeclarationInferred && len(f.Kinds) == 1 && f.Kinds[0] == fleet.KindService, "filter %+v", f)
	got, _ := cmd.GetFlagCompletionFunc("declaration")
	expect(t, got != nil, "--declaration completes")
	l, err := inferredQuery(t, false).Entities(f)
	expect(t, err == nil && l.Total == 2, "inferred entities %v %+v", err, l)
}

func TestFleetSearchDeclarationFlag(t *testing.T) {
	cmd := newFleetSearchCommand(app.NewService(nil, nil), viper.New())
	expect(t, cmd.ParseFlags([]string{"--declaration", "declared"}) == nil, "flags")
	f, err := searchFilterFromCmd(cmd, "")
	expect(t, err == nil && f.Declaration == fleet.DeclarationDeclared, "filter %+v %v", f, err)
	res, err := inferredQuery(t, false).Search(f)
	expect(t, err == nil && res.Total == 1 && res.Services[0].Name == "orders", "declared search %v %+v", err, res)
	res, err = inferredQuery(t, false).Search(fleet.SearchFilter{})
	expect(t, err == nil, "search: %v", err)
	out := printed(t, func(c *cobra.Command) error { return printFleetSearch(c, res, "text") })
	for line := range strings.Lines(out) {
		expect(t, strings.HasPrefix(line, "  k8s:") == strings.HasSuffix(line, " inferred\n"), "only inferred rows are marked: %q\n%s", line, out)
	}
}

func TestFleetEntitiesPrintsInferredRow(t *testing.T) {
	l, err := inferredQuery(t, false).Entities(fleet.EntityFilter{Declaration: fleet.DeclarationInferred, Kinds: []fleet.EntityKind{fleet.KindService}})
	expect(t, err == nil, "entities: %v", err)
	out := printed(t, func(c *cobra.Command) error { return printFleetEntities(c, l, "text") })
	for _, want := range []string{
		"  inferred    " + billingName,
		"              namespace shop  kind Deployment  image registry.example/billing:1.4.2\n",
		"              namespace shop  kind Deployment  image -\n",
	} {
		expect(t, strings.Contains(out, want), "entities lack %q:\n%s", want, out)
	}
}

func TestFleetEntityPrintsInferredWorkload(t *testing.T) {
	q := inferredQuery(t, true)
	out := printed(t, func(c *cobra.Command) error { return printFleetEntity(c, inferredDetail(t, q), "text") })
	for _, want := range []string{
		"Workload: Deployment shop/billing\n",
		"  image registry.example/billing:1.4.2 (from k8s-workloads: Deployment spec.template.spec.containers[name=billing].image)\n",
		"  port 8080 http TCP (from k8s-workloads: Deployment ",
		"  readiness probe /healthz port 8080 (from k8s-workloads: Deployment ",
		"Calls (1):\n  orders\n",
	} {
		expect(t, strings.Contains(out, want), "entity lacks %q:\n%s", want, out)
	}
	expect(t, !strings.Contains(out, "Placement") && !strings.Contains(out, "Changes"), "an inferred detail prints contract sections:\n%s", out)
	view, err := q.GetService(billingName)
	expect(t, err == nil, "get: %v", err)
	out = printed(t, func(c *cobra.Command) error { return printFleetService(c, view, "text") })
	expect(t, strings.Contains(out, "Workload: Deployment shop/billing\n  image registry.example/billing:1.4.2"), "get lacks the workload:\n%s", out)
}

func TestFleetEntityPrintsCallsUnknown(t *testing.T) {
	out := printed(t, func(c *cobra.Command) error {
		return printFleetEntity(c, inferredDetail(t, inferredQuery(t, false)), "text")
	})
	expect(t, strings.Contains(out, "Calls unknown: Pacto reads no traffic source here\n"), "unknown calls:\n%s", out)
	q := inferredQuery(t, true)
	pwn, err := q.EntityDetail(fleet.KindService, string(fleet.NewServiceKey("k8s:shop/Deployment/pwn")))
	expect(t, err == nil, "detail: %v", err)
	out = printed(t, func(c *cobra.Command) error { return printFleetEntity(c, pwn, "text") })
	expect(t, strings.Contains(out, "No calls to declared services seen as of "+pwn.Meta.AsOf.Format("2006-01-02T15:04:05Z07:00")+"\n"), "no calls:\n%s", out)
}

func TestFleetAttentionPrintsInferredCaller(t *testing.T) {
	res, err := inferredQuery(t, true).Attention(fleet.AttentionFilter{Category: "undeclared"})
	expect(t, err == nil, "attention: %v", err)
	out := printed(t, func(c *cobra.Command) error { return printFleetAttention(c, res, "text") })
	expect(t, strings.Contains(out, " (inferred service "+string(fleet.NewServiceKey(billingName))+")\n"), "attention lacks the caller:\n%s", out)
}

// TestInferredEnvNeverReachesCLIOrMCPOrTUI: no environment value, Secret,
// ConfigMap, header, exec, argument or annotation of the Decision 9 object,
// and no terminal escape, reaches the CLI's text or JSON or the fleet MCP
// tools. The TUI half is TestDetailRendersInferredWorkload.
func TestInferredEnvNeverReachesCLIOrMCPOrTUI(t *testing.T) {
	q := inferredQuery(t, true)
	view, err := q.GetService(billingName)
	expect(t, err == nil, "get: %v", err)
	res, err := q.Search(fleet.SearchFilter{})
	expect(t, err == nil, "search: %v", err)
	answers := map[string]string{}
	for _, format := range []string{"text", "json"} {
		answers["entity "+format] = printed(t, func(c *cobra.Command) error { return printFleetEntity(c, inferredDetail(t, q), format) })
		answers["get "+format] = printed(t, func(c *cobra.Command) error { return printFleetService(c, view, format) })
		answers["search "+format] = printed(t, func(c *cobra.Command) error { return printFleetSearch(c, res, format) })
	}
	server, err := pactomcp.New("v", pactomcp.Authoring{Remote: true}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{
		Src: func(context.Context) (*fleet.Query, error) { return q, nil }}})
	expect(t, err == nil, "server: %v", err)
	session := sessionOn(t, server)
	answers["pacto_fleet_get"], _ = callText(t, session, "pacto_fleet_get", map[string]any{"service": billingName})
	answers["pacto_fleet_search"], _ = callText(t, session, "pacto_fleet_search", map[string]any{})
	answers["pacto_fleet_entity"], _ = callText(t, session, "pacto_fleet_entity", map[string]any{"kind": "service", "key": string(fleet.NewServiceKey(billingName))})
	for name, a := range answers {
		expect(t, strings.Contains(a, "billing"), "%s does not name billing:\n%s", name, a)
		for _, leak := range inferredLeakStrings {
			expect(t, !strings.Contains(a, leak), "%s holds %q:\n%s", name, leak, a)
		}
	}
}
