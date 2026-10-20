package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// Tier 0 operation type ids.
const (
	TypePromote  = "pacto.dev/promote"
	TypeRollback = "pacto.dev/rollback"
	TypeOverride = "pacto.dev/override"
	TypeUnfreeze = "pacto.dev/unfreeze"
)

// Gate ids.
const (
	GateDiff     = "pacto.diff"
	GateImpact   = "pacto.impact"
	GateContract = "pacto.contract"
)

// OperationType is a type descriptor: what an operation of this type changes,
// what checks it, when a person is needed and how it is undone. Agents discover
// types; they never register them. In M0 the four Tier 0 types are compiled-in
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
		PreGates:          []string{GateDiff, GateImpact},
		VerifyGates:       []string{GateContract},
		ApprovalFloor:     "pkg/impact: no person when no consumer is incompatible or unknown, the analysis is complete and the version to roll back to is verified; an incompatible consumer asks its owners; anything Pacto cannot see asks a person to accept the unknown",
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
		ApprovalFloor:     "none; an agent rollback on a frozen Stage needs a Fail verdict as its cause",
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
}

// Types returns the Tier 0 operation types.
func Types() []OperationType { return slices.Clone(tier0) }

// TypeByID returns the Tier 0 type with the given id.
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
	User   string
	Groups []string
}

// ClassPolicy names the identities the promoter treats specially.
type ClassPolicy struct {
	// ControllerUser is the promoter's own service account username.
	ControllerUser string
	// AgentGroups mark agents running under a person-like identity.
	AgentGroups []string
	// BreakGlassGroups may override and edit the pointer directly.
	BreakGlassGroups []string
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
	case isServiceAccount(id.User) || intersects(id.Groups, p.AgentGroups):
		return ClassAgent
	default:
		return ClassHuman
	}
}

// IsHuman reports whether id may approve: not a service account and in no agent
// or controller group.
func (p ClassPolicy) IsHuman(id Identity) bool {
	return id.User != p.ControllerUser && !isServiceAccount(id.User) && !intersects(id.Groups, p.AgentGroups)
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
