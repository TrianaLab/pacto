package fleetsrc

import (
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
}

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
