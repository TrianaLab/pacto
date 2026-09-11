package fleet

import (
	"context"
	"strings"
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

// Two sources may pin one immutable digest and still ship different pacto.lock
// content. Taking whichever arrived first would let source completion order decide
// which bundle a declared dependency resolves to, so the lock is dropped and the
// drop is sticky against a third, agreeing contributor.
func TestMergeRevision_LockConflictDropsAndSticks(t *testing.T) {
	existing := &ContractRevision{Key: "svc@sha256:x", Source: "a", Sources: []string{"a"},
		Lock: &lock.Lock{LockVersion: 2, Root: lock.RootInfo{Name: "svc", Version: "1.0.0"}}}
	add := &ContractRevision{Key: "svc@sha256:x", Source: "b",
		Lock: &lock.Lock{LockVersion: 2, Root: lock.RootInfo{Name: "svc", Version: "2.0.0"}}}
	lims := mergeRevision(existing, add)
	if len(lims) != 1 || lims[0].Code != LimitationRevisionLockConflict {
		t.Fatalf("lock disagreement should report REVISION_LOCK_CONFLICT, got %v", lims)
	}
	if existing.Lock != nil {
		t.Errorf("a contested lock must be dropped, not resolved by arrival order: %+v", existing.Lock)
	}
	third := &ContractRevision{Key: "svc@sha256:x", Source: "c", Lock: add.Lock}
	if lims := mergeRevision(existing, third); lims != nil {
		t.Errorf("the conflict is already reported; a third contributor must not re-report it: %v", lims)
	}
	if existing.Lock != nil {
		t.Errorf("a third contributor agreeing with one side must not reinstate contested pins: %+v", existing.Lock)
	}
}

// Two contributions of the same lock are agreement, not a conflict. Two locks
// that record the same resolutions but name different producing CLI versions are
// agreement too: pacto.Version is provenance, and calling it a disagreement would
// discard every pin the revision has and degrade all of its declared dependencies
// and references to unresolved. A dependency pinned to a different digest is the
// real thing.
func TestMergeRevision_LockAgreementIgnoresProvenance(t *testing.T) {
	mk := func(cliVersion, depDigest string) *lock.Lock {
		return &lock.Lock{
			LockVersion:  lock.CurrentLockVersion,
			Pacto:        lock.PactoInfo{Version: cliVersion},
			Root:         lock.RootInfo{Name: "svc", Version: "1.0.0"},
			Dependencies: []lock.Entry{{Name: "dep", Source: "oci", Digest: depDigest}},
		}
	}
	rev := func(src string, l *lock.Lock) *ContractRevision {
		return &ContractRevision{Key: "svc@sha256:x", Source: src, Sources: []string{src}, Lock: l}
	}

	agree := rev("a", mk("3.2.0", "sha256:dep"))
	if lims := mergeRevision(agree, rev("b", mk("3.2.1", "sha256:dep"))); lims != nil {
		t.Errorf("only the producing CLI version differs; that is not a disagreement: %v", lims)
	}
	if agree.Lock == nil {
		t.Error("agreed pins must survive a Pacto version bump between contributors")
	}

	conflict := rev("a", mk("3.2.0", "sha256:dep"))
	lims := mergeRevision(conflict, rev("b", mk("3.2.0", "sha256:other")))
	if len(lims) != 1 || lims[0].Code != LimitationRevisionLockConflict {
		t.Fatalf("a disagreeing dependency pin is a real conflict, got %v", lims)
	}
	if conflict.Lock != nil {
		t.Errorf("a contested lock must be dropped: %+v", conflict.Lock)
	}
}

// A discarded lock must not read as "there was never a lock". lockReference
// returns nil either way, so without a carried reason the reference detail tells
// the operator that pacto.lock recorded no resolution — and they re-run
// `pacto lock`, regenerate identical pins and see nothing change.
func TestRefResolution_DiscardedLockNamesTheConflict(t *testing.T) {
	c := &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "checkout", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Configurations: []contract.Configuration{{Name: "settlement", Ref: "oci://ghcr.io/acme/shared:1.0.0", Required: true}},
	}
	src := func(id, digest string) Source {
		return NewMemorySource(id, "oci", &Collection{Revisions: []RawRevision{{
			Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}},
			Domain: "d", Digest: "sha256:checkout",
			Lock: &lock.Lock{LockVersion: lock.CurrentLockVersion,
				Root: lock.RootInfo{Name: "checkout", Version: "1.0.0"},
				References: []lock.Reference{{
					Kind: contract.ReferenceKindConfig, Name: "settlement", Source: "oci",
					Ref: "oci://ghcr.io/acme/shared:1.0.0", Version: "1.0.0", Digest: digest,
				}}},
		}}})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		src("a", refDigest("shared-a")), src("b", refDigest("shared-b")))
	if err != nil {
		t.Fatal(err)
	}
	var reason string
	for _, rel := range snap.Relationships {
		if rel.Type == RelationshipConfigRef && rel.To == "settlement" {
			reason = rel.Reason
		}
	}
	if !strings.Contains(reason, "disagreeing pacto.lock") {
		t.Errorf("the reference must say its pins were discarded over a conflict, got %q", reason)
	}
}

