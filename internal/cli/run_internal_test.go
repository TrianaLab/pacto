package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/ebpf"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/evalsink"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// runContract declares the interface api and the dependency db.
func runContract() *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "svc", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "x"}},
		Dependencies: []contract.Dependency{{Name: "db", Ref: "oci://r/db", Required: true}},
	}}
}

// fakeSession records what the run did with its cgroup.
type fakeSession struct {
	parent, name string
	prepared     bool
	unstartable  bool
	closeErr     error
	closed       bool
}

func (f *fakeSession) Prepare(c *exec.Cmd) {
	f.prepared = true
	if f.unstartable {
		c.Dir = "/no/such/dir" // as a cgroup the kernel will not start the command in
	}
}
func (f *fakeSession) Close() error { f.closed = true; return f.closeErr }

// statusCollector reports a fixed status.
type statusCollector struct {
	nopCollector
	status runtimewatch.CollectorStatus
}

func (s statusCollector) Status() runtimewatch.CollectorStatus { return s.status }

// runStubs are the seams of one test, defaulting to a host where eBPF fails to load.
type runStubs struct {
	collector  runtimewatch.Collector
	collectErr error
	session    *fakeSession
	sessionErr error
	subreaper  error
	agent      func(ctx context.Context, cfg runtimewatch.Config)
	signals    chan<- os.Signal
	signalsSet chan struct{}
}

func stubRun(t *testing.T, s *runStubs) deps {
	t.Helper()
	d := defaultDeps()
	if s.collector == nil {
		s.collector = statusCollector{status: runtimewatch.CollectorStatus{Failed: true, Reason: "LoadFailed", Message: "no BPF here"}}
	}
	d.newCollector = func(uint32) (runtimewatch.Collector, error) { return s.collector, s.collectErr }
	d.runAgent = func(ctx context.Context, cfg runtimewatch.Config) {
		if s.agent != nil {
			s.agent(ctx, cfg)
			return
		}
		<-ctx.Done()
	}
	d.newSession = func(parent, name string) (ebpf.Session, error) {
		if s.sessionErr != nil {
			return nil, s.sessionErr
		}
		if s.session == nil {
			s.session = &fakeSession{}
		}
		s.session.parent, s.session.name = parent, name
		return s.session, nil
	}
	d.setSubreaper = func() error { return s.subreaper }
	s.signalsSet = make(chan struct{}, 1)
	d.notifySignals = func(c chan<- os.Signal, _ ...os.Signal) {
		s.signals = c
		s.signalsSet <- struct{}{}
	}
	d.resolveBundle = func(_ *app.Service, _ context.Context, ref string) (*contract.Bundle, error) {
		if ref == "missing" {
			return nil, errors.New("no such bundle")
		}
		return runContract(), nil
	}
	d.attachTimeout = 5 * time.Second
	d.pollInterval = 100 * time.Millisecond
	return d
}

// runResult is what one `pacto run` printed and returned.
type runResult struct {
	stdout, stderr string
	err            error
	rounds         []agentRound
}

// final is the last round, which the run marks final.
func (r runResult) final(t *testing.T) evalsink.Result {
	t.Helper()
	if len(r.rounds) == 0 || !r.rounds[len(r.rounds)-1].Final || len(r.rounds[len(r.rounds)-1].Targets) != 1 {
		t.Fatalf("no final round: %+v\nstdout %s\nstderr %s", r.rounds, r.stdout, r.stderr)
	}
	return r.rounds[len(r.rounds)-1].Targets[0]
}

func runRun(t *testing.T, d deps, args ...string) runResult {
	t.Helper()
	v := viper.New()
	v.Set(outputFormatKey, "json")
	cmd := newRunCommand(&app.Service{}, v, "v9", d)
	// As under the root command, so stdout and stderr carry only what the run itself writes.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{"--interval=1s"}, args...))
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := runResult{err: cmd.Execute()}
	r.stdout, r.stderr = out.String(), errOut.String()
	// The rounds are on stderr, among the notices: stdout is the command's alone.
	sc := bufio.NewScanner(strings.NewReader(r.stderr))
	for sc.Scan() {
		var round agentRound
		if json.Unmarshal(sc.Bytes(), &round) == nil && !round.EvaluatedAt.IsZero() {
			r.rounds = append(r.rounds, round)
		}
	}
	return r
}

