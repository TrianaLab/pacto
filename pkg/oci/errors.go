package oci

import (
	"errors"
	"fmt"
	"net/http"

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
	Ref string
	Err error
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("authentication failed for %s: %v — run `pacto login` or check PACTO_REGISTRY_TOKEN", e.Ref, e.Err)
}

func (e *AuthenticationError) Unwrap() error { return e.Err }

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
