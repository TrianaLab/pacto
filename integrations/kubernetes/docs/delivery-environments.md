# Delivery reference: environments and the installation ladder

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

With delivery on, a service needs no Stage, no `environments[]` and no Flux or
Argo CD object of its own. The promoter reads the organization index, finds
each service whose published version has an accepted publisher verdict and
creates its [Stages](../../glossary.md#stage) along the installation ladder.
Set up delivery first with
[Delivery reference: setup and gates](delivery-setup.md).

## The installation ladder

The installation ladder is the chain of environments every delivered service
goes through, in promotion order. It counts as declared:

```yaml
delivery:
  environments:
    enabled: true
    ladder: [staging, production]
```

Each rung takes the versions the rung before it verified; the first takes them
from the registry. A cluster whose name names an environment, such as
`eu-prod`, hosts only that rung (`prod` reads as `production`); any other
cluster hosts every rung.

A contract changes this only when it declares
[`environments`](../../contract-reference/environments.md):

- With no `environments[]`, the service gets every rung.
- With `environments[]`, the list is the exact set. A rung it leaves out is
  opted out, and an entry outside the ladder, such as a temporary `pr-412`, is
  a leaf that takes its versions from the registry.

## Proposed rungs

The promoter also proposes rungs it finds in the cluster: a suffix `-<x>`
shared by the namespaces `<service>-<x>` of two or more services, and the
environments cluster names advertise. A proposed rung is muted and changes
nothing until a person confirms it, and once confirmed it stays a leaf, so
inference never decides what reaches a declared rung.

`pacto delivery inbox` and the dashboard inbox show the proposal with the
exact command to confirm it:

```bash
pacto delivery ladder confirm --proposal sha256:<digest>
pacto delivery ladder confirm --proposal sha256:<digest> --drop qa
```

The command signs the confirmation keyless after a fresh login and pushes it to
the approvals repository the promoter reads. It signs nothing when the proposal
changed since the inbox showed it. The promoter counts the newest record a
person signed under one of `delivery.identities.signers` who may approve
`environmentladders`, granted to the user, since a certificate names no group.
No API serves `environmentladders`, so the ClusterRole is applied as a manifest:

```bash
kubectl apply -f - <<'YAML'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: pacto-ladder-approver}
rules: [{apiGroups: [delivery.pacto.trianalab.io], resources: [environmentladders], verbs: [approve]}]
YAML
kubectl create clusterrolebinding pacto-ladder-approver \
  --clusterrole=pacto-ladder-approver --user=<user>
```

A dropped rung is never proposed again.

## Where a service runs

For each rung, the promoter places the service in the first namespace that
applies:

1. The namespace of a hand-written Stage of the service for that environment.
2. A rule of the signed placement policy (declared).
3. Where a Pacto of the service runs, when its contract comes from the same
   repository and team (inferred, observed once an applier runs it).
4. The namespace of the workload the published contract was inferred from.
5. The namespace a GitOps source of the service's verified repository deploys to.
6. The namespace named after the environment (default).

The promoter refuses `kube-*`, the delivery, `flux-system` and `argocd`
namespaces, a namespace an earlier rung of the service took, and a namespace
holding a workload or Pacto of an unknown owner or of another team, unless a
signed rule declared it. A workload Helm, an IDP or another tool manages reads
unknown: Pacto never adds a second applier. An unplaced environment says why.

## The Stages Pacto creates

A placed environment gets the Stage `<service>-<environment>` in its
namespace, beside the adopted Argo CD Application or Flux object that already
applies the workload. The Stage of a later rung names the Stage of the rung
before it as `upstreamStage`. Each carries the label
`delivery.pacto.trianalab.io/managed-by=pacto` and annotations with its
placement. Pacto sets `startLevel` and `approval` only when it creates the
Stage: an applier labelled for it starts autonomous with every promote held
for a person, and any other starts at `observe`. Pacto never touches a
hand-written Stage, whatever its labels say: it acts only on the objects its
private state records it created.

## IDP mode

Under `profile: idp` with Flux (`idp.flux.install`), a service no applier runs
yet gets, in each placed environment, an `OCIRepository` and a Kustomization
or HelmRelease named `<service>-<environment>`. They are created suspended,
and the pointer has no `spec.ref`, so nothing applies before a person.

The first promote waits for a person, and its question starts with the
placement, such as "deploy payments to namespace staging?". Approving it
confirms the placement under that person's name, creates the
`<service>-deployer` ServiceAccount with a RoleBinding to the chart's
`pacto-operator-delivery-deployer` ClusterRole, which holds only namespaced
workload kinds, and unsuspends the objects. Flux then applies every version as
that account. An agent's approval confirms nothing, and a declared placement
needs no confirmation.

## Namespaces

With `delivery.environments.createNamespaces=true` in IDP mode, the promoter
creates a missing namespace at once for a declared or observed placement. Any
other waits until a person runs
`pacto delivery ladder confirm --create-namespaces`, which signs the list of
candidates that person may read. Pacto labels the namespaces it creates and
never deletes one. `profile: hardened` refuses the value.

## Retirement and events

A Stage Pacto created is retired `delivery.environments.retireAfter` (default
`168h`) after its environment leaves the installation ladder or the contract,
or a temporary environment passes its `expires`. Retiring deletes the Stage,
the Flux objects Pacto created for it and, once the service has no Stage left
in that namespace, its deployer account. Pacto never deletes a namespace or a
workload, and a pass that read the index or the ladder records only in part
retires nothing.

The promoter reports `EnvironmentCreated` and `EnvironmentDeleted` once for
each Stage it created, including a delete made while it was down, and posts
them to `delivery.recorder.cdeventsSink` as CDEvents.

## What the promoter publishes

| ConfigMap in the delivery namespace | Holds | Who reads it |
|---|---|---|
| `pacto-delivery-environments` | The declared and confirmed rungs, the open proposal and its digest, the count of unplaced environments, `asOf` and completeness. It names no service | Every authenticated user |
| `pacto-delivery-placements` | Each service's environments with their source, placement and Stage, the private state and the namespaces waiting to be created | Only the promoter and whoever the platform binds |

`pacto delivery environments` and the dashboard's Environments view show the
rows a reader may see and count the rest.

## The placement policy

A security owner who wants explicit placement signs a placement policy: an
in-toto statement of predicate type `delivery.pacto.trianalab.io/placement/v1`
signed by a `role: policy` key of `delivery.publishers.policySigners`, pushed
under the tag `placement` of the repository the
[signed delivery policy](delivery-security.md#the-signed-delivery-policy)
lives in. An older promoter keeps reading the delivery policy.

```yaml
predicate:
  at: "2026-11-22T09:00:00Z"   # signed time; an older policy is ignored
  rules:                       # first match wins
    - name: payments-team
      services: ["pay*"]       # empty places every service
      environments: ["*"]
      namespace: "{service}-{environment}"
```
