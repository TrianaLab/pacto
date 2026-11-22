package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

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

// PredicateClusterJoin is the predicate of a person's signed decision to
// approve a cluster as a member of a hub, or to remove it.
const PredicateClusterJoin = "delivery.pacto.trianalab.io/cluster-join/v1"

// MaxJoinRecords bounds the distinct join records a hub reads per cluster.
const MaxJoinRecords = 20

// ClusterJoinStream is the record stream a cluster's join records sit on,
// beside its target snapshot.
func ClusterJoinStream(uid string) string {
	return uid + "/" + TargetSnapshotRepository + "/join"
}

// JoinDecision is what a join record decides.
type JoinDecision string

const (
	// JoinApprove approves the member's key.
	JoinApprove JoinDecision = "approve"
	// JoinRemove removes the member.
	JoinRemove JoinDecision = "remove"
)

// ClusterJoin is a person's decision on a cluster joining a hub.
type ClusterJoin struct {
	// Cluster is the member's UID; Hub the hub cluster's UID.
	Cluster string `json:"cluster"`
	Hub     string `json:"hub"`
	// KeyID and PublicKey are the member key the person checked, as the
	// snapshot's join block names them.
	KeyID     string       `json:"keyID"`
	PublicKey string       `json:"publicKey"`
	Decision  JoinDecision `json:"decision"`
	// Name optionally overrides the member's display name, a DNS label.
	Name string `json:"name,omitempty"`
	// Approver is the Kubernetes user the person's certificate maps to.
	Approver string `json:"approver"`
	// At is when the person says they signed (RFC 3339): display only.
	At string `json:"at"`
}

// Check refuses a record a hub could not place: a cluster that cannot sit in
// a tag, no hub, approver or key, a key id that is not the fingerprint of an
// Ed25519 key, a name that is not a DNS label or an unknown decision.
func (j ClusterJoin) Check() error {
	if _, err := TargetSnapshotTag(j.Cluster); err != nil {
		return err
	}
	switch {
	case j.Hub == "":
		return errors.New("no hub")
	case j.Approver == "":
		return errors.New("no approver")
	case j.Name != "" && !dnsLabel(j.Name):
		return fmt.Errorf("name %q is not a DNS label", j.Name)
	case j.Decision != JoinApprove && j.Decision != JoinRemove:
		return fmt.Errorf("decision %q: want approve or remove", j.Decision)
	}
	return checkKeyID(j.PublicKey, j.KeyID)
}

// ClusterJoinStatement is an in-toto Statement v1 carrying a join record.
type ClusterJoinStatement struct {
	Type          string      `json:"_type"`
	Subject       []Subject   `json:"subject"`
	PredicateType string      `json:"predicateType"`
	Predicate     ClusterJoin `json:"predicate"`
}

// NewClusterJoinStatement builds the statement of j: its subject is the
// member's snapshot tag, by the sha256 of the member key's DER (its key id).
func NewClusterJoinStatement(j ClusterJoin) ClusterJoinStatement {
	return ClusterJoinStatement{Type: StatementType,
		Subject:       []Subject{{Name: TargetSnapshotRepository + ":" + TargetSnapshotTagPrefix + j.Cluster, Digest: map[string]string{"sha256": j.KeyID}}},
		PredicateType: PredicateClusterJoin, Predicate: j}
}

// SignClusterJoin signs st and returns the bundle.
func SignClusterJoin(ctx context.Context, s Signer, st ClusterJoinStatement) ([]byte, error) {
	bundle, _, err := signStatement(ctx, s, st)
	return bundle, err
}

// OpenClusterJoin verifies a join record bundle with verify and returns its
// statement, once Check accepts the record and its subject is the snapshot
// tag of the cluster and the key it names.
func OpenClusterJoin(bundle []byte, verify func(bundle []byte) error) (ClusterJoinStatement, error) {
	var st ClusterJoinStatement
	if _, err := openStatement(bundle, PredicateClusterJoin, &st); err != nil {
		return ClusterJoinStatement{}, err
	}
	if err := verify(bundle); err != nil {
		return ClusterJoinStatement{}, fmt.Errorf("signature: %w", err)
	}
	if err := st.Predicate.Check(); err != nil {
		return ClusterJoinStatement{}, err
	}
	want := NewClusterJoinStatement(st.Predicate).Subject
	if !slices.EqualFunc(st.Subject, want, func(a, b Subject) bool { return a.Name == b.Name && maps.Equal(a.Digest, b.Digest) }) {
		return ClusterJoinStatement{}, fmt.Errorf("its subject is not %s keyed sha256:%s", want[0].Name, st.Predicate.KeyID)
	}
	return st, nil
}

// JoinCandidate is one cluster-<uid> snapshot a hub read for joining.
type JoinCandidate struct {
	UID string
	// Join is the snapshot's join block; nil when it carries none.
	Join *SnapshotJoin
	// SelfSigned says the key Join names signed the snapshot.
	SelfSigned bool
	AsOf       string
	Status     fleet.SourceStatus
	// Ref is <repository>@<digest> of the snapshot read.
	Ref string
	// Err says why the snapshot could not be used; empty when it was.
	Err string
}

