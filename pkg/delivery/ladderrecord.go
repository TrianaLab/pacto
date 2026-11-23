package delivery

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// PredicateEnvironmentLadder is the predicate of a person's signed
// confirmation of the installation ladder of one cluster: the declared chain
// as it stood and the proposed rungs the person kept.
const PredicateEnvironmentLadder = "delivery.pacto.trianalab.io/environment-ladder/v1"

// RungConfirmed: a person confirmed the proposed rung in a signed ladder
// record.
const RungConfirmed = "confirmed"

// MaxLadderRecords bounds the distinct ladder records a promoter reads per
// pass.
const MaxLadderRecords = 20

// ladderClockSkew is how far a ladder record's At may be from the time a
// timestamp authority or the log saw it signed.
const ladderClockSkew = 5 * time.Minute

// LadderStream is the record stream a cluster's ladder records sit on, in the
// approvals repository.
func LadderStream(clusterID string) string { return clusterID + "/pacto-delivery/ladder" }

// LadderRecord is a person's confirmation of a cluster's installation ladder:
// the declared chain first (provenance declared, each rung fed by the one
// before), then the proposed rungs the person kept, each a leaf. It binds the
// digest of the proposal the person saw and names the rungs they dropped.
type LadderRecord struct {
	ClusterID      string            `json:"clusterID"`
	Rungs          []LadderRung      `json:"rungs"`
	ProposalDigest string            `json:"proposalDigest"`
	Dropped        []string          `json:"dropped,omitempty"`
	Signer         PublisherIdentity `json:"signer"`
	// At is when the person says they signed (RFC 3339). It never orders
	// records: the verified signing time does.
	At string `json:"at"`
}

// proposalDigestPattern is a ProposalDigest.
var proposalDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Check refuses a record a promoter could not use: no cluster, signer or
// rung, a name that is not a DNS label or is named twice, a declared chain
// that is not a chain or does not come first, a kept rung with an upstream
// (an inferred rung is always a leaf), over 16 rungs of either kind, a
// proposal digest that is not sha256:<64 hex>, a dropped rung that is kept,
// and an At that is not RFC 3339 or is more than 5 minutes from signedAt, the
// time a timestamp authority or the log saw it signed.
func (l LadderRecord) Check(signedAt time.Time) error {
	switch {
	case l.ClusterID == "":
		return errors.New("no cluster")
	case l.Signer.Issuer == "" || l.Signer.Subject == "":
		return errors.New("no signer")
	case len(l.Rungs) == 0:
		return errors.New("no rung")
	case !proposalDigestPattern.MatchString(l.ProposalDigest):
		return fmt.Errorf("proposal digest %q is not sha256:<64 hex>", l.ProposalDigest)
	}
	if err := checkRungs(l.Rungs); err != nil {
		return err
	}
	if err := checkDropped(l.Dropped, l.Rungs); err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339, l.At)
	if err != nil {
		return fmt.Errorf("at %q is not RFC 3339", l.At)
	}
	if d := at.Sub(signedAt); d > ladderClockSkew || d < -ladderClockSkew {
		return fmt.Errorf("at %s is more than %s from the verified signing time %s", l.At, ladderClockSkew, signedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

// checkRungs refuses rungs whose declared chain does not come first, is not a
// chain or holds over 16 rungs, whose kept rungs are not leaves or number
// over 16, or that name a rung twice or not as a DNS label.
func checkRungs(rungs []LadderRung) error {
	seen := map[string]bool{}
	chain, kept := 0, 0
	for i, r := range rungs {
		if !dnsLabel(r.Name) || seen[r.Name] {
			return fmt.Errorf("rung %q is not a DNS label or is named twice", r.Name)
		}
		seen[r.Name] = true
		upstream := ""
		if i > 0 {
			upstream = rungs[i-1].Name
		}
		if err := checkRung(r, upstream, kept > 0); err != nil {
			return err
		}
		if r.Provenance == RungDeclared {
			chain++
		} else {
			kept++
		}
	}
	if chain > maxProposedRungs || kept > maxProposedRungs {
		return fmt.Errorf("%d declared and %d kept rungs, over the %d bound", chain, kept, maxProposedRungs)
	}
	return nil
}

// checkRung refuses a declared rung after a kept one (afterKept) or not fed
// by the rung before it (before, "" for the first), and a kept rung that is
// not an inferred leaf.
func checkRung(r LadderRung, before string, afterKept bool) error {
	switch {
	case r.Provenance == RungDeclared && afterKept:
		return fmt.Errorf("declared rung %s follows a kept rung", r.Name)
	case r.Provenance == RungDeclared && r.Upstream != before:
		return fmt.Errorf("declared rung %s does not take its versions from the rung before it", r.Name)
	case r.Provenance == RungDeclared:
		return nil
	case r.Provenance != RungInferred:
		return fmt.Errorf("rung %s: provenance %q, want declared or inferred", r.Name, r.Provenance)
	case r.Upstream != "":
		return fmt.Errorf("kept rung %s takes its versions from %s: a proposed rung is always a leaf", r.Name, r.Upstream)
	}
	return nil
}

// checkDropped refuses over 16 dropped rungs, one that is not a DNS label
// and one that is also kept.
func checkDropped(dropped []string, rungs []LadderRung) error {
	if len(dropped) > maxProposedRungs {
		return fmt.Errorf("%d dropped rungs, over the %d bound", len(dropped), maxProposedRungs)
	}
	for _, d := range dropped {
		if !dnsLabel(d) || slices.ContainsFunc(rungs, func(r LadderRung) bool { return r.Name == d }) {
			return fmt.Errorf("dropped rung %q is not a DNS label or is also kept", d)
		}
	}
	return nil
}

// LadderStatement is an in-toto Statement v1 carrying a ladder record. Its
// subject is the sha256 of the cluster's ladder stream id.
type LadderStatement struct {
	Type          string       `json:"_type"`
	Subject       []Subject    `json:"subject"`
	PredicateType string       `json:"predicateType"`
	Predicate     LadderRecord `json:"predicate"`
}

// NewLadderStatement builds the statement of l: its subject is the ladder
// stream of the cluster it names.
func NewLadderStatement(l LadderRecord) LadderStatement {
	return LadderStatement{Type: StatementType, Subject: []Subject{ladderSubject(l.ClusterID)},
		PredicateType: PredicateEnvironmentLadder, Predicate: l}
}

// SignLadder signs st and returns the bundle.
func SignLadder(ctx context.Context, s Signer, st LadderStatement) ([]byte, error) {
	bundle, _, err := signStatement(ctx, s, st)
	return bundle, err
}

// ladderSubject is the subject of the ladder records of clusterID.
func ladderSubject(clusterID string) Subject {
	sum := sha256.Sum256([]byte(LadderStream(clusterID)))
	return Subject{Name: "ladder", Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])}}
}

