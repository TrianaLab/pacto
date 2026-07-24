/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package validator is the single source of truth for runtime validation.
// Deprecated: all validation now happens via pacto v2 validation.Evaluate.
// This package remains only for backward-compat types (Check, Result).
package validator

// Check represents a single validation check result.
type Check struct {
	Name     string // condition type: uses constants from pactov1alpha1
	Passed   bool
	Reason   string
	Message  string
	Severity string // "error" or "warning" — empty defaults to error for backward compat
}

// Result is the output of validation.
type Result struct {
	Checks []Check
	Ports  PortsResult
}

// PortsResult is the explicit port comparison.
type PortsResult struct {
	Expected   []int32
	Observed   []int32
	Missing    []int32
	Unexpected []int32
}

// Deprecated: all validation now happens via pacto v2 validation.Evaluate.
// This package remains only for backward-compat types (Check, Result).
