# Read owners from your Backstage catalog

Backstage stays your catalog. When it already records who owns a service, the
system it belongs to and what it depends on, Pacto reads those values so
nobody writes them twice. Each one is **inferred**: context for agents and
people, never authority. Pacto only reads the catalog and never writes to
Backstage.

## Point Pacto at the catalog

`--backstage-url` takes the catalog's base URL on `pacto fleet`, `pacto impact`,
`pacto tui`, `pacto mcp --fleet` and `pacto export backstage`. The reader token
comes only from `PACTO_BACKSTAGE_TOKEN`, never from a flag, so it stays out of
your shell history and process list:

```bash
export PACTO_BACKSTAGE_TOKEN=...
pacto fleet get oci://ghcr.io/acme/ledger --backstage-url https://backstage.acme.internal
```

`pacto dashboard` takes the same flag, or `PACTO_DASHBOARD_BACKSTAGE_URL`, and
refuses to start on a URL or token it would refuse later. On the
operator-managed dashboard, set the chart value and name a Secret in the
operator namespace that holds the token:

```yaml
dashboard:
  backstage:
    url: https://backstage.acme.internal
    tokenSecret:
      name: backstage-reader
      key: token
```

The operator passes the token to the dashboard as a `secretKeyRef` and never
reads the Secret itself. A `tokenSecret.name` with no `url` fails the install,
so the token never reaches a catalog nobody configured.

Pacto reads the `entities/by-query` API of Backstage 1.12 or later. A token
travels over `https`, or over plain `http` only to a loopback host or, from a
dashboard running in the cluster, to a `.svc` Service name. A laptop that
resolves a `.svc` name through its DNS search path never sends it in clear
text.

## A detected catalog

The operator [detects](integrations/kubernetes/detection.md) an in-cluster
Backstage: a running `backstage` image, or one named `backstage-*-app` or
`backstage-*-backend`, with a Service on port 7007 in its namespace. With no
URL set, the dashboard reads that Service, always without a token, because
anyone allowed to create a Service could have created it. A catalog that
needs a token then refuses the read and the source says to set
`dashboard.backstage.url` and `tokenSecret`. Candidates in several namespaces
give no address to read: set the URL. Data sources names the Service read as
`detected: <service>.<namespace>`. `pacto fleet` and `pacto mcp` show the
detection row and never read a detected catalog.

## Who sees the values

Every dashboard user sees the owners, systems and dependencies the reader
token can read, filtered only by Pacto's namespace restrictions. Backstage's
permission framework does not apply to them. Give the token catalog read on
the entities every dashboard user may see. In a restricted view, a service
the reader may not see carries no inferred value, and an inferred edge to it
is dropped without being counted.

## What Pacto reads

Pacto reads Components only, 500 per page and at most 5000 per read, with 10
seconds per request, 16 MiB per page and no redirects:

| Backstage field | Inferred value |
|---|---|
| `spec.owner` | The owner: a `group:` ref or a bare name is a team, a `user:` ref a DRI |
| `spec.system` | The business domain, when it is a valid domain slug |
| `spec.dependsOn` | A dependency for each `component:` ref; a `resource:` ref is left out |

Pacto matches each entity to a service in this order:

1. An entity that carries `pacto.trianalab.io/provenance` came from
   [the export](backstage.md), so Pacto skips it instead of reading its own
   values back.
2. An entity with `pacto.trianalab.io/service-key` matches that service key or
   nothing.
3. Otherwise `metadata.name`, lowercased, matches the one service with that
   name in every registry, or the one inferred service whose workload has
   that name (`ledger` matches `k8s:shop/Deployment/ledger`).

An entity whose name matches no service is ignored. A name that matches
several services, or two entities that give one service different values,
leave that value out and raise one `SERVICE_FACT_AMBIGUOUS` limitation per
source that names them.

## Where the values show

`pacto fleet get` prints an inferred owner under the owner line, with its
source, entity and read time:

```text
Service: ledger
Owner: unknown
  inferred: team-ledger (backstage-catalog component:default/ledger, asOf 2026-11-14T09:00:00Z)
```

- Fleet JSON and `pacto_fleet_get` carry `inferredOwner` and
  `inferredBusinessDomain` as `{value, source, where, asOf}`, and
  `ownerProvenance: inferred` when no declared, attested or
  declared-via-target owner exists.
- `pacto tui` and the dashboard service page show the value muted next to No
  declared owner, Owner unknown or the domain, with one mark whose tooltip
  names the source.
- A dependency is an `inferred` edge whose source kind is `serviceFact`,
  muted and filterable like every inferred edge.
- [`pacto export backstage`](backstage.md#annotations) writes the inferred owner and
  domain to annotations only, with their source.

## What never changes

An inferred value never fills `owner` or the business domain and never
becomes an owner claim. It never routes an approval, never clears a gate and
never changes an impact verdict: impact never reads an inferred edge. A
service with only an inferred owner still counts as unowned, and the export
keeps `spec.owner: unknown`.

The source is advisory. An unreachable catalog is `unavailable` with the
reason, and a read stopped at 5000 Components is `partial`. Neither makes a
fleet answer partial nor blocks the export. A service the catalog says nothing
about reads exactly as it did before.
