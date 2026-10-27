# Delivery reference: eval providers

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

## The runner's answer

The promoter posts the same `{"input": ..., "params": ...}` as to an
[HTTP provider](delivery-health.md#http-providers), the input also
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

## Trusted runners

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

The Stage sets the pass bar:

| Param | Default | Meaning |
|---|---|---|
| `minPassRate` | 1 | The share of cases that must pass, above 0 and up to 1. Fewer fails |

These answers count as Unknown:

- an answer no listed signer signed
- an answer about another version or another suite
- an answer for another operation or another challenge
- a suite whose sha256 is not the Stage's `suiteDigest`
- no cases, or no run to cite
- a version whose contract cannot be read or does not ship the suite

The verdict cites the contract, the suite, the run and the runner (`runner`),
and keeps the signed answer (`runnerBundle`) for
[`record verify`](delivery-records.md#verify-records).

## Which model answered

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
