package contract_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestParseError_Error_WithPath(t *testing.T) {
	e := &contract.ParseError{Path: "service.name", Message: "is required"}
	got := e.Error()
	want := "parse error at service.name: is required"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseError_Error_WithoutPath(t *testing.T) {
	e := &contract.ParseError{Message: "invalid YAML"}
	got := e.Error()
	want := "parse error: invalid YAML"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestValidationError_Error(t *testing.T) {
	e := &contract.ValidationError{
		Path:    "runtime.state.type",
		Code:    "INVALID_VALUE",
		Message: "must be stateless or stateful",
	}
	got := e.Error()
	want := "[INVALID_VALUE] runtime.state.type: must be stateless or stateful"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("underlying cause")
	e := &contract.ParseError{
		Path:    "service.name",
		Message: "is required",
		Err:     cause,
	}
	if !errors.Is(e, cause) {
		t.Error("expected Unwrap to return the underlying error")
	}
}

func TestParseError_Unwrap_Nil(t *testing.T) {
	e := &contract.ParseError{Message: "no cause"}
	if e.Unwrap() != nil {
		t.Error("expected Unwrap to return nil when no underlying error")
	}
}

func TestValidationWarning_String(t *testing.T) {
	w := &contract.ValidationWarning{
		Path:    "dependencies[0].ref",
		Code:    "TAG_NOT_DIGEST",
		Message: "use digest pinning",
	}
	got := w.String()
	want := "[TAG_NOT_DIGEST] dependencies[0].ref: use digest pinning"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
