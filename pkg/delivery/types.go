package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Operation type ids: the four Tier 0 types, freeze, autonomy, acknowledge and trust.
const (
	TypePromote  = "delivery.pacto.trianalab.io/promote"
	TypeRollback = "delivery.pacto.trianalab.io/rollback"
	TypeOverride = "delivery.pacto.trianalab.io/override"
	TypeUnfreeze = "delivery.pacto.trianalab.io/unfreeze"
	TypeFreeze   = "delivery.pacto.trianalab.io/freeze"
	TypeAutonomy = "delivery.pacto.trianalab.io/autonomy"
	// TypeAcknowledge accepts one unrecordable stand-in in the upstream
	// stream a Stage follows, so the Stage offers versions again.
	TypeAcknowledge = "delivery.pacto.trianalab.io/acknowledge"
	// TypeTrust asks a person to confirm a CI identity no publisher rule,
	// pin or policy covers; once it succeeds the recorder pins it.
	TypeTrust = "delivery.pacto.trianalab.io/trust"
)

// Gate ids.
const (
	GateDiff     = "pacto.diff"
	GateImpact   = "pacto.impact"
	GateContract = "pacto.contract"
	// GatePublished admits a promote only of a version `pacto publish`
	// published: signed (Flux or the registry watch verified it on the
	// candidate source) and carrying its contract.
	GatePublished = "pacto.published"
	// GateSuites holds a promote of a version that does not ship a suite
	// one of the Stage's checks runs.
	GateSuites = "pacto.suites"
	// GateEnvironment holds a promote of a version whose contract declares
	// environments but does not back the Stage's: no environment of its
	// name, or values that fail their configuration schemas.
	GateEnvironment = "pacto.environment"
	// GateApplier refuses a promote while the Stage's applier is not the
	// manager that owns the workloads it delivers (status.applier), or the
	// version's contract names another applier: Pacto never writes a pointer
	// another manager may overwrite.
	GateApplier = "pacto.applier"
	// GateAutonomy judges a change of level.
	GateAutonomy = "pacto.autonomy"
)

// OperationType is a type descriptor: what an operation of this type changes,
// what checks it, when a person is needed and how it is undone. Agents discover
// types; they never register them. The types are compiled-in
// values and a switch on Facet executes them.
type OperationType struct {
	ID          string       `json:"id"`
	Version     string       `json:"version"`
	Description string       `json:"description"`
	Facet       string       `json:"facet"`
	Params      []string     `json:"params"`
	Raisers     []ActorClass `json:"raisers"`
	// RequiresWitnessed is the class that may raise this type only as a
	// witnessed operation (the promoter recording an edit it did not make).
	RequiresWitnessed ActorClass `json:"requiresWitnessed,omitempty"`
	PreGates          []string   `json:"preGates"`
	VerifyGates       []string   `json:"verifyGates"`
	ApprovalFloor     string     `json:"approvalFloor"`
	Inverse           string     `json:"inverse,omitempty"`
	Cancellable       string     `json:"cancellable"`
	ApprovalDeadline  string     `json:"approvalDeadline"`
	ExecutionDeadline string     `json:"executionDeadline"`
}

