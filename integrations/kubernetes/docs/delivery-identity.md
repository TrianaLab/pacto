# Delivery reference: sign-in, approvals and agents

People decide in the dashboard and agents reach Pacto over the network, each
under their own Kubernetes identity. The dashboard and the MCP server pass the
caller's own token on, so the apiserver decides what the caller may do and its
audit log names the person. There is no impersonation.

## The cluster trusts your identity provider

Approving needs the login time, read by the apiserver itself. Structured
authentication maps the OIDC `auth_time` claim to
`userInfo.extra["delivery.pacto.trianalab.io/auth-time"]`, only for tokens issued to the two
clients that make a person log in again: the dashboard and `pacto delivery
approve`'s own client, `pacto-approve`. A client-credentials or exchanged
token never has it, and neither does a kubeconfig login (`pacto-cli`): any
process in your shell, an agent included, can reuse that one.

```yaml
apiVersion: apiserver.config.k8s.io/v1
kind: AuthenticationConfiguration
jwt:
- issuer: {url: https://idp.example.com, audiences: [pacto-dashboard, pacto-cli], audienceMatchPolicy: MatchAny}
  claimMappings:
    username: {expression: "'https://idp.example.com#' + claims.sub"}
    groups: {claim: groups, prefix: ""}
    extra:
    - key: delivery.pacto.trianalab.io/auth-time
      valueExpression: >-
        has(claims.auth_time) && has(claims.azp) && claims.azp in ['pacto-dashboard', 'pacto-approve']
        ? string(int(claims.auth_time)) : ''
```

The identity provider must put `azp` on these tokens. Dex sets it only on a
token with two audiences, so give the dashboard and `pacto delivery approve`
the extra scope `audience:server:client_id:pacto-cli` and make `pacto-cli`
trust `pacto-dashboard` and `pacto-approve`. Register `pacto-approve` as a
public client (PKCE, no secret) whose redirect is the loopback address
`http://127.0.0.1:<port>/callback`, and use it for nothing else. A cluster that
cannot trust your identity provider keeps the read-only dashboard and the CLI,
and nobody can approve there.

## Where approving works

The apiserver must load this file (flag `--authentication-config`), and its
`claimMappings` must be able to set `extra`. That is in your hands wherever you
set the apiserver's flags, as with kind, kubeadm or k3s; Pacto's own tests run
it on kind. Managed clusters mostly decide it for you:

| Cluster | Can it map `delivery.pacto.trianalab.io/auth-time`? |
|---|---|
| kind, kubeadm, k3s and others where you set apiserver flags | Yes, tested on kind |
| AKS | Not verified: structured authentication is in preview |
| EKS, GKE | Not verified |

To check a cluster, sign in again and run `kubectl auth whoami -o yaml`. If
`extra` holds no `delivery.pacto.trianalab.io/auth-time`, nobody can approve there. Delivery
still promotes every change that needs no decision, people can still freeze,
unfreeze, roll back and cancel, and the dashboard and the CLI still read
everything. A held change then ends one of three ways: it expires, someone
cancels it and publishes a version that asks nothing, or a member of the
break-glass group raises an override with
`pacto delivery propose --type override --to sha256:...`. An override moves the version at once with no checks, is kept
in the history like any change and freezes the Stage until a person unfreezes
it.

## Who counts as a person, an agent or a team

Set the groups once, on the delivery chart:

```yaml
identities:
  agentGroups: [pacto:agents]          # agents propose, never approve or unfreeze
  breakGlassGroups: [pacto:break-glass]
  teamGroupPrefix: ""                  # owner team T is group T, or this prefix plus T
  requireOwnerTeam: true               # a question naming no team is answered by nobody
  humanGroups: []                      # when set, everyone outside these groups is an agent
```

The chart renders the admission policies from these values and publishes them
in the ConfigMap `pacto-delivery-identities`, which every signed-in user may
read. The promoter mounts that ConfigMap and runs with it, and restarts when it
changes. `pacto delivery`, `pacto mcp --delivery` and the dashboard read it
too, from `--delivery-namespace` (default `pacto-delivery`), so what they
offer matches what admission and the promoter decide. A client that cannot read
it falls back to the defaults above; admission and the promoter still decide.

