# Troubleshooting

Start every investigation with the CR status: it carries the contract status,
conditions and typed findings.

```bash
kubectl describe pacto <name>
kubectl get pacto <name> -o yaml                   # the whole object
kubectl get pacto <name> -o yaml | yq '.status'    # just the status, if you have yq
```

The finding codes referenced below are defined on the
[Runtime observations](runtime-observations.md) page.

## Reading the conditions

`status.contractStatus` says *what* the verdict is. The conditions say *which
stage produced it*, which is usually what you need to fix. There are three, and
each one's `reason` is a fixed identifier you can match on:

| Condition | Status | Reason | What happened |
| --- | --- | --- | --- |
| `ContractValid` | `True` | `Parsed` | The contract loaded and passed validation. |
| `ContractValid` | `True` | `ReferenceOnly` | Same, and the contract declares no `spec.target`, so nothing further is observed. |
| `ContractValid` | `False` | `Invalid` | The contract loaded and is structurally wrong. `status.validation` names the errors. |
| `ContractValid` | `Unknown` | `Unavailable` | The contract could not be **obtained** -- registry unreachable, auth rejected, tag not found. Validity is undetermined, so `status.validation` is deliberately left empty. |
| `RuntimeObserved` | `True` | `Found` | Runtime evidence was collected. |
| `RuntimeObserved` | `False` | `ObservationFailed` | A cluster query errored. The message carries the API error. |
| `ReadinessSatisfied` | `True` | `Satisfied` | `score >= minScore`. |
| `ReadinessSatisfied` | `False` | `BelowMinScore` | The gate is not met. The message breaks the score down by claim status. |
| `ReadinessSatisfied` | `False` | `Expired` | The assessment is past its `expires:` date, which scores it 0. |

Two absences are meaningful:

- **`RuntimeObserved` is missing entirely** when the reconciliation never got as
  far as observing -- a reference-only contract, or one that failed at
  `ContractValid`. Its absence is not a failed observation.
- **`ReadinessSatisfied` is missing entirely** when the contract declares no
  `readiness:` block. The operator does not report a gate that was never
  declared.

Conditions are sticky: each keeps its `lastTransitionTime` while its status is
unchanged, and carries the `observedGeneration` it was set from. A condition
whose `observedGeneration` is behind `metadata.generation` was not re-evaluated
on the latest spec.

## Reading the events

The `Events:` block that closes `kubectl describe pacto <name>` is the
reconciliation's own account of itself. Conditions say what the state *is*;
events say what *changed*, including things no field records -- a tag being
force-pushed underneath you, or a readiness gate flipping.

```bash
kubectl describe pacto <name>                       # events are the last block
kubectl get events --field-selector involvedObject.name=<name> \
  --sort-by=.lastTimestamp
```

The operator emits exactly seven, and no others:

| Reason | Type | Emitted when | What it tells you |
| --- | --- | --- | --- |
| `ContractInvalid` | `Warning` | The contract was obtained and judged invalid | Carries the same message as the `ContractValid` / `Invalid` condition. Fix the contract. |
| `ContractUnavailable` | `Warning` | The contract could not be obtained at all | Registry unreachable, auth rejected, tag missing. See [Contract not resolving](#contract-not-resolving). |
| `ValidationFailed` | `Warning` | A contract that *did* load ends the reconcile as anything but `Compliant` or `Reference` | Carries the counts -- `ContractStatus: NonCompliant, 2 errors, 1 warnings`. `status.findings` names each one. |
| `RevisionCreated` | `Normal` | A `PactoRevision` was created for a newly resolved contract | `Created revision <name> for contract v<version>`. Expected on the first resolve and on every version change; not a problem. |
| `TagOverwritten` | `Warning` | A tag that already resolved now points at a different digest | Someone force-pushed the tag. See [Choosing a reference form](contract-bindings.md#choosing-a-reference-form). |
| `ReadinessGateUnmet` | `Warning` | The readiness gate went from met to unmet | The message breaks the score down by claim status. |
| `ReadinessRecovered` | `Normal` | The readiness gate went from unmet to met | The other half of the pair above. |

Three things about them are easy to misread:

- **`ValidationFailed` overstates the `Unknown` case.** Nothing failed
  validation when the status is `Unknown` -- an assertion could not be
  *evaluated* -- and the event's own counts say so:
  `ValidationFailed ... ContractStatus: Unknown, 0 errors, 0 warnings`. Read
  the counts, not the reason. A contract that could not be obtained at all
  never reaches this event; it gets `ContractUnavailable` instead.
- **Only the two readiness events are transition-gated.** The rest are emitted by
  the reconcile that produces them, so a contract that stays broken keeps
  producing one. Kubernetes folds repeats of the same reason and message into a
  single entry with a rising `Count`, so a `Count` of 40 means forty failed
  reconciles, not forty distinct faults.
- **Events expire.** The API server discards them after its `--event-ttl`, one
  hour by default. An absent event means nothing -- absence is not evidence that
  it never fired. Conditions and `status` are the durable record; events are the
  narration.

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
is set, and active health probing requires `--enable-probing`. Both are
controller flags the Helm chart does not expose — see
[Opt-in features](limitations.md#opt-in-features) before you try to enable them,
and [Operator configuration](operator-configuration.md) for what they do.

With probing off, health is read from the workload instead, and that fallback is
narrower than it sounds: it needs an **`httpGet` readiness probe** on the
container behind the health capability's port, plus a Ready endpoint for it. A
liveness-only pod, an `exec` or `tcpSocket` probe, or no probe at all leaves
nothing to read, and the dimension reports `Unknown`. So does a pod that has the
probe and is not Ready.

Passive observation can therefore confirm health but never contradict it. A
declared health endpoint is reported *absent* only when an active probe gets a
`404` on it for longer than the
[stabilization window](limitations.md#stabilization-delay) — which is another way
of saying that `Unknown` here is the honest answer, not a broken one.

## Operator RBAC errors

If the logs show forbidden errors reading Services, workloads or EndpointSlices,
confirm the operator's `ClusterRole` is installed. See [RBAC](rbac.md).

The optional metrics-observation feature needs an additional
`metrics-observation-role` ClusterRole, which the Helm chart does not package —
see [Opt-in features](limitations.md#opt-in-features) for what is and is not
reachable through the chart.
