package validation

import (
	"testing"
)

func TestValidationResult_AddWarning(t *testing.T) {
	var r ValidationResult
	r.AddWarning("path", "CODE", "message")
	if len(r.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(r.Warnings))
	}
	if r.Warnings[0].Path != "path" {
		t.Errorf("expected path 'path', got %q", r.Warnings[0].Path)
	}
	if r.Warnings[0].Code != "CODE" {
		t.Errorf("expected code 'CODE', got %q", r.Warnings[0].Code)
	}
	if r.Warnings[0].Message != "message" {
		t.Errorf("expected message 'message', got %q", r.Warnings[0].Message)
	}
}

func TestValidationResult_Merge(t *testing.T) {
	var r1 ValidationResult
	r1.AddError("a", "CODE_A", "err a")
	r1.AddWarning("b", "CODE_B", "warn b")

	var r2 ValidationResult
	r2.AddError("c", "CODE_C", "err c")
	r2.AddWarning("d", "CODE_D", "warn d")

	r1.Merge(r2)

	if len(r1.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(r1.Errors))
	}
	if len(r1.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(r1.Warnings))
	}
}

func TestValidationResult_IsValid_NoErrors(t *testing.T) {
	var r ValidationResult
	r.AddWarning("x", "Y", "z")
	if !r.IsValid() {
		t.Error("expected IsValid() to return true with only warnings")
	}
}

func TestValidationResult_IsValid_WithErrors(t *testing.T) {
	var r ValidationResult
	r.AddError("x", "Y", "z")
	if r.IsValid() {
		t.Error("expected IsValid() to return false with errors")
	}
}
