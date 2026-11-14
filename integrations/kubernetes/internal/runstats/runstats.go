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
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

const (
	queueSize      = 256
	publishTimeout = 30 * time.Second
)

// Dropped counts the run records that were not published. A record that is
// missing from the stats repository is counted here, never silently lost.
var Dropped = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "pacto_delivery_stats_dropped_total",
	Help: "Run records of concluded operations that were not published, by reason: queue-full, publish-failed, no-stage or shutdown.",
}, []string{"reason"})

func init() { metrics.Registry.MustRegister(Dropped) }

// Emitter queues the run record of each concluded operation and publishes it
// from one worker. It never blocks the caller and never retries.
type Emitter struct {
	ClusterID, PactoVersion string
	Now                     func() time.Time
	// publish is replaced by tests; production uses the closure New builds.
	publish func(context.Context, runrecord.Record) error
	q       chan runrecord.Record
}

// New returns an emitter that publishes to repository ("<registry>/<repository>")
// with kc, reaching the hosts in insecure over plain HTTP.
func New(repository string, kc authn.Keychain, insecure []string, clusterID, pactoVersion string) *Emitter {
	return &Emitter{
		ClusterID: clusterID, PactoVersion: pactoVersion, Now: time.Now,
		q: make(chan runrecord.Record, queueSize),
		publish: func(ctx context.Context, rec runrecord.Record) error {
			repo, err := registry.Open(repository, kc, insecure)
			if err != nil {
				return err
			}
			_, err = registry.Publish(ctx, repo, rec)
			return err
		},
	}
}

// Emit builds op's record and queues it. A full queue drops the record.
func (e *Emitter) Emit(op *dv1.Operation) {
	rec, ok := runrecord.FromOperation(runrecord.OperationInput{
		UID: string(op.UID), Namespace: op.Namespace, Spec: op.Spec, Status: op.Status,
		ClusterID: e.ClusterID, PactoVersion: e.PactoVersion, RecordedAt: e.Now(),
	})
	if !ok {
		Dropped.WithLabelValues("no-stage").Inc()
		return
	}
	select {
	case e.q <- rec:
	default:
		Dropped.WithLabelValues("queue-full").Inc()
	}
}

// Run publishes queued records until ctx ends, then counts what is still queued.
func (e *Emitter) Run(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case rec := <-e.q:
			pctx, cancel := context.WithTimeout(ctx, publishTimeout)
			if err := e.publish(pctx, rec); err != nil {
				log.Info("run record not published", "operation", rec.ID, "service", rec.Service, "error", err.Error())
				Dropped.WithLabelValues("publish-failed").Inc()
			}
			cancel()
		}
	}
	n := 0
	for {
		select {
		case <-e.q:
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
