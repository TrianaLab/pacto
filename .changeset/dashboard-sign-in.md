---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A shared dashboard can sign people in, and then shows nothing until they do.
`pacto dashboard` takes an OpenID Connect issuer (`--oidc-*`, the client secret
in `PACTO_DASHBOARD_OIDC_CLIENT_SECRET`), a private issuer CA
(`--oidc-ca-file`), an internal issuer address (`--oidc-internal-issuer-url`),
the API server's claim mapping, session lifetimes (`--session-idle-timeout`,
`--session-max-age`) and `--token-login`, which takes a pasted Kubernetes token
for clusters that trust no identity provider. The API server names each
signed-in person (SelfSubjectReview), and every view and action runs with
their token and RBAC. `/api/session` says who is signed in and how; every
other endpoint answers `401` with the code `LoginRequired` until they sign in.
The header names who they are, and the sign-in button says Sign in with OIDC
or names the provider (`--oidc-display-name`). A remote `pacto mcp` reads the
same claim mapping flags and logs each caller as the mapping names them.

The `pacto-operator` chart gains `dashboard.auth`: `oidc` (`issuerURL`,
`internalIssuerURL`, `caBundle`, `clientID`, `clientSecretRef`, `redirectURL`,
`extraScopes`, `displayName` and the claim mapping `usernameClaim`,
`usernamePrefix`, `groupsClaim`, `groupsPrefix`), `session` (`idleTimeout`,
`maxAge`) and `tokenLogin.enabled`. See
[Dashboard authentication](https://pacto.run/latest/integrations/kubernetes/dashboard-authentication/).
