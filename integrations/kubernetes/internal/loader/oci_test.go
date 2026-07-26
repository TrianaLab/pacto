/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/v2/pkg/oci"
)

func TestStaticKeychain_ReturnsCredentials(t *testing.T) {
	kc := staticKeychain{
		auth: &authn.AuthConfig{Username: "user", Password: "pass"},
	}

	reg, _ := name.NewRegistry("ghcr.io")
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, _ := auth.Authorization()
	if cfg.Username != "user" {
		t.Fatalf("expected user, got %s", cfg.Username)
	}
}

func TestEffectiveKeychain_NoOverride(t *testing.T) {
	puller := &OCIPuller{keychain: authn.DefaultKeychain}
	kc := puller.effectiveKeychain(nil)
	if kc != authn.DefaultKeychain {
		t.Fatal("expected default keychain when no override")
	}
}

func TestEffectiveKeychain_WithOverride(t *testing.T) {
	puller := &OCIPuller{keychain: authn.DefaultKeychain}
	override := &authn.AuthConfig{Username: "override", Password: "secret"}
	kc := puller.effectiveKeychain(override)
	if kc == authn.DefaultKeychain {
		t.Fatal("expected a different keychain when override is provided")
	}
	// The override should be tried first via MultiKeychain
	reg, _ := name.NewRegistry("ghcr.io")
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, _ := auth.Authorization()
	if cfg.Username != "override" {
		t.Fatalf("expected override user, got %s", cfg.Username)
	}
}

func TestLibrary_HasExplicitTag(t *testing.T) {
	tests := []struct {
		ref    string
		expect bool
	}{
		{"ghcr.io/org/svc:1.0.0", true},
		{"ghcr.io/org/svc", false},
		{"ghcr.io/org/svc@sha256:abc", true},
		{"registry:5000/org/svc", false},
		{"registry:5000/org/svc:1.0.0", true},
	}

	for _, tt := range tests {
		got := oci.HasExplicitTag(tt.ref)
		if got != tt.expect {
			t.Errorf("HasExplicitTag(%q) = %v, want %v", tt.ref, got, tt.expect)
		}
	}
}

func TestLibrary_BestTag(t *testing.T) {
	tags := []string{"1.0.0", "2.1.0", "1.5.0", "latest", "dev"}
	best, err := oci.BestTag(tags, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if best != "2.1.0" {
		t.Fatalf("expected 2.1.0, got %s", best)
	}
}

func TestLibrary_BestTag_NoSemver(t *testing.T) {
	tags := []string{"latest", "dev", "nightly"}
	_, err := oci.BestTag(tags, "")
	if err == nil {
		t.Fatal("expected error for no semver tags")
	}
}

func TestNormalizeOCIRef(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"oci://ghcr.io/org/svc", "ghcr.io/org/svc"},
		{"ghcr.io/org/svc", "ghcr.io/org/svc"},
		{"oci://ghcr.io/org/svc%3A1.0.0", "ghcr.io/org/svc:1.0.0"},
	}

	for _, tt := range tests {
		got := normalizeOCIRef(tt.input)
		if got != tt.expect {
			t.Errorf("normalizeOCIRef(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}
