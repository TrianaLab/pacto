package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const backupRef = "x.acme.io/backup"

var extKey = NewServiceKeyDomain(acme, "payments")

// extRevision is a revision of payments that declares the backup extension and
// a health capability.
func extRevision(version string) RawRevision {
	c := svc24("payments", version, "")
	c.Capabilities = []contract.Capability{{Type: contract.CapabilityHealth}, {Type: contract.CapabilityExtension, Ref: backupRef}}
	return ociRev(acme, c, nil)
}

func extRev(version string) RevisionKey {
	return NewRevisionKey(extKey, testDigest(acme, "payments", version))
}

// extReport is a fresh, signed report by acme-backup that the backup
// extension of payments version is present.
func extReport(version string, mods ...func(*RawExtension)) RawExtension {
	at, yes := fixedNow().Add(-time.Hour), true
	r := RawExtension{Service: "payments", Domain: acme, Digest: testDigest(acme, "payments", version), Ref: backupRef,
		Present: &yes, Outcome: "Observed", Producer: "acme-backup", ProducerKeyID: "acme-backup-2026",
		ObservedAt: at, AcceptedAt: &at, Verified: true}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func reportedAbsent(r *RawExtension) {
	no := false
	r.Present = &no
}

func producedBy(p string) func(*RawExtension) { return func(r *RawExtension) { r.Producer = p } }

func reportedFailed(r *RawExtension) { r.Outcome, r.Present = "Failed", nil }

func reportedAt(d time.Duration) func(*RawExtension) {
	return func(r *RawExtension) {
		at := fixedNow().Add(-d)
		r.ObservedAt, r.AcceptedAt = at, &at
	}
}

// extTarget runs version in namespace ns with compliance.
func extTarget(ns, version, compliance string) RawTarget {
	return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: "payments", Service: "payments", Domain: acme,
		Digest: testDigest(acme, "payments", version), Compliance: compliance}
}

// extSnap builds base (an oci source) with each report list as its own
// evidence source.
func extSnap(t *testing.T, window time.Duration, base *Collection, reports ...[]RawExtension) *FleetSnapshot {
	t.Helper()
	sources := []Source{NewMemorySource("oci", "oci", base)}
	for i, recs := range reports {
		sources = append(sources, NewMemorySource(fmt.Sprintf("evidence-%d", i), "evidence-http", &Collection{Extensions: recs}))
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: window}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func extFleet(versions ...string) *Collection {
	col := &Collection{}
	for _, v := range versions {
		col.Revisions = append(col.Revisions, extRevision(v))
	}
	return col
}

func checkOn(snap *FleetSnapshot, version string) *ExtensionCheck {
	r := snap.Revisions[extRev(version)]
	if r == nil || len(r.Extensions) == 0 {
		return nil
	}
	return &r.Extensions[0]
}

func TestExtensionCheckStateTable(t *testing.T) {
	details := json.RawMessage(`{"lastBackup":"2026-07-29T03:00:00Z"}`)
	withDetails := func(r *RawExtension) { r.Details = details }
	cases := []struct {
		name     string
		versions []string
		window   time.Duration
		reports  []RawExtension
		on       string
		want     *ExtensionCheck
	}{
		{"covered", []string{"1.3.0"}, 0, []RawExtension{extReport("1.3.0", withDetails)}, "1.3.0",
			&ExtensionCheck{State: ExtensionCovered, Producer: "acme-backup", Details: details}},
		{"failing", []string{"1.3.0"}, 0, []RawExtension{extReport("1.3.0", reportedAbsent)}, "1.3.0",
			&ExtensionCheck{State: ExtensionFailing, Producer: "acme-backup", Producers: []string{"acme-backup"}, Reason: "reported absent by acme-backup"}},
		{"failing-beats-covered", []string{"1.3.0"}, 0, []RawExtension{
			extReport("1.3.0", producedBy("c-tool")), extReport("1.3.0", reportedAbsent, producedBy("b-tool")), extReport("1.3.0", reportedAbsent, producedBy("a-tool")),
		}, "1.3.0", &ExtensionCheck{State: ExtensionFailing, Producer: "a-tool", Producers: []string{"a-tool", "b-tool"}, Reason: "reported absent by a-tool, b-tool"}},
		{"newest-report-per-producer", []string{"1.3.0"}, 0, []RawExtension{extReport("1.3.0", reportedAbsent, reportedAt(2*time.Hour)), extReport("1.3.0")}, "1.3.0",
			&ExtensionCheck{State: ExtensionCovered, Producer: "acme-backup"}},
		{"non-observed", []string{"1.3.0"}, 0, []RawExtension{extReport("1.3.0", reportedFailed), extReport("1.3.0", producedBy("b-tool"))}, "1.3.0",
			&ExtensionCheck{State: ExtensionUnknown, Producer: "acme-backup", Reason: "acme-backup reported failed"}},
		{"stale", []string{"1.3.0"}, 2 * time.Hour, []RawExtension{extReport("1.3.0", reportedAt(3*time.Hour))}, "1.3.0",
			&ExtensionCheck{State: ExtensionUnknown, Producer: "acme-backup", Stale: true, Reason: "the last report is older than the freshness window"}},
		{"other-revision", []string{"1.2.0", "1.3.0"}, 0, []RawExtension{extReport("1.2.0")}, "1.3.0",
			&ExtensionCheck{State: ExtensionUnknown, Producer: "acme-backup", Revision: &EntityRef{Label: "payments 1.2.0"}, Reason: "last report is for 1.2.0; 1.3.0 has no report yet"}},
		{"never-reported", []string{"1.3.0"}, 0, nil, "1.3.0", nil},
		{"unsigned-dropped", []string{"1.3.0"}, 0, []RawExtension{extReport("1.3.0", func(r *RawExtension) { r.Verified = false })}, "1.3.0", nil},
		{"unmatched-dropped", []string{"1.3.0"}, 0, []RawExtension{extReport("1.3.0", func(r *RawExtension) { r.Ref = "x.acme.io/restore" })}, "1.3.0", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkOn(extSnap(t, tc.window, extFleet(tc.versions...), tc.reports), tc.on)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("check = %+v, want none", got)
				}
				return
			}
			if got == nil {
				t.Fatal("no check")
			}
			if !checkMatches(got, tc.want) {
				t.Errorf("check = %+v (revision %+v), want %+v", got, got.Revision, tc.want)
			}
		})
	}
}

