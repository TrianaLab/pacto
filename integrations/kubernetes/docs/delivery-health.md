# Delivery reference: health checks and claims

Beyond the Pacto contract check, a Stage can check a version's errors and
response times in your monitoring and that it calls only what its contract
declares. Every check answers Pass, Fail or Unknown and cites what it read, so
anyone can run it again. Unknown never passes.

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

The checks run on the time since Flux applied the version. Any Fail rolls it
back, all Pass verifies it and anything else waits; when `window` runs out a
person is asked to accept the unknown or reject the version.

## Providers

`pacto.runtime` is built in. Every other provider is configured in the
delivery chart, so a Stage or an agent can name a provider but never point the
promoter at a host:

```yaml
gates:
  providers:
    - {name: mira, type: mira, url: "http://mira.observability:4318"}
```

A `mira` provider counts the service's spans in [Mira](https://github.com/trianalab/mira)
by `service.name`. Its params:

| Param | Default | Meaning |
|---|---|---|
| `minSpans` | 20 | Fewer spans than this is Unknown |
| `maxErrorRatio` | 0.05 | A larger share of failed spans fails |
| `soak` | 1m | A clean result passes only after this long |
| `latencyThreshold` | off | Spans slower than this count as slow |
| `maxLatencyRatio` | 0.05 | A larger share of slow spans fails |
| `where` | none | Extra Mira terms as JSON, for example the namespace |

The check reads every page of Mira's rows; a read that fails or stops short is
Unknown. The citation holds the exact queries with absolute time bounds, so
posting them to Mira's `/api/v1/query` again reads the same spans.

An `http` provider is the plug-in format for any other tool. The promoter
posts `{"input": ..., "params": ...}`, where the input names the operation,
the Stage, the service, the digests under check and the time window. The tool
answers `{"result": "Pass|Fail|Unknown", "message": "...", "citation": {...}}`.

An `eval` provider is an eval runner, the check an AI agent ships behind (see
[agents](../../contract-reference/agent.md)). The promoter posts the
same `{"input": ..., "params": ...}`; the runner runs the agent's eval suite
on the version under check and answers how many cases passed:
`{"subject": "sha256:...", "suite": "sha256:...", "run": "...", "passed": 48, "total": 50}`.
The Stage decides, not the runner:

| Param | Default | Meaning |
|---|---|---|
| `minPassRate` | 1 | The share of cases that must pass, from 0 to 1. Fewer fails |

An answer about another version, with no cases, or with no `run` or `suite`
to cite is Unknown. The citation holds the run, the suite, the version and
the counts.

## Claims against the running service

`pacto.runtime` uses the [node agent](runtime-conformance.md). It fails a
version the kernel saw calling a Pacto-managed service its contract never
declares, or not listening on a port it declares. It reads the Pacto
evaluation of exactly this version and passes only when
`status.evaluatedRevision.kernelCoverage` is `Complete`; otherwise the answer
is Unknown.

## After release

After release the checks run again every `recheck` (default 1m) on the last
`window`. A confirmed Fail marks the version failed and raises one rollback
citing the check. The Stage freezes instead when that cause already had its
rollback, when there is nothing to roll back to, or when a person or an agent
chose the running version (a rollback, or a promote during auto-hold).
Unknown after release never rolls back, and a result about a version the Stage
stopped running is dropped. The newest result is in `status.lastCheck`, which
also says when the operation that shipped the running version is gone.

## Explain and history

```bash
pacto delivery explain -n prod <operation>
pacto delivery history -n prod --stage payments
```

`explain` shows where an operation is and every verdict with its citation and
how to run it again. `history` lists a Stage's operations; see
[history, blame and verification](delivery-history.md).

`--cause-verdict '<verdict JSON>'` cites a verdict as an operation's cause, for
example to roll back a frozen Stage. The promoter runs it again on the Stage's
own check over the cited window, which must fall in the time the running
version has run. It refuses a cause that comes back with another result, and
any cause while the running version has no applied time.
