---
"@pacto/k8s-module": minor
---

Add health checks, checks after release and claim checks to the delivery
promoter, and report the node agent's coverage in every Pacto evaluation.

`Pacto.status.evaluatedRevision.kernelCoverage` says how far the node agent's
view of the workload's pods could be trusted in that evaluation: `Complete`,
`Partial`, `Failed` or `Stale`, empty when kernel evidence is off.

A Stage lists its own verify checks in `spec.gates.verify.checks`, each naming
a provider the delivery chart configures (`gates.providers`, type `mira` or
`http`) or the built-in `pacto.runtime`. The promoter verifies with the Pacto
contract check plus those checks over the time since Flux applied the version,
and runs them again every `spec.gates.verify.recheck` (default 1m) once the
version shipped. A confirmed Fail after release marks the version failed and
raises its one bound rollback, which cites the failing verdict, or freezes the
Stage when that cause already had its rollback or a person or an agent chose
the running version. Unknown after release never rolls back, and a result about
a version the Stage stopped running while the checks ran is dropped.
`status.lastCheck` holds the newest re-check.

A verdict a person or an agent cites as the cause of an operation is run again
at admission against the Stage's own gate, over a window inside the time the
running version has run; a re-run that comes back Unknown or with another
result is refused with `CauseUnconfirmed`, as is any cause while the running
version has no applied time.
