package discover

import "testing"

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{"risk": "risk", "1.0.0": "1.0.0", "1.0": "'1.0'", "yes": "'yes'", "it's": "'it''s'"} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}
