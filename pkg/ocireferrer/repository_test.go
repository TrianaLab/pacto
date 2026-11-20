package ocireferrer

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
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

// The default posture (no fallback) is pinned by TestNoTagFallback.
func TestOpenAllowsFallbackWhenAsked(t *testing.T) {
	_, path := open(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ := Open(path, Options{AllowTagFallback: true})
	ctx := t.Context()
	anchor := Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	rec := Build("application/x-rec", "application/x-layer", []byte("r"), &anchor.ManifestDesc, nil)
	for _, art := range []Artifact{anchor, rec} {
		if err := Push(ctx, repo, art); err != nil {
			t.Fatal(err)
		}
	}
	fresh, _ := Open(path, Options{AllowTagFallback: true})
	var listed []ocispec.Descriptor
	if err := fresh.Referrers(ctx, anchor.ManifestDesc, "", func(p []ocispec.Descriptor) error { listed = append(listed, p...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Digest != rec.ManifestDesc.Digest {
		t.Fatalf("referrers through the tag fallback = %+v", listed)
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

// scopedRepo opens path under a scope rooted at the test registry's /team, plus
// an unguarded view of the same repository to inspect what was written.
func scopedRepo(t *testing.T, repoName string) (guarded, plain *remote.Repository) {
	t.Helper()
	srv, _ := open(t, testutil.ReferrersOptions{})
	host := strings.TrimPrefix(srv.URL, "http://")
	scope := oci.NewPushScope(host + "/team")
	guarded, err := Open(host+"/team/"+repoName, Options{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	plain, _ = Open(host+"/team/"+repoName, Options{})
	return guarded, plain
}

func TestOpenScopeRefusesTagOutsideScope(t *testing.T) {
	repo, plain := scopedRepo(t, "app")
	ctx := t.Context()
	art := Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	if err := Push(ctx, repo, art); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("subject-less push outside: err = %v, want ErrOutsidePushScope", err)
	}
	if err := Push(ctx, plain, art); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(ctx, art.ManifestDesc, "v1"); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("tag outside: err = %v, want ErrOutsidePushScope", err)
	}
	if _, err := plain.Resolve(ctx, "v1"); err == nil {
		t.Fatal("the registry holds a tag the scope refused")
	}
}

func TestOpenScopeAllowsReferrerOnSubject(t *testing.T) {
	repo, plain := scopedRepo(t, "app")
	ctx := t.Context()
	anchor := Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	if err := Push(ctx, plain, anchor); err != nil {
		t.Fatal(err)
	}
	rec := Build("application/x-rec", "application/x-layer", []byte("r"), &anchor.ManifestDesc, nil)
	if err := Push(ctx, repo, rec); err != nil {
		t.Fatalf("referrer outside the scope: %v", err)
	}
}

func TestOpenScopeAllowsTagInsideScope(t *testing.T) {
	repo, _ := scopedRepo(t, oci.TargetsRepository)
	ctx := t.Context()
	art := Build("application/x-snapshot", "application/x-layer", []byte("s"), nil, nil)
	if err := Push(ctx, repo, art); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(ctx, art.ManifestDesc, "latest"); err != nil {
		t.Fatalf("tag inside the scope: %v", err)
	}
}

func TestOpenNilScopeUnchanged(t *testing.T) {
	_, path := open(t, testutil.ReferrersOptions{})
	repo, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if repo.Client.(*auth.Client).Client != retry.DefaultClient {
		t.Fatal("an unscoped repository no longer uses the default retry client")
	}
	art := Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	if err := Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(t.Context(), art.ManifestDesc, "v1"); err != nil {
		t.Fatalf("unscoped tag: %v", err)
	}
}
