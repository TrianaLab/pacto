# Choose an installation profile

The chart value `profile` selects how much the operator may do in your
cluster. It takes `default`, `hardened` or `idp`, or a list of `hardened` and
`idp`. No profile is required: `default` is what the chart rendered before the
value existed.

## default

The operator watches the whole cluster with one ClusterRole and runs the
managed dashboard with sign-in. Probing and metrics observation are `auto`,
so a namespace owner turns them on with a label. Pacto delivery and the node
agent stay off until you turn them on.

## hardened

For a reviewer who signs off on namespaced privilege only. Create the release
namespace, then install into it with a list of namespaces to watch:

```bash
kubectl create namespace pacto-operator-system
helm install pacto oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system \
  --set profile=hardened \
  --set 'controller.watchNamespaces={payments,ledger}'
```

The chart renders no ClusterRole or ClusterRoleBinding, no managed dashboard,
no `pacto-viewer` ServiceAccount, no node agent, no Namespace object and no
Pacto delivery. Without `controller.watchNamespaces` the install fails with
`profile hardened needs controller.watchNamespaces`. A value that adds
cluster-wide privilege fails the install with its own message, so nothing is
applied. The table below lists every control.

## idp

For a platform team that runs Pacto as its internal developer platform layer:

```bash
helm install pacto oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --create-namespace \
  --set profile=idp
```

