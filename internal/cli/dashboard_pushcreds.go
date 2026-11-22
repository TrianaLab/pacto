package cli

import (
	"encoding/json"
	"fmt"
	"os"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
)

// dockerConfigKeychain answers each registry with its entry of one Docker
// config.json, and anonymously for any other: a push credential reaches
// only the registries it names.
type dockerConfigKeychain map[string]gcrauthn.AuthConfig

// loadDockerConfig reads the Docker config.json at path, as a
// kubernetes.io/dockerconfigjson Secret mounts it.
func loadDockerConfig(path string) (dockerConfigKeychain, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the declare push credential: %w", err)
	}
	var cfg struct {
		Auths map[string]gcrauthn.AuthConfig `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("the declare push credential %s is no Docker config.json: %w", path, err)
	}
	return dockerConfigKeychain(cfg.Auths), nil
}

// Resolve implements authn.Keychain.
func (k dockerConfigKeychain) Resolve(target gcrauthn.Resource) (gcrauthn.Authenticator, error) {
	host := target.RegistryStr()
	for _, key := range []string{host, "https://" + host, "https://" + host + "/v1/"} {
		if a, ok := k[key]; ok {
			return gcrauthn.FromConfig(a), nil
		}
	}
	return gcrauthn.Anonymous, nil
}
