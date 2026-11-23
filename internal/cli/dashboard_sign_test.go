package cli

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// signViper is a shared dashboard with OIDC sign-in and a sign client.
func signViper(fulcio string) *viper.Viper {
	v := viper.New()
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "pacto-dashboard")
	v.Set("dashboard.oidc-redirect-url", "https://dash.example/auth/callback")
	v.Set("dashboard.sign-client-id", "pacto-sign")
	v.Set("dashboard.sign-redirect-url", "https://dash.example/auth/sign/callback")
	v.Set("dashboard.fulcio-url", fulcio)
	return v
}

func mustOIDC(t *testing.T) *authn.OIDC {
	t.Helper()
	o, err := authn.NewOIDC(authn.Config{IssuerURL: "https://idp.example", ClientID: "pacto-dashboard", RedirectURL: "https://dash.example/auth/callback"})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// fakeFulcio answers Fulcio's configuration and counts the reads.
func fakeFulcio(t *testing.T, body string) (string, *atomic.Int32) {
	t.Helper()
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &reads
}

func TestDashboardSigningNeedsOIDC(t *testing.T) {
	s, err := newDashboardSigning(viper.New(), nil)
	expect(t, s == nil && err == nil, "no sign client, no signing login: %v", err)
	expect(t, s.login() == nil, "nothing to serve")
	s.report(context.Background()) // says nothing

	v := viper.New()
	v.Set("dashboard.sign-client-id", "pacto-sign")
	v.Set("dashboard.allow-anonymous", true)
	_, err = newDashboardIdentity(v, "0.0.0.0", nil, homeEntries(nil), defaultDeps(), nil)
	expect(t, err == errSignNeedsOIDC, "a sign client without OIDC sign-in is refused at start: %v", err)

	v = signViper("https://fulcio.example")
	v.Set("dashboard.sign-client-id", "pacto-dashboard")
	_, err = newDashboardIdentity(v, "0.0.0.0", nil, homeEntries(nil), defaultDeps(), nil)
	expect(t, err != nil && strings.Contains(err.Error(), "must differ"), "the sign client is never the sign-in client: %v", err)
}

func TestDashboardServesTheSigningLogin(t *testing.T) {
	id := identityFor(t, signViper("https://fulcio.example"), "0.0.0.0", nil)
	expect(t, id.signing != nil && id.signing.login() != nil, "a sign client builds the signing login")
	rec := httptest.NewRecorder()
	id.auth.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/sign?ticket=x", nil))
	expect(t, rec.Code == http.StatusForbidden, "/auth/sign is served and needs a session and a ticket: %d", rec.Code)

	v := signViper("")
	v.Set("dashboard.sign-client-id", "")
	rec = httptest.NewRecorder()
	identityFor(t, v, "0.0.0.0", nil).auth.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/sign", nil))
	expect(t, rec.Code == http.StatusNotFound, "without a sign client there is no signing login: %d", rec.Code)
}

func TestDashboardSigningTrustIsCached(t *testing.T) {
	url, reads := fakeFulcio(t, `{"issuers":[{"issuerUrl":"https://idp.example","audience":"pacto-sign"}]}`)
	s, err := newDashboardSigning(signViper(url), mustOIDC(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	ok, reason := s.trusted(context.Background())
	expect(t, ok && reason == "", "Fulcio trusts the sign client: %v %q", ok, reason)
	s.trusted(context.Background())
	expect(t, reads.Load() == 1, "the answer is kept: %d reads", reads.Load())
	now = now.Add(fulcioTrustTTL)
	s.trusted(context.Background())
	expect(t, reads.Load() == 2, "and asked again after %s: %d reads", fulcioTrustTTL, reads.Load())
}

func TestDashboardSigningUnreadableFulcioIsUntrusted(t *testing.T) {
	s, err := newDashboardSigning(signViper("http://127.0.0.1:1"), mustOIDC(t))
	if err != nil {
		t.Fatal(err)
	}
	ok, reason := s.trusted(context.Background())
	expect(t, !ok && strings.Contains(reason, "reading Fulcio's configuration"), "unknown is never trusted: %v %q", ok, reason)
}

func TestDashboardSigningReport(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"trusted":   {`{"issuers":[{"issuerUrl":"https://idp.example","audience":"pacto-sign"}]}`, "sign declarations in the dashboard with a fresh login"},
		"untrusted": {`{"issuers":[{"issuerUrl":"https://idp.example","audience":"sigstore"}]}`, "save drafts instead"},
	} {
		url, _ := fakeFulcio(t, tc.body)
		s, err := newDashboardSigning(signViper(url), mustOIDC(t))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		s.report(logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&buf, nil))))
		expect(t, strings.Contains(buf.String(), tc.want), "%s: %s", name, buf.String())
	}
}

// signPathFiles are the files a dashboard signature passes through.
var signPathFiles = []string{"dashboard_sign.go", "../authn/sign.go", "dashboard_declare.go", "declare_drafts_writer.go", "../../pkg/dashboard/declare.go"}

// TestSignNeverUsesSessionToken: the session's ID token is the bearer the API
// server accepts, so it must never reach Fulcio. No sign path calls
// Session.IDToken() or reads authn.State.Token, and the session's raw token
// is read in one place only: the same-person comparison of the callback. The
// one State.Token read allowed is the person's own cluster read,
// authn.RESTConfig(cluster, st.Token), which never reaches Fulcio.
func TestSignNeverUsesSessionToken(t *testing.T) {
	for _, path := range signPathFiles {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		compared := map[ast.Expr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "samePerson" {
					for _, a := range c.Args {
						compared[a] = true
					}
				}
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "RESTConfig" && len(c.Args) == 2 {
					compared[c.Args[1]] = true
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "IDToken":
				t.Errorf("%s reads %s: the session's token never takes part in signing", path, sel.Sel.Name)
			case "Token":
				expect(t, compared[sel], "%s reads Token outside the person's own cluster read: the session's token never takes part in signing", path)
			case "idToken":
				expect(t, compared[sel], "%s reads the session's raw token outside the same-person check", path)
			}
			return true
		})
	}
}

// TestDashboardSigningFulcioErrorIsUnknown: a failed read of Fulcio's
// configuration says Pacto could not check, never that Fulcio refuses, and
// is asked again soon.
func TestDashboardSigningFulcioErrorIsUnknown(t *testing.T) {
	s, err := newDashboardSigning(signViper("http://127.0.0.1:1"), mustOIDC(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	m := s.signingMode(context.Background(), false, "oidc", "ana@acme.com")
	expect(t, m.Mode == "none" && strings.HasPrefix(m.Reason, "Pacto could not check whether Fulcio") && !strings.Contains(m.Reason, "does not issue"), "%+v", m)
	expect(t, s.until.Equal(now.Add(fulcioErrorTTL)), "an error is kept %s, not %s", fulcioErrorTTL, s.until.Sub(now))
}
