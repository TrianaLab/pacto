//go:build !js

package oci

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

func sourceNames(k *PactoKeychain) []string {
	out := make([]string, 0, len(k.sources))
	for _, s := range k.sources {
		out = append(out, s.name)
	}
	return out
}

func regOf(t *testing.T, host string) name.Registry {
	t.Helper()
	r, err := name.NewRegistry(host)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// countingKeychain answers every host and counts the calls.
type countingKeychain struct{ calls map[string]int }

func (c countingKeychain) Resolve(t authn.Resource) (authn.Authenticator, error) {
	c.calls[t.RegistryStr()]++
	return authn.FromConfig(authn.AuthConfig{Username: "u", Password: "p"}), nil
}

// countingHelper answers every host and counts the calls.
type countingHelper struct{ calls map[string]int }

func (c countingHelper) Get(host string) (string, string, error) {
	c.calls[host]++
	return "AWS", "secret", nil
}

// fakeHelper answers only for host.
type fakeHelper struct{ host string }

func (f fakeHelper) Get(host string) (string, string, error) {
	if host != f.host {
		return "", "", errors.New("no credentials")
	}
	return "AWS", "ecr-pass", nil
}

// swapCloud replaces the cloud package variables for one test.
func swapCloud(t *testing.T, g authn.Keychain, h authn.Helper, tok func(context.Context) (string, error)) {
	t.Helper()
	og, oh, ot := googleKeychain, ecrHelper, acrToken
	t.Cleanup(func() { googleKeychain, ecrHelper, acrToken = og, oh, ot })
	googleKeychain, ecrHelper, acrToken = g, h, tok
}

func swapExchange(t *testing.T, srv *httptest.Server) {
	t.Helper()
	oc, ou := acrClient, acrExchangeURL
	t.Cleanup(func() { acrClient, acrExchangeURL = oc, ou })
	acrClient = srv.Client()
	acrExchangeURL = func(string) string { return srv.URL + "/oauth2/exchange" }
}

func TestNewKeychainOrder(t *testing.T) {
	base := []string{"pacto login", "gh", "docker", "google", "ecr", "acr"}
	cases := []struct {
		opts CredentialOptions
		want []string
	}{
		{CredentialOptions{}, base},
		{CredentialOptions{Token: "t"}, append([]string{"env token"}, base...)},
		{CredentialOptions{Username: "u", Password: "p"}, append([]string{"env user/pass"}, base...)},
	}
	for _, c := range cases {
		if got := sourceNames(NewKeychain(c.opts)); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("NewKeychain(%+v) sources = %v, want %v", c.opts, got, c.want)
		}
	}
}

func TestNewKeychainSkipCloud(t *testing.T) {
	if got := sourceNames(NewKeychain(CredentialOptions{SkipCloud: true})); fmt.Sprint(got) != "[pacto login gh docker]" {
		t.Errorf("SkipCloud sources = %v", got)
	}
	if got := sourceNames(CloudKeychain()); fmt.Sprint(got) != "[google ecr acr]" {
		t.Errorf("CloudKeychain sources = %v", got)
	}
}

func TestCloudHostPredicates(t *testing.T) {
	cases := []struct {
		name  string
		match func(string) bool
		hosts map[string]bool
	}{
		{"google", googleHost, map[string]bool{
			"europe-docker.pkg.dev": true, "gcr.io": true, "eu.gcr.io": true,
			"pkg.dev": false, "pkg.dev.evil.com": false, "gcr.io.evil.com": false, "evilgcr.io": false, "ghcr.io": false,
		}},
		{"ecr", ecrHost, map[string]bool{
			"123456789012.dkr.ecr.eu-west-1.amazonaws.com": true, "123456789012.dkr.ecr-fips.us-east-1.amazonaws.com": true,
			"123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn": true, "public.ecr.aws": true,
			"dkr.ecr.amazonaws.com.evil": false, "123.dkr.ecr.eu-west-1.amazonaws.com": false,
			"123456789012.dkr.ecr.eu-west-1.amazonaws.com.evil": false, "evil.public.ecr.aws": false,
		}},
		{"acr", acrHost, map[string]bool{
			"acme.azurecr.io": true, "acme.azurecr.cn": true, "acme.azurecr.us": true,
			"azurecr.io": false, "acme.azurecr.io.evil": false, "acmeazurecr.io": false,
		}},
	}
	for _, c := range cases {
		for host, want := range c.hosts {
			if got := c.match(host); got != want {
				t.Errorf("%s(%q) = %v, want %v", c.name, host, got, want)
			}
		}
	}
}

