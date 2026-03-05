---
title: GitHub Actions
layout: default
nav_order: 8
---

# GitHub Actions Integration
{: .no_toc }

Automate contract validation, breaking-change detection, and publishing in your CI/CD pipeline using the official [Pacto CLI](https://github.com/marketplace/actions/pacto-cli) GitHub Action.

---

## Overview

The [TrianaLab/pacto-actions](https://github.com/TrianaLab/pacto-actions) action provides three commands through a single reusable action:

| Command | Purpose |
|---------|---------|
| `setup` | Install the Pacto CLI binary |
| `diff` | Compare two contracts and detect breaking changes |
| `push` | Push contracts to an OCI registry |

After `setup`, the `pacto` binary is available for subsequent steps — so you can call any CLI command (like `pacto validate`) directly.

---

## Quick example

```yaml
- uses: TrianaLab/pacto-actions@v1
  with:
    command: setup

- run: pacto validate ./contracts/my-service

- uses: TrianaLab/pacto-actions@v1
  with:
    command: diff
    old: oci://ghcr.io/my-org/my-service:latest
    new: ./contracts/my-service

- uses: TrianaLab/pacto-actions@v1
  if: github.event_name == 'push' && github.ref == 'refs/heads/main'
  env:
    GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  with:
    command: push
    ref: ghcr.io/my-org/my-service
    path: ./contracts/my-service
```

{: .note }
For `ghcr.io` registries, the default `GITHUB_TOKEN` already has the `write:packages` scope needed by the `push` command.

---

## Further reading

- [Pacto CLI on GitHub Marketplace](https://github.com/marketplace/actions/pacto-cli)
- [pacto-actions README](https://github.com/TrianaLab/pacto-actions) — full list of inputs, outputs, and advanced configuration
