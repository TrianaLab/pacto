package fleetsrc

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// LiveEdges is what the dashboard's live edge sources share for its whole
// life: per source name, the last read with complete coverage (available,
// uncapped, nothing dropped, exactly one candidate) and the edges it read.
// The OTLP receiver stays off while one of them is recent. One mutex guards
// every field: [fleet.Build] collects sources concurrently.
type LiveEdges struct {
	mu       sync.Mutex
	now      func() time.Time
	complete map[string]coverage
	// Receiver is the dashboard's OTLP receiver, nil when it does not run.
	// Set once before the first refresh.
	Receiver *Receiver
}

// winnerWindow is how recent a complete read must be to win: two refreshes.
// ponytail: sources run concurrently, so the receiver follows a winner one
// refresh (30s) late.
const winnerWindow = 60 * time.Second

// winnerOrder is who wins when several sources had complete coverage.
var winnerOrder = []string{serviceGraphName, jaegerName}

// coverage is one source's last complete read.
type coverage struct {
	at    time.Time
	edges []fleet.ObservedEdge
}

// NewLiveEdges is an empty holder on the clock now.
func NewLiveEdges(now func() time.Time) *LiveEdges {
	return &LiveEdges{now: now, complete: map[string]coverage{}}
}

// markComplete records that the source named name just read edges with
// complete coverage.
func (l *LiveEdges) markComplete(name string, edges []fleet.ObservedEdge) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.complete[name] = coverage{at: l.now(), edges: slices.Clone(edges)}
}

// winner is the first source of winnerOrder with complete coverage within
// winnerWindow and the edges it read, or "" and nil.
func (l *LiveEdges) winner() (string, []fleet.ObservedEdge) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, name := range winnerOrder {
		if c, ok := l.complete[name]; ok && l.now().Sub(c.at) <= winnerWindow {
			return name, c.edges
		}
	}
	return "", nil
}

// verdict is the source that won and how many calls the receiver saw in its
// window that the winner's last edges miss: the receiver is off while a
// winner misses none. Without a receiver nothing is missing.
func (l *LiveEdges) verdict() (string, int) {
	name, edges := l.winner()
	if name == "" || l.Receiver == nil {
		return name, 0
	}
	missing := 0
	for _, e := range l.Receiver.window().edges {
		if !slices.ContainsFunc(edges, func(w fleet.ObservedEdge) bool { return w.From == e.From && w.To == e.To }) {
			missing += e.Count
		}
	}
	return name, missing
}

// missesText says the winner misses calls the receiver saw; both rows say it.
func missesText(winner string, calls int) string {
	return fmt.Sprintf("%s misses %d calls the receiver saw", winner, calls)
}
