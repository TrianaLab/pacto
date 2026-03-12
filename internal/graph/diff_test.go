package graph

import (
	"testing"
)

func TestDiffGraphs_NilInputs(t *testing.T) {
	d := DiffGraphs(nil, nil)
	if len(d.Changes) != 0 {
		t.Errorf("expected no changes, got %d", len(d.Changes))
	}
}

func TestDiffGraphs_AddedDependencies(t *testing.T) {
	old := &Result{
		Root: &Node{Name: "svc", Version: "1.0.0"},
	}
	new := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/redis:7.2.0", Node: &Node{Name: "redis", Version: "7.2.0"}},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(d.Changes))
	}
	c := d.Changes[0]
	if c.Name != "redis" || c.ChangeType != AddedNode || c.NewVersion != "7.2.0" {
		t.Errorf("unexpected change: %+v", c)
	}
}

func TestDiffGraphs_RemovedDependencies(t *testing.T) {
	old := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/redis:7.2.0", Node: &Node{Name: "redis", Version: "7.2.0"}},
			},
		},
	}
	new := &Result{
		Root: &Node{Name: "svc", Version: "1.0.0"},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(d.Changes))
	}
	c := d.Changes[0]
	if c.Name != "redis" || c.ChangeType != RemovedNode || c.OldVersion != "7.2.0" {
		t.Errorf("unexpected change: %+v", c)
	}
}

func TestDiffGraphs_VersionChanged(t *testing.T) {
	old := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/postgres:16.4.0", Node: &Node{Name: "postgres", Version: "16.4.0"}},
			},
		},
	}
	new := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/postgres:17.0.0", Node: &Node{Name: "postgres", Version: "17.0.0"}},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(d.Changes))
	}
	c := d.Changes[0]
	if c.ChangeType != VersionChanged || c.OldVersion != "16.4.0" || c.NewVersion != "17.0.0" {
		t.Errorf("unexpected change: %+v", c)
	}
}

func TestDiffGraphs_NoChanges(t *testing.T) {
	graph := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/postgres:16.4.0", Node: &Node{Name: "postgres", Version: "16.4.0"}},
			},
		},
	}

	d := DiffGraphs(graph, graph)

	if len(d.Changes) != 0 {
		t.Errorf("expected no changes, got %d", len(d.Changes))
	}
}

func TestDiffGraphs_TransitiveChanges(t *testing.T) {
	old := &Result{
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
							{Ref: "reg/keycloak:26.0.0", Node: &Node{Name: "keycloak", Version: "26.0.0"}},
						},
					},
				},
				{Ref: "reg/keycloak:26.0.0", Shared: true, Node: &Node{Name: "keycloak", Version: "26.0.0"}},
			},
		},
	}
	new := &Result{
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
							{Ref: "reg/postgres:17.0.0", Node: &Node{Name: "postgres", Version: "17.0.0"}},
							{Ref: "reg/keycloak:26.1.0", Node: &Node{Name: "keycloak", Version: "26.1.0"}},
							{Ref: "reg/redis:7.2.0", Node: &Node{Name: "redis", Version: "7.2.0"}},
						},
					},
				},
				{Ref: "reg/keycloak:26.1.0", Shared: true, Node: &Node{Name: "keycloak", Version: "26.1.0"}},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(d.Changes), d.Changes)
	}

	changeMap := map[string]GraphChange{}
	for _, c := range d.Changes {
		changeMap[c.Name] = c
	}

	if c, ok := changeMap["keycloak"]; !ok || c.ChangeType != VersionChanged {
		t.Errorf("expected keycloak version change, got %+v", c)
	}
	if c, ok := changeMap["postgres"]; !ok || c.ChangeType != VersionChanged {
		t.Errorf("expected postgres version change, got %+v", c)
	}
	if c, ok := changeMap["redis"]; !ok || c.ChangeType != AddedNode {
		t.Errorf("expected redis added, got %+v", c)
	}
}

func TestDiffGraphs_SharedDependencies(t *testing.T) {
	old := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{
					Ref: "reg/a:1.0.0",
					Node: &Node{
						Name:    "a",
						Version: "1.0.0",
						Dependencies: []Edge{
							{Ref: "reg/shared:1.0.0", Node: &Node{Name: "shared", Version: "1.0.0"}},
						},
					},
				},
				{Ref: "reg/shared:1.0.0", Shared: true, Node: &Node{Name: "shared", Version: "1.0.0"}},
			},
		},
	}
	new := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{
					Ref: "reg/a:1.0.0",
					Node: &Node{
						Name:    "a",
						Version: "1.0.0",
						Dependencies: []Edge{
							{Ref: "reg/shared:2.0.0", Node: &Node{Name: "shared", Version: "2.0.0"}},
						},
					},
				},
				{Ref: "reg/shared:2.0.0", Shared: true, Node: &Node{Name: "shared", Version: "2.0.0"}},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(d.Changes))
	}
	if d.Changes[0].Name != "shared" || d.Changes[0].ChangeType != VersionChanged {
		t.Errorf("unexpected change: %+v", d.Changes[0])
	}
}

