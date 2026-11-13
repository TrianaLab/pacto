package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
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
	wantErr(t, remoteCmd(t, d, "--delivery", "--fleet", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp"), "cannot be combined")
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

func newRemoteWorld(t *testing.T) *remoteWorld {
	t.Helper()
	w := &remoteWorld{fa: &fakeMCPAuth{exchange: true}, user: "system:serviceaccount:agents:bot"}
	d := deliveryDeps(w.user, nil)
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
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
	s := mcpServe{auth: w.fa, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	_ = cmd.Flags().Set("oidc-username-claim", "email")
	_ = cmd.Flags().Set("delivery", "true")
	err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", d, &s)
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
	if err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", d, &s); err == nil || !strings.Contains(err.Error(), "trust config") {
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
}

func newRemoteFleet(t *testing.T) *remoteFleet {
	t.Helper()
	w := &remoteFleet{fa: &fakeMCPAuth{exchange: true}, reviewed: authn.Person{User: "alice", Groups: []string{"team-a"}}, readers: map[string]bool{"alice": true}}
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
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
	s := mcpServe{auth: w.fa, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	expect(t, remoteCallerFor(cmd, app.NewService(nil, nil), "v", d, &s) == nil, "remote fleet")
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
	// (remoteCmd runs on a cancelled context, so the build fails).
	wantErr(t, remoteCmd(t, d, "--fleet", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp", "--exchange-client-id", "c"),
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
	// Without the argument nothing fills it in: not the token, not the review.
	body = propose(`"namespace":"prod","stage":"payments-prod","to":"` + cliB + `"`)
	expect(t, strings.Contains(body, `\"actor\"`) && !strings.Contains(body, "onBehalfOf") && !strings.Contains(body, "agent@example.com"),
		"no derived on_behalf_of: %s", body)
}
