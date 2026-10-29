---
"@pacto/core": patch
---

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
  exits `2` instead of `0`. A job that gates on deployed impact alone can treat
  `2` as a pass. The `pacto_impact` MCP tool returns the same gate.
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
