package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// fakeMCPAuth accepts the bearer "good" and exchanges it unless told not to.
type fakeMCPAuth struct {
	exchange    bool
	exchangeErr error
	exchanged   []string
}

func (f *fakeMCPAuth) Verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if token != "good" {
		return nil, auth.ErrInvalidToken
	}
	// The verified token as authn.MCP keeps it: an ID token naming the agent.
	jwt := "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp","sub":"agent","email":"agent@example.com"}`)) + ".s"
	return &auth.TokenInfo{UserID: "https://idp#agent", Expiration: time.Now().Add(time.Hour), Extra: map[string]any{"token": jwt}}, nil
}

func (f *fakeMCPAuth) CanExchange() bool { return f.exchange }

func (f *fakeMCPAuth) Exchange(_ context.Context, info *auth.TokenInfo) (string, error) {
	f.exchanged = append(f.exchanged, info.UserID)
	return "for-the-cluster", f.exchangeErr
}

func remoteCmd(t *testing.T, d deps, args ...string) error {
	t.Helper()
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return cmd.ExecuteContext(ctx)
}

// wantErr fails unless err says want.
func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	expect(t, err != nil && strings.Contains(err.Error(), want), "want an error saying %q, got %v", want, err)
}

func TestMCPRemoteFlags(t *testing.T) {
	fa := &fakeMCPAuth{exchange: true}
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	var got authn.MCPConfig
	d.newMCPAuth = func(c authn.MCPConfig) (mcpAuth, error) { got = c; return fa, nil }
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	t.Setenv("PACTO_MCP_EXCHANGE_CLIENT_SECRET", "s3cret")
	err := remoteCmd(t, d, "-t", "http", "--host", "0.0.0.0", "--port", "0")
	expect(t, errors.Is(err, errAuthBeyondLoopback), "beyond loopback without auth: %v", err)
	for _, f := range [][]string{{"--auth", "bearerAuth=t"}, {"--allow-writes"}} {
		err = remoteCmd(t, d, append([]string{"-t", "http", "--host", "0.0.0.0", "--port", "0", "--auth-issuer", "https://idp",
			"--resource", "https://mcp.example/mcp"}, f...)...)
		expect(t, errors.Is(err, errServiceCredsBeyondLoopback), "%v beyond loopback, even with auth: %v", f, err)
	}
	wantErr(t, remoteCmd(t, d, "-t", "http", "--auth-issuer", "https://idp"), "--resource")
	err = remoteCmd(t, d, "--delivery", "-t", "http", "--host", "0.0.0.0", "--port", "0", "--auth-issuer", "https://idp",
		"--resource", "https://mcp.example/mcp", "--exchange-client-id", "pacto-mcp")
	expect(t, err == nil, "remote delivery starts with exchange configured: %v", err)
	want := authn.MCPConfig{IssuerURL: "https://idp", Audience: "https://mcp.example/mcp", ClientID: "pacto-mcp", ClientSecret: "s3cret", ExchangeAudience: "pacto-cli"}
	expect(t, got == want, "the auth configuration %+v", got)
	_ = remoteCmd(t, d, "-t", "http", "--port", "0", "--auth-issuer", "https://idp", "--resource", "https://mcp.example/mcp", "--audience", "pacto-mcp")
	expect(t, got.Audience == "pacto-mcp", "--audience overrides the resource: %+v", got)
	c, err := newDynamicForConfig(&rest.Config{Host: "https://k"})
	expect(t, err == nil && c != nil, "newDynamicForConfig: %v", err)
	_, err = newMCPAuth(authn.MCPConfig{})
	expect(t, err != nil, "newMCPAuth validates")
}

func TestMCPRemoteRefusals(t *testing.T) {
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return nil, errors.New("issuer down") }
	wantErr(t, remoteCmd(t, d, "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp"), "issuer down")
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return &fakeMCPAuth{}, nil }
	err := remoteCmd(t, d, "--delivery", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp")
	expect(t, errors.Is(err, errDeliveryNoExchange), "delivery without exchange: %v", err)
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return &fakeMCPAuth{exchange: true}, nil }
	d.clusterConfig = func() (*rest.Config, error) { return nil, errors.New("no cluster") }
	wantErr(t, remoteCmd(t, d, "--delivery", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp", "--exchange-client-id", "c"), "no cluster")
	wantErr(t, remoteCmd(t, d, "--delivery", "--root", "x", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp"), "cannot be combined")
}

// post sends one JSON-RPC request through h and returns the answer.
func post(t *testing.T, h http.Handler, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postLogged(t, h, bearer, body, &strings.Builder{})
}

// postLogged is post with the request's log written to logs.
func postLogged(t *testing.T, h http.Handler, bearer, body string, logs *strings.Builder) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://mcp.example/mcp", strings.NewReader(body))
	r = r.WithContext(logging.WithLogger(r.Context(), logging.New(logs, true)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

// remoteWorld is a remote delivery server over a fake cluster: who calls is
// read per request from user, and the cluster can be told to fail.
type remoteWorld struct {
	fa          *fakeMCPAuth
	user        string
	cfgs        []*rest.Config
	clientErr   error
	whoAmIFails bool
	h           http.Handler
}

// newRemoteWorld takes the server's auth from a, else w.fa.
func newRemoteWorld(t *testing.T, a ...mcpAuth) *remoteWorld {
	t.Helper()
	w := &remoteWorld{fa: &fakeMCPAuth{exchange: true}, user: "system:serviceaccount:agents:bot"}
	d := deliveryDeps(w.user, nil)
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
	d.resolveK8s = ambientEntries(&d)
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
		w.cfgs = append(w.cfgs, c)
		if w.clientErr != nil {
			return nil, w.clientErr
		}
		dyn, _ := deliveryDeps(w.user, nil).newDynamic()
		if w.whoAmIFails {
			dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("token rejected")
			})
		}
		return dyn, nil
	}
	s := mcpServe{auth: authOr(w.fa, a), issuer: "https://idp", resource: "https://mcp.example/mcp"}
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	_ = cmd.Flags().Set("oidc-username-claim", "email")
	_ = cmd.Flags().Set("delivery", "true")
	err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s)
	expect(t, err == nil, "remote delivery: %v", err)
	w.h = mcpHandler(s)
	return w
}

func TestRemoteMCPNeedsABearer(t *testing.T) {
	w := newRemoteWorld(t)
	for _, tok := range []string{"", "bad"} {
		rec := post(t, w.h, tok, listTools)
		expect(t, rec.Code == http.StatusUnauthorized, "token %q: %d", tok, rec.Code)
		expect(t, strings.Contains(rec.Header().Get("WWW-Authenticate"), `resource_metadata="https://mcp.example/.well-known/oauth-protected-resource/mcp"`),
			"the 401 names the metadata: %v", rec.Header())
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		rec := httptest.NewRecorder()
		w.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		expect(t, rec.Code == http.StatusOK, "metadata %s: %d", path, rec.Code)
		expect(t, strings.Contains(rec.Body.String(), `"resource":"https://mcp.example/mcp"`), "metadata %s", rec.Body)
		expect(t, strings.Contains(rec.Body.String(), `"authorization_servers":["https://idp"]`), "metadata %s", rec.Body)
	}
}

func TestRemoteMCPToolsPerCaller(t *testing.T) {
	w := newRemoteWorld(t)
	// An agent: exchanged, the cluster asked with the exchanged token only,
	// propose and cancel listed, no approve tool.
	rec := post(t, w.h, "good", listTools)
	body := rec.Body.String()
	expect(t, rec.Code == http.StatusOK && strings.Contains(body, "pacto_delivery_operation_propose"), "agent tools: %d %s", rec.Code, body)
	expect(t, !strings.Contains(body, "pacto_delivery_approve"), "no approve tool: %s", body)
	last := w.cfgs[len(w.cfgs)-1]
	expect(t, last.BearerToken == "for-the-cluster" && last.Host == "https://k", "the cluster must see the exchanged token, never the process's: %+v", last)
	expect(t, len(w.fa.exchanged) > 0, "the token was exchanged")
	// A person, on the next request: read tools only.
	w.user = "https://idp#carol"
	body = post(t, w.h, "good", listTools).Body.String()
	expect(t, !strings.Contains(body, "pacto_delivery_operation_propose") && strings.Contains(body, "pacto_delivery_get"), "a person's tools are read-only: %s", body)
}

func TestRemoteMCPFailuresSayWhy(t *testing.T) {
	w := newRemoteWorld(t)
	w.fa.exchangeErr = errors.New("the IdP refused the exchange")
	logs := &strings.Builder{}
	rec := postLogged(t, w.h, "good", listTools, logs)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "refused the exchange"), "exchange refused: %d %s", rec.Code, rec.Body)
	w.fa.exchangeErr, w.clientErr = nil, errors.New("bad config")
	rec = postLogged(t, w.h, "good", listTools, logs)
	expect(t, rec.Code == http.StatusInternalServerError, "client error: %d", rec.Code)
	w.clientErr, w.whoAmIFails = nil, true
	rec = postLogged(t, w.h, "good", listTools, logs)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "token rejected"), "whoami refused: %d %s", rec.Code, rec.Body)
	// A review that names nobody gets no tools either.
	w.whoAmIFails, w.user = false, ""
	rec = post(t, w.h, "good", listTools)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), errNobodyReviewed.Error()), "nobody: %d %s", rec.Code, rec.Body)
	w.whoAmIFails, w.user = true, "system:serviceaccount:agents:bot"
	// Every refusal is logged with the caller and the reason, never the token.
	for _, want := range []string{"could not exchange a caller's token", "could not build a caller's client", "did not say who a caller is",
		"claimed=agent@example.com", "refused the exchange", "bad config", "token rejected", "path=/mcp"} {
		expect(t, strings.Contains(logs.String(), want), "the log lacks %q:\n%s", want, logs)
	}
	expect(t, !strings.Contains(logs.String(), "good"), "the log carries the token:\n%s", logs)
	// A served request logs who it is for and whether it got the write tools.
	w.whoAmIFails = false
	logs.Reset()
	postLogged(t, w.h, "good", listTools, logs)
	expect(t, strings.Contains(logs.String(), "remote MCP request") && strings.Contains(logs.String(), "user=system:serviceaccount:agents:bot") &&
		!strings.Contains(logs.String(), "agent@example.com") && strings.Contains(logs.String(), "class=agent") &&
		strings.Contains(logs.String(), "writeTools=true"), "the grant is not logged:\n%s", logs)
}

func TestRemoteDeliveryRefusesABadTrustConfig(t *testing.T) {
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	_ = cmd.Flags().Set("delivery-trust", filepath.Join(t.TempDir(), "absent"))
	_ = cmd.Flags().Set("delivery", "true")
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}}
	if err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s); err == nil || !strings.Contains(err.Error(), "trust config") {
		t.Fatalf("a missing trust config must fail: %v", err)
	}
}

// TestAuditRemoteDeliveryCallerIsReviewedUser: the audit line names the user
// the apiserver reviewed, never the token's opaque subject.
func TestAuditRemoteDeliveryCallerIsReviewedUser(t *testing.T) {
	w := newRemoteWorld(t)
	w.user = "dave"
	logs := &strings.Builder{}
	rec := postLogged(t, w.h, "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_delivery_status","arguments":{"service":"payments"}}}`, logs)
	line := ""
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, `msg="mcp tool call"`) {
			line = l
		}
	}
	expect(t, strings.Contains(line, "caller=dave") && strings.Contains(line, "callerSource=reviewed") &&
		!strings.Contains(line, "idp#agent"), "audit line: %q\n%s\n%s", line, logs, rec.Body)
}

