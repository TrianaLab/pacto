package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// newCICommand builds `pacto ci [root]`: plan every service of a repository
// for a CI run. It reads the registry and writes nothing to it.
func newCICommand(svc *app.Service, v *viper.Viper, _ deps, rec *runRecorder) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci [root]",
		Short: "Plan every service of a repository for a CI run",
		Long: "Walks root (default .) for pacto.yaml, skipping the folders the root .pactoignore names, and " +
			"prints one plan: the CI platform, the registry and the base, each with its source, then one row per service: " +
			"whether it changed against its published baseline, the version it would publish, the classification of the change " +
			"and its impact on the consumers the fleet source flags name.\n\n" +
			"On GitHub Actions the plan is also appended to $GITHUB_STEP_SUMMARY; on GitLab CI it is written to " +
			"pacto-plan.md in $CI_PROJECT_DIR. --output-format json writes the plan as one JSON document.\n\n" +
			"The run is a plan: it reads the registry and the consumer sources you name and writes nothing to them. " +
			"A run record goes to --stats-file only, one file per impacted service.\n\n" +
			"The exit status follows the plan: 0 clear, 1 blocked, 2 unverified. A repository with no pacto.yaml, or a folder it cannot read, exits 2.",
		Example: `  # Plan every service of this repository against a registry
  pacto ci --registry ghcr.io/acme

  # Check the impact on the consumers a target-state file names, as JSON
  pacto ci --registry ghcr.io/acme --target-state targets.yaml --output-format json

  # Why the payments row reads as it does
  pacto ci --registry ghcr.io/acme --explain payments`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			registry, _ := cmd.Flags().GetString("registry")
			explain, _ := cmd.Flags().GetString("explain")
			fo := fleetOptions(cmd)
			if !cmd.Flags().Changed("local") {
				fo.LocalRoots = nil // CIPlan reads the root
			}
			if !cmd.Flags().Changed("external-catalog") {
				// In CI the working directory is the repository, so a config
				// file there is pull-request content: the organization catalog
				// (a host read with the registry credentials) comes from the
				// flag or the environment only.
				fo.ExternalCatalogRef = os.Getenv("PACTO_EXTERNAL_CATALOG")
			}
			svc.ExternalCatalogRef = fo.ExternalCatalogRef
			ciStatsNote(cmd)
			revs := map[string]*revisions{}
			bundles := map[string]*contract.Bundle{}
			p, results, err := svc.CIPlan(cmd.Context(), app.CIOptions{Root: root, Registry: registry, Fleet: fo, Getenv: os.Getenv, OnResolved: ciHook(cmd, rec, revs), Bundles: bundles})
			if err != nil {
				return err
			}
			row, err := explainRow(p, explain)
			if err != nil {
				return err
			}
			writeCISummaries(cmd, ci.Detect(os.Getenv), p)
			ciRecords(cmd, rec, revs, results)
			code, reason := p.Exit, p.Reason
			printed := p
			if row != nil {
				// --explain exits with the row's code, so the JSON document
				// says the row's gate, exit and reason too.
				code, reason = gateExit[row.Impact.Gate], ""
				if code != 0 {
					reason = row.Service + ": " + row.Impact.Text
				}
				q := *p
				q.Gate, q.Exit, q.Reason = row.Impact.Gate, code, reason
				printed = &q
			}
			if err := printCI(cmd, svc, v.GetString(outputFormatKey), printed, row, results[explain], root, bundles); err != nil {
				return err
			}
			if code != 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), reason)
			}
			return exitStatus(code, reason)
		},
	}
	cmd.Flags().String("registry", "", "registry the services publish to, for example ghcr.io/acme; default from the CI platform")
	cmd.Flags().String("explain", "", "explain one service's row: its folder, baseline, why it changed, its inferred fields and its consumers")
	addFleetSourceFlags(cmd.Flags())
	addStatsFlags(cmd)
	return cmd
}

// gateExit is the exit code of each gate verdict.
var gateExit = map[string]int{impact.GateClear: 0, impact.GateBlocked: 1, impact.GateUnverified: 2}

// exitStatus is the error that ends the process with code and reason, nil for 0.
func exitStatus(code int, reason string) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code, Reason: reason}
}

// explainRow is the row --explain names, nil when it names none, or an error
// listing the services the plan found.
func explainRow(p *ci.Plan, service string) (*ci.Row, error) {
	if service == "" {
		return nil, nil
	}
	var names []string
	for i, r := range p.Rows {
		if r.Service == service {
			return &p.Rows[i], nil
		}
		if r.Service != "" {
			names = append(names, r.Service)
		}
	}
	slices.Sort(names)
	return nil, fmt.Errorf("no service %q in the plan; services: %s", service, strings.Join(names, ", "))
}

