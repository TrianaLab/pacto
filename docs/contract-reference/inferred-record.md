# Inferred record

`pacto.inferred.yaml` holds what Pacto completes for a service from its folder,
apart from what `pacto.yaml` declares. Pacto writes it into the bundle of a
`pactoVersion: "2.5"` contract when it packs, pushes or publishes, and never
into `pacto.yaml`. Only a `--cluster` draft gets a copy in its folder. Each value names the file and line it
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
  discovery: "5"               # raised when what Pacto reads from a folder changes
  sources:                     # every file a field cites, sorted by path
    - {path: deploy/app.yaml, digest: "sha256:..."}
    - {path: openapi/v1.yaml, digest: "sha256:..."}
    - {path: "repository:.github/CODEOWNERS", digest: "sha256:..."}
  chartDependencies:           # each chart dependency Chart.lock pins
    - {chart: chart, name: redis, version: 19.0.1, repository: "https://charts.example", digest: "sha256:..."}
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
  - field: ports[8080]
    value: 8080/tcp
    source: Dockerfile:12
    confidence: likely
    outranks: [deploy/app.yaml:18]
  - field: service.owner
    value: payments
    source: catalog-info.yaml:7
    confidence: likely
    declare: |
      service:
        owner:
          team: payments
    class: ownerFile
    outranks: ["repository:.github/CODEOWNERS:4"]
findings:                      # what Pacto recognized and could not read
  - code: DISCOVERY_CHART_DEPENDENCY_UNLOCKED
    source: chart/Chart.yaml:9
    message: chart dependency 'redis' has no Chart.lock digest, not read