// remoteFleet is a remote fleet server over a fake cluster: the apiserver
// reviews each exchanged token as reviewed (per request), and RBAC lets only
// readers read the whole cluster. asked records whom RBAC was asked about.
type remoteFleet struct {
	fa         *fakeMCPAuth
	reviewed   authn.Person
	reviewErr  error
	readers    map[string]bool
	asked      []authn.Person
	h          http.Handler
	perRequest bool
	// tokens is the bearer of every client built, in order.
	tokens []string
}

// newRemoteFleet takes the server's auth from a, else w.fa.
func newRemoteFleet(t *testing.T, a ...mcpAuth) *remoteFleet {
	t.Helper()
	return newRemoteFleetFlags(t, nil, a...)
}

// newRemoteFleetFlags is newRemoteFleet with more pacto mcp flags set.
func newRemoteFleetFlags(t *testing.T, flags map[string]string, a ...mcpAuth) *remoteFleet {
	t.Helper()
	w := &remoteFleet{fa: &fakeMCPAuth{exchange: true}, reviewed: authn.Person{User: "alice", Groups: []string{"team-a"}}, readers: map[string]bool{"alice": true}}
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
	d.resolveK8s = ambientEntries(&d)
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
		w.tokens = append(w.tokens, c.BearerToken)
		dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
		dyn.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			if c.BearerToken != "for-the-cluster" {
				return true, nil, errors.New("only the exchanged token is reviewed")
			}
			if w.reviewErr != nil {
				return true, nil, w.reviewErr
			}
			groups := []any{}
			for _, g := range w.reviewed.Groups {
				groups = append(groups, g)
			}
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{
				"username": w.reviewed.User, "groups": groups}}}}, nil
		})
		dyn.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
			user, _, _ := unstructured.NestedString(o.Object, "spec", "user")
			groups, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "groups")
			w.asked = append(w.asked, authn.Person{User: user, Groups: groups})
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": w.readers[user]}}}, nil
		})
		return dyn, nil
	}
	root := t.TempDir()
	writeCLIBundle(t, filepath.Join(root, "orders"), "orders", "1.0.0")
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	for k, v := range map[string]string{"fleet": "true", "local": root, "transport": "http", "auth-issuer": "https://idp", "oidc-username-claim": "email"} {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	for k, v := range flags {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	s := mcpServe{auth: authOr(w.fa, a), issuer: "https://idp", resource: "https://mcp.example/mcp"}
	expect(t, remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s) == nil, "remote fleet")
	w.perRequest = s.perRequest != nil
	w.h = mcpHandler(s)
	return w
}

