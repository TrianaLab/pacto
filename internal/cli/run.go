package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/ebpf"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/evalsink"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/probe"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// Provenance collectors of `pacto run`, by mode.
const (
	runCollector       = "pacto-run"
	runCollectorKernel = runCollector + "+ebpf"
)

// Modes of `pacto run`, as rounds name them.
const (
	modeEBPF         = "ebpf"
	modeUnprivileged = "unprivileged"
	modeUnsupported  = "unsupported"
)

var failOnLevels = []string{"none", "noncompliant", "unknown"}

// ExitError ends the process with Code. The command has already said why, so
// main prints nothing.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// newRunCommand builds `pacto run`: run one command and check its process tree
// against a contract while it runs.
func newRunCommand(svc *app.Service, v *viper.Viper, version string, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run --contract <dir|oci ref> [flags] -- command [args...]",
		Short: "Run a command and check it against its contract while it runs",
		Long: "Starts the command and watches the TCP sockets of its whole process tree: the interfaces bound " +
			"with --bind must be listened on, the dependencies bound with --dep reached. Every --interval it prints " +
			"the contract's compliance and findings in --output-format; once the command exits it prints a final " +
			"round from the last view taken while the command ran, and exits with the command's exit code. With " +
			"--fail-on it exits 1 instead of 0 when the final verdict is that bad or worse.\n\n" +
			"The command runs in a process group of its own, so a Ctrl-C reaches it once. When pacto run owns the " +
			"terminal, the command's group takes it over: it reads the terminal and gets Ctrl-C and Ctrl-Z straight " +
			"from it, and pacto run takes the terminal back once it exits. Otherwise SIGINT and SIGTERM sent to " +
			"pacto run are passed on to the command's whole group.\n\n" +
			"Pacto leaves the command's stdin, stdout and stderr untouched. Rounds go to stderr, each in a single " +
			"write, or with --report to a file: the file is truncated when the run starts and every round " +
			"is appended to it in a single write, so a reader following it sees whole rounds (with json, one " +
			"object per line).\n\n" +
			"With root (or BPF, PERFMON, SYS_PTRACE and write access to its own cgroup) on Linux 5.11 or later with " +
			"BTF and cgroup v2 (or 5.8 or later with BTF and root or CAP_SYS_RESOURCE), the command starts inside a new cgroup pacto-run-<pid> and an eBPF program sees " +
			"every listener and every connection outcome of the tree, failures included (collector pacto-run+ebpf). " +
			"Otherwise it falls back to polling /proc every second and says so (collector pacto-run): listeners are " +
			"seen in full, so a missing one is still confirmed, but a connection is seen only while it is open at a " +
			"poll and a failed connection never, so a dependency is reported reachable or unknown, never unreachable. " +
			"If the program attached but the command cannot start inside the new cgroup, the run falls back to " +
			"polling too and says so. " +
			"Elsewhere than Linux the command runs unobserved and every runtime assertion is unsupported.\n\n" +
			"A negative is reported once it persists for --stabilization-window. Health and metrics capabilities are " +
			"probed on --host at their bound interface's port. With --evidence-url every round is also signed and " +
			"sent to an Evidence Server, which needs the contract pinned as oci://...@sha256:<digest>.",
		Example: `  pacto run --contract ./payments --bind api=8080 --dep postgres=127.0.0.1:5432 -- ./payments-server
  pacto run --contract oci://ghcr.io/acme/payments:1.4.0 --bind api=8080 --fail-on noncompliant \
    --stabilization-window 30s --output-format json --report rounds.jsonl -- java -jar payments.jar`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommand(cmd, svc, v, version, args, d)
		},
	}
	// Everything after the command's name is the command's own.
	cmd.Flags().SetInterspersed(false)
	f := cmd.Flags()
	f.String("contract", "", "contract bundle directory or oci:// reference (required)")
	f.StringArray("bind", nil, "interface=port the command serves the interface on (repeatable)")
	f.StringArray("dep", nil, "dependency=host:port the command reaches the dependency at (repeatable)")
	f.String("host", "127.0.0.1", "address capability and protocol probes dial")
	f.Duration("stabilization-window", 2*time.Minute, "how long a negative must persist before it is reported")
	f.Bool("enable-probing", false, "also probe the wire protocol of every interface found listening")
	f.Duration("interval", 10*time.Second, "how often a round is evaluated and printed (whole seconds)")
	f.String("fail-on", "none", fmt.Sprintf("exit 1 when the command exits 0 but the final verdict is this bad or worse: one of %v", failOnLevels))
	f.String("report", "", "file every round is appended to instead of stderr (truncated when the run starts)")
	f.String("cgroup-root", "", "cgroup v2 directory the session cgroup is created in (default: the caller's own cgroup under /sys/fs/cgroup)")
	f.String("proc-root", "/proc", "where /proc is mounted")
	f.Uint32("ringbuf-size", 256*1024, "kernel ring buffer size in bytes; a power of two and a multiple of the page size")
	addEvidenceFlags(f, "")
	return cmd
}

