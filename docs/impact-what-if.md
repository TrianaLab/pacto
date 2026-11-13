# Ask what happens if (`pacto impact --unavailable`)

[Impact analysis](impact.md) answers who a contract change reaches. The same
analysis answers what happens if a service becomes unavailable, is removed or
regresses, over the same snapshot and with the same answer shape.

## Ask from the CLI

Three flags ask about the service as it is, with no contract change. Each takes
a fleet service key and no positional arguments:

```bash
pacto impact --unavailable redis --local examples/demo/bundles
pacto impact --removed postgresql --local examples/demo/bundles
pacto impact --regressed payments-service@sha256:<digest> --local examples/demo/bundles
```

Event answers always read the snapshot's observed edges and its callers with
no contract, because availability is a runtime question. Each consumer carries
an `effect`, graded over every path from it to the service:

| `effect` | When |
|----------|------|
| `breaks` | Some path holds only required dependencies. |
| `may-degrade` | Every path holds an optional dependency. These consumers have their own lane and never count as unaffected. |
| `unknown` | The path holds a hop known only from a configuration reference or an observed call, or the caller has no contract. |

No version is judged, so every `compatibilityVerdict` is `unknown`.

## The event gate

An event never blocks: only a contract change has a version a consumer can
refuse. The gate is `unverified` (exit `2`) when anything depends on the
service or the answer is partial. It is `clear` (exit `0`) only when the
complete snapshot holds no consumer, and its reason names the observation
sources consulted, since a service with no contract can still call it. The
reason names the consumers that break on fresh targets first. A consumer whose
target evidence is stale was last seen there more than 24 hours before `asOf`.

## Removal and regression evidence

`--removed` lists each consumer's `resources`: what it asked the service for,
with the provisioning status per environment. When the service declares its
lifecycle, such as deprecated or sunset, each consumer cites it as `lifecycle`
evidence. Policy referrers join a
removal as `unknown`, since their next revision fails validation. Golden paths
that reference it are listed under `templates`, never as consumers.

`--regressed` grades consumers like `--unavailable`. When the revision is an
artifact digest (`sha256:...`), Pacto reads delivery's recorded failures of
that digest with your own cluster credentials: Fail verdicts, failed digests
and rollbacks, at most 50, under `regression`. A
`DELIVERY_EVIDENCE_TRUNCATED` limitation counts the facts beyond 50. A rollback line read from a
Stage's status is marked "from status, not signed". A version names no single
artifact, so it reads nothing. Whenever delivery cannot be read in full, the
answer carries a `DELIVERY_EVIDENCE_UNAVAILABLE` limitation with the reason. An
unread Stage never reads as "no failure recorded".

## From an agent

The `pacto_impact` MCP tool takes `event` (`unavailable`, `removed` or
`regressed`) with `service`, and `revision` for a regression, in place of
`old_ref` and `new_ref`. Only a stdio server reads delivery evidence, a
`traces` path or a local ref, so run `pacto mcp --fleet` over stdio beside the
agent to cite delivery's recorded failures; see
[Fleet query safety](mcp-reference.md#fleet-query-safety).

## In the dashboard

Switch the workspace to **What if this service…** to ask the same events. The
service page and the graph's selected-service panel link to it with **What if
it becomes unavailable?**. The answer draws one lane per effect, plus the
sources it could not read, and the gate sits under the lanes that decide it.

![Change analysis event mode for redis becoming unavailable: 14 consumers break, 2 may degrade and the gate needs a person](assets/screenshots/dashboard-change-analysis-event-light.png#only-light)
![Change analysis event mode for redis becoming unavailable: 14 consumers break, 2 may degrade and the gate needs a person](assets/screenshots/dashboard-change-analysis-event-dark.png#only-dark)

## What's next

- [Impact analysis](impact.md): the fields every consumer carries and the
  release gate of a contract change
- [Impact consumers and evidence](impact-consumers.md): policy referrers,
  observed callers and the 24-hour freshness rule