const listEntities = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_fleet_entities","arguments":{}}}`

func TestRemoteFleetNamesCallerByReview(t *testing.T) {
	w := newRemoteFleet(t)
	rec := post(t, w.h, "good", listEntities)
	expect(t, rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "orders"), "alice reads the fleet: %d %s", rec.Code, rec.Body)
	expect(t, len(w.asked) > 0, "RBAC was asked")
	for _, p := range w.asked {
		// The token's claims say agent@example.com; only the review counts.
		expect(t, p.User == "alice" && len(p.Groups) == 1 && p.Groups[0] == "team-a", "RBAC was asked about %+v, not the reviewed alice/team-a", p)
	}
	expect(t, len(w.fa.exchanged) > 0, "the token was exchanged")
}

func TestRemoteFleetNeedsExchange(t *testing.T) {
	d := defaultDeps()
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return &fakeMCPAuth{}, nil }
	err := remoteCmd(t, d, "--fleet", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp")
	expect(t, errors.Is(err, errFleetNoExchange), "remote fleet without exchange: %v", err)
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return &fakeMCPAuth{exchange: true}, nil }
	d.clusterConfig = func() (*rest.Config, error) { return nil, errors.New("no cluster") }
	wantErr(t, remoteCmd(t, d, "--fleet", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp", "--exchange-client-id", "c"), "no cluster")
	// The first snapshot still fails startup.
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	// (remoteCmd runs on a cancelled context, so the build of the local
	// source, which HTTP reads only when named, fails).
	wantErr(t, remoteCmd(t, d, "--fleet", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp", "--exchange-client-id", "c", "--local", "."),
		"context canceled")
}

func TestRemoteFleetReviewFailureRefuses(t *testing.T) {
	w := newRemoteFleet(t)
	logs := &strings.Builder{}
	w.reviewErr = errors.New("token rejected")
	rec := postLogged(t, w.h, "good", listEntities, logs)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "token rejected") && !strings.Contains(rec.Body.String(), "orders"),
		"a failed review refuses: %d %s", rec.Code, rec.Body)
	expect(t, strings.Contains(logs.String(), "did not say who a caller is") && strings.Contains(logs.String(), "claimed=agent@example.com"), "refusal log:\n%s", logs)
	// A review that names nobody is no anonymous view either.
	w.reviewErr, w.reviewed = nil, authn.Person{}
	rec = post(t, w.h, "good", listEntities)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), errNobodyReviewed.Error()), "nobody reviewed: %d %s", rec.Code, rec.Body)
	expect(t, len(w.asked) == 0, "RBAC is never asked about an unreviewed caller: %+v", w.asked)
}

