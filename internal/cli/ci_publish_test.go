package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

const (
	pubIdentity = "keyless (https://github.com/acme/shop/.github/workflows/publish.yml@refs/heads/main)"
	pubDigest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// pubRows are a run where ledger published a BREAKING 3.0.0, reports was
// blocked by it and payments was unchanged.
func pubRows() []ci.Row {
	return []ci.Row{
		{Service: "ledger", Dir: "ledger", Changed: "yes", Baseline: "2.3.0", Next: "3.0.0", To: "3.0.0", Classification: "BREAKING", Tool: "ko",
			Identity: pubIdentity, Published: "ghcr.io/acme/ledger:3.0.0@" + pubDigest, State: ci.StatePublished, Attempts: 1},
		{Service: "reports", Dir: "reports", Changed: "yes", Next: "1.0.0", State: ci.StateBlocked, Reason: "depends on ledger 3.0.0, which is BREAKING for it"},
		{Service: "payments", Dir: "payments", Changed: "no", State: ci.StateUnchanged},
	}
}

// ciPubDeps answers pacto ci --publish with rows, handing each to Record as
// CIPublish does, and keeps the options it was given.
func ciPubDeps(rows []ci.Row, refused string, got *app.CIPublishOptions) deps {
	d := defaultDeps()
	d.ciPublish = func(_ *app.Service, _ context.Context, opts app.CIPublishOptions) (*ci.Plan, error) {
		if got != nil {
			*got = opts
		}
		p := &ci.Plan{Registry: ci.Value{Value: "ghcr.io/acme", Source: "--registry"}, Rows: rows, Refused: refused}
		for _, r := range rows {
			var res *publish.Result
			if r.State == ci.StatePublished {
				res = &publish.Result{Plan: publish.Plan{Service: r.Service, Version: r.To, Baseline: r.Baseline}, Actions: []string{"pushed"},
					Digests: map[string]string{publish.BundleItem: pubDigest}}
				for _, img := range r.Images {
					repo, digest, _ := strings.Cut(img, "@")
					res.Images = append(res.Images, publish.ImageRecord{Name: "image", Repository: repo, Digest: digest, Source: publish.SourceBuilt})
				}
			}
			opts.Record(r, res, nil)
		}
		p.SettlePublish(".")
		return p, nil
	}
	return d
}

func TestCIPublishOutputTable(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	out, stderr, err := runRoot(t, ciPubDeps(pubRows(), "", nil), nil, "ci", t.TempDir(), "--publish", "--registry", "ghcr.io/acme")
	want := `CI: none
Registry: ghcr.io/acme (from --registry)
Base: none

ledger    2.3.0 -> 3.0.0  built with ko  signed ` + pubIdentity + `  published ghcr.io/acme/ledger:3.0.0@` + pubDigest + `
reports   blocked: depends on ledger 3.0.0, which is BREAKING for it
payments  unchanged
`
	if ciExit(err) != 1 || out != want || stderr != "not published: 1 of 3 services\n" {
		t.Fatalf("exit %d stderr %q stdout:\n%s", ciExit(err), stderr, out)
	}
}

func TestCIPublishJSON(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	out, _, err := runRoot(t, ciPubDeps(pubRows()[:1], "", nil), nil, "ci", t.TempDir(), "--publish", "--output-format", "json")
	var p ci.Plan
	if jerr := json.Unmarshal([]byte(out), &p); jerr != nil || err != nil {
		t.Fatalf("%v %v:\n%s", jerr, err, out)
	}
	r := p.Rows[0]
	if p.Exit != 0 || p.Gate != ci.StatePublished || r.State != ci.StatePublished || r.Identity != pubIdentity || r.To != "3.0.0" || r.Attempts != 1 ||
		!strings.Contains(out, `"published": "ghcr.io/acme/ledger:3.0.0@`) {
		t.Fatalf("%s", out)
	}
}

func TestCIPublishExitCodes(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	rows := pubRows()
	for name, c := range map[string]struct {
		rows    []ci.Row
		refused string
		want    int
	}{
		"published": {rows: rows[:1], want: 0},
		"unchanged": {rows: rows[2:], want: 0},
		"failed":    {rows: []ci.Row{{Service: "a", Changed: "yes", State: ci.StateFailed, Reason: "boom"}}, want: 1},
		"blocked":   {rows: rows[:2], want: 1},
		"refused":   {rows: []ci.Row{{Service: "a", Changed: "yes", State: ci.StateRefused, Reason: app.RefusedPullRequest}}, refused: app.RefusedPullRequest, want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := runRoot(t, ciPubDeps(c.rows, c.refused, nil), nil, "ci", t.TempDir(), "--publish")
			if got := ciExit(err); got != c.want {
				t.Fatalf("exit %d, want %d:\n%s", got, c.want, out)
			}
			if c.refused != "" && !strings.Contains(out, "refused: "+app.RefusedPullRequest+"\n") {
				t.Fatalf("stdout:\n%s", out)
			}
		})
	}
}