```

- `value` is a short summary for people. A configuration key carries its type
  only, never its value, and a dependency carries the host name only, never
  the URL.
- `source` is `path:line`, or the path alone when the whole file counts, such
  as an OpenAPI document. A file at the repository root outside the folder,
  only CODEOWNERS, is `repository:<path>:<line>`, relative to the repository
  root. It is not in the bundle, so `inputs.sources` cites it with its digest
  and a change to it changes the record. A version derived by
  `pacto ci --publish` cites `registry:<repo>@sha256:<digest>`, the verified
  baseline it was raised from, or `registry:<repo>` when the repository held
  no version. An image passed on the command line cites `flag:--image`.
- `class: ownerFile` marks a `service.owner` read from CODEOWNERS or
  `catalog-info.yaml`, the only value and the only field it goes on.
  `pacto init` shows it as "attested". The marker alone raises no trust: the
  owner stays inferred unless a CI identity the publisher rule accepts signed
  the bundle at the commit `inputs.commit` names, which makes it
  [attested](../owner-setting.md#attested-owners). Neither ever clears a gate.
- `outranks` lists, sorted, the other sources that gave the same field. The
  ranks are in
  [What Pacto reads from your folder](../pactify.md#what-pacto-reads-from-your-folder).
- `findings` lists each file Pacto recognized and could not read, with a code,
  a `path:line` and a fixed message that never quotes a value.
  `DISCOVERY_TRUNCATED` comes first, then the rest by code and source. At most
  100 are kept; `findingsDropped` counts the rest.
- `inputs.chartDependencies` lists each chart dependency `Chart.lock` pins,
  with the repository URL stripped of credentials. Pacto never downloads one.
- `inputs.acceptances` lists each signed acceptance `pacto ci --publish`
  carried from the previous version: the record as `<repository>@sha256:<hex>`,
  its signer and the fields it pinned into the published `pacto.yaml` (or the
  break it accepts). The next run reads them again and drops one that no longer
  verifies.
- `inputs.declaration` cites the [registry declaration](../publishing.md#fields-declared-in-the-registry)
  `pacto ci --publish` carried fields from: `record`, the declaration bundle as
  `<repository>@sha256:<64 hex>`; `signer`, as `<subject> (<issuer>)`; and
  `fields`, the keys it pinned into the published `pacto.yaml`, comma-separated
  and sorted, each `service.owner`, `dependencies[<name>]` or
  `dependencies[<name>].compatibility`. Each later carry cites the same
  declaration, and a key it lists that `pacto.yaml` does not declare fails
  validation (`CARRIED_FIELD_NOT_DECLARED`):

  ```yaml
  inputs:
    declaration:
      record: ghcr.io/acme/invoices@sha256:5a54bebec06a5e0c1f8d2b3a4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f
      signer: ines@acme.com (https://accounts.google.com)
      fields: dependencies[ledger],service.owner
  ```

- `confidence` is `likely` (read from a file in the repository) or `possible`.
- `declare` is the YAML to add to `pacto.yaml`. For a configuration key it is
  the property to add to the configuration's JSON Schema.
- A record completed from a folder has no timestamp: the same folder at the
  same commit gives the same bytes. Only a `--cluster` draft's record carries
  read times ([Cluster facts](#cluster-facts)).

| Field | Read from | Recorded unless `pacto.yaml` |
|---|---|---|
| `workload` | The Kubernetes workload kind | declares `workload` |
| `service.owner` | `catalog-info.yaml`, CODEOWNERS or the workload's team label | declares `service.owner` |
| `service.summary` | The Dockerfile's `org.opencontainers.image.description` label | declares `service.summary` |
| `service.domain` | `spec.system` in `catalog-info.yaml` | declares `service.domain` |
| `interfaces[api]`, `interfaces[events]`, `interfaces[grpc]` | An OpenAPI, AsyncAPI or gRPC file at any path | declares an interface of that name or whose `ref` is that file |
| `ports[<n>]` | `EXPOSE`, a `containerPort`, Helm values or Compose `ports` | declares an interface on that `port`; a port reaches `interfaces[].port` only when a person declares it |
| `image` | Helm values, Kustomize `images`, Compose or a manifest | is never compared |
| `environments[<env>].file` | `environments/<env>.yaml`, a Helm values file or a Kustomize overlay, confirmed by an Argo CD or Flux object | declares an environment of that name |
| `capabilities[health]` | A readiness, liveness or startup probe, else the Dockerfile `HEALTHCHECK` | declares a `health` capability |
| `configuration.<KEY>` | Each environment key, ConfigMaps included | declares the key in a configuration's `values` or schema `properties`, or declares a configuration whose keys Pacto cannot list (a `ref`, or a schema it cannot read or that uses `allOf`, `anyOf`, `oneOf`, `$ref` or `patternProperties`) |
| `dependencies[<name>]` | A service name inside the cluster in an environment value, or `spec.dependsOn` in `catalog-info.yaml` | declares a dependency of that name or `ref` |
| `service.version` | The verified baseline and the change since it, when `pacto ci --publish` or `pacto publish` derives it, such as `1.4.3 (1.4.2 + patch, NON_BREAKING)` or `1.0.0 (no versions in <repo>)`. The bundle's `pacto.yaml` carries the same version | declares `service.version` |
| `publish.build` | The build tool the folder shows, or `image supplied, not built by Pacto: <ref>` for `--image` or a workload image pinned by digest | declares `publish` |
| `publish.deploy` | The folder's Kustomization, Helm chart or manifests, and the image pinned by digest | declares `publish` |

### External dependencies

A vendor API, managed cloud service or legacy host the folder names is a
`dependencies[<name>]` field with an `external` member, one per identity, or
per identity and resource name for a managed resource whose account, region or
id the folder does not show. The field's `value` is the identity, its `source` the
first place that names it and its `confidence` the strongest source's.
`inputs.catalog` lists the digest of each catalog that classified, default
first:

```yaml
inputs:
  catalog:
    - default sha256:62a3fd52...
fields:
  - field: dependencies[stripe]
    value: stripe
    source: chart/values.yaml:9
    confidence: likely
    external:
      identity: stripe
      catalog: default              # the catalog that classified it: default or organization
      sources:                      # sorted; every file is also in inputs.sources
        - {kind: helmValues, path: chart/values.yaml, line: 9, key: STRIPE_API_BASE, host: api.stripe.com}
        - {kind: sdk, path: requirements.txt, line: 1}
      client: {sdk: stripe, major: 11}   # major left out when no lockfile or exact range gives it
  - field: dependencies[newsletter-signups]
    value: aws/sqs
    source: infra/main.tf:1
    confidence: possible
    external:
      identity: aws/sqs
      catalog: default
      sources:
        - {kind: terraform, path: infra/main.tf, line: 1, key: aws_sqs_queue.signups}
      resource: {type: aws_sqs_queue, name: newsletter-signups, missing: [account, region]}
```

- `kind` is `env`, `configMap`, `secretKeyRef`, `secretRef`, `helmValues`,
  `compose`, `kustomize`, `sdk`, `terraform` or `crossplane`.
- `host` is a host and an optional port, `key` a configuration key name and
  `path` a relative path inside the folder. Pacto refuses a record that holds a
  user name, a password, a query or a path outside the folder in any of them.
- `missing` names only `account`, `region` and `id`; the
  [external catalog reference](../external-catalog-reference.md#managed-resource-identities)
  says when a managed resource gets its full identity.
- How `pacto init` finds them is in
  [External dependencies](../pactify.md#external-dependencies).

### Migrations

When [`pacto ci`](../ci.md#older-contracts) raises an older `pactoVersion` in
memory, the bundle's record says so. A record can hold `migrations` and an
empty `fields`:

```yaml
format: pacto.inferred/v1
inputs:
  commit: 4e1a9c0d...
  pacto: 4.6.0
  discovery: "1"
  sources: []
fields: []
migrations:
  - from: "2.4"                # the version pacto.yaml declares in your folder
    to: "2.5"                  # the version the bundled copy declares
    source: pacto.yaml
    by: pacto ci 4.6.0
```

- Entries are sorted by `source`, then `from`. Each needs all four keys, a
  `from` and `to` this Pacto knows with `from` older than `to`, and a `source`
  no other entry names.
- Every entry must match the bundle it sits in: `source` is a file in the
  bundle and the `pactoVersion` it declares equals `to`. A stale or edited
  entry makes the record `INFERRED_RECORD_INVALID`, and the fleet then shows
  no migration.
- `source` is not listed in `inputs.sources`: its bytes are the raised ones.

### Cluster facts

A [`--cluster` draft](../bootstrap-contracts.md#the-inferred-record-of-a-draft)
writes its record into the draft folder, completed from the folder and then
from what the cluster showed. A cluster entry is added only for a field the
folder did not give.

```yaml
inputs:
  cluster:
    asOf: 2026-11-14T10:02:00Z     # when the cluster was read
    object: shop/Deployment/invoices
    callWindow: 24h
    maxItems: 10000
fields:
  - field: dependencies[ledger].compatibility
    value: compatibility unknown
    proposed: ^2.3.0
    source: cluster:fleet reported versions 2.3.1
    confidence: likely
    asOf: 2026-11-14T09:58:12Z
    completeness: partial
    declare: 'compatibility: "^2.3.0"'
coverage:                          # always in this source order
  - {source: workloads, state: complete}
  - {source: endpointslices, state: complete}
  - {source: runtime-calls, state: partial, reason: 2 of 3 nodes observed}
hidden: {services: 1, callers: 2}
```

| Field | Read from | Source | Recorded unless `pacto.yaml` |
|---|---|---|---|
| A field of the table above | The draft's `manifests.yaml` | `cluster:manifest <object> <file>:<n>` (or `<file>` alone when the line was not found), or `cluster:label <object> <label key>` for the team label | declares it, as for a folder |
| `image.digest[<container>]` | The running pods' image digests, sorted and joined | `cluster:runtime pods of <object>` | is never compared: a digest is never declared |
| `dependencies[<name>]` | A Service the pods were seen calling | `cluster:runtime calls from <object>` | declares a dependency of that name or `ref` |
| `dependencies[<name>].compatibility` | The versions the Pactos describing the called Service report | `cluster:fleet reported versions <v>[, <v>...]` | declares that dependency with a range |

- `<object>` is always `inputs.cluster.object`, the drafted workload, as
  `<namespace>/<Kind>/<name>`. A source never names another object, a value,
  an argument or a URL, and Pacto refuses a record whose source does.
- `asOf` is required on every cluster entry, in UTC to the second. Entries
  from `manifests.yaml` and image digests carry the read time. `runtime` and
  `fleet` entries carry the oldest node observation they rest on.
- `completeness` is `complete` or `partial`, on `runtime` and `fleet` entries
  only. It is `partial` when a node the workload ran on was not observed, when
  a source they rest on was cut short or failed, or when a called Service is
  hidden. An image digest is `partial` when the pods, ReplicaSets or Jobs list
  was cut short.
- `proposed` is a range, on a `dependencies[<name>].compatibility` entry with a
  `cluster:fleet` source only.
  The dependency keeps `compatibility: unknown` until a person declares one.
- `coverage` lists the sources `workloads`, `pods`, `replicasets`, `jobs`,
  `services`, `endpointslices`, `pactos` and `runtime-calls`, each `complete`,
  `partial` or `failed`. A source that is not `complete` has a fixed reason
  that names no namespace and no object: `stopped at the item bound (<n>)`,
  `forbidden`, `not found`, `timed out`, `the continue token expired`,
  `listing failed (<HTTP status>)`, `listing failed (no response)` when the
  API server never answered, `listing failed (malformed observation)`,
  `<k> of <m> nodes observed`, `no node runtime observations`,
  `<x> called services you may not read`, `the pods were not all read` or
  `no running pods observed` when no pod of the workload ran on a node.
- `hidden` counts the Services the workload called and the callers of its own
  Services that a read on behalf of a viewer drops. It never names them.
- Bundle verification accepts these keys only beside a
  [`declaration`](#declaration): a bundle whose record holds a cluster source,
  `inputs.cluster`, `coverage` or `hidden` without one is
  `INFERRED_RECORD_INVALID`.

### Declaration

[`pacto declare`](../publishing.md#declare-a-running-service) adds a
`declaration` block after `fields`, and so does
[`pacto claim`](../inferred-contracts.md#claim-an-external-system). It says who
declared the service, which workload or external system it came from and which
proposed ranges the signer accepted.

```yaml
declaration:
  state: declared
  asker: {subject: ana@acme.com, issuer: https://accounts.google.com, class: human}
  inferredFrom: k8s:billing/Deployment/invoices
  cluster: 3f1c0b6e-...
  baseline:
    version: 1.4.0
    bundle: ghcr.io/acme/invoices@sha256:...
    deploy: ghcr.io/acme/invoices-deploy@sha256:...
  accepted:
    - {field: dependencies[ledger].compatibility, range: ^2.3.0}
  draft: {tag: draft-3, digest: sha256:..., asker: {subject: "", issuer: none, class: agent}, onBehalfOf: ana}
  repository:
    url: https://github.com/acme/invoices
    rung: signed-provenance
    signer: https://github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main
    folder: deploy
    folderRung: gitops
```

| Key | Meaning |
|---|---|
| `state` | `declared`, signed through a person's Sigstore login, or `draft`, written by anyone else |
| `asker` | The identity that signed: `subject`, `issuer` and `class` (`human`, `agent` or `ci`). A declared record needs `human`, a draft never has it. An unsigned draft has exactly `{subject: "", issuer: none, class: agent}`, a draft signed with a key `{subject: "", issuer: key, class: agent}`, and a draft whose token names no readable identity has issuer `unknown` |
| `onBehalfOf` | On a draft only: the person an agent asserts it acts for, never verified |
| `inferredFrom` | What it came from: `k8s:<namespace>/<Kind>/<name>` for an inferred service, `external:<identity>` (such as `external:stripe`) for the external system a [claim](../inferred-contracts.md#claim-an-external-system) names, which must match `service.external` (`DECLARATION_EXTERNAL_MISMATCH`) or `offering:<producer>/<service>@sha256:<hex>` for the [inferred offering](../inferred-offerings.md#accept-an-inferred-offering) a person accepted, the producer's record by digest, which must name the offering `service.offering` names (`DECLARATION_OFFERING_MISMATCH`) |
| `cluster` | The uid of the cluster's `kube-system` namespace, left out when it could not be read |
| `baseline` | The published version it starts from, its bundle and its deploy artifact, each pinned by digest |
| `accepted` | On a declared record only: each proposed range the signer accepted, which `pacto.yaml` declares |
| `requested` | On a draft only: each proposed range the agent asks a person to accept |
| `draft` | On a declared record that accepted a draft: the draft's tag, digest, signer and `onBehalfOf` |
| `repository` | The service's source repository and how Pacto found it ([Open a pull request](../publishing.md#open-a-pull-request)); left out when no source names one |
| `repository.url` | `https://<host>/<path>`, with no credentials, query, fragment or `.git`, at most 512 bytes |
| `repository.rung` | `signed-provenance`, `gitops`, `image-label`, `repository-map` or `given` (`--repository`) |
| `repository.signer` | Exactly when `rung` is `signed-provenance`: the CI certificate subject that named the repository, at most 512 bytes |
| `repository.folder` | A slash path inside the repository, `.` for the root, with no `..` and no leading `/`, at most 256 bytes |
| `repository.folderRung` | `gitops`, `repository-map`, `given` (`--folder`) or `default` (the root, when no source names a folder) |

- Validation checks the block against `pacto.yaml`: each accepted or
  requested range is the declared `compatibility`
  (`DECLARATION_ACCEPTED_MISMATCH`), and each other proposed range stays
  `unknown` (`DECLARATION_RANGE_NOT_ACCEPTED`). A draft's `pacto.yaml`
  declares the ranges it requests; accepting it sets them back to `unknown`
  unless the person accepts them again.
- A reader shows the cluster facts of a declared record only when the bundle
  carries a signature it verified naming exactly `asker` and its installation
  signer policy allows that person. Otherwise the cluster facts, `coverage`
  and `hidden` are dropped with `DECLARATION_SIGNER_UNVERIFIED` or
  `DECLARATION_SIGNER_NOT_ALLOWED`, and the answer is `partial`.
- A draft is never a version: the fleet leaves it out with
  `DECLARATION_DRAFT_IGNORED`.
- `repository` is inferred: it names no Application, namespace or object,
  never changes a publisher verdict, a pin or a gate, and a viewer who may not
  read the workload's namespace does not see it.

### Surface

The inferred contract of an [external system](../inferred-contracts.md) holds
a `surface` block: the hosts, ports and client majors the
fleet sees of it. [The bundle](../inferred-contracts.md#the-bundle) shows an
example.

| Key | Meaning |
|---|---|
| `identity` | The external identity; it equals the contract's `service.external` (`INFERRED_RECORD_INVALID` otherwise) |
| `class` | The external class, required |
| `asOf`, `completeness` | When the surface was computed and whether the fleet snapshot was complete |
| `catalog` | The catalog entry that classified it: `origin`, `digest` and `entry`, all required when present |
| `hosts`, `ports`, `clients` | Each value once, with the `sources` that saw it, the catalog digest and `firstSeen` and `lastSeen`, both set or both left out |

- A client is `<name>/<major>`, such as `stripe-go/76` or
  `npm:@stripe/stripe-js/4`: up to 500 letters, digits and `@._~:/-`, then a
  major of up to 9 digits.
- A source is 1 to 253 characters with no whitespace or control character.
- A value listed twice is `INFERRED_RECORD_INVALID`.

## When Pacto writes it

- `pacto init` and the MCP tool `pacto_create` (with `pactify: true`) on a
  folder that holds a `pactoVersion: "2.5"` `pacto.yaml` say how many values
  it declares and how many Pacto completed. They write no file: the record is
  generated, so a committed copy would go stale on the next commit.
- An older `pactoVersion` is never completed, so its bundle keeps the same
  bytes. `init` says to set `pactoVersion: "2.5"`. `pacto ci` completes a
  contract it raised in memory and adds a `migrations` entry.
- A `--cluster` draft completes its contract and writes `pacto.inferred.yaml`
  into the draft folder, because its cluster facts cannot be read from the
  folder again. It keeps a record already there.
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
`publish.sign`, or the CI token's identity when it declares none. An unsigned tag, or one signed by someone else, is a conflict
that needs the next version.

The cited files are part of the bundle, so editing one changes the bundle and
needs a new version. When the declared files match but the record cites a
different set of files, for example after a newer Pacto reads more of the
folder, the plan asks for a patch version. A record whose digests do not match
its own bundle never counts as a record-only change. A contract with
`publish.artifacts` takes a patch on every new commit, because each artifact is
built from that commit.

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
  block on the service page, or in its **Where the values come from** fold
  when the revision carries a declaration, and in the **Inferred** block of a
  revision. A
  mark beside each value gives its source and confidence.
- A record Pacto cannot read or verify shows as "Pacto could not read the
  inferred record", never as an empty list, and the fleet answer is `partial`.
