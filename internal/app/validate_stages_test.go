package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// stageLine is each stage as "name:status", the order a run reports them in.
func stageLine(r *ValidateResult) string {
	var parts []string
	for _, st := range r.Stages {
		parts = append(parts, st.Name+":"+st.Status)
	}
	return strings.Join(parts, " ")
}

func TestValidateStages_Pass(t *testing.T) {
	dir := writeTestBundle(t)
	r, err := NewService(nil, nil).Validate(context.Background(), ValidateOptions{Path: dir, Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stageLine(r), "load:pass lock:skip structural:pass cross-field:pass policy:pass guarantees:skip readiness:pass closure:skip"; got != want {
		t.Errorf("stages = %s, want %s", got, want)
	}
	if r.Stages[7].Reason != "not requested (--closure)" || r.Stages[1].Reason != "no pacto.lock" || r.Stages[5].Reason != "no dependency declares requires" {
		t.Errorf("skip reasons = %q, %q, %q", r.Stages[7].Reason, r.Stages[1].Reason, r.Stages[5].Reason)
	}
	if r.File != filepath.Join(dir, "pacto.yaml") || r.Next != "pacto impact PUBLISHED_REF "+dir {
		t.Errorf("file = %q, next = %q", r.File, r.Next)
	}
}

// TestValidateStages_ParseErrorLocated: a contract the loader refuses fails
// the load stage at the line yaml names, the closest field is suggested, and
// every later stage is skipped.
func TestValidateStages_ParseErrorLocated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.0\"\nservice:\n  name: svc\n  version: 1.0.0\nworkload: service\nstate:\n  type: stateless\n  persistance: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewService(nil, nil).Validate(context.Background(), ValidateOptions{Path: dir, Closure: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stageLine(r), "load:fail lock:skip structural:skip cross-field:skip policy:skip guarantees:skip readiness:skip closure:skip"; got != want {
		t.Errorf("stages = %s, want %s", got, want)
	}
	if r.Stages[1].Reason != "load failed" || r.Stages[7].Reason != "load failed" || r.Stages[6].Reason != "not requested (--readiness)" {
		t.Errorf("skip reasons: %+v", r.Stages)
	}
	e := r.Errors[0]
	if e.Line != 8 || !strings.Contains(e.Message, `did you mean "persistence"?`) {
		t.Errorf("error = %+v", e)
	}
	want := "fix " + filepath.Join(dir, "pacto.yaml") + ":8 [PARSE_ERROR], then run pacto validate --closure " + dir
	if r.Next != want {
		t.Errorf("next = %q, want %q", r.Next, want)
	}
}

// TestValidateStages_SchemaErrorsInFileOrder: the schema layer's errors are
// located and sorted by position, so Next names the first in the file.
func TestValidateStages_SchemaErrorsInFileOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.0\"\nservice:\n  name: svc\n  version: 1.0.0\nworkload: servce\nstate:\n  type: stateless\n  persistence:\n    scope: local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewService(nil, nil).Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stageLine(r), "load:pass lock:skip structural:fail cross-field:skip policy:skip guarantees:skip readiness:skip closure:skip"; got != want {
		t.Errorf("stages = %s, want %s", got, want)
	}
	var lines []int
	for _, e := range r.Errors {
		lines = append(lines, e.Line)
	}
	if len(lines) != 3 || lines[0] != 5 || lines[1] != 6 || lines[2] != 8 {
		t.Errorf("error lines = %v, want [5 6 8]", lines)
	}
	if !strings.Contains(r.Errors[0].Message, `did you mean "service"?`) {
		t.Errorf("enum error = %q", r.Errors[0].Message)
	}
	if want := "fix workload at " + filepath.Join(dir, "pacto.yaml") + ":5:1 [SCHEMA_VIOLATION], then run pacto validate " + dir; r.Next != want {
		t.Errorf("next = %q, want %q", r.Next, want)
	}
}

