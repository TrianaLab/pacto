# Fleet query semantics

The five questions the [operational graph](operational-graph.md) answers, and
what a bounded answer still tells you about the whole population.

## Query semantics

The read model answers five kinds of question, each a pure operation. None
performs I/O; a single snapshot serves concurrent queries. **Every answer
carries a `meta` envelope with `schemaVersion`, `snapshotId`, `asOf`,
`completeness`, `limitations` and `sources`**. `schemaVersion`
(`pacto.dev/fleet/v1`) is the compatibility contract to branch on; `snapshotId`
is the content digest that proves two answers came from the same system view.

| Query | Answers |
|-------|---------|
| **search** | Which logical services match this filter (owner, label, status, compliance, capability, dependency, readiness). Bounded and deterministically ordered. |
| **get** | Everything about one service (its revisions, targets, declared dependencies, dependents, tools and skills) or one target. |
| **graph** | Traverse dependencies or dependents from a service — direct or transitive, cycle-safe, with unresolved edges surfaced. |
| **status** | What needs attention: non-compliant or unknown targets, invalid contracts, stale evidence, missing readiness, unresolved dependencies. |
| **explain** | Deterministic, structured reasons for a subject's state. Pacto embeds no model — it hands an agent structured reasons to turn into prose. |

Two more operations sit beside the five and are not queries: `snapshot` emits
the whole read model as one document, and `reconcile` reports declared
dependencies against observed ones ([Observed dependencies and
reconciliation](operational-graph.md#observed-dependencies-and-reconciliation)).

Errors are typed: a missing identity is a not-found, an ambiguous one lists its
matches.

A `pacto fleet search --output-format json` answer over a local bundle root with
an unreachable OCI source:

```json
{
  "meta": {
    "schemaVersion": "pacto.dev/fleet/v1",
    "snapshotId": "sha256:c81df8fd572ecaa7c884968e728daff5b47c40b8e8c5cbcf1926c19740db95a9",
    "asOf": "2026-07-29T10:00:00Z",
    "completeness": "partial",
    "limitations": [
      { "code": "SOURCE_UNAVAILABLE", "source": "oci",
        "message": "source oci is unavailable; its records are missing from this snapshot" }
    ],
    "sources": [
      { "id": "local", "kind": "local", "status": "available",
        "lastSuccessfulSync": "2026-07-29T10:00:00Z",
        "observedAt": "2026-07-29T10:00:00Z",
        "revisionCount": 25, "targetCount": 0 },
      { "id": "oci", "kind": "oci", "status": "unavailable",
        "error": { "code": "UNAVAILABLE", "message": "the source is unavailable" },
        "revisionCount": 0, "targetCount": 0 }
    ]
  },
  "total": 1,
  "count": 1,
  "services": [
    { "key": "payments-service", "name": "payments-service",
      "owner": "team/payments", "status": "NotEvaluated",
      "revisionCount": 6, "targetCount": 0, "sources": ["local"] }
  ]
}
```

`total` is the whole matched population; `count` is how many rows this page
carries. `key` is the service's canonical identity — match on it, not on `name`,
which is not unique across domains. `owner` here is the authored owner label, not
the canonical owner key (see [ownership](#ownership)).

---

## Aggregates: what a bounded list can still tell you about the whole

Every list answer is bounded, so its rows are one slice of the matched population.
Alongside them the read model returns an **aggregate computed in the backend over
the complete matched population, before paging** — a distribution drawn from the
rows would present the first page as the fleet.

That population is heterogeneous by design — one query can match services,
revisions and targets at once — so **every tally names the population it
partitions** rather than sharing one denominator, and per-kind counts are reported
rather than summed from buckets, so a disagreement stays visible.

| Tally | Partitions | Buckets |
|-------|-----------|---------|
| **serviceCompliance** | matched services | the compliance states, rolled up from each service's targets |
| **targetCompliance** | matched operational targets | the compliance states as observed per target |
| **ownership** | matched services | `consistent` · `conflicting` · `unowned` |
| **readiness** | matched contract revisions | `passing` · `belowThreshold` · `expired` · `notDeclared` |

`serviceCompliance` and `targetCompliance` are never summed: a service status is
already a roll-up of its targets, so adding them counts the same operational
reality twice.

### Ownership

Ownership is a property of revisions agreeing, not of one field somebody set —
`service.owner` is authored per revision. A service is `consistent` when every
revision that declares an owner declares the *same* one; a revision that declares
none is silence, not a contradiction. `conflicting` is folded into neither
`unowned` nor `consistent`: "two teams claim this" and "nobody claims this" need
opposite fixes, and the owner shown on a conflicted service is a documented
tie-break, not agreement.

Beside the partition sits a **bounded ranking** of the consistently owned services
by owner (`byOwner`), largest first. It is not a partition: `beyondRanking` holds
the services whose owner fell past the bound, `unidentifiedOwnership` those whose
declared owner resolves to no canonical identity and `distinctOwners` how many
owners exist in total — so
`sum(byOwner.services) + beyondRanking + unidentifiedOwnership == ownership.consistent`.

#### Owner identity and contact points

An owner identity is **namespaced**: `team:payments` and `dri:payments` print the
same word and are two owners. A ranking row whose label is shared across
namespaces is flagged `ambiguous`, and a consumer must show the namespace.
Ambiguity is decided over the complete population of distinct owner keys, never
over the rows that survived the cut.

Declared **contact points** — an email address, a chat channel, a URL — travel
with ownership as bounded metadata and are never identity: no owner key, link or
ranking row derives from one, which is what `unidentifiedOwnership` counts. A
contract naming a mailing list but no Team or DRI *has* declared an owner, so
`unowned` would report a gap the team already closed, and minting a key from the
address would invent an identity nobody authored. The preview is a pointer: its
absence means "not carried here", never "none declared".

### Readiness

Readiness is bucketed per contract revision, never per service, target or fleet:
it is the authored preparedness of one immutable contract, assessed against the
threshold that contract set for itself. It is orthogonal to compliance — a
revision whose readiness passes can be running on a target observed to violate its
contract. `notDeclared` is its own bucket because "nobody wrote an assessment" is
not "the assessment does not pass", and `expired` because an assessment past its
`expires` date cannot be read as current.

The overview carries both tallies over the whole snapshot rather than a filtered
population, and outside the attention backlog: neither is an operational failure.
"Is ownership declared at all" and "is anyone assessing readiness" are systemic
questions about how the fleet is organized and authored.
