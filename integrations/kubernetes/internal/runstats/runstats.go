/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package runstats publishes unsigned run records of the operations the
// promoter concludes, best effort. It never reads a record and never affects a
// conclusion.
package runstats

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

const (
	queueSize = 256
	// namespaceCap is how many queued records one namespace may hold, so a
	// burst of operations in one namespace cannot drop every other's records.
	// ponytail: a flat cap per namespace; weigh it by Stages if one namespace legitimately bursts past it.
	namespaceCap   = queueSize / 4
	publishTimeout = 30 * time.Second
)

// dropReasons are the reasons a record is not published. Each series exists
// from New, at zero, so the first drop of each moves rate() and increase().
var dropReasons = []string{"queue-full", "namespace-full", "publish-failed", "no-stage", "shutdown"}

// Dropped counts the run records that were not published. A record that is
// missing from the stats repository is counted here, except one concluded
// after the promoter began to stop, which is logged by a process about to exit.
var Dropped = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "pacto_delivery_stats_dropped_total",
	Help: "Run records of concluded operations that were not published, by reason: queue-full, namespace-full, publish-failed, no-stage or shutdown.",
}, []string{"reason"})

func init() { metrics.Registry.MustRegister(Dropped) }

// queued is a record and the namespace it counts against.
type queued struct {
	rec runrecord.Record
	ns  string
}

// Emitter queues the run record of each concluded operation and publishes it
// from one worker. It never blocks the caller and never retries.
type Emitter struct {
	ClusterID, PactoVersion string
	Now                     func() time.Time
	// PublishTimeout bounds one publish.
	PublishTimeout time.Duration
	// publish is replaced by tests; production uses the closure New builds.
	publish func(context.Context, runrecord.Record) error
	q       chan queued

	mu      sync.Mutex
	perNS   map[string]int
	stopped bool
}

// New returns an emitter that publishes to repository ("<registry>/<repository>")
// with kc, reaching the hosts in insecure over plain HTTP, and refuses every
// write outside scope (nil writes anywhere). It refuses a repository it cannot
// parse or that names a tag or digest. The repository is
// opened once, so its token cache and Referrers API probe outlive a record;
// kc is still asked on every token exchange.
func New(repository string, kc authn.Keychain, insecure []string, scope *oci.PushScope, clusterID, pactoVersion string) (*Emitter, error) {
	repo, err := registry.Open(repository, kc, insecure, scope)
	if err != nil {
		return nil, fmt.Errorf("stats repository %q: %w", repository, err)
	}
	if repo.Reference.Reference != "" {
		return nil, fmt.Errorf("stats repository %q names a tag or digest: give <registry>/<repository>", repository)
	}
	for _, r := range dropReasons {
		Dropped.WithLabelValues(r)
	}
	return &Emitter{
		ClusterID: clusterID, PactoVersion: pactoVersion, Now: time.Now, PublishTimeout: publishTimeout,
		q: make(chan queued, queueSize), perNS: map[string]int{},
		publish: func(ctx context.Context, rec runrecord.Record) error {
			_, err := registry.Publish(ctx, repo, rec)
			return err
		},
	}, nil
}

// Emit builds op's record and queues it. A full queue, a namespace at its cap
// or a stopped worker drops the record.
func (e *Emitter) Emit(op *dv1.Operation) {
	rec, ok := runrecord.FromOperation(runrecord.OperationInput{
		UID: string(op.UID), Namespace: op.Namespace, Spec: op.Spec, Status: op.Status,
		ClusterID: e.ClusterID, PactoVersion: e.PactoVersion, RecordedAt: e.Now(),
	})
	if !ok {
		Dropped.WithLabelValues("no-stage").Inc()
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.stopped:
		Dropped.WithLabelValues("shutdown").Inc()
		ctrl.Log.Info("run record not published: the promoter is stopping", "operation", rec.ID, "service", rec.Service)
		return
	case e.perNS[op.Namespace] >= namespaceCap:
		Dropped.WithLabelValues("namespace-full").Inc()
		return
	}
	select {
	case e.q <- queued{rec, op.Namespace}:
		e.perNS[op.Namespace]++
	default:
		Dropped.WithLabelValues("queue-full").Inc()
	}
}

// dequeued releases the namespace slot of a record the worker took.
func (e *Emitter) dequeued(ns string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.perNS[ns]--; e.perNS[ns] <= 0 {
		delete(e.perNS, ns)
	}
}

// Run publishes queued records until ctx ends, then refuses new records and
// counts what is still queued. A record in flight when ctx ends counts as
// publish-failed.
func (e *Emitter) Run(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case q := <-e.q:
			e.dequeued(q.ns)
			pctx, cancel := context.WithTimeout(ctx, e.PublishTimeout)
			if err := e.publish(pctx, q.rec); err != nil {
				log.Info("run record not published", "operation", q.rec.ID, "service", q.rec.Service, "error", err.Error())
				Dropped.WithLabelValues("publish-failed").Inc()
			}
			cancel()
		}
	}
	// Emit checks stopped under mu, so no record is queued after this drain.
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
	n := 0
	for {
		select {
		case q := <-e.q:
			e.dequeued(q.ns)
			n++
			Dropped.WithLabelValues("shutdown").Inc()
		default:
			if n > 0 {
				log.Info("run records not published at shutdown", "count", n)
			}
			return nil
		}
	}
}