Probing and metrics observation go from `auto` to `on` in every watched
namespace. A value you set to `off` stays `off`. Pacto delivery turns on, with
the [manifest inspector](delivery-setup.md#how-a-change-moves)
(`delivery.manifestInspector`, on wherever delivery is enabled). Dashboard
sign-in keeps its OIDC settings. No profile installs Flux until you set
`idp.flux.install=true` ([IDP mode](delivery-setup.md#idp-mode)).

Under idp an explicit `auto` also reads `on`, so a namespace label no longer
selects which namespaces Pacto observes. To observe only labelled namespaces,
leave idp: `--set profile=default --set delivery.enabled=true`.

### What idp adds

You choose `idp`, so you choose what it adds:

- The read of ServiceMonitors and PodMonitors (`get`, `list`, `watch`), in the
  operator ClusterRole, or in each watch Role with `controller.watchNamespaces`.
- Pacto delivery's cluster roles: the `-promoter`, `-agent`, `-approver`,
  `-view` and `-edit` ClusterRoles, plus the recorder's ClusterRole when the
  recorder runs (the table below). See [Delivery](delivery.md).
- The promoter's Flux grants: `delivery.flux.enabled: auto` reads `true`, so
  they render before Flux arrives, in the same release or a later upgrade.
- The Evidence Server lifecycle in the release namespace: `create`, `delete`,
  `get`, `patch` and `update` on Deployments and Services.
- With `idp.flux.install=true`, upstream Flux and its privilege, below.
- With `idp.crossplane.install=true` only, the upstream Crossplane chart's
  RBAC (see [Crossplane](#crossplane)).

### The packaged Flux

`idp.flux.install=true` installs upstream Flux 2.10.1 with this release: the
source-controller, kustomize-controller and helm-controller in `flux-system`,
their CRDs and RBAC.
The chart renders the upstream release manifests vendored under `files/flux/`,
pinned in `files/flux/VERSION`, and changes only Pacto's labels,
`helm.sh/resource-policy: keep` and these controller arguments:

- `--no-cross-namespace-refs=true` and `--default-service-account=default` on
  kustomize-controller and helm-controller: an applier object reads sources
  only in its own namespace, and one without `serviceAccountName` applies as
  the namespace's `default` account, never as cluster-admin.
- `--no-remote-bases=true` on kustomize-controller: a kustomization pulls no
  base the manifest inspector did not read.
- No `--events-addr`: no notification-controller runs.

The privilege is upstream's: the Flux ClusterRoles and the cluster-admin
binding of kustomize-controller and helm-controller, listed in the chart
README. Each Pacto chart version pins one Flux version: to keep it, stay on
that chart version; `helm upgrade` to a newer chart upgrades Flux when the
release notes name a newer one.

The chart refuses the value under `default` (`idp.flux.install needs profile
idp`) and with hardened (`profile hardened renders no ClusterRole: leave
idp.flux.install false`). When a `source-controller` or `kustomize-controller`
Deployment that this release did not install already runs, it refuses with
`Flux is already installed: leave idp.flux.install false`. `helm template`
cannot look, so check that no other Flux runs first.

### What idp turns on when the cluster supports it

Each piece below runs only where its prerequisite holds. You set no further
value. The `Evidence`, `Recorder` and `Crossplane` rows of
`pacto fleet providers`, `pacto_fleet_status.providers` and Detected providers
on Data sources say whether each piece is on and why.

| Piece | Prerequisite | How to turn it off |
|---|---|---|
| Evidence Server | The registry serves OCI Referrers; a subject or subject prefix is set, on that registry; the trust Secret holds a non-empty `.pub` key, or the `evidence.trust.configKey` key when that is set | Remove the public keys from the trust Secret, or leave profile idp |
| Recorder | Pacto delivery renders; a streams repository (`registry` or `delivery.recorder.streamsRepository`); exactly one complete signing method; `delivery.recorder.enabled` left unset | `delivery.recorder.enabled=false` |
| Crossplane | Detected under every profile; idp installs it only with `idp.crossplane.install=true` | `idp.crossplane.install=false` (see [Crossplane](#crossplane)) |

The trust Secret defaults to `<fullname>-evidence-trust`, or
`evidence.trust.existingSecret`. Pacto never creates a key. With no subjects
or prefixes set, the subject prefix defaults to `oci://<registry>/` when
`registry` has a path, such as `ghcr.io/acme`. A bare host gets none, and the
row asks for `evidence.registry.subjectPrefixes`. When the operator cannot
tell whether a prerequisite holds, the row reads `unknown` and the Evidence
Server stays as it was. The operator probes each registry once, so a registry
that later serves Referrers is seen after an operator restart. See
[The Evidence Server](evidence-server.md#under-profile-idp).

A running recorder changes how approvals count, and older CLIs and promoters
refuse its records. Upgrade the CLIs and promoters that follow these streams
first, or set `delivery.recorder.enabled=false`. An upgrade that
reuses an earlier chart's values keeps the `false` that chart set by
default, so the recorder stays off until you set
`delivery.recorder.enabled=null`.

### Crossplane

The operator reads Crossplane from its running core Deployment, so CRDs left
behind never read as a running Crossplane. A Crossplane that runs before it
serves its APIs reads `Crossplane starting` until the next detection run, and
a core Deployment with no available replica reads `Crossplane not available`.
Under idp with no Crossplane, the row proposes the bootstrap, except under
profile hardened, which refuses it. The bootstrap installs upstream
Crossplane into the release namespace with this release:

```bash
helm upgrade pacto oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --reset-then-reuse-values \
  --set idp.crossplane.install=true
```

The install fails with `idp.crossplane.install needs profile idp` outside
idp, with `profile hardened renders no ClusterRole: leave
idp.crossplane.install false` under hardened, and with `Crossplane is already
installed: leave idp.crossplane.install false` when another release or a hand
install runs a Crossplane Deployment. The last check needs the cluster: under
`helm template` and Argo CD the chart cannot see another Crossplane, so check
that none runs before you set the value.

The bootstrap adds the upstream chart's privilege: the `crossplane`
ServiceAccount writes CRDs, Secrets, ServiceAccounts, Services, Deployments and
webhook configurations cluster-wide, the `rbac-manager` ServiceAccount may escalate
and bind ClusterRoles, and the `crossplane:masters` group gets
`crossplane-admin`.

### Turn the Crossplane bootstrap off

Crossplane creates its CRDs and webhook configurations at runtime, so Helm
never owns them. `idp.crossplane.install=false` removes the Crossplane core
and keeps its CRDs, XRDs, Compositions, Providers and claims. To keep
Crossplane running, install it as its own release before you turn the value
off. If Helm refuses objects this release owns, such as the `crossplane`
ClusterRole, turn the value off and run the install right after:

```bash
helm repo add crossplane-stable https://charts.crossplane.io/stable
helm install crossplane crossplane-stable/crossplane \
  --namespace crossplane-system --create-namespace
```

If you remove it instead, the leftover webhook configurations call the
deleted `crossplane-webhooks` Service and refuse writes of Crossplane
objects. Delete them:

```bash
kubectl get validatingwebhookconfigurations,mutatingwebhookconfigurations -o json |
  jq -r '.items[] | select(any(.webhooks[]?; .clientConfig.service.name? == "crossplane-webhooks"))
    | "\(.kind | ascii_downcase)/\(.metadata.name)"' |
  xargs kubectl delete
```

### Inferred offerings

Under every profile, a cluster-wide operator with `registry` set reads each
XRD and the Compositions that select it and pushes it as an
[inferred offering](../../inferred-offerings.md), signed with the installation
key, to `<registry>/pacto-inferred/crossplane-<clusterID>/services`. Each
installation, member clusters included, writes its own repository, so several
installations can share one registry. The detection ConfigMap advertises that
repository, the offerings the last complete run observed and when.

For this the chart adds one read-only rule: `get`, `list` and `watch` on
`compositeresourcedefinitions` and `compositions` in
`apiextensions.crossplane.io`. Without Crossplane the
operator reads nothing and checks again on the next run. Turn it off with
`crossplane.offerings.enabled=false` (`--crossplane-offerings=false`).

## Combine hardened and idp

Pass both names as a list:

```bash
--set 'profile={hardened,idp}'
```

A bare `--set profile=hardened,idp` is a Helm parse error; in a values file,
`profile: hardened,idp` works too, written without spaces. Where the two disagree, hardened wins:
probing and metrics observation are `on`, the monitor read lands in each
watch Role and Pacto delivery stays off.

## Controls under hardened

| Control | Under hardened | How to turn it back on |
|---|---|---|
| Cluster-wide RBAC (operator ClusterRole and ClusterRoleBinding) | Not rendered; each watched namespace gets a Role | Leave profile hardened and empty `controller.watchNamespaces` |
| Managed dashboard, its Service, Ingress and HTTPRoute | Off | Leave profile hardened; meanwhile run `pacto dashboard --detected k8s:<ns>/<fullname>-detected` with your own credentials |
| `pacto-viewer` ServiceAccount and the TokenReview grant | Not rendered | Leave profile hardened; they follow the managed dashboard |
| MCP endpoint at `/mcp` | Off with the managed dashboard | Leave profile hardened; elsewhere it is on by default, and `dashboard.mcp.enabled=false` turns it off |
| Member review channel (`hub.url`) | Off: answering the hub's reviews needs a cluster-wide SubjectAccessReview grant | Leave profile hardened, then set `hub.url` |
| Anonymous dashboard | Refused | Leave profile hardened, then set `dashboard.auth.allowAnonymous=true` |
| Node agent | Refused: it needs a privileged namespace and cluster-wide reads | Leave profile hardened, then set `nodeAgent.enabled=true` |
| Dex subchart's cluster-scoped RBAC | Refused | Set `dex.rbac.createClusterScoped=false` or `dex.enabled=false`; otherwise leave profile hardened |
| Namespaces the chart creates (node agent, delivery) | None rendered | Leave profile hardened; outside it, `nodeAgent.createNamespace` and `delivery.createNamespace` |
| Pacto delivery | Refused, because its roles are cluster-wide | Leave profile hardened, then set `delivery.enabled=true` or `profile: idp` |
| Packaged Flux (`idp.flux.install`) | Refused: upstream Flux binds cluster-admin | Leave profile hardened, set `profile: idp`, then `idp.flux.install=true` |
| The delivery namespace after delivery is dropped | Kept with its trust config, pins and keys | Turn delivery back on to adopt it again, or `kubectl delete namespace <ns>` |
| Watch Roles, with `namespaces get` on the watched namespace while a mode is `auto` | Kept: one Role per watched namespace | Already on; set probing and metrics to `on` or `off` to drop the `namespaces get` |
| Release-namespace grants: leader election, events, `secrets` `get`, and `secrets` `create` with `registry` | Kept | Already on; leave `registry` empty to drop `create` |
| Teardown grants in the release namespace: `get` and `delete` by name on the `pacto-dashboard`, `pacto-evidence` and `pacto-node-agent` objects | Kept, so an upgrade to hardened removes what an earlier release created | Always on |
| `controller.secretGrants` and `registryPullSecret` | Kept: one Role per named Secret | Already on; empty the values to drop them |
| Cluster-identity Role in `kube-system` (`get` on `namespaces/kube-system` only) | Rendered with `registry` | Leave `registry` empty to drop it; the installer needs RBAC rights in `kube-system` |
| CRDs | Installed | Set `crds.install=false` and apply them yourself |
| Evidence Server | Off until `evidence.enabled=true`, in the release namespace | Set `evidence.enabled=true` |
| Probing and metrics observation | `auto`, `on` with `idp` | Set `controller.probing` and `controller.metricsObservation` |

The operator stops without a cluster id when `registry` is set, because the
target snapshot it publishes names the cluster. A namespace list renders no
ClusterRole to read it, so the chart reads the `kube-system` Namespace through
the cluster-identity Role and nothing else.

When an upgrade drops Pacto delivery, Helm keeps its namespace and the
delivery CRDs, and the release notes print the command that deletes the
namespace. `helm uninstall` keeps the namespace too; see
[Uninstall](installation.md#uninstall).

## Read the mode

`pacto_capabilities` and `GET /api/capabilities` answer `installation`: the
profile, the registry value, whether the cluster serves the Pacto delivery
API, the clusters in your view and whether the server serves MCP. The delivery
CRDs outlive a chart that dropped delivery, so beside profile hardened a served
API reads `partial`. Each part carries its `source`,
`asOf`, `provenance` and `completeness`. A part Pacto cannot read is `null`
with the reason. The Installation row on Data sources names the profile.
