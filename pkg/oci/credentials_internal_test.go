package oci

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

func TestGhKeychain_TokenAvailable(t *testing.T) {
	dir := t.TempDir()
	mockGh := filepath.Join(dir, "gh")
	if err := os.WriteFile(mockGh, []byte("#!/bin/sh\necho ghp_test_token_123\n"), 0755); err != nil {
		t.Fatal(err)
	}

	kc := &ghKeychain{
		execCommandFn: func(name string, arg ...string) *exec.Cmd {
			return exec.Command(mockGh, arg...)
		},
	}

	reg, _ := name.NewRegistry("ghcr.io", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	cfg, _ := auth.Authorization()
	if cfg.Username != "x-access-token" {
		t.Errorf("expected Username=x-access-token, got %q", cfg.Username)
	}
	if cfg.Password != "ghp_test_token_123" {
		t.Errorf("expected Password=ghp_test_token_123, got %q", cfg.Password)
	}
}

func TestGhKeychain_GhNotInstalled(t *testing.T) {
	kc := &ghKeychain{
		execCommandFn: func(name string, arg ...string) *exec.Cmd {
			return exec.Command("/nonexistent/binary")
		},
	}

	reg, _ := name.NewRegistry("ghcr.io", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if auth != authn.Anonymous {
		t.Error("expected Anonymous when gh not installed")
	}
}

func TestGhKeychain_NonGitHubRegistry(t *testing.T) {
	called := false
	kc := &ghKeychain{
		execCommandFn: func(name string, arg ...string) *exec.Cmd {
			called = true
			return exec.Command("echo", "token")
		},
	}

	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if auth != authn.Anonymous {
		t.Error("expected Anonymous for non-GitHub registry")
	}
	if called {
		t.Error("expected gh not to be called for non-GitHub registry")
	}
}

func TestGhKeychain_EmptyToken(t *testing.T) {
	dir := t.TempDir()
	mockGh := filepath.Join(dir, "gh")
	if err := os.WriteFile(mockGh, []byte("#!/bin/sh\necho\n"), 0755); err != nil {
		t.Fatal(err)
	}

	kc := &ghKeychain{
		execCommandFn: func(name string, arg ...string) *exec.Cmd {
			return exec.Command(mockGh, arg...)
		},
	}

	reg, _ := name.NewRegistry("ghcr.io", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if auth != authn.Anonymous {
		t.Error("expected Anonymous for empty token")
	}
}

func TestGhKeychain_DockerPkgGithub(t *testing.T) {
	dir := t.TempDir()
	mockGh := filepath.Join(dir, "gh")
	if err := os.WriteFile(mockGh, []byte("#!/bin/sh\necho ghp_token\n"), 0755); err != nil {
		t.Fatal(err)
	}

	kc := &ghKeychain{
		execCommandFn: func(name string, arg ...string) *exec.Cmd {
			return exec.Command(mockGh, arg...)
		},
	}

	reg, _ := name.NewRegistry("docker.pkg.github.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	cfg, _ := auth.Authorization()
	if cfg.Username != "x-access-token" {
		t.Errorf("expected Username=x-access-token, got %q", cfg.Username)
	}
}

func TestIsGitHubRegistry(t *testing.T) {
	tests := []struct {
		registry string
		want     bool
	}{
		{"ghcr.io", true},
		{"docker.pkg.github.com", true},
		{"example.com", false},
		{"docker.io", false},
	}
	for _, tt := range tests {
		t.Run(tt.registry, func(t *testing.T) {
			if got := isGitHubRegistry(tt.registry); got != tt.want {
				t.Errorf("isGitHubRegistry(%q) = %v, want %v", tt.registry, got, tt.want)
			}
		})
	}
}

func TestGhKeychain_GhError(t *testing.T) {
	dir := t.TempDir()
	mockGh := filepath.Join(dir, "gh")
	if err := os.WriteFile(mockGh, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	kc := &ghKeychain{
		execCommandFn: func(name string, arg ...string) *exec.Cmd {
			return exec.Command(mockGh, arg...)
		},
	}

	reg, _ := name.NewRegistry("ghcr.io", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if auth != authn.Anonymous {
		t.Error("expected Anonymous when gh returns error")
	}
}
