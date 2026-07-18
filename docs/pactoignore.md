# Packaging ignore

The `.pactoignore` file lets you exclude files and directories from contract bundles when running `pacto pack` or `pacto push`. It uses gitignore-style syntax and is applied automatically when present in the contract directory.

---

## Syntax

`.pactoignore` follows the same pattern matching rules as `.gitignore`:

| Pattern | Behavior |
|---------|----------|
| `#` at start of line | Comment (ignored) |
| Blank lines | Ignored |
| `*` | Match any number of characters except `/` |
| `?` | Match a single character except `/` |
| `[abc]` | Character class — match `a`, `b` or `c` |
| `**` | Match zero or more directories |
| `/` at start | Anchor to the contract root |
| `/` at end | Match directories only |
| `!` at start | Negate the pattern (include matching files even if earlier patterns excluded them) |

Last-match-wins: if a file matches multiple patterns, the final pattern in the file determines whether it is ignored or included.

---

## Default ignores

These patterns are always applied, even when no `.pactoignore` file exists:

- `.git/`
- `.pactoignore`
- `.DS_Store`

**Note:** `pacto.lock` is **not** in the default ignore list. When a lockfile is present it ships inside the bundle by default (see [lockfile.md](lockfile.md)). To keep it out of a pushed bundle, add an explicit `pacto.lock` line to `.pactoignore`.

---

## Immutable files

`pacto.yaml` is **never ignorable**. Listing it in `.pactoignore` is a no-op — the contract file is always included in the bundle.

---

## Referenced file validation

Ignoring a file that is referenced by the contract makes `pacto validate`, `pacto pack` and `pacto push` fail. You cannot ship a bundle missing a file it declares.

For example, if your contract references `interfaces/openapi.yaml` and your `.pactoignore` contains:

```
interfaces/openapi.yaml
```

Then `pacto pack` and `pacto push` will exit with a validation error. To see the message naming the missing file, run `pacto validate` — `pack` and `push` report only `contract validation failed with N error(s)`.

The same rule applies to `configurations[].schema`, `policies[].schema` and any other file path declared in `pacto.yaml`.

---

## Example `.pactoignore`

```
# Build artifacts
dist/
build/
*.log

# Editor files
.vscode/
.idea/
*.swp

# Temporary files
tmp/
*.tmp

# Large binaries not needed in the bundle
data/*.bin

# Include specific exception
!data/config.bin
```

The final `!data/config.bin` re-includes one file that an earlier pattern excluded (negation).

---

## Commands affected

`.pactoignore` is applied by:

- **`pacto pack`** — files matching ignore patterns are excluded from the tar.gz archive
- **`pacto push`** — files matching ignore patterns are excluded from the OCI artifact

Every command that loads a local bundle reads through the ignore filter, so ignored files are invisible to all of them. The commands that validate the bundle (`validate`, `pack`, `push`) hard-fail on a missing referenced file, while `graph`, `diff`, `doc` and `explain` simply do not see the file and produce incomplete output. Only `pacto pack` and `pacto push` additionally package the filtered file set into the shipped artifact.

---

## Pattern examples

- `/docs/` — anchored: matches `docs/` at the contract root, not `subdir/docs/`
- `*.log` — unanchored: matches `build.log` and `output/build.log`
- `tmp/` — trailing slash matches the directory `tmp/`, not a file named `tmp`
- `!README.md` after `*.md` — re-includes `README.md`; place negations after the broader exclude

---

## Validation and troubleshooting

`pacto pack -v` enables debug logging of the high-level pack steps (load/validate, archive, write); there is no per-file include/exclude log. If a referenced file is excluded, `pacto pack` and `pacto push` fail with `contract validation failed with N error(s)` — the specific `FILE_NOT_FOUND` message is not surfaced, even with `-v`. To see the error naming the missing file, run `pacto validate`, which prints `FILE_NOT_FOUND` (e.g. `interface contract file "interfaces/openapi.yaml" not found in bundle`). Neither reports which ignore pattern excluded it.