A held question goes to the teams that own the affected services. When neither
the service nor its consumers declare an owner, nobody may answer it with
`requireOwnerTeam: true`, and only break-glass can override. The plan says so
in its question. Set it to `false` to let any person answer instead.

Service accounts are always agents, but a person-like agent identity is an
agent only while it is in an agent group. Left out by mistake, it counts as a
person: it is still refused an approval, because its token carries no login
time, but records name it a person. Set `humanGroups` to the groups your people
are in, and anyone outside them is an agent.

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
    required: true             # sign-in for the fleet views and the whole API too, not only delivery
    deliveryNamespace: ""      # where the delivery chart publishes its identity policy; empty means pacto-delivery
  miraURL: http://mira.observability:4318
```

Without `required`, anyone who reaches the dashboard sees the fleet views; only
delivery asks for sign-in. `pacto dashboard` takes the same settings as
`--oidc-*` flags and `--auth-required`, with the secret in
`PACTO_DASHBOARD_OIDC_CLIENT_SECRET`. The dashboard's ServiceAccount stays
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
`teamGroupPrefix` followed by `T`. The promoter ignores an approval given
more than its login freshness (`promoter.authFreshness`, fifteen minutes by
default) after its author's login, and every Stage publishes that freshness in
`status.authFreshness`. To decide, a person whose login is older than two
thirds of it (ten of fifteen minutes by default) signs in again
(`prompt=login`). The decision takes one more click, never sent on its own.
The dashboard and `pacto delivery approve` check a decision with the
promoter's own rule before they record it, and refuse one it would not count,
saying why. Agents are refused, and so is a token with no login time: on a
cluster that reports none, the dashboard says approving is not possible there
and offers no decision.

## Approve from the terminal

```bash
export PACTO_DELIVERY_OIDC_ISSUER_URL=https://idp.example.com
pacto delivery approve -n prod <operation> --decision approve \
  --oidc-extra-scopes audience:server:client_id:pacto-cli --oidc-redirect-port 8001
```

The command never sends your kubeconfig's credentials. It prints an address
with `prompt=login` and `max_age=0`, waits up to ten minutes on the loopback
port for you to log in again in your browser, refuses a token whose
`auth_time` is older than the command, and sends the Approval with that token
alone. The token is not stored. `--oidc-client-id` defaults to
`pacto-approve`; a confidential client's secret comes from
`PACTO_DELIVERY_OIDC_CLIENT_SECRET`. Without `--oidc-redirect-port` it picks a
free port, which suits a provider that allows any loopback port (RFC 8252).

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
your agent group), or set `humanGroups`. Without token exchange the server refuses `--delivery` over
HTTP.

Both servers log every refused token, sign-in and signed-in request as a
warning with the issuer, audience or client, the reason and where it came from
(`X-Request-Id` too, when a proxy sets it), never the token. With `--verbose`
the MCP server also logs each caller's class and whether it got the write
tools.

## Limits

- An agent holding a person's credentials, session or refresh token is that
  person to the cluster. Fresh sign-in raises the bar; it does not close the
  gap. `pacto delivery approve` asks for the login with `prompt=login`, but
  the cluster cannot see what a login asked for: a process that drives your
  browser could run the `pacto-approve` login itself and ride your identity
  provider's session, and it gets a login time only as old as your last real
  login there. An identity provider that ignores `prompt=login` widens this.
- A stolen dashboard session can approve only in the first two thirds of the
  promoter's login freshness (`promoter.authFreshness`, ten of fifteen minutes
  by default) after that person signed in again, and can take their other
  actions until it ends.
- Sessions live in the dashboard's memory: it runs as one replica and a restart
  signs everyone out. A session ends after an hour idle or twelve in all.
- A change's timeline shows only the Operation's own times, not Kubernetes
  events. Published versions are those your Stages know, and refused publishes
  are listed as the registry holds them, without checking their signatures.
