# Dashboard authentication

A shared dashboard signs people in before it shows anything. Every view then
follows that person's Kubernetes RBAC and every action runs as them:

- **The fleet and the delivery lists come from shared caches, filtered by
  your RBAC.** The dashboard keeps one copy of the fleet and one of the
  delivery Stages and Operations, read and watched with its own
  ServiceAccount. Before it answers you from either, it asks the API server
  (SubjectAccessReview) whether you may make the same list or get there, so a
  namespace your RBAC denies stays out of every view, and a list across every
  namespace needs your cluster-wide permission. The caches are an index: they
  decide what you see, never what you may do, and one copy scales where a copy
  per person does not.
- **Every other read and every action uses your token.** Propose, approve,
  cancel, roll back and unfreeze go to the API server with your own token, so
  RBAC and admission decide and the audit log names you. The reads an action
  is built on, Approvals and workload access use your token too. The dashboard
  offers an action only where your RBAC allows it, with the same 10-second
  review as the reads.
- **The fleet views** show only the namespaces you may read
  ([who sees what](dashboard-access.md)). A service known only from a registry
  belongs to no namespace, so only people who may list Pactos in every
  namespace see it, and counts and totals leave it out for everyone else. A
  platform offering's provider with no Kubernetes target is the exception:
  every signed-in person sees it, with its targets hidden and its hidden
  consumers counted. A [golden path](../../golden-paths.md) is seen the same
  way, with only its revisions that declare `service.template`; the services
  created from it that you may not read are counted, never named.
