package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// TestMetricsStepMatchesRecords: the delivery tour's Metrics step quotes the
// numbers the demo's own records total over the default window, so the
// sentence cannot drift from the data. Any other integer in it fails, so a
// third number (a held count, a window length) cannot slip in unchecked.
func TestMetricsStepMatchesRecords(t *testing.T) {
	var text string
	for _, s := range tourSteps(t, "DELIVERY_TOUR") {
		if s.title == "What Pacto did this month" {
			text = s.text
		}
	}
	if text == "" {
		t.Fatal("no \"What Pacto did this month\" step in DELIVERY_TOUR")
	}
	port, err := demoStats(story.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	set, err := port(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st := stats.Aggregate(set, stats.Query{Window: stats.Window{Since: story.Now.Add(-stats.DefaultWindow), Until: story.Now}}, stats.Scope{}, story.Now)
	if st.Totals == nil {
		t.Fatal("the default window holds no records")
	}
	quoted := map[string]int{
		`caught (\d+) breaking changes`: st.Totals.BreakingCaught.Distinct,
		`blocked (\d+) changes`:         st.Totals.Gates.Blocked,
	}
	rest := text
	for pattern, want := range quoted {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(text)
		if m == nil {
			t.Errorf("step text %q has no %q", text, pattern)
			continue
		}
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("step says %q, the records total %d", m[0], want)
		}
		rest = re.ReplaceAllString(rest, "")
	}
	if n := regexp.MustCompile(`\d+`).FindString(rest); n != "" {
		t.Errorf("step text quotes %s, which no metric checks: %q", n, text)
	}
	// The browser demo generates its records in the tab and names no
	// registry, and a tile counting 0 links nowhere.
	for _, claim := range []string{"registry you name", "each tile"} {
		if strings.Contains(text, claim) {
			t.Errorf("step text claims %q, which is not true of the browser demo: %q", claim, text)
		}
	}
}
