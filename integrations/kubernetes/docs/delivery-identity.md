# Delivery reference: sign-in, approvals and agents

People decide in the dashboard and agents reach Pacto over the network, each
under their own Kubernetes identity. The dashboard and the MCP server pass the
caller's own token on, so the apiserver decides what the caller may do and its
audit log names the person. There is no impersonation.

## The cluster trusts your identity provider

Approving needs the login time, read by the apiserver itself. Structured
authentication maps the OIDC `auth_time` claim to
`userInfo.extra["pacto.dev/auth-time"]`, only for tokens issued to the
interactive clients, so a client-credentials or exchanged token never has it:

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
`pacto-dashboard`.

## Where approving works

The apiserver must load this file (flag `--authentication-config`), and its
`claimMappings` must be able to set `extra`. That is in your hands wherever you
set the apiserver's flags, as with kind, kubeadm or k3s; Pacto's own tests run
it on kind. Managed clusters mostly decide it for you:

| Cluster | Can it map `pacto.dev/auth-time`? |
|---|---|
| kind, kubeadm, k3s and others where you set apiserver flags | Yes, tested on kind |
| AKS | Not verified: structured authentication is in preview |
| EKS, GKE | Not verified |

To check a cluster, sign in again and run `kubectl auth whoami -o yaml`. If
`extra` holds no `pacto.dev/auth-time`, nobody can approve there. Delivery
still promotes every change that needs no decision, people can still freeze,
unfreeze, roll back and cancel, and the dashboard and the CLI still read
everything. A held change then ends one of three ways: it expires, someone
cancels it and publishes a version that asks nothing, or a member of the
break-glass group raises an override with
`pacto delivery propose --type override --to sha256:...`. An override moves the version at once with no checks, is kept
in the history like any change and freezes the Stage until a person unfreezes
it.

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
    agentGroups: []            # the same as identities.agentGroups; empty means pacto:agents
    breakGlassGroups: []       # the same as identities.breakGlassGroups; empty means pacto:break-glass
  miraURL: http://mira.observability:4318
```

`pacto dashboard` takes the same settings as `--oidc-*` flags, with the secret
in `PACTO_DASHBOARD_OIDC_CLIENT_SECRET`. The dashboard's ServiceAccount stays
read-only: it gains nothing in delivery and cannot impersonate.

Without sign-in, a dashboard bound beyond loopback shows no delivery data and
offers no actions. On loopback it acts as your kubeconfig user and prints a URL
with a launch token that its delivery reads and actions need. It never decides there, because any
process on your machine could do it as you; it shows the
`pacto delivery approve` command instead.

## Follow delivery in the dashboard

The **Delivery** tab ([what it shows](delivery.md#where-you-follow-it))
appears only where the cluster serves the delivery API and the dashboard knows
who you are. Every action shows the plan first, and the fresh plan if anything
moved since. When you may not list delivery objects across the cluster, the
views say they may be incomplete.

## Approve from the dashboard

A held change asks one question of the teams that own the affected services.
Only their members may answer. A contract owner team `T` is the group `T`, or
`teamGroupPrefix` followed by `T`. To decide, a person whose login is older
than ten minutes signs in again (`prompt=login`), and the promoter ignores an
approval more than fifteen minutes after its login. The decision takes one more
click, never sent on its own. Agents are refused, and so is a token with no
login time.

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
your agent group). Without token exchange the server refuses `--delivery` over
HTTP.

## Limits

- An agent holding a person's credentials, session or refresh token is that
  person to the cluster. Fresh sign-in raises the bar; it does not close the
  gap.
- A stolen dashboard session can approve only in the ten minutes after that
  person signed in again, and can take their other actions until it ends.
- Sessions live in the dashboard's memory: it runs as one replica and a restart
  signs everyone out. A session ends after an hour idle or twelve in all.
- A change's timeline shows only the Operation's own times, not Kubernetes
  events. Published versions are those your Stages know, and refused publishes
  are listed as the registry holds them, without checking their signatures.
