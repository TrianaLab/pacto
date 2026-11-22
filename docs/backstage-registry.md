# Export from the registry offline

[`pacto export backstage`](backstage.md) can read what your registry holds
instead of a list of repositories. Every `pacto publish`, `pacto push` and
`pacto ci --publish` writes an entry to the organization index,
`<registry>/pacto-index`, so the index already lists every published service.

## Export the index

`--index` names the index, with or without `oci://`. The export reads the
newest entry of every service the index lists, up to 2000, and pulls each
bundle by digest with the same provenance and owner checks as `--oci`. It also
reads the inferred contracts those bundles name under
`<registry>/pacto-inferred`. It needs no cluster and no delivery:

```bash
pacto export backstage --index oci://ghcr.io/acme/pacto-index \
  --target-snapshot oci://ghcr.io/acme/pacto-targets > entities.yaml
```

The file holds every published service and the external systems their
contracts and inferred records name. `--target-snapshot` adds the inferred
services the clusters published:

```yaml
kind: Component
metadata:
  name: k8s-shop-deployment-cart
  tags: [pacto-inferred]
  annotations:
    pacto.trianalab.io/provenance: inferred
    pacto.trianalab.io/source: workload
    pacto.trianalab.io/workload: Deployment prod-eu/shop/cart
spec: { type: service, owner: unknown, lifecycle: unknown }
---
kind: Resource
metadata:
  name: stripe
  annotations:
    pacto.trianalab.io/provenance: inferred
    pacto.trianalab.io/source: pod-spec,repository
    pacto.trianalab.io/inferred-contract: ghcr.io/acme/pacto-inferred/stripe
spec: { type: vendor-api, owner: unknown }
```

An inferred entity never gets an owner or a system. `pacto fleet`, `pacto tui`,
`pacto mcp` and `pacto reconcile` read `--index` the same way. `pacto impact`
and `pacto ci` read it only for consumers
([Impact consumers](impact-consumers.md#consumers-from-the-organization-index)),
so the index alone never clears a gate.

## When an entry is broken

An entry that cannot be read, or whose contract names another service, makes
the index partial. The export then writes nothing and exits with code 2, and
the message names the entry's tag so you can remove or republish it. A broken
entry holds the sync because dropping it would make Backstage orphan a
published service. Pass `--allow-partial` to write the file anyway.

An inferred contract the export cannot read never holds the sync. Its external
system stays in the file without the `pacto.trianalab.io/inferred-contract`
annotation, and the advisory source `registry-index-inferred` lists
`INFERRED_CONTRACT_UNREAD`.

## The managed dashboard

On a dashboard the operator manages, the operator sets `PACTO_DASHBOARD_INDEX`
to `<registry>/pacto-index` from the chart's `registry` value, so
`/api/backstage/entities` serves every published service and Backstage needs
one catalog location. The dashboard reads the index with
`dashboard.ociSecrets`; on a private registry, add a pull credential there or
the route answers 503. Set `dashboard.index: none` to turn it off. A dashboard
you run yourself reads the index only when you pass `--index`.

A fresh installation reads an empty index until its first publish. Once the
dashboard has listed entries, a missing index makes the route answer 503: a
moved prefix or a narrowed credential must not empty the catalog.

## Who sees what

A reader with cluster-wide read sees every service the index lists. A
namespace-scoped reader sees no service known only from the registry, except
services that declare a platform offering, which show with no targets. Mint
the route's reader as [Serve entities from the
dashboard](backstage.md#serve-entities-from-the-dashboard) shows.
