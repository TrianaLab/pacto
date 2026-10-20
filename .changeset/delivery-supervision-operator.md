---
"@pacto/k8s-module": minor
---

The delivery promoter takes an approval only from a member of a team the held
question names, and the operator chart configures dashboard sign-in.

An `Approval` carries `spec.groups`, which admission binds to the approving
user's groups. The promoter ignores an Approval whose groups name none of the
operation's `floor.owners`, by name or with `--team-group-prefix` (chart
`identities.teamGroupPrefix`), and checks that the approver is a person on
those groups. Apply the delivery CRDs before `helm upgrade pacto-delivery`: the
Approval CRD gains `spec.groups`.

The `pacto-operator` chart gains `dashboard.auth.oidc` (`issuerURL`,
`clientID`, `clientSecretRef`, `redirectURL`, `extraScopes`),
`dashboard.auth.teamGroupPrefix` and `dashboard.miraURL`, passed to the managed
dashboard, the client secret by `secretKeyRef`. The dashboard's role gains
nothing: no delivery right and no impersonation. Its namespace scope is now
passed in `PACTO_DASHBOARD_NAMESPACE`, the variable the dashboard reads.
