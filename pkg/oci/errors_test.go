package oci

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func TestRegistryUnreachableError(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	err := &RegistryUnreachableError{Ref: "example.com/repo:v1", Err: cause}

	// Test Error() message.
	msg := err.Error()
	if !strings.Contains(msg, "registry unreachable") {
		t.Errorf("Error() = %q, want to contain %q", msg, "registry unreachable")
	}
	if !strings.Contains(msg, "example.com/repo:v1") {
		t.Errorf("Error() = %q, want to contain ref", msg)
	}

	// Test Unwrap().
	if !errors.Is(err, cause) {
		t.Error("Unwrap() should return the cause")
	}
}

func TestAuthenticationError(t *testing.T) {
	cause := fmt.Errorf("unauthorized")
	err := &AuthenticationError{Ref: "example.com/repo:v1", Err: cause}

	msg := err.Error()
	if !strings.Contains(msg, "authentication failed") {
		t.Errorf("Error() = %q, want to contain %q", msg, "authentication failed")
	}
	if !strings.Contains(msg, "example.com/repo:v1") {
		t.Errorf("Error() = %q, want to contain ref", msg)
	}
	if !strings.Contains(msg, "pacto login") {
		t.Errorf("Error() = %q, want to contain %q", msg, "pacto login")
	}

	if !errors.Is(err, cause) {
		t.Error("Unwrap() should return the cause")
	}
}

func TestArtifactNotFoundError(t *testing.T) {
	cause := fmt.Errorf("not found")
	err := &ArtifactNotFoundError{Ref: "example.com/repo:v1", Err: cause}

	msg := err.Error()
	if !strings.Contains(msg, "artifact not found") {
		t.Errorf("Error() = %q, want to contain %q", msg, "artifact not found")
	}
	if !strings.Contains(msg, "example.com/repo:v1") {
		t.Errorf("Error() = %q, want to contain ref", msg)
	}

	if !errors.Is(err, cause) {
		t.Error("Unwrap() should return the cause")
	}
}

func TestWrapRemoteError_401(t *testing.T) {
	transportErr := &transport.Error{StatusCode: 401}
	err := wrapRemoteError("reg.io/img:v1", transportErr)

	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
	if authErr.Ref != "reg.io/img:v1" {
		t.Errorf("Ref = %q, want %q", authErr.Ref, "reg.io/img:v1")
	}
}

func TestWrapRemoteError_403(t *testing.T) {
	transportErr := &transport.Error{StatusCode: 403}
	err := wrapRemoteError("reg.io/img:v1", transportErr)

	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestWrapRemoteError_404(t *testing.T) {
	transportErr := &transport.Error{StatusCode: 404}
	err := wrapRemoteError("reg.io/img:v1", transportErr)

	var notFoundErr *ArtifactNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected ArtifactNotFoundError, got %T: %v", err, err)
	}
	if notFoundErr.Ref != "reg.io/img:v1" {
		t.Errorf("Ref = %q, want %q", notFoundErr.Ref, "reg.io/img:v1")
	}
}

func TestWrapRemoteError_500(t *testing.T) {
	transportErr := &transport.Error{StatusCode: 500}
	err := wrapRemoteError("reg.io/img:v1", transportErr)

	var unreachable *RegistryUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected RegistryUnreachableError, got %T: %v", err, err)
	}
}

func TestWrapRemoteError_NonTransport(t *testing.T) {
	plainErr := fmt.Errorf("dial tcp: connection refused")
	err := wrapRemoteError("reg.io/img:v1", plainErr)

	var unreachable *RegistryUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected RegistryUnreachableError, got %T: %v", err, err)
	}
	if unreachable.Ref != "reg.io/img:v1" {
		t.Errorf("Ref = %q, want %q", unreachable.Ref, "reg.io/img:v1")
	}
}
