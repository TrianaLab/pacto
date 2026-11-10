# Impact consumers and evidence

[Impact analysis](impact.md) walks the declared dependencies of the changed
service. This page covers the consumers it finds through other edges and how it
judges the age of the evidence behind each one.

## Configuration-reference consumers

A service that takes a configuration schema from the changed service by `ref`
is a consumer too. Pacto selects its configuration in the new revision and
checks its inline and environment values against that schema. With a
dependency too, the stricter of the range and values verdicts speaks.

- Values that pass give `compatible`.
- Values that fail give `incompatible`, and the note lists each violation as
  `configuration <name>: <violation> against <provider>@<version>`.
- A selection that fails in a 2.4 consumer gives `incompatible`.
- `unknown` remains when the consumer's values or the new schema cannot be
  read, or an older consumer's selection fails, and the note says why.

These consumers carry `declared` confidence and feed the
[release gate](impact.md#release-gate-and-exit-status): a `BREAKING` change with one of
them never ends `clear`.

A configuration reference that is neither digest-pinned nor locked cannot be
placed on a revision. When such references name the changed service, by
repository or by name, the
answer adds a `CONFIG_REF_CONSUMERS_UNRESOLVED` limitation and its
completeness is `partial`, so the gate stays `unverified`.

### Policy referrers

A service that references a policy the changed service publishes is a
consumer too, with `via: policy_reference`. Pacto checks the referrer's
contract against the new revision's policy:

- A contract that passes gives `compatible`.
- A contract that fails gives `incompatible`, whatever the classification. The
  gate is `blocked` when the referrer runs on a known target and `unverified`
  otherwise.
- `unknown` remains when the check cannot decide, and the answer adds a counted
  `POLICY_REFERRERS_UNCHECKED` limitation.

A `BREAKING` change with referrers never ends `clear`. Policy references that
are neither digest-pinned nor locked add `POLICY_REF_CONSUMERS_UNRESOLVED` and
make the answer `partial`. A referrer the viewer may not see is never named;
the dashboard counts it in `consumers.hidden`.

### Observed consumers

Observed edges come from OpenTelemetry traces via `--traces <file>`. Beyond
corroborating declared consumers, traces surface **observed-only (shadow)
consumers**: services seen calling the changed service without declaring the
dependency. With traces they appear as direct consumers at `observed` confidence.

## Evidence freshness

Impact judges evidence against the 24 hours before the snapshot's `asOf`.
A consumer whose target evidence is older, or missing, gets confidence
`unknown`, an `evidence` entry that says how old it is and a counted
`CONSUMER_EVIDENCE_STALE` limitation. Stale evidence never raises confidence.
An observed call last seen before that window, in the snapshot or in a
`--traces` file, no longer corroborates. Its caller stays in the list with
confidence `unknown`, so it cannot drop out of the answer. A live source such
as Mira reports no window, and its evidence says "no observation window".

With `--include-observed` and no observed edge anywhere, the answer adds
`OBSERVED_EVIDENCE_ABSENT`: observed-only consumers cannot be found. Observed
calls whose services Pacto cannot identify raise
`OBSERVED_IDENTITY_UNRESOLVED` and make the answer `partial`.

## What's next

- [Impact analysis](impact.md): the analysis these consumers feed, its
  confidence model and its release gate
- [Ask what happens if](impact-what-if.md): impact for a service that becomes
  unavailable, is removed or regresses
