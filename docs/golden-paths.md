# Create a service from a golden path

A golden path is a contract your platform team publishes as a starting point
for new services. It is an ordinary signed Pacto bundle whose contract declares
`service.template`. A developer finds it, reads what it requires and creates a
service from it with `pacto init --from`. The fleet then links the new service
back to the path it came from.

This guide follows the `http-service` path of the
[demo](examples/demo-tour.md#story-8-start-a-service-from-a-golden-path) from
both sides.

## Publish a golden path

A golden path holds the contract and files every new service of its kind
starts with:

```yaml
pactoVersion: "2.4"
service:
  name: http-service
  version: 1.0.0
  owner:
    team: platform
  summary: An HTTP API on the platform runtime with a PostgreSQL database
  template:
    plugins:
      - name: dockerfile
        description: A Dockerfile for a Go HTTP service
      - name: github-actions
        description: A CI workflow that validates and publishes the contract
        options:
          go-version: "1.27"
dependencies:
  - name: postgresql
    ref: oci://ghcr.io/trianalab/pacto/postgresql
    required: true
    compatibility: ^16.0.0
```

Beside the dependency, a configuration reference requests a database from the
PostgreSQL offering:

```yaml
configurations:
  - name: database
    ref: oci://ghcr.io/trianalab/pacto/postgresql:16.1.0
    configuration: provisioning
    required: true
    values:
      size: small
      storageGi: 20
```

- `service.template` marks the contract as a golden path. Each entry of
  `plugins` names a [plugin](plugins.md) the path offers to run, with the
  `options` it receives.
- `service.summary` is what the Golden paths list shows.
- The configuration references and dependencies point at the
  [offerings](provision-infrastructure.md) every service created from the path
  requires, and policy references at the platform rules it must meet. The
  fleet lists them as the path's requirements.
- Files such as `interfaces/openapi.yaml` or `docs/` are copied into each new
  service.

Publish each version with `pacto publish`, signed with the key under
`publish.sign`, so developers can check who signed it.

## Find one

A golden path runs nowhere, so no Pacto resource points at it. Make your paths
visible on each surface by naming their repositories:

- CLI: `pacto fleet ... --oci oci://ghcr.io/acme/paths/http-service`.
- MCP: `pacto mcp --fleet --oci oci://ghcr.io/acme/paths/http-service`.
- Local dashboard: the repository as an argument,
  `pacto dashboard oci://ghcr.io/acme/paths/http-service`, or a
  comma-separated list in `PACTO_DASHBOARD_REPO`.
- Operator dashboard: the `dashboard.repositories` chart value, a list of
  `oci://` references.

Developers then list the paths with `pacto fleet entities --template`,
`pacto_fleet_entities` with `template: only` or the dashboard's Golden paths
tab. Search results mark each path; `pacto fleet search --template` keeps only
paths and `--template=exclude` leaves them out. Each path shows its summary,
its requirements, its plugins, its signature and a command to copy, pinned to
the path's digest when a registry served it.

A golden path never counts as a running service or as a consumer: Overview,
owner and domain counts, attention, impact and offering consumers leave it out.
A viewer restricted to some namespaces still sees a path that runs nowhere.
Services created from it that the viewer may not read appear only as a count.

A service is a golden path only while it runs nowhere. A deployed service that
declares `service.template` stays a service, keeps its dependents and raises
`GOLDEN_PATH_DEPLOYED` until someone removes the field or stops running it.

## Create a service

```bash
pacto init gift-cards --from oci://ghcr.io/acme/paths/http-service:1.0.0 \
  --owner checkout --domain commerce --registry ghcr.io/acme --plan
```

`--from` takes an `oci://` reference or a directory, and `--owner` is required.
`--plan` shows the contract, the files and the step list and changes nothing;
run the command again without it to write them. A tag is resolved to a digest
first, so the bytes checked are the bytes copied.

The new contract keeps the path's interfaces, dependencies, configurations,
policies, workload, state and capabilities. Pacto resets the identity:

- `service.name` is the folder name or `--name`, and the version is `0.1.0`;
- `service.owner` is `--owner` and `service.domain` is `--domain`;
- the path's offering, summary, lifecycle, readiness, `metadata.repo` and
  `metadata.summary` are dropped, each with an Unknown line that names it;
- `publish.bundle` and every artifact move under `--registry`, or under the
  `registry.example` placeholder;
- `publish.sign` names the new service's own key, `cosign.key`.

`--values` files, then `--set` entries, change any field before the identity is
set; a `null` removes a field. `service.origin` records the path's reference
and the content digest of its files; the
[reference](golden-paths-reference.md#declared-origins) says what a directory
records. A directory may hold only regular files. YAML comments are not kept,
and the path's `pacto.lock` is not copied: run `pacto lock`.

Pacto validates the result strictly, with every reference resolved, before it
writes anything. A configuration reference it cannot resolve fails with
`CONFIG_REF_UNRESOLVED`, and values the provider's schema rejects fail with
`CONFIG_VALUES_VALIDATION_FAILED`. It only adds files: a folder that already
holds a `pacto.yaml` is refused and any other file there is left as it is. A
path or plugin that gives a file under `.git` or a private key is refused.

An agent creates a service the same way with `pacto_create` and `from`,
`owner`, `domain`, `values`, `set`, `run_plugins`, `delivery` and `dry_run`.
`from` works only when `pacto mcp` runs over stdio, beside the agent and under
its own credentials.

## Plan its delivery

`--delivery` also plans the cluster pieces from the new contract, as
[`pacto init --delivery`](pactify.md#4-connect-the-service-to-your-cluster)
does, with `--env`, `--registry-secret` and `--insecure-registry`. An agent
passes `delivery` to `pacto_create`. It needs a path that publishes manifests
under `publish.deploy`. Without it, the delivery step says `unknown` and names
the missing field. The demo's `http-service` path publishes no manifests, so it
plans none. A Stage also needs the version that runs: in a new folder, with no
workload manifest, only `delivery/pacto.yaml` is planned and the step says why.
Files already there are kept as they are, and the path's own `delivery/` files
are left out.

## Trust

Pacto checks the signature against the template keys of a trust config, read
from `--trust` or `PACTO_DELIVERY_TRUST` (`pacto mcp --delivery-trust` for an
agent). A template key has `role: template` and lists in `streams` the
`path.Match` patterns of the repositories it may sign for, such as
`ghcr.io/acme/paths/*`; empty means every repository. It takes no `untilSeq`,
`untilRecord` or `providers`: to stop trusting a signer, remove its entry. A
bare PEM key given to `--trust` is a template key for every repository. A bare
PEM key in `PACTO_DELIVERY_TRUST` or `--delivery-trust` stays a record key and
never verifies a golden path, so an agent needs a trust config with a
`role: template` key. See
[Check who signed a golden path](verify-a-record.md#check-who-signed-a-golden-path)
for an example.

When the trust config names a template key, a path that no matching key signed
creates nothing. When it names none, or the path is a directory, the signature
step says `unknown`: Pacto did not check who signed the path. Unknown is
neither a pass nor a failure. To check the same signature without Pacto, run
`cosign verify --key platform.pub` on the path's `repository@sha256:...`.

The fleet shows a path's signature as Verified when the bundle verifies against
the signer its own contract declares, and anyone who can push declares their
own signer. So the checklist of a service created from the path keeps its
signature step `unknown`. Only the creation step, checked against your
template keys, can say `done`.

## Plugins

Each plugin the path names appears in the step list with the binary it
resolves to, or as not installed. Pacto never fetches a plugin. Only the ones
you name run:

```bash
pacto init gift-cards --from oci://ghcr.io/acme/paths/http-service:1.0.0 \
  --owner checkout --run-plugins dockerfile,github-actions
```

Every name must be one the path lists, and nothing runs under `--plan`. When
the trust config names a template key, plugins run only from a path a template
key verified. A plugin reads a temporary copy of the planned files, never your
folder, and the files it returns are added like the path's own. A plugin is how
a platform team adds a repository, a CI workflow or any other file it wants a
new service to start with.

## Follow the creation

`pacto init --from` ends with the step list: signature, contract, validation,
each plugin, delivery with `--delivery`, publishing, the fleet, deployment and
provisioning. A step is
`done` only when Pacto established it. Publishing, the fleet and deployment
stay `unknown` at creation, since no source has observed the new service yet.

Once a fleet source reads the service, `pacto fleet entity service gift-cards`
and the dashboard show its declared origin, the golden path revision whose
content digest matches and the same steps as a checklist derived from what the
fleet observes. `pacto fleet entities --created-from http-service` and
`pacto_fleet_entities` with `created_from` list every service created from a
path.

The [golden paths reference](golden-paths-reference.md) lists how each
checklist step is derived.
