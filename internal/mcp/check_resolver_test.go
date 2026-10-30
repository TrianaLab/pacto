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

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// pullCounter is a registry stub that records every ref it is asked for and
// answers with the same bundle, or the same error.
type pullCounter struct {
	bundle *contract.Bundle
	err    error
	refs   []string
}

// validator wires the stub registry behind the real `pacto validate` path, the
// one the command layer hands NewServer.
func (p *pullCounter) validator() Validator {
	return app.NewService(&testutil.MockBundleStore{
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			p.refs = append(p.refs, ref)
			return p.bundle, p.err
		},
	}, nil).ValidationReport
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
		r := &pullCounter{err: errors.New("not found")}
		res, err := Check(context.Background(), r.validator(), dir)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Valid {
			t.Error("valid = true, want false for an unresolvable policy ref")
		}
		assertCode(t, res, "POLICY_REF_UNRESOLVED")
		if len(r.refs) == 0 || !strings.Contains(r.refs[0], "ghcr.io/acme/platform-policy:1.0.0") {
			t.Errorf("registry saw %v, want the declared ref", r.refs)
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
		r := &pullCounter{bundle: &contract.Bundle{FS: fstest.MapFS{
			"policy/schema.json": {Data: []byte(`{"required":["metadata"]}`)},
		}}}
		res, err := Check(context.Background(), r.validator(), dir)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Valid {
			t.Error("valid = true, want false: the resolved policy requires a section the contract lacks")
		}
		assertCode(t, res, "POLICY_VIOLATION")
	})
}

// TestCheckTool_UsesTheWiredValidator proves the validator reaches the tool, not
// just the function: NewServer's parameter is what closes the gap.
func TestCheckTool_UsesTheWiredValidator(t *testing.T) {
	dir := bundleWithPolicyRef(t)
	r := &pullCounter{err: errors.New("registry unreachable")}
	res := callToolOn(t, NewServer(r.validator(), "test"), "pacto_check", map[string]any{"path": dir})
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
		t.Error("the wired validator never reached the registry")
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

// TestCheck_VerifiesTheLockfile pins the reason Check takes the whole validate
// path: with a resolver alone a corrupt or drifted pacto.lock passed pacto_check
// and then failed `pacto validate` in CI.
func TestCheck_VerifiesTheLockfile(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	if err := os.WriteFile(filepath.Join(dir, "pacto.lock"), []byte("not a lockfile"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(context.Background(), app.NewService(nil, nil).ValidationReport, dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Valid {
		t.Fatal("valid = true, want false for an unverifiable pacto.lock")
	}
	// The same stages and next step `pacto validate` reports.
	if len(res.Stages) != 9 || res.Stages[1].Name != "lock" || res.Stages[1].Status != validation.StageFail || !strings.HasPrefix(res.Next, "fix ") {
		t.Errorf("stages = %+v, next = %q", res.Stages, res.Next)
	}
}

// TestCheck_LocatesErrors: pacto_check carries each error's line and column.
func TestCheck_LocatesErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.0\"\nservice:\n  name: svc\n  version: 1.0.0\nworkload: servce\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(context.Background(), app.NewService(nil, nil).ValidationReport, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) == 0 || res.Errors[0].Line != 5 || res.Errors[0].Column != 1 {
		t.Errorf("errors = %+v", res.Errors)
	}
}

// TestCheck_ValidatorErrorIsReturned: a validation path that cannot reach a
// verdict is an error, never a pass.
func TestCheck_ValidatorErrorIsReturned(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	failing := func(context.Context, string) (validation.Report, error) {
		return validation.Report{}, errors.New("boom")
	}
	if _, err := Check(context.Background(), failing, dir); err == nil {
		t.Fatal("expected the validator error")
	}
}
