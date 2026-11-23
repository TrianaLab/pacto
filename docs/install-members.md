# Install members

This page describes how a hub lists the clusters its Argo CD, Flux, Cluster
API, OCM or Rancher already know, and how to install the Pacto chart on every
one of them through Argo CD or Flux. Each installed cluster then joins as a
pending [member](glossary.md#member) and waits for a person's approval, as
[Member clusters](member-clusters.md) describes. Pacto writes nothing to Git
and installs nothing itself: the engine applies the files you hand it.

## Grant the reads

A person grants the reads with one `helm upgrade` on the hub's operator. The
Cluster inventory row on Data sources proposes this command with the
namespaces the hub's Argo CD and Flux use:

```shell
helm upgrade pacto-operator oci://ghcr.io/trianalab/pacto/charts/pacto-operator \
  --namespace pacto-operator-system --reset-then-reuse-values \
  --set clusterInventory.enabled=true \
  --set 'clusterInventory.secretNamespaces={argocd,flux-system}'
```

The grant lets the operator's ServiceAccount list the Secrets in each named
namespace. Pacto requests their metadata only, never their data, and no
ClusterRole grants Secrets.

- `clusterInventory.secretNamespaces` renders a Role with `list` on `secrets`
  and its RoleBinding in each namespace, for Argo CD cluster Secrets and Flux
  kubeconfig Secrets.
- `clusterInventory.enabled` adds `list` on Cluster API clusters, OCM managed
  clusters and Rancher clusters, each only when the cluster serves that API.

A source the operator may not list reads `unknown` with the reason.

## Read candidate clusters

A candidate cluster is a cluster one of those tools knows. On Data sources,
the Clusters block shows them under **Candidate clusters**, grouped by source,
with their labels. The same list comes from the CLI and the MCP tool
`pacto_cluster_list`:

```sh
pacto cluster list --detected k8s:pacto-operator-system/pacto-operator-detected
```

Candidates show only to people who may approve clusterjoins; others see how
many are hidden. A candidate never joins on its own and never counts as a
member. Its environment labels (`env`, `environment` or
`delivery.pacto.trianalab.io/environment`) appear on a matched member as
inferred inventory environments, naming the label and the object they came
from. They never reach a gate or a placement.

## State a cluster's UID

A candidate matches a member only by the cluster UID its object states. Names
never match: a display name or a Secret name is no identity, and a member can
publish any name. To state a UID, annotate the candidate's Secret or cluster
object with the member's `kube-system` namespace UID:

```sh
kubectl --context eu-prod get namespace kube-system -o jsonpath='{.metadata.uid}'
kubectl -n argocd annotate secret eu-prod pacto.trianalab.io/cluster-uid=9f1c4e2a-0000-4000-8000-000000000001
```

An OCM cluster claim `id.k8s.io` holding a UUID states one too. A candidate
matches only an approved member whose snapshot names the key a person
approved. A pending or removed member with that UID reads "states the same
UID": the cluster reports its UID about itself, and nobody approved it under
that key yet. Every other candidate reads "not matched", with the reason in
the tooltip.

## Install through Argo CD

```sh
pacto cluster install --from argocd --detected k8s:pacto-operator-system/pacto-operator-detected > pacto-members.yaml
```

The output holds an ApplicationSet over every Argo CD cluster Secret and the
Helm repository Secret for the chart. Its values are `registry`, plus
`hub.url` when you pass `--hub-url`. The chart version is the one the hub's
operator runs; `--chart-version` and `--chart` pick another one or a mirror.
On Data sources, **Get ApplicationSet** downloads the same files. The
download sets no `hub.url`: add it with `--hub-url` from the CLI.

The ApplicationSet skips the `https://kubernetes.default.svc` cluster. Before
you apply it, label the hub's own cluster Secret, and the Secret of every
cluster that already runs Pacto, so that no cluster gets a second release:

```sh
kubectl -n argocd label secret hub pacto.trianalab.io/install=false
```

The command warns about each candidate that states the UID of a member.

## Install through Flux

```sh
pacto cluster install --from flux --detected k8s:pacto-operator-system/pacto-operator-detected > pacto-members.yaml
```

The output holds, per namespace, one OCIRepository for the chart and one
HelmRelease for each kubeconfig ref the hub's Kustomizations and HelmReleases
name in `spec.kubeConfig`. Apply the files on the hub, or push them and apply
the bootstrap that pins them. On Data sources, **Get Flux manifests**
downloads the same files.

## Give each member a push credential

A member publishes its snapshot once it holds a credential that can push to
`pacto-targets` on your registry. The generated file says so in a comment.
Create the Secret in namespace `pacto` of each member before the engine
syncs:

```sh
kubectl --context eu-prod create namespace pacto
kubectl --context eu-prod -n pacto create secret docker-registry pacto-registry \
  --docker-server=ghcr.io --docker-username=eu-prod --docker-password="$TOKEN"
```

Give it push without delete, as
[the registry boundary](member-clusters.md#the-registry-boundary) explains,
and name it through the engine's values: under `helm.valuesObject` of the
ApplicationSet template, or under `values` of each HelmRelease:

```yaml
registryPullSecret: pacto/pacto-registry
```

## A private CA or a local registry

A member behind a private CA needs `hub.caConfigMap`, set through the same
values beside `hub.url`. The generated install never sets it, since the
ConfigMap does not exist in a new member.

For a local registry over plain HTTP, pass `--insecure-registry`. It adds
`insecureRegistries` with the registry host to the values and lets Argo CD or
Flux pull over HTTP. Use it for a local registry only.

## Push the artifact and pin its digest

```sh
pacto cluster install --from flux --detected k8s:pacto-operator-system/pacto-operator-detected --push > bootstrap.yaml
```

`--push` writes the files as one OCI artifact to `pacto-targets` with your
own registry credentials, tagged `members-flux-` and the start of its digest,
and prints the digest. For Flux it prints a bootstrap OCIRepository and
Kustomization that pin that digest; `--digest` pins an artifact pushed
earlier. For Argo CD, point an Application at the printed reference with the
digest as its revision.

Every member can push to `pacto-targets`, so a member can move any tag there,
and no generated object follows a tag. Every member can also read the
artifact, which names the hub's GitOps namespaces and kubeconfig Secrets. It
is unsigned, and the push is optional.

## Removal leaves Pacto running

Removal orphans by default. Deleting the ApplicationSet or the bootstrap
Kustomization leaves Pacto running on every member. Flux uninstalls the
release of a HelmRelease you delete, so suspend it first with
`flux suspend helmrelease`. To remove Pacto from a member on purpose, run
`helm uninstall pacto -n pacto` there, then
`pacto cluster remove` on the hub.

## Cluster API, OCM and Rancher clusters

Pacto generates no install for these sources. Register their clusters in
Argo CD or Flux, the way you register any cluster, and they show as Argo CD
or Flux candidates. A Flux Kustomization or HelmRelease can name a Cluster API
kubeconfig Secret in `spec.kubeConfig`. A Rancher cluster shows under its
object name; Pacto does not read its display name.
