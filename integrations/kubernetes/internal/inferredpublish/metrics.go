/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package inferredpublish

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// Published counts the inferred contract revisions the operator pushed.
	Published = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pacto_inferred_contracts_published_total",
		Help: "Inferred contract revisions the operator published to <registry>/pacto-inferred.",
	})
	// PublishErrors counts the failed runs and the contracts a run could not publish.
	PublishErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pacto_inferred_contracts_publish_errors_total",
		Help: "Inferred contracts the operator could not publish, plus runs that published nothing.",
	})
)

func init() { metrics.Registry.MustRegister(Published, PublishErrors) }
