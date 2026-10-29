---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

Make Pacto easier for an agent to drive from the CLI help and the MCP tool
text alone.

**`pacto impact` has a new exit status.** It now exits `2` when a breaking
change cannot be shown to be safe. A CI job that treated any non-zero exit as
a failure fails on these runs too; one that gates on deployed impact alone can
treat `2` as a pass.

`pacto impact` and the fleet tools no longer give answers that read as safe
when they are not.

- `pacto impact` adds the new revision to the snapshot when no fleet source
  holds the changed service, so `--local ./fleet` with only the consumers
  finds them instead of answering "No affected consumers".
- Every impact answer ends in a release gate, `gate` and `gateReason`, and the
  exit status follows it: `0` clear, `1` blocked (a breaking change reaches an
  incompatible consumer on a known target) and the new `2` unverified (a
  breaking change on a partial answer, or with consumers none of which is shown
  to be safe). A declared-only run of a breaking change with consumers now
  exits `2` instead of `0`. The `pacto_impact` MCP tool, the dashboard's Change analysis
  view and the terminal UI show the same gate and its reason.
- On a breaking change, a consumer whose range accepts the new version carries
  a `note`: the range match does not show that it avoids what the change
  removes. An open range such as `>=1.0.0` is named as open.
- A declared dependency carries `rangeStatus` (`satisfied`, `unsatisfied` or
  `unknown`) beside `resolved`. A provider in the fleet at a version outside the
  range is reported by `pacto fleet status`, `pacto_fleet_status`, `explain`
  and the dashboard's attention list as `DEPENDENCY_RANGE_UNSATISFIED`, in the
  new `out-of-range` category (`--unsatisfied-ranges`, `unsatisfied_ranges`).
- `pacto_fleet_status` with no category selected, including empty input,
  reports every category, as `pacto fleet status` does. It returned
  `items: null` before.
- `owner` has one shape in `search` and `get`, the contract's
  `{team, dri, contacts}`, and is left out when no contract declares one. A
  service read only from Kubernetes takes its owner from the operator's
  `status.contract.owner`, where it was empty before.

Delivery plans are readable to an agent from the tools alone. The CLI and the
MCP delivery tools now say the same thing. The operator's promoter shares this
code, so its messages and the Rejected-plan behaviour change with it.

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

`pacto init` and `pacto_create` with `pactify` read more of a service into its
contract. The workload's environment, including the ConfigMaps it reads,
becomes a configuration schema with each value as its default. A URL in it
that names another service in the cluster becomes a dependency: with that
service's contract in `--fleet`, its bundle and a range from its version;
without it, a placeholder range that the contract comment and Unknown name. A
readiness, liveness or startup probe becomes a health check.

`pacto init --describe-only` (`describe_only` in `pacto_create`) writes a
contract that only describes the service: no publish section, no signing key
and no `.pactoignore`. Without it, `init` still writes the publish section and
now says how to leave it out.

`pacto init`, `pacto_create` and `pacto_edit` flag an owner written as
`team/billing`, and `pacto_check` suggests the `pacto_edit` call that writes
`billing`. `pacto_check` also explains how to declare a service of
your own whose contract is not published yet, so it shows as unresolved until it
is. The contract schema and the `DEPENDENCY_REF_REQUIRED` message say the same.

The MCP server instructions, `pacto_schema` and `pacto --help` now describe
Pacto as an operational contract system with optional delivery, and
`pacto_schema` returns how to describe an AI agent as a service.
