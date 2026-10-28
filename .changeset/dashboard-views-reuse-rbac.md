---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

A dashboard people sign in to now shows each person only what Kubernetes RBAC
lets them read. A namespace's services, revisions, targets, edges and evidence
appear only if the person may list Pactos there, checked with a
SubjectAccessReview for their username and groups, as the API server names
them, and kept a minute. One filter covers every fleet view, counts, search, the graph, change
analysis and a remote `pacto mcp --fleet` with `--auth-issuer`. The dashboard
says when services are hidden without naming them, and a service page counts
the hidden services on each side. A service known only from a registry, with no
namespace, shows only to people who may list Pactos in every namespace. Local
and sign-in-free dashboards are unchanged.
The operator grants the dashboard's ServiceAccount create on
`subjectaccessreviews` and holds that right itself while the dashboard is enabled.