// runOptions are the flags of one `pacto run`.
type runOptions struct {
	interval   time.Duration
	failOn     string
	cgroupRoot string
	proc       runtimewatch.ProcFS
	ringbuf    uint32
	version    string
}

// runCommand evaluates the flags, picks the mode, runs the child and returns its exit code.
func runCommand(cmd *cobra.Command, svc *app.Service, v *viper.Viper, version string, args []string, d deps) error {
	f := cmd.Flags()
	ref, _ := f.GetString("contract")
	o := runOptions{version: version}
	o.interval, _ = f.GetDuration("interval")
	o.failOn, _ = f.GetString("fail-on")
	o.cgroupRoot, _ = f.GetString("cgroup-root")
	procRoot, _ := f.GetString("proc-root")
	o.proc = runtimewatch.OSProcFS{Root: procRoot}
	o.ringbuf, _ = f.GetUint32("ringbuf-size")
	format := v.GetString(outputFormatKey)
	switch {
	case ref == "":
		return errors.New("--contract is required")
	case !slices.Contains(failOnLevels, o.failOn):
		return fmt.Errorf("--fail-on must be one of %v, got %q", failOnLevels, o.failOn)
	case o.interval < time.Second || o.interval%time.Second != 0:
		return fmt.Errorf("--interval must be a whole number of seconds, at least 1s, got %s", o.interval)
	}
	if err := checkOutputFormat(format); err != nil {
		return err
	}
	name := fmt.Sprintf("pacto-run-%d", os.Getpid())
	target, err := runTarget(cmd, ref, name)
	if err != nil {
		return err
	}
	var send func(context.Context, []evalsink.Result) error
	if url, _ := f.GetString("evidence-url"); url != "" {
		if err := sendableContract(ref); err != nil {
			return err
		}
		if send, err = evidenceFlags(cmd, svc, version); err != nil {
			return err
		}
	}
	bundle, err := d.resolveBundle(svc, cmd.Context(), ref)
	if err != nil {
		return err
	}
	if err := checkBound(bundle.Contract, target); err != nil {
		return err
	}
	window, _ := f.GetDuration("stabilization-window")
	protocol, _ := f.GetBool("enable-probing")
	r := &runSession{opts: o, name: name, target: target, d: d}
	r.eval = &evalsink.Sink{
		Sources: []func(context.Context) ([]evalsink.Target, error){r.targets},
		Load:    func(context.Context, string) (*contract.Bundle, error) { return bundle, nil },
		Prober:  probe.New(probeTimeout), Window: window, Protocol: protocol, Now: time.Now, Name: runCollector,
	}
	// Rounds never share stdout with the command: it may be piped into something that parses it.
	out := cmd.ErrOrStderr()
	if path, _ := f.GetString("report"); path != "" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("--report: %w", err)
		}
		defer func() { _ = file.Close() }()
		out = file
	}
	r.eval.Emit = func(ctx context.Context, results []evalsink.Result) error {
		r.final = results
		// One write per round, so it neither interleaves with the command's own stderr nor leaves a
		// half round in the report for a reader following it.
		var buf bytes.Buffer
		_ = printRound(&buf, format, agentRound{EvaluatedAt: r.eval.Now().UTC(), Mode: r.mode, Final: r.ended, Targets: results})
		_, err := out.Write(buf.Bytes())
		if send != nil {
			err = errors.Join(err, send(ctx, results))
		}
		return err
	}
	return r.run(cmd, args)
}

