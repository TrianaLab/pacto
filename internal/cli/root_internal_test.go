package cli

import (
	"bytes"
	"testing"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/update"
)

func TestNewRootCommand_PanicRecovery(t *testing.T) {
	old := checkForUpdateFn
	checkForUpdateFn = func(string) *update.CheckResult {
		panic("injected panic for test")
	}
	defer func() { checkForUpdateFn = old }()

	t.Setenv("PACTO_NO_UPDATE_CHECK", "")

	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "v1.0.0"})
	root.SetArgs([]string{"version"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
