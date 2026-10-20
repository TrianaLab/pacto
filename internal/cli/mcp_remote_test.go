package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
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
	return &auth.TokenInfo{UserID: "https://idp#agent", Expiration: time.Now().Add(time.Hour), Extra: map[string]any{"token": token}}, nil
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

func TestMCPRemoteFlags(t *testing.T) {
	fa := &fakeMCPAuth{exchange: true}
	d := deliveryDeps("system:serviceaccount:agents:bot", nil)
	var got authn.MCPConfig
	d.newMCPAuth = func(c authn.MCPConfig) (mcpAuth, error) { got = c; return fa, nil }
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	t.Setenv("PACTO_MCP_EXCHANGE_CLIENT_SECRET", "s3cret")
	if err := remoteCmd(t, d, "-t", "http", "--host", "0.0.0.0", "--port", "0"); !errors.Is(err, errAuthBeyondLoopback) {
		t.Fatalf("beyond loopback without auth: %v", err)
	}
	if err := remoteCmd(t, d, "-t", "http", "--auth-issuer", "https://idp"); err == nil || !strings.Contains(err.Error(), "--resource") {
		t.Fatalf("auth needs a resource: %v", err)
	}
	err := remoteCmd(t, d, "--delivery", "-t", "http", "--host", "0.0.0.0", "--port", "0", "--auth-issuer", "https://idp",
		"--resource", "https://mcp.example/mcp", "--exchange-client-id", "pacto-mcp")
	if err != nil || got.Audience != "https://mcp.example/mcp" || got.ClientSecret != "s3cret" || got.ExchangeAudience != "pacto-cli" || got.ClientID != "pacto-mcp" {
		t.Fatalf("remote delivery starts with exchange configured: %v %+v", err, got)
	}
	_ = remoteCmd(t, d, "-t", "http", "--port", "0", "--auth-issuer", "https://idp", "--resource", "https://mcp.example/mcp", "--audience", "pacto-mcp")
	if got.Audience != "pacto-mcp" {
		t.Fatalf("--audience overrides the resource: %+v", got)
	}
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return nil, errors.New("issuer down") }
	if err := remoteCmd(t, d, "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp"); err == nil || err.Error() != "issuer down" {
		t.Fatalf("an unreadable issuer stops startup: %v", err)
	}
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return &fakeMCPAuth{}, nil }
	if err := remoteCmd(t, d, "--delivery", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp"); !errors.Is(err, errDeliveryNoExchange) {
		t.Fatalf("delivery without exchange: %v", err)
	}
	d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return fa, nil }
	d.clusterConfig = func() (*rest.Config, error) { return nil, errors.New("no cluster") }
	if err := remoteCmd(t, d, "--delivery", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp", "--exchange-client-id", "c"); err == nil ||
		!strings.Contains(err.Error(), "no cluster") {
		t.Fatalf("no cluster: %v", err)
	}
	if err := remoteCmd(t, d, "--delivery", "--fleet", "-t", "http", "--auth-issuer", "https://idp", "--resource", "https://m/mcp"); err == nil ||
		!strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("modes still exclusive: %v", err)
	}
	if c, err := newDynamicForConfig(&rest.Config{Host: "https://k"}); err != nil || c == nil {
		t.Fatalf("newDynamicForConfig: %v", err)
	}
	if _, err := newMCPAuth(authn.MCPConfig{}); err == nil {
		t.Fatal("newMCPAuth validates")
	}
}

// post sends one JSON-RPC request through h and returns the answer.
func post(t *testing.T, h http.Handler, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://mcp.example/mcp", strings.NewReader(body))
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

func TestRemoteDeliveryIdentityPerRequest(t *testing.T) {
	fa := &fakeMCPAuth{exchange: true}
	user := "system:serviceaccount:agents:bot"
	var cfgs []*rest.Config
	var clientErr error
	whoAmIFails := false
	d := deliveryDeps(user, nil)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil }
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
		cfgs = append(cfgs, c)
		if clientErr != nil {
			return nil, clientErr
		}
		dyn, _ := deliveryDeps(user, nil).newDynamic()
		if whoAmIFails {
			dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("token rejected")
			})
		}
		return dyn, nil
	}
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	s := mcpServe{auth: fa, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	if err := remoteDeliveryFor(cmd, app.NewService(nil, nil), "v", d, &s); err != nil {
		t.Fatal(err)
	}
	h := mcpHandler(s)
	// No token, a bad token: 401 naming the metadata.
	for _, tok := range []string{"", "bad"} {
		rec := post(t, h, tok, listTools)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"),
			`resource_metadata="https://mcp.example/.well-known/oauth-protected-resource/mcp"`) {
			t.Fatalf("token %q: %d %v", tok, rec.Code, rec.Header())
		}
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"resource":"https://mcp.example/mcp"`) ||
			!strings.Contains(rec.Body.String(), `"authorization_servers":["https://idp"]`) {
			t.Fatalf("metadata %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// An agent: exchanged, the cluster asked with the exchanged token only,
	// propose and cancel listed, no approve tool.
	rec := post(t, h, "good", listTools)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "pacto_delivery_operation_propose") || strings.Contains(body, "pacto_delivery_approve") {
		t.Fatalf("agent tools: %d %s", rec.Code, body)
	}
	if last := cfgs[len(cfgs)-1]; last.BearerToken != "for-the-cluster" || last.Host != "https://k" || len(fa.exchanged) == 0 {
		t.Fatalf("the cluster must see the exchanged token, never the process's: %+v", last)
	}
	// A person, on the next request: read tools only.
	user = "https://idp#carol"
	if body = post(t, h, "good", listTools).Body.String(); strings.Contains(body, "pacto_delivery_operation_propose") ||
		!strings.Contains(body, "pacto_delivery_get") {
		t.Fatalf("a person's tools are read-only: %s", body)
	}
	// Failures say why.
	fa.exchangeErr = errors.New("the IdP refused the exchange")
	if rec = post(t, h, "good", listTools); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "refused the exchange") {
		t.Fatalf("exchange refused: %d %s", rec.Code, rec.Body)
	}
	fa.exchangeErr, clientErr = nil, errors.New("bad config")
	if rec = post(t, h, "good", listTools); rec.Code != http.StatusInternalServerError {
		t.Fatalf("client error: %d", rec.Code)
	}
	clientErr, whoAmIFails = nil, true
	if rec = post(t, h, "good", listTools); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "token rejected") {
		t.Fatalf("whoami refused: %d %s", rec.Code, rec.Body)
	}
}
