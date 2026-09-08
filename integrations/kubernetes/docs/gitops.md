# GitOps promotion gates

The operator reaches a verdict on every reconcile and writes it to
`status.contractStatus`. Neither Flux nor Argo CD reads that field on its own, so
a Kustomization holding a `NonCompliant` Pacto reports healthy and a violated
contract cannot turn an Argo Application red. Both tools have an extension point
for exactly this. This page is the two snippets that close the gap.

Nothing here changes the operator, the CRD or your contracts. It is configuration
you add to the delivery tool you already run.

## What the gate buys you

The operator observes; it never writes to your workloads. By the time a verdict
exists the pods are already serving. What these snippets buy is that **the next
step stops**: the Kustomization goes unready, the dependent one never starts, the
Argo Application goes Degraded and whatever alerting you already point at
unhealthy resources picks it up.

For catching a breaking change *before* it lands, the tool is
[`pacto impact`](../../cli-reference.md) in the pull request. A promotion gate is
the second line, not the first.

## Flux

Flux decides whether a resource is healthy using kstatus, which recognises three
condition names: `Ready`, `Reconciling` and `Stalled`. Pacto publishes
`ContractValid`, `RuntimeObserved` and `ReadinessSatisfied`, so kstatus discards
all three unread and the object falls through to `Current`. The gate looks
configured and gates nothing.

`spec.healthCheckExprs` (kustomize-controller v1.5.0, flux2 v2.5.0 and later)
replaces the guess with a CEL expression:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/flux-kustomization.yaml"
```

`sourceRef` is whatever you already use — `GitRepository`, `OCIRepository` or
`Bucket`. The gate does not care where the manifests came from, only what the
operator says about them once they are applied.

Four things about that snippet are worth knowing, each checked against
`fluxcd/pkg` `runtime/cel/status_evaluator.go`:

- **There is no `inProgress` expression, deliberately.** When no expression
  matches, Flux falls through to in-progress. So `Unknown`, `NotEvaluated` and
  any verdict added in a later release hold the deploy and time out rather than
  going falsely green. The gate fails closed.
- **There is no hand-written generation guard, deliberately.** Flux already
  compares `status.observedGeneration` to `metadata.generation` before any
  expression runs. Writing your own is redundant, and it throws on an object that
  has no status yet.
- **`Warning` sits in the passing set.** Move that one word to `failed` if you
  want contract warnings to block promotions. That is the whole knob.
- **`timeout` must exceed the operator's stabilization window.** Set it lower and
  a real violation reaches you as an ambiguous timeout instead of a clean
  failure. See [Timing](#timing) below.

A Pacto that has just been created has no `status` at all, and the expression
errors while that is true. Flux treats the error as not-yet-healthy and keeps
polling, so the first few seconds of a fresh apply are noisy in the logs and
harmless in the outcome.

That snippet is not an illustration. `tests/acceptance/kind/gitops-flux.sh`
applies **that exact file** to a kind cluster running Flux, publishes a contract
that contradicts the workload that ships, and asserts the dependent
Kustomization's own manifest never reaches the cluster — then corrects the
contract and asserts it does.

`HelmRelease` gained the same field in helm-controller v1.5.0 / flux2 v2.8.0. The
snippet above should transfer unchanged, but it is not covered here.

## Argo CD

Argo picks a health check from a fixed list of built-in kinds and returns nothing
for everything else, and the roll-up that turns resource health into Application
health starts at Healthy and ignores that nothing. A Pacto object is therefore
not unhealthy to Argo — it is invisible, and nothing reports that a check is
missing.

A resource health customization in `argocd-cm` supplies the missing check:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/argocd-cm-pacto-health.yaml"
```

Apply it as a merge patch. `argocd-cm` holds Argo's own configuration and a plain
`kubectl apply` of the manifest above would drop it:

```bash
kubectl -n argocd patch configmap argocd-cm --type merge \
  --patch-file argocd-cm-pacto-health.yaml
```

- **Argo has no built-in generation check**, so the script does its own. Be
  precise about what that proves: `observedGeneration` here is the Pacto object's
  own generation, so it says the operator has seen the current *contract* — not
  the current workload.
- **The findings loop puts the reason in the Argo UI** instead of a bare red dot.
- **Nothing maps to Argo's `Unknown`.** It ranks worse than `Degraded` in the
  roll-up, so it would mask genuinely broken workloads in the same Application,
  and it does not fire the on-degraded trigger. Anything unrecognised becomes
  `Progressing` and times out.
- **The Lua runs with the string library disabled.** Concatenation, comparison,
  `ipairs` and `tostring()` are available; `string.format`, `s:gsub()` and the
  rest are not, and reaching for one fails at runtime rather than at load.

That snippet is not an illustration either. `tests/acceptance/kind/gitops-argocd.sh`
runs **that exact file** twice. Once with no cluster at all, through
`argocd admin settings resource-overrides health`, which puts every contract
status through Argo's own Lua sandbox — including the states a running cluster
passes through too quickly to catch, like a verdict that has not caught up with
the contract yet. Then inside a kind cluster running Argo CD, where an
Application must go `Degraded` naming the finding while the contract is violated
and back to `Healthy` once it is corrected.

### Check that it took

Argo ignores a `data` key it does not recognise, and an ignored key looks exactly
like having configured nothing. Confirm the customization is live before you rely
on it:

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
A key that did not take prints `Health script is not configured for
'pacto.trianalab.io/Pacto'` instead — and prints it while **exiting 0**, so read
the output rather than the exit code if you wire this into a check.

## Timing

The gate is only as fresh as the verdict behind it, and verdicts do not all land
at the same speed.

| Change | When it becomes `NonCompliant` |
| --- | --- |
| A mismatch — workload, persistence or configuration conformance | The first reconcile after the workload is observed |
| An absence — a missing interface, capability, dependency, Secret or ConfigMap | After the [stabilization window](limitations.md#stabilization-delay) (default two minutes) plus one requeue interval |

That split is why `timeout` has a floor. A five-minute timeout against the
default two-minute window leaves room for the window, one requeue and the apply
itself.

There is one gap worth naming. kstatus will not call a Deployment current until
its controller writes `observedGeneration` back, and that same write is the watch
event that queues the Pacto reconcile. So the re-check is guaranteed *queued*
before Flux can first see the workload as current. It is not guaranteed
*finished*. The gap is one reconcile.

## Limits

- **Argo health customizations are instance-global.** They live in `argocd-cm`,
  so one Argo serving several teams cannot give one team a blocking `Warning` and
  another a passing one.
- **Neither tool can refuse an artifact for what is inside it.** Flux's only
  pre-apply gate is a signature check. "Reject this because its contract breaks
  three consumers" is not something a health gate can express — that is a pull
  request check.
- **The verdict is about the contract, not the rollout.** A Pacto reporting
  `Compliant` says the running workload matches its declared contract. It says
  nothing about request errors, saturation or anything else your normal
  progressive-delivery signals cover.
- **`status.lastReconciledAt` cannot be used as a freshness gate.** Flux hands
  the expression the custom resource and nothing else; Argo hands the Lua only
  `obj`. Neither has a clock to compare it against.

## Related

- [Troubleshooting](troubleshooting.md#reading-the-events) — the events the
  operator emits when a verdict changes, and why a `Count` above 1 means the
  status actually flapped.
- [Limitations](limitations.md) — what the operator declines to judge, and why
  those cases read `Unknown` rather than `NonCompliant`.
- [CRD reference](crd-reference.md) — the full `status` schema the expressions
  above read from.
