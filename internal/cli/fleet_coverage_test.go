package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// coverageOfLocal is stats.CoverageOf over the same local fleet status reads.
func coverageOfLocal(t *testing.T, root, evidence string) runrecord.Coverage {
	t.Helper()
	snap, err := app.NewService(nil, nil).Fleet(t.Context(), app.FleetOptions{LocalRoots: []string{root}, TargetStateFiles: []string{evidence}})
	if err != nil {
		t.Fatal(err)
	}
	return stats.CoverageOf(fleet.NewQuery(snap))
}

func TestFleetStatusWritesCoverageRecord(t *testing.T) {
	statsOff(t)
	root, ev := testutil.ImpactFleet(t, "^1.0.0")
	var calls []statsCall
	_, stderr, err := runRoot(t, recDeps(defaultDeps(), &calls, nil), nil,
		"fleet", "status", "--local", root, "--target-state", ev, "--stats-registry", "reg.example/acme/stats")
	if err != nil || len(calls) != 1 || !strings.Contains(stderr, "pacto: run record published to reg.example/acme/stats@") {
		t.Fatalf("err %v, calls %d, stderr %s", err, len(calls), stderr)
	}
	got := calls[0].rec
	want := coverageOfLocal(t, root, ev)
	if got.Kind != runrecord.KindCoverage || got.Service != runrecord.CoverageService || got.Coverage == nil {
		t.Fatalf("record %+v", got)
	}
	// The snapshot time differs between the two builds; every count must not.
	want.SnapshotAsOf = got.Coverage.SnapshotAsOf
	if *got.Coverage.Knowledge != *want.Knowledge || got.Coverage.Links != want.Links ||
		got.Coverage.Services != want.Services || got.Coverage.Uncontracted != want.Uncontracted || got.Coverage.Completeness != want.Completeness {
		t.Fatalf("coverage %+v, want %+v", got.Coverage, want)
	}
	if wantID := runrecord.ID(runrecord.KindCoverage, runrecord.CoverageService, "", got.Coverage.SnapshotAsOf.Format(time.RFC3339)); got.ID != wantID {
		t.Fatalf("id %s, want the coverage id %s", got.ID, wantID)
	}
	if _, err := runrecord.Encode(got); err != nil {
		t.Fatalf("written record does not parse: %v", err)
	}
}

func TestFleetStatusCoverageToFile(t *testing.T) {
	statsOff(t)
	root, ev := testutil.ImpactFleet(t, "^1.0.0")
	file := filepath.Join(t.TempDir(), "coverage.json")
	_, stderr, err := runRoot(t, defaultDeps(), nil, "fleet", "status", "--local", root, "--target-state", ev, "--stats-file", file)
	data, rerr := os.ReadFile(file)
	if err != nil || rerr != nil {
		t.Fatalf("err %v %v, stderr %s", err, rerr, stderr)
	}
	rec, perr := runrecord.Parse(data)
	if perr != nil || rec.Kind != runrecord.KindCoverage || rec.Coverage.Completeness != "complete" {
		t.Fatalf("record %+v %v", rec, perr)
	}
}

func TestFleetStatusIgnoresStatsEnv(t *testing.T) {
	root, ev := testutil.ImpactFleet(t, "^1.0.0")
	file := filepath.Join(t.TempDir(), "coverage.json")
	t.Setenv("PACTO_STATS_REGISTRY", "reg.example/acme/stats")
	t.Setenv("PACTO_STATS_FILE", file)
	var calls []statsCall
	_, stderr, err := runRoot(t, recDeps(defaultDeps(), &calls, nil), nil, "fleet", "status", "--local", root, "--target-state", ev)
	if _, serr := os.Stat(file); err != nil || len(calls) != 0 || !errors.Is(serr, os.ErrNotExist) || strings.Contains(stderr, "run record") {
		t.Fatalf("err %v, calls %d, file %v, stderr %s", err, len(calls), serr, stderr)
	}
}

func TestFleetStatusCoverageFailureKeepsExitCode(t *testing.T) {
	statsOff(t)
	root, ev := testutil.ImpactFleet(t, "^1.0.0")
	args := []string{"fleet", "status", "--local", root, "--target-state", ev}
	wantOut, _, wantErr := runRoot(t, defaultDeps(), nil, args...)
	var calls []statsCall
	out, stderr, err := runRoot(t, recDeps(defaultDeps(), &calls, errors.New("403 Forbidden")), nil,
		append(args, "--stats-registry", "reg.example/acme/stats")...)
	if (err == nil) != (wantErr == nil) || out != wantOut || !strings.Contains(stderr, "pacto: warning: run record not published: 403 Forbidden") {
		t.Fatalf("err %v (want %v), stderr %s", err, wantErr, stderr)
	}
}

func TestFleetStatusCoveragePartialSnapshot(t *testing.T) {
	statsOff(t)
	root, _ := testutil.ImpactFleet(t, "^1.0.0")
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var calls []statsCall
	_, stderr, _ := runRoot(t, recDeps(defaultDeps(), &calls, nil), nil,
		"fleet", "status", "--local", root, "--target-state", missing, "--stats-registry", "reg.example/acme/stats")
	if len(calls) != 1 || calls[0].rec.Coverage.Completeness != "partial" {
		t.Fatalf("calls %+v, stderr %s", calls, stderr)
	}
}

func TestFleetStatusCoverageEmptySnapshotWritten(t *testing.T) {
	statsOff(t)
	file := filepath.Join(t.TempDir(), "coverage.json")
	_, stderr, err := runRoot(t, defaultDeps(), nil, "fleet", "status", "--local", t.TempDir(), "--stats-file", file)
	data, rerr := os.ReadFile(file)
	if err != nil || rerr != nil {
		t.Fatalf("err %v %v, stderr %s", err, rerr, stderr)
	}
	rec, perr := runrecord.Parse(data)
	if perr != nil || rec.Coverage.Completeness != "partial" || rec.Coverage.Services != 0 {
		t.Fatalf("record %+v %v", rec, perr)
	}
}

func TestFleetStatusHelpHasNoEnvDefault(t *testing.T) {
	status, _, err := newFleetCommand(app.NewService(nil, nil), nil, &runRecorder{}).Find([]string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stats-registry", "stats-file"} {
		f := status.Flags().Lookup(name)
		if f == nil || strings.Contains(f.Usage, "$PACTO_") || strings.Contains(f.Usage, "`") {
			t.Fatalf("--%s: %+v", name, f)
		}
	}
}

// saveFlagsOnly computes the run id when the record carries none.
func TestSaveFlagsOnlyComputesMissingID(t *testing.T) {
	cmd := &cobra.Command{}
	addCoverageFlags(cmd)
	_ = cmd.Flags().Set("stats-registry", "reg/x")
	cmd.SetErr(&strings.Builder{})
	var calls []statsCall
	rec := newRunRecorder("dev", recDeps(deps{}, &calls, nil))
	r := validRecord()
	r.ID = ""
	rec.saveFlagsOnly(cmd, &r)
	if len(calls) != 1 || calls[0].rec.ID != runrecord.ID(r.Kind, r.Service, r.OldRevision, r.NewRevision) {
		t.Fatalf("calls %+v", calls)
	}
}
