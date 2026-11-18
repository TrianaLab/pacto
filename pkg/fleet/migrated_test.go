package fleet

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// migratedOpts is newsletter published at contract.Current(), its record
// saying pacto ci raised pacto.yaml from from to to.
func migratedOpts(from, to string) recordFleetOpts {
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs:     contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"},
		Migrations: []contract.InferredMigration{{From: from, To: to, Source: "pacto.yaml", By: "pacto ci 4.6.0"}}}
	return recordFleetOpts{version: contract.Current(), record: rec.Marshal(), manifest: recordManifest,
		contract: "pactoVersion: \"" + contract.Current() + "\"\nservice:\n  name: newsletter\n"}
}

func TestRevisionDetailMigratedFromRecordOnly(t *testing.T) {
	prev := contract.SupportedVersions[len(contract.SupportedVersions)-2]
	_, rd := newsletterDetail(t, recordFleet(t, migratedOpts(prev, contract.Current())))
	if rd.MigratedFrom != prev || rd.PactoVersionCurrent != "" || rd.InferredUnreadable {
		t.Errorf("migrated: from %q current %q unreadable %v", rd.MigratedFrom, rd.PactoVersionCurrent, rd.InferredUnreadable)
	}
	o := migratedOpts(prev, contract.Current())
	o.record = nil
	_, rd = newsletterDetail(t, recordFleet(t, o))
	if rd.MigratedFrom != "" || rd.PactoVersionCurrent != "" {
		t.Errorf("no record: from %q current %q", rd.MigratedFrom, rd.PactoVersionCurrent)
	}
}

func TestRevisionDetailForgedMigrationDropped(t *testing.T) {
	// The entry parses but says pacto.yaml was published as an older version
	// than the one it declares.
	n := len(contract.SupportedVersions)
	q := recordFleet(t, migratedOpts(contract.SupportedVersions[n-3], contract.SupportedVersions[n-2]))
	_, rd := newsletterDetail(t, q)
	if rd.MigratedFrom != "" || !rd.InferredUnreadable {
		t.Errorf("forged: from %q unreadable %v", rd.MigratedFrom, rd.InferredUnreadable)
	}
	if s := q.SnapshotForSerialization(); !hasLimitation(s.Limitations, LimitationInferredRecordUnreadable) {
		t.Errorf("limitations = %+v", s.Limitations)
	}
}

func TestMigratedFromNeedsMatchingEntry(t *testing.T) {
	cur := contract.Current()
	for _, tc := range []struct {
		name string
		rec  *contract.InferredRecord
	}{
		{"no record", nil},
		{"other source", &contract.InferredRecord{Migrations: []contract.InferredMigration{{From: "2.3", To: cur, Source: "other.yaml"}}}},
		{"other version", &contract.InferredRecord{Migrations: []contract.InferredMigration{{From: "2.3", To: "99.0", Source: "pacto.yaml"}}}},
	} {
		if got := migratedFrom(&ContractRevision{PactoVersion: cur, Record: tc.rec}); got != "" {
			t.Errorf("%s: migratedFrom = %q", tc.name, got)
		}
	}
}

func TestPactoVersionTextMigrated(t *testing.T) {
	for _, tc := range []struct {
		r    RevisionDetailData
		want string
	}{
		{RevisionDetailData{PactoVersion: "2.5", MigratedFrom: "2.4"}, "2.5 (migrated from 2.4, inferred)"},
		{RevisionDetailData{PactoVersion: "2.5", MigratedFrom: "2.4", PactoVersionCurrent: "99.0"}, "2.5 (behind 99.0; migrated from 2.4, inferred)"},
		{RevisionDetailData{PactoVersion: "2.3", PactoVersionCurrent: "2.5"}, "2.3 (behind 2.5)"},
		{RevisionDetailData{PactoVersion: "9.9", PactoVersionCurrent: "2.5", PactoVersionUnknown: true}, "9.9 (unknown to this Pacto, latest 2.5)"},
		{RevisionDetailData{PactoVersion: "99.0", MigratedFrom: "2.4", PactoVersionCurrent: "2.5", PactoVersionUnknown: true}, "99.0 (unknown to this Pacto, latest 2.5)"},
		{RevisionDetailData{}, ""},
	} {
		if got := tc.r.PactoVersionText(); got != tc.want {
			t.Errorf("PactoVersionText(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
}