func TestValidateStages_Lock(t *testing.T) {
	t.Run("an up-to-date lock passes", func(t *testing.T) {
		dir := writeRoot(t)
		s := NewService(authStore("sha256:v1"), nil)
		if _, err := s.Lock(context.Background(), LockOptions{Path: dir}); err != nil {
			t.Fatal(err)
		}
		r, err := s.Validate(context.Background(), ValidateOptions{Path: dir})
		if err != nil {
			t.Fatal(err)
		}
		if r.Stages[1].Name != "lock" || r.Stages[1].Status != validation.StagePass {
			t.Errorf("lock stage = %+v", r.Stages[1])
		}
	})
	t.Run("a drifted lock names the command its error gives", func(t *testing.T) {
		dir := writeRoot(t)
		r, err := staleLockNextTo(t, dir).Validate(context.Background(), ValidateOptions{Path: dir})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := stageLine(r), "load:pass lock:fail structural:skip cross-field:skip policy:skip guarantees:skip readiness:skip closure:skip"; got != want {
			t.Errorf("stages = %s, want %s", got, want)
		}
		if r.Next != "pacto lock --update "+dir {
			t.Errorf("next = %q", r.Next)
		}
	})
}

func TestValidateStages_OCIFile(t *testing.T) {
	r, err := NewService(&mockBundleStore{}, nil).Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if r.File != "pacto.yaml" || r.Stages[1].Status != validation.StageSkip {
		t.Errorf("file = %q, lock = %+v", r.File, r.Stages[1])
	}
}

func TestRerunCommand(t *testing.T) {
	got := rerunCommand("svc", ValidateOptions{Readiness: true, Closure: true, Overrides: override.Overrides{ValueFiles: []string{"prod.yaml"}, SetValues: []string{"service.version=2.0.0"}}})
	if want := "pacto validate --readiness --closure -f prod.yaml --set service.version=2.0.0 svc"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestNewValidateResult_UnlocatedErrorsLast: an error with no position sorts
// after every located one.
func TestNewValidateResult_UnlocatedErrorsLast(t *testing.T) {
	raw := []byte("service:\n  name: svc\n")
	r := newValidateResult("svc", ValidateOptions{}, raw, validation.RanStage("structural", validation.ValidationResult{
		Errors: []contract.ValidationError{{Code: "LOOSE"}, {Path: "service.name", Code: "AT"}},
	}))
	if r.Errors[0].Code != "AT" || r.Errors[1].Code != "LOOSE" {
		t.Errorf("errors = %+v", r.Errors)
	}
}

// TestClosure_RootPoliciesUnresolved: a root whose own policies do not resolve
// checks its closure for resolution failures only.
func TestClosure_RootPoliciesUnresolved(t *testing.T) {
	root := t.TempDir()
	writeClosureFile(t, root, "payments/pacto.yaml", closureService("payments", "", "ledger")+"policies:\n  - name: gone\n    ref: ../gone\n")
	writeClosureFile(t, root, "ledger/pacto.yaml", closureService("ledger", ""))
	r, err := NewService(nil, nil).Validate(context.Background(), ValidateOptions{Path: filepath.Join(root, "payments"), Closure: true})
	if err != nil {
		t.Fatal(err)
	}
	if st := r.Stages[7]; st.Name != "closure" || st.Status != validation.StagePass {
		t.Errorf("closure stage = %+v", st)
	}
}

func TestCheckClosureNode_Unreadable(t *testing.T) {
	errs := checkClosureNode(&graph.Node{Name: "ledger", Version: "1.0.0", Ref: "../ledger"}, nil, os.ErrNotExist, nil)
	if len(errs) != 1 || errs[0].Code != "CLOSURE_DEPENDENCY_UNRESOLVED" || errs[0].Path != "dependencies[ledger@1.0.0]" {
		t.Errorf("errs = %+v", errs)
	}
}

func TestValidationReport_CarriesStages(t *testing.T) {
	r, err := NewService(nil, nil).ValidationReport(context.Background(), writeInvalidBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stages) != len(stageOrder) || !strings.HasPrefix(r.Next, "fix ") || r.IsValid() {
		t.Errorf("report = %+v", r)
	}
}
