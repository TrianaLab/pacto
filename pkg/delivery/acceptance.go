package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// The acceptance record: a person accepts proposed dependency fields of a
// service, or a break a provider announced, with one signed statement
// attached to the accepting service's published bundle.
const (
	PredicateAcceptance    = "delivery.pacto.trianalab.io/acceptance/v1"
	AcceptanceArtifactType = "application/vnd.pacto.acceptance.v1+json"
	// AcceptanceDraftArtifactType is an acceptance a person has not signed
	// yet: an agent, CI or a key wrote it on someone's behalf.
	AcceptanceDraftArtifactType = "application/vnd.pacto.acceptance-draft.v1+json"
	// PredicateAcceptanceDraft is a signed acceptance draft: its signer asks,
	// it decides nothing.
	PredicateAcceptanceDraft = "delivery.pacto.trianalab.io/acceptance-draft/v1"
	// AcceptanceTagPrefix starts every write-once acceptance tag:
	// pacto-accept-<subject hex>-<12 hex of the manifest digest>.
	AcceptanceTagPrefix = "pacto-accept-"
	// AcceptanceDraftTagPrefix starts every acceptance draft tag,
	// pacto-accept-draft-<n>: never an acceptance tag, whose next part is hex.
	AcceptanceDraftTagPrefix = "pacto-accept-draft-"
	AcceptKindRanges         = "ranges"
	AcceptKindBreak          = "break"
	// MaxAcceptances is the most acceptance records a reader opens per
	// subject; MaxAcceptanceBytes the largest record layer it fetches.
	MaxAcceptances     = 50
	MaxAcceptanceBytes = 64 << 10
)

// AcceptanceRecord is delivery.pacto.trianalab.io/acceptance/v1.
type AcceptanceRecord struct {
	// Service is the accepting service's fleet key; Version the version
	// of the bundle the record is attached to.
	Service string `json:"service"`
	Version string `json:"version"`
	// Kind is ranges or break.
	Kind   string          `json:"kind"`
	Ranges []AcceptedRange `json:"ranges,omitempty"`
	Break  *AcceptedBreak  `json:"break,omitempty"`
	// Intent is AcceptanceIntent of the record and its subject.
	Intent string `json:"intent,omitempty"`
}

// AcceptedRange is one accepted field: its key, the accepted value, what
// Pacto proposed and where the proposal came from.
type AcceptedRange struct {
	Field    string `json:"field"`
	Value    string `json:"value"`
	Proposed string `json:"proposed,omitempty"`
	Source   string `json:"source,omitempty"`
}

// AcceptedBreak names the accepted break: the provider (fleet key), the
// version that breaks, the digest of its breaks[] entry naming the consumer
// (BreakAnnouncement) and, when that version is published, its bundle.
type AcceptedBreak struct {
	Provider     string `json:"provider"`
	Version      string `json:"version"`
	Announcement string `json:"announcement"`
	Bundle       string `json:"bundle,omitempty"`
}

// AcceptanceStatement is an in-toto Statement v1 carrying an acceptance.
// Its subject is the accepting service's bundle.
type AcceptanceStatement struct {
	Type          string           `json:"_type"`
	Subject       []Subject        `json:"subject"`
	PredicateType string           `json:"predicateType"`
	Predicate     AcceptanceRecord `json:"predicate"`
}

// NewAcceptanceStatement builds the statement accepting r on subject
// (<repository>@sha256:<hex>).
func NewAcceptanceStatement(subject string, r AcceptanceRecord) AcceptanceStatement {
	repo, dgst, _ := strings.Cut(subject, "@")
	algo, hex, _ := strings.Cut(dgst, ":")
	return AcceptanceStatement{Type: StatementType, Subject: []Subject{{Name: repo, Digest: map[string]string{algo: hex}}},
		PredicateType: PredicateAcceptance, Predicate: r}
}

// SignAcceptance signs st and returns the bundle and its id, as SignRecord does.
func SignAcceptance(ctx context.Context, s Signer, st AcceptanceStatement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}

// AcceptanceTag is the write-once tag of the acceptance manifest attached to
// subject, where the registry serves no Referrers API: the prefix, the
// subject's hex and 12 hex of the manifest digest (OCI tags stop at 128
// characters).
func AcceptanceTag(subject, manifest digest.Digest) string {
	return AcceptanceTagPrefix + subject.Encoded() + "-" + manifest.Encoded()[:12]
}

// AcceptanceDraft is an acceptance nobody who may decide has signed: the
// record it would sign, the subject, who it is on behalf of and who asked (the signer of
// the draft, empty when it is unsigned). The intent digest binds the record
// to Subject, so the person accepting it signs exactly this. A person accepts it with
// pacto accept --draft.
type AcceptanceDraft struct {
	Record AcceptanceRecord `json:"record"`
	// Subject is the bundle the record accepts for (<repository>@sha256:<hex>).
	Subject    string                  `json:"subject"`
	OnBehalfOf string                  `json:"onBehalfOf,omitempty"`
	Asker      contract.InferredSigner `json:"asker"`
}

// SignAcceptanceDraft signs d as an in-toto statement whose subject is the
// bundle d accepts for, and returns the bundle and its id.
func SignAcceptanceDraft(ctx context.Context, s Signer, d AcceptanceDraft) (bundle []byte, id string, err error) {
	st := NewAcceptanceStatement(d.Subject, AcceptanceRecord{})
	return signStatement(ctx, s, struct {
		Type          string          `json:"_type"`
		Subject       []Subject       `json:"subject"`
		PredicateType string          `json:"predicateType"`
		Predicate     AcceptanceDraft `json:"predicate"`
	}{st.Type, st.Subject, PredicateAcceptanceDraft, d})
}

