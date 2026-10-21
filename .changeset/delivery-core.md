---
"@pacto/core": minor
---

Add Pacto delivery, the opt-in part of Pacto that decides which version each
environment runs. This release adds the pure core and the client side.

`pkg/delivery` holds the operation model, the four Tier 0 operation types
(`pacto.dev/promote`, `rollback`, `override` and `unfreeze`), the intent digest
and `PlanFor`, the one function the promoter, the CLI and the MCP tools all
call. A promote runs two pre-gates: `pacto.diff` compares the running contract
with the new one, and `pacto.impact` projects the change onto every consumer
that declares a compatibility range. A consumer the new version breaks holds
the change and routes the question to that consumer's owners. Anything Pacto
cannot see (an artifact with no `pacto.dev/contract` annotation, a Pacto it
cannot read, a range it cannot evaluate on a change that is not plainly
non-breaking, a rollback target that was never verified) holds it for a person
to accept the unknown. A plan judges only the consumers running in the Stage's
environment. When one of them does not accept the new version, the plan's
`order` says which consumer must move first, the version running there and the
range it declares, its Stage in that environment and either the newest version
its upstream Stage verified that accepts both the running provider version and
the new one or why Pacto cannot tell. Compatibility comes from the declared
ranges, not from which fields or endpoints a consumer uses. Pacto never moves
the other service itself. Every registry read a plan makes gives up after 10
seconds, so a registry that is down makes a contract unreadable instead of
holding the plan. `pkg/impact` now judges a consumer that runs more than one
revision by the one that accepts the new version least. `ContractVerdict` maps
the Pacto contract check onto Pass, Fail or Unknown only when the check
evaluated exactly the applied version.

`pacto delivery types`, `describe`, `plan`, `propose`, `get`, `cancel` and
`approve` drive the promoter over the ambient cluster. A tag is refused, and
the refusal names the digest it resolves to; `propose` with the plan's intent
digest refuses with `PlanStale` if anything moved since. `plan` and `explain`
print what must move first. `approve` is for people with a fresh login.

`pacto mcp --delivery` serves `pacto_delivery_operation_types`,
`pacto_delivery_operation_describe`, `pacto_delivery_operation_plan` and
`pacto_delivery_get` to anyone, and `pacto_delivery_operation_propose` and
`pacto_delivery_cancel` only when the apiserver says the caller is an agent (a
service account or an agent group). There is no approve tool.

`oci.Client` gains `Annotations`, so an artifact can name its contract.
