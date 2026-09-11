package tui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// TestStatusStyle asserts against the vocabularies EntityRef.Status actually
// carries, taken from the fleet constants rather than retyped.
//
// Its predecessor asserted on "ok", "healthy", "ready", "failed" and
// "degraded" -- five values the fleet has never produced -- and on lower-case
// spellings of three it does. It passed because the code it tested made the
// same mistake, so a status column that rendered every row in the default
// style was green in CI for as long as it existed. Sourcing the keys from
// pkg/fleet is what stops the pair drifting together again.
func TestStatusStyle(t *testing.T) {
	for _, tt := range []struct {
		status string
		want   color.Color
	}{
		{fleet.StatusInvalid, colRed},
		{fleet.StatusNonCompliant, colOrange},
		{fleet.StatusUnknown, colYellow},
		{fleet.StatusWarning, colAmber},
		{fleet.StatusCompliant, colGreen},
		{fleet.StatusReference, colCyan},
		{fleet.StatusNotEvaluated, colGrey},
		{string(fleet.SourceUnavailable), colRed},
		{string(fleet.SourcePartial), colAmber},
		{string(fleet.SourceStale), colYellow},
		{string(fleet.SourceAvailable), colGreen},
		// Neither vocabulary, and the empty string a target without a verdict
		// carries: grey, because an unrecognised status is not a pass.
		{"unrecognised", colGrey},
		{"", colGrey},
	} {
		t.Run(tt.status, func(t *testing.T) {
			if got := statusPresentationFor(tt.status).Color; got != tt.want {
				t.Errorf("statusPresentationFor(%q).Color = %v, want %v", tt.status, got, tt.want)
			}
			want := style{lipgloss.NewStyle().Foreground(tt.want)}.String()
			if got := statusStyle(tt.status).String(); got != want {
				t.Errorf("statusStyle(%q) = %q, want %q", tt.status, got, want)
			}
		})
	}
}

// TestStatusPresentationRanksBySeverity pins the order the banner and the
// attention filter both read: worst first, and every rank distinct within the
// compliance vocabulary.
func TestStatusPresentationRanksBySeverity(t *testing.T) {
	worstFirst := []string{
		fleet.StatusInvalid, fleet.StatusNonCompliant, fleet.StatusUnknown,
		fleet.StatusWarning, fleet.StatusCompliant, fleet.StatusReference,
		fleet.StatusNotEvaluated,
	}
	for i := 1; i < len(worstFirst); i++ {
		prev, cur := statusPresentationFor(worstFirst[i-1]).Rank, statusPresentationFor(worstFirst[i]).Rank
		if prev >= cur {
			t.Fatalf("rank(%s)=%d is not more severe than rank(%s)=%d",
				worstFirst[i-1], prev, worstFirst[i], cur)
		}
	}
	if r := statusPresentationFor("unrecognised").Rank; r <= statusPresentationFor(fleet.StatusNotEvaluated).Rank {
		t.Fatalf("an unrecognised status ranks %d, want it last", r)
	}
}

// TestNeedsAttentionIsConfirmedProblemsOnly guards the line the pulse keys off:
// a state that means "could not observe" must not throb like one that means
// "observed a contradiction".
func TestNeedsAttentionIsConfirmedProblemsOnly(t *testing.T) {
	for _, s := range []string{fleet.StatusInvalid, fleet.StatusNonCompliant, string(fleet.SourceUnavailable)} {
		if !needsAttention(s) {
			t.Errorf("needsAttention(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		fleet.StatusUnknown, fleet.StatusWarning, fleet.StatusCompliant,
		fleet.StatusReference, fleet.StatusNotEvaluated,
		string(fleet.SourceStale), string(fleet.SourcePartial), string(fleet.SourceAvailable),
		"unrecognised", "",
	} {
		if needsAttention(s) {
			t.Errorf("needsAttention(%q) = true, want false", s)
		}
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
		{"a right-to-left override cannot reorder the line", "a\u202eb", `a\u202eb`},
		{"U+202A is the first bidi embedding code", "a\u202ab", `a\u202ab`},
		{"U+2066 is the first bidi isolate", "a\u2066b", `a\u2066b`},
		{"U+2069 is the last bidi isolate", "a\u2069b", `a\u2069b`},
		{"U+2065 is below the isolates and stays", "a\u2065b", "a\u2065b"},
		{"U+206A is above the isolates and stays", "a\u206ab", "a\u206ab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeText(tt.in); got != tt.want {
				t.Fatalf("safeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
