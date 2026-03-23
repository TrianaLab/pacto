// Command genbundle exports generated bundle artifacts to stdout.
// Usage: genbundle config-schema
package main

import (
	"fmt"
	"os"

	"github.com/trianalab/pacto/pkg/dashboard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genbundle config-schema")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "config-schema":
		data, err := dashboard.ExportConfigSchema()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(data)
		_, _ = os.Stdout.Write([]byte("\n"))
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}
