package ci

import (
	"strings"
	"testing"
)

func TestPlanSettleUnreadableFolder(t *testing.T) {
	p := &Plan{Rows: []Row{{Impact: Impact{Gate: "clear"}}}, Unreadable: 2}
	p.Settle("repo")
	if p.Gate != "unverified" || p.Exit != 2 || p.Reason != "unverified: 2 folders could not be read" {
		t.Fatalf("got %s %d %q", p.Gate, p.Exit, p.Reason)
	}
	// A blocked row still decides.
	p = &Plan{Rows: []Row{{Impact: Impact{Gate: "blocked"}}}, Unreadable: 1}
	if p.Settle("repo"); p.Exit != 1 {
		t.Fatalf("exit %d", p.Exit)
	}
}

func TestPlanSettleRedacts(t *testing.T) {
	const secret = "https://u:tok@h.example/x?token=s"
	p := &Plan{Notes: []string{secret}, Rows: []Row{{
		Impact: Impact{Gate: "unverified", Text: "unverified (baseline unreadable: GET " + secret + ")"},
		Reason: secret, Notes: []string{secret}, Errors: []string{secret},
	}}}
	p.Settle("repo")
	r := p.Rows[0]
	for _, s := range []string{p.Notes[0], r.Impact.Text, r.Reason, r.Notes[0], r.Errors[0]} {
		if strings.Contains(s, "tok") || !strings.Contains(s, "https://h.example/x (redacted)") {
			t.Errorf("not redacted: %q", s)
		}
	}
}

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
