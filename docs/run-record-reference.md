# Run record reference

A run record is a small JSON document that `pacto diff`, `pacto impact` and
`pacto publish` can leave behind in CI to count what Pacto catches. Records are
unsigned statistics, never evidence: nothing in Pacto that validates, diffs,
analyzes impact, publishes or delivers reads them. Only `pacto stats` and the
read-only MCP tool `pacto_stats` read them back, to
[total them](#the-stats-answer). Recording is opt-in and best effort. A problem with recording prints one
warning line on stderr, and the exit code and stdout stay the same as a run
without recording. MCP tools never record. `pacto impact` event answers
(`--unavailable`, `--removed` and `--regressed`) and `pacto publish --plan`
record nothing, and neither does a run that could not produce a result.

## Turn recording on

| Flag | Environment variable | What it does |
| --- | --- | --- |
| `--stats-registry` | `PACTO_STATS_REGISTRY` | publishes the record to this repository, for example `ghcr.io/acme/pacto-stats` |
| `--stats-file` | `PACTO_STATS_FILE` | writes the record to this file, for a later `pacto stats publish` |
| `--stats-context` | none | adds the [context fields](#context-fields) it names |

A flag takes precedence over its environment variable. The repository is never
defaulted, and these settings are never read from `pacto.yaml`. Credentials and
insecure registries come from the same settings every other registry command
uses (`PACTO_INSECURE_REGISTRIES` lists registries reached over plain HTTP).
An empty flag, such as `--stats-file=`, turns its environment variable off for
that command. A file holds one record: `--stats-file` replaces the record a file
already holds, with a warning, and writes an empty file like a missing one. It
refuses to overwrite a file that holds something other than a run record.
`PACTO_STATS_FILE` applies to every recording command in the job, so give each
command its own file with `--stats-file` when a job runs more than one.

Recording prints one of these lines on stderr:

- `pacto: run record published to <repo>@<digest> (unsigned statistics)`
- `pacto: run record written to <path>`
- `pacto: warning: run record not published: <reason>`
- `pacto: warning: run record not written: <reason>`
- `pacto: warning: replaced the run record of <kind> <service> in <path>`

A `diff` or `impact` run whose bundle digest cannot be computed records nothing
and says `run record not written: could not compute the bundle digest`.

`pacto stats publish <file...>` publishes record files to `--registry` (or
`PACTO_STATS_REGISTRY`). It tries every file, prints the kind, service and id
of each record it publishes, and exits `1` when any file could not be read,
parsed or published. It rejects a record whose `id` does not match its kind,
service and revisions.

## Who can read records

Anyone with pull access to the stats repository can list every recorded service
name and its consumer counts with plain `oras discover`. That listing bypasses
the restricted views of the dashboard and the fleet tools. Use a dedicated
repository whose pull access matches who may see the whole fleet. A bundle
repository is a poor fit, because its readers are usually every consumer of
the service.

The job that runs `pacto stats publish` vouches for the contents of each file
with its own credentials. The `id` check stops a forged `id`, and nothing stops
a forged service name or count. Publish only files written by trusted jobs, and
never publish files from untrusted (fork) jobs into a repository others read.

## Layout

Each service has one anchor in the stats repository, and each record is an OCI
referrer of that anchor. Records sit next to bundles and never inside them.

| Part | Value |
| --- | --- |
| Anchor artifact type | `application/vnd.pacto.stats.v1` |
| Anchor layer | `application/vnd.pacto.stats.v1+json`, holding `{"stats":"<service>"}` |
| Anchor tag | `pacto-stats-<hex sha256 of the service name>` |
| Record artifact type | `application/vnd.pacto.run.v1` |
| Record layer | `application/vnd.pacto.run.v1+json`, holding the encoded record (at most 64 KiB) |
| Record annotation | `dev.pacto.recorded-at`: the record's `recordedAt`, an unsigned ordering hint |

The tag keeps a registry that collects untagged manifests from deleting the
anchor. The `pacto-stats-` prefix keeps Pacto's anchors apart from other tools'
tags in a shared repository. Publishing the same record again changes nothing.

## Fields

Every record has `version`, `id`, `kind`, `service`, `outcome`, `pactoVersion`
and `recordedAt`. The other keys appear only where the table says.

| Key | Type | Present | Holds |
| --- | --- | --- | --- |
| `version` | string | always | `pacto.trianalab.io/run-record/v1` |
| `id` | string | always | `sha256:` and the hex SHA-256 of `kind`, `service`, `oldRevision` and `newRevision` joined by vertical bars: the distinct-change key, so reruns of one change share it |
| `kind` | string | always | `diff`, `impact` or `publish`, or `operation` for an [operation record](#operation-records) |
| `service` | string | always | the contract's `service.name`; for `diff` and `impact`, the name in the new revision, so a renamed service records under its new name |
| `oldRevision` | string | when known | `diff` and `impact`: the old bundle's digest. `publish`: the highest published version below this one |
| `newRevision` | string | when known | `diff` and `impact`: the new bundle's digest. `publish`: the version being published |
| `bundleDigest` | string | `publish`, when it did not fail | the digest of the published bundle |
| `deployDigest` | string | `publish`, when it did not fail | the digest of the published deploy artifact |
| `classification` | string | when computed | `NON_BREAKING`, `POTENTIAL_BREAKING` or `BREAKING` |
| `gate` | object | `impact`, refused `publish` | the gate the run reached |
| `gate.verdict` | string | with `gate` | `impact`: `clear`, `blocked` or `unverified`. `publish`: `refused` |
| `gate.reason` | string | with `gate` | `impact`: a [gate code](#impact-gate-codes). `publish`: the first failed check (`version`, `sign`, `refs`, `sbom`, `revision`, `validation` or `publish`), else `conflict` or `unreadable` |
| `consumers` | object | `impact` | consumer counts of the change |
| `consumers.incompatible` | integer | with `consumers` | consumers whose declared range or required guarantee rejects the new version, running or not |
| `consumers.unknown` | integer | with `consumers` | consumers the evidence could not classify |
| `consumers.compatible` | integer | with `consumers` | consumers whose declared range accepts the new version; a `BREAKING` change can still break them |
| `consumers.unclassified` | integer | with `consumers` | consumers with no recognised verdict |
| `consumers.blocking` | integer | with `consumers` | consumers that hold the gate: incompatible and running on a known target |
| `completeness` | string | `impact` | `complete`, `partial` or `empty`: how complete the snapshot behind the counts was |
| `asOf` | string | `impact` | the snapshot's time, RFC 3339 in UTC at full precision, equal to the `asOf` of the impact answer |
| `outcome` | string | always | `diff` and `impact`: `passed`, `failed` or `unverified`. `publish`: `published`, `unchanged`, `refused` or `failed` |
| `pactoVersion` | string | always | the Pacto release that wrote the record, for example `4.6.0` or `dev`. It is distinct from the contract's `pactoVersion` |
| `recordedAt` | string | always | when the record was written, RFC 3339 in UTC to the second |
| `repository` | string | when named | see [context fields](#context-fields) |
| `ciRunUrl` | string | when named | see [context fields](#context-fields) |
| `environment` | string | when named | see [context fields](#context-fields); on an operation record, the environment its Stage delivers to |

The four consumer counts `incompatible`, `unknown`, `compatible` and
`unclassified` split the consumers with no overlap, so a reader never has to
subtract one count from another. The counts come from declared ranges and
guarantees; none of them is observed runtime behaviour. An `unknown` consumer is
never counted as safe. `blocking` is a subset of `incompatible`. Read the counts together with
`completeness` and `asOf`: a count from a partial snapshot is a lower bound.

A bundle digest is the digest of the content compared. With `--old-set`,
`--new-set`, `--old-values` or `--new-values`, that content is the overridden
bundle, so its digest names no published bundle.

A record never holds a person's name, an email address, a token or any
environment variable it does not name here.

### Operation records

The delivery promoter writes an `operation` record for each delivery operation
it concludes. Its `id` is the operation's Kubernetes UID, so a record written
twice is one record, and its `outcome` is the operation's outcome: `Succeeded`,
`Failed`, `Errored`, `Cancelled`, `Expired`, `Rejected`, `Superseded` or
`Witnessed`. An operation record never holds the CI-only keys above
(`oldRevision` through `asOf`, `repository` and `ciRunUrl`), and a CI record
never holds the keys below.

| Key | Type | Present | Holds |
| --- | --- | --- | --- |
| `type` | string | always | the last segment of the operation type, such as `promote` or `rollback` |
| `verification` | string | when set | `Verified`, `AcceptedUnknown` or `Unverified` |
| `reason` | string | when it is a plain code | the outcome's machine code, such as `PlanStale` |
| `actorClass` | string | when set | `controller`, `agent`, `human` or `break-glass`, never a name |
| `delegated` | boolean | when `true` | the operation named a person it acted for (`onBehalfOf`); the person is never recorded |
| `heldFor` | string | when the operation was held | how long it last waited on a person, as a Go duration such as `14m0s` |
| `serviceDomain` | string | when the Stage's service has one | the domain of the Stage's service |
| `stage` | object | always | the Stage the operation ran on |
| `stage.namespace` | string | always | the Stage's namespace |
| `stage.name` | string | always | the Stage's name |
| `stage.cluster` | string | always | the cluster's id: its `kube-system` namespace UID |
| `to` | string | pointer operations, when a digest | the digest the operation moves to |
| `from` | string | pointer operations, when a digest | the digest the operation moves from |
| `contract` | string | when a gate cited one | the contract digest a gate cited |
| `gates` | array | when gates ran | one entry per gate result, in order |
| `gates.gate` | string | with `gates` | the gate id |
| `gates.phase` | string | with `gates` | `pre` or `verify` |
| `gates.result` | string | with `gates` | `Pass`, `Fail` or `Unknown` |
| `startedAt` | string | when admitted | when the operation was admitted, RFC 3339 |
| `finishedAt` | string | always | when the operation concluded, RFC 3339 |

`service` is the service the Stage declares. An operation record holds no
actor, approver, note, message, citation or correlation id.

### Impact gate codes

| Code | Verdict |
| --- | --- |
| `CONSUMERS_INCOMPATIBLE` | `blocked` |
| `POLICY_REFERRERS_FAIL_RUNNING` | `blocked` |
| `POLICY_REFERRERS_FAIL_UNPLACED` | `unverified` |
| `POLICY_UNCHECKED` | `unverified` |
| `SERVICE_NOT_IN_FLEET` | `unverified` |
| `ANSWER_PARTIAL` | `unverified` |
| `CONSUMERS_UNCHECKED` | `unverified` |
| `NOT_BREAKING` | `clear` |
| `POTENTIAL_BREAKING` | `clear` |
| `NO_CONSUMERS` | `clear` |

## Context fields

`--stats-context` takes a comma-separated list of names. A bare name reads its
value from the CI environment, and `name=value` gives the value explicitly. A
name that is not listed stays out of the record. When a name appears twice, the
last one wins. `--stats-context` has no environment variable, so pass it on each
command. It has no effect unless `--stats-registry` or `--stats-file` is set.

| Name | Record key | Value of a bare name |
| --- | --- | --- |
| `repository` | `repository` | `GITHUB_REPOSITORY`, else `CI_PROJECT_PATH` |
| `run-url` | `ciRunUrl` | `$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID`, else `CI_JOB_URL` |
| `environment` | `environment` | none: write `environment=<name>` |

`run-url` keeps only an `http` or `https` URL with a host, and drops its query
and fragment. A URL that carries credentials is left out. An unknown name, an
item with no name, an empty value or a rejected URL is a warning, and the record
is still written. A warning quotes only the name, never the value, because a
value can hold a secret.

## Registries without native Referrers

GHCR and released versions of the CNCF `distribution` registry have no native
Referrers API. On such a registry, run records fall back to the OCI referrers
tag `sha256-<anchor digest>`, an index that lists the anchor's records. Signed
delivery and evidence records never fall back. The fallback has two caveats:

- two records of one service published at the same moment can overwrite each
  other's index update, which drops one record;
- the index grows with every record until the registry's manifest size limit,
  after which publishing warns. [Prune](#prune-records) old records to keep it
  small.

Each publish replaces the index and leaves the previous one untagged instead of
deleting it, because GHCR and `distribution`'s default configuration refuse a
manifest delete. A registry that collects untagged manifests removes them.

## Record in CI

Set `PACTO_STATS_REGISTRY` on a job that can push to the stats repository:

```yaml
jobs:
  contract:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    env:
      PACTO_STATS_REGISTRY: ghcr.io/acme/pacto-stats
    steps:
      - uses: actions/checkout@v5
      - run: pacto diff oci://ghcr.io/acme/payments:1.2.0 . --stats-context repository,run-url
```

A job without write access writes the record to a file, and a later job in the
same trusted repository publishes it:

```yaml
jobs:
  impact:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v5
      - run: pacto impact oci://ghcr.io/acme/payments:1.2.0 . --stats-file run-record.json
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: run-record
          path: run-record.json
  stats:
    needs: impact
    if: always()
    runs-on: ubuntu-latest
    permissions:
      packages: write
    steps:
      - uses: actions/download-artifact@v4
        with:
          name: run-record
      - run: pacto stats publish --registry ghcr.io/acme/pacto-stats run-record.json
```

`if: always()` keeps the record of a run that exited `1` or `2`.

## Prune records

Pacto does not delete records. List a service's records with `oras` and delete
the ones you no longer need:

```shell
oras discover registry.example.com/acme/pacto-stats:pacto-stats-$(printf payments | shasum -a 256 | cut -d' ' -f1)
oras manifest delete registry.example.com/acme/pacto-stats@sha256:<record digest>
```

GHCR refuses a manifest delete through the registry API. Delete a record there
as a package version, in the package settings on GitHub or with the
[REST API for packages](https://docs.github.com/en/rest/packages/packages).

## The stats answer

`pacto stats` and the MCP tool `pacto_stats` total the records in a stats
repository for a window and give the same answer, from one aggregation.

| Flag | MCP argument | Default | What it does |
| --- | --- | --- | --- |
| `--registry` | `registry`, over stdio only | `PACTO_STATS_REGISTRY`; for MCP, `--stats-read-registry` first | the stats repository to read |
| `--since` | `since` | `30d` | start of the window: a duration such as `30d` or `12h`, a date or an RFC 3339 time |
| `--until` | `until` | now | end of the window, in the same forms |
| `--by` | `by` | no groups | `service`, `owner`, `domain`, `repository` or `environment` |
| `--service` | `service` | every service | totals one service |
| `--kind`, repeatable | `kinds`, comma-separated | every kind | totals these kinds |

```shell
pacto stats --registry ghcr.io/acme/pacto-stats --since 7d --by environment
```

`--output-format json` prints the answer below. Text prints a table and, when
the answer is not complete, its limitations on stderr. A missing or unreadable
record is never a zero, and a window with no records prints one sentence
instead of a table of zeros.

| Field | Holds |
| --- | --- |
| `asOf` | when the answer was computed, UTC |
| `window` | `since` (inclusive) and `until`, UTC |
| `registry` | the stats repository read |
| `provenance` | always `unsigned statistics` |
| `recordsRead` | records read, of any kind and in the window or out of it; skipped records are counted apart |
| `runs` | records in the window; records that share an `id` (retries and reruns of one change) count once, and the newest `recordedAt` (then the larger manifest digest) speaks |
| `distinctChanges` | distinct changes (`service`, `oldRevision`, `newRevision`) among `diff`, `impact` and `publish` records in the window |
| `skipped` | `malformed` (not a valid record, attached to another service's anchor or dated more than 5 minutes in the future), `oversized` (manifest or layer over 64 KiB) and `unreadable` (listed but not served); left out of every number |
| `truncated` | the bound that cut the read (1000 records, or 64 pages of one listing), empty when the read is whole. A read that takes over 30 seconds is an error |
| `hidden` | `present`: whether records name services outside the caller's view; they are never counted |
| `completeness` | `partial` when a record was skipped, the read was cut, records are outside the view, delivery was unavailable or the attribution is partial; `empty` when the window holds no records; else `complete` |
| `limitations` | a `code` and a `message` for each reason the answer is incomplete |
| `by` | what the groups are by |
| `attribution` | with `--by owner` or `--by domain`: `provenance` (always `inferred`), `asOf` and `snapshotId` of the fleet snapshot owner and domain were resolved from, and `partial` when that snapshot is partial |
| `totals` | the metrics over every record in the window; absent when there are none |
| `groups` | `key`, `label` and `metrics` per group; a value no record names groups as `unknown`, `unowned` or `none` |

### Metrics

| Metric | Definition |
| --- | --- |
| `breakingCaught` | `distinct`: changes with a `diff` or `impact` record classified `BREAKING`; `runs`: the records behind them, at most one per kind, since reruns of one change share an `id` |
| `gates` | `clear`, `blocked` and `unverified`: changes by the verdict of their newest `impact` or `publish` record that carries one; a refused publish counts nowhere |
| `consumersProtected` | `count`: `consumers.blocking` summed over changes whose newest gate is `blocked`; `unknown`: `consumers.unknown` over the same changes. A change no impact run judged adds nothing |
| `environments` | one entry per environment, alphabetical; names imply no promotion order |
| `environments.environment` | the record's `environment`, else an operation record's Stage name |
| `environments.source` | `context` (the record's `environment`) or `stage` (the Stage name) |
| `environments.operations` | operation records of every type; a type not named below counts here and in no rate |
| `environments.held` | promote operations whose `heldFor` is more than zero, of promote operations; a `heldFor` that does not parse is not held |
| `environments.failed` | promote operations that ended `Failed` or `Errored`, of promote operations |
| `environments.rolledBack` | `Succeeded` rollback operations, of `Succeeded` promote operations; the count can pass `of` when a rollback undoes a promotion from before the window |
| `environments.verificationFailed` | promote operations `Unverified`, of promote operations `Verified` or `Unverified` |
| `environments.acceptedUnknown` | promote operations `AcceptedUnknown`, neither verified nor failed |
| `environments.leadTime` | from a publish record's `recordedAt` to the first `Succeeded` and `Verified` promote per Stage whose `to` is its deploy digest (else whose `contract` is its bundle digest), within one service. It holds the nearest-rank `p50Seconds` and `p90Seconds` and the `joined` and `unjoined` promotion counts. A promotion whose `finishedAt` does not parse is unjoined. With nothing joined there is no percentile and `unknown` is `true` |
| a rate (`held`, `failed`, `rolledBack`, `verificationFailed`) | `count` and `of`; with `of` at zero, `unknown` is `true` and no rate is given |

| Limitation code | Meaning |
| --- | --- |
| `RUN_RECORDS_SKIPPED` | some records could not be read, and the message says how many are dated in the future |
| `RUN_RECORDS_TRUNCATED` | a bound cut the read |
| `NO_RUN_RECORDS` | the window holds no records: recording may be off for these jobs |
| `LEAD_TIME_UNJOINED` | some promotions have no publish record with their digest, so their lead time is unknown; the answer stays complete |
| `OUTSIDE_YOUR_VIEW` | some records name services outside your view and are left out of every number |
| `DELIVERY_UNAVAILABLE_OVER_HTTP` | a signed-in HTTP caller whose identity the server could not exchange gets no operation records |
| `ATTRIBUTION_PARTIAL` | the fleet snapshot owner and domain come from is partial |

The CLI and a stdio MCP server act as you and see every record. An HTTP MCP
server serves `pacto_stats` only when started with `--stats-read-registry`,
and a signed-in caller sees only services in their view and operation records
only where their own identity may list operations.
