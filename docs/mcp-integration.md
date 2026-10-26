# Drive Pacto from an agent

This page is for an engineer who wants an AI agent, such as Claude, Cursor
or Copilot, to use Pacto. It covers how to connect the agent, what it can and
cannot do, and the limits that keep it safe. Every tool and input is listed
in the [MCP reference](mcp-reference.md).

## The words on this page

| Word | Meaning |
|---|---|
| Pacto | A tool that describes each of your services in a contract and, with delivery turned on, decides which version runs in each environment |
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

How you start the server decides what the agent can reach. Pick one; the
flags cannot be combined.

| Start it with | The agent can | It can change |
|---|---|---|
| `pacto mcp` | Write and check contracts in local folders | Contract files on disk |
| `pacto mcp --root oci://...` | Read published contracts and what they depend on | Nothing |
| `pacto mcp --fleet` | Everything the default does, plus answer questions about your fleet | Contract files on disk |
| `pacto mcp ./my-service --base-url https://...` | Everything the default does, plus call the service's own API, one tool per operation | Contract files; the live service only with `--allow-writes` |
| `pacto mcp --publish` | Everything the default does, plus publish a version to your registry | Contract files and your registry |
| `pacto mcp --delivery` | Read delivery state and, if the cluster counts it as an agent, ask for or cancel a change | Only its own requests; it can never approve |

If the agent must change nothing at all, use `--root`.

## What keeps it safe

- **An agent never approves.** In delivery mode it can ask for a change, but
  Pacto's checks and the owning team decide. There is no approve tool, and
  the cluster refuses an approval from an agent anyway. Run the agent with
  its own Kubernetes identity, never your login: an agent holding your
  credentials is you to the cluster.
- **Calls to your service are read-only by default.** Only `GET` and `HEAD`
  operations become tools unless you pass `--allow-writes`. Each call stops
  after 30 seconds and follows no redirect.
- **Tools carry no safety labels.** Pacto does not mark its tools as
  read-only or destructive in MCP, so build allow-lists by name. Tools named
  `pacto_*` are Pacto's own: `pacto_create`, `pacto_edit` and `pacto_publish`
  write, and the rest read. Tools without the `pacto_` prefix call your
  service.
- **A missing answer is not proof.** Fleet answers carry `asOf` and
  `completeness`. When a source was unreachable the answer says `partial`,
  and "not found" then means "not known", not "does not exist".
- **Answers are frozen at start.** `--root`, `--fleet` and a bundle are read
  once when the server starts. Restart it to see newer services or versions.
- **Over the network, callers must sign in.** `pacto mcp -t http` listens only
  on this machine by default. To listen beyond it you must add
  `--auth-issuer` and `--resource`, so every request carries a token the
  server checks, and the server then refuses `--auth` and `--allow-writes`.
  See [agents over the network](integrations/kubernetes/delivery-identity.md#agents-over-the-network).

## Try it

```text
You:    Create a pacto contract for a stateful Go HTTP API called user-service
        that stores data in PostgreSQL
Claude: [writes pacto.yaml with an HTTP interface and persistent state]

You:    Add a dependency on payments-api
Claude: [calls pacto_edit with add_dependencies]

You:    Check the contract in ./payments-api
Claude: payments-api is valid. Suggestion: "No dependencies declared. If this
        service depends on others, declare them explicitly."
```

Pacto never guesses which services yours calls, so you add them yourself, as
in the second prompt.

## If the tools do not show up

1. Run `pacto version` to check Pacto is installed and on your `PATH`.
2. Check your MCP configuration file for JSON syntax errors.
3. Ask your agent to list its tools (in Claude Code, `/mcp`).
4. Add `-v` to `args` to see what the server does while it starts, such as
   reading a bundle from a registry. It logs nothing per tool call.
