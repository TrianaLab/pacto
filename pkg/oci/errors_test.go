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

func TestAuthenticationError_WithSources(t *testing.T) {
	cause := fmt.Errorf("forbidden")
	tests := []struct {
		name        string
		err         *AuthenticationError
		wantContain []string
		wantOmit    []string
	}{
		{
			name:        "tried and rejected",
			err:         &AuthenticationError{Ref: "reg.io/img:v1", Err: cause, Tried: []string{"pacto login", "gh"}, Rejected: []string{"pacto login", "gh"}},
			wantContain: []string{"rejected: pacto login, gh", "tried: pacto login, gh", "pacto logout <registry>"},
		},
		{
			name:        "rejected only",
			err:         &AuthenticationError{Ref: "reg.io/img:v1", Err: cause, Rejected: []string{"pacto login"}},
			wantContain: []string{"rejected: pacto login"},
			wantOmit:    []string{"tried:"},
		},
		{
			name:        "tried only",
			err:         &AuthenticationError{Ref: "reg.io/img:v1", Err: cause, Tried: []string{"anonymous"}},
			wantContain: []string{"tried: anonymous"},
			wantOmit:    []string{"rejected:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			for _, want := range tt.wantContain {
				if !strings.Contains(msg, want) {
					t.Errorf("Error() = %q, want to contain %q", msg, want)
				}
			}
			for _, omit := range tt.wantOmit {
				if strings.Contains(msg, omit) {
					t.Errorf("Error() = %q, should not contain %q", msg, omit)
				}
			}
		})
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
