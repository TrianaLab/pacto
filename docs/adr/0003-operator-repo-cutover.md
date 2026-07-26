# ADR 0003 — TrianaLab/pacto-operator repository cutover

Status: Accepted (checklist — DO NOT EXECUTE until the precondition below holds)
Date: 2026-07-26

Relates to: [ADR 0001](0001-integrations-monorepo.md) (why the operator source
moved into the monorepo) and [ADR 0002](0002-changesets-release.md) (the release
pipeline that must be proven before this runs).

## Purpose

The Kubernetes integration now lives in `TrianaLab/pacto` under
`integrations/kubernetes` (history-preserved, ADR 0001). This is the
administrative checklist for retiring the standalone `TrianaLab/pacto-operator`
repository **after** the monorepo release path is proven. It is a runbook, not
code — every step is an intentional, mostly one-way maintainer action.

## Precondition — HARD GATE

**Do NOT start any step below until a full staging release simulation of the
monorepo pipeline (ADR 0002) has passed**, i.e.:

- `release/scripts/verify-standalone.sh` is green (operator module builds
  `GOWORK=off`, no `replace`, against the NEXT published core).
- A staging `publish.mjs` run has published every k8s release unit (module,
  image, chart, docs) to a staging coordinate in the correct order, idempotently.
- The k8s integration image + chart from the monorepo pipeline have been pulled
  and smoke-tested on a real cluster (the kind acceptance leg).

If any of these is red, the old repo stays the source of truth. Cutover is
reversible only with effort — treat the gate as blocking.

## Checklist

Ordering matters: **stop new publication first, verify, then archive.** Archiving
is last because an archived repo is read-only and cannot be edited to fix a
mistake without un-archiving.

### 1. Freeze publication from the old repo (do first)

- [ ] Disable/delete the old release + image + chart publish workflows in
      `TrianaLab/pacto-operator/.github/workflows` (release, docker/ghcr publish,
      chart/Artifact Hub publish, docs deploy). Prefer deleting the trigger or
      setting `on: workflow_dispatch` only, so nothing fires on push/tag.
- [ ] Remove or expire any repo/org secrets and registry credentials the old
      workflows used to push images/charts (or scope the ghcr package + Artifact
      Hub repo so the old repo's token can no longer write). *(maintainer action —
      registry + org-secret permissions.)*
- [ ] Disable Renovate/Dependabot and any scheduled automation on the old repo.
- [ ] Verify no other automation (org-level workflows, external CI, bots) can
      still publish the operator image or chart from the old repo.

### 2. Verify the freeze

- [ ] Push a no-op commit / dispatch and confirm **no** image, chart, docs or
      release is produced from the old repo.
- [ ] Confirm the monorepo pipeline is the only publisher of the operator image,
      chart, k8s module and k8s docs (cross-check
      `release/artifact-pipeline-ledger.json` + `release/release-manifest.json`:
      one publisher per coordinate).

### 3. Close open work as superseded

- [ ] Close **PR #144** with a comment marking it superseded by the monorepo
      integration (`TrianaLab/pacto`, `integrations/kubernetes`), linking ADR 0001.
- [ ] Triage remaining open PRs/issues: migrate anything still relevant to
      `TrianaLab/pacto` (label `area/k8s-integration`), close the rest as
      superseded with a link to the monorepo.

### 4. Redirect contributors

- [ ] Update the old repo description to: "Moved to TrianaLab/pacto —
      integrations/kubernetes" and pin an issue / update the README top banner
      pointing to the monorepo integration path.
- [ ] Update the old repo's `CONTRIBUTING`/issue templates (or disable Issues on
      the old repo) so new issues are opened in `TrianaLab/pacto`. If Issues are
      left enabled temporarily, add an auto-reply/redirect.
- [ ] Redirect docs: point Artifact Hub repo metadata, README badges and any
      external links at the monorepo docs site. *(Pages + Artifact Hub are
      maintainer actions — repo settings + Artifact Hub ownership.)*

### 5. Retain history, then archive (do last)

- [ ] Confirm historical **releases, tags and issues remain intact** on the old
      repo (do not delete — they are the historical record; the subtree import
      already preserved commit history in the monorepo).
- [ ] Confirm published historical operator images/charts (`v4.7.0` and earlier)
      remain available — the monorepo continues the `v4.x` line, it does not
      republish or reset history.
- [ ] Archive `TrianaLab/pacto-operator` (repo Settings -> Archive). Archived =
      read-only; it stays browsable and its releases stay downloadable.
      *(maintainer action — repo admin.)*

## Maintainer-action note

Steps flagged *(maintainer action)* touch GitHub Pages, Artifact Hub ownership,
container-registry package permissions and org secrets. They cannot be scripted
from this repo and must be performed by a maintainer with the corresponding admin
rights. Everything else (workflow edits, PR/issue closes, description updates) is
routine repo maintenance.

## Rollback

Before archiving (step 5), cutover is reversible: re-enable the old workflows and
secrets. After archiving, un-archive first. Because publication is frozen in step
1 and archiving is last, a failed monorepo publish never leaves both repos able
to publish the same coordinate.