func TestDiffGraphs_NilEdgeNodes(t *testing.T) {
	old := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/missing:1.0.0", Node: nil, Error: "not found"},
			},
		},
	}
	new := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/missing:1.0.0", Node: nil, Error: "not found"},
			},
		},
	}

	d := DiffGraphs(old, new)
	if len(d.Changes) != 0 {
		t.Errorf("expected no changes for nil edge nodes, got %d", len(d.Changes))
	}
}

func TestDiffGraphs_OldNilNewHasDeps(t *testing.T) {
	new := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/redis:7.2.0", Node: &Node{Name: "redis", Version: "7.2.0"}},
			},
		},
	}

	d := DiffGraphs(nil, new)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(d.Changes))
	}
	if d.Changes[0].ChangeType != AddedNode {
		t.Errorf("expected added, got %s", d.Changes[0].ChangeType)
	}
	if d.Root.Name != "svc" {
		t.Errorf("expected root name 'svc', got %q", d.Root.Name)
	}
}

func TestDiffGraphs_OldHasDepsNewNil(t *testing.T) {
	old := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/redis:7.2.0", Node: &Node{Name: "redis", Version: "7.2.0"}},
			},
		},
	}

	d := DiffGraphs(old, nil)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(d.Changes))
	}
	if d.Changes[0].ChangeType != RemovedNode {
		t.Errorf("expected removed, got %s", d.Changes[0].ChangeType)
	}
	if d.Root.Name != "svc" {
		t.Errorf("expected root name 'svc', got %q", d.Root.Name)
	}
}

func TestDiffGraphs_ChangesSorted(t *testing.T) {
	old := &Result{Root: &Node{Name: "svc", Version: "1.0.0"}}
	new := &Result{
		Root: &Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []Edge{
				{Ref: "reg/zebra:1.0.0", Node: &Node{Name: "zebra", Version: "1.0.0"}},
				{Ref: "reg/alpha:1.0.0", Node: &Node{Name: "alpha", Version: "1.0.0"}},
				{Ref: "reg/mid:1.0.0", Node: &Node{Name: "mid", Version: "1.0.0"}},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(d.Changes))
	}
	if d.Changes[0].Name != "alpha" || d.Changes[1].Name != "mid" || d.Changes[2].Name != "zebra" {
		t.Errorf("expected sorted changes, got %s, %s, %s",
			d.Changes[0].Name, d.Changes[1].Name, d.Changes[2].Name)
	}
}

func TestMarkAll_NilEdgeNode(t *testing.T) {
	root := &Node{
		Name:    "svc",
		Version: "1.0.0",
		Dependencies: []Edge{
			{Ref: "reg/missing:1.0.0", Node: nil, Error: "not found"},
		},
	}
	dn := markAll(root, RemovedNode, map[string]bool{})
	if len(dn.Children) != 0 {
		t.Errorf("expected 0 children for nil edge node, got %d", len(dn.Children))
	}
}

func TestMarkAll_SharedEdges(t *testing.T) {
	root := &Node{
		Name:    "svc",
		Version: "1.0.0",
		Dependencies: []Edge{
			{
				Ref: "reg/a:1.0.0",
				Node: &Node{
					Name:    "a",
					Version: "1.0.0",
					Dependencies: []Edge{
						{Ref: "reg/b:2.0.0", Node: &Node{Name: "b", Version: "2.0.0"}},
					},
				},
			},
			{Ref: "reg/b:2.0.0", Shared: true, Node: &Node{Name: "b", Version: "2.0.0"}},
		},
	}

	dn := markAll(root, RemovedNode, map[string]bool{})

	if len(dn.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(dn.Children))
	}
	// First child should have sub-children (not shared)
	if len(dn.Children[0].Children) != 1 {
		t.Errorf("expected 1 sub-child for 'a', got %d", len(dn.Children[0].Children))
	}
	// Second child is shared, no sub-children
	if len(dn.Children[1].Children) != 0 {
		t.Errorf("expected 0 sub-children for shared 'b', got %d", len(dn.Children[1].Children))
	}
}

