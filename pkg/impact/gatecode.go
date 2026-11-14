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
	GateCodeAnswerPartial               = "ANSWER_PARTIAL"
	GateCodeConsumersUnchecked          = "CONSUMERS_UNCHECKED"
	// Clear.
	GateCodeNotBreaking       = "NOT_BREAKING"
	GateCodePotentialBreaking = "POTENTIAL_BREAKING"
	GateCodeNoConsumers       = "NO_CONSUMERS"
)

// GateCode is the stable reason code of the gate of a contract change answer,
// walking the same predicates in the same order gate() does. It is "" for an
// event answer.
func (r *Result) GateCode() string {
	if r.Event != nil {
		return ""
	}
	if code := r.blockingCode(); code != "" {
		return code
	}
	if r.policyUnchecked() != "" && r.Classification != "BREAKING" {
		return GateCodePolicyUnchecked
	}
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
	case r.Completeness != fleet.CompletenessComplete:
		return GateCodeAnswerPartial
	case len(r.Consumers) > 0:
		return GateCodeConsumersUnchecked
	}
	return GateCodeNoConsumers
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
func (r *Result) Blocking() int {
	n := 0
	for _, c := range r.Consumers {
		if r.blocks(c) {
			n++
		}
	}
	return n
}
