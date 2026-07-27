/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package metrics exposes OpenTelemetry metrics for Pacto contract compliance,
// bridged to Prometheus via the OTel Prometheus exporter registered with
// controller-runtime's metrics registry.
package metrics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

const meterName = "pacto.trianalab.io/operator"

// allStatuses is the set of ContractStatus values emitted by RecordContractStatus.
var allStatuses = []string{
	pactov1alpha1.ContractStatusCompliant,
	pactov1alpha1.ContractStatusWarning,
	pactov1alpha1.ContractStatusNonCompliant,
	pactov1alpha1.ContractStatusReference,
	pactov1alpha1.ContractStatusUnknown,
	pactov1alpha1.ContractStatusInvalid,
	pactov1alpha1.ContractStatusNotEvaluated,
}

// allReadinessStatuses is the set of readiness gate states emitted by RecordReadiness.
var allReadinessStatuses = []string{
	pactov1alpha1.ReasonReadinessSatisfied,
	pactov1alpha1.ReasonReadinessBelowMinScore,
	pactov1alpha1.ReasonReadinessExpired,
}

// readinessCheckStatuses is the set of declared per-check statuses for pacto_readiness_checks.
var readinessCheckStatuses = []string{"done", "partial", "not-done", "deferred"}

var (
	contractStatus  otelmetric.Int64Gauge
	readinessScore  otelmetric.Int64Gauge
	readinessGate   otelmetric.Int64Gauge
	readinessStatus otelmetric.Int64Gauge
	readinessChecks otelmetric.Int64Gauge
)

func init() {
	// Create a Prometheus exporter that writes to controller-runtime's registry.
	// otelprom.New cannot fail with a non-nil registerer (ctrlmetrics.Registry is always set).
	exporter := must(otelprom.New(
		otelprom.WithRegisterer(ctrlmetrics.Registry),
	))
	registerGauges(exporter)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func registerGauges(exporter sdkmetric.Reader) {
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	otel.SetMeterProvider(provider)

	meter := provider.Meter(meterName)

	// Int64Gauge never returns an error for valid instrument names (OTel SDK guarantee).
	contractStatus, _ = meter.Int64Gauge("pacto_contract_status",
		otelmetric.WithDescription("Contract status by phase (1=active, 0=inactive). Label 'status' is one of: Compliant, Warning, NonCompliant, Reference, Unknown, Invalid, NotEvaluated"),
	)
	readinessScore, _ = meter.Int64Gauge("pacto_readiness_score",
		otelmetric.WithDescription("Derived operational readiness score (0-100)"),
	)
	readinessGate, _ = meter.Int64Gauge("pacto_readiness_gate",
		otelmetric.WithDescription("Whether the readiness gate is met (1=passing, 0=not passing)"),
	)
	readinessStatus, _ = meter.Int64Gauge("pacto_readiness_status",
		otelmetric.WithDescription("Readiness gate state (1=active, 0=inactive). Label 'status' is one of: Satisfied, BelowMinScore, Expired"),
	)
	readinessChecks, _ = meter.Int64Gauge("pacto_readiness_checks",
		otelmetric.WithDescription("Number of readiness checks by declared status. Label 'status' is one of: done, partial, not-done, deferred"),
	)
}

// RecordContractStatus emits the info-style pacto_contract_status gauge.
// The current status gets value 1; all other statuses get 0.
// Uses the Pacto CR name as identifier so it works even when no service is configured.
func RecordContractStatus(namespace, name, status string) {
	ctx := context.Background()
	for _, s := range allStatuses {
		val := int64(0)
		if s == status {
			val = 1
		}
		contractStatus.Record(ctx, val, otelmetric.WithAttributes(
			attribute.String("name", name),
			attribute.String("namespace", namespace),
			attribute.String("status", s),
		))
	}
}

// RecordReadiness emits the readiness gauges for a Pacto CR: the derived score,
// the gate result, an info-style per-state gauge (pacto_readiness_status), and
// per-declared-status check counts (pacto_readiness_checks). gateReason is the
// ReadinessSatisfied condition reason (Satisfied/BelowMinScore/Expired). It is a
// no-op when the contract declares no readiness (rs is nil).
func RecordReadiness(namespace, name string, rs *pactov1alpha1.ReadinessStatus, gateReason string) {
	if rs == nil {
		return
	}
	ctx := context.Background()
	idAttrs := []attribute.KeyValue{
		attribute.String("name", name),
		attribute.String("namespace", namespace),
	}

	readinessScore.Record(ctx, int64(rs.Score), otelmetric.WithAttributes(idAttrs...))

	gate := int64(0)
	if rs.Passing {
		gate = 1
	}
	readinessGate.Record(ctx, gate, otelmetric.WithAttributes(idAttrs...))

	for _, s := range allReadinessStatuses {
		val := int64(0)
		if s == gateReason {
			val = 1
		}
		readinessStatus.Record(ctx, val, otelmetric.WithAttributes(
			attribute.String("name", name),
			attribute.String("namespace", namespace),
			attribute.String("status", s),
		))
	}

	counts := map[string]int64{
		"done":     int64(rs.DoneCount),
		"partial":  int64(rs.PartialCount),
		"not-done": int64(rs.NotDoneCount),
		"deferred": int64(rs.DeferredCount),
	}
	for _, s := range readinessCheckStatuses {
		readinessChecks.Record(ctx, counts[s], otelmetric.WithAttributes(
			attribute.String("name", name),
			attribute.String("namespace", namespace),
			attribute.String("status", s),
		))
	}
}
