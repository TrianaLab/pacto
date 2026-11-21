# GitOps promotion gates

Make Flux or Argo CD stop the next rollout step when a service breaks its
contract. For Kargo, see [Kargo](gitops-kargo.md#kargo). A `NonCompliant`
Pacto turns the Kustomization unready or the Application Degraded.

The health snippets stop **the next step**: by the time the operator has a
verdict the pods are already serving. The [Argo CD plugin](#gate-every-argo-cd-sync)
stops **the render**, before Argo CD applies anything. With Flux, catch a
breaking change in the pull request with [`pacto impact`](../../cli-reference.md).

Flux and Argo CD read `status.contractStatus` only through an extension point,
which the two health snippets below configure.

## Flux

Flux decides health with kstatus, its built-in status reader, which recognises
only the `Ready`, `Reconciling` and `Stalled` conditions. Pacto publishes `ContractValid`, `RuntimeObserved` and
`ReadinessSatisfied`, so kstatus discards all three and the object falls through
to `Current`: the gate looks configured and passes everything.
`spec.healthCheckExprs` (kustomize-controller v1.5.0, flux2 v2.5.0 and later)
replaces that fallback with a CEL expression:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/flux-kustomization.yaml"
```

`sourceRef` is whatever you already use. Three things about that snippet, each
checked against `fluxcd/pkg` `runtime/cel/status_evaluator.go`:

- **The snippet leaves out `inProgress` on purpose.** When no expression
  matches, Flux falls through to in-progress, so an unrecognised verdict holds
  the deploy until it times out. It fails closed.
- **`Warning` sits in the passing set.** Move that one word to `failed` if you
  want contract warnings to block promotions. That is the whole knob.
- **`timeout` must exceed the operator's stabilization window**, or a real
  violation reaches you as an ambiguous timeout. See [Timing](#timing).

A freshly created Pacto has no `status` and the expression errors while that is
true; Flux treats the error as not-yet-healthy and keeps polling.

`tests/acceptance/kind/gitops-flux.sh` applies **that exact file** to a kind cluster and asserts the dependent
Kustomization never reaches it while the contract is violated.

## Argo CD

Argo picks a health check from a fixed list of built-in kinds and returns no
health for any other kind. The roll-up into Application health skips a resource
with no health, so Argo ignores a Pacto. A resource health customization in
`argocd-cm` supplies the missing check:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/argocd-cm-pacto-health.yaml"
```

Apply it as a merge patch, because `argocd-cm` holds Argo's own configuration
and a plain `kubectl apply` would drop it. Then restart the controller:

```bash
kubectl -n argocd patch configmap argocd-cm --type merge \
  --patch-file argocd-cm-pacto-health.yaml
kubectl -n argocd rollout restart statefulset/argocd-application-controller
```

**The restart is required.** The application controller reads health
customizations into its resource cache at startup, and that cached verdict is
what it compares to decide whether a changed object needs re-examining. Until it
has the customization, every verdict the operator writes compares equal to the
last and the Application only catches up on the next periodic resync. On a core
install, without `argocd-server`, a restart is the *only* way in: hot reload of
`argocd-cm` needs `server.secretkey`, which only `argocd-server` creates.

In the Lua, **anything unrecognised becomes `Progressing`**. Argo's `Unknown`
ranks worse than `Degraded` and would mask broken workloads, so the script never
returns it. **The string library is also disabled**, so
`string.format` and `s:gsub()` fail at runtime, after the script loads.

`tests/acceptance/kind/gitops-argocd.sh` runs **that exact file** through the
Lua sandbox with no cluster, and then inside a kind cluster where an Application
must go `Degraded` naming the finding and back to `Healthy` once corrected.

### Check that it took

Argo ignores a `data` key it does not recognise, and an ignored key looks
exactly like having configured nothing:

```bash
# The key must read back exactly, group and kind included.
kubectl -n argocd get cm argocd-cm \
  -o jsonpath='{.data.resource\.customizations\.health\.pacto\.trianalab\.io_Pacto}'
```

Then ask Argo what it makes of a real object. `argocd admin settings` evaluates
the customization against files on disk, in the same Lua sandbox the controller
uses, so it answers without waiting for a sync:

```bash
kubectl -n argocd get cm argocd-cm -o yaml > /tmp/argocd-cm.yaml
kubectl -n <namespace> get pacto <name> -o yaml > /tmp/pacto.yaml

argocd admin settings resource-overrides health /tmp/pacto.yaml \
  --argocd-cm-path /tmp/argocd-cm.yaml
```

A `Compliant` Pacto prints `STATUS: Healthy` and `MESSAGE: contract satisfied`.
A key that did not take prints `Health script is not configured` while
**exiting 0**, so read the output and ignore the exit code. Both checks read the
ConfigMap, so they also pass on a controller that started before the patch.

## Gate every Argo CD sync

A Config Management Plugin sidecar on `argocd-repo-server` gates every
auto-detected Application whose folder holds a contract `pacto.yaml`. It
renders the folder with `helm template` or `kustomize build`, then runs
`pacto impact --running` on the rendered Pacto object's `contractRef`, or on
`pacto.yaml` when the render holds none. That judges the contract against the
revisions that run on known targets, read from the signed
[target snapshots](../../impact-consumers.md#target-snapshot-consumers).
Argo CD receives the manifests only after a pass.

An Application that sets `spec.source.helm`, `kustomize` or `directory` skips
plugin discovery, so its editors can opt out: restrict who edits Applications
with AppProjects and Argo CD RBAC.

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/argocd-cmp-pacto.yaml"
```

The patch adds no ServiceAccount, Role or binding:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/argocd-repo-server-pacto-patch.yaml"
```

```bash
kubectl apply -f argocd-cmp-pacto.yaml
kubectl -n argocd create configmap pacto-trust --from-file=trust.yaml
kubectl -n argocd patch deployment argocd-repo-server \
  --patch-file argocd-repo-server-pacto-patch.yaml
```

`tests/acceptance/gitopsplugin` and `tests/acceptance/kind/gitops-argocd-sync.sh`
run **that exact script**.

### Point it at your clusters

Change one line: `PACTO_TARGET_SNAPSHOT` holds one
`oci://<registry>/pacto-targets:cluster-<uid>` word per destination cluster,
published by each cluster's operator (see
[Target snapshot](delivery-records.md#target-snapshot)). A missing tag reads
partial. An empty value, or a word without `:cluster-`, is refused unless
`PACTO_ALLOW_CLUSTER_DISCOVERY=true`.

`trust.yaml` holds one entry per cluster, from its installation key and scoped
to its UID: see [Target snapshot consumers](../../impact-consumers.md#target-snapshot-consumers)
and [the registry value](installation.md#turn-on-the-registry-backed-features).

For registry reads, mount a host-keyed Docker config and set `DOCKER_CONFIG`,
or use workload identity. `PACTO_REGISTRY_*` sends its credentials to every
host Pacto pulls from; the plugin unsets it before Helm or Kustomize. Running
revisions are pulled only from the snapshot hosts and an `oci://`
`contractRef`'s host, plus the comma-separated `PACTO_RUNNING_REGISTRIES`;
any other host makes the answer unverified. The plugin reads Pacto settings
from `/etc/pacto/config.yaml` in the trust ConfigMap, if present, and never
from the folder's `pacto.yaml`, which is the contract.

### Exit codes

| `pacto impact` | The render | Message |
| --- | --- | --- |
| `0` clear | Passes | |
| `1` blocked | Fails | `pacto impact: BLOCKED` |
| `1`, could not run | Fails | `pacto impact: ERROR` |
| `2` unverified | Fails unless allowed | `pacto impact: UNVERIFIED` |

A rendered `contractRef` whose tag disagrees with `service.version` fails
with `pacto plugin: ERROR`; a digest `contractRef` has no tag to compare and is
judged as rendered. `PACTO_ALLOW_UNVERIFIED` decides exit `2`: `false`
(default, also any unknown value) fails, `true` renders, `per-application`
renders only for an Application with
`spec.source.plugin.env: [{name: PACTO_ALLOW_UNVERIFIED, value: "true"}]`.

### What the message shows

```console
$ argocd app get payments
CONDITION        MESSAGE
ComparisonError  ... plugin pacto: pacto impact: BLOCKED (the reason is in the pacto container log of argocd-repo-server), exit 1
```

Core installs read the Application's `ComparisonError` condition. The full
reason is in the `pacto` container log (`PACTO_PLUGIN_LOG` names another
file). The plugin judges whatever service the folder or its rendered Pacto
object names, so anyone who can write to a repository the plugin renders
could otherwise read consumer names, clusters and refs of any service in the
snapshot through their own Application. With `PACTO_SHOW_REASON=true` the
message carries the first line of the reason, cut at 512 bytes: set it only
when every writer of those repositories may read the snapshot. Either way,
Application readers see the verdict.

### Before you install

- `spec.source.helm` values and parameters skip the plugin; keep values in
  the chart folder.
- Copy `argocd-cm` `kustomize.buildOptions` into `PACTO_KUSTOMIZE_BUILD_OPTIONS`.
- A folder with no `Chart.yaml` or Kustomization fails the render.

### Fallback: a PreSync Job per Application

An Application with an explicit source type can run this hook on its
published bundle, one Job each; exit 1 or 2 fails the sync:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/argocd-presync-pacto-impact.yaml"
```

## Kargo

Kargo verifies Freight after each promotion. One shared
`ClusterAnalysisTemplate` runs `pacto impact --running` on the Freight's
contract bundle in every Stage: see [Kargo](gitops-kargo.md#kargo).

## Timing

The gate is as fresh as the verdict behind it, and verdicts land at different
speeds.

| Change | When it becomes `NonCompliant` |
| --- | --- |
| A mismatch: workload, persistence or configuration conformance | The first reconcile after the workload is observed |
| An absence: a missing interface, capability, dependency, Secret or ConfigMap | After the [stabilization window](limitations.md#stabilization-delay) (default two minutes) plus one requeue interval |

That split is why `timeout` has a floor: five minutes against the default
two-minute window leaves room for the window, one requeue and the apply.

Argo re-examines a Pacto when its health status *changes*, so `Healthy` to
`Degraded` shows up in about a second. But one `NonCompliant` reason replaced by
another keeps the stale message until the next periodic resync. The status
changes at once and the message catches up later.

## Limits

- **Argo health customizations are instance-global.** One Argo serving several
  teams cannot give one a blocking `Warning` and another a passing one.
- **Flux cannot refuse an artifact for what is inside it.** There, "reject
  this because it breaks three consumers" is a pull-request check.
- **The plugin gates discovered Applications only**, and judges at render
  time: a consumer deployed later counts at the next render or hard refresh.
- **`ARGOCD_EXEC_TIMEOUT`** (default 90 seconds) bounds the render and
  `pacto impact` together; each snapshot read stops at 20 seconds.
- **Each destination cluster is pinned by hand** in `PACTO_TARGET_SNAPSHOT`.
- **The verdict is about the contract.** `Compliant` says the workload matches
  its contract. It says nothing about request errors or saturation.
- **`status.lastReconciledAt` cannot gate on freshness.** Neither tool hands the
  expression a clock.
