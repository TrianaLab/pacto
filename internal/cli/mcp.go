package cli

import (
	"fmt"
	"net/http"
	"os/signal"
	"syscall"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/internal/app"
	pactomcp "github.com/trianalab/pacto/internal/mcp"
)

func newMCPCommand(svc *app.Service, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start an MCP server",
		Long:  "Starts a Model Context Protocol (MCP) server exposing Pacto tools for AI agents. Supports stdio (default) and HTTP transports.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			transport, _ := cmd.Flags().GetString("transport")
			port, _ := cmd.Flags().GetInt("port")
			server := pactomcp.NewServer(svc, version)

			if transport == "http" {
				handler := mcpsdk.NewStreamableHTTPHandler(
					func(_ *http.Request) *mcpsdk.Server { return server },
					nil,
				)

				ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
				defer stop()

				addr := fmt.Sprintf("127.0.0.1:%d", port)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "MCP server listening on http://%s/mcp\n", addr)

				srv := &http.Server{Addr: addr}
				mux := http.NewServeMux()
				mux.Handle("/mcp", handler)
				srv.Handler = mux

				errCh := make(chan error, 1)
				go func() { errCh <- srv.ListenAndServe() }()

				select {
				case <-ctx.Done():
					return srv.Close()
				case err := <-errCh:
					return err
				}
			}

			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "MCP server running on stdio")
			return server.Run(cmd.Context(), &mcpsdk.StdioTransport{})
		},
	}

	cmd.Flags().StringP("transport", "t", "stdio", "transport type: stdio or http")
	cmd.Flags().Int("port", 8585, "port for HTTP transport")

	return cmd
}
