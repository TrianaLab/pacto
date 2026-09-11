// Package diff compares two versioned contracts and classifies each change as
// non-breaking, potentially breaking, or breaking to downstream consumers.
//
// Two classifiers share that work, because they key on different things and
// neither can do the other's job. Named contract elements — a field, an
// interface, a policy, an OpenAPI path or method, an AsyncAPI channel, an rpc —
// are looked up in the deterministic (path, change type) rule table in
// classification.go, so the same structural change always classifies the same
// way. Everything the recursive JSON walk reaches — a request body, a response,
// a JSON Schema, an event payload — is classified by classifySchemaChange in
// schema.go from the shape of its JSON path, because those paths are open-ended
// and cannot be enumerated in advance. That classifier also takes the walk's
// direction, because the same edit is not the same news on both sides: a
// required field added to a request is a new obligation on the caller, added to
// a response it is a stronger guarantee to the reader.
//
// The boundary is the reason the rule table has no Modified row for anything the
// walk descends into: an element present on both sides is deep-diffed, so the
// table is never asked. Adding such a row changes nothing and reads as if it
// does. SBOM artifacts are diffed separately and are informational only.
//
// Two sites adjust a classifier's answer in place, so the table alone does not
// decide severity for openapi.parameters or for a configuration's values.
// diffParameters (openapi.go) escalates to Breaking when a parameter arrives
// required or turns required, because the table cannot see the parameter's own
// required flag. diffConfigValues (interfaces.go) overwrites every walk result
// with NonBreaking, because inline values are the provider's own defaults rather
// than consumer-facing surface. Tuning either of those two table rows without
// reading its call site will look like it did nothing.
package diff

import (
	"context"
	"encoding/json"
	"io/fs"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/sbom"
)

// Classification represents the severity of a change.
type Classification int

const (
	NonBreaking       Classification = iota // Consumers are not affected.
	PotentialBreaking                       // Consumers may be affected.
	Breaking                                // Consumers are definitely affected.
)

// MarshalJSON serializes the Classification as its String form (e.g. "BREAKING").
func (c Classification) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

// String returns the upper-snake-case name: "NON_BREAKING", "POTENTIAL_BREAKING", "BREAKING", or "UNKNOWN".
func (c Classification) String() string {
	switch c {
	case NonBreaking:
		return "NON_BREAKING"
	case PotentialBreaking:
		return "POTENTIAL_BREAKING"
	case Breaking:
		return "BREAKING"
	default:
		return "UNKNOWN"
	}
}

// ChangeType describes how a field changed.
type ChangeType int

const (
	Added ChangeType = iota
	Removed
	Modified
)

// MarshalJSON serializes the ChangeType as its lowercase String form (e.g. "added").
func (t ChangeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// String returns the lowercase name: "added", "removed", "modified", or "unknown".
func (t ChangeType) String() string {
	switch t {
	case Added:
		return "added"
	case Removed:
		return "removed"
	case Modified:
		return "modified"
	default:
		return "unknown"
	}
}

// Change represents a single detected change between two contracts.
type Change struct {
	Path           string         `json:"path"`
	Type           ChangeType     `json:"type"`
	OldValue       any            `json:"oldValue,omitempty"`
	NewValue       any            `json:"newValue,omitempty"`
	Classification Classification `json:"classification"`
	Reason         string         `json:"reason"`
}

// Result holds the output of comparing two contracts.
type Result struct {
	Classification Classification `json:"classification"`
	Changes        []Change       `json:"changes"`
	SBOMDiff       *sbom.Result   `json:"sbomDiff,omitempty"`
}

// Compare compares two contracts and produces a classified diff result.
// oldFS and newFS provide access to referenced files (OpenAPI specs, JSON Schemas)
// within each contract's bundle. Either may be nil if file-level diffs are not needed.
// ctx carries the logger used for informational SBOM-parse diagnostics.
func Compare(ctx context.Context, old, new *contract.Contract, oldFS, newFS fs.FS) *Result {
	var changes []Change

	changes = append(changes, diffContract(old, new)...)
	changes = append(changes, diffDependencies(old, new)...)
	changes = append(changes, diffInterfaces(old, new, oldFS, newFS)...)
	changes = append(changes, diffConfiguration(old, new, oldFS, newFS)...)
	changes = append(changes, diffPolicy(old, new, oldFS, newFS)...)
	changes = append(changes, diffReadiness(old.Readiness, new.Readiness)...)

	overall := NonBreaking
	for i := range changes {
		if changes[i].Classification > overall {
			overall = changes[i].Classification
		}
	}

	return &Result{
		Classification: overall,
		Changes:        changes,
		SBOMDiff:       diffSBOM(ctx, oldFS, newFS),
	}
}

// diffSBOM compares SBOM documents from both bundle filesystems.
// SBOM changes are informational and do not affect the classification.
func diffSBOM(ctx context.Context, oldFS, newFS fs.FS) *sbom.Result {
	oldDoc, err := sbom.ParseFromFS(oldFS)
	if err != nil {
		logging.LoggerFromContext(ctx).Debug("failed to parse old SBOM", "error", err)
		return nil
	}
	newDoc, err := sbom.ParseFromFS(newFS)
	if err != nil {
		logging.LoggerFromContext(ctx).Debug("failed to parse new SBOM", "error", err)
		return nil
	}

	result := sbom.Diff(oldDoc, newDoc)
	if result != nil && len(result.Changes) == 0 {
		return nil
	}
	return result
}
