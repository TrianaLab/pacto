package ci

import (
	"slices"
	"testing"
)

func TestResolveRegistryPrecedence(t *testing.T) {
	gh := Context{Platform: "github", Owner: "Acme"}
	gl := Context{Platform: "gitlab", RegistryImage: "registry.gitlab.com/acme/repo"}
	imgs := []ImageRef{{Image: "ghcr.io/acme/payments:1.0", Source: "services/payments/chart/values.yaml:12"}}
	for _, tc := range []struct {
		name, flag    string
		ctx           Context
		value, source string
	}{
		{"flag beats GitHub", "r.example/acme", gh, "r.example/acme", "--registry"},
		{"GitHub beats images", "", gh, "ghcr.io/acme", "GITHUB_REPOSITORY_OWNER"},
		{"github.com server", "", Context{Platform: "github", Owner: "acme", ServerURL: "https://github.com"}, "ghcr.io/acme", "GITHUB_REPOSITORY_OWNER"},
		{"GitLab beats images", "", gl, "registry.gitlab.com/acme/repo", "CI_REGISTRY_IMAGE"},
		{"images", "", Context{}, "ghcr.io/acme", "image references in services/payments/chart/values.yaml:12"},
	} {
		v, notes := ResolveRegistry(tc.flag, tc.ctx, imgs)
		if v.Value != tc.value || v.Source != tc.source || len(notes) != 0 {
			t.Errorf("%s: got %+v %v", tc.name, v, notes)
		}
	}
}

func TestResolveRegistryGHESFallsThrough(t *testing.T) {
	v, notes := ResolveRegistry("", Context{Platform: "github", Owner: "acme", ServerURL: "https://git.acme.internal"}, nil)
	if v.Value != "" || !slices.Equal(notes, []string{"GitHub Enterprise Server: no registry host to assume"}) {
		t.Fatalf("got %+v %v", v, notes)
	}
	v, _ = ResolveRegistry("", Context{Platform: "github", Owner: "acme", ServerURL: "https://git.acme.internal"},
		[]ImageRef{{Image: "ghcr.io/acme/x", Source: "a/compose.yaml:3"}})
	if v.Value != "ghcr.io/acme" {
		t.Fatalf("GHES falls through to images: %+v", v)
	}
}

func TestResolveRegistryImagesDisagree(t *testing.T) {
	v, notes := ResolveRegistry("", Context{}, []ImageRef{
		{Image: "ghcr.io/acme/payments:1.0", Source: "a"}, {Image: "ghcr.io/acme/ledger", Source: "b"}, {Image: "nginx:1.27", Source: "c"},
	})
	if v.Value != "" || !slices.Equal(notes, []string{"images name 2 registries (ghcr.io/acme, docker.io/library); pass --registry"}) {
		t.Fatalf("got %+v %v", v, notes)
	}
	v, notes = ResolveRegistry("", Context{}, []ImageRef{{Image: "nginx:1.27", Source: "c"}})
	if v.Value != "" || len(notes) != 0 {
		t.Fatalf("Docker Hub library alone: %+v %v", v, notes)
	}
	if v, _ := ResolveRegistry("", Context{}, nil); v.String() != "none" {
		t.Fatalf("no images: %+v", v)
	}
}
