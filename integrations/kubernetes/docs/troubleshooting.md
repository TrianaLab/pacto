# Troubleshooting

Start every investigation with the CR status: it carries the contract status,
conditions and typed findings.

```bash
kubectl describe pacto <name>
kubectl get pacto <name> -o yaml | yq '.status'
```

The finding codes referenced below are defined on the
[Runtime observations](runtime-observations.md) page.

## Status is `Unknown`

`Unknown` means a required assertion could not be evaluated -- it is not a
violation. Common causes:

- **`EVIDENCE_MISSING`** -- no observation was collected for a required assertion.
  The target Service or workload may not exist yet, or `spec.target.serviceName`
  does not match a real Service.
- **`OBSERVATION_UNSUPPORTED`** -- the dimension cannot be observed in this
  environment (for example an `ExternalName` dependency, or a metrics capability
  while `--enable-metrics-observation` is off). See [Limitations](limitations.md).
- **`COLLECTION_FAILED`** -- the cluster query errored; check the `RuntimeObserved`
  condition and the operator logs.
- A contract that could not be obtained transiently (registry or auth error) also
  reads `Unknown` rather than `Invalid`.

## Status is `Invalid`

`Invalid` means structural validation failed or the artifact could not be parsed
(fail-closed). Check the `ContractValid` condition and `status.validation` for the
specific `SCHEMA_VIOLATION` or parse error, then validate the contract locally with
the CLI:

```bash
pacto validate oci://ghcr.io/your-org/my-service-pacto:1.2.0
```

## Status is `NonCompliant`

`NonCompliant` means at least one confirmed violation. `CONFIGURATION_ABSENT` (a
declared configuration is missing) is distinct from `CONFIGURATION_MISMATCH` (it
exists but differs) -- the finding message names which. Runtime-drift findings only
fire after the stabilization window; a transient negative reads `Unknown` until the
window elapses (tune with `--stabilization-window`).

## Status is `Reference`

The contract declares no `spec.target`, so it is reference-only: parsed and
validated, never observed. Add a `spec.target` to enable runtime observation.

## Contract not resolving

- Confirm the OCI reference is reachable and, for private registries, that a pull
  secret is set via `spec.contractRef.pullSecretRef`.
- For an unversioned reference the operator tracks the highest semver tag; a
  registry with no valid semver tags resolves nothing.
- Inspect the created `PactoRevision` resources: `kubectl get pactorevisions`.

## Metrics or health always `Unknown`

The metrics dimension returns `Unsupported` unless `--enable-metrics-observation`
is set, and active health probing (Tier A) requires `--enable-probing`. With
probing off, health uses only passive readiness-probe and EndpointSlice signals.
See [Operator configuration](operator-configuration.md).

## Operator RBAC errors

If the logs show forbidden errors reading Services, workloads or EndpointSlices,
confirm the operator's `ClusterRole` is installed. For the optional
metrics-observation feature apply the separate `metrics-observation-role` too. See
[RBAC](rbac.md).
