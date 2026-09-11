---
"@pacto/core": minor
---

Add `--root` to `pacto fleet`, `pacto tui` and `pacto impact`: a contract root
whose whole dependency **closure** joins the snapshot.

Every other definition source stops at what someone remembered to list. `--local`
scans a directory and finds the bundles that happen to be in it; `--oci` pulls
exactly the references you typed. So a bundle declaring a dependency on
`oci://ghcr.io/acme/payments:2.1.0` left a dangling edge unless you also passed
that reference yourself — and the graph reported a service with no dependents
when the truth was that nobody had looked.

`--root` resolves the root you name and then follows its declarations,
transitively, the way `pacto mcp --root` already did. It is the same discovery
through the same resolver, so a catalog session and a fleet snapshot cannot
disagree about what a reference means. Roots and dependencies that fail to
resolve stay visible as limitations rather than vanishing, so a short closure is
never served as a whole one.

```bash
pacto fleet graph payments --root ./orders          # follows orders' declarations
pacto tui --root oci://ghcr.io/acme/platform:1.4.0  # the whole platform closure
```

On `pacto mcp`, `--root` keeps its existing meaning — it selects the read-only
catalog server, and stays mutually exclusive with `--fleet`.

Fixes a related identity bug this exposed: `--local` hashed the raw directory
while the lockfile, the catalog and a pushed artifact all hash the *packaged*
file set. One developer's stray `.DS_Store` was therefore enough to make the same
bundle look like two different revisions of one service at one version — a
content conflict reported against a fleet where nothing had changed. Local
revisions now hash the `.pactoignore`-filtered file set, like everywhere else.