// printCI writes the plan, or the one row --explain names, in format.
func printCI(cmd *cobra.Command, svc *app.Service, format string, p *ci.Plan, row *ci.Row, res *impact.Result, root string, bundles map[string]*contract.Bundle) error {
	w := cmd.OutOrStdout()
	text := func() error { ci.Text(w, p); return nil }
	markdown := func() error { _, err := io.WriteString(w, ci.Markdown(p)); return err }
	if row != nil {
		text = func() error { return printCIExplain(cmd, svc, *row, res, root, bundles[row.Dir]) }
		markdown = nil
	}
	return formatResult(cmd, format, p, text, markdown)
}

// printCIExplain writes why row reads as it does: where it was read and
// judged, why it changed, its inferred fields with their sources and the
// YAML that declares them, and the consumers impact found.
// b is the bundle the plan judged, nil when the folder could not be read.
func printCIExplain(cmd *cobra.Command, svc *app.Service, row ci.Row, res *impact.Result, root string, b *contract.Bundle) error {
	w := cmd.OutOrStdout()
	for _, kv := range [][2]string{
		{"Service", row.Service}, {"Folder", row.Dir}, {"Repository", row.Repository},
		{"Baseline", strings.TrimSpace(row.Baseline + " " + row.BaselineDigest)}, {"Changed", strings.TrimSpace(row.Changed + " " + row.Reason)},
		{"Classification", row.Classification}, {"Impact", row.Impact.Text},
	} {
		if kv[1] != "" {
			_, _ = fmt.Fprintf(w, "%s: %s\n", kv[0], kv[1])
		}
	}
	for _, e := range row.Errors {
		_, _ = fmt.Fprintf(w, "error: %s\n", e)
	}
	for _, n := range row.Notes {
		_, _ = fmt.Fprintf(w, "note: %s\n", n)
	}
	er, err := svc.Explain(cmd.Context(), app.ExplainOptions{Path: filepath.Join(root, row.Dir), Bundle: b})
	if err != nil {
		return err
	}
	printExplainInferred(w, er)
	if res != nil {
		_, _ = fmt.Fprintln(w)
		printImpactConsumers(w, res)
	}
	return nil
}

// writeCISummaries appends the plan to GitHub's step summary and writes it
// to GitLab's pacto-plan.md, when the platform says where. A failure is a
// warning: it never changes the plan's exit code.
func writeCISummaries(cmd *cobra.Command, cctx ci.Context, p *ci.Plan) {
	md := ci.Markdown(p)
	if cctx.Summary != "" {
		warnIf(cmd, "step summary", appendFile(cctx.Summary, md))
	}
	if cctx.ArtifactDir != "" {
		warnIf(cmd, "pacto-plan.md", os.WriteFile(filepath.Join(cctx.ArtifactDir, "pacto-plan.md"), []byte(md), 0o644))
	}
}

// appendFile appends data to path, creating it.
func appendFile(path, data string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(data)
	return errors.Join(err, f.Close())
}

// warnIf warns on stderr that what was not written, when err is set.
func warnIf(cmd *cobra.Command, what string, err error) {
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "pacto: warning: %s not written: %s\n", what, ci.Redact(err.Error()))
	}
}

// ciStatsNote says a registry run record is skipped: pacto ci is a plan,
// and a plan writes nothing to a registry.
func ciStatsNote(cmd *cobra.Command) {
	if reg, _ := statsTargets(cmd); reg != "" {
		from := "PACTO_STATS_REGISTRY"
		if cmd.Flags().Changed("stats-registry") {
			from = "--stats-registry"
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "pacto: note: plan mode, run record not published (%s ignored)\n", from)
	}
}

// ciHook gives each impacted service a hook that records its revisions in
// revs, when a run record file is asked for.
func ciHook(cmd *cobra.Command, rec *runRecorder, revs map[string]*revisions) func(string) func(old, new *contract.Bundle) {
	if _, file := statsTargets(cmd); file == "" {
		return nil
	}
	return func(service string) func(old, new *contract.Bundle) {
		r := &revisions{}
		revs[service] = r
		return rec.hook(cmd, r)
	}
}

// ciRecords writes one impact run record per impacted service, to
// <stem>.<service><ext> beside --stats-file, since a record file holds one
// record. It never publishes: pacto stats publish takes the files.
func ciRecords(cmd *cobra.Command, rec *runRecorder, revs map[string]*revisions, results map[string]*impact.Result) {
	_, file := statsTargets(cmd)
	if file == "" {
		return
	}
	ext := filepath.Ext(file)
	for _, service := range slices.Sorted(maps.Keys(results)) {
		r := revs[service]
		if digestFailed(cmd, r) {
			continue
		}
		out := impactRecord(*r, results[service])
		rec.write(cmd, rec.stamp(cmd, *out), "", strings.TrimSuffix(file, ext)+"."+service+ext)
	}
}
