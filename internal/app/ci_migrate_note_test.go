package app

import (
	"slices"
	"strconv"
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

// TestCIPlanKeptLine: raising the contract would resolve a configuration ref
// it does not resolve today, which fails; it is kept and published as
// written, with one note naming why.
func TestCIPlanKeptLine(t *testing.T) {
	e := newCIPub(t)
	keptLegacy(t, e)
	r := pubRow(t, e.ciPublish(t, nil), "legacy")
	got := migrationNotes(r)
	if r.State != ci.StatePublished || len(got) != 1 || !strings.HasPrefix(got[0], "pacto.yaml is "+keptFrom+", kept at "+keptFrom+" (") ||
		!strings.Contains(got[0], " at configurations[0].ref") || !strings.HasSuffix(got[0], " from "+contract.Current()+")") {
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
		{"migrated, baseline not read", &ciFolder{b: &contract.Bundle{}, asWritten: written, migration: CIMigration{From: prevVersion, To: cur, State: migrationMigrated}},
			"pacto.yaml is " + prevVersion + ", would publish as " + cur + " if it changed (baseline not read)"},
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

// TestCIPlanUnreadBaselineLine: a migrated row whose baseline is never read
// (publish.bundle outside the registry) says what it would publish, not
// what it publishes.
func TestCIPlanUnreadBaselineLine(t *testing.T) {
	e := newCIPub(t)
	e.write(t, map[string]string{"legacy/pacto.yaml": asBehind(e.contract("legacy", "1.0.0", "", ""))})
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: "other.example/acme"})
	if err != nil {
		t.Fatal(err)
	}
	r := pubRow(t, p, "legacy")
	if got := migrationNotes(r); r.Changed != "" || !slices.Equal(got, []string{"pacto.yaml is " + prevVersion + ", would publish as " + contract.Current() + " if it changed (baseline not read)"}) {
		t.Fatalf("row %+v", r)
	}
}

// TestCIPublishKeptWithoutSectionNamesKeptReason: a kept contract older than
// 2.5 with no publish section fails with the reason the raise was refused,
// never advice to make that raise.
func TestCIPublishKeptWithoutSectionNamesKeptReason(t *testing.T) {
	cur := contract.Current()
	b := &contract.Bundle{Contract: &contract.Contract{PactoVersion: keptFrom}}
	reason := "ENVIRONMENT_VALUES_MISSING at environments[0] from " + cur
	cp := &ciPublisher{bundles: map[string]*contract.Bundle{"legacy": b}, migrations: map[string]CIMigration{"legacy": {From: keptFrom, To: keptFrom, State: migrationKept, Reason: reason}}}
	r := ci.Row{Dir: "legacy", Changed: "yes"}
	cp.row(t.Context(), &r)
	if r.State != ci.StateFailed || r.Reason != "declares no publish section, and pactoVersion stays at "+keptFrom+" ("+reason+"): fix that or declare publish" {
		t.Fatalf("row %+v", r)
	}
}

// keptLegacy writes a behind legacy service whose raise adds a finding:
// configuration refs are resolved from 2.4, and its provider is published
// nowhere.
const keptFrom = "2.3"

func keptLegacy(t *testing.T, e *ciPubEnv) {
	t.Helper()
	extra := "configurations:\n  - {name: db, ref: \"oci://" + e.host + "/acme/pg:16.1.0\", required: true}\n"
	e.write(t, map[string]string{
		"legacy/pacto.yaml": strings.Replace(e.contract("legacy", "1.0.0", extra, e.deployPub("legacy")), strconv.Quote(contract.Current()), strconv.Quote(keptFrom), 1),
		"legacy/render.sh":  "#!/bin/sh\n",
	})
}
