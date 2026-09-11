package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
)

func TestResolvePinned(t *testing.T) {
	bundle := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "auth", Version: "1.2.0"}}}

	t.Run("success", func(t *testing.T) {
		store := &testutil.MockBundleStore{
			ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return []string{"1.0.0", "1.2.0", "2.0.0"}, nil
			},
			ResolveFn: func(_ context.Context, ref string) (string, error) {
				return "sha256:" + ref, nil
			},
			PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) { return bundle, nil },
		}
		p, err := resolvePinned(context.Background(), store, "ghcr.io/acme/auth", "^1.0.0")
		if err != nil {
			t.Fatalf("resolvePinned: %v", err)
		}
		if p.Ref != "ghcr.io/acme/auth:1.2.0" {
			t.Errorf("resolvedRef = %q, want ghcr.io/acme/auth:1.2.0", p.Ref)
		}
		if p.Digest != "sha256:ghcr.io/acme/auth:1.2.0" {
			t.Errorf("digest = %q", p.Digest)
		}
		if p.Bundle != bundle {
			t.Error("the bundle the digest was taken from was not returned")
		}
	})

	// The whole point of the helper: the bytes it returns and the digest it
	// reports come from one call, so a store cannot hand back a digest for an
	// artifact it never downloaded.
	t.Run("the digest names the bytes that were fetched", func(t *testing.T) {
		var pinnedRef string
		store := &testutil.MockBundleStore{
			ResolveFn: func(_ context.Context, _ string) (string, error) { return "sha256:abc", nil },
			PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
				pinnedRef = ref
				return bundle, nil
			},
		}
		p, err := resolvePinned(context.Background(), store, "ghcr.io/acme/auth:1.2.0", "")
		if err != nil {
			t.Fatalf("resolvePinned: %v", err)
		}
		if pinnedRef != "ghcr.io/acme/auth@sha256:abc" {
			t.Errorf("pulled %q, want the digest-pinned reference", pinnedRef)
		}
		if p.Digest != "sha256:abc" {
			t.Errorf("digest = %q, want sha256:abc", p.Digest)
		}
	})

	t.Run("ResolveRef error", func(t *testing.T) {
		store := &testutil.MockBundleStore{
			ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return nil, fmt.Errorf("list tags failed")
			},
		}
		_, err := resolvePinned(context.Background(), store, "ghcr.io/acme/auth", "^1.0.0")
		if err == nil {
			t.Error("expected error when ListTags fails")
		}
	})

	// A registry that will not say what a tag points at still yields real bytes
	// under no claimed digest, so only a failed PULL fails the helper.
	t.Run("Pull error", func(t *testing.T) {
		store := &testutil.MockBundleStore{
			ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return []string{"1.0.0"}, nil
			},
			ResolveFn: func(_ context.Context, _ string) (string, error) {
				return "", fmt.Errorf("resolve digest failed")
			},
			PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
				return nil, fmt.Errorf("pull failed")
			},
		}
		_, err := resolvePinned(context.Background(), store, "ghcr.io/acme/auth", "^1.0.0")
		if err == nil {
			t.Error("expected error when Pull fails")
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
