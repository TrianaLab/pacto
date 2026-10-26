# Delivery reference: health checks

Beyond the Pacto contract check, a Stage can check a version's errors and
response times in your monitoring, an AI agent's evals and that the service
calls only what its contract declares (its claims). Every check answers Pass,
Fail or Unknown and cites what it read, so anyone can run it again. Unknown
never passes.

## Add checks to a Stage

List the checks under `spec.gates.verify.checks`. Each names a provider:

```yaml
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata:
  name: payments
  namespace: prod
spec:
  service: {name: payments}
  environment: prod
  applyRef: {name: payments}
  candidates:
    upstreamStage: {name: payments, namespace: test}
  auto: true
  gates:
    verify:
      window: 5m
      recheck: 1m
      checks:
        - name: health
          provider: mira
          params: {minSpans: "50", maxErrorRatio: "0.02", soak: "3m"}
        - name: claims
          provider: pacto.runtime
```

The checks run on the time since Flux applied the version, all at once, and
one that takes longer than a minute is Unknown. The promoter works on
`delivery.promoter.workers` (4 by default) operations and Stages at a time, so a slow
provider does not hold up the others. Any Fail rolls it
back, all Pass verifies it and anything else waits; when `window` runs out a
person is asked to accept the unknown or reject the version. While that
question waits, the checks run again every `recheck`: a Fail still rolls the
version back and all Pass verifies it without the person.

## Providers

`pacto.runtime` is built in. Every other provider is configured in the
operator chart's delivery section, so a Stage or an agent can name a provider
but never point the promoter at a host:

```yaml
delivery:
  gates:
    providers:
      - {name: mira, type: mira, url: "http://mira.observability:4318"}
```

## Mira

A `mira` provider counts the service's spans in [Mira](https://github.com/trianalab/mira)
by `service.name`, one span kind at a time: server spans by default, so one
span per request served. Its params:

| Param | Default | Meaning |
|---|---|---|
| `spanKind` | `server` | The spans counted: `server`, `consumer`, `producer`, `client`, `internal`, `unspecified` or `any` |
| `minSpans` | 20 | Fewer spans than this is Unknown |
| `maxErrorRatio` | 0.05 | A larger share of failed spans fails |
| `soak` | 1m | A clean result passes only after this long |
| `latencyThreshold` | off | Spans slower than this count as slow |
| `maxLatencyRatio` | 0.05 | A larger share of slow spans fails |
| `where` | none | Extra Mira terms as JSON, for example the namespace |
| `matchVersion` | `false` | Count only spans whose `service.version` is the contract's `service.version` |

By default the provider counts every span of the service in the window, so
while a rollout is still replacing pods the old version's spans count too:
they can hide a breach or fail a clean version. The window starts when Flux or
Argo CD reports the version applied, not when the rollout finished. Set
`matchVersion: "true"` to judge only the version under check, once the service
sets the `service.version` resource attribute to its contract's
`service.version` (for example through `OTEL_RESOURCE_ATTRIBUTES`). Without a
contract version to match, the check is Unknown.

A read that fails or stops short is Unknown. Mira's query API has no count, so
the provider pages through the spans and stops at 200,000 per query. A window
holding more is judged on its newest `soak` only, and the message says so; if
even that holds more, the check is Unknown, so narrow it with `where`. The
citation holds the exact queries with absolute time bounds, so posting them to
Mira again reads the same spans.

## HTTP and eval providers

An `http` provider is the plug-in format for any other tool. The promoter
posts `{"input": ..., "params": ...}`, where the input names the operation,
the Stage, the service, the digests under check and the time window. The tool
answers `{"result": "Pass|Fail|Unknown", "message": "...", "citation": {...}}`.

An `eval` provider is an eval runner, the check an AI agent ships behind (see
[agents](../../contract-reference/agent.md)). The promoter posts the
same `{"input": ..., "params": ...}`, the input also naming the contract the
version ships and the suite in it:
`"contract": "oci://...@sha256:...", "evals": {"path": "evals/suite.yaml", "digest": "sha256:..."}`.
The runner runs that suite on the version under check and answers how many
cases passed, with `suite` the sha256 of the suite it ran:
`{"subject": "sha256:...", "suite": "sha256:...", "run": "...", "passed": 48, "total": 50}`.
The Stage decides, not the runner:

| Param | Default | Meaning |
|---|---|---|
| `minPassRate` | 1 | The share of cases that must pass, above 0 and up to 1. Fewer fails |

An answer about another version or another suite, with no cases or nothing to
cite is Unknown, and so is a version whose contract cannot be read or ships no
suite (`agent.evals`). The verdict cites the contract, the suite's path and
digest and the run.

## Claims against the running service

`pacto.runtime` uses the [node agent](runtime-conformance.md). It fails a
version the kernel saw calling a Pacto-managed service its contract never
declares, or not listening on a port it declares. It reads the Pacto
evaluation of exactly this version and passes only when
`status.evaluatedRevision.kernelCoverage` is `Complete` and the version has run
for `soak` (a Go duration, default 1m); otherwise the answer is Unknown. A Pass
means only that the node agent saw neither of those two things: right after a
rollout the version may not have handled traffic yet, which is what the soak
is for. A contradiction the kernel saw fails at once.

## After release

After release the checks run again every `recheck` (default 1m) on the last
`window`. A confirmed Fail marks the version failed and raises one rollback
citing the check. The Stage freezes instead when that cause already had its
rollback, when there is nothing to roll back to, or when a person or an agent
chose the running version (a rollback, or a promote during auto-hold). It also
freezes, naming the operation, when an operation Pacto did not raise holds the
name of the rollback or freeze it raises (`<operation>-rollback`,
`<operation>-freeze`).
Unknown after release never rolls back, and a result about a version the Stage
stopped running is dropped. The newest result is in `status.lastCheck`, which
also says when the operation that shipped the running version is gone. The
Stage's `Watched` condition is `True` while that result passed. It turns
`False` with reason `RecheckUnknown` while nothing watches the running version
(a provider down, too many spans, no coverage) and `RecheckFailed` after a
Fail. The promoter emits an Event when it turns `False`, and the
`pacto_delivery_stage_watched` gauge drops to 0, so you can alert on it.

## Explain

```bash
pacto delivery explain -n prod <operation>
```

`explain` shows where an operation is and every verdict with its citation and
how to run it again. `--cause-verdict` on `propose` cites a
verdict as its cause, for example on a frozen Stage; the promoter runs it again
and refuses a cause that comes back with another result.
