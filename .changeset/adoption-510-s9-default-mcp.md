---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

The operator-managed dashboard now serves MCP to AI agents at `/mcp` by default, an upgrade with `--reuse-values` included, in the same process and behind the same Service with its sign-in; set `dashboard.mcp.enabled=false` to turn it off, and `profile: hardened` has no managed dashboard and so no endpoint. New chart values `dashboard.mcp.enabled`, `dashboard.mcp.url` and `dashboard.mcp.exchangeAudience`, operator flags `--dashboard-mcp`, `--dashboard-mcp-url` and `--dashboard-mcp-exchange-audience`, and `pacto dashboard --mcp`, `--mcp-url` and `--mcp-exchange-audience`. The install notes print an MCP client snippet with a `pacto-viewer` token. A ServiceAccount token minted for `pacto-dashboard` reads the fleet only and `pacto_capabilities` says so with a hint; one also minted for the cluster's audience proposes and cancels delivery operations as that ServiceAccount; a person's own token reads as them; with an exchange audience an issuer token is exchanged. Nobody gets an approve tool.
