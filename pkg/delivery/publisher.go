package delivery

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Publisher verdicts: what a check of who signed a digest concluded. Only
// accepted lets a digest through; unsigned and unknown are never positive.
const (
	VerdictAccepted  = "accepted"
	VerdictNewSigner = "new-signer"
	VerdictRefused   = "refused"
	VerdictUnsigned  = "unsigned"
	VerdictUnknown   = "unknown"
)

// Publisher verdict sources: what decided. policy is declared and signed, pin
// accepted by a named person, rule the installation's default derived from a
// chart value, and inferred a rule derived from where the artifact lives when
// no installation config was read: it never makes a signature verified.
const (
	PublisherSourceRule     = "rule"
	PublisherSourcePin      = "pin"
	PublisherSourcePolicy   = "policy"
	PublisherSourceInferred = "inferred"
	PublisherSourceNone     = "none"
)

// CI issuers the derived rule knows, by registry host.
const (
	issuerGitHubActions = "https://token.actions.githubusercontent.com"
	issuerGitLab        = "https://gitlab.com"
)

// PublisherIdentity is who signed a digest, read from the verified Sigstore
// certificate.
type PublisherIdentity struct {
	Issuer     string `json:"issuer" doc:"The OIDC issuer of the signer's certificate"`
	Subject    string `json:"subject" doc:"The certificate's subject: a CI workflow URI or a person's email"`
	Repository string `json:"repository,omitempty" doc:"The source repository the certificate attests, set for a CI workload identity"`
	Ref        string `json:"ref,omitempty" doc:"The source repository ref the certificate attests"`
	CI         bool   `json:"ci,omitempty" doc:"The certificate is a CI workload identity: it attests a source repository"`
	// +optional
	// +kubebuilder:validation:Enum=human;agent;ci
	Class ActorClass `json:"class,omitempty" enum:"human,agent,ci" doc:"Who the signer is: a person (human), an agent or a CI workload; only a person's signature makes a record a decision"`
	// +optional
	ClassifiedBy string `json:"classifiedBy,omitempty" doc:"What classified the signer: policy:<rule name> or default:<reason>"`
}

// SignerClassRule classifies keyless signers: a certificate from Issuer whose
// subject matches Subject (anchored) is Class.
type SignerClassRule struct {
	Name    string     `json:"name"`
	Issuer  string     `json:"issuer"`
	Subject string     `json:"subject"`
	Class   ActorClass `json:"class"`
}

// ciIssuers are OIDC issuers that only issue CI workload identities.
var ciIssuers = []string{issuerGitHubActions, "https://agent.buildkite.com"}

// ClassifySigner says whether a person, an agent or a CI workload signed as
// id, and what said so: the first of rules (the signed policy's signers)
// that matches, else the default rule. A signer the default rule cannot
// place is an agent: unknown is never a person.
func ClassifySigner(id PublisherIdentity, rules []SignerClassRule) (ActorClass, string) {
	for _, r := range rules {
		if r.Issuer == id.Issuer && anchoredMatch(r.Subject, id.Subject) {
			return r.Class, "policy:" + r.Name
		}
	}
	sub := strings.ToLower(id.Subject)
	switch {
	case id.CI:
		return ClassCI, "default:the certificate attests a source repository"
	case slices.Contains(ciIssuers, id.Issuer) || strings.HasPrefix(id.Issuer, "https://oidc.circleci.com/org/"):
		return ClassCI, "default:" + id.Issuer + " issues CI workload identities"
	case strings.HasPrefix(sub, "spiffe://") || strings.HasPrefix(sub, "https://kubernetes.io/namespaces/") ||
		strings.HasSuffix(sub, "gserviceaccount.com") || strings.HasSuffix(sub, ".svc.id.goog"):
		return ClassAgent, "default:the certificate names a workload identity"
	case strings.Contains(sub, "@") && !strings.Contains(sub, "://"):
		return ClassHuman, "default:the certificate names an email from an interactive login"
	}
	return ClassAgent, "default:the certificate names no person"
}

// Classified is id with the class ClassifySigner gives it under rules.
func Classified(id PublisherIdentity, rules []SignerClassRule) PublisherIdentity {
	id.Class, id.ClassifiedBy = ClassifySigner(id, rules)
	return id
}

