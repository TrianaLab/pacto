package ocireferrer

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"oras.land/oras-go/v2/registry/remote/auth"
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

func TestOpen_RejectsUnparseablePath(t *testing.T) {
	if _, err := Open("not a host/team/orders", Options{}); err == nil {
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

func TestCredentialFunc_ResolvesTheKeychain(t *testing.T) {
	cred, err := credentialFunc(failingKeychain{})(t.Context(), "ghcr.io")
	if err != nil || cred.Username != "u" || cred.Password != "p" {
		t.Fatalf("credential = %+v, %v", cred, err)
	}
}