// OpenLadderRecord verifies a ladder record bundle with verify, which returns
// the time a timestamp authority or the log saw it signed, and returns its
// statement and that time, once Check accepts the record at that time and its
// subject is the ladder stream of the cluster it names.
func OpenLadderRecord(bundle []byte, verify func(bundle []byte) (time.Time, error)) (LadderStatement, time.Time, error) {
	var st LadderStatement
	if _, err := openStatement(bundle, PredicateEnvironmentLadder, &st); err != nil {
		return LadderStatement{}, time.Time{}, err
	}
	signedAt, err := verify(bundle)
	if err != nil {
		return LadderStatement{}, time.Time{}, fmt.Errorf("signature: %w", err)
	}
	if err := st.Predicate.Check(signedAt); err != nil {
		return LadderStatement{}, time.Time{}, err
	}
	want := ladderSubject(st.Predicate.ClusterID)
	if len(st.Subject) != 1 || st.Subject[0].Name != want.Name || st.Subject[0].Digest["sha256"] != want.Digest["sha256"] || len(st.Subject[0].Digest) != 1 {
		return LadderStatement{}, time.Time{}, fmt.Errorf("its subject is not the ladder stream of %s", st.Predicate.ClusterID)
	}
	return st, signedAt, nil
}

// VerifiedLadder is one ladder record that counts: its signature verified
// under an approver signer, its certificate maps to User, a person, and the
// cluster lets that user approve environmentladders. SignedAt is the time a
// timestamp authority or the log saw it signed; Ref is where it is.
type VerifiedLadder struct {
	Record   LadderRecord
	User     string
	Ref      string
	SignedAt time.Time
}

// ConfirmedLadder is the rungs the newest counted record of clusterID (by
// verified signing time, never by At) confirmed, each a leaf with provenance
// RungConfirmed, the record they come from and a note. declared, the chart's
// chain, always wins: a kept rung declared since is dropped, and a record
// signed over another chain keeps its kept rungs and says the chain changed.
// Records of another cluster never count. No counted record confirms nothing.
func ConfirmedLadder(records []VerifiedLadder, clusterID string, declared []string) ([]LadderRung, *VerifiedLadder, string) {
	var from *VerifiedLadder
	for i := range records {
		if r := &records[i]; r.Record.ClusterID == clusterID && (from == nil || r.SignedAt.After(from.SignedAt)) {
			from = r
		}
	}
	if from == nil {
		return nil, nil, ""
	}
	taken := map[string]bool{}
	for _, d := range declared {
		taken[canonicalEnvironment(d)] = true
	}
	var chain []string
	var rungs []LadderRung
	for _, r := range from.Record.Rungs {
		switch {
		case r.Provenance == RungDeclared:
			chain = append(chain, r.Name)
		case !taken[canonicalEnvironment(r.Name)]:
			rungs = append(rungs, LadderRung{Name: r.Name, Source: r.Source, Provenance: RungConfirmed})
		}
	}
	note := ""
	if !slices.Equal(chain, declared) {
		note = fmt.Sprintf("the installation ladder %s confirmed was %s; the chart value delivery.environments.ladder now names %s, which wins",
			from.User, cmp.Or(strings.Join(chain, " -> "), "empty"), strings.Join(declared, " -> "))
	}
	return rungs, from, note
}
