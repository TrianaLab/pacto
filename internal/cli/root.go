package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/ebpf"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/evalsink"
	"github.com/trianalab/pacto/v4/internal/tui"
	"github.com/trianalab/pacto/v4/internal/update"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/doc"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

const outputFormatKey = "output-format"

// deps are what the commands reach beyond the process through: the update server, the terminal, the
// kernel, a cluster, the Docker Engine and the registry. NewRootCommand wires the real ones; a test builds
// a command with its own, so no test swaps package state.
type deps struct {
	checkForUpdate    func(current string) *update.CheckResult
	readPassword      func(fd int) ([]byte, error)
	executable        func() (string, error)
	runTUI            func(context.Context, tui.Options) error
	buildStaticExport func(*dashboard.ServiceDetails, *dashboard.GlobalGraph) (map[string][]byte, error)
	// The runtime commands: the collector and a session touch the kernel, the dynamic client a cluster,
	// runAgent blocks until its context ends.
	newCollector func(ringbuf uint32) (runtimewatch.Collector, error)
	newDynamic   func() (dynamic.Interface, error)
	// newDeliveryRegistry is the read-only registry `pacto delivery` resolves artifacts through.
	newDeliveryRegistry func(insecureHosts ...string) app.DeliveryRegistry
	runAgent            func(context.Context, runtimewatch.Config)
	newDocker           func(socket string) *evalsink.Docker
	newSession          func(parent, name string) (ebpf.Session, error)
	setSubreaper        func() error
	notifySignals       func(chan<- os.Signal, ...os.Signal)
	resolveBundle       func(svc *app.Service, ctx context.Context, ref string) (*contract.Bundle, error)
	// selfProc is this process's own /proc, read for its network namespace.
	selfProc runtimewatch.ProcFS
	// attachTimeout bounds the wait for `pacto run`'s eBPF program before the command starts.
	attachTimeout time.Duration
	// pollInterval is how often `pacto run`'s unprivileged mode reads the tree's sockets: a connection
	// shorter than this may go unseen.
	pollInterval time.Duration
	// updateWait is how long a command waits, once done, for the update check: long enough for a cache
	// read, short enough that a slow network fetch never delays the user.
	updateWait time.Duration
}

// defaultDeps are the real dependencies.
func defaultDeps() deps {
	return deps{
		checkForUpdate:      update.CheckForUpdate,
		readPassword:        term.ReadPassword,
		executable:          os.Executable,
		runTUI:              tui.Run,
		buildStaticExport:   doc.BuildStaticExport,
		newCollector:        ebpf.New,
		newDynamic:          k8sclient.NewDynamic,
		newDeliveryRegistry: newDeliveryRegistry,
		runAgent:            runtimewatch.Run,
		newDocker:           evalsink.NewDocker,
		newSession:          ebpf.NewSession,
		setSubreaper:        ebpf.SetSubreaper,
		notifySignals:       signal.Notify,
		resolveBundle:       (*app.Service).ResolveBundle,
		selfProc:            runtimewatch.OSProcFS{Root: "/proc"},
		attachTimeout:       10 * time.Second,
		pollInterval:        time.Second,
		updateWait:          200 * time.Millisecond,
	}
}

// NewRootCommand constructs the Cobra command tree with the given app service.
func NewRootCommand(svc *app.Service, info VersionInfo) *cobra.Command {
	return newRootCommand(svc, info, defaultDeps())
}

