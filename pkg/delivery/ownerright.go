package delivery

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// MaxOwnerPolicyRules bounds the signer rules of one owner policy, default
// approvers and team rules together.
const MaxOwnerPolicyRules = 200

// SignerRule matches one signer by its OIDC issuer, compared exactly, and its
// subject, matched by a pinned pattern.
type SignerRule struct {
	Name    string `json:"name,omitempty" doc:"A name that refusals and the who-may-set list print"`
	Issuer  string `json:"issuer" doc:"The OIDC issuer, compared exactly"`
	Subject string `json:"subject" doc:"A pattern anchored with ^ and $ that the signer subject must match"`
}

// OwnerPolicy is who may set a service owner beside the owner's own members:
// the default approvers, the signers that count as members of a team and the
// issuers whose email subjects count as people.
type OwnerPolicy struct {
	DefaultApprovers []SignerRule            `json:"defaultApprovers,omitempty" doc:"Signers who may set any service owner"`
	Teams            map[string][]SignerRule `json:"teams,omitempty" doc:"Per team, the signers who count as its members"`
	EmailIssuers     []string                `json:"emailIssuers,omitempty" doc:"Issuers whose email subjects match an owner's DRI or email contacts"`
}

// Check refuses a rule with no issuer or a subject pattern that is not pinned,
// an empty email issuer and more than MaxOwnerPolicyRules rules.
func (p OwnerPolicy) Check() error {
	n := len(p.DefaultApprovers)
	if err := checkSignerRules("default approver", p.DefaultApprovers); err != nil {
		return err
	}
	teams := make([]string, 0, len(p.Teams))
	for team := range p.Teams {
		teams = append(teams, team)
	}
	slices.Sort(teams)
	for _, team := range teams {
		n += len(p.Teams[team])
		if err := checkSignerRules("team "+team, p.Teams[team]); err != nil {
			return err
		}
	}
	if n > MaxOwnerPolicyRules {
		return fmt.Errorf("%d rules, over the %d rule bound", n, MaxOwnerPolicyRules)
	}
	if slices.Contains(p.EmailIssuers, "") {
		return errors.New("emailIssuers holds an empty issuer")
	}
	return nil
}

// checkSignerRules refuses a rule in rules with no issuer or an unpinned
// subject; where names the list in the error.
func checkSignerRules(where string, rules []SignerRule) error {
	for i, r := range rules {
		switch {
		case r.Issuer == "":
			return fmt.Errorf("%s rule %d: issuer is empty", where, i)
		case !PinnedPattern(r.Subject):
			return fmt.Errorf("%s rule %d: subject %q is not a pinned pattern (^...$ matching one identity)", where, i, r.Subject)
		}
	}
	return nil
}

// ReadOwnerPolicy reads the owner policy at path, decoded strictly and checked.
// On any failure it returns nil and the reason: a policy that cannot be read is
// no policy, never an empty one. The file is a mounted ConfigMap, which
// Kubernetes bounds at 1 MiB.
func ReadOwnerPolicy(path string) (*OwnerPolicy, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err.Error()
	}
	var p OwnerPolicy
	if err := strictjson.Unmarshal(b, &p); err != nil {
		return nil, "the owner policy " + path + ": " + err.Error()
	}
	if err := p.Check(); err != nil {
		return nil, "the owner policy " + path + ": " + err.Error()
	}
	return &p, ""
}

// The owner right refusal codes: stable, published in pacto_declare's
// refusal enum and on every owner-right verdict.
const (
	// OwnerSignerNotPerson: an agent, CI or workload identity never sets an
	// owner, whoever it acts for.
	OwnerSignerNotPerson = "OWNER_SIGNER_NOT_PERSON"
	// OwnerSignerNotAllowed: the person is no member of the current owner
	// and no default approver.
	OwnerSignerNotAllowed = "OWNER_SIGNER_NOT_ALLOWED"
	// OwnerPolicyUnverified: no verified owner policy was read, so who may
	// set the owner is unknown.
	OwnerPolicyUnverified = "OWNER_POLICY_UNVERIFIED"
	// OwnerSignerUnverified: the bundle's signer, or in the fleet the owner
	// before it, could not be verified.
	OwnerSignerUnverified = "OWNER_SIGNER_UNVERIFIED"
)

// OwnerRightCodes are the owner right refusal codes, in enum order.
var OwnerRightCodes = []string{OwnerSignerNotPerson, OwnerSignerNotAllowed, OwnerPolicyUnverified, OwnerSignerUnverified}

// DefaultEmailIssuers are the issuers whose email subjects name a person's
// login when the policy lists none.
var DefaultEmailIssuers = []string{"https://accounts.google.com", "https://github.com/login/oauth", "https://login.microsoftonline.com"}

// maxMayAccept bounds who a verdict names as able to set the owner.
const maxMayAccept = 10

// OwnerClaim is a service owner and where it comes from: its provenance
// (declared, attested, declared-via-target or inferred) and, for an attested
// owner, the citation it was read at.
type OwnerClaim struct {
	Owner      contract.Owner
	Provenance string
	Source     string
}