// ReadsAsDecision reports whether a record signed by class decides: only a
// person's does. An agent's or a CI workload's acceptance, approval, claim or
// owner setting is a proposal.
func ReadsAsDecision(class ActorClass) bool {
	return class == ClassHuman
}

// PublisherRule names the CI identities that may publish. Subject and
// Repository are anchored regular expressions, both required; Ref, when set,
// must equal the certificate's; Services are path.Match patterns over service
// names, and empty means every service.
type PublisherRule struct {
	Name       string   `json:"name,omitempty" doc:"The rule's name"`
	Issuer     string   `json:"issuer" doc:"The OIDC issuer the signer's certificate must carry"`
	Subject    string   `json:"subject" doc:"An anchored regular expression the certificate's subject must match"`
	Repository string   `json:"repository" doc:"An anchored regular expression the attested source repository must match"`
	Ref        string   `json:"ref,omitempty" doc:"The source repository ref the certificate must attest"`
	Services   []string `json:"services,omitempty" doc:"Service name patterns the rule covers; empty covers every service"`
}

// IdentityPin is a person's confirmation that one CI identity may publish one
// service: Approval is the approval record id, Operation the trust operation.
// Namespace is the namespace of the Stage whose trust operation was
// confirmed: only Stages of that namespace use the pin, so a confirmation in
// one namespace never decides for a Stage of the same service in another.
type IdentityPin struct {
	Service    ServiceRef `json:"service" doc:"The service the identity may publish"`
	Namespace  string     `json:"namespace" doc:"The namespace of the Stage whose trust operation a person confirmed; only Stages of that namespace use the pin"`
	Issuer     string     `json:"issuer" doc:"The pinned signer's OIDC issuer"`
	Subject    string     `json:"subject" doc:"The pinned signer's exact subject"`
	Repository string     `json:"repository" doc:"The source repository the pinned signer attests"`
	Approver   string     `json:"approver" doc:"Who confirmed the identity"`
	Approval   string     `json:"approval" doc:"The signed approval record of that confirmation"`
	Operation  string     `json:"operation" doc:"The trust operation the approval answered"`
	At         string     `json:"at" doc:"When the pin was signed"`
}

// PublisherVerdict is what a publisher check concluded about one digest.
type PublisherVerdict struct {
	Digest string `json:"digest" doc:"The digest checked"`
	// +optional
	Version string `json:"version,omitempty" doc:"The version tag that named the digest"`
	// +kubebuilder:validation:Enum=accepted;new-signer;refused;unsigned;unknown
	Verdict string `json:"verdict" enum:"accepted,new-signer,refused,unsigned,unknown" doc:"What the check concluded; only accepted lets the digest through"`
	// +kubebuilder:validation:Enum=rule;pin;policy;inferred;none
	Source string `json:"source" enum:"rule,pin,policy,inferred,none" doc:"What decided: an installation rule, a person's pin, the signed policy, a rule inferred from where the artifact lives, or nothing"`
	// +optional
	Identity *PublisherIdentity `json:"identity,omitempty" doc:"Who signed it, when a signature verified"`
	Reason   string             `json:"reason" doc:"Why, in words"`
	// +optional
	At string `json:"at,omitempty" doc:"When the check ran"`
}

// Bounds of the registry watch: the newest version tags it judges per
// pass, and the verdicts a Stage keeps.
const (
	MaxWatchedTags       = 20
	MaxPublisherVerdicts = 20
)

