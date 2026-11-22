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
namespace. A value you set to `off` stays `off`. Pacto delivery turns on.
Dashboard sign-in keeps its OIDC settings.

Under idp an explicit `auto` also reads `on`, so a namespace label no longer
selects which namespaces Pacto observes. To observe only labelled namespaces,
leave idp: `--set profile=default --set delivery.enabled=true`.

### What idp adds

You choose `idp`, so you choose what it adds:

- The read of ServiceMonitors and PodMonitors (`get`, `list`, `watch`), in the
  operator ClusterRole, or in each watch Role with `controller.watchNamespaces`.
- Pacto delivery's cluster roles: the `-promoter`, `-agent`, `-approver`,
  `-view` and `-edit` ClusterRoles, plus the recorder's ClusterRole when
  `delivery.recorder.enabled` is `true` (the quickstart sets it). See
  [Delivery](delivery.md).

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
| Member review channel (`hub.url`) | Off: answering the hub's reviews needs a cluster-wide SubjectAccessReview grant | Leave profile hardened, then set `hub.url` |
| Anonymous dashboard | Refused | Leave profile hardened, then set `dashboard.auth.allowAnonymous=true` |
| Node agent | Refused: it needs a privileged namespace and cluster-wide reads | Leave profile hardened, then set `nodeAgent.enabled=true` |
| Dex subchart's cluster-scoped RBAC | Refused | Set `dex.rbac.createClusterScoped=false` or `dex.enabled=false`; otherwise leave profile hardened |
| Namespaces the chart creates (node agent, delivery) | None rendered | Leave profile hardened; outside it, `nodeAgent.createNamespace` and `delivery.createNamespace` |
| Pacto delivery | Refused, because its roles are cluster-wide | Leave profile hardened, then set `delivery.enabled=true` or `profile: idp` |
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
