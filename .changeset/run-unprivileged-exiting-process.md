---
"@pacto/core": patch
---

`pacto run` in unprivileged mode no longer ends on an `Unknown` verdict when
the command exits right as a poll reads it. A poll read each network
namespace's tcp tables through one process only, and if that process was
exiting the whole view was marked truncated, so its listeners went missing and
the final round said `EVIDENCE_INSUFFICIENT` instead of `Compliant` or
`INTERFACE_ABSENT` (and `--fail-on` could exit 0 when it should have failed).
The tables are now read through another process still in that namespace, and
a namespace whose processes have all exited hides nothing.
