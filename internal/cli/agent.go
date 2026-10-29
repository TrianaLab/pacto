package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/evalsink"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/kubesink"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/probe"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

var sinkKinds = []string{"kubernetes", "stdout", "local", "evidence"}

const (
	// dockerSocket is where the Docker Engine API listens.
	dockerSocket = "/var/run/docker.sock"
	// probeTimeout bounds one capability or protocol probe.
	probeTimeout = 3 * time.Second
	// evidenceTTL is how long a sent envelope stays valid, `pacto evidence sign`'s default.
	evidenceTTL = 24 * time.Hour
)

// newAgentCommand builds `pacto agent`: the host daemon that observes TCP
// socket state with eBPF and publishes per-workload runtime facts to a sink.
func newAgentCommand(svc *app.Service, v *viper.Viper, version string, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Observe workloads' TCP sockets on this host with eBPF",
		Long: "Runs until interrupted. Watches TCP listeners and outbound connection outcomes " +
			"with an eBPF program, attributes each socket to the workload that created it (a " +
			"Kubernetes pod, a container, a systemd unit or a pacto run session, from its cgroup, " +
			"or from its network namespace when the cgroup names none) and publishes a snapshot " +
			"every --publish-interval. --sink kubernetes writes the node's NodeRuntimeObservation " +
			"status (pods only; what the Pacto operator's node agent DaemonSet runs); --sink stdout " +
			"prints one JSON snapshot per line.\n\n" +
			"--sink local and --sink evidence check workloads against their contracts on this host. " +
			"The workloads come from --bindings (a YAML list of {match: {unit|container|cgroup|session|pid}, " +
			"contract, host, interfaces: {name: port}, dependencies: {name: host:port}}) and with --docker " +
			"from running containers labelled dev.pacto.contract=<dir|oci ref>, dev.pacto.bind.<interface>=<port> " +
			"and dev.pacto.dep.<dependency>=<host:port>. Interfaces and dependencies are judged from the " +
			"kernel's view, health and metrics capabilities by probing the bound port; a negative is reported " +
			"once it persists for --stabilization-window. --sink local prints every target's compliance and " +
			"findings each interval in --output-format; --sink evidence signs each target's evidence and " +
			"sends it to an Evidence Server, which needs contracts pinned as oci://...@sha256:<digest>. " +
			"Envelope sequences start at the wall clock in nanoseconds and count up, so they keep increasing " +
			"across restarts as long as the clock does.\n\n" +
			"Needs Linux 5.11 or later with BTF, or 5.8 or later with BTF and root or CAP_SYS_RESOURCE; root " +
			"(or BPF, PERFMON and SYS_PTRACE); and the host's /proc and cgroupfs.",
		Example: `  pacto agent --sink stdout --publish-interval 5s
  pacto agent --sink kubernetes --cgroup-root /host/sys/fs/cgroup
  pacto agent --sink local --docker --cgroup-root /host/sys/fs/cgroup
  pacto agent --sink evidence --bindings bindings.yaml --evidence-url https://evidence.example.com \
    --evidence-key agent.key --evidence-key-id k1 --evidence-producer host-1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			sink, _ := f.GetString("sink")
			nodeName, _ := f.GetString("node-name")
			namespace, _ := f.GetString("namespace")
			publish, _ := f.GetDuration("publish-interval")
			resync, _ := f.GetDuration("resync-interval")
			procRoot, _ := f.GetString("proc-root")
			cgroupRoot, _ := f.GetString("cgroup-root")
			ringbuf, _ := f.GetUint32("ringbuf-size")
			if err := validateIntervals(publish, resync); err != nil {
				return err
			}
			cfg := runtimewatch.Config{
				NodeName:        nodeName,
				AgentVersion:    version,
				PublishInterval: publish,
				ResyncInterval:  resync,
				Proc:            runtimewatch.OSProcFS{Root: procRoot},
			}
			if cgroupRoot != "" {
				cfg.Cgroups = os.DirFS(cgroupRoot)
			}
			var eval *evalsink.Sink
			switch sink {
			case "kubernetes":
				if nodeName == "" || namespace == "" {
					return fmt.Errorf("--sink kubernetes needs --node-name and --namespace (or NODE_NAME and POD_NAMESPACE)")
				}
				dyn, err := d.newDynamic()
				if err != nil {
					return fmt.Errorf("kubernetes sink: %w", err)
				}
				cfg.Sink = &kubesink.Sink{Client: dyn, Namespace: namespace, Name: nodeName}
				cfg.Keep = func(id runtimefacts.Identity) bool { return id.Kind == runtimefacts.KindPod }
			case "stdout":
				cfg.Sink = runtimewatch.JSONSink{W: cmd.OutOrStdout()}
			case "local", "evidence":
				var err error
				if eval, err = newEvalSink(cmd, svc, v, version, cfg.Proc, d); err != nil {
					return err
				}
				cfg.Sink = eval
			default:
				return fmt.Errorf("--sink must be one of %v, got %q", sinkKinds, sink)
			}
			col, err := d.newCollector(ringbuf)
			if err != nil {
				return err
			}
			cfg.Collector = col
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			if eval != nil {
				go eval.Run(ctx)
			}
			d.runAgent(ctx, cfg)
			return nil
		},
	}
	f := cmd.Flags()
	f.String("sink", "stdout", fmt.Sprintf("where snapshots go: one of %v", sinkKinds))
	f.String("node-name", os.Getenv("NODE_NAME"),
		"name of this host; with --sink kubernetes also the NodeRuntimeObservation name (default $NODE_NAME)")
	f.String("namespace", os.Getenv("POD_NAMESPACE"),
		"namespace the NodeRuntimeObservation is written to with --sink kubernetes (default $POD_NAMESPACE)")
	f.Duration("publish-interval", 30*time.Second, "how often a snapshot is published (whole seconds)")
	f.Duration("resync-interval", 60*time.Second, "how often workloads, network namespaces and listeners are re-read from /proc")
	f.String("proc-root", "/proc", "where the host /proc is mounted (a pod needs hostPID, a container --pid=host)")
	f.String("cgroup-root", "/sys/fs/cgroup",
		"where the host cgroup v2 hierarchy is mounted; empty attributes by network namespace only")
	f.Uint32("ringbuf-size", 256*1024, "kernel ring buffer size in bytes; a power of two and a multiple of the page size")
	f.String("bindings", "", "with --sink local or evidence: YAML file binding workloads to contracts")
	f.Bool("docker", false, "with --sink local or evidence: check running containers labelled "+evalsink.LabelContract+
		" (Docker Engine API on "+dockerSocket+")")
	f.StringArray("docker-label-filter", nil, "with --docker: only check containers that also carry this label "+
		"(key=value or key; repeatable, all must match), e.g. com.docker.compose.project=demo")
	f.Duration("stabilization-window", 2*time.Minute, "how long a negative must persist before it is reported")
	f.Bool("enable-probing", false, "also probe the wire protocol of every interface found listening")
	addEvidenceFlags(f, "with --sink evidence: ")
	return cmd
}

// newEvalSink builds the local or evidence sink from the flags.
func newEvalSink(cmd *cobra.Command, svc *app.Service, v *viper.Viper, version string, proc runtimewatch.ProcFS, d deps) (*evalsink.Sink, error) {
	f := cmd.Flags()
	sink, _ := f.GetString("sink")
	bindings, _ := f.GetString("bindings")
	docker, _ := f.GetBool("docker")
	window, _ := f.GetDuration("stabilization-window")
	protocol, _ := f.GetBool("enable-probing")
	if bindings == "" && !docker {
		return nil, fmt.Errorf("--sink %s needs --bindings or --docker to know which workloads to check", sink)
	}
	labels, err := dockerLabelFilters(f, docker)
	if err != nil {
		return nil, err
	}
	s := &evalsink.Sink{Load: svc.ResolveBundle, Proc: proc, Prober: probe.New(probeTimeout), Window: window,
		Protocol: protocol, Now: time.Now, ForeignNetns: runtimewatch.ForeignNetns(proc, d.selfProc)}
	if s.ForeignNetns {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "pacto agent: this process is not on the host's network, so workloads "+
			"probed on a loopback address (host-network containers, bindings without a host) cannot be probed: "+
			"their capabilities and interface protocols are unsupported (run the agent with --network=host)")
	}
	var fileTargets []evalsink.Target
	if bindings != "" {
		var err error
		if fileTargets, err = evalsink.LoadBindings(bindings); err != nil {
			return nil, err
		}
		s.Sources = append(s.Sources, func(context.Context) ([]evalsink.Target, error) { return fileTargets, nil })
	}
	if docker {
		engine := d.newDocker(dockerSocket)
		engine.Labels = labels
		s.Sources = append(s.Sources, engine.Targets)
	}
	if sink == "local" {
		format := v.GetString(outputFormatKey)
		if err := checkOutputFormat(format); err != nil {
			return nil, err
		}
		w := cmd.OutOrStdout()
		s.Emit = func(_ context.Context, results []evalsink.Result) error {
			return printRound(w, format, agentRound{EvaluatedAt: s.Now().UTC(), Targets: results})
		}
		return s, nil
	}
	emit, err := evidenceFlags(cmd, svc, version)
	if err != nil {
		return nil, err
	}
	for _, t := range fileTargets {
		if err := sendableContract(t.Contract); err != nil {
			return nil, fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	s.Emit = emit
	return s, nil
}

// dockerLabelFilters reads the repeatable --docker-label-filter, which only
// means something alongside --docker.
func dockerLabelFilters(f *pflag.FlagSet, docker bool) ([]string, error) {
	labels, _ := f.GetStringArray("docker-label-filter")
	if len(labels) > 0 && !docker {
		return nil, errors.New("--docker-label-filter needs --docker")
	}
	for _, l := range labels {
		if l == "" || strings.HasPrefix(l, "=") {
			return nil, fmt.Errorf("--docker-label-filter %q: want key or key=value", l)
		}
	}
	return labels, nil
}

// evidenceFlags reads the --evidence-* flags into an emitter that signs and sends every result.
func evidenceFlags(cmd *cobra.Command, svc *app.Service, version string) (func(context.Context, []evalsink.Result) error, error) {
	f := cmd.Flags()
	opts := app.SignOptions{ProducerVersion: version, TTL: evidenceTTL}
	url, _ := f.GetString("evidence-url")
	opts.KeyPath, _ = f.GetString("evidence-key")
	opts.KeyID, _ = f.GetString("evidence-key-id")
	opts.ProducerID, _ = f.GetString("evidence-producer")
	if url == "" || opts.KeyPath == "" || opts.KeyID == "" || opts.ProducerID == "" {
		return nil, errors.New("sending evidence needs --evidence-url, --evidence-key, --evidence-key-id and --evidence-producer")
	}
	return evidenceEmitter(svc, url, opts, uint64(time.Now().UnixNano())), nil
}

// addEvidenceFlags declares the --evidence-* flags; when says when they apply.
func addEvidenceFlags(f *pflag.FlagSet, when string) {
	f.String("evidence-url", "", when+"Evidence Server base URL")
	f.String("evidence-key", "", when+"private key file envelopes are signed with")
	f.String("evidence-key-id", "", when+"key id (must match the server's trust entry)")
	f.String("evidence-producer", "", when+"producer id")
}

// sendableContract rejects a contract evidence cannot be sent about: an Evidence Server stores evidence on
// immutable contract revisions only.
func sendableContract(ref string) error {
	if evidenceingest.CheckContractRef(ref) != nil {
		return fmt.Errorf("contract %s is not pinned as oci://...@sha256:<digest>, so its evidence cannot be sent (check it with --sink local)", ref)
	}
	return nil
}

// evidenceEmitter signs every evaluated target's evidence and posts it. Sequences count up from seq: seeded
// from the wall clock, they keep increasing across restarts while the clock does.
func evidenceEmitter(svc *app.Service, url string, opts app.SignOptions, seq uint64) func(context.Context, []evalsink.Result) error {
	return func(ctx context.Context, results []evalsink.Result) error {
		var errs []error
		for _, r := range results {
			if r.Error != "" {
				errs = append(errs, fmt.Errorf("%s: %s", r.Target, r.Error))
				continue
			}
			if err := sendableContract(r.Contract); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.Target, err))
				continue
			}
			seq++
			opts.Sequence = seq
			env, err := svc.SignEvidenceSet(r.Evidence, opts)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.Target, err))
				continue
			}
			res, err := svc.PostEnvelope(ctx, url, env)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", r.Target, err))
			case res.StatusCode/100 != 2:
				errs = append(errs, fmt.Errorf("%s: evidence server answered %d: %s", r.Target, res.StatusCode, strings.TrimSpace(res.Body)))
			}
		}
		return errors.Join(errs...)
	}
}

// agentRound is one evaluation as --output-format json prints it, one line each. `pacto run` adds the
// mode it observes in and marks the round it prints once the command has exited.
type agentRound struct {
	EvaluatedAt time.Time         `json:"evaluatedAt"`
	Mode        string            `json:"mode,omitempty"`
	Final       bool              `json:"final,omitempty"`
	Targets     []evalsink.Result `json:"targets"`
}

// printRound prints one evaluation of every target.
func printRound(w io.Writer, format string, round agentRound) error {
	if format == "json" {
		return json.NewEncoder(w).Encode(round)
	}
	head := round.EvaluatedAt.Format(time.RFC3339)
	if round.Mode != "" {
		head += "  " + round.Mode
	}
	if round.Final {
		head += "  final"
	}
	md := format == "markdown"
	if md {
		_, _ = fmt.Fprintf(w, "### %s\n\n", head)
	} else {
		_, _ = fmt.Fprintln(w, head)
	}
	for _, r := range round.Targets {
		head := fmt.Sprintf("%s  %s  %d/%d evaluated  %s", r.Target, r.Status, r.Coverage.Evaluated, r.Coverage.Required, r.Contract)
		if r.Error != "" {
			head = fmt.Sprintf("%s  error  %s: %s", r.Target, r.Contract, r.Error)
		}
		if md {
			head = "- **" + head + "**"
		}
		_, _ = fmt.Fprintln(w, head)
		for _, f := range r.Findings {
			collector := ""
			if len(f.EvidenceRefs) > 0 && f.EvidenceRefs[0].Collector != "" {
				collector = " (" + f.EvidenceRefs[0].Collector + ")"
			}
			_, _ = fmt.Fprintf(w, "  - [%s] %s %s/%s: %s%s\n", f.Severity, f.Code, f.Subject.Kind, f.Subject.Name, f.Message, collector)
		}
		for _, n := range r.Notes {
			_, _ = fmt.Fprintf(w, "  - note: %s\n", n)
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// validateIntervals rejects what time.NewTicker would panic on and a publish
// interval a snapshot cannot carry: publishIntervalSeconds is whole seconds,
// at least 1.
func validateIntervals(publish, resync time.Duration) error {
	if resync <= 0 {
		return fmt.Errorf("--resync-interval must be positive, got %s", resync)
	}
	if publish < time.Second || publish%time.Second != 0 {
		return fmt.Errorf("--publish-interval must be a whole number of seconds, at least 1s, got %s", publish)
	}
	return nil
}
