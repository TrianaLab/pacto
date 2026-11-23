/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package offeringpublish

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// Published counts the inferred offering revisions the operator pushed.
	Published = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pacto_offering_published_total",
		Help: "Inferred offering revisions the operator published to its pacto-inferred contribution repository.",
	})
	// PublishErrors counts the failed runs and the offerings a run could not publish.
	PublishErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pacto_offering_publish_errors_total",
		Help: "Inferred offerings the operator could not publish, plus runs that published nothing.",
	})
)

func init() { metrics.Registry.MustRegister(Published, PublishErrors) }
