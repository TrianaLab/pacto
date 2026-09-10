package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
)

// stubPolicyResolver answers every ref with the same bundle, or the same error.
type stubPolicyResolver struct {
	bundle *contract.Bundle
	err    error
	refs   []string
}

func (r *stubPolicyResolver) ResolveBundle(_ context.Context, ref string) (*contract.Bundle, error) {
	r.refs = append(r.refs, ref)
	return r.bundle, r.err
}

// bundleWithPolicyRef writes a contract whose only policy is a ref, which is the
// one construct the two validators disagree about.
func bundleWithPolicyRef(t *testing.T) string {
	t.Helper()
	dir := testutil.WriteTestBundle(t)
	yaml := string(testutil.ValidPactoYAML()) + `policies:
  - name: platform
    ref: oci://ghcr.io/acme/platform-policy:1.0.0
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCheck_RefPolicyIsResolved pins finding 18. pacto_check used to run the
// local-only validator, which downgrades an unresolvable policies[].ref to a
// warning, so an agent looping until "valid" stopped on a contract `pacto
// validate` and CI both reject.
func TestCheck_RefPolicyIsResolved(t *testing.T) {
	dir := bundleWithPolicyRef(t)

	t.Run("a ref the resolver cannot fetch is invalid", func(t *testing.T) {
		r := &stubPolicyResolver{err: errors.New("not found")}
		res, err := Check(context.Background(), r, dir)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Valid {
			t.Error("valid = true, want false for an unresolvable policy ref")
		}
		assertCode(t, res, "POLICY_REF_UNRESOLVED")
		if len(r.refs) != 1 || r.refs[0] != "oci://ghcr.io/acme/platform-policy:1.0.0" {
			t.Errorf("resolver saw %v, want the declared ref exactly once", r.refs)
		}
	})

	t.Run("no resolver fails closed", func(t *testing.T) {
		res, err := Check(context.Background(), nil, dir)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Valid {
			t.Error("valid = true, want false: an unenforced policy is not a passing one")
		}
		assertCode(t, res, "POLICY_REF_UNRESOLVED")
	})

	t.Run("a resolvable ref is enforced, not skipped", func(t *testing.T) {
		r := &stubPolicyResolver{bundle: &contract.Bundle{FS: fstest.MapFS{
			"policy/schema.json": {Data: []byte(`{"required":["metadata"]}`)},
		}}}
		res, err := Check(context.Background(), r, dir)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Valid {
			t.Error("valid = true, want false: the resolved policy requires a section the contract lacks")
		}
		assertCode(t, res, "POLICY_VIOLATION")
	})
}

// TestCheckTool_UsesTheWiredResolver proves the resolver reaches the tool, not
// just the function: NewServer's parameter is what closes the gap.
func TestCheckTool_UsesTheWiredResolver(t *testing.T) {
	dir := bundleWithPolicyRef(t)
	r := &stubPolicyResolver{err: errors.New("registry unreachable")}
	res := callToolOn(t, NewServer(r, "test"), "pacto_check", map[string]any{"path": dir})
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	var parsed CheckResult
	if err := json.Unmarshal([]byte(resultText(t, res)), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if parsed.Valid {
		t.Error("valid = true, want false for an unresolvable policy ref")
	}
	if len(r.refs) == 0 {
		t.Error("the wired resolver was never consulted")
	}
}

func assertCode(t *testing.T, res *CheckResult, want string) {
	t.Helper()
	for _, e := range res.Errors {
		if e.Code == want {
			return
		}
	}
	t.Errorf("errors = %+v, want one coded %s", res.Errors, want)
}

// TestCheck_ResolverIsNotNeededWithoutARefPolicy keeps the offline path honest:
// a contract with no ref-based policy validates the same with or without one.
func TestCheck_ResolverIsNotNeededWithoutARefPolicy(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	res, err := Check(context.Background(), nil, dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Valid {
		t.Errorf("valid = false, want true: %+v", res.Errors)
	}
	if strings.Contains(resultString(res), "POLICY") {
		t.Errorf("unexpected policy diagnostics: %+v", res)
	}
}

func resultString(res *CheckResult) string {
	data, _ := json.Marshal(res)
	return string(data)
}
