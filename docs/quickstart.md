---
# This is the getting-started page; it should win that query against any
# section that merely contains the words.
search:
  boost: 2
---

# Quickstart

From nothing to a checked contract that catches breaking changes, in about
five minutes. A contract is one file, `pacto.yaml`, that says who owns a
service, what it offers and which other services it calls. Everything here
runs on your machine, with no account and no registry.

---

## 1. Install Pacto

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

Or via Go:

```bash
go install github.com/trianalab/pacto/v4/cmd/pacto@latest
```

See [Installation](installation.md) for other methods and for what to do if the script exits with `Failed to fetch latest version` — that is the anonymous GitHub API rate limit, not a broken release.

## 2. Create a contract

Run this from an empty directory and stay there through step 5; step 6 moves inside `my-service`.

```bash
pacto init my-service
```

This writes three files: `pacto.yaml` (the contract), `interfaces/openapi.yaml` (a placeholder API spec) and `configuration/schema.json` (a placeholder config schema). The argument is the folder to write into, and its last part becomes `service.name`, so `pacto init fleet/checkout` writes a contract for `checkout` in `fleet/checkout`. Run `pacto init` in a folder that already holds a service and it reads the Dockerfile and Kubernetes files instead: see [Pactify a service](pactify.md).

Edit `my-service/pacto.yaml` and set `service.version: 1.0.0`. Then replace `my-service/interfaces/openapi.yaml` with an API that has required response fields:

```yaml
openapi: "3.0.0"
info:
  title: my-service
  version: 1.0.0
paths:
  /orders/{id}:
    get:
      summary: Get an order
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, total]
                properties:
                  id:
                    type: string
                  total:
                    type: number
```

## 3. Validate

```console
$ pacto validate my-service
my-service is valid
```

Validation checks three things: the file's shape, that fields agree with each
other (a field that names an interface must name one the contract declares,
for example) and your policies, the rules your platform team may set for every
contract. See the [Contract Reference](contract-reference/validation.md#validation-layers) for the full rules.

## 4. Add consumers

Two services depend on `my-service`, each with a different compatibility range. Copy the folder twice and declare the dependencies:

```bash
cp -r my-service checkout
cp -r my-service reporting
```

In `checkout/pacto.yaml`, set `service.name: checkout` and replace the `dependencies:` block that `pacto init` wrote as comments with:

```yaml
dependencies:
  - name: my-service
    ref: ../my-service
    required: true
    compatibility: "^1.0.0"
```

In `reporting/pacto.yaml`, set `service.name: reporting` and use a wider range:

```yaml
dependencies:
  - name: my-service
    ref: ../my-service
    required: true
    compatibility: ">=1.0.0"
```

`checkout` accepts `^1.0.0` (1.x only), `reporting` accepts `>=1.0.0` (1.x and above). This difference is what makes the next step interesting.

## 5. Introduce a breaking change

Copy `my-service` to `v2` and delete the `total` field from the response schema:

```bash
cp -r my-service v2
```

In `v2/pacto.yaml`, set `service.version: 2.0.0`. In `v2/interfaces/openapi.yaml`, remove `total` from `required` and from `properties` so the schema becomes:

```yaml
schema:
  type: object
  required: [id]
  properties:
    id:
      type: string
```

## 6. Diff the two versions

From inside `my-service`, compare the original contract against `v2`:

```console
$ pacto diff . ../v2
Classification: BREAKING
Required bump: major
Changes (3):
  [NON_BREAKING] service.version (modified): service.version modified [1.0.0 -> 2.0.0]
  [BREAKING] openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.properties.total (removed): openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.properties.total removed [- map[type:number]]
  [BREAKING] openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.required[total] (removed): openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.required total removed [- total]
breaking changes detected
$ echo $?
1
```

The classification is `BREAKING`, so the next published version must be a new major version, and the exit code is 1. That is the CI gate: `pacto diff` exits non-zero when the change breaks consumers, so a branch-protection rule that requires passing checks blocks the merge.

## 7. Find the affected consumers

`pacto impact` takes that diff, finds every service that depends on `my-service` and tells you which ones break:

```console
$ pacto impact . ../v2 --local ..
Impact: my-service 1.0.0 -> 2.0.0
Classification: BREAKING
Breaking changes: 2
Potentially breaking changes: 0
Affected consumers (2):
  checkout                     direct     confidence=contractual  compat=incompatible owner=my-team
  reporting                    direct     confidence=contractual  compat=compatible   owner=my-team
```

`checkout` and `reporting` declare the *same* edge to the *same* service and get *opposite* verdicts. `checkout` pins `^1.0.0` (1.x only) and breaks. `reporting` accepts `>=1.0.0` (any major) and survives. A catalog that records only "`checkout` depends on `my-service`" cannot tell those two apart; Pacto can, because the contract carries the compatibility range.

`owner=my-team` is the placeholder team `pacto init` wrote. `confidence=contractual`
means the answer comes from the declared ranges alone: Pacto has not also seen
the call happen at runtime. `direct` means each consumer declares `my-service` itself rather than reaching it through another service.

---

## Optional: publish to a registry

The steps above need no registry. To see the full round trip, start a local registry and push the contract:

```bash
docker run -d --rm -p 127.0.0.1:5001:5000 --name pacto-registry registry:3
pacto push oci://localhost:5001/demo/my-service-pacto -p .
```

Port 5001 because macOS binds 5000 for AirPlay. `127.0.0.1:` because the registry accepts anonymous writes.

Read it back without pulling:

```bash
pacto explain oci://localhost:5001/demo/my-service-pacto:1.0.0
```

Or pull the bundle:

```bash
pacto pull oci://localhost:5001/demo/my-service-pacto:1.0.0 -o pulled
```

Clean up:

```bash
docker rm -f pacto-registry
```

For a real registry like GHCR, you need `pacto login` with a personal access token. See [Installation](installation.md) for the supply-chain guarantees and [Put it in your repository](ci.md) for what to commit.

---

## What to do next

| Goal | Guide |
|------|-------|
| Let Pacto check every release of a real service | [Pactify a service](pactify.md) |
| Put it in your repository | [Put it in your repository](ci.md) |
| Understand every contract field | [Contract Reference](contract-reference/index.md) |
| See contracts for real services | [Examples](examples/index.md) |
| Explore contracts visually | Run `pacto dashboard` or [`pacto tui`](fleet-tools.md#the-terminal-ui) |
| Check what a running service does on the network | [Runtime conformance](runtime-conformance.md) |
| Check a service in CI or on your laptop | [`pacto run`](runtime-run.md) |
| Check every service on a Docker host or server | [`pacto agent`](runtime-agent.md) |
| Runtime compliance in Kubernetes | [Kubernetes Operator](integrations/kubernetes/overview.md) |
