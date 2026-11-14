# Run record reference

A run record is a small JSON document that `pacto diff`, `pacto impact` and
`pacto publish` can leave behind in CI to count what Pacto catches. Records are
unsigned statistics, never evidence: nothing in Pacto that validates, diffs,
analyzes impact, publishes or delivers reads them, and the CLI never reads one
back. Recording is opt-in and best effort. A problem with recording prints one
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
`--stats-file` refuses to overwrite a file that holds something other than a run
record.

Recording prints one of these lines on stderr:

- `pacto: run record published to <repo>@<digest> (unsigned statistics)`
- `pacto: run record written to <path>`
- `pacto: warning: run record not published: <reason>`
- `pacto: warning: run record not written: <reason>`

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
| `kind` | string | always | `diff`, `impact` or `publish` |
| `service` | string | always | the contract's `service.name`; for `diff` and `impact`, the name in the new revision, so a renamed service records under its new name |
| `oldRevision` | string | when known | `diff` and `impact`: the old bundle's digest. `publish`: the highest published version below this one |
| `newRevision` | string | when known | `diff` and `impact`: the new bundle's digest. `publish`: the version being published |
| `bundleDigest` | string | `publish`, when written | the digest of the published bundle |
| `deployDigest` | string | `publish`, when it did not fail | the digest of the published deploy artifact |
| `classification` | string | when computed | `NON_BREAKING`, `POTENTIAL_BREAKING` or `BREAKING` |
| `gate` | object | `impact`, refused `publish` | the gate the run reached |
| `gate.verdict` | string | with `gate` | `impact`: `clear`, `blocked` or `unverified`. `publish`: `refused` |
| `gate.reason` | string | with `gate` | `impact`: a [gate code](#impact-gate-codes). `publish`: the first failed check (`version`, `sign`, `refs`, `sbom`, `revision`, `validation` or `publish`), else `conflict` or `unreadable` |
| `consumers` | object | `impact` | consumer counts of the change |
| `consumers.incompatible` | integer | with `consumers` | consumers that break on the change |
| `consumers.unknown` | integer | with `consumers` | consumers the evidence could not classify |
| `consumers.compatible` | integer | with `consumers` | consumers that keep working |
| `consumers.unclassified` | integer | with `consumers` | consumers with no recognised verdict |
| `consumers.blocking` | integer | with `consumers` | consumers that hold the gate: incompatible and running on a known target |
| `completeness` | string | `impact` | `complete`, `partial` or `empty`: how complete the snapshot behind the counts was |
| `asOf` | string | `impact` | the snapshot's time, RFC 3339 in UTC at full precision, equal to the `asOf` of the impact answer |
| `outcome` | string | always | `diff` and `impact`: `passed`, `failed` or `unverified`. `publish`: `published`, `unchanged`, `refused` or `failed` |
| `pactoVersion` | string | always | the Pacto release that wrote the record, for example `4.6.0` or `dev`. It is distinct from the contract's `pactoVersion` |
| `recordedAt` | string | always | when the record was written, RFC 3339 in UTC to the second |
| `repository` | string | when named | see [context fields](#context-fields) |
| `ciRunUrl` | string | when named | see [context fields](#context-fields) |
| `environment` | string | when named | see [context fields](#context-fields) |

The four consumer counts `incompatible`, `unknown`, `compatible` and
`unclassified` split the consumers with no overlap, so a reader never has to
subtract one count from another. An `unknown` consumer is never counted as
safe. `blocking` is a subset of `incompatible`. Read the counts together with
`completeness` and `asOf`: a count from a partial snapshot is a lower bound.

A record never holds a person's name, an email address, a token or any
environment variable it does not name here.

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
oras discover ghcr.io/acme/pacto-stats:pacto-stats-$(printf payments | shasum -a 256 | cut -d' ' -f1)
oras manifest delete ghcr.io/acme/pacto-stats@sha256:<record digest>
```
