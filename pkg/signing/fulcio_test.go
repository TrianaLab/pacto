package signing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fulcioConfig = `{"issuers":[
 {"issuerUrl":"https://login.acme.com/realms/acme","audience":"pacto-dashboard-sign"},
 {"issuerUrl":"https://accounts.example","audience":"sigstore"},
 {"wildcardIssuerUrl":"https://oidc.eks.*.amazonaws.com/id/*","audience":"sigstore"}]}`

func fakeFulcio(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/configuration" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFulcioTrustsMatchesIssuerAndAudience(t *testing.T) {
	url := fakeFulcio(t, http.StatusOK, fulcioConfig)
	ok, reason, err := FulcioTrusts(context.Background(), http.DefaultClient, url+"/", "https://login.acme.com/realms/acme", "pacto-dashboard-sign")
	if err != nil || !ok || reason != "" {
		t.Fatalf("trusted issuer and audience: %v %q %v", ok, reason, err)
	}
	ok, reason, err = FulcioTrusts(context.Background(), http.DefaultClient, url, "https://login.other.com", "pacto-dashboard-sign")
	if err != nil || ok || !strings.Contains(reason, "does not accept tokens from https://login.other.com") {
		t.Fatalf("unknown issuer: %v %q %v", ok, reason, err)
	}
}

func TestFulcioTrustsWildcard(t *testing.T) {
	url := fakeFulcio(t, http.StatusOK, fulcioConfig)
	ok, _, err := FulcioTrusts(context.Background(), http.DefaultClient, url, "https://oidc.eks.eu-west-1.amazonaws.com/id/ABC123", "sigstore")
	if err != nil || !ok {
		t.Fatalf("a wildcard issuer must match: %v %v", ok, err)
	}
	for _, issuer := range []string{
		"https://oidc.eks.eu.west.amazonaws.com/id/ABC",   // * stands for one label, never a dot
		"https://oidc.eks.eu-west-1.amazonaws.com/id/a/b", // nor a slash
		"https://oidc.eksXeu-west-1.amazonaws.com/id/ABC", // the dots are literal
	} {
		if ok, _, _ := FulcioTrusts(context.Background(), http.DefaultClient, url, issuer, "sigstore"); ok {
			t.Errorf("%s must not match the wildcard", issuer)
		}
	}
}

func TestFulcioTrustsWrongAudience(t *testing.T) {
	url := fakeFulcio(t, http.StatusOK, fulcioConfig)
	ok, reason, err := FulcioTrusts(context.Background(), http.DefaultClient, url, "https://login.acme.com/realms/acme", "pacto-dashboard")
	if err != nil || ok || !strings.Contains(reason, "another audience than pacto-dashboard") {
		t.Fatalf("a token for the session client must never be trusted: %v %q %v", ok, reason, err)
	}
}

func TestFulcioUnreadableIsNone(t *testing.T) {
	for name, url := range map[string]string{
		"status":  fakeFulcio(t, http.StatusInternalServerError, "{}"),
		"json":    fakeFulcio(t, http.StatusOK, "not json"),
		"down":    "http://127.0.0.1:1",
		"request": "http://bad host",
	} {
		ok, _, err := FulcioTrusts(context.Background(), http.DefaultClient, url, "https://accounts.example", "sigstore")
		if ok || err == nil {
			t.Errorf("%s: an unreadable configuration is unknown, never trusted: %v %v", name, ok, err)
		}
	}
}
