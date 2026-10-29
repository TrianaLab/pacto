# Generate contracts from what you already have

`pacto init` reads the files a service folder already holds and writes a
contract for it. It takes many folders in one run. This guide takes a folder of
existing services, such as `services/*` in a monorepo, to one checked contract
per service.

Every command here runs on your machine. It writes `plan.json`, files in the
service folders and, with `--cluster`, drafts in the `--out` folder. It
publishes nothing.

## 1. Take an inventory

Plan every folder at once. `--plan` changes nothing:

```bash
pacto init --plan --output-format json services/* > plan.json
jq -r '.results[] | [.service, .mode, (.unknown | length)] | @tsv' plan.json
```

```text
checkout  pactify  2
ledger    blank    1
notifier  blank    0
payments  pactify  2
```

Each entry in `results` has a `mode`. `pactify` means init found a Dockerfile
or Kubernetes manifests and read the contract from them. `blank` means it found
neither and plans a starter template with placeholder values. The `found`,
`dependencies`, `unknown` and `next` lists say what init read, which services
the workload calls, what it could not tell and what a person still has to do.
List every open question across the fleet:

```bash
jq -r '.results[] | .service as $s | .unknown[] | "\($s): \(.)"' plan.json
```

### What init reads

| Input | Contract fields |
|---|---|
| `Dockerfile` (or `Dockerfile.*`, `*.Dockerfile`) | an image artifact under `publish.artifacts`, built from the Dockerfile |
| Kubernetes YAML with a Deployment, StatefulSet, DaemonSet, Job or CronJob | `service.name` from the workload, `service.version` from the image tag, the registry from the image, `workload` and a manifests artifact that copies the YAML files |
| The `team` label on the workload | `service.owner.team` |
| The workload's environment, with the ConfigMaps in the folder that it reads | a configuration schema at `configuration/schema.json`, each value as its default |
| An environment value that names another in-cluster service | `dependencies`, with the range from that service's contract in `--fleet`, or the placeholder `^1.0.0` |
| A readiness, liveness or startup probe | a `health` capability |
| A file named exactly `openapi` or `swagger`, with a `.yaml`, `.yml` or `.json` extension | one `openapi` interface |
| `kustomization.yaml` | the manifests artifact keeps the folder layout, and init asks you to set the Kustomization's `spec.path` |

Init reads only these. A file such as `api/openapi.v1.yaml` is missed, and
state stays for you to write. A placeholder range is named in a comment above
the dependency and under `unknown`.

Init skips the files and folders that the service's
[`.pactoignore`](pactoignore.md) lists. It also skips every folder that holds a
`Chart.yaml`, and documents in the `backstage.io` and `skaffold` API groups,
such as `catalog-info.yaml` and `skaffold.yaml`.

## 2. Prepare the folders init cannot read

Init keeps a `pacto.yaml` that is already there, so prepare these folders
before step 3.

**Helm charts.** Init does not render charts. It skips each chart's
templates, subcharts and CRDs and names every chart under `unknown`, so a
chart-only folder plans as `blank`. Render the chart into the folder, then
init reads the result:

```bash
mkdir -p services/ledger/k8s
helm template --include-crds ledger services/ledger/chart \
  > services/ledger/k8s/rendered.yaml
```

The contract then publishes the rendered file, so render again when the chart
changes. The `unknown` line that names the chart stays after you render it. To
publish the chart itself, add it under `publish.artifacts` as a `chart`
([Publish](contract-reference/publish.md)).

**Compose files.** Init does not read `compose.yaml`. A Compose-only folder
gets the blank template. The blank template writes `pactoVersion: "2.0"` and
no `publish` section, so `pacto publish` refuses it until you set
`pactoVersion: "2.1"` and add `publish`
([Publish](contract-reference/publish.md)).

## 3. Write the contracts

```bash
pacto init services/*
```

Init prints what it found per folder and ends with a summary table. It only
adds files and missing `.pactoignore` lines, so you can run it again after you
fix a folder.

!!! warning "Check the build command before you publish"
    Init treats any other YAML file with an `apiVersion` and a `kind` as a
    Kubernetes manifest, such as a kind cluster configuration, and lists it in
    the manifests artifact's `build.command`. List such a file in the folder's
    `.pactoignore` before you run init. In a `pacto.yaml` that init already
    wrote, read the `cp` list and remove any file that is not a manifest your
    cluster applies.

## 4. Fill in what init could not tell

Work through the `unknown` list of each service.

- **Owner.** Write `service.owner.team`, or pass `--owner` when one team owns
  every folder in the run.
- **Interfaces.** The `openapi-infer` plugin writes `interfaces/openapi.yaml`
  from FastAPI or Huma code and needs the `pacto.yaml` from step 3. Run it from
  the service folder with the project's dependencies installed:

    ```bash
    cd services/payments
    pacto generate openapi-infer . -o .
    ```

    Then add the interface to `pacto.yaml`:

    ```yaml
    interfaces:
      - name: api
        type: openapi
        ref: interfaces/openapi.yaml
    ```

- **Configuration.** Init's schema lists the environment only. `schema-infer`
  writes a JSON Schema from a config file
  ([For developers](developers.md#2-infer-schemas-from-your-code-optional)).
- **Dependencies.** Replace each placeholder `compatibility` with the versions
  the service accepts.

Then check every contract and see who declares each service:

```bash
for d in services/*/; do pacto validate "$d"; done
pacto init --plan services/payments --fleet services
```

`--fleet` reads the other contracts and lists the services that declare this
one under `Used by`.

## Draft from a running cluster

`pacto init --cluster --namespace <ns> --out <dir>` drafts a contract for each
workload in the namespace that no Pacto resource describes, in
`<dir>/<ns>/<name>`, from your current kubeconfig context. Without
`--namespace` it reads every namespace, `kube-system` included. It lists
Services, EndpointSlices, Pactos and node runtime observations in every
namespace even with `--namespace`, so your identity needs cluster-wide `list`
on them. It needs that permission even when the Pacto CRDs are not installed.

Each draft holds a `manifests.yaml` with the workload and its Services as the
cluster returns them, without namespace, uid or status. It also holds a
`pacto.yaml` and a `.pactoignore`. `--plan` writes nothing. A workload that a
Pacto targets is skipped.

Without the [node agent](runtime-agent.md), dependencies come only from
environment values, as in a folder. The draft holds no ConfigMaps, so each one the workload reads is named under
`unknown` and its keys stay out of the configuration schema.
A value such as `host:6443` matches whether or not `host` is a Service, so a
control-plane endpoint can become a dependency. Check each one.

## From an agent

The MCP tool `pacto_create` with `pactify: true` and `paths` plans the same
contracts for an agent. It plans only, unless the agent sets `dry_run` to
false ([AI assistants](mcp-integration.md)).

## What's next

| Goal | Page |
|------|------|
| Publish a contract and connect it to delivery | [Pactify a service](pactify.md) |
| Check every change in CI | [Put it in your repository](ci.md) |
| Write the fields init leaves out | [Contract sections](contract-reference/sections.md) |
