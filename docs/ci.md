# CI integration

Two steps in your pipeline prevent breaking changes from reaching production: validate the contract on every pull request, and diff it against what you published. `pacto diff` exits non-zero when the change is breaking. Branch protection is what actually stops the merge.

---

## What to commit

Put three things in your repository:

1. **`pacto.yaml`** at the repository root
2. **`pacto.lock`** next to it — the dependency lockfile
3. **The CI workflow** that runs `pacto validate` and `pacto diff`

`pacto lock .` writes the lockfile by resolving every dependency and recording the digest. Commit it so every build pins the same closure. Interface files (`interfaces/openapi.yaml`, configuration schemas) go in too.

---

## The two steps

### Step 1: Validate

```yaml
- name: Validate contract
  run: pacto validate .
```

This catches structural errors, cross-field violations and policy failures. Add `--readiness` to gate on readiness scores; without it, validation ignores the `readiness:` block.

### Step 2: Diff against the published version

```yaml
- name: Check for breaking changes
  run: pacto diff oci://ghcr.io/acme/my-service-pacto .
```

`pacto diff` exits 1 when the classification is `BREAKING`. The first argument is the baseline, the second is the updated contract. For a local diff with no registry, compare two directories: `pacto diff ./main ./feature-branch`.

---

## Exit codes and what blocks a merge

`pacto diff` exits 1 on a breaking change. The CI step fails, marking the pull request as failing checks. Branch protection is what prevents the merge. Pacto does not do that: the exit code is the signal and your repository's protection rules are the gate.

A `BREAKING` classification means the new version is incompatible with consumers pinned to the old major. Release it as a new major (`1.4.2` → `2.0.0`) so consumers can choose when to move.

---

## Handling exceptions

Sometimes a breaking change is unavoidable. Branch protection lets you merge a failing check with override permission. Document the reason (in the PR description, a comment or an ADR), then merge. Do not change `pactoVersion` or remove fields to dodge the gate.

---

## Publishing the contract

After the PR merges, publish to your OCI registry:

```yaml
- name: Push contract
  run: pacto push oci://ghcr.io/acme/my-service-pacto -p .
  env:
    PACTO_REGISTRY_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

`pacto push` tags the bundle with `service.version`. If that tag exists, the command skips the push unless you pass `--force`.

---

## GitHub Actions

The [GitHub Actions integration](github-actions.md) wraps these steps in a reusable action. Use `command: validate` and `command: diff` instead of writing `run:` steps, and the action handles installation, caching and PR comments for you.

For other CI systems (GitLab CI, Jenkins, CircleCI), install the CLI with the [installer script](installation.md#via-installer-script) and call the commands directly as shown above.
