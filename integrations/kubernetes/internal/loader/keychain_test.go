/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/credentials"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// countingKeychain answers every resource with fixed credentials and counts the calls.
type countingKeychain struct{ calls *int }

func (c countingKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	*c.calls++
	return authn.FromConfig(authn.AuthConfig{Username: "installation", Password: "install"}), nil
}

func TestLoaderUsesGivenKeychain(t *testing.T) {
	reg := newRecordingRegistry(t, true)
	reg.push(t, "acme/pg:16.1.0")
	l := New(WithKeychain(staticKeychain{auth: &authn.AuthConfig{Username: "given", Password: "kc"}}))

	if _, err := l.Load(t.Context(), "oci://"+reg.host+"/acme/pg:16.1.0", "", nil); err != nil {
		t.Fatal(err)
	}
	auths, _ := reg.seen()
	if !strings.Contains(strings.Join(auths, "\n"), basic("given", "kc")) {
		t.Errorf("the given keychain was never sent; saw %q", auths)
	}
}

// The tag listing and the configuration-ref resolver of a ref outside the push prefixes never
// reach the installation source, even on the installation registry's host.
func TestRangeResolverUsesNoInstallationCredentialOutsidePrefix(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, false)
	reg.push(t, "acme/pg:16.1.0")
	calls := 0
	kc := oci.NewKeychain(oci.CredentialOptions{SkipCloud: true}).Prepend("installation", credentials.PrefixKeychain{
		Prefixes: []string{reg.host + "/acme/pacto-targets"},
		Inner:    countingKeychain{calls: &calls},
	})
	l := New(WithKeychain(kc))

	tags, err := l.ListTags(t.Context(), "oci://"+reg.host+"/acme/pg", nil)
	if err != nil || len(tags) != 1 {
		t.Fatalf("ListTags = %v, %v", tags, err)
	}
	r := l.ConfigRefResolver("oci://"+reg.host+"/acme/app:1.0.0", nil, "")
	if _, err := r.ResolveBundle(t.Context(), "oci://"+reg.host+"/acme/pg:16.1.0"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("installation source calls = %d, want 0", calls)
	}
}

// TestLoaderKeychainIsWhatItPullsWith: a Pacto's pull credentials answer
// first, the loader's own keychain behind them.
func TestLoaderKeychainIsWhatItPullsWith(t *testing.T) {
	own := staticKeychain{auth: &authn.AuthConfig{Username: "own"}}
	l := New(WithKeychain(own))
	if l.Keychain(nil) != authn.Keychain(own) {
		t.Fatal("no pull credentials: the loader's own keychain")
	}
	a, err := l.Keychain(&authn.AuthConfig{Username: "cr"}).Resolve(name.MustParseReference("r.example/app:1").Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := a.Authorization(); cfg.Username != "cr" {
		t.Fatalf("the Pacto's credentials answer first: %+v", cfg)
	}
}
