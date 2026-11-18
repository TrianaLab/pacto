package ci

import "testing"

func TestPlanSettle(t *testing.T) {
	row := func(gate string) Row { return Row{Impact: Impact{Gate: gate}} }
	for _, tc := range []struct {
		name   string
		rows   []Row
		gate   string
		exit   int
		reason string
	}{
		{"no rows", nil, "unverified", 2, "unverified: no pacto.yaml under repo"},
		{"clear", []Row{row("clear")}, "clear", 0, ""},
		{"unverified beats clear", []Row{row("clear"), row("unverified")}, "unverified", 2, "unverified: 1 of 2 services"},
		{"blocked beats unverified", []Row{row("unverified"), row("blocked"), row("unverified")}, "blocked", 1, "blocked: 1 of 3 services"},
	} {
		p := &Plan{Rows: tc.rows}
		p.Settle("repo")
		if p.Gate != tc.gate || p.Exit != tc.exit || p.Reason != tc.reason {
			t.Errorf("%s: got %s %d %q", tc.name, p.Gate, p.Exit, p.Reason)
		}
		for _, r := range p.Rows {
			if r.Impact.Sources == nil {
				t.Errorf("%s: a row's sources are null, want []", tc.name)
			}
		}
	}
}
