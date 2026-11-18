package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// migrationNotes are the notes of r that name a pactoVersion.
func migrationNotes(r ci.Row) []string {
	return slices.DeleteFunc(slices.Clone(r.Notes), func(n string) bool { return !strings.HasPrefix(n, "pacto.yaml is ") })
}

func TestCIPlanMigratedLine(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	cur := contract.Current()
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme"})
	if err != nil {
		t.Fatal(err)
	}
	if got := migrationNotes(pubRow(t, p, "legacy")); !slices.Equal(got, []string{"pacto.yaml is " + prevVersion + ", publishes as " + cur + " (migrated in memory)"}) {
		t.Fatalf("plan notes %q", got)
	}
	r := pubRow(t, e.ciPublish(t, nil), "legacy")
	if got := migrationNotes(r); r.State != ci.StatePublished || !slices.Equal(got, []string{"pacto.yaml is " + prevVersion + ", published as " + cur + " (migrated in memory)"}) {
		t.Fatalf("publish row %+v", r)
	}
}

// TestCIPlanKeptLine: a declared build is deprecated from the current
// version, so raising the contract would add a warning; it is kept and
// published as written, with one note naming why.
func TestCIPlanKeptLine(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", e.deployPub("legacy"))), "legacy/render.sh": "#!/bin/sh\n"})
	r := pubRow(t, e.ciPublish(t, nil), "legacy")
	got := migrationNotes(r)
	if r.State != ci.StatePublished || len(got) != 1 || !strings.HasPrefix(got[0], "pacto.yaml is "+prevVersion+", kept at "+prevVersion+" (") ||
		!strings.Contains(got[0], " at publish.artifacts[0]") || !strings.HasSuffix(got[0], " from "+contract.Current()+")") {
		t.Fatalf("row %+v", r)
	}
	f := &ciFolder{migration: CIMigration{From: "2.3", To: "2.3", State: migrationKept, Reason: "INFERRED_RECORD_INVALID at pacto.inferred.yaml after completion"}}
	if got := migrationNote(f); got != "pacto.yaml is 2.3, kept at 2.3 (INFERRED_RECORD_INVALID at pacto.inferred.yaml after completion)" {
		t.Fatalf("after completion %q", got)
	}
}

func TestCIPlanUnchangedBehindLine(t *testing.T) {
	cur := contract.Current()
	written := &contract.Bundle{}
	for _, tc := range []struct {
		name string
		f    *ciFolder
		want string
	}{
		{"migrated, as-written baseline", &ciFolder{b: written, asWritten: written, row: ci.Row{Changed: "no"}, migration: CIMigration{From: prevVersion, To: cur, State: migrationMigrated}},
			"pacto.yaml is " + prevVersion + ", published as " + prevVersion + " (raises to " + cur + " when it next changes)"},
		{"migrated, migrated baseline", &ciFolder{b: &contract.Bundle{}, asWritten: written, row: ci.Row{Changed: "no"}, migration: CIMigration{From: prevVersion, To: cur, State: migrationMigrated}},
			"pacto.yaml is " + prevVersion + ", published as " + cur + " (migrated in memory)"},
		{"kept", &ciFolder{b: written, asWritten: written, row: ci.Row{Changed: "no"}, migration: CIMigration{From: "2.3", To: "2.3", State: migrationKept, Reason: "ENVIRONMENT_VALUES_MISSING at environments[0] from " + cur}},
			"pacto.yaml is 2.3, kept at 2.3 (ENVIRONMENT_VALUES_MISSING at environments[0] from " + cur + ")"},
		{"upgrade error", &ciFolder{b: written, asWritten: written, row: ci.Row{Changed: "no"}, migration: CIMigration{From: "2.3", To: "2.3", State: migrationKept, Reason: "boom"}},
			"pacto.yaml is 2.3, kept at 2.3 (boom)"},
	} {
		if got := migrationNote(tc.f); got != tc.want || strings.Contains(got, "raises") != strings.Contains(tc.want, "raises") {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// End to end: a baseline an earlier CLI published as written.
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	e.publishAs(t, "legacy", "r1")
	r := pubRow(t, e.ciPublish(t, nil), "legacy")
	if got := migrationNotes(r); r.State != ci.StateUnchanged || !slices.Equal(got, []string{"pacto.yaml is " + prevVersion + ", published as " + prevVersion + " (raises to " + cur + " when it next changes)"}) {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPlanCurrentPrintsNoLine(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.0.0", "", "")})
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme"})
	if err != nil {
		t.Fatal(err)
	}
	if r := pubRow(t, p, "payments"); len(migrationNotes(r)) != 0 {
		t.Fatalf("notes %q", r.Notes)
	}
	if got := migrationNote(&ciFolder{migration: CIMigration{From: contract.Current(), To: contract.Current(), State: migrationCurrent}}); got != "" {
		t.Fatalf("current note %q", got)
	}
}

// TestCIMigrationNeverChangesExit: a repository that is clear apart from one
// migrated and one kept contract exits 0 in plan and publish mode.
func TestCIMigrationNeverChangesExit(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{
		"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", "")),
		"old/pacto.yaml":    asBehind(e.contract("old", "1.0.0", "", e.deployPub("old"))), "old/render.sh": "#!/bin/sh\n",
	})
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme"})
	if err != nil || p.Exit != 0 || len(migrationNotes(pubRow(t, p, "legacy"))) != 1 || len(migrationNotes(pubRow(t, p, "old"))) != 1 {
		t.Fatalf("%v plan exit %d rows %+v", err, p.Exit, p.Rows)
	}
	if p := e.ciPublish(t, nil); p.Exit != 0 {
		t.Fatalf("publish exit %d rows %+v", p.Exit, p.Rows)
	}
}