func TestRemoteFleetIsPerRequest(t *testing.T) {
	w := newRemoteFleet(t)
	expect(t, w.perRequest, "remote fleet serves per request")
	alice := post(t, w.h, "good", listEntities).Body.String()
	w.reviewed = authn.Person{User: "bob", Groups: []string{"team-b"}}
	bob := post(t, w.h, "good", listEntities).Body.String()
	expect(t, strings.Contains(alice, `orders`) && !strings.Contains(bob, `"orders"`), "each caller's own view:\nalice %s\nbob %s", alice, bob)
}

func TestMCPModeCheckedOnce(t *testing.T) {
	err := remoteCmd(t, defaultDeps(), "--delivery", "--publish", "-t", "http")
	wantErr(t, err, "cannot be combined")
	src, rerr := os.ReadFile("mcp.go")
	expect(t, rerr == nil && strings.Count(string(src), "checkMCPMode(") == 2, "checkMCPMode is defined once and called once")
}

func TestRemoteCallerLogsUser(t *testing.T) {
	w := newRemoteFleet(t)
	logs := &strings.Builder{}
	postLogged(t, w.h, "good", listEntities, logs)
	var line string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, `msg="remote MCP request"`) {
			line = l
		}
	}
	expect(t, strings.Contains(line, "user=alice") && strings.Contains(line, "writeTools=false") && !strings.Contains(line, "class=") &&
		!strings.Contains(line, "agent@example.com"), "remote request line: %q", line)
	var audit string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, `msg="mcp tool call"`) {
			audit = l
		}
	}
	expect(t, strings.Contains(audit, "caller=alice") && strings.Contains(audit, "callerSource=reviewed"), "audit line: %q", audit)
}

func TestRemoteDeliveryPassesAssertedOnBehalfOf(t *testing.T) {
	w := newRemoteWorld(t)
	propose := func(args string) string {
		return post(t, w.h, "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_delivery_operation_propose","arguments":{`+args+`}}}`).Body.String()
	}
	// The actor is the reviewed agent; on_behalf_of is the argument, as asserted.
	body := propose(`"namespace":"prod","stage":"payments-prod","to":"` + cliB + `","on_behalf_of":"erin"`)
	expect(t, strings.Contains(body, `\"onBehalfOf\": {\n        \"user\": \"erin\"`) &&
		strings.Contains(body, `\"user\": \"system:serviceaccount:agents:bot\"`), "asserted on_behalf_of: %s", body)
	// Without the argument nothing fills it in: not the token, not the review,
	// so the agent's propose is refused for naming nobody.
	body = propose(`"namespace":"prod","stage":"payments-prod","to":"` + cliB + `"`)
	expect(t, strings.Contains(body, "an agent must name the person it acts for") && !strings.Contains(body, `\"onBehalfOf\": {`) &&
		!strings.Contains(body, "agent@example.com"), "no derived on_behalf_of: %s", body)
}

// loopbackServe is a loopback HTTP server without an issuer, as mcpRemote
// reads it.
func loopbackServe(t *testing.T, args ...string) mcpServe {
	t.Helper()
	cmd := newMCPCommand(app.NewService(nil, nil), "v", deps{})
	_ = cmd.Flags().Set("transport", "http")
	for i := 0; i < len(args); i += 2 {
		_ = cmd.Flags().Set(args[i], args[i+1])
	}
	s := mcpServe{transport: "http", server: mustNewMCP("v", pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, pactomcp.Modes{})}
	expect(t, mcpRemote(cmd, deps{newMCPAuth: func(authn.MCPConfig) (mcpAuth, error) { return &fakeMCPAuth{}, nil }}, &s) == nil, "mcpRemote")
	return s
}

func TestLoopbackHTTPNeedsLaunchBearer(t *testing.T) {
	s := loopbackServe(t)
	expect(t, strings.HasPrefix(s.launch, "pmcp_"), "launch token %q", s.launch)
	h := mcpHandler(s)
	for _, tok := range []string{"", "pmcp_wrong"} {
		rec := post(t, h, tok, listTools)
		expect(t, rec.Code == http.StatusUnauthorized, "token %q: %d", tok, rec.Code)
	}
	// The bearer passes; the server then answers (a stateful session wants
	// initialize first, which is the server's own reply).
	rec := post(t, h, s.launch, listTools)
	expect(t, rec.Code == http.StatusOK, "launch token: %d %s", rec.Code, rec.Body)
}

