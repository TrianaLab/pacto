# Read owners from your Backstage catalog

Backstage stays your catalog. When it already records who owns a service, the
system it belongs to, what it depends on and which software templates create
services, Pacto reads those values so nobody writes them twice. Each one is **inferred**: context for agents and
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
give no address to read: set the URL, and so does a detection that sampled
part of a large cluster's pods. Data sources marks the source `detected`, and
the Detected providers row names the Service read. `pacto fleet` and
`pacto mcp` show the detection row and never read a detected catalog.

## Who sees the values

Every dashboard user sees the owners, systems and dependencies the reader
token can read, filtered only by Pacto's namespace restrictions. Backstage's
permission framework does not apply to them. Give the token catalog read on
the entities every dashboard user may see. In a restricted view, a service
the reader may not see carries no inferred value, and an inferred edge to it
is dropped without being counted.

## What Pacto reads

Pacto reads Components and [software templates](#software-templates), 500 per page and at most 5000 per read, with 10
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
- The dashboard service page shows the value muted next to No declared
  owner, Owner unknown or the domain, with one mark whose tooltip names the
  source. `pacto tui` shows the inferred owner the same way.
- A dependency is an `inferred` edge whose source kind is `serviceFact`,
  muted and filterable like every inferred edge.
- [`pacto export backstage`](backstage.md#annotations) writes the inferred owner and
  domain to annotations only, with their source.

## Software templates

Pacto reads every Template entity whose `apiVersion` starts with
`scaffolder.backstage.io/` from the same pages: its name, namespace, title,
description, owner and type. Each one is an
[inferred golden path](golden-paths.md#find-one), listed apart from the
declared ones:

```text
$ pacto fleet entities --template --backstage-url https://backstage.acme.internal
1 of 1 entit(ies):
  golden path http-service                 http-service                     NotEvaluated
    open: https://backstage.acme.internal/create/templates/default/http-service
Inferred golden paths (complete, asOf 2026-11-23T09:00:00Z):
  springboot-service  inferred from Backstage (backstage.acme.internal)  template:default/springboot-service
    open: https://backstage.acme.internal/create/templates/default/springboot-service
    accept: pacto declare template:default/springboot-service --registry <registry> --backstage-url https://backstage.acme.internal
```

- The link is `/create/templates/<namespace>/<name>` under the configured
  URL. A detected in-cluster Service gets no link, because a browser cannot
  open it, and no accept command: set the URL to
  [accept](#accept-a-template-as-a-golden-path) its templates.
- A template named like a declared golden path is that path: its row shows
  the link and the template is listed once. A name another service holds is
  listed with no accept.
- A name a contract cannot carry is skipped and named in
  `INFERRED_TEMPLATES_SKIPPED`. At most 200 templates are listed; past that
  the block is `partial` with `INFERRED_TEMPLATES_CAPPED`.
- Every viewer, restricted ones included, sees each template the reader token
  reads.
- An unavailable or partial catalog makes the block `partial` with the
  reason. It never reads as "no templates".

With no Backstage in a watched namespace, Detected providers reads
`Backstage catalog: not detected`, partial, because an external catalog is
invisible to the cluster. The dashboard and `pacto fleet` hide that row when
a URL is set, since the source's own row then speaks for it.

## Accept a template as a golden path

Accepting a software template turns it into a declared golden path, through
the same flow as [`pacto declare`](publishing.md#declare-a-running-service):

```bash
export PACTO_BACKSTAGE_TOKEN=...
pacto declare template:default/springboot-service \
  --registry ghcr.io/acme/paths --backstage-url https://backstage.acme.internal
```

Without `--yes` it prints the plan and writes nothing. The plan drafts a
`pacto.yaml` that names the template, takes its description or title as
`service.summary` and declares `service.template`. The golden path holds the
contract only: Backstage keeps the template's steps and skeleton, and "Open in
Backstage" stays the way to scaffold code from it. The owner Backstage names
is a plan note: declare `service.owner` in a later version. With `--yes` you
sign the bundle at `<registry>/<name>`.

An agent calls `pacto_declare` with `service: template:<namespace>/<name>` and
writes a draft a person accepts with `--from-draft`. The dashboard's **Accept
as golden path** button opens the same plan.

Pacto accepts a template only when:

- it comes from the catalog `--backstage-url` or `dashboard.backstage.url`
  names, never from a detected one;
- `<registry>/<name>` holds no version yet; later versions go through
  `pacto publish`;
- on a shared dashboard, you may `update pactos` in the installation
  namespace.

## What never changes

An inferred value never fills `owner` or the business domain and never
becomes an owner claim. It never routes an approval, never clears a gate and
never changes an impact verdict: impact never reads an inferred edge. A
service with only an inferred owner still counts as unowned, and the export
keeps `spec.owner: unknown`.

The source is advisory. An unreachable catalog is `unavailable` with the
reason. A read stopped at 5000 Components or 10 pages, or one that left out
the component dependencies past the first 50 of a Component, is `partial`.
Neither makes a fleet answer partial nor blocks the export, and `pacto fleet`
text output adds a note saying the catalog was not fully read. A service the
catalog says nothing about reads exactly as it did before. A Component whose
owner is the placeholder `unknown` has no inferred owner.
