// Package catalog turns a finite, explicitly supplied set of Pacto contract
// roots plus their dependency closure into a bounded, immutable, discoverable
// catalog.
//
// The catalog is NOT the operational fleet. pkg/fleet describes runtime targets,
// what was observed about them and how observation reconciles with what was
// declared. This package describes contract discovery from roots a caller
// handed in: no runtime, no observation, no routes, no deployment. The two share
// completeness vocabulary and identity discipline; they do not share a model.
//
// Three separations are load-bearing and appear throughout:
//
//   - A requested reference is not a resolved reference is not a content
//     identity. Only the last is identity, and only a registry digest or a
//     deterministic local content hash may serve as one.
//   - A revision is not a declaration is not a path. One immutable revision can
//     be declared by many contracts and reached by many routes; collapsing any
//     of the three loses provenance a consumer cannot reconstruct.
//   - Partial is not empty and is not complete. An unresolved root, an
//     unresolved dependency or any bound that stopped work makes the answer
//     partial. A partial answer is never served as an authoritative empty one.
//
// Construction resolves every reference exactly once. Afterwards the catalog is
// a frozen value: queries are pure, network-free, deterministically ordered and
// safe for concurrent readers, and neither a caller mutating what it passed in
// nor a caller mutating what it got back can change it. A registry tag that
// moves after construction does not move the catalog.
//
// The package is framework-independent by construction: it imports the contract
// model, go-digest and the standard library, and nothing else. Reference
// parsing, credentials, caching and registry access live behind the [Resolver]
// port, in an adapter the caller supplies.
//
// Discovery is not authorization and discovery is not execution. Nothing here
// decides who may see a revision, and nothing here runs one.
package catalog