func exitOf(err error) int {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	return -1
}

// codes maps each finding's code to the collector of its first evidence reference.
func codes(r evalsink.Result) map[finding.Code]string {
	out := map[finding.Code]string{}
	for _, f := range r.Findings {
		out[f.Code] = ""
		if len(f.EvidenceRefs) > 0 {
			out[f.Code] = f.EvidenceRefs[0].Collector
		}
	}
	return out
}

func TestRunCommand_Errors(t *testing.T) {
	d := stubRun(t, &runStubs{})
	empty := t.TempDir()
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no contract":      {args: []string{"--", "true"}, want: "--contract is required"},
		"no command":       {args: []string{"--contract=c"}, want: "requires at least 1 arg"},
		"bad fail-on":      {args: []string{"--contract=c", "--fail-on=warning", "--", "true"}, want: "--fail-on must be one of"},
		"bad interval":     {args: []string{"--contract=c", "--interval=1500ms", "--", "true"}, want: "--interval"},
		"bad bind":         {args: []string{"--contract=c", "--bind=api", "--", "true"}, want: `--bind "api" is not interface=port`},
		"bad bind port":    {args: []string{"--contract=c", "--bind=api=0", "--", "true"}, want: "out of range"},
		"bad dep":          {args: []string{"--contract=c", "--dep=db", "--", "true"}, want: `--dep "db" is not dependency=host:port`},
		"bad dep address":  {args: []string{"--contract=c", "--dep=db=nowhere", "--", "true"}, want: `dependency "db"`},
		"unknown bind":     {args: []string{"--contract=c", "--bind=web=80", "--", "true"}, want: `declares no interface "web"`},
		"unknown dep":      {args: []string{"--contract=c", "--dep=cache=h:1", "--", "true"}, want: `declares no dependency "cache"`},
		"missing contract": {args: []string{"--contract=missing", "--", "true"}, want: "no such bundle"},
		"unpinned evidence": {args: []string{"--contract=c", "--evidence-url=http://e", "--", "true"},
			want: "not pinned"},
		"evidence flags": {args: []string{"--contract=oci://r/a@sha256:" + strings.Repeat("a", 64), "--evidence-url=http://e", "--", "true"},
			want: "--evidence-key"},
		"no such command": {args: []string{"--contract=c", "--proc-root=" + empty, "--", filepath.Join(empty, "nope")}, want: "start "},
	} {
		t.Run(name, func(t *testing.T) {
			if r := runRun(t, d, tc.args...); r.err == nil || !strings.Contains(r.err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", r.err, tc.want)
			}
		})
	}
	v := viper.New()
	v.Set(outputFormatKey, "yaml")
	cmd := newRunCommand(&app.Service{}, v, "v9", d)
	cmd.SetArgs([]string{"--contract=c", "--", "true"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("bad format: %v", err)
	}
}

// Without eBPF and without /proc the command still runs and its exit code is the run's; every runtime
// assertion is unsupported.
func TestRunCommand_Unsupported(t *testing.T) {
	d := stubRun(t, &runStubs{collectErr: errors.New("kernel collection is unsupported")})
	r := runRun(t, d, "--contract=c", "--bind=api=8080", "--proc-root="+t.TempDir(), "--", "sh", "-c", "echo hello; exit 3")
	if exitOf(r.err) != 3 || r.err.Error() != "exit status 3" || r.stdout != "hello\n" || len(r.rounds) == 0 ||
		!strings.Contains(r.stderr, "runtime evidence is unsupported here (eBPF: kernel collection is unsupported") {
		t.Fatalf("err %v\nstdout %s\nstderr %s", r.err, r.stdout, r.stderr)
	}
	res := r.final(t)
	if r.rounds[len(r.rounds)-1].Mode != modeUnsupported || codes(res)[finding.CodeObservationUnsupported] != runCollector {
		t.Fatalf("final = %+v", res)
	}
}

