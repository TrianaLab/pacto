package cli

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/oci"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// runningFleet is a local fleet where ledger depends on payments with rng,
// the new payments folder at 2.0.0 and a target state running each of
// running.
func runningFleet(t *testing.T, rng string, running ...string) (root, newDir, targets string) {
	t.Helper()
	root, newDir = t.TempDir(), t.TempDir()
	writeFiles(t, root, map[string]string{
		"payments/pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: payments, version: 1.0.0}\n",
		"ledger/pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: ledger, version: 1.0.0}\ndependencies:\n  - name: payments\n    ref: oci://ghcr.io/x/payments\n    required: true\n" +
			"    compatibility: \"" + rng + "\"\n",
	})
	writeFiles(t, newDir, map[string]string{"pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: payments, version: 2.0.0}\n"})
	return root, newDir, targetState(t, running...)
}

// runningExit runs pacto impact --running and returns its exit code and stdout.
func runningExit(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, err := runImpactInternal(t, defaultDeps(), "text", append([]string{"--running"}, args...)...)
	if code := ciExit(err); code != -1 {
		return code, out
	}
	return 1, out // a plain error exits 1
}

func TestImpactRunningExitCodes(t *testing.T) {
	root, newDir, targets := runningFleet(t, "^2", "ledger")
	if code, out := runningExit(t, newDir, "--local", root, "--target-state", targets); code != 0 || !strings.Contains(out, "Running: none on a known target (first version)") {
		t.Fatalf("clear: exit %d\n%s", code, out)
	}
	root, newDir, targets = runningFleet(t, "^1", "ledger")
	if code, out := runningExit(t, newDir, "--local", root, "--target-state", targets); code != 1 || !strings.Contains(out, "Release gate: blocked") {
		t.Fatalf("blocked: exit %d\n%s", code, out)
	}
	// payments runs on a target no revision is linked to: never clear.
	root, newDir, targets = runningFleet(t, "^2", "ledger", "payments")
	if code, out := runningExit(t, newDir, "--local", root, "--target-state", targets); code != 2 || !strings.Contains(out, "Not read: ") {
		t.Fatalf("unverified: exit %d\n%s", code, out)
	}
}

func TestImpactRunningAmbiguousExitsOne(t *testing.T) {
	e := newCIEnv(t)
	e.write(t, map[string]string{"payments/pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: payments, version: 1.0.0}\n"})
	e.publish(t, "payments", "1.0.0")
	newDir := t.TempDir()
	writeFiles(t, newDir, map[string]string{"pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: payments, version: 2.0.0}\n"})
	// payments is in the local folder's domain and in the registry's.
	root := newRootCommand(app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)), nil), VersionInfo{Version: "dev"}, defaultDeps())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := Execute(t.Context(), root, []string{"impact", "--running", newDir, "--local", e.root, "--oci", "oci://" + e.host + "/acme/payments:1.0.0", "--index", "none"})
	var amb *fleet.AmbiguousError
	if ciExit(err) != -1 || !errors.As(err, &amb) || !strings.Contains(err.Error(), e.host) {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestImpactRunningArgs(t *testing.T) {
	for _, c := range []struct {
		args, flags []string
		want        string
	}{
		{[]string{"new"}, nil, ""},
		{[]string{"old", "new"}, nil, "--running takes one argument, <new>, received 2 arguments"},
		{[]string{}, []string{"--unavailable", "x"}, "takes no --unavailable"},
		{[]string{"new"}, []string{"--old-set", "a=b"}, "takes no --old-set"},
	} {
		cmd := newImpactCommand(newTestService(t), nil, defaultDeps(), nil)
		if err := cmd.ParseFlags(append([]string{"--running"}, c.flags...)); err != nil {
			t.Fatal(err)
		}
		err := impactArgs(cmd, c.args)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%v %v: %v, want %q", c.args, c.flags, err, c.want)
		}
	}
}

func TestImpactRunningPrintsRunningLine(t *testing.T) {
	for _, c := range []struct {
		r    *impact.RunningBaseline
		want string
	}{
		{&impact.RunningBaseline{Refs: []string{"oci://r/p:1"}, Judged: "oci://r/p:1"}, "Running: 1 revision (judged against oci://r/p:1)\n"},
		{&impact.RunningBaseline{Refs: []string{"a", "b"}, Judged: "a", Unread: []string{"x", "y"}}, "Running: 2 revisions (judged against a)\nNot read: x; y\n"},
		{&impact.RunningBaseline{Refs: []string{}, FirstVersion: true}, "Running: none on a known target (first version)\n"},
		{&impact.RunningBaseline{Refs: []string{}, FirstVersion: true, Unread: []string{"x"}}, "Running: none Pacto could read (judged as a first version)\nNot read: x\n"},
		{&impact.RunningBaseline{Refs: []string{"a"}, Judged: "a", Excluded: []string{"n"}}, "Running: 1 revision (judged against a); the new revision also runs and is not its own baseline\n"},
		{&impact.RunningBaseline{Refs: []string{}, FirstVersion: true, Excluded: []string{"n", "m"}},
			"Running: only the new revision, which is not its own baseline (judged as a first version)\n"},
		{&impact.RunningBaseline{Refs: []string{}, FirstVersion: true, Excluded: []string{"n"}, Unread: []string{"x"}},
			"Running: none Pacto could read (judged as a first version); the new revision also runs and is not its own baseline\nNot read: x\n"},
	} {
		var buf bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		if err := printImpactResult(cmd, &impact.Result{Service: "p", Running: c.r}, "text"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("missing %q in:\n%s", c.want, buf.String())
		}
	}
}

// TestImpactRunningMissingTrustFileFails: the Kargo template points
// $PACTO_DELIVERY_TRUST at an optional Secret's file; when the Secret is
// missing, the run fails (exit 1) and names the trust config.
func TestImpactRunningMissingTrustFileFails(t *testing.T) {
	repo, _ := publishedTargets(t)
	host := strings.TrimSuffix(strings.TrimPrefix(repo, "oci://"), "/acme/pacto-targets")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: payments, version: 1.0.0}\n"})
	svc := app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(host)), nil)
	if _, err := svc.Push(t.Context(), app.PushOptions{Ref: "oci://" + host + "/acme/payments:1.0.0", Path: dir}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(app.TrustEnv, filepath.Join(t.TempDir(), "trust.yaml"))
	root := newRootCommand(svc, VersionInfo{Version: "dev"}, defaultDeps())
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := Execute(t.Context(), root, []string{"impact", "--running", "oci://" + host + "/acme/payments:1.0.0", "--local", ""})
	if err == nil || ciExit(err) != -1 || !strings.Contains(err.Error(), "reading the delivery trust config") {
		t.Fatalf("err = %v", err)
	}
}
