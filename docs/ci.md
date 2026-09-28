# CI integration

Two steps in your pipeline prevent breaking changes from reaching production: validate the contract on every pull request, and diff it against what you published. `pacto diff` exits non-zero when the change is breaking. Branch protection is what actually stops the merge.

---

## What to commit

Put these three things in your repository:

1. **`pacto.yaml`** at the repository root (or wherever the contract lives)
2. **`pacto.lock`** next to it — the dependency lockfile, analogous to `go.sum` or `package-lock.json`
3. **The CI workflow** that runs `pacto validate` and `pacto diff`

The contract and lockfile belong in the same repository as the service. `pacto lock .` writes the lockfile by resolving every dependency and recording the digest. Commit it so every build pins the same closure.

Interface files (`interfaces/openapi.yaml`, configuration schemas) go in too. Keeping the OpenAPI spec alongside the code that implements it means one pull request changes both and your diff catches a mismatch before it ships.

---

## The two steps

### Step 1: Validate

Run `pacto validate .` on every pull request. This catches structural errors, cross-field violations and policy failures before the change merges. A contract that fails validation never reaches the registry.

```yaml
- name: Validate contract
  run: pacto validate .
```

The step exits non-zero on any validation error. Add `--readiness` if you gate on readiness scores; without it, validation ignores the `readiness:` block entirely.

### Step 2: Diff against the published version

Compare the pull request's contract against the version you last published. `pacto diff` exits 1 when the classification is `BREAKING`, so the step fails and the merge is blocked until someone either fixes the contract or approves an exception.

```yaml
- name: Check for breaking changes
  run: pacto diff oci://ghcr.io/acme/my-service-pacto .
```

The first argument is the baseline (what you published), the second is the updated contract (the pull request's working tree). The command answers: can a consumer pinned to the published version accept this change without breaking?

For a local diff with no registry involved, compare two directories: `pacto diff ./main ./feature-branch`. Clone the repository twice, check out different branches and point the command at both.

---

## Exit codes and what blocks a merge

`pacto diff` exits 1 on a breaking change. The CI step fails, which marks the pull request as failing checks. Branch protection — the repository setting that requires passing checks before merge — is what prevents the merge. Pacto does not enforce this itself; the exit code is the signal, and your repository's protection rules are the gate.

A `BREAKING` classification means the new version is incompatible with consumers pinned to the old major version. Release it as a new major (`1.4.2` → `2.0.0`) so consumers can choose when to move. Nothing enforces that you bump the version — Pacto reports the classification, the rest is up to you.

---

## Handling exceptions

Sometimes a breaking change is unavoidable. Branch protection lets you merge a failing check if you have the permission to override it, or if the repository allows approval from a designated reviewer. Document the reason (in the pull request description, a comment or an ADR), then merge.

Do not change `pactoVersion` or remove fields from `pacto.yaml` to dodge the gate. `pacto diff` is designed to catch real incompatibilities; circumventing it hides the break from your consumers and from the operational graph.

---

## Publishing the contract

After the pull request merges, publish the new version to your OCI registry. Run `pacto push` in a post-merge workflow or on release:

```yaml
- name: Push contract
  run: pacto push oci://ghcr.io/acme/my-service-pacto -p .
  env:
    PACTO_REGISTRY_PASSWORD: ${{ secrets.GITHUB_TOKEN }}
```

`pacto push` tags the bundle with `service.version` from `pacto.yaml`. If that tag already exists in the registry, the command skips the push unless you pass `--force`.

`PACTO_REGISTRY_PASSWORD` is the environment variable the CLI reads for registry credentials. Prefix your registry hostname with `PACTO_REGISTRY_` and uppercase it: `PACTO_REGISTRY_GHCR_IO_PASSWORD` for `ghcr.io`, `PACTO_REGISTRY_DOCKER_IO_PASSWORD` for Docker Hub.

---

## GitHub Actions

The [GitHub Actions integration](github-actions.md) wraps these steps in a reusable action. Use `command: validate` and `command: diff` instead of writing `run:` steps, and the action handles installation, caching and PR comments for you.

For other CI systems (GitLab CI, Jenkins, CircleCI), install the CLI with the [installer script](installation.md#via-installer-script) and call the commands directly as shown above.