// fakeProc writes a /proc with one child of this process holding the given sockets: a tcp table line per
// socket, its inode the line's.
func fakeProc(t *testing.T, tcp ...string) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, data string) {
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(name, target string) {
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	write("self/stat", "1 (pacto) S 0")
	write("self/cgroup", "0::/user.slice\n")
	write("999999/stat", fmt.Sprintf("999999 (srv) S %d 1 1 0 -1 0", os.Getpid()))
	_ = os.MkdirAll(filepath.Join(root, "999999/fd"), 0o755)
	link("999999/ns/net", "net:[1]")
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	for i, line := range tcp {
		ino := 100 + i
		table += fmt.Sprintf("   %d: %s 0:0 0:0 0 0 0 %d 1\n", i, line, ino)
		link("999999/fd/"+strconv.Itoa(ino), fmt.Sprintf("socket:[%d]", ino))
	}
	write("999999/net/tcp", table)
	write("999999/net/tcp6", "")
	return root
}

const (
	listen8080  = "00000000:1F90 00000000:0000 0A"
	dialedDB    = "0100007F:A000 0100007F:1538 01" // 127.0.0.1:40960 -> 127.0.0.1:5432
	unrelatedOK = "0100007F:A001 0500000A:0050 01"
)

func TestRunCommand_Unprivileged(t *testing.T) {
	t.Run("served", func(t *testing.T) {
		s := &runStubs{subreaper: errors.New("no prctl")}
		d := stubRun(t, s)
		r := runRun(t, d, "--contract=c", "--bind=api=8080", "--dep=db=127.0.0.1:5432", "--fail-on=noncompliant",
			"--proc-root="+fakeProc(t, listen8080, dialedDB, unrelatedOK), "--", "sleep", "0.3")
		if r.err != nil || !strings.Contains(r.stderr, "unprivileged mode (eBPF: no BPF here)") ||
			!strings.Contains(r.stderr, "never unreachable") || !strings.Contains(r.stderr, "daemonizes leaves the watched tree (no prctl)") {
			t.Fatalf("err %v\nstderr %s", r.err, r.stderr)
		}
		if res := r.final(t); res.Status != finding.StatusCompliant || r.rounds[len(r.rounds)-1].Mode != modeUnprivileged {
			t.Fatalf("final = %+v", res)
		}
	})
	t.Run("deaf", func(t *testing.T) {
		d := stubRun(t, &runStubs{})
		r := runRun(t, d, "--contract=c", "--bind=api=8080", "--dep=db=127.0.0.1:5432", "--stabilization-window=0s",
			"--fail-on=noncompliant", "--proc-root="+fakeProc(t), "--", "sleep", "1.2")
		res := r.final(t)
		got := codes(res)
		// The interface's absence is confirmed from the complete listener view; the dependency's silence is
		// not a failure, and this mode never sees one.
		if exitOf(r.err) != 1 || got[finding.CodeInterfaceAbsent] != runCollector || res.Status != finding.StatusNonCompliant ||
			!strings.Contains(r.stderr, "fails --fail-on noncompliant") {
			t.Fatalf("err %v, final %+v", r.err, res)
		}
		if _, ok := got[finding.CodeDependencyUnreachable]; ok {
			t.Fatalf("unprivileged mode confirmed a dependency failure: %+v", res.Findings)
		}
	})
	t.Run("silent dependency fails --fail-on unknown", func(t *testing.T) {
		d := stubRun(t, &runStubs{})
		r := runRun(t, d, "--contract=c", "--bind=api=8080", "--dep=db=127.0.0.1:5432", "--fail-on=unknown",
			"--proc-root="+fakeProc(t, listen8080), "--", "true")
		if res := r.final(t); exitOf(r.err) != 1 || res.Status != finding.StatusUnknown {
			t.Fatalf("err %v, final %+v", r.err, res)
		}
	})
}

func TestRunCommand_EBPF(t *testing.T) {
	var cfg runtimewatch.Config
	s := &runStubs{
		collector: statusCollector{status: runtimewatch.CollectorStatus{AttachedAt: time.Now()}},
		session:   &fakeSession{closeErr: errors.New("cgroup busy")},
		agent: func(ctx context.Context, c runtimewatch.Config) {
			cfg = c
			// Like the real agent it publishes on every tick: the gate opens only after the attach
			// returns, so a snapshot published before that is dropped.
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				now := time.Now()
				_ = c.Sink.Publish(ctx, runtimefacts.Snapshot{PublishIntervalSeconds: 1, LastPublished: now,
					Collection: runtimefacts.Collection{State: runtimefacts.StateRunning},
					Workloads: []runtimefacts.Workload{{
						Identity:   runtimefacts.Identity{Kind: runtimefacts.KindSession, ID: "c/pacto-run-1", Name: fmt.Sprintf("pacto-run-%d", os.Getpid())},
						BaselineAt: now.Add(time.Second), Listeners: []runtimefacts.Listener{{Port: 8080, Address: "0.0.0.0"}},
						Connections: []runtimefacts.Connection{{Address: "10.0.0.5", Port: 5432, Successes: 1, LastSuccessAt: now}},
					}}})
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		},
	}
	d := stubRun(t, s)
	r := runRun(t, d, "--contract=c", "--bind=api=8080", "--dep=db=10.0.0.5:5432", "--cgroup-root=/sys/fs/cgroup/x", "--fail-on=noncompliant", "--proc-root="+t.TempDir(),
		"--", "sleep", "0.3")
	name := fmt.Sprintf("pacto-run-%d", os.Getpid())
	if r.err != nil || !strings.Contains(r.stderr, "observing the command in cgroup "+name+" with eBPF (collector pacto-run+ebpf)") ||
		!strings.Contains(r.stderr, "pacto run: cgroup busy") {
		t.Fatalf("err %v\nstderr %s", r.err, r.stderr)
	}
	if !s.session.prepared || !s.session.closed || s.session.parent != "/sys/fs/cgroup/x" || s.session.name != name {
		t.Fatalf("session = %+v", s.session)
	}
	if res := r.final(t); res.Status != finding.StatusCompliant || r.rounds[len(r.rounds)-1].Mode != modeEBPF {
		t.Fatalf("final = %+v", res)
	}
	if cfg.Cgroups == nil || cfg.PublishInterval != time.Second || !cfg.Keep(runtimefacts.Identity{Kind: runtimefacts.KindSession, Name: name}) ||
		cfg.Keep(runtimefacts.Identity{Kind: runtimefacts.KindSession, Name: "pacto-run-0"}) || cfg.Keep(runtimefacts.Identity{Kind: runtimefacts.KindUnit, Name: name}) {
		t.Fatalf("agent config = %+v", cfg)
	}
}

// Every way the privileged mode can fail falls back to the unprivileged one and says why.
func TestRunCommand_Fallbacks(t *testing.T) {
	proc := fakeProc(t, listen8080)
	noV2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(noV2, "self"), 0o755)
	_ = os.WriteFile(filepath.Join(noV2, "self/stat"), []byte("1 (x) S 0"), 0o644)
	_ = os.WriteFile(filepath.Join(noV2, "self/cgroup"), []byte("1:cpu:/\n"), 0o644)
	noCgroup := t.TempDir()
	_ = os.MkdirAll(filepath.Join(noCgroup, "self"), 0o755)
	_ = os.WriteFile(filepath.Join(noCgroup, "self/stat"), []byte("1 (x) S 0"), 0o644)
	for name, tc := range map[string]struct {
		stubs *runStubs
		proc  string
		want  string
	}{
		"no cgroup v2": {proc: noV2, stubs: &runStubs{}, want: "eBPF: this process is in no cgroup v2"},
		"no cgroup":    {proc: noCgroup, stubs: &runStubs{}, want: "self/cgroup"},
		"no session":   {proc: proc, stubs: &runStubs{sessionErr: errors.New("read-only cgroupfs")}, want: "eBPF: read-only cgroupfs"},
		"load fails":   {proc: proc, stubs: &runStubs{}, want: "eBPF: no BPF here"},
		"never attaches": {proc: proc, stubs: &runStubs{collector: statusCollector{status: runtimewatch.CollectorStatus{Failed: true, Reason: ebpf.ReasonAttaching}}},
			want: "did not attach within 50ms"},
	} {
		t.Run(name, func(t *testing.T) {
			s := tc.stubs
			d := stubRun(t, s)
			d.attachTimeout = 50 * time.Millisecond
			r := runRun(t, d, "--contract=c", "--proc-root="+tc.proc, "--", "true")
			if r.err != nil || !strings.Contains(r.stderr, "unprivileged mode ("+strings.TrimPrefix(tc.want, "eBPF: ")) && !strings.Contains(r.stderr, tc.want) {
				t.Fatalf("err %v\nstderr %s", r.err, r.stderr)
			}
			if s.session != nil && !s.session.closed {
				t.Fatal("the session of a failed attach was not removed")
			}
		})
	}
}

