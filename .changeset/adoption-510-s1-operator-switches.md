---
"@pacto/k8s-module": minor
"@pacto/core": patch
---

Probing and metrics observation take `auto`, `on` or `off` and default to
`auto`, which turns each one on only in namespaces whose owner labels them
`pacto.trianalab.io/observe` with `probing`, `metrics` or `probing.metrics`.
A namespace owner opts in with `kubectl label ns payments
pacto.trianalab.io/observe=probing`, with no chart change, and every other
namespace stays passive. The chart values are `controller.probing` and
`controller.metricsObservation`; `controller.enableProbing` and
`controller.enableMetricsObservation` are deprecated and still honoured, so an
upgrade that reuses the earlier release's values renders the same arguments. An
explicit `auto` overrides a deprecated `true`. ServiceMonitor and PodMonitor
read access still comes with `metricsObservation: on` only.

`controller.interfaceNameMatchDiscovery` and `nodeAgent.image` are new, so
every operator flag has a chart value or a documented reason, and a test keeps
it that way. `controller.kernelEvidence.enabled` takes `auto`, `on` or `"off"`;
`false` keeps following `nodeAgent.enabled`.

The default install gains a `namespaces get` grant for the label read (in each
watched namespace's Role under `controller.watchNamespaces`). The health probe
no longer follows ExternalName or selector-less Services. The `RuntimeObserved`
message names where each setting came from, and a transient namespace read
failure marks it `False`.