func TestCIPublishImageFlag(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	var got app.CIPublishOptions
	_, _, err := runRoot(t, ciPubDeps(pubRows()[:1], "", &got), nil, "ci", t.TempDir(), "--publish", "--image", "ledger=r.io/l@"+pubDigest, "--image", "r.io/x@"+pubDigest,
		"--sign-key", "/k/cosign.key", "--no-transparency-log")
	if err != nil || got.Images["ledger"] != "r.io/l@"+pubDigest || got.Images[""] != "r.io/x@"+pubDigest || got.SignKey != "/k/cosign.key" || !got.NoTransparencyLog {
		t.Fatalf("%v %+v", err, got)
	}
	for args, want := range map[string]string{
		"--publish --image ledger=r.io/l:1":  "--image ledger=r.io/l:1 needs a digest: a tag can move",
		"--publish --no-transparency-log":    "--no-transparency-log needs --sign-key: keyless signing needs the transparency log",
		"--image r.io/x@" + pubDigest:        "--image needs --publish",
		"--sign-key k":                       "--sign-key needs --publish",
		"--no-transparency-log --sign-key k": "--sign-key needs --publish",
	} {
		_, _, err := runRoot(t, ciPubDeps(nil, "", nil), nil, append([]string{"ci", t.TempDir()}, strings.Fields(args)...)...)
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
}

func TestCIPublishRunRecords(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	var calls []statsCall
	file := filepath.Join(t.TempDir(), "rec.json")
	_, stderr, err := runRoot(t, recDeps(ciPubDeps(pubRows(), "", nil), &calls, nil), nil, "ci", t.TempDir(), "--publish", "--stats-file", file, "--stats-registry", "r.io/stats")
	if ciExit(err) != 1 || len(calls) != 2 || strings.Contains(stderr, "plan mode") {
		t.Fatalf("exit %d calls %+v stderr %s", ciExit(err), calls, stderr)
	}
	ledger, reports := readRecord(t, strings.TrimSuffix(file, ".json")+".ledger.json"), readRecord(t, strings.TrimSuffix(file, ".json")+".reports.json")
	if ledger.Outcome != runrecord.OutcomePublished || ledger.BundleDigest != pubDigest || ledger.NewRevision != "3.0.0" {
		t.Fatalf("ledger %+v", ledger)
	}
	if reports.Outcome != runrecord.OutcomeRefused || *reports.Gate != (runrecord.Gate{Verdict: runrecord.VerdictRefused, Reason: "dependency"}) {
		t.Fatalf("reports %+v", reports)
	}
	if _, err := os.Stat(strings.TrimSuffix(file, ".json") + ".payments.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an unchanged service leaves no record: %v", err)
	}
}

