//go:build !race

package dashboard

import (
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestFleetMapBudget: every map request answers within its time and byte
// budget (Review note N5), as the median of 5 after one warm-up, the
// snapshot build excluded: at 3000 services always, at 10000 with
// PACTO_SCALE_FULL=1, where four node pages are also asked at once. It runs
// through make test-fleet-budgets, never under the race detector.
func TestFleetMapBudget(t *testing.T) {
	sizes := []int{3000}
	if os.Getenv("PACTO_SCALE_FULL") == "1" {
		sizes = append(sizes, 10000)
	}
	for _, n := range sizes {
		q := genQuery(t, n)
		base := mapServer(t, Options{Fleet: fleetOf(q)})
		for _, b := range mapBudgets(t, q, n) {
			took, size := median(func() (time.Duration, int) { return timedGet(t, base, b.path) })
			t.Logf("n=%d %s: %v, %d bytes", n, b.name, took.Round(time.Millisecond), size)
			if took > b.maxTime || size > b.maxBytes {
				t.Errorf("n=%d %s: %v and %d bytes, budget %v and %d", n, b.name, took, size, b.maxTime, b.maxBytes)
			}
		}
		if n > 3000 {
			concurrentPages(t, q, base)
		}
	}
}

// concurrentPages asks a page of each of the four largest environment
// groups at once; the slowest answers within the page budget.
func concurrentPages(t *testing.T, q *fleet.Query, base string) {
	m, _ := q.Map(fleet.MapQuery{GroupBy: fleet.MapGroupByEnvironment})
	took, _ := median(func() (time.Duration, int) {
		var wg sync.WaitGroup
		times := make([]time.Duration, 4)
		for i := range times {
			wg.Go(func() {
				times[i], _ = timedGet(t, base, "/api/fleet/map/nodes?groupBy=environment&group="+url.QueryEscape(m.Groups[i].Key))
			})
		}
		wg.Wait()
		return slices.Max(times), 0
	})
	t.Logf("four concurrent pages: slowest %v", took.Round(time.Millisecond))
	if took > 200*time.Millisecond {
		t.Errorf("four concurrent pages: slowest %v, budget 200ms", took)
	}
}

// median runs f once to warm up, then 5 times, and returns the median time
// and the last size.
func median(f func() (time.Duration, int)) (time.Duration, int) {
	f()
	times := make([]time.Duration, 5)
	size := 0
	for i := range times {
		times[i], size = f()
	}
	slices.Sort(times)
	return times[2], size
}
