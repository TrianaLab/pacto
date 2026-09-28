# GitHub Actions Integration

Validate contracts, detect breaking changes and publish bundles from CI with the
official [Pacto CLI](https://github.com/marketplace/actions/pacto-cli) GitHub
Action.

---

## Quick start

The action is command-driven: `command: setup` installs the `pacto` binary for
later `run:` steps, while `command: validate`, `diff`, `push` or `doc` run those
operations natively.

```yaml
name: Contract CI

on:
  pull_request:
    paths:
      - 'pacto.yaml'
      - 'interfaces/**'
      - 'configuration/**'
      - 'policy/**'

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install Pacto CLI
        uses: TrianaLab/pacto-actions@v1
        with:
          command: setup

      - name: Validate contract
        run: pacto validate .
```

## Common workflows

### Detect breaking changes

`pacto diff` takes the old contract first and the new one second, and exits
non-zero on a `BREAKING` result (see
[change classification rules](contract-reference/diff.md#change-classification-rules)):

```yaml
      - name: Check for breaking changes
        uses: TrianaLab/pacto-actions@v1
        with:
          command: diff
          old: oci://ghcr.io/acme/my-service-pacto
          new: .
          comment-on-pr: 'true'
```

`fail-on-breaking` defaults to `true`, so a breaking change fails the step, and
`comment-on-pr` posts the diff. That comment is written as the
workflow's `GITHUB_TOKEN`, which is read-only by default, so grant the job
`pull-requests: write` or the comment fails with a `403` while the diff itself
passes.

### Publish on release

Push the contract bundle to an OCI registry when a release is created:

```yaml
name: Publish Contract

on:
  release:
    types: [published]

jobs:
  push:
    runs-on: ubuntu-latest
    permissions:
      packages: write
    steps:
      - uses: actions/checkout@v4

      - name: Install Pacto CLI
        uses: TrianaLab/pacto-actions@v1
        with:
          command: setup

      - name: Push contract
        uses: TrianaLab/pacto-actions@v1
        with:
          command: push
          ref: oci://ghcr.io/${{ github.repository }}-pacto
          path: .
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
```

## Action reference

Every input is documented in the
[pacto-actions](https://github.com/TrianaLab/pacto-actions) repository, which
owns the action and its version history.

### Outputs

Give the step an `id` to read its outputs.

| Output | Set by | Value |
|---|---|---|
| `version` | `setup` | The exact Pacto version installed |
| `has-breaking-changes` | `diff` | `"true"` or `"false"` |
| `diff-output` | `diff` | The full diff output, in `output-format` |
| `doc-output` | `doc` | The generated markdown documentation |

`has-breaking-changes` is set from the CLI exit code. `pacto diff` exits non-zero
on a `BREAKING` classification, so a `POTENTIAL_BREAKING` result reads `"false"`.
A diff that fails to run — an unresolvable reference, an unparseable contract —
also exits non-zero and so also reads `"true"`; check the step log before treating
it as a contract verdict. The output is written whether or not
`fail-on-breaking` stops the step, so you can gate on it yourself:

```yaml
      - name: Check for breaking changes
        id: contract-diff
        uses: TrianaLab/pacto-actions@v1
        with:
          command: diff
          old: oci://ghcr.io/acme/my-service-pacto
          new: .
          fail-on-breaking: 'false'

      - name: Require an approved exception
        if: steps.contract-diff.outputs.has-breaking-changes == 'true'
        run: exit 1
```