func TestDiffGraphs_DirectDepRemovedStillTransitive(t *testing.T) {
	// frontend depends on backend and keycloak directly.
	// backend also depends on keycloak transitively.
	// frontend-new removes the direct keycloak dependency.
	// keycloak is still reachable via backend, but should show as removed
	// at the root level.
	old := &Result{
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
							{Ref: "reg/keycloak:26.0.0", Node: &Node{Name: "keycloak", Version: "26.0.0"}},
						},
					},
				},
				{Ref: "reg/keycloak:26.0.0", Shared: true, Node: &Node{Name: "keycloak", Version: "26.0.0"}},
			},
		},
	}
	new := &Result{
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
							{Ref: "reg/keycloak:26.0.0", Node: &Node{Name: "keycloak", Version: "26.0.0"}},
						},
					},
				},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change (keycloak removed as direct dep), got %d: %+v", len(d.Changes), d.Changes)
	}
	if d.Changes[0].Name != "keycloak" || d.Changes[0].ChangeType != RemovedNode {
		t.Errorf("expected keycloak removed, got %+v", d.Changes[0])
	}
}

func TestChildMap_NilNode(t *testing.T) {
	m := childMap(nil)
	if len(m) != 0 {
		t.Errorf("expected empty map for nil node, got %v", m)
	}
}

// TestDiffGraphs_SharedOldNodeNoPhantomChanges reproduces a bug where
// non-deterministic concurrent resolution causes phantom dependency
// additions. When the old graph resolves a node (e.g. keycloak) fully
// under one parent but as shared (shallow, no Dependencies) under
// another, and the new graph resolves it fully under the second parent,
// diffTrees would recurse into the shallow old copy and see all its
// children as "added" because childMap returns empty for shallow nodes.
func TestDiffGraphs_SharedOldNodeNoPhantomChanges(t *testing.T) {
	// Old graph: keycloak fully resolved under svc-a, shared (shallow)
	// under svc-b. This simulates svc-a winning the concurrent fetch.
	old := &Result{
		Root: &Node{
			Name:    "runtime",
			Version: "1.0.0",
			Dependencies: []Edge{
				{
					Ref: "reg/svc-a:1.0.0",
					Node: &Node{
						Name:    "svc-a",
						Version: "1.0.0",
						Dependencies: []Edge{
							{
								Ref: "reg/keycloak:26.0.0",
								Node: &Node{
									Name:    "keycloak",
									Version: "26.0.0",
									Dependencies: []Edge{
										{Ref: "reg/postgres:17.0.0", Node: &Node{Name: "postgres", Version: "17.0.0"}},
									},
								},
							},
							{Ref: "reg/postgres:17.0.0", Shared: true, Node: &Node{Name: "postgres", Version: "17.0.0"}},
						},
					},
				},
				{
					Ref: "reg/svc-b:1.0.0",
					Node: &Node{
						Name:    "svc-b",
						Version: "1.0.0",
						Dependencies: []Edge{
							// Shared: keycloak was already resolved under svc-a.
							// Shallow copy — no Dependencies.
							{Ref: "reg/keycloak:26.0.0", Shared: true, Node: &Node{Name: "keycloak", Version: "26.0.0"}},
							{Ref: "reg/postgres:17.0.0", Shared: true, Node: &Node{Name: "postgres", Version: "17.0.0"}},
						},
					},
				},
			},
		},
	}

	// New graph: keycloak fully resolved under svc-b (different
	// goroutine won), shared under svc-a. Identical dependency
	// structure — only the resolution order differs.
	new := &Result{
		Root: &Node{
			Name:    "runtime",
			Version: "1.0.0",
			Dependencies: []Edge{
				{
					Ref: "reg/svc-a:1.0.0",
					Node: &Node{
						Name:    "svc-a",
						Version: "1.0.0",
						Dependencies: []Edge{
							// Shared: keycloak resolved under svc-b this time.
							{Ref: "reg/keycloak:26.0.0", Shared: true, Node: &Node{Name: "keycloak", Version: "26.0.0"}},
							{Ref: "reg/postgres:17.0.0", Shared: true, Node: &Node{Name: "postgres", Version: "17.0.0"}},
						},
					},
				},
				{
					Ref: "reg/svc-b:1.0.0",
					Node: &Node{
						Name:    "svc-b",
						Version: "1.0.0",
						Dependencies: []Edge{
							{
								Ref: "reg/keycloak:26.0.0",
								Node: &Node{
									Name:    "keycloak",
									Version: "26.0.0",
									Dependencies: []Edge{
										{Ref: "reg/postgres:17.0.0", Node: &Node{Name: "postgres", Version: "17.0.0"}},
									},
								},
							},
							{Ref: "reg/postgres:17.0.0", Shared: true, Node: &Node{Name: "postgres", Version: "17.0.0"}},
						},
					},
				},
			},
		},
	}

	d := DiffGraphs(old, new)

	if len(d.Changes) != 0 {
		t.Errorf("expected no changes (identical graphs with different resolution order), got %d: %+v",
			len(d.Changes), d.Changes)
		t.Logf("rendered diff tree:\n%s", RenderDiffTree(d))
	}
}
