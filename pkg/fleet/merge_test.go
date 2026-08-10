package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

func TestMergeRevision_FillsEmptyAndUnionsSources(t *testing.T) {
	existing := &ContractRevision{Key: "svc@sha256:x", Service: "svc", Source: "oci", Sources: []string{"oci"}, Digest: "sha256:x"}
	add := &ContractRevision{
		Key: "svc@sha256:x", Service: "svc", Source: "local", Digest: "sha256:x",
		Lock: &lock.Lock{LockVersion: 1}, Readiness: &readiness.Result{Score: 90},
		Validation: []finding.Finding{{Code: "X"}}, Valid: true, validated: true,
		Tools: []ToolSummary{{Name: "t"}}, Skills: []string{"s"},
		// A doc list is only adoptable together with the filesystem that backs it:
		// paths whose bodies can never be read are not a projection worth filling.
		Docs:        []DocRef{{Path: "docs/d.md", digest: "abc"}},
		bundle:      &contract.Bundle{FS: fstest.MapFS{"docs/d.md": {Data: []byte("d")}}},
		ResolvedRef: "oci://x:1.0.0",
	}
	if lims := mergeRevision(existing, add); lims != nil {
		t.Errorf("no conflict expected, got %v", lims)
	}
	if existing.Lock == nil || existing.Readiness == nil || len(existing.Validation) == 0 ||
		!existing.Valid || !existing.validated || len(existing.Tools) == 0 ||
		len(existing.Skills) == 0 || len(existing.Docs) == 0 || existing.ResolvedRef == "" {
		t.Errorf("empty projections should be filled from the second source: %+v", existing)
	}
	if !containsStr(existing.Sources, "oci") || !containsStr(existing.Sources, "local") {
		t.Errorf("sources should union: %v", existing.Sources)
	}
}

func TestMergeRevision_ContentConflict(t *testing.T) {
	// Same content-addressed key but different derived content digests means two
	// sources pinned the same identity to different contract bodies.
	existing := &ContractRevision{Key: "svc@v1", Source: "a", Sources: []string{"a"}, content: "sha256:a"}
	add := &ContractRevision{Key: "svc@v1", Source: "b", content: "sha256:b"}
	lims := mergeRevision(existing, add)
	if len(lims) != 1 || lims[0].Code != LimitationRevisionConflict {
		t.Errorf("content disagreement should report REVISION_CONTENT_CONFLICT, got %v", lims)
	}
}

func TestMergeRef(t *testing.T) {
	cases := []struct {
		a, b, want string
		conflict   bool
	}{
		{"", "b", "b", false},
		{"a", "", "a", false},
		{"a", "a", "a", false},
		{"a", "b", "a", true},
	}
	for _, c := range cases {
		got, conflict := mergeRef(c.a, c.b)
		if got != c.want || conflict != c.conflict {
			t.Errorf("mergeRef(%q,%q) = (%q,%v), want (%q,%v)", c.a, c.b, got, conflict, c.want, c.conflict)
		}
	}
}

func TestTargetFresher(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	ev := func(x *time.Time) *TargetRecord { return &TargetRecord{EvidenceAt: x} }
	rec := func(x *time.Time) *TargetRecord { return &TargetRecord{ReconciledAt: x} }
	if !targetFresher(ev(&t1), ev(&t0)) {
		t.Error("later evidence is fresher")
	}
	if !targetFresher(ev(&t0), ev(nil)) {
		t.Error("having evidence beats none")
	}
	if targetFresher(ev(nil), ev(&t0)) {
		t.Error("no evidence is not fresher than some")
	}
	if !targetFresher(rec(&t1), rec(&t0)) {
		t.Error("later reconciliation is fresher when no evidence")
	}
	if !targetFresher(rec(&t0), &TargetRecord{}) {
		t.Error("having reconciliation beats nothing")
	}
}