func readRecord(t *testing.T, path string) runrecord.Record {
	t.Helper()
	r, err := runrecord.Parse([]byte(readFile(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRowRecord(t *testing.T) {
	for state, want := range map[string]string{ci.StateRefused: "pull-request", ci.StateFailed: ""} {
		r := rowRecord(ci.Row{Service: "a", State: state})
		if state == ci.StateFailed && (r.Outcome != runrecord.OutcomeFailed || r.Gate != nil) {
			t.Fatalf("%s: %+v", state, r)
		}
		if want != "" && (r.Outcome != runrecord.OutcomeRefused || r.Gate.Reason != want) {
			t.Fatalf("%s: %+v", state, r)
		}
	}
	if r := rowRecord(ci.Row{Service: "a", State: ci.StateRefused, Derived: true, Baseline: "1.3.0", Next: "1.4.0", Classification: "NON_BREAKING"}); r.NewRevision != "" || r.OldRevision != "" || r.Classification != "" {
		t.Fatalf("an unverified derived version is recorded: %+v", r)
	}
	if rowRecord(ci.Row{Service: "a", State: ci.StateUnchanged}) != nil {
		t.Fatal("an unchanged service leaves no record")
	}
}

func TestCIPublishJobSummary(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	_, _, _ = runRoot(t, ciPubDeps(pubRows(), "", nil), nil, "ci", t.TempDir(), "--publish")
	got := readFile(t, summary)
	for _, want := range []string{"### pacto ci publish\n", "- `ledger  2.3.0 -> 3.0.0  built with ko  signed " + pubIdentity, "- `reports  blocked: depends on ledger 3.0.0", "- `payments  unchanged`\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestCIPublishRowImages: the images a published bundle deploys are on its
// row in JSON, under --explain and in its run record.
func TestCIPublishRowImages(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	rows := pubRows()[:1]
	rows[0].Images = []string{"ghcr.io/acme/ledger@" + pubDigest}
	out, _, err := runRoot(t, ciPubDeps(rows, "", nil), nil, "ci", t.TempDir(), "--publish", "--output-format", "json")
	if err != nil || !strings.Contains(out, `"images": [`+"\n"+`        "ghcr.io/acme/ledger@`+pubDigest+`"`) {
		t.Fatalf("%v:\n%s", err, out)
	}
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"ledger/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n"})
	out, _, err = runRoot(t, ciPubDeps(rows, "", nil), nil, "ci", root, "--publish", "--explain", "ledger")
	if !strings.Contains(out, "images: ghcr.io/acme/ledger@"+pubDigest+"\n") {
		t.Fatalf("%v:\n%s", err, out)
	}
	file := filepath.Join(t.TempDir(), "rec.json")
	_, _, _ = runRoot(t, ciPubDeps(rows, "", nil), nil, "ci", t.TempDir(), "--publish", "--stats-file", file)
	if r := readRecord(t, strings.TrimSuffix(file, ".json")+".ledger.json"); len(r.Images) != 1 || r.Images[0] != rows[0].Images[0] {
		t.Fatalf("record %+v", r)
	}
}

// TestCIPublishExplainKeepsPublishExit: --explain under --publish exits with
// the publish outcome, never the plan-mode impact gate of the row.
func TestCIPublishExplainKeepsPublishExit(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"ledger/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n"})
	failed := []ci.Row{{Service: "ledger", Dir: "ledger", Changed: "yes", State: ci.StateFailed, Reason: "build failed",
		Impact: ci.Impact{Gate: "clear"}}}
	out, _, err := runRoot(t, ciPubDeps(failed, "", nil), nil, "ci", root, "--publish", "--explain", "ledger", "--output-format", "json")
	var p ci.Plan
	if jerr := json.Unmarshal([]byte(out), &p); jerr != nil || ciExit(err) != 1 || p.Exit != 1 || p.Gate != ci.StateFailed {
		t.Fatalf("exit %d %v:\n%s", ciExit(err), jerr, out)
	}
	published := pubRows()[:1]
	published[0].Impact.Gate = "unverified"
	if _, _, err := runRoot(t, ciPubDeps(published, "", nil), nil, "ci", root, "--publish", "--explain", "ledger"); err != nil {
		t.Fatalf("a published row with an unverified impact exits 0: %v", err)
	}
}

// TestCIPublishRecordFilesNeverCollide: a service name another row took gets
// a record file of its own.
func TestCIPublishRecordFilesNeverCollide(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	rows := []ci.Row{
		{Service: "api", Dir: "one", Changed: "yes", State: ci.StateFailed, Reason: "boom"},
		{Service: "api", Dir: "svc/two", Changed: "yes", State: ci.StateFailed, Reason: "boom"},
	}
	file := filepath.Join(t.TempDir(), "rec.json")
	_, _, _ = runRoot(t, ciPubDeps(rows, "", nil), nil, "ci", t.TempDir(), "--publish", "--stats-file", file)
	stem := strings.TrimSuffix(file, ".json")
	for _, f := range []string{".api.json", ".dir-svc_two.json"} {
		readRecord(t, stem+f)
	}
}

func TestCIPublishImageFlagTwice(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	_, _, err := runRoot(t, ciPubDeps(nil, "", nil), nil, "ci", t.TempDir(), "--publish", "--image", "a=r.io/a@"+pubDigest, "--image", "a=r.io/b@"+pubDigest)
	if err == nil || !strings.Contains(err.Error(), "name one service") {
		t.Fatal(err)
	}
}
