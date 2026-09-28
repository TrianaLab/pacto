---
# This is the getting-started page; it should win that query against any
# section that merely contains the words.
search:
  boost: 2
---

# Quickstart

From zero to a validated contract that detects breaking changes: about five minutes. Everything runs locally with no account, no registry, nothing to sign up for.

---

## 1. Install Pacto

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

Or via Go:

```bash
go install github.com/trianalab/pacto/v3/cmd/pacto@latest
```

See [Installation](installation.md) for other methods and for what to do if the script exits with `Failed to fetch latest version` — that is the anonymous GitHub API rate limit, not a broken release.

## 2. Create a contract

Run this from an empty directory and stay there through step 5; step 6 moves inside `my-service`.

```bash
pacto init my-service
```

This writes three files: `pacto.yaml` (the contract), `interfaces/openapi.yaml` (a placeholder API spec) and `configuration/schema.json` (a placeholder config schema). The argument is the service name, not a path. It becomes the directory and `service.name`, so `pacto init fleet/checkout` creates a bundle that fails validation.

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

Validation runs three layers: structural, cross-field and policy checks. See the [Contract Reference](contract-reference/validation.md#validation-layers) for the full rules.

## 4. Add consumers

Two services depend on `my-service`, each with a different compatibility range. Copy the bundle twice and declare the dependencies:

```bash
cp -r my-service checkout
cp -r my-service reporting
```

In `checkout/pacto.yaml`, set `service.name: checkout` and replace the commented-out `dependencies:` block with:

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
Changes (3):
  [NON_BREAKING] service.version (modified): service.version modified [1.0.0 -> 2.0.0]
  [BREAKING] openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.properties.total (removed): openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.properties.total removed [- map[type:number]]
  [BREAKING] openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.required[total] (removed): openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.required total removed [- total]
breaking changes detected
$ echo $?
1
```

The classification is `BREAKING` and the exit code is 1. That is the CI gate: `pacto diff` exits non-zero when the change breaks consumers, so a branch-protection rule that requires passing checks blocks the merge.

## 7. Find the affected consumers

`pacto impact` projects the diff onto the dependency graph and reports which consumers are incompatible:

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

`confidence=contractual` means the dependency is declared with a usable range and nothing observed it at runtime. `direct` means each consumer declares `my-service` itself rather than reaching it through another service.

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

For a real registry like GHCR, you need `pacto login` with a personal access token. See [Installation](installation.md) for the supply-chain guarantees and [CI integration](ci.md) for what to commit to your repository.

---

## What to do next

| Goal | Guide |
|------|-------|
| Put it in your repository | [CI integration](ci.md) |
| Understand every contract field | [Contract Reference](contract-reference/index.md) |
| See contracts for real services | [Examples](examples/index.md) |
| Explore contracts visually | Run `pacto dashboard` or [`pacto tui`](fleet-tools.md#the-terminal-ui) |
| Runtime compliance in Kubernetes | [Kubernetes Operator](integrations/kubernetes/overview.md) |
