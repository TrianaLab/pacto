# Lockfile

`pacto.lock` is to a contract bundle what `go.sum` is to a Go module:

| Tool | Lockfile | What it pins |
|------|----------|--------------|
| Go | `go.sum` | Module hashes |
| Helm | `Chart.lock` | Chart versions and repository URLs |
| npm | `package-lock.json` | Exact package versions and integrity hashes |
| Pacto | `pacto.lock` | OCI digests for dependencies and references |

It captures the exact resolved state of your contract's dependency closure and reference closure, committed to git and shipped inside the pushed bundle. When the lock is present, Pacto verifies that every resolved dependency and every config/policy reference matches its pinned digest. Any mismatch is a hard error: you cannot validate, graph, diff or push a bundle whose lock has drifted.

The lockfile **ships inside any bundle produced from a directory that contains `pacto.lock`**, because both `pacto pack` and `pacto push` archive it. The dashboard can therefore surface pinned digests and drift for services sourced from OCI registries or Kubernetes clusters, not just local directories. Shipping the lock remains opt-in. A contract without a `pacto.lock` next to `pacto.yaml` behaves as before: dependencies and references resolve live and nothing extra is included in the bundle. (To keep an existing lock out of a pushed bundle, see [`.pactoignore`](pactoignore.md).)

---

## What the lockfile pins

A `pacto.lock` file records:

- **Full transitive dependency closure** — every dependency declared in `dependencies[]`, recursively resolved through the dependency tree, pinned by OCI digest.
- **Full transitive reference closure** — every config/policy reference declared in `configurations[].ref` and `policies[].ref`, recursively resolved (reference jumps), pinned by OCI digest. One entry per declared reference *occurrence*, tagged with the contract that declared it, so a root contract's `settings` reference and a referenced bundle's own `settings` reference stay distinct pins.
- **Local dependencies and references** — file-based deps and refs pinned by their content hash (`sha256:...`) so local changes invalidate the lock.

The lock does not capture configuration values (`configurations[].values`), runtime metadata or scaling parameters. It only pins the structural dependencies and references that form the contract's external boundary.

---

## Commands

See the [CLI reference](cli-reference.md#pacto-lock) for the full flag list; this section explains what each mode does.

### `pacto lock`

Creates or refreshes `pacto.lock` in the current directory. Resolves the full dependency and reference closures and writes the result to disk. Existing pins are preserved on rewrite when the dependency's constraint is unchanged; a consistent lock is left untouched.

```bash
# Create or refresh the lockfile
pacto lock

# Operate on a specific directory
pacto lock ./my-service
```

The lock command respects `--values` and `--set` overrides just like `validate` and `graph` — the lock you generate matches the contract as it would be evaluated at command time.

### `pacto lock --update`

Re-resolves the entire closure from scratch, ignoring existing pins. Use this when you want to bump all dependencies and references to their latest compatible versions.

```bash
# Re-resolve everything
pacto lock --update
```

The `--update-name` flag re-resolves only the named dependency (repeatable) to the newest version its constraint allows, leaving every other dependency pin untouched.

### `pacto lock --check`

Verifies that the lockfile is up-to-date without modifying it. Exits non-zero if the lock is stale or has conflicts. Useful as a CI gate: the step fails unless contributors have run `pacto lock` after editing dependencies or references. Re-lock whenever a floating (unpinned) upstream ref is republished — it changes the resolved digest, so `lock --check` fails until you re-pin and commit. Use `pacto lock --update` (or `--update-name <dep>`): plain `pacto lock` keeps a dependency pin whose constraint is unchanged, so it rewrites the file without clearing the drift.

```bash
pacto lock --check
```

---

## Verification

When a `pacto.lock` file exists in a contract directory, the following commands verify the lock before proceeding:

- `pacto validate`
- `pacto graph`
- `pacto diff`
- `pacto push`

If any resolved dependency or reference produces a digest that does not match the pin in the lock, the command fails with one of these error codes:

| Code | Description |
|------|-------------|
| `LOCK_DIGEST_MISMATCH` | A resolved OCI ref's digest does not match the lockfile |
| `LOCK_LOCAL_DRIFT` | A local file's content hash changed since the lock was written |
| `LOCK_STALE` | The lockfile is missing a newly added dependency or reference |
| `LOCK_CONFLICT` | The dependency closure requires the same service at two incompatible versions |
| `LOCK_UNRESOLVED` | A ref in the lockfile could not be resolved (network error, registry unreachable, bundle deleted) |
| `LOCK_MISSING` | `pacto lock --check` was run but no `pacto.lock` file exists. It is the only command that treats a missing lock as an error; the commands listed above skip verification entirely when there is no lock to verify |
| `LOCK_AMBIGUOUS_REFERENCE` | One declared reference would have to be pinned to two different destinations, so the lock cannot record it |
| `LOCK_DUPLICATE_DECLARATION` | A contract in the closure declares the same configuration or policy name twice, so it holds two declarations the lock cannot tell apart |
| `LOCK_ERROR` | Fallback for a lock failure that carries no more specific `LOCK_*` code — for example a `lockVersion` outside the supported range |

A lock below `lockVersion` 3 is reported as `LOCK_STALE`: re-run `pacto lock` to
rewrite it. Rewriting is the whole migration — the closure is re-resolved from the
contract, so nothing in an old lock has to be translated.

---

## Example `pacto.lock`

```yaml
lockVersion: 3
pacto:
  version: v3.2.1
root:
  name: payments-api
  version: 2.1.0
dependencies:
  - name: auth-service
    source: oci
    ref: oci://ghcr.io/acme/auth-service-pacto
    constraint: ^2.0.0
    version: 2.5.1
    digest: sha256:abc123...
    dependsOn:
      - crypto-lib
  - name: crypto-lib
    source: oci
    ref: oci://ghcr.io/acme/crypto-pacto
    constraint: ~1.2.0
    version: 1.2.0
    digest: sha256:def456...
  - name: shared-lib
    source: local
    path: ../shared-lib
    constraint: ^1.0.0
    version: 1.5.0
    contentHash: sha256:789abc...
references:
  - kind: policy
    name: org-security
    source: oci
    ref: oci://ghcr.io/acme/org-security-policy
    version: 1.3.0
    digest: sha256:fff111...
  - kind: config
    name: shared-config
    source: oci
    ref: oci://ghcr.io/acme/shared-config
    version: 2.0.0
    digest: sha256:222eee...
  - from: oci:sha256:222eee...
    kind: config
    name: limits
    source: oci
    ref: oci://ghcr.io/acme/platform-limits
    version: 4.0.0
    digest: sha256:333ddd...
```

The lockfile starts with `lockVersion` (schema version), `pacto.version` (the CLI version that wrote the lock) and `root` (the contract's name and version).

Each `dependencies[]` entry records the `source` (oci or local), the full ref or path as written in the contract, the constraint and resolved version and the digest (for OCI) or contentHash (for local). The lockfile's `constraint` is the dependency's `compatibility` range copied from `pacto.yaml`. The `dependsOn` field captures the dependency chain so Pacto can rebuild the full graph structure from the lock without re-resolving upstream refs.

The `references[]` section records config and policy refs with their `kind`, `source` and digest — references carry no `constraint` (only dependencies do). Local file-based refs appear here with a contentHash instead of an OCI digest. A contract that declares no config or policy refs produces a lock with no `references` key at all, rather than an empty list.

---

## Non-goals

- **The operator does not gate on the lock.** The lockfile is a CLI-side reproducibility guarantee. The Kubernetes operator resolves contracts independently from CRD specs and does not consult the lockfile.
