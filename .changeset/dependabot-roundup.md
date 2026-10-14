---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

This week's dependency bumps in one go: `bubbletea` 2.0.10 → 2.1.0 and
`x/sync` 0.23.0 → 0.24.0 in the engine, OpenTelemetry 1.46.0 → 1.47.0 (and the
Prometheus exporter 0.68.0 → 0.69.0) in the operator module. No source changes.

Go goes 1.26.6 → 1.26.9 and `x/net` 0.59.0 → 0.60.0 in both modules, because
govulncheck flagged reachable `net/http` and `x/net/http2` vulns
(GO-2026-6607 to GO-2026-6617) and wouldn't let any of the bot's PRs through.
Both shipped binaries were exposed, so it's worth the patch release.
