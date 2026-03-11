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
	if !strings.Contains(output, "stdio") {
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

func TestMCPCommand_Flags(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")

	for _, cmd := range root.Commands() {
		if cmd.Name() == "mcp" {
			transportFlag := cmd.Flags().Lookup("transport")
			if transportFlag == nil {
				t.Fatal("expected --transport flag")
			}
			if transportFlag.DefValue != "stdio" {
				t.Errorf("expected default transport=stdio, got %s", transportFlag.DefValue)
			}
			if transportFlag.Shorthand != "t" {
				t.Errorf("expected shorthand -t, got %s", transportFlag.Shorthand)
			}

			portFlag := cmd.Flags().Lookup("port")
			if portFlag == nil {
				t.Fatal("expected --port flag")
			}
			if portFlag.DefValue != "8585" {
				t.Errorf("expected default port=8585, got %s", portFlag.DefValue)
			}
			return
		}
	}
	t.Error("mcp command not found")
}
