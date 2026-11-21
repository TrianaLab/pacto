---
"@pacto/core": minor
"@pacto/k8s-module": minor
"@pacto/operator-chart": minor
---

A cluster that already runs an OpenTelemetry Collector, Tempo or Jaeger now
gives the operational graph observed calls between services with no chart
value. The operator detects each backend, plus a Datadog Agent, from the pods
and Services it already reads. The dashboard reads the calls of the last 15
minutes from Jaeger's `/api/dependencies` and from the service graph that
Tempo's metrics-generator or the Collector's `servicegraph` connector writes to
Prometheus, all read-only. For a Collector with no Prometheus, the dashboard
receives OTLP/HTTP JSON traces on port 4318 (`--otlp-receiver`, set by the
operator), keeps no span, serves nothing else there and turns off while
another source proves complete coverage. The chart adds a NetworkPolicy that
admits 4318 from pods in the cluster only. Receiver calls never corroborate a
declared dependency and never clear a gate. Each backend is one Detected
providers row with its own status, the reason it is partial and the Collector
configuration to fix it. Pacto reads no Datadog Secret and holds no Datadog
client.
