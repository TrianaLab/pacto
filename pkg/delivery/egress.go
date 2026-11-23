package delivery

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// RuntimeLimitations are the target limitations a Pacto's runtime
// observation adds: [CallerLimitations], [ExternalLimitations] and
// [EgressLimitations]. p must carry status.contract.
func RuntimeLimitations(p PactoView, reconciled *time.Time, now time.Time) []fleet.Limitation {
	lims := append(CallerLimitations(p, reconciled, now), ExternalLimitations(p, reconciled, now)...)
	return append(lims, EgressLimitations(p, reconciled, now)...)
}

// EgressState is how far a Pacto's observedEgress can be trusted: the state
// and reason the operator wrote, or unavailable with stale-status when it
// stopped rewriting them, the reason in plain words, and the nodes it counted.
type EgressState struct {
	State, Reason, Why    string
	Nodes, NodesWithAgent int32
}

// EgressCoverage is p's [EgressState]. ok is false when the status carries
// no egress coverage, or is older than [StatusFormatEgress]: kernel egress
// is not observed for this Pacto, which is no claim either way.
func EgressCoverage(p PactoView, reconciled *time.Time, now time.Time) (EgressState, bool) {
	cov := p.Status.ObservedEgressCoverage
	if cov == nil || p.Status.StatusFormat < StatusFormatEgress {
		return EgressState{}, false
	}
	state, reason := freshCoverage(cov.CallerCoverageView, reconciled, now)
	return EgressState{State: state, Reason: reason, Why: coverageWhy(state, reason), Nodes: cov.Nodes, NodesWithAgent: cov.NodesWithAgent}, true
}

// EgressLimitations says when the external systems the node agent saw a
// Pacto's target call cannot be listed in full: its egress coverage is not
// complete or went stale (the rule of [CallerLimitations]). The limitation is
// EXTERNALS_UNREPORTED, so every external answer turns partial while the rest
// of the snapshot stays complete. No coverage changes nothing (see
// [EgressCoverage]). p must carry status.contract.
func EgressLimitations(p PactoView, reconciled *time.Time, now time.Time) []fleet.Limitation {
	st, ok := EgressCoverage(p, reconciled, now)
	if !ok || st.State == "complete" {
		return nil
	}
	return []fleet.Limitation{{Code: fleet.LimitationExternalsUnreported, Source: SnapshotSourceID,
		Message: "the external calls " + p.Status.Contract.ServiceName + " makes cannot be ruled out: " + st.Why}}
}
