# Measure what Pacto does

Pacto can record what it catches as unsigned statistics in a registry you name;
`pacto stats` and the dashboard Metrics page total them, and nothing leaves your
registry. This guide shows how to turn recording on, read the totals and keep
the records small. The [run record reference](run-record-reference.md) lists
every field a record holds.

## What is recorded

A run record is a small JSON document. Pacto writes one for each `pacto diff`,
`pacto impact` and `pacto publish` run in CI that reaches a result, one for
each delivery operation the promoter concludes and, when you ask
`pacto fleet status` for it, one coverage snapshot of the fleet. `pacto impact`
event answers, `pacto publish --plan` and MCP calls record nothing. Recording is
opt-in and best effort: a problem with recording prints one warning line, and
the command's exit code and output stay the same.

A record holds no person's name, email address or token. The CI context fields
(`repository`, `ciRunUrl` and `environment`) appear only when you list them in
`--stats-context`, and an operation record holds the class of its actor, never
a name.

## Where records go

Records go to the repository you name with `--stats-registry` or
`PACTO_STATS_REGISTRY`, for example `ghcr.io/acme/pacto-stats`. Pacto never
picks one for you. Each service has one anchor in that repository, tagged
`pacto-stats-<sha256 of the service name>`, and each record is an OCI referrer
of that anchor, next to your bundles and never inside them. Every record stays
in the registry you name. Pacto sends nothing to TrianaLab or any other party.

Anyone with pull access to the repository can list every recorded service name
with plain `oras discover`, so give it pull access that matches who may see the
whole fleet.

## Turn it on in CI

Set the repository on a job that can push to it:

```yaml
- run: pacto diff oci://ghcr.io/acme/payments:1.2.0 . --stats-context repository
  env:
    PACTO_STATS_REGISTRY: ghcr.io/acme/pacto-stats
```

A job without write access writes the record to a file with
`--stats-file run-record.json` and uploads it as an artifact. A later job of
the same trusted repository publishes it:

```shell
pacto stats publish --registry ghcr.io/acme/pacto-stats run-record.json
```

The [reference](run-record-reference.md#record-in-ci) has the complete two-job
workflow. Never publish files written by untrusted (fork) jobs.

## Turn it on in delivery

The promoter records every operation it concludes when the chart names a
repository:

```shell
helm upgrade pacto-operator oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --reset-then-reuse-values \
  --set delivery.stats.repository=ghcr.io/acme/pacto-stats
```

[Delivery run records](integrations/kubernetes/delivery-run-records.md) covers
the push credentials and why the repository must be private.

## Read the totals

`pacto stats` totals the records of a window. With `--by owner` it infers each
service's owner from the fleet snapshot the fleet source flags build:

```shell
pacto stats --registry ghcr.io/acme/pacto-stats --since 30d --by owner
```

```text
asOf 2026-11-15T18:44:53Z  window 2026-10-16T18:44:53Z to 2026-11-15T18:44:53Z  records 112 read, 0 skipped (complete)  unsigned statistics; owner inferred as of 2026-11-15T19:44:53+01:00
OWNER     BREAKING CAUGHT  CONSUMERS PROTECTED  CLEAR  BLOCKED  UNVERIFIED
checkout  0 (0 runs)       0 (0 unknown)        4      0        0
orders    0 (0 runs)       0 (0 unknown)        0      0        0
payments  1 (2 runs)       1 (0 unknown)        3      1        0

Environments (alphabetical)
ENVIRONMENT  OPERATIONS  HELD        FAILED     ROLLED BACK  VERIFICATION FAILED  LEAD TIME p50  p90
prod         13          2/13 (15%)  0/13 (0%)  0/13 (0%)    0/13 (0%)            21h0m0s        22h0m0s
test         9           0/9 (0%)    0/9 (0%)   0/9 (0%)     0/9 (0%)             2h0m0s         2h0m0s
```

The read-only MCP tool `pacto_stats` gives the same answer to an agent, and
`pacto dashboard --stats-registry` draws it as trends on the Metrics page, each
number linked to the records behind it. A rate with nothing to take it over and
a lead time with no publish record to join are shown as unknown, never as 0.
Every answer carries its `asOf`, its completeness and how many records it read
and skipped.

![The dashboard Metrics page: breaking changes caught, gates, delivery rates and lead time per environment, labelled unsigned statistics](assets/screenshots/dashboard-metrics-light.png#only-light)
![The dashboard Metrics page: breaking changes caught, gates, delivery rates and lead time per environment, labelled unsigned statistics](assets/screenshots/dashboard-metrics-dark.png#only-dark)

## Run records are unsigned statistics

Run records are unsigned statistics. Anyone with push access to the repository
can add or change them, so nothing in Pacto that decides reads them: validation,
diff, impact, publish and delivery gates work the same with recording on or
off. Read the numbers as a trend of what Pacto caught, and use signed delivery
records and evidence for anything you have to prove.

## Who sees which records

The CLI and a stdio MCP server act as you and see every record. A
[signed-in dashboard](integrations/kubernetes/dashboard-authentication.md) and
an HTTP MCP server started with `--auth-issuer` show the records of services
the viewer may list Pactos for, and operation records only in namespaces where
the viewer may list Operations. Records outside the view are left out of every
number and never named: the answer says some records are outside your view and
is marked partial. Coverage history is shown only to readers who see the whole
fleet. [Dashboard access](integrations/kubernetes/dashboard-access.md#metrics)
has the details.

## Registries without native Referrers

GHCR and released versions of the CNCF `distribution` registry have no native
Referrers API. On those, run records fall back to the OCI referrers tag, an
index of the anchor's records. The fallback has two caveats: two records of one
service published at the same moment can drop one of them, and the index grows
with every record, so prune it.

## Prune records

Pacto never deletes records. List a service's records with `oras`, pick the old
ones by their `dev.pacto.recorded-at` annotation and delete them:

```shell
repo=registry.example.com/acme/pacto-stats
anchor=pacto-stats-$(printf payments | shasum -a 256 | cut -d' ' -f1)
oras discover --format json "$repo:$anchor" |
  jq -r '.manifests[] | select(.annotations["dev.pacto.recorded-at"] < "2026-01-01") | .digest' |
  while read -r digest; do oras manifest delete --force "$repo@$digest"; done
```

GHCR refuses a manifest delete through the registry API; delete a record there
as a package version.

## Try it in the demos

- The [browser demo](examples/dashboard-demo.md) opens Metrics with about 90
  days of generated records that follow the payments story.
- The [Compose demo](examples/compose-demo.md) seeds CI records into its own
  registry.
- The [Kubernetes demo](examples/kubernetes-demo.md) records its real publishes
  and promoter operations on top of a seeded history.
