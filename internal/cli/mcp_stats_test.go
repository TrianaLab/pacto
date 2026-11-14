package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

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

// signedInRemoteStats is a remote fleet server whose caller, alice, signs in
// and reads the fleet of orders, deployed in prod-a and prod-b, and the
// operation records of both namespaces. Her own SelfSubjectAccessReviews
// allow listing operations in prod-a only; reviews records which token each
// review came from.
func signedInRemoteStats(t *testing.T) (h http.Handler, reviews map[string][]string) {
	t.Helper()
	opA, opB := recordAs(t, "operation.golden.json", "orders"), recordAs(t, "operation.golden.json", "orders")
	opA.ID, opA.Environment, opA.Stage = "0f6c2a1e-5b7d-4c3a-9e2f-00000000000a", "prod-a", &runrecord.StageRef{Namespace: "prod-a", Name: "prod-a", Cluster: opA.Stage.Cluster}
	opB.ID, opB.Environment, opB.Stage = "0f6c2a1e-5b7d-4c3a-9e2f-00000000000b", "prod-b", &runrecord.StageRef{Namespace: "prod-b", Name: "prod-b", Cluster: opB.Stage.Cluster}
	path := statsRegistryOf(t, true, opA, opB)
	var mu sync.Mutex
	reviews = map[string][]string{}
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
		dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
		dyn.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{"username": "alice"}}}}, nil
		})
		dyn.PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": true}}}, nil
		})
		dyn.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			ns, _, _ := unstructured.NestedString(a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object, "spec", "resourceAttributes", "namespace")
			mu.Lock()
			reviews[c.BearerToken] = append(reviews[c.BearerToken], ns)
			mu.Unlock()
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": c.BearerToken == "for-the-cluster" && ns == "prod-a"}}}, nil
		})
		return dyn, nil
	}
	root := t.TempDir()
	writeCLIBundle(t, filepath.Join(root, "orders"), "orders", "1.0.0")
	targets := filepath.Join(t.TempDir(), "targets.yaml")
	mustWriteFile(t, targets, "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n"+
		"  - {scope: prod-a, kind: kubernetes, name: orders, service: orders}\n"+
		"  - {scope: prod-b, kind: kubernetes, name: orders, service: orders}\n")
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background())
	expect(t, cmd.ParseFlags([]string{"--fleet", "--local", root, "--target-state", targets, "-t", "http", "--auth-issuer", "https://idp",
		"--stats-read-registry", path}) == nil, "flags")
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	expect(t, remoteCallerFor(cmd, svc, "v", nil, d, &s) == nil, "remote fleet")
	return mcpHandler(s), reviews
}

// remoteStatsText calls pacto_stats over h as the signed-in caller and
// returns the tool's text.
func remoteStatsText(t *testing.T, h http.Handler) string {
	t.Helper()
	body := post(t, h, "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_stats","arguments":{}}}`).Body.String()
	_, data, _ := strings.Cut(body, "data: ") // one server-sent event
	var res struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	expect(t, json.Unmarshal([]byte(data), &res) == nil && len(res.Result.Content) == 1 && !res.Result.IsError, "pacto_stats: %s", body)
	return res.Result.Content[0].Text
}

func TestMCPStatsSignedInOperationAccess(t *testing.T) {
	h, _ := signedInRemoteStats(t)
	text := remoteStatsText(t, h)
	var st stats.Stats
	expect(t, json.Unmarshal([]byte(text), &st) == nil, "decode: %s", text)
	expect(t, st.Totals != nil && len(st.Totals.Environments) == 1 && st.Totals.Environments[0].Environment == "prod-a" &&
		st.Totals.Environments[0].Operations == 1, "only prod-a operations count: %s", text)
	expect(t, st.Hidden.Present && st.Completeness == fleet.CompletenessPartial, "the refused namespace is outside the view: %s", text)
	expect(t, !strings.Contains(text, "prod-b") && !strings.Contains(text, stats.LimitationDeliveryUnavailable), "prod-b is named: %s", text)
}

func TestMCPStatsSignedInNeverUsesServerCredentials(t *testing.T) {
	h, reviews := signedInRemoteStats(t)
	remoteStatsText(t, h)
	expect(t, len(reviews["process-token"]) == 0, "the server's own client was reviewed: %v", reviews["process-token"])
	expect(t, len(reviews["for-the-cluster"]) == 2, "the caller's own client reviews each snapshot namespace once: %v", reviews)
}