// With --report the rounds go to the file, one whole round per line, and neither stdout nor stderr carries any.
func TestRunCommand_Report(t *testing.T) {
	d := stubRun(t, &runStubs{collectErr: errors.New("no BPF here")})
	report := filepath.Join(t.TempDir(), "rounds.jsonl")
	if err := os.WriteFile(report, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runRun(t, d, "--contract=c", "--bind=api=8080", "--report="+report, "--proc-root="+t.TempDir(), "--", "sh", "-c", "echo out; echo err >&2")
	b, err := os.ReadFile(report)
	if err != nil || r.err != nil || r.stdout != "out\n" || len(r.rounds) != 0 || !strings.Contains(r.stderr, "err\n") {
		t.Fatalf("err %v %v\nstdout %q\nstderr %s", err, r.err, r.stdout, r.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var last agentRound
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil || !last.Final || strings.Contains(string(b), "stale") {
		t.Fatalf("report %s: %v", b, err)
	}
	if r := runRun(t, d, "--contract=c", "--report="+filepath.Join(t.TempDir(), "no", "such", "dir"), "--", "true"); r.err == nil ||
		!strings.Contains(r.err.Error(), "--report: ") {
		t.Fatalf("err %v", r.err)
	}
}

func TestRunCommand_Signals(t *testing.T) {
	s := &runStubs{}
	d := stubRun(t, s)
	// The command is this test binary (TestRunSignalHelper), not a shell script: a shell cannot trap a
	// signal it was started with ignored, ignores a SIGINT its foreground child exited normally through,
	// and dies of one that lands before its trap is set, so whether a forwarded SIGINT reaches a script's
	// trap depends on timing and on how the test was launched. The signal goes out once the helper says
	// it handles SIGINT.
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv(signalHelperEnv, ready)
	go func() {
		<-s.signalsSet
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(ready); err == nil {
				break
			}
		}
		s.signals <- os.Interrupt
	}()
	r := runRun(t, d, "--contract=c", "--proc-root="+t.TempDir(), "--", os.Args[0], "-test.run=^TestRunSignalHelper$")
	if exitOf(r.err) != 7 {
		t.Fatalf("err %v\nstderr %s", r.err, r.stderr)
	}
	// A command a signal kills exits as a shell reports it.
	d = stubRun(t, &runStubs{})
	if r := runRun(t, d, "--contract=c", "--proc-root="+t.TempDir(), "--", "sh", "-c", "kill -TERM $$"); exitOf(r.err) != 143 {
		t.Fatalf("err %v", r.err)
	}
}

// signalHelperEnv names the file TestRunSignalHelper creates once it handles SIGINT.
const signalHelperEnv = "PACTO_TEST_RUN_SIGNAL_HELPER"

// TestRunSignalHelper is the command TestRunCommand_Signals runs: it exits 7 on SIGINT.
func TestRunSignalHelper(t *testing.T) {
	ready := os.Getenv(signalHelperEnv)
	if ready == "" {
		t.Skip("the command TestRunCommand_Signals runs")
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		os.Exit(1)
	}
	<-c
	os.Exit(7)
}
func TestRunCommand_Evidence(t *testing.T) {
	d := stubRun(t, &runStubs{})
	kp, err := (&app.Service{}).GenerateKey(t.TempDir(), "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		posts++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := runRun(t, d, "--contract=oci://r/a@sha256:"+strings.Repeat("a", 64), "--bind=api=8080", "--proc-root="+t.TempDir(),
		"--evidence-url="+srv.URL, "--evidence-key="+kp.PrivateKeyPath, "--evidence-key-id="+kp.KeyID, "--evidence-producer=ci", "--", "true")
	mu.Lock()
	defer mu.Unlock()
	if r.err != nil || posts != 1 {
		t.Fatalf("err %v, posts %d\nstderr %s", r.err, posts, r.stderr)
	}
}

func TestRunSession_Fails(t *testing.T) {
	for _, tc := range []struct {
		failOn, status, err string
		want                bool
	}{
		{"none", finding.StatusNonCompliant, "", false},
		{"noncompliant", finding.StatusUnknown, "", false},
		{"noncompliant", finding.StatusNonCompliant, "", true},
		{"noncompliant", "", "no contract", true},
		{"unknown", finding.StatusUnknown, "", true},
		{"unknown", finding.StatusWarning, "", false},
		{"unknown", finding.StatusCompliant, "", false},
	} {
		r := &runSession{opts: runOptions{failOn: tc.failOn}, final: []evalsink.Result{{Status: tc.status, Error: tc.err}}}
		if got := r.fails(); got != tc.want {
			t.Errorf("fail-on %s, status %q, error %q: %v", tc.failOn, tc.status, tc.err, got)
		}
	}
}

type countSink struct{ n int }

func (c *countSink) Publish(context.Context, runtimefacts.Snapshot) error { c.n++; return nil }

func TestGateSink(t *testing.T) {
	next := &countSink{}
	g := &gateSink{next: next}
	_ = g.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "before"})
	g.open()
	_ = g.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "live"})
	if last := g.freeze(); last.NodeName != "live" || next.n != 1 {
		t.Fatalf("last %+v, passed on %d", last, next.n)
	}
	_ = g.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "after"})
	if next.n != 1 || g.last.NodeName != "live" {
		t.Fatal("a snapshot passed a frozen gate")
	}
	// Once the command has begun to exit, its snapshots are dropped.
	g = &gateSink{next: next}
	g.open()
	ending := false
	g.follow(func() bool { return ending })
	_ = g.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "running"})
	ending = true
	_ = g.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "teardown"})
	if last := g.freeze(); last.NodeName != "running" || next.n != 2 {
		t.Fatalf("last %+v, passed on %d", last, next.n)
	}
}

