package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// pinned is ONE observation of a registry artifact: the concrete repo:tag a
// constraint selected, the bundle that answered and the manifest digest of the
// artifact those exact bytes came from.
type pinned struct {
	Ref    string
	Bundle *contract.Bundle
	Digest string
}

// resolvePinned resolves a bare OCI location under a constraint and reads the
// bundle and its digest as one observation.
//
// Asking a store what a tag points at and separately downloading that tag are
// two observations of a MUTABLE name, and against a cached store they are not
// even racing: Resolve always reaches the registry while Pull may serve a
// generation cached under repo:tag, so a warm cache plus a re-pushed tag pairs
// the new digest with the old bundle deterministically. A lock built that way
// records an identity for bytes nobody read, and `pacto lock --check` then
// certifies it clean. [oci.PullPinned] binds the two, so what the lock and the
// catalog record is what they actually read.
func resolvePinned(ctx context.Context, store oci.BundleStore, location, constraint string) (pinned, error) {
	ref, err := oci.ResolveRef(ctx, store, location, constraint)
	if err != nil {
		return pinned{}, err
	}
	b, digest, err := oci.PullPinned(ctx, store, ref)
	if err != nil {
		return pinned{}, err
	}
	if digest == "" {
		// [oci.PullPinned] reports real bytes under no claimed digest when the
		// registry would not say what the tag points at -- the honest answer for a
		// caller that only wants content. Both callers here are pinning something
		// permanent, so an unknown identity is a failure, not an empty field.
		return pinned{}, fmt.Errorf("the registry would not say what %s points at", ref)
	}
	return pinned{Ref: ref, Bundle: b, Digest: digest}, nil
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
