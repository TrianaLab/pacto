---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

With delivery on, Pacto creates and retires each delivered service's Stages
from the installation ladder, so a service published with `pacto ci --publish`
needs no `environments[]`, Stage or Flux object of its own. The chart value
`delivery.environments.ladder` (default `staging` then `production`) is the
declared chain; the promoter proposes further rungs from namespaces and
cluster names, and a person confirms them once with
`pacto delivery ladder confirm`, a keyless record in the approvals repository.
Each service is placed by a hand-written Stage, a signed placement policy
(`delivery.pacto.trianalab.io/placement/v1`), its running Pacto, the workload
its contract was inferred from, a GitOps source or the namespace named after
the environment, and never in a system namespace or another team's. Pacto
creates Stages only beside an adopted Argo CD or Flux applier; under
`profile: idp` it creates suspended Flux objects, and a person's first
approval confirms the placement, creates the `<service>-deployer` account and
unsuspends them. Pacto-created Stages retire `delivery.environments.retireAfter`
(default `168h`) after their environment leaves the ladder or the contract or
expires, with one `EnvironmentCreated` and `EnvironmentDeleted` each; Pacto
deletes no namespace or workload. New chart values:
`delivery.environments.{enabled,ladder,createNamespaces,retireAfter}`; an
upgrade that reuses older values renders none of it. `pacto delivery
environments`, the inbox, MCP and the dashboard show each environment's source
and placement and the open proposal.

`pactoVersion` 2.6 makes `environments[].file` optional: an environment with
no values file adds no values of its own and is checked on its
configurations' defaults. A contract older than 2.6 still requires `file`.

`pacto init --delivery` no longer writes environments, Stages or Flux objects;
`--env` is deprecated and ignored.
