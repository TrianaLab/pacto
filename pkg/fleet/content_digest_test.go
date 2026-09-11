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
	if hasLim(snap.Limitations, LimitationRevisionDocConflict) || hasLim(snap.Limitations, LimitationRevisionLockConflict) {
		t.Errorf("agreement on the immutable digest must not produce a conflict: %+v", snap.Limitations)
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
	// A dropped record is a record-level problem, so the source that dropped it is
	// partial. Reporting it available would tell a reader the source had been read
	// completely while the snapshot is silently missing one of its revisions.
	if len(snap.Sources) != 1 || snap.Sources[0].Status != SourcePartial {
		t.Errorf("a source whose revision was dropped must be partial, got %+v", snap.Sources)
	}
}

// REVISION_IDENTITY_UNRESOLVED is emitted on both the drop path and the success
// path, and only one of them is a record problem. A derived content digest means
// the revision was read whole and is queryable; it just has no registry identity.
// Calling that partial would put an "incomplete knowledge" envelope on every
// answer drawn from a disk cache written before ref sidecars existed, where
// nothing is missing at all.
func TestRevision_DerivedContentDigest_KeepsSourceAvailable(t *testing.T) {
	src := NewMemorySource("cache", "oci", &Collection{Revisions: []RawRevision{{
		Bundle: &contract.Bundle{
			Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "1.0.0"}},
			FS:       fstest.MapFS{},
		},
		Domain: "d", // no Digest -> a content identity is derived and the revision is kept
	}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 1 {
		t.Fatalf("a hashable digest-less revision must be kept, got %+v", snap.Revisions)
	}
	if !hasLim(snap.Limitations, LimitationRevisionUnresolved) {
		t.Errorf("the derived identity must still be disclosed, got %+v", snap.Limitations)
	}
	if len(snap.Sources) != 1 || snap.Sources[0].Status != SourceAvailable {
		t.Errorf("nothing was dropped, so the source stays available, got %+v", snap.Sources)
	}
	if snap.Completeness != CompletenessComplete {
		t.Errorf("every revision was read completely, want complete, got %q", snap.Completeness)
	}
	if hasLim(snap.Limitations, LimitationSourcePartial) {
		t.Errorf("no source returned a partial result: %+v", snap.Limitations)
	}
}

// A source that declares its own state does not get to out-rank the limitations it
// shipped with that state: a self-declared "available" alongside a collection-level
// limitation is still partial (finding 9).
func TestSourceState_DeclaredAvailableWithLimitations_IsPartial(t *testing.T) {
	src := NewMemorySource("declared", "k8s", &Collection{
		State:       &SourceState{ID: "declared", Kind: "k8s", Status: SourceAvailable},
		Limitations: []Limitation{{Code: LimitationSourcePartial, Source: "declared", Message: "listing was truncated"}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sources) != 1 || snap.Sources[0].Status != SourcePartial {
		t.Errorf("declared available + collection limitations must be partial, got %+v", snap.Sources)
	}
}
