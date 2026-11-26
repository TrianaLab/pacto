# What Pacto detects

On start and every 5 minutes the operator reads what the cluster provides
and writes it to the `<release>-detected` ConfigMap in its namespace, such as
`pacto-operator-detected`. Each row is inferred and carries its source and
when it was read. Nothing is turned on:
detection changes no argument, rule or feature.

| Row | Read from | Example |
|---|---|---|
| Registry | Running pods, one vote per namespace | `ghcr.io/acme in 9 of 11 namespaces, 87% of workloads` |
| Cloud | Node `providerID` and labels | `GKE on 6 of 6 nodes` |
| Sign-in | API discovery | `Dex serves dex.coreos.com/v1` |
| Exposure | HTTPRoutes and Ingresses, one vote per namespace | `Gateway API with HTTPRoutes in 4 of 5 namespaces` |
| Metrics | API discovery | `Prometheus Operator serves monitoring.coreos.com/v1` |
| Nodes | Node kernels | `kernel 6.1 on 6 of 6 with BTF likely` |
| Istio egress and Cilium FQDN | API discovery; see [Calls that leave the cluster](../../operational-graph-egress.md) | `ServiceEntry CRD present` |
| Egress gateway | Pods labelled `istio=egressgateway` in `istio-system`, only when found | `1 Istio egress gateway found; logs: auto` |
| OTel Collector, Tempo, Jaeger and Datadog | Running pods and Services, only when found; see [Trace backends](../../operational-graph-providers.md#trace-backends) | `query API on port 16686` |
| Managed resources | Crossplane, ACK, Config Connector and ASO objects of the external catalog's kinds; see [Managed resources](#managed-resources) | `14 managed resources` |
| Terraform state | The states `controller.terraformStates` lists, only when set | `2 listed states read` |
| Cloud inventory | The cloud `controller.cloudInventory` names, only with a workload identity | `aws` |
| Backstage | A running Backstage image and a Service on port 7007 in its namespace; see [A detected catalog](../../backstage-catalog.md#a-detected-catalog). With no Backstage image in a watched namespace the row reads `not detected`, partial, because an external Backstage is invisible to the cluster | `Backstage` |

The rows show under Data sources in the dashboard, in `helm upgrade` notes
and in the CLI:

```bash
pacto fleet status --k8s --detected k8s:pacto-operator-system/pacto-operator-detected
```

A registry or exposure row prints a command to review only when one
candidate holds most namespaces and leads every other one. Docker Hub
official images are never proposed as a push target. BTF reads likely,
unlikely or unknown and is never confirmed. A missing or stale detection shows on its own row and never
makes an answer partial.

With `controller.watchNamespaces` the registry row is partial and names the
watched namespaces, and cloud and nodes are unknown. Beyond the ConfigMap it
writes and the operator Deployment that owns it, the only grant detection adds
is `nodes list` in the cluster-wide ClusterRole; see
[Run with least privilege](least-privilege.md#what-detection-reads-and-writes).

## Managed resources

The operator lists the objects of the `crossplane` kinds of the
[default external catalog](../../external-catalog-reference.md#the-default-catalog)
and writes their names, verified claim and engine to the ConfigMap, at most
500 per cluster; the fleet shows them as
[managed resources](../../inferred-contracts.md#managed-resources). The chart
grants `list`, and no other verb, on those kinds' plurals in each catalog group
the cluster serves when `helm` runs. It never grants a group by suffix, so a
kind that holds key material, such as Config Connector's
`IAMServiceAccountKey`, is never read. When the cluster serves
`apiextensions.crossplane.io/v1`, the chart also grants `list` on the XRDs and
on the composite and claim plurals of each XRD it can look up. Under
`helm template` the lookup is empty and the row names the claim kinds it
cannot list. A claim label counts only when the composite's `spec.claimRef`
names that claim. A group the cluster serves after install reads `unread` and
raises `MANAGED_GROUP_UNREAD` until you run:

```bash
helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator --version <version> -n <namespace> --reset-then-reuse-values
```

### Terraform state and cloud inventory

The operator reads Terraform and OpenTofu states and the cloud inventory only
when you name them, and only through the workload identity on
`serviceAccount.annotations`, never a node identity:

```yaml
controller:
  terraformStates:
    - s3://acme-tf/prod/terraform.tfstate?region=eu-west-1
    - gs://acme-tf/prod/default.tfstate
    - azurerm://acmetf/tfstate/prod.terraform.tfstate
  cloudInventory: aws  # aws, gcp, azure or "off" (the default)
```

From a state it keeps the resource type, id, region and engine, and no other
attribute value. Repository discovery reports an `s3`, `gcs` or `azurerm` backend block as the
finding `TERRAFORM_BACKEND`, and the operator reads that state once you list it.
The same identity often pulls from the registry, so `cloudInventory` stays
`"off"` until you name the cloud. Bind the identity a read-only role:

| Cloud | Annotation | Read-only grants |
|---|---|---|
| AWS | `eks.amazonaws.com/role-arn`, or EKS Pod Identity | `resource-explorer-2:Search` on the default view of `AWS_REGION`, `s3:GetObject` on the listed states |
| Google Cloud | `iam.gke.io/gcp-service-account` | `roles/cloudasset.viewer` on the service account's project, `roles/storage.objectViewer` on the state bucket |
| Azure | `azure.workload.identity/client-id` | `Reader` on the subscriptions to read, `Storage Blob Data Reader` on the state container |

On Google Cloud the operator refuses to read when the metadata server answers
with a service account other than the annotated one.

A key the [Evidence Server](evidence-server.md) accepts `ResourceProvisioned`
from must set `allowedProviderRepos` to the offering provider's repository, so
a consumer team's own key cannot put its resource through an offering. The
ConfigMap holds the inventory for anyone who may read ConfigMaps in the
operator namespace, so keep that namespace admin-only.
