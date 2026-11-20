---
"@pacto/core": minor
---

`pacto impact --running <new>` and the `running` argument of `pacto_impact`
judge a contract against the revisions of its service that run on known
targets, read from the signed target snapshots, instead of an old revision.
A local `<new>` takes the fleet domain that holds its service name. With no
running revision the contract is a first version: a running consumer whose
range rejects it blocks, and it clears only on a complete answer. A running
revision Pacto could not link, pull or trust, one linked by tag only, an
inferred workload of the service and a revision on a host outside the
snapshot hosts and `PACTO_RUNNING_REGISTRIES` each make the answer unverified
at best. The answer carries `running` with every ref, the one judged and the
unread ones, and a restricted MCP view refuses the question.

Detected providers rows carry `offers`, Pacto integrations that fit the
provider: the Argo CD row offers "Gate every Argo CD sync with one
repo-server plugin" on `pacto fleet providers`, `pacto_fleet_status` and the
dashboard's Data sources.

The GitOps page documents that plugin: a Config Management Plugin sidecar on
`argocd-repo-server` that renders each Application folder holding a contract,
runs `pacto impact --running` on the contract the render applies and hands
Argo CD the manifests only after a pass, with no edit per Application and no
Kubernetes RBAC. It also documents a per-Application PreSync Job as the
fallback.
