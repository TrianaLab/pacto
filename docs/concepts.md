# Concepts

[The Pacto model](model.md) is how a declaration is compared against the
previous revision, against its constraints and against evidence.

That comparison rests on a small number of distinctions Pacto refuses to
collapse. Each one exists because collapsing it produces an answer that is
confidently wrong rather than honestly uncertain, and a confidently wrong answer
is worse than no answer at all — for a person and much worse for an agent.

This page is the index of those distinctions. It states each one in a sentence,
names what breaks if you conflate the two sides, and links to the page that
explains the mechanics. It deliberately duplicates none of them.

---

## Identity

### A service is not a revision, and a revision is not a running instance

`payments-api` is a name with an owner. `payments-api@sha256:…` is one immutable
thing that name once declared. `production-eu/kubernetes-workload/payments-api`
is a place something runs. Flatten them and "is payments-api compliant?" stops
having a single answer, because the name has many revisions and each revision
may run in many places at once, at different versions.
→ [Three identities, never flattened](operational-graph.md#three-identities-never-flattened)

### A requested reference is not a resolved identity

`payments-api:latest` is a question; `payments-api@sha256:…` is an answer. A tag
is mutable, so the same requested reference can name different content tomorrow.
A requested reference must be resolved to an immutable identity before anything
treats it as exact content.
→ [Requested, resolved, identity](mcp-catalog-discovery.md#requested-resolved-identity)

### A resolution has a lifetime, and the lifetime belongs to the boundary that made it

A catalog discovery session resolves every root as it is built and then freezes
the result, so a tag that moves in a registry does not move that catalog. The
Kubernetes operator's `Latest` resolution policy resolves the highest semver tag
on every reconciliation, so the same requested reference legitimately answers
differently once a new version publishes; `PinnedTag` and `PinnedDigest` do not.
Both obey the rule above at different lifetimes: a resolution is exact for the
snapshot, session or reconciliation that produced it, and nothing carries it
past that boundary.

### The same name in two domains is two services

Identity is domain-qualified, so two organizations that both run a
`payments-api` do not merge, and the *same* content mirrored into two domains
stays two services. Conversely a repository basename, a policy entry name or a
path leaf is a label, never an identity.

### An exact revision match is not retrievable content

These are two independent facts about one operational target and Pacto reports
both.

- *Match certainty* asks: do we know which revision this target is running?
  It is `exact`, `inferred`, `ambiguous` or `unresolved` — [what each value
  means, and which field carries
  it](operational-graph.md#how-certainly-a-target-is-matched-to-a-revision).
- *Content retrievability* asks: can Pacto fetch exactly that content now? It
  depends on whether a digest is present, whether the reference is mutable and
  whether the artifact is reachable at all.

A target can pin a digest that names a revision unambiguously while that content
sits in a registry Pacto cannot read: the match is `exact` and the content is not
retrievable, with no contradiction between them. Anything that needs the *content* — a diff, an impact
verdict — requires retrievability and says so when it is missing; anything that
needs only the *identity* does not.

### Declared ownership is not a canonical owner identity, and a contact point is neither

A contract declares an owning team or a directly responsible individual (DRI);
Pacto canonicalizes that declaration into an owner identity whose kind and value
are both part of the key, so a team and a person who happen to share a string
never merge. An email address or a chat channel is how you reach an owner, not
who they are.

---

## Knowledge

Every Pacto answer carries how much of the world it actually saw. The words below
are not degrees of the same thing — they are different claims.

| Word | Claim |
|------|-------|
| **complete** | Every source answered. Nothing is missing. |
| **empty** | Every source answered and there is genuinely nothing. This is *complete* knowledge of an empty result. |
| **partial** | At least one source was unavailable, stale or itself partial. What you see is a floor, not a total. |
| **stale** | Every source answered, but one of them last saw the world a while ago. Its records are real and may have moved on since. |
| **unavailable** | A source did not answer at all. Whatever it knows is missing from this answer entirely. |
| **unknown** | We never received a completeness we could assert. Not the same as empty. |

Three of these travel on the wire, in every answer's `meta.completeness`:
`complete`, `partial` and `empty`. The other three are a consumer's reading of the
same envelope — `stale` and `unavailable` come from the per-source health reported
alongside it, and `unknown` is what is left when no envelope arrived at all. The
dashboard takes the worst of the six and gates every all-clear on that, because
per-source health is the stricter signal: a source that is down must not be masked
by the one word the snapshot chose for itself.

### Empty is not unknown

"There are no non-compliant services" and "we could not find out" render
identically as a blank list and mean opposite things. Pacto distinguishes them
everywhere, and the dashboard will not show an all-clear under anything less
than complete knowledge.

### Partial is not complete, and partial is not empty

A partial answer with zero results is not an empty result set — it is a result
set we could not finish building.
→ [Partial is not empty, and not complete](mcp-catalog-discovery.md#partial-is-not-empty-and-not-complete)

### Stale is not unavailable

A stale source answered, just not recently: its records are real but may have
moved on. An unavailable source did not answer at all. Treating stale as
unavailable throws away true facts; treating it as current presents old ones as
new.

### Unknown is not not-evaluated

"We evaluated this contract and cannot decide" and "we never evaluated it" are
different states, and an aggregate that merges them misreports the same fleet as
inconclusive when it is merely untouched.

### A bounded list is not the population

Every list Pacto returns is bounded: the rows are one slice of the whole,
`truncated` says whether more exist, and a count taken from the visible rows is
a floor and is labelled as one.
→ [Aggregates over a bounded list](fleet-queries.md#aggregates-what-a-bounded-list-can-still-tell-you-about-the-whole)

### An unknown total is not a total of zero

A bounded list carries the true total whenever the total is knowable. Sometimes
the counting work is itself bounded — the walk stops before it can reach the end
of the population — and then the total is omitted rather than estimated, because
an estimate would be indistinguishable from a count. Four states stay separate:

| What you get | What it claims |
|------|------|
| `total` equal to the row count | The population was counted, and the list is all of it. |
| `total` above the row count, `truncated` | The population was counted; the rows are one page of it. |
| `total` of zero | Counted, and there is genuinely nothing. An authoritative zero. |
| no `total`, `truncated`, a count of what was reached | Counting stopped early. The count is a lower bound and the true total is unknown. |

### An authoritative zero is not a missing total

"Zero dependents, and we counted" and "we have no dependent count" are distinct,
and only the first licenses a conclusion. An absent total rendered as `0` is the
substitution that turns "we stopped counting" into "we counted, and there are
none" — honest uncertainty presented as a confident falsehood.

The remaining distinctions -- what is declared against what is observed, and
which surface owns which answer -- are in
[Evidence and boundaries](concepts-boundaries.md).

---

## See also

- [The Pacto Operational Graph](operational-graph.md) — the read model these
  distinctions are enforced in
- [The Pacto model](model.md) — the engine, the compliance states and the
  control loop these distinctions feed
- [Architecture](architecture.md) — the layers and the package that owns each
  concept
- [Collectors and the evidence boundary](collectors.md) — how evidence is
  produced and where the boundary sits
- [Impact analysis](impact.md) — the confidence model over a change
- [Composition patterns](patterns/index.md) — how the primitives are combined
  in practice
- [MCP integration](mcp-integration.md) — how an agent reads all of this
- [Kubernetes operator](integrations/kubernetes/overview.md) — where these words
  become a per-contract verdict in a live cluster
