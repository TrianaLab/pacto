package diff

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

type breakRow struct {
	path     string
	ct       ChangeType
	old, new any
}

func breakRows(old, new []contract.Break) []breakRow {
	var out []breakRow
	for _, c := range diffBreaks(old, new) {
		if c.Classification != NonBreaking {
			panic("break change " + c.Path + " is not NON_BREAKING")
		}
		out = append(out, breakRow{c.Path, c.Type, c.OldValue, c.NewValue})
	}
	return out
}

func TestDiffBreaksAddedRemoved(t *testing.T) {
	a := contract.Break{Consumers: []string{"reports"}, Reason: "euros"}
	b := contract.Break{Consumers: []string{"audit", "billing"}, Reason: "v2 only"}
	got := breakRows([]contract.Break{a}, []contract.Break{b})
	want := []breakRow{{"breaks", Removed, "reports", nil}, {"breaks", Added, nil, "audit,billing"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := breakRows(nil, nil); len(got) != 0 {
		t.Fatalf("no breaks: %v", got)
	}
}

func TestDiffBreaksReasonAndExpires(t *testing.T) {
	o := []contract.Break{{Consumers: []string{"reports"}, Reason: "euros", Expires: "2026-12-14"}}
	cases := map[string]struct {
		new  contract.Break
		want []breakRow
	}{
		"same":            {o[0], nil},
		"reason":          {contract.Break{Consumers: []string{"reports"}, Reason: "cents", Expires: "2026-12-14"}, []breakRow{{"breaks.reason", Modified, "reports: euros", "reports: cents"}}},
		"expires moved":   {contract.Break{Consumers: []string{"reports"}, Reason: "euros", Expires: "2027-01-01"}, []breakRow{{"breaks.expires", Modified, "reports: 2026-12-14", "reports: 2027-01-01"}}},
		"expires dropped": {contract.Break{Consumers: []string{"reports"}, Reason: "euros"}, []breakRow{{"breaks.expires", Removed, "reports: 2026-12-14", "reports: "}}},
	}
	for name, tc := range cases {
		if got := breakRows(o, []contract.Break{tc.new}); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
	if got, want := breakRows([]contract.Break{{Consumers: []string{"reports"}, Reason: "euros"}}, o), []breakRow{{"breaks.expires", Added, "reports: ", "reports: 2026-12-14"}}; !slices.Equal(got, want) {
		t.Errorf("expires added: got %v, want %v", got, want)
	}
}

func TestDiffBreaksKeyIgnoresConsumerOrder(t *testing.T) {
	o := []contract.Break{{Consumers: []string{"reports", "audit"}, Reason: "euros"}}
	n := []contract.Break{{Consumers: []string{"audit", "reports"}, Reason: "euros"}}
	if got := breakRows(o, n); len(got) != 0 {
		t.Fatalf("reordered consumers: %v", got)
	}
	if o[0].Consumers[0] != "reports" {
		t.Fatal("the caller's consumers were reordered")
	}
	// Compare reports it.
	old, new := minimalContract(), minimalContract()
	new.Breaks = n
	if r := Compare(t.Context(), old, new, nil, nil); len(r.Changes) != 1 || r.Changes[0].Path != "breaks" || r.Classification != NonBreaking {
		t.Fatalf("Compare: %+v", r)
	}
}