// JoinRecords are the join records read for one cluster: verified (or
// refused, with why), whether the listing was cut or failed, and the tags
// it could not use (Forks).
type JoinRecords struct {
	Verified []VerifiedJoin
	Cut      bool
	Err      string
	Forks    []string
}

// VerifiedJoin is one join record and whether it counts: its signature
// verified under an approver signer, its certificate maps to Record.Approver
// and the hub lets that user approve the cluster. Why says why it does not
// count. Verified says the signature and the identity held, whether or not
// the review then counted it.
type VerifiedJoin struct {
	Record   ClusterJoin
	User     string
	Ref      string
	SignedAt time.Time
	Verified bool
	Counted  bool
	Why      string
}

// ClusterRef names a cluster.
type ClusterRef struct {
	UID  string `json:"uid"`
	Name string `json:"name,omitempty"`
}

// JoinState is where a cluster stands with a hub.
type JoinState string

const (
	JoinPending  JoinState = "pending"
	JoinApproved JoinState = "approved"
	JoinRemoved  JoinState = "removed"
)

// ClusterJoinState is one cluster a hub read from its registry and where it
// stands.
type ClusterJoinState struct {
	UID string `json:"uid"`
	// Name is the display name and NameSource where it came from: the
	// snapshot's, or approval for a person's override.
	Name       string `json:"name,omitempty"`
	NameSource string `json:"nameSource,omitempty" enum:"approval,node-label,context,file,uid"`
	// KeyID and PublicKey are the key the snapshot names and is signed by:
	// empty when its own key did not sign it, so it is never approvable.
	KeyID     string    `json:"keyID,omitempty"`
	PublicKey string    `json:"publicKey,omitempty"`
	State     JoinState `json:"state" enum:"pending,approved,removed"`
	Reason    string    `json:"reason,omitempty"`
	// Environments are the environments the member seems to host: inferred.
	Environments []AdvertisedEnvironment `json:"environments,omitempty"`
	// Approver, ApprovedAt and Record name the record that decided.
	Approver   string `json:"approver,omitempty"`
	ApprovedAt string `json:"approvedAt,omitempty"`
	Record     string `json:"record,omitempty"`
	// ApprovedKeyID and ApprovedPublicKey are the key the newest counted
	// approval names, which a removal revokes even when the snapshot is gone.
	ApprovedKeyID     string             `json:"approvedKeyID,omitempty"`
	ApprovedPublicKey string             `json:"approvedPublicKey,omitempty"`
	SnapshotAsOf      string             `json:"snapshotAsOf,omitempty"`
	Status            fleet.SourceStatus `json:"status,omitempty"`
	// MayReplace names the approved member this pending cluster shares a
	// display name with: a rebuilt cluster.
	MayReplace *ClusterRef `json:"mayReplace,omitempty"`
}

// ClusterJoinList is every cluster a hub read from its registry.
type ClusterJoinList struct {
	Hub          ClusterRef         `json:"hub"`
	Repository   string             `json:"repository"`
	AsOf         string             `json:"asOf"`
	Completeness fleet.Completeness `json:"completeness"`
	Limitations  []fleet.Limitation `json:"limitations,omitempty"`
	Clusters     []ClusterJoinState `json:"clusters"`
	// Hidden counts the rows the reader may not see; OtherHubs the verified
	// records that name another hub and were not used.
	Hidden    int `json:"hidden"`
	OtherHubs int `json:"otherHubs"`
	// CanApprove says the surface offers the reader the approve command:
	// the hub's review allowed them on a restricted surface, always on an
	// unrestricted one. pacto cluster approve checks the right itself.
	CanApprove bool `json:"canApprove"`
}

// JoinStates are the states of cands as hub reads them with recs (by UID):
// the newest counted record by verified signing time decides, a removal on a
// tie. A candidate
// with no usable join block, or whose own key did not sign it, a cut or
// failed record listing or no counted record is pending; an approval of
// another key than the snapshot's is pending. The hub itself is skipped.
// It also returns how many verified records name another hub.
func JoinStates(hub string, cands []JoinCandidate, recs map[string]JoinRecords) ([]ClusterJoinState, int) {
	var out []ClusterJoinState
	others := 0
	for _, c := range cands {
		if c.UID == hub {
			continue
		}
		st, n := decide(hub, c, recs[c.UID])
		others += n
		out = append(out, st)
	}
	return mayReplace(out), others
}

// decide is the state of c under its records rs, and how many of them name
// another hub.
func decide(hub string, c JoinCandidate, rs JoinRecords) (ClusterJoinState, int) {
	st := ClusterJoinState{UID: c.UID, State: JoinPending, SnapshotAsOf: c.AsOf, Status: c.Status}
	if c.Join != nil {
		j := c.Join
		st.Name, st.NameSource, st.Environments = j.Name, j.NameSource, j.Environments
		if c.SelfSigned {
			st.KeyID, st.PublicKey = j.KeyID, j.PublicKey
		}
	}
	n := counted(hub, c.UID, rs.Verified)
	if a := n.newest; a != nil && a.Record.Decision == JoinApprove {
		st.ApprovedKeyID, st.ApprovedPublicKey = a.Record.KeyID, a.Record.PublicKey
	}
	whys := append(n.whys, rs.Forks...)
	if st.Reason = blocked(c, rs, n.newest); st.Reason == "" {
		st.Reason = ruled(&st, n)
	}
	st.Reason = strings.Join(slices.DeleteFunc(append([]string{st.Reason}, whys...), func(s string) bool { return s == "" }), "; ")
	return st, n.others
}

