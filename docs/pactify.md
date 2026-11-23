# Pactify a service

To [pactify](glossary.md#pactify) a service is to give it a Pacto
[contract](glossary.md#contract), `pacto.yaml` kept next to your code, and let
Pacto check every new version before it runs. This guide takes a plain service to a contract,
signed published versions and environments Pacto only watches, and shows how
an environment moves past watching once it has earned it. Every command is on
this page except the one-time cluster setup for approvals. To preview the
result, press **Play the delivery story** in the
[live dashboard demo](examples/dashboard-demo.md), which runs in your browser.

In the cluster, [Flux](glossary.md#flux) does the deploying and Pacto tells it
which version. Each environment starts on observe, the first level of
[the autonomy ladder](autonomy.md): Pacto watches and moves nothing until a
person raises it.

## Before you begin

You need:

- the `pacto` command, from [Installation](installation.md);
- a service in a git repository, with a Dockerfile and the Kubernetes files
  that run it (a Deployment and a Service). Those files are what lands in the
  cluster. Leave the namespace out of them: each environment gets its own;
- Docker with a builder that can write an image to a folder, as Pacto builds
  the image there and pushes it itself:
  `docker buildx create --use --driver docker-container`;
- a container registry, and a login to it:
  `pacto login registry.example -u <user>` (or `docker login`). Any registry
  works for this page. The optional signed [record](glossary.md#record) of each
  change is stored next to what you publish, in a registry that serves OCI
  Referrers. Without records a Stage stops at advise; see
  [What delivery needs](index.md#what-delivery-needs);
- a CI that hands out an OIDC token (GitHub Actions or GitLab CI), so Pacto
  signs keyless, or a cosign key pair if you sign with a key;
- for step 4, a Kubernetes cluster (1.30 or later), `kubectl` and `helm`.
  People approve with a fresh company login, which needs a one-time cluster
  setup, maybe by you:
  [the cluster trusts your identity provider](integrations/kubernetes/delivery-identity.md#the-cluster-trusts-your-identity-provider).
  Until it works, nobody can approve; see
  [where approving works](integrations/kubernetes/delivery-identity.md#where-approving-works).

Pacto writes the contract from your service.

If you only want the contract and its CI checks, run
`pacto init --describe-only`: its next steps say how to validate the contract
instead of how to publish it. Then go on to [Put it in your repository](ci.md):
`pacto ci --publish` publishes it when `publish.bundle` is declared.

## 1. See what Pacto would add

From the root of your repository:

```bash
pacto init --delivery --plan
```

`pacto init` reads what is already there, such as the Dockerfile and the
Kubernetes files ([the full list](#what-pacto-reads-from-your-folder)), and
fills in a contract from it. It reads your registry from
the image name in your Deployment; pass `--registry registry.example/acme` if it
cannot, or if the image is a bare name such as `checkout`: a Docker Hub official
image only Docker can push to. `--delivery` adds the cluster files of step 4, so
the first version you publish already carries them. With `--plan` it shows
exactly what it would add and changes nothing. For a private registry, add
`--registry-secret registry-creds` (step 4 creates that Secret). For a local
registry that speaks plain HTTP, add `--insecure-registry`. `--no-transparency-log`
applies with `--from` only: it writes `transparencyLog: false` under the
golden path's `publish.sign`.

The plan lists the services yours depends on, what Pacto could not work out,
marked Unknown, and a **Next** list of what to do after.

## 2. Write the contract

Add any flags you needed in step 1, such as `--registry-secret`:

```bash
pacto init --delivery --owner payments
pacto validate
```

`--owner` names the team that owns the service: the name of its group in your
company login, whose members Pacto asks about this service
([Decide, unfreeze and roll back](pactify-decide.md)). `init` writes
`pacto.yaml`, the `delivery/` folder and a `.pactoignore`, and never
overwrites your edits. For an image
`registry.example/acme/my-service:1.0.0` and the files `k8s/deployment.yaml` and
`k8s/service.yaml`, the contract is:

```yaml
pactoVersion: "2.6"        # 2.5 and later hold compatibility: unknown
service:
  name: my-service         # no version: pacto ci --publish derives it
  owner:
    team: payments
workload: service           # from the Kubernetes workload kind
capabilities:               # from a readiness, liveness or startup probe
  - type: health
dependencies:               # one per service a URL in the environment names
  - name: billing
    ref: oci://registry.example/acme/contracts/billing   # where its contract would be published
    required: true                              # yours cannot run without it
    compatibility: unknown   # no range proposed: no contract or running version of billing was read
configurations:             # the environment, ConfigMaps included: keys and types, no values
  - name: env
    schema: configuration/schema.json
    required: true
```

- The contract has no `publish` section and no `service.version`:
  [`pacto ci --publish`](publish-from-ci.md) derives both, building the image
  with the tool the folder shows and assembling the deploy artifact from your
  Kubernetes files. The image tag, `1.0.0` here, is named under **Found** as
  the version to declare for a first publish.
- The configuration schema holds each key and its type, never a value, so it
  declares no defaults.
- It marks a key `writeOnly` when its name holds password, passwd, secret,
  token, key, credential, private or dsn (any case), when it is read from a Secret or when
  its value is a URL with credentials.
- Next steps name each manifest or ConfigMap that sets such a key to a
  literal value, because the bundle publishes that file.
- A `--cluster` draft reads such a key from the Secret `<workload>-env`, which
  you create with `kubectl create secret generic --from-env-file`
  ([details](bootstrap-contracts.md#configuration-keys-and-sensitive-values)).

### What pactify recognizes

- `init` finds the vendor APIs, managed cloud services and legacy hosts the
  folder names and records them in the inferred record, never in
  `pacto.yaml`; see [External dependencies](#external-dependencies). A
  `--cluster` draft lists them the same way.
- A `--cluster` draft is completed by the same engine as a folder, and its
  `pacto.inferred.yaml` adds what the cluster showed, each value with when it
  was read and whether every node was observed
  ([details](bootstrap-contracts.md#the-inferred-record-of-a-draft)).
- On a folder that already holds a `pactoVersion: "2.5"` `pacto.yaml`, `init`
  completes the contract and never changes it: what the folder adds goes into
  the [inferred record](contract-reference/inferred-record.md), published with
  the bundle, each value with its source.
- For a new contract, `init` lists what the folder shows beyond the
  `pacto.yaml` it writes. `pacto pack` publishes that record only once
  `pacto.yaml` declares `pactoVersion: "2.5"`, and `init` says so under
  **Next** when it writes an older version.

Fill in what the plan marked Unknown, above all the dependencies:

- Replace each `compatibility: unknown` with the versions yours accepts, such
  as `^2.0.0` for any 2.x. With that service's contract in `--fleet`, `init`
  reads its bundle and proposes one.
- Declare an OpenAPI document under `interfaces`
  ([how](ci.md#declare-your-api-first)): Pacto sees API changes only there.
  Every field is in [Publish](contract-reference/publish.md) and
  [Dependencies](contract-reference/dependencies-and-state.md).
- For private images, add `imagePullSecrets: [{name: registry-creds}]` under
  `spec.template.spec` in your Deployment.
- With dependencies, run `pacto lock .` ([Lockfile](lockfile.md)), and commit
  what `init` wrote, as [What to commit](ci.md#what-to-commit) lists.

## 3. Publish a version

In your build pipeline, on the merged commit, after your tests:

```bash
pacto ci              # what would be published, and the version; writes nothing
pacto ci --publish    # publish it
```

`pacto ci --publish` builds the image with the tool your folder uses, pushes it
with your Kubernetes files and the contract to your registry and signs them so
anyone can check they came from you. With no key configured, it signs keyless
as the CI identity, which needs `id-token: write` on GitHub Actions or an
`id_tokens` entry on GitLab, and prints that identity. To sign with a key
instead, pass `--sign-key` with a key file outside the folder. The details are
in [Publish from CI](publish-from-ci.md).

Versions have three numbers, major.minor.patch, such as 2.4.1. With no
`service.version` declared, the first publish is `1.0.0`, and each later one
raises the number the change needs: a change that breaks the services using
yours raises the major. A declared version too small for the change is
refused, and a published version is never overwritten; see
[Publish a version](publishing.md).

## 4. Connect the service to your cluster

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

Install, once per cluster, Flux, the Pacto operator (it checks each
running service against its contract) and delivery (it decides which version
runs in each environment). One Helm chart installs both. With a private registry, give delivery a login
in the operator's namespace and add
`--set delivery.promoter.registryCredentials.secretName=registry-creds` to the
delivery command below:

```bash
kubectl create namespace pacto-operator-system
kubectl -n pacto-operator-system create secret docker-registry registry-creds \
  --docker-server=registry.example --docker-username=<user> --docker-password=<token>
```

```bash
kubectl apply -f https://github.com/fluxcd/flux2/releases/latest/download/install.yaml
```

--8<-- "integrations/kubernetes/docs/generated/_install-command.md"

--8<-- "integrations/kubernetes/docs/generated/_delivery-install-command.md"

Replace `staff` with your people's group; [Install](integrations/kubernetes/delivery-setup.md#install)
says why.

Delivery is opt-in: without it, Pacto never changes what runs.

With delivery on, Pacto creates each environment's [Stage](glossary.md#stage)
from the installation ladder (`staging`, then `production`) once a signed
version is published, on the Argo CD or Flux object that runs the workload. In
IDP mode it creates the Flux objects, suspended until a person approves the
first promote. See [Setup and gates](integrations/kubernetes/delivery-setup.md).

`delivery/pacto.yaml` tells the operator to check the service. It ships inside
every version, so you never apply it. With a private registry, create the
login in each namespace the service runs in:

```bash
kubectl -n <namespace> create secret docker-registry registry-creds \
  --docker-server=registry.example --docker-username=<user> --docker-password=<token>
```

With `--registry-secret`, the Pacto resource logs in with it. A read-only token is enough; see
[Private registries](integrations/kubernetes/limitations.md#delivery-and-private-registries).

`init` declares no [`environments`](contract-reference/environments.md) and
writes no Stage; `--env` is deprecated and ignored.

Each Stage starts at `observe` unless its applier is guarded: it moves
nothing, and your GitOps tool keeps applying the tag you deploy.

```bash
kubectl get stages.delivery.pacto.trianalab.io -A    # POINTER: the digest of the version each one runs
pacto delivery history -n test --stage my-service     # what happened in test
kubectl -n test describe stages.delivery.pacto.trianalab.io my-service  # if nothing moves: why, under Ready
```

## 5. Ship the next version

Make a change and merge it: `pacto ci --publish` publishes it with the next
version the change needs, or with the `service.version` you declare. While the Stages observe, you deploy the new tag as before:
set `spec.ref.tag` in the OCIRepository your GitOps repository holds. Each
Stage records what it would have done.

To leave observe, in this order:

1. Keep deploying this way until 10 versions passed their checks
   (`autonomy.proposeAfter` lowers that, see
   [the ladder](autonomy.md#earned-autonomy-pacto-proposes-a-person-decides)).
   The Stage's `status.proposal` lists what is still missing.
2. Remove `spec.ref` from the OCIRepository that applies the service in test
   and apply it, so Flux stops setting the version, and label it so only Pacto may
   write its digest. Pacto then proposes `advise`:

    ```bash
    kubectl -n test label ocirepository my-service delivery.pacto.trianalab.io/apply-ref=my-service
    ```

3. Ask for the raise; another person who may update the workload approves it (see
   [Why you were asked](pactify-decide.md#why-you-were-asked)). Be quick:
   until the raise, nothing moves the version.

    ```bash
    pacto delivery propose --type delivery.pacto.trianalab.io/autonomy --stage my-service -n test --to advise
    ```

Each level above that is one more approved operation. A Stage on a guarded
applier has `approval: always: true`, so every promote there waits for a person:
the contract check cannot see a consumer with no contract, or tell whether the
version is healthy. Delete it from the Stage once a health check
judges each version there (`gates.verify.checks`, see
[Delivery reference: health checks](integrations/kubernetes/delivery-health.md)).
A version that fails its checks after release is rolled back or freezes its
Stage, as [What happens to a change](integrations/kubernetes/delivery.md#what-happens-to-a-change)
explains.

Each move is a [change](glossary.md#change); one that waits for a person shows
`Held`. To approve one, as a person who may update the workload
([Decide, unfreeze and roll back](pactify-decide.md) has the details):

```bash
kubectl get operations.delivery.pacto.trianalab.io -n prod   # PHASE shows Held
pacto delivery approve -n prod <name> --decision approve
```

## Many services at once

`pacto init` takes several folders and ends with a summary table.
`--cluster` drafts a contract for every workload no Pacto describes yet, from
your current kubeconfig context, in `<out>/<namespace>/<name>`. Pass
`--namespace`: without it, `--cluster` reads every namespace, system ones
included:

```bash
pacto init --plan services/checkout services/payments
pacto init --cluster --namespace shop --out drafts --plan
```

With the Pacto node agent running, the Services a workload calls become
dependencies. When the Pactos describing `shop/payments` report 2.3.1 and 2.4.0,
the draft proposes `^2.3.0` in a comment and keeps `compatibility: unknown`
until you declare a range; until then, impact cannot verify a BREAKING
change. Without the agent, environment URLs name
dependencies. Drafts are
never published, and an invalid one fails the command. The MCP tool
`pacto_create` with `pactify: true` takes `paths`, `cluster` and `namespaces`,
and only plans unless `dry_run` is false. With `cluster` it also takes
`max_items`, `call_window` and `continue`.
[Generate contracts from what you already have](bootstrap-contracts.md#draft-from-a-running-cluster)
lists the permissions `--cluster` needs and walks through a whole folder of
services.

## What Pacto reads from your folder

`pacto init`, `pacto explain` and `pacto pack` read these files at any depth of
the service folder. Each value goes into the
[inferred record](contract-reference/inferred-record.md) with its `path:line`,
never into `pacto.yaml`.

| Format | Files | What it records | What it never reads | When it cannot read it |
|---|---|---|---|---|
| Dockerfile | `Dockerfile`, `Dockerfile.*`, `*.Dockerfile` | ports from `EXPOSE`, `service.summary` from the `org.opencontainers.image.description` label, a health check from a localhost URL in `HEALTHCHECK` | the `HEALTHCHECK` command and the URL query | `DISCOVERY_UNRESOLVED` for a port set through a variable or a range |
| OpenAPI, AsyncAPI and gRPC | any `.yaml`, `.yml`, `.json` or `.proto` file that parses as one | one interface per kind: `api`, `events` or `grpc` | GraphQL (`*.graphql`, `*.graphqls`, `*.gql`), which has no Pacto interface type, and package tool files such as `package-lock.json` | `DISCOVERY_AMBIGUOUS` when two files hold one kind, and no interface for that kind |
| Helm chart | `Chart.yaml`, `Chart.lock`, `values.yaml`, `values-<env>.yaml` and `templates/*.yaml`, up to two folders deep | image, ports, configuration keys, environments and each locked dependency under `inputs.chartDependencies` | a configuration value beyond the host it names; it never renders a template, downloads a dependency, opens a vendored `charts/*.tgz` or reads a subchart unpacked under `charts/` | `DISCOVERY_CHART_DEPENDENCY_UNLOCKED` for a dependency with no `Chart.lock` digest; `DISCOVERY_UNRESOLVED` once per template with an expression it does not resolve |
| Kubernetes manifests | Deployment, StatefulSet, DaemonSet, Job and CronJob outside a chart's `templates/`, `charts/` and `crds/` folders | image, container ports and environment key names | an environment value beyond the host it names | `DISCOVERY_UNREADABLE` for YAML that does not parse |
| Kustomize | `kustomization.yaml` and its local `resources` | image from `images`, an environment per `overlays/<env>/` | remote resources and `helmCharts` | `DISCOVERY_UNSUPPORTED_FILE`, naming a remote resource without its credentials |
| Compose | `compose.yaml` or `docker-compose.yaml` at the folder root | image, container ports and environment keys of the service named like yours (the `service.name` in `pacto.yaml`, else the folder's name), else of the only one | `include`, `extends` from another file and `env_file`; an environment value only for the host it names | `DISCOVERY_AMBIGUOUS` when it lists several services and none is named like yours |
| Owner files | `catalog-info.yaml` in the folder; `.github/CODEOWNERS`, `CODEOWNERS` or `docs/CODEOWNERS` at the repository root, the last rule that matches `pacto.yaml` | `service.owner` with the marker `class: ownerFile`, shown as "attested" by `pacto init`; from `catalog-info.yaml` also `service.domain` (`spec.system`) and dependencies (`spec.dependsOn`) | `spec.lifecycle`. The owner is unverified until a signed commit is checked, so it never routes a decision or clears a gate | `DISCOVERY_UNRESOLVED` for a CODEOWNERS rule with email owners only |
| Environments | `environments/<env>.yaml`, Helm values files, overlays, and Argo CD `Application` and Flux `Kustomization` and `HelmRelease` objects in the folder | `environments[<env>].file`; a GitOps object confirms it | an Argo CD `ApplicationSet`, and the paths of a Flux object whose source is an OCI artifact, a bucket or a Helm repository, such as the `delivery/` files `pacto init` writes | `DISCOVERY_UNRESOLVED` for an invalid environment name, or for a path outside the folder or one the folder does not hold (an Argo CD `Application` that deploys from another repository) |
| Other formats | `helmfile.yaml`, `helmfile.yml`, `Tiltfile`, `*.jsonnet`, `*.libsonnet`, `*.cue` | nothing | the whole file, a program or a template for another tool | `DISCOVERY_UNSUPPORTED_FILE` |

- When two files give one value, the more direct one wins and the record
  lists the others under `outranks`: for a port `EXPOSE`, then a manifest or
  template, then Compose; for the owner `catalog-info.yaml`, then CODEOWNERS,
  then the workload's team label.
- A file over 1 MiB is never read, and a link, a FIFO or a device is never
  opened.
- A finding names the file and line, never a value. It changes no validation
  result and no gate, and the record keeps at most 100.

| Finding | Meaning |
|---|---|
| `DISCOVERY_UNSUPPORTED_FILE` | Pacto recognizes the file, or a part of it, and does not read it |
| `DISCOVERY_AMBIGUOUS` | several files or services fit, so none is recorded |
| `DISCOVERY_CHART_DEPENDENCY_UNLOCKED` | a chart dependency has no `Chart.lock` digest, so it is not read |
| `DISCOVERY_UNREADABLE` | the file is over 1 MiB, is not valid YAML, is not a regular file or has a `:` in its name |
| `DISCOVERY_UNRESOLVED` | a template expression, an invalid environment name, a path outside the folder or missing from it, or a CODEOWNERS rule with email owners only |
| `DISCOVERY_TRUNCATED` | discovery stopped after 5000 files, so results are partial; always listed first |

## External dependencies

`pacto init`, `pacto explain` and `pacto pack` record the systems a folder
names outside the cluster:

- Each vendor API, managed cloud service or legacy host becomes a
  `dependencies[<name>]` field of the
  [inferred record](contract-reference/inferred-record.md#external-dependencies),
  with every file and line that names it.
- Pacto never writes one into `pacto.yaml`, and an inferred external never
  clears a gate. To declare one, copy its `declare` YAML into `pacto.yaml` and
  set the version you call.

| Source | What is read | Confidence |
|---|---|---|
| Workload environment, ConfigMaps included | Each value that names a host, and each key name | `likely` for a host, `possible` for a key name alone |
| `secretKeyRef` and `envFrom` `secretRef` | The Secret name and key names, never a value | `possible` |
| Helm `values.yaml` and `values-<env>.yaml` | Every string value, read as data, and its key | `likely` for a host, `possible` for a key name alone |
| Compose `environment` | Each value, as a map or a `KEY=VALUE` list | `likely` for a host, `possible` for a key name alone |
| Kustomize `configMapGenerator` | `literals` and the `envs` files | `likely` for a host, `possible` for a key name alone |
| SDK manifests and lockfiles | `go.mod` (direct requires), `package.json` (`dependencies` and `optionalDependencies`) with `package-lock.json`, `requirements*.txt`, `poetry.lock` (no dev packages), `pom.xml` (no `test` or `provided` scope) and `gradle.lockfile` (no test-only lines): each package name and its major version | `possible` |
| Terraform `*.tf` | Each `resource` whose type the catalog lists: its id attribute and `region` when they are literals, and the region and account of its `provider` block | `possible` |
| Crossplane objects | Each object whose `<group>/<Kind>` the catalog lists: its `crossplane.io/external-name` or name, `spec.forProvider.region` and connection Secret name | `likely` when the workload reads that Secret, else `possible` |

- A library in a manifest or a resource in the folder proves no call, so those
  sources stay `possible`.
- The SDK major is the client version the service uses. Without a lockfile or
  an exact range, Pacto leaves it out and never guesses it.
- A managed resource gets the identity
  `<provider>/<service>/<account>/<region>/<id>` only when all four parts are
  literals in the folder. Otherwise it keeps the catalog identity, such as
  `aws/sqs`, and the record lists the parts it misses. A Terraform resource
  whose id attribute is not a literal is named by its label, and the record
  lists the id as missing.

### What Pacto never reads

- A URL keeps its host and port. The user name, password, path and query are
  stripped before anything stores it.
- A value under a key named like a password, secret, token, key, credential,
  private key or DSN is never parsed, and a Secret's values are never read.
- Compose `env_file`, `.tfstate` and `.tfstate.backup` files, a
  `package-lock.json` `resolved` URL, `pom.xml` repositories and every
  Terraform attribute other than the id, region and account are never read.
- A catalog can never make Pacto read a secret attribute: a sensitive
  Terraform id attribute is refused when the catalog loads and again before
  the scan reads it.
- A symlinked or ignored `envs` file is never followed.

### The plan line

`init` prints one line, then where each external comes from:

```text
External dependencies: 2 (sqs:newsletter-signups, stripe), inferred
  dependencies[newsletter-signups]  external, from infra/main.tf:1 (aws_sqs_queue newsletter-signups, possible)
  dependencies[stripe]              external, from chart/values.yaml:9 (STRIPE_API_BASE) and requirements.txt:1 (stripe, possible)
  catalog default sha256:62a3fd52...
```

- A `possible` external is also named under **Unknown**, such as
  `infra/main.tf:1 suggests aws/sqs: nothing else in the folder confirms it`.
- Each file Pacto recognizes and does not read is a finding and is counted, so
  an unread input never reads as "calls nothing":
  `External dependencies: none found in what was read, 2 sources not read, inferred`.
  That covers `yarn.lock`, `pnpm-lock.yaml`, a Gradle build without
  `gradle.lockfile`, `setup.py`, `Pipfile`, `Pipfile.lock`, a `pyproject.toml`
  without `poetry.lock`, a Terraform `module`, `*.tf.json`, Terraform state, a
  symlinked `envs` file and an organization catalog Pacto cannot pull. A
  folder too large to read whole, or with more findings than a record keeps,
  is counted too.
- A host outside the cluster that no catalog entry identifies is a
  `DISCOVERY_UNRESOLVED` finding; declare it in `pacto.yaml` if the service
  calls it.

### Choose the catalog

- The [external catalog](external-catalog-reference.md#matching) decides what
  each host, package, resource type or kind belongs to.
- `init` classifies with the default catalog, plus an organization catalog
  pinned by digest with `--external-catalog oci://REPOSITORY@sha256:DIGEST` or
  `PACTO_EXTERNAL_CATALOG`. A tag is refused.
- `pacto pack`, `pacto push` and `pacto explain` read `PACTO_EXTERNAL_CATALOG`
  too, so they classify alike.
- The record lists each catalog digest under `inputs.catalog`, so a new
  catalog changes the record and nothing else.
- When the organization catalog cannot be read, the default classifies alone
  and **Unknown** says so.
- MCP `pacto_create` uses the catalog the server was started with.

## What's next

| Goal | Page |
|------|------|
| Decide on a waiting change or roll one back | [Decide, unfreeze and roll back](pactify-decide.md) |
| Understand what happens to a change | [Delivery](integrations/kubernetes/delivery.md) |
| Fail a commit that would break a consumer | [Put it in your repository](ci.md) |
| Check errors and response times after release | [Health checks](integrations/kubernetes/delivery-health.md) |
| Keep a signed record of every change | [Signed records](integrations/kubernetes/delivery-records.md) |
| Set up Stages by hand | [Setup and gates](integrations/kubernetes/delivery-setup.md) |
| Start a new service from your platform team's golden path with `pacto init --from` | [Create a service from a golden path](golden-paths.md) |
