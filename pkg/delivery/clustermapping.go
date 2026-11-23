package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// PredicateClusterGroupMapping is the predicate of a person's signed group
// mapping for one member and one OIDC issuer: which member groups a hub
// reader's groups stand for when the member's apiserver does not accept the
// hub's tokens. It answers observation reads only, never approvals.
const PredicateClusterGroupMapping = "delivery.pacto.trianalab.io/cluster-group-mapping/v1"

// MaxGroupRules bounds the rules one group mapping holds.
const MaxGroupRules = 64

// GroupRule maps the hub group From to the member group To.
type GroupRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ClusterGroupMapping is a person's group mapping for a member and an issuer.
// A mapping with no rules clears the issuer's mapping.
type ClusterGroupMapping struct {
	// Cluster is the member's UID; Hub the hub cluster's UID.
	Cluster string `json:"cluster"`
	Hub     string `json:"hub"`
	// Issuer is the OIDC issuer whose people the rules map.
	Issuer string      `json:"issuer"`
	Rules  []GroupRule `json:"rules,omitempty"`
	// Approver is the Kubernetes user the person's certificate maps to.
	Approver string `json:"approver"`
	// At is when the person says they signed (RFC 3339): display only.
	At string `json:"at"`
}

// Validate refuses a mapping a hub could not place or must never send: a
// cluster that cannot sit in a tag, no hub or approver, an issuer that is
// not an https URL, over 64 rules, a rule with no hub group, or a member
// group that is empty or starts with system:, which would hand a mapped
// reader a cluster-local identity.
func (m ClusterGroupMapping) Validate() error {
	if _, err := TargetSnapshotTag(m.Cluster); err != nil {
		return err
	}
	u, err := url.Parse(m.Issuer)
	switch {
	case m.Hub == "":
		return errors.New("no hub")
	case m.Approver == "":
		return errors.New("no approver")
	case err != nil || u.Scheme != "https" || u.Host == "":
		return fmt.Errorf("issuer %q is not an https URL", m.Issuer)
	case len(m.Rules) > MaxGroupRules:
		return fmt.Errorf("%d rules, over the %d bound", len(m.Rules), MaxGroupRules)
	}
	for _, r := range m.Rules {
		switch {
		case r.From == "":
			return errors.New("a rule maps no hub group")
		case r.To == "":
			return fmt.Errorf("the rule for %s maps to no group", r.From)
		case strings.HasPrefix(r.To, "system:"):
			return fmt.Errorf("the rule for %s maps to %s, and a member group may not start with system", r.From, r.To)
		}
	}
	return nil
}

// Apply is the member groups groups stand for: the sorted, deduplicated To
// of every rule whose From is in groups.
func (m ClusterGroupMapping) Apply(groups []string) []string {
	out := []string{}
	for _, r := range m.Rules {
		if slices.Contains(groups, r.From) {
			out = append(out, r.To)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ClusterGroupMappingStatement is an in-toto Statement v1 carrying a group
// mapping.
type ClusterGroupMappingStatement struct {
	Type          string              `json:"_type"`
	Subject       []Subject           `json:"subject"`
	PredicateType string              `json:"predicateType"`
	Predicate     ClusterGroupMapping `json:"predicate"`
}

// NewClusterGroupMappingStatement builds the statement of m: its subject is
// the member's snapshot tag, by the sha256 of the issuer it maps.
func NewClusterGroupMappingStatement(m ClusterGroupMapping) ClusterGroupMappingStatement {
	sum := sha256.Sum256([]byte(m.Issuer))
	return ClusterGroupMappingStatement{Type: StatementType,
		Subject:       []Subject{{Name: TargetSnapshotRepository + ":" + TargetSnapshotTagPrefix + m.Cluster, Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])}}},
		PredicateType: PredicateClusterGroupMapping, Predicate: m}
}

// SignClusterGroupMapping signs st and returns the bundle.
func SignClusterGroupMapping(ctx context.Context, s Signer, st ClusterGroupMappingStatement) ([]byte, error) {
	bundle, _, err := signStatement(ctx, s, st)
	return bundle, err
}

// OpenClusterGroupMapping verifies a group mapping bundle with verify and
// returns its statement, once Validate accepts the mapping and its subject
// is the snapshot tag of the cluster and the issuer it names.
func OpenClusterGroupMapping(bundle []byte, verify func(bundle []byte) error) (ClusterGroupMappingStatement, error) {
	var st ClusterGroupMappingStatement
	if _, err := openStatement(bundle, PredicateClusterGroupMapping, &st); err != nil {
		return ClusterGroupMappingStatement{}, err
	}
	if err := verify(bundle); err != nil {
		return ClusterGroupMappingStatement{}, fmt.Errorf("signature: %w", err)
	}
	if err := st.Predicate.Validate(); err != nil {
		return ClusterGroupMappingStatement{}, err
	}
	want := NewClusterGroupMappingStatement(st.Predicate).Subject
	if !slices.EqualFunc(st.Subject, want, func(a, b Subject) bool { return a.Name == b.Name && maps.Equal(a.Digest, b.Digest) }) {
		return ClusterGroupMappingStatement{}, fmt.Errorf("its subject is not %s keyed by its issuer", want[0].Name)
	}
	return st, nil
}

// GroupMappingState is the group mapping a hub applies for one member and
// one issuer, and who approved it.
type GroupMappingState struct {
	Issuer     string      `json:"issuer"`
	Rules      []GroupRule `json:"rules"`
	ApprovedBy string      `json:"approvedBy"`
	// ApprovedAt is the verified signing time (RFC 3339).
	ApprovedAt string `json:"approvedAt"`
	// Record is the sha256 digest of the record's manifest.
	Record string `json:"record"`
}