// PublisherStatus is what a Stage's registry watch (candidates.registry)
// judged, written by the promoter only.
type PublisherStatus struct {
	// Source is what the watch judges against: the installation's rule, the
	// signed policy, or none when there is no rule and only a person's pin
	// accepts a signer.
	// +optional
	// +kubebuilder:validation:Enum=rule;policy;none
	Source string `json:"source,omitempty" enum:"rule,policy,none" doc:"What the watch judges against: the installation's rule, the signed policy, or none when only a person's pin accepts a signer"`
	// +optional
	Rule *PublisherRule `json:"rule,omitempty" doc:"The publisher rule the watch judges against"`
	// +optional
	PolicyRef string `json:"policyRef,omitempty" doc:"The signed delivery policy's reference"`
	// +optional
	PolicyDigest string `json:"policyDigest,omitempty" doc:"The digest of the policy whose rules are in force"`
	// +optional
	PolicyAt string `json:"policyAt,omitempty" doc:"When the policy in force was signed: an older one is refused"`
	// +optional
	Pins int `json:"pins,omitempty" doc:"How many pinned signers of the service were read"`
	// +optional
	PinAt string `json:"pinAt,omitempty" doc:"When the newest pin read was signed: an older newest pin is never used"`
	// +optional
	PinDigest string `json:"pinDigest,omitempty" doc:"The digest of the newest pin read"`
	// +optional
	Message string `json:"message,omitempty" doc:"Why the watch cannot judge, or what it could not read"`
	// Verdicts are the newest judged digests, newest first (cap 20).
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Verdicts []PublisherVerdict `json:"verdicts,omitempty" doc:"The newest judged digests, newest first"`
	// +optional
	CheckedAt string `json:"checkedAt,omitempty" doc:"When the watch last judged (RFC 3339)"`
}

// foreignIdentity is an issuer and subject no publisher has: a pattern that
// matches it matches anyone.
const foreignIdentity = "https://example.invalid/x"

// PinnedPattern reports whether the identity pattern p is anchored at both
// ends, is a valid expression and does not match foreignIdentity.
func PinnedPattern(p string) bool {
	re, err := regexp.Compile(p)
	return err == nil && strings.HasPrefix(p, "^") && strings.HasSuffix(p, "$") && !re.MatchString(foreignIdentity)
}

// NormalizeRepo is a repository URL in one comparable form: no scheme, no
// .git suffix, no trailing slash and a lowercase host.
func NormalizeRepo(s string) string {
	s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://"), "/")
	s = strings.TrimSuffix(s, ".git")
	host, rest, ok := strings.Cut(s, "/")
	if !ok {
		return strings.ToLower(s)
	}
	return strings.ToLower(host) + "/" + rest
}

// PactoActionsWorkflow is the shared reusable workflow pacto ci init calls:
// a certificate names it, at the caller's pin, as its subject.
const PactoActionsWorkflow = "TrianaLab/pacto-actions/.github/workflows/ci.yml"

