---
"@pacto/core": minor
---

Add health checks to Pacto delivery, checks that keep running after release
and checks of what a version claims against what it does.

`pkg/delivery` gains the gate protocol: a gate takes the operation uid, the
Stage, the service, the facet, the digests under check and an absolute time
window, and answers Pass, Fail or Unknown with a citation that can be run
again. A Pass that cites nothing is Unknown. Two providers ship with it: `Mira`
reads Mira's query API and judges the error ratio, and optionally the share of
slow spans, of the service's traces since the version was applied. Mira's API
refuses count-only queries, so the gate pages to the end for an exact count; a
failed or partial read, or fewer spans than `minSpans`, is Unknown. `Webhook`
is the plug-in format for any other tool. `RuntimeVerdict` is the
`pacto.runtime` claim gate: a version the node agent saw calling a service its
contract never declares, or not listening on a port it declares, fails;
without complete kernel coverage it is Unknown. `Rerun` runs a cited verdict
again against the Stage's own gate, keeping only the cited window, which must
fall in the time the version under check has run.

`pacto delivery explain` shows a live operation with every verdict, its
citation and how to run it again. `pacto delivery history` lists a Stage's
operations from the cluster and says the answer is partial until signed
records exist. `plan` and `propose` take `--cause-verdict`. The MCP server adds
`pacto_delivery_explain`, `pacto_delivery_history` and a `cause_verdict`
argument.
