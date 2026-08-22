# GitHub Actions Integration
Automate contract validation, breaking-change detection and publishing in your CI/CD pipeline using the official [Pacto CLI](https://github.com/marketplace/actions/pacto-cli) GitHub Action.

---

## Quick start

The action is command-driven: `command: setup` installs the `pacto` binary for later `run:` steps, while `command: validate`, `diff`, `push` or `doc` run those operations natively.

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

The [quick start](#quick-start) already runs `pacto validate .` on every pull request to catch schema and cross-field errors before merge. The workflows below add breaking-change detection and publishing.

### Detect breaking changes

Compare the PR contract against the published version and block breaking changes. `pacto diff` takes the old contract first and the new one second, and exits non-zero on a `BREAKING` result (see [change classification rules](contract-reference/diff.md#change-classification-rules)):

```yaml
      - name: Check for breaking changes
        uses: TrianaLab/pacto-actions@v1
        with:
          command: diff
          old: oci://ghcr.io/acme/my-service-pacto
          new: .
          comment-on-pr: 'true'
```

`fail-on-breaking` defaults to `true`, so the step blocks the merge on a breaking change and `comment-on-pr` posts the diff. To gate in a plain `run:` step instead, `pacto diff oci://ghcr.io/acme/my-service-pacto .` exits non-zero on the same result.

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

### Environment-specific validation

Validate the contract with environment-specific overrides:

```yaml
      - name: Validate production config
        run: pacto validate . --values values/production.yaml
```

## Action reference

The tables below cover `TrianaLab/pacto-actions@v1` (currently `v1.8.2`).

### Inputs

| Input | Applies to | Default | What it does |
|---|---|---|---|
| `command` | every command | *(required)* | `setup`, `validate`, `diff`, `push` or `doc` |
| `version` | `setup` | `latest` | Pacto version to install (e.g. `v0.2.1`) |
| `github-token` | every command | `${{ github.token }}` | Passed to the command as `GH_TOKEN` |
| `cache` | every command | `true` | Reuse the OCI bundle cache (`~/.cache/pacto/oci/`) across runs |
| `path` | `validate`, `push`, `doc` | `.` | Contract directory or `oci://` reference |
| `old` | `diff` | — | Baseline contract: directory path or `oci://` reference |
| `new` | `diff` | — | Updated contract: directory path or `oci://` reference |
| `output-format` | `diff` | `text` | `text`, `json` or `markdown` |
| `fail-on-breaking` | `diff` | `true` | Fail the step when `pacto diff` exits non-zero |
| `ref` | `push` | — | Target OCI reference (e.g. `oci://ghcr.io/org/name:tag`) |
| `registry` | `push` | — | Registry hostname to authenticate against (e.g. `ghcr.io`) |
| `username` | `push` | — | Registry username |
| `password` | `push` | — | Registry password or token |
| `values` | `validate`, `diff`, `doc`, `push` | — | Values file(s) merged into the contract — newline-separated, last wins |
| `set` | `validate`, `diff`, `doc`, `push` | — | Inline contract values, newline-separated (e.g. `service.version=2.0.0`) |
| `old-values` | `diff` | — | Values file(s) merged into the old contract only |
| `old-set` | `diff` | — | Inline values set on the old contract only |
| `new-values` | `diff` | — | Values file(s) merged into the new contract only |
| `new-set` | `diff` | — | Inline values set on the new contract only |
| `output-path` | `doc` | — | File path to save the generated markdown |
| `comment-on-pr` | `diff`, `doc` | `false` | Post the output as a pull-request comment |
| `add-to-summary` | `doc` | `true` | Add the documentation to the GitHub step summary |

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

## Further reading

For advanced configuration options, see the [pacto-actions](https://github.com/TrianaLab/pacto-actions) repository.
