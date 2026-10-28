package authn

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIssuerClientTrustsTheCABundle(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	ca := writeFile(t, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))

	plain, err := IssuerClient(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(srv.URL); err == nil {
		t.Fatal("a client without the CA bundle trusted a self-signed issuer")
	}
	c, err := IssuerClient(srv.URL, "", ca)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestIssuerClientRefusesABadCABundle(t *testing.T) {
	if _, err := IssuerClient("https://idp", "", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing CA file was accepted")
	}
	if _, err := IssuerClient("https://idp", "", writeFile(t, "not a certificate")); err == nil || !strings.Contains(err.Error(), "no PEM") {
		t.Fatalf("a file without certificates: %v", err)
	}
	old := systemRoots
	systemRoots = func() (*x509.CertPool, error) { return nil, errors.New("no roots") }
	defer func() { systemRoots = old }()
	if _, err := IssuerClient("https://idp", "", writeFile(t, "x")); err == nil || !strings.Contains(err.Error(), "no roots") {
		t.Fatalf("system roots failing: %v", err)
	}
}

func TestIssuerClientRefusesABadInternalURL(t *testing.T) {
	for _, c := range []struct{ issuer, internal string }{
		{"://bad", "http://idp"},
		{"https://idp", "://bad"},
		{"https://idp", "ftp://idp"},
		{"https://idp", "http:///path"},
		{"https://idp", "http://idp?x=1"},
	} {
		if _, err := IssuerClient(c.issuer, c.internal, ""); err == nil {
			t.Errorf("%s via %s was accepted", c.issuer, c.internal)
		}
	}
}

// A dashboard reaching the issuer at an internal address signs people in
// against the public issuer the browser uses: discovery, the code exchange,
// keys and revocation go to the internal address, the browser to the public
// one, and the ID token is still checked against the public issuer.
func TestOIDCSignsInThroughAnInternalIssuerURL(t *testing.T) {
	f := newIdP(t)
	f.public = "https://idp.example.com/realms/acme"
	var seen []string
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		f.srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer internal.Close()
	client, err := IssuerClient(f.public, internal.URL+"/", "")
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewOIDC(Config{IssuerURL: f.public, ClientID: "pacto-dashboard", ClientSecret: "s",
		RedirectURL: "https://dash.example/auth/callback", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, f.public+"/auth?") {
		t.Fatalf("the browser goes to %q, not the public issuer", loc)
	}
	cb := login(t, o, f, "")
	if cb.Code != http.StatusFound {
		t.Fatalf("callback: %d %s", cb.Code, cb.Body)
	}
	if got := strings.Join(seen, " "); !strings.Contains(got, "/.well-known/openid-configuration") ||
		!strings.Contains(got, "/token") || !strings.Contains(got, "/keys") {
		t.Fatalf("the internal address saw %q", got)
	}

	// Another host passes through unchanged.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	defer other.Close()
	resp, err := client.Get(other.URL + "/realms/acme")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("another host answered %d", resp.StatusCode)
	}
	// A path outside the issuer's keeps its path on the internal address.
	n := len(seen)
	resp, err = client.Get("https://idp.example.com/realms/acmex")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if seen[n] != "/realms/acmex" {
		t.Fatalf("a path outside the issuer became %q", seen[n])
	}
}
