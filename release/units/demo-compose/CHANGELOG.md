# @pacto/demo-compose

## 4.6.0

### Minor Changes

- bd0bdc4: `pacto impact` and `pacto ci` find the consumers that run without cluster
  credentials. From the change's `oci://` ref they try
  `<host>/<first path segment>/pacto-targets`, then `<host>/pacto-targets`, read
  every `cluster-<uid>` snapshot of the first that exists and print a `Registry:`
  line and one `Targets:` line per snapshot, pinned by digest. `--target-snapshot`
  (repeatable) names a repository or pins cluster tags or digests, and `off`
  reads none; `--delivery-trust` or `$PACTO_DELIVERY_TRUST` names the trust
  config. A snapshot counts only when a key whose `streams` name its cluster's
  UID verified it and its `asOf` is less than 15 minutes old; otherwise its
  source reads `partial`, `stale` or `unavailable` with one of
  `TARGET_SNAPSHOT_UNVERIFIED`, `TARGET_SNAPSHOT_UNSCOPED`,
  `TARGET_SNAPSHOT_STALE`, `TARGET_SNAPSHOT_INVALID` or `TARGET_SNAPSHOT_BOUND`,
  and a `BREAKING` change with consumers never clears. No `pacto-targets`
  repository leaves today's answer and exit code. `--root` in the CI recipe is
  now optional, for consumers that run nowhere yet.
  
  A snapshot becomes up to three fleet sources per cluster: `target-snapshot`
  for its Pacto resources, `kubernetes-workloads` for its inferred services,
  which never count as consumers, and `evidence-http` for the targets its
  Evidence Server accepted. Source states gain `ref`, `signer`, `publisher` and
  `cluster`, and `impact` answers gain `registry` and `targetSnapshots`, also on
  the `pacto_impact` MCP tool over stdio. `pacto mcp` over HTTP refuses
  `--target-snapshot`. `pacto delivery targets publish` signs and pushes a
  snapshot document with a PEM key or a `pacto evidence keygen` seed.
  
  The operator publishes the cluster's signed snapshot to
  `<registry>/pacto-targets:cluster-<uid>` every 5 minutes when the chart's new
  top-level `registry` value is set: the cluster's Pactos, the workloads no
  Pacto targets (without env values or probes) and, with `evidence.enabled`, the
  Evidence Server's targets, each with its own `asOf` and completeness. Give
  `pacto-targets` its own read ACL: pull on it reads every namespace's Pacto
  status and workload shape.
  
  The Compose demo runs a `targets` service that publishes a signed snapshot of
  the demo cluster to `registry:5000/demo/pacto-targets` every 5 minutes.
- 73ce984: Pacto publishes an inferred contract for every external system your services call. Once `registry` is set the operator writes `<registry>/pacto-inferred/<identity>` every 5 minutes, signed with the installation key and annotated `run.pacto.contract.provenance: inferred`, and only when the hosts, ports or client majors it sees change; `pacto fleet publish-inferred` does the same where no operator runs. The 2.5 contract field `service.external` names the system and the `pacto.inferred.yaml` section `surface` records what Pacto saw with its sources, catalog digest and first and last seen; it never names a consumer. A range against an inferred contract reads `rangeStatus: unknown` with the new `rangeReason: INFERRED_PROVIDER` and gates as unverified. A revision only the operator signed reads `unverified` with the new signature source `operator` and an `INFERRED_PROVIDER` attention item suggests `pacto declare`. The Compose demo publishes `demo/pacto-inferred/stripe`.

## 4.5.0

No changes in this release.

## 4.4.2

No changes in this release.

## 4.4.1

No changes in this release.

## 4.4.0

No changes in this release.

## 4.3.0

No changes in this release.

## 4.2.0

No changes in this release.

## 4.1.0

No changes in this release.

## 4.0.0

## 3.3.6

## 3.3.5

## 3.3.4

## 3.3.3

## 3.3.2

## 3.3.1

## 3.3.0

## 3.2.9

## 3.2.8

## 3.2.7

## 3.2.6

## 3.2.5

## 3.2.4

## 3.2.3

## 3.2.2

## 3.2.1

## 3.2.0
