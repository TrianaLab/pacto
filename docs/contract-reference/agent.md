# Agents

An AI agent is released like any other service. Its contract says which tools
it exposes, which services and models it calls and which eval suite checks it.
Delivery then ships it through the same Stages, with the evals as a check. The
other sections are in [Contract sections](sections.md) and
[Dependencies and state](dependencies-and-state.md).

## `agent`

Optional. Needs `pactoVersion: "2.1"`, like the `mcp` interface type: a 2.0
contract with either fails validation.

```yaml
pactoVersion: "2.1"
service:
  name: support-agent
  version: 1.3.0
  owner: {team: support}
interfaces:
  - name: tools
    type: mcp                          # the tools the agent exposes
    ref: interfaces/tools.json         # a tools/list result
dependencies:
  - name: orders                       # a service the agent calls
    ref: oci://ghcr.io/acme/pacto/orders
    required: true
    compatibility: "^2.0.0"
agent:
  models: [anthropic/claude-sonnet-4-5]  # models with no contract of their own
  evals: evals/suite.yaml                # ships in the bundle
```

| Field | Meaning |
|---|---|
| `models` | The model ids the agent calls, exactly as its code names them. A model served behind its own Pacto contract is a dependency instead, with a version range |
| `evals` | The eval suite's file in the bundle (`FILE_NOT_FOUND` when missing). Pacto ships it with the version and only hashes it, to tie the eval gate's verdict to it: an eval runner runs it |

`agent` needs at least one of the two.

### Tools

An `mcp` interface points at the agent's tools in the shape an MCP server
answers `tools/list` with, as JSON or YAML:

```json
{"tools": [{"name": "refund", "inputSchema": {"type": "object", "required": ["order"]}}]}
```

Every tool needs a name, used once (`INVALID_MCP_TOOLS` otherwise).

### What a change needs

[`pacto diff`](diff.md) reads the tools and the agent section, and
[`pacto publish`](publish.md) turns the result into the version rule:

| Change | Classification |
|---|---|
| A tool removed | Breaking |
| A tool added | Non-breaking |
| A tool's `inputSchema` or `outputSchema` changed | Read like an OpenAPI request and response: a new required input is breaking, a dropped output field is breaking |
| A model added, removed or swapped | Potentially breaking: the agent can answer differently, so it needs at least a minor |
| An eval suite added | Non-breaking: it only adds a check |
| The eval suite removed, moved or edited | Potentially breaking: a release that drops the cases it fails would loosen the check it ships behind, so a person reviews it |

`pacto diff` compares the suite's bytes, so it sees an edit but cannot tell a
stricter suite from a weaker one: that is the reviewer's call. Tool descriptions are not compared. The services the agent calls are ordinary
dependencies, so impact and ordering treat the agent like any other consumer.

### Delivering an agent

A Stage checks an agent with an `eval` provider, configured in the delivery
chart like any other check (see
[health checks](../integrations/kubernetes/delivery-health.md)):

```yaml
gates:
  verify:
    checks:
      - name: evals
        provider: evals                # gates.providers: {name: evals, type: eval, url: ...}
        params: {minPassRate: "0.95"}
```

The promoter tells the runner which contract the version ships and the suite
in it, by path and sha256. The runner runs that suite on the version under
check and says how many cases passed and the sha256 of the suite it ran. Pass
and Fail work as for any check; an answer about another version or another
suite, with no cases or with nothing to cite is Unknown, and Unknown never
passes. `minPassRate` must be above 0.

### What Pacto does not do

- It does not run evals or read what the suite says. The runner does both;
  Pacto only hashes the file, to tie the runner's answer to it.
- It does not check at runtime which models the agent really calls. `models`
  is what the contract declares.
- Model ids are matched exactly. They have no version ranges.
