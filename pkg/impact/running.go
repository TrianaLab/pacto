package impact

import (
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// RunningBaseline is what a --running answer judged the new revision
// against: the revisions of the changed service that run on known targets.
type RunningBaseline struct {
	Refs         []string `json:"refs" doc:"The registry refs of the revisions that run on known targets, sorted; empty when none runs"`
	Judged       string   `json:"judged,omitempty" doc:"The running revision this answer compared the new one with; empty for a first version"`
	Unread       []string `json:"unread,omitempty" doc:"The running revisions Pacto could not read, link or trust, in words; the gate never clears over them"`
	FirstVersion bool     `json:"firstVersion,omitempty" doc:"No running revision could be judged, so the new revision was compared with itself and only the consumers' ranges speak"`
}

// maxUnreadInReason bounds the unread revisions a gate reason names.
const maxUnreadInReason = 3

// heldGate is the gate whatever the classification, in order: an inferred
// provider, a consumer shown to break, then a --running answer's rules;
// "" when none holds.
func (r *Result) heldGate() (string, string) {
	if why := r.inferredProvider(); why != "" {
		return GateUnverified, why
	}
	if gate, why := r.blockingGate(); gate != "" {
		return gate, why
	}
	return r.runningGate()
}

// runningGate is the gate of a --running answer when no consumer is shown to
// break: unverified when a running revision could not be read, the
// first-version gate when none runs, "" otherwise.
func (r *Result) runningGate() (string, string) {
	if n := len(r.unreadBaselines); n > 0 {
		names := r.unreadBaselines[:min(n, maxUnreadInReason)]
		why := strings.Join(names, "; ")
		if n > maxUnreadInReason {
			why += " and " + strconv.Itoa(n-maxUnreadInReason) + " more"
		}
		return GateUnverified, "Pacto could not read every revision of " + r.Service + " that runs, so the change cannot clear: " + why +
			". Make each running revision readable from the registry and run the analysis again"
	}
	if r.firstVersion {
		return r.firstVersionGate()
	}
	return "", ""
}

// firstVersionGate is the gate of a first version no consumer is shown to
// reject: clear only on a complete answer whose running consumers all accept
// the new version.
func (r *Result) firstVersionGate() (string, string) {
	lead := "first version of " + r.Service + " on a known target"
	if why := r.policyUnchecked(); why != "" {
		return GateUnverified, lead + "; " + why
	}
	switch {
	case r.Completeness != fleet.CompletenessComplete:
		return GateUnverified, lead + ", but the answer is partial, so consumers Pacto cannot see may not accept " + r.NewVersion +
			". Fix the sources named in limitations and run the analysis again"
	case r.nameInferred() != "":
		return GateUnverified, lead + ", but " + r.nameInferred()
	}
	running := 0
	var unknown []string
	for _, c := range r.Consumers {
		if len(c.Targets) == 0 {
			continue
		}
		running++
		if c.CompatibilityVerdict != CompatibilityCompatible {
			unknown = append(unknown, c.Service)
		}
	}
	switch {
	case len(unknown) > 0:
		return GateUnverified, lead + ", but Pacto cannot tell whether these running consumers accept " + r.NewVersion + ": " +
			strings.Join(unknown, ", ") + ". Declare a compatibility range on each dependency"
	case running == 0:
		return GateClear, lead + ": no consumer runs"
	}
	return GateClear, lead + ": every consumer that runs accepts " + r.NewVersion
}

// runningCode is the code of runningGate(), "" when it returns no gate.
func (r *Result) runningCode() string {
	switch g, _ := r.runningGate(); {
	case g == "":
		return ""
	case len(r.unreadBaselines) > 0:
		return GateCodeRunningUnread
	case g == GateClear:
		return GateCodeFirstVersionAccepted
	}
	return GateCodeFirstVersionUnchecked
}
