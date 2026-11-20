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
ledger    blank    3
notifier  blank    2
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
| `Dockerfile` (or `Dockerfile.*`, `*.Dockerfile`) | `publish.build` in the inferred record: [`pacto ci --publish`](publish-from-ci.md) builds the image with it |
| Kubernetes YAML with a Deployment, StatefulSet, DaemonSet, Job or CronJob | `service.name` and `workload` from the workload; `publish.deploy` in the inferred record, and a `found` line naming the image tag as the version a first publish can declare |
| The `team` label on the workload | `service.owner.team` |
| The workload's environment, with the ConfigMaps in the folder that it reads | a configuration schema at `configuration/schema.json` with each key and its type, no values; sensitive keys are `writeOnly` |
| An environment value that names another in-cluster service | `dependencies`, each with `compatibility: unknown` and, from that service's contract in `--fleet`, a proposed range in a comment |
| A readiness, liveness or startup probe | a `health` capability |
| An OpenAPI document at any path in the folder | one `openapi` interface; AsyncAPI and gRPC files go to the inferred record |
| `kustomization.yaml` | `publish.deploy` in the inferred record: the deploy artifact keeps the folder layout |

These fill in the contract. Init also reads Helm charts, Kustomize, Compose,
CODEOWNERS, `catalog-info.yaml` and environment files into the inferred record
([the full list](pactify.md#what-pacto-reads-from-your-folder)). State stays
for you to write. Every dependency range is a proposal: init writes
`compatibility: unknown` with the proposal in a comment, and the dependency is
named under `unknown`.

Init skips the files and folders that the service's
[`.pactoignore`](pactoignore.md) lists. When it looks for manifests, it also
skips every folder that holds a `Chart.yaml`, and documents in the `backstage.io` and `skaffold` API groups,
such as `catalog-info.yaml` and `skaffold.yaml`.

### Configuration keys and sensitive values

Init reads the environment values to find types and dependencies and writes
only keys and types. A key is sensitive when its name holds password, passwd,
secret, token, key, credential, private or dsn in any case, when the workload reads it
from a Secret or when its value is a URL with a password. A sensitive key is a
`string` with `writeOnly: true` and a description. Schema summaries and schema
diffs show its default and any other schema value as `(sensitive)`. A value
you declare under `configurations[].values` or in an environment file is shown
as written, so keep secrets out of it. Init never reads the values of a Secret.

When a manifest or ConfigMap in the folder sets a sensitive key to a literal
value, init names the file and the key under next steps. The bundle publishes
that file to anyone who can pull it, so read the key from a Secret instead.
Init keeps a `configuration/schema.json` that is already there: remove any
default that holds a secret and publish a new version.

## 2. Prepare the folders init cannot read

Init keeps a `pacto.yaml` that is already there, so prepare these folders
before step 3.

**Helm charts.** Init reads charts statically and never renders them. It
skips each chart's templates, subcharts and CRDs as manifests, and reads its
`Chart.yaml`, values files and templates for images, ports, configuration keys
and environments. What it reads is listed under `found` and kept in the
inferred record, and a chart dependency with no `Chart.lock` digest is a
finding ([what init reads](pactify.md#what-pacto-reads-from-your-folder)).
A chart-only folder plans as `blank`. To have init write the workload
from the chart, render the chart into the folder, then init reads the result:

```bash
mkdir -p services/ledger/k8s
helm template --include-crds ledger services/ledger/chart \
  > services/ledger/k8s/rendered.yaml
```

Once init has written the contract, delete the rendered file:
[`pacto ci --publish`](publish-from-ci.md) publishes the chart itself as the
deploy artifact, and a folder with both a chart and manifests is a finding
that asks for a `publish` section.

**Compose files.** Init reads the image, ports and environment keys of
`compose.yaml` into the inferred record, and a Compose-only folder still gets
the blank template. The blank template writes no publish section and no
`service.version`: [`pacto ci --publish`](publish-from-ci.md) derives both,
publishing to the registry it resolves or the one `--registry` names.

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
- **Dependencies.** Replace each `compatibility: unknown` with the versions the
  service accepts. Until you do, impact cannot tell whether a change to that
  dependency breaks the service: its verdict is unknown, and a BREAKING change
  to the dependency stays unverified.

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
namespace even with `--namespace`. It never lists or reads a Secret or a
ConfigMap.

- The read lists each kind of object in pages and stops at `--max-items` items
  per kind (10000 by default).
- `--call-window` sets how far back a call counts as a dependency (24h by
  default).
- A run `--max-items` stopped while listing workloads prints a `Continue:`
  line with the same command and `--continue <token>`. Another kind of object
  cut short stays `partial`, and a resumed run lists it again. Run it to resume the read where it
  stopped. A token made for other namespaces is refused.
- A kind of object your identity may not list reads as `failed` with
  `forbidden`, and the run goes on. The run fails only when it reads no
  workload at all.
- The `Coverage:` line names each kind of object the read did not see whole,
  with its state, `partial` or `failed`, and a reason. A draft built from a
  partial read can miss dependencies and callers, so treat what it lacks as
  unknown.

Each draft holds a `manifests.yaml` with the workload and its Services as the
cluster returns them, without namespace, uid or status. It also holds a
`pacto.yaml` and a `.pactoignore`. `--plan` writes nothing. A workload that a
Pacto targets is skipped. When the Pactos were not all read, the draft of a
workload says under `Unknown` that a Pacto may already describe it. A container environment variable that sets a
sensitive key to a literal value is read in the draft from the Secret
`<name>-env`; create it before you apply the draft with
`kubectl -n <ns> create secret generic <name>-env --from-env-file=<file>`, from
a file of `KEY=value` lines. When such a value is a URL, the draft no longer
lists the service it names as a dependency, and next steps ask you to add it.
A literal secret in a container's `command` or `args`, such as
`--db-password=hunter2`, moves to the same Secret: the draft reads it through
`$(DB_PASSWORD)` from an environment variable of that container.

### The inferred record of a draft

Each draft also holds a `pacto.inferred.yaml`, completed by the engine
`pacto ci` uses, with one entry per field Pacto could tell
([Inferred record](contract-reference/inferred-record.md#cluster-facts)).

- Each entry names its source: `manifest` or `label` for what the draft's
  `manifests.yaml` shows, `runtime` for what the pods ran and called, and
  `fleet` for the versions the Pactos of a called Service report.
- Each cluster entry carries `asOf`, when it was read. A `runtime` or `fleet`
  entry also carries its completeness: `partial` when not every node the
  workload ran on was observed or a source was cut short.
- `coverage` gives the state of each kind of object read, and `hidden` counts
  the callers and Services a read on behalf of a viewer may not show.
- The running image digest of each container is recorded as
  `image.digest[<container>]`. It is never declared and never becomes a
  deploy artifact.
- A proposed range sits in a `proposed` value beside
  `compatibility: unknown`, never declared.
- When the draft folder already holds a `pacto.inferred.yaml`, it is kept and
  next steps say to delete it to record what the cluster shows now.
- `pacto validate`, `pacto explain` and `pacto publish` recompute the record
  from the folder, so the cluster facts stay in the draft. A published bundle
  whose record carries cluster facts fails validation with
  `INFERRED_RECORD_INVALID`, unless the record holds a declaration.
- `pacto declare <workload> --registry <registry>` publishes the draft as a
  contract-only version you sign, with its cluster facts; a key or an ambient
  token writes a `draft-<n>` tag a person accepts
  ([Declare a running service](publishing.md#declare-a-running-service)).

### Dependencies of a draft

With the [node agent](runtime-agent.md), the Services a workload's pods were
seen calling become dependencies with `compatibility: unknown`. Each gets a
proposed range from the lowest version the reconciled Pactos describing the
called Service report, and none when those Pactos disagree on the contract.
Without the agent, dependencies come only from
environment values, as in a folder. The draft holds no ConfigMaps, so each one
the workload reads is named under `unknown` and its keys stay out of the
configuration schema. A value such as `host:6443` matches whether or not `host`
is a Service, so a control-plane endpoint can become a dependency. Check each
one.

## From an agent

The MCP tool `pacto_create` with `pactify: true` and `paths` plans the same
contracts for an agent. It plans only, unless the agent sets `dry_run` to
false ([AI assistants](mcp-integration.md)).

- With `cluster`, it takes `max_items`, `call_window` and `continue`, the
  `--max-items`, `--call-window` and `--continue` flags. A read stopped by
  `max_items` returns `continue`, and the next call passes it back to resume
  the read.
- `pacto_declare` plans a declaration of a running workload. An agent's write
  is a `draft-<n>` tag, and the person it acts for accepts it with
  `pacto declare <workload> --from-draft draft-<n>`
  ([the agent write contract](mcp-reference.md#declare-a-running-service)).

## What's next

| Goal | Page |
|------|------|
| Publish a contract and connect it to delivery | [Pactify a service](pactify.md) |
| Check every change in CI | [Put it in your repository](ci.md) |
| Write the fields init leaves out | [Contract sections](contract-reference/sections.md) |
