---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

An automatic Stage that is not Ready no longer raises a promote: it would
only be refused, and each refusal named the next promote anew, so the
promoter raised one rejected promote and signed record after another. The
reason the promoter waits now says the Stage is not Ready and why. The Flux
pointer Pacto creates under `profile: idp` sets `spec.insecure` when the
promoter reaches its registry over plain HTTP.
