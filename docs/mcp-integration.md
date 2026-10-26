# Drive Pacto from an agent

This page is for an engineer who wants an AI agent, such as Claude, Cursor
or Copilot, to use Pacto. It covers how to connect the agent, what it can and
cannot do, and the limits that keep it safe. Every tool and input is listed
in the [MCP reference](mcp-reference.md).

## The words on this page

| Word | Meaning |
|---|---|
| Pacto | A tool that describes each of your services in a contract and checks changes against those contracts |
| Delivery | The optional part of Pacto that runs in a Kubernetes cluster and decides which version of each service runs in each environment, such as test or prod |
| Contract | A file, `pacto.yaml`, in which a service says who owns it, what it offers (its APIs) and which other services it calls |
| Bundle | A contract together with the files it points to, such as the service's OpenAPI document. It is a folder on disk or a published copy in a container registry, written `oci://registry/name:version` |
| Fleet | All your services together, as Pacto sees them from contracts, clusters and registries |
| Change | In delivery, a request to move one service in one environment to another version |
| MCP | Model Context Protocol, the standard way an AI agent finds and calls tools. `pacto mcp` is an MCP server |

## Connect your agent

Install the `pacto` command first ([Installation](installation.md)) and
check that `pacto version` works. Every agent then starts `pacto mcp` and
talks to it over its standard input and output. Pick yours:

=== "Claude Code"

    Add to your project's `.mcp.json`:

    ```json
    {
      "mcpServers": {
        "pacto": { "command": "pacto", "args": ["mcp"] }
      }
    }
    ```

