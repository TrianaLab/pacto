---
"@pacto/k8s-module": major
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

**Breaking changes and migration (operator v6)**

- The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v6` (was `.../v5`) and requires `github.com/trianalab/pacto/v4`. Go code importing `api/v1alpha1` updates its imports; `go get github.com/trianalab/pacto/integrations/kubernetes/v6@v6.0.0`.
- Apply the v6 CRDs before `helm upgrade`, which never touches them: `kubectl apply --server-side --force-conflicts -f charts/pacto-operator/crds/`. v6 adds the `NodeRuntimeObservation` CRD and the operator exits at startup without it (`no matches for kind "NodeRuntimeObservation"`), whether or not the node agent is enabled.
- Removed `--webhook-cert-path`, `--webhook-cert-name` and `--webhook-cert-key`. The operator registers no webhook and starts no webhook server; drop the flags from any manifest that still passes them, or the operator refuses to start on an unknown flag.
- Removed `--dashboard-oci-secret` and the `dashboard.ociSecret` chart value, superseded by `--dashboard-oci-secrets` and `dashboard.ociSecrets`. Migrate with `dashboard.ociSecrets: [<name>]`; the chart schema rejects the old key.
- Removed the condition reasons no reconciler emitted (`NotFound`, `AllPortsMatch`, `MissingPorts`, the seven endpoint probe reasons `OK`, `ConnectionFailed`, `InvalidStatusCode`, `EmptyResponse`, `NotDeclared`, `InterfaceHasNoPort`, `InterfaceNotFound`, and `Match`, `Mismatch`, `Missing`, `Skipped`) and the `SeverityError` and `SeverityWarning` constants from `api/v1alpha1`. Nothing in a cluster carries them; Go code importing them uses `finding.Severity` and the reasons in `troubleshooting.md`.

**Samples**

- The scenario samples under `config/samples/scenarios/` use `pactoVersion: "2.0"` contracts. The duplicate showcase samples were removed in favour of `config/samples/demo/`.
