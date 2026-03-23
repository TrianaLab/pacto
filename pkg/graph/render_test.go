package graph

import (
	"strings"
	"testing"
)

func TestRenderTree_Nil(t *testing.T) {
	if got := RenderTree(nil); got != "" {
		t.Errorf("expected empty string for nil result, got %q", got)
	}
	if got := RenderTree(&Result{}); got != "" {
		t.Errorf("expected empty string for nil root, got %q", got)
	}
}

func TestRenderTree_NoDependencies(t *testing.T) {
	r := &Result{Root: &Node{Name: "svc", Version: "1.0.0"}}
	got := RenderTree(r)
	if got != "svc@1.0.0\n" {
		t.Errorf("expected 'svc@1.0.0\\n', got %q", got)
	}
}

func TestRenderTree_DirectDependencies(t *testing.T) {
	r := &Result{
		Root: &Node{
			Name:    "svc-a",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/svc-b:1.0.0", Node: &Node{Name: "svc-b", Version: "1.0.0"}},
				{Ref: "reg/svc-c:2.0.0", Node: &Node{Name: "svc-c", Version: "2.0.0"}},
			},
		},
	}
	got := RenderTree(r)
	mustContain := []string{
		"svc-a@1.0.0",
		"├─ svc-b@1.0.0",
		"└─ svc-c@2.0.0",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("expected %q in output:\n%s", s, got)
		}
	}
}

func TestRenderTree_TransitiveWithShared(t *testing.T) {
	// frontend -> backend -> postgres
	//                     -> keycloak -> postgres (shared)
	//          -> keycloak (shared)
	r := &Result{
		Root: &Node{
			Name:    "frontend",
			Version: "1.0.0",
			Dependencies: []Edge{
				{
					Ref: "reg/backend:1.0.0",
					Node: &Node{
						Name:    "backend",
						Version: "1.0.0",
						Dependencies: []Edge{
							{Ref: "reg/postgres:16.4.0", Node: &Node{Name: "postgres", Version: "16.4.0"}},
							{
								Ref: "reg/keycloak:26.0.0",
								Node: &Node{
									Name:    "keycloak",
									Version: "26.0.0",
									Dependencies: []Edge{
										{Ref: "reg/postgres:16.4.0", Shared: true, Node: &Node{Name: "postgres", Version: "16.4.0"}},
									},
								},
							},
						},
					},
				},
				{Ref: "reg/keycloak:26.0.0", Shared: true, Node: &Node{Name: "keycloak", Version: "26.0.0"}},
			},
		},
	}
	got := RenderTree(r)
	mustContain := []string{
		"frontend@1.0.0",
		"├─ backend@1.0.0",
		"│  ├─ postgres@16.4.0",
		"│  └─ keycloak@26.0.0",
		"│     └─ postgres@16.4.0 (shared)",
		"└─ keycloak@26.0.0 (shared)",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("expected %q in output:\n%s", s, got)
		}
	}
}

func TestRenderTree_ErrorEdge(t *testing.T) {
	r := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "ghcr.io/org/missing:1.0.0", Error: "not found"},
			},
		},
	}
	got := RenderTree(r)
	if !strings.Contains(got, "missing:1.0.0 (error: not found)") {
		t.Errorf("expected shortened ref with error, got:\n%s", got)
	}
}

func TestRenderTree_BareRef(t *testing.T) {
	r := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "ghcr.io/org/dep:1.0.0"},
			},
		},
	}
	got := RenderTree(r)
	if !strings.Contains(got, "└─ dep:1.0.0") {
		t.Errorf("expected shortened bare ref, got:\n%s", got)
	}
}

func TestRenderTree_CyclesAndConflicts(t *testing.T) {
	r := &Result{
		Root: &Node{Name: "svc", Version: "1.0.0"},
		Cycles: [][]string{
			{"svc", "dep-a", "dep-b", "dep-a"},
		},
		Conflicts: []Conflict{
			{Name: "dep-c", Versions: []string{"dep-c@1.0.0", "dep-c@2.0.0"}},
		},
	}
	got := RenderTree(r)
	if !strings.Contains(got, "Cycles (1):") {
		t.Errorf("expected cycles section, got:\n%s", got)
	}
	if !strings.Contains(got, "svc -> dep-a -> dep-b -> dep-a") {
		t.Errorf("expected cycle path, got:\n%s", got)
	}
	if !strings.Contains(got, "Conflicts (1):") {
		t.Errorf("expected conflicts section, got:\n%s", got)
	}
	if !strings.Contains(got, "dep-c: [dep-c@1.0.0 dep-c@2.0.0]") {
		t.Errorf("expected conflict details, got:\n%s", got)
	}
}

func TestRenderTree_LocalAnnotation(t *testing.T) {
	r := &Result{
		Root: &Node{
			Name:    "svc-a",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "../dep-svc", Local: true, Node: &Node{Name: "dep-svc", Version: "2.0.0", Local: true}},
				{Ref: "oci://reg/remote:1.0.0", Node: &Node{Name: "remote", Version: "1.0.0"}},
			},
		},
	}
	got := RenderTree(r)
	if !strings.Contains(got, "dep-svc@2.0.0 [local]") {
		t.Errorf("expected [local] annotation, got:\n%s", got)
	}
	if strings.Contains(got, "remote@1.0.0 [local]") {
		t.Errorf("remote should NOT have [local], got:\n%s", got)
	}
}

func TestRenderTree_ReferenceEdge(t *testing.T) {
	r := &Result{
		Root: &Node{
			Name:    "svc-a",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/svc-b:1.0.0", Node: &Node{Name: "svc-b", Version: "1.0.0"}},
				{Ref: "oci://registry.io/config:2.0.0", Type: EdgeReference},
				{Ref: "oci://registry.io/policy:3.0.0", Type: EdgeReference},
			},
		},
	}
	got := RenderTree(r)
	mustContain := []string{
		"svc-a@1.0.0",
		"├─ svc-b@1.0.0",
		"├─ config:2.0.0 [ref]",
		"└─ policy:3.0.0 [ref]",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("expected %q in output:\n%s", s, got)
		}
	}
	// Reference edges should not render children or other annotations
	if strings.Contains(got, "(shared)") {
		t.Errorf("reference edges should not show (shared):\n%s", got)
	}
}

func TestShortRef(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ghcr.io/org/svc:1.0.0", "svc:1.0.0"},
		{"ghcr.io/org/svc@sha256:abc123def456789", "svc@sha256:abc123d"},
		{"ghcr.io/org/svc@sha256:short", "svc@sha256:short"},
		{"simple-ref", "simple-ref"},
		{"registry.io/a/b/deep:2.0.0", "deep:2.0.0"},
	}
	for _, tt := range tests {
		got := ShortRef(tt.input)
		if got != tt.want {
			t.Errorf("ShortRef(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
