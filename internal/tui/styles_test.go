package tui

import "testing"

func TestStatusStyle(t *testing.T) {
	tests := []struct {
		status      string
		wantStyle   string
		description string
	}{
		{"compliant", "ok", "compliant renders as ok"},
		{"ok", "ok", "ok renders as ok"},
		{"healthy", "ok", "healthy renders as ok"},
		{"ready", "ok", "ready renders as ok"},
		{"non-compliant", "error", "non-compliant renders as error"},
		{"invalid", "error", "invalid renders as error"},
		{"error", "error", "error renders as error"},
		{"failed", "error", "failed renders as error"},
		{"unknown", "warn", "unknown renders as warn"},
		{"stale", "warn", "stale renders as warn"},
		{"degraded", "warn", "degraded renders as warn"},
		{"unrecognised", "plain", "unrecognised renders as plain"},
		{"", "plain", "empty string renders as plain"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			got := statusStyle(tt.status)
			var want string
			switch tt.wantStyle {
			case "ok":
				want = okStyle.String()
			case "error":
				want = errorStyle.String()
			case "warn":
				want = warnStyle.String()
			case "plain":
				want = ""
			}
			if got.String() != want {
				t.Errorf("statusStyle(%q) style mismatch", tt.status)
			}
		})
	}
}

func TestSafeTextEscapesEveryControlCharacter(t *testing.T) {
	for _, tt := range []struct{ name, in, want string }{
		{"ordinary text is untouched", "payments-api", "payments-api"},
		{"non-ASCII is untouched", "pagos-españa", "pagos-españa"},
		{"escape becomes caret bracket", "a\x1b[2Kb", "a^[[2Kb"},
		{"a carriage return cannot return the cursor", "a\rb", "a^Mb"},
		{"a newline cannot break the row", "a\nb", "a^Jb"},
		{"a NUL is visible rather than swallowed", "a\x00b", "a^@b"},
		{"DEL has its own caret spelling", "a\x7fb", "a^?b"},
		{"8-bit CSI is escaped, because the frame's parser acts on it", "a\u009b2Kb", `a\x9b2Kb`},
		{"8-bit OSC is escaped too", "a\u009db", `a\x9db`},
		{"the C1 range starts at U+0080", "a\u0080b", `a\x80b`},
		{"U+009F is the last C1", "a\u009fb", `a\x9fb`},
		{"U+00A0 is past C1 and stays", "a\u00a0b", "a\u00a0b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeText(tt.in); got != tt.want {
				t.Fatalf("safeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
