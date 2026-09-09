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
