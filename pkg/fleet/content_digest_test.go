package fleet

import (
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
	a := contentDigest(mk("openapi: 3.0.0 # variant A"))
	b := contentDigest(mk("openapi: 3.0.0 # variant B"))
	if a == b {
		t.Fatalf("identical pacto.yaml with different openapi.yaml must differ: both %s", a)
	}
	// Two independent bundles with identical content hash identically (deterministic).
	same1, same2 := contentDigest(mk("same")), contentDigest(mk("same"))
	if same1 != same2 {
		t.Error("identical bundles must hash identically")
	}

	// FS-less (runtime-only) bundle falls back to contract + raw YAML.
	noFS := &contract.Bundle{Contract: c, RawYAML: raw}
	if d := contentDigest(noFS); d == "" || d == a {
		t.Errorf("FS-less digest should be a distinct non-empty value, got %q", d)
	}
	// Contract-only (no FS, no raw) still yields a stable digest.
	if contentDigest(&contract.Bundle{Contract: c}) == "" {
		t.Error("contract-only digest must be non-empty")
	}
}
