# Checks

`checks` lists the test and eval suites a version ships. Delivery runs them
after release through the same Stages as any other check. The other sections
are in [Contract sections](sections.md) and
[Dependencies and state](dependencies-and-state.md).

## `checks`

Optional. Needs `pactoVersion: "2.1"`: a 2.0 contract with it fails
validation with `NEEDS_PACTO_2_1`.

```yaml
pactoVersion: "2.1"
service:
  name: support-agent
  version: 1.3.0
  owner: {team: support}
checks:
  - name: evals                  # a Stage runs it by this name
    file: evals/suite.yaml       # ships in the bundle
  - name: smoke
    file: tests/smoke.yaml
```

| Field | Meaning |
|---|---|
| `name` | The suite's name, unique in the contract (`DUPLICATE_CHECK_NAME` otherwise). A Stage names it to run it |
| `file` | The suite's file in the bundle (`FILE_NOT_FOUND` when missing) |

Pacto never reads a suite. It packs the file in the bundle, records its
sha256 and hands file and digest to the runner the Stage names. It accepts
only a verdict about that exact digest: no verdict, a timeout or an answer
about another file is Unknown, and Unknown never passes. The contract does not
say which runner runs a suite; the environment does.

### What a change needs

[`pacto diff`](diff.md) compares the suites by name:

| Change | Classification |
|---|---|
| A suite added | Non-breaking: it only adds a check |
| A suite removed, moved or edited | Potentially breaking: a release that drops the cases it fails would loosen the check it ships behind, so a person reviews it |

`pacto diff` compares the suite's bytes, so it sees an edit but cannot tell a
stricter suite from a weaker one: that is the reviewer's call.

### Running a suite in delivery

A Stage runs a suite with an `eval` provider, configured in the delivery chart
like any other check (see
[health checks](../integrations/kubernetes/delivery-health.md)):

```yaml
gates:
  verify:
    checks:
      - name: evals
        provider: evals                # gates.providers: {name: evals, type: eval, url: ...}
        suite: evals                   # the checks[].name to run
        params: {minPassRate: "0.95"}
```

`suite` names the check to run; it can be left out when the version ships
exactly one. A version that does not ship the named suite is held before it
ships, with a message that says so: a person accepts it or it does not ship.
It is neither failed nor passed. The runner says how many cases passed and the
sha256 of the suite it ran, and only an answer about that exact sha256 counts.
`minPassRate` must be above 0.

### An agent is a service

An AI agent needs no section of its own:

- The tools it exposes are an interface of type `mcp`: a `tools/list` result
  such as `{"tools": [{"name": "refund", "inputSchema": {...}}]}`, every tool
  named once (`INVALID_MCP_TOOLS` otherwise). A tool removed is breaking, one
  added is not, and its schemas are read like an OpenAPI request and response.
- The model it calls is a dependency. A model with a contract is compared by
  its range; one without is an
  [external dependency](dependencies-and-state.md#external-dependencies) named
  by its version, so swapping it is potentially breaking.
- Its eval suite is a `checks` entry.

Pacto does not check at runtime which model an agent really calls.
