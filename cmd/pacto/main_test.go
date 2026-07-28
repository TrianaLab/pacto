package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

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
