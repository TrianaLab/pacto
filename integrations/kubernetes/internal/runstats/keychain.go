/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runstats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
)

// Keychain resolves credentials from the Docker config.json in dir (the auths
// of a kubernetes.io/dockerconfigjson Secret) and asks fallback for any host
// it does not list. An empty dir is fallback.
//
// ponytail: plain auths only, no credential helpers and no docker.io aliases.
func Keychain(dir string, fallback authn.Keychain) authn.Keychain {
	if dir == "" {
		return fallback
	}
	return dirKeychain{file: filepath.Join(dir, "config.json"), fallback: fallback}
}

type dirKeychain struct {
	file     string
	fallback authn.Keychain
}

// Resolve reads the file on every call, so a rotated Secret takes effect on
// the next record. A missing, malformed or undecodable entry falls back.
func (k dirKeychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	var cfg struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if data, err := os.ReadFile(k.file); err == nil && json.Unmarshal(data, &cfg) == nil {
		for key, raw := range cfg.Auths {
			var ac authn.AuthConfig
			if hostOf(key) == res.RegistryStr() && json.Unmarshal(raw, &ac) == nil {
				return authn.FromConfig(ac), nil
			}
		}
	}
	return k.fallback.Resolve(res)
}

// hostOf reduces a config key ("https://host/v1/" or "host") to its host.
func hostOf(key string) string {
	key = strings.TrimPrefix(strings.TrimPrefix(key, "https://"), "http://")
	host, _, _ := strings.Cut(key, "/")
	return host
}