// DeriveRule is the default publisher rule of an installation whose services
// publish to registry (host/org): the CI of host's provider, any workflow of
// org on defaultBranch (main when empty). The second result says why there is
// no rule; the first is then the zero rule.
func DeriveRule(registry, defaultBranch string) (PublisherRule, string) {
	host, rest, _ := strings.Cut(strings.TrimPrefix(registry, "oci://"), "/")
	host = strings.ToLower(host)
	org, _, _ := strings.Cut(rest, "/")
	if org == "" {
		return PublisherRule{}, fmt.Sprintf("%q names no organization, so the first signer of each service is confirmed by a person", registry)
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	// The org matches in any case: GitHub and GitLab names are case-insensitive
	// and their registries lowercase them (ghcr.io/trianalab is TrianaLab).
	o, b := "(?i:"+regexp.QuoteMeta(org)+")", regexp.QuoteMeta(defaultBranch)
	var r PublisherRule
	switch host {
	case "ghcr.io":
		// The pacto-actions reusable workflow signs at whatever commit the
		// caller pins; Repository and Ref still anchor the caller.
		r = PublisherRule{Issuer: issuerGitHubActions,
			Subject: `^https://github\.com/(` + o + `/[^/]+/\.github/workflows/[^@]+@refs/heads/` + b + `|` +
				regexp.QuoteMeta(PactoActionsWorkflow) + `@[^@]+)$`,
			Repository: `^https://github\.com/` + o + `/[^/]+$`}
	case "registry.gitlab.com":
		r = PublisherRule{Issuer: issuerGitLab,
			Subject:    `^https://gitlab\.com/` + o + `/[^/]+//\.gitlab-ci\.yml@refs/heads/` + b + `$`,
			Repository: `^https://gitlab\.com/` + o + `/[^/]+$`}
	default:
		return PublisherRule{}, host + " names no CI provider, so the first signer of each service is confirmed by a person"
	}
	// ponytail: org and branch are quoted, so both patterns always pass
	// PinnedPattern (TestDeriveRuleAlwaysPinned); ruleCovering checks it anyway.
	r.Name, r.Ref = "derived from "+host+"/"+org, "refs/heads/"+defaultBranch
	return r, ""
}

// JudgePublisher decides whether id may publish service: a rule covers it
// (accepted, source ruleSource), a pin of the service names it (accepted,
// source pin), it is a CI identity of the artifact's own repository (new-signer:
// a person decides), or it is refused. source is the artifact's
// org.opencontainers.image.source annotation, or "" when it carries none. A
// person identity is never offered for trust on first use.
func JudgePublisher(service ServiceRef, id PublisherIdentity, source string, rules []PublisherRule, ruleSource string, pins []IdentityPin) PublisherVerdict {
	idc := id
	v := PublisherVerdict{Identity: &idc, Source: PublisherSourceNone}
	r, p := ruleCovering(service, id, rules), PinFor(service, id, pins, rules)
	switch {
	case id.CI && !sameSource(source, id.Repository):
		v.Verdict = VerdictRefused
		v.Reason = fmt.Sprintf("signed by %s (%s), whose certificate attests %q while the artifact names its source %q", id.Subject, id.Issuer, id.Repository, source)
	case r != nil:
		v.Verdict, v.Source = VerdictAccepted, ruleSource
		v.Reason = fmt.Sprintf("signed by %s (%s), which the publisher rule %q covers", id.Subject, id.Issuer, r.Name)
	case p != nil:
		v.Verdict, v.Source = VerdictAccepted, PublisherSourcePin
		v.Reason = fmt.Sprintf("signed by %s (%s), which %s pinned for %s at %s", id.Subject, id.Issuer, p.Approver, service.Name, p.At)
	case id.CI:
		v.Verdict = VerdictNewSigner
		v.Reason = fmt.Sprintf("signed by %s (issuer %s, repository %s), a CI identity no publisher rule, pin or policy covers: a person confirms it", id.Subject, id.Issuer, id.Repository)
	default:
		v.Verdict = VerdictRefused
		v.Reason = fmt.Sprintf("signed by %s (%s), a person identity; a deploy artifact is accepted only from a CI identity the rule, a pin or the policy covers. A registry-declared revision must reuse its CI-signed baseline's deploy digest", id.Subject, id.Issuer)
	}
	return v
}

// JudgeSigners judges every verified signer of one digest: the first accepted
// wins, else the most decisive verdict (refused over new-signer).
func JudgeSigners(service ServiceRef, signers []PublisherIdentity, source string, rules []PublisherRule, ruleSource string, pins []IdentityPin) PublisherVerdict {
	best := PublisherVerdict{Verdict: VerdictUnknown, Source: PublisherSourceNone, Reason: "no signature verified"}
	for i, id := range signers {
		v := JudgePublisher(service, id, source, rules, ruleSource, pins)
		if v.Verdict == VerdictAccepted {
			return v
		}
		if i == 0 || (v.Verdict == VerdictRefused && best.Verdict != VerdictRefused) {
			best = v
		}
	}
	return best
}

// sameSource reports whether the artifact's source annotation, when it has
// one, names the repository the certificate attests.
func sameSource(source, repository string) bool {
	return source == "" || NormalizeRepo(source) == NormalizeRepo(repository)
}

// ruleCovering is the first rule that covers id for service, or nil.
func ruleCovering(service ServiceRef, id PublisherIdentity, rules []PublisherRule) *PublisherRule {
	for i, r := range rules {
		if r.Issuer == id.Issuer && (r.Ref == "" || r.Ref == id.Ref) && servesService(r.Services, service.Name) &&
			anchoredMatch(r.Subject, id.Subject) && anchoredMatch(r.Repository, id.Repository) {
			return &rules[i]
		}
	}
	return nil
}

// anchoredMatch reports whether s matches the pinned pattern p; an open or
// invalid pattern matches nothing.
func anchoredMatch(p, s string) bool {
	if !PinnedPattern(p) {
		return false
	}
	return regexp.MustCompile(p).MatchString(s) // PinnedPattern compiled it
}

// servesService reports whether a rule's service patterns cover name.
func servesService(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return len(patterns) == 0
}

// PinFor is the pin of service that names id, or nil. Only a CI identity is
// ever pinned: a person is trusted only by a policy rule. A subject naming a
// reusable workflow of another repository matches at whatever ref the caller
// pins it, and only when id's ref is the ref a rule for this service and
// repository requires (the default branch): a pin stores no ref, and such a
// subject does not tie it to a branch.
func PinFor(service ServiceRef, id PublisherIdentity, pins []IdentityPin, rules []PublisherRule) *IdentityPin {
	for i, p := range pins {
		if !id.CI || p.Service != service || p.Issuer != id.Issuer || NormalizeRepo(p.Repository) != NormalizeRepo(id.Repository) {
			continue
		}
		stable := stableSubject(id.Subject, id.Repository)
		if stableSubject(p.Subject, p.Repository) == stable && (stable == id.Subject || refRuled(service, id, rules)) {
			return &pins[i]
		}
	}
	return nil
}

// stableSubject is subject without its @<ref> when it names a workflow
// outside repository, as a reusable workflow's does; otherwise subject.
func stableSubject(subject, repository string) string {
	if repository == "" || strings.HasPrefix(NormalizeRepo(subject), NormalizeRepo(repository)+"/") {
		return subject
	}
	base, _, _ := strings.Cut(subject, "@")
	return base
}

// refRuled reports whether a rule of id's issuer that applies to service and
// id's repository requires exactly id's ref: a rule written for another
// service or repository says nothing about this caller's default branch.
func refRuled(service ServiceRef, id PublisherIdentity, rules []PublisherRule) bool {
	return slices.ContainsFunc(rules, func(r PublisherRule) bool {
		return r.Issuer == id.Issuer && r.Ref != "" && r.Ref == id.Ref &&
			servesService(r.Services, service.Name) && anchoredMatch(r.Repository, id.Repository)
	})
}

// Signed publisher statements: a person's confirmation of one signer, which
// the recorder signs, and the delivery policy a security owner signs.
const (
	PredicateIdentityPin     = "delivery.pacto.trianalab.io/identity-pin/v1"
	PredicatePublisherPolicy = "delivery.pacto.trianalab.io/policy/v1"
)

// PinStatement is an in-toto Statement v1 carrying an identity pin. Its
// subject is the sha256 of the service's pin stream id.
type PinStatement struct {
	Type          string      `json:"_type"`
	Subject       []Subject   `json:"subject"`
	PredicateType string      `json:"predicateType"`
	Predicate     IdentityPin `json:"predicate"`
}

// PinStream is the stream a service's pins are attached to:
// pins/<domain or ->/<name>, the domain path-escaped so every pin stream has
// three segments and the trust pattern pins/*/* covers them all.
func PinStream(s ServiceRef) string {
	return "pins/" + url.PathEscape(cmp.Or(s.Domain, "-")) + "/" + s.Name
}

// NewPinStatement builds the statement of pin p.
func NewPinStatement(p IdentityPin) PinStatement {
	sum := sha256.Sum256([]byte(PinStream(p.Service)))
	return PinStatement{Type: StatementType, PredicateType: PredicateIdentityPin, Predicate: p,
		Subject: []Subject{{Name: "pins", Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])}}}}
}

