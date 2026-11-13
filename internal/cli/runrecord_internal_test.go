package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

func validRecord() runrecord.Record {
	return runrecord.Record{
		Version: runrecord.Version, ID: runrecord.ID(runrecord.KindDiff, "payments", "a", "b"),
		Kind: runrecord.KindDiff, Service: "payments", OldRevision: "a", NewRevision: "b",
		Outcome: runrecord.OutcomePassed, PactoVersion: "dev", RecordedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}
}

func TestDefaultPublishRunRecord(t *testing.T) {
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	for _, v := range []string{"PACTO_REGISTRY_USERNAME", "PACTO_REGISTRY_PASSWORD", "PACTO_REGISTRY_TOKEN"} {
		t.Setenv(v, "")
	}
	repo := host + "/acme/pacto-stats"
	got, err := defaultPublishRunRecord(t.Context(), repo, validRecord())
	if err != nil || !strings.HasPrefix(got, repo+"@sha256:") {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := defaultPublishRunRecord(t.Context(), "not a reference", validRecord()); err == nil {
		t.Fatal("published to a bad reference")
	}
}

func TestDefaultPublishRunRecordTimeout(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hang.Close()
	host := strings.TrimPrefix(hang.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	old := statsTimeout
	statsTimeout = 50 * time.Millisecond
	t.Cleanup(func() { statsTimeout = old })
	_, err := defaultPublishRunRecord(t.Context(), host+"/acme/pacto-stats", validRecord())
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("err = %v", err)
	}
}

// recCase is one recording command run: the deps it runs over and its args.
type recCase struct {
	name string
	d    func() deps
	args []string
	out  io.Writer // stdout; nil is a buffer
	want runrecord.Record
}

// statsOff clears both stats env vars, so no shell setting leaks in.
func statsOff(t *testing.T) {
	t.Helper()
	t.Setenv("PACTO_STATS_REGISTRY", "")
	t.Setenv("PACTO_STATS_FILE", "")
}

// recDeps is d with a fake publishRunRecord that captures every record and answers err.
func recDeps(d deps, calls *[]statsCall, err error) deps {
	d.publishRunRecord = func(_ context.Context, repo string, r runrecord.Record) (string, error) {
		*calls = append(*calls, statsCall{repo, r})
		if err != nil {
			return "", err
		}
		return repo + "@sha256:abc", nil
	}
	return d
}

// runRoot runs the whole command tree over d and returns stdout, stderr and the error.
func runRoot(t *testing.T, d deps, out io.Writer, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "dev"}, d)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if out == nil {
		out = stdout
	}
	root.SetOut(out)
	root.SetErr(stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// diffDirs writes an old contract and a new one; rename makes the change BREAKING.
func diffDirs(t *testing.T, rename bool) (string, string) {
	t.Helper()
	newYAML := string(testutil.ValidPactoYAML())
	if rename {
		newYAML = strings.Replace(newYAML, "name: test-svc", "name: renamed-svc", 1)
	}
	return writeContractDir(t, string(testutil.ValidPactoYAML())), writeContractDir(t, newYAML)
}

func writeContractDir(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pubDeps(res *publish.Result, err error) func() deps {
	return func() deps { var got app.PublishOptions; return publishDeps(res, err, &got) }
}

func recordCases(t *testing.T) []recCase {
	brOld, brNew := diffDirs(t, true)
	okOld, okNew := diffDirs(t, false)
	refused := fmt.Errorf("%w: version: needs a major bump", publish.ErrNotReady)
	published := &publish.Result{Plan: publishPlan(true), Actions: []string{"pushed"},
		Digests: map[string]string{publish.BundleItem: "sha256:b"}, Deploy: "reg/deploy@sha256:d"}
	published.Plan.Baseline, published.Plan.Classification = "0.9.0", "NON_BREAKING"
	unchanged := &publish.Result{Plan: publishPlan(true), Digests: map[string]string{}}
	failed := &publish.Result{Plan: publishPlan(true), Deploy: "reg/deploy@sha256:d"}
	return append([]recCase{
		{name: "diff breaking", d: defaultDeps, args: []string{"diff", brOld, brNew},
			want: runrecord.Record{Kind: runrecord.KindDiff, Service: "renamed-svc", Classification: "BREAKING", Outcome: runrecord.OutcomeFailed}},
		{name: "diff non-breaking", d: defaultDeps, args: []string{"diff", okOld, okNew},
			want: runrecord.Record{Kind: runrecord.KindDiff, Service: "test-svc", Classification: "NON_BREAKING", Outcome: runrecord.OutcomePassed}},
		{name: "diff breaking, stdout fails", d: defaultDeps, out: errWriter{}, args: []string{"diff", "--output-format", "json", brOld, brNew},
			want: runrecord.Record{Kind: runrecord.KindDiff, Service: "renamed-svc", Classification: "BREAKING", Outcome: runrecord.OutcomeFailed}},
		{name: "publish refused", d: pubDeps(&publish.Result{Plan: publishPlan(false)}, refused), args: []string{"publish"},
			want: runrecord.Record{Kind: runrecord.KindPublish, Service: "payments", NewRevision: "1.0.0", Outcome: runrecord.OutcomeRefused,
				Gate: &runrecord.Gate{Verdict: runrecord.VerdictRefused, Reason: "version"}}},
		{name: "publish published", d: pubDeps(published, nil), args: []string{"publish"},
			want: runrecord.Record{Kind: runrecord.KindPublish, Service: "payments", OldRevision: "0.9.0", NewRevision: "1.0.0",
				BundleDigest: "sha256:b", DeployDigest: "sha256:d", Classification: "NON_BREAKING", Outcome: runrecord.OutcomePublished}},
		{name: "publish unchanged", d: pubDeps(unchanged, nil), args: []string{"publish"},
			want: runrecord.Record{Kind: runrecord.KindPublish, Service: "payments", NewRevision: "1.0.0", Outcome: runrecord.OutcomeUnchanged}},
		{name: "publish failed", d: pubDeps(failed, errors.New("push failed")), args: []string{"publish"},
			want: runrecord.Record{Kind: runrecord.KindPublish, Service: "payments", NewRevision: "1.0.0", Outcome: runrecord.OutcomeFailed}},
	}, impactCases(t)...)
}

// impactDirs writes orders 1.0.0 and a new revision: a workload change is BREAKING.
func impactDirs(t *testing.T, breaking bool) (string, string) {
	t.Helper()
	oldDir, newDir := t.TempDir(), t.TempDir()
	testutil.WriteOrders(t, oldDir, "1.0.0", "service")
	if breaking {
		testutil.WriteOrders(t, newDir, "2.0.0", "job")
	} else {
		testutil.WriteOrders(t, newDir, "1.0.0", "service")
	}
	return oldDir, newDir
}

// impactWant is the impact record a row expects; checkRecord fills the computed fields.
func impactWant(class, verdict, reason, completeness, outcome string, c runrecord.Consumers) runrecord.Record {
	return runrecord.Record{Kind: runrecord.KindImpact, Service: "orders", Classification: class,
		Gate: &runrecord.Gate{Verdict: verdict, Reason: reason}, Consumers: &c, Completeness: completeness, Outcome: outcome}
}

func impactCases(t *testing.T) []recCase {
	brOld, brNew := impactDirs(t, true)
	okOld, okNew := impactDirs(t, false)
	blockRoot, blockEv := testutil.ImpactFleet(t, "^1.0.0")
	openRoot, openEv := testutil.ImpactFleet(t, ">=1.0.0")
	soloRoot := t.TempDir()
	testutil.WriteOrders(t, filepath.Join(soloRoot, "orders"), "1.0.0", "service")
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	blocked := impactWant("BREAKING", runrecord.VerdictBlocked, impact.GateCodeConsumersIncompatible, "complete", runrecord.OutcomeFailed,
		runrecord.Consumers{Incompatible: 1, Blocking: 1})
	return []recCase{
		{name: "impact blocked", d: defaultDeps, args: []string{"impact", brOld, brNew, "--local", blockRoot, "--target-state", blockEv}, want: blocked},
		{name: "impact unverified", d: defaultDeps, args: []string{"impact", brOld, brNew, "--local", openRoot, "--target-state", openEv, "--include-observed"},
			want: impactWant("BREAKING", runrecord.VerdictUnverified, impact.GateCodeConsumersUnchecked, "complete", runrecord.OutcomeUnverified,
				runrecord.Consumers{Compatible: 1})},
		{name: "impact clear", d: defaultDeps, args: []string{"impact", okOld, okNew, "--local", soloRoot, "--target-state", missing},
			want: impactWant("NON_BREAKING", runrecord.VerdictClear, impact.GateCodeNotBreaking, "partial", runrecord.OutcomePassed, runrecord.Consumers{})},
		{name: "impact blocked, stdout fails", d: defaultDeps, out: errWriter{},
			args: []string{"impact", "--output-format", "json", brOld, brNew, "--local", blockRoot, "--target-state", blockEv}, want: blocked},
	}
}

// sameErr says whether two command errors are indistinguishable to a caller.
func sameErr(a, b error) bool {
	var ea, eb *ExitError
	return fmt.Sprintf("%T %v", a, a) == fmt.Sprintf("%T %v", b, b) && errors.As(a, &ea) == errors.As(b, &eb) && (ea == nil || ea.Code == eb.Code)
}

func TestRunRecordExitCodesUnchanged(t *testing.T) {
	statsOff(t)
	for _, c := range recordCases(t) {
		for _, fake := range []struct {
			name string
			err  error
			line string
		}{
			{"ok", nil, "pacto: run record published to ghcr.io/acme/pacto-stats@sha256:abc (unsigned statistics)"},
			{"denied", errors.New("ghcr.io/acme/pacto-stats: denied"), "pacto: warning: run record not published: ghcr.io/acme/pacto-stats: denied"},
			{"deadline", context.DeadlineExceeded, "pacto: warning: run record not published: context deadline exceeded"},
		} {
			t.Run(c.name+"/"+fake.name, func(t *testing.T) {
				var calls []statsCall
				_, _, base := runRoot(t, recDeps(c.d(), &calls, nil), c.out, c.args...)
				_, stderr, err := runRoot(t, recDeps(c.d(), &calls, fake.err), c.out, append(c.args, "--stats-registry", "ghcr.io/acme/pacto-stats")...)
				if !sameErr(base, err) {
					t.Fatalf("error changed: %v, want %v", err, base)
				}
				if !strings.Contains(stderr, fake.line) || len(calls) != 1 {
					t.Fatalf("calls %d, stderr:\n%s", len(calls), stderr)
				}
				checkRecord(t, calls[0].rec, c.want)
			})
		}
	}
}

// checkRecord compares the fields a row decides, and the id the recorder fills.
func checkRecord(t *testing.T, got, want runrecord.Record) {
	t.Helper()
	if want.Kind == runrecord.KindImpact { // the snapshot time: computed, so only its presence is pinned
		if got.AsOf == nil || got.AsOf.IsZero() || got.AsOf.Location() != time.UTC {
			t.Fatalf("asOf %v", got.AsOf)
		}
		want.AsOf = got.AsOf
	}
	if want.Kind == runrecord.KindDiff || want.Kind == runrecord.KindImpact { // bundle digests: computed, so only their shape is pinned
		if !strings.HasPrefix(got.OldRevision, "sha256:") || !strings.HasPrefix(got.NewRevision, "sha256:") {
			t.Fatalf("revisions %q %q", got.OldRevision, got.NewRevision)
		}
		want.OldRevision, want.NewRevision = got.OldRevision, got.NewRevision
	}
	want.Version, want.PactoVersion, want.RecordedAt = runrecord.Version, "dev", got.RecordedAt
	want.ID = runrecord.ID(want.Kind, want.Service, want.OldRevision, want.NewRevision)
	g, _ := runrecord.Encode(got)
	w, _ := runrecord.Encode(want)
	if !bytes.Equal(g, w) || got.RecordedAt.IsZero() {
		t.Fatalf("record\n%s\nwant\n%s", g, w)
	}
}

// timeVariant matches what two runs of one impact answer differ in: the
// snapshot time and the snapshot id derived from it.
var timeVariant = regexp.MustCompile(`\d{4}-\d\d-\d\dT[0-9:.]+(Z|[+-]\d\d:\d\d)|"snapshotId": "sha256:[0-9a-f]+"`)

// stable is out with its time-variant parts masked.
func stable(out string) string { return timeVariant.ReplaceAllString(out, "<t>") }

func TestRunRecordStdoutUnchanged(t *testing.T) {
	statsOff(t)
	for _, c := range recordCases(t) {
		if c.out != nil {
			continue
		}
		var calls []statsCall
		args := append(c.args, "--output-format", "json")
		off, _, _ := runRoot(t, recDeps(c.d(), &calls, nil), nil, args...)
		on, _, _ := runRoot(t, recDeps(c.d(), &calls, nil), nil, append(args, "--stats-registry", "ghcr.io/acme/pacto-stats")...)
		if stable(off) != stable(on) || off == "" {
			t.Fatalf("%s: stdout changed\n%s\n---\n%s", c.name, off, on)
		}
	}
}

func TestRunRecordOffIsInvisible(t *testing.T) {
	statsOff(t)
	for _, c := range recordCases(t) {
		var calls []statsCall
		stdout, stderr, err := runRoot(t, recDeps(c.d(), &calls, nil), c.out, c.args...)
		// The same run through a recorder that would fail loudly on any call.
		d := c.d()
		d.publishRunRecord = func(context.Context, string, runrecord.Record) (string, error) { panic("recorded with recording off") }
		stdout2, stderr2, err2 := runRoot(t, d, c.out, c.args...)
		if len(calls) != 0 || stable(stdout) != stable(stdout2) || stable(stderr) != stable(stderr2) || !sameErr(err, err2) || strings.Contains(stderr, "run record") {
			t.Fatalf("%s: calls %d, stderr %q", c.name, len(calls), stderr)
		}
	}
	cmd := &cobra.Command{}
	addStatsFlags(cmd)
	if (&runRecorder{}).hook(cmd, &revisions{}) != nil {
		t.Fatal("OnResolved is set with recording off")
	}
}

func TestRunRecordNothingWithoutResult(t *testing.T) {
	statsOff(t)
	file := filepath.Join(t.TempDir(), "run.json")
	old, _ := diffDirs(t, false)
	eventRoot, _ := testutil.ImpactFleet(t, "^1.0.0")
	var got app.PublishOptions
	for name, run := range map[string]struct {
		d    deps
		args []string
	}{
		"usage error":       {defaultDeps(), []string{"diff", old}},
		"missing contract":  {defaultDeps(), []string{"diff", old, "/nonexistent/dir"}},
		"publish --plan":    {publishDeps(&publish.Result{Plan: publishPlan(true)}, nil, &got), []string{"publish", "--plan"}},
		"publish no result": {publishDeps(nil, errors.New("no contract"), &got), []string{"publish"}},
		"impact traces":     {defaultDeps(), []string{"impact", old, old, "--traces", filepath.Join(t.TempDir(), "missing.json")}},
		"impact event":      {defaultDeps(), []string{"impact", "--unavailable", "orders", "--local", eventRoot}},
	} {
		var calls []statsCall
		_, _, _ = runRoot(t, recDeps(run.d, &calls, nil), nil, append(run.args, "--stats-registry", "reg/x", "--stats-file", file)...)
		if _, err := os.Stat(file); len(calls) != 0 || err == nil {
			t.Fatalf("%s: calls %d, file %v", name, len(calls), err)
		}
	}
}

func TestRunRecordEnvFallback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "run.json")
	t.Setenv("PACTO_STATS_REGISTRY", "reg/env")
	t.Setenv("PACTO_STATS_FILE", file)
	old, new := diffDirs(t, false)
	var calls []statsCall
	_, stderr, err := runRoot(t, recDeps(defaultDeps(), &calls, nil), nil, "diff", old, new)
	if _, ferr := os.Stat(file); err != nil || ferr != nil || len(calls) != 1 || calls[0].repo != "reg/env" {
		t.Fatalf("err %v, file %v, calls %+v, stderr %s", err, ferr, calls, stderr)
	}
	if !strings.Contains(stderr, "pacto: run record written to "+file) {
		t.Fatalf("stderr %s", stderr)
	}
}

