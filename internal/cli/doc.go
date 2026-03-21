package cli

import (
	"fmt"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/pkg/doc"
)

func newDocCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc [dir | oci://ref]",
		Short: "Generate Markdown documentation from a contract",
		Long:  "Reads a pacto.yaml in the given directory (or oci:// reference) and generates structured Markdown documentation.",
		Example: `  # Print documentation to stdout
  pacto doc my-service

  # Write documentation to a file
  pacto doc my-service -o docs/

  # Serve documentation in the browser
  pacto doc my-service --serve

  # Serve on a custom port
  pacto doc my-service --serve --port 9090

  # Launch an interactive API explorer (Scalar UI)
  pacto doc my-service --ui swagger

  # Select a specific interface
  pacto doc my-service --ui swagger --interface public-api

  # Point try-it-out requests to a running backend
  pacto doc my-service --ui swagger --target http://localhost:3000

  # Per-interface target mapping
  pacto doc my-service --ui swagger --target public-api=http://localhost:3000 --target admin-api=http://localhost:3001`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				path = args[0]
			}

			output, _ := cmd.Flags().GetString("output")
			serve, _ := cmd.Flags().GetBool("serve")
			ui, _ := cmd.Flags().GetString("ui")
			iface, _ := cmd.Flags().GetString("interface")
			port, _ := cmd.Flags().GetInt("port")
			targets, _ := cmd.Flags().GetStringArray("target")

			if err := validateDocFlags(serve, ui, output, iface); err != nil {
				return err
			}

			result, err := svc.Doc(cmd.Context(), app.DocOptions{
				Path:      path,
				OutputDir: output,
				Overrides: getOverrides(cmd),
			})
			if err != nil {
				return err
			}

			if ui != "" {
				return serveUI(cmd, result, ui, iface, port, targets)
			}
			if serve {
				ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
				defer stop()
				addr := fmt.Sprintf("http://127.0.0.1:%d", port)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Serving documentation at %s\nPress Ctrl+C to stop\n", addr)
				return doc.Serve(ctx, result.Markdown, result.ServiceName, port)
			}

			format := v.GetString(outputFormatKey)
			return printDocResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output directory for generated Markdown file")
	cmd.Flags().Bool("serve", false, "start a local HTTP server to view documentation in the browser")
	cmd.Flags().String("ui", "", "UI type for interactive API explorer (e.g. swagger)")
	cmd.Flags().String("interface", "", "interface name to display (used with --ui)")
	cmd.Flags().Int("port", 8484, "port for the documentation server (used with --serve or --ui)")
	cmd.Flags().StringArray("target", nil, "target server URL for try-it-out requests; supports interface=url mapping (used with --ui)")

	addOverrideFlags(cmd)

	return cmd
}

func serveUI(cmd *cobra.Command, result *app.DocResult, ui, iface string, port int, targets []string) error {
	_ = ui // reserved for future UI types (e.g. redoc)

	specs := doc.CollectSwaggerSpecs(result.Bundle.Contract.Interfaces)
	if len(specs) == 0 {
		return fmt.Errorf("no HTTP interfaces with OpenAPI contracts found")
	}

	if iface != "" {
		filtered := doc.FilterSpecs(specs, iface)
		if len(filtered) == 0 {
			return fmt.Errorf("interface %q not found among OpenAPI interfaces", iface)
		}
		specs = filtered
	}

	globalTarget, ifaceTargets := parseTargets(targets)

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf("http://127.0.0.1:%d", port)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Serving API explorer at %s\nPress Ctrl+C to stop\n", addr)

	return doc.ServeSwagger(ctx, doc.SwaggerOptions{
		Specs:   specs,
		FS:      result.Bundle.FS,
		Title:   result.ServiceName,
		Port:    port,
		Target:  globalTarget,
		Targets: ifaceTargets,
	})
}

func validateDocFlags(serve bool, ui, output, iface string) error {
	if serve && ui != "" {
		return fmt.Errorf("--serve and --ui are mutually exclusive")
	}
	if (serve || ui != "") && output != "" {
		return fmt.Errorf("--serve/--ui and --output are mutually exclusive")
	}
	if iface != "" && ui == "" {
		return fmt.Errorf("--interface requires --ui")
	}
	return nil
}

// parseTargets splits target values into a global target and per-interface
// targets. A value like "http://host:port" is a global target that applies
// to all interfaces. A value like "api=http://host:port" maps to a specific
// interface.
func parseTargets(targets []string) (string, map[string]string) {
	var global string
	var ifaceTargets map[string]string
	for _, t := range targets {
		if idx := strings.Index(t, "="); idx > 0 && !strings.Contains(t[:idx], "://") {
			if ifaceTargets == nil {
				ifaceTargets = make(map[string]string)
			}
			ifaceTargets[t[:idx]] = t[idx+1:]
		} else {
			global = t
		}
	}
	return global, ifaceTargets
}
