package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// recordAs is the named fixture recorded for service: a CI record gets the
// id its kind, service and revisions give it; an operation keeps its UID.
func recordAs(t *testing.T, fixture, service string) runrecord.Record {
	t.Helper()
	r := fixtureRecord(t, fixture)
	r.Service = service
	if r.Kind != runrecord.KindOperation {
		r.ID = runrecord.ID(r.Kind, r.Service, r.OldRevision, r.NewRevision)
	}
	return r
}

// statsMCPCommand is a pacto mcp command with flags set.
func statsMCPCommand(t *testing.T, svc *app.Service, flags map[string]string) *cobra.Command {
	t.Helper()
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	cmd.SetErr(io.Discard)
	for k, v := range flags {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	return cmd
}

// statsMCPSession builds the --fleet server the flags select over an empty
// local fleet and connects a client to it.
func statsMCPSession(t *testing.T, flags map[string]string) *mcpsdk.ClientSession {
	t.Helper()
	flags["fleet"], flags["local"] = "true", t.TempDir()
	svc := app.NewService(nil, nil)
	server, err := buildMCPServer(statsMCPCommand(t, svc, flags), svc, "v", nil, defaultDeps())
	expect(t, err == nil, "server: %v", err)
	return sessionOn(t, server)
}

// statsAnswer calls pacto_stats and decodes its answer.
func statsAnswer(t *testing.T, s *mcpsdk.ClientSession, args map[string]any) stats.Stats {
	t.Helper()
	text, isErr := callText(t, s, "pacto_stats", args)
	expect(t, !isErr, "pacto_stats: %s", text)
	var st stats.Stats
	expect(t, json.Unmarshal([]byte(text), &st) == nil, "decode: %s", text)
	return st
}

func TestMCPStatsStdioUsesFlagThenEnv(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	st := statsAnswer(t, statsMCPSession(t, map[string]string{"stats-read-registry": path}), map[string]any{})
	expect(t, st.Registry == path && st.Runs == 3, "flag: %+v", st)
	t.Setenv("PACTO_STATS_REGISTRY", path)
	st = statsAnswer(t, statsMCPSession(t, map[string]string{}), map[string]any{})
	expect(t, st.Registry == path && st.Runs == 3, "env: %+v", st)
}

func TestMCPStatsStdioHonoursRegistryArgument(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	st := statsAnswer(t, statsMCPSession(t, map[string]string{"stats-read-registry": "127.0.0.1:1/nowhere"}), map[string]any{"registry": path})
	expect(t, st.Registry == path, "registry = %s", st.Registry)
}

func TestMCPStatsStdioRegisteredWithoutRegistry(t *testing.T) {
	statsRegistry(t)
	s := statsMCPSession(t, map[string]string{})
	text, isErr := callText(t, s, "pacto_stats", map[string]any{})
	expect(t, isErr && text == errNoStatsRegistryMCP.Error(), "answer = %s", text)
	expect(t, strings.Contains(s.InitializeResult().Instructions, "pacto_stats totals"), "no stats instructions")
	// A bad argument is the tool's error, not a smaller answer.
	t.Setenv("PACTO_STATS_REGISTRY", "r.example/s")
	text, isErr = callText(t, s, "pacto_stats", map[string]any{"kinds": "coverage"})
	expect(t, isErr && strings.Contains(text, `"coverage"`), "answer = %s", text)
}

func TestStatsToolRefusesRegistryOverHTTP(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	s := statsMCPSession(t, map[string]string{"transport": "http", "stats-read-registry": path})
	text, isErr := callText(t, s, "pacto_stats", map[string]any{"registry": "attacker.example/stats"})
	expect(t, isErr && text == errStatsRegistryOverHTTP.Error(), "answer = %s", text)
	expect(t, statsAnswer(t, s, map[string]any{}).Registry == path, "the started registry answers")
}

func TestMCPStatsNotRegisteredWithoutRegistryOverHTTP(t *testing.T) {
	statsRegistry(t)
	expect(t, !builtTools(t, nil, map[string]string{"transport": "http", "fleet": "true", "local": t.TempDir()})["pacto_stats"], "pacto_stats over HTTP without the flag")
}

func TestMCPStatsHTTPIgnoresEnvRegistry(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	t.Setenv("PACTO_STATS_REGISTRY", path)
	s := statsMCPSession(t, map[string]string{"transport": "http"})
	expect(t, !toolNamesOf(t, s)["pacto_stats"], "pacto_stats registered from the environment over HTTP")
	expect(t, !strings.Contains(s.InitializeResult().Instructions, "pacto_stats"), "stats instructions over HTTP without the flag")
}

func toolNamesOf(t *testing.T, s *mcpsdk.ClientSession) map[string]bool {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	expect(t, err == nil, "tools: %v", err)
	out := map[string]bool{}
	for _, tl := range res.Tools {
		out[tl.Name] = true
	}
	return out
}

func TestMCPStatsHTTPNoAuthIsUnrestricted(t *testing.T) {
	path := statsRegistry(t, allFixtures...)
	st := statsAnswer(t, statsMCPSession(t, map[string]string{"transport": "http", "stats-read-registry": path}), map[string]any{"by": "service"})
	expect(t, !st.Hidden.Present && st.Runs == 3 && st.Totals != nil && len(st.Totals.Environments) == 1, "answer = %+v", st)
	expect(t, len(st.Groups) == 1 && st.Groups[0].Label == "payments", "an unplaced service is counted for the local user: %+v", st.Groups)
}

// signedInStats is an HTTP server whose callers sign in: view narrows the
// fleet of orders (prod) and vault (secret), as mcpFleetView would for the
// reviewed caller, and pacto_stats reads repo.
func signedInStats(t *testing.T, repo string, view pactomcp.FleetView) *mcpsdk.ClientSession {
	t.Helper()
	svc := app.NewService(nil, nil)
	cmd := statsMCPCommand(t, svc, map[string]string{"transport": "http", "stats-read-registry": repo})
	server, err := pactomcp.New("v", pactomcp.Authoring{Remote: true}, pactomcp.Modes{Transport: "http", Fleet: &pactomcp.FleetMode{
		Src: impactViewFleet(t), Opts: pactomcp.FleetOptions{View: view, Stats: mcpStatsProvider(cmd, svc, false)},
	}})
	expect(t, err == nil, "server: %v", err)
	return sessionOn(t, server)
}

// aliceFleetView is aliceView as the MCP fleet view: prod and staging.
func aliceFleetView(ctx context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
	return aliceView()(ctx, q)
}

func TestMCPStatsSignedInIsRestricted(t *testing.T) {
	path := statsRegistryOf(t, true, recordAs(t, "impact-blocked.json", "orders"), recordAs(t, "impact-blocked.json", "vault"),
		recordAs(t, "publish-refused.json", "vault"))
	s := signedInStats(t, path, aliceFleetView)
	text, isErr := callText(t, s, "pacto_stats", map[string]any{"by": "service"})
	expect(t, !isErr, "pacto_stats: %s", text)
	var st stats.Stats
	expect(t, json.Unmarshal([]byte(text), &st) == nil, "decode: %s", text)
	expect(t, st.Hidden.Present && st.Completeness == fleet.CompletenessPartial && st.RecordsRead == 1 && st.Runs == 1, "answer = %s", text)
	expect(t, len(st.Groups) == 1 && st.Groups[0].Label == "orders", "groups = %+v", st.Groups)
	expect(t, !strings.Contains(text, "vault") && !strings.Contains(text, runrecord.AnchorTag("vault")), "the hidden service is named: %s", text)
	expect(t, strings.Contains(text, stats.LimitationOutsideView), "no outside-view limitation: %s", text)
}

func TestRestrictedServiceFilterHiddenIsNotFound(t *testing.T) {
	var requests atomic.Int32
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(reg.Close)
	t.Setenv("PACTO_INSECURE_REGISTRIES", strings.TrimPrefix(reg.URL, "http://"))
	s := signedInStats(t, strings.TrimPrefix(reg.URL, "http://")+"/acme/pacto-stats", aliceFleetView)
	hidden, isErr := callText(t, s, "pacto_stats", map[string]any{"service": "vault"})
	absent, _ := callText(t, s, "pacto_fleet_get", map[string]any{"service": "vault"})
	expect(t, isErr && hidden == absent, "pacto_stats = %q, pacto_fleet_get = %q", hidden, absent)
	expect(t, requests.Load() == 0, "the registry was read %d times", requests.Load())
}

func TestMCPStatsHTTPWithoutExchangeHasNoDelivery(t *testing.T) {
	path := statsRegistryOf(t, true, recordAs(t, "impact-blocked.json", "orders"), recordAs(t, "operation.golden.json", "orders"))
	everything := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) { return q, nil }
	text, isErr := callText(t, signedInStats(t, path, everything), "pacto_stats", map[string]any{})
	expect(t, !isErr, "pacto_stats: %s", text)
	var st stats.Stats
	expect(t, json.Unmarshal([]byte(text), &st) == nil, "decode: %s", text)
	expect(t, (st.Totals == nil || len(st.Totals.Environments) == 0) && st.Completeness == fleet.CompletenessPartial && st.Runs == 1, "answer = %s", text)
	expect(t, strings.Contains(text, stats.LimitationDeliveryUnavailable) && strings.Contains(text, mcpNoDelivery), "no delivery limitation: %s", text)
}