func TestRunRecordNoConfigKey(t *testing.T) {
	statsOff(t)
	old, new := diffDirs(t, false)
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "pacto.yaml"), []byte("stats-registry: x\nstats-file: y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)
	var calls []statsCall
	_, stderr, _ := runRoot(t, recDeps(defaultDeps(), &calls, nil), nil, "diff", old, new)
	if _, err := os.Stat("y"); len(calls) != 0 || err == nil || strings.Contains(stderr, "run record") {
		t.Fatalf("calls %d, file %v, stderr %s", len(calls), err, stderr)
	}
}

func TestRunRecordFlagsOnlyOnRecordingCommands(t *testing.T) {
	statsOff(t)
	for _, c := range []string{"validate", "fleet", "graph"} {
		if _, _, err := runRoot(t, defaultDeps(), nil, c, "--stats-registry", "x"); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("%s: %v", c, err)
		}
	}
	for _, c := range []string{"diff", "impact", "publish"} {
		help, _, err := runRoot(t, defaultDeps(), nil, c, "--help")
		if err != nil || !strings.Contains(help, "--stats-file string") || !strings.Contains(help, "--stats-registry string") {
			t.Fatalf("%s: %v\n%s", c, err, help)
		}
	}
}

func TestRunRecordStatsFileWrites(t *testing.T) {
	statsOff(t)
	file := filepath.Join(t.TempDir(), "run.json")
	old, new := diffDirs(t, true)
	for range 2 { // the second run replaces the first run's record
		_, stderr, _ := runRoot(t, defaultDeps(), nil, "diff", old, new, "--stats-file", file)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%v\n%s", err, stderr)
		}
		parsed, err := runrecord.Parse(data)
		enc, _ := runrecord.Encode(parsed)
		if err != nil || !bytes.Equal(data, enc) {
			t.Fatalf("%v: %s", err, data)
		}
	}
}

