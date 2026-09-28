# GitOps promotion gates

Nothing here blocks a deploy. By the time the operator has a verdict the pods
are already serving. What these snippets buy is that **the next step stops**:
the Kustomization goes unready, the dependent one never starts, the Argo
Application goes Degraded. For catching a breaking change *before* it lands, the
tool is [`pacto impact`](../../cli-reference.md) in the pull request.

Neither tool reads `status.contractStatus` on its own, so a Kustomization
holding a `NonCompliant` Pacto reports healthy. Both have an extension point for
exactly this, and the two snippets below use it.

## Flux

Flux decides health with kstatus, which recognises only `Ready`, `Reconciling`
and `Stalled`. Pacto publishes `ContractValid`, `RuntimeObserved` and
`ReadinessSatisfied`, so kstatus discards all three and the object falls through
to `Current`: the gate looks configured and gates nothing.
`spec.healthCheckExprs` (kustomize-controller v1.5.0, flux2 v2.5.0 and later)
replaces the guess with a CEL expression:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/flux-kustomization.yaml"
```

`sourceRef` is whatever you already use. Three things about that snippet, each
checked against `fluxcd/pkg` `runtime/cel/status_evaluator.go`:

- **There is no `inProgress` expression, deliberately.** When no expression
  matches, Flux falls through to in-progress, so an unrecognised verdict holds
  the deploy and times out rather than going falsely green. It fails closed.
- **`Warning` sits in the passing set.** Move that one word to `failed` if you
  want contract warnings to block promotions. That is the whole knob.
- **`timeout` must exceed the operator's stabilization window**, or a real
  violation reaches you as an ambiguous timeout. See [Timing](#timing).

A freshly created Pacto has no `status` and the expression errors while that is
true; Flux treats the error as not-yet-healthy and keeps polling.

That snippet is not an illustration: `tests/acceptance/kind/gitops-flux.sh`
applies **that exact file** to a kind cluster and asserts the dependent
Kustomization never reaches it while the contract is violated.

## Argo CD

Argo picks a health check from a fixed list of built-in kinds, returns nothing
for everything else and the roll-up into Application health ignores that
nothing: a Pacto is not unhealthy to Argo but invisible. A resource health
customization in `argocd-cm` supplies the missing check:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/argocd-cm-pacto-health.yaml"
```

Apply it as a merge patch — `argocd-cm` holds Argo's own configuration and a
plain `kubectl apply` would drop it — and then restart the controller:

```bash
kubectl -n argocd patch configmap argocd-cm --type merge \
  --patch-file argocd-cm-pacto-health.yaml
kubectl -n argocd rollout restart statefulset/argocd-application-controller
```

**The restart is not optional.** The application controller reads health
customizations into its resource cache at startup, and that cached verdict is
what it compares to decide whether a changed object needs re-examining. Until it
has the customization, every verdict the operator writes compares equal to the
last and the Application only catches up on the next periodic resync. On a core
install, without `argocd-server`, a restart is the *only* way in: hot reload of
`argocd-cm` needs `server.secretkey`, which only `argocd-server` creates.

In the Lua, **nothing maps to Argo's `Unknown`** — it ranks worse than
`Degraded` and would mask genuinely broken workloads, so anything unrecognised
becomes `Progressing`. **The string library is also disabled**, so
`string.format` and `s:gsub()` fail at runtime rather than at load.

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
A key that did not take prints `Health script is not configured` instead — and
prints it while **exiting 0**, so read the output, not the exit code. Both
checks read the ConfigMap rather than the controller, so they pass on an
instance that started before the patch.

## Timing

The gate is only as fresh as the verdict behind it, and verdicts do not all land
at the same speed.

| Change | When it becomes `NonCompliant` |
| --- | --- |
| A mismatch — workload, persistence or configuration conformance | The first reconcile after the workload is observed |
| An absence — a missing interface, capability, dependency, Secret or ConfigMap | After the [stabilization window](limitations.md#stabilization-delay) (default two minutes) plus one requeue interval |

That split is why `timeout` has a floor: five minutes against the default
two-minute window leaves room for the window, one requeue and the apply.

Argo re-examines a Pacto when its health status *changes*, so `Healthy` to
`Degraded` shows up in about a second. But one `NonCompliant` reason replaced by
another keeps the stale message until the next periodic resync. The red dot
is prompt; the wording is not.

## Limits

- **Argo health customizations are instance-global.** One Argo serving several
  teams cannot give one a blocking `Warning` and another a passing one.
- **Neither tool can refuse an artifact for what is inside it.** "Reject this
  because its contract breaks three consumers" is a pull-request check.
- **The verdict is about the contract, not the rollout.** `Compliant` says the
  workload matches its contract, nothing about request errors or saturation.
- **`status.lastReconciledAt` cannot gate on freshness.** Neither tool hands the
  expression a clock.