// Digest is the sha256 of the descriptor's encoding/json bytes. Records cite it.
func (t OperationType) Digest() string {
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Deadlines returns the type's approval and execution deadlines, with the
// Stage's overrides applied when they parse.
func (t OperationType) Deadlines(stage Deadlines) (approval, execution time.Duration) {
	approval = durationOr(stage.Approval, t.ApprovalDeadline)
	execution = durationOr(stage.Execution, t.ExecutionDeadline)
	return approval, execution
}

// durationOr parses v, falling back to def (which always parses).
func durationOr(v, def string) time.Duration {
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	d, _ := time.ParseDuration(def)
	return d
}

const (
	defaultApprovalDeadline  = "24h"
	defaultExecutionDeadline = "15m"
	// DefaultVerifyWindow applies when a Stage does not set one.
	DefaultVerifyWindow = "5m"
	// DefaultRecheck is how often a shipped version is checked again.
	DefaultRecheck = "1m"
)

var tier0 = []OperationType{
	{
		ID: TypePromote, Version: "1.0.0", Facet: FacetPointer,
		Description:       "Move the Stage pointer to a new digest. The only way to ship a version.",
		Params:            []string{"from", "to"},
		Raisers:           []ActorClass{ClassAgent, ClassHuman, ClassController},
		PreGates:          []string{GatePublished, GateApplier, GateDiff, GateImpact, GateSuites, GateEnvironment},
		VerifyGates:       []string{GateContract},
		ApprovalFloor:     "pkg/impact: no person when no consumer is incompatible or unknown, the analysis is complete and the version to roll back to is verified; an incompatible consumer, or one whose required guarantee the version does not meet, asks its owners; a version whose contract declares environments but none for the Stage's spec.environment, or values there that fail their schemas, asks the service's team; anything Pacto cannot see, a suite a Stage check runs that the version does not ship or environment values Pacto cannot check, asks a person to accept the unknown; with spec.records.required, an agent or promoter promote asks a person while the Stage's records are not being written; a workload another manager owns, or whose manager Pacto cannot tell (pacto.applier), refuses the promote, and nobody can accept it",
		Inverse:           TypeRollback,
		Cancellable:       "until the pointer moves",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeRollback, Version: "1.0.0", Facet: FacetPointer,
		Description:       "Undo one specific operation: move the pointer back to that operation's before digest.",
		Params:            []string{"from", "to", "undoes"},
		Raisers:           []ActorClass{ClassAgent, ClassHuman, ClassController},
		PreGates:          []string{},
		VerifyGates:       []string{GateContract},
		ApprovalFloor:     "none, unless a person or agent raised it and a consumer does not accept the version it returns to: then that consumer's team approves; only a person or break-glass may undo a rollback or an override, which for anyone else is a promote; an agent rollback on a frozen Stage needs a Fail verdict as its cause; with spec.records.required, an agent or promoter rollback asks a person while the Stage's records are not being written",
		Inverse:           TypePromote,
		Cancellable:       "until the pointer moves",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeOverride, Version: "1.0.0", Facet: FacetPointer,
		Description:       "Break glass: move the pointer at once, with no gates, and freeze the Stage until a person unfreezes it.",
		Params:            []string{"from", "to"},
		Raisers:           []ActorClass{ClassBreakGlass},
		RequiresWitnessed: ClassController,
		PreGates:          []string{},
		VerifyGates:       []string{},
		ApprovalFloor:     "none",
		Inverse:           TypeUnfreeze,
		Cancellable:       "never",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeUnfreeze, Version: "1.0.0", Facet: FacetFreeze,
		Description:       "Clear a freeze or an auto-hold so automation may move the Stage again.",
		Params:            []string{},
		Raisers:           []ActorClass{ClassHuman, ClassBreakGlass},
		PreGates:          []string{},
		VerifyGates:       []string{},
		ApprovalFloor:     "none",
		Cancellable:       "until it applies",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeFreeze, Version: "1.0.0", Facet: FacetFreeze,
		Description:       "Freeze the Stage on the version it runs, so automation stops moving it until a person unfreezes it. The promoter raises one when a check after release fails and it cannot roll back, so the record names the failed version.",
		Params:            []string{"from"},
		Raisers:           []ActorClass{ClassHuman, ClassBreakGlass, ClassController},
		PreGates:          []string{},
		VerifyGates:       []string{},
		ApprovalFloor:     "none",
		Inverse:           TypeUnfreeze,
		Cancellable:       "until it applies",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeAutonomy, Version: "1.0.0", Facet: FacetLevel,
		Description:       "Move the Stage one level up or down the autonomy ladder: observe, advise, auto-nonbreaking, autonomous.",
		Params:            []string{"from", "to"},
		Raisers:           []ActorClass{ClassHuman, ClassAgent, ClassBreakGlass, ClassController},
		PreGates:          []string{GateAutonomy},
		VerifyGates:       []string{},
		ApprovalFloor:     "raise one level: a person other than the raiser, from the team of the running service; lower: none. The promoter only steps down one level from an automatic level, and an agent never lowers to observe",
		Inverse:           TypeAutonomy,
		Cancellable:       "until it applies",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeAcknowledge, Version: "1.0.0", Facet: FacetUpstream,
		Description:       "Acknowledge one unrecordable stand-in in the upstream stream this Stage follows, after a person checked what the operation did upstream, so the Stage offers versions again. It needs a reason, and the record keeps who acknowledged which stand-in.",
		Params:            []string{"record"},
		Raisers:           []ActorClass{ClassHuman, ClassBreakGlass},
		PreGates:          []string{},
		VerifyGates:       []string{},
		ApprovalFloor:     "none; only a person of the team of the service running on the Stage, or break-glass",
		Cancellable:       "until it applies",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
	{
		ID: TypeTrust, Version: "1.0.0", Facet: FacetTrust,
		Description:       "Confirm that a CI identity no publisher rule, pin or policy covers may publish the Stage's service. The promoter raises it from the registry watch; once a person approves, the recorder writes a signed pin the watch reads, and a later different signer asks again.",
		Params:            []string{"to", "publisher"},
		Raisers:           []ActorClass{ClassController},
		PreGates:          []string{},
		VerifyGates:       []string{},
		ApprovalFloor:     "always a person who may update the workload of the service's first Stage (and, when owner teams narrow who may answer, in the team of the service running there); an agent never decides",
		Cancellable:       "until approved",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
}

// AdmitAcknowledge says why an acknowledge cannot be admitted on a Stage, or
// "": the Stage must follow records from another cluster, the record must be
// a record id and a person must say why. Who may raise it is MayRaise and,
// for a person, the team rule the promoter applies at admission.
func AdmitAcknowledge(spec OperationSpec, stage StageSpec) string {
	switch {
	case spec.Type != TypeAcknowledge:
		return ""
	case stage.Candidates.UpstreamRecords == nil:
		return "NoUpstreamRecords: " + spec.Stage + " does not follow the records of a Stage in another cluster, so it has no stand-in to acknowledge"
	case !validRecordID(spec.Params.Record):
		return "InvalidRecord: record must be the sha256 id of the upstream stand-in, not " + strconv.Quote(spec.Params.Record)
	case strings.TrimSpace(spec.Reason) == "":
		return "ReasonRequired: say what you checked upstream, for the record"
	}
	return ""
}

func validRecordID(id string) bool {
	_, err := ParseDigest(id)
	return err == nil
}

// Types returns the operation types.
func Types() []OperationType { return slices.Clone(tier0) }

// TypeByID returns the operation type with the given id.
func TypeByID(id string) (OperationType, bool) {
	for _, t := range tier0 {
		if t.ID == id {
			return t, true
		}
	}
	return OperationType{}, false
}

// MayRaise reports whether class may raise an operation of type id. witnessed
// is the operation's witnessed flag: a controller-class override is legal only
// as a witnessed one, and only the controller may set the flag at all.
func MayRaise(id string, class ActorClass, witnessed bool) bool {
	t, ok := TypeByID(id)
	if !ok {
		return false
	}
	if witnessed {
		return class == ClassController && t.RequiresWitnessed == ClassController
	}
	return slices.Contains(t.Raisers, class)
}

// Identity is what the apiserver says about a user, and the groups that make it
// an agent, a controller or break-glass.
type Identity struct {
	User   string   `json:"user"`
	Groups []string `json:"groups,omitempty"`
}

// ClassPolicy names the identities the promoter treats specially. The
// chart's delivery section publishes it as JSON in the PolicyConfigMap, which the
// promoter runs with and every client reads, so they cannot drift.
type ClassPolicy struct {
	// ControllerUser is the promoter's own service account username.
	ControllerUser string `json:"-"`
	// AgentGroups mark agents running under a person-like identity.
	AgentGroups []string `json:"agentGroups,omitempty"`
	// BreakGlassGroups may override and edit the pointer directly.
	BreakGlassGroups []string `json:"breakGlassGroups,omitempty"`
	// TeamGroupPrefix maps a contract owner team to an identity-provider group
	// when TeamNarrowing is on: a member of group TeamGroupPrefix+team belongs
	// to the team. Empty, the group named exactly like the team counts.
	TeamGroupPrefix string `json:"teamGroupPrefix,omitempty"`
	// TeamNarrowing makes owner teams narrow who may answer a question. Off,
	// owner teams only route it. The chart sets it when
	// delivery.identities.teamGroupPrefix is set, even to "".
	TeamNarrowing bool `json:"teamNarrowing,omitempty"`
	// RequireOwnerTeam leaves a question that names no team to nobody: only
	// break-glass can then override. Off, any person may answer it.
	RequireOwnerTeam bool `json:"requireOwnerTeam"`
	// HumanGroups is a deprecated override: when set, anyone in none of them
	// is an agent. Empty, people and agents are told apart by how they sign
	// in (ClassifyBy).
	HumanGroups []string `json:"humanGroups,omitempty"`
	// GateProviders names the type of each gate provider the chart
	// configures (delivery.gates.providers), never its URL or signers, so a
	// client's plan reads which checks run on an eval provider as the
	// promoter's does.
	GateProviders map[string]string `json:"gateProviders,omitempty"`
	// Signers are the issuers whose keyless certificates may sign an
	// approval. Empty, keyless approvals never count.
	Signers []PersonSigner `json:"signers,omitempty"`
}

// PersonSigner is one issuer whose keyless certificates may sign an
// approval, and how a client signs with it.
type PersonSigner struct {
	// Issuer is the OIDC issuer URL the certificate names.
	Issuer string `json:"issuer"`
	// ClientID is the OIDC client a person's client logs in with.
	ClientID string `json:"clientID,omitempty"`
	// UsernamePrefix is put before the certificate's email to name the
	// Kubernetes user the approval is checked as.
	UsernamePrefix string `json:"usernamePrefix,omitempty"`
	// Subjects is the regular expression an approver's whole email must
	// match (it is anchored at both ends); required, since an issuer vouches
	// for addresses that are no approver.
	Subjects string `json:"subjects"`
	// FulcioURL and TimestampURL are where a client gets its certificate
	// and its signed timestamp.
	FulcioURL    string `json:"fulcioURL,omitempty"`
	TimestampURL string `json:"timestampURL,omitempty"`
	// TrustedRoot is the Sigstore trusted_root.json the certificate chains to.
	TrustedRoot string `json:"trustedRoot,omitempty"`
}

// SignerFor is the configured signer of issuer.
func (p ClassPolicy) SignerFor(issuer string) (PersonSigner, bool) {
	i := slices.IndexFunc(p.Signers, func(s PersonSigner) bool { return s.Issuer == issuer })
	if i < 0 {
		return PersonSigner{}, false
	}
	return p.Signers[i], true
}

// ApproverIdentity is who a keyless approval from issuer's certificate
// naming san speaks for: usernamePrefix+san with no groups, since a
// certificate names none. A URI SAN (a CI workflow), a SAN outside the
// signer's subjects or in a machine domain, or an identity ClassifyBy calls
// no person is ClassBySignerURI, an agent; any other is ClassByUser. An
// issuer with no configured signer is an error.
func ApproverIdentity(issuer, san string, p ClassPolicy) (Identity, ClassSource, error) {
	s, ok := p.SignerFor(issuer)
	switch {
	case !ok:
		return Identity{}, "", fmt.Errorf("no approver signer is configured for the issuer %s", issuer)
	case san == "":
		return Identity{}, "", errors.New("the certificate names no subject")
	case s.Subjects == "":
		return Identity{}, "", fmt.Errorf("the signer for %s has no subjects, so it vouches for nobody", issuer)
	}
	// Anchored: an unanchored @acme\.com would also take eve@acme.com.evil.io.
	re, err := regexp.Compile("^(?:" + s.Subjects + ")$")
	if err != nil {
		return Identity{}, "", fmt.Errorf("the subjects of the signer for %s: %w", issuer, err)
	}
	id := Identity{User: s.UsernamePrefix + san}
	lower := strings.ToLower(san)
	machine := strings.HasSuffix(lower, "gserviceaccount.com") || strings.Contains(lower, ".svc.id.goog")
	if class, _ := p.ClassifyBy(id); strings.Contains(san, "://") || machine || !re.MatchString(san) || class != ClassHuman {
		return id, ClassBySignerURI, nil
	}
	return id, ClassByUser, nil
}

// KeylessRefusal is the guard every keyless approval path asks before
// ApprovalRefusalAsking: a signer ApproverIdentity calls an agent never
// approves. IsHuman alone would call a URI SAN a person.
func KeylessRefusal(id Identity, source ClassSource) (code, why string) {
	if source == ClassBySignerURI {
		return "AgentsMayNotApprove", "agents cannot approve: " + id.User + " is " + sourcePhrase(source)
	}
	return "", ""
}

// PlanGates is GateProviders as PlanInput.Gates: planning only asks a
// provider's type, so each eval provider is an Eval that is never run.
func (p ClassPolicy) PlanGates() map[string]Gate {
	gates := map[string]Gate{}
	for name, typ := range p.GateProviders {
		if typ == ProviderEval {
			gates[name] = Eval{}
		}
	}
	return gates
}

// PolicyConfigMap is the ConfigMap, in delivery.namespace, that
// holds the ClassPolicy as JSON under PolicyKey. Every authenticated user may
// read it.
const (
	PolicyConfigMap = "pacto-delivery-identities"
	PolicyKey       = "policy.json"
	// PolicyNamespace is the default delivery.namespace.
	PolicyNamespace = "pacto-delivery"
)

// DefaultClassPolicy is the chart's default delivery.identities: what a client assumes
// when the policy is not published.
func DefaultClassPolicy() ClassPolicy {
	return ClassPolicy{AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}}
}

// ParseClassPolicy reads a published ClassPolicy. A policy that carries
// teamGroupPrefix and no teamNarrowing was published by a chart from before
// narrowing was opt-in, whose promoter always narrows, so narrowing is on:
// the chart now writes teamGroupPrefix only together with teamNarrowing.
func ParseClassPolicy(b []byte) (ClassPolicy, error) {
	var p ClassPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return ClassPolicy{}, fmt.Errorf("the delivery identity policy: %w", err)
	}
	var keys struct {
		Prefix    *string `json:"teamGroupPrefix"`
		Narrowing *bool   `json:"teamNarrowing"`
	}
	_ = json.Unmarshal(b, &keys) // the same bytes parsed above
	if keys.Prefix != nil && keys.Narrowing == nil {
		p.TeamNarrowing = true
	}
	return p, nil
}

// Caller is who the apiserver says is calling (a SelfSubjectReview), the class
// that makes them and when they last logged in interactively.
type Caller struct {
	Identity
	Class       ActorClass  `json:"class"`
	ClassSource ClassSource `json:"classSource,omitempty" doc:"How the class was decided: controller, break-glass-group, service-account, agent-group, outside-human-groups, system-user or user"`
	AuthTime    string      `json:"authTime,omitempty"`
	// Policy is the published ClassPolicy the caller was classified with,
	// for the teams and person checks a client makes before it offers.
	Policy ClassPolicy `json:"-"`
	// PolicyErr says why the published policy could not be read, so Policy
	// is the defaults and Class may be wrong: offers are then unavailable.
	PolicyErr string `json:"-"`
}

// InTeams reports whether groups name one of the teams: the group T, or with
// a TeamGroupPrefix only that prefix plus T. A Kubernetes system: group is
// never a team, as every user carries one. No teams means the question names
// nobody: any person may answer it, or nobody with RequireOwnerTeam. With
// TeamNarrowing off any groups answer a named team. It is the one decision
// that reads TeamNarrowing, so every caller agrees; Narrows only says it.
func (p ClassPolicy) InTeams(groups, teams []string) bool {
	if len(teams) == 0 {
		return !p.RequireOwnerTeam
	}
	if !p.TeamNarrowing {
		return true
	}
	for _, t := range teams {
		if g := p.TeamGroupPrefix + t; !strings.HasPrefix(g, "system:") && slices.Contains(groups, g) {
			return true
		}
	}
	return false
}

// Narrows reports whether owner teams narrow who may answer, for copy that
// says so. It decides nothing: who may answer is InTeams.
func (p ClassPolicy) Narrows() bool { return p.TeamNarrowing }

// ClassSource says how ClassifyBy decided an identity's class.
type ClassSource string

const (
	// ClassByController: the promoter's own service account.
	ClassByController ClassSource = "controller"
	// ClassByBreakGlassGroup: a member of a break-glass group.
	ClassByBreakGlassGroup ClassSource = "break-glass-group"
	// ClassByServiceAccount: a Kubernetes service account, always an agent.
	ClassByServiceAccount ClassSource = "service-account"
	// ClassByAgentGroup: a member of an agent group, automation that signs in
	// through OIDC.
	ClassByAgentGroup ClassSource = "agent-group"
	// ClassByOutsideHumanGroups: outside the deprecated HumanGroups override.
	ClassByOutsideHumanGroups ClassSource = "outside-human-groups"
	// ClassBySystemUser: a system: user that is no service account (a
	// control-plane component, a node or system:anonymous) or an
	// unauthenticated request. None of them is a person.
	ClassBySystemUser ClassSource = "system-user"
	// ClassByUser: any other authenticated identity, a person.
	ClassByUser ClassSource = "user"
	// ClassBySignerURI: a keyless signer whose certificate names a workload
	// or an address outside the approvers' subjects (ApproverIdentity).
	ClassBySignerURI ClassSource = "signer-uri"
)

// Classify derives the actor class (ClassifyBy without its source).
func (p ClassPolicy) Classify(id Identity) ActorClass {
	c, _ := p.ClassifyBy(id)
	return c
}

// ClassifyBy derives the actor class from how id signed in, and says how.
// Service accounts other than the promoter are agents: an agent must run
// under its own identity, and a person never logs in as a service account.
// So are system: users and unauthenticated requests. Every other
// authenticated identity is a person unless an agent group or the
// deprecated HumanGroups override says otherwise.
func (p ClassPolicy) ClassifyBy(id Identity) (ActorClass, ClassSource) {
	switch {
	case id.User == p.ControllerUser:
		return ClassController, ClassByController
	case intersects(id.Groups, p.BreakGlassGroups):
		return ClassBreakGlass, ClassByBreakGlassGroup
	case isServiceAccount(id.User):
		return ClassAgent, ClassByServiceAccount
	case intersects(id.Groups, p.AgentGroups):
		return ClassAgent, ClassByAgentGroup
	case p.unenrolled(id):
		return ClassAgent, ClassByOutsideHumanGroups
	case isSystemUser(id):
		return ClassAgent, ClassBySystemUser
	default:
		return ClassHuman, ClassByUser
	}
}

// IsHuman reports whether id may approve: not a service account or a system
// user, in no agent or controller group and, when HumanGroups is set, in one
// of them. A break-glass member who is otherwise a person counts.
func (p ClassPolicy) IsHuman(id Identity) bool {
	return id.User != p.ControllerUser && !isServiceAccount(id.User) && !intersects(id.Groups, p.AgentGroups) && !p.unenrolled(id) && !isSystemUser(id)
}

// isSystemUser: a system: username (a service account included) or the
// group every unauthenticated request carries.
func isSystemUser(id Identity) bool {
	return strings.HasPrefix(id.User, "system:") || slices.Contains(id.Groups, "system:unauthenticated")
}

// unenrolled: a human allowlist is set and id is in none of it.
func (p ClassPolicy) unenrolled(id Identity) bool {
	return len(p.HumanGroups) > 0 && !intersects(id.Groups, p.HumanGroups)
}

func isServiceAccount(user string) bool { return strings.HasPrefix(user, "system:serviceaccount:") }

func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
