---
"@pacto/core": minor
---

Give an external system an owner without touching Git. `pacto claim <identity>
--owner <team>` plans, then with `--yes` publishes a declared contract with
`service.external` and `service.owner.team` to `<registry>/<name>`, signed
keyless by you; its `pacto.inferred.yaml` declaration names `inferredFrom:
external:<identity>`. Naming the first owner takes a default approver of the
installation's owner policy. The MCP tool `pacto_declare` takes
`service: external:<identity>` with `owner` and `name` and writes a draft a
person accepts with `pacto claim --from-draft`, and the External page on the
dashboard gains an Owner line with a **Claim** button. After a claim the
declared contract wins: the external record carries `claim`, explain says
`EXTERNAL_CLAIMED`, impact names the claimed contract and `INFERRED_PROVIDER`
asks to point the ranges at it, while a range on `pacto-inferred` stays
unknown. Every unclaimed external system with consumers raises
`UNOWNED_EXTERNAL` (info) in the `unowned` category, ranked by how many
services use it, counting hidden consumers without naming them and routed to
the installation's default approvers for routing only. Its next action is
`claim`. The Overview tile becomes "Services and external systems with no
owner" and `unownedExternals` counts the external ones. Validation reports
`DECLARATION_EXTERNAL_MISMATCH` when a declaration claims an external system
`service.external` does not name.
