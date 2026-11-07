---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

`service.template` (pactoVersion 2.4) marks a contract as a golden path and
names the generate plugins it offers, and `service.origin` (pactoVersion 2.4)
records the golden path a service was created from as declared provenance
(`ref` and the path's content `digest`). A `"2.0"` to `"2.3"` contract that
sets either field reports `NEEDS_PACTO_2_4`; a repeated plugin reports
`DUPLICATE_TEMPLATE_PLUGIN` and an origin that is neither an `oci://` reference
nor a directory reports `INVALID_ORIGIN_REF`. `pacto diff` classifies every
change to either field as non-breaking.

`pacto init <dir> --from <oci-ref|dir> --owner <team>` creates a service from a
golden path: it resets the identity, applies `--values` and `--set`, records
`service.origin`, validates the result with its references resolved and only
adds files. A path read from a registry is pulled by digest and, when the
trust config (`--trust` or `$PACTO_DELIVERY_TRUST`) names a `role: template`
key, must be signed by one; otherwise the signature step reads Unknown.
`--run-plugins` runs only the plugins of the path you name. `--delivery`, with
`--env`, `--registry-secret` and `--insecure-registry`, plans the cluster pieces
from the created contract when the path publishes manifests under
`publish.deploy`. The step list says what was checked and what Pacto cannot
tell yet. The MCP `pacto_create` tool gains `from`, `values`, `set`,
`run_plugins` and `domain`, and takes `delivery` and its options with `from`;
`from` serves stdio only. `pacto_edit` gains `template`.

A blank `pacto init` now writes `--owner`, `--domain` and a publish section,
and no placeholder readiness block. `pacto_create` without `pactify` refuses a
folder that already holds `pacto.yaml` and keeps any interface or schema file
already there.

The operational graph treats a service whose newest readable revision declares
`service.template` and that runs nowhere as a golden path. Its dependencies and
references are requirements, never consumption: impact, dependents, offering
consumers, attention and the Overview population counts leave it out, and the
Overview counts it in `goldenPaths` and `invalidGoldenPaths`. A running
service that declares `service.template` stays a service and raises
`GOLDEN_PATH_DEPLOYED`. A declared origin links to the golden path revision
whose content digest matches. Entity lists take `template` (`only` or
`exclude`) and `createdFrom` (MCP `created_from`), search takes `template` and
marks golden path hits, and a service created from a path carries a creation
checklist derived from what the fleet observes. A restricted viewer sees a
golden path with no Kubernetes target, with the services created from it that
they may not read counted. The CLI, MCP tools, dashboard (a Golden paths tab)
and TUI show all of it. Consume snippets carry structured `entries`, and a
values skeleton leaves out and names in `missingValues` every required value
no candidate satisfies.

The operator chart's `dashboard.repositories` (operator flag
`--dashboard-repositories`) lists `oci://` references the operator-managed
dashboard reads beside the contracts it discovers, so golden paths, which are
never deployed, show on its Golden paths tab.
