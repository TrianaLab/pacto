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
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				path = args[0]
			}

			output, _ := cmd.Flags().GetString("output")
			serve, _ := cmd.Flags().GetBool("serve")
			port, _ := cmd.Flags().GetInt("port")

			if serve && output != "" {
				return fmt.Errorf("--serve and --output are mutually exclusive")
			}

			result, err := svc.Doc(cmd.Context(), app.DocOptions{
				Path:      path,
				OutputDir: output,
			})
			if err != nil {
				return err
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
	cmd.Flags().Int("port", 8484, "port for the documentation server (used with --serve)")

	return cmd
}
