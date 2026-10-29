package cli_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
)

// --closure is opt-in: the same contract passes without it and fails with it
// when a dependency cannot be resolved.
func TestValidateCommand_ClosureFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `pactoVersion: "2.0"
service:
  name: payments
  version: 1.0.0
dependencies:
  - name: ledger
    ref: ../ledger
    compatibility: ^1.0.0
    required: true
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		args    []string
		wantErr bool
	}{
		{args: []string{"validate", dir}, wantErr: false},
		{args: []string{"validate", "--closure", dir}, wantErr: true},
	} {
		root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "test"})
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(tt.args)
		if err := root.Execute(); (err != nil) != tt.wantErr {
			t.Errorf("%v: err = %v, wantErr %v", tt.args, err, tt.wantErr)
		}
	}
}
