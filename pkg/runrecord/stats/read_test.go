package stats

import "testing"

func TestSkippedAddAndTotal(t *testing.T) {
	got := Skipped{Malformed: 1, Unreadable: 2}.Add(Skipped{Malformed: 1, Oversized: 3})
	if got != (Skipped{Malformed: 2, Oversized: 3, Unreadable: 2}) || got.Total() != 7 {
		t.Fatalf("got %+v total %d", got, got.Total())
	}
}
