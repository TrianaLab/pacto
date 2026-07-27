package dashboard

import "testing"

// FuzzComputeCompliance proves status aggregation is total and internally
// consistent for any contract status + condition mix: it never panics, always
// yields a known status, and every derived count/score obeys its invariants.
func FuzzComputeCompliance(f *testing.F) {
	f.Add(uint8(0), []byte{})
	f.Add(uint8(3), []byte{0, 1, 2, 3})
	f.Add(uint8(7), []byte{1, 1, 1})

	statuses := []ContractStatus{
		StatusCompliant, StatusWarning, StatusNonCompliant, StatusUnknown,
		StatusReference, StatusInvalid, StatusNotEvaluated,
	}
	condStatus := []string{"True", "False", "Unknown", "Other"}
	condSeverity := []string{"", "error", "warning"}

	validStatus := map[ComplianceStatus]bool{
		ComplianceOK: true, ComplianceWarning: true, ComplianceError: true,
		ComplianceReference: true, ComplianceUnknown: true,
	}

	f.Fuzz(func(t *testing.T, csSel uint8, data []byte) {
		cs := statuses[int(csSel)%len(statuses)]
		var conditions []Condition
		for _, b := range data {
			conditions = append(conditions, Condition{
				Type:     "Cond",
				Status:   condStatus[int(b)%len(condStatus)],
				Severity: condSeverity[int(b/uint8(len(condStatus)))%len(condSeverity)],
			})
		}

		info := ComputeCompliance(cs, conditions)
		if info == nil {
			t.Fatal("ComputeCompliance returned nil")
		}
		if !validStatus[info.Status] {
			t.Fatalf("unknown compliance status %q", info.Status)
		}
		if info.Score != nil && (*info.Score < 0 || *info.Score > 100) {
			t.Fatalf("score %d out of [0,100]", *info.Score)
		}
		if s := info.Summary; s != nil {
			if s.Total != len(conditions) {
				t.Fatalf("Total=%d, want %d", s.Total, len(conditions))
			}
			if s.Failed != s.Errors+s.Warnings {
				t.Fatalf("Failed=%d != Errors+Warnings=%d", s.Failed, s.Errors+s.Warnings)
			}
			if s.Passed+s.Errors+s.Warnings+s.Unknown > s.Total {
				t.Fatalf("counted %d > total %d", s.Passed+s.Errors+s.Warnings+s.Unknown, s.Total)
			}
			if s.Conclusive != s.Passed+s.Errors+s.Warnings {
				t.Fatalf("Conclusive=%d inconsistent", s.Conclusive)
			}
		}
	})
}
