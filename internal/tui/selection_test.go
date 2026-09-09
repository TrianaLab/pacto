package tui

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestBundleRef(t *testing.T) {
	tests := []struct {
		name      string
		id        fleet.RevisionIdentity
		wantRef   string
		wantLocal bool
	}{
		// IdentityClass is deliberately left unset throughout: bundleRef must not
		// consult it, and setting it here would let a class-keyed implementation
		// pass while failing on every revision fleet actually builds.
		{
			"a local bundle becomes a plain path",
			fleet.RevisionIdentity{RequestedRef: "file:///tmp/svc"},
			"/tmp/svc",
			true,
		},
		{
			"a local bundle with no scheme is passed through",
			fleet.RevisionIdentity{RequestedRef: "/tmp/svc"},
			"/tmp/svc",
			false,
		},
		{
			"a registry revision uses the resolved ref",
			fleet.RevisionIdentity{RequestedRef: "oci://r/s:1", ResolvedRef: "oci://r/s@sha256:aa"},
			"oci://r/s@sha256:aa",
			false,
		},
		{
			"a registry revision with no resolved ref falls back to the requested one",
			fleet.RevisionIdentity{RequestedRef: "oci://r/s:latest"},
			"oci://r/s:latest",
			false,
		},
		{"nothing to go on yields nothing", fleet.RevisionIdentity{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRef, gotLocal := bundleRef(tt.id)
			if gotRef != tt.wantRef {
				t.Fatalf("bundleRef ref = %q, want %q", gotRef, tt.wantRef)
			}
			if gotLocal != tt.wantLocal {
				t.Fatalf("bundleRef local = %v, want %v", gotLocal, tt.wantLocal)
			}
		})
	}
}

func TestResolveSelectionForARevision(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Ref == "" {
		t.Fatal("a revision selection resolved to no ref")
	}
}

func TestResolveSelectionForARevisionWithEmptyVersionFallback(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	ref.Version = ""
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Version == "" {
		t.Fatal("revision selection should populate Version from detail when EntityRef.Version is empty")
	}
}

func TestResolveSelectionForAServiceGoesThroughItsActiveRevision(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Ref == "" {
		t.Fatal("a service selection resolved to no ref")
	}
	if sel.Kind != fleet.KindService {
		t.Fatalf("Kind = %q, want the original service kind", sel.Kind)
	}
}

func TestResolveSelectionForAnOwnerHasNoRef(t *testing.T) {
	// An owner is not a bundle. Resolution must succeed and say so rather than
	// inventing a ref, so the verb layer can grey out the bundle verbs instead
	// of running one against nonsense.
	c := newLoadedContext(t)
	sel, err := resolveSelection(c, fleet.EntityRef{Kind: fleet.KindOwner, Key: "team:x", Label: "team:x"})
	if err != nil {
		t.Fatalf("owner resolution errored: %v", err)
	}
	if sel.Ref != "" {
		t.Fatalf("Ref = %q, want empty for an owner", sel.Ref)
	}
}

func TestResolveSelectionPropagatesALookupError(t *testing.T) {
	c := newLoadedContext(t)
	if _, err := resolveSelection(c, fleet.EntityRef{Kind: fleet.KindService, Key: "no-such"}); err == nil {
		t.Fatal("want an error for an unknown key")
	}
}

func TestResolveSelectionForASource(t *testing.T) {
	c := newLoadedContext(t)
	sel, err := resolveSelection(c, fleet.EntityRef{Kind: fleet.KindSource, Key: "local", Label: "local"})
	if err != nil {
		t.Fatalf("source resolution errored: %v", err)
	}
	if sel.Ref != "" {
		t.Fatalf("Ref = %q, want empty for a source", sel.Ref)
	}
}

func TestResolveSelectionStripsTheSchemeFromARealLocalRevision(t *testing.T) {
	// The unit table above can only assert what a hand-built identity does. This
	// asserts what the fleet actually produces: internal/fleetsrc records a local
	// bundle as "file://<dir>" and never sets a resolved ref, so the identity comes
	// back IdentityNoRef. A ref that still carries the scheme is one no verb can
	// stat.
	c := newLoadedContext(t)
	sel, err := resolveSelection(c, fleet.EntityRef{Kind: fleet.KindService, Key: testServiceName})
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(sel.Ref, "file://") {
		t.Fatalf("Ref = %q, want the bare directory", sel.Ref)
	}
	if sel.Ref == "" {
		t.Fatal("the local fixture service resolved to no ref at all")
	}
}

func TestResolveSelectionForTarget(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindTarget)
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Ref == "" {
		t.Fatal("a target selection resolved to no ref")
	}
	if sel.Kind != fleet.KindTarget {
		t.Fatalf("Kind = %q, want target", sel.Kind)
	}
}

func TestRefFromServiceRevisionsWithEmptyList(t *testing.T) {
	c := newLoadedContext(t)
	s := &fleet.ServiceDetailData{ActiveRevisions: fleet.RefPreview{Total: 0}}
	if ref, _ := refFromServiceRevisions(c, s); ref != "" {
		t.Fatalf("refFromServiceRevisions with no active revisions = %q, want empty", ref)
	}
}

func TestRefFromServiceRevisionsWithNoItems(t *testing.T) {
	c := newLoadedContext(t)
	s := &fleet.ServiceDetailData{ActiveRevisions: fleet.RefPreview{Total: 1, Items: nil}}
	if ref, _ := refFromServiceRevisions(c, s); ref != "" {
		t.Fatalf("refFromServiceRevisions with nil items = %q, want empty", ref)
	}
}

func TestRefFromServiceRevisionsWithBadRevisionKey(t *testing.T) {
	c := newLoadedContext(t)
	s := &fleet.ServiceDetailData{
		ActiveRevisions: fleet.RefPreview{
			Total: 1,
			Items: []fleet.EntityRef{{Key: "no-such-revision"}},
		},
	}
	if ref, _ := refFromServiceRevisions(c, s); ref != "" {
		t.Fatalf("refFromServiceRevisions with bad key = %q, want empty", ref)
	}
}

func TestRefFromTargetWithNoRevision(t *testing.T) {
	c := newLoadedContext(t)
	tgt := &fleet.TargetDetailData{Revision: nil}
	if ref, _ := refFromTarget(c, tgt); ref != "" {
		t.Fatalf("refFromTarget with no revision = %q, want empty", ref)
	}
}

func TestRefFromTargetWithBadRevisionKey(t *testing.T) {
	c := newLoadedContext(t)
	tgt := &fleet.TargetDetailData{Revision: &fleet.EntityRef{Key: "no-such-revision"}}
	if ref, _ := refFromTarget(c, tgt); ref != "" {
		t.Fatalf("refFromTarget with bad key = %q, want empty", ref)
	}
}
