# Dashboard authentication

A shared dashboard signs people in before it shows anything. Every request then
runs as that person: delivery reads and actions go to the API server with their
own token, and the fleet views show only the namespaces their Kubernetes RBAC
lets them read ([who sees what](dashboard-access.md)). A service known only
from a registry belongs to no namespace, so only people who may list Pactos
in every namespace see it, and counts and totals leave it out for everyone
else. The dashboard's
ServiceAccount stays read-only, with one extra right: asking the API server
what a person may read (SubjectAccessReview). There is no impersonation, so
the API server's audit log names the person.

## How it works

1. A person opens the dashboard and sees only its sign-in screen. Every API
   endpoint and `/metrics` answer `401` until they sign in; the page itself,
   `/health`, `/api/session` and `/auth/*` stay open.
2. They sign in with your identity provider (OpenID Connect, authorization code
   with PKCE) or, where you allow it, paste a Kubernetes bearer token.
3. The dashboard keeps the tokens in a server-side session behind an
   `HttpOnly`, `Secure` cookie. Browser JavaScript never sees a token.
4. Each request uses the session's token. The API server authenticates it and
   RBAC decides, exactly as for `kubectl`.

On every page, the person button in the header opens who you are, your groups
and **Sign out**.
Approving a held change needs a fresh identity provider sign-in
([where approving works](delivery-identity.md#where-approving-works)).

A dashboard on your own machine (`pacto dashboard` on loopback, with no
sign-in configured) acts as your kubeconfig user and shows no sign-in screen.

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
      # The claim mapping: the same values as the API server's claimMappings.
      usernameClaim: sub              # empty: sub
      usernamePrefix: ""              # empty: the issuer URL and #, none for email; "-" for none
      groupsClaim: groups             # empty: groups
      groupsPrefix: ""
    session:
      idleTimeout: 1h                 # empty: 1h
      maxAge: 12h                     # empty: 12h
    tokenLogin:
      enabled: false
```

Register `https://<dashboard>/auth/callback` as the client's redirect URL and
terminate TLS in front of the dashboard. `pacto dashboard` takes the same
settings as `--oidc-*`, `--session-*` and `--token-login` flags
([CLI reference](../../cli-reference.md)), with the client secret in
`PACTO_DASHBOARD_OIDC_CLIENT_SECRET`. A remote `pacto mcp` with
`--auth-issuer` takes the same claim mapping flags
([who sees what](dashboard-access.md)).

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

## Verify History

The dashboard verifies delivery records against a trust config before History
shows them. Put the trust config (or the recorder's PEM public key) in a
ConfigMap in the operator namespace and name it:

```yaml
dashboard:
  deliveryTrust: {configMap: delivery-trust, key: trust.yaml}
```

The trust config is the one the CLI reads
([trust and key rotation](delivery-record-keys.md#trust-and-key-rotation)).
`pacto dashboard` takes it as `--delivery-trust`. Without it, History shows
records unverified and every answer says it is partial.

## Match the API server's claim mapping

The API server decides who a token is. The dashboard asks RBAC about the same
person, so its claim mapping must equal the API server's. With the defaults
above, the API server's
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

A mismatch shows as the wrong fleet: the views ask about a user the API server
never sees. To check, sign in and compare the header with `kubectl auth whoami`
run with a token from the same issuer. The dashboard drops any group starting
with `system:` from a token, as the API server does.

## Sessions

The dashboard renews the ID token with the refresh token before it expires, so
a session lasts until `idleTimeout` passes without a request or `maxAge` after
sign-in. A provider that issues refresh tokens only for `offline_access` needs
it in `extraScopes`; without a refresh token the session ends when the ID token
expires. Renewal keeps the original login time, so it never counts as a fresh
sign-in.

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

Set `tokenLogin.enabled: true` for a cluster whose API server trusts no
identity provider you can use. The sign-in screen then takes any bearer token
the API server accepts, such as a ServiceAccount token from
`kubectl create token`. The dashboard checks it with a SelfSubjectReview,
keeps it in the server-side session and checks it again every five minutes, so
a token that expires or is revoked ends its session. A token session reads and
acts with that token's RBAC and never decides on a held change: it proves no
recent sign-in. Token sign-in works with or without `oidc`.

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
