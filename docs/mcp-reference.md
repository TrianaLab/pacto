# MCP reference

This page lists the tools, inputs and limits of `pacto mcp`. For a
walkthrough, start with [Drive Pacto from an agent](mcp-integration.md).

## Three tool families and their boundaries

| Family | Tools | What they do | Safety boundary |
|--------|-------|--------------|-----------------|
| **Authoring** | `pacto_create`, `pacto_edit`, `pacto_check`, `pacto_schema` | Create, edit and validate Pacto *contracts*. | Operate on contract files only. `pacto_edit` writes only after validation. Over HTTP only `pacto_schema` is served. |
| **Generated service** | Derived per operation from a bundle's OpenAPI interfaces (`getUser`, `createRefund`, …) | Invoke the *live service* the contract describes. | Read-only (`GET`/`HEAD`) unless you pass `--allow-writes`; every call has a 30-second timeout and follows no redirect, so a 3xx comes back to the agent as the result. |
| **Fleet query** | `pacto_fleet_search`, `pacto_fleet_get`, `pacto_fleet_graph`, `pacto_fleet_status`, `pacto_fleet_explain`, `pacto_fleet_entities`, `pacto_fleet_entity`, `pacto_fleet_attention`, `pacto_fleet_neighborhood`, `pacto_fleet_versions`, `pacto_fleet_revision_document`, `pacto_fleet_overview`, [`pacto_impact`](impact.md#the-mcp-tool-and-the-dashboard) | Read-only queries over the *operational system*: services, revisions, targets, owners, business domains, offerings, [external systems](external-catalog-reference.md), relationships and what needs attention. `pacto_fleet_get`, `pacto_fleet_graph` and `pacto_fleet_explain` take `external:<identity>`, and `pacto_fleet_entities` lists them with `kind: external`. `pacto_impact` projects a contract diff onto that system to report which consumers a change affects, or asks what happens if a service becomes unavailable, is removed or regresses. `pacto_fleet_get` returns each revision's bundle signature check and OCI referrers as `provenance`, and the service's `ownerProvenance`: where its owner comes from, `declared`, `declared-via-target`, `attested` or `inferred`, with `ownerSource`, the file, commit and CI signer an `attested` owner was read from. `pacto_fleet_revision_document` reads one document a revision's bundle carries, as the dashboard's revision page does. `pacto_fleet_overview` answers "how healthy is the organization and how much do we know" in one call, as `pacto fleet overview` does: a target is failing when its own health check reports it is not healthy and unknown when Pacto cannot tell, and a failing check leaves compliance `Unknown`; see [Organizational health and knowledge](operational-graph.md#organizational-health-and-knowledge). The fleet tools mark target evidence older than 24 hours as stale unless `--freshness` says otherwise. `pacto_fleet_entities` takes `consumes` to list the services that use an [offering](platform-offerings.md#consumers), and with `phase` and `environment` only those whose provisioning status in that phase is in that environment. For a service, `pacto_fleet_entity` lists the [resources it consumes](provision-infrastructure.md#resources-a-service-consumes) with `providerOwner`, `providerAvailability`, `requested` (declared) and `consumption`, plus `hiddenProviders`, and its `changes` in version order; `reached: true` on a stdio server adds when each change reached a Stage, from delivery records, and any other server answers `times: not-read`; `pacto_fleet_attention` and `pacto_fleet_status` report `PROVISIONING_FAILED` and `PROVISIONING_STALE` in the `provisioning` category. `pacto_fleet_status` also carries `providers`, the [providers Pacto detected](operational-graph-providers.md) with their evidence, what each is used for and their status, and `providersTruncated` when the list was cut. With `--detected`, `pacto mcp --fleet` adds the [rows the operator detected](operational-graph-providers.md#detected-rows), each `provenance: inferred` with its `asOf` and without its `hint`. | Always read-only: they write nothing. They still make network calls, and the server that hosts them may also register write tools; see [Fleet query safety](#fleet-query-safety). Catalog mode is the only server with no write tool. |
| **Fleet query**: member clusters | `pacto_cluster_list` | Lists the member clusters that joined this hub through the registry: approved, pending with why and removed, and per approved member its `mappings`: the group mapping in force per issuer, with who approved it and when. See [Member clusters](member-clusters.md). | Always read-only; no tool approves or removes a member. It appears with `--fleet --k8s --detected k8s:<namespace>/<name>` when the operator advertises a registry. A signed-in caller who may not approve `clusterjoins` sees the approved members only, the rest counted in `hidden`. Only a person writes a group mapping, with `pacto cluster map-groups`. |
| **Fleet query**: statistics | `pacto_stats` | Totals the unsigned run records of a stats repository for a window. See [the stats answer](run-record-reference.md#the-stats-answer). | Always read-only. Over HTTP it appears only when the server was started with `--stats-read-registry` and reads only that repository; a signed-in caller sees only records of services in their view. |

Outside these families: `pacto_capabilities` (what this server serves, see [Combine modes](#combine-modes)), `pacto_skill` (a bundle's skill files) and `pacto_catalog_revision` (one revision from the catalog, see [Catalog mode](#catalog-mode)). The publish and delivery tools, including the seven delivery read tools (`pacto_delivery_trust` among them: which publishers each service accepts), are listed in [MCP publish and delivery tools](mcp-delivery-tools.md).

These tasks are CLI-only: inspecting a registry contract, resolving a dependency
graph, diffing revisions, generating docs and the two non-query `pacto fleet`
operations, `reconcile` (declared dependencies against observed traffic) and
`snapshot` (the whole read model as one document). Only `pacto_publish` pushes,
and no tool deploys. In delivery mode an agent asks for a change; Pacto's
checks and people decide. [Server modes](cli-reference.md#server-modes) lists which tools each
invocation registers.

!!! warning "Annotations are hints"
    Every tool carries MCP
    [`annotations`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#tool-annotations).
    `pacto_check`, `pacto_schema`, `pacto_skill`, `pacto_fleet_*`, `pacto_impact`, `pacto_stats`, `pacto_cluster_list`,
    `pacto_catalog_revision`, `pacto_publish_plan` and the delivery reads set
    `readOnlyHint`. `pacto_edit` sets `destructiveHint`: it removes entries in
    place. A generated tool takes its hints from its
    HTTP method: `GET` and `HEAD` are read-only, `PUT` is idempotent and `DELETE` is
    destructive. Every other method leaves `destructiveHint` unset, which
    MCP clients read as destructive. Hints are advice to the client;
    `--allow-writes` and the stdio-only rules decide which write tools exist.
    A bundle cannot claim a `pacto_` name: Pacto skips every bundle operation
    whose name starts with `pacto_`.

### Fleet query safety

- **They are read-only.** They project the [Pacto Operational Graph](operational-graph.md) and write nothing. Every fleet tool, `pacto_impact` included, answers from one shared snapshot. Over stdio `pacto_impact` resolves both refs with your credentials; when no source holds the changed service, it rebuilds the snapshot with the new revision added, reaching every source (your cluster too if `--k8s` is on), and it reads the organization index, reported in `consumerIndex`; an HTTP server never reads it. An HTTP server, whether its callers sign in or not, reads no `traces` path and accepts only `oci://` refs that name a revision in the caller's view, by requested ref, resolved ref or digest; it then reads that revision's own digest-pinned ref. A revision read without a recorded digest has no pinned ref, so it is refused. It also reads no delivery evidence: a `regressed` event over HTTP carries `DELIVERY_EVIDENCE_UNAVAILABLE`. Run `pacto mcp --fleet` over stdio to read them with your own credentials. The fleet snapshot of an HTTP server holds local bundles only from the `--local` roots it was started with; over stdio `--local` defaults to the folder the server was started in.
- **Target snapshots are read over stdio only.** On a stdio server `pacto_impact` reads the change's signed [target snapshot](ci.md#where-consumers-come-from), as `pacto impact` does, unless the server names a target source of its own (`--k8s`, `--target-state`, `--evidence-url`, `--target-snapshot`) or was started with `--target-snapshot off`, and returns `registry` (the `pacto-targets` repositories it tried and the one it read) and `targetSnapshots` (each snapshot's pinned `ref`, `signer`, `cluster` and status). `pacto mcp --fleet --target-snapshot <oci://ref>` adds snapshots to every fleet tool. A server over HTTP refuses `--target-snapshot` at startup and its `pacto_impact` reads none, because restriction reviews a viewer in the local cluster only.
- **`running` needs the whole fleet.** `pacto_impact` with `running` and `new_ref` alone judges `new_ref` against the revisions of its service that run on known targets, as [`pacto impact --running`](impact.md#against-what-runs) does, and returns `running` naming each ref, the one judged and the unread ones. A server whose answers are restricted to your view refuses it, because a hidden target could hide a running revision. Over HTTP it takes only an `oci://` `new_ref` in your view.
- **They expose knowledge only.** They never grant, scope or revoke a permission.
- **A `nextAction` is a suggestion.** An attention item whose fix is a contract edit names `edit-contract` and `pacto_edit` in `nextAction`, and `INFERRED_PROVIDER` names `declare` and `pacto_declare` ([attention categories](operational-graph-tallies.md#attention-categories)); Pacto infers it from the item's code, and the edit itself decides.
- **`rangeReason: INFERRED_PROVIDER` is never a pass.** A `rangeStatus: unknown` with that reason means the provider is an [inferred contract](inferred-contracts.md); treat the range as unverified.
- **Partial or stale results are incomplete knowledge.** Every answer carries an `asOf` time, a `completeness` value and structured `limitations`. Branch on those before trusting an answer.
- **Each cluster has its own status.** When the snapshot knows a cluster, every fleet answer's meta lists `clusters`: each cluster's `id`, `name`, `status`, `access` (why a restricted view shows nothing of it), `targets` and, on a hub, `link`: a member's channel (`live` or `down`) and its identity model per token issuer (`shared-issuer`, `group-mapping` or `issuer-not-accepted`). `pacto_fleet_entities` takes `cluster`, a cluster id, to keep that cluster's targets and the services and revisions that run there. See [Multi-cluster](multi-cluster.md).
- **Under partial coverage, a missing result means unknown.** If a source was unavailable, "not found" means "not known here".
- **The snapshot refreshes on demand.** A call 30 seconds or more after the last rebuild attempt rebuilds it first, reaching your sources again. Calls inside that interval share one `snapshotId`; a failed rebuild keeps the last good snapshot.
- **The server keeps no state.** Durable evidence lives in an [Evidence Server](evidence.md); the MCP server's state ends with its process.

`--fleet` names its sources the same way `pacto fleet` does; see the [`pacto mcp` reference](cli-reference.md#pacto-mcp):

```bash
pacto mcp --fleet --k8s --oci ghcr.io/acme/payments-api-pacto:2.1.0
```

`--mira-source <url>` adds the calls [Mira](investigate-a-problem.md) saw.

### Link answers to the dashboard

With `--dashboard-url <url>`, each entity in a `pacto_fleet_*` answer except
`pacto_fleet_get` and `pacto_fleet_explain` carries an `href` to its page on
that dashboard, as does each contracted `pacto_impact` consumer.

## Combine modes

A bundle reference, `--fleet` and `--delivery` combine in one server:

```bash
pacto mcp --fleet --k8s --delivery
```

`--root` and `--publish` each start a server of their own. Pacto registers
its own tools before a bundle's, so a bundle never replaces one, and the
instructions describe only the served modes.

`pacto_capabilities` reports `modes`, the contract tools in `authoring`,
`writes`, `transport`, `asOf` and the keys `fleet`, `impact`, `observed`,
`delivery` and `installation` of the dashboard's `GET /api/capabilities`.
`caller.callerSource` says how Pacto knows `caller.user`: `reviewed` by the
API server, a verified `token` (`user` is then `unknown`) or `local` (stdio or
the loopback launch bearer). With delivery, `caller.class` is the actor class as read at
`caller.classAsOf` (startup over stdio); `caller.policyError` marks policy
defaults.

| `installation` part | Provenance | Value |
|---|---|---|
| Every part | - | Carries `source`, `asOf`, `provenance` and `completeness` |
| `profile` | declared | The [chart's profile](integrations/kubernetes/profiles.md) names from the operator's detection; `null` with the reason in `source` without `--detected`, from an older operator, for a cluster your view hides or before a snapshot holds the cluster's Installation row |
| `registry` | declared | The operator's `registry` value, empty when set to nothing |
| `delivery` | observed | `enabled` says the cluster serves the delivery API, from this server's delivery probe (its `asOf` is when the probe last asked), `null` when it cannot tell; `partial` beside profile hardened, whose kept CRDs still serve it. `appliers` inferred from available Argo CD and Flux rows that count an Application, Kustomization or HelmRelease in the installation's cluster; `partial` when no snapshot or workload source read them |
| `provisioning` | inferred | `engine` stays `unknown` until a source detects one |
| `clusters` | observed | `joined` counts the clusters in your view of the snapshot; `pending` is `null` |
| `mcp` | host | `served` is `true` on the MCP server and `false` on the dashboard |

## Tool-call log

With `--verbose`, the server logs one `mcp tool call` line per call to stderr
for the people who run it: `tool`, `caller`, `callerSource`, `readOnly`,
`outcome` (`ok`, `error` or `failed`), `durationMs`, `snapshotId` for fleet
answers and `argsDigest`, an HMAC-SHA256 of the arguments under a per-process
key, so the log holds no value an agent passed. A call to a tool the server
does not serve, or with arguments its input schema refuses, runs nothing and
logs no line.

## The authoring tools

Every mode except `--root` carries these four,
[`pacto_declare`](#declare-a-running-service) and
[`pacto_accept`](#accept-proposed-ranges-and-breaks) over stdio. Over HTTP only
`pacto_schema` and a planning `pacto_declare`.

| Tool | Description |
|------|-------------|
| `pacto_create` | Create a new contract from intent-level inputs. Supports dry run. |
| `pacto_edit` | Edit an existing contract, including its configuration references, environments and their values and the service lifecycle. Supports dry run. |
| `pacto_check` | Validate a contract and return errors, warnings and suggestions. A contract with an older `pactoVersion` gets the info warning `CONTRACT_BEHIND` and a suggestion that says whether raising it keeps the contract's meaning. |
| `pacto_schema` | Return the full JSON Schema reference. |

**Structured inputs are JSON-encoded strings.** Every input that carries structure has the MCP wire type `string`, and the value is JSON serialised into a string:

```json
{
  "name": "orders",
  "interfaces": "[{\"name\":\"api\",\"type\":\"openapi\"}]"
}
```

!!! warning
    Passing a real JSON array or object where the JSON-encoded string is expected is a **silent no-op**. The argument is discarded, the call still succeeds, `changes` comes back `null` and nothing is written. Check the result's `changes` and `summary`. `pacto_check`'s own "add an interface" suggestion carries `add_interfaces` as a real array, so an agent that forwards that tool call verbatim hits this case.

**`pacto_check` is `pacto validate`.** It runs the same validation, including `pacto.lock` verification and recursive `policies[].ref` resolution, so a contract it accepts passes CI. Its result carries the same `stages` and `next` as `pacto validate --output-format json`, and each error carries `line` and `column` when Pacto can locate it.

**`pacto_check` compares environments:**

- `environments` takes two or more distinct comma-separated names, such as `production,pr-482`, and the result also lists the configuration values those environments disagree on, as `pacto explain --environments` does.
- Left empty, it compares every declared environment when the contract declares two or more.
- `comparedEnvironments` names the environments compared. When it is present and `environmentDifferences` is absent, the environments agree.

**`add_config_properties` writes the schema.** `pacto_edit` merges the properties into the first configuration's local schema file (`configuration/schema.json` when it has to declare one). A property already in the schema is replaced and `required` names are unioned.

**`pacto_edit` edits configuration references, environments and the lifecycle:**

- `add_configuration_refs` adds references to published contracts whose configuration the service takes. Each entry's `configuration` picks one of the referenced contract's configurations and `values` holds the service's values for it. Either field raises `pactoVersion` to 2.4.
- `remove_configuration_refs` removes references by name or ref, and removes their values from every environment file.
- `add_environments` declares environments and creates each missing file holding `{}`. Each entry may set `lifetime` (`temporary` or `persistent`), `expires` (an RFC 3339 time, only with `temporary`) and `owner` (`{team, dri?, contacts?}`). It raises `pactoVersion` to 2.3, or to 2.4 when an entry sets `lifetime`, `expires` or `owner`; see [temporary environments](contract-reference/environments.md#temporary-environments).
- `remove_environments` removes environments by name, such as `["pr-482"]`, and deletes each file no remaining environment names. Nothing in a cluster changes.
- `set_environment_values` merges values into environment files. Objects merge key by key and a `null` removes its key.
- `lifecycle` sets `service.lifecycle` (`deprecated`, `sunset`, `successor`), and `{"clear": true}` removes it. Setting it raises `pactoVersion` to 2.4.
- `template` sets [`service.template`](contract-reference/sections.md#golden-path-template), which makes the contract a golden path: `{"plugins": [...]}` replaces it, an empty `plugins` list writes `template: {}` and `{"clear": true}` removes it. Either raises `pactoVersion` to 2.4. No input sets `service.origin`: only creation from a golden path writes it.
- A field that needs a newer `pactoVersion` raises it only when validation finds nothing new at that version, and `changes` reads `set pactoVersion to "2.4" (was "2.3")`. When the raise would add a finding, such as `ENVIRONMENT_VALUES_MISSING`, the edit fails with that finding and writes nothing. When the contract already fails validation, the edit fails naming its first error; run `pacto_check`.

**Value edits are checked first.** Adding configuration references or environments, or setting environment values, writes only after `pacto_check` passes.

### What an edit leads to

- `pacto_edit` returns `impact`, `chain` and `executor`, with `dry_run` or without. `impact` is who the edit affects, analysed like `pacto_impact` from the contract on disk (`oldVersion`) to the edited one (`newVersion`) for the service the fleet holds under that name: `classification`, `gate`, `gateReason`, `affected` (the consumer count), `hidden`, `owners`, `templates` (golden paths to review), `approvals`, `completeness`, `asOf`, `snapshotId` and `limitations`. `approvals` names the owners of the consumers the edit breaks and always ends with the promote's own plan deciding. A gate is never clear when the fleet holds no revision at `oldVersion` or no source holds the service. `impact` is present only on a server that also serves `pacto_impact` (`pacto mcp --fleet` over stdio); when the session has no snapshot, the snapshot holds no single service of that name or the analysis fails, it is absent and the first `chain` step says who the edit affects is not known.
- `chain`: the steps that carry the edit to running, in order: `pacto_impact` when impact is not known (after a dry run, run it once the edit is written), then `pacto_publish_plan`, `pacto_publish` and `pacto_delivery_operation_plan`. Each step has `command` (the CLI form), `why`, `tool` and, for the publish steps, `args` with the contract's `path`. A step whose tool this session does not register leaves `tool` empty and its `why` says so.
- `executor`: who acts on the published revision. For configuration references and environments it is the provider (an adapter or a GitOps tool); otherwise Flux or Argo applies the version a Stage points at.
- `pacto_create` from intent-level inputs (without `pactify` or `from`) returns `chain` too, from publish on: a new service has no published revision to compare with. A consumer that already declares it lists that dependency as unresolved until it is published. Neither tool runs a step of the chain.

### How `pacto_create` reads a description

`description` is matched against fixed keyword lists, case-insensitively and on whole words. A match only fills in what the call left unsaid: explicit `interfaces` or `workload` always win, and every inference is listed in the result's `derived`.

| Keywords | Inferred |
|----------|----------|
| `REST`, `HTTP`, `API`, `web` | a public `openapi` interface named `http-api` |
| `gRPC` | an internal `grpc` interface named `grpc-api` |
| `event`, `kafka`, `nats`, `pubsub`, `queue`, `message`, `rabbit` | an `asyncapi` interface named `events` |
| `worker`, `consumer`, `processor` | a `job` workload |
| `cron`, `scheduled`, `periodic`, `batch` | a `scheduled` workload (wins over `job`) |

The match is a keyword heuristic: "no HTTP" still matches `HTTP`. State is never inferred: naming `postgres` declares a database dependency and leaves the service stateless.

### Declare a running service

`pacto_declare` plans what
[`pacto declare`](publishing.md#declare-a-running-service) would publish for a
running workload.

- It is the first tool of the agent write contract, which every MCP tool that
  writes to a registry for an agent follows. `pacto_publish_plan` and
  `pacto_publish` stay outside it.
- It takes `service`, `namespace`, `dir` (stdio only), `registry`, `accept`,
  `on_behalf_of`, `dry_run`, `intent_digest`, `max_items` and `call_window`.
- `dry_run` defaults to true: the call returns the plan and its `intentDigest`.
- A write needs `dry_run: false` and the `intent_digest` of the plan the agent
  reviewed. The digest binds what would be declared, never the cluster facts
  read at the time, so a re-read with the same content keeps it.
- Every MCP caller is an agent, so a write is a write-once `draft-<n>` tag
  that names the person in `on_behalf_of` (required, asserted and never
  verified). `acceptCLI` is the `pacto declare --from-draft` command that
  person runs to read the workload again and sign. A draft is never a version
  and the fleet never lists it.
- Over stdio the draft is signed only by an ambient CI or agent token. A
  person's ambient token is never used by an agent, so that draft and one with
  no token are unsigned.
- Over HTTP the plan reads the cluster with the caller's own exchanged token
  and only the registries `--oci` names. It returns the baseline only when the
  caller may see the service. A write is refused with `ExecuteNeedsStdio`.
- A plan that changes `service.owner` lists who may set it in `mayAccept`;
  see [Who may set a service owner](owner-setting.md). An HTTP server leaves
  it out, with a generic refusal, for a caller who may not see the service.

A refusal carries `code`, an enum in the output schema, `message` and `cli`,
the command a person runs:

| Code | Meaning |
|---|---|
| `PlanStale` | The plan changed since the `intent_digest` was reviewed |
| `IntentRequired` | A write passed no `intent_digest` |
| `WorkloadNotFound` | No workload has that name, or `dir` holds no cluster draft |
| `WorkloadAmbiguous` | Several workloads have that name: pass `namespace/Kind/name` |
| `AlreadyDeclared` | A Pacto already describes the workload: change it with `pacto publish` |
| `RangeNotProposed` | An `accept` name has no proposed range |
| `NoRegistry` | No `registry`, or over HTTP one outside the installation's registries |
| `RegistryUnreadable` | Listing or reading the registry failed |
| `VersionConflict` | The version is taken, or no tag is signed by a publisher the installation trusts |
| `SignerNotAllowed` | The signer is outside the installation signer policy |
| `SignerUnverified` | The pushed signature does not name the planned signer; nothing was tagged |
| `DraftNotFound` | The draft tag does not exist or holds no draft of the service |
| `ExecuteNeedsStdio` | An HTTP server only plans: run the `cli` command |
| `BaselineNeedsMigration` | The baseline is below `pactoVersion: "2.5"`: run `pacto migrate` |
| `BundleRepoMismatch` | The baseline publishes to another repository than `registry` |
| `OWNER_SIGNER_NOT_PERSON` | Only a person sets `service.owner`: an agent, CI or workload signer never does, whoever it acts for |
| `OWNER_SIGNER_NOT_ALLOWED` | The person is no member of the current owner and no default approver |
| `OWNER_POLICY_UNVERIFIED` | No verified owner policy was read, so who may set `service.owner` is unknown |
| `OWNER_SIGNER_UNVERIFIED` | No signature of the owner-setting version verified |

### Accept proposed ranges and breaks

- `pacto_accept` plans what [`pacto accept`](ci.md#accept-proposed-ranges)
  would sign for the newest published version of a service and follows the
  same agent write contract as `pacto_declare`. It is served over stdio only.
- It takes `service` (a bundle repository or a checkout folder), `proposed`,
  `fields`, `break` (`<provider>@<version>`), `from`, `on_behalf_of`,
  `dry_run` and `intent_digest`.
- The plan names each field with its value and source, or the break, the
  acceptances already attached, who may accept (the service's declared owner)
  and the `intentDigest`.
- A write writes a write-once `pacto-accept-draft-<n>` tag in the service's
  bundle repository carrying `on_behalf_of`, never a signed acceptance, even
  with a person's ambient token. `acceptCLI` is the
  `pacto accept <service> --draft <ref>` command that person runs to sign.
- Its refusals add these codes to the table above:

| Code | Meaning |
|---|---|
| `AcceptNothingProposed` | The newest published version proposes nothing to accept |
| `FieldNotAcceptable` | The field is no proposed dependency `ref` or `compatibility`: declare it with `pacto declare` |
| `BreakNotAnnounced` | The provider version announces no break for this service, published or in `from` |
| `NotPublished` | The service has no published version to attach the acceptance to |
| `PersonSigningUnavailable` | Accepting a draft needs a person's interactive Sigstore login |

## Bundle capabilities

Pass a bundle reference and Pacto turns that bundle's OpenAPI operations into executable agent tools:

```bash
pacto mcp ./my-service --base-url https://api.example.com
```

Each operation becomes a tool named by its `operationId` (or `<method>_<path>`).

**The `pacto_` names are reserved.** A contract declaring `operationId: pacto_check` would take over the authoring tool. Pacto skips such operations.

**Read-only by default.** Only `GET`/`HEAD` operations are exposed unless you pass `--allow-writes`. The live host comes from `--base-url`, falling back to the spec's `servers[0]` URL. When you supply credentials, `--base-url` is required.

**Service authentication (`--auth`).** Credentials are supplied per OpenAPI security scheme with `--auth name=value`:

| Scheme type | How the credential is applied |
|-------------|-------------------------------|
| `apiKey` | Sent as the declared header or query parameter |
| `http` `bearer` (and `oauth2` / `openIdConnect`) | `Authorization: Bearer <value>` |
| `http` `basic` | `Authorization: Basic <value>` (supply pre-encoded `user:pass`) |

**`pacto_skill`** lists a bundle's `skills/*.md` files and returns their content.

**What a session freezes.** Bundle mode and `--root` resolve their inputs once at startup; restart to pick up changes. Two exceptions: `pacto_skill` reads from disk on every call, and the authoring tools act on whatever is on disk.

## Catalog mode

`pacto mcp --root <ref>` starts a read-only contract catalog: the roots you name, plus their dependency closure, resolved once at startup and then frozen for the life of the process.

```bash
# One published platform, one contract you are still working on
pacto mcp \
  --root oci://ghcr.io/acme/platform:1.4.0 \
  --root ./experimental-platform
```

`--root` is repeatable and takes a local bundle directory or an `oci://` reference. It never crawls a registry, guesses repository names or reads a catalog file.

The surface is two MCP *resources* (`pacto://catalog` and `pacto://catalog/closure`) plus one tool (`pacto_catalog_revision`). The catalog resource carries the schema version (`pacto.dev/catalog/v1`), catalog id, generation time, bounds, completeness and every requested root, resolved or not and why. The closure resource carries every deduplicated revision (content identity, rank and retained paths), every dependency edge and every conflict and cycle.

Catalog mode registers nothing else, so it cannot modify a contract. A root or a dependency that could not be resolved stays visible with a category such as `NOT_FOUND`, `AUTH_FAILED` or `UNAVAILABLE` in place of the raw registry error, and the whole answer is marked `partial`. Under `partial`, a revision missing from the answer is *unknown*.

## Create a service's files with pactify

`pacto_create` with `pactify: true` does what `pacto init` does for a folder
holding a service: it reads the Dockerfile and Kubernetes files and writes the
contract. See [Pactify a service](pactify.md).

- `describe_only: true` makes the next steps say how to validate the
  contract instead of how to publish it, for a request to describe a service.
  `delivery: true` also writes the cluster files and is refused with
  `describe_only`.
- `domain`, `registry`, `environments`, `fleet_roots`, `insecure_registry`
  and `registry_secret` match the CLI flags, and `dry_run` plans without
  writing. `max_items`, `call_window` and `continue` match the CLI flags too
  and go with `cluster`. `no_transparency_log` applies with `from` only: it writes
  `transparencyLog: false` under the golden path's `publish.sign`.
- The contract has no publish section and no `service.version`:
  `pacto ci --publish` derives both.
- External dependencies are classified with the catalog the server was
  started with (`--external-catalog`).

Without `pactify`, `pacto_create` refuses a folder that already holds
`pacto.yaml` (change that contract with `pacto_edit`) and leaves any interface
or schema file already there as it is, listed in `kept`.

## Create a service from a golden path

- `pacto_create` with `from` creates a service from a
  [golden path](golden-paths.md), like `pacto init --from`.
- `from` is an `oci://` reference or a directory. `name` and `owner` are
  required; `domain`, `path`, `registry`, `describe_only`, `delivery`,
  `environments`, `registry_secret`, `insecure_registry` and `dry_run` match
  the CLI flags.
- `values` is a JSON object merged over the path's contract and `set` a JSON
  array of `key=value` overrides, like `--values` and `--set`.
- `run_plugins` is a JSON array of plugin names, each one the path lists.
  Nothing else runs. A `dry_run` lists each plugin and the binary it runs.
- `from` refuses the intent inputs, such as `interfaces`, and `pactify`,
  `fleet_roots` and the cluster inputs.
- `pacto_create`, `pacto_edit` and `pacto_check` serve stdio only. Over HTTP
  every caller would pull with the server's registry credentials, read its
  directories and write to its paths, so an HTTP server offers only
  `pacto_schema` from the contract tools.
- `pacto_fleet_entities` with `template: only` lists golden paths.
- `declaration` on `pacto_fleet_entities` and `pacto_fleet_search` takes
  `inferred`, which keeps only the services Pacto infers from Kubernetes
  workloads no contract describes, or `declared`, which leaves them out. Empty
  lists every service and marks the inferred ones `declaration: inferred`.
- `group` on `pacto_fleet_entities` takes a group key, `workload.group.key`,
  and keeps the inferred services the cluster groups under it. An inferred
  service's `workload.group` says how the cluster groups it and where Pacto
  read that, `workload.repositories` lists the source repositories its Argo CD
  Application or Flux GitRepository names, and `workload.repositoryUnknown`
  says why none shows; see
  [Groups and repositories](operational-graph-inferred.md#groups-and-repositories).
  List rows carry `group` and `repository`.
- `pacto_fleet_graph` lists runtime calls with an inferred service on one end
  in `edges` as `inferred`, and `pacto_fleet_get` and `pacto_fleet_graph`
  answer the key of a
  [replaced inferred service](operational-graph-inferred.md#when-a-contract-takes-over)
  with the service that replaced it and `replacedFrom`.
- `pacto_fleet_get`, `pacto_fleet_explain` and `pacto_fleet_entity` carry,
  for a service whose current revision carries a declaration or that replaced
  an inferred service through one, `fields`: each field of its current revision as `declared`,
  `accepted` (with `by`), `attested` or `inferred`, with `source`, `asOf`,
  `completeness` and, for a range left `unknown`, `proposed`, and
  `replacesHidden`, the replacements whose workload the caller may not see.
  `replaces` on `pacto_fleet_get` and `pacto_fleet_entity` lists the inferred
  services the service replaced, with `link: inferred-from`, `revision`, `by`
  and `workload` when a person's declaration names one.
  `pacto_fleet_explain` adds one `REPLACES_INFERRED` reason per replacement;
  see
  [When a declaration replaces an inferred service](operational-graph-inferred.md#when-a-declaration-replaces-an-inferred-service).

`pacto_create` and `pacto_edit` return `warnings` for a value that validates
but is likely wrong, such as an owner written as `team/billing`.
`pacto_schema` returns the JSON Schema and, under `agents`, how to describe an
AI agent as a service.