import (
	"context"
	"errors"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// SchemaVersion identifies the catalog wire model.
const SchemaVersion = "pacto.dev/catalog/v1"

// Completeness reports whether a catalog covers everything it was asked to.
// The values match pkg/fleet's vocabulary so one agent reads both; the
// definitions are this package's own, because the reasons differ.
type Completeness string

const (
	// CompletenessComplete means every root and every declared dependency in the
	// closure resolved, and no bound stopped any work.
	CompletenessComplete Completeness = "complete"
	// CompletenessPartial means at least one root or dependency did not resolve,
	// or a bound stopped resolution or traversal. The retained portion is valid;
	// what is missing is unknown, not absent.
	CompletenessPartial Completeness = "partial"
	// CompletenessEmpty means the catalog holds nothing and nothing is unknown.
	// A built catalog is never empty: [Build] rejects an empty root set, and a
	// root that fails to resolve yields partial knowledge, never an authoritative
	// empty result. The value exists so that distinction stays explicit.
	CompletenessEmpty Completeness = "empty"
)

// Limitation codes are stable and typed so agents branch on them. Each one
// names either something that did not resolve or the exact bound that stopped
// work; the human message is advisory.
const (
	// LimitationRootUnresolved: a requested root did not resolve. The root stays
	// in the catalog with its reason -- it is never silently dropped.
	LimitationRootUnresolved = "ROOT_UNRESOLVED"
	// LimitationUnresolvedDep: a declared dependency did not resolve. Shares
	// pkg/fleet's code, because it means the same thing.
	LimitationUnresolvedDep = "UNRESOLVED_DEPENDENCY"
	// LimitationRootLimit: more roots were requested than Bounds.MaxRoots. The
	// surplus was never resolved.
	LimitationRootLimit = "ROOT_LIMIT_EXCEEDED"
	// LimitationRevisionLimit: Bounds.MaxRevisions was reached, so a reference
	// that was not already resolved was refused before any resolver call.
	LimitationRevisionLimit = "REVISION_LIMIT_EXCEEDED"
	// LimitationEdgeLimit: Bounds.MaxEdges was reached, so a dependency and the
	// declarations after it were refused before any resolver call. Ref names the
	// first one refused, as a representative rather than an inventory.
	LimitationEdgeLimit = "EDGE_LIMIT_EXCEEDED"
	// LimitationDepthLimit: Bounds.MaxDepth stopped the walk; the deeper closure
	// was never traversed or resolved.
	LimitationDepthLimit = "DEPTH_LIMIT_EXCEEDED"
	// LimitationPathLengthLimit: Bounds.MaxPathLength stopped the walk before a
	// longer route could be retained or resolved.
	LimitationPathLengthLimit = "PATH_LENGTH_LIMIT_EXCEEDED"
	// LimitationPathLimit: a revision already holds Bounds.MaxPaths retained
	// paths, so a further route was neither retained nor followed.
	LimitationPathLimit = "PATH_LIMIT_EXCEEDED"
	// LimitationUnresolvedLimit: Bounds.MaxUnresolved unresolved dependencies are
	// already recorded, so further failures are counted only by this limitation.
	LimitationUnresolvedLimit = "UNRESOLVED_LIMIT_EXCEEDED"
	// LimitationConflictLimit: Bounds.MaxConflicts was reached.
	LimitationConflictLimit = "CONFLICT_LIMIT_EXCEEDED"
	// LimitationLimitationLimit: Bounds.MaxLimitations was reached, so further
	// distinct limitations are not recorded.
	LimitationLimitationLimit = "LIMITATION_LIMIT_EXCEEDED"
	// LimitationCancelled: the caller's context ended mid-construction.
	LimitationCancelled = "CANCELLED"
)

// Limitation is a structured, machine-readable reason a catalog is incomplete.
// Prose lives in Message; agents branch on Code. Ref carries the requested or
// declared reference the limitation is about, when there is one.
type Limitation struct {
	Code    string `json:"code"`
	Ref     string `json:"ref,omitempty"`
	Message string `json:"message"`
}

// Reason codes classify why one reference did not resolve. They are categories,
// never raw transport text, so a credential, token or host name in an
// underlying error can never reach a consumer through them.
const (
	ReasonNotFound         = "NOT_FOUND"
	ReasonAuthFailed       = "AUTH_FAILED"
	ReasonCancelled        = "CANCELLED"
	ReasonUnavailable      = "UNAVAILABLE"
	ReasonInvalidReference = "INVALID_REFERENCE"
	ReasonInvalidContract  = "INVALID_CONTRACT"
	ReasonInvalidIdentity  = "INVALID_IDENTITY"
	// ReasonBoundExceeded means a bound stopped the resolution before it was
	// attempted. The reference is unknown, not missing.
	ReasonBoundExceeded = "BOUND_EXCEEDED"
)

// Reason is a sanitized, structured resolution failure.
type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResolveError lets a [Resolver] classify a failure. Message must already be
// sanitized: any error that is not a *ResolveError is deliberately reduced to a
// fixed generic reason rather than echoed, so a resolver cannot leak transport
// text into the catalog by accident.
type ResolveError struct {
	Code    string
	Message string
}

func (e *ResolveError) Error() string { return e.Message }

func reasonFrom(err error) Reason {
	var re *ResolveError
	if errors.As(err, &re) && re != nil && re.Code != "" {
		return Reason{Code: re.Code, Message: re.Message}
	}
	return Reason{Code: ReasonUnavailable, Message: "the reference could not be resolved"}
}

// ResolveRequest asks the resolver for one reference.
type ResolveRequest struct {
	// Ref is the reference text exactly as a caller requested it or a contract
	// declared it. The catalog never interprets it.
	Ref string
	// Base is the declaring revision's [Resolution.Base], echoed back unchanged,
	// so a relative reference resolves against the contract that declared it and
	// not against whichever contract the walk happened to reach first. It is
	// empty for a root.
	Base string
	// Constraint is the compatibility constraint the declaring contract attached
	// to this dependency, empty for a root. A reference that names no version
	// resolves differently under different constraints, so the constraint is part
	// of the question: without it the catalog would pick a revision the declaring
	// contract never accepted, and two declarations that genuinely disagree would
	// collapse into one instead of surfacing as a conflict.
	Constraint string
}

// Resolution is one resolved reference. The catalog keeps only a projection of
// it -- identity, service, version, owner and declared dependencies -- and never
// retains the contract pointer or any filesystem view, so nothing a caller
// mutates afterwards can reach catalog state.
type Resolution struct {
	// Contract is the parsed contract. It is read during construction only.
	Contract *contract.Contract
	// Domain qualifies the service name, so two services called "api" in
	// different domains stay distinct.
	Domain string
	// Content is the immutable content identity: an exact registry digest or a
	// deterministic local content hash. A tag, a version or a service name is
	// never acceptable here.
	Content ContentID
	// ResolvedRef is the immutable reference the request resolved to -- a
	// digest-pinned registry reference. It is empty for a local resolution, where
	// Content is itself the resolved immutable identity.
	ResolvedRef string
	// Base is the context this revision's own relative references resolve
	// against. It is opaque to the catalog, which only echoes it back down.
	Base string
}

// Resolver turns a reference into an immutable revision. It owns everything the
// catalog deliberately does not: reference syntax, credentials, caching,
// registry access and local filesystem access.
//
// It must be pure with respect to the catalog: the catalog calls it at most once
// per distinct (Base, Ref, Constraint) triple during construction and never
// afterwards.
type Resolver interface {
	Resolve(ctx context.Context, req ResolveRequest) (Resolution, error)
}