func TestLoopbackHTTPPrintsBearer(t *testing.T) {
	s := loopbackServe(t)
	s.port = 0
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = runMCPServer(ctx, s, &stderr)
	expect(t, strings.Contains(stderr.String(), "/mcp   bearer: "+s.launch+" (this launch only)"), "startup line: %s", stderr.String())
}

func TestIssuerServerHasNoLaunchToken(t *testing.T) {
	s := loopbackServe(t, "auth-issuer", "https://idp", "resource", "https://mcp.example/mcp")
	expect(t, s.launch == "" && s.launchVerify == nil && s.auth != nil, "an issuer server verifies issuer tokens only")
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = runMCPServer(ctx, s, &stderr)
	expect(t, !strings.Contains(stderr.String(), "bearer:"), "no bearer printed: %s", stderr.String())
}

// TestRemoteComposedPerCaller: one remote server with --fleet and --delivery
// answers each request for the caller the apiserver reviewed: the fleet
// narrowed to what they may read, delivery tools for their class.
func TestRemoteComposedPerCaller(t *testing.T) {
	user := "system:serviceaccount:agents:bot"
	var asked []string
	d := deliveryDeps(user, nil)
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
	d.resolveK8s = ambientEntries(&d)
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) {
		dyn, _ := deliveryDeps(user, nil).newDynamic()
		dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
			who, _, _ := unstructured.NestedString(o.Object, "spec", "user")
			asked = append(asked, who)
			return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": who == "system:serviceaccount:agents:bot"}}}, nil
		})
		return dyn, nil
	}
	root := t.TempDir()
	writeCLIBundle(t, filepath.Join(root, "orders"), "orders", "1.0.0")
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	for k, v := range map[string]string{"fleet": "true", "delivery": "true", "local": root, "transport": "http", "auth-issuer": "https://idp"} {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	expect(t, remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s) == nil, "remote composed")
	h := mcpHandler(s)

	tools := post(t, h, "good", listTools).Body.String()
	entities := post(t, h, "good", listEntities).Body.String()
	expect(t, strings.Contains(tools, "pacto_delivery_operation_propose") && strings.Contains(tools, "pacto_fleet_entities") &&
		strings.Contains(tools, "pacto_capabilities"), "the agent's composed tools: %s", tools)
	expect(t, strings.Contains(entities, "orders") && slices.Contains(asked, user), "the agent reads the fleet as itself: %s %v", entities, asked)
	caps := post(t, h, "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_capabilities","arguments":{}}}`).Body.String()
	expect(t, strings.Contains(caps, `\"callerSource\": \"reviewed\"`) && strings.Contains(caps, `\"user\": \"`+user+`\"`) &&
		strings.Contains(caps, `\"class\": \"agent\"`) && strings.Contains(caps, `\"transport\": \"http\"`), "capabilities: %s", caps)

	// A person on the next request: read-only delivery and their own view.
	user = "https://idp#carol"
	tools = post(t, h, "good", listTools).Body.String()
	entities = post(t, h, "good", listEntities).Body.String()
	expect(t, !strings.Contains(tools, "pacto_delivery_operation_propose") && strings.Contains(tools, "pacto_delivery_get"), "a person's tools: %s", tools)
	expect(t, !strings.Contains(entities, `\"orders\"`) && slices.Contains(asked, user), "carol's own view: %s %v", entities, asked)
}

// TestRemoteBundleNoticesOnceAtStartup: a remote server reports what its
// bundle skipped once, at startup, never again on each request, with or
// without per-caller delivery.
func TestRemoteBundleNoticesOnceAtStartup(t *testing.T) {
	for _, delivery := range []string{"false", "true"} {
		user := "https://idp#carol"
		d := deliveryDeps(user, nil)
		d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
		d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return deliveryDeps(user, nil).newDynamic() }
		bundle := writeCapabilityBundle(t)
		spec := `{"paths":{"/ping":{"get":{"operationId":"ping"},"post":{"operationId":"poke"}}}}`
		expect(t, os.WriteFile(filepath.Join(bundle, "interfaces", "openapi.json"), []byte(spec), 0o644) == nil, "spec")
		cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
		cmd.SetContext(context.Background())
		var stderr bytes.Buffer
		cmd.SetErr(&stderr)
		for k, v := range map[string]string{"fleet": "true", "delivery": delivery, "local": t.TempDir(), "transport": "http",
			"auth-issuer": "https://idp", "base-url": "http://svc.example"} {
			expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
		}
		s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp", resource: "https://mcp.example/mcp"}
		expect(t, remoteCallerFor(cmd, app.NewService(nil, nil), "v", []string{bundle}, d, &s) == nil, "delivery=%s: remote server", delivery)
		h := mcpHandler(s)
		for range 3 {
			expect(t, post(t, h, "good", listTools).Code == http.StatusOK, "delivery=%s: tools/list", delivery)
		}
		expect(t, strings.Count(stderr.String(), "skipped 1 mutating operation(s)") == 1, "delivery=%s: one notice: %q", delivery, stderr.String())
	}
}

// remoteRecordTool is the record tool name as the remote server lists it to
// a signed-in person.
func remoteRecordTool(t *testing.T, w *remoteWorld, name string) map[string]any {
	t.Helper()
	body := post(t, w.h, "good", listTools).Body.String()
	if i := strings.Index(body, "{"); i >= 0 {
		body = body[i:]
	}
	var res struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&res); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	for _, tool := range res.Result.Tools {
		if tool["name"] == name {
			return tool["inputSchema"].(map[string]any)
		}
	}
	t.Fatalf("%s is not listed: %s", name, body)
	return nil
}