// A dependency edge resolves by NAME, so a contested lock leaves it resolved but
// silently un-pinned: no LockedDigest, no ResolvedRevision and — before this — no
// reason either, which is indistinguishable from a revision that ships no
// pacto.lock at all. The winner of a lock disagreement is "neither", and it is
// "neither" in both contribution orders; the edge has to say so.
func TestDepResolution_DiscardedLockNamesTheConflict(t *testing.T) {
	checkout := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "checkout", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{{Name: "ledger", Ref: "oci://x/ledger", Required: true, Compatibility: "^1.0.0"}},
	}
	ledger := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "ledger", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
	}
	ledgerSrc := NewMemorySource("ledger", "oci", &Collection{Revisions: []RawRevision{{
		Bundle: &contract.Bundle{Contract: ledger, FS: fstest.MapFS{}},
		Domain: "d", Digest: "sha256:ledger",
	}}})
	src := func(id, depDigest string) Source {
		return NewMemorySource(id, "oci", &Collection{Revisions: []RawRevision{{
			Bundle: &contract.Bundle{Contract: checkout, FS: fstest.MapFS{}},
			Domain: "d", Digest: "sha256:checkout",
			Lock: &lock.Lock{LockVersion: lock.CurrentLockVersion,
				Root:         lock.RootInfo{Name: "checkout", Version: "1.0.0"},
				Dependencies: []lock.Entry{{Name: "ledger", Source: "oci", Ref: "oci://x/ledger", Version: "1.0.0", Digest: depDigest}},
			},
		}}})
	}
	// "sha256:ledger" is the digest of a revision the snapshot actually holds, so a
	// first-lock-wins tiebreak would produce a confident pin at ledger@sha256:ledger
	// in one contribution order and a dangling one in the other.
	a, b := src("a", "sha256:ledger"), src("b", "sha256:other-ledger")
	for _, order := range [][]Source{{ledgerSrc, a, b}, {ledgerSrc, b, a}} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, order...)
		if err != nil {
			t.Fatal(err)
		}
		var got *Relationship
		for i := range snap.Relationships {
			if rel := &snap.Relationships[i]; rel.Type == RelationshipDependency && rel.To == "ledger" {
				got = rel
			}
		}
		if got == nil {
			t.Fatalf("%s first: the dependency edge is missing", order[1].ID())
		}
		if !got.Resolved || got.ToService != NewServiceKeyDomain("d", "ledger") {
			t.Errorf("%s first: a contested lock must not unresolve a name-resolved dependency: %+v", order[1].ID(), got)
		}
		if got.LockedDigest != "" || got.LockedVersion != "" || got.ResolvedRevision != "" {
			t.Errorf("%s first: neither side of a contested lock may pin the edge: %+v", order[1].ID(), got)
		}
		if !strings.Contains(got.Reason, "disagreeing pacto.lock") {
			t.Errorf("%s first: the edge must say its pins were discarded over a conflict, got %q", order[1].ID(), got.Reason)
		}
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

// Staleness is a verdict ABOUT an evidence timestamp, so it belongs to whichever
// contribution owns the evidence — not to an AND across every contributor. Stale is
// only ever SET from a non-nil EvidenceAt (see targetFrom), so a contributor carrying
// no evidence time at all has Stale==false by default, and ANDing let it clear another
// source's staleness while that source's 30-day-old EvidenceAt stayed on the record:
// an all-clear asserted by a source that never observed freshness at all. Absence of
// evidence is not evidence of freshness.
func TestMergeTarget_ContributorWithNoEvidenceDoesNotClearStale(t *testing.T) {
	old := time.Unix(1000, 0)
	rec := time.Unix(9000, 0)
	check := func(name string, existing, add *TargetRecord) {
		t.Helper()
		mergeTarget(existing, add)
		if !existing.Stale {
			t.Errorf("%s: stale cleared by a contributor with no evidence timestamp (EvidenceAt is still %v)", name, existing.EvidenceAt)
		}
		if existing.EvidenceAt == nil || !existing.EvidenceAt.Equal(old) {
			t.Errorf("%s: EvidenceAt = %v, want the only observed evidence time %v", name, existing.EvidenceAt, old)
		}
	}
	// Both contribution orders: the merge is order-independent.
	check("stale first",
		&TargetRecord{Key: "prod/k8s/web", Source: "evidence", Sources: []string{"evidence"}, EvidenceAt: &old, Stale: true},
		&TargetRecord{Key: "prod/k8s/web", Source: "k8s", ReconciledAt: &rec})
	check("stale second",
		&TargetRecord{Key: "prod/k8s/web", Source: "k8s", Sources: []string{"k8s"}, ReconciledAt: &rec},
		&TargetRecord{Key: "prod/k8s/web", Source: "evidence", EvidenceAt: &old, Stale: true})
}
