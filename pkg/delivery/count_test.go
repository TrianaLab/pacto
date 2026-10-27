package delivery

import "testing"

func TestCountOf(t *testing.T) {
	for n, want := range map[int]string{0: "0 consumers", 1: "1 consumer", 2: "2 consumers"} {
		if got := countOf(n, "consumer"); got != want {
			t.Errorf("countOf(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestClassWords_UnknownClassPassesThrough(t *testing.T) {
	if got := classWords("SOMETHING_ELSE"); got != "SOMETHING_ELSE" {
		t.Errorf("classWords = %q, want the class unchanged", got)
	}
}
