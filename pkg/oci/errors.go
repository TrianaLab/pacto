package oci

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// RegistryUnreachableError indicates a network or DNS failure.
type RegistryUnreachableError struct {
	Ref string
	Err error
}

func (e *RegistryUnreachableError) Error() string {
	return fmt.Sprintf("registry unreachable for %s: %v", e.Ref, e.Err)
}

func (e *RegistryUnreachableError) Unwrap() error { return e.Err }

// AuthenticationError indicates credential rejection (401/403).
type AuthenticationError struct {
	Ref      string
	Err      error
	Tried    []string
	Rejected []string
}

func (e *AuthenticationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "authentication failed for %s", e.Ref)
	if len(e.Rejected) > 0 {
		fmt.Fprintf(&b, " (rejected: %s)", strings.Join(e.Rejected, ", "))
	}
	if len(e.Tried) > 0 {
		fmt.Fprintf(&b, " (tried: %s)", strings.Join(e.Tried, ", "))
	}
	fmt.Fprintf(&b, ": %v — run `pacto login`, `pacto logout <registry>`, switch your `gh` account, or set PACTO_REGISTRY_TOKEN", e.Err)
	return b.String()
}

func (e *AuthenticationError) Unwrap() error { return e.Err }

// isAuthError reports whether err is a transport error with a 401 or 403 status.
func isAuthError(err error) bool {
	var transportErr *transport.Error
	if !errors.As(err, &transportErr) {
		return false
	}
	return transportErr.StatusCode == http.StatusUnauthorized || transportErr.StatusCode == http.StatusForbidden
}

// ArtifactNotFoundError indicates the reference does not exist (404).
type ArtifactNotFoundError struct {
	Ref string
	Err error
}

func (e *ArtifactNotFoundError) Error() string {
	return fmt.Sprintf("artifact not found: %s", e.Ref)
}

func (e *ArtifactNotFoundError) Unwrap() error { return e.Err }

// wrapRemoteError translates go-containerregistry errors into domain error types.
func wrapRemoteError(ref string, err error) error {
	var transportErr *transport.Error
	if errors.As(err, &transportErr) {
		switch transportErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return &AuthenticationError{Ref: ref, Err: err}
		case http.StatusNotFound:
			return &ArtifactNotFoundError{Ref: ref, Err: err}
		}
	}
	return &RegistryUnreachableError{Ref: ref, Err: err}
}
