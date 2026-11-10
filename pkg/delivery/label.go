package delivery

import "slices"

// EvidenceGates are the gates that gather evidence: they pass once they ran,
// whatever they found. What they found that holds the change is in the
// plan's floor and question. The dashboard's EVIDENCE_GATES
// (pkg/dashboard/frontend/src/lib/delivery.ts) is the same set, and a test
// keeps them equal.
var EvidenceGates = []string{GateDiff, GateImpact, GateEnvironment}

// VerdictLabel is the word a person reads for v, as the dashboard's
// verdictBadge writes it: N/A for a check that does not apply to the version,
// Checked for a Pass on an evidence gate (it only says the gate ran), else
// the result. Verdict.Result itself never changes: records are signed.
func VerdictLabel(v Verdict) string {
	switch {
	case v.Citation["applicable"] == "false":
		return "N/A"
	case v.Result == Pass && slices.Contains(EvidenceGates, v.Gate):
		return "Checked"
	}
	return string(v.Result)
}