// TestRemoteRecordRefNeedsStageAccess: the remote server's record tools take
// no ref: a record is read only through the caller's Stage, with their token.
func TestRemoteRecordRefNeedsStageAccess(t *testing.T) {
	w := newRemoteWorld(t)
	w.user = "https://idp#carol"
	for name, required := range map[string]string{"pacto_delivery_record_get": "namespace,stage,record", "pacto_delivery_record_verify": ""} {
		s := remoteRecordTool(t, w, name)
		props := s["properties"].(map[string]any)
		var req []string
		for _, r := range func() []any { r, _ := s["required"].([]any); return r }() {
			req = append(req, r.(string))
		}
		if _, ok := props["ref"]; ok || strings.Join(req, ",") != required {
			t.Fatalf("%s: %v required %v", name, props, req)
		}
	}
}

// TestRemoteRecordRefRefusesForeignHost: a ref the caller names is refused
// by the schema before any registry is reached.
func TestRemoteRecordRefRefusesForeignHost(t *testing.T) {
	w := newRemoteWorld(t)
	for _, tool := range []string{"pacto_delivery_record_get", "pacto_delivery_record_verify"} {
		body := post(t, w.h, "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+
			`","arguments":{"ref":"evil.example/x:y"}}}`).Body.String()
		if !strings.Contains(body, `"isError":true`) && !strings.Contains(body, `"error":`) {
			t.Fatalf("%s took a ref: %s", tool, body)
		}
		if !strings.Contains(body, "ref") {
			t.Fatalf("%s: the refusal does not name ref: %s", tool, body)
		}
	}
}

// TestNoMCPServerListsApproveTool pins that agents read approval records and
// never write one (#545, 2026-11-17): no MCP server, stdio for a person or an
// agent or the remote server for either, lists a tool named approve, and
// every delivery tool says so in noApprove's exact words.
func TestNoMCPServerListsApproveTool(t *testing.T) {
	const noApprove = " There is no approve tool: when a person must decide, the plan names who, and they approve themselves."
	servers := map[string][]*mcpsdk.Tool{}
	for name, user := range map[string]string{"stdio person": "alice", "stdio agent": "system:serviceaccount:agents:bot"} {
		d := deliveryDeps(user, nil)
		cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
		cmd.SetContext(context.Background())
		_ = cmd.Flags().Set("delivery", "true")
		mode, err := buildDeliveryMode(cmd, d, nil)
		expect(t, err == nil, "%s: %v", name, err)
		expect(t, mode.Writes == (name == "stdio agent"), "%s writes: %v", name, mode.Writes)
		server, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, d)
		expect(t, err == nil, "%s: %v", name, err)
		servers[name] = listedTools(t, server)
	}
	w := newRemoteWorld(t)
	for name, user := range map[string]string{"remote agent": w.user, "remote person": "https://idp#carol"} {
		w.user = user
		body := post(t, w.h, "good", listTools).Body.String()
		if i := strings.Index(body, "data: "); i >= 0 {
			body = body[i+len("data: "):]
		}
		var res struct {
			Result struct{ Tools []*mcpsdk.Tool } `json:"result"`
		}
		expect(t, json.Unmarshal([]byte(strings.TrimSpace(body)), &res) == nil, "%s: %s", name, body)
		servers[name] = res.Result.Tools
	}
	_, dash := newDashMCP(t)
	for name, bearer := range map[string]string{"dashboard agent": "agent", "dashboard person": "person"} {
		var res struct{ Tools []*mcpsdk.Tool }
		rpcResult(t, dash, bearer, listTools, &res)
		servers[name] = res.Tools
	}
	for name, tools := range servers {
		var delivery int
		for _, tool := range tools {
			expect(t, !strings.Contains(tool.Name, "approve"), "%s lists %s", name, tool.Name)
			if strings.HasPrefix(tool.Name, "pacto_delivery_") {
				delivery++
				expect(t, strings.Contains(tool.Description, noApprove), "%s: %s does not say there is no approve tool", name, tool.Name)
			}
		}
		expect(t, delivery > 0, "%s lists no delivery tool", name)
	}
}

// listedTools lists the tools server serves, descriptions included.
func listedTools(t *testing.T, server *mcpsdk.Server) []*mcpsdk.Tool {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	expect(t, err == nil, "connect: %v", err)
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	expect(t, err == nil, "client: %v", err)
	defer func() { _ = session.Close() }()
	res, err := session.ListTools(context.Background(), nil)
	expect(t, err == nil, "list: %v", err)
	return res.Tools
}

// authOr is a[0], else def.
func authOr(def mcpAuth, a []mcpAuth) mcpAuth {
	if len(a) > 0 {
		return a[0]
	}
	return def
}

const viewerSA = "system:serviceaccount:pacto:pacto-viewer"

// fakeCredentiallessAuth is fakeMCPAuth plus the token path: the bearer
// "viewer" is a ServiceAccount the token review named whose token reads the
// fleet only (Exchange answers authn.ErrNoCredential); "good" is exchanged as
// fakeMCPAuth exchanges it.
type fakeCredentiallessAuth struct {
	fakeMCPAuth
	viewer authn.Person
}

