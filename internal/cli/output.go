package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/graph"
)

// formatResult dispatches between JSON and text output. If format is "json",
// it encodes result as indented JSON. Otherwise it calls the textFn formatter.
func formatResult(cmd *cobra.Command, format string, result any, textFn func() error) error {
	if format == "json" {
		return printJSON(cmd, result)
	}
	return textFn()
}

func printInitResult(cmd *cobra.Command, result *app.InitResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created %s/\n", result.Dir)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", result.Path)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s/interfaces/\n", result.Dir)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s/configuration/\n", result.Dir)
		return nil
	})
}

func printValidateResult(cmd *cobra.Command, result *app.ValidateResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		if result.Valid {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is valid\n", result.Path)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is invalid\n", result.Path)
		}

		for _, e := range result.Errors {
			_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  ERROR [%s] %s: %s\n", e.Code, e.Path, e.Message)
		}

		for _, w := range result.Warnings {
			_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  WARN  [%s] %s: %s\n", w.Code, w.Path, w.Message)
		}

		return nil
	})
}

func printPackResult(cmd *cobra.Command, result *app.PackResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Packed %s@%s -> %s\n", result.Name, result.Version, result.Output)
		return nil
	})
}

func printPushResult(cmd *cobra.Command, result *app.PushResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pushed %s@%s -> %s\n", result.Name, result.Version, result.Ref)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Digest: %s\n", result.Digest)
		return nil
	})
}

func printPullResult(cmd *cobra.Command, result *app.PullResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pulled %s@%s -> %s/\n", result.Name, result.Version, result.Output)
		return nil
	})
}

func printDiffResult(cmd *cobra.Command, result *app.DiffResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Classification: %s\n", result.Classification)
		if len(result.Changes) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No changes detected.")
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Changes (%d):\n", len(result.Changes))
		for _, c := range result.Changes {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s (%s): %s\n",
				c.Classification, c.Path, c.Type, c.Reason)
		}

		return nil
	})
}

func printGraphResult(cmd *cobra.Command, result *app.GraphResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%s@%s\n", result.Root.Name, result.Root.Version)
		printDeps(cmd, result.Root.Dependencies, "")

		if len(result.Cycles) > 0 {
			_, _ = fmt.Fprintf(w, "\nCycles (%d):\n", len(result.Cycles))
			for _, cycle := range result.Cycles {
				_, _ = fmt.Fprintf(w, "  ")
				for i, ref := range cycle {
					if i > 0 {
						_, _ = fmt.Fprintf(w, " -> ")
					}
					_, _ = fmt.Fprintf(w, "%s", ref)
				}
				_, _ = fmt.Fprintln(w)
			}
		}

		if len(result.Conflicts) > 0 {
			_, _ = fmt.Fprintf(w, "\nConflicts (%d):\n", len(result.Conflicts))
			for _, c := range result.Conflicts {
				_, _ = fmt.Fprintf(w, "  %s: %v\n", c.Name, c.Versions)
			}
		}

		return nil
	})
}

func printDeps(cmd *cobra.Command, edges []graph.Edge, indent string) {
	w := cmd.OutOrStdout()
	for _, edge := range edges {
		if edge.Error != "" {
			_, _ = fmt.Fprintf(w, "%s  ! %s (error: %s)\n", indent, edge.Ref, edge.Error)
			continue
		}
		if edge.Node != nil {
			_, _ = fmt.Fprintf(w, "%s  - %s@%s (%s)\n", indent, edge.Node.Name, edge.Node.Version, edge.Ref)
			printDeps(cmd, edge.Node.Dependencies, indent+"  ")
		} else {
			_, _ = fmt.Fprintf(w, "%s  - %s\n", indent, edge.Ref)
		}
	}
}

func printExplainResult(cmd *cobra.Command, result *app.ExplainResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Service: %s@%s\n", result.Name, result.Version)
		if result.Owner != "" {
			_, _ = fmt.Fprintf(w, "Owner: %s\n", result.Owner)
		}
		_, _ = fmt.Fprintf(w, "Pacto Version: %s\n", result.PactoVersion)

		_, _ = fmt.Fprintf(w, "\nRuntime:\n")
		_, _ = fmt.Fprintf(w, "  Workload: %s\n", result.Runtime.WorkloadType)
		_, _ = fmt.Fprintf(w, "  State: %s\n", result.Runtime.StateType)
		_, _ = fmt.Fprintf(w, "  Persistence: %s/%s\n", result.Runtime.Scope, result.Runtime.Durability)
		_, _ = fmt.Fprintf(w, "  Data Criticality: %s\n", result.Runtime.DataCriticality)

		if len(result.Interfaces) > 0 {
			_, _ = fmt.Fprintf(w, "\nInterfaces (%d):\n", len(result.Interfaces))
			for _, iface := range result.Interfaces {
				if iface.Port != nil {
					_, _ = fmt.Fprintf(w, "  - %s (%s, port %d", iface.Name, iface.Type, *iface.Port)
				} else {
					_, _ = fmt.Fprintf(w, "  - %s (%s", iface.Name, iface.Type)
				}
				if iface.Visibility != "" {
					_, _ = fmt.Fprintf(w, ", %s", iface.Visibility)
				}
				_, _ = fmt.Fprintln(w, ")")
			}
		}

		if len(result.Dependencies) > 0 {
			_, _ = fmt.Fprintf(w, "\nDependencies (%d):\n", len(result.Dependencies))
			for _, dep := range result.Dependencies {
				req := "optional"
				if dep.Required {
					req = "required"
				}
				_, _ = fmt.Fprintf(w, "  - %s (%s, %s)\n", dep.Ref, dep.Compatibility, req)
			}
		}

		if result.Scaling != nil {
			_, _ = fmt.Fprintf(w, "\nScaling: %d-%d\n", result.Scaling.Min, result.Scaling.Max)
		}

		return nil
	})
}

func printGenerateResult(cmd *cobra.Command, result *app.GenerateResult, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Generated %d file(s) using %s\n", result.FilesCount, result.Plugin)
		_, _ = fmt.Fprintf(w, "Output: %s/\n", result.OutputDir)
		if result.Message != "" {
			_, _ = fmt.Fprintf(w, "Message: %s\n", result.Message)
		}
		return nil
	})
}

func printJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