// SignPin signs st and returns the bundle and its id, as SignRecord does.
func SignPin(ctx context.Context, s Signer, st PinStatement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}

// OpenPin verifies a pin bundle with verify and returns its statement and
// id. A bundle that is not one signed identity-pin/v1 statement is an error.
func OpenPin(bundle []byte, verify func(bundle []byte) error) (PinStatement, string, error) {
	var st PinStatement
	payload, err := openStatement(bundle, PredicateIdentityPin, &st)
	if err != nil {
		return PinStatement{}, "", err
	}
	if err := verify(bundle); err != nil {
		return PinStatement{}, "", fmt.Errorf("signature: %w", err)
	}
	return st, sha256Hex(payload), nil
}

// TrustOperationName is the name of the one trust operation a Stage raises
// for id: the same identity always names the same operation, so a second
// pass, a restart or a second digest finds the one already raised.
func TrustOperationName(stage string, id PublisherIdentity) string {
	sum := sha256.Sum256([]byte(id.Issuer + "|" + id.Subject + "|" + id.Repository))
	return stage + "-trust-" + hex.EncodeToString(sum[:])[:10]
}

// PublisherPolicy is the signed delivery policy (policy/v1): the publisher
// rules that replace the installation's derived rule, and when it was signed.
type PublisherPolicy struct {
	Publishers []PublisherRule `json:"publishers"`
	// Signers classify keyless signers before the default rule does.
	Signers []SignerClassRule `json:"signers,omitempty"`
	// At is when the policy was signed (RFC 3339): a promoter refuses a
	// policy older than the one it already uses.
	At string `json:"at"`
}