// runTarget builds the session's target from --bind, --dep and --host.
func runTarget(cmd *cobra.Command, ref, name string) (evalsink.Target, error) {
	f := cmd.Flags()
	binds, _ := f.GetStringArray("bind")
	deps, _ := f.GetStringArray("dep")
	host, _ := f.GetString("host")
	sel := evalsink.Selector{Session: name}
	// Start is at the snapshots' one-second precision, so a connection seen in the second the run started
	// is not mistaken for one from before it.
	t := evalsink.Target{Name: sel.String(), Contract: ref, Match: sel, Host: host,
		Interfaces: map[string]int32{}, Dependencies: map[string]string{}, Start: time.Now().Truncate(time.Second)}
	for _, b := range binds {
		iface, p, ok := strings.Cut(b, "=")
		port, err := strconv.ParseInt(p, 10, 32)
		if !ok || iface == "" || err != nil {
			return t, fmt.Errorf("--bind %q is not interface=port", b)
		}
		t.Interfaces[iface] = int32(port)
	}
	for _, d := range deps {
		dep, addr, ok := strings.Cut(d, "=")
		if !ok || dep == "" {
			return t, fmt.Errorf("--dep %q is not dependency=host:port", d)
		}
		t.Dependencies[dep] = addr
	}
	return t, t.Validate()
}

// checkBound rejects a --bind or --dep the contract does not declare: a typo would otherwise leave the
// declared one unobserved without a word.
func checkBound(c *contract.Contract, t evalsink.Target) error {
	for name := range t.Interfaces {
		if !slices.ContainsFunc(c.Interfaces, func(i contract.Interface) bool { return i.Name == name }) {
			return fmt.Errorf("--bind %s: the contract declares no interface %q", name, name)
		}
	}
	for name := range t.Dependencies {
		if !slices.ContainsFunc(c.Dependencies, func(d contract.Dependency) bool { return d.Name == name }) {
			return fmt.Errorf("--dep %s: the contract declares no dependency %q", name, name)
		}
	}
	return nil
}

// runSession is one `pacto run`: its target, its mode and the rounds it printed.
type runSession struct {
	opts   runOptions
	d      deps
	name   string
	eval   *evalsink.Sink
	gate   gateSink
	mode   string
	ended  bool
	final  []evalsink.Result
	target evalsink.Target
	// stop ends the watcher of the mode and waits for it.
	stop func()
	// cleanup releases what the mode holds after the child has exited.
	cleanup func() error
}

// targets is the evaluation's only source: the session.
func (r *runSession) targets(context.Context) ([]evalsink.Target, error) {
	return []evalsink.Target{r.target}, nil
}

