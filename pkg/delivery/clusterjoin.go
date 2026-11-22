package delivery

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// maxAdvertisedEnvironments bounds the environments a join block names, and
// maxEnvironmentSource the length of each one's source.
const (
	maxAdvertisedEnvironments = 16
	maxEnvironmentSource      = 200
)

// SnapshotJoin is the join block of a target snapshot: what a hub shows a
// person deciding whether to approve the cluster as a member, and the key
// the snapshot is signed with.
type SnapshotJoin struct {
	// Name is the cluster's display name, an RFC 1123 label; never an
	// identity.
	Name string `json:"name"`
	// NameSource says how Name was found: a fleet.ClusterNameFrom* value
	// other than approval.
	NameSource string `json:"nameSource"`
	// KeyID is the hex sha256 of PublicKey's DER: its signing.Hint.
	KeyID string `json:"keyID"`
	// PublicKey is the base64 PKIX DER of the installation's Ed25519 public
	// key.
	PublicKey string `json:"publicKey"`
	// Environments are the environments the cluster seems to host, inferred.
	Environments []AdvertisedEnvironment `json:"environments,omitempty"`
}

// AdvertisedEnvironment is one environment a cluster seems to host and
// where that was inferred from.
type AdvertisedEnvironment struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// snapshotNameSources are the name sources a join block may claim.
var snapshotNameSources = []string{fleet.ClusterNameFromNodeLabel, fleet.ClusterNameFromContext, fleet.ClusterNameFromFile, fleet.ClusterNameFromUID}

// checkJoin refuses a join block whose key id is not the fingerprint of its
// Ed25519 key, whose name or an environment name is not a DNS label, whose
// name source is unknown or approval, with over 16 environments or a source
// that is empty, over 200 bytes or not printable ASCII.
func checkJoin(j SnapshotJoin) error {
	if err := checkKeyID(j.PublicKey, j.KeyID); err != nil {
		return err
	}
	if !dnsLabel(j.Name) {
		return fmt.Errorf("name %q is not a DNS label", j.Name)
	}
	if !slices.Contains(snapshotNameSources, j.NameSource) {
		return fmt.Errorf("name source %q: want one of %s", j.NameSource, strings.Join(snapshotNameSources, ", "))
	}
	if len(j.Environments) > maxAdvertisedEnvironments {
		return fmt.Errorf("%d environments, over the %d bound", len(j.Environments), maxAdvertisedEnvironments)
	}
	for _, e := range j.Environments {
		if !dnsLabel(e.Name) {
			return fmt.Errorf("environment name %q is not a DNS label", e.Name)
		}
		if e.Source == "" || len(e.Source) > maxEnvironmentSource || strings.ContainsFunc(e.Source, func(r rune) bool { return r < ' ' || r > '~' }) {
			return fmt.Errorf("environment %s: source must be 1 to %d printable ASCII characters", e.Name, maxEnvironmentSource)
		}
	}
	return nil
}

// checkKeyID refuses a publicKey that is not the base64 PKIX DER of an
// Ed25519 key, and a keyID that is not the hex sha256 of that DER.
func checkKeyID(publicKey, keyID string) error {
	der, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return errors.New("public key is not base64")
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	if _, ok := pub.(ed25519.PublicKey); !ok {
		return fmt.Errorf("public key is a %T, want Ed25519", pub)
	}
	if sum := sha256.Sum256(der); keyID != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("key id %q is not the sha256 of the public key", keyID)
	}
	return nil
}

// dnsLabelPattern is an RFC 1123 label.
var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// dnsLabel reports whether s is an RFC 1123 label: at most 63 lower-case
// letters, digits or dashes, starting and ending alphanumeric. No homoglyph,
// bidi control or shell metacharacter fits.
func dnsLabel(s string) bool { return len(s) <= 63 && dnsLabelPattern.MatchString(s) }

// environmentTokens are the cluster-name tokens that name an environment.
var environmentTokens = []string{"prod", "production", "staging", "stage", "dev", "development", "test", "qa"}

// AdvertisedEnvironments are the environments a cluster seems to host,
// inferred: each distinct EnvironmentLabel value on its Pactos (by name), then
// each environment token of its name unless the name is the uid's. Values
// that are not DNS labels are dropped; at most 16 are kept, labels first.
func AdvertisedEnvironments(pactos []PactoView, name, nameSource string) []AdvertisedEnvironment {
	counts := map[string]int{}
	for _, p := range pactos {
		if env := p.Metadata.Labels[EnvironmentLabel]; dnsLabel(env) {
			counts[env]++
		}
	}
	var out []AdvertisedEnvironment
	for _, env := range slices.Sorted(maps.Keys(counts)) {
		noun := "Pactos"
		if counts[env] == 1 {
			noun = "Pacto"
		}
		out = append(out, AdvertisedEnvironment{Name: env,
			Source: fmt.Sprintf("label %s on %d %s", EnvironmentLabel, counts[env], noun)})
	}
	if nameSource != fleet.ClusterNameFromUID {
		for _, tok := range strings.Split(name, "-") {
			if slices.Contains(environmentTokens, tok) && counts[tok] == 0 {
				counts[tok] = -1 // named once
				out = append(out, AdvertisedEnvironment{Name: tok, Source: "the cluster name " + name})
			}
		}
	}
	return out[:min(len(out), maxAdvertisedEnvironments)]
}
