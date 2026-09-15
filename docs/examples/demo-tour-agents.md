---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Guided tour: traffic and agents

The last two stories of the [guided tour](demo-tour.md), on the same fixture.

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

### One process, a human and an agent

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

### The same snapshot in the terminal UI

The same snapshot — plus the observed edges from story 5 — also opens as a
full-screen terminal UI, for reading the fleet without a browser. It needs an
interactive terminal, so unlike the recorded stories it leaves no transcript:

```bash
pacto tui \
  --local examples/demo/bundles \
  --target-state examples/demo/fleet-targets.yaml \
  --traces examples/demo/traces.json \
  --freshness 24h
```

It opens on the Services tab over the same sixteen services and four deployed
targets, so before you press anything `orders-service` reads `NonCompliant`,
`auth-service` reads `Unknown` and `payments-service` and `fraud-service` read
`Compliant`. Press `a` for what needs attention — the twenty-two items
`pfleet status` prints above — and `g` on a highlighted row for its neighborhood
graph. `--read-only` drops push, pull, lock update and generate from the UI
entirely, and the full key table is in
[The terminal UI](../fleet-tools.md#the-terminal-ui).

### The same read model as agent tools

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