// The program attached but the command cannot start in the session cgroup: the run falls back to the
// unprivileged mode, says so and removes the session.
func TestRunCommand_EBPFStartFails(t *testing.T) {
	s := &runStubs{
		collector: statusCollector{status: runtimewatch.CollectorStatus{AttachedAt: time.Now()}},
		session:   &fakeSession{unstartable: true},
	}
	d := stubRun(t, s)
	r := runRun(t, d, "--contract=c", "--bind=api=8080", "--cgroup-root=/sys/fs/cgroup/x", "--proc-root="+fakeProc(t, listen8080),
		"--", "sleep", "0.3")
	name := fmt.Sprintf("pacto-run-%d", os.Getpid())
	if r.err != nil || !strings.Contains(r.stderr, "unprivileged mode (eBPF: starting the command in cgroup "+name+" failed: ") ||
		!s.session.closed {
		t.Fatalf("err %v, session %+v\nstderr %s", r.err, s.session, r.stderr)
	}
	if res := r.final(t); codes(res)[finding.CodeInterfaceAbsent] != "" || r.rounds[len(r.rounds)-1].Mode != modeUnprivileged {
		t.Fatalf("final = %+v", res)
	}
	// A command that cannot start at all still fails the run.
	s = &runStubs{collector: statusCollector{status: runtimewatch.CollectorStatus{AttachedAt: time.Now()}}}
	d = stubRun(t, s)
	if r := runRun(t, d, "--contract=c", "--cgroup-root=/x", "--proc-root="+fakeProc(t), "--", "/no/such/command"); r.err == nil ||
		!strings.Contains(r.err.Error(), "start /no/such/command") {
		t.Fatalf("err %v", r.err)
	}
}

// The command leads a process group of its own, so a terminal's Ctrl-C is not delivered to it twice.
func TestRunCommand_OwnProcessGroup(t *testing.T) {
	d := stubRun(t, &runStubs{})
	r := runRun(t, d, "--contract=c", "--proc-root="+t.TempDir(), "--", "sh", "-c", `test "$(ps -o pgid= -p $$ | tr -d ' ')" = "$$"`)
	if r.err != nil {
		t.Fatalf("the command is not its own group leader: %v\nstderr %s", r.err, r.stderr)
	}
}