// checkMatches reports whether got has want's state, producers, reason,
// details and revision label, and the provenance every check carries.
func checkMatches(got, want *ExtensionCheck) bool {
	sameRevision := (got.Revision == nil) == (want.Revision == nil) && (got.Revision == nil || got.Revision.Label == want.Revision.Label)
	return sameRevision && got.Ref == backupRef && got.State == want.State && got.Producer == want.Producer && slices.Equal(got.Producers, want.Producers) &&
		got.Stale == want.Stale && got.Reason == want.Reason && string(got.Details) == string(want.Details) &&
		got.Provenance == ProvenanceObserved && got.AsOf != nil && got.Source != "" && got.SourceKind == "evidence-http" && got.ProducerKeyID == "acme-backup-2026"
}

func TestExtensionCheckTwoRevisionsSameProducer(t *testing.T) {
	for _, order := range [][2]time.Duration{{2 * time.Hour, time.Hour}, {time.Hour, 2 * time.Hour}} {
		prod := extReport("1.2.0", reportedAt(order[0]))
		staging := extReport("1.3.0", reportedAbsent, reportedAt(order[1]))
		snap := extSnap(t, 0, extFleet("1.2.0", "1.3.0"), []RawExtension{prod, staging})
		if c := checkOn(snap, "1.2.0"); c == nil || c.State != ExtensionCovered {
			t.Errorf("order %v: 1.2.0 = %+v", order, c)
		}
		if c := checkOn(snap, "1.3.0"); c == nil || c.State != ExtensionFailing {
			t.Errorf("order %v: 1.3.0 = %+v", order, c)
		}
	}
}

