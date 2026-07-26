# Release-safety redesign

Grounds the fix for the release-critical findings on PR #248. The current
release state machine infers "publish" from a file changing and then runs every
publisher unconditionally against whatever versions the manifest happens to hold.
That lets a normal feature merge overwrite existing production artifact bytes.

This document is the contract the implementation follows.

## Root causes (verified against the committed head a84d39b)

- `release.yml` `detect`: `release=true` when `git diff HEAD^ HEAD` touches
  `release/release-manifest.json`, or on any `workflow_dispatch`. The manifest is
  in this PR's diff and still reads `core 2.7.0 / k8s 4.7.0`, so **merging the
  feature PR fires every publisher against 2.7.0/4.7.0**.
- Publishers are unconditional and non-selective: `dashboard-image`,
  `operator-image`, `operator-chart`, `demo-bundles` `docker build-push` /
  `helm push` a versioned tag with **no existence or digest check** — they
  overwrite existing bytes.
- No component selection: one `release` boolean fans out to all publishers.
- `workflow_dispatch` republishes whatever is in the manifest.
- `core` creates the GitHub Release (`--generate-notes`) **before** `cli` builds
  and attaches binaries: a finalized release with missing assets.
- CLI `ldflags` bake `date -u` wall-clock => retries are not byte-identical.
- No checksums, SBOM or provenance.
- `operator-image` builds with `context: integrations/kubernetes` but the
  Dockerfile is root-context (COPY `go.work`, root `go.mod`, `pkg/`) => the prod
  build cannot succeed.
- `publish.mjs` orchestrates synthetic JSON to a local registry; `release.yml`
  has separate ad hoc real publishers => staging proves nothing about prod.
- `pacto.yml` publishes the dashboard contract bundle on **both**
  `release:published` and `workflow_run(Release)`, resolving the tag via
  `gh release view` (latest) — duplicate + wrong-tag risk.
- `docs.yml` deploys on push/release/workflow_run and derives versions from
  `gh release view` (latest) — outside any transaction.
- `Chart.yaml` `sources` still lists `TrianaLab/pacto-operator` as an active
  source.
- `ci-e2e-kind` only runs `setup-test-e2e`; the real `tests/e2e/kind/run.sh` is
  never executed and is not a required check.

## The release transaction

Releasing is driven by **consumed changesets**, never by a file diff.

`release/release-transaction.json` (schema `pacto-release-transaction/v1`) is
emitted **only** by `release:version` (`changeset version` +
`build-release-plan.mjs`). It is the single source of release intent:

```
{
  "schema": "pacto-release-transaction/v1",
  "ready": true,                     // false/absent => publish nothing
  "transactionId": "<sha256(changedUnits+newVersions+previousVersions)[:16]>",
  "sourceSha": "",                   // filled at release time = GITHUB_SHA
  "manifestSha": "<sha256(release-manifest.json)>",
  "changedGroups": ["core"],         // groups with >=1 bumped unit
  "changedUnits": ["core","cli","dashboard-image","dashboard-contract-bundle","demo-bundles"],
  "previousVersions": {"core":"2.7.0","k8s-module":"4.7.0", ...},
  "newVersions":      {"core":"2.8.0","k8s-module":"4.7.0", ...},
  "expectedTags":     {"core":"v2.8.0", "k8s-module":"integrations/kubernetes/v4.7.0", ...},
  "expectedCoordinates": { ...per unit... },
  "dependencyOrder": ["core:go-module","core:cli", ... ,"kubernetes:go-module", ...],
  "units": { "<unit>": {"status":"pending"} }   // pending|complete|failed, for resume
}
```

Determinism: `transactionId` and every field derive from the changeset-computed
versions and the previous manifest — no clock, no randomness (scripts cannot use
`Date.now()`). Running `release:version` twice on the same changesets is
byte-identical.

`changedUnits` is computed by diffing `newVersions` against `previousVersions`
(the pre-bump manifest). **If no changeset bumped a unit, `changedUnits` is empty
and `ready` is false.**

### State machine

```
feature PR (adds .changeset/*.md) merged to main
  -> changesets job: `changeset version` opens/updates the Version Packages PR
  -> NO ready transaction on main  => detect: release=false => publish nothing

Version Packages PR (consumes .changeset/*.md, applies bumps + ready txn) merged
  -> detect: ready txn present with non-empty changedUnits => release=true
  -> publish ONLY changedUnits, in dependencyOrder
```

`detect` computes `release` as: a `release-transaction.json` with `ready:true`
and non-empty `changedUnits` exists at HEAD **and** did not exist (or was not
ready) at HEAD^ — i.e. this commit is the one that landed the version bump. It
never keys off the manifest file alone. Fail-closed: any parse/shape error =>
release=false.