func TestCloudSourcesOnlyAnswerTheirHosts(t *testing.T) {
	g := countingKeychain{calls: map[string]int{}}
	h := countingHelper{calls: map[string]int{}}
	swapCloud(t, g, h, func(context.Context) (string, error) {
		return "", azidentity.NewCredentialUnavailableError("none")
	})
	kc := CloudKeychain()
	// Every fake answers every host; only the Pacto-side predicate stops them.
	owner := map[string]string{
		"ghcr.io":               "",
		"index.docker.io":       "",
		"europe-docker.pkg.dev": "google",
		"123456789012.dkr.ecr.eu-west-1.amazonaws.com": "ecr",
		"acme.azurecr.io": "acr",
	}
	for host, want := range owner {
		before := map[string]int{"google": g.calls[host], "ecr": h.calls[host]}
		calledACR := false
		acrToken = func(context.Context) (string, error) {
			calledACR = true
			return "", azidentity.NewCredentialUnavailableError("none")
		}
		if _, err := kc.Candidates(regOf(t, host)); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		got := map[string]bool{
			"google": g.calls[host] > before["google"],
			"ecr":    h.calls[host] > before["ecr"],
			"acr":    calledACR,
		}
		for src, called := range got {
			if called != (src == want) {
				t.Errorf("%s: source %s called = %v, owner %q", host, src, called, want)
			}
		}
	}
}

func TestGoogleSourceUsesKeychain(t *testing.T) {
	g := countingKeychain{calls: map[string]int{}}
	swapCloud(t, g, fakeHelper{}, acrToken)
	cands, err := CloudKeychain().Candidates(regOf(t, "europe-docker.pkg.dev"))
	if err != nil || len(cands) != 1 || cands[0].Name != "google" {
		t.Fatalf("Candidates = %v, %v; want one google", cands, err)
	}
}

func TestECRSourceUsesHelper(t *testing.T) {
	const host = "123456789012.dkr.ecr.eu-west-1.amazonaws.com"
	swapCloud(t, countingKeychain{calls: map[string]int{}}, fakeHelper{host: host}, acrToken)
	auth, err := CloudKeychain().Resolve(regOf(t, host))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := auth.Authorization()
	if cfg.Password != "ecr-pass" {
		t.Errorf("ECR password = %q", cfg.Password)
	}
	// A helper error reads as anonymous.
	auth, err = CloudKeychain().Resolve(regOf(t, "999999999999.dkr.ecr.eu-west-1.amazonaws.com"))
	if err != nil || auth != authn.Anonymous {
		t.Errorf("helper error = %v, %v; want anonymous", auth, err)
	}
}

