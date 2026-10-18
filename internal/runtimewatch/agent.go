/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// Config is one agent: where it reads the host, what it keeps and where it
// publishes.
type Config struct {
	NodeName        string
	AgentVersion    string
	PublishInterval time.Duration
	ResyncInterval  time.Duration
	// Proc is the host /proc.
	Proc ProcFS
	// Cgroups is the host cgroup v2 root; nil attributes by network namespace
	// only.
	Cgroups fs.FS
	// Keep selects the workloads the agent tracks; nil keeps every kind. The
	// Kubernetes sink publishes pods only, so it keeps pods only: anything else
	// would spend the workload bound on entries nobody reads.
	Keep      func(runtimefacts.Identity) bool
	Collector Collector
	Sink      Sink
}

// Run serves the agent until ctx ends, then waits for the collector to release
// the kernel objects.
func Run(ctx context.Context, cfg Config) {
	keep := cfg.Keep
	if keep == nil {
		keep = func(runtimefacts.Identity) bool { return true }
	}
	var resolve Resolve
	var resolver *CgroupResolver
	if cfg.Cgroups != nil {
		resolver = &CgroupResolver{FS: cfg.Cgroups, MinRefresh: time.Second, Now: time.Now}
		resolve = func(cg uint64) (runtimefacts.Identity, bool) {
			id, ok := resolver.Identify(cg)
			return id, ok && keep(id)
		}
	}
	release, _ := cfg.Proc.ReadFile("sys/kernel/osrelease")

	events := make(chan Event, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cfg.Collector.Run(ctx, events)
	}()
	resync := time.NewTicker(cfg.ResyncInterval)
	defer resync.Stop()
	publish := time.NewTicker(cfg.PublishInterval)
	defer publish.Stop()

	logging.LoggerFromContext(ctx).Info("Starting runtime agent", "node", cfg.NodeName, "version", cfg.AgentVersion)
	(&Runner{
		Info: HostInfo{
			NodeName:        cfg.NodeName,
			AgentVersion:    cfg.AgentVersion,
			KernelRelease:   strings.TrimSpace(string(release)),
			PublishInterval: cfg.PublishInterval,
			StartedAt:       time.Now(),
		},
		Aggregator: NewAggregator(DefaultBounds(), resolve),
		Scan: func() (Scan, error) {
			// Cgroups created since the last walk resolve on their first event;
			// this walk forgets the deleted ones.
			if resolver != nil {
				resolver.Refresh()
			}
			mono := cfg.Collector.Mono()
			scan, err := ScanProc(cfg.Proc, keep)
			scan.Mono = mono
			return scan, err
		},
		Sink:        cfg.Sink,
		Collector:   cfg.Collector,
		Events:      events,
		ResyncTick:  resync.C,
		PublishTick: publish.C,
		Now:         time.Now,
	}).Run(ctx)
	<-done
	logging.LoggerFromContext(ctx).Info("Runtime agent stopped")
}

// JSONSink writes every snapshot as one JSON line: the stdout sink, for local
// debugging.
type JSONSink struct {
	W io.Writer
}

// Publish implements Sink.
func (s JSONSink) Publish(_ context.Context, snap runtimefacts.Snapshot) error {
	return json.NewEncoder(s.W).Encode(snap)
}