// run picks the mode, starts the child, passes signals on until it exits, prints the final round and turns
// the child's exit and the verdict into the command's result.
func (r *runSession) run(cmd *cobra.Command, args []string) error {
	stderr := cmd.ErrOrStderr()
	// The watchers outlive the context main cancels on SIGINT: the child decides when the run ends.
	ctx := context.WithoutCancel(cmd.Context())
	r.gate.next = r.eval
	tty, foreground := foregroundTerminal(cmd.InOrStdin())
	newChild := func() *exec.Cmd {
		c := exec.Command(args[0], args[1:]...)
		c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), stderr
		ownGroup(c, tty, foreground)
		return c
	}
	child := newChild()
	_, _ = fmt.Fprintln(stderr, "pacto run: "+r.start(ctx, child))
	err := child.Start()
	// After the start, so the command does not inherit the SIGTTOU this ignores.
	defer holdTerminal(tty, foreground)()
	if err != nil && r.mode == modeEBPF {
		// The program attached but the kernel would not start the command in the session cgroup: watch it
		// from /proc instead of failing the run.
		r.stop()
		r.release(stderr)
		child = newChild()
		why := fmt.Errorf("starting the command in cgroup %s failed: %w", r.name, err)
		_, _ = fmt.Fprintln(stderr, "pacto run: "+r.fallback(ctx, why))
		err = child.Start()
	}
	if err != nil {
		r.stop()
		r.release(stderr)
		return fmt.Errorf("start %s: %w", args[0], err)
	}
	// Evaluation starts with the command, so no round interleaves with the notices above; the snapshot
	// published meanwhile waits for it.
	evalCtx, cancelEval := context.WithCancel(ctx)
	evalDone := make(chan struct{})
	go func() {
		defer close(evalDone)
		r.eval.Run(evalCtx)
	}()
	stopAll := func() {
		r.stop()
		cancelEval()
		<-evalDone
	}
	pid := child.Process.Pid
	r.gate.follow(func() bool { return runtimewatch.Exiting(r.opts.proc, pid) })
	sigs := make(chan os.Signal, 4)
	r.d.notifySignals(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	for done := false; !done; {
		select {
		case s := <-sigs:
			// The command starts shutting down now, closing its listeners first if it is graceful: the
			// view before this is the one of the running command.
			r.gate.freeze()
			_ = signalGroup(child.Process, s)
		case <-waited:
			done = true
		}
	}
	// ponytail: a snapshot published between the reap and this freeze (microseconds) passes the gate; the
	// stabilization window keeps one such round from becoming a finding.
	last := r.gate.freeze()
	stopAll()
	r.ended = true
	r.eval.Evaluate(ctx, &last)
	r.release(stderr)

	code := exitCode(child.ProcessState)
	if code == 0 && r.fails() {
		_, _ = fmt.Fprintf(stderr, "pacto run: the final verdict fails --fail-on %s\n", r.opts.failOn)
		code = 1
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// release removes the session cgroup, if any, once the child is gone.
func (r *runSession) release(stderr io.Writer) {
	if err := r.cleanup(); err != nil {
		_, _ = fmt.Fprintf(stderr, "pacto run: %v\n", err)
	}
}

// start picks the best mode this host allows and starts its watcher, returning the notice that says which
// one it is and what it cannot see.
func (r *runSession) start(ctx context.Context, child *exec.Cmd) string {
	err := r.startEBPF(ctx, child)
	if err == nil {
		r.mode, r.eval.Kernel = modeEBPF, runCollectorKernel
		r.gate.open()
		return fmt.Sprintf("observing the command in cgroup %s with eBPF (collector %s)", r.name, runCollectorKernel)
	}
	return r.fallback(ctx, err)
}

// fallback starts the unprivileged mode, or the unsupported one when there is no /proc either, because of
// why eBPF cannot be used, and returns the notice that says which one it is and what it cannot see.
func (r *runSession) fallback(ctx context.Context, err error) string {
	r.stop, r.cleanup = func() {}, func() error { return nil }
	if _, perr := r.opts.proc.ReadFile("self/stat"); perr != nil {
		// Nothing to watch with: every assertion is unsupported, and the binding is dropped so none is judged.
		r.mode = modeUnsupported
		r.target.Interfaces, r.target.Dependencies, r.target.Host = nil, nil, ""
		return fmt.Sprintf("runtime evidence is unsupported here (eBPF: %v; /proc: %v); the command runs unobserved", err, perr)
	}
	r.mode, r.eval.Kernel = modeUnprivileged, runCollector
	notice := fmt.Sprintf("unprivileged mode (eBPF: %v): polling the process tree's sockets in /proc every %s. Listeners are seen "+
		"in full; connections only while open at a poll and failed connections never, so a dependency is reported reachable "+
		"or unknown, never unreachable (collector %s)", err, r.d.pollInterval, runCollector)
	if err := r.d.setSubreaper(); err != nil {
		notice += "; a process the command daemonizes leaves the watched tree (" + err.Error() + ")"
	}
	w := &runtimewatch.TreeWatcher{
		Proc:     r.opts.proc,
		Root:     os.Getpid(),
		Identity: runtimefacts.Identity{Kind: runtimefacts.KindSession, ID: r.name, Name: r.name},
		Info:     r.hostInfo(),
		Sink:     &r.gate,
		Now:      time.Now,
	}
	r.gate.open()
	r.stop = r.watch(ctx, func(ctx context.Context) {
		poll, publish := time.NewTicker(r.d.pollInterval), time.NewTicker(r.opts.interval)
		defer poll.Stop()
		defer publish.Stop()
		w.Run(ctx, poll.C, publish.C)
	})
	return notice
}

// startEBPF creates the session cgroup, prepares the child to start in it and runs the agent scoped to it,
// returning once the program is attached; on any failure it undoes what it did.
func (r *runSession) startEBPF(ctx context.Context, child *exec.Cmd) error {
	col, err := r.d.newCollector(r.opts.ringbuf)
	if err != nil {
		return err
	}
	parent, err := r.sessionParent()
	if err != nil {
		return err
	}
	sess, err := r.d.newSession(parent, r.name)
	if err != nil {
		return err
	}
	info := r.hostInfo()
	stop := r.watch(ctx, func(ctx context.Context) {
		r.d.runAgent(ctx, runtimewatch.Config{
			NodeName: info.NodeName, AgentVersion: info.AgentVersion,
			PublishInterval: r.opts.interval, ResyncInterval: r.opts.interval,
			Proc: r.opts.proc, Cgroups: os.DirFS(parent),
			Keep:      func(id runtimefacts.Identity) bool { return id.Kind == runtimefacts.KindSession && id.Name == r.name },
			Collector: col, Sink: &r.gate,
		})
	})
	if err := awaitAttach(col, r.d.attachTimeout); err != nil {
		stop()
		_ = sess.Close()
		return err
	}
	sess.Prepare(child)
	r.stop, r.cleanup = stop, sess.Close
	return nil
}

// watch runs fn until the returned stop is called, which waits for it.
func (r *runSession) watch(ctx context.Context, fn func(context.Context)) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// hostInfo is the static part of every snapshot.
func (r *runSession) hostInfo() runtimewatch.HostInfo {
	host, _ := os.Hostname()
	return runtimewatch.HostInfo{NodeName: host, AgentVersion: r.opts.version, PublishInterval: r.opts.interval, StartedAt: time.Now()}
}

// sessionParent is the cgroup the session is created in: --cgroup-root, or the caller's own cgroup v2.
func (r *runSession) sessionParent() (string, error) {
	if r.opts.cgroupRoot != "" {
		return r.opts.cgroupRoot, nil
	}
	b, err := r.opts.proc.ReadFile("self/cgroup")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join("/sys/fs/cgroup", p), nil
		}
	}
	return "", errors.New("this process is in no cgroup v2")
}

