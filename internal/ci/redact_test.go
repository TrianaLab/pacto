package ci

import "testing"

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"targets.yaml":                                 "targets.yaml",
		"--evidence-url https://e.example/x":           "--evidence-url https://e.example/x",
		"--evidence-url https://u:p@e.example/x":       "--evidence-url https://e.example/x (redacted)",
		"get https://e.example/x?token=s3cret failed":  "get https://e.example/x (redacted) failed",
		"get \"https://u@e.example/?a=1\" and http://": "get \"https://e.example/ (redacted)\" and http://",
		"bad https://[::1/x?t=1":                       "bad https://[::1/x (redacted)",
		"https://ci:p%ss@evidence.acme/?token=s":       "https://evidence.acme/ (redacted)",
		"https://ci:tok@evidence.acme:abc/":            "https://evidence.acme:abc/ (redacted)",
		"https://evidence/x#access_token=s":            "https://evidence/x (redacted)",
		"https://e.example/@user/x":                    "https://e.example/@user/x",
		"https://u:p@h.example?t=1":                    "https://h.example (redacted)",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
