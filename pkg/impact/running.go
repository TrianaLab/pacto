package impact

import (
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// RunningBaseline is what a --running answer judged the new revision
// against: the revisions of the changed service that run on known targets.
type RunningBaseline struct {
	Refs         []string `json:"refs" doc:"The registry refs of the revisions that run on known targets, sorted, the new revision left out (see excluded); empty when no other revision runs"`
	Judged       string   `json:"judged,omitempty" doc:"The running revision this answer compared the new one with; empty for a first version"`
	Unread       []string `json:"unread,omitempty" doc:"The running revisions Pacto could not read, link or trust, in words; the gate never clears over them"`
	Excluded     []string `json:"excluded,omitempty" doc:"The running revisions with the new revision's digest, left out of the baseline: a revision is never its own baseline"`
	FirstVersion bool     `json:"firstVersion,omitempty" doc:"No running revision other than the new one could be judged, so the new revision was compared with itself and only the consumers' ranges speak"`
}

// maxUnreadInReason bounds the unread revisions a gate reason names.
const maxUnreadInReason = 3

// heldGate is the gate whatever the classification, in order: an inferred
// provider, a consumer shown to break, a failing range policy, the rest of
// blockingGate, then a --running answer's rules;
// "" when none holds.
func (r *Result) heldGate() (string, string) {
	if why := r.inferredProvider(); why != "" {
		return GateUnverified, why
	}
	gate, why := r.blockingGate()
	if gate == GateBlocked {
		return gate, why
	}
	if code, policyWhy := r.policyGate(); code != "" {
		return GateBlocked, "the change is BREAKING: " + policyWhy
	}
	if gate != "" {
		return gate, why
	}
	if guessed := r.guessedConsumers(); len(guessed) > 0 {
		return GateUnverified, guessedReason(guessed)
	}
	return r.runningGate()
}

// guessedReason is the unverified reason of consumers whose running version
// Pacto guessed.
func guessedReason(names []string) string {
	return "Pacto cannot tell which version of " + strings.Join(names, ", ") + " runs: its image digest is recorded by several release versions " +
		"(see " + fleet.LimitationLinkVersionAmbiguous + "), so the change cannot clear. Set pacto.trianalab.io/contract on the workload to name its contract"
}

// runningGate is the gate of a --running answer when no consumer is shown to
// break: unverified when the service is in no fleet domain or a running
// revision could not be read, the first-version gate when none runs,
// unverified when a judged answer is partial (a cluster Pacto could not read
// may run another revision), "" otherwise.
func (r *Result) runningGate() (string, string) {
	if r.notInFleet() {
		return GateUnverified, r.Service + " is in no fleet domain, so consumers that depend on it by registry ref cannot be found. " +
			"Judge its oci:// reference so Pacto knows its domain, or add the source that holds it"
	}
	if n := len(r.unreadBaselines); n > 0 {
		names := r.unreadBaselines[:min(n, maxUnreadInReason)]
		why := strings.Join(names, "; ")
		if n > maxUnreadInReason {
			why += " and " + strconv.Itoa(n-maxUnreadInReason) + " more"
		}
		return GateUnverified, "Pacto could not read every revision of " + r.Service + " that runs, so the change cannot clear: " + why +
			". Make each running revision readable from the registry and run the analysis again"
	}
	switch {
	case r.firstVersion:
		return r.firstVersionGate()
	case r.running && r.Completeness != fleet.CompletenessComplete:
		return GateUnverified, "the answer is partial, so a cluster Pacto could not read may run another revision of " + r.Service +
			" or a consumer that does not accept " + r.NewVersion + ". Fix the sources named in limitations and run the analysis again"
	}
	return "", ""
}

// notInFleet reports whether a --running answer's service is in no fleet
// domain (Options.NotInFleet).
func (r *Result) notInFleet() bool {
	return (r.running || r.firstVersion) && hasCode(r.Limitations, "SERVICE_NOT_IN_FLEET")
}

// firstVersionGate is the gate of a first version no consumer is shown to
// reject: clear only on a complete answer whose running consumers all accept
// the new version.
func (r *Result) firstVersionGate() (string, string) {
	lead := r.firstVersionLead()
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

// firstVersionLead opens a first-version reason: no revision of the service
// runs, none Pacto could read (the unread lines say which), or only the new
// revision runs, so no earlier one is left to judge against.
func (r *Result) firstVersionLead() string {
	switch {
	case len(r.unreadBaselines) > 0:
		return "no running revision of " + r.Service + " Pacto could read"
	case r.newRevisionRuns:
		return "only the new revision of " + r.Service + " runs on a known target, so no earlier revision is left to judge it against"
	}
	return "first version of " + r.Service + " on a known target"
}

// runningCode is the code of runningGate(), "" when it returns no gate.
func (r *Result) runningCode() string {
	switch g, _ := r.runningGate(); {
	case g == "":
		return ""
	case r.notInFleet():
		return GateCodeServiceNotInFleet
	case len(r.unreadBaselines) > 0:
		return GateCodeRunningUnread
	case !r.firstVersion:
		return GateCodeAnswerPartial
	case g == GateClear:
		return GateCodeFirstVersionAccepted
	}
	return GateCodeFirstVersionUnchecked
}
