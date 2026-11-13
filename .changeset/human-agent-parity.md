---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

People and agents now see the same organization and meet the same policy on
every surface.

Changes that callers notice:

- An MCP server over HTTP serves only `pacto_schema` of the contract tools.
  `pacto_create`, `pacto_edit` and `pacto_check` read and write the server's
  folders and follow refs with its credentials, so they run beside the agent
  with `pacto mcp` over stdio.
- A remote `pacto mcp --fleet` with `--auth-issuer` needs token exchange
  (`--exchange-client-id`): it asks the API server who each caller is, the way
  remote delivery does, in place of reading the token's claims.
- `pacto mcp -t http` on loopback without `--auth-issuer` prints a bearer at
  launch (`pmcp_...`), and every request must carry it.
- `pacto dashboard` bound beyond loopback without sign-in refuses to start
  unless `--allow-anonymous` (`PACTO_DASHBOARD_ALLOW_ANONYMOUS=true`) allows
  it. The operator passes `--dashboard-allow-anonymous` from the new chart
  value `dashboard.auth.allowAnonymous`, `true` by default with a warning in
  the install notes, so existing installs keep starting.
- A bundle operation whose name starts with `pacto_` is skipped, so a bundle
  never takes a Pacto tool's name.
- `pacto_impact` over HTTP, and the impact view of a dashboard that is not on
  loopback, accept only refs of revisions in the caller's view and read each
  at its digest-pinned ref.

Additions:

- Every MCP tool carries annotations (`readOnlyHint`, `destructiveHint`,
  `idempotentHint`, `openWorldHint`); a generated tool takes them from its
  HTTP method. `--allow-writes` and the stdio-only rules still decide what
  runs.
- A bundle, `--fleet` and `--delivery` combine in one `pacto mcp` process,
  whose instructions describe only the modes it serves. `pacto_capabilities`
  reports the modes, the contract tools, the caller and how Pacto knows them,
  whether writes are exposed and the transport.
- Six delivery read tools answer what the dashboard's delivery views show,
  with the caller's identity: `pacto_delivery_caller`, `pacto_delivery_inbox`,
  `pacto_delivery_environments`, `pacto_delivery_autonomy`,
  `pacto_delivery_published` and `pacto_delivery_refusals`. The CLI gains
  `pacto delivery inbox`, `environments`, `autonomy`, `published` and
  `refusals` over the same route code.
- `pacto_fleet_revision_document` reads one document of a revision's bundle,
  as the dashboard's revision page does.
- With `--verbose`, every MCP tool call logs one `mcp tool call` line with the
  tool, the caller and how Pacto knows them, the outcome, the duration and a
  keyed digest of the arguments.
- Generated tool calls send `User-Agent: pacto-mcp/<version>`.
- [Fleet views](https://pacto.run/latest/fleet-tools/) maps each question to
  its command, MCP tool and dashboard route.
