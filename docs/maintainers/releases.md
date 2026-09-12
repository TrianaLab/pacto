---
# Contributor-internal. It is thorough, so it keeps outranking the reader
# pages on general queries; halve it rather than thin the page.
search:
  boost: 0.5
---

# Release architecture

How the Pacto monorepo releases its artifacts. This describes the system as it is —
the mechanisms, invariants and manual steps a maintainer needs. The implementation
lives in `release/` and `.github/workflows/release.yml`; code comments there link
back here for the broader picture.

## Release units and fixed groups

A **release unit** is one independently-published artifact. They move in two fixed
groups (a group releases together or not at all):

| Group | Units | Coordinate kind |
|-------|-------|-----------------|
| **core** | `core`, `cli`, `dashboard-image`, `dashboard-contract-bundle`, `demo-bundles`, `demo-compose` | Go module tag, GitHub Release binaries, OCI image, OCI contract bundle, OCI bundles, OCI demo artifact |
| **kubernetes** | `k8s-module`, `operator-image`, `operator-chart`, `k8s-docs` | Go module tag, OCI image, Helm/OCI chart, versioned docs |

`release/release-manifest.json` is the source of truth for the units, their
coordinates and versions; `release/release-plan.json` is the derived plan (regenerated
by `release/scripts/build-release-plan.mjs`, drift-gated in CI).

## Changesets: feature PR vs version PR

Versioning is **changeset-driven**, never inferred from a file diff or a PR title:

1. A feature PR includes a `.changeset/*.md` describing the bump per package.
2. Merging it to `main` runs the `changesets` job, which opens/updates the **Version
   PR** (`release:version` = `changeset version` + `build-release-plan.mjs --transaction`
   + `apply-release-plan.mjs`). This consumes the changesets, bumps versions, and
   regenerates the manifest, plan and the **release transaction**.
3. Merging the **Version PR** is what publishes. A plain feature merge publishes
   nothing.

## The release transaction + source-SHA binding

`release/release-transaction.json` (schema `pacto-release-transaction/v1`) records
`transactionId`, `sourceSha`, `manifestSha`, `changedGroups`, `changedUnits`,
`newVersions`, `expectedTags`/`expectedCoordinates` and `dependencyOrder`. It is
emitted **only** by `release:version`.

`detect.mjs` decides from it:

- **push to main** — release only when a *ready* transaction with a non-empty
  `changedUnits` is newly introduced by this commit (a feature merge carrying the
  previous release's stale transaction never re-fires).
- **workflow_dispatch** — recovery only: validates `transactionId` + `sourceSha`
  (+ optional narrowing subset, never adding units) and refuses anything else.

**Source-SHA binding.** Every publisher/tagger job checks out
`ref: needs.detect.outputs.source_sha` and stamps all commit-bearing metadata (Go
tag targets, image `revision` label + `GIT_COMMIT`, CLI build commit, GitHub Release
target) from that SHA — never `github.sha`. On push they are the same commit; on a
recovery run after `main` advanced they are not, so recovery always rebuilds the
exact transaction commit. `tests/release/source_sha_test.go` enforces this.

## Dependency ordering

The core Go module tag is created first (the kubernetes module's `go.mod` pins the
published core). A `core-ready` barrier always runs for a release: if core changed it
requires `core-tag` to have succeeded; if not it verifies the pinned core tag already
exists — so a **kubernetes-only** release still has a resolvable core dependency.

Go module tag conventions: root/core `vX.Y.Z`; the nested kubernetes module
`integrations/kubernetes/vX.Y.Z` (its own major line, currently v5).

## Durable per-unit ledger

`release/orchestrator/ledger.sh` records per-unit truth as **immutable OCI
artifacts**, so parallel publishers never lose updates and a resumed run reconstructs
state from a durable store:

- `<repo>:<txn>` — transaction metadata (`transactionId`, `sourceSha`, `manifestSha`).
- `<repo>:<txn>-<unit>` — a unit's result (`coordinate`, `version`, `digest`, `status`).
- `<repo>:<txn>-<unit>.plan` — a unit's precomputed expected digest, written only
  when the caller supplies `PACTO_EXPECT_DIGEST`. No caller does, so the tag is
  unused today.

Every tag is write-once: a second write must be byte-identical (idempotent resume)
else it fails closed. `ledger-init` validates existing metadata (`ledger.sh verify`)
before resuming. Recovery completion is derived from this ledger, never from the
committed transaction file's static `units[*].status`.

## Publish semantics: absent / identical / adopt / conflict

`verify-oci.sh` + the shared adapter `publish-oci-unit.sh` are the single publish
path (both production `release.yml` and the staging dry-run call them). For each unit:

- **absent** — publish, read the digest, record `complete`.
- **identical** — the remote already equals the recorded/precomputed digest; record
  (idempotent) and skip.
- **adopt** — the remote exists with no recorded digest (a push-before-record crash)
  but its identity proves it is *this* transaction's artifact: a content-addressed
  digest match (bundles, chart), matching OCI `revision`+`version` provenance
  labels (images), or the complete native Compose identity (`demo-compose`, whose
  publisher is `docker compose publish` — it stamps a moving `created` timestamp
  into the manifest and writes no provenance, so `PACTO_EXPECT_CONTENT` — the
  `sha256` of the projected compose file — is its identity). Record the remote
  digest and skip re-pushing.

  "Complete" is the operative word for that last one: an `artifactType` of exactly
  `application/vnd.docker.compose.project`, exactly one layer, that layer's media
  type exactly `application/vnd.docker.compose.file+yaml`, and its digest exactly
  `PACTO_EXPECT_CONTENT`. Bytes are not a type — any artifact can carry the same
  compose file as one octet-stream layer under any artifact type, and adopting it
  on the strength of a layer count and a digest would record something
  `docker compose -f oci://…` cannot run. `verify-oci.sh` is the one place that
  rule lives; `publish-oci-unit.sh` asserts through it after its own push, before
  the ledger records the unit complete, rather than keeping a second copy.
- **conflict** — anything else: fail closed, never overwrite an immutable tag.

**Crash recovery** rests on `adopt`: publish, verify the remote matches the unit's
expected identity, record `complete`. A crash in the push→record window is recovered
by proving the remote artifact is this transaction's.

## Staging / production parity

The staging dry-run (`make release-dry-run`, `release/orchestrator/dry-run.sh`) runs
the real artifacts against a disposable local registry through the **same** adapters
production uses — only coordinates, credentials, signing mode and transport differ.
It proves transaction selection (core-only / k8s-only / coordinated / recovery),
per-line partial-failure + resume, concurrency, crash-window recovery, digest
idempotency and fail-closed conflict.

## Chart vs Artifact Hub

The immutable operator chart and the mutable `artifacthub.io` alias are separate
results. The chart digest is recorded immediately after push; the Artifact Hub
metadata is a separate step (runs on absent or identical, round-trip verified), so an
Artifact Hub failure never leaves a published-but-unrecorded chart or wedges a resume.

## Docs versioning

`mike` publishes to `gh-pages`. Only a **release** deploys the docs: it publishes the
exact released core version and moves the `latest` alias + default (`make docs-deploy`).
A **non-release** main-push does NOT deploy — `docs.yml` runs a strict build only, to
validate the docs. So merging a breaking PR before its Version PR releases can never
mislabel the stable docs or move `latest`. There is no published `next` snapshot in the
version selector; preview unreleased docs locally with `make docs` / `mkdocs serve`.

## Demo bundle immutability

Demo bundles are published per service:version with the same absent/identical/conflict
rule. A changed bundle needs a new contract version; replacing content under an
existing tag is refused unless the deliberate one-time migration is explicitly
authorized.

Because that refusal happens *during* the release — after other units have already
published irreversibly — the same gate runs read-only at PR time as CI's
`demo-bundle-immutability` leg (`publish-demo-bundles.sh --check`). It compares each
bundle's locally computed OCI digest against the published one and never pushes.
Exit 75 means the registry could not be read: the check compared nothing, so CI warns
rather than blocking, and that is not evidence there is no conflict.

## Every job installs the CLIs its scripts reach

A job's shell runs scripts, and those scripts run `oras`, `crane`, `cosign`, `syft`
and `helm`, none of which the runner image provides. Nothing in the YAML declares
that dependency — the install step and the script that needs the tool sit in
different files. `tests/release/workflow_tooling_test.go` walks each job's shell
through its `make` targets and the transitive closure of the scripts it invokes, and
fails when a job can reach a gated CLI it never installed. It is one-directional: an
unused install is not an error, an uninstalled use is.

This matters more than a missing binary usually would, because the release scripts
answer questions with strings. `ledger.sh` returning `""` means "nothing recorded" —
which is also what it returned when `oras` was absent. It now refuses to run without
its tools, and distinguishes a genuine 404 from a registry it could not read at all.
Callers must assign its output to a variable before testing it: inside `[ "$(…)" ]`
the exit status is discarded, so a failed read reads as an empty one.

## A major bump needs the module path renamed first

Go binds a module's version to its import path: a path ending `/vN` carries only
`vN.y.z`, and an unsuffixed path only v0 and v1. Both Go modules here publish on a
suffixed path — `github.com/trianalab/pacto/v3` and
`github.com/trianalab/pacto/integrations/kubernetes/v5` — so the major in the path
and the major in the version are the same number, always.

A major changeset moves the version. It does not move the path, and it cannot: the
rename touches the import path, every importer in the tree, `go.work`'s replace, the
`coordinate` in `release/units/*/package.json` and the operator's own `module` line.
That is a deliberate first commit of a major release, not something the version
script should infer. Landing the changeset without it used to produce a require go
refuses to parse:

```
go.mod:79:2: require github.com/trianalab/pacto/v3: version "v4.0.0" invalid: should be v3, not v4
```

`build-release-plan.mjs` now refuses to emit a plan whose version does not fit its
path, and `apply-release-plan.mjs` refuses to write the require, so the release stops
before the Version PR exists rather than mid-transaction. **Rename the path, merge
that, then land the major changeset.** `tests/release/k8s_module_path_test.go` holds
the Kubernetes half of the rename to every declared coordinate.

## Abandoned transaction `522e9507410f16fc` (3.2.0 / 5.2.0)

This transaction published four units and then failed. v3.2.0 and v5.2.0 are
**tagged and resolvable through the Go module proxy but were never released**:
neither has a GitHub Release, and `releases/latest` — which the CLI's update
check and `scripts/get-pacto.sh` both read — correctly skips them. Two rules
follow, and they are permanent:

- **Never create a back-dated GitHub Release for either version.** GitHub picks
  `latest` by creation time, so it would advertise them as newer than whatever
  has shipped since.
- **Never expect the transaction to resume.** Every publisher checks out
  `ref: source_sha`, and that commit cannot publish. It was superseded by
  3.2.1 / 5.2.1. Its ledger entries are permanent, so a recovery dispatch for
  that transaction id stays armed and still refuses to double-publish the four
  units that completed.

## Abandoned transaction `3bf445d36fd2c3fc` (5.2.2)

This one published **nothing** — no module tag, no operator image, no chart — and
reported success. Run 32647472671 on `be20e3b3` (#319) is green in the Actions list.
`detect` resolved correctly (`release=true`, the four Kubernetes units), `core-ready`
succeeded, and then `ledger-init` and every publisher below it skipped.

The cause was skip propagation through `needs`. A Kubernetes-only transaction has no
core unit, so `core-tag` skips by design. `core-ready` carried `always()` and ran, but
`always()` exempts only the job that declares it: the skip still reached everything
downstream of `core-tag`, and `ledger-init` — which did not declare it — skipped with
its `if:` satisfied and both of its `needs` green. A skipped job is not a failed job,
so the run's conclusion stayed `success`. Fixed in #361 and #362.

5.2.2 is therefore unlike 3.2.0 / 5.2.0: there is no tag, so nothing resolves through
the module proxy either, and no artifact exists to be adopted or conflicted with. It
was superseded by **5.2.3**, which published in full. Nothing needs recovering, and a
recovery dispatch for this transaction id would rebuild from a commit whose versions
have since been republished — do not send one.