// PolicyStatement is an in-toto Statement v1 carrying a delivery policy.
type PolicyStatement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     PublisherPolicy `json:"predicate"`
}

// OpenPolicy verifies a delivery policy bundle with verify and returns the
// policy. Decoding is strict, so a key an older promoter does not know is a
// failed read; a rule with no issuer or an open subject or repository
// pattern, or a policy with no signed time, is refused.
func OpenPolicy(bundle []byte, verify func(bundle []byte) error) (PublisherPolicy, error) {
	var st PolicyStatement
	if _, err := openStatement(bundle, PredicatePublisherPolicy, &st); err != nil {
		return PublisherPolicy{}, err
	}
	if err := verify(bundle); err != nil {
		return PublisherPolicy{}, fmt.Errorf("signature: %w", err)
	}
	p := st.Predicate
	if _, err := time.Parse(time.RFC3339, p.At); err != nil {
		return PublisherPolicy{}, errors.New("the policy carries no signed time (at, RFC 3339)")
	}
	for i, r := range p.Publishers {
		if r.Issuer == "" || !PinnedPattern(r.Subject) || !PinnedPattern(r.Repository) {
			return PublisherPolicy{}, fmt.Errorf("publisher rule %d (%s) needs an issuer and anchored subject and repository patterns that match no foreign identity", i, r.Name)
		}
	}
	for i, r := range p.Signers {
		if r.Issuer == "" || !PinnedPattern(r.Subject) || !slices.Contains([]ActorClass{ClassHuman, ClassAgent, ClassCI}, r.Class) {
			return PublisherPolicy{}, fmt.Errorf("signer rule %d (%s) needs an issuer, an anchored subject pattern that matches no foreign identity and a class of human, agent or ci", i, r.Name)
		}
	}
	return p, nil
}

// MaxTrustItems bounds each list of one service's trust answer: its pins,
// the signers waiting for a person and its newest verdicts.
const MaxTrustItems = 20

// PolicyState is the signed delivery policy an installation names and the
// one a Stage's watch verified and uses.
type PolicyState struct {
	Ref string `json:"ref" doc:"The delivery policy the installation names (delivery.publishers.policy)"`
	// +optional
	Digest string `json:"digest,omitempty" doc:"The digest of the policy a Stage's watch verified and uses; empty while none has"`
	// +optional
	At string `json:"at,omitempty" doc:"When that policy was signed"`
}

// PendingSigner is a CI identity that signed a version of a service and
// waits for a person to confirm it may publish the service.
type PendingSigner struct {
	Identity  PublisherIdentity `json:"identity" doc:"Who signed the version"`
	Digest    string            `json:"digest" doc:"The digest it signed"`
	Namespace string            `json:"namespace" doc:"The Stage's namespace"`
	Stage     string            `json:"stage" doc:"The Stage whose registry watch read it"`
	Operation string            `json:"operation,omitempty" doc:"The trust operation of the identity: held while a person is asked, concluded when a person already answered it (the reason says how); empty while another trust operation of the Stage is pending"`
	Reason    string            `json:"reason" doc:"Why a person is asked, or why nobody is asked again yet"`
}

// ServiceTrust is what one service's publishers are: the identities a
// person pinned, the signers waiting for one and the newest verdicts of its
// Stages' registry watch (each list capped at MaxTrustItems).
type ServiceTrust struct {
	Service  ServiceRef         `json:"service"`
	Pins     []IdentityPin      `json:"pins" doc:"The identities a person confirmed for the service, newest first; the newest of each namespace is the one its Stages use. null when they could not be read (unreadable says why)" nullable:"true"`
	Pending  []PendingSigner    `json:"pending" doc:"The CI identities that wait for a person to confirm them"`
	Verdicts []PublisherVerdict `json:"verdicts" doc:"The newest digests its Stages' registry watch judged, newest first; each at is when the verdict was first reached"`
}

