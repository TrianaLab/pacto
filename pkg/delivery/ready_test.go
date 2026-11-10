package delivery

import "testing"

func TestReadyFixCoversEveryReason(t *testing.T) {
	for _, r := range []string{ReadyReasonUnguarded, ReadyReasonCandidateNotFound, ReadyReasonCandidateUnverified,
		ReadyReasonUpstreamUntrusted, ReadyReasonUpstreamUnreadable, ReadyReasonNoCandidateSource, ReadyReasonApplierUnreadable,
		ReadyReasonApplierUnlabelled, ReadyReasonApplierMultiSource, ReadyReasonApplierNotOCI, ReadyReasonApplierNotAutomated} {
		if ReadyFix(r) == "" {
			t.Errorf("%s has no fix", r)
		}
	}
	if ReadyFix("Something") != "" || ReadyFix(ConditionReady) != "" {
		t.Fatal("an unknown reason has no fix")
	}
}
