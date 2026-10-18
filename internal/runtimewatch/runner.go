/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// Sink receives every published snapshot: the Kubernetes sink writes it as the
// node's NodeRuntimeObservation status, the stdout sink prints it.
type Sink interface {
	Publish(ctx context.Context, s runtimefacts.Snapshot) error
}

// Collector is the kernel side. The eBPF loader implements it.
type Collector interface {
	// Run attaches, streams events into out and re-attaches until ctx ends.
	Run(ctx context.Context, out chan<- Event)
	// Status reports the collector's health.
	Status() CollectorStatus
	// Attached receives after every (re)attach; it never blocks the collector.
	Attached() <-chan struct{}
	// Mono reads the clock events are stamped with (CLOCK_MONOTONIC, ns); 0
	// when unreadable, which disables the comparison.
	Mono() uint64
}

// HostInfo is the static part of the published snapshot.
type HostInfo struct {
	NodeName        string
	AgentVersion    string
	KernelRelease   string
	PublishInterval time.Duration
	StartedAt       time.Time
}

// Runner ties the event stream, the /proc resync and the sink together. All
// time comes from Now and the two tick channels, so tests drive it by hand.
type Runner struct {
	Info       HostInfo
	Aggregator *Aggregator
	Scan       func() (Scan, error)
	Sink       Sink
	Collector  Collector
	// Events is never closed: the collector stops sending when ctx ends.
	Events      <-chan Event
	ResyncTick  <-chan time.Time
	PublishTick <-chan time.Time
	Now         func() time.Time
}

// MaxStatusBytes bounds the published snapshot JSON: well inside etcd's 1.5 MiB
// object limit and the apiserver's 3 MiB request limit.
const MaxStatusBytes = 1 << 20

// Run baselines and publishes once, then serves events and ticks until ctx ends.
// Failures are logged and retried on the next tick: an agent that exits stops
// heartbeating, and a missing heartbeat already means "cannot observe".
func (r *Runner) Run(ctx context.Context) {
	r.resync(ctx)
	r.publish(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-r.Events:
			r.Aggregator.Apply(ev, r.Now())
		case <-r.ResyncTick:
			r.resync(ctx)
		case <-r.PublishTick:
			r.publish(ctx)
		case <-r.Collector.Attached():
			// Drops the collector counted for the detach predate this baseline:
			// the gap before the attach is closed by a scan taken after it.
			r.Aggregator.ObserveDrops(r.Collector.Status().Dropped, r.Now())
			r.resync(ctx)
		}
	}
}

func (r *Runner) resync(ctx context.Context) {
	scan, err := r.Scan()
	if err != nil {
		logging.LoggerFromContext(ctx).Error("Resync from /proc failed", "error", err)
		r.Aggregator.ResyncFailed(err)
		return
	}
	r.Aggregator.Resync(scan, r.Now())
}

func (r *Runner) publish(ctx context.Context) {
	now := r.Now()
	col, workloads := r.Aggregator.Snapshot(now, r.Collector.Status())
	snap := runtimefacts.Snapshot{
		NodeName:               r.Info.NodeName,
		AgentVersion:           r.Info.AgentVersion,
		KernelRelease:          r.Info.KernelRelease,
		PublishIntervalSeconds: int32(r.Info.PublishInterval / time.Second),
		LastPublished:          seconds(now),
		ObservingSince:         seconds(r.Info.StartedAt),
		Collection:             col,
		Workloads:              workloads,
	}
	if jsonSize(snap) > MaxStatusBytes {
		// Set before trimming, so the trim makes room for the message too.
		if col.State != runtimefacts.StateFailed {
			snap.Collection.State, snap.Collection.Reason = runtimefacts.StateDegraded, ReasonObjectTooLarge
			snap.Collection.Message = "the snapshot exceeded 1 MiB; the oldest connections, then listeners, of the largest workloads were left out"
		}
		fitSnapshot(&snap, MaxStatusBytes)
	}
	// A hung sink must not stall event consumption past one interval.
	ctx, cancel := context.WithTimeout(ctx, r.Info.PublishInterval)
	defer cancel()
	if err := r.Sink.Publish(ctx, snap); err != nil {
		logging.LoggerFromContext(ctx).Error("Publish failed", "node", r.Info.NodeName, "error", err)
	}
}

// fitSnapshot trims s until its JSON fits in limit, taking from the workload
// with the most entries each time: its oldest connection, or its last listener
// once it has no connections left. Trimmed workloads are marked truncated.
func fitSnapshot(s *runtimefacts.Snapshot, limit int) {
	for {
		excess := jsonSize(s) - limit
		if excess <= 0 {
			return
		}
		// Shed the estimated excess, then measure again: the estimate ignores
		// the truncated flag and the brackets of emptied lists.
		for excess > 0 {
			w := largestWorkload(s.Workloads)
			if w == nil {
				return
			}
			if len(w.Connections) > 0 {
				i := oldestConnection(w.Connections)
				excess -= jsonSize(w.Connections[i]) + 1
				w.Connections = slices.Delete(w.Connections, i, i+1)
			} else {
				i := len(w.Listeners) - 1
				excess -= jsonSize(w.Listeners[i]) + 1
				w.Listeners = w.Listeners[:i]
			}
			w.Truncated = true
		}
	}
}

// jsonSize is the encoded size of a snapshot fragment. Plain structs always
// encode, so the error is unreachable.
func jsonSize(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// largestWorkload is the first workload with the most listeners and
// connections, or nil when every workload is empty.
func largestWorkload(ws []runtimefacts.Workload) *runtimefacts.Workload {
	var best *runtimefacts.Workload
	most := 0
	for i := range ws {
		if n := len(ws[i].Listeners) + len(ws[i].Connections); n > most {
			best, most = &ws[i], n
		}
	}
	return best
}

// oldestConnection is the index of the first connection with the earliest last
// attempt.
func oldestConnection(cs []runtimefacts.Connection) int {
	last := func(c runtimefacts.Connection) time.Time {
		if c.LastSuccessAt.After(c.LastFailureAt) {
			return c.LastSuccessAt
		}
		return c.LastFailureAt
	}
	oldest := 0
	for i := range cs {
		if last(cs[i]).Before(last(cs[oldest])) {
			oldest = i
		}
	}
	return oldest
}