### Component selection

Each publisher job runs `if: unit in changedUnits`. An unchanged group's jobs do
not run — nothing is rebuilt, retagged, uploaded or redeployed. Coordinated
core+k8s: core units first, external core verification, then k8s units.

### workflow_dispatch = recovery only

Requires inputs `transactionId` + `sourceSha`. Rejects: missing transaction;
`manifestSha` mismatch; `sourceSha` mismatch; a unit not in the original
`changedUnits`; adding new units; a version already occupied by different bytes.
Operates only on units whose recorded status is `pending`/`failed`. It is not a
second release trigger.

## Unified publisher

One node library `release/orchestrator/` drives both staging and production.

- `plan.mjs` — load + validate the transaction; order units.
- `adapters/*.mjs` — one per artifact kind, each implementing
  `verify(coord, expected) -> {state: absent|identical|conflict, digest}` and
  `publish(ctx) -> {digest}`:
  - `go-tag` (git tag -> SHA), `binaries` (build + sha256 + attach),
    `oci-image` (build/push by digest), `oci-bundle` (pacto push),
    `helm-chart` (package/push + artifacthub), `docs` (mike deploy),
    `github-release` (create last, after assets verified).
- `orchestrator.mjs` — staged pipeline: build all selected -> checksums/SBOM/
  provenance -> verify -> push go tags in order -> OCI by digest -> chart ->
  docs+bundles -> attach assets -> finalize GitHub Release -> mark complete.
  A ledger records per-unit `{status, digest}` for resume.

Environment selects credentials + coordinates only:

- staging: disposable local `registry:2` coordinates, ephemeral git clone,
  simulated GitHub Release payload, no prod creds, no cosign keyless.
- production: real coordinates + `GITHUB_TOKEN`, invoked by `release.yml`.

The implementation is identical; only config differs.

### Immutability (fail-closed)

Before treating anything as published, `verify` compares the existing artifact to
the expected content:

- git tag points at the expected source SHA;
- GitHub Release targets the expected tag/SHA and every existing asset checksum
  matches;
- image / chart / bundle manifest digest matches;
- artifacthub metadata content matches.

`state:identical` => skip. `state:absent` => publish. `state:conflict`
(exists, different bytes) => **fail the transaction**; never overwrite, never
push a versioned tag unconditionally, never invent a new version.

Reproducible CLI: build date = `git show -s --format=%cI <sourceSha>`, not
wall-clock, so a resumed build reproduces byte-identical binaries.

## Docs + dashboard contract as transaction units

- `k8s-docs` and the docs site deploy become a selected unit driven by the
  transaction (exact core + k8s versions + sourceSha + transactionId). No
  `gh release view` latest. A k8s-only release updates integration docs without
  claiming a new core version. Stable site model: **core-versioned snapshot
  carrying the compatible integration docs** (mike alias `latest` tracks the
  released core; integration docs within it are the versions in the transaction).
- `dashboard-contract-bundle` publication is a single transaction unit with one
  uniquely identified invocation, exact version + sourceSha from the plan.
  `pacto.yml` keeps only the PR-time build/validate/diff; it no longer publishes.

## CI

- Root-cause + fix the missing CI check-runs on the PR head.
- `ci-e2e-kind` runs `tests/e2e/kind/run.sh` with the exact image + packaged
  chart from the staging simulation; upgrade from the latest public compatible
  chart (or a faithful fixture) to the new one; asserts controller start, CRDs,
  RBAC reads, dashboard path, Compliant->Unknown->NonCompliant->recovery,
  coverage reaches CR status, uninstall cleanup.
- `ci-e2e-kind` + `release-dry-run` are required checks, path-filtered to
  `release/**`, `.github/workflows/**`, `integrations/kubernetes/**`, `go.work`,
  `go.mod`, Dockerfiles, charts, CRDs, RBAC, demo artifacts.
- Pin tools + third-party actions to immutable revisions; checksum any
  `curl | tar`; pin `trianalab/pacto-actions` in release paths.

## Tests

- `release/orchestrator/*.test.mjs` (node --test): transaction generation
  determinism; feature-merge => empty changedUnits => zero publishers;
  component-selection matrix (core-only, k8s-only, both, chart/docs-only,
  no-release, prerelease, manual recovery); immutability (identical skip,
  conflict fail-closed); resume (skip complete, resume pending, reject differ,
  no new version); dispatch rejections.
- `tests/release/one_publisher_test.go`: extend to detect duplicate triggers +
  duplicate execution paths, not just duplicate `pacto-publishes:` markers.
- Operator prod Docker context test.
- Stale-link gate distinguishing historical from active links.
