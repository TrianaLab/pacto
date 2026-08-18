package evidenceoci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func TestPlainHTTP(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		insecure []string
		want     bool
	}{
		{"listed insecure", "registry.internal:5000", []string{"registry.internal:5000"}, true},
		{"loopback address", "127.0.0.1:5000", nil, true},
		{"loopback name", "localhost:5000", nil, true},
		{"public registry", "ghcr.io", nil, false},
		{"another host is listed", "ghcr.io", []string{"registry.internal:5000"}, false},
		{"unparseable host", "not a host", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := plainHTTP(tc.host, tc.insecure); got != tc.want {
				t.Errorf("plainHTTP(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestNewRepository_RejectsUnparseableSubject(t *testing.T) {
	if _, err := newRepository(Subject{Registry: "not a host", Repository: "team/orders"}, RepositoryOptions{}); err == nil {
		t.Fatal("opening a repository on an unparseable host succeeded")
	}
}

func TestCredentialFunc_WithoutAKeychainIsAnonymous(t *testing.T) {
	cred, err := credentialFunc(nil)(t.Context(), "ghcr.io")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cred != auth.EmptyCredential {
		t.Errorf("credential = %+v, want anonymous", cred)
	}
}

func TestCredentialFromConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  authn.AuthConfig
		want auth.Credential
	}{
		{"user and password", authn.AuthConfig{Username: "u", Password: "p"},
			auth.Credential{Username: "u", Password: "p"}},
		{"pacto login base64", authn.AuthConfig{Auth: base64.StdEncoding.EncodeToString([]byte("u:p"))},
			auth.Credential{Username: "u", Password: "p"}},
		{"password containing a colon", authn.AuthConfig{Auth: base64.StdEncoding.EncodeToString([]byte("u:a:b"))},
			auth.Credential{Username: "u", Password: "a:b"}},
		{"registry token", authn.AuthConfig{RegistryToken: "tok"}, auth.Credential{AccessToken: "tok"}},
		{"identity token", authn.AuthConfig{IdentityToken: "refresh"}, auth.Credential{RefreshToken: "refresh"}},
		{"explicit fields win over base64", authn.AuthConfig{Username: "u", Password: "p", Auth: base64.StdEncoding.EncodeToString([]byte("other:secret"))},
			auth.Credential{Username: "u", Password: "p"}},
		{"base64 without a separator", authn.AuthConfig{Auth: base64.StdEncoding.EncodeToString([]byte("nopassword"))}, auth.Credential{}},
		{"base64 that will not decode", authn.AuthConfig{Auth: "!!!not base64!!!"}, auth.Credential{}},
		{"nothing at all", authn.AuthConfig{}, auth.Credential{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := credentialFromConfig(tc.cfg); got != tc.want {
				t.Errorf("credentialFromConfig() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// failingKeychain and its authenticator stand in for a credential helper that is
// installed but broken: an unreadable Docker config, a helper binary that exits
// non-zero. A broken helper must surface, not silently downgrade to anonymous.
type failingKeychain struct {
	resolveErr error
	authErr    error
	nilConfig  bool
}

func (k failingKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	if k.resolveErr != nil {
		return nil, k.resolveErr
	}
	return failingAuthenticator(k), nil
}

type failingAuthenticator failingKeychain

func (a failingAuthenticator) Authorization() (*authn.AuthConfig, error) {
	if a.authErr != nil {
		return nil, a.authErr
	}
	if a.nilConfig {
		return nil, nil
	}
	return &authn.AuthConfig{Username: "u", Password: "p"}, nil
}

func TestCredentialFunc_SurfacesKeychainFailures(t *testing.T) {
	boom := errors.New("credential helper exited 1")
	cases := []struct {
		name     string
		kc       authn.Keychain
		hostport string
		wantErr  error
	}{
		{"unparseable host", failingKeychain{}, "not a host", nil},
		{"keychain cannot resolve", failingKeychain{resolveErr: boom}, "ghcr.io", boom},
		{"authenticator cannot authorize", failingKeychain{authErr: boom}, "ghcr.io", boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cred, err := credentialFunc(tc.kc)(t.Context(), tc.hostport)
			if err == nil {
				t.Fatalf("resolving %q succeeded, want a failure", tc.hostport)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.wantErr)
			}
			if cred != auth.EmptyCredential {
				t.Errorf("credential = %+v, want anonymous alongside the error", cred)
			}
		})
	}

	t.Run("authenticator returns no config", func(t *testing.T) {
		cred, err := credentialFunc(failingKeychain{nilConfig: true})(t.Context(), "ghcr.io")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if cred != auth.EmptyCredential {
			t.Errorf("credential = %+v, want anonymous", cred)
		}
	})
}

// authGate fronts the registry with a distribution-style challenge, so a scan
// only succeeds when Pacto's keychain produced the exact Authorization header a
// real registry would demand.
type authGate struct {
	inner  http.Handler
	scheme string
	want   string

	mu   sync.Mutex
	seen string
}

func (g *authGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got := r.Header.Get("Authorization")
	if got != "" {
		g.mu.Lock()
		g.seen = got
		g.mu.Unlock()
	}
	if got != g.want {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf("%s realm=%q,service=%q", g.scheme, "http://"+r.Host+"/token", "registry"))
		http.Error(w, `{"errors":[{"code":"UNAUTHORIZED"}]}`, http.StatusUnauthorized)
		return
	}
	g.inner.ServeHTTP(w, r)
}

func (g *authGate) lastSeen() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seen
}

func startGatedRegistry(t *testing.T, scheme, want string) (string, *authGate) {
	t.Helper()
	gate := &authGate{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), scheme: scheme, want: want}
	return serve(t, gate), gate
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// isolateCredentials points every ambient credential source at empty temporary
// directories, so a case only ever authenticates with the source it configures
// and never with the developer's own logins.
func isolateCredentials(t *testing.T) (xdg, docker string) {
	t.Helper()
	xdg, docker = t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("DOCKER_CONFIG", docker)
	return xdg, docker
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// Every credential source Pacto already supports for contract pulls must also
// reach the evidence registry, through the same keychain. Phase 10C adds no
// second login command, credential file or registry-auth model.
func TestScanSubject_AuthenticatesWithEveryPactoCredentialSource(t *testing.T) {
	cases := []struct {
		name   string
		scheme string
		header string
		// setup configures one credential source and returns the CLI credential
		// options that source needs, given the registry host.
		setup func(t *testing.T, host string) oci.CredentialOptions
	}{
		{
			name: "env user and password", scheme: "Basic", header: basicHeader("robot", "s3cret"),
			setup: func(t *testing.T, _ string) oci.CredentialOptions {
				return oci.CredentialOptions{Username: "robot", Password: "s3cret"}
			},
		},
		{
			name: "env token", scheme: "Bearer", header: "Bearer tok-abc",
			setup: func(t *testing.T, _ string) oci.CredentialOptions {
				return oci.CredentialOptions{Token: "tok-abc"}
			},
		},
		{
			name: "pacto login", scheme: "Basic", header: basicHeader("bob", "hunter2"),
			setup: func(t *testing.T, host string) oci.CredentialOptions {
				writeJSON(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "pacto", "config.json"), oci.PactoConfig{
					Auths: map[string]oci.PactoAuth{host: {Auth: base64.StdEncoding.EncodeToString([]byte("bob:hunter2"))}},
				})
				return oci.CredentialOptions{}
			},
		},
		{
			name: "docker config", scheme: "Basic", header: basicHeader("dock", "er-pass"),
			setup: func(t *testing.T, host string) oci.CredentialOptions {
				writeJSON(t, filepath.Join(os.Getenv("DOCKER_CONFIG"), "config.json"), map[string]any{
					"auths": map[string]any{host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("dock:er-pass"))}},
				})
				return oci.CredentialOptions{}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCredentials(t)
			host, gate := startGatedRegistry(t, tc.scheme, tc.header)
			opts := RepositoryOptions{Keychain: oci.NewKeychain(tc.setup(t, host))}

			subj, desc := seedContract(t, host, opts)
			repo := publishRecords(t, host, subj, desc, 1, opts)
			if got := mustScan(t, repo, subj, desc); len(got.Found) != 1 {
				t.Fatalf("read %d records, want 1", len(got.Found))
			}
			if gate.lastSeen() != tc.header {
				t.Errorf("registry saw Authorization %q, want %q", gate.lastSeen(), tc.header)
			}
		})
	}
}

// A registry that rejects the configured credential must fail the read closed,
// and the failure must be safe to log: it names neither the password nor the
// token that was tried.
func TestScanSubject_RejectedCredentialsFailClosedWithoutLeaking(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	isolateCredentials(t)
	host, _ := startGatedRegistry(t, "Basic", basicHeader("robot", "the-real-one"))
	good := RepositoryOptions{Keychain: oci.NewKeychain(oci.CredentialOptions{Username: "robot", Password: "the-real-one"})}
	subj, desc := seedContract(t, host, good)

	bad, err := newRepository(subj, RepositoryOptions{Keychain: oci.NewKeychain(oci.CredentialOptions{Username: "robot", Password: secret})})
	if err != nil {
		t.Fatalf("newRepository: %v", err)
	}
	_, err = scanSubject(context.Background(), bad, subj, desc)
	if err == nil {
		t.Fatal("scanning with a rejected credential succeeded; it must fail closed")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error names the secret that was tried: %v", err)
	}
}
