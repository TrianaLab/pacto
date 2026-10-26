# Delivery reference: health checks

Beyond the Pacto contract check, a Stage can check a version's errors and
response times in your monitoring, an AI agent's evals and that the service
calls only what its contract declares (its claims). Every check answers Pass,
Fail or Unknown and cites what it read, so anyone can run it again. Unknown
never passes.

The Pacto resource ships inside every version and names its own target, so
`pacto.contract` and `pacto.runtime` count only a Pacto that evaluated the
workload the Stage says the service runs: `spec.workload` (`kind` and `name`
in the Stage's namespace), or the Deployment named `spec.service.name` when
it is empty. A Pacto that checked another workload, a decoy a version shipped
for example, is Unknown. `pacto init --delivery` writes `spec.workload`.

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
  startRung: advise
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
      - {name: mira, type: mira, url: "https://mira.observability:4318"}
```

The URL must be https. A verdict ends up in a signed record, so an answer
forged on the way could get a Pass signed. Plain http works only on loopback,
or with `allowPlainHTTP: true` on a test cluster. The promoter never follows a
redirect: a provider that answers one gets Unknown.

A provider inside the cluster often has a certificate from an internal CA.
Put that CA's PEM certificates in a ConfigMap in `delivery.namespace` and name
it, so the promoter trusts it on top of the public roots:

```yaml
delivery:
  gates:
    caBundle: {configMap: internal-ca, key: ca.crt}
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

### What a verdict can prove

Mira and `http` verdicts are unsigned and, by default, self-reported. Mira
counts the spans the service sends about itself, keyed on the `service.name`
and `service.version` the sender sets. The version under check, or any pod
that can reach the OTLP endpoint, can leave out its errors or send clean
spans to force a Pass, or send failed ones to force a Fail and a rollback.
An `http` provider's answer is believed as it arrives over https. To make a
verdict worth more:

- accept telemetry only from authenticated senders at the collector;
- add a `where` on attributes the collector sets, not the sender, such as the
  `k8s.pod.uid` or `k8s.namespace.name` the `k8sattributes` processor adds;
- count the spans of the service's callers (`spanKind: client` on the caller's
  side) instead of the service's own.

## HTTP providers

An `http` provider is the plug-in format for any other tool. The promoter
posts `{"input": ..., "params": ...}`, where the input names the operation,
the Stage, the service, the digests under check and the time window. The tool
answers `{"subject": "sha256:...", "result": "Pass|Fail|Unknown", "message": "...", "citation": {...}}`,
where `subject` is the digest it judged. An answer that names no subject, or
a version other than the one under check, is Unknown.

## Eval providers

An `eval` provider is a suite runner: it runs one of the
[checks](../../contract-reference/checks.md) the version ships, such as an AI
agent's eval suite. The Stage check names it in `suite`:

```yaml
spec:
  gates:
    verify:
      checks:
        - name: quality
          provider: evals
          suite: evals
          suiteDigest: sha256:...   # optional
          params:
            minPassRate: "0.95"
```

Without `suite` the runner runs the only check the version ships. A version
that does not ship the named check, or with no `suite` ships no check or more
than one, is held before it ships (the `pacto.suites` gate): a person accepts
it or it does not ship.

`suiteDigest` pins the sha256 of the suite the version ships, which covers
every file in the suite's directory (see
[Checks](../../contract-reference/checks.md#checks)): a version with
another suite is held the same way, and its eval is Unknown. A suite must be
self-contained in its directory.

The promoter posts the same `{"input": ..., "params": ...}`, the input also
naming the contract the version ships and the suite in it:
`"contract": "oci://...@sha256:...", "suite": {"name": "evals", "path": "evals/suite.yaml", "digest": "sha256:..."}`.
The runner runs that suite on the version under check and answers with a
Sigstore bundle: a DSSE envelope over a statement it signs.

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [{"name": "pay-agent", "digest": {"sha256": "..."}}],
  "predicateType": "delivery.pacto.trianalab.io/eval/v1",
  "predicate": {
    "operation": "<the input's operationUID>",
    "challenge": "<the input's challenge>",
    "suite": "sha256:...",
    "run": "https://evals.example/runs/7",
    "passed": 48,
    "total": 50,
    "observedModel": "anthropic/claude-sonnet-4-5",
    "message": "two refunds wrong"
  }
}
```

`operation` and `challenge` echo the input's: the challenge is new for every
check, so an old answer cannot be replayed.
`suite` is the sha256 of the suite it ran and `observedModel` (optional) the model it saw answer. The provider lists the runners it trusts in `signers`,
each a public key or a keyless identity:

```yaml
delivery:
  gates:
    providers:
      - name: evals
        type: eval
        url: https://evals.internal
        signers:
          - name: evals-ci
            publicKey: |
              -----BEGIN PUBLIC KEY-----
              ...
          - name: evals-keyless
            identity: https://github.com/acme/evals/.github/workflows/run.yaml@refs/heads/main
            issuer: https://token.actions.githubusercontent.com
            trustedRoot: '{"mediaType": "application/vnd.dev.sigstore.trustedroot+json;version=0.1", ...}'
```

The Stage decides, not the runner:

| Param | Default | Meaning |
|---|---|---|
| `minPassRate` | 1 | The share of cases that must pass, above 0 and up to 1. Fewer fails |

These are Unknown, never Pass or Fail:

- an answer no listed signer signed
- an answer about another version or another suite
- an answer for another operation or another challenge
- a suite whose sha256 is not the Stage's `suiteDigest`
- no cases, or no run to cite
- a version whose contract cannot be read or does not ship the suite

The verdict cites the contract, the suite, the run and the runner (`runner`),
and keeps the signed answer (`runnerBundle`) for
[`record verify`](delivery-records.md#verify-records).

### Which model answered

When the runner reports an `observedModel` that no external dependency
`version` of the version under check names, the check fails and says both.
Pacto takes the runner's signed word for it; nothing else checks it. A Stage
check can require the runner to say:

```yaml
        - name: quality
          provider: evals
          suite: evals
          requireObservedModel: true
```

Then no `observedModel`, or no dependency version to compare it with, is
Unknown.

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
rollback, when there is nothing to roll back to or when a person or an agent
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