// TrustView is which publishers each service accepts: the installation's
// rule or signed policy and, per service with a Stage that watches the
// registry, its pins, pending signers and verdicts.
type TrustView struct {
	Listing
	// +optional
	Rule       *PublisherRule `json:"rule,omitempty" doc:"The installation's publisher rule, derived from delivery.publishers.registry"`
	RuleSource string         `json:"ruleSource" enum:"rule,policy,none,unknown" doc:"What every service is judged against: the installation's rule, the signed policy, none when only a person's pin accepts a signer, or unknown when the publisher trust config could not be read"`
	// +optional
	NoRule string `json:"noRule,omitempty" doc:"Why no rule or policy accepts a signer, so only pins do, or why the rule is unknown"`
	// +optional
	Policy   *PolicyState   `json:"policy,omitempty" doc:"The signed delivery policy, when the installation names one"`
	Services []ServiceTrust `json:"services"`
}

// PublisherSourceUnknown is a trust view's rule source when the publisher
// trust config could not be read.
const PublisherSourceUnknown = "unknown"

// TrustServices are the services of the Stages that watch the registry
// (candidates.registry), sorted by name and domain.
func TrustServices(stages []StageObject) []ServiceRef {
	var out []ServiceRef
	for _, st := range stages {
		if st.Spec.Candidates.Registry != nil && !slices.Contains(out, st.Spec.Service) {
			out = append(out, st.Spec.Service)
		}
	}
	slices.SortFunc(out, func(a, b ServiceRef) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Domain, b.Domain)) })
	return out
}

// TrustNamespaces are the namespaces of the Stages that watch the registry
// for svc, sorted: pins are kept per namespace.
func TrustNamespaces(stages []StageObject, svc ServiceRef) []string {
	var out []string
	for _, st := range stages {
		if st.Spec.Candidates.Registry != nil && st.Spec.Service == svc && !slices.Contains(out, st.Metadata.Namespace) {
			out = append(out, st.Metadata.Namespace)
		}
	}
	slices.Sort(out)
	return out
}

// TrustOf is the trust view of the Stages and operations the caller listed,
// judged under cfg: pins (nil reads none) gives the pins of a service in a
// namespace, nil when they could not be read. Only services of the listed
// Stages and pins of their namespaces are named. A listed Stage whose watch
// cannot judge is named in Unreadable; AsOf and completeness are the caller's
// to set.
func TrustOf(cfg PublisherConfig, stages []StageObject, ops []OperationObject, pins func(ns string, svc ServiceRef) []IdentityPin) TrustView {
	v := TrustView{Services: []ServiceTrust{}}
	trustRule(&v, cfg, stages)
	for _, svc := range TrustServices(stages) {
		st := ServiceTrust{Service: svc, Pins: []IdentityPin{}, Pending: []PendingSigner{}, Verdicts: []PublisherVerdict{}}
		for _, ns := range TrustNamespaces(stages, svc) {
			if pins == nil || st.Pins == nil {
				continue
			}
			p := pins(ns, svc)
			if p == nil {
				st.Pins = nil
				continue
			}
			st.Pins = append(st.Pins, p...)
		}
		for _, s := range stages {
			if s.Spec.Candidates.Registry != nil && s.Spec.Service == svc {
				serviceTrustOf(&st, s, ops)
				v.Unreadable = append(v.Unreadable, watchUnreadable(s)...)
			}
		}
		slices.SortStableFunc(st.Pins, func(a, b IdentityPin) int { return cmp.Compare(b.At, a.At) })
		slices.SortStableFunc(st.Verdicts, func(a, b PublisherVerdict) int { return cmp.Compare(b.At, a.At) })
		if st.Pins != nil {
			st.Pins = st.Pins[:min(len(st.Pins), MaxTrustItems)]
		}
		st.Pending, st.Verdicts = st.Pending[:min(len(st.Pending), MaxTrustItems)], st.Verdicts[:min(len(st.Verdicts), MaxTrustItems)]
		v.Services = append(v.Services, st)
	}
	return v
}

