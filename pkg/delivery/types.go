package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Operation type ids: the four Tier 0 types, freeze and autonomy.
const (
	TypePromote  = "delivery.pacto.trianalab.io/promote"
	TypeRollback = "delivery.pacto.trianalab.io/rollback"
	TypeOverride = "delivery.pacto.trianalab.io/override"
	TypeUnfreeze = "delivery.pacto.trianalab.io/unfreeze"
	TypeFreeze   = "delivery.pacto.trianalab.io/freeze"
	TypeAutonomy = "delivery.pacto.trianalab.io/autonomy"
)

// Gate ids.
const (
	GateDiff     = "pacto.diff"
	GateImpact   = "pacto.impact"
	GateContract = "pacto.contract"
	// GatePublished admits a promote only of a version `pacto publish`
	// published: signed (Flux verified it on the candidate source) and
	// carrying its contract.
	GatePublished = "pacto.published"
	// GateAutonomy judges a change of rung.
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
		PreGates:          []string{GatePublished, GateDiff, GateImpact},
		VerifyGates:       []string{GateContract},
		ApprovalFloor:     "pkg/impact: no person when no consumer is incompatible or unknown, the analysis is complete and the version to roll back to is verified; an incompatible consumer asks its owners; anything Pacto cannot see asks a person to accept the unknown; with spec.records.required, an agent or promoter promote asks a person while the Stage's records are not being written",
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
		ApprovalFloor:     "none; an agent rollback on a frozen Stage needs a Fail verdict as its cause; with spec.records.required, an agent or promoter rollback asks a person while the Stage's records are not being written",
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
		ID: TypeAutonomy, Version: "1.0.0", Facet: FacetRung,
		Description:       "Move the Stage one rung up or down the autonomy ladder: observe, advise, auto-nonbreaking, autonomous.",
		Params:            []string{"from", "to"},
		Raisers:           []ActorClass{ClassHuman, ClassAgent, ClassBreakGlass, ClassController},
		PreGates:          []string{GateAutonomy},
		VerifyGates:       []string{},
		ApprovalFloor:     "raise one rung: a person other than the raiser, from the team of the running service; lower: none. The promoter only steps down one rung from an automatic rung, and an agent never lowers to observe",
		Inverse:           TypeAutonomy,
		Cancellable:       "until it applies",
		ApprovalDeadline:  defaultApprovalDeadline,
		ExecutionDeadline: defaultExecutionDeadline,
	},
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
	// TeamGroupPrefix maps a contract owner team to an identity-provider group:
	// a member of group TeamGroupPrefix+team belongs to the team. A group named
	// exactly like the team always counts.
	TeamGroupPrefix string `json:"teamGroupPrefix,omitempty"`
	// RequireOwnerTeam leaves a question that names no team to nobody: only
	// break-glass can then override. Off, any person may answer it.
	RequireOwnerTeam bool `json:"requireOwnerTeam"`
	// HumanGroups, when set, is the only way to count as a person: anyone in
	// none of them is an agent. Empty classifies by AgentGroups alone, so an
	// agent identity left out of every agent group counts as a person.
	HumanGroups []string `json:"humanGroups,omitempty"`
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
	return ClassPolicy{AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}, RequireOwnerTeam: true}
}

// ParseClassPolicy reads a published ClassPolicy.
func ParseClassPolicy(b []byte) (ClassPolicy, error) {
	var p ClassPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return ClassPolicy{}, fmt.Errorf("the delivery identity policy: %w", err)
	}
	return p, nil
}

// Caller is who the apiserver says is calling (a SelfSubjectReview), the class
// that makes them and when they last logged in interactively.
type Caller struct {
	Identity
	Class    ActorClass `json:"class"`
	AuthTime string     `json:"authTime,omitempty"`
	// Policy is the published ClassPolicy the caller was classified with,
	// for the teams and person checks a client makes before it offers.
	Policy ClassPolicy `json:"-"`
}

// InTeams reports whether groups name one of the teams: the group T, or with
// a TeamGroupPrefix only that prefix plus T. A Kubernetes system: group is
// never a team, as every user carries one. No teams means the question names
// nobody: any person may answer it, or nobody with RequireOwnerTeam.
func (p ClassPolicy) InTeams(groups, teams []string) bool {
	if len(teams) == 0 {
		return !p.RequireOwnerTeam
	}
	for _, t := range teams {
		if g := p.TeamGroupPrefix + t; !strings.HasPrefix(g, "system:") && slices.Contains(groups, g) {
			return true
		}
	}
	return false
}

// Classify derives the actor class. Service accounts other than the promoter
// are agents: an agent must run under its own identity, and a person never logs
// in as a service account.
func (p ClassPolicy) Classify(id Identity) ActorClass {
	switch {
	case id.User == p.ControllerUser:
		return ClassController
	case intersects(id.Groups, p.BreakGlassGroups):
		return ClassBreakGlass
	case isServiceAccount(id.User) || intersects(id.Groups, p.AgentGroups) || p.unenrolled(id):
		return ClassAgent
	default:
		return ClassHuman
	}
}

// IsHuman reports whether id may approve: not a service account, in no agent
// or controller group and, when HumanGroups is set, in one of them.
func (p ClassPolicy) IsHuman(id Identity) bool {
	return id.User != p.ControllerUser && !isServiceAccount(id.User) && !intersects(id.Groups, p.AgentGroups) && !p.unenrolled(id)
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
