# Put it in your repository

Add Pacto to your build pipeline (CI) as one step. On every pull request it
plans each service of the repository and fails the check when a change breaks
a consumer it sees running. With the reusable workflow's and the GitLab
component's defaults it cannot see where consumers run, so a breaking change
with consumers reads `unverified` and passes with a warning; `fail-on:
unverified` or the delivery trust config turns it red
([Who breaks](#on-every-pull-request-who-breaks)). On every merge to the
default branch it publishes each changed service. This page assumes you have
a contract, `pacto.yaml`, from [Pactify a service](pactify.md). An empty
contract plans, and it publishes once it declares `service.name`, which still
fits in two lines.

## What to commit

Commit what `pacto init` wrote, any files the contract points to, such as an
OpenAPI document, and the CI file `pacto ci init` writes. No signing key goes
in the repository: keyless signing needs none, and a key lives in a CI secret.

If your contract lists dependencies, also run `pacto lock .` and commit the
`pacto.lock` it writes. It records the exact published build of every
contract yours depends on, so every build checks against the same ones. It is
optional: without it, Pacto looks dependencies up fresh each time. See
[Lockfile](lockfile.md).

## Declare your API first

`pacto ci` sees API changes only in the interfaces the contract declares.
If `pacto init` did not find your OpenAPI document, add it to `pacto.yaml`
yourself, with the path from the repository root:

```yaml
interfaces:
  - name: rest-api
    type: openapi
    ref: api/openapi.yaml
    visibility: public          # other teams may call it
```

Other interface types and fields are in [Contract sections](contract-reference/sections.md#interfaces).

## Add Pacto to your pipeline

From the root of your repository:

```bash
pacto ci init
```

It reads which CI the repository uses, writes the caller into the working
tree and prints it. Review the file and commit it yourself: Pacto never
commits, pushes or needs write access to your Git host. `--dry-run` prints
the file and writes nothing; `--platform github` or `--platform gitlab`
picks one when the repository has both or neither.

### GitHub Actions

`pacto ci init` writes `.github/workflows/pacto.yml`:

```yaml
--8<-- "examples/ci/github/pacto.yml"
```

It calls the pacto-actions reusable workflow, pinned by commit, which
Dependabot can keep current. The `permissions` line is the most any of its jobs
may use. Inputs, secrets and the jobs are in
[GitHub Actions](github-actions.md).

### GitLab CI/CD

`pacto ci init` adds one entry to the `include` list of `.gitlab-ci.yml` and
leaves every other line as it was:

```yaml
--8<-- "examples/ci/gitlab/.gitlab-ci.yml"
```

The component adds a `pacto-plan` job to the `test` stage and a
`pacto-publish` job to the `deploy` stage. Run `pacto ci init` again to move
the pin to a newer commit.

### Other CI systems

Buildkite, CircleCI, Azure Pipelines and Jenkins run the same command with
Pacto installed ([installer script](installation.md#via-installer-script)):

```sh
--8<-- "examples/ci/any-ci.sh"
```

[From an imperative pipeline to one step](ci-from-pipeline.md) maps each step
of an existing pipeline to `pacto ci`.

## What the plan shows

`pacto ci` finds each `pacto.yaml` under the root, skips
[ignored and too-deep folders](pactoignore.md#at-the-repository-root) and
compares each with its last published version. The plan writes nothing to the
registry, so it needs only read credentials: the workflow's token on GitHub,
else `PACTO_REGISTRY_USERNAME` and `PACTO_REGISTRY_PASSWORD`.

The plan prints each value it detected with its source:

- `CI`: GitHub Actions or GitLab CI, from `GITHUB_ACTIONS` or `GITLAB_CI`
- `Registry`: `--registry`, else `ghcr.io/<owner>` on GitHub Actions or
  `CI_REGISTRY_IMAGE` on GitLab CI
- `Base`: the target branch or merge-request base, shown for context only;
  Pacto reads no Git history and works on a shallow checkout
- `Consumer source`: each source of consumers you named and what it answered,
  or `not read` when no changed service has a published baseline
- `Index`: the [organization index](#consumers-from-the-organization-index)
  read and how complete the read was; the index alone is no consumer source
- `Ranges`: how many dependency edges of the services it walked declare a
  range or carry an [accepted](#accept-proposed-ranges) one, then each
  `unknown` range with the range Pacto proposes

Then one row per service:

| Column | Shows |
| --- | --- |
| `SERVICE` | the service name |
| `CHANGED` | whether its content differs from the published version |
| `VERSION` | published version `->` the version it would publish |
| `CLASSIFICATION` | `NON_BREAKING`, `POTENTIAL_BREAKING` or `BREAKING` |
| `IMPACT` | `clear`, `blocked (...)` or `unverified (...)` |
| `INFERRED` | how many fields Pacto read from the folder |

--8<-- "examples/demo/generated/ci-plan.md"

A folder Pacto cannot read makes the run unverified. It reads the
[target snapshot](#where-consumers-come-from) as `pacto impact` does; a changed
service with none reads `unverified (no consumer source)` until you name its
consumers with the flags `pacto impact` takes, such as `--root`.

`pacto ci --explain payments` shows why one row reads as it does: its
baseline, what changed, each inferred field with its source and the consumers.
It exits with that row's code. `--output-format json` prints the plan as JSON.

`pacto ci` reads the organization catalog from `--external-catalog` or
`PACTO_EXTERNAL_CATALOG` only, never from a `pacto` config file in the working
directory, which a pull request can change.

- On GitHub Actions the plan is also added to the job summary, and each
  service that does not read as unchanged gets an annotation on its
  `pacto.yaml`.
- On GitLab CI it is written to `pacto-plan.md`, which the component keeps
  as the `pacto plan` merge request artifact. Without the component, keep it
  with `artifacts: paths: [pacto-plan.md]`.

### Older contracts

`pacto ci` raises an older `pactoVersion` in the bundle only, when that
adds no validation finding. Its row says:

- `ledger: pacto.yaml is 2.4, publishes as 2.5 (migrated in memory)`
- `kept at 2.4 (<finding>)`: published as written when it declares a `publish`
  section; with none, the row fails until you fix the finding and raise `pactoVersion`
- `published as 2.4 (raises to 2.5 when it next changes)`: a raise alone never
  republishes

No note changes the exit code. The bundle records the raise in
`pacto.inferred.yaml` ([Migrations](contract-reference/inferred-record.md#migrations)),
shown as inferred in the dashboard. `pacto migrate` rewrites the file itself.

### Accept proposed ranges

`pacto ci` proposes, in `pacto.inferred.yaml` and with a source:

- A [dependency by name](contract-reference/dependencies-and-state.md#depend-on-a-service-by-name)
  gets its `ref` from the organization index entry of the service of that
  name in your domain (`index:<repo>:<tag>`).
- A dependency whose range is absent or `unknown` gets `^MAJOR.MINOR.0` of
  the provider version this service runs with in the target snapshot
  (`snapshot:<repo>@sha256:<hex>`).
- A proposal publishes as `compatibility: unknown`: it reads unverified and
  never clears the gate.
- A changed spec file that is only inferred may print
  `possible break (inferred)` with the YAML that declares the interface. The
  row stays unverified until you declare it; see
  [a possible break on an inferred interface](impact-consumers.md#a-possible-break-on-an-inferred-interface).

The service owner accepts them:

```bash
pacto accept oci://ghcr.io/acme/reports --proposed --dry-run    # the plan and its intent digest
pacto accept oci://ghcr.io/acme/reports --proposed --intent sha256:...
```

- `--proposed` accepts every proposed range of the newest published version.
  A proposed `ref` is accepted only by naming it,
  `--field 'dependencies[ledger].ref'`, and
  `--field 'dependencies[ledger].compatibility=^2.0.0'` accepts another range.
- Only your interactive Sigstore login (a browser, or `--device`) signs. The
  record is attached to that version in its own bundle repository with your
  registry credentials: a referrer, or on a registry without the Referrers
  API, such as GHCR, a write-once `pacto-accept-<hex>-<hex>` tag.
- Only a record signed by the service's declared owner (its DRI or an email
  contact) counts. One by anybody else reads `waiting for the owner of
  reports` with who may accept. A key, an ambient OIDC token in CI or an
  agent through `pacto_accept` writes a `pacto-accept-draft-<n>` that a
  person accepts with `--draft`.
- A counted range reads `accepted` in the `Ranges:` line. The next
  `pacto ci --publish` pins it into the published `pacto.yaml`, where it reads
  declared, and cites the record in `inputs.acceptances`. A range your
  `pacto.yaml` declares wins; `compatibility: unknown` declares none. A
  declared `service.version` that is already published pins nothing until you
  bump it.
- One command accepts ranges or a break, never both.
- The dashboard's service page offers **Accept all proposed ranges** inside
  **Inferred by Pacto** to a signed-in or local viewer: it shows the plan and
  the `pacto accept` command, and, for a person who may update the service's
  workload, signs with a fresh login or saves an acceptance draft, as
  [Declare this service](dashboard-service-page.md#declare-an-inferred-service)
  does.

- `--policy` on `pacto ci` and `pacto impact` sets how a `BREAKING` change
  treats what no range covers. The default `warn` adds a `POLICY_WARNING`
  note. `unknown-range=fail` blocks while a direct consumer's range is unknown
  and not accepted (`POLICY_UNKNOWN_RANGE`). `uncontracted=fail:7d` blocks
  while a caller no contract describes was seen in the last 7 days
  (`POLICY_UNCONTRACTED`).

## Who breaks { #on-every-pull-request-who-breaks }

`pacto ci` checks each changed service against its consumers and whether they
run. It finds them through the organization index that every publish keeps at
`ghcr.io/acme/pacto-index`, so it needs no `--root`, and checks them against
the signed target snapshot each cluster's operator publishes. A snapshot
counts only when the job has the delivery trust config (`PACTO_DELIVERY_TRUST`),
never cluster credentials. The reusable workflow and the GitLab component pass
none, so there a `BREAKING` change with consumers reads `unverified`. To clear
it, run `pacto ci` or, for one contract, `pacto impact` in a job you write:

```yaml
- name: Check who the change breaks
  run: |
    pacto impact oci://ghcr.io/acme/contracts/my-service . \
      --output-format json > impact.json || [ $? -eq 2 ]
    jq -r '.consumers[] | select(.compatibilityVerdict == "incompatible") | "\(.service) (owner \(.owner // "unknown")) accepts \(.compatibility)"' impact.json
    jq -e '.classification != "BREAKING" or all(.consumers[]; .compatibilityVerdict != "incompatible")' impact.json
    jq -e '.classification != "BREAKING" or .completeness == "complete"' impact.json
  env:
    PACTO_DELIVERY_TRUST: pacto-trust.yaml   # committed: public keys only
```

The first `jq` lists each consumer whose `compatibility` range rejects the new
version, with its owner; the second fails the step when a `BREAKING` change
has an `incompatible` consumer. A range Pacto cannot evaluate reads
`unknown` and passes. The third fails a `BREAKING` change whose answer is
partial, such as an untrusted snapshot or an index with no target snapshot.

`pacto impact` exits `0` clear, `1` blocked and `2` unverified, as does a
`BREAKING` change with compatible consumers. `|| [ $? -eq 2 ]` lets the step
reach the `jq` checks; a `1` still fails it.

### Consumers from the organization index

Every published version writes one entry, and impact reads each service's
newest ([how](impact-consumers.md#consumers-from-the-organization-index)):

```text
Consumers: 41 from ghcr.io/acme/pacto-index (listed by tag, complete)
```

It says `complete` only when a target snapshot was read; with `--k8s` or
`--target-state` alone a row reads `unverified` (`CONSUMER_INDEX_UNCHECKED`).
`--root` replaces the index. With `--publish`, `--index` also names where the
run writes its entries and `--index none` turns them off. A run that finds a
version already published writes its missing entry; an entry it cannot write
is a note on its row.

An entry exposes each published service's name, version, bundle reference and
dependency references. On GitHub the first publish
creates the `pacto-index` package: in its **Manage Actions access** settings,
grant **write** to each repository that publishes and **read** to each that
runs impact. Internal visibility shows those names and the dependency graph to
every organization member, even for packages they cannot read. A publish
without access succeeds with a warning.

### Where consumers come from

Pacto tries `pacto-targets` under each repository prefix, deepest first,
and reads every `cluster-<uid>` snapshot in the first that exists (JSON
fields `registry` and `targetSnapshots`):

```text
Registry: ghcr.io/acme (found ghcr.io/acme/pacto-targets, 1 cluster read)
Targets: ghcr.io/acme/pacto-targets@sha256:4f1c... (cluster 6f1e2a90, asOf 3m, complete, signed by installation-6f1e2a90 (publisher system:serviceaccount:pacto-system:pacto-operator))
```

Snapshots are pinned by digest; only a verified one clears a `BREAKING`
change:

- `unverified`: the job has no trust config. The answer is partial and a
  `BREAKING` change with consumers exits `2`.
- `not scoped to this cluster`: the key that signed has no `streams` entry for
  the cluster's UID, so another cluster's operator could hold it. Partial.
- `stale`: older than 15 minutes, because its operator stopped publishing
  or its tag was moved back. Partial.
- A bad signature, a cluster other than its tag, a registry error or a timeout:
  the snapshot is unused and the answer is partial.
- No `pacto-targets` repository or no cluster tag: the Registry line says so
  (`not found`, `denied (check registry read credentials)` or `no cluster
  tag`) and consumers come only from the sources you name.

The trust config holds one key per cluster, scoped to its `kube-system` UID
(a bare PEM key is unscoped).
[Target snapshot](integrations/kubernetes/delivery-records.md#target-snapshot)
shows where each cluster advertises its key:

```yaml
keys:
  - name: installation-6f1e2a90
    streams: ["6f1e2a90-5b7d-4c1e-8a3f-2d6b0e9c7a41/pacto-targets/snapshot"]
    publicKey: |
      -----BEGIN PUBLIC KEY-----
      ...
      -----END PUBLIC KEY-----
```

- `--delivery-trust` or `$PACTO_DELIVERY_TRUST` names the file.
- `auto`, the default, reads only the clusters whose snapshot exists at that
  moment, so a cluster that stopped publishing drops its consumers. Pin the
  clusters you expect, once per cluster:
  `--target-snapshot oci://ghcr.io/acme/pacto-targets:cluster-<uid>`. A pinned
  tag that is missing makes the answer partial.
- `--target-snapshot` with a repository reads it when the derivation cannot
  find it, and `--target-snapshot off` reads none.
- Pull on `pacto-targets` reads every cluster's Pacto status and workload
  shape regardless of Kubernetes RBAC and, where the Evidence Server is on,
  every target, provisioning and usage record (cost included) it accepted.
  Give that repository its own read ACL.

### Consumers that run nowhere yet

A consumer deployed nowhere is in no snapshot. The index finds it once it is
published; name an unpublished one with `--root ./services/checkout`. A
`BREAKING` change that reaches only these exits `2`.

## After a merge: publish

On a push to the default branch the publish job runs `pacto ci --publish`. It
derives each changed service's version, builds its image with the tool the
folder shows and signs it; see [Publish from CI](publish-from-ci.md).

- Signing is keyless by default. Only the publish job has
  `id-token: write`. The CI mints a short-lived token for it, which the Pacto
  CLI in that job exchanges with Sigstore for a certificate; the token never
  leaves the job, and the Pacto installation never holds it.
- Keyless signing records the repository, workflow and commit in the public
  Rekor log. A private repository that must not appear there sets the
  `sign-key` secret, or declares `publish.sign` with a private log; see
  [Publish a version](publishing.md).
- One publish runs per repository at a time; a version lost to a concurrent
  run is planned again.
- The publisher identity is your repository on its default branch, also when
  the workflow that signs is the shared pacto-actions one, so a newer pin
  keeps earlier versions verifying.
- Publishing never turns delivery on: a cluster rolls out a published version
  only once you set up [delivery](integrations/kubernetes/delivery.md).
- Each published bundle carries a signed record of its image digests, so the
  operator [links the running workload](integrations/kubernetes/linked-workloads.md)
  to its contract however you deploy it, and `pacto impact` counts that
  workload as a consumer.

## A breaking change you need to ship { #ship-a-breaking-change }

Announce a deliberate break, with a new major version (`2.4.0` becomes
`3.0.0`), in [`breaks`](contract-reference/dependencies-and-state.md#breaks):

```yaml
breaks:
  - consumers: [reports]
    reason: Amounts move from cents to euros; read amount_eur.
```

- `pacto ci` shows each announced consumer `announced for reports, not
  accepted` and the gate holds the change.
- Each consumer owner accepts with
  `pacto accept oci://ghcr.io/acme/reports --break ledger@3.0.0`, signed as
  for [proposed ranges](#accept-proposed-ranges). Before ledger 3.0.0 is
  published, `--from ../ledger` (its checkout or a pushed `oci://` ref) reads
  the announcement.
- A consumer also reads `accepted` when its own published ranges admit the
  new version, a source shows where it runs and no revision that refuses it
  runs there.
- The gate clears (`BREAKING_ACCEPTED`) once every consumer accepted. Nobody
  overrides a failed check.
- An announcement without `expires` expires 30 days after the commit time,
  recorded in `pacto.inferred.yaml`. An expired break blocks again.
- A plain `pacto publish` carries no acceptance forward, so one stays found
  only while the accepted version is the consumer's newest or runs on a
  target. `pacto ci --publish` carries it.
- Gate on `pacto ci` or `pacto impact`: `pacto diff` still fails on the
  change, because it is still breaking.

## Exit codes

| Run | `0` | `1` | `2` |
| --- | --- | --- | --- |
| `pacto ci` (plan) | clear | blocked | unverified |
| `pacto ci --publish` | every changed service published | anything else | |

On GitHub, the reusable workflow's `fail-on` input decides what fails the
plan job: `blocked` (the default) passes on `2` with a warning annotation for
each unverified service; `unverified` fails on it. On GitLab, `pacto-plan`
passes with a warning on `2` (`allow_failure: exit_codes: [2]`). An unverified
plan never reads as a plain pass.