// TestStatsCLIAndMCPAgree: one registry, one fleet fixture and one clock
// give the same answer from pacto stats and pacto_stats; only the snapshot
// each side built differs.
func TestStatsCLIAndMCPAgree(t *testing.T) {
	path := statsRegistryOf(t, true, recordAs(t, "impact-blocked.json", "app"), recordAs(t, "publish-refused.json", "app"),
		recordAs(t, "impact-blocked.json", "lonely"), fixtureRecord(t, "operation.golden.json"))
	flags := parityFixture(t)
	cli, _, err := runRoot(t, defaultDeps(), nil, append([]string{"stats", "--registry", path, "--by", "owner", "--since", "30d", "--output-format", "json"}, flags...)...)
	expect(t, err == nil, "pacto stats: %v", err)

	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	cmd.SetErr(io.Discard)
	expect(t, cmd.ParseFlags(append([]string{"--fleet", "--stats-read-registry", path}, flags...)) == nil, "flags")
	server, err := buildMCPServer(cmd, svc, "v", nil, defaultDeps())
	expect(t, err == nil, "server: %v", err)
	mcp, isErr := callText(t, sessionOn(t, server), "pacto_stats", map[string]any{"by": "owner", "since": "30d"})
	expect(t, !isErr, "pacto_stats: %s", mcp)

	decode := func(body string) map[string]any {
		var v map[string]any
		expect(t, json.Unmarshal([]byte(body), &v) == nil, "decode: %s", body)
		a, ok := v["attribution"].(map[string]any)
		expect(t, ok, "no attribution: %s", body)
		a["asOf"], a["snapshotId"] = "", ""
		return v
	}
	c, m := decode(cli), decode(mcp)
	expect(t, reflect.DeepEqual(c, m), "pacto stats and pacto_stats differ:\ncli: %s\nmcp: %s", cli, mcp)
	expect(t, len(c["groups"].([]any)) > 1, "the fixture groups by more than one owner: %s", cli)
}
