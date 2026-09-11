package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// TestRequireDetailRefusesANilDetail pins A17. Query.EntityDetail returns
// jsonClone(d) and jsonClone swallows both of its errors, so (nil, nil) is a
// representable answer and all three call sites dereference the result on the
// next line. A nil that reaches them takes the whole session down.
func TestRequireDetailRefusesANilDetail(t *testing.T) {
	if _, err := requireDetail(nil, nil); err == nil {
		t.Fatal("requireDetail(nil, nil) returned no error; the callers would dereference the nil")
	}
	if _, err := requireDetail(nil, errBoom); !errors.Is(err, errBoom) {
		t.Fatalf("requireDetail(nil, errBoom) = %v, want the caller's own error unwrapped", err)
	}
	want := &fleet.EntityDetail{}
	got, err := requireDetail(want, nil)
	if err != nil || got != want {
		t.Fatalf("requireDetail(det, nil) = %v, %v; want the detail through untouched", got, err)
	}
}

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
			// A21. Trimming the scheme is what turns a path into an option: the
			// bare remainder reaches argv, and generate takes -o, --set and -f.
			// The ref comes from a CR status (internal/fleetsrc/k8s.go:84), so it
			// is attacker-shaped input, and ./ keeps the meaning while making it
			// unreadable as a flag.
			"a local path that looks like a flag is kept a path",
			fleet.RevisionIdentity{RequestedRef: "file://-o/tmp/evil"},
			"./-o/tmp/evil",
			true,
		},
		{
			"a single leading dash is enough to trigger it",
			fleet.RevisionIdentity{ResolvedRef: "file://--set=x"},
			"./--set=x",
			true,
		},
		{
			// bundleRef does not sniff the shape of the string. A bare path that
			// no source emits is treated as a registry reference and fails saying
			// so, which beats reading a directory off a guess.
			"a scheme-less path is not called local and is not special-cased",
			fleet.RevisionIdentity{RequestedRef: "/tmp/svc"},
			"oci:///tmp/svc",
			false,
		},
		{
			// The regression this flag exists for. internal/fleetsrc/oci.go:179
			// leaves ResolvedRef scheme-less unless a digest was recorded, and
			// internal/fleetsrc/k8s.go:84 passes the operator's ref through as it
			// found it. Reading locality off the absence of an oci:// prefix would
			// call this local and offer to run plugin binaries against a registry.
			// The scheme goes on here because graph.ParseDependencyRef resolves a
			// scheme-less ref as a filesystem path (pkg/graph/depref.go:59), so
			// every verb downstream would go looking for a directory by that name.
			"a scheme-less registry reference is remote and gains its scheme",
			fleet.RevisionIdentity{ResolvedRef: "ghcr.io/acme/svc:1.0"},
			"oci://ghcr.io/acme/svc:1.0",
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

// TestResolveSelectionKeepsTheVersionTheRowCarries is the other half of the
// fallback above: the detail's version is a default for a row that has none,
// never an override. The fixture's two agree, so only a row that disagrees can
// tell the guard from an unconditional assignment -- and the row is what the
// reader is looking at when they press the key.
func TestResolveSelectionKeepsTheVersionTheRowCarries(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindRevision)
	ref.Version = "9.9.9"
	sel, err := resolveSelection(c, ref)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Version != "9.9.9" {
		t.Fatalf("Version = %q, want the row's own 9.9.9", sel.Version)
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

// TestResolveSelectionCarriesLocalityOffEveryPath is the counterpart to the
// unit table above, which can only assert what a hand-built identity does. This
// asserts what the fleet actually produces, over each of the three ways a
// selection reaches a bundle: a revision directly, a service through its active
// revision and a target through the revision it runs.
//
// Both halves matter. internal/fleetsrc records a local bundle as "file://<dir>"
// and never sets a resolved ref, so a ref that still carries the scheme is one
// no verb can stat. And Local is what every local-only verb keys on, so a path
// that drops it silently hides push, generate and both lock verbs on the one
// selection they are for.
func TestResolveSelectionCarriesLocalityOffEveryPath(t *testing.T) {
	c := newLoadedContext(t)
	for _, tt := range []struct {
		name      string
		ref       fleet.EntityRef
		wantLocal bool
	}{
		{
			"the local revision directly",
			fleet.EntityRef{Kind: fleet.KindRevision, Key: revisionKeyOf(t, c, testServiceName)},
			true,
		},
		{
			"the local service, through its active revision",
			fleet.EntityRef{Kind: fleet.KindService, Key: testServiceName},
			true,
		},
		{
			"the local target, through the revision it runs",
			fleet.EntityRef{Kind: fleet.KindTarget, Key: "default/Deployment/test-svc-deploy"},
			true,
		},
		{
			"the registry revision",
			fleet.EntityRef{Kind: fleet.KindRevision, Key: revisionKeyOf(t, c, "another-svc")},
			false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sel, err := resolveSelection(c, tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			if sel.Ref == "" {
				t.Fatal("resolved to no ref at all")
			}
			if strings.HasPrefix(sel.Ref, "file://") {
				t.Fatalf("Ref = %q, want the bare directory", sel.Ref)
			}
			if sel.Local != tt.wantLocal {
				t.Fatalf("Ref = %q came back Local=%v, want %v", sel.Ref, sel.Local, tt.wantLocal)
			}
		})
	}
}

// revisionKeyOf returns the fixture's revision key for a service. The key
// carries a digest, and spelling it out at each call site would make the test
// about the fixture's constants rather than about resolution.
func revisionKeyOf(t *testing.T, c *Context, service string) string {
	t.Helper()
	list, err := c.Query.Entities(fleet.EntityFilter{
		Kinds: []fleet.EntityKind{fleet.KindRevision}, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Entities {
		if strings.HasPrefix(e.Key, service+"@") {
			return e.Key
		}
	}
	t.Fatalf("the fixture has no revision of %s", service)
	return ""
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

// TestRefFromServiceRevisionsTakesTheFirstActiveRevision pins which end of the
// list is read, which the fixture services cannot: they have one active
// revision each, so Items[0] and Items[len-1] are the same row. Two revisions
// of different locality tell them apart, and locality is what decides whether
// the local-only verbs are offered at all.
func TestRefFromServiceRevisionsTakesTheFirstActiveRevision(t *testing.T) {
	c := newLoadedContext(t)
	s := &fleet.ServiceDetailData{
		ActiveRevisions: fleet.RefPreview{
			Total: 2,
			Items: []fleet.EntityRef{
				{Kind: fleet.KindRevision, Key: revisionKeyOf(t, c, testServiceName)},
				{Kind: fleet.KindRevision, Key: revisionKeyOf(t, c, "another-svc")},
			},
		},
	}
	wantRef, wantLocal := refFromRevisionKey(c, s.ActiveRevisions.Items[0].Key)
	if !wantLocal {
		t.Fatalf("test setup: %s is meant to be the local revision", testServiceName)
	}
	ref, local := refFromServiceRevisions(c, s)
	if ref != wantRef || local != wantLocal {
		t.Fatalf("refFromServiceRevisions = (%q, %v), want the first item's (%q, %v)",
			ref, local, wantRef, wantLocal)
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
