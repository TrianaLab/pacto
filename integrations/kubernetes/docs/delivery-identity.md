# Delivery: sign-in, approvals and agents

People decide in the dashboard and agents reach Pacto over the network, each
under their own Kubernetes identity. Neither the dashboard nor the MCP server
holds a right to act for anyone: they pass the caller's own token on, so the
apiserver decides what the caller may do and its audit log names the person.
There is no impersonation.

## The cluster trusts your identity provider

Approving needs the login time from your identity provider, read by the
apiserver itself. Structured authentication maps the OIDC `auth_time` claim to
`userInfo.extra["pacto.dev/auth-time"]`, but only for tokens issued to the
interactive clients, so a client-credentials or exchanged token never carries
it:

```yaml
apiVersion: apiserver.config.k8s.io/v1
kind: AuthenticationConfiguration
jwt:
- issuer: {url: https://idp.example.com, audiences: [pacto-dashboard, pacto-cli], audienceMatchPolicy: MatchAny}
  claimMappings:
    username: {expression: "'https://idp.example.com#' + claims.sub"}
    groups: {claim: groups, prefix: ""}
    extra:
    - key: pacto.dev/auth-time
      valueExpression: >-
        has(claims.auth_time) && has(claims.azp) && claims.azp in ['pacto-dashboard', 'pacto-cli']
        ? string(int(claims.auth_time)) : ''
```

The identity provider must put `azp` on interactive tokens. Dex sets it only
on a token with two audiences, so give the dashboard the extra scope
`audience:server:client_id:pacto-cli` and make `pacto-cli` trust
`pacto-dashboard`. A cluster that cannot trust your identity provider keeps the
read-only dashboard and the CLI, and nobody can approve there.

## Sign in to the dashboard

Set the sign-in on the operator chart. The client secret stays in a Secret in
the operator namespace, and TLS must end in front of the dashboard because its
session cookie is `Secure`:

```yaml
dashboard:
  auth:
    oidc:
      issuerURL: https://idp.example.com
      clientID: pacto-dashboard
      clientSecretRef: {name: dashboard-oidc, key: client-secret}
      redirectURL: https://pacto.example.com/auth/callback
      extraScopes: [audience:server:client_id:pacto-cli]
    teamGroupPrefix: ""        # the same value as the delivery chart's identities.teamGroupPrefix
  miraURL: http://mira.observability:4318
```

`pacto dashboard` takes the same settings as `--oidc-*` flags, with the secret
in `PACTO_DASHBOARD_OIDC_CLIENT_SECRET`. The dashboard's ServiceAccount stays
read-only: it gains nothing in delivery and cannot impersonate.

Without sign-in, a dashboard bound beyond loopback shows no delivery data and
offers no actions. On loopback it acts as your kubeconfig user, like
`pacto delivery`, and prints a URL with a launch token that its actions need.
It never decides: any process on your machine could do it as you, so it shows
the `pacto delivery approve` command instead.

## Follow delivery in the dashboard

The dashboard shows a **Delivery** tab only where the cluster serves the
delivery API and the dashboard knows who you are. It has four views:

- **Inbox:** the changes waiting for your teams, with the question asked and
  the services it affects, then paused environments and changes that could not
  finish.
- **Environments:** every service in every environment, the version it runs
  and whether that version is verified, failed, paused or waiting. Promote and
  unpause start here.
- **History:** past changes still kept in the cluster. Older ones are gone, so
  this is never the whole story.
- **Published:** the versions your environments know, whether their signature
  was checked and where each one runs.

A change has its own page: the plan, what Pacto predicted next to what
happened, the proof behind each check (a link into Mira when the dashboard has
`--mira-url`, otherwise the query to copy), who decided, the affected services
and a timeline. Roll back, cancel and the decision start there. Promote, roll
back and unpause first show the plan, and if anything moved since, you get the
fresh plan instead of a stale change. When the cluster refuses, the page shows its reason
as it gave it.

## Approve from the dashboard

A held change asks one question of the teams that own the affected services,
or of the team that owns the service itself. Only their members may answer:
the dashboard offers the decision to no one else and the promoter ignores an
Approval from anyone else. A contract owner team `T` is the group `T`, or
`teamGroupPrefix` followed by `T`.

To decide, the person signs in again: the dashboard sends them to the identity
provider with `prompt=login` when their login is older than ten minutes, and
the promoter ignores an approval made more than fifteen minutes after its
login. After that login the decision is shown again and the person clicks once
more; it is never sent on its own. Agents are refused, and so is a login with no
login time.

## Agents over the network

```bash
PACTO_MCP_EXCHANGE_CLIENT_SECRET=... pacto mcp --delivery -t http --host 0.0.0.0 \
  --auth-issuer https://idp.example.com --resource https://pacto-mcp.example.com/mcp \
  --exchange-client-id pacto-mcp
```

Every request must carry a bearer token for the resource, and the server
publishes where to get one at `/.well-known/oauth-protected-resource`. The
server exchanges each caller's token at the identity provider (RFC 8693) for
one the apiserver accepts, as its own OAuth client, and asks the apiserver who
the caller is on every request. An agent gets propose and cancel, a person the
read tools and nobody an approve tool. Put agent clients in `pacto:agents` (or
your agent group). Without token exchange the server refuses `--delivery` over
HTTP.

## What this does not stop

- Someone holding a person's live session, refresh token or password is that
  person to the cluster.
- A stolen dashboard session can approve only in the ten minutes after that
  person signed in again; it can take the person's other actions until it
  ends.
- Sessions live in the dashboard's memory: a restart signs everyone out, and
  the dashboard runs as one replica.
