# Delivery reference: agents

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

An agent is a service account, any other `system:` user or an identity in an
agent group
([who counts as a person, an agent or a team](delivery-identity.md#who-counts-as-a-person-an-agent-or-a-team)).
Agents plan, propose and cancel changes under their own identity. People
approve them in the dashboard or from the terminal
([approvals](delivery-identity.md)).

## An agent names the person it acts for

When an agent proposes a change, it names the person who asked for it in
`onBehalfOf`: `--on-behalf-of` for `pacto delivery propose`, `on_behalf_of` for
`pacto_delivery_operation_propose` and the "Who asked you for this" field in the
dashboard. The name is the person's Kubernetes username, any OIDC prefix included, as
`kubectl auth whoami` prints it for them: a Git login never matches their
approval. The cluster's admission policy and the promoter both refuse an
agent's Operation that names nobody, the agent itself, a service account, a
system user or the promoter. The client refuses it first, before anything reaches the
cluster:

```text
refused: an agent (system:serviceaccount:ci:deploy-bot) proposed this change; an agent must name the person it acts for (onBehalfOf); that person may not approve this change
  fix: pass the person who asked for it with --on-behalf-of (MCP on_behalf_of)
```

Planning needs no person, so an agent can always preview a change, an undo or
its progress. Changes the promoter raises itself, such as an automatic
rollback, a freeze or a step-down, need none either.

The person an agent names may not approve that change, and neither may the
agent. The name is asserted, never verified: Pacto records it in the Operation
and its signed record (`for:       dana@example.com (asserted)` in
`pacto delivery blame` and `for dana@example.com (asserted)` in
`pacto delivery record get`), so a reviewer reads who the agent says it acts for.
A person who controls an agent's credentials can still name someone else and
then approve the change as themselves. Where that matters, run every agent
under its own identity and ask for two distinct approvers.

An approval of an agent's change that names nobody, such as one proposed
before this rule existed, is refused: propose it again with `onBehalfOf`.

### Where agents cannot approve

- The MCP server has no approve tool for anyone.
- `pacto delivery approve` and the dashboard refuse a caller in the agent class.
- The promoter ignores an approval whose author is an agent.
- An agent that holds a person's kubeconfig, session or refresh token is that
  person to the cluster, and none of the above applies
  ([limits](delivery-identity.md#limits)).

## Delegate an operational task

To hand an agent a task, such as "promote checkout 2.3.0 to production", run
the agent under its own identity and have it name you. It plans, then proposes
as itself with you in `onBehalfOf`, and ties the change to your ticket:

```bash
pacto delivery propose -n prod --stage checkout --type promote --to 2.3.0 \
  --on-behalf-of alice@example.com --correlation-id CHG-2041 \
  --reason "release 2.3.0" --intent-digest <digest from the plan>
```

The Operation and its record are the whole trail of the task. When the plan
needs a person, the change is held in the **Inbox** of the team the approval
floor names: the team that owns the service, or for a
breaking change the consumer that would break. `pacto delivery inbox` and
`pacto_delivery_inbox` list it there. A person in that team other than you
decides it, because neither you nor the agent may approve it. The record names
the agent as the actor and you as the person it said it acts for.

## Agents over the network

```bash
PACTO_MCP_EXCHANGE_CLIENT_SECRET=... pacto mcp --delivery -t http --host 0.0.0.0 \
  --auth-issuer https://idp.example.com --resource https://pacto-mcp.example.com/mcp \
  --exchange-client-id pacto-mcp
```

Every request must carry a bearer token for the resource; the server publishes
where to get one at `/.well-known/oauth-protected-resource`. It exchanges each
caller's token at the identity provider (RFC 8693) for one the apiserver
accepts and asks the apiserver who the caller is on every request. Everyone
gets the read tools, an agent also propose and cancel, nobody an approve tool. Put agent clients in `pacto:agents` (or
your agent group): signed in through OIDC, they otherwise count as people. Without token exchange the server refuses `--delivery` over
HTTP. With `--fleet` it needs token exchange too, names each caller the same
way and answers [as the dashboard does](dashboard-access.md). The exchanged
token is the agent's own, so `on_behalf_of` still comes from the tool's
arguments. One server serves both:

```bash
PACTO_MCP_EXCHANGE_CLIENT_SECRET=... pacto mcp --fleet --k8s --delivery -t http \
  --host 0.0.0.0 --auth-issuer https://idp.example.com \
  --resource https://pacto-mcp.example.com/mcp --exchange-client-id pacto-mcp
```

Both servers log every refused token, sign-in and signed-in request as a
warning with the issuer, audience or client, the reason and where it came from
(`X-Request-Id` too, when a proxy sets it), never the token. With `--verbose`
the MCP server also logs each caller's class and whether it got the write
tools, and a [line per tool call](../../mcp-reference.md#tool-call-log).
