package fleet

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// A revision's fallback content identity must cover the COMPLETE bundle, not just
// the parsed contract — two bundles with an identical pacto.yaml but different
// referenced files (OpenAPI, schema, skills, docs) must get DIFFERENT revision
// identities (review section 5).
func TestContentDigest_CoversReferencedFiles(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "svc", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "openapi.yaml"}},
	}
	raw := []byte("pactoVersion: \"2.0\"\nservice: {name: svc, version: \"1.0.0\"}\n")
	mk := func(openapi string) *contract.Bundle {
		return &contract.Bundle{
			Contract: c,
			RawYAML:  raw,
			FS: fstest.MapFS{
				"pacto.yaml":   &fstest.MapFile{Data: raw},
				"openapi.yaml": &fstest.MapFile{Data: []byte(openapi)},
			},
		}
	}
	dg := func(b *contract.Bundle) string {
		t.Helper()
		d, err := contentDigest(b)
		if err != nil {
			t.Fatalf("contentDigest: %v", err)
		}
		return d
	}
	a := dg(mk("openapi: 3.0.0 # variant A"))
	b := dg(mk("openapi: 3.0.0 # variant B"))
	if a == b {
		t.Fatalf("identical pacto.yaml with different openapi.yaml must differ: both %s", a)
	}
	// Two independent bundles with identical content hash identically (deterministic).
	if same1, same2 := dg(mk("same")), dg(mk("same")); same1 != same2 {
		t.Error("identical bundles must hash identically")
	}

	// FS-less (runtime-only) bundle falls back to contract + raw YAML.
	noFS := &contract.Bundle{Contract: c, RawYAML: raw}
	if d := dg(noFS); d == "" || d == a {
		t.Errorf("FS-less digest should be a distinct non-empty value, got %q", d)
	}
	// Contract-only (no FS, no raw) still yields a stable digest.
	if dg(&contract.Bundle{Contract: c}) == "" {
		t.Error("contract-only digest must be non-empty")
	}
}

type errFS struct{}

func (errFS) Open(string) (fs.File, error) { return nil, errors.New("fs read blocked") }

// A revision that pins an immutable digest takes that digest as its content
// identity, so two sources agreeing on the same digest never conflict on content
// even when their local filesystems differ (regenerated/cache artifacts) —
// review section S13.
func TestRevision_SameDigestDifferentFS_NoSpuriousConflict(t *testing.T) {
	mk := func(src, openapi string) Source {
		return NewMemorySource(src, "oci", &Collection{Revisions: []RawRevision{{
			Bundle: &contract.Bundle{
				Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "orders", Version: "1.0.0"}},
				RawYAML:  []byte("pactoVersion: \"2.0\"\nservice: {name: orders, version: \"1.0.0\"}\n"),
				FS:       fstest.MapFS{"openapi.yaml": {Data: []byte(openapi)}},
			},
			Domain: "d", ResolvedRef: "oci://reg/orders@sha256:X", Digest: "sha256:X",
		}}})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		mk("a", "# regenerated locally A"), mk("b", "# from the oci tar B"))
	if err != nil {
		t.Fatal(err)
	}
	if hasLim(snap.Limitations, LimitationRevisionConflict) {
		t.Errorf("agreement on the immutable digest must not produce a content conflict: %+v", snap.Limitations)
	}
	if len(snap.Revisions) != 1 {
		t.Errorf("both contributions share one revision key, want 1 revision, got %d", len(snap.Revisions))
	}
}

// A revision with no immutable digest whose bundle content cannot be hashed must
// be omitted with a limitation, never assigned a contract-only identity presented
// as collision-safe (review section S13).
func TestRevision_UnhashableNoDigest_OmittedWithLimitation(t *testing.T) {
	src := NewMemorySource("bad", "local", &Collection{Revisions: []RawRevision{{
		Bundle: &contract.Bundle{
			Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "1.0.0"}},
			FS:       errFS{},
		},
		Domain: "d", // no Digest -> must derive a content identity, but the FS fails
	}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 0 {
		t.Errorf("an unhashable, non-digest revision must be omitted, got %+v", snap.Revisions)
	}
	if !hasLim(snap.Limitations, LimitationRevisionUnresolved) {
		t.Errorf("expected a REVISION_IDENTITY_UNRESOLVED limitation, got %+v", snap.Limitations)
	}
}