func newCredentiallessAuth() *fakeCredentiallessAuth {
	return &fakeCredentiallessAuth{fakeMCPAuth: fakeMCPAuth{exchange: true}, viewer: authn.Person{User: viewerSA}}
}

func (f *fakeCredentiallessAuth) Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	if token == "viewer" {
		return &auth.TokenInfo{UserID: f.viewer.User, Expiration: time.Now().Add(time.Minute), Extra: map[string]any{"fleetOnly": true}}, nil
	}
	return f.fakeMCPAuth.Verify(ctx, token, r)
}

func (f *fakeCredentiallessAuth) Exchange(ctx context.Context, info *auth.TokenInfo) (string, error) {
	if info.Extra["fleetOnly"] == true {
		return "", authn.ErrNoCredential
	}
	return f.fakeMCPAuth.Exchange(ctx, info)
}

func (f *fakeCredentiallessAuth) PersonOf(*auth.TokenInfo) authn.Person { return f.viewer }

// rpcResult posts one JSON-RPC request as bearer and decodes its result.
func rpcResult(t *testing.T, h http.Handler, bearer, body string, result any) {
	t.Helper()
	rec := post(t, h, bearer, body)
	text := rec.Body.String()
	if i := strings.Index(text, "{"); i >= 0 {
		text = text[i:]
	}
	var res struct {
		Result json.RawMessage `json:"result"`
	}
	expect(t, rec.Code == http.StatusOK && json.Unmarshal([]byte(strings.TrimSpace(text)), &res) == nil && json.Unmarshal(res.Result, result) == nil,
		"%d %s", rec.Code, rec.Body)
}

// remoteToolNames lists the tools the server serves the caller of bearer.
func remoteToolNames(t *testing.T, h http.Handler, bearer string) []string {
	t.Helper()
	var res struct{ Tools []struct{ Name string } }
	rpcResult(t, h, bearer, listTools, &res)
	names := []string{}
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// remoteCall calls one tool as the caller of bearer and returns its text.
func remoteCall(t *testing.T, h http.Handler, bearer, tool, args string) (string, bool) {
	t.Helper()
	var res struct {
		Content []struct{ Text string }
		IsError bool
	}
	rpcResult(t, h, bearer, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+args+`}}`, &res)
	expect(t, len(res.Content) == 1, "%s: %+v", tool, res)
	return res.Content[0].Text, res.IsError
}

// remoteCapabilitiesCaller is pacto_capabilities' caller as bearer reads it.
func remoteCapabilitiesCaller(t *testing.T, h http.Handler, bearer string) map[string]any {
	t.Helper()
	text, isErr := remoteCall(t, h, bearer, "pacto_capabilities", "{}")
	var got map[string]any
	expect(t, !isErr && json.Unmarshal([]byte(text), &got) == nil, "pacto_capabilities: %s", text)
	return got["caller"].(map[string]any)
}

func hasPrefixed(names []string, prefix string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, prefix) })
}

// TestRemoteMCPFleetOnlyCaller: a caller whose token is no credential reads
// the fleet narrowed to them, gets no client, no delivery tool and no write,
// and pacto_capabilities says so with a hint. A caller with a credential on
// the same server is unchanged.
func TestRemoteMCPFleetOnlyCaller(t *testing.T) {
	t.Run("fleet", func(t *testing.T) {
		ca := newCredentiallessAuth()
		w := newRemoteFleet(t, ca)
		w.readers[viewerSA] = true
		logs := &strings.Builder{}
		postLogged(t, w.h, "viewer", listTools, logs)
		expect(t, strings.Contains(logs.String(), "user="+viewerSA) && strings.Contains(logs.String(), "fleetOnly=true") &&
			strings.Contains(logs.String(), "writeTools=false"), "request line:\n%s", logs)
		names := remoteToolNames(t, w.h, "viewer")
		expect(t, slices.Contains(names, "pacto_fleet_status") && !hasPrefixed(names, "pacto_delivery_"), "fleet-only tools: %v", names)
		text, isErr := remoteCall(t, w.h, "viewer", "pacto_fleet_entities", "{}")
		expect(t, !isErr && strings.Contains(text, "orders"), "the viewer reads the fleet: %s", text)
		expect(t, slices.ContainsFunc(w.asked, func(p authn.Person) bool { return p.User == viewerSA }), "RBAC was asked about the viewer: %+v", w.asked)
		ca.viewer = authn.Person{User: "system:serviceaccount:other:nobody"}
		text, _ = remoteCall(t, w.h, "viewer", "pacto_fleet_entities", "{}")
		expect(t, !strings.Contains(text, `"orders"`), "the view narrows to the caller: %s", text)
		ca.viewer = authn.Person{User: viewerSA}
		caller := remoteCapabilitiesCaller(t, w.h, "viewer")
		hint, _ := caller["hint"].(string)
		expect(t, caller["fleetOnly"] == true && caller["writes"] == false &&
			strings.Contains(hint, "--audience pacto-dashboard --audience <your cluster's API audience>"), "fleet-only caller: %v", caller)
		expect(t, !slices.Contains(w.tokens, "for-the-cluster") && !slices.Contains(w.tokens, "viewer"), "a client was built for the viewer: %v", w.tokens)
		// alice holds a credential: exchanged, reviewed, no fleetOnly.
		caller = remoteCapabilitiesCaller(t, w.h, "good")
		expect(t, caller["user"] == "alice" && caller["fleetOnly"] == nil && caller["hint"] == nil, "credentialed caller: %v", caller)
	})
	t.Run("delivery", func(t *testing.T) {
		w := newRemoteWorld(t, newCredentiallessAuth())
		names := remoteToolNames(t, w.h, "viewer")
		expect(t, !hasPrefixed(names, "pacto_delivery_") && slices.Contains(names, "pacto_capabilities"), "fleet-only tools: %v", names)
		expect(t, len(w.cfgs) == 0, "a client was built for the viewer: %v", w.cfgs)
		caller := remoteCapabilitiesCaller(t, w.h, "viewer")
		expect(t, caller["fleetOnly"] == true && caller["class"] == nil && caller["writes"] == false, "fleet-only caller: %v", caller)
		names = remoteToolNames(t, w.h, "good")
		expect(t, slices.Contains(names, "pacto_delivery_operation_propose") && slices.Contains(names, "pacto_delivery_cancel"), "agent tools: %v", names)
	})
	t.Run("unnamed", func(t *testing.T) {
		a := newCredentiallessAuth()
		a.viewer = authn.Person{}
		w := newRemoteFleet(t, a)
		rec := post(t, w.h, "viewer", listTools)
		expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), errNobodyReviewed.Error()), "nobody named: %d %s", rec.Code, rec.Body)
	})
}

