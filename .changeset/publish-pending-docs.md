---
"@pacto/core": patch
---

Publish four documentation fixes that have been correct on `main` but absent from
the site.

`docs/examples/dashboard-demo.md` named `v1.2.0 → v2.0.0` as the breaking step of
the demo fleet. OCI tags are immutable, so republishing that fixture under changed
bytes renumbered it to `v1.2.1 → v2.0.1` and left the page pointing at two tags
that no longer exist. The same stale literals were what broke the WASM demo smoke
test and turned the docs pipeline red; that test now finds the breaking step by
behaviour rather than by version, and this ships the half a reader can see.

The rest of the batch rides along because `release.yml`'s `docs` job is the only
publisher and it builds the whole site: the mkdocs-material 9.6.21 → 9.7.7 bump,
the two `aria-labels.js` fixes that keep it accessible and a testing note. 9.7
wraps every code block's buttons in an unnamed `<nav>` and labels the breadcrumb
with the same string the primary navigation uses, so every page carrying two code
blocks fails landmark-unique. The theme bump has never been published without its
fixes, and shipping them in one transaction keeps it that way.