func TestRunRecordStatsFileNeverClobbers(t *testing.T) {
	statsOff(t)
	old, new := diffDirs(t, false)
	file := filepath.Join(old, "pacto.yaml")
	before, _ := os.ReadFile(file)
	_, stderr, err := runRoot(t, defaultDeps(), nil, "diff", old, new, "--stats-file", file)
	after, _ := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) || !strings.Contains(stderr, "pacto: warning: run record not written: "+file+" holds something other than a run record") {
		t.Fatalf("err %v, stderr %s", err, stderr)
	}
}

func TestWriteRecordFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := writeRecordFile(dir, []byte("{}")); err == nil {
		t.Fatal("wrote over a directory")
	}
	notDir := writeTemp(t, "f", []byte("x"))
	if err := writeRecordFile(filepath.Join(notDir, "run.json"), []byte("{}")); err == nil {
		t.Fatal("wrote below a file")
	}
}

func TestRunRecordInvalidRecordWarns(t *testing.T) {
	statsOff(t)
	cmd := &cobra.Command{}
	addStatsFlags(cmd)
	file := filepath.Join(t.TempDir(), "run.json")
	_ = cmd.Flags().Set("stats-file", file)
	_ = cmd.Flags().Set("stats-registry", "reg/x")
	stderr := &strings.Builder{}
	cmd.SetErr(stderr)
	var calls []statsCall
	rec := newRunRecorder("dev", recDeps(deps{}, &calls, nil))
	out := &runrecord.Record{Kind: runrecord.KindDiff, Outcome: runrecord.OutcomePassed} // no service
	rec.save(cmd, &out)
	if len(calls) != 0 || strings.Count(stderr.String(), "service is empty") != 2 {
		t.Fatalf("calls %d, stderr %s", len(calls), stderr)
	}
}

