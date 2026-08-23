---
# This is the getting-started page; it should win that query against any
# section that merely contains the words.
search:
  boost: 2
---

# Quickstart

Getting started with Pacto: from zero to a published contract in about five
minutes. Everything here runs on your machine — no account, no registry of your
own, nothing to sign up for.

You need the Pacto CLI (step 1) and, from step 6 onwards,
[Docker](https://docs.docker.com/get-started/get-docker/) to run a throwaway
registry. Step 9 deletes everything this page creates.

---
## 1. Install Pacto

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

Or via Go:

```bash
go install github.com/trianalab/pacto/v3/cmd/pacto@latest
```

See [Installation](installation.md) for the other methods, for
[installing without `sudo`](installation.md#installing-without-sudo), and for
what to do if the script exits with `Failed to fetch latest version` — that is
the anonymous GitHub API rate limit, not a broken release.

## 2. Scaffold a contract

```bash
$ pacto init my-service
Created my-service/
  my-service/pacto.yaml
  my-service/interfaces/
  my-service/configuration/
```

Three files, all valid as they stand:

| File | What it is |
|---|---|
| `pacto.yaml` | the contract — the only required file |
| `interfaces/openapi.yaml` | a placeholder OpenAPI spec with one path, `/health` |
| `configuration/schema.json` | a placeholder JSON Schema for the service's configuration |

`pacto.yaml` also ships a `readiness:` block (step 5) and a `metadata:` block,
both filled with placeholders.

If your service has no interfaces or no configuration, remove the directory
**and** the matching `interfaces:` / `configurations:` section in `pacto.yaml` —
deleting the directory alone breaks validation. The scaffold's `health` and
`metrics` capabilities are declared without an interface binding, so they need
no changes.

These are standard formats — OpenAPI for interfaces, JSON Schema for
configuration — so you can drop in files you already own (for example your Helm
chart's `values.schema.json`) instead of authoring new ones. Pacto composes the
interfaces you already have rather than inventing a config language.

## 3. Validate

```bash
$ pacto validate my-service
my-service is valid
```

Validation runs three layers — structural, cross-field and policy enforcement.
See the [Contract Reference](contract-reference/validation.md#validation-layers)
for the full rules.

## 4. Customize your contract

Edit `my-service/pacto.yaml` to match your service. A minimal contract only
requires `pactoVersion` and `service`:

```yaml
pactoVersion: "2.0"

service:
  name: my-service
  version: 0.1.0
  owner:
    team: backend
```

Add sections as needed — interfaces, workload, state, capabilities,
dependencies, configuration, policy, readiness. See the
[Contract Reference](contract-reference/index.md) for every available field.

`service.version` is the contract's version, and it becomes the registry tag in
step 6. The scaffold starts at `0.1.0`; the rest of this page uses that value.

## 5. Set the readiness claims

`pacto init` scaffolds a `readiness:` block with `minScore: 80` and two claims
that start at `not-done`: `runbook` (weight 40) and `security-review`
(weight 60). Readiness is scored separately from validity, so plain
`pacto validate` ignores it. Ask for it explicitly:

```bash
$ pacto validate my-service --readiness
my-service is invalid
  ERROR [READINESS_GATE_UNMET] readiness: score below gate: score 0, minScore 80 (0 done, 0 partial, 2 not-done, 0 deferred)
validation failed with 1 error(s)
```

That is the gate working, not a broken scaffold: nothing is done yet, so the
score is 0. Replace the placeholder claims with your service's real ones, then
set each `status:` to `done`, `partial`, `not-done` or `deferred` as you go. With
both scaffolded claims at `done`:

```bash
$ pacto validate my-service --readiness
my-service is valid
```

The gate is opt-in because the result is time-dependent — an assessment past its
`expires:` date scores 0. See
[Contract Reference](contract-reference/sections.md#readiness) for the scoring
rules, including how `partial` claims earn part of their weight.

## 6. Publish to a registry

A contract is only useful once other people can resolve it. Start a throwaway
registry so you can do the whole round trip with no account:

Docker has to be **running**, not just installed: if the daemon (or Docker
Desktop) is down, `docker run` fails with a connection error naming the Docker
socket rather than anything about Pacto.

```bash
$ docker run -d --rm -p 5001:5000 --name pacto-registry registry:3
6202c6df98fc5f50cf3b3375ea112d0928b274cb3bd6aa7b7d25d1e8e6b56aa2
```

`registry:3` is the official `registry` image — CNCF Distribution
(`github.com/distribution/distribution/v3`), the reference OCI registry. Docker
pulls it the first time and prints download progress, then prints the container
ID as above; yours will differ. It listens on `localhost:5001`, holds nothing on
disk and disappears in step 9. Port 5001 rather than 5000 because macOS binds
5000 for AirPlay.

```bash
# Auto-tags with service.version; skips if that tag already exists (--force overwrites)
$ pacto push oci://localhost:5001/demo/my-service-pacto -p my-service
Pushed my-service@0.1.0 -> localhost:5001/demo/my-service-pacto:0.1.0
Digest: sha256:<64 hex characters>
```

The digest is the full 64-character hash, and it is content-addressed: yours
will differ from anyone else's the moment you edit the contract.

No `pacto login` here — a local registry needs no credentials. A real registry
needs two things you have to arrange yourself: an account you can publish to
(`your-org` must be a GitHub user or organisation you own) and, for GHCR, a
personal access token carrying the `write:packages` scope. With both in hand:

```bash
$ pacto login ghcr.io -u your-username
Password:
Login succeeded for ghcr.io
$ pacto push oci://ghcr.io/your-org/my-service-pacto -p my-service
```

Paste the token at the `Password:` prompt; it is not echoed. `login` stores the
credentials in `~/.config/pacto/config.json` without contacting the registry, so
`Login succeeded` only means they were saved — a wrong token or a missing scope
surfaces on the `push`.

!!! note "`pacto pack` is not a step on this path"
    `pacto pack my-service` writes `my-service-0.1.0.tar.gz`, a bundle you can
    hand to someone with no registry access. `pacto push` reads the directory
    directly and rejects a tarball (`my-service-0.1.0.tar.gz is not a
    directory`), so packing before pushing does nothing. No other Pacto command
    reads the archive either — whoever receives it extracts it first.

## 7. Read it back

`explain` takes a registry reference, so you can read what you published
without downloading it first:

```bash
$ pacto explain oci://localhost:5001/demo/my-service-pacto:0.1.0
Service: my-service@0.1.0
Owner: my-team
Pacto Version: 2.0

Workload: service

State:
  Type: stateless
  Persistence: local/ephemeral
  Data Criticality: low

Capabilities (2):
  - health
  - metrics

Interfaces (1):
  - api (openapi: interfaces/openapi.yaml, internal)

Readiness:
  ...
```

The `Readiness:` block is abridged above; the real output continues with the
score, the gate result, a per-claim table and the revision history.

To get the files themselves, pull the bundle. `pull` writes to a directory named
after the service unless you say otherwise — pass `-o` so it does not overwrite
the copy you are editing:

```bash
$ pacto pull oci://localhost:5001/demo/my-service-pacto:0.1.0 -o pulled
Pulled my-service@0.1.0 -> pulled/
```

You can also browse a contract instead of reading it:

```bash
$ pacto doc my-service --serve
Serving documentation at http://127.0.0.1:8484
Press Ctrl+C to stop
```

## 8. Detect a breaking change

Remove the `/health` path from `my-service/interfaces/openapi.yaml` so the file
ends with an empty `paths:` map:

```yaml
openapi: "3.0.0"
info:
  title: my-service
  version: 0.1.0
paths: {}
```

Then diff your working copy against the version you published:

```bash
$ pacto diff oci://localhost:5001/demo/my-service-pacto:0.1.0 my-service
Classification: BREAKING
Changes (1):
  [BREAKING] openapi.paths[/health] (removed): API path /health removed [- /health]
breaking changes detected
```

The exit code is 1 when the classification is `BREAKING` — that is the CI gate.

Both sides of a diff can be local, so this check needs no registry at all:
`pacto diff ./v1 ./v2` compares two directories and prints the same
classification. That is the form to reach for when comparing a release branch
against `main` in CI.

Removing an API path is one rule out of the full table:
[Change classification](contract-reference/diff.md) lists every field `pacto diff`
compares and the verdict it reaches for each. See
[Detecting breaking changes](developers.md#detecting-breaking-changes) and the
[GitHub Actions](github-actions.md) integration for wiring it into a pipeline.

## 9. Clean up

```bash
$ docker rm -f pacto-registry
$ rm -rf my-service pulled my-service-0.1.0.tar.gz
```

Nothing published to `localhost:5001` outlives the container. Two things do
survive, both outside this directory:

- resolved bundles cached under `~/.cache/pacto/oci/`, safe to delete;
- credentials, if you ran `pacto login` against a real registry —
  `pacto logout ghcr.io` removes them from `~/.config/pacto/config.json`.

---

## What to do next

| Goal | Guide |
|------|-------|
| Understand every contract field | [Contract Reference](contract-reference/index.md) |
| Write and maintain contracts | [For Developers](developers.md) |
| Consume contracts for deployment | [For Platform Engineers](platform-engineers.md) |
| See contracts for real services | [Examples](examples/index.md) (PostgreSQL, Redis, RabbitMQ, NGINX, gRPC and more) |
| Integrate with CI/CD | [GitHub Actions](github-actions.md) |
| Explore contracts visually | Run `pacto dashboard` to launch the web UI with dependency graph |
| Runtime compliance in Kubernetes | [Kubernetes Operator](integrations/kubernetes/overview.md) |
| Build a generation plugin | [Plugin Development](plugins.md) |
