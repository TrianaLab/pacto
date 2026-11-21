---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

`pacto ci init` writes the CI caller that runs `pacto ci` into a repository's
working tree: five lines in `.github/workflows/pacto.yml` that call the
pacto-actions v2 reusable workflow pinned by commit, or one `include` entry
in `.gitlab-ci.yml` for its GitLab component. It never commits, pushes or
needs Git write access. The plan job runs `pacto ci` on pull requests,
annotates each changed service and writes the job summary; the publish job,
the only one with `id-token: write`, runs `pacto ci --publish` on the default
branch, one run per repository at a time. Pacto's own CI runs the caller over
a demo monorepo with empty contracts.

Keyless publishes through the shared reusable workflow keep verifying across
pin bumps and stay trusted by the operator's derived publisher rule: Pacto
matches the caller repository and branch that Fulcio certifies, never the
reusable workflow's commit. A repository moving from its own keyless release
workflow keeps its earlier versions verifying. The operator picks the rule up
through the core module.

The CI guide, a new guide "From an imperative pipeline to one step" and the
GitHub Actions reference teach `pacto ci` in place of `pacto push`,
`COSIGN_KEY` and hand-raised versions.

Until the pacto-actions release that pins a Pacto with `pacto ci`, a caller
written by `pacto ci init` adds `with: {pacto-version: <this release>}`.
