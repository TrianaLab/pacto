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

For usage examples, inputs, outputs, and advanced configuration see:

- [Pacto CLI on GitHub Marketplace](https://github.com/marketplace/actions/pacto-cli)
- [pacto-actions README](https://github.com/TrianaLab/pacto-actions)
