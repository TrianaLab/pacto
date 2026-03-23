---
title: GitHub Actions
layout: default
nav_order: 8
---

# GitHub Actions Integration
{: .no_toc }

Automate contract validation, breaking-change detection, and publishing in your CI/CD pipeline using the official [Pacto CLI](https://github.com/marketplace/actions/pacto-cli) GitHub Action.

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

## Quick start

Add the Pacto CLI action to any workflow step. The action installs the `pacto` binary and makes it available for subsequent steps.

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

      - name: Validate contract
        run: pacto validate .
```

## Common workflows

### Validate on pull request

Catch schema violations and cross-field errors before they reach main:

```yaml
      - name: Validate contract
        run: pacto validate .
```

### Detect breaking changes

Compare the PR contract against the published version to block breaking changes:

```yaml
      - name: Check for breaking changes
        run: |
          pacto diff oci://ghcr.io/acme/my-service-pacto . --output-format json > diff.json
          if jq -e '.classification == "BREAKING"' diff.json > /dev/null 2>&1; then
            echo "::error::Breaking contract change detected"
            exit 1
          fi
```

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

      - name: Log in to GHCR
        run: pacto login ghcr.io --username "${{ github.actor }}" --password "${{ secrets.GITHUB_TOKEN }}"

      - name: Push contract
        run: pacto push oci://ghcr.io/${{ github.repository }}-pacto -p .
```

### Environment-specific validation

Validate the contract with environment-specific overrides:

```yaml
      - name: Validate production config
        run: pacto validate . --values values/production.yaml
```

## Further reading

For the full list of inputs, outputs, and advanced configuration options, see the [pacto-actions](https://github.com/TrianaLab/pacto-actions) repository.