// OwnerRight is the verdict on one owner-setting signer: the access answer
// (source registry-policy), the refusal code, what granted it and who may set
// the owner.
type OwnerRight struct {
	WorkloadAccess
	Code      string   `json:"code,omitempty" enum:"OWNER_SIGNER_NOT_PERSON,OWNER_SIGNER_NOT_ALLOWED,OWNER_POLICY_UNVERIFIED,OWNER_SIGNER_UNVERIFIED" doc:"Why the signer may not set the owner; empty when allowed"`
	Grant     string   `json:"grant,omitempty" doc:"What allowed it: unchanged, declared-owner, attested-owner or default-approver:<rule name>"`
	MayAccept []string `json:"mayAccept,omitempty" doc:"Who may set the owner: the owner's members, its DRI and the default approver rules, at most 10"`
	Message   string   `json:"message" doc:"The verdict as one sentence"`
}

// DecideOwnerRight decides whether signer may change service's owner from
// current to proposed (removing it included). In order: an unchanged owner
// needs no right; a signer who is no person never has it; a member of a
// current declared or attested owner has it, and so does a default approver.
// With no verified policy (nil, policyFrom says why) anyone else is
// unverified, never allowed; otherwise refused.
func DecideOwnerRight(service string, signer PublisherIdentity, current, proposed OwnerClaim, policy *OwnerPolicy, policyFrom string, now time.Time) OwnerRight {
	r := OwnerRight{WorkloadAccess: WorkloadAccess{Resource: "services", Name: service, Source: AccessByRegistryPolicy, At: now.UTC().Format(time.RFC3339)},
		MayAccept: mayAccept(current, policy)}
	memberSource := current.Provenance == ownerDeclared || current.Provenance == ownerAttested
	switch {
	case proposed.Owner.Equal(current.Owner):
		r.Allowed, r.Grant = true, "unchanged"
	case !ReadsAsDecision(signer.Class):
		r.Code = OwnerSignerNotPerson
	case memberSource && isMember(signer, current.Owner, policy):
		r.Allowed, r.Grant = true, current.Provenance+"-owner"
	case policy != nil && approverRule(signer, policy) != "":
		r.Allowed, r.Grant = true, "default-approver:"+approverRule(signer, policy)
	case policy == nil:
		r.Code, r.Unverified = OwnerPolicyUnverified, "no verified owner policy: "+policyFrom
	default:
		r.Code = OwnerSignerNotAllowed
	}
	r.Message = ownerRightMessage(r, signer, current, policy, policyFrom)
	return r
}

// JudgeOwnerSigners decides the owner change a bundle makes from each of its
// verified signers through DecideOwnerRight: the first allowed verdict wins,
// else the most decisive refusal (a person outside the rule over a signer who
// is no person over an unknown policy). With no verified signer the change is
// unverified, OWNER_SIGNER_UNVERIFIED, and why says what was read.
func JudgeOwnerSigners(service string, signers []PublisherIdentity, why string, current, proposed OwnerClaim, policy *OwnerPolicy, policyFrom string, now time.Time) OwnerRight {
	var best *OwnerRight
	for _, s := range signers {
		v := DecideOwnerRight(service, s, current, proposed, policy, policyFrom, now)
		if v.Allowed {
			return v
		}
		if best == nil || decisive(v.Code) > decisive(best.Code) {
			best = &v
		}
	}
	if best != nil {
		return *best
	}
	why = cmp.Or(why, "no signature of it verified")
	return OwnerRight{WorkloadAccess: WorkloadAccess{Resource: "services", Name: service, Source: AccessByRegistryPolicy,
		Unverified: why, At: now.UTC().Format(time.RFC3339)}, Code: OwnerSignerUnverified, MayAccept: mayAccept(current, policy),
		Message: "the owner this version of " + service + " sets is unverified: " + why}
}

// decisive ranks the refusal codes, the most decisive highest.
func decisive(code string) int {
	return len(OwnerRightCodes) - slices.Index([]string{OwnerSignerNotAllowed, OwnerSignerNotPerson, OwnerPolicyUnverified, OwnerSignerUnverified}, code)
}

// The owner provenances a member of the owner is read from (516-S1's
// constants in pkg/fleet).
const (
	ownerDeclared = "declared"
	ownerAttested = "attested"
)

// isMember reports whether signer belongs to owner: a team rule of the
// policy, or a person's email login equal to the DRI or an email contact.
func isMember(signer PublisherIdentity, owner contract.Owner, policy *OwnerPolicy) bool {
	if policy != nil && owner.Team != "" && slices.ContainsFunc(policy.Teams[owner.Team], func(r SignerRule) bool { return ruleMatches(r, signer) }) {
		return true
	}
	if !emailIssuerAllowed(signer.Issuer, policy) || !strings.Contains(signer.Subject, "@") {
		return false
	}
	if strings.EqualFold(signer.Subject, owner.DRI) {
		return true
	}
	return slices.ContainsFunc(owner.Contacts, func(c contract.OwnerContact) bool {
		return c.Type == "email" && strings.EqualFold(c.Value, signer.Subject)
	})
}