// TestRemoteMCPFleetOnlyCallerCannotDeclare: pacto_declare and pacto_accept
// read the caller's own client, so the fleet-only server registers neither;
// a caller with a credential still lists pacto_declare.
func TestRemoteMCPFleetOnlyCallerCannotDeclare(t *testing.T) {
	w := newRemoteFleet(t, newCredentiallessAuth())
	names := remoteToolNames(t, w.h, "viewer")
	expect(t, !slices.Contains(names, "pacto_declare") && !slices.Contains(names, "pacto_accept"), "fleet-only tools: %v", names)
	for _, tool := range []string{"pacto_declare", "pacto_accept"} {
		rec := post(t, w.h, "viewer", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":{}}}`)
		text := rec.Body.String()
		expect(t, rec.Code == http.StatusOK && strings.Contains(text, "unknown tool"), "%s for a fleet-only caller: %d %s", tool, rec.Code, text)
	}
	expect(t, slices.Contains(remoteToolNames(t, w.h, "good"), "pacto_declare"), "a credentialed caller lists pacto_declare")
}

// tokenPathServe is the dashboard's token path: no issuer, no fixed resource.
func tokenPathServe(t *testing.T) http.Handler {
	t.Helper()
	w := newRemoteFleet(t)
	s := mcpServe{auth: w.fa, server: mcpsdk.NewServer(&mcpsdk.Implementation{Name: "pacto", Version: "v"}, nil)}
	return mcpHandler(s)
}

func TestRemoteMCPMetadataWithoutIssuer(t *testing.T) {
	h := tokenPathServe(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/.well-known/oauth-protected-resource", nil))
	expect(t, rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), "authorization_servers"), "metadata: %d %s", rec.Code, rec.Body)
}

func TestRemoteMCPMetadataFromRequestOrigin(t *testing.T) {
	h := tokenPathServe(t)
	for _, c := range []struct{ target, origin string }{
		{"http://127.0.0.1:3000", "http://127.0.0.1:3000"},
		{"https://pacto.example", "https://pacto.example"},
	} {
		r := httptest.NewRequest(http.MethodPost, c.target+"/mcp", strings.NewReader(listTools))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Header().Get("WWW-Authenticate"),
			`resource_metadata="`+c.origin+`/.well-known/oauth-protected-resource/mcp"`), "%s: %d %v", c.target, rec.Code, rec.Header())
		for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.target+path, nil))
			expect(t, rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"resource":"`+c.origin+`/mcp"`), "%s%s: %s", c.target, path, rec.Body)
		}
	}
}

// TestFleetOnlyModesHint: the fleet-only server serves no delivery, and its
// hint names the API server's audience when the dashboard could read it.
func TestFleetOnlyModesHint(t *testing.T) {
	m := fleetOnlyModes(pactomcp.Modes{Delivery: &pactomcp.DeliveryMode{Writes: true}}, "https://kubernetes.default.svc")
	expect(t, m.Delivery == nil && m.Bundle == nil && m.DeliveryHidden, "fleet-only modes serve delivery: %+v", m)
	expect(t, !fleetOnlyModes(pactomcp.Modes{}, "").DeliveryHidden, "a host with no delivery hides none")
	expect(t, strings.HasSuffix(m.FleetOnlyHint, "--audience pacto-dashboard --audience https://kubernetes.default.svc"), "hint: %s", m.FleetOnlyHint)
}

// TestRemoteCreatorRefusesABadTrustConfig: with a registry and the fleet
// view, pacto_create_service reads the trust file at start, and an
// unreadable one fails it.
func TestRemoteCreatorRefusesABadTrustConfig(t *testing.T) {
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"delivery-trust": filepath.Join(t.TempDir(), "absent"), "fleet": "true", "registry": "ghcr.io/acme",
		"declare-registry-config": emptyDockerConfig(t)} {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}}
	err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s)
	expect(t, err != nil && strings.Contains(err.Error(), "absent"), "an unreadable trust file was accepted: %v", err)
}
