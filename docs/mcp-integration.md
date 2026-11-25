# Drive Pacto from an agent

This page is for an engineer who wants an AI agent, such as Claude, Cursor
or Copilot, to use Pacto. It covers how to connect the agent, what it can do
and the limits that keep it safe. Every tool and input is listed
in the [MCP reference](mcp-reference.md).

Words in links are defined in the [glossary](glossary.md): [MCP](glossary.md#mcp),
[contract](glossary.md#contract), [bundle](glossary.md#bundle),
[fleet](glossary.md#fleet), [delivery](glossary.md#delivery) and
[change](glossary.md#change).

## Connect your agent

With the [Kubernetes operator](integrations/kubernetes/installation.md)
installed, [connect to it](#connect-to-an-installed-pacto) instead. For a
local server, install the `pacto` command first ([Installation](installation.md)) and
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

## Connect to an installed Pacto

The operator's managed dashboard also serves MCP at `/mcp`, in the same
process and with the same sign-in. One entry gives the agent your
organization's context (services, owners and what runs), the impact of a
change on its consumers and, where delivery is on, the Stages and the changes
through them. The install notes print the URL and a token for the read-only
`pacto-viewer` ServiceAccount:

```bash
export PACTO_TOKEN=$(kubectl -n pacto-operator-system create token pacto-viewer --audience pacto-dashboard --duration 1h)
kubectl -n pacto-operator-system port-forward svc/pacto-dashboard 3000:3000
```

=== "Claude Code"

    ```json
    {
      "mcpServers": {
        "pacto": {
          "type": "http",
          "url": "http://127.0.0.1:3000/mcp",
          "headers": { "Authorization": "Bearer ${PACTO_TOKEN}" }
        }
      }
    }
    ```

=== "Cursor"

    ```json
    {
      "mcpServers": {
        "pacto": {
          "url": "http://127.0.0.1:3000/mcp",
          "headers": { "Authorization": "Bearer ${env:PACTO_TOKEN}" }
        }
      }
    }
    ```

=== "GitHub Copilot"

    ```json
    {
      "servers": {
        "pacto": {
          "type": "http",
          "url": "http://127.0.0.1:3000/mcp",
          "headers": { "Authorization": "Bearer ${env:PACTO_TOKEN}" }
        }
      }
    }
    ```

Behind an Ingress or HTTPRoute, use its host instead. What the agent may do
follows its token:

| Token | The agent can |
|---|---|
| `pacto-viewer`, or any ServiceAccount token minted with `--audience pacto-dashboard` only | Read the fleet. `pacto_capabilities` answers `caller.fleetOnly: true` and a `hint` naming the audience to add |
| A ServiceAccount token minted with `--audience pacto-dashboard` and the cluster's own audience | Read the fleet and delivery, and propose and cancel changes as that ServiceAccount |
| A person's own token the cluster accepts, such as an OIDC ID token or an EKS token | Read the fleet and delivery as that person |
| A token from your issuer, with `dashboard.mcp.exchangeAudience` set | Act as the identity the exchanged token names |

With `dashboard.mcp.exchangeAudience` set, tokens from the dashboard's own
issuer are exchanged and must name the MCP URL as their audience.

Nobody gets an approve tool.

**Agents that propose.** Give the agent its own ServiceAccount, bind it to
`pacto-operator-delivery-agent` and mint its token for both audiences:

```bash
kubectl create clusterrolebinding my-agent-delivery \
  --clusterrole=pacto-operator-delivery-agent --serviceaccount=agents:my-agent
AUD=$(kubectl -n agents create token my-agent |
  jq -rR 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | .aud[0]')
export PACTO_TOKEN=$(kubectl -n agents create token my-agent \
  --audience pacto-dashboard --audience "$AUD" --duration 1h)
```

**Your issuer.** With `dashboard.auth.oidc` set, `dashboard.mcp.exchangeAudience`
makes the dashboard accept tokens your issuer minted for the MCP URL and
exchange them with its own OIDC client (RFC 8693) for one the API server
accepts. The URL must be fixed: `dashboard.mcp.url`, an Ingress host or an
HTTPRoute hostname. Its path is exactly `/mcp`; a path prefix is not supported. The metadata at `/.well-known/oauth-protected-resource/mcp`
names the issuer, so a client that supports MCP authorization signs in by
itself.

To turn the endpoint off, set `dashboard.mcp.enabled=false`. `profile:
hardened` runs no managed dashboard, so it serves no `/mcp`. A dashboard you
run yourself serves it with `pacto dashboard --mcp`. See
[MCP at /mcp](integrations/kubernetes/dashboard-authentication.md#mcp-at-mcp).

## Pick what the agent may do

How you start the server decides what the agent can reach. A bundle,
`--fleet` and `--delivery` combine in one server; `--root` and `--publish`
each start a server of their own. The agent's first call can be
`pacto_capabilities`, which lists what the server serves and who the caller
is to it.

| Start it with | The agent can | It can change |
|---|---|---|
| `pacto mcp` | Write and check contracts in local folders | Contract files on disk |
| `pacto mcp --root oci://...` | Read published contracts and what they depend on | Nothing |
| `pacto mcp --fleet` | Everything the default does, plus answer questions about your fleet | Contract files on disk |
| `pacto mcp ./my-service --base-url https://...` | Everything the default does, plus call the service's own API, one tool per operation. The bundle is a folder or an `oci://` reference; `--base-url` is where the running service answers | Contract files; the live service only with `--allow-writes` |
| `pacto mcp --publish` | Everything the default does, plus publish a version to your registry | Contract files and your registry |
| `pacto mcp --delivery` | Everything the default does, plus read delivery state and, if the cluster counts it as an agent, ask for or cancel a change | Contract files and its own requests; it can never approve |
| `pacto mcp --fleet --delivery` | Both in one session: answer questions about your fleet, read what the dashboard's delivery views show and ask for a change | Contract files and its own requests; it can never approve |

If the agent must change nothing at all, use `--root`.

**Files.** The contract tools run over stdio only. They write to the folder
the agent names in each call, relative to the folder the server was started in. Pacto does not limit
which folder, so start the server in the project the agent should work on,
as a user that can write only there. Claude Code and Cursor start
it in your project; for Claude Desktop, have the agent pass full paths.

**Fleet.** `--fleet` reads the sources you name with more flags, such as
`--local ./contracts` for folders, `--oci <ref>` for published contracts and
`--k8s` for your kubeconfig's cluster. Over stdio it also reads the
bundles under the folder it was started in (`--local .`); over HTTP it reads
local bundles only from the `--local` roots you name. See
[`pacto mcp`](cli-reference.md#pacto-mcp).
[Ask questions about your organization](ask-your-organization.md) lists the
questions an agent can answer with it, and [Investigate a problem with
Mira](investigate-a-problem.md) pairs it with Mira's traces during an incident.

**Credentials.** `--publish`, `--root` and `--fleet` use the registry logins
of `pacto login`. A bundle's API calls use `--auth name=value` for the
service's own sign-in, as its OpenAPI document declares it.

## Delivery: who counts as an agent

`--delivery` acts on the cluster your kubeconfig points at, as
the identity in it. Every service account counts as an agent, and so does
anyone the platform team's groups name as one; see
[Who counts as a person, an agent or a team](integrations/kubernetes/delivery-identity.md#who-counts-as-a-person-an-agent-or-a-team).
Only an agent gets the tools to
ask for and cancel a change; a person gets only the read tools. So give the
agent a kubeconfig for its own service account, never yours: an agent holding
your credentials is you to the cluster. Have the agent fill `on_behalf_of` on
`pacto_delivery_operation_propose` with the person it works for: then that
person cannot also approve the change, which keeps two people on every
change.

## What keeps it safe

- **An agent never approves.** In delivery mode it can ask for a change, and
  Pacto's checks and the owning team decide
  ([Who may approve](integrations/kubernetes/delivery.md#who-may-approve)).
  There is no approve tool.
- **An agent's create is a draft.** `pacto_create_service` on `/mcp` leaves
  a `draft-<n>` a person reviews and signs in the dashboard, and only after
  the platform team grants the caller `create` on
  `services.pacto.trianalab.io`; see
  [Create a service from the dashboard or an agent](golden-paths.md#create-a-service-from-the-dashboard-or-an-agent).
- **Calls to your service are read-only by default.** Only `GET` and `HEAD`
  operations become tools unless you pass `--allow-writes`. Each call stops
  after 30 seconds and follows no redirect.
- **Tools carry safety hints.** Every tool says in MCP whether it is
  read-only or destructive. Hints guide your client; `--allow-writes` and
  the stdio-only rules decide what runs.
- **Partial answers say so.** Fleet answers carry `asOf`, the time
  they describe, and `completeness`, `complete` or `partial`. When a source was unreachable the answer says `partial`,
  and "not found" then means "not known".
- **Most answers are frozen at start.** `--root` and a bundle (the
  `./my-service` argument) are read
  once when the server starts. Restart it to see newer services or versions.
  `--fleet` rebuilds its snapshot when a call arrives 30 seconds or more
  after the last rebuild attempt.
- **Over the network, callers must sign in.** By default the server talks
  over standard input and output. With `-t http` it serves HTTP on this
  machine only, and every request carries the bearer it prints at launch.
  Serving other machines needs `--auth-issuer` and `--resource`, so every
  request carries a token from your company sign-in, and the server then
  refuses `--auth` and `--allow-writes`, which would lend one credential to
  every caller. With `--fleet` or `--delivery` it exchanges each caller's
  token (`--exchange-client-id`) and the API server names the caller, as the
  dashboard does. See
  [agents over the network](integrations/kubernetes/delivery-agents.md#agents-over-the-network).
- **Over HTTP the only contract tool is `pacto_schema`.** `pacto_create`,
  `pacto_edit` and `pacto_check` read and write the server's folders, read
  its kubeconfig and follow refs with its registry credentials, so they run
  beside the agent with `pacto mcp` over stdio.

## Which tools exist

| Tools | Mode | Read or write |
|---|---|---|
| `pacto_create`, `pacto_edit` | every mode but `--root`, over stdio only | Write contract files. `pacto_create` with `pactify: true` does what `pacto init` does for a folder or a cluster (see [Pactify a service](pactify.md)) |
| `pacto_check` | every mode but `--root`, over stdio only | Read |
| `pacto_schema` | every mode but `--root` | Read |
| `pacto_capabilities` | every mode but `--root` | Read: what this server serves and who the caller is |
| `pacto_fleet_search`, `pacto_fleet_get`, `pacto_fleet_graph`, `pacto_fleet_status`, `pacto_fleet_explain`, `pacto_fleet_entities`, `pacto_fleet_entity`, `pacto_fleet_attention`, `pacto_fleet_neighborhood`, `pacto_fleet_map`, `pacto_fleet_versions`, `pacto_fleet_revision_document`, `pacto_fleet_overview`, `pacto_impact`, `pacto_stats` | `--fleet` (`pacto_stats` over HTTP only with `--stats-read-registry`) | Read |
| `pacto_catalog_revision`, plus two MCP resources (documents the agent reads): `pacto://catalog` and `pacto://catalog/closure`, the contracts you named plus everything they depend on | `--root` | Read |
| One tool per API operation, named by its `operationId` (or `<method>_<path>`) | a bundle | Calls your service: read-only, or its write operations too with `--allow-writes` |
| `pacto_skill` | a bundle | Read: returns the notes a bundle ships for agents in `skills/*.md` |
| `pacto_publish_plan` | `--publish` | Read |
| `pacto_publish` | `--publish` | Writes to your registry |
| `pacto_delivery_status`, `pacto_delivery_operation_types`, `pacto_delivery_candidates`, `pacto_delivery_operation_plan`, `pacto_delivery_get`, `pacto_delivery_explain`, `pacto_delivery_history`, `pacto_delivery_blame`, `pacto_delivery_undo_plan`, `pacto_delivery_record_get`, `pacto_delivery_record_verify`, `pacto_delivery_lifecycle` | `--delivery` | Read |
| `pacto_delivery_caller`, `pacto_delivery_inbox`, `pacto_delivery_environments`, `pacto_delivery_autonomy`, `pacto_delivery_published`, `pacto_delivery_refusals`, `pacto_delivery_trust` | `--delivery` | Read: what the dashboard's delivery views show, with the caller's identity |
| `pacto_delivery_operation_propose`, `pacto_delivery_cancel` | `--delivery`, agents only | Ask for or cancel a change |

Over HTTP, from `pacto mcp -t http` or the dashboard's `/mcp`, the contract
file tools are left out, and the dashboard's `/mcp` serves no `pacto_stats`.
No server has an approve tool. Pacto skips any API operation named `pacto_*`, so the prefix always means a
Pacto tool. Inputs and outputs are in the [MCP reference](mcp-reference.md).

## Try it

```text
You:    Create a contract for a stateful Go HTTP API called user-service
        that stores data in PostgreSQL
Claude: [writes pacto.yaml with an HTTP interface and marks the service as
         keeping data that survives a restart]

You:    Add a dependency on payments-api
Claude: [calls pacto_edit with add_dependencies]

You:    Check the contract in ./user-service
Claude: user-service is valid.
```

Pacto adds a dependency only from calls it observed, which pactify does when
the [node agent](glossary.md#node-agent) runs in the cluster. The node agent
is a Pacto program on each Kubernetes node, separate from the AI agent that
calls these tools. Add any other dependency yourself, as in the second
prompt.

## If the tools do not show up

1. Run `pacto version` to check Pacto is installed and on your `PATH`.
2. Check your MCP configuration file for JSON syntax errors.
3. Ask your agent to list its tools (in Claude Code, `/mcp`).
4. Add `-v` to `args` to see what the server does while it starts, such as
   reading a bundle from a registry. It logs nothing per tool call.
