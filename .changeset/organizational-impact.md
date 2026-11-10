---
"@pacto/core": minor
---

`pacto impact`, the `pacto_impact` MCP tool and the dashboard's Change analysis
ask what happens if a service becomes unavailable, is removed or regresses:
`--unavailable`, `--removed` and `--regressed <service>@<revision>` on the CLI,
`event`, `service` and `revision` on the tool and an event mode in Change
analysis. Each consumer carries an `effect` (`breaks`, `may-degrade` or
`unknown`); the event gate is `unverified` (exit 2) when anything depends on
the service and `clear` (exit 0) only when the complete snapshot shows nothing
does. Event answers always read observed and uncontracted callers. A removal
lists each consumer's `resources` and the provider's declared lifecycle. A
regression named by artifact digest cites delivery's recorded failures, read
with the caller's own credentials.

Services that reference a changed policy are consumers (`via:
policy_reference`), checked against the new policy: one that fails it blocks,
or needs a person, whatever the classification. A change of any classification
with a referrer Pacto could not check or resolve needs a person too. Golden
paths that reference the policy run nowhere: they are listed under
`templates` to review and never hold the gate. Consumers the viewer may not
see are counted in `hidden` (`consumers.hidden` in the dashboard).

`owners` now holds canonical owner keys such as `team:shop`, and
`consumerTallies.byOwner` groups by its new `ownerKey`, keeping the display
label in `owner`; in an event answer its `incompatible` counts the consumers
that break. Each consumer gains `ownerKey`, `via`, `evidence`, `effect` and
`resources`. Target and
observed-call evidence older than 24 hours before `asOf` lowers a consumer's
confidence to `unknown`, and `--traces` edges carry their last-seen time, so
stale ones stop corroborating. `OBSERVED_IDENTITY_UNRESOLVED` makes the answer
partial. New limitations: `POLICY_REF_CONSUMERS_UNRESOLVED`,
`POLICY_REFERRERS_UNCHECKED`, `OBSERVED_EVIDENCE_ABSENT`,
`CONSUMER_EVIDENCE_STALE`, `DELIVERY_EVIDENCE_UNAVAILABLE` and
`DELIVERY_EVIDENCE_TRUNCATED`. An HTTP fleet MCP
server, whether its callers sign in or not, refuses `traces` paths and local
refs and reads no delivery. The blast radius badge, which nothing computed, is
removed.
