---
"@pacto/k8s-module": minor
---

Fixes from a whole-repo architecture review of the operator. Two of them change what you see.

**Behaviour changes**

- Controller logs default to JSON (production mode). Pass `--zap-devel` to get the old console format back.
- The `RuntimeObserved` condition is now `False` with reason `ObservationFailed` whenever any runtime read failed during collection, and the message lists what failed. Before, it could read `True` while parts of the collection had failed.

**Fixes**

- PactoRevision names and version labels are valid for every semver version, including build metadata (`1.0.0+build.1`), uppercase pre-releases and very long versions. Names that were already valid do not change.
- ServiceMonitor and PodMonitor endpoints that name their port (the usual case in real clusters) are now resolved, so metrics discovery through them works.
- An unset numeric `targetPort` now means the Service port everywhere, as the API server defaults it.
- Configuration schemas are compiled once per contract revision instead of on every reconcile.

**Deprecations (removed at v6)**

- `--webhook-cert-path`, `--webhook-cert-name` and `--webhook-cert-key`: the operator registers no webhook and no longer starts a webhook server.

**Samples**

- The scenario samples under `config/samples/scenarios/` use `pactoVersion: "2.0"` contracts. The duplicate showcase samples were removed in favour of `config/samples/demo/`.
