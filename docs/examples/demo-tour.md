---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Guided tour

Six things people actually need from a fleet they did not build, answered against
one fixture, in the order the questions arrive. Each story is one or two commands
and what its output means.

Stories 1 to 5 are terminal commands whose transcripts were produced by running
the command shown above them. `make gen-demo-transcripts` regenerates them and
`make docs-check` compares them against the committed tree, so a change in what
the CLI prints fails a gate rather than quietly making this page fiction. Story 6
shows output copied in by hand, because no generator covers a server that holds
stdin open. Every command on the page runs in
`tests/acceptance/local/demo-arc.sh`, which asserts on the bytes.

## Before you start

Everything here is offline except the last command. No cluster, no registry, no
running service and no network — the fixture is committed to the repository, so
it works on a plane. `pacto dashboard` probes for a kubeconfig and for OCI
repositories while it boots; it runs without either.

You need the [Pacto CLI](../installation.md) on your `PATH`, and a clone, because
the fixture lives in it:

```bash
git clone https://github.com/TrianaLab/pacto.git && cd pacto
```

Every command below is written to be pasted from the repository root.

To drive a real stack instead — an OCI registry with published revisions, an
Evidence Server ingesting a signed envelope and the dashboard on top — see the
[Docker Compose demo](compose-demo.md), which needs no clone and no CLI. To point
your own MCP client at one of these bundles, see
[Connecting to a bundle](../mcp-integration.md#connecting-to-a-bundle).

## The fleet

Sixteen services are in the snapshot. These eight carry the stories:

| Service | Owner | Its part |
|---------|-------|----------|
| `payments-service` | payments-team | six revisions; the change stories 3 and 4 analyse |
| `orders-service` | commerce-team | declares a dependency on payments, and is observed calling it |
| `api-gateway` | platform-foundations | declares the same dependency, and has an expired readiness assessment |
| `auth-service` | identity-team | deployed, but never observed — the Unknown target |
| `fraud-service` | payments-team | evidenced and conformant — the Compliant target |
| `audit-log` | platform-foundations-security | calls payments and declares nothing — the shadow consumer |
| `frontend` | frontend-team | reaches payments transitively, through the gateway |
| `pacto-demo` | platform-foundations | one more hop out, so the blast radius has depth |

A contract says what a service exposes, what it needs and how it behaves — never
how it is deployed. Here is the shape of the one story 3 refuses,
[`payments-service` at 2.0.1](https://github.com/TrianaLab/pacto/blob/main/examples/demo/bundles/payments-service/v2.0.1/pacto.yaml):

```yaml
pactoVersion: '2.0'
service:
  name: payments-service
  version: 2.0.1
  owner: { team: team/payments }
interfaces:
- { name: http, type: openapi, ref: interfaces/openapi.json, visibility: internal }
- { name: events, type: asyncapi, ref: interfaces/events.json, visibility: internal }
capabilities:
- type: health
  binding: { type: http, interface: http, path: /healthz }
configurations:
- { name: platform, ref: 'oci://ghcr.io/trianalab/pacto/platform-app-config', required: true }
policies:
- { name: http-security, ref: 'oci://ghcr.io/trianalab/pacto/platform-http-policy' }
dependencies:
- { name: postgresql, ref: 'oci://ghcr.io/trianalab/pacto/postgresql', required: true, compatibility: ^16.0.0 }
- { name: fraud-service, ref: 'oci://ghcr.io/trianalab/pacto/fraud-service', required: true, compatibility: ^1.0.0 }
workload: service
state:
  type: stateful
  persistence: { scope: shared, durability: persistent }
  dataCriticality: high
metadata:
  tier: domain
  criticality: high
  summary: Payment Intents API with mandatory fraud detection - BREAKING from v1.x
    charges API
```

Nothing above says how the service is deployed, and `metadata` is not decoration:
the `http-security` policy this contract references requires it, which is why
abridging it any further would fail validation. The
[contract reference](../contract-reference/sections.md) has every section.

## Story 1 — "I inherited this fleet and I do not know what is in it"

*Enumerate everything from contracts alone, then find out what state it is in.*

--8<-- "examples/demo/generated/_beat-01.md"

Sixteen services, each with its owner, how many contract revisions it has
published and how many deployed targets are running it. `NotEvaluated` is not a
verdict: no evidence source is wired up, so nothing has been evaluated against
anything.

`--target-state` folds in what a platform observed about the running deployments.
The fixture is one file, and it models what an evidence pipeline would ingest —
three of its targets, abridged:

```yaml
schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - { scope: production-eu, kind: kubernetes-workload, name: payments/payments-service,
      service: payments-service, compliance: Compliant,
      coverage: { evaluated: 6, required: 6 }, evidenceAt: 2026-07-29T09:40:00Z }

  - scope: production-eu
    kind: kubernetes-workload
    name: commerce/orders-service
    service: orders-service
    compliance: NonCompliant
    coverage: { evaluated: 5, required: 5 }
    evidenceAt: 2026-07-29T09:40:00Z
    findings:
      - code: STATELESS_PERSISTENT_CONFLICT
        severity: error
        category: RuntimeDrift
        message: declared stateless but the observed workload mounts a persistent volume

  - { scope: production-eu, kind: kubernetes-workload, name: identity/auth-service,
      service: auth-service, compliance: Unknown,
      coverage: { evaluated: 1, required: 4 } }
```

--8<-- "examples/demo/generated/_beat-02.md"

Four states in one screen. `Compliant` is a verdict backed by evidence,
`NonCompliant` is a confirmed contradiction, `Unknown` is evidence that never
arrived and `NotEvaluated` means nobody is deployed there to evaluate. The
distinction that matters is between the middle two: one is a problem with the
service, the other is a problem with your ability to see it.

## Story 2 — "Something says Unknown and I need to know whether that is bad"

*Get the reason behind a verdict, so absence of evidence never reads as a pass.*

--8<-- "examples/demo/generated/_beat-03.md"

Targets can be addressed by their unique name or by their canonical key, which
escapes slashes as `%2F`.

`EVIDENCE_MISSING` is a fact about the observer, not about the service. The
second command is the same lesson from the other side: `api-gateway` declares
five readiness claims, every one marked `done` with evidence attached, and the
assessment earns zero because it expired on 2025-01-01. An assertion nobody has
re-checked is not a passing check. Pacto fails closed on both.

A confirmed violation reads differently:

--8<-- "examples/demo/generated/_beat-04.md"

`Coverage: 5/5 evaluated` is what separates this from the Unknown above: every
check ran, so the verdict is a conclusion rather than a gap. The finding names
the exact contradiction — the contract declares the workload stateless, the
observation found a persistent volume mounted. One fact disagreeing with one
declaration, not a score.

## Story 3 — "I am about to ship a change that might break someone"

*Classify a change from two contracts, with nothing running, and gate CI on it.*

--8<-- "examples/demo/generated/_beat-05.md"

Thirty-nine changes, one verdict, and a non-zero exit so CI can gate on it. The
interesting part is the spread: two API paths removed, two event channels
withdrawn, a required request field swapped for a differently-named one, two
configuration keys becoming required, a capability dropped, an optional
dependency becoming mandatory and one SBOM package version moving. All of it is
one contract compared with another — no running service was consulted. The
[classification rules](../contract-reference/diff.md#change-classification-rules)
are a published table, not a heuristic.

The event surface is in that list because Pacto compares AsyncAPI content, not
just the `ref`: `payment.completed` and `payment.failed` are gone outright, and
`payment.refunded` swapped `charge_id` for `payment_intent_id` in both its
payload properties and its `required` set. What Pacto does not compare is
[a published table too](../contract-reference/diff.md#not-currently-compared),
because a coverage gap you can read is worth more than one you infer from a clean
result.

Not every release is a break. The same service, one pair earlier:

--8<-- "examples/demo/generated/_beat-06.md"

`POTENTIAL_BREAKING`, not `BREAKING`: a new endpoint, a new event channel and a
new optional configuration property break nobody by themselves, but adding a
property to a schema can still surprise a consumer that validates strictly. Three
classifications exist because two would force every additive change into one of
the wrong ones.

## Story 4 — "It does break. Who do I have to tell?"

*Turn a classification into a list of consumers, each graded by how it is known.*

--8<-- "examples/demo/generated/_beat-07.md"

Four consumers. `confidence=contractual` means the consumer declared this
dependency in its own contract; `confidence=inferred` means it was reached
transitively through one that did. `compat=incompatible` is a second, independent
judgement: the consumer's declared version range does not admit 2.0.1. A consumer
graded `unknown` is not safe — it is unassessed.

Contracts only find consumers that wrote one down. Add observed traffic:

--8<-- "examples/demo/generated/_beat-08.md"

Five now. `audit-log` calls `payments-service` in production and declares nothing
about it, so no amount of reading contracts would ever have found it — it arrives
as `confidence=observed`. And `orders-service`, which both declared the dependency
and was seen using it, is upgraded to `confidence=corroborated`. Declaration and
observation are separate evidence, and Pacto keeps them separate instead of
averaging them into a number.

Then ask where the change actually lands:

--8<-- "examples/demo/generated/_beat-09.md"

The consumer list is identical. What is new is the last two lines. Two deployed
targets sit in the path of this change — `orders-service`, one of the five
consumers, and `payments-service` itself — so this is not hypothetical, and the
command exits non-zero. Story 3 refused a change for what it is. This refuses it
for where it lands.

## Story 5 — "My architecture diagram and my traffic disagree"

*Reconcile the edges contracts declare against the edges something observed.*

--8<-- "examples/demo/generated/_beat-10.md"

Three verdicts, and the two that are not `matched` are the ones worth reading.
`observed-not-declared` is the shadow dependency from story 4, stated as a
reconciliation result rather than as a side effect of an impact analysis.
`declared-not-observed` is the opposite risk — an edge in the diagram that no
traffic has ever taken — and here it is mostly an artefact of a small trace
fixture, which is exactly the ambiguity the verdict is honest about.

## Story 6 — "I want an agent on this, without handing it write access"

*Serve a contract as MCP tools, and check what the default withholds.*

`pacto mcp` turns a bundle's OpenAPI interface into MCP tools. By default it turns
only the safe half.

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | pacto mcp examples/demo/bundles/payments-service/v2.1.0 \
      --base-url http://127.0.0.1:1
```

```console
pacto mcp: skipped 5 mutating operation(s) in interface "http" (use --allow-writes to expose)
MCP server running on stdio
server is closing: EOF
```

Five mutating operations — every `POST`, `PUT`, `PATCH` and `DELETE` the
interface declares — were withheld, and the server said so on stderr rather than
leaving you to count tools. Add `--allow-writes` and both the warning and the
restriction disappear. (`--base-url http://127.0.0.1:1` is deliberately a dead
address: this is about which tools exist, not about calling one. The exit status
is not the signal either — the server exits non-zero on EOF either way, so the
stderr line is the only difference.)

That default covers the interface, not the whole tool list. Beside the interface
operations the server always registers Pacto's own authoring tools, and two of
them — `pacto_create` and `pacto_edit` — write contract files to disk.
`--allow-writes` governs the interface half only; withhold the authoring tools by
not registering this server where you do not want contracts written.

Finally, point a human and an agent at one process. The Pacto dashboard is itself
a Pacto bundle with a real OpenAPI contract, so the agent's tools are generated
from the contract of the very server the human is looking at:

=== "CLI"

    ```bash
    pacto dashboard examples/demo/bundles --port 8899
    ```

    Then open <http://127.0.0.1:8899/#/fleet> and read the same fleet stories 1
    to 5 printed, with the dependency graph drawn and the blast radius
    highlighted.

=== "Ask the agent"

    Register the dashboard's own bundle against the running server. In
    `.mcp.json`:

    ```json
    {
      "mcpServers": {
        "pacto-dashboard": {
          "command": "pacto",
          "args": ["mcp", "examples/demo/pacto-dashboard",
                   "--base-url", "http://127.0.0.1:8899"]
        }
      }
    }
    ```

    The tools come from the dashboard's OpenAPI interface, one per read-only
    operation. Calling `health` reaches the live server, and the raw exchange is
    the proof (the `version` string reflects the build):

    ```console
    {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\n  \"StatusCode\": 200,\n  \"Headers\": {\n    \"Content-Length\": \"32\",\n    \"Content-Type\": \"application/json\",\n    \"Date\": \"Sun, 06 Sep 2026 16:59:12 GMT\"\n  },\n  \"Body\": \"{\\\"status\\\":\\\"ok\\\",\\\"version\\\":\\\"dev\\\"}\\n\"\n}"}]}}
    ```

Nobody wrote a `health` tool. It exists because the dashboard's contract declares
the operation, which is the same reason the human's browser can reach it.

## What this proved

| Story | The guarantee |
|-------|---------------|
| 1 | A fleet is enumerable from contracts alone, and compliance has four states — "not evaluated" is not one of the passing ones |
| 2 | Missing evidence, an expired assessment and a confirmed violation are three different answers, each naming its reason |
| 3 | A breaking change is classified from two contracts with nothing running, exits non-zero, and additive change is its own verdict |
| 4 | Blast radius is graded by how each consumer is known, observed traffic finds the ones no contract declares, and a change is refused for where it lands |
| 5 | Declared and observed edges are reconciled into explicit verdicts rather than averaged |
| 6 | Mutating operations are withheld by default and the withholding is announced, and a human and an agent read one server from one contract |

## Beyond the stories

These are not asserted anywhere, but they run against the same fixture and answer
what the stories raise next:

```bash
BUNDLES=examples/demo/bundles
EVIDENCE=examples/demo/fleet-targets.yaml
alias pfleet="pacto fleet --local $BUNDLES --target-state $EVIDENCE --freshness 24h"

pfleet search --owner payments                                # who owns what
pfleet graph auth-service --direction dependents --transitive # who depends on it
pfleet status                                                 # what needs attention
pfleet search --output-format json                            # machine-readable
```

With `--output-format json` the `meta` block carries `schemaVersion`,
`snapshotId`, `asOf`, `completeness` and any `limitations`. Point `--target-state`
at a file that does not exist to see what partial looks like: the local revisions
still come back and the missing source is reported as `unavailable`, never as
empty. A missing result does not prove absence when the sources are incomplete,
which is the whole point of [the operational graph](../operational-graph.md).

The same read model is available to an agent. `pacto mcp --fleet` serves it as six
read-only tools — `pacto_fleet_search`, `pacto_fleet_get`, `pacto_fleet_graph`,
`pacto_fleet_status`, `pacto_fleet_explain` and `pacto_impact`, which is story 4
as one call — alongside the four authoring tools, which are not read-only:
`pacto_create` and `pacto_edit` write contract files to disk.

```bash
pacto mcp --fleet \
  --local examples/demo/bundles \
  --target-state examples/demo/fleet-targets.yaml \
  --freshness 24h
```

Ask it "which services depend on `auth-service`, who owns them and which of their
deployed targets are Unknown or NonCompliant?" and it resolves to
`pacto_fleet_graph` for the dependents, `pacto_fleet_get` for each owner,
`pacto_fleet_status { needs_attention: true }` for the attention codes and
`pacto_fleet_explain` for the reason behind Unknown.

Always pass `needs_attention` to `pacto_fleet_status`. Called with no arguments it
returns an empty item list, which reads as a clean bill of health and is not one.
These fleet tools observe: they never modify a contract, deploy anything, call a
live service or grant an authorization.

The dashboard serves the same read model over HTTP at `/api/fleet/snapshot`,
`/api/fleet/services`, `/api/fleet/services/{name}/graph` and `/api/fleet/status`.

## Next

The [Quickstart](../quickstart.md) takes an empty directory to a published
contract in about five minutes. The
[contract reference](../contract-reference/sections.md) is the complete list of
sections, and [`pacto diff`](../contract-reference/diff.md#change-classification-rules)
is the complete table of what counts as a breaking change.