func TestMergeTarget_FreshnessLabelsAndConflicts(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	existing := &TargetRecord{
		Key: "prod/k8s/web", Source: "inventory", Sources: []string{"inventory"},
		ResolvedRef: "oci://x:1", Labels: map[string]string{"env": "prod"},
		Compliance: StatusUnknown, EvidenceAt: &t0, Stale: true,
	}
	add := &TargetRecord{
		Key: "prod/k8s/web", Source: "k8s", ResolvedRef: "oci://x:1",
		Labels:     map[string]string{"env": "prod", "team": "payments"},
		Compliance: StatusNonCompliant, Findings: []finding.Finding{{Code: "DRIFT"}},
		EvidenceAt: &t1, Stale: false,
	}
	lims := mergeTarget(existing, add)
	if len(lims) != 0 {
		t.Errorf("agreeing refs/labels should not conflict, got %v", lims)
	}
	if existing.Compliance != StatusNonCompliant || len(existing.Findings) == 0 {
		t.Error("fresher observation should own compliance/findings")
	}
	if existing.Labels["team"] != "payments" {
		t.Error("labels should union")
	}
	if existing.Stale {
		t.Error("merged stale should be false when one source is fresh")
	}
	if !containsStr(existing.Sources, "inventory") || !containsStr(existing.Sources, "k8s") {
		t.Errorf("sources should union: %v", existing.Sources)
	}
}

func TestMergeTargetLabels_NoAddLabels(t *testing.T) {
	existing := &TargetRecord{Key: "k", Source: "a", Sources: []string{"a"}, Labels: map[string]string{"env": "prod"}}
	if lims := mergeTarget(existing, &TargetRecord{Key: "k", Source: "b"}); lims != nil {
		t.Errorf("merging a target with no labels should not conflict, got %v", lims)
	}
	if existing.Labels["env"] != "prod" {
		t.Error("existing labels must be preserved")
	}
}

func TestMergeTargetLabels_ExistingHasNoLabels(t *testing.T) {
	// existing has nil Labels; the second source's labels initialize the map.
	existing := &TargetRecord{Key: "k", Source: "a", Sources: []string{"a"}}
	if lims := mergeTarget(existing, &TargetRecord{Key: "k", Source: "b", Labels: map[string]string{"env": "prod"}}); lims != nil {
		t.Errorf("no conflict expected, got %v", lims)
	}
	if existing.Labels["env"] != "prod" {
		t.Errorf("labels should be adopted when existing had none: %v", existing.Labels)
	}
}

func TestMergeTarget_RefAndLabelConflicts(t *testing.T) {
	existing := &TargetRecord{Key: "k", Source: "a", Sources: []string{"a"}, ResolvedRef: "oci://x:1", Labels: map[string]string{"env": "prod"}}
	add := &TargetRecord{Key: "k", Source: "b", ResolvedRef: "oci://x:2", Labels: map[string]string{"env": "staging"}}
	lims := mergeTarget(existing, add)
	var refConflict, fieldConflict bool
	for _, l := range lims {
		if l.Code == LimitationTargetRefConflict {
			refConflict = true
		}
		if l.Code == LimitationTargetFieldConflict {
			fieldConflict = true
		}
	}
	if !refConflict || !fieldConflict {
		t.Errorf("disagreeing ref and label should both conflict, got %v", lims)
	}
	if existing.ResolvedRef != "oci://x:1" {
		t.Error("conflicting ref keeps the existing value")
	}
}

// TestBuild_ComplementaryMerge proves two sources contributing to the same
// revision and target combine into one richer record.
func TestBuild_ComplementaryMerge(t *testing.T) {
	// OCI source: the contract bundle (revision), no runtime target.
	ociRev := RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}
	oci := NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev}})
	// A rawless revision of the same key from another source carrying only a lock.
	bare := &contract.Bundle{Contract: validLeafBundle(t).Contract, FS: fstest.MapFS{}}
	addRev := RawRevision{Bundle: bare, Digest: "sha256:leaf", Lock: &lock.Lock{LockVersion: 1}}
	locks := NewMemorySource("locks", "local", &Collection{Revisions: []RawRevision{addRev}})
	// k8s source: a runtime target for the same service.
	k8s := NewMemorySource("k8s", "kubernetes", &Collection{Targets: []RawTarget{{
		Scope: "prod", Kind: "k8s", Name: "leaf", Service: "leaf-svc",
		Digest: "sha256:leaf", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow()),
	}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, oci, locks, k8s)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 1 {
		t.Fatalf("same revision should merge to 1, got %d", len(snap.Revisions))
	}
	for _, r := range snap.Revisions {
		if r.Lock == nil {
			t.Error("merge should fill the lock from the second source")
		}
		if !containsStr(r.Sources, "oci") || !containsStr(r.Sources, "locks") {
			t.Errorf("revision should retain both contributing sources: %v", r.Sources)
		}
	}
	// The service records all three contributing sources.
	if s := snap.Service("leaf-svc"); len(s.Sources) < 3 {
		t.Errorf("service should record oci+locks+k8s sources, got %v", s.Sources)
	}
}
