---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Group inferred services the way the cluster already does: by Argo CD
Application, Flux Kustomization or HelmRelease, Helm release or
app.kubernetes.io labels, with the source repository URL and where Pacto read
it. `pacto fleet entities --group`, `pacto_fleet_entities.group` and
`GET /api/fleet/entities?group` filter by group; Data sources lists Argo CD,
Flux and Helm. The dashboard role lists Argo CD Applications and Flux
Kustomizations, HelmReleases and GitRepositories. Data sources also lists who
promotes and who reports, from an optional `fingerprints` chart value and
`--fingerprints` flag that name an installation's own workload managers and
promoters; a workload one of those managers applies reads `external: <product>`
in the Stage applier verdict, and the promoter writes no pointer for it.