// emailIssuerAllowed reports whether issuer's email subjects name people:
// the policy's email issuers, else DefaultEmailIssuers.
func emailIssuerAllowed(issuer string, policy *OwnerPolicy) bool {
	issuers := DefaultEmailIssuers
	if policy != nil && len(policy.EmailIssuers) > 0 {
		issuers = policy.EmailIssuers
	}
	return slices.Contains(issuers, issuer)
}

// approverRule is the name (else the subject) of the first default approver
// rule signer matches, "" for none.
func approverRule(signer PublisherIdentity, policy *OwnerPolicy) string {
	for _, r := range policy.DefaultApprovers {
		if ruleMatches(r, signer) {
			return cmp.Or(r.Name, r.Subject)
		}
	}
	return ""
}

// ruleMatches reports whether signer has r's issuer and a subject r pins.
func ruleMatches(r SignerRule, signer PublisherIdentity) bool {
	return r.Issuer == signer.Issuer && anchoredMatch(r.Subject, signer.Subject)
}

// mayAccept is who may set an owner over current: its team's members and
// DRI, when current is declared or attested, then each default approver
// rule, deduplicated and bounded.
func mayAccept(current OwnerClaim, policy *OwnerPolicy) []string {
	var out []string
	if current.Provenance == ownerDeclared || current.Provenance == ownerAttested {
		if current.Owner.Team != "" {
			out = append(out, current.Owner.Team+" members")
		}
		if current.Owner.DRI != "" {
			out = append(out, current.Owner.DRI)
		}
	}
	if policy != nil {
		for _, r := range policy.DefaultApprovers {
			out = append(out, cmp.Or(r.Name, r.Subject))
		}
	}
	out = dedupe(out)
	return out[:min(len(out), maxMayAccept)]
}

// dedupe is xs with every repeat after the first dropped, in order.
func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// ownerRightMessage is the verdict as one sentence, one builder per code.
func ownerRightMessage(r OwnerRight, signer PublisherIdentity, current OwnerClaim, policy *OwnerPolicy, policyFrom string) string {
	switch r.Code {
	case "":
		return "allowed to set service.owner of " + r.Name + " (" + r.Grant + ")"
	case OwnerSignerNotPerson:
		return notPersonMessage(signer, current, policy)
	case OwnerPolicyUnverified:
		return "who may set service.owner is unknown: no verified owner policy was read (" + policyFrom + "); " + whoMay(current, policy) + " may set it"
	}
	return notAllowedMessage(current, policy)
}

// notPersonMessage refuses a signer who is no person.
func notPersonMessage(signer PublisherIdentity, current OwnerClaim, policy *OwnerPolicy) string {
	who := cmp.Or(signer.Subject, "an unsigned run")
	return "only a person may set service.owner, and " + who + " signs as " + cmp.Or(string(signer.Class), "no person") + "; " +
		whoMay(current, policy) + " may set it"
}

// notAllowedMessage refuses a person outside the rule, in the approved UX
// form: service.owner is attested as team payments (<citation>); only
// payments members or default approvers (platform-admins) may set it.
func notAllowedMessage(current OwnerClaim, policy *OwnerPolicy) string {
	label := ownerLabel(current.Owner)
	switch current.Provenance {
	case ownerAttested:
		return "service.owner is attested as " + label + " (" + current.Source + "); " + whoMay(current, policy) + " may set it"
	case ownerDeclared:
		return "service.owner is declared as " + label + "; " + whoMay(current, policy) + " may set it"
	}
	return "service.owner has no declared or attested owner yet; " + whoMay(current, policy) + " may set the first one"
}

// ownerLabel is how a sentence names owner: team payments, DRI ana@acme.com.
func ownerLabel(o contract.Owner) string {
	if k, ok := o.Key(); ok {
		return strings.ToLower(k.KindLabel()) + " " + k.Value
	}
	return "its contacts"
}

// whoMay names who may set the owner over current: only payments members or
// default approvers (platform-admins). With no verified policy the default
// approvers are unknown, never absent, and the list is never "only".
func whoMay(current OwnerClaim, policy *OwnerPolicy) string {
	var parts []string
	if current.Provenance == ownerDeclared || current.Provenance == ownerAttested {
		switch {
		case current.Owner.Team != "":
			parts = append(parts, current.Owner.Team+" members")
		case current.Owner.DRI != "":
			parts = append(parts, current.Owner.DRI)
		}
	}
	var names []string
	if policy != nil {
		for _, r := range policy.DefaultApprovers {
			names = append(names, cmp.Or(r.Name, r.Subject))
		}
	}
	if names = dedupe(names); len(names) > 0 {
		parts = append(parts, "default approvers ("+strings.Join(names[:min(len(names), maxMayAccept)], ", ")+")")
	}
	if policy == nil {
		return strings.Join(append(parts, "the default approvers (unknown until a verified owner policy is read)"), " or ")
	}
	if len(parts) == 0 {
		return "nobody (no default approvers are configured)"
	}
	return "only " + strings.Join(parts, " or ")
}
