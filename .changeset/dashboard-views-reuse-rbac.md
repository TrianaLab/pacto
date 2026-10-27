---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A dashboard people sign in to now shows each person only what Kubernetes RBAC
lets them read. A namespace's services, revisions, targets, edges and evidence
appear only if the person may list Pactos there, checked with a
SubjectAccessReview for their `<issuer>#<sub>` username and groups and kept a
minute. One filter covers every fleet view, counts, search, the graph, change
analysis and a remote `pacto mcp --fleet` with `--auth-issuer`. Hidden services
are never named: the dashboard says some are hidden and a service page counts
the hidden services on each side. A visitor who has not signed in is checked as
`system:anonymous`, so a dashboard with sign-in but without `required` no longer
shows them namespaced services. Local and sign-in-free dashboards are unchanged.
The operator grants the dashboard's ServiceAccount create on
`subjectaccessreviews` and holds that right itself while the dashboard is enabled.
