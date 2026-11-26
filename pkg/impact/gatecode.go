package impact

import "github.com/trianalab/pacto/v4/pkg/fleet"

// Gate reason codes: a stable code for why the gate holds, one per gate()
// branch. Each appears under exactly one verdict.
const (
	// Blocked.
	GateCodeConsumersIncompatible      = "CONSUMERS_INCOMPATIBLE"
	GateCodePolicyReferrersFailRunning = "POLICY_REFERRERS_FAIL_RUNNING"
	// Unverified.
	GateCodePolicyReferrersFailUnplaced = "POLICY_REFERRERS_FAIL_UNPLACED"
	GateCodePolicyUnchecked             = "POLICY_UNCHECKED"
	GateCodeServiceNotInFleet           = "SERVICE_NOT_IN_FLEET"
	GateCodeServiceNameInferred         = LimitationServiceNameInferred
	GateCodeAnswerPartial               = "ANSWER_PARTIAL"
	GateCodeClustersUnread              = fleet.LimitationClustersUnread
	GateCodeConsumersUnchecked          = "CONSUMERS_UNCHECKED"
	GateCodeInferredPossibleBreak       = "INFERRED_POSSIBLE_BREAK"
	GateCodeInferredProvider            = LimitationInferredProvider
	GateCodeRunningUnread               = "RUNNING_UNREAD"
	GateCodeFirstVersionUnchecked       = "FIRST_VERSION_UNCHECKED"
	// Clear.
	GateCodeNotBreaking          = "NOT_BREAKING"
	GateCodePotentialBreaking    = "POTENTIAL_BREAKING"
	GateCodeNoConsumers          = "NO_CONSUMERS"
	GateCodeBreakingAccepted     = "BREAKING_ACCEPTED"
	GateCodeFirstVersionAccepted = "FIRST_VERSION_ACCEPTED"
)

// GateCode is the stable reason code of the gate of a contract change answer,
// walking the same predicates in the same order gate() does. It is "" for an
// event answer.
func (r *Result) GateCode() string {
	if r.Event != nil {
		return ""
	}
	if r.inferredProvider() != "" {
		return GateCodeInferredProvider
	}
	code := r.blockingCode()
	if code == GateCodeConsumersIncompatible || code == GateCodePolicyReferrersFailRunning {
		return code
	}
	if policy, _ := r.policyGate(); policy != "" {
		return policy
	}
	if code != "" {
		return code
	}
	if len(r.guessedConsumers()) > 0 {
		return GateCodeConsumersUnchecked
	}
	if code := r.runningCode(); code != "" {
		return code
	}
	if code := r.unclassifiedCode(); code != "" {
		return code
	}
	return r.classifiedCode()
}

// classifiedCode is the code of gate()'s branches that follow the
// classification: the class itself unless BREAKING, then why a break passes
// or is held.
func (r *Result) classifiedCode() string {
	switch r.Classification {
	case "BREAKING":
	case "POTENTIAL_BREAKING":
		return GateCodePotentialBreaking
	default:
		return GateCodeNotBreaking
	}
	switch {
	case hasCode(r.Limitations, "SERVICE_NOT_IN_FLEET"):
		return GateCodeServiceNotInFleet
	case r.nameInferred() != "":
		return GateCodeServiceNameInferred
	case r.Completeness != fleet.CompletenessComplete:
		return GateCodeAnswerPartial
	case r.UnreadClusters > 0:
		return GateCodeClustersUnread
	case len(r.Consumers) > len(r.acceptedConsumers()):
		return GateCodeConsumersUnchecked
	case len(r.Consumers) > 0:
		return GateCodeBreakingAccepted
	}
	return GateCodeNoConsumers
}

// unclassifiedCode is the code of gate()'s unverified branches that come
// before the classification: an unchecked policy, then a possible break on an
// inferred interface; "" for neither.
func (r *Result) unclassifiedCode() string {
	switch {
	case r.policyUnchecked() != "" && r.Classification != "BREAKING":
		return GateCodePolicyUnchecked
	case r.advisoryGate() != "":
		return GateCodeInferredPossibleBreak
	}
	return ""
}

// blockingCode is the code of blockingGate(), "" when it returns no gate.
func (r *Result) blockingCode() string {
	failing, _ := r.policyFailures()
	switch {
	case r.ReleaseBlocking():
		if active, _ := r.blockingConsumers(); len(active) > 0 {
			return GateCodeConsumersIncompatible
		}
		return GateCodePolicyReferrersFailRunning
	case len(failing) > 0:
		return GateCodePolicyReferrersFailUnplaced
	}
	return ""
}

// Blocking counts the consumers that hold the gate (see [Result.blocks]).
func (r *Result) Blocking() int { return len(r.BlockingServices()) }

// BlockingServices names the consumers that hold the gate, in answer order.
func (r *Result) BlockingServices() []string {
	var out []string
	for _, c := range r.Consumers {
		if r.blocks(c) {
			out = append(out, c.Service)
		}
	}
	return out
}
