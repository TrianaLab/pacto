# Lockfile

Pacto lockfiles enable reproducible dependency resolution and supply-chain pinning for contract bundles. Like `go.sum` and `Chart.lock`, `pacto.lock` captures the exact resolved state of your contract's dependency closure and reference closure, committed to git and enforced by the CLI.

When a `pacto.lock` file is present, Pacto verifies that every resolved dependency and every config/policy reference matches the digest pinned in the lock. Any mismatch is a hard error — you cannot validate, graph, diff, or push a bundle whose lock has drifted.

---

## What the lockfile pins

A `pacto.lock` file records:

- **Full transitive dependency closure** — every dependency declared in `dependencies[]`, recursively resolved through the dependency tree, pinned by OCI digest.
- **Full transitive reference closure** — every config/policy reference declared in `configurations[].ref` and `policies[].ref`, recursively resolved (reference jumps), pinned by OCI digest.
- **Local dependencies and references** — file-based deps and refs pinned by their content hash (`sha256:...`) so local changes invalidate the lock.

The lock does not capture configuration values (`configurations[].values`), runtime metadata, or scaling parameters. It only pins the structural dependencies and references that form the contract's external boundary.

---

## Opt-in behavior

Lockfiles are **opt-in**. A contract without a `pacto.lock` behaves as before — dependencies and references are resolved live on every command. The lock only activates when present.

---

## Commands

### `pacto lock`

Creates or refreshes `pacto.lock` in the current directory. Resolves the full dependency and reference closures and writes the result to disk. Existing pins are preserved when possible (same ref, still reachable).

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

# Bump only a specific dependency or reference
pacto lock --update --update-name auth-service
```

The `--update-name` flag limits the re-resolution to the named dependency or reference. All other pins are preserved.

### `pacto lock --check`

Verifies that the lockfile is up-to-date without modifying it. Exits non-zero if the lock is stale or has conflicts. Useful as a CI gate to enforce that contributors have run `pacto lock` after editing dependencies or references.

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
| `LOCK_CONFLICT` | The lockfile contains a pin for a ref that no longer appears in the contract |
| `LOCK_UNRESOLVED` | A ref in the lockfile could not be resolved (network error, registry unreachable, bundle deleted) |
| `LOCK_MISSING` | A dependency or reference has no corresponding lock entry |

Because `pacto push` enforces the lock, **you cannot publish a bundle whose lock is stale**. This makes the lock an automatic supply-chain gate — if upstream dependencies have shifted or local files have changed without a `pacto lock --update`, the push is rejected.

---

## Example `pacto.lock`

```yaml
dependencies:
  - name: auth-service
    ref: oci://ghcr.io/acme/auth-pacto:2.3.0
    resolvedDigest: sha256:abc123def456...
    dependsOn: []

  - name: user-store
    ref: oci://ghcr.io/acme/user-store-pacto:1.0.0
    resolvedDigest: sha256:789abc012def...
    dependsOn:
      - auth-service

  - name: notifications
    ref: oci://ghcr.io/acme/notifications-pacto:1.5.2
    resolvedDigest: sha256:deadbeef1234...
    dependsOn: []

references:
  - name: platform-config
    ref: oci://ghcr.io/acme/platform-config-pacto:1.0.0
    resolvedDigest: sha256:feedface5678...

  - name: platform-policy
    ref: oci://ghcr.io/acme/platform-policy-pacto:1.0.0
    resolvedDigest: sha256:cafebabe9abc...
```

Each `dependencies[]` entry records the full ref (tag or digest) as written in the contract plus the resolved digest. The `dependsOn` field captures the dependency chain so Pacto can rebuild the full graph structure from the lock without re-resolving upstream refs.

The `references[]` section records config and policy refs with their resolved digests. Local file-based refs appear here with a `sha256:` content hash instead of an OCI digest.

---

## Non-goals

- **The operator does not gate on the lock.** The lockfile is a CLI-side reproducibility guarantee. The Kubernetes operator resolves contracts independently from CRD specs and does not consult the lockfile.
- **The lock is not embedded in the pushed bundle.** When you `pacto push`, the lock stays in your git repo. It is not packaged into the OCI artifact. This keeps bundles clean of development-time artifacts.

---

## Analogy

If you're familiar with other package managers:

| Tool | Lockfile | What it pins |
|------|----------|--------------|
| Go | `go.sum` | Module hashes |
| Helm | `Chart.lock` | Chart versions and repository URLs |
| npm | `package-lock.json` | Exact package versions and integrity hashes |
| Pacto | `pacto.lock` | OCI digests for dependencies and references |

Like these tools, Pacto's lockfile ensures that the same contract always resolves to the same closure, regardless of when or where you run the command.
