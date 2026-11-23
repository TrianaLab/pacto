---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

A Backstage catalog Pacto reads now also gives its software templates
(`scaffolder.backstage.io` Template entities) as inferred golden paths. They
show as muted rows on the dashboard's Golden paths page, under
`pacto fleet entities --template` and as `inferredTemplates` in
`pacto_fleet_entities` and `GET /api/fleet/entities` with `template: only`,
each with where and when it was read, an "Open in Backstage" link for a
configured catalog and the block's completeness. `--declaration inferred`
keeps only those. An inferred golden path is never signed, never counted and
never offered by `pacto init --from`, `pacto_create` or Create service, which
refuse a `template:` reference with the command that accepts it.

`pacto declare template:<namespace>/<name>` with `--backstage-url`,
`pacto_declare` with that `service` and the dashboard's Accept as golden path
button plan a contract-only golden path at `<registry>/<name>` that declares
`service.template`. Only a template from the configured catalog is accepted,
only into a repository with no version yet, and on a shared dashboard only by
a person who may `update pactos` in the installation namespace. New refusal
codes: `TemplateNotFound`, `TemplateCatalogUnset`,
`TemplateCatalogUnavailable`, `TemplateNameInvalid`, `TemplateNameTaken` and
`TemplateSourceUnverified`.

Detection now reports the Backstage row as `not detected`, partial, when no
Backstage runs in a watched namespace; the dashboard hides that row when
`dashboard.backstage.url` is set. `backstage.ReadCatalogPage` returns a
`CatalogRead` struct with the facts, templates, next cursor and trimmed flag.
No contract schema or `pactoVersion` change.
