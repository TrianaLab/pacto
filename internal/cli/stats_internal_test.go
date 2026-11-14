package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// statsCall is one publishRunRecord call a fake saw.
type statsCall struct {
	repo string
	rec  runrecord.Record
}

// statsDeps records every publish and answers each with err.
func statsDeps(calls *[]statsCall, err error) deps {
	return deps{publishRunRecord: func(_ context.Context, repo string, r runrecord.Record) (string, error) {
		*calls = append(*calls, statsCall{repo, r})
		return repo + "@sha256:abc", err
	}}
}

func runStatsPublish(t *testing.T, d deps, args ...string) (string, error) {
	t.Helper()
	cmd := newStatsCommand(d)
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs(append([]string{"publish"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// fixture copies a pkg/runrecord golden record into a temp file.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "pkg", "runrecord", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, name, data)
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatsPublishFiles(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "")
	var calls []statsCall
	a, b := fixture(t, "impact-blocked.json"), fixture(t, "publish-refused.json")
	if _, err := runStatsPublish(t, statsDeps(&calls, nil), a, b, "--registry", "reg/acme/stats"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].rec.Kind != runrecord.KindImpact || calls[1].rec.Kind != runrecord.KindPublish || calls[0].repo != "reg/acme/stats" {
		t.Fatalf("calls = %+v", calls)
	}
	data, _ := os.ReadFile(a)
	want, _ := runrecord.Parse(data)
	got, _ := runrecord.Encode(calls[0].rec)
	if string(got) != strings.TrimSpace(string(data)) || calls[0].rec.ID != want.ID {
		t.Fatalf("the published record differs from the file:\n%s", got)
	}
}

func TestStatsPublishNoRegistry(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "")
	var calls []statsCall
	_, err := runStatsPublish(t, statsDeps(&calls, nil), fixture(t, "impact-blocked.json"))
	if err == nil || err.Error() != "no stats registry: pass --registry or set PACTO_STATS_REGISTRY" || len(calls) != 0 {
		t.Fatalf("err = %v, %d calls", err, len(calls))
	}
}

func TestStatsPublishEnvRegistry(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "env/acme/stats")
	var calls []statsCall
	if _, err := runStatsPublish(t, statsDeps(&calls, nil), fixture(t, "impact-blocked.json")); err != nil || calls[0].repo != "env/acme/stats" {
		t.Fatalf("err = %v, calls %+v", err, calls)
	}
}

func TestStatsPublishPartialFailure(t *testing.T) {
	t.Setenv("PACTO_STATS_REGISTRY", "reg/acme/stats")
	var calls []statsCall
	good := fixture(t, "impact-blocked.json")
	malformed := writeTemp(t, "malformed.json", []byte("{"))
	oversized := writeTemp(t, "oversized.json", make([]byte, runrecord.MaxRecordBytes+10))
	missing := filepath.Join(t.TempDir(), "missing.json")
	dir := t.TempDir()
	_, err := runStatsPublish(t, statsDeps(&calls, nil), malformed, oversized, missing, dir, good)
	if err == nil || len(calls) != 1 || calls[0].rec.Kind != runrecord.KindImpact {
		t.Fatalf("err = %v, calls %+v", err, calls)
	}
	if !strings.HasPrefix(err.Error(), "4 of 5 run records not published: ") {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{malformed, oversized, missing, dir} {
		if !strings.Contains(err.Error(), bad+": ") {
			t.Errorf("the error does not name %s: %v", bad, err)
		}
	}
	if strings.Contains(err.Error(), good) {
		t.Errorf("the error names the published file: %v", err)
	}
}

func TestStatsPublishDenied(t *testing.T) {
	var calls []statsCall
	file := fixture(t, "impact-blocked.json")
	_, err := runStatsPublish(t, statsDeps(&calls, errors.New("response status code 403: denied")), file, "--registry", "reg/acme/stats")
	if err == nil || !strings.Contains(err.Error(), file+": response status code 403: denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestStatsPublishRejectsForgedID(t *testing.T) {
	var calls []statsCall
	data, _ := os.ReadFile(filepath.Join("..", "..", "pkg", "runrecord", "testdata", "impact-blocked.json"))
	r, _ := runrecord.Parse(data)
	r.ID = runrecord.ID(r.Kind, "orders", r.OldRevision, r.NewRevision)
	forged, _ := runrecord.Encode(r)
	file := writeTemp(t, "forged.json", forged)
	_, err := runStatsPublish(t, statsDeps(&calls, nil), file, "--registry", "reg/acme/stats")
	if err == nil || len(calls) != 0 || !strings.Contains(err.Error(), file+": id does not match its kind, service and revisions") {
		t.Fatalf("err = %v, %d calls", err, len(calls))
	}
}

func TestStatsPublishSuccessLine(t *testing.T) {
	var calls []statsCall
	file := fixture(t, "impact-blocked.json")
	out, err := runStatsPublish(t, statsDeps(&calls, nil), file, "--registry", "reg/acme/stats")
	want := "published " + file + " (impact payments " + calls[0].rec.ID + ") to reg/acme/stats@sha256:abc (unsigned statistics)\n"
	if err != nil || out != want {
		t.Fatalf("out = %q, want %q (%v)", out, want, err)
	}
}