func TestValidExtensionTable(t *testing.T) {
	now := fixedNow()
	cases := []struct {
		name string
		mod  func(*RawExtension)
		ok   bool
	}{
		{"valid", func(*RawExtension) {}, true},
		{"valid details", func(r *RawExtension) { r.Details = json.RawMessage(`{"a":1}`) }, true},
		{"valid failed", reportedFailed, true},
		{"unsigned", func(r *RawExtension) { r.Verified = false }, false},
		{"no service", func(r *RawExtension) { r.Service = "" }, false},
		{"bad digest", func(r *RawExtension) { r.Digest = "sha256:abc" }, false},
		{"un-namespaced ref", func(r *RawExtension) { r.Ref = "backup" }, false},
		{"no observedAt", func(r *RawExtension) { r.ObservedAt = time.Time{} }, false},
		{"unknown outcome", func(r *RawExtension) { r.Outcome = "Maybe" }, false},
		{"no producer", func(r *RawExtension) { r.Producer = "" }, false},
		{"control character producer", func(r *RawExtension) { r.Producer = "acme\x1b[31m" }, false},
		{"ahead of accept", func(r *RawExtension) { r.ObservedAt = r.AcceptedAt.Add(MaxProvisioningClockSkew + time.Second) }, false},
		{"observed without present", func(r *RawExtension) { r.Present = nil }, false},
		{"failed with present", func(r *RawExtension) { r.Outcome = "Failed" }, false},
		{"details on failed", func(r *RawExtension) { reportedFailed(r); r.Details = json.RawMessage(`{"a":1}`) }, false},
		{"details not an object", func(r *RawExtension) { r.Details = json.RawMessage(`[1]`) }, false},
		{"details too large", func(r *RawExtension) {
			r.Details = json.RawMessage(`{"a":"` + strings.Repeat("x", 2048) + `"}`)
		}, false},
	}
	for _, tc := range cases {
		if got := validExtension(extReport("1.3.0", tc.mod), now); got != tc.ok {
			t.Errorf("%s: valid = %t, want %t", tc.name, got, tc.ok)
		}
	}
}