func TestACRExchangeTradesTheAADToken(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method != http.MethodPost || r.Form.Get("grant_type") != "access_token" ||
			r.Form.Get("service") != "acme.azurecr.io" || r.Form.Get("access_token") != "aad" {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"refresh_token":"acr-refresh"}`))
	}))
	defer srv.Close()
	swapCloud(t, googleKeychain, ecrHelper, func(context.Context) (string, error) { return "aad", nil })
	swapExchange(t, srv)
	auth, err := CloudKeychain().Resolve(regOf(t, "acme.azurecr.io"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := auth.Authorization()
	if cfg.Username != acrUsername || cfg.Password != "acr-refresh" {
		t.Errorf("ACR auth = %+v", cfg)
	}
}

func TestACRCredentialUnavailableIsAnonymous(t *testing.T) {
	swapCloud(t, googleKeychain, ecrHelper, func(context.Context) (string, error) {
		return "", fmt.Errorf("wrapped: %w", azidentity.NewCredentialUnavailableError("no identity"))
	})
	auth, err := CloudKeychain().Resolve(regOf(t, "acme.azurecr.io"))
	if err != nil || auth != authn.Anonymous {
		t.Errorf("Resolve = %v, %v; want anonymous", auth, err)
	}
}

func TestACRTokenFailureNamesTheSource(t *testing.T) {
	swapCloud(t, googleKeychain, ecrHelper, func(context.Context) (string, error) {
		return "", errors.New("identity misconfigured")
	})
	_, err := CloudKeychain().Resolve(regOf(t, "acme.azurecr.io"))
	if err == nil || !strings.Contains(err.Error(), "acr: identity misconfigured") {
		t.Errorf("err = %v", err)
	}
}

func TestACRExchangeFailureNamesTheSource(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	swapCloud(t, googleKeychain, ecrHelper, func(context.Context) (string, error) { return "aad", nil })
	swapExchange(t, srv)
	_, err := CloudKeychain().Resolve(regOf(t, "acme.azurecr.io"))
	if err == nil || !strings.Contains(err.Error(), "acr") || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v; want acr and 401", err)
	}
}

func TestACRExchangeMissingRefreshToken(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	swapCloud(t, googleKeychain, ecrHelper, func(context.Context) (string, error) { return "aad", nil })
	swapExchange(t, srv)
	_, err := CloudKeychain().Resolve(regOf(t, "acme.azurecr.io"))
	if err == nil || !strings.Contains(err.Error(), "no refresh_token") {
		t.Errorf("err = %v", err)
	}
}

func TestACRExchangeRequestErrors(t *testing.T) {
	oc, ou := acrClient, acrExchangeURL
	t.Cleanup(func() { acrClient, acrExchangeURL = oc, ou })
	acrExchangeURL = func(string) string { return "://bad" }
	if _, err := acrExchange(context.Background(), "acme.azurecr.io", "aad"); err == nil || !strings.Contains(err.Error(), "acr") {
		t.Errorf("bad URL err = %v", err)
	}
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	acrExchangeURL = func(string) string { return srv.URL }
	srv.Close() // connection refused
	if _, err := acrExchange(context.Background(), "acme.azurecr.io", "aad"); err == nil || !strings.Contains(err.Error(), "acr") {
		t.Errorf("closed server err = %v", err)
	}
}

func TestACRExchangeIgnoresInsecureRegistries(t *testing.T) {
	t.Setenv("PACTO_INSECURE_REGISTRIES", "acme.azurecr.io")
	if got := acrExchangeURL("acme.azurecr.io"); got != "https://acme.azurecr.io/oauth2/exchange" {
		t.Errorf("exchange URL = %q", got)
	}
}

func TestDefaultACRTokenFailsWithoutIdentity(t *testing.T) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "AZURE_") {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
	t.Setenv("PATH", t.TempDir()) // no az, azd or pwsh to fall back on
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := defaultACRToken(ctx); err == nil {
		t.Fatal("defaultACRToken succeeded with no identity")
	}
	// An invalid AZURE_TOKEN_CREDENTIALS fails at construction.
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "nonsense")
	if _, err := defaultACRToken(ctx); err == nil {
		t.Fatal("defaultACRToken accepted an invalid AZURE_TOKEN_CREDENTIALS")
	}
}

func TestPactoKeychainCandidatesIncludeCloud(t *testing.T) {
	const host = "acme.azurecr.io"
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DOCKER_CONFIG", dir)
	auth := base64.StdEncoding.EncodeToString([]byte("stale:cred"))
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{"`+host+`":{"auth":"`+auth+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"refresh_token":"r"}`))
	}))
	defer srv.Close()
	swapCloud(t, googleKeychain, ecrHelper, func(context.Context) (string, error) { return "aad", nil })
	swapExchange(t, srv)
	cands, err := NewKeychain(CredentialOptions{}).Candidates(regOf(t, host))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cands {
		got = append(got, c.Name)
	}
	if fmt.Sprint(got) != "[docker acr]" {
		t.Errorf("Candidates = %v, want [docker acr]", got)
	}
}
