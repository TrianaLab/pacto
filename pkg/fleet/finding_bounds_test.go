package fleet

import (
	"runtime"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/finding"
)

// bytesAllocated reports the heap bytes a call allocates. It is used to prove that
// a bounded product answer does BOUNDED WORK: its allocation must not grow with the
// (arbitrary, untrusted) input width, only with the emitted prefix (requirement,
// item 8).
func bytesAllocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestProductFinding_BoundedEvidenceConversion proves productFinding converts only
// the emitted evidence-ref prefix, never the whole (possibly extension-supplied,
// unbounded) EvidenceRefs slice, while still reporting the truthful total.
func TestProductFinding_BoundedEvidenceConversion(t *testing.T) {
	const n = 500_000 // orders of magnitude above MaxEvidenceRefsPreview
	refs := make([]finding.EvidenceRef, n)
	for i := range refs {
		refs[i] = finding.EvidenceRef{Source: "s", ObservedAt: "t"}
	}
	f := finding.Finding{Severity: finding.SeverityWarning, EvidenceRefs: refs}

	var pf ProductFinding
	allocated := bytesAllocated(func() { pf = productFinding(f) })

	if pf.EvidenceRefs.Total != n {
		t.Errorf("Total = %d, want %d (the truthful full count)", pf.EvidenceRefs.Total, n)
	}
	if pf.EvidenceRefs.Count != MaxEvidenceRefsPreview {
		t.Errorf("Count = %d, want %d (bounded prefix)", pf.EvidenceRefs.Count, MaxEvidenceRefsPreview)
	}
	if !pf.EvidenceRefs.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(pf.EvidenceRefs.Items) != MaxEvidenceRefsPreview {
		t.Errorf("emitted %d items, want %d", len(pf.EvidenceRefs.Items), MaxEvidenceRefsPreview)
	}
	// Converting all n refs would allocate n*sizeof(ProductEvidenceRef) (~16 MB);
	// the bounded conversion touches only the emitted prefix, so it must stay far
	// below that regardless of input width.
	if allocated > 1<<20 {
		t.Errorf("productFinding allocated %d bytes for a %d-ref finding; want bounded (prefix only)", allocated, n)
	}
}

// TestFindingsPreview_BoundedConversion proves findingsPreview converts only the
// emitted findings prefix, not every finding in a pathologically large slice.
func TestFindingsPreview_BoundedConversion(t *testing.T) {
	const n = 200_000 // orders of magnitude above MaxDetailPreview
	fs := make([]finding.Finding, n)
	for i := range fs {
		fs[i] = finding.Finding{Severity: finding.SeverityError, Message: "m"}
	}

	var fp FindingsPreview
	allocated := bytesAllocated(func() { fp = findingsPreview(fs) })

	if fp.Total != n {
		t.Errorf("Total = %d, want %d", fp.Total, n)
	}
	if fp.Count != MaxDetailPreview {
		t.Errorf("Count = %d, want %d", fp.Count, MaxDetailPreview)
	}
	if !fp.Truncated {
		t.Error("Truncated = false, want true")
	}
	// Converting all n findings would allocate n*sizeof(ProductFinding); the bounded
	// conversion must allocate only the emitted prefix.
	if allocated > 1<<20 {
		t.Errorf("findingsPreview allocated %d bytes for %d findings; want bounded (prefix only)", allocated, n)
	}
}

// TestAttributedTargetFindings_BoundedConversion proves the service-detail
// attributed-findings aggregation converts only the emitted prefix across all
// targets, while reporting the truthful total summed across them.
func TestAttributedTargetFindings_BoundedConversion(t *testing.T) {
	const perTarget = 100_000
	const targets = 4
	ts := make([]*TargetRecord, targets)
	for i := range ts {
		fs := make([]finding.Finding, perTarget)
		for j := range fs {
			fs[j] = finding.Finding{Severity: finding.SeverityError, Message: "m"}
		}
		ts[i] = &TargetRecord{Key: TargetKey("k"), Name: "n", Findings: fs}
	}

	var afp AttributedFindingsPreview
	allocated := bytesAllocated(func() { afp = attributedTargetFindingsPreview(ts) })

	if afp.Total != perTarget*targets {
		t.Errorf("Total = %d, want %d (summed across targets)", afp.Total, perTarget*targets)
	}
	if afp.Count != MaxDetailPreview {
		t.Errorf("Count = %d, want %d", afp.Count, MaxDetailPreview)
	}
	if !afp.Truncated {
		t.Error("Truncated = false, want true")
	}
	if allocated > 1<<20 {
		t.Errorf("attributedTargetFindingsPreview allocated %d bytes; want bounded (prefix only)", allocated)
	}
}
