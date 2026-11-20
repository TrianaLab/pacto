package ci

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

func TestIndexText(t *testing.T) {
	read := func(ix impact.ConsumerIndex, lims ...fleet.Limitation) *impact.Result {
		ix.Status, ix.Repository = impact.IndexRead, "ghcr.io/acme/pacto-index"
		return &impact.Result{ConsumerIndex: &ix, Limitations: lims}
	}
	for name, tc := range map[string]struct {
		res  *impact.Result
		want string
	}{
		"none":    {&impact.Result{}, ""},
		"checked": {read(impact.ConsumerIndex{Consumers: 41, Checked: true, Completeness: fleet.CompletenessComplete}), "41 from ghcr.io/acme/pacto-index (listed by tag, complete)"},
		"fallback": {read(impact.ConsumerIndex{Consumers: 2, Checked: true, Completeness: fleet.CompletenessComplete, Reason: "after ghcr.io/acme/pacto-index: not found"}),
			"2 from ghcr.io/acme/pacto-index (listed by tag, complete, after ghcr.io/acme/pacto-index: not found)"},
		"unchecked": {read(impact.ConsumerIndex{Consumers: 41, Services: 120}), "41 from ghcr.io/acme/pacto-index (listed by tag, 120 services listed, unchecked)"},
		"one":       {read(impact.ConsumerIndex{Services: 1}), "0 from ghcr.io/acme/pacto-index (listed by tag, 1 service listed, unchecked)"},
		"partial": {read(impact.ConsumerIndex{Consumers: 3, Checked: true, Completeness: fleet.CompletenessPartial},
			fleet.Limitation{Code: "OTHER", Source: "k8s", Message: "x"},
			fleet.Limitation{Code: impact.LimitationConsumerNotIndexed, Source: "impact", Message: "1 consumer the fleet holds has no usable organization index entry: web."}),
			"3 from ghcr.io/acme/pacto-index (listed by tag, partial: 1 consumer the fleet holds has no usable organization index entry: web)"},
		"partial unknown": {read(impact.ConsumerIndex{Checked: true, Completeness: fleet.CompletenessPartial}), "0 from ghcr.io/acme/pacto-index (listed by tag, partial: see the limitations)"},
		"not found": {&impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexNotFound, Reason: "no organization index at a/pacto-index or b/pacto-index"}},
			"no organization index at a/pacto-index or b/pacto-index (partial)"},
		"unavailable": {&impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexUnavailable, Repository: "a/pacto-index", Reason: "denied"}},
			"the organization index a/pacto-index could not be listed: denied (partial)"},
		"skipped": {&impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexSkipped, Reason: "--index none"}}, "organization index not read (--index none)"},
	} {
		if got := IndexText(tc.res); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// TestPlanNoteIndexKeepsTheLeastComplete: a plan's Index line is never
// complete while another row's answer is partial or unchecked.
func TestPlanNoteIndexKeepsTheLeastComplete(t *testing.T) {
	read := func(checked bool, c fleet.Completeness) *impact.Result {
		return &impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexRead, Repository: "r/pacto-index", Checked: checked, Completeness: c}}
	}
	complete, partial, unchecked := read(true, fleet.CompletenessComplete), read(true, fleet.CompletenessPartial), read(false, fleet.CompletenessPartial)
	for name, rows := range map[string][]*impact.Result{
		"partial after complete":   {&impact.Result{}, complete, partial, complete},
		"unchecked after complete": {complete, unchecked},
		"first partial kept":       {partial, unchecked, complete},
	} {
		var p Plan
		for _, r := range rows {
			p.NoteIndex(r)
		}
		if strings.Contains(p.Index, "complete)") || p.Index == "" {
			t.Errorf("%s: %q", name, p.Index)
		}
	}
}

func TestTextPlanIndexLine(t *testing.T) {
	var b strings.Builder
	Text(&b, &Plan{Index: "organization index not read (--index none)"})
	if !strings.Contains(b.String(), "Consumer source: none (IMPACT reads unverified)\nIndex: organization index not read (--index none)\n") {
		t.Fatalf("got:\n%s", b.String())
	}
}

func TestRangesLines(t *testing.T) {
	if RangesLines(nil) != nil {
		t.Fatal("no coverage printed a line")
	}
	c := &impact.RangeCoverage{Edges: 3, Declared: 1, UnknownTotal: 2, Unknown: []impact.UnknownRange{{From: "a", To: "b"}}}
	got := strings.Join(RangesLines(c), "\n")
	if got != "Ranges: 1 declared and 0 accepted of 3 dependency edges (33%)\n  unknown: a -> b\n  and 1 more" {
		t.Fatalf("got %q", got)
	}
}
