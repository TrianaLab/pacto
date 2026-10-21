# Health checks and claims

A Stage can ask more of a new version than the Pacto contract check. It can
check the version's errors and response times in your monitoring, and check
that the version only calls what its contract declares. These checks keep
running after release, so a problem found an hour later still rolls the
version back.

Every check answers Pass, Fail or Unknown and cites what it read, so anyone can
run it again. Unknown never passes.

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

The promoter runs the Pacto contract check and every listed check on the time
since Flux applied the version. Any Fail fails the version and rolls it back.
All Pass verifies it. Anything else waits, and when `window` runs out a person
is asked to accept the unknown or reject the version.

## Providers

`pacto.runtime` is built in. Every other provider is configured in the
delivery chart, never in a Stage, so a Stage or an agent can name a provider
but never point the promoter at a host:

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

Mira's API returns rows, not counts, so the check reads every page. A read
that fails or stops short is Unknown, never Pass. The citation holds the exact
query documents with absolute time bounds in nanoseconds, so posting them to
Mira's `/api/v1/query` again reads exactly the same spans.

An `http` provider is the plug-in format for any other tool. The promoter
posts `{"input": ..., "params": ...}`, where the input names the operation,
the Stage, the service, the digests under check and the time window. The tool
answers `{"result": "Pass|Fail|Unknown", "message": "...", "citation": {...}}`.

## Claims against the running service

`pacto.runtime` compares what the version does with what its contract says,
using the [node agent](runtime-conformance.md). It fails a version the kernel
saw calling a Pacto-managed service its contract never declares, or not
listening on a port it declares. It reads the Pacto evaluation of exactly this
version, and passes only when
`status.evaluatedRevision.kernelCoverage` is `Complete`. If the node agent is
off or could not see every pod, the answer is Unknown.

## After release

Once a version ships, the promoter runs the same checks again every
`recheck` (default 1m) on the last `window` of time. A confirmed Fail marks the
version failed and raises one rollback, which cites the failing check. If that
cause already had its rollback, or there is nothing to roll back to, the Stage
freezes and waits for a person. It also freezes when a person or an agent
chose the running version, with a rollback or a promote while the Stage is on
auto-hold: automation never undoes that choice. Unknown after release never
rolls back, and a result about a version the Stage stopped running while the
checks ran is dropped. The newest result is in the Stage's `status.lastCheck`,
which also says when the operation that shipped the running version is gone.

## Explain and history

```bash
pacto delivery explain -n prod <operation>
pacto delivery history -n prod --stage payments
```

`explain` shows where an operation is, every verdict with its citation and how
to run it again. `history` lists a Stage's operations in order, from its
signed records when the recorder runs; see [reading records](delivery-history.md).

A person or an agent can cite a verdict as the cause of an operation with
`--cause-verdict '<verdict JSON>'`, for example to roll back a frozen Stage.
The promoter runs it again on the Stage's own check when it admits the
operation, over the cited window, which must fall in the time the running
version has run. A cause that comes back Unknown or with another result is
refused, and so is any cause while the running version has no applied time
(Flux never confirmed it, or its operation is gone).
