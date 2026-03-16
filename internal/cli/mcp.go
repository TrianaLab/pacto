package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

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
		Example: `  # Start MCP server over stdio (default)
  pacto mcp

  # Start MCP server over HTTP
  pacto mcp -t http

  # Start MCP server on a custom port
  pacto mcp -t http --port 9090`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			transport, _ := cmd.Flags().GetString("transport")
			port, _ := cmd.Flags().GetInt("port")
			server := pactomcp.NewServer(svc, version)
			return runMCPServer(cmd.Context(), server, transport, port, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().StringP("transport", "t", "stdio", "transport type: stdio or http")
	cmd.Flags().Int("port", 8585, "port for HTTP transport")

	return cmd
}

func runMCPServer(ctx context.Context, server *mcpsdk.Server, transport string, port int, stderr io.Writer) error {
	if transport == "http" {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "MCP server listening on http://%s/mcp\n", addr)
		return serveHTTP(ctx, server, listener)
	}
	_, _ = fmt.Fprintln(stderr, "MCP server running on stdio")
	return server.Run(ctx, &mcpsdk.StdioTransport{})
}

func serveHTTP(ctx context.Context, server *mcpsdk.Server, listener net.Listener) error {
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcpsdk.Server { return server },
		nil,
	)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			handler.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		return srv.Close()
	case err := <-errCh:
		return err
	}
}
