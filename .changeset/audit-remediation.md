---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
breaking-change diff false-negatives — path-item-level parameters are now diffed,
the request body is deep-diffed so a newly required property is BREAKING, and
optional→required / added-required parameters are BREAKING — so a BREAKING-only
release gate can no longer be bypassed. Further security and dashboard fixes land
in the same PR.

As the first release since v3.1.0, this also ships the previously-merged but
unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.
