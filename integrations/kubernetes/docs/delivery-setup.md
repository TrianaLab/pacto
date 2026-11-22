# Delivery reference: setup and gates

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

Reference for [Pacto delivery](delivery.md). To pactify a service end to end,
follow [Pactify a service](../../pactify.md) instead. The API group is
`delivery.pacto.trianalab.io/v1alpha1` (alpha).
Plans, approvals and who may do what are in
[Delivery reference: plan, propose and approve](delivery-operations.md).

## Registry

The two automatic levels need [signed records](delivery-records.md#install),
and records work on any OCI registry. The recorder probes the streams
repository: where it serves the OCI Referrers API, records are referrers;
where it does not, as on GHCR and CNCF distribution, they are
[write-once tags](delivery-records.md#write-once-tags) in the streams
repository.

- The recorder chooses the mode by itself, and a Stage that recorded
  `native` never narrows to tags.
- With tags, records attached to versions cannot be listed, so what a plan
  says a version did on other Stages stays partial.
- Without the Referrers API, `pacto publish` also gives each signature and
  SBOM a write-once `pacto-sig-<subject hex>-<12 hex>` tag, and Pacto reads
  only those, never the shared `sha256-<hex>` tag.
- Only the recorder pushes to the streams repository. People who approve
  from the CLI need push only on the approvals repository
  (`delivery.recorder.approvalsRepository`, default
  `<streamsRepository>-approvals`). The recorder also pushes there, once
  `delivery.identities.signers` lists a signer. The promoter's
  `delivery.promoter.registryCredentials` need pull there: an approval record
  it cannot read does not count.

## Install

Delivery is the `pacto-operator` chart's `delivery` section, off by default. Turn it on:

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

- `delivery.flux.enabled` and `delivery.argocd.enabled` default to `auto`: the
  chart grants the promoter access to Flux and Argo CD when the cluster serves
  their APIs at install or upgrade. Install Flux or Argo CD before Pacto, or
  run `helm upgrade` after; with `helm template`, pass their APIs with
  `--api-versions`. `true` or `false` decide regardless of the API.
- The promoter moves versions through the manager that already applies each
  workload ([which manager applies a Stage](delivery-reach.md#which-manager-applies-a-stage)).
  The first Stage of a service admits new versions through the promoter's
  registry watch (`spec.candidates.registry`, judged against
  `delivery.publishers`) or a Flux `OCIRepository` with `spec.verify`. A
  cluster that runs only Argo CD uses the registry watch and needs no Flux.

It needs no identity values: whoever may update a Stage's workload decides
([who may decide](delivery-identity.md#who-may-decide)).

If you set `crds.install=false` to manage the CRDs yourself, apply the
delivery CRDs before you turn delivery on. The promoter and the recorder do not
start without them:

--8<-- "integrations/kubernetes/docs/generated/_delivery-crd-apply.md"

This needs Helm 3.14 or later. Delivery runs in `delivery.namespace`
(`pacto-delivery`, never the release or node agent namespace), where the operator holds no
role, so it cannot act as the promoter. It reads its Secrets only when
[watched or granted](least-privilege.md): prefer KMS or keyless recorder
signing. Set `delivery.createNamespace=false` to
create the namespace, with the recorder key, yourself. Bind
`pacto-operator-delivery-agent` to agents. People with `edit` or `admin` in a
namespace may already decide there. The image is
[signed](artifact-hub.md).

The admission policies make the actor of an Operation and the person behind
an Approval true, so the chart always installs them with delivery and refuses
Kubernetes below 1.30 and a cluster without the `ValidatingAdmissionPolicy`
API. With `helm template`, pass
`--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy`,
or set `delivery.admissionPolicies.skipCapabilityCheck=true` to skip only that
check. While a policy the chart rendered is missing or weakened, the promoter
admits no operation, counts no approval and shows `Unguarded`
([when a policy is missing](delivery-security.md#when-a-policy-is-missing)).

To run an unreleased build, from the repository root:

```bash
docker build -f integrations/kubernetes/Dockerfile --target delivery --build-arg CMD=cmd/delivery \
  -t registry.example.com/pacto-delivery:dev .
helm upgrade --install pacto-operator integrations/kubernetes/charts/pacto-operator \
  -n pacto-operator-system --create-namespace --set delivery.enabled=true \
  --set delivery.image.registry=registry.example.com --set delivery.image.repository=pacto-delivery \
  --set delivery.image.tag=dev
```

The repository of [delivery run records](delivery-run-records.md) must be private.

## Metrics and Events

The promoter and the recorder serve Prometheus metrics over plain HTTP on
port 8080. The promoter's are behind the `<release>-delivery-metrics` Service; set
`delivery.metrics.serviceMonitor.enabled=true` to create a Prometheus Operator
ServiceMonitor for it. `delivery.recorder.monitoring.enabled=true` gives the
recorder's their own Service, ServiceMonitor and
[alerts](delivery-records.md).

| Metric | What it counts |
|--------|----------------|
| `pacto_delivery_operations_total` | concluded operations by `type`, `outcome`, `verification` and `actor_class`: how much shipped with no person, and how often a person accepted an unknown |
| `pacto_delivery_verdicts_total` | gate verdicts of concluded operations and re-checks, by `gate`, `phase` and `result` |
| `pacto_delivery_stage_frozen` | 1 while a Stage is frozen |
| `pacto_delivery_stage_watched` | 0 while the newest re-check of the running version did not pass |
| `pacto_delivery_records_pending` | concluded operations per stream that have no record yet (recorder) |
| `pacto_delivery_unverified_approval_records` | approval records in an approvals repository that do not verify, each counted once; the recorder never turns them into an Approval (recorder) |
| `pacto_delivery_stats_dropped_total` | [run records](delivery-run-records.md) not published, by `reason`: `queue-full`, `namespace-full` (one namespace already holds a quarter of the queue), `publish-failed` (logged with the operation, and the record in flight when the promoter stops), `no-stage` (the operation had no Stage, so no service to file it under) or `shutdown` (still queued, or concluded, after the promoter began to stop). Every reason reads 0 from startup. A `shutdown` drop is counted by a process about to exit, so its lasting trace is the promoter's log line `run records not published at shutdown` |

The promoter emits a Kubernetes Event on an operation when it waits on a person
(`Held`, with `TeamNarrowingOff` while owner teams do not narrow who may
answer) and when it concludes (its outcome). It emits one on a Stage when it
raises a rollback (`RollbackRaised`), freezes (`Frozen`) or stops being watched
(`RecheckUnknown`, `RecheckFailed`).
It emits `EnvironmentCreated` and `EnvironmentDeleted` on a Stage
([when](../../contract-reference/environments.md#create-list-compare-and-retire)).

## Upgrading

[Upgrade the operator](upgrade.md) as usual: `helm upgrade` updates the
delivery CRDs; `helm.sh/resource-policy: keep` keeps them, their history
and `delivery.namespace` on [uninstall](installation.md#uninstall).

## IDP mode

Under `profile: idp` delivery is on. Where no GitOps engine runs, the Applier
row on Data sources proposes one value of your Pacto release:

```text
Set idp.flux.install=true on this Pacto release: helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator -n <namespace> --reset-then-reuse-values --set idp.flux.install=true
```

The chart then installs upstream Flux 2.10.1 and upgrades it with the release;
nobody runs a separate install ([what it adds](profiles.md#the-packaged-flux)).

Turning the value off or `helm uninstall` keeps Flux and the workloads it
applied (`helm.sh/resource-policy: keep`). Keep it as your own, or remove it
in this order, which stops the controllers so nothing is pruned:

```bash
kubectl -n flux-system scale deployment -l app.kubernetes.io/part-of=flux --replicas=0
# Labelled apply objects need break-glass to delete (see below)
for crd in $(kubectl get crd -l app.kubernetes.io/part-of=flux -o name | cut -d/ -f2); do
  kubectl get "$crd" -A --no-headers -o custom-columns=NS:.metadata.namespace,N:.metadata.name |
    while read -r ns n; do
      kubectl -n "$ns" patch "$crd" "$n" --type merge -p '{"metadata":{"finalizers":null}}'
      kubectl -n "$ns" delete "$crd" "$n"
    done
done
kubectl delete clusterrolebinding,clusterrole -l app.kubernetes.io/part-of=flux
kubectl delete namespace flux-system
kubectl delete crd -l app.kubernetes.io/part-of=flux
```

## How a change moves

A **Stage** names the applier, such as a Flux `OCIRepository`, and its
`status.pointer` is the digest it moved the environment to. Only the promoter
moves it, and the only field it writes on an object it does not own is that
repository's `spec.ref.digest`. Every change is an **Operation** whose spec
never changes:

```mermaid
flowchart LR
  P[Pending] -->|gates pass| L[lock]
  P -->|a person must decide| H[Held]
  H -->|approved| L
  L --> R[Running]
  R -->|Flux applied it| V[Verifying]
  V -->|Pass| S[Succeeded]
  V -->|Fail| F[Failed, rolled back]
  S -->|re-check fails| F
```

- Before: `pacto.published` admits only a version
  [`pacto publish`](../../publishing.md) published, `pacto.diff` compares the
  contracts and `pacto.impact` checks every consumer in the environment that
  declares a range on the service, as
  [Who is asked and why](delivery.md#who-is-asked-and-why) explains. Anything
  unseen (an unreadable Pacto, a registry silent for 10 seconds) is
  [Unknown](delivery.md#what-unknown-means).
  `pacto.suites` holds a version that does not ship a suite one of the Stage's
  checks names, for a person to accept the unknown. It also holds a version
  that changes a suite it ships, on every level.
  `pacto.environment` holds a version whose contract declares
  [environments](../../contract-reference/environments.md#environments-and-stages)
  but none named like the Stage's `spec.environment`, or values there that
  fail their schemas. A contract with no environments passes it.
  `pacto.applier` refuses a promote while the Stage's
  [`status.applier`](delivery-reach.md#which-manager-applies-a-stage) reads
  `Conflict` or `Unknown`. A Stage whose applier API the cluster does not
  serve reads `no applier: install Flux or Argo CD` and never promotes.
  `pacto.manifests`, on wherever delivery is enabled
  (`delivery.manifestInspector: false` opts out), reads the version's Flux or
  Helm artifact before the pointer moves. It refuses a cluster-scoped kind, a
  chart's `crds/`, an object outside the Stage namespace, a kustomization
  `namespace:` or an applier `targetNamespace` elsewhere, a RoleBinding to a
  ClusterRole and any Pacto delivery, Flux or Argo CD object, each with a
  signed refusal on `pacto delivery refusals`. It asks a person about what it
  cannot read: a templated or unknown kind, a `${` substitution, a
  kustomization or applier object that transforms the artifact and a
  RoleBinding that grants a group, a user or another namespace's
  ServiceAccount. It judges every chart template as if it renders, so a
  ClusterRole behind an `if` is refused too. Rollbacks skip it.
- After: the Pacto contract check counts only when
  `status.evaluatedRevision` names this version's contract digest and the
  current workload generation. `Compliant` and `Warning` pass, `NonCompliant`
  and `Invalid` fail, anything else is Unknown.
  [Health checks](delivery-health.md) can join it.
- Rollbacks, freezes and the 50 failed versions never tried again work as
  [What happens to a change](delivery.md#what-happens-to-a-change) says. A
  rollback that is refused or unclear also freezes the Stage, and a person's
  rollback puts an [auto-hold](../../glossary.md#auto-hold) on a Stage on an
  automatic level.

Only six built-in operation types exist: promote, rollback, override, freeze,
unfreeze and autonomy, which moves the Stage up or down
[the autonomy ladder](../../autonomy.md). Stage approval policy is
`approval.always`, not an expression.

## What the pointer guarantees

Pacto mediates the pointer and signed records name it. The pointer alone
decides what runs only when nothing else can change it; the Stage's `Mediated`
condition says whether that holds. It is `True` when exactly one
Flux Kustomization applies the apply `OCIRepository`, both carry the
`delivery.pacto.trianalab.io/apply-ref` label, the Kustomization sets a
`serviceAccountName` and sets none of `images`,
`patches`, `components`, `postBuild`, `namePrefix`, `nameSuffix`, `kubeConfig`,
`suspend` or `decryption`, and the `OCIRepository` sets no `spec.ignore`. Without a
`serviceAccountName` Flux applies each version as kustomize-controller,
cluster-admin in a default install, and the levels judge only the contract;
`pacto init --delivery` writes a namespaced deployer account for each
environment. On
Argo CD it is `True` when the Application carries the label, has one source and sets no
Kustomize images, patches or components, Helm parameters, values or value
files, or plugin. Otherwise it is `False` and the reason
(`ApplyRefUnguarded`, `NoApplier`, `SeveralAppliers`, `ApplierUnguarded`,
`ApplierUnscoped`, `ApplierOverrides`, `SeveralSources`) names what else decides what runs.

### What only break-glass may change

On labelled objects, only break-glass may change: removing
the label, the `OCIRepository`'s `spec.url`, `spec.layerSelector` and
`spec.ignore`, the rest of an Application's `spec.source`, its `spec.sources`,
`spec.destination` and `spec.project`, and a Kustomization's
`sourceRef`, `path`, `images`, `patches`, `components`, `postBuild`,
`namePrefix`, `nameSuffix`, `targetNamespace`, `serviceAccountName`,
`kubeConfig`, `suspend` and `decryption`. A GitOps tool that applies
those objects must leave these fields alone or run as break-glass.
Deleting a labelled object, or creating one with the label, is an edit too:
only break-glass, `system:masters`, the namespace controller, the garbage
collector and the users in `delivery.admissionPolicies.gitopsUsers` (Flux's
kustomize-controller and Argo CD's application controller by default) may.
On kubeadm clusters, kind included, the admin kubeconfig is in
`kubeadm:cluster-admins`, not `system:masters`, so let GitOps create the
pointers or add that user to `gitopsUsers`.
If one of them is ever recreated without the label, the Stage shows `Ready`
`False` with the reason `ApplierUnlabelled` and `Mediated` `False` with the
reason `ApplyRefUnguarded`.
`kubectl set image` on the workload itself is not mediated: Flux puts it back
on its next reconcile, and Argo CD does with `selfHeal`; until then it runs
beside the pointer.
The [security model](delivery-security.md) lists what each admission policy
checks and what stays open.

## Publishers

- A service's first Stage reads new versions from the registry by default:
  set `spec.candidates.registry`, and the promoter watches the repository the
  Stage's applier takes versions from, or `candidates.registry.repository`.
- A Stage names at most one candidate source: `registry`, `ociRepositoryRef`,
  `upstreamStage` or `upstreamRecords`.
- The promoter accepts a version only from a publisher it knows
  ([which publishers Pacto accepts](delivery-security.md#which-publishers-pacto-accepts)).

```yaml
spec:
  service: {name: payments}
  applyRef: {name: payments}
  candidates:
    registry: {}   # or {repository: ghcr.io/acme/payments-deploy}
```

| Value | What it sets |
|---|---|
| `delivery.publishers.registry` | The `host/org` your services publish to. On `ghcr.io` and `registry.gitlab.com` the promoter derives a rule from it; empty, only the policy or a person's confirmation in the inbox accepts a signer |
| `delivery.publishers.defaultBranch` | The branch whose CI the derived rule accepts (`main`) |
| `delivery.publishers.policy` | A signed delivery policy (`repository:tag`) that replaces the derived rule |
| `delivery.publishers.policySigners` | The trust config whose `role: policy` keys sign that policy |
| `delivery.publishers.trustedRoot` | A `trusted_root.json` for a private Sigstore; empty uses public-good Sigstore |
| `delivery.recorder.signing.publicKey` | The PEM public key of an Ed25519 or KMS recorder, so the promoter verifies the pins it writes. Keyless recorder signing needs none |

- GHCR serves no Referrers API; the watch reads `pacto publish`'s write-once
  `pacto-sig-` tags there, so GHCR works with no extra value.
- Clients read the same configuration from `publishers.json` in the
  `pacto-delivery-identities` ConfigMap in `delivery.namespace`.
- A Stage whose registry cannot be listed has `Ready` `False` with the reason
  `RegistryUnreadable`, and one without a trusted root
  `PublisherUnverifiable`; nothing is published meanwhile.
- When a policy narrows, a digest among the newest 20 tags that is no longer
  accepted leaves `status.published`. One older than those 20 stays there
  until 50 newer versions push it out: the watch reads only the newest tags.

## Set up a Stage

With Flux, a first Stage can follow a candidate `OCIRepository` instead. It
follows the deploy artifact's repository and its
`spec.verify` names the publisher, so Flux checks every signature. The Stage
lists each digest Flux verified in `status.published` and refuses anything else
as `NotPublished`. Without a `secretRef` or `matchOIDCIdentity` Flux accepts a
keyless signature from anyone, so such a Stage promotes nothing. Flux reads a
`matchOIDCIdentity` issuer and subject as regular expressions, so each must be
anchored (`^...$`) and must not match an identity nobody has: `.*`, or a
subject left open at the end, counts as no identity at all. A Stage that
cannot promote has `Ready` `False` with the reason `CandidateUnverified`,
`CandidateNotFound`, `NoCandidateSource` or `UpstreamUntrusted`.

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: payments-candidates, namespace: test}
spec:
  url: oci://registry/apps/payments
  ref: {semver: ">=0.0.0"}
  verify: {provider: cosign, secretRef: {name: cosign-pub}}
  # keyless: matchOIDCIdentity with the issuer and subject of publish.sign
```

Label the `OCIRepository` that applies the Stage with
`delivery.pacto.trianalab.io/apply-ref=<stage name>`, so admission keeps
everyone but the promoter and break-glass off its digest, and give it the same
`spec.verify`. The value names the one Stage that applies through it, so a
second Stage cannot drive the same pointer. Without the label, or with another
Stage's name in it, the promoter may not write the digest either: the Stage has
`Ready` `False` with the reason `ApplierUnlabelled`, and a refused write shows
in the operation's `status.message`. A downstream Stage takes only what its upstream lists in
`status.offered`: versions that passed the published gate and were verified
there, never one let through on an accepted unknown.

```yaml
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments, namespace: prod}
spec:
  service: {name: payments}
  environment: prod
  applyRef: {name: payments}
  candidates:
    upstreamStage: {name: payments, namespace: test}
  startLevel: advise
  gates:
    verify: {window: 5m}
```
