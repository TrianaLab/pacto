---
"@pacto/core": minor
---

People follow and decide on delivery in the dashboard, and agents reach Pacto
delivery over the network, each under their own Kubernetes identity. Nothing
acts for anyone: the dashboard and the MCP server pass the caller's own token
on, so the apiserver decides and its audit log names the caller.

`pacto dashboard` signs people in with OpenID Connect (`--oidc-issuer-url`,
`--oidc-client-id`, `--oidc-redirect-url`, `--oidc-extra-scopes`, the secret in
`PACTO_DASHBOARD_OIDC_CLIENT_SECRET`), with PKCE, no offline access and a
server-side session behind a `__Host-` cookie. The new `/api/delivery/*`
endpoints (inbox, environments, published versions, history, one change, plan,
propose, decide and cancel) and `/api/session` run with the signed-in person's
token. A decision needs a login the person repeated within ten minutes, comes
only from a member of a team the question names and is never taken from an
agent. On loopback without sign-in the dashboard acts as the kubeconfig user,
needs the launch token it prints for its actions and never decides; bound
anywhere else without sign-in it shows no delivery data. Every answer refuses
framing, and a signed-in request that changes something needs the CSRF header
and the dashboard's own origin. `/api/capabilities` gains `delivery` and
`auth`.

The dashboard gains a Delivery tab where the cluster serves delivery: an inbox
of what waits on your teams, an environments board, a page per change (the
plan, predicted next to actual verdicts with their proof and Mira links, who
decided, the affected services and a timeline), history and published
versions. Promote, roll back, unpause, cancel and the decisions run from there.
A decision that needs a fresh login keeps your choice while you sign in again
and shows it back to you; it is never sent on its own.

`pacto mcp -t http` gains `--host`, `--auth-issuer`, `--resource` and
`--audience`: every request then carries a bearer token the server verifies,
and the server publishes protected-resource metadata. Binding beyond loopback
needs them. `--delivery` over HTTP exchanges each request's token at the
identity provider (`--exchange-client-id`, the secret in
`PACTO_MCP_EXCHANGE_CLIENT_SECRET`) for one the apiserver accepts: an agent
gets propose and cancel, a person the read tools, and nobody an approve tool.
Without token exchange it is refused.

Only the team a held question names may answer it. An `Approval` records the
approver's groups, `pkg/delivery` routes a question that names no consumer's
owner to the team of the Stage's own service, and `ClassPolicy` maps a team to
a group by name or with `TeamGroupPrefix`. Adds `github.com/coreos/go-oidc/v3`.
