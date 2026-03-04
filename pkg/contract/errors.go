package contract

import "fmt"

// ParseError represents an error encountered during YAML parsing.
type ParseError struct {
	Path    string
	Message string
	Err     error // underlying cause, if any
}

func (e *ParseError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("parse error at %s: %s", e.Path, e.Message)
	}
	return fmt.Sprintf("parse error: %s", e.Message)
}

func (e *ParseError) Unwrap() error {
	return e.Err
}

// ValidationError represents a validation failure that makes a contract invalid.
type ValidationError struct {
	Path    string
	Code    string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Code, e.Path, e.Message)
}

// ValidationWarning represents a validation concern that does not invalidate the contract.
type ValidationWarning struct {
	Path    string
	Code    string
	Message string
}

func (w *ValidationWarning) String() string {
	return fmt.Sprintf("[%s] %s: %s", w.Code, w.Path, w.Message)
}
