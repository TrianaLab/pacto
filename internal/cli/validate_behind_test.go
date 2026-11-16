package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// A contract behind the current pactoVersion is told so as INFO, and the run
// still passes.
func TestValidateCommandBehindExitsZero(t *testing.T) {
	dir := t.TempDir()
	prev := contract.SupportedVersions[len(contract.SupportedVersions)-2]
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: '"+prev+"'\nservice: {name: s, version: 1.0.0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		var out bytes.Buffer
		root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "test"})
		root.SetOut(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	if text := run("validate", dir); !strings.Contains(text, "is valid") || !strings.Contains(text, "  INFO  [CONTRACT_BEHIND] pactoVersion: ") {
		t.Fatalf("text:\n%s", text)
	}
	var res struct {
		Valid    bool `json:"valid"`
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(run("validate", "--output-format", "json", dir)), &res); err != nil || !res.Valid || len(res.Warnings) != 1 || res.Warnings[0].Code != "CONTRACT_BEHIND" {
		t.Fatalf("json: %+v %v", res, err)
	}
}
