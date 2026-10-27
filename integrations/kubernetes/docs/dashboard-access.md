# Who sees what in the dashboard

With sign-in, the fleet views reuse Kubernetes RBAC. You see a namespace's
services, revisions, targets, edges and evidence only if you may list Pactos
there. The dashboard asks the apiserver with a SubjectAccessReview for your
username and groups, keeps each answer a minute and applies one filter to every
view: counts, search, the graph, change analysis and the remote MCP fleet tools.
A visitor who has not signed in is checked as `system:anonymous`.

The username is `<issuer>#<sub>` and the groups claim is taken as it is, the
mapping [the cluster trusts](delivery-identity.md#the-cluster-trusts-your-identity-provider). A service known only from a registry, with no Pacto in any
namespace, stays visible. A hidden service is never named: the dashboard says
some services are hidden, and a service page counts the hidden services it
depends on and that depend on it. If the apiserver cannot answer, the views
show an error, not a smaller fleet.

Without sign-in nothing is filtered. On loopback the dashboard reads the fleet
as your kubeconfig user and shows all of it. A shared dashboard without sign-in
shows everything its ServiceAccount reads to anyone who reaches it.

## What the dashboard needs

The operator's chart grants the dashboard's ServiceAccount create on
`subjectaccessreviews`, and the operator holds it too while the dashboard is
enabled, since Kubernetes lets it grant only what it has. A dashboard you run
yourself with `pacto dashboard --oidc-issuer-url` needs the same right for the
credentials it runs with. A remote `pacto mcp --fleet` with `--auth-issuer`
does too.
