package cli

import (
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/doc"
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

  # Launch an interactive API explorer (Swagger/Scalar UI)
  pacto doc my-service --swagger

  # Point try-it-out requests to a running backend
  pacto doc my-service --swagger --target http://localhost:3000`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				path = args[0]
			}

			output, _ := cmd.Flags().GetString("output")
			serve, _ := cmd.Flags().GetBool("serve")
			swagger, _ := cmd.Flags().GetBool("swagger")
			port, _ := cmd.Flags().GetInt("port")
			target, _ := cmd.Flags().GetString("target")

			if err := validateDocFlags(serve, swagger, output); err != nil {
				return err
			}

			result, err := svc.Doc(cmd.Context(), app.DocOptions{
				Path:      path,
				OutputDir: output,
			})
			if err != nil {
				return err
			}

			if serve || swagger {
				ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
				defer stop()

				addr := fmt.Sprintf("http://127.0.0.1:%d", port)

				if swagger {
					specs := doc.CollectSwaggerSpecs(result.Bundle.Contract.Interfaces)
					if len(specs) == 0 {
						return fmt.Errorf("no HTTP interfaces with OpenAPI contracts found")
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Serving API explorer at %s\nPress Ctrl+C to stop\n", addr)
					return doc.ServeSwagger(ctx, doc.SwaggerOptions{
						Specs:  specs,
						FS:     result.Bundle.FS,
						Title:  result.ServiceName,
						Port:   port,
						Target: target,
					})
				}

				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Serving documentation at %s\nPress Ctrl+C to stop\n", addr)
				return doc.Serve(ctx, result.Markdown, result.ServiceName, port)
			}

			format := v.GetString(outputFormatKey)
			return printDocResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output directory for generated Markdown file")
	cmd.Flags().Bool("serve", false, "start a local HTTP server to view documentation in the browser")
	cmd.Flags().Bool("swagger", false, "start a local API explorer with interactive Swagger UI")
	cmd.Flags().Int("port", 8484, "port for the documentation server (used with --serve or --swagger)")
	cmd.Flags().String("target", "", "target server URL for try-it-out requests (used with --swagger)")

	return cmd
}

func validateDocFlags(serve, swagger bool, output string) error {
	if serve && swagger {
		return fmt.Errorf("--serve and --swagger are mutually exclusive")
	}
	if (serve || swagger) && output != "" {
		return fmt.Errorf("--serve/--swagger and --output are mutually exclusive")
	}
	return nil
}
