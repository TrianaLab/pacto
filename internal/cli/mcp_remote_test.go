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
	err := remoteDeliveryFor(newMCPCommand(app.NewService(nil, nil), "v", d), app.NewService(nil, nil), "v", d, &s)
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
	rec := post(t, w.h, "good", listTools)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "refused the exchange"), "exchange refused: %d %s", rec.Code, rec.Body)
	w.fa.exchangeErr, w.clientErr = nil, errors.New("bad config")
	rec = post(t, w.h, "good", listTools)
	expect(t, rec.Code == http.StatusInternalServerError, "client error: %d", rec.Code)
	w.clientErr, w.whoAmIFails = nil, true
	rec = post(t, w.h, "good", listTools)
	expect(t, rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "token rejected"), "whoami refused: %d %s", rec.Code, rec.Body)
}