// watchUnreadable names a registry Stage whose watch cannot judge (Ready
// False because the registry, the trusted root, the policy or the pins
// could not be read), so its verdicts and pending signers may be stale.
func watchUnreadable(s StageObject) []string {
	r := readyOf(s.Status)
	if r == nil || r.Status == "True" || (r.Reason != ReadyReasonRegistryUnreadable && r.Reason != ReadyReasonPublisherUnverifiable) {
		return nil
	}
	return []string{"the registry watch of " + s.Metadata.Namespace + "/" + s.Metadata.Name + " cannot judge, so its verdicts may be stale: " + r.Message}
}

// trustRule sets what every service is judged against: the policy cfg
// names (with the newest digest a listed Stage's watch judges with), else
// the derived rule, else nothing.
func trustRule(v *TrustView, cfg PublisherConfig, stages []StageObject) {
	if cfg.Policy == "" {
		r, why := cfg.Rule()
		if why != "" {
			v.RuleSource, v.NoRule = PublisherSourceNone, why
			return
		}
		v.RuleSource, v.Rule = PublisherSourceRule, &r
		return
	}
	v.RuleSource, v.Policy = PublisherSourcePolicy, &PolicyState{Ref: cfg.Policy}
	for _, s := range stages {
		// The digests a status keeps are high-water marks: only a watch that
		// judges with the policy now says it is in force.
		if p := s.Status.Publisher; p != nil && p.Source == PublisherSourcePolicy && p.PolicyDigest != "" && p.PolicyAt > v.Policy.At {
			v.Policy.Digest, v.Policy.At = p.PolicyDigest, p.PolicyAt
		}
	}
	if v.Policy.Digest == "" {
		v.NoRule = "no Stage you can list judges with a verified delivery policy " + cfg.Policy + ", so only pinned signers are accepted"
	}
}

// serviceTrustOf adds what Stage s knows to st: its watch's verdicts, its
// held trust operations and the new signers no held operation asks about:
// one a person already answered says so.
func serviceTrustOf(st *ServiceTrust, s StageObject, ops []OperationObject) {
	asked, ended := map[string]bool{}, map[string]OperationObject{}
	for _, o := range ops {
		p := o.Spec.Params.Publisher
		if o.Spec.Type != TypeTrust || p == nil || o.Metadata.Namespace != s.Metadata.Namespace || o.Spec.Stage != s.Metadata.Name {
			continue
		}
		if o.Status.Phase == PhaseConcluded {
			ended[o.Metadata.Name] = o
			continue
		}
		asked[p.Issuer+"|"+p.Subject+"|"+p.Repository] = true
		st.Pending = append(st.Pending, PendingSigner{Identity: *p, Digest: o.Spec.Params.To, Namespace: s.Metadata.Namespace,
			Stage: s.Metadata.Name, Operation: o.Metadata.Name, Reason: o.Spec.Reason})
	}
	if s.Status.Publisher == nil {
		return
	}
	for _, vd := range s.Status.Publisher.Verdicts {
		st.Verdicts = append(st.Verdicts, vd)
		id := vd.Identity
		if vd.Verdict != VerdictNewSigner || id == nil || asked[id.Issuer+"|"+id.Subject+"|"+id.Repository] {
			continue
		}
		asked[id.Issuer+"|"+id.Subject+"|"+id.Repository] = true
		ps := PendingSigner{Identity: *id, Digest: vd.Digest, Namespace: s.Metadata.Namespace, Stage: s.Metadata.Name, Reason: vd.Reason}
		if o, ok := ended[TrustOperationName(s.Metadata.Name, *id)]; ok {
			ps.Operation, ps.Reason = o.Metadata.Name, answered(o)
		}
		st.Pending = append(st.Pending, ps)
	}
}

// answered is why a signer whose trust operation o concluded is not asked
// again yet.
func answered(o OperationObject) string {
	at := o.Status.Times.Finished
	switch o.Status.Outcome {
	case Rejected:
		by := "a person"
		if n := len(o.Status.Approvals); n > 0 {
			by = o.Status.Approvals[n-1].By
		}
		return "refused by " + by + " at " + at + " in " + o.Metadata.Name + "; it is asked again once that operation is collected"
	case Succeeded:
		return "confirmed at " + at + " in " + o.Metadata.Name + ", but no pin of it is in use"
	}
	return "the trust operation " + o.Metadata.Name + " ended " + string(o.Status.Outcome) + " at " + at + "; it is asked again"
}
