package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRun_InsecureRegistriesEnv(t *testing.T) {
	t.Setenv("PACTO_INSECURE_REGISTRIES", "reg.local:5000, other:5001")
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"pacto", "version"}
	if err := run(); err != nil {
		t.Fatalf("run with insecure registries env: %v", err)
	}
}

func TestSignalContext_CancelsOnSignal(t *testing.T) {
	ctx, stop := signalContext()
	defer stop()

	// The handler is registered, so this diverts the signal to cancel ctx rather
	// than terminating the test process.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("signal context was not cancelled on SIGTERM")
	}
}

func TestRun_Version(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"pacto", "version"}

	if err := run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"pacto", "nonexistent-command"}

	if err := run(); err == nil {
		t.Error("expected error for unknown command")
	}
}

func TestMain_ErrorExit(t *testing.T) {
	if os.Getenv("TEST_PACTO_MAIN_ERROR") == "1" {
		os.Args = []string{"pacto", "nonexistent-command"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMain_ErrorExit")
	cmd.Env = append(os.Environ(), "TEST_PACTO_MAIN_ERROR=1")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Error("expected non-zero exit code")
	}
}

// pacto run ends with its command's exit code and adds no error line of its own.
func TestMain_RunExitCode(t *testing.T) {
	if dir := os.Getenv("TEST_PACTO_MAIN_RUN"); dir != "" {
		os.Args = []string{"pacto", "run", "--contract", dir, "--proc-root", dir, "--", "sh", "-c", "exit 5"}
		main()
		return
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/pacto.yaml", []byte("pactoVersion: \"2.0\"\nservice:\n  name: s\n  version: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMain_RunExitCode")
	cmd.Env = append(os.Environ(), "TEST_PACTO_MAIN_RUN="+dir)
	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 5 || strings.Contains(string(out), "exit status 5") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
}
