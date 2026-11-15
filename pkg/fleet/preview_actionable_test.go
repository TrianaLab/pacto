package fleet

import "testing"

func TestAttentionPreviewActionableExcludesInfo(t *testing.T) {
	l := &AttentionList{Total: 5, Severities: SeverityTally{Errors: 1, Warnings: 1, Infos: 2, Unknown: 1}}
	p := attentionPreviewFromList(l)
	if p.Total != 5 || p.Actionable != 3 {
		t.Fatalf("total %d actionable %d, want 5 and 3", p.Total, p.Actionable)
	}
}
