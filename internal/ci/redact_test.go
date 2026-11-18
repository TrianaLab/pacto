package ci

import "testing"

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"targets.yaml":                                 "targets.yaml",
		"--evidence-url https://e.example/x":           "--evidence-url https://e.example/x",
		"--evidence-url https://u:p@e.example/x":       "--evidence-url https://e.example/x (redacted)",
		"get https://e.example/x?token=s3cret failed":  "get https://e.example/x (redacted) failed",
		"get \"https://u@e.example/?a=1\" and http://": "get \"https://e.example/ (redacted)\" and http://",
		"bad https://[::1/x?t=1":                       "bad https://[::1/x?t=1",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