- **A sign-in link reads the fleet only.** Its token is no credential for the
  API server, so the session sees the fleet views its ServiceAccount's RBAC
  allows, no Delivery tab and no action ([token sign-in](#token-sign-in)).

The dashboard's ServiceAccount stays read-only: it may get, list and watch
Stages and Operations, ask the API server what a person may read and review
whose token a sign-in link carries (TokenReview). There is no impersonation.

## How it works

1. A person opens the dashboard and sees only its sign-in screen. Every API
   endpoint and `/metrics` answer `401` with the code `LoginRequired` until
   they sign in; the page itself, `/health`, `/api/session` and `/auth/*`
   stay open.
2. They sign in with your identity provider (OpenID Connect, authorization code
   with PKCE), paste a Kubernetes token or open the sign-in link the install
   notes print.
3. The dashboard keeps the tokens in a server-side session behind an
   `HttpOnly`, `Secure` cookie. Browser JavaScript sees only a sign-in link's
   token, which the page takes out of the address bar, posts once and drops.
4. Each request uses the session's token; a sign-in link session reads only
   the fleet views. The API server says who it belongs
   to and RBAC decides, exactly as for `kubectl`. The dashboard keeps each
   answer about the delivery objects you may read for 10 seconds and each
   answer about the namespaces the fleet views show for a minute, so a removed
   RoleBinding hides its namespace within that time. Until the shared cache has loaded,
   and for a moment after you change something, the dashboard reads with your
   token instead.

On every page, the person button in the header opens who you are, your groups
and **Sign out**.
Approving a held change needs your own identity, from your identity provider
or your own token, and a recent sign-in where delivery asks for one
([a recent sign-in to decide](delivery-identity.md#a-recent-sign-in-to-decide)).
The delivery views read their own settings under `dashboard.delivery`
([the dashboard's delivery settings](delivery-identity.md#the-dashboards-delivery-settings)).

A dashboard on your own machine (`pacto dashboard` on loopback, with no
sign-in configured) acts as your kubeconfig user and shows no sign-in screen.

The managed dashboard of a namespace-list install without delivery runs with
`PACTO_DASHBOARD_REVIEW=local`. It reviews each person with a
LocalSubjectAccessReview in each watched namespace only, so it needs no
ClusterRole. In that mode a service with no namespace, such as a registry-only
service or a caller outside Kubernetes, is hidden from everyone, cluster
administrators included. A `pacto dashboard` or `pacto mcp` you run yourself
opts in with `--review=local` and `--namespace`.

## Configure the chart

```yaml
dashboard:
  auth:
    oidc:
      issuerURL: https://idp.example.com
      clientID: pacto-dashboard
      clientSecretRef: {name: dashboard-oidc, key: client-secret}
      redirectURL: https://pacto.example.com/auth/callback
      extraScopes: [offline_access]   # what your provider needs beyond openid, profile and groups
      displayName: Keycloak           # the button says "Sign in with Keycloak"; empty: "Sign in with OIDC"
      # The claim mapping: the same values as the API server's claimMappings.
      usernameClaim: sub              # empty: sub
      usernamePrefix: ""              # empty: the issuer URL and #, none for email; "-" for none
      groupsClaim: groups             # empty: groups
      groupsPrefix: ""
    session:
      idleTimeout: 1h                 # empty: 1h
      maxAge: 12h                     # empty: 12h
    tokenSignIn:
      enabled: true                   # the default: token sign-in and the sign-in link
    allowAnonymous: false             # the default
```

For an evaluation cluster, the chart can bundle Dex and wire it for you
([quickstart](quickstart.md)).

The sign-in screen's button says **Sign in with OIDC**. Set `displayName` to
the name people know the provider by and it says **Sign in with** that name
instead.

Register `https://<dashboard>/auth/callback` as the client's redirect URL and
terminate TLS in front of the dashboard. `pacto dashboard` takes the same
settings as `--oidc-*`, `--session-*` and `--token-login` flags
([CLI reference](../../cli-reference.md)), with the client secret in
`PACTO_DASHBOARD_OIDC_CLIENT_SECRET`. A remote `pacto mcp` with
`--auth-issuer` asks the API server who each caller is, as the dashboard does,
and takes the same claim mapping flags only to name a token it refuses in its
log ([who sees what](dashboard-access.md)).

## Reach the issuer

The dashboard reads the issuer's discovery document, keys and tokens itself.
Two settings cover an issuer it cannot reach the way browsers do:

```yaml
dashboard:
  auth:
    oidc:
      issuerURL: https://idp.example.com
      # Discovery, tokens and keys are read here; browsers still go to issuerURL.
      internalIssuerURL: http://dex.dex.svc:5556
      # PEM certificates of the CAs that sign the issuer's certificate.
      caBundle: {configMap: corporate-ca, key: ca.crt}   # or {secret: corporate-ca}
```

- `caBundle` names a ConfigMap or a Secret in the operator namespace whose key
  holds a PEM bundle. The dashboard trusts those CAs on top of the image's
  public roots, for an issuer whose certificate a corporate or self-signed CA
  signs.
- `internalIssuerURL` sends every request for the issuer's scheme and host to
  that address, with the issuer's path replaced by the internal URL's. The
  discovery document and every ID token must still name `issuerURL`, so the
  issuer must be configured with its public URL.

`pacto dashboard` takes them as `--oidc-ca-file` and
`--oidc-internal-issuer-url`.

## Match the API server's claim mapping

The API server decides who a token is. At sign-in and on every renewal the
dashboard asks it (SelfSubjectReview) with the person's ID token, and every
view uses the answer. The claim mapping names the person only while the API
server does not accept the ID token, for example before it trusts the issuer,
so it must equal the API server's. With the defaults above, the API server's
[structured authentication](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#using-authentication-configuration)
reads:

```yaml
apiVersion: apiserver.config.k8s.io/v1
kind: AuthenticationConfiguration
jwt:
- issuer: {url: https://idp.example.com, audiences: [pacto-dashboard]}
  claimMappings:
    username: {claim: sub, prefix: "https://idp.example.com#"}
    groups: {claim: groups, prefix: ""}
```

Then a mismatch shows as the wrong fleet: the views ask about a user the API
server never sees. To check, sign in and compare the header with `kubectl auth whoami`
run with a token from the same issuer. The dashboard drops any group starting
with `system:` from a token, as the API server does.

## Sessions

The dashboard renews the ID token with the refresh token before it expires, so
a session lasts until `idleTimeout` passes without a request or `maxAge` after
sign-in. A provider that issues refresh tokens only for `offline_access` needs
it in `extraScopes`; without a refresh token the session ends when the ID token
expires. Renewal keeps the original login time, which is what
[a recent sign-in to decide](delivery-identity.md#a-recent-sign-in-to-decide)
measures when delivery asks for one (`delivery.promoter.authFreshness`).

**Sign out** ends the session, revokes the refresh token where the provider
supports revocation and, where the provider publishes an
`end_session_endpoint`, sends the browser there to end the provider's session
too. Sessions live in the memory of one dashboard process. The operator runs
the dashboard as one replica, and a restart signs everyone out. A
`pacto dashboard` you deploy yourself with sign-in needs one replica too, or a
load balancer with session affinity (for example `sessionAffinity: ClientIP`
on its Service) so each person keeps reaching the replica that holds their
session.

## Token sign-in

Token sign-in is on by default (`dashboard.auth.tokenSignIn.enabled`; for a
`pacto dashboard` you run yourself, `--token-login`). It works with or
without `oidc`, and the sign-in screen takes two kinds of token:

- **A ServiceAccount token minted for the dashboard**
  (`kubectl create token <service-account> -n <namespace> --audience pacto-dashboard`).
  The dashboard checks it with a TokenReview for the `pacto-dashboard`
  audience, which needs `create` on `tokenreviews`. The chart grants that to
  the dashboard through the ClusterRole `<release>-dashboard-tokenreview`,
  with `controller.watchNamespaces` too, because TokenReview has no namespaced
  form. The session reads the fleet views as that ServiceAccount's RBAC
  allows and never acts. A ServiceAccount token without the audience is
  refused with `TokenAudienceRequired`, and the message prints the command.
- **Your own token** (an OIDC ID token, an EKS token, a static token). The API
  server checks it (SelfSubjectReview), and the session reads and acts as you.
  You may approve held changes where [the approver rule](delivery-identity.md#who-may-decide)
  allows you. A ServiceAccount never approves.

The dashboard keeps the token in the server-side session and checks it again
every five minutes, so a token that expires or is revoked ends its session.
Where delivery asks for [a recent sign-in](delivery-identity.md#a-recent-sign-in-to-decide),
only a token that carries its login time can approve: an OIDC ID token on a
cluster that maps `auth_time`. Static and EKS tokens carry none.

To get your own token:

- **With a static token** in your kubeconfig, run
  `kubectl config view --raw --minify -o jsonpath='{.users[0].user.token}'`.
- **On EKS**, run `aws eks get-token --cluster-name <cluster>` and take its
  `status.token` field.
- **On GKE**, run `gcloud auth print-access-token`.

Cloud provider tokens are short-lived, and the session ends when its token
expires.

### The sign-in link

The install notes print a port-forward and a link to the dashboard:

```bash
kubectl -n <namespace> port-forward svc/pacto-dashboard 3000:3000
echo "http://127.0.0.1:3000/#token=$(kubectl -n <namespace> create token pacto-viewer --audience pacto-dashboard --duration 10m)"
```

The link carries a 10-minute token for `pacto-viewer`, a ServiceAccount the
chart creates in the release namespace that may get, list and watch Pactos
and PactoRevisions and nothing else: cluster-wide through the ClusterRole
`<release>-viewer`, or a Role in each of `controller.watchNamespaces`.
Minting its token needs `create` on `serviceaccounts/token` in the release
namespace, which a namespace administrator has. The token sits after `#`, so
the browser never sends it to a server and it reaches no access log or
proxy. After 10 minutes the link says "link expired, run the command again".
A link carries only a token minted for the dashboard: a link with any other
token is refused with `SignInLinkNeedsDashboardToken`, so a link never opens
a session that acts.

### Serve without sign-in

To turn token sign-in off, set `dashboard.auth.tokenSignIn.enabled=false`.
With no issuer either, the dashboard then refuses to start unless you also set
`dashboard.auth.allowAnonymous=true`. It then serves everyone who reaches it
everything its ServiceAccount reads, and the install notes warn about it
([without sign-in](dashboard-access.md#without-sign-in)).

## Identity providers

| Provider | `issuerURL` | Claim mapping | Notes |
|---|---|---|---|
| Dex | `https://dex.example.com` | defaults | Add `offline_access` for renewal. For approvals add `audience:server:client_id:pacto-cli` ([why](delivery-identity.md#the-cluster-trusts-your-identity-provider)). |
| Keycloak | `https://keycloak.example.com/realms/<realm>` | `usernameClaim: preferred_username` | Add a Group Membership mapper named `groups` with full group path off. |
| Microsoft Entra ID | `https://login.microsoftonline.com/<tenant-id>/v2.0` | `usernameClaim: oid` | Add the `groups` optional claim; groups arrive as object IDs, so bind RBAC to those. Add `offline_access` for renewal. |
| Okta | `https://<org>.okta.com/oauth2/<server-id>` | defaults | Add a `groups` claim to the authorization server and `offline_access` for renewal. |
| Google | `https://accounts.google.com` | `usernameClaim: email` | The ID token carries no groups, so bind RBAC to users. Sessions end when the ID token expires, after about an hour. |

Use `oid` with Entra ID because its `sub` differs per application.

## Managed clusters

The API server must trust the same issuer with the same mapping.

- **EKS**: associate an OIDC identity provider with the cluster
  (`aws eks associate-identity-provider-config`) and give it the same
  `usernameClaim`, `usernamePrefix`, `groupsClaim` and `groupsPrefix`.
- **GKE**: configure your provider in Identity Service for GKE and match its
  `userClaim`, `userPrefix`, `groupsClaim` and `groupPrefix`.
- **AKS**: managed Entra ID integration accepts tokens for its own server
  application. To sign in with the dashboard's client, use structured
  authentication where the cluster offers it, or token sign-in.

Approvals need more from the API server than sign-in does; the
[approval table](delivery-identity.md#where-approving-works) lists which
clusters can provide it.
