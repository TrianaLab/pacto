package fleetsrc

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// A bundle repository holding write-once draft-<n> tags (a declaration waiting
// for a person) still contributes exactly one revision: the published 1.0.0.
// The drafts are pullable here, so a reader that took one would show it.
func TestDraftTagsNeverAFleetRevision(t *testing.T) {
	const repo = "reg.internal:5000/acme/invoices"
	dgst := validDigest("d")
	store := &fakeStore{
		bundles: map[string]*contract.Bundle{
			repo + ":1.0.0":   bundleFor("invoices"),
			repo + ":draft-1": bundleFor("invoices"),
			repo + ":draft-2": bundleFor("invoices"),
		},
		digest: map[string]string{
			repo + ":1.0.0":   dgst,
			repo + ":draft-1": validDigest("e"),
			repo + ":draft-2": validDigest("f"),
		},
		tags: map[string][]string{repo: {"draft-2", "1.0.0", "draft-1"}},
	}
	col, err := NewOCISource("oci", store, []string{"oci://" + repo}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %d (%+v), want 1", len(col.Revisions), col.Revisions)
	}
	if got := col.Revisions[0]; got.Digest != dgst || got.ResolvedRef != "oci://"+repo+"@"+dgst {
		t.Errorf("revision = %q/%q, want the published 1.0.0 pinned to %s", got.ResolvedRef, got.Digest, dgst)
	}
}
