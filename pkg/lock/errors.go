package lock

import "fmt"

// DriftError: a locked OCI digest no longer matches the current resolution.
type DriftError struct{ Name, Locked, Current string }

// Code returns the machine-readable LOCK_DIGEST_MISMATCH code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *DriftError) Code() string { return "LOCK_DIGEST_MISMATCH" }

// Error formats a LOCK_DIGEST_MISMATCH message naming the locked and current digests.
func (e *DriftError) Error() string {
	return fmt.Sprintf("%s: %s locked at %s but resolves to %s; run `pacto lock --update` to re-pin", e.Code(), e.Name, e.Locked, e.Current)
}

// LocalDriftError: a local dependency's content hash changed.
type LocalDriftError struct{ Name, Locked, Current string }

// Code returns the machine-readable LOCK_LOCAL_DRIFT code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *LocalDriftError) Code() string { return "LOCK_LOCAL_DRIFT" }

// Error formats a LOCK_LOCAL_DRIFT message for the changed local dependency.
func (e *LocalDriftError) Error() string {
	return fmt.Sprintf("%s: local dependency %s content changed since lock; run `pacto lock --update`", e.Code(), e.Name)
}

// StaleError: pacto.yaml and pacto.lock disagree on which deps/refs exist.
type StaleError struct{ Detail string }

// Code returns the machine-readable LOCK_STALE code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *StaleError) Code() string { return "LOCK_STALE" }

// Error formats a LOCK_STALE message describing the pacto.yaml/pacto.lock mismatch.
func (e *StaleError) Error() string {
	return fmt.Sprintf("%s: %s; run `pacto lock`", e.Code(), e.Detail)
}

// ConflictError: the closure requires a service at two incompatible versions.
type ConflictError struct{ Service string }

// Code returns the machine-readable LOCK_CONFLICT code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *ConflictError) Code() string { return "LOCK_CONFLICT" }

// Error formats a LOCK_CONFLICT message naming the service required at conflicting versions.
func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: %s required at conflicting versions", e.Code(), e.Service)
}

// UnresolvedError: a ref could not be resolved while building the lock.
type UnresolvedError struct {
	Ref    string
	Reason string
}

// Code returns the machine-readable LOCK_UNRESOLVED code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *UnresolvedError) Code() string { return "LOCK_UNRESOLVED" }

// Error formats a LOCK_UNRESOLVED message with the ref and the reason it failed.
func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("%s: cannot resolve %s: %s", e.Code(), e.Ref, e.Reason)
}

// DuplicateDeclarationError: one contract declares the same configuration or
// policy name twice, so it holds two declarations that share an occurrence
// identity — and a lock records one entry per declaration.
//
// Canonical validation already rejects this (DUPLICATE_CONFIGURATION_NAME /
// DUPLICATE_POLICY_NAME), but `pacto lock` resolves the closure without
// validating it, so the closure walk enforces the same rule over the contract's
// declarations rather than over the refs it happens to resolve. Comparing
// RESOLUTIONS is not enough: two duplicates pointing at the same bytes are still
// two declarations, and collapsing them writes a lock that claims the contract
// declared one.
type DuplicateDeclarationError struct{ Occurrence Occurrence }

// Code returns the machine-readable LOCK_DUPLICATE_DECLARATION code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *DuplicateDeclarationError) Code() string { return "LOCK_DUPLICATE_DECLARATION" }

// Error formats a LOCK_DUPLICATE_DECLARATION message naming the repeated declaration.
func (e *DuplicateDeclarationError) Error() string {
	return fmt.Sprintf("%s: %s more than once; a name is unique within its kind in one contract, and a lock has no way to tell two declarations of it apart",
		e.Code(), e.Occurrence)
}

// AmbiguousError: one reference occurrence would have to hold two different
// resolutions, so the lock cannot record both.
//
// Duplicate names in one contract are NOT this: they are rejected earlier, by
// *DuplicateDeclarationError, whether or not they resolve alike. What reaches
// here is two byte-identical local bundle directories in different places, each
// resolving the same relative ref to a different sibling. They are one contract
// by content, and a declaration is identified by the contract that contains it,
// so the pair is genuinely outside what the lock can express.
//
// Either way this fails the lock rather than silently pinning one of the two.
type AmbiguousError struct {
	Occurrence    Occurrence
	First, Second string
}

// Code returns the machine-readable LOCK_AMBIGUOUS_REFERENCE code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *AmbiguousError) Code() string { return "LOCK_AMBIGUOUS_REFERENCE" }

// Error formats a LOCK_AMBIGUOUS_REFERENCE message naming the occurrence and both resolutions.
func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s: %s resolves to both %s and %s; a lock records one resolution per declared reference, so these cannot both be pinned",
		e.Code(), e.Occurrence, e.First, e.Second)
}

// MissingError: a lock was required (e.g. --check) but none exists.
type MissingError struct{ Path string }

// Code returns the machine-readable LOCK_MISSING code. It is the one place the
// code is written: Error renders it, and callers read it back with errors.As
// instead of splitting the rendered message apart.
func (e *MissingError) Code() string { return "LOCK_MISSING" }

// Error formats a LOCK_MISSING message naming the lock path that was not found.
func (e *MissingError) Error() string {
	return fmt.Sprintf("%s: no %s found", e.Code(), e.Path)
}