func TestRefusalReason(t *testing.T) {
	for want, p := range map[string]publish.Plan{
		"sign":                     {Checks: []publish.Check{{Name: "version", Passed: true}, {Name: "sign"}}},
		string(publish.Conflict):   {Items: []publish.Item{{State: publish.Absent}, {State: publish.Conflict}}},
		string(publish.Unreadable): {Items: []publish.Item{{State: publish.Unreadable}}},
		"":                         {},
	} {
		if got := refusalReason(p); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestRunRecordNoPersonName(t *testing.T) {
	statsOff(t)
	t.Setenv("GITHUB_ACTOR", "alice")
	t.Setenv("GITLAB_USER_EMAIL", "a@b")
	t.Setenv("USER", "alice")
	file := filepath.Join(t.TempDir(), "run.json")
	old, new := diffDirs(t, false)
	_, _, _ = runRoot(t, defaultDeps(), nil, "diff", old, new, "--stats-file", file)
	data, err := os.ReadFile(file)
	if err != nil || bytes.Contains(data, []byte("alice")) || bytes.Contains(data, []byte("@b")) {
		t.Fatalf("%v: %s", err, data)
	}
}

func TestMCPNeverRecords(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "reg/x")
	var got app.PublishOptions
	var calls []statsCall
	d := recDeps(publishDeps(&publish.Result{Plan: publishPlan(true), Actions: []string{"pushed"}}, nil, &got), &calls, nil)
	if _, err := mcpPublish(app.NewService(nil, nil), d)(t.Context(), "svc", "r", false); err != nil || len(calls) != 0 {
		t.Fatalf("err %v, calls %d", err, len(calls))
	}
	oldDir, newDir := impactDirs(t, true)
	root, _ := testutil.ImpactFleet(t, "^1.0.0")
	cmd := &cobra.Command{}
	addFleetSourceFlags(cmd.Flags())
	_ = cmd.Flags().Set("local", root)
	snap, err := app.NewService(nil, nil).Fleet(t.Context(), fleetOptions(cmd))
	if err != nil {
		t.Fatal(err)
	}
	impactFn := mcpImpactProvider(cmd, app.NewService(nil, nil), d, true, true)
	if res, err := impactFn(t.Context(), snap, pactomcp.ImpactRequest{OldRef: oldDir, NewRef: newDir}); err != nil || res == nil || len(calls) != 0 {
		t.Fatalf("err %v, calls %d", err, len(calls))
	}
}

func TestImpactRecordPartition(t *testing.T) {
	statsOff(t)
	// A partial snapshot (a missing target-state) and a consumer that is
	// incompatible on paper but runs on no known target.
	oldDir, newDir := impactDirs(t, true)
	root := t.TempDir()
	testutil.WriteConsumer(t, filepath.Join(root, "webapp"), "^1.0.0")
	file := filepath.Join(t.TempDir(), "run.json")
	stdout, _, _ := runRoot(t, defaultDeps(), nil, "impact", oldDir, newDir, "--local", root,
		"--target-state", filepath.Join(t.TempDir(), "missing.yaml"), "--output-format", "json", "--stats-file", file)
	var res struct {
		AsOf      time.Time
		Consumers []json.RawMessage
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	rec, err := runrecord.Parse(data)
	if err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	c := rec.Consumers
	if rec.Completeness != "partial" || !rec.AsOf.Equal(res.AsOf) ||
		c.Incompatible != 1 || c.Blocking != 0 || c.Incompatible+c.Unknown+c.Compatible+c.Unclassified != len(res.Consumers) {
		t.Fatalf("record %s\nresult consumers %d asOf %v", data, len(res.Consumers), res.AsOf)
	}
	// A verdict this engine does not recognise is counted, never dropped.
	got := impactRecord(revisions{service: "orders"}, &impact.Result{Gate: impact.GateUnverified,
		Consumers:       []impact.AffectedConsumer{{Service: "a"}, {Service: "b", CompatibilityVerdict: impact.CompatibilityUnknown}},
		ConsumerTallies: impact.ConsumerTallies{ByOutcome: []impact.TallyBucket{{Key: impact.OutcomeUnknown, Count: 1}, {Key: impact.OutcomeUnclassified, Count: 1}}}})
	if *got.Consumers != (runrecord.Consumers{Unknown: 1, Unclassified: 1}) || got.Outcome != runrecord.OutcomeUnverified {
		t.Fatalf("record %+v", got)
	}
}

func TestRecordVerdictsMatchImpact(t *testing.T) {
	for rec, imp := range map[string]string{
		runrecord.VerdictBlocked: impact.GateBlocked, runrecord.VerdictUnverified: impact.GateUnverified, runrecord.VerdictClear: impact.GateClear,
	} {
		if rec != imp {
			t.Fatalf("run record verdict %q, impact gate %q", rec, imp)
		}
	}
}

// envOf is a getenv over m.
func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// ghEnv is a GitHub Actions run's environment, with the actor a record never names.
var ghEnv = map[string]string{"GITHUB_REPOSITORY": "acme/payments", "GITHUB_SERVER_URL": "https://github.com",
	"GITHUB_RUN_ID": "42", "GITHUB_ACTOR": "alice", "CI_PROJECT_PATH": "acme/gl", "CI_JOB_URL": "https://gitlab.com/acme/gl/-/jobs/7"}

type ctxWant struct{ repo, runURL, env string }

func checkContext(t *testing.T, spec string, env map[string]string, want ctxWant, warnings int) {
	t.Helper()
	repo, runURL, envName, warns := statsContext(spec, envOf(env))
	if (ctxWant{repo, runURL, envName}) != want || len(warns) != warnings {
		t.Fatalf("%q: got %q %q %q %q, want %+v and %d warnings", spec, repo, runURL, envName, warns, want, warnings)
	}
	for _, w := range warns {
		if strings.Contains(w, "alice") || strings.Contains(w, "u:p") {
			t.Fatalf("%q: warning echoes a secret: %q", spec, w)
		}
	}
}

func TestStatsContextOnlyNamedFields(t *testing.T) {
	checkContext(t, "", ghEnv, ctxWant{}, 0)
	checkContext(t, "repository", ghEnv, ctxWant{repo: "acme/payments"}, 0)
	checkContext(t, " repository , ", ghEnv, ctxWant{repo: "acme/payments"}, 0)
}

func TestStatsContextGitHubAndGitLab(t *testing.T) {
	checkContext(t, "repository,run-url", ghEnv, ctxWant{"acme/payments", "https://github.com/acme/payments/actions/runs/42", ""}, 0)
	gl := map[string]string{"CI_PROJECT_PATH": "acme/gl", "CI_JOB_URL": "https://gitlab.com/acme/gl/-/jobs/7?x=1#f", "GITHUB_REPOSITORY": "", "GITHUB_RUN_ID": "9"}
	checkContext(t, "repository,run-url", gl, ctxWant{"acme/gl", "https://gitlab.com/acme/gl/-/jobs/7", ""}, 0)
	checkContext(t, "repository,run-url,environment", nil, ctxWant{}, 3)
}

func TestStatsContextExplicitValues(t *testing.T) {
	checkContext(t, "repository=acme/x,run-url=http://ci.example/r/1?token=s,environment=prod", ghEnv,
		ctxWant{"acme/x", "http://ci.example/r/1", "prod"}, 0)
	checkContext(t, "repository=,environment=", ghEnv, ctxWant{}, 2)
	checkContext(t, "actor,actor=alice", ghEnv, ctxWant{}, 2)
}

func TestStatsContextRejectsUserinfo(t *testing.T) {
	checkContext(t, "run-url=https://u:p@x/y", ghEnv, ctxWant{}, 1)
	checkContext(t, "run-url=ftp://x/y", ghEnv, ctxWant{}, 1)
	checkContext(t, "run-url=https:///nohost", ghEnv, ctxWant{}, 1)
	checkContext(t, "run-url=%zz", ghEnv, ctxWant{}, 1)
}

func TestRunRecordBadContextNeverFails(t *testing.T) {
	statsOff(t)
	t.Setenv("GITHUB_ACTOR", "alice")
	t.Setenv("GITHUB_REPOSITORY", "acme/payments")
	file := filepath.Join(t.TempDir(), "run.json")
	old, new := diffDirs(t, true)
	baseOut, _, base := runRoot(t, defaultDeps(), nil, "diff", old, new)
	out, stderr, err := runRoot(t, defaultDeps(), nil, "diff", old, new, "--stats-file", file, "--stats-context", "actor,repository")
	data, rerr := os.ReadFile(file)
	if !sameErr(base, err) || out != baseOut || rerr != nil {
		t.Fatalf("err %v (want %v), read %v, stdout\n%s", err, base, rerr, out)
	}
	if !strings.Contains(stderr, `pacto: warning: run record context: unknown context "actor"`) ||
		bytes.Contains(data, []byte("actor")) || bytes.Contains(data, []byte("alice")) ||
		!bytes.Contains(data, []byte(`"repository":"acme/payments"`)) || bytes.Contains(data, []byte("ciRunUrl")) {
		t.Fatalf("stderr %s\nrecord %s", stderr, data)
	}
}
