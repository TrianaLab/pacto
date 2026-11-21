# Kargo

Gate every Kargo promotion with one shared `ClusterAnalysisTemplate`. When
Freight lands in a Stage, Kargo runs an AnalysisRun whose Job runs
`pacto impact --running` on the Freight's contract bundle. The Freight
verifies only when no consumer that runs breaks.

**Kargo verifies after it promotes.** A failed verification keeps the Freight
from the next Stage and leaves this Stage as promoted: it does not roll back.
Pacto writes nothing to Kargo, Argo or Git.

## Before you start

- Kargo v1.12 or later, with the Argo Rollouts CRDs and controller: Rollouts
  runs the AnalysisRun Job.
- Each service publishes its contract bundle with `pacto push`, and a Kargo
  Warehouse subscribes to that bundle repository like an image, with the
  default SemVer selection.
- Every cluster Kargo deploys to runs the Pacto operator with
  [the registry value](installation.md#turn-on-the-registry-backed-features),
  so it publishes its signed
  [target snapshot](../../impact-consumers.md#target-snapshot-consumers).

## Install the template

Apply the template once per Kargo installation:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/kargo-cluster-analysis-template.yaml"
```

In each Kargo project namespace, create the `pacto-impact` ServiceAccount,
the `pacto-delivery-trust` Secret and, for a private registry, the
`pacto-registry` Secret:

```bash
kubectl apply -f kargo-cluster-analysis-template.yaml
kubectl -n <project> create serviceaccount pacto-impact
kubectl -n <project> create secret generic pacto-delivery-trust --from-file=trust.yaml
kubectl -n <project> create secret docker-registry pacto-registry \
  --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
```

`trust.yaml` holds one entry per cluster, from its installation key and
scoped to its UID: see
[Target snapshot consumers](../../impact-consumers.md#target-snapshot-consumers).
Pacto hands each registry only its own entry from the mounted Docker config.
The template takes no registry credentials as args and cannot mark a registry
insecure.

## Verify each Stage

Every Stage carries the same stanza. `vars.pactoBundle` is the only
service-specific value, and the Stage already names it for its subscription:

```yaml
spec:
  vars:
    - name: pactoBundle
      value: ghcr.io/acme/contracts/payments
  verification:
    analysisTemplates:
      - name: pacto-impact
        kind: ClusterAnalysisTemplate
    args:
      - name: new
        value: oci://${{ vars.pactoBundle }}@${{ imageFrom(vars.pactoBundle).Digest }}
```

| `pacto impact` | The Freight |
| --- | --- |
| `0` clear | Verified |
| `1` blocked, or could not run | Failed |
| `2` unverified | Failed unless the Stage sets `allow-unverified: "true"` |

That is the policy of the
[pull request check](../../ci.md#on-every-pull-request-who-breaks): an
unverified answer never passes by default.

## What it judges against

Kargo cannot name a Stage's previously verified Freight, so `--running`
judges the Freight against every revision of its service that runs on a known
target, and the worst answer wins. A revision already running with the
Freight's digest is not its own baseline, so Freight that Argo CD has synced
is never judged against itself. With no other revision running, only the
consumers' version ranges speak, and a running consumer that refuses the new
version still blocks. Once every target runs the Freight, the revision it
replaced is no longer observed, so a breaking change that consumers' ranges
accept clears here; only the
[pull request check](../../ci.md#on-every-pull-request-who-breaks), which
runs before promotion, catches it.

## Choose where targets come from

The `targets` arg picks the source:

- **`snapshot`** (default) reads the signed target snapshots in
  `<registry>/pacto-targets` beside the bundle and needs no cluster RBAC. A
  missing, unsigned or stale snapshot makes the answer partial, so unverified.
- **`k8s`** reads the Job's own cluster. Use it only when Kargo and every
  target share this cluster. Apply the reader role and bind it to each
  project's ServiceAccount:

```yaml
--8<-- "tests/acceptance/kind/fixtures/gitops/kargo-impact-rbac.yaml"
```

A ClusterRoleBinding gives that project read access to every namespace's Pacto
CRs and to the names and images of its workloads. The role lists workloads
because an unread workload may run a revision no contract describes, and an
unread revision never clears.

## Read the reason

The AnalysisRun only says the metric failed. The reason is in the Job log:

```console
$ kubectl -n shop get analysisruns.argoproj.io -l kargo.akuity.io/stage=prod
NAME                 STATUS
prod.01jd...         Failed
$ kubectl -n shop logs job/<analysisrun-job>
Impact: payments 1.0.0 -> 2.0.0
...
Running: 1 revision (judged against oci://ghcr.io/acme/contracts/payments:1.0.0@sha256:4f1c...)
...
Release gate: blocked
impact exit 1
```

The `Running:` line names the judged revision by tag and digest.

### Check that it took

```bash
kubectl get clusteranalysistemplates.argoproj.io pacto-impact
```

Then promote Freight whose contract you know is safe and watch its
AnalysisRun reach `Successful`.

## Fallback: a project-local template

A project that cannot use a cluster-scoped template copies the same metric
into a namespaced `AnalysisTemplate` and references it with
`kind: AnalysisTemplate`.

## Limits

- **A ClusterAnalysisTemplate is cluster-wide:** every project that references
  it gets the same exit policy unless its Stage overrides `allow-unverified`.
- **Verification follows promotion.** Gate before a promotion in the pull
  request that publishes the contract.
