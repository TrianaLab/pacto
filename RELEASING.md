# Releasing

The operator depends on the [Pacto engine](https://github.com/TrianaLab/pacto)
module `github.com/trianalab/pacto/v2`. Releasing the operator is therefore a
**cross-repo, engine-first** procedure: the engine version the operator imports
must be published *before* the operator is released.

## Why the order matters

`go.mod` pins the engine with a `require` and, on the development branch, a
`replace` that points at a sibling checkout:

```
require github.com/trianalab/pacto/v2 vX.Y.Z
replace github.com/trianalab/pacto/v2 => ../pacto
```

The `replace` makes the build use the local `../pacto` working copy instead of
the published module. It exists purely for co-development (engine and operator
changing together) and is toggled by `make pacto-local` / `make pacto-remote`.

The operator imports engine packages such as `pkg/evidence` and `pkg/finding`.
These packages may be newer than the currently pinned engine tag — in that case
the operator builds **only** through the `replace` sibling. A clean clone (or CI
without the sibling) cannot resolve them, so dropping the `replace` while the
`require` still points at a tag that lacks those packages is a build blocker.

Verify what a candidate engine tag actually contains before pinning it:

```bash
git -C ../pacto ls-tree -r --name-only <engine-tag> -- pkg/evidence pkg/finding
```

Empty output means the tag does **not** contain those packages and is not a
valid pin for this operator.

## Release procedure

1. **Publish the engine first.** Tag and publish the engine (`TrianaLab/pacto`)
   at a version whose tree contains every package the operator imports —
   including `pkg/evidence` and `pkg/finding`. Confirm with the `ls-tree` check
   above. The concrete version is **TBD by the maintainer** (do not invent one).

2. **Bump the operator's `require`.** Update `go.mod` to that published engine
   version, then `go mod tidy`:

   ```bash
   go mod edit -require github.com/trianalab/pacto/v2@<engine-tag>
   go mod tidy
   ```

3. **Drop the dev-only `replace`.** The `replace => ../pacto` is a
   sibling-checkout convenience and MUST NOT ship in a release. Remove it (it is
   only valid on the integration branch, where the sibling is present):

   ```bash
   make pacto-remote   # go mod edit -dropreplace + go mod tidy
   ```

4. **Build against the published engine.** With the `replace` gone, confirm the
   module graph resolves from the registry alone:

   ```bash
   go build ./...
   make ci
   ```

5. **Tag and release the operator.** The auto-release workflow (see
   [CONTRIBUTING.md](CONTRIBUTING.md#releasing)) takes over from a merge to
   `main`.

## CI note

Operator CI must be able to resolve the engine dependency. Until the operator's
`require` points at a published engine tag that contains the imported packages
**and** the `replace` is dropped, CI needs the sibling engine checked out at
`../pacto` (or an equivalent `replace`). Once step 1 and step 3 are done, no
sibling is required.
