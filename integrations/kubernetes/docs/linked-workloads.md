# Evaluate a workload with no Pacto

A team publishes `payments` 2.3.0 with `pacto ci --publish` (or
`pacto publish --image`) and deploys it with Argo CD, Flux or any other tool.
The operator sees the Deployment run an image that the published bundle
records, and checks that workload against the `payments` 2.3.0 contract with
the same evaluation and finding codes a `Pacto` resource gets. Nobody writes a
`Pacto` resource, and the operator creates none.

## How the operator links a workload

Every minute the operator lists the Deployments, StatefulSets and CronJobs in
the namespaces it watches, with their running pods, and links each workload to a published contract by one of two signals:

| Signal | When it links | Example |
|---|---|---|
| Image digest | A running container's image digest is in the signed images record of a bundle the [organization index](../../ci.md#consumers-from-the-organization-index) names, and the record's image repository is the container's | `observed: image digest sha256:ab12… since 3m ago; no Pacto` |
| `pacto.trianalab.io/contract` annotation | The workload carries the annotation, set by the team's own chart. Use it for an image several services share | `pacto.trianalab.io/contract: oci://ghcr.io/acme/contracts/payments:2.3.0` |

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: payments
  namespace: shop
  annotations:
    pacto.trianalab.io/contract: oci://ghcr.io/acme/contracts/payments:2.3.0
```

The rules, in order:

- **A `Pacto` resource wins.** A workload a `Pacto` targets is never linked;
  the `Pacto` evaluates it. A declaration that names the workload
  ([`pacto declare`](../../operational-graph-inferred.md#when-a-declaration-replaces-an-inferred-service))
  wins over a link too.
- **The annotation wins over the digest.** Its value must be a strict OCI
  reference, `oci://host/repo:tag` or `oci://host/repo@sha256:<64 hex>`, with
  no user info and at most 255 characters. Anything else links nothing and
  reports `LINK_ANNOTATION_INVALID`, which names the workload and never repeats
  the value.
- **Credentials.** The operator pulls an annotated contract with the
  credentials it loads `Pacto` contracts with only when the organization index
  names its repository. It pulls every other annotated contract anonymously,
  so writing a Deployment never borrows the operator's registry access. It
  reads the organization index with the installation credentials, and the
  images records with the contract loader's.
- **Recent versions.** By digest, a workload links when it runs one of the
  five newest release versions of its service, or the version its image tag
  names. A workload that runs an older version with a changed image stays an
  inferred service.
- **Ambiguity never merges.** A digest the images records of two services
  hold, or containers whose digests point to two services, link nothing and
  report `INFERRED_REPLACEMENT_AMBIGUOUS`. When several release versions
  of one service record every digest a workload runs, or its pods run the
  images of different versions mid-rollout, it links to the newest with
  `LINK_VERSION_AMBIGUOUS`. Its version is then unknown: `pacto impact` reads
  it as an unverified consumer, and a dependency on it reads no version.
- **No registry, no digest.** With no `registry` value the operator links by
  annotation only, and the source state says `LINK_DIGEST_OFF`.

An images record carries a signature, and the operator does not verify it
yet. Until it does, any publisher with write access to a
contract repository can claim another team's digest from the same image
repository. The link tip in the dashboard says the record is unverified. A link only judges a workload by observation; it never
wins over a `Pacto` or a declaration.

## What the evaluation sees

The operator judges the linked workload as if a `Pacto` named it, with
`spec.target.workloadRef` set and `serviceName` set to the one Service whose
selector matches the pod template labels. With no matching Service, or more
than one, the interfaces read unknown.

- Dependencies are judged in full: a dependency on a linked provider reads
  reachable or `DEPENDENCY_UNREACHABLE`. A `Pacto` that matches the provider
  always wins over a linked workload.
- A linked workload carries no `interfaceBindings`, so each interface reads
  `OBSERVATION_UNSUPPORTED` (unknown) unless `controller.interfaceNameMatchDiscovery`
  is on and a Service port is named after the interface. To get
  `INTERFACE_ABSENT`, write a `Pacto` resource with `interfaceBindings`.
- Observation windows live in operator memory. A restart starts them again,
  so a negative becomes a violation only after a full stabilization window.

## Where the result lives

The operator writes the linked workloads to the `linked.json` key of the
`<release>-detected` ConfigMap, beside what [detection](detection.md) writes,
under its own field manager `pacto-linker`. A reader trusts that key only when
that manager owns it. The linker adds no RBAC rule and no Helm value; it runs
whenever the operator runs from the chart.

- `pacto fleet --detected k8s:<namespace>/<release>-detected` and the managed
  dashboard read the key as source kind `kubernetes-linked`. Each linked
  workload is a target of its contract revision, and the inferred service it
  showed before is [handed over](../../operational-graph-inferred.md#when-a-running-image-links-a-contract)
  to the declared service.
- The signed [target snapshot](delivery-records.md#target-snapshot) carries
  the linked targets, so `pacto impact` and `pacto ci` count them as
  consumers.
- The linked workloads hold no workload a `Pacto` targets, so `pacto ci`
  never clears a row when `--detected` is its only consumer source: add
  `--k8s` or a target snapshot. An operator that runs no linker leaves the
  linked source advisory, so it changes no answer.
- Linked targets older than three minutes read stale.

| Limitation | Meaning |
|---|---|
| `LINK_INDEX_PARTIAL` | Some index entries or images records were not read this cycle (at most 200 reads per cycle). An unread entry may hold another version or service that records the same digest, so no new workload is linked by digest until they are read; a workload linked before keeps its link; partial |
| `LINK_DIGEST_OFF` | The operator has no registry, so it links by annotation only. Informational: it never makes the source partial |
| `SOURCE_PARTIAL` | A namespace could not be read, a linked contract could not be loaded, or the operator sampled the first 20000 running pods of a scope; partial |
| `LINKED_TARGETS_CAPPED` | More linked workloads than `linked.json` holds: 640 KiB, or less when the detection key leaves less room in the 1 MiB ConfigMap. The rest are left out and the source is partial |
| `LINK_VERSION_AMBIGUOUS` | Several release versions of the service record every running digest, or its pods run the images of different versions; the newest is evaluated, impact reads the workload as an unverified consumer and a dependency on it reads no version |
| `LINK_ANNOTATION_INVALID` | The workload's `pacto.trianalab.io/contract` value is not a strict OCI reference; the workload stays inferred |
| `LINK_ANNOTATION_UNREADABLE` | The contract the annotation names could not be pulled, so the workload is unknown; partial |

With no operator, as with `pacto fleet --k8s` from a laptop and no
`--detected`, nothing observes the running digest, so the workload stays an
[inferred service](../../operational-graph-inferred.md).
