---
"@pacto/core": patch
---

Make delivery plans readable to an agent from the tools alone. The CLI and the
MCP delivery tools now say the same thing:

- `pacto delivery candidates` and the `pacto_delivery_candidates` tool list the
  versions a Stage may take, with their digests: what its candidate source
  offers that `pacto publish` published. The `to` of plan and propose takes a
  version (`3.0.0`), a tag or a digest. A version or a tag must name exactly one
  candidate; Pacto refuses one that names none or several, or that it cannot
  match because a candidate cannot be read, and says why. A missing `to` says
  where to find a valid value.
- A plan carries `next`: which teams must sign (and that their approval also
  meets a Stage's rule that a person approves every promote), that an agent
  cannot answer and the operation is then Held, and that an operation nobody
  decides before its approval deadline concludes Expired with nothing changed.
  `pacto delivery get`, `pacto_delivery_get` and `pacto delivery explain` say
  what happens at the deadline of a held operation.
- A plan predicted Rejected asks nobody anything, and its failing verdict says
  what to do first, such as promoting the version on the upstream Stage.
- The message for a consumer that must move first names the Stage to promote it
  on in plain words.
- `PlanStale` names the reason or the on-behalf-of person when a propose adds
  one the plan did not have, and otherwise lists what the intent digest binds
  instead of saying the Stage moved.
