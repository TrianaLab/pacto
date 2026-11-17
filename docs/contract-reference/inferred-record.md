# Inferred record

`pacto.inferred.yaml` holds what Pacto completes for a service from its folder,
apart from what `pacto.yaml` declares. Pacto writes it into the bundle of a
`pactoVersion: "2.5"` contract when it packs, pushes or publishes, and never
into your folder or into `pacto.yaml`. Each value names the file and line it
was read from, how sure Pacto is and the YAML that would declare it.

A declared value always wins: Pacto never records a field that `pacto.yaml`
already declares, and it never changes `pacto.yaml`. The other sections are in
[Contract sections](sections.md) and
[Dependencies and state](dependencies-and-state.md).

## Format

```yaml
format: pacto.inferred/v1
inputs:
  commit: 4e1a9c0d...          # left out when the folder is not a git checkout
  pacto: 4.6.0                 # the Pacto that completed it
  discovery: "1"               # raised when what Pacto reads from a folder changes
  sources:                     # every file a field cites, sorted by path
    - {path: deploy/app.yaml, digest: "sha256:..."}
    - {path: openapi/v1.yaml, digest: "sha256:..."}
fields:                        # sorted by field, then source
  - field: capabilities[health]
    value: GET /healthz (readinessProbe)
    source: deploy/app.yaml:31
    confidence: likely
    declare: |
      capabilities:
        - type: health
  - field: configuration.DB_PASSWORD
    value: string, writeOnly
    source: deploy/app.yaml:24
    confidence: likely
    declare: '"DB_PASSWORD": {"type":"string","writeOnly":true,"description":"..."}'
```

- `value` is a short summary for people. A configuration key carries its type
  only, never its value, and a dependency carries the host name only, never
  the URL.
- `source` is `path:line`, or the path alone when the whole file counts, such
  as an OpenAPI document.
- `confidence` is `likely` (read from a file in the repository) or `possible`.
- `declare` is the YAML to add to `pacto.yaml`. For a configuration key it is
  the property to add to the configuration's JSON Schema.
- The record has no timestamp: the same folder at the same commit gives the
  same bytes.

| Field | Read from | Recorded unless `pacto.yaml` |
|---|---|---|
| `workload` | The Kubernetes workload kind | declares `workload` |
| `service.owner` | The workload's team label | declares `service.owner` |
| `interfaces[api]` | An OpenAPI document | declares an interface whose `ref` is that file |
| `capabilities[health]` | A readiness, liveness or startup probe | declares a `health` capability |
| `configuration.<KEY>` | Each environment key, ConfigMaps included | declares the key in a configuration schema |
| `dependencies[<name>]` | A service name inside the cluster in an environment value | declares a dependency of that name or `ref` |

## When Pacto writes it

- `pacto init` and the MCP tool `pacto_create` (with `pactify: true`) on a
  folder that holds a `pactoVersion: "2.5"` `pacto.yaml` say how many values
  it declares and how many Pacto completed. They write no file: the record is
  generated, so a committed copy would go stale on the next commit.
- An older `pactoVersion` is never completed, so its bundle keeps the same
  bytes. `init` says to set `pactoVersion: "2.5"`.
- A `--cluster` draft never completes a contract: only the folder is read.
- `pacto validate`, `pacto diff`, `pacto explain`, `pacto impact` and
  `pacto fleet --local` compute the record in memory for a local folder.
- `pacto pack`, `pacto push` and `pacto publish` put a fresh record into the
  bundle and replace any copy in the folder. With nothing inferred, the bundle
  holds no record.
- [Validation](validation.md#layer-1-structural-json-schema) refuses a record beside an older
  `pactoVersion` (`NEEDS_PACTO_2_5`), and one that is malformed or cites a file
  the bundle lacks or holds with another digest (`INFERRED_RECORD_INVALID`).

## Publishing again

When a published tag differs from the new bundle only in its inferred record,
and both records cite the same files with the same digests, `pacto publish`
reports the bundle as adopted (`only the inferred record differs ... not
republished`). Pacto first checks that the tag is signed by the identity in
`publish.sign`. An unsigned tag, or one signed by someone else, is a conflict
that needs the next version.

When the cited files changed but the declared contract did not, the plan asks
for a patch version. A contract with `publish.artifacts` takes a patch on every
new commit, because each artifact is built from that commit.

## Reading it

- `pacto explain` lists each inferred field with its source under
  **Inferred (not part of the contract)**, then the YAML to copy into
  `pacto.yaml` to declare them.
- [`pacto diff`](diff.md#inferred-record) classifies every change in the record
  as `NON_BREAKING`, so it never asks for more than a patch.
- The operational graph reads a dependency in the record as an `inferred`
  relationship with `compatibility: unknown`. Impact counts its consumer with
  confidence `inferred`, so a breaking change reads `unverified` and never
  `clear`. Dependents and the dashboard's expected dependencies list declared
  ones only.
- The dashboard shows inferred values in a collapsed **Inferred by Pacto**
  block on the service page and in the **Inferred** block of a revision. A
  mark beside each value gives its source and confidence.
- A record Pacto cannot read or verify shows as "Pacto could not read the
  inferred record", never as an empty list, and the fleet answer is `partial`.