// OpenAcceptance verifies an acceptance bundle with verify and returns its
// statement and id. A bundle that is not one signed
// delivery.pacto.trianalab.io/acceptance/v1 statement is an error.
func OpenAcceptance(bundle []byte, verify func(bundle []byte) error) (AcceptanceStatement, string, error) {
	var st AcceptanceStatement
	payload, err := openStatement(bundle, PredicateAcceptance, &st)
	if err != nil {
		return AcceptanceStatement{}, "", err
	}
	if err := verify(bundle); err != nil {
		return AcceptanceStatement{}, "", fmt.Errorf("signature: %w", err)
	}
	return st, sha256Hex(payload), nil
}

// AcceptanceIntent is the intent digest of accepting r on subject
// (<repository>@sha256:<hex>): the sha256 of r's canonical JSON without
// its Intent, then the subject. A plan and the record it signs agree on it.
func AcceptanceIntent(r AcceptanceRecord, subject string) string {
	r.Intent = ""
	b, _ := json.Marshal(r) // plain data; marshalling cannot fail
	return sha256Hex(append(append(b, '\n'), subject...))
}

// BreakAnnouncement is the digest of a provider's breaks[] entry: the
// sha256 of its canonical JSON with the consumers sorted, so their order
// does not matter and any other change does.
func BreakAnnouncement(b contract.Break) string {
	b.Consumers = slices.Sorted(slices.Values(b.Consumers))
	raw, _ := json.Marshal(b) // plain data; marshalling cannot fail
	return sha256Hex(raw)
}

// JudgedAcceptance is one verified acceptance record and how it was judged:
// its id, where it is (<repository>@sha256:<hex>), who signed it, when, and
// its state (an impact.Accept* constant) with why.
type JudgedAcceptance struct {
	Record   AcceptanceRecord
	ID       string
	Ref      string
	Signer   string
	Issuer   string
	State    string
	Reason   string
	SignedAt time.Time
}

// JudgeAcceptance says whether a verified acceptance counts: only when a
// person signed it (ReadsAsDecision of the signer's class under rules),
// that person is the accepting service's declared owner (its DRI or an
// email contact, case-insensitively) and, for a break, the announcement has
// not expired at asOf. expires is the break's YYYY-MM-DD expiry, "" when
// none is recorded: unknown never counts.
func JudgeAcceptance(st AcceptanceStatement, signer PublisherIdentity, rules []SignerClassRule, owner contract.Owner, expires string, asOf time.Time) (state, reason string) {
	if class, by := ClassifySigner(signer, rules); !ReadsAsDecision(class) {
		return impact.AcceptProposal, fmt.Sprintf("signed by %s, a %s signer (%s): a proposal until a person accepts it", signer.Subject, class, by)
	}
	if !ownerMatches(owner, signer.Subject) {
		_, name := fleet.ParseServiceKey(fleet.ServiceKey(st.Predicate.Service))
		route, declared := OwnerRoute(name, owner)
		if declared {
			route = "waiting for " + route
		}
		return impact.AcceptRouted, route
	}
	if st.Predicate.Kind == AcceptKindBreak {
		if why := breakExpired(expires, asOf); why != "" {
			return impact.AcceptExpired, why
		}
	}
	return impact.AcceptCounted, ""
}

// ownerMatches reports whether san is owner's DRI or one of its email
// contacts, case-insensitively. A team-only owner matches nobody: a
// certificate names a person, never a team.
func ownerMatches(owner contract.Owner, san string) bool {
	if san == "" {
		return false
	}
	if strings.EqualFold(owner.DRI, san) {
		return true
	}
	return slices.ContainsFunc(owner.Contacts, func(c contract.OwnerContact) bool {
		return c.Type == "email" && strings.EqualFold(c.Value, san)
	})
}

// OwnerRoute is who may accept for service: its declared owner's DRI and
// email contacts (declared), or how to get an owner when it declares none.
func OwnerRoute(service string, owner contract.Owner) (route string, declared bool) {
	var who []string
	if owner.DRI != "" {
		who = append(who, owner.DRI)
	}
	for _, c := range owner.Contacts {
		if c.Type == "email" {
			who = append(who, c.Value)
		}
	}
	if len(who) == 0 {
		return service + " declares no owner: declare service.owner with pacto declare, or ask a default approver (whoever may update its workload)", false
	}
	return "the owner of " + service + ": " + strings.Join(who, ", "), true
}

// breakExpired says why a break with expiry expires (YYYY-MM-DD) no longer
// applies at asOf, "" while it does. No expiry, or one that does not read,
// is unknown, which never counts.
func breakExpired(expires string, asOf time.Time) string {
	if expires == "" {
		return "expiry unknown: run pacto ci or declare expires"
	}
	at, err := time.Parse(time.DateOnly, expires)
	if err != nil {
		return "expiry " + expires + " is not a YYYY-MM-DD date"
	}
	if !asOf.Before(at.AddDate(0, 0, 1)) {
		return "the break expired on " + expires
	}
	return ""
}

// ForImpact is j in the shape pkg/impact reads.
func (j JudgedAcceptance) ForImpact() impact.Acceptance {
	a := impact.Acceptance{State: j.State, By: j.Signer, Issuer: j.Issuer, Record: j.Ref, Reason: j.Reason, SignedAt: j.SignedAt}
	if b := j.Record.Break; b != nil {
		a.Break = &impact.BreakRef{Provider: b.Provider, Version: b.Version, Announcement: b.Announcement}
	}
	return a
}
