package fleet

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// at is fixedNow minus d, as a pointer.
func at(d time.Duration) *time.Time {
	t := fixedNow().Add(-d)
	return &t
}

const prodRepo = ctbRoot + "/pacto-inferred/acme-idp/services"

// TestProducerRowStatuses: each producer row has its own status and says why.
func TestProducerRowStatuses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		facts   ProducerFacts
		status  SourceStatus
		usedFor []string
		why     string
	}{
		{"fresh targets", ProducerFacts{NewestReport: at(time.Hour)}, SourceAvailable, []string{UseReportedTargets}, ""},
		{"silent producer", ProducerFacts{NewestReport: at(26 * time.Hour)}, SourceStale, []string{UseReportedTargets}, "last reported 26h ago"},
		{"old contribution", ProducerFacts{Repository: prodRepo, Read: 1, NewestContribution: at(40 * 24 * time.Hour)}, SourceStale,
			[]string{UseContributedRecords}, "recorded 40d ago"},
		{"undated contribution", ProducerFacts{Repository: prodRepo, Read: 1}, SourceStale, []string{UseContributedRecords}, "no record says when"},
		{"fresh contribution", ProducerFacts{Repository: prodRepo, Read: 2, NewestContribution: at(time.Hour)}, SourceAvailable, []string{UseContributedRecords}, ""},
		{"no tag", ProducerFacts{Repository: prodRepo}, SourcePartial, []string{UseContributedRecords}, "no contribution found at " + prodRepo},
		{"refused", ProducerFacts{Repository: prodRepo, Read: 1, NewestContribution: at(time.Hour), Problems: []string{"2 records were unsigned"}},
			SourcePartial, []string{UseContributedRecords}, "2 records were unsigned"},
		{"unlisted", ProducerFacts{Repository: prodRepo, Unread: prodRepo + ": authentication with the source failed"}, SourceUnavailable,
			[]string{UseContributedRecords}, "could not read " + prodRepo + ": authentication"},
		{"one root unlisted", ProducerFacts{Repository: prodRepo + " and other.example/acme/pacto-inferred/acme-idp/services", Read: 1, NewestContribution: at(time.Hour),
			Unread: "other.example/acme/pacto-inferred/acme-idp/services: it is no contribution repository"}, SourcePartial,
			[]string{UseContributedRecords}, "could not read other.example/acme/pacto-inferred/acme-idp/services: it is no contribution repository"},
		{"adapter only", ProducerFacts{AdapterKinds: []string{"ResourceUsage"}}, SourceAvailable, []string{UseReporting}, ""},
		{"all three", ProducerFacts{NewestReport: at(time.Minute), AdapterKinds: []string{"ResourceUsage"}, Repository: prodRepo, Read: 1, NewestContribution: at(time.Hour)},
			SourceAvailable, []string{UseReportedTargets, UseReporting, UseContributedRecords}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.facts.Producer = "acme-idp"
			row, ok := ProducerRow(tc.facts, fixedNow(), 24*time.Hour)
			if !ok || row.ID != "producer/acme-idp" || row.Name != "acme-idp" || row.Status != tc.status || !slices.Equal(row.UsedFor, tc.usedFor) {
				t.Fatalf("row = %+v %v", row, ok)
			}
			if tc.why == "" && len(row.Limitations) != 0 {
				t.Errorf("an available row says %+v", row.Limitations)
			}
			if tc.why != "" && !slices.ContainsFunc(row.Limitations, func(l Limitation) bool { return strings.Contains(l.Message, tc.why) }) {
				t.Errorf("limitations = %+v, want %q", row.Limitations, tc.why)
			}
		})
	}
}

// TestProducerRowEdges: a producer with nothing to show has no row; no
// window checks no age; AsOf is the newest time and ignored tags a detail.
func TestProducerRowEdges(t *testing.T) {
	if _, ok := ProducerRow(ProducerFacts{Producer: "quiet"}, fixedNow(), time.Hour); ok {
		t.Error("a producer with no target, no adapter and no grant has a row")
	}
	row, _ := ProducerRow(ProducerFacts{Producer: "p", NewestReport: at(26 * time.Hour), Repository: prodRepo, Read: 1, NewestContribution: at(time.Hour), Ignored: 2}, fixedNow(), 0)
	if row.Status != SourceAvailable || !row.AsOf.Equal(*at(time.Hour)) || len(row.Details) != 1 || !strings.HasPrefix(row.Details[0], "2 tags") {
		t.Errorf("no window checks no age; AsOf is the newest time; ignored tags are a detail: %+v", row)
	}
}

