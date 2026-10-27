# Who sees what in the dashboard

With [sign-in](dashboard-authentication.md), the fleet views reuse Kubernetes
RBAC. You see a namespace's services, revisions, targets, edges and evidence
only if you may list Pactos there. The dashboard asks the apiserver with a
SubjectAccessReview for your username and groups, keeps each answer a minute
and applies one filter to every view: counts, search, the graph, change
analysis and the remote MCP fleet tools. A visitor who has not signed in gets
the sign-in screen and no fleet data.

Your username and groups come from your ID token through the
[claim mapping](dashboard-authentication.md#match-the-api-servers-claim-mapping),
which must match the API server's. After a token sign-in they are what the API
server answered for that token. A remote `pacto mcp --fleet` takes the same
mapping as `--oidc-username-claim`, `--oidc-username-prefix`,
`--oidc-groups-claim` and `--oidc-groups-prefix`, so a person is the same
identity in both. When your RBAC lets you read no namespace, the
dashboard says so and names your groups.

## What stays hidden

Change analysis on raw references answers only for services you can see: a
hidden one, on either side of the change, reads as not found. A service known
only from a registry, with no Pacto in any namespace, belongs to no namespace:
only people who may list Pactos in every namespace see it, and for everyone
else it is hidden like the rest. A hidden service is never named: the dashboard says
some services are hidden, and a service page counts the hidden services it
depends on and that depend on it. If the apiserver cannot answer, the views
show an error, not a smaller fleet.

Without sign-in nothing is filtered. On loopback the dashboard reads the fleet
as your kubeconfig user and shows all of it. A shared dashboard without sign-in
shows everything its ServiceAccount reads to anyone who reaches it.

## What the dashboard needs

The operator's chart grants the dashboard's ServiceAccount create on
`subjectaccessreviews` and get, list and watch on delivery Stages and
Operations, for the [shared cache](dashboard-authentication.md). The operator
holds the same rights while the dashboard is enabled, since Kubernetes lets it
grant only what it has. A dashboard you run yourself with
`pacto dashboard --oidc-issuer-url` needs the same rights for the credentials
it runs with; without the delivery reads, it reads delivery with each person's
token. A remote `pacto mcp --fleet` with `--auth-issuer` needs create on
`subjectaccessreviews`.

For delivery, the ServiceAccount also lists namespaces. A person who may not
list Stages in every namespace sees the Stages of each namespace where their
own RBAC lets them list Stages, including a namespace that runs no Pacto yet.
The ServiceAccount learns only the namespace names; the person's own RBAC
decides which Stages they read. Without this right the dashboard reads Stages
only in the namespaces of the fleet's workloads and marks the answer partial.
