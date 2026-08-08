// Command genbundle exports generated bundle artifacts to stdout.
// Usage: genbundle config-schema | dashboard-openapi
package main

import (
	"fmt"
	"os"

	"github.com/trianalab/pacto/v3/pkg/dashboard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genbundle config-schema | dashboard-openapi")
		os.Exit(1)
	}
	var (
		data []byte
		err  error
	)
	switch os.Args[1] {
	case "config-schema":
		data, err = dashboard.ExportConfigSchema()
	case "dashboard-openapi":
		// The deterministic OpenAPI contract that feeds the generated TypeScript SDK
		// (make generate-dashboard-sdk). Huma marshals schemas with sorted keys, so
		// re-running produces byte-identical output, which the SDK drift gate relies on.
		data, err = dashboard.ExportOpenAPI()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(data)
	_, _ = os.Stdout.Write([]byte("\n"))
}
