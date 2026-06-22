package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/v2/internal/testutil"
	"github.com/trianalab/pacto/v2/pkg/graph"
)

func TestResolveDigest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &testutil.MockBundleStore{
			ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return []string{"1.0.0", "1.2.0", "2.0.0"}, nil
			},
			ResolveFn: func(_ context.Context, ref string) (string, error) {
				return "sha256:" + ref, nil
			},
		}
		ref, digest, err := resolveDigest(context.Background(), store, "ghcr.io/acme/auth", "^1.0.0")
		if err != nil {
			t.Fatalf("resolveDigest: %v", err)
		}
		if ref != "ghcr.io/acme/auth:1.2.0" {
			t.Errorf("resolvedRef = %q, want ghcr.io/acme/auth:1.2.0", ref)
		}
		if digest != "sha256:ghcr.io/acme/auth:1.2.0" {
			t.Errorf("digest = %q", digest)
		}
	})

	t.Run("ResolveRef error", func(t *testing.T) {
		store := &testutil.MockBundleStore{
			ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return nil, fmt.Errorf("list tags failed")
			},
		}
		_, _, err := resolveDigest(context.Background(), store, "ghcr.io/acme/auth", "^1.0.0")
		if err == nil {
			t.Error("expected error when ListTags fails")
		}
	})

	t.Run("Resolve error", func(t *testing.T) {
		store := &testutil.MockBundleStore{
			ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return []string{"1.0.0"}, nil
			},
			ResolveFn: func(_ context.Context, ref string) (string, error) {
				return "", fmt.Errorf("resolve digest failed")
			},
		}
		_, _, err := resolveDigest(context.Background(), store, "ghcr.io/acme/auth", "^1.0.0")
		if err == nil {
			t.Error("expected error when Resolve fails")
		}
	})
}

func TestWalkClosure(t *testing.T) {
	t.Run("deduplication", func(t *testing.T) {
		shared := &graph.Node{Name: "shared", Version: "1.0.0", Ref: "oci://r/shared"}
		root := &graph.Node{
			Name: "root", Ref: "root",
			Dependencies: []graph.Edge{
				{Ref: "oci://r/a", Type: "dependency", Node: &graph.Node{Name: "a", Ref: "oci://r/a",
					Dependencies: []graph.Edge{{Ref: "oci://r/shared", Type: "dependency", Node: shared}}}},
				{Ref: "oci://r/shared", Type: "dependency", Node: shared},
			},
		}
		seen := map[string]int{}
		walkClosure(root, func(_ *graph.Node, _ graph.Edge, n *graph.Node) { seen[n.Ref]++ })
		if seen["oci://r/shared"] != 1 {
			t.Errorf("shared visited %d times, want 1", seen["oci://r/shared"])
		}
		if seen["oci://r/a"] != 1 {
			t.Errorf("a visited %d times, want 1", seen["oci://r/a"])
		}
	})

	t.Run("skip nil Node", func(t *testing.T) {
		root := &graph.Node{
			Name: "root", Ref: "root",
			Dependencies: []graph.Edge{
				{Ref: "oci://r/missing", Type: "dependency", Node: nil},
				{Ref: "oci://r/valid", Type: "dependency", Node: &graph.Node{Name: "valid", Ref: "oci://r/valid"}},
			},
		}
		seen := map[string]int{}
		walkClosure(root, func(_ *graph.Node, _ graph.Edge, n *graph.Node) { seen[n.Ref]++ })
		if seen["oci://r/valid"] != 1 {
			t.Errorf("valid visited %d times, want 1", seen["oci://r/valid"])
		}
		if len(seen) != 1 {
			t.Errorf("expected 1 node visited, got %d", len(seen))
		}
	})

	t.Run("fallback to Name when Ref empty", func(t *testing.T) {
		shared := &graph.Node{Name: "shared", Version: "1.0.0", Ref: ""}
		root := &graph.Node{
			Name: "root", Ref: "root",
			Dependencies: []graph.Edge{
				{Ref: "oci://r/a", Type: "dependency", Node: &graph.Node{Name: "a", Ref: "oci://r/a",
					Dependencies: []graph.Edge{{Ref: "local:shared", Type: "dependency", Node: shared}}}},
				{Ref: "local:shared", Type: "dependency", Node: shared},
			},
		}
		seen := map[string]int{}
		walkClosure(root, func(_ *graph.Node, _ graph.Edge, n *graph.Node) { seen[n.Name]++ })
		if seen["shared"] != 1 {
			t.Errorf("shared visited %d times, want 1", seen["shared"])
		}
		if seen["a"] != 1 {
			t.Errorf("a visited %d times, want 1", seen["a"])
		}
	})
}