=== "Claude Desktop"

    Add to `claude_desktop_config.json` (`~/Library/Application Support/Claude/` on macOS, `%APPDATA%\Claude\` on Windows):

    ```json
    {
      "mcpServers": {
        "pacto": { "command": "pacto", "args": ["mcp"] }
      }
    }
    ```

=== "Cursor"

    Add to `.cursor/mcp.json`:

    ```json
    {
      "mcpServers": {
        "pacto": { "command": "pacto", "args": ["mcp"] }
      }
    }
    ```

=== "GitHub Copilot"

    Add to `.vscode/mcp.json` (requires VS Code 1.99+ and the Copilot Chat extension):

    ```json
    {
      "servers": {
        "pacto": { "command": "pacto", "args": ["mcp"] }
      }
    }
    ```

That starts the default server. To pick another, add its flag to `args`, for
example `["mcp", "--delivery"]`.

## Pick what the agent may do

How you start the server decides what the agent can reach. Pick one mode:
`--root`, `--fleet`, a bundle, `--publish` and `--delivery` cannot be
combined with each other.

| Start it with | The agent can | It can change |
|---|---|---|
| `pacto mcp` | Write and check contracts in local folders | Contract files on disk |
| `pacto mcp --root oci://...` | Read published contracts and what they depend on | Nothing |
| `pacto mcp --fleet` | Everything the default does, plus answer questions about your fleet | Contract files on disk |
| `pacto mcp ./my-service --base-url https://...` | Everything the default does, plus call the service's own API, one tool per operation. The bundle is a folder or an `oci://` reference; `--base-url` is where the running service answers | Contract files; the live service only with `--allow-writes` |
| `pacto mcp --publish` | Everything the default does, plus publish a version to your registry | Contract files and your registry |
| `pacto mcp --delivery` | Everything the default does, plus read delivery state and, if the cluster counts it as an agent, ask for or cancel a change | Contract files and its own requests; it can never approve |

If the agent must change nothing at all, use `--root`.

**Files.** The contract tools write to the folder the agent names in each
call, relative to the folder the server was started in. Pacto does not limit
which folder, so start the server in the project the agent should work on,
as a user that can write only there. Claude Code and Cursor start
it in your project; for Claude Desktop, have the agent pass full paths.

**Fleet.** `--fleet` reads the sources you name with more flags, such as
`--local ./contracts` for folders, `--oci <ref>` for published contracts and
`--k8s` for your kubeconfig's cluster. See
[`pacto mcp`](cli-reference.md#pacto-mcp).

**Delivery.** `--delivery` acts on the cluster your kubeconfig points at, as
the identity in it. The cluster treats a Kubernetes service account as an
agent, and also anyone in the agent group your platform team set up
(`pacto:agents` by default). Only an agent gets the tools to ask for and
cancel a change; a person gets only the read tools. So give the agent a
kubeconfig for its own service account, never yours: an agent holding your
credentials is you to the cluster.

**Credentials.** `--publish`, `--root` and `--fleet` use the registry logins
of `pacto login`. A bundle's API calls use `--auth name=value` for the
service's own sign-in, as its OpenAPI document declares it.

## What keeps it safe

- **An agent never approves.** In delivery mode it can ask for a change, but
  Pacto's checks (is the version signed, does it break a service that calls
  it, is it healthy after release) and the owning team decide. There is no
  approve tool, and the cluster refuses an approval from an agent anyway.
- **Calls to your service are read-only by default.** Only `GET` and `HEAD`
  operations become tools unless you pass `--allow-writes`. Each call stops
  after 30 seconds and follows no redirect.
- **Tools carry no safety labels.** Pacto does not mark its tools as
  read-only or destructive in MCP, so build allow-lists by name from the
  table below.
- **A missing answer is not proof.** Fleet answers carry `asOf`, the time
  they describe, and `completeness`, `complete` or `partial`. When a source was unreachable the answer says `partial`,
  and "not found" then means "not known", not "does not exist".
- **Answers are frozen at start.** `--root`, `--fleet` and a bundle (the
  `./my-service` argument) are read
  once when the server starts. Restart it to see newer services or versions.
- **Over the network, callers must sign in.** By default the server talks
  over standard input and output. With `-t http` it serves HTTP on this
  machine only. Serving other machines needs `--auth-issuer` and `--resource`,
  so every request carries a token from your company sign-in, and the server
  then refuses `--auth` and `--allow-writes`, which would lend one
  credential to every caller. See
  [agents over the network](integrations/kubernetes/delivery-identity.md#agents-over-the-network).

## Which tools exist

| Tools | Mode | Read or write |
|---|---|---|
| `pacto_create`, `pacto_edit` | every mode but `--root` | Write contract files. `pacto_create` with `pactify: true` does what `pacto init` does for a folder or a cluster (see [Pactify a service](pactify.md)) |
| `pacto_check`, `pacto_schema` | every mode but `--root` | Read |
| `pacto_fleet_search`, `pacto_fleet_get`, `pacto_fleet_graph`, `pacto_fleet_status`, `pacto_fleet_explain`, `pacto_impact` | `--fleet` | Read |
| `pacto_catalog_revision`, plus two MCP resources (documents the agent can read, not tools): `pacto://catalog` and `pacto://catalog/closure`, the contracts you named plus everything they depend on | `--root` | Read |
| One tool per API operation, named by its `operationId` (or `<method>_<path>`) | a bundle | Calls your service: read-only, or its write operations too with `--allow-writes` |
| `pacto_skill` | a bundle | Read: returns the notes a bundle ships for agents in `skills/*.md` |
| `pacto_publish_plan` | `--publish` | Read |
| `pacto_publish` | `--publish` | Writes to your registry |
| `pacto_delivery_operation_types`, `pacto_delivery_operation_plan`, `pacto_delivery_get`, `pacto_delivery_explain`, `pacto_delivery_history`, `pacto_delivery_blame`, `pacto_delivery_undo_plan`, `pacto_delivery_record_verify` | `--delivery` | Read |
| `pacto_delivery_operation_propose`, `pacto_delivery_cancel` | `--delivery`, agents only | Ask for or cancel a change |

Pacto skips any API operation named `pacto_*`, so the prefix always means a
Pacto tool. Inputs and outputs are in the [MCP reference](mcp-reference.md).

## Try it

```text
You:    Create a pacto contract for a stateful Go HTTP API called user-service
        that stores data in PostgreSQL
Claude: [writes pacto.yaml with an HTTP interface and marks the service as
         keeping data that survives a restart]

You:    Add a dependency on payments-api
Claude: [calls pacto_edit with add_dependencies]

You:    Check the contract in ./user-service
Claude: user-service is valid.
```

Pacto adds a dependency only from calls it actually observed (with the node
agent running, pactify does), never from a guess. Otherwise you add them
yourself, as in the second prompt.

## If the tools do not show up

1. Run `pacto version` to check Pacto is installed and on your `PATH`.
2. Check your MCP configuration file for JSON syntax errors.
3. Ask your agent to list its tools (in Claude Code, `/mcp`).
4. Add `-v` to `args` to see what the server does while it starts, such as
   reading a bundle from a registry. It logs nothing per tool call.
