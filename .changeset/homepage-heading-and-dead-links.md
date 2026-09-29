---
"@pacto/core": patch
---

Retitle the homepage section that compared Pacto to a service catalog, and fix four dead links.

"What a service catalog does not do" headed a section on both the README and
the homepage without anything setting the comparison up — neither page mentions
a catalog before it — and the section then did not describe catalogs. What
follows it is a worked `pacto impact` run answering which consumers a major
version bump breaks, so the heading now says that. The comparison keeps the two
places that have the context for it: the "What Pacto is not" list in the model,
and the comparison table in the README.

Four links were dead. The operator chart's README pointed at an
`api-reference.md` that exists nowhere in the repo, which mattered because that
README is the package description on Artifact Hub; it now points at the CRD
reference page. Two links named a `pacto-dashboard` repository that does not
exist. The Cosign overview and the Kubernetes resource-quantity pages had both
moved. Nothing checked any of them: `docs_lint.check_links` walks root-level
Markdown only, and mkdocs validates only what it builds.
