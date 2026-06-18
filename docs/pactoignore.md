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
| `[abc]` | Character class — match `a`, `b`, or `c` |
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
- `pacto.lock`
- `.DS_Store`

You do not need to list these in your `.pactoignore` — they are excluded automatically.

---

## Immutable files

`pacto.yaml` is **never ignorable**. Listing it in `.pactoignore` is a no-op — the contract file is always included in the bundle.

---

## Referenced file validation

Ignoring a file that is referenced by the contract makes `pacto validate`, `pacto pack`, and `pacto push` fail. You cannot ship a bundle missing a file it declares.

For example, if your contract references `interfaces/openapi.yaml` and your `.pactoignore` contains:

```
interfaces/openapi.yaml
```

Then `pacto pack` and `pacto push` will exit with an error stating that the referenced file is missing.

The same rule applies to `configurations[].schema`, `policies[].schema`, and any other file path declared in `pacto.yaml`.

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

This pattern:

1. Excludes all `dist/`, `build/`, and `.log` files
2. Excludes editor directories and swap files
3. Excludes `tmp/` and `.tmp` files
4. Excludes `.bin` files in the `data/` directory, except `data/config.bin` (negation)

---

## Commands affected

`.pactoignore` is applied by:

- **`pacto pack`** — files matching ignore patterns are excluded from the tar.gz archive
- **`pacto push`** — files matching ignore patterns are excluded from the OCI artifact

Other commands (`validate`, `graph`, `diff`, `doc`, `explain`) do not apply ignore patterns — they operate on the local directory as-is.

---

## Typical use cases

- Exclude build artifacts and logs from the bundle
- Exclude large generated files that are not part of the contract
- Exclude editor-specific directories (`.vscode/`, `.idea/`)
- Exclude temporary or cache directories
- Exclude local development tooling files that should not be distributed

---

## Anchoring

A pattern like `/docs/` matches the `docs/` directory at the contract root but not nested directories like `subdir/docs/`.

A pattern without a leading `/` matches anywhere in the tree: `*.log` matches `build.log` and also `output/build.log`.

---

## Trailing slash

A trailing `/` means "directories only": `tmp/` matches the directory `tmp/` but not a file named `tmp`.

Without the trailing slash, both the directory and a file of the same name would match.

---

## Negation

Use `!` at the start of a pattern to re-include a file that was previously excluded. Last-match-wins, so place negations after the broader exclusion patterns.

Example:

```
# Exclude all markdown files
*.md

# Except README.md
!README.md
```

This excludes all `.md` files but re-includes `README.md`.

---

## Validation and troubleshooting

If you are unsure whether a file will be included in the bundle, run `pacto pack` in verbose mode:

```bash
pacto pack -v
```

Pacto logs each file it processes and whether it was included or excluded.

If a referenced file is missing due to an ignore pattern, the error message will identify the exact file and the pattern that excluded it.
