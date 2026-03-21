package graph

import (
	"strings"
	"testing"
)

func TestRenderDiffTree_Nil(t *testing.T) {
	if got := RenderDiffTree(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
}

func TestRenderDiffTree_NoChanges(t *testing.T) {
	d := &GraphDiff{Root: DiffNode{Name: "svc"}}
	if got := RenderDiffTree(d); got != "" {
		t.Errorf("expected empty string for no changes, got %q", got)
	}
}

func TestRenderDiffTree_VersionChange(t *testing.T) {
	d := &GraphDiff{
		Root: DiffNode{
			Name: "frontend",
			Children: []DiffNode{
				{
					Name:    "backend",
					Version: "1.0.0",
					Children: []DiffNode{
						{
							Name:    "postgres",
							Version: "17.0.0",
							Change:  &GraphChange{Name: "postgres", ChangeType: VersionChanged, OldVersion: "16.4.0", NewVersion: "17.0.0"},
						},
					},
				},
			},
		},
		Changes: []GraphChange{
			{Name: "postgres", ChangeType: VersionChanged, OldVersion: "16.4.0", NewVersion: "17.0.0"},
		},
	}

	got := RenderDiffTree(d)

	mustContain := []string{
		"frontend",
		"└─ backend",
		"└─ postgres",
		"16.4.0 → 17.0.0",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("expected %q in output:\n%s", s, got)
		}
	}
}

func TestRenderDiffTree_AddedAndRemoved(t *testing.T) {
	d := &GraphDiff{
		Root: DiffNode{
			Name: "svc",
			Children: []DiffNode{
				{
					Name:    "redis",
					Version: "7.2.0",
					Change:  &GraphChange{Name: "redis", ChangeType: AddedNode, NewVersion: "7.2.0"},
				},
				{
					Name:    "elastic",
					Version: "8.11.0",
					Change:  &GraphChange{Name: "elastic", ChangeType: RemovedNode, OldVersion: "8.11.0"},
				},
			},
		},
		Changes: []GraphChange{
			{Name: "redis", ChangeType: AddedNode, NewVersion: "7.2.0"},
			{Name: "elastic", ChangeType: RemovedNode, OldVersion: "8.11.0"},
		},
	}

	got := RenderDiffTree(d)

	if !strings.Contains(got, "+7.2.0") {
		t.Errorf("expected '+7.2.0' for added dep, got:\n%s", got)
	}
	if !strings.Contains(got, "-8.11.0") {
		t.Errorf("expected '-8.11.0' for removed dep, got:\n%s", got)
	}
}

func TestRenderDiffTree_FiltersUnchangedBranches(t *testing.T) {
	d := &GraphDiff{
		Root: DiffNode{
			Name: "svc",
			Children: []DiffNode{
				{Name: "unchanged", Version: "1.0.0"},
				{
					Name:    "changed",
					Version: "2.0.0",
					Change:  &GraphChange{Name: "changed", ChangeType: VersionChanged, OldVersion: "1.0.0", NewVersion: "2.0.0"},
				},
			},
		},
		Changes: []GraphChange{
			{Name: "changed", ChangeType: VersionChanged, OldVersion: "1.0.0", NewVersion: "2.0.0"},
		},
	}

	got := RenderDiffTree(d)

	if strings.Contains(got, "unchanged") {
		t.Errorf("did not expect 'unchanged' in output:\n%s", got)
	}
	if !strings.Contains(got, "changed") {
		t.Errorf("expected 'changed' in output:\n%s", got)
	}
}

func TestRenderDiffTree_TransitiveTree(t *testing.T) {
	d := &GraphDiff{
		Root: DiffNode{
			Name: "frontend",
			Children: []DiffNode{
				{
					Name:    "backend",
					Version: "1.0.0",
					Children: []DiffNode{
						{
							Name:    "postgres",
							Version: "17.0.0",
							Change:  &GraphChange{Name: "postgres", ChangeType: VersionChanged, OldVersion: "16.4.0", NewVersion: "17.0.0"},
						},
						{
							Name:    "keycloak",
							Version: "26.1.0",
							Change:  &GraphChange{Name: "keycloak", ChangeType: VersionChanged, OldVersion: "26.0.0", NewVersion: "26.1.0"},
						},
						{
							Name:    "redis",
							Version: "7.2.0",
							Change:  &GraphChange{Name: "redis", ChangeType: AddedNode, NewVersion: "7.2.0"},
						},
					},
				},
				{
					Name:    "keycloak",
					Version: "26.1.0",
					Change:  &GraphChange{Name: "keycloak", ChangeType: VersionChanged, OldVersion: "26.0.0", NewVersion: "26.1.0"},
				},
			},
		},
		Changes: []GraphChange{
			{Name: "keycloak", ChangeType: VersionChanged, OldVersion: "26.0.0", NewVersion: "26.1.0"},
			{Name: "postgres", ChangeType: VersionChanged, OldVersion: "16.4.0", NewVersion: "17.0.0"},
			{Name: "redis", ChangeType: AddedNode, NewVersion: "7.2.0"},
		},
	}

	got := RenderDiffTree(d)

	mustContain := []string{
		"frontend",
		"├─ backend",
		"│  ├─ postgres",
		"16.4.0 → 17.0.0",
		"│  ├─ keycloak",
		"26.0.0 → 26.1.0",
		"│  └─ redis",
		"+7.2.0",
		"└─ keycloak",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("expected %q in output:\n%s", s, got)
		}
	}
}

func TestRenderDiffTree_DeepUnchangedParent(t *testing.T) {
	// Parent has no change itself but child does — parent should appear.
	d := &GraphDiff{
		Root: DiffNode{
			Name: "svc",
			Children: []DiffNode{
				{
					Name:    "middleware",
					Version: "1.0.0",
					Children: []DiffNode{
						{
							Name:    "db",
							Version: "2.0.0",
							Change:  &GraphChange{Name: "db", ChangeType: VersionChanged, OldVersion: "1.0.0", NewVersion: "2.0.0"},
						},
					},
				},
			},
		},
		Changes: []GraphChange{
			{Name: "db", ChangeType: VersionChanged, OldVersion: "1.0.0", NewVersion: "2.0.0"},
		},
	}

	got := RenderDiffTree(d)

	if !strings.Contains(got, "middleware") {
		t.Errorf("expected unchanged parent 'middleware' in output:\n%s", got)
	}
	if !strings.Contains(got, "db") {
		t.Errorf("expected changed 'db' in output:\n%s", got)
	}
}

func TestHasChanges(t *testing.T) {
	t.Run("no change", func(t *testing.T) {
		if hasChanges(DiffNode{Name: "a"}) {
			t.Error("expected false")
		}
	})
	t.Run("direct change", func(t *testing.T) {
		if !hasChanges(DiffNode{Name: "a", Change: &GraphChange{}}) {
			t.Error("expected true")
		}
	})
	t.Run("child change", func(t *testing.T) {
		n := DiffNode{
			Name: "a",
			Children: []DiffNode{
				{Name: "b", Change: &GraphChange{}},
			},
		}
		if !hasChanges(n) {
			t.Error("expected true for child change")
		}
	})
}

func TestFormatDiffLabel_UnknownType(t *testing.T) {
	n := DiffNode{Name: "x", Change: &GraphChange{ChangeType: "unknown"}}
	got := formatDiffLabel(n)
	if got != "x" {
		t.Errorf("expected 'x', got %q", got)
	}
}
