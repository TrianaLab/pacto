package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/cli"
)

func TestMCPCommand_Help(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"mcp", "--help"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("mcp --help failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Model Context Protocol") {
		t.Errorf("expected MCP description, got: %s", output)
	}
	if !strings.Contains(output, "stdin/stdout") {
		t.Errorf("expected stdio mention, got: %s", output)
	}
}

func TestMCPCommand_Registered(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")

	found := false
	for _, cmd := range root.Commands() {
		if cmd.Name() == "mcp" {
			found = true
			break
		}
	}
	if !found {
		t.Error("mcp command not registered")
	}
}

func TestMCPCommand_NoArgs(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"mcp", "extra-arg"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error for extra arguments")
	}
}
