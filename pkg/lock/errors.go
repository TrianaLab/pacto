package lock

import "fmt"

// DriftError: a locked OCI digest no longer matches the current resolution.
type DriftError struct{ Name, Locked, Current string }

// Error formats a LOCK_DIGEST_MISMATCH message naming the locked and current digests.
func (e *DriftError) Error() string {
	return fmt.Sprintf("LOCK_DIGEST_MISMATCH: %s locked at %s but resolves to %s; run `pacto lock --update` to re-pin", e.Name, e.Locked, e.Current)
}

// LocalDriftError: a local dependency's content hash changed.
type LocalDriftError struct{ Name, Locked, Current string }

// Error formats a LOCK_LOCAL_DRIFT message for the changed local dependency.
func (e *LocalDriftError) Error() string {
	return fmt.Sprintf("LOCK_LOCAL_DRIFT: local dependency %s content changed since lock; run `pacto lock --update`", e.Name)
}

// StaleError: pacto.yaml and pacto.lock disagree on which deps/refs exist.
type StaleError struct{ Detail string }

// Error formats a LOCK_STALE message describing the pacto.yaml/pacto.lock mismatch.
func (e *StaleError) Error() string { return fmt.Sprintf("LOCK_STALE: %s; run `pacto lock`", e.Detail) }

// ConflictError: the closure requires a service at two incompatible versions.
type ConflictError struct{ Service string }

// Error formats a LOCK_CONFLICT message naming the service required at conflicting versions.
func (e *ConflictError) Error() string {
	return fmt.Sprintf("LOCK_CONFLICT: %s required at conflicting versions", e.Service)
}

// UnresolvedError: a ref could not be resolved while building the lock.
type UnresolvedError struct {
	Ref    string
	Reason string
}

// Error formats a LOCK_UNRESOLVED message with the ref and the reason it failed.
func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("LOCK_UNRESOLVED: cannot resolve %s: %s", e.Ref, e.Reason)
}

// MissingError: a lock was required (e.g. --check) but none exists.
type MissingError struct{ Path string }

// Error formats a LOCK_MISSING message naming the lock path that was not found.
func (e *MissingError) Error() string {
	return fmt.Sprintf("LOCK_MISSING: no %s found", e.Path)
}
