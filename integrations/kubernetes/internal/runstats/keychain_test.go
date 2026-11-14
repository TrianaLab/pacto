/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runstats

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

// fallbackKC answers every host with the fallback user.
type fallbackKC struct{}

func (fallbackKC) Resolve(authn.Resource) (authn.Authenticator, error) {
	return authn.FromConfig(authn.AuthConfig{Username: "fallback"}), nil
}

func configDir(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func resolve(t *testing.T, kc authn.Keychain, host string) authn.AuthConfig {
	t.Helper()
	reg, err := name.NewRegistry(host)
	if err != nil {
		t.Fatal(err)
	}
	a, err := kc.Resolve(reg)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	return *cfg
}

func TestKeychainReadsAuthsFromDir(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("bot:s3cret"))
	kc := Keychain(configDir(t, `{"auths":{
		"https://ghcr.io/v1/":{"auth":"`+auth+`"},
		"registry.example.com":{"username":"ci","password":"pw"}}}`), fallbackKC{})
	if got := resolve(t, kc, "ghcr.io"); got.Username != "bot" || got.Password != "s3cret" {
		t.Fatalf("ghcr.io = %+v", got)
	}
	if got := resolve(t, kc, "registry.example.com"); got.Username != "ci" || got.Password != "pw" {
		t.Fatalf("registry.example.com = %+v", got)
	}
}

func TestKeychainIdentityToken(t *testing.T) {
	kc := Keychain(configDir(t, `{"auths":{"registry.example.com":{"identitytoken":"tok"}}}`), fallbackKC{})
	if got := resolve(t, kc, "registry.example.com"); got != (authn.AuthConfig{IdentityToken: "tok"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestKeychainMalformedJSONFallsBack(t *testing.T) {
	kc := Keychain(configDir(t, `{"auths":`), fallbackKC{})
	if got := resolve(t, kc, "registry.example.com"); got.Username != "fallback" {
		t.Fatalf("got %+v", got)
	}
}

func TestKeychainBadAuthFallsBack(t *testing.T) {
	noColon := base64.StdEncoding.EncodeToString([]byte("nocolon"))
	kc := Keychain(configDir(t, `{"auths":{"a.example.com":{"auth":"%%%"},"b.example.com":{"auth":"`+noColon+`"}}}`), fallbackKC{})
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if got := resolve(t, kc, host); got.Username != "fallback" {
			t.Fatalf("%s = %+v", host, got)
		}
	}
}

func TestKeychainFallsBackForUnlistedHost(t *testing.T) {
	kc := Keychain(configDir(t, `{"auths":{"ghcr.io":{"username":"bot","password":"pw"}}}`), fallbackKC{})
	if got := resolve(t, kc, "registry.example.com"); got.Username != "fallback" {
		t.Fatalf("got %+v", got)
	}
}

func TestKeychainMissingConfigFallsBack(t *testing.T) {
	kc := Keychain(t.TempDir(), fallbackKC{})
	if got := resolve(t, kc, "ghcr.io"); got.Username != "fallback" {
		t.Fatalf("got %+v", got)
	}
}

func TestKeychainEmptyDirIsFallback(t *testing.T) {
	if _, ok := Keychain("", fallbackKC{}).(fallbackKC); !ok {
		t.Fatal("an empty dir must return the fallback itself")
	}
}
