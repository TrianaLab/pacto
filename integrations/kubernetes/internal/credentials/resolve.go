/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package credentials resolves OCI registry credentials from Kubernetes Secrets.
// It supports three secret formats:
//   - Opaque with "token" key (bearer/registry token)
//   - Opaque with "username" + "password" keys (basic auth)
//   - kubernetes.io/dockerconfigjson (standard Docker registry auth)
//
// For dashboard credential merging (MergeToDockerConfigJSON), Opaque secrets must
// also include a "registry" key specifying the target hostname (e.g. "ghcr.io").
package credentials

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
)

const dockerIORegistry = "docker.io"

// dockerConfigJSON mirrors the Docker config.json structure.
type dockerConfigJSON struct {
	Auths map[string]dockerConfigEntry `json:"auths"`
}

type dockerConfigEntry struct {
	Auth     string `json:"auth,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// FromSecret extracts OCI registry credentials from a Kubernetes Secret.
// For dockerconfigjson secrets, registry selects the matching auth entry.
// For opaque secrets, a "registry" key (when present) binds the credentials to that
// host and a request for any other registry is refused; keyless opaque secrets keep
// the legacy global behavior.
//
// Precedence for opaque secrets: token > username+password.
// Precedence for dockerconfigjson: exact host match > host with scheme stripped.
func FromSecret(secret *corev1.Secret, registry string) (*authn.AuthConfig, error) {
	if secret.Type == corev1.SecretTypeDockerConfigJson {
		return fromDockerConfigJSON(secret, registry)
	}
	return fromOpaque(secret, registry)
}

// fromOpaque extracts credentials from an Opaque secret. When the secret declares a
// "registry" key, the credentials are bound to that host: a request for any other
// registry (or no registry) is refused. This stops a token scoped to one registry
// from being handed to another — e.g. a contract that points pull traffic at an
// attacker-controlled host to exfiltrate a referenced-but-unreadable secret. Secrets
// without a "registry" key keep the legacy global behavior.
func fromOpaque(secret *corev1.Secret, registry string) (*authn.AuthConfig, error) {
	if bound := strings.TrimSpace(string(secret.Data["registry"])); bound != "" {
		if registry == "" || normalizeRegistryHost(bound) != normalizeRegistryHost(registry) {
			return nil, fmt.Errorf("secret %q is scoped to registry %q but credentials were requested for %q",
				secret.Name, bound, registry)
		}
	}

	if token := string(secret.Data["token"]); token != "" {
		return &authn.AuthConfig{RegistryToken: token}, nil
	}

	user := string(secret.Data["username"])
	pass := string(secret.Data["password"])
	if user != "" && pass != "" {
		return &authn.AuthConfig{Username: user, Password: pass}, nil
	}

	return nil, fmt.Errorf("secret %q must contain either 'token' or 'username'+'password' keys", secret.Name)
}

// fromDockerConfigJSON extracts credentials for a specific registry from a
// kubernetes.io/dockerconfigjson secret.
func fromDockerConfigJSON(secret *corev1.Secret, registry string) (*authn.AuthConfig, error) {
	raw, ok := secret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("secret %q is type %s but missing key %q",
			secret.Name, corev1.SecretTypeDockerConfigJson, corev1.DockerConfigJsonKey)
	}

	var cfg dockerConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("secret %q: malformed dockerconfigjson: %w", secret.Name, err)
	}

	if len(cfg.Auths) == 0 {
		return nil, fmt.Errorf("secret %q: dockerconfigjson has no auth entries", secret.Name)
	}

	entry, found := matchRegistry(cfg.Auths, registry)
	if !found {
		registries := make([]string, 0, len(cfg.Auths))
		for k := range cfg.Auths {
			registries = append(registries, k)
		}
		return nil, fmt.Errorf("secret %q: no auth entry for registry %q (available: %s)",
			secret.Name, registry, strings.Join(registries, ", "))
	}

	return entryToAuthConfig(entry, secret.Name)
}

// matchRegistry finds the best matching auth entry for the given registry host.
// It tries exact match first, then normalized (scheme-stripped) match.
func matchRegistry(auths map[string]dockerConfigEntry, registry string) (dockerConfigEntry, bool) {
	registry = normalizeRegistryHost(registry)

	// Exact match on normalized host
	for key, entry := range auths {
		if normalizeRegistryHost(key) == registry {
			return entry, true
		}
	}
	return dockerConfigEntry{}, false
}

// normalizeRegistryHost strips scheme and trailing slashes from a registry host.
// "https://ghcr.io/v2/" → "ghcr.io"
// "docker.io" → "index.docker.io" (Docker Hub canonical form)
func normalizeRegistryHost(host string) string {
	// Strip scheme
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// Strip path and trailing slashes
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimRight(host, "/")
	// Canonicalize Docker Hub
	if host == dockerIORegistry {
		return dockerHubHost
	}
	return host
}

// entryToAuthConfig converts a Docker config auth entry to authn.AuthConfig.
func entryToAuthConfig(entry dockerConfigEntry, secretName string) (*authn.AuthConfig, error) {
	// Prefer explicit username/password fields
	if entry.Username != "" && entry.Password != "" {
		return &authn.AuthConfig{Username: entry.Username, Password: entry.Password}, nil
	}

	// Fall back to base64-encoded "auth" field (username:password)
	if entry.Auth != "" {
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			return nil, fmt.Errorf("secret %q: invalid base64 in auth field: %w", secretName, err)
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("secret %q: auth field must be base64(username:password)", secretName)
		}
		return &authn.AuthConfig{Username: parts[0], Password: parts[1]}, nil
	}

	return nil, fmt.Errorf("secret %q: auth entry has no credentials", secretName)
}

const dockerHubHost = "index.docker.io"

// RegistryFromRef extracts the registry hostname from an OCI reference.
// "ghcr.io/org/repo:tag" → "ghcr.io"
// "registry:5000/org/repo" → "registry:5000"
func RegistryFromRef(ref string) string {
	ref = strings.TrimPrefix(ref, "oci://")

	// Strip digest
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}

	// The registry is everything before the first slash, unless it looks like
	// a simple name (no dots, no colons) — then it's a Docker Hub library image.
	registry, _, found := strings.Cut(ref, "/")
	if !found {
		return dockerHubHost
	}
	if strings.ContainsAny(registry, ".:") {
		return registry
	}
	return dockerHubHost
}

// MergeToDockerConfigJSON combines credentials from multiple secrets into a single
// dockerconfigjson-formatted byte slice. This is used by the dashboard reconciler
// to create a managed secret from multiple source secrets.
//
// For opaque secrets, the "registry" key determines the hostname used in .auths.
// For dockerconfigjson secrets, all auth entries are merged.
// Later secrets override earlier ones for the same registry host.
func MergeToDockerConfigJSON(secrets []*corev1.Secret) ([]byte, error) {
	merged := dockerConfigJSON{Auths: make(map[string]dockerConfigEntry)}

	for _, secret := range secrets {
		if secret.Type == corev1.SecretTypeDockerConfigJson {
			raw, ok := secret.Data[corev1.DockerConfigJsonKey]
			if !ok {
				return nil, fmt.Errorf("secret %q: missing key %q", secret.Name, corev1.DockerConfigJsonKey)
			}
			var cfg dockerConfigJSON
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, fmt.Errorf("secret %q: malformed dockerconfigjson: %w", secret.Name, err)
			}
			for registry, entry := range cfg.Auths {
				merged.Auths[normalizeRegistryHost(registry)] = entry
			}
		} else {
			registry := strings.TrimSpace(string(secret.Data["registry"]))
			if registry == "" {
				return nil, fmt.Errorf("secret %q: opaque secrets must contain a 'registry' key "+
					"specifying the registry hostname (e.g. ghcr.io)", secret.Name)
			}
			auth, err := fromOpaque(secret, registry)
			if err != nil {
				return nil, fmt.Errorf("secret %q: %w", secret.Name, err)
			}
			merged.Auths[normalizeRegistryHost(registry)] = dockerConfigEntry{
				Username: auth.Username,
				Password: auth.Password,
				Auth:     encodeAuth(auth),
			}
		}
	}

	return json.Marshal(merged)
}

func encodeAuth(auth *authn.AuthConfig) string {
	if auth.RegistryToken != "" {
		return base64.StdEncoding.EncodeToString([]byte("_token:" + auth.RegistryToken))
	}
	return base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))
}
