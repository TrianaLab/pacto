# Put it in your repository

Add Pacto to your build pipeline (CI) so a pull request that would break the
services using yours fails its checks, and every merge publishes a version.
This page assumes you have a contract, `pacto.yaml`, from
[Pactify a service](pactify.md). The examples use the names from that page:
the contract is published to `oci://ghcr.io/acme/contracts/my-service`, the
`publish.bundle` field of `pacto.yaml`.

## What to commit

Commit what `pacto init` wrote (`pacto.yaml`, `.pactoignore`, `.gitignore`,
`delivery/` and any files the contract points to, such as an OpenAPI
document).

If your contract lists dependencies, also run `pacto lock .` and commit the
`pacto.lock` it writes. It records the exact published build of every
contract yours depends on, so every build checks against the same ones. It is
optional: without it, Pacto looks dependencies up fresh each time. See
[Lockfile](lockfile.md).

## Declare your API first

`pacto diff` sees API changes only in the interfaces the contract declares.
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

## On every pull request

The snippets below are steps of a GitHub Actions job, after
`actions/checkout`. Install Pacto first; the token avoids GitHub's limit on
anonymous API calls, which shared CI runners hit:

```yaml
- name: Install Pacto
  run: curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
  env:
    GH_TOKEN: ${{ github.token }}
```

Add two checks. The second reads your registry, so give the job a login. These
variables work for any registry; on GitHub's, the built-in token will do:

```yaml
env:
  PACTO_REGISTRY_USERNAME: ${{ github.actor }}
  PACTO_REGISTRY_PASSWORD: ${{ secrets.GITHUB_TOKEN }}
```

```yaml
- name: Validate the contract
  run: pacto validate .

- name: Check for breaking changes
  run: pacto diff oci://ghcr.io/acme/contracts/my-service .
```

`pacto validate` catches missing or malformed fields, fields that contradict
each other and failed policies, the rules your platform team may set for
every contract.

