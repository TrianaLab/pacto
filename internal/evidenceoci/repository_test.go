package evidenceoci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

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
// reach the evidence registry, through the same keychain. Evidence-in-the-registry
// adds no second login command, credential file or registry-auth model.
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
