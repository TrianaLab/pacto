package oci

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
)

// CredentialOptions holds explicit credentials provided via CLI flags or env vars.
type CredentialOptions struct {
	Username string
	Password string
	Token    string
}

var userHomeDirFn = os.UserHomeDir

// ExportedUserHomeDirFn returns the current userHomeDirFn for testing.
func ExportedUserHomeDirFn() func() (string, error) { return userHomeDirFn }

// SetUserHomeDirFn sets userHomeDirFn and returns the previous value for deferred restore.
func SetUserHomeDirFn(fn func() (string, error)) func() (string, error) {
	old := userHomeDirFn
	userHomeDirFn = fn
	return old
}

// PactoConfigDir returns the pacto configuration directory.
// It respects $XDG_CONFIG_HOME, defaulting to ~/.config/pacto.
func PactoConfigDir() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := userHomeDirFn()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "pacto"), nil
}

// PactoConfigPath returns the path to pacto's dedicated config file.
// It respects $XDG_CONFIG_HOME, defaulting to ~/.config/pacto/config.json.
func PactoConfigPath() (string, error) {
	dir, err := PactoConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// NewKeychain builds a keychain that tries, in order:
// 1. Explicit credentials (flags/env vars)
// 2. Pacto config (~/.config/pacto/config.json)
// 3. gh CLI token (for GitHub registries)
// 4. Docker config, credential helpers, and cloud auto-detection
func NewKeychain(opts CredentialOptions) authn.Keychain {
	keychains := make([]authn.Keychain, 0, 4)

	if opts.Token != "" {
		keychains = append(keychains, staticKeychain{auth: &authn.AuthConfig{RegistryToken: opts.Token}})
	} else if opts.Username != "" && opts.Password != "" {
		keychains = append(keychains, staticKeychain{auth: &authn.AuthConfig{Username: opts.Username, Password: opts.Password}})
	}

	keychains = append(keychains, &pactoConfigKeychain{})
	keychains = append(keychains, &ghKeychain{execCommandFn: exec.Command})
	keychains = append(keychains, authn.DefaultKeychain)

	return authn.NewMultiKeychain(keychains...)
}

// staticKeychain returns the same credentials for any registry.
type staticKeychain struct {
	auth *authn.AuthConfig
}

func (k staticKeychain) Resolve(_ authn.Resource) (authn.Authenticator, error) {
	return authn.FromConfig(*k.auth), nil
}

// pactoConfigKeychain reads credentials from pacto's dedicated config file.
type pactoConfigKeychain struct{}

// PactoConfig represents the structure of pacto's config.json file.
type PactoConfig struct {
	Auths map[string]PactoAuth `json:"auths"`
}

// PactoAuth represents a single registry auth entry.
type PactoAuth struct {
	Auth string `json:"auth"`
}

func (k *pactoConfigKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	cfgPath, err := PactoConfigPath()
	if err != nil {
		return authn.Anonymous, nil
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return authn.Anonymous, nil
	}

	var cfg PactoConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return authn.Anonymous, nil
	}

	entry, ok := cfg.Auths[target.RegistryStr()]
	if !ok {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{Auth: entry.Auth}), nil
}

// ghKeychain uses the gh CLI to obtain tokens for GitHub registries.
type ghKeychain struct {
	execCommandFn func(name string, arg ...string) *exec.Cmd
}

// isGitHubRegistry reports whether the registry is a GitHub container registry.
func isGitHubRegistry(registry string) bool {
	return registry == "ghcr.io" || registry == "docker.pkg.github.com"
}

func (k *ghKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	if !isGitHubRegistry(target.RegistryStr()) {
		return authn.Anonymous, nil
	}

	out, err := k.execCommandFn("gh", "auth", "token").Output()
	if err != nil {
		return authn.Anonymous, nil
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{
		Username: "x-access-token",
		Password: token,
	}), nil
}