// blocked is why c is pending whatever its records say, or "": its record
// listing failed or was cut, its snapshot could not be used (a removal
// needs none) or it has no counted record.
func blocked(c JoinCandidate, rs JoinRecords, newest *VerifiedJoin) string {
	switch {
	case rs.Err != "":
		return "its join records could not be read: " + rs.Err
	case rs.Cut:
		return fmt.Sprintf("more than %d join records; the newest could not be read", MaxJoinRecords)
	case newest != nil && newest.Record.Decision == JoinRemove:
		return ""
	case c.Err != "":
		return c.Err
	case c.Join == nil:
		return "the snapshot carries no join block"
	case !c.SelfSigned:
		return "the snapshot is not signed by the key it names"
	case newest == nil:
		return "no approval yet"
	}
	return ""
}

// ruled sets st from the newest counted record and returns why st stays
// pending, or "": a removal removes; a newer removal that verified but was
// not counted (its review failed or its signer may no longer approve) keeps
// it pending; an approval of another key is pending.
func ruled(st *ClusterJoinState, n joinCount) string {
	newest := n.newest
	at := newest.SignedAt.UTC().Format(time.RFC3339)
	switch {
	case newest.Record.Decision == JoinRemove:
		st.State, st.Approver, st.ApprovedAt, st.Record = JoinRemoved, newest.User, at, newest.Ref
	case n.removal != nil && n.removal.SignedAt.After(newest.SignedAt):
		return fmt.Sprintf("a newer removal by %s was not counted, so the approval by %s no longer decides: approve again to restore it", n.removal.User, newest.User)
	case newest.Record.KeyID != st.KeyID:
		return fmt.Sprintf("the key changed from %s since %s approved it on %s", short("sha256:"+newest.Record.KeyID), newest.User, newest.SignedAt.UTC().Format(time.DateOnly))
	default:
		st.State, st.Approver, st.ApprovedAt, st.Record = JoinApproved, newest.User, at, newest.Ref
		if newest.Record.Name != "" {
			st.Name, st.NameSource = newest.Record.Name, fleet.ClusterNameFromApproval
		}
	}
	return ""
}

// joinCount is what the records of one cluster say for a hub: the newest
// counted record, the newest removal that verified but was not counted, the
// reasons the records that do not count give, and how many name another hub.
type joinCount struct {
	newest, removal *VerifiedJoin
	whys            []string
	others          int
}

// counted reads the records vs of uid for hub.
func counted(hub, uid string, vs []VerifiedJoin) joinCount {
	var n joinCount
	for i, v := range vs {
		switch {
		case !v.Counted:
			n.whys = append(n.whys, v.Why)
			if v.Verified && v.Record.Hub == hub && v.Record.Cluster == uid && v.Record.Decision == JoinRemove && (n.removal == nil || v.SignedAt.After(n.removal.SignedAt)) {
				n.removal = &vs[i]
			}
		case v.Record.Hub != hub:
			n.others++
		case v.Record.Cluster != uid:
			n.whys = append(n.whys, "a record by "+v.User+" names cluster "+v.Record.Cluster+" and was not counted")
		case n.newest == nil || v.SignedAt.After(n.newest.SignedAt),
			v.SignedAt.Equal(n.newest.SignedAt) && v.Record.Decision == JoinRemove: // a tie removes
			n.newest = &vs[i]
		}
	}
	return n
}

// mayReplace marks each pending state whose snapshot name (not the uid's)
// is the display name of an approved member.
func mayReplace(states []ClusterJoinState) []ClusterJoinState {
	approved := map[string]string{}
	for _, s := range states {
		if s.State == JoinApproved {
			approved[s.Name] = s.UID
		}
	}
	for i, s := range states {
		if uid, ok := approved[s.Name]; ok && s.State == JoinPending && s.NameSource != fleet.ClusterNameFromUID {
			states[i].MayReplace = &ClusterRef{UID: uid, Name: s.Name}
		}
	}
	return states
}

// JoinTrust is the trust config approved members' snapshots verify with:
// each approved key, named member:<name> and scoped to its member's UID.
func JoinTrust(states []ClusterJoinState) *TrustConfig {
	t := &TrustConfig{}
	for _, s := range states {
		if s.State != JoinApproved {
			continue
		}
		der, _ := base64.StdEncoding.DecodeString(s.PublicKey) // checked with the snapshot
		t.Keys = append(t.Keys, TrustedKey{Name: "member:" + s.Name, Streams: []string{s.UID + "/*/*"},
			PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))})
	}
	return t
}
