---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

Pacto keeps what it infers in pacto.inferred.yaml, a separate bundle file with
each value's source, for pactoVersion 2.5 contracts. pacto init and
pacto_create complete an existing pacto.yaml without changing it, pacto explain
lists each inferred field and the YAML to declare it, a publish that changes
only the inferred record is not republished, the diff classifies inferred-only
changes as non-breaking and impact never clears a gate on an inferred
consumer. The dashboard shows inferred values apart from declared ones.
