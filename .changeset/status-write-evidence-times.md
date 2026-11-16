---
"@pacto/k8s-module": patch
---

The operator no longer writes a Pacto's status on every reconcile that lands in a
new second. Each finding's evidence times (`observedAt`, `detectedAt`) are stamped
when the evidence is read, so comparing them made almost every reconcile look like a
change. They now refresh with the `lastReconciledAt` heartbeat, at least once every
half check interval, and a status that is otherwise unchanged is not rewritten.
