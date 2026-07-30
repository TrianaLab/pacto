package app

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
)

// trustConfigAPIVersion is the only trust-config schema this build understands.
const trustConfigAPIVersion = "pacto.dev/evidence-trust/v1"

// trustConfig is the structured, versioned trust configuration. Unlike the bare
// directory-of-*.pub mode, it can bind per-key subject and contract-repository
// allowlists (which the verification and ingestion layers already ENFORCE but the
// filename-only loader could never express).
type trustConfig struct {
	APIVersion string           `yaml:"apiVersion"`
	Keys       []trustConfigKey `yaml:"keys"`
}

type trustConfigKey struct {
	KeyID                string   `yaml:"keyId"`
	ProducerID           string   `yaml:"producerId"`
	PublicKeyFile        string   `yaml:"publicKeyFile"`
	AllowedSubjects      []string `yaml:"allowedSubjects"`
	AllowedContractRepos []string `yaml:"allowedContractRepos"`
}

// loadStructuredTrustStore parses a versioned trust-config file into a
// MapTrustStore, populating each key's subject and contract-repo allowlists.
// publicKeyFile paths resolve relative to the config file's directory (a mounted
// Kubernetes Secret whose data is the config plus the .pub files just works). It
// validates the schema version, identifier grammar, duplicate/contradictory
// bindings, missing key files and malformed subject/repo patterns.
func loadStructuredTrustStore(configPath string) (evidenceenvelope.MapTrustStore, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg trustConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("trust config %q: %w", configPath, err)
	}
	if cfg.APIVersion != trustConfigAPIVersion {
		return nil, fmt.Errorf("trust config %q: unsupported apiVersion %q (want %q)", configPath, cfg.APIVersion, trustConfigAPIVersion)
	}
	if len(cfg.Keys) == 0 {
		return nil, fmt.Errorf("trust config %q: no keys configured", configPath)
	}
	dir := filepath.Dir(configPath)
	ts := evidenceenvelope.MapTrustStore{}
	pubToProducer := map[string]string{}
	for _, k := range cfg.Keys {
		entry, err := trustEntryFromConfigKey(k, dir, ts, pubToProducer)
		if err != nil {
			return nil, fmt.Errorf("trust config %q: %w", configPath, err)
		}
		ts[k.KeyID] = entry
	}
	return ts, nil
}

// trustEntryFromConfigKey validates one config key and builds its TrustEntry,
// recording its public-key/producer binding in pubToProducer for contradiction
// detection. It returns an error for a bad identifier, a duplicate key id, a
// contradictory producer binding, a missing/traversing key file, or a malformed
// subject/repo pattern.
func trustEntryFromConfigKey(k trustConfigKey, dir string, ts evidenceenvelope.MapTrustStore, pubToProducer map[string]string) (evidenceenvelope.TrustEntry, error) {
	if err := validateKeyIdent("key id", k.KeyID); err != nil {
		return evidenceenvelope.TrustEntry{}, err
	}
	if err := validateKeyIdent("producer", k.ProducerID); err != nil {
		return evidenceenvelope.TrustEntry{}, fmt.Errorf("key %q: %w", k.KeyID, err)
	}
	if _, dup := ts[k.KeyID]; dup {
		return evidenceenvelope.TrustEntry{}, fmt.Errorf("duplicate key id %q", k.KeyID)
	}
	if k.PublicKeyFile == "" {
		return evidenceenvelope.TrustEntry{}, fmt.Errorf("key %q: publicKeyFile is required", k.KeyID)
	}
	if k.PublicKeyFile != filepath.Base(k.PublicKeyFile) || strings.Contains(k.PublicKeyFile, "..") {
		return evidenceenvelope.TrustEntry{}, fmt.Errorf("key %q: publicKeyFile %q must be a bare filename in the config directory", k.KeyID, k.PublicKeyFile)
	}
	if prev, ok := pubToProducer[k.PublicKeyFile]; ok && prev != k.ProducerID {
		return evidenceenvelope.TrustEntry{}, fmt.Errorf("public key %q is bound to contradictory producers %q and %q", k.PublicKeyFile, prev, k.ProducerID)
	}
	pubToProducer[k.PublicKeyFile] = k.ProducerID
	pub, err := loadPublicKey(filepath.Join(dir, k.PublicKeyFile))
	if err != nil {
		return evidenceenvelope.TrustEntry{}, fmt.Errorf("key %q: %w", k.KeyID, err)
	}
	for _, pat := range k.AllowedSubjects {
		if _, err := path.Match(pat, ""); err != nil {
			return evidenceenvelope.TrustEntry{}, fmt.Errorf("key %q: malformed subject pattern %q: %w", k.KeyID, pat, err)
		}
	}
	for _, repo := range k.AllowedContractRepos {
		if err := validateContractRepoPrefix(repo); err != nil {
			return evidenceenvelope.TrustEntry{}, fmt.Errorf("key %q: %w", k.KeyID, err)
		}
	}
	return evidenceenvelope.TrustEntry{
		PublicKey:     pub,
		ProducerID:    k.ProducerID,
		Subjects:      k.AllowedSubjects,
		ContractRepos: k.AllowedContractRepos,
	}, nil
}

// validateContractRepoPrefix rejects a malformed contract-repo allowlist prefix:
// it must be a bare registry/repo path (no scheme, no digest, no whitespace).
func validateContractRepoPrefix(prefix string) error {
	switch {
	case prefix == "":
		return fmt.Errorf("contract-repo prefix must not be empty")
	case strings.ContainsAny(prefix, " \t\n"):
		return fmt.Errorf("contract-repo prefix %q must not contain whitespace", prefix)
	case strings.Contains(prefix, "oci://"), strings.Contains(prefix, "@"):
		return fmt.Errorf("contract-repo prefix %q must be a bare registry/repo path (no scheme or digest)", prefix)
	}
	return nil
}
