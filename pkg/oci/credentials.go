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

// EnvCredentialOptions reads the registry credentials Pacto takes from the
// environment. It is the ONE definition of those variable names, shared by the
// CLI root and the Evidence Server, so a process cannot end up reaching a
// registry under a different credential policy than `pacto pull` would.
func EnvCredentialOptions() CredentialOptions {
	return CredentialOptions{
		Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
		Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
		Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
	}
}

// EnvInsecureRegistries returns the hosts PACTO_INSECURE_REGISTRIES marks as
// plain-HTTP (comma-separated), for a controlled in-cluster registry such as the
// evidence-server E2E. https hosts are unaffected.
func EnvInsecureRegistries() []string {
	var out []string
	for _, part := range strings.Split(os.Getenv("PACTO_INSECURE_REGISTRIES"), ",") {
		if host := strings.TrimSpace(part); host != "" {
			out = append(out, host)
		}
	}
	return out
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

// CredSource is a named credential source resolved for a registry.
type CredSource struct {
	Name          string
	Authenticator authn.Authenticator
}

// namedKeychain pairs a keychain with a human-readable source name.
type namedKeychain struct {
	name string
	kc   authn.Keychain
}

// PactoKeychain resolves credentials across ordered named sources and supports
// falling through to the next source when the registry rejects the current one.
type PactoKeychain struct {
	sources []namedKeychain
}

// Resolve returns the first non-anonymous authenticator, preserving back-compat
// with go-containerregistry's remote.WithAuthFromKeychain callers.
func (k *PactoKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	for _, src := range k.sources {
		auth, err := src.kc.Resolve(target)
		if err != nil {
			return nil, err
		}
		if auth != authn.Anonymous {
			return auth, nil
		}
	}
	return authn.Anonymous, nil
}

// Candidates returns all non-anonymous credential sources for the target,
// in priority order, each tagged with its source name.
func (k *PactoKeychain) Candidates(target authn.Resource) ([]CredSource, error) {
	var cands []CredSource
	for _, src := range k.sources {
		auth, err := src.kc.Resolve(target)
		if err != nil {
			return nil, err
		}
		if auth != authn.Anonymous {
			cands = append(cands, CredSource{Name: src.name, Authenticator: auth})
		}
	}
	return cands, nil
}

// NewKeychain builds a keychain that tries, in order:
// 1. Explicit credentials (flags/env vars)
// 2. Pacto config (~/.config/pacto/config.json)
// 3. gh CLI token (for GitHub registries)
// 4. Docker config, credential helpers, and cloud auto-detection
func NewKeychain(opts CredentialOptions) authn.Keychain {
	sources := make([]namedKeychain, 0, 4)

	if opts.Token != "" {
		sources = append(sources, namedKeychain{name: "env token", kc: staticKeychain{auth: &authn.AuthConfig{RegistryToken: opts.Token}}})
	} else if opts.Username != "" && opts.Password != "" {
		sources = append(sources, namedKeychain{name: "env user/pass", kc: staticKeychain{auth: &authn.AuthConfig{Username: opts.Username, Password: opts.Password}}})
	}

	sources = append(sources, namedKeychain{name: "pacto login", kc: &pactoConfigKeychain{}})
	sources = append(sources, namedKeychain{name: "gh", kc: &ghKeychain{execCommandFn: exec.Command}})
	sources = append(sources, namedKeychain{name: "docker", kc: authn.DefaultKeychain})

	return &PactoKeychain{sources: sources}
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
