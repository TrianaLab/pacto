# Delivery reference: approvals

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

People decide in the dashboard and agents reach Pacto over the network, each
under their own Kubernetes identity
([agents](delivery-agents.md)). The dashboard and the MCP server pass the
caller's own token on, so the apiserver decides what the caller may do and its
audit log names the person. There is no impersonation.

## The cluster trusts your identity provider

A person decides with the sign-in they have. If you also ask for
[a recent sign-in to decide](#a-recent-sign-in-to-decide), approving needs the
login time, read by the apiserver itself. Structured authentication maps the OIDC `auth_time` claim to
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
`http://127.0.0.1:<port>/callback`, and use it for nothing else. Without that
trust, people decide from the terminal with their own kubeconfig.

## Where approving works

Only a recent sign-in needs the login time. The apiserver must load this file (flag `--authentication-config`), and its
`claimMappings` must be able to set `extra`. That is in your hands wherever you
set the apiserver's flags, as with kind, kubeadm or k3s; Pacto's own tests run
it on kind. Managed clusters mostly decide it for you:

| Cluster | Can it map `delivery.pacto.trianalab.io/auth-time`? |
|---|---|
| kind, kubeadm, k3s and others where you set apiserver flags | Yes, tested on kind |
| AKS | Not verified: structured authentication is in preview |
| EKS, GKE | Not verified |

To check a cluster, sign in again and run `kubectl auth whoami -o yaml`. If
`extra` holds no `delivery.pacto.trianalab.io/auth-time`, nobody can approve
there with a recent sign-in required. Delivery
still promotes every change that needs no decision. People can still freeze,
unfreeze, roll back and cancel. The dashboard and the CLI still read
everything. [What you need to be allowed](../../pactify-decide.md#what-you-need-to-be-allowed)
lists the three ways a held change still ends.

## Who counts as a person, an agent or a team

Delivery tells people from agents by how they sign in. A ServiceAccount, any
other `system:` user and an unauthenticated request are agents. Every other
signed-in identity, such as an OIDC or client certificate user, is a person.
These values, all optional, mark automation and narrow who may answer:

```yaml
delivery:
  identities:
    agentGroups: [pacto:agents]          # agents propose, never approve or unfreeze
    breakGlassGroups: [pacto:break-glass]
    teamGroupPrefix: null                # set it, even to "", to let only owner teams answer
    requireOwnerTeam: false              # true: a question naming no team is answered by nobody
```

The chart renders the admission policies from these values and publishes them
in the ConfigMap `pacto-delivery-identities` in `delivery.namespace`, which
every signed-in user may read. The promoter runs with that ConfigMap and restarts when it
changes. `pacto delivery`, `pacto mcp --delivery` and the dashboard read it
too, from `--delivery-namespace` (default `pacto-delivery`; the chart points
its own dashboard at `delivery.namespace`), so what they offer matches what admission and the promoter decide.

### When a client cannot read the policy

The client still classifies you with the defaults above for display, but its
status, offers, undo offers and "why not yet" fixes then offer nothing and
name the error. When the ConfigMap is not found, the error suggests passing
`--delivery-namespace` with the namespace the chart's `delivery.namespace`
names. Admission and the promoter still decide.

### Owner teams and agent groups

A held question goes to the teams that own the affected services. Unless
`teamGroupPrefix` is set, that only routes it, and whoever
[may decide](#who-may-decide) answers. Once set, only owner team members
answer: team `T` is the group `T` with `""`, or the prefix followed by `T`. A
`system:` group is never a team. With `requireOwnerTeam: true`, a question
whose services declare no owner is answered by nobody but break-glass, as its
plan says.

Automation that signs in through OIDC counts as a person until you put it in
`agentGroups`. So does a bot that completes a `pacto-approve` login with
stored credentials: the public client gives it a login time. `humanGroups` is
deprecated: when set, anyone outside its groups is an agent.

## Who may decide

A person may decide a held change when they may update the Stage's
workload. When it counts an Approval, the promoter asks the apiserver a
SubjectAccessReview with the Approval's user and groups, such as
`update deployments/checkout in payments`. The same review decides a person's
unfreeze and acknowledge. With delivery on, the chart aggregates delivery
reads into the built-in `view`, `edit` and `admin` roles, and creating
Approvals and Operations into `edit` and `admin`, so `edit` or `admin` in the
namespace is enough. A custom role without `edit` also needs the
`pacto-operator-delivery-approver` ClusterRole.

Agents never decide. Without update rights the refusal is
`CannotUpdateWorkload`. A review that fails, or a workload kind with no known
resource, is `ApproverUnverified`, never allowed.

Records keep how each actor was classified and, for each approval, the access
phrase, which review answered it and when. Older records show "not recorded".

## Upgrading

To keep the earlier team rules, set both values before you upgrade:

```yaml
delivery:
  identities:
    teamGroupPrefix: ""
    requireOwnerTeam: true
```

- An upgrade that takes the new chart's defaults turns team narrowing off
  unless you set `teamGroupPrefix` yourself, and the ownerless protection
  unless you set `requireOwnerTeam`: a values file through Argo CD or a Flux
  HelmRelease, `helm upgrade -f` and `--reset-then-reuse-values`. The
  promoter then emits a `TeamNarrowingOff` Event on each Operation it holds.
- An upgrade that reuses the earlier release's values whole keeps the earlier
  defaults, and so keeps narrowing.
- Approvers need `update` on the Stage's workload, with or without narrowing.
- `allowEmptyHumanGroups` is ignored. Other `system:` users are agents.
- Records gain `actor.classifiedBy` and, on each approval, `classifiedBy`,
  `access`, `accessSource` and `accessAt`. Signed records are read strictly,
  so upgrade a promoter that follows another cluster's records
  (`upstreamTrust`) no later than the recording cluster.

## The dashboard's delivery settings

The delivery views need [dashboard sign-in](dashboard-authentication.md). For
approvals, add the extra scope `audience:server:client_id:pacto-cli` (see
above). The delivery settings sit under `dashboard.delivery`:

```yaml
dashboard:
  auth:
    oidc:
      extraScopes: [audience:server:client_id:pacto-cli]
  delivery:
    namespace: ""      # where the identity policy is; empty means delivery.namespace when delivery.enabled
    trust: {configMap: delivery-trust, key: trust.yaml}   # verifies History
    miraURL: http://mira.observability:4318
```

`trust` is the trust config the CLI reads
([trust and key rotation](delivery-record-keys.md#trust-and-key-rotation)).
The dashboard's ServiceAccount gains nothing in delivery and cannot
impersonate. A dashboard on loopback without sign-in shows the
`pacto delivery approve` command instead of deciding: any process on your
machine could decide as you.

## Follow delivery in the dashboard

The **Delivery** tab ([what it shows](delivery.md#where-you-follow-it))
appears only where the cluster serves the delivery API and the dashboard holds
your own credentials. Every action shows the plan first, and the fresh plan if anything
moved since. When you may not list delivery objects across the cluster, the
views say they may be incomplete.

## Approve from the dashboard

A held change asks one question of the teams that own the affected services.
Whoever [may decide](#who-may-decide) answers it, and only owner team members
when team narrowing is on. A person decides with the sign-in they
have. The dashboard and `pacto delivery approve` check a decision with the
promoter's own rule, asking with your own credentials, before they record it, and refuse one it would not count,
saying why. Agents are refused, ServiceAccount tokens included; a person's token decides where [its login time](#a-recent-sign-in-to-decide) allows.

### A recent sign-in to decide

Set `delivery.promoter.authFreshness` to a duration such as `15m` to make
deciding need a recent sign-in. The promoter then ignores an approval given
more than that long after its author's login, and every Stage publishes the
setting in `status.authFreshness` for the dashboard and `pacto delivery
approve` to follow. Once a login is older than two thirds of it, the dashboard
says so next to the decision buttons, and a decision opens a dialog that sends
the person to sign in again (`prompt=login`), keeping their choice and note
for one more click. A token with no login time cannot approve.

## Approve from the terminal

```bash
pacto delivery approve -n prod <operation> --decision approve
```

It decides as your kubeconfig signs you in. Where the Stage asks for
[a recent sign-in](#a-recent-sign-in-to-decide) or cannot be read, it never
sends your kubeconfig's credentials:

```bash
export PACTO_DELIVERY_OIDC_ISSUER_URL=https://idp.example.com
pacto delivery approve -n prod <operation> --decision approve \
  --oidc-extra-scopes audience:server:client_id:pacto-cli --oidc-redirect-port 8001
```

It prints an address with `prompt=login` and `max_age=0`, waits up to ten minutes on the loopback
port for you to log in again in your browser, refuses a token whose
`auth_time` is older than the command, and sends the Approval with that token
alone. The token is not stored. `--oidc-client-id` defaults to
`pacto-approve`; a confidential client's secret comes from
`PACTO_DELIVERY_OIDC_CLIENT_SECRET`. Without `--oidc-redirect-port` it picks a
free port, which suits a provider that allows any loopback port (RFC 8252).

## Limits

- An agent holding a person's credentials, session or refresh token is that
  person to the cluster, and by default it can approve as them. A recent
  sign-in raises the bar; it does not close the gap. `pacto delivery approve` asks for the login with `prompt=login`, but
  the cluster cannot see what a login asked for: a process that drives your
  browser could run the `pacto-approve` login itself and ride your identity
  provider's session, and it gets a login time only as old as your last real
  login there. An identity provider that ignores `prompt=login` widens this.
- Run agents under their own identity. An agent that runs `pacto` with a
  person's kubeconfig is that person: it can approve as them, and it needs no
  `--on-behalf-of`. Set `delivery.promoter.authFreshness` so that an approval
  needs a recent sign-in, which a kubeconfig login never carries.
- A stolen dashboard session can take that person's actions until it ends. With
  a recent sign-in required, it approves only in the first two thirds of
  `delivery.promoter.authFreshness` after they signed in again.
- The SubjectAccessReview names the Approval's user and groups, not its
  `extra`. An authorizer that narrows by `extra`, such as token scopes,
  answers for the user without them.
- Sessions live in the dashboard's memory: it runs as one replica and a restart
  signs everyone out ([sessions](dashboard-authentication.md#sessions)).
- A change's timeline shows only the Operation's own times, not Kubernetes
  events. Published versions are those your Stages know, and refused publishes
  are listed as the registry holds them, without checking their signatures.
