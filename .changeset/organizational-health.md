---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A failing health check is reported as `HEALTH_FAILING` (category
`Inconclusive`, severity `unknown`), and compliance stays `Unknown`: a failing
endpoint does not contradict the contract. The operator reports it when the
declared health endpoint answers with a 5xx other than 501, or when the
readiness probe on the declared health path keeps every pod out of Ready;
`pacto run` and `pacto agent` report it on a 5xx other than 501. A 501 or a
405 stays `EVIDENCE_INSUFFICIENT`, and `COLLECTION_FAILED` now means only that
a collector query errored. The Evidence Server re-evaluates the same evidence
and reaches the same finding. The `CapabilityObserved` evidence payload gains
the optional `healthy` key, which only a present capability may carry. Strict
decoders reject unknown keys, so upgrade the Evidence Server with or before the
operator.
