# GitHub Actions Integration
Automate contract validation, breaking-change detection, and publishing in your CI/CD pipeline using the official [Pacto CLI](https://github.com/marketplace/actions/pacto-cli) GitHub Action.

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

## Further reading

For the full list of inputs, outputs, and advanced configuration options, see the [pacto-actions](https://github.com/TrianaLab/pacto-actions) repository.