// newRootCommand constructs the command tree over d.
func newRootCommand(svc *app.Service, info VersionInfo, d deps) *cobra.Command {
	v := viper.New()

	root := &cobra.Command{
		Use:           "pacto",
		Short:         "Pacto — service contract standard for cloud-native services",
		Long:          "Pacto is a CLI tool for managing OCI-distributed service contracts.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Persistent flags
	root.PersistentFlags().String("config", "", "config file path")
	root.PersistentFlags().String(outputFormatKey, "text", "output format (text, json, markdown)")
	root.PersistentFlags().Bool("no-cache", false, "disable OCI bundle cache")
	root.PersistentFlags().Bool("no-anim", false, "disable animations")
	root.PersistentFlags().BoolP("verbose", "v", false, "enable verbose output")

	// A closed vocabulary the code already owns, so declaring it costs nothing
	// at runtime and turns empty completion into real completion.
	_ = root.RegisterFlagCompletionFunc(outputFormatKey, staticCompletions(outputFormats...))

	// Bind to Viper
	_ = v.BindPFlag("config", root.PersistentFlags().Lookup("config"))
	_ = v.BindPFlag(outputFormatKey, root.PersistentFlags().Lookup(outputFormatKey))
	_ = v.BindPFlag("no-cache", root.PersistentFlags().Lookup("no-cache"))
	_ = v.BindPFlag("no-anim", root.PersistentFlags().Lookup("no-anim"))
	_ = v.BindPFlag("verbose", root.PersistentFlags().Lookup("verbose"))

	// Env prefix
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	// Channel for async update check result
	updateResultCh := make(chan *update.CheckResult, 1)

	// Config file search + async update check
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Configure one logger for THIS command Execute and carry it on the
		// command context (per-invocation, never a process global). Every app
		// and pkg call site logs through it via logging.LoggerFromContext, so
		// concurrent in-process Execute calls never race on a shared logger.
		lg := logging.New(cmd.OutOrStderr(), v.GetBool("verbose"))
		cmd.SetContext(logging.WithLogger(cmd.Context(), lg))

		cfgFile := v.GetString("config")
		if cfgFile != "" {
			v.SetConfigFile(cfgFile)
		} else {
			v.SetConfigName("pacto")
			v.SetConfigType("yaml")
			v.AddConfigPath(".")
			v.AddConfigPath("$HOME/.config/pacto")
		}
		// Read config silently — it's optional
		_ = v.ReadInConfig()

		// After the config read, so a bad value in pacto.yaml is caught too.
		if err := checkOutputFormat(v.GetString(outputFormatKey)); err != nil {
			return err
		}

		if v.GetBool("no-cache") {
			// Materialize the resolved decision back onto the flag: viper also
			// answers from PACTO_NO_CACHE and the config file, and everything
			// downstream (fleetOptions) reads the flag, so this keeps one answer.
			_ = cmd.Flags().Set("no-cache", "true")
			// A nil store reads nothing, so there is no cache that could answer
			// in the registry's place and nothing to disable.
			if svc.BundleStore != nil {
				toggler, ok := svc.BundleStore.(oci.CacheDisabler)
				if !ok {
					// Silently proceeding would run the whole command against the very
					// cache the caller asked it to ignore, and report success.
					return fmt.Errorf("--no-cache: this bundle store has no cache to disable")
				}
				toggler.DisableCache()
			}
		}

		// Carry the --no-anim decision on the command context (per-invocation),
		// so concurrent in-process Execute calls never race on shared state.
		cmd.SetContext(withAnimDisabled(cmd.Context(), v.GetBool("no-anim")))

		// Start async update check
		if info.Version != "dev" && !envFlag("PACTO_NO_UPDATE_CHECK") {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						lg.Debug("update check panicked", "panic", r)
						sendUpdate(updateResultCh, nil)
					}
				}()
				sendUpdate(updateResultCh, d.checkForUpdate(info.Version))
			}()
		} else {
			// Non-blocking: cobra skips PersistentPostRunE when RunE errors, so
			// the buffer can still hold a value from a previous failed Execute.
			// Dropping the nil is correct — it only means "no update to report".
			sendUpdate(updateResultCh, nil)
		}

		return nil
	}

	// Post-run: show update notification if available
	root.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
		// Wait briefly for async check to complete (cache reads are near-instant)
		select {
		case result := <-updateResultCh:
			if result != nil && cmd.Name() != "update" && v.GetString(outputFormatKey) == "text" {
				_, _ = fmt.Fprintf(cmd.OutOrStderr(), "\nA new version of pacto is available: %s -> %s\nRun 'pacto update' to update.\n", result.CurrentVersion, result.LatestVersion)
			}
		case <-time.After(d.updateWait):
			// Check took too long (likely a network fetch) — don't delay the user
		}
		return nil
	}

	// Register subcommands
	root.AddCommand(newInitCommand(svc, v))
	root.AddCommand(newValidateCommand(svc, v))
	root.AddCommand(newDiffCommand(svc, v))
	root.AddCommand(newPackCommand(svc, v))
	root.AddCommand(newPushCommand(svc, v))
	root.AddCommand(newPullCommand(svc, v))
	root.AddCommand(newGraphCommand(svc, v))
	root.AddCommand(newFleetCommand(svc, v))
	root.AddCommand(newEvidenceCommand(svc, v))
	root.AddCommand(newAgentCommand(svc, v, info.Version, d))
	root.AddCommand(newRunCommand(svc, v, info.Version, d))
	root.AddCommand(newImpactCommand(svc, v))
	root.AddCommand(newOTelCommand(svc, v))
	root.AddCommand(newExplainCommand(svc, v))
	root.AddCommand(newDocCommand(svc, v, d))
	root.AddCommand(newGenerateCommand(svc, v))
	root.AddCommand(newLockCommand(svc, v))
	root.AddCommand(newLoginCommand(d))
	root.AddCommand(newLogoutCommand())
	root.AddCommand(newVersionCommand(info, v))
	root.AddCommand(newUpdateCommand(info.Version, update.New()))
	root.AddCommand(newMCPCommand(svc, info.Version, d))
	root.AddCommand(newDeliveryCommand(v, d))
	root.AddCommand(newDashboardCommand(svc, v, info.Version))
	root.AddCommand(newTUICommand(svc, v, d))

	attachBanner(root)
	return root
}

// sendUpdate posts an update-check result without ever blocking. The channel is
// drained by PersistentPostRunE, which cobra skips when RunE errors, so a stale
// value can still be sitting in the buffer. Dropping a result is harmless: the
// notice is advisory.
func sendUpdate(ch chan<- *update.CheckResult, r *update.CheckResult) {
	select {
	case ch <- r:
	default:
	}
}

// attachBanner prints the colored logo above the root command's help on a TTY.
func attachBanner(root *cobra.Command) {
	def := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		if !cmd.HasParent() && useColor(out) {
			_, _ = fmt.Fprint(out, bannerStatic(true)) // static colored banner, no animation
		}
		def(cmd, args)
	})
}