// awaitAttach waits for the collector's first attach, or its first failure, at most timeout.
func awaitAttach(col runtimewatch.Collector, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st := col.Status()
		switch {
		case !st.AttachedAt.IsZero():
			return nil
		case st.Failed && st.Reason != ebpf.ReasonAttaching:
			return errors.New(st.Message)
		case time.Now().After(deadline):
			return fmt.Errorf("the eBPF program did not attach within %s", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fails reports whether the final round is as bad as --fail-on or worse: a result that could not be
// evaluated counts as non-compliant.
func (r *runSession) fails() bool {
	rank := func(res evalsink.Result) int {
		switch {
		case res.Error != "" || res.Status == finding.StatusNonCompliant:
			return 2
		case res.Status == finding.StatusUnknown:
			return 1
		}
		return 0
	}
	threshold := map[string]int{"noncompliant": 2, "unknown": 1}[r.opts.failOn] // none: 0, never
	for _, res := range r.final {
		if threshold > 0 && rank(res) >= threshold {
			return true
		}
	}
	return false
}

// exitCode is the child's exit status, or 128 plus the signal that killed it, as a shell reports it.
func exitCode(ps *os.ProcessState) int {
	if ws, _ := ps.Sys().(syscall.WaitStatus); ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// gateSink passes snapshots on to the evaluation only while the session is live: none from an eBPF attempt
// that failed before the mode was chosen, none once the command was signalled or has begun to exit. It
// keeps the last one it passed on for the final round.
type gateSink struct {
	next runtimewatch.Sink
	mu   sync.Mutex
	live bool
	last runtimefacts.Snapshot
	// ending reports whether the command has begun to exit: a snapshot built before is published after,
	// so one published then may already show the teardown.
	ending func() bool
}

// Publish implements runtimewatch.Sink.
func (g *gateSink) Publish(ctx context.Context, s runtimefacts.Snapshot) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.live || (g.ending != nil && g.ending()) {
		return nil
	}
	g.last = s
	return g.next.Publish(ctx, s)
}

// follow drops every snapshot published once ending reports true.
func (g *gateSink) follow(ending func() bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ending = ending
}

func (g *gateSink) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.live = true
}

// freeze closes the gate for good and returns the last snapshot, the zero one (stale) when there was none.
func (g *gateSink) freeze() runtimefacts.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.live = false
	return g.last
}
