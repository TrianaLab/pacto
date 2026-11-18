package ci

import "testing"

func TestPlanSettle(t *testing.T) {
	row := func(gate string) Row { return Row{Impact: Impact{Gate: gate}} }
	for _, tc := range []struct {
		name string
		rows []Row
		gate string
		exit int
	}{
		{"no rows", nil, "unverified", 2},
		{"clear", []Row{row("clear")}, "clear", 0},
		{"unverified beats clear", []Row{row("clear"), row("unverified")}, "unverified", 2},
		{"blocked beats unverified", []Row{row("unverified"), row("blocked"), row("unverified")}, "blocked", 1},
	} {
		p := &Plan{Rows: tc.rows}
		p.Settle()
		if p.Gate != tc.gate || p.Exit != tc.exit {
			t.Errorf("%s: got %s %d", tc.name, p.Gate, p.Exit)
		}
	}
}
