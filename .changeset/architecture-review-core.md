---
"@pacto/core": minor
---

Fixes from a whole-repo architecture review. Three of them change behaviour.

**Behaviour changes**

- An `extension` capability no longer keeps a contract at Unknown forever.
  Pacto has no evaluator for one, so `validation.Evaluate` now lists it in
  `Coverage.Unverified` as `capabilities[ref=<ref>]` instead of counting it as a
  required assertion and emitting `EXTENSION_EVALUATOR_UNAVAILABLE`. A contract
  declaring an extension can reach Compliant; that verdict says nothing about the
  extension. The code stays registered so stored findings still classify, and is
  deprecated.
- An evidence observation with outcome `Observed` must carry every
  verdict-bearing payload key for its kind (`present`, `type`, `reachable` or
  `durable`, plus `conformant` for a present configuration). A missing or `null`
  key used to decode to `false` and read as a confirmed violation; it is now
  rejected. The payloads Pacto's own producers emit always carry these keys.
- `PACTO_NO_ANIM` and `PACTO_NO_UPDATE_CHECK` share one parser: Go's boolean
  spellings, with any other non-empty value read as set. `PACTO_NO_ANIM=0` or
  `false` now leaves animations on, and `PACTO_NO_UPDATE_CHECK=true` now disables
  the update check. `1` works as before.

**Fixes**

- MCP `pacto_check` runs the same validation path as `pacto validate`,
  including `pacto.lock` verification, so a drifted lockfile no longer passes.
- MCP `pacto_edit add_config_properties` writes the properties into the first
  configuration's schema file, merging with what is there, instead of declaring
  a configuration and dropping them. Edited files are written through a
  uniquely named temporary file that keeps the target's permissions.
- `pacto agent --docker-label-filter key=value` (repeatable) narrows `--docker`
  to containers carrying every given label, so one agent on a shared daemon can
  keep to one Compose project. The Compose demo now does.
- Evidence ingestion keys a record's domain by the same rule the fleet sources
  use (`contract.OCIDomain`), and keeps the evidence validation detail in the
  server log. Sending evidence uses a client with a timeout and a bounded
  response read.
- Free text in `pacto doc` tables escapes `|` and folds line breaks.
- The CLI reference now states that `PACTO_INSECURE_REGISTRIES` applies to
  every registry access and that an unknown `--output-format` is rejected.

**API additions**

- `contract.OCIScheme`, `TrimOCIScheme`, `SplitOCIDigest`, `SplitOCITag`,
  `SplitOCIRef` and `OCIDomain`: one lenient set of reference helpers, now used
  everywhere Pacto split references by hand.
- `contract.DocsDir` and `contract.WalkDocs`, `lock.ReadFS` and
  `capability.ToolsForContract`: the bundle readers the fleet, the offline view
  and MCP share.
- `finding.Registry` returns every registry row; `fleet.Paginate` and
  `fleet.Page` page a sorted list; `fleet.AttentionCategoriesList` returns the
  attention order as a copy; `fleet.SourceKindKubernetes` names the Kubernetes
  source kind.

**Deprecations, removed at v4**

- `fleet.BuildOptions.AllowPartial` (never read; use `DisallowPartial`).
- `fleet.AttentionCategories` (use `AttentionCategoriesList`).
- `finding.CodeExtensionEvaluatorUnavailable` (no longer emitted).
- `contractview.LookupValidation` and `contractview.ValidationCatalogEntry`
  (the retired operator condition vocabulary).