`pacto diff` compares the contract in the pull request (the second argument)
with the last one you published (the first). With no version after the name,
it picks the highest published version. It exits 1 only when the change is
`BREAKING` (`POTENTIAL_BREAKING` and `NON_BREAKING` pass), a
[breaking change](glossary.md#breaking-change), so the check fails. Your branch protection rules decide whether a failed check
blocks the merge.

Until the first version is published there is nothing to compare with and
`pacto diff` fails, so add this step after your first `pacto publish`. To
compare two folders with no registry, run `pacto diff ./main ./feature-branch`.

## Plan every service with pacto ci

When one repository holds several services, `pacto ci` checks them all in one
step. It finds every `pacto.yaml` under the repository root, skips
[ignored and too-deep folders](pactoignore.md#at-the-repository-root) and
compares each service with its last
published version in your registry. It writes nothing to the registry, so it
needs only read credentials (`PACTO_REGISTRY_USERNAME` and
`PACTO_REGISTRY_PASSWORD`):

```yaml
- name: Plan every service
  run: pacto ci
```

The plan prints each value it detected with its source:

- `CI`: GitHub Actions or GitLab CI, from `GITHUB_ACTIONS` or `GITLAB_CI`
- `Registry`: `--registry`, else `ghcr.io/<owner>` on GitHub Actions or
  `CI_REGISTRY_IMAGE` on GitLab CI
- `Base`: the target branch or merge-request base, shown for context only;
  Pacto reads no Git history and works on a shallow checkout
- `Consumer source`: each source of consumers you named and what it answered,
  or `not read` when no changed service has a published baseline

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

`pacto ci` exits like `pacto impact`: `0` clear, `1` blocked and `2`
unverified. A folder Pacto cannot read makes the run unverified. It reads the
[target snapshot](#where-consumers-come-from) as `pacto impact` does; a changed
service with none reads `unverified (no consumer source)` until you name its
consumers with the flags `pacto impact` takes, such as `--root`.

`pacto ci --explain payments` shows why one row reads as it does: its
baseline, what changed, each inferred field with its source and the consumers.
Its exit code is that row's. `--output-format json` prints the plan as one JSON
document, with the row's exit under `--explain`.

`pacto ci` reads the organization catalog from `--external-catalog` or
`PACTO_EXTERNAL_CATALOG` only, never from a `pacto` config file in the working
directory: in CI that directory is the repository a pull request can change.

On GitHub Actions the plan is also added to the job summary. On GitLab CI it
is written to `pacto-plan.md`; keep it with
`artifacts: paths: [pacto-plan.md]`.

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

## On every pull request: who breaks

`pacto diff` says whether a change is breaking. `pacto impact` says which
consumers it breaks and whether they run, from the signed target snapshot each
cluster's delivery recorder publishes to your registry. The job needs the
login variables, the delivery trust config and `jq`, never cluster
credentials:

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
partial, since a snapshot Pacto cannot trust lists no consumers.

`pacto impact` exits `0` clear, `1` blocked and `2` unverified, as does a
`BREAKING` change with compatible consumers. `|| [ $? -eq 2 ]` lets the step
reach the `jq` checks; a `1` still fails it.

### Where consumers come from

Pacto tries `ghcr.io/acme/pacto-targets`, then `ghcr.io/pacto-targets`, and
reads every `cluster-<uid>` snapshot in the first that exists (JSON fields
`registry` and `targetSnapshots`):

```text
Registry: ghcr.io/acme (found ghcr.io/acme/pacto-targets, 1 cluster read)
Targets: ghcr.io/acme/pacto-targets@sha256:4f1c... (cluster 6f1e2a90, asOf 3m, complete, signed by recorder-6f1e2a90 (publisher system:serviceaccount:pacto-system:pacto-operator-delivery-recorder))
```

Snapshots are pinned by digest; only a verified one clears a `BREAKING`
change:

- `unverified`: the job has no trust config. The answer is partial and a
  `BREAKING` change with consumers exits `2`.
- `not scoped to this cluster`: the key that signed has no `streams` entry for
  the cluster's UID, so another cluster's recorder could hold it. Partial.
- `stale`: the snapshot is older than 15 minutes, because its recorder stopped
  publishing or its tag was moved back. Partial.
- A bad signature, a cluster other than its tag, a registry error or a timeout:
  the snapshot is not used and the answer is partial.
- No `pacto-targets` repository or no cluster tag: the Registry line says so
  (`not found`, `denied (check registry read credentials)` or `no cluster
  tag`) and consumers come only from the sources you name, as before.

The trust config holds one key per cluster, scoped to its `kube-system` UID
(a bare PEM key is unscoped). Each recorder's key is in
[Target snapshot](integrations/kubernetes/delivery-records.md#target-snapshot):

```yaml
keys:
  - name: recorder-6f1e2a90
    streams: ["6f1e2a90-5b7d-4c1e-8a3f-2d6b0e9c7a41/*/*"]
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

A consumer deployed nowhere is in no snapshot, so name each one's contract with
`--root oci://ghcr.io/acme/contracts/checkout`. A
`BREAKING` change that reaches only these exits `2`. `--k8s` reads the
current cluster, `--evidence-url` an Evidence Server and `--target-state` a
fixture file.

## After a merge: publish

To publish every changed service of the repository in one step, with the
version and the build derived and keyless signing, use
[`pacto ci --publish`](publish-from-ci.md). For one contract with a declared
`publish` section and the same login variables:

```yaml
- name: Set up a Docker builder
  run: docker buildx create --use --driver docker-container

- name: Publish a version
  run: |
    echo "$COSIGN_KEY" > cosign.key      # publish.sign reads it from the repository root
    pacto publish
  env:
    COSIGN_KEY: ${{ secrets.COSIGN_KEY }}
    COSIGN_PASSWORD: ${{ secrets.COSIGN_PASSWORD }}
```

`pacto publish` builds the image, pushes it to your registry with your
Kubernetes files and the contract, and signs them; delivery only rolls out
versions published this way. Store the contents of `cosign.key` and its
password as CI secrets. On GitHub, the job also needs
`permissions: packages: write` to push.

It never overwrites a published version, and a merge that does not raise
`service.version` in `pacto.yaml`, such as a docs change, fails here. Raise
the version with each change you want to ship, or run this step only when
`pacto.yaml` changed. See [Publish a version](publishing.md).

If you use contracts without delivery, `pacto push oci://ghcr.io/acme/contracts/my-service -p .`
uploads only the contract.

## A breaking change you need to ship

Sometimes a breaking change is the right call. Give it a new major version
(`1.4.2` becomes `2.0.0`): `pacto publish` refuses a breaking change with a
smaller version. With delivery, the change then waits for the teams whose
services it would break. Do not remove fields to get past the check. Raising
`pactoVersion` never hides a breaking change: `pacto ci` publishes an older
contract as the current version only when that keeps its meaning.

`pacto diff` still fails with the new major version, because the change is
still breaking, so that pull request's check stays red. Merge it the way your
repository lets a reviewer override a failed check, such as an admin bypass.
Or make the diff step informational (`continue-on-error: true` on GitHub) and
let `pacto publish` be the gate, since it refuses a breaking change without a
new major version.

## GitHub Actions and other CI systems

The [GitHub Actions integration](github-actions.md) wraps these steps in a
ready-made action that also installs Pacto and comments on the pull request.
For GitLab CI, Jenkins or CircleCI, install Pacto with the
[installer script](installation.md#via-installer-script) and run the same
commands.
