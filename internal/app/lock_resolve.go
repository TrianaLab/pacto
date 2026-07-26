package app

import (
	"context"

	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// resolveDigest resolves a bare OCI location under a constraint to a concrete
// repo:tag and its current manifest digest.
func resolveDigest(ctx context.Context, store oci.BundleStore, location, constraint string) (string, string, error) {
	resolvedRef, err := oci.ResolveRef(ctx, store, location, constraint)
	if err != nil {
		return "", "", err
	}
	digest, err := store.Resolve(ctx, resolvedRef)
	if err != nil {
		return "", "", err
	}
	return resolvedRef, digest, nil
}

// walkClosure visits each unique node in the resolved graph exactly once
// (deduping by Node.Ref), calling visit with the edge that introduced it.
// Edges with no resolved Node (errors/unresolved) are skipped by the caller.
func walkClosure(root *graph.Node, visit func(parent *graph.Node, e graph.Edge, n *graph.Node)) {
	seen := map[string]bool{}
	var rec func(parent *graph.Node)
	rec = func(parent *graph.Node) {
		for _, e := range parent.Dependencies {
			if e.Node == nil {
				continue
			}
			key := e.Node.Ref
			if key == "" {
				key = e.Node.Name
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			visit(parent, e, e.Node)
			rec(e.Node)
		}
	}
	rec(root)
}
