package validation

import "github.com/trianalab/pacto/pkg/contract"

// ValidationResult aggregates errors and warnings from all validation layers.
type ValidationResult struct {
	Errors   []contract.ValidationError
	Warnings []contract.ValidationWarning
}

// IsValid returns true if there are no errors.
func (r *ValidationResult) IsValid() bool {
	return len(r.Errors) == 0
}

// AddError appends a validation error.
func (r *ValidationResult) AddError(path, code, message string) {
	r.Errors = append(r.Errors, contract.ValidationError{
		Path:    path,
		Code:    code,
		Message: message,
	})
}

// AddWarning appends a validation warning.
func (r *ValidationResult) AddWarning(path, code, message string) {
	r.Warnings = append(r.Warnings, contract.ValidationWarning{
		Path:    path,
		Code:    code,
		Message: message,
	})
}

// Merge combines another result into this one.
func (r *ValidationResult) Merge(other ValidationResult) {
	r.Errors = append(r.Errors, other.Errors...)
	r.Warnings = append(r.Warnings, other.Warnings...)
}
