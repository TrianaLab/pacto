package ci

import (
	"slices"
	"strings"
	"testing"
)

func TestResolveRegistryPrecedence(t *testing.T) {
	gh := Context{Platform: "github", Owner: "Acme"}
	gl := Context{Platform: "gitlab", RegistryImage: "registry.gitlab.com/acme/repo"}
	imgs := []ImageRef{{Image: "ghcr.io/acme/payments:1.0", Source: "services/payments/chart/values.yaml:12"}}
	adv := Value{Value: "europe-docker.pkg.dev/acme/pacto", Source: "the installation advertised at k8s:pacto-system/pacto-detected"}
	for _, tc := range []struct {
		name, flag    string
		advertised    Value
		ctx           Context
		value, source string
	}{
		{"flag beats advertised", "r.example/acme", adv, gh, "r.example/acme", "--registry"},
		{"advertised beats GitHub", "", adv, gh, adv.Value, adv.Source},
		{"GitHub beats images", "", Value{}, gh, "ghcr.io/acme", "GITHUB_REPOSITORY_OWNER"},
		{"github.com server", "", Value{}, Context{Platform: "github", Owner: "acme", ServerURL: "https://github.com"}, "ghcr.io/acme", "GITHUB_REPOSITORY_OWNER"},
		{"GitLab beats images", "", Value{}, gl, "registry.gitlab.com/acme/repo", "CI_REGISTRY_IMAGE"},
		{"images", "", Value{}, Context{}, "ghcr.io/acme", "image references in services/payments/chart/values.yaml:12"},
	} {
		v, notes := ResolveRegistry(tc.flag, tc.advertised, tc.ctx, imgs)
		if v.Value != tc.value || v.Source != tc.source || len(notes) != 0 {
			t.Errorf("%s: got %+v %v", tc.name, v, notes)
		}
	}
}

func TestResolveRegistryGHESFallsThrough(t *testing.T) {
	v, notes := ResolveRegistry("", Value{}, Context{Platform: "github", Owner: "acme", ServerURL: "https://git.acme.internal"}, nil)
	if v.Value != "" || !slices.Equal(notes, []string{"GitHub Enterprise Server: no registry host to assume"}) {
		t.Fatalf("got %+v %v", v, notes)
	}
	v, _ = ResolveRegistry("", Value{}, Context{Platform: "github", Owner: "acme", ServerURL: "https://git.acme.internal"},
		[]ImageRef{{Image: "ghcr.io/acme/x", Source: "a/compose.yaml:3"}})
	if v.Value != "ghcr.io/acme" {
		t.Fatalf("GHES falls through to images: %+v", v)
	}
}

func TestResolveRegistryImagesDisagree(t *testing.T) {
	v, notes := ResolveRegistry("", Value{}, Context{}, []ImageRef{
		{Image: "ghcr.io/acme/payments:1.0", Source: "a"}, {Image: "ghcr.io/acme/ledger", Source: "b"}, {Image: "nginx:1.27", Source: "c"},
	})
	if v.Value != "" || !slices.Equal(notes, []string{"images name 2 registries (ghcr.io/acme, docker.io/library); pass --registry"}) {
		t.Fatalf("got %+v %v", v, notes)
	}
	v, notes = ResolveRegistry("", Value{}, Context{}, []ImageRef{{Image: "nginx:1.27", Source: "c"}})
	if v.Value != "" || len(notes) != 0 {
		t.Fatalf("Docker Hub library alone: %+v %v", v, notes)
	}
	if v, _ := ResolveRegistry("", Value{}, Context{}, nil); v.String() != "none" {
		t.Fatalf("no images: %+v", v)
	}
}

func TestNormalizeRegistry(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "ghcr.io/acme": "ghcr.io/acme", "oci://localhost:1/acme": "localhost:1/acme", "localhost:1/acme/": "localhost:1/acme",
	} {
		if got, err := NormalizeRegistry(in); err != nil || got != want {
			t.Errorf("NormalizeRegistry(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"https://user:tok@localhost:1/acme", "user:tok@localhost:1/acme", "ghcr.io/acme?x=1", "/acme", "oci://", "http://h/acme"} {
		_, err := NormalizeRegistry(in)
		if err == nil || strings.Contains(err.Error(), "tok") {
			t.Errorf("NormalizeRegistry(%q) error = %v, want a redacted refusal", in, err)
		}
	}
}