func TestExtensionInvalidRecordsCounted(t *testing.T) {
	snap := extSnap(t, 0, extFleet("1.3.0"), []RawExtension{extReport("1.3.0", func(r *RawExtension) { r.Verified = false })})
	if n := countLimitations(snap.Limitations, LimitationSourceRecordInvalid); n != 1 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

func TestExtensionUnmatchedLimitation(t *testing.T) {
	snap := extSnap(t, 0, extFleet("1.3.0"),
		[]RawExtension{extReport("1.3.0", func(r *RawExtension) { r.Ref = "x.acme.io/restore" }), extReport("9.9.9")},
		[]RawExtension{extReport("1.3.0")})
	var got []Limitation
	for _, l := range snap.Limitations {
		if l.Code == LimitationExtensionUnmatched {
			got = append(got, l)
		}
	}
	if len(got) != 1 || got[0].Source != "evidence-0" || !strings.HasPrefix(got[0].Message, "2 extension reports") || strings.Contains(got[0].Message, "payments") {
		t.Errorf("limitations = %+v", got)
	}
	if c := checkOn(snap, "1.3.0"); c == nil || c.State != ExtensionCovered {
		t.Errorf("the matched report was lost: %+v", c)
	}
}

func TestBuildWithoutExtensionRecordsIsUnchanged(t *testing.T) {
	base := func() *Collection {
		col := extFleet("1.3.0")
		col.Targets = []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)}
		return col
	}
	without := extSnap(t, 0, base())
	empty := extSnap(t, 0, base(), []RawExtension{})
	data, err := json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"extensions"`) {
		t.Error("the snapshot carries an extensions key with no extension record")
	}
	d, err := NewQuery(without).EntityDetail(KindRevision, string(extRev("1.3.0")))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(d); strings.Contains(string(data), `"check"`) {
		t.Error("the revision entity carries a check key with no extension record")
	}
	if without.Services[extKey].Status != StatusCompliant || empty.Services[extKey].Status != StatusCompliant {
		t.Errorf("status = %s / %s", without.Services[extKey].Status, empty.Services[extKey].Status)
	}
	if !slices.Equal(limitationCodes(without), limitationCodes(empty)) {
		t.Errorf("limitations differ: %v vs %v", limitationCodes(without), limitationCodes(empty))
	}
}

func limitationCodes(snap *FleetSnapshot) []string {
	var out []string
	for _, l := range snap.Limitations {
		if l.Source != "evidence-0" {
			out = append(out, l.Code)
		}
	}
	return out
}

func TestCurrentRevisionShared(t *testing.T) {
	col := extFleet("1.2.0", "1.3.0", "1.4.0")
	col.Targets = []RawTarget{extTarget("prod", "1.2.0", StatusCompliant), extTarget("staging", "1.3.0", StatusCompliant)}
	snap := extSnap(t, 0, col)
	q := NewQuery(snap)
	s := snap.Services[extKey]
	if got := currentRevision(snap, s); got != q.currentRevision(s) || got.Version != "1.3.0" {
		t.Fatalf("current = %+v", got)
	}
	if got := statusRevisions(snap, s); len(got) != 2 {
		t.Errorf("status revisions = %d", len(got))
	}
	// A quarantined target still names its revision as in use.
	for _, tk := range s.Targets {
		snap.Targets[tk].Quarantined = true
	}
	if got := currentRevision(snap, s); got != q.currentRevision(s) || got.Version != "1.3.0" {
		t.Errorf("quarantined current = %+v", got)
	}
	// A revision or target the snapshot no longer holds is skipped.
	cp := *s
	cp.Revisions = append(slices.Clone(s.Revisions), "ghost@sha256:00")
	cp.Targets = append(slices.Clone(s.Targets), "ghost")
	if got := currentRevision(snap, &cp); got != q.currentRevision(&cp) || got.Version != "1.3.0" {
		t.Errorf("current with ghosts = %+v", got)
	}
	// No targets: the newest known; no revisions: none.
	cp.Targets = nil
	if got := statusRevisions(snap, &cp); len(got) != 1 || got[0].Version != "1.4.0" {
		t.Errorf("status revisions with no target = %+v", got)
	}
	cp.Revisions = nil
	if currentRevision(snap, &cp) != nil || q.currentRevision(&cp) != nil || statusRevisions(snap, &cp) != nil {
		t.Error("a service with no revision has a current one")
	}
}

func TestServiceStatusFoldsCurrentExtensionChecks(t *testing.T) {
	cases := []struct {
		name    string
		targets []RawTarget
		reports []RawExtension
		want    string
	}{
		{"failing on current", []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)}, []RawExtension{extReport("1.3.0", reportedAbsent)}, StatusNonCompliant},
		{"unknown on current", []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)}, []RawExtension{extReport("1.3.0", reportedFailed)}, StatusUnknown},
		{"unknown leaves non-compliant", []RawTarget{extTarget("prod", "1.3.0", StatusNonCompliant)}, []RawExtension{extReport("1.3.0", reportedFailed)}, StatusNonCompliant},
		{"covered changes nothing", []RawTarget{extTarget("prod", "1.3.0", StatusWarning)}, []RawExtension{extReport("1.3.0")}, StatusWarning},
		{"failing on a revision no target runs", []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)},
			[]RawExtension{extReport("1.2.0", reportedAbsent), extReport("1.3.0")}, StatusCompliant},
		{"failing on an older in-use revision", []RawTarget{extTarget("prod", "1.2.0", StatusCompliant), extTarget("staging", "1.3.0", StatusCompliant)},
			[]RawExtension{extReport("1.2.0", reportedAbsent), extReport("1.3.0")}, StatusNonCompliant},
		{"no targets, failing on current", nil, []RawExtension{extReport("1.3.0", reportedAbsent)}, StatusNonCompliant},
		{"no targets, no report", nil, nil, StatusNotEvaluated},
	}
	for _, tc := range cases {
		col := extFleet("1.2.0", "1.3.0")
		col.Targets = tc.targets
		snap := extSnap(t, 0, col, tc.reports)
		if got := snap.Services[extKey].Status; got != tc.want {
			t.Errorf("%s: status = %s, want %s", tc.name, got, tc.want)
		}
	}
	// Invalid still wins over a failing check.
	snap := extSnap(t, 0, extFleet("1.3.0"), []RawExtension{extReport("1.3.0", reportedAbsent)})
	rev := snap.Revisions[extRev("1.3.0")]
	rev.validated, rev.Valid = true, false
	if got := serviceStatus(snap, snap.Services[extKey]); got != StatusInvalid {
		t.Errorf("invalid status = %s", got)
	}
}

func TestWorstStatus(t *testing.T) {
	for _, tc := range [][3]string{{"", "", ""}, {"", StatusUnknown, StatusUnknown}, {StatusCompliant, "", StatusCompliant},
		{StatusCompliant, StatusNonCompliant, StatusNonCompliant}, {StatusInvalid, StatusUnknown, StatusInvalid}} {
		if got := worstStatus(tc[0], tc[1]); got != tc[2] {
			t.Errorf("worstStatus(%q, %q) = %q", tc[0], tc[1], got)
		}
	}
}

// clusterFleet runs payments 1.3.0 in cluster-a and returns its snapshot with
// reports, each list an evidence source in cluster.
func clusterFleet(t *testing.T, reports map[string][]RawExtension) *FleetSnapshot {
	t.Helper()
	sources := []Source{
		NewMemorySource("oci", "oci", extFleet("1.2.0", "1.3.0")),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: &Cluster{ID: "cluster-a"},
			Targets: []RawTarget{extTarget("prod", "1.3.0", StatusCompliant), {Scope: "secret", Kind: SourceKindKubernetes, Name: "other", Service: "other", Domain: acme}}}),
	}
	for _, cluster := range slices.Sorted(maps.Keys(reports)) {
		recs := slices.Clone(reports[cluster])
		for i := range recs {
			recs[i].Cluster = &Cluster{ID: cluster}
		}
		sources = append(sources, NewMemorySource("evidence-"+cluster, "evidence-http", &Collection{Extensions: recs}))
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestRestrictKeepsExtensionChecksOfVisibleServices(t *testing.T) {
	snap := clusterFleet(t, map[string][]RawExtension{"cluster-a": {extReport("1.3.0", reportedAbsent)}})
	if snap.Services[extKey].Status != StatusNonCompliant {
		t.Fatalf("status = %s", snap.Services[extKey].Status)
	}
	r := NewQuery(snap).Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), nil)
	if c := checkOn(r.snap, "1.3.0"); c == nil || c.State != ExtensionFailing {
		t.Errorf("check = %+v", c)
	}
	if got := r.snap.Services[extKey].Status; got != StatusNonCompliant {
		t.Errorf("restricted status = %s", got)
	}
	// A report about a revision the view dropped names no revision.
	snap = extSnap(t, 0, extFleet("1.2.0", "1.3.0"), []RawExtension{extReport("1.2.0")})
	out := *snap
	out.Revisions = maps.Clone(snap.Revisions)
	delete(out.Revisions, extRev("1.2.0"))
	deriveExtensionChecks(&out, false)
	c := checkOn(&out, "1.3.0")
	if c == nil || c.Revision != nil || c.Reason != "the last report is for a revision you cannot see" {
		t.Errorf("check = %+v", c)
	}
	if base := checkOn(snap, "1.3.0"); base == nil || base.Revision == nil {
		t.Errorf("the restricted derivation wrote through the snapshot: %+v", base)
	}
}

func TestRestrictHidesExtensionChecksOfUnreadableCluster(t *testing.T) {
	noX := func(cluster, ns string) bool { return cluster != "cluster-x" }
	// The only record comes from cluster-x: no check, and the status read
	// without it.
	snap := clusterFleet(t, map[string][]RawExtension{"cluster-x": {extReport("1.3.0", reportedAbsent)}})
	want := clusterFleet(t, nil).Services[extKey].Status
	r := NewQuery(snap).Restrict(noX, nil)
	if c := checkOn(r.snap, "1.3.0"); c != nil {
		t.Errorf("a check from an unreadable cluster shows: %+v", c)
	}
	if got := r.snap.Services[extKey].Status; got != want || snap.Services[extKey].Status != StatusNonCompliant {
		t.Errorf("restricted status = %s, want %s (unrestricted %s)", got, want, snap.Services[extKey].Status)
	}
	// A readable record from another cluster still speaks.
	snap = clusterFleet(t, map[string][]RawExtension{"cluster-x": {extReport("1.3.0", reportedAbsent)}, "cluster-a": {extReport("1.3.0", producedBy("a-tool"))}})
	r = NewQuery(snap).Restrict(noX, nil)
	if c := checkOn(r.snap, "1.3.0"); c == nil || c.State != ExtensionCovered || c.Producer != "a-tool" {
		t.Errorf("check = %+v", c)
	}
	if c := checkOn(snap, "1.3.0"); c == nil || c.State != ExtensionFailing {
		t.Errorf("unrestricted check = %+v", c)
	}
}

func TestExplainServiceNamesExtensionProducer(t *testing.T) {
	cases := []struct {
		name    string
		window  time.Duration
		reports []RawExtension
		code    string
		message string
	}{
		{"failing", 0, []RawExtension{extReport("1.3.0", reportedAbsent)}, "CAPABILITY_ABSENT", "reported absent by acme-backup"},
		{"stale", 2 * time.Hour, []RawExtension{extReport("1.3.0", reportedAt(3*time.Hour))}, LimitationSourceStale, "the last report is older than the freshness window"},
		{"non-observed", 0, []RawExtension{extReport("1.3.0", reportedFailed)}, LimitationEvidenceMissing, "acme-backup reported failed"},
		{"covered", 0, []RawExtension{extReport("1.3.0")}, "", ""},
		{"failing on a revision no target runs", 0, []RawExtension{extReport("1.2.0", reportedAbsent), extReport("1.3.0")}, "", ""},
	}
	for _, tc := range cases {
		col := extFleet("1.2.0", "1.3.0")
		col.Targets = []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)}
		res, err := NewQuery(extSnap(t, tc.window, col, tc.reports)).Explain(string(extKey))
		if err != nil {
			t.Fatal(err)
		}
		var got []Reason
		for _, r := range res.Reasons {
			if r.Assertion != nil && r.Assertion.Kind == "capability" {
				got = append(got, r)
			}
		}
		if tc.code == "" {
			if len(got) != 0 {
				t.Errorf("%s: reasons = %+v", tc.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Code != tc.code || got[0].Message != tc.message || got[0].Assertion.Name != backupRef || got[0].ObservedAt == nil || got[0].Source != "evidence-0" {
			t.Errorf("%s: reasons = %+v", tc.name, got)
		}
	}
}

func TestAttentionExtensionItemsCurrentRevisionOnly(t *testing.T) {
	cases := []struct {
		name     string
		window   time.Duration
		reports  []RawExtension
		code     string
		category string
		reason   string
		next     string
	}{
		{"failing", 0, []RawExtension{extReport("1.3.0", reportedAbsent)}, "CAPABILITY_ABSENT", categoryNonCompliant,
			"acme-backup reports x.acme.io/backup absent on 1.3.0", "Fix x.acme.io/backup or ask acme-backup why it reports it absent"},
		{"stale", 2 * time.Hour, []RawExtension{extReport("1.3.0", reportedAt(3*time.Hour))}, codeExtensionUnreported, categoryStale,
			"the last report is older than the freshness window", "Have acme-backup report on 1.3.0"},
		{"non-observed", 0, []RawExtension{extReport("1.3.0", reportedFailed)}, codeExtensionUnreported, categoryUnknown,
			"acme-backup reported failed", "Have acme-backup report on 1.3.0"},
		{"covered", 0, []RawExtension{extReport("1.3.0")}, "", "", "", ""},
		{"failing on a revision no target runs", 0, []RawExtension{extReport("1.2.0", reportedAbsent), extReport("1.3.0")}, "", "", "", ""},
	}
	for _, tc := range cases {
		col := extFleet("1.2.0", "1.3.0")
		col.Targets = []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)}
		q := NewQuery(extSnap(t, tc.window, col, tc.reports))
		var got []AttentionItem
		for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
			if it.Code == "CAPABILITY_ABSENT" || it.Code == codeExtensionUnreported {
				got = append(got, it)
			}
		}
		if tc.code == "" {
			if len(got) != 0 {
				t.Errorf("%s: items = %+v", tc.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Code != tc.code || got[0].Category != tc.category || got[0].Reason != tc.reason || got[0].NextStep != tc.next ||
			got[0].NextAction != nil || got[0].Service != string(extKey) || got[0].Label != "payments 1.3.0: "+backupRef {
			t.Errorf("%s: items = %+v", tc.name, got)
		}
	}
}

func TestKnowledgeUnverifiedCapabilityCountsUncheckedOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reports []RawExtension
		gap     bool
	}{
		{"no report", nil, true},
		{"covered", []RawExtension{extReport("1.3.0")}, false},
		{"failing", []RawExtension{extReport("1.3.0", reportedAbsent)}, false},
		{"report on another revision only", []RawExtension{extReport("1.2.0")}, false},
	} {
		col := extFleet("1.2.0", "1.3.0")
		col.Targets = []RawTarget{extTarget("prod", "1.3.0", StatusCompliant)}
		q := NewQuery(extSnap(t, 0, col, tc.reports))
		gaps, _ := q.serviceKnowledge(q.snap.Services[extKey])
		if got := slices.Contains(gaps, GapUnverifiedCapability); got != tc.gap {
			t.Errorf("%s: gaps = %v, want unverified-capability %t", tc.name, gaps, tc.gap)
		}
	}
}