// TestProducerRowEvidenceNamesNoNamespace: the evidence names no namespace
// and counts no service.
func TestProducerRowEvidenceNamesNoNamespace(t *testing.T) {
	row, _ := ProducerRow(ProducerFacts{Producer: "acme-idp", NewestReport: at(3 * time.Minute), Repository: prodRepo, Read: 7, NewestContribution: at(time.Hour)}, fixedNow(), time.Hour)
	want := "reports targets through the Evidence Server; newest report 3m ago; contributes inferred services from " + prodRepo
	if row.Evidence != want {
		t.Errorf("evidence = %q", row.Evidence)
	}
	if regexp.MustCompile(`\b7\b`).MatchString(row.Evidence + strings.Join(row.Details, " ")) {
		t.Errorf("the row counts services: %+v", row)
	}
}

// TestRestrictProducerRowCountsNothing: a reader who sees none of a
// producer's contributed services reads the same row as one who sees all.
func TestRestrictProducerRowCountsNothing(t *testing.T) {
	row, _ := ProducerRow(ProducerFacts{Producer: "acme-idp", Repository: prodRepo, Read: 1, NewestContribution: at(time.Hour)}, fixedNow(), 24*time.Hour)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour},
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Providers: []Provider{row},
			Revisions: []RawRevision{ctbRev("ledger-sync", ctbYAML("ledger-sync"), ctbRepo, "acme-idp", ctbRecord(), fixedNow())}}))
	if err != nil {
		t.Fatal(err)
	}
	all := NewQuery(snap).Providers().Providers
	none := NewQuery(snap).Restrict(anyCluster(func(string) bool { return false }), nil).Providers().Providers
	if len(all) != 1 || len(none) != 1 || all[0].Evidence != none[0].Evidence || regexp.MustCompile(`\d`).MatchString(none[0].Evidence) {
		t.Errorf("all = %+v\nnone = %+v", all, none)
	}
}

func TestSanitizedReasonNeverEchoes(t *testing.T) {
	if got := SanitizedReason(errors.New("GET https://user:secret@ghcr.io/v2/: 401")); strings.Contains(got, "secret") || got != "authentication with the source failed" {
		t.Errorf("reason = %q", got)
	}
}

// TestRestrictProducerRowHidesReportsOfHiddenTargets: a reader who sees none
// of the targets a producer reported reads nothing about its reports: not
// their use, their newest time nor that they stopped.
func TestRestrictProducerRowHidesReportsOfHiddenTargets(t *testing.T) {
	facts := ProducerFacts{Producer: "acme-idp", NewestReport: at(30 * time.Hour), Repository: prodRepo, Read: 1, NewestContribution: at(time.Hour)}
	row, _ := ProducerRow(facts, fixedNow(), 24*time.Hour)
	tgt := RawTarget{Scope: "acme-idp", Kind: "external", Name: "payments-core-prod", Service: "payments-core", Compliance: StatusCompliant, EvidenceAt: at(30 * time.Hour)}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour},
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Providers: []Provider{row}, Targets: []RawTarget{tgt}}))
	if err != nil {
		t.Fatal(err)
	}
	all := NewQuery(snap).Providers().Providers
	if len(all) != 1 || !slices.Contains(all[0].UsedFor, UseReportedTargets) || all[0].Status != SourceStale {
		t.Fatalf("all = %+v", all)
	}
	none := NewQuery(snap).Restrict(anyCluster(func(string) bool { return false }), nil).Providers().Providers
	if len(none) != 1 {
		t.Fatalf("none = %+v", none)
	}
	got := none[0]
	if slices.Contains(got.UsedFor, UseReportedTargets) || strings.Contains(got.Evidence, "report") || got.Status != SourceAvailable ||
		!got.AsOf.Equal(*at(time.Hour)) || slices.ContainsFunc(got.Limitations, func(l Limitation) bool { return strings.Contains(l.Message, "reported") }) {
		t.Errorf("a hidden target's report shows: %+v", got)
	}
}

// TestRefusedContributionMakesRowPartial: a bundle the reader counted read
// and Build refused makes its producer row partial, saying why.
func TestRefusedContributionMakesRowPartial(t *testing.T) {
	row, _ := ProducerRow(ProducerFacts{Producer: "acme-idp", Repository: prodRepo, Read: 1, NewestContribution: at(time.Hour)}, fixedNow(), 24*time.Hour)
	declaring := "pactoVersion: \"2.5\"\nservice:\n  name: ledger-sync\n  version: 0.1.0\n  owner: team-a\n"
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour},
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Providers: []Provider{row},
			Revisions: []RawRevision{ctbRev("ledger-sync", declaring, ctbRepo, "acme-idp", ctbRecord(), fixedNow())}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []*Query{NewQuery(snap), NewQuery(snap).Restrict(anyCluster(func(string) bool { return false }), nil)} {
		rows := q.Providers().Providers
		if len(rows) != 1 || rows[0].Status != SourcePartial || !slices.ContainsFunc(rows[0].Limitations, func(l Limitation) bool {
			return strings.Contains(l.Message, "refused because it declares service.owner")
		}) {
			t.Errorf("rows = %+v", rows)
		}
	}
}
