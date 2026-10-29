# Checks

`checks` lists the test and eval suites a version ships. Delivery runs them
after release through the same Stages as any other check. The other sections
are in [Contract sections](sections.md) and
[Dependencies and state](dependencies-and-state.md).

## `checks`

Optional. Needs `pactoVersion: "2.1"`: a 2.0 contract with it fails
validation with `NEEDS_PACTO_2_1`. 2.1 needs Pacto core 4.1.0 or later and
operator 6.1.0 or later; see [Versions](../installation.md#versions).

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

Pacto treats a suite as opaque. It packs the file in the bundle, records a
sha256 of the suite and hands file and digest to the runner the Stage names. It
accepts only a verdict about that exact digest. No verdict, a timeout or an
answer about another suite is Unknown, and Unknown never passes. The
environment picks the runner for a suite.

A suite is every file in the directory that holds its `file`, because a runner
may read cases, fixtures or golden answers from beside it. Keep each suite in its
own directory, such as `evals/`. A suite file at the bundle root takes in the
whole bundle. The digest is what
`sha256sum` prints for those files, sorted by path, hashed once more:

```bash
cd evals && find . -type f | sed 's|^\./||' | LC_ALL=C sort | xargs sha256sum | sha256sum
```

### What a change needs

[`pacto diff`](diff.md) compares the suites by name:

| Change | Classification |
|---|---|
| A suite added | Non-breaking: it only adds a check |
| A suite removed, moved or edited | Potentially breaking: a release that drops the cases it fails would loosen the check it ships behind, so a person reviews it |

`pacto diff` compares the suite's digest, including every file beside it. It
detects an edit, and the reviewer decides whether the suite got stricter or
weaker.

In delivery, any suite change (added, removed, moved or edited) holds the
version for a person on every level, including autonomous.

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
        suiteDigest: sha256:...        # optional: the exact suite to accept
        params: {minPassRate: "0.95"}
```

`suite` names the check to run. You can omit it when the version ships exactly
one suite. A version that lacks the named suite is held before it ships, with a
message that names the missing suite, until a person accepts it. The check
records no verdict. `pacto delivery plan` and the dashboard predict this hold
too: the chart publishes each provider's name and type (and keeps its URL and
keys private) beside the identity policy, so a client knows which checks run
on an `eval` provider. The runner says how many cases passed and the
sha256 of the suite it ran, and only an answer about that exact sha256 counts.
`minPassRate` must be above 0.

The runner signs its answer, and the provider lists the runner keys it
trusts. An unsigned answer, or one signed by a key outside the provider's
list, is Unknown. The signed answer stays in the delivery record, so an
auditor can see who ran the exam and check that runner's signature.

`suiteDigest` pins the suite to bytes the Stage owner chose. A version that
ships a suite with another sha256 is held for a person before it ships, and its
eval check is Unknown, never Pass.

### An agent is a service

An AI agent uses the same sections as any other service:

- The tools it exposes are an interface of type `mcp`: a `tools/list` result
  such as `{"tools": [{"name": "refund", "inputSchema": {...}}]}`, every tool
  named once (`INVALID_MCP_TOOLS` otherwise). Removing a tool is breaking,
  adding one is non-breaking, and Pacto compares tool schemas like an OpenAPI
  request and response.
- The model it calls is a dependency. A model with a contract is compared by
  its range; one without is an
  [external dependency](dependencies-and-state.md#external-dependencies) named
  by its version, so swapping it is potentially breaking.
- Its eval suite is a `checks` entry, run by an eval runner that signs its
  result.

To pin an agent completely:

- Put its prompt and its agent policy (allowed tools, safety rules) as files in
  the bundle. The contract digest then covers their bytes.
- Declare its tools in the `mcp` interface.
- Name the model by an exact version, such as a dated model ID, and avoid
  aliases such as `latest`. Set it in the deploy manifests, which delivery moves
  by digest. A ConfigMap or Secret outside them escapes the digest.
- Have the eval runner report the model that answered (`observedModel`).
  The eval check then fails when no external dependency version in the
  contract names that model, and a Stage check with `requireObservedModel: true` stays
  Unknown until the runner says.

Two things stay outside the pin: a prompt or tool list the agent fetches at
runtime, and the model the agent calls in production. The eval runner's
`observedModel` records what it saw during the exam, signed with its own key.
Pacto compares it only with the contract's external dependency versions.
