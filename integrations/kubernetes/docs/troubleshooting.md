# Troubleshooting

Start every investigation with the CR status: it carries the contract status,
conditions and typed findings.

```bash
kubectl describe pacto <name>
kubectl get pacto <name> -o yaml                   # the whole object
kubectl get pacto <name> -o yaml | yq '.status'    # just the status, if you have yq
```

The finding codes below are defined on
[Runtime observations](runtime-observations.md).

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
  far as observing — a reference-only contract, or one that failed at
  `ContractValid`. That is not a failed observation.
- **`ReadinessSatisfied` is missing entirely** when the contract declares no
  `readiness:` block.

Conditions are sticky: each keeps its `lastTransitionTime` while its status is
unchanged and carries the `observedGeneration` it was set from, so one behind
`metadata.generation` was not re-evaluated on the latest spec.

## Reading the events

Conditions say what the state *is*; the `Events:` block that closes
`kubectl describe pacto <name>` says what *changed*, including things no field
records — a tag force-pushed underneath you, or a readiness gate flipping.

```bash
kubectl describe pacto <name>                       # events are the last block
kubectl get events --field-selector involvedObject.name=<name> \
  --sort-by=.lastTimestamp
```

The operator emits exactly eight, and no others:

| Reason | Type | Emitted when | What it tells you |
| --- | --- | --- | --- |
| `ContractInvalid` | `Warning` | The contract was obtained and judged invalid | Carries the same message as the `ContractValid` / `Invalid` condition. Fix the contract. |
| `ContractUnavailable` | `Warning` | The contract could not be obtained at all | Registry unreachable, auth rejected, tag missing. See [Contract not resolving](#contract-not-resolving). |
| `ValidationFailed` | `Warning` | A contract that *did* load ends the reconcile as anything but `Compliant` or `Reference`, and the status differs from the one the previous reconcile persisted | Carries the counts -- `ContractStatus: NonCompliant, 2 errors, 1 warnings`. `status.findings` names each one. |
| `ContractRecovered` | `Normal` | The contract status returned to `Compliant` or `Reference` from any other value | The other half of the three warnings above. |
| `RevisionCreated` | `Normal` | A `PactoRevision` was created for a newly resolved contract | `Created revision <name> for contract v<version>`. Expected on the first resolve and on every version change; not a problem. |
| `TagOverwritten` | `Warning` | A tag that already resolved now points at a different digest | Someone force-pushed the tag. See [Choosing a reference form](contract-bindings.md#choosing-a-reference-form). |
| `ReadinessGateUnmet` | `Warning` | The readiness gate went from met to unmet | The message breaks the score down by claim status. |
| `ReadinessRecovered` | `Normal` | The readiness gate went from unmet to met | The other half of the pair above. |

Three things about them are easy to misread:

- **`ValidationFailed` overstates the `Unknown` case.** Nothing failed
  validation there — an assertion could not be *evaluated* — and the event's own
  counts say so: `ContractStatus: Unknown, 0 errors, 0 warnings`. Read the
  counts, not the reason.
- **Every event except `RevisionCreated` and `TagOverwritten` is transition-gated.**
  A contract that stays broken emits one event, not one per reconcile, so a
  `Count` above 1 means the status actually flapped. `TagOverwritten` is the
  exception that still repeats: see
  [Choosing a reference form](contract-bindings.md#choosing-a-reference-form).
- **Events expire**, after the API server's `--event-ttl` of one hour by
  default. An absent event is not evidence that it never fired: conditions and
  `status` are the durable record.

## Chasing the evidence behind a finding

Every finding carries `evidenceRefs`, and it is worth knowing what they are
before you go looking for something that is not there:

```yaml
evidenceRefs:
  - source: k8s
    observedAt: "2026-08-22T21:31:04Z"
```

That is the whole record — which collector saw it and when. There is no body to
fetch: the operator's observations are **recomputed every reconcile and never
stored**, so the reference dates the reading rather than retrieving it. What it
is good for is age: an `observedAt` that stopped advancing while
`metadata.generation` moved on is a leftover from a reconcile that no longer
runs.

To re-observe, nudge the resource. Any update enqueues a reconcile, and the
annotation key is arbitrary:

```bash
kubectl annotate pacto <name> reconcile-requested-at="$(date -u +%FT%TZ)" --overwrite
```

Observations that must outlive the reconcile that made them — an audit trail, a
signed record — are the [Evidence Server](../../evidence.md), which writes to
your registry. Cluster status is a live reading, not a log.

## Status is `Unknown`

A required assertion could not be evaluated — not a violation. The finding code
says why: `EVIDENCE_MISSING`, `OBSERVATION_UNSUPPORTED` or `COLLECTION_FAILED`,
each defined on [Runtime observations](runtime-observations.md). A contract that
could not be obtained transiently also reads `Unknown` rather than `Invalid`.

## Status is `Invalid`

Structural validation failed or the artifact could not be parsed (fail-closed).
`status.validation` carries the `SCHEMA_VIOLATION` or parse error; reproduce it
locally:

```bash
pacto validate oci://ghcr.io/your-org/my-service-pacto:1.2.0
```

## Status is `NonCompliant`

At least one confirmed violation. The two configuration codes do not arrive at
the same speed: a **mismatch** (`CONFIGURATION_MISMATCH`) is `NonCompliant` on
the first reconcile that observes it, while an **absence**
(`CONFIGURATION_ABSENT`) reads `Unknown` until the negative streak spans the
[stabilization window](limitations.md#stabilization-delay).

## Status is `Reference`

The contract declares no `spec.target`: parsed and validated, never observed.
Add a `spec.target` to enable runtime observation.

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
[Opt-in features](limitations.md#opt-in-features).

With probing off, health falls back to the workload, which needs an **`httpGet`
readiness probe** on the container behind the health capability's port plus a
Ready endpoint for it. A liveness-only pod, an `exec` or `tcpSocket` probe, or
no probe leaves nothing to read. Passive observation can confirm health but
never contradict it, so `Unknown` is the honest answer here, not a broken one.

## Operator RBAC errors

If the logs show forbidden errors reading Services, workloads or EndpointSlices,
confirm the operator's `ClusterRole` is installed. See [RBAC](rbac.md). Metrics
observation needs an additional `metrics-observation-role` ClusterRole the chart
does not package — again
[Opt-in features](limitations.md#opt-in-features).
