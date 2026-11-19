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
make the answer `partial`. A change of any classification with an unchecked or
unresolved referrer ends `unverified`: the diff can miss a policy the provider
tightened, so a referrer Pacto could not check is never taken as safe. A
referrer the viewer may not see is never named; the answer counts it in
`hidden` (`consumers.hidden` in the dashboard).

A golden path that references the policy runs nowhere, so it is never a
consumer and never holds the gate. The answer lists it in `templates` to
review: a service created from it checks against the new policy.

### Observed consumers

Observed edges come from OpenTelemetry traces via `--traces <file>`. Beyond
corroborating declared consumers, traces surface **observed-only (shadow)
consumers**: services seen calling the changed service without declaring the
dependency. With traces they appear as direct consumers at `observed` confidence.

## Consumers from the organization index

Every `pacto publish`, `pacto push` and `pacto ci --publish` writes one entry
per published version to `<registry>/pacto-index`. `<registry>` comes from the
provider reference as for the [target snapshot](ci.md#where-consumers-come-from):
`ghcr.io/acme/contracts/ledger` tries `ghcr.io/acme/pacto-index`, then
`ghcr.io/pacto-index`. `--index` names another repository and `--index none`
reads none. Any `--root` skips it, since `--root` names the consumers.

`pacto impact` lists the index by tag and reads each service's newest entry.
It refuses an entry not bound to its tag, its index host and its service's
bundle repository, then pulls each selected consumer's bundle by digest and
judges its declared dependency. Each code below makes the answer partial, so a
`BREAKING` change never ends `clear` on the index alone:

| Code | Meaning |
|---|---|
| `CONSUMER_INDEX_NOT_FOUND` | No candidate holds an index |
| `CONSUMER_INDEX_UNAVAILABLE` | The registry refused or failed the listing |
| `CONSUMER_INDEX_TRUNCATED` | A read bound or the timeout stopped the read |
| `CONSUMER_INDEX_ENTRY_INVALID` | Entries malformed, unbound or unconfirmed by their own repository were not used; a service whose newest entry is refused counts as not indexed |
| `CONSUMER_INDEX_UNCHECKED` | No target source to check the index against |
| `CONSUMER_NOT_INDEXED` | A declared consumer the fleet holds has no entry; it stays in the answer |

The answer reports the read in `consumerIndex`. `pacto_impact` over stdio reads
the index; over HTTP and in the dashboard it is never read.

## Target snapshot consumers

The delivery recorder of each cluster publishes a signed target snapshot to
`<registry>/pacto-targets:cluster-<uid>` every 5 minutes, where `<registry>`
is the operator chart's top-level `registry` value; with no `registry` it
publishes none. `pacto impact` and `pacto ci` read it with registry read
access alone, so CI finds the consumers that run without a kubeconfig. See
[Where consumers come from](ci.md#where-consumers-come-from) for how Pacto
finds the repository and pins a cluster.

A snapshot carries every target source the installation accepted, each as
its own fleet source with its own `asOf` and completeness:

- `targets-<uid>`, kind `target-snapshot`: the Pacto resources of the cluster,
  read from their status. Their consumers carry `declared` confidence.
- `targets-<uid>-workloads`, kind `kubernetes-workloads`: workloads no Pacto
  targets, as [inferred services](operational-graph-inferred.md). An inferred
  service never counts as a consumer and its health never gates impact.
- `targets-<uid>-evidence`, kind `evidence-http`: the targets the cluster's
  [Evidence Server](evidence.md) accepted from any producer, `pacto agent`
  included. They count as observed targets, as `--evidence-url` does.

A snapshot counts only when a trust entry scoped to its cluster verified its
signature and its `asOf` is less than 15 minutes old. Otherwise every section
reads `partial`, `stale` or `unavailable` with a `TARGET_SNAPSHOT_*`
limitation (see
[Sources](operational-graph-reference.md#target-snapshots)), and a
`BREAKING` change with consumers ends `unverified`. A section the publisher
could only read in part, such as an Evidence Server that did not answer,
makes its own source and the answer partial, and the other sections still
answer.

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
`OBSERVED_EVIDENCE_ABSENT`: observed-only consumers cannot be found. An
observed call to a name that matches more than one service, or that is neither
a service nor an external system, raises `OBSERVED_IDENTITY_UNRESOLVED` and
makes the answer `partial`. A call to an external system that the
[external catalog](external-catalog-reference.md#matching) or a host name
identifies, such as `api.stripe.com`, is no consumer of a service and raises
nothing.

## What's next

- [Impact analysis](impact.md): the analysis these consumers feed, its
  confidence model and its release gate
- [Ask what happens if](impact-what-if.md): impact for a service that becomes
  unavailable, is removed or regresses
