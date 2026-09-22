---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Take the week's dependency bumps in one go: `modelcontextprotocol/go-sdk`
1.7.0 → 1.8.0 in the engine, and `controller-runtime` 0.25.0 → 0.25.1,
`ginkgo` 2.32.2 → 2.33.0 and `gomega` 1.43.0 → 1.43.1 in the operator module.
No source changes — all four are drop-in.

Dependabot also learns to group: Kubernetes libraries move as one train
because client-go, apimachinery and controller-runtime pass each other's
types, Charm moves as one because bubbletea, bubbles and lipgloss share the v2
renderer, everything else minor-or-patch lands in a single weekly PR. Five
open PRs for one afternoon of bumps is how a bot teaches you to stop reading
it.
