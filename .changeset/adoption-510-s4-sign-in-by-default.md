---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

The dashboard asks people to sign in by default, and the install notes print
a short-lived sign-in link.

Upgrade effect first. After any upgrade, one that reuses the earlier
release's values included, the dashboard asks people to sign in.
To keep serving without sign-in, set `dashboard.auth.tokenSignIn.enabled=false`
and `dashboard.auth.allowAnonymous=true`; the install notes say so when the
previous release served without sign-in.

- The new chart value `dashboard.auth.tokenSignIn.enabled` defaults to `true`.
  `dashboard.auth.allowAnonymous` defaults to `false` and passes
  `--dashboard-allow-anonymous` only when set to `true`.
  `dashboard.auth.tokenLogin.enabled` is deprecated and ignored; the schema
  still accepts it.
- The install notes print a port-forward and a sign-in link carrying a
  10-minute token for the chart's read-only `pacto-viewer` ServiceAccount,
  minted with `--audience pacto-dashboard` and passed after `#`, so no server
  or log sees it. An expired link says "link expired, run the command again".
- A ServiceAccount token signs in only when a TokenReview proves it was minted
  for the `pacto-dashboard` audience, and its session reads the fleet only:
  the Delivery tab is hidden and delivery reads answer `SignInLinkReadsFleet`.
  A ServiceAccount token without the audience is refused with
  `TokenAudienceRequired`, and a sign-in link with any other token with
  `SignInLinkNeedsDashboardToken`.
- A person's own token (an OIDC ID token, an EKS or static token) signs in as
  them, and their token session may now approve held changes under the
  approver rule. ServiceAccount tokens never approve. `TokenCannotDecide` is
  gone.
- The dashboard's ServiceAccount gains `create` on `tokenreviews` through the
  ClusterRole `<release>-dashboard-tokenreview`, also with
  `controller.watchNamespaces`. `pacto-viewer` gets `get`, `list` and `watch`
  on Pactos and PactoRevisions through the ClusterRole `<release>-viewer`, or a
  Role in each watched namespace.
