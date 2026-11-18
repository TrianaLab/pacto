package delivery

import (
	"fmt"
	"path"
	"regexp"
	"strings"
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
type IdentityPin struct {
	Service    ServiceRef `json:"service" doc:"The service the identity may publish"`
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
	o, b := regexp.QuoteMeta(org), regexp.QuoteMeta(defaultBranch)
	var r PublisherRule
	switch host {
	case "ghcr.io":
		r = PublisherRule{Issuer: issuerGitHubActions,
			Subject:    `^https://github\.com/` + o + `/[^/]+/\.github/workflows/[^@]+@refs/heads/` + b + `$`,
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
	r, p := ruleCovering(service, id, rules), pinFor(service, id, pins)
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

// pinFor is the pin of service that names id exactly, or nil. Only a CI
// identity is ever pinned: a person is trusted only by a policy rule.
func pinFor(service ServiceRef, id PublisherIdentity, pins []IdentityPin) *IdentityPin {
	for i, p := range pins {
		if id.CI && p.Service == service && p.Issuer == id.Issuer && p.Subject == id.Subject && NormalizeRepo(p.Repository) == NormalizeRepo(id.Repository) {
			return &pins[i]
		}
	}
	return nil
}
