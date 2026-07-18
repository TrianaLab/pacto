# Progressive policy versioning

**Problem.** You want to raise the bar on what a "compliant service" means — without breaking every service that's already on the platform. New services should adopt the strictest rules; existing services should migrate at their pace.

**How it works.** The policy contract is versioned. Each major version represents a new compliance bar. Services pin to whichever version they've achieved, and migrate forward by bumping the ref.

| Version | What it enforces |
|---------|-----------------|
| `1.0.0` | `service.owner` declared, `runtime.health` defined |
| `2.0.0` | + `runtime.workload` declared, `interfaces[]` if exposed |
| `3.0.0` | + `configurations[]` schema present, `metadata.labels` required |
| `4.0.0` | + `runtime.health.path` set, `scaling.min >= 2` for `service` workloads |

A service pinned to `platform-policy:2.0.0` keeps validating against v2's rules until the team is ready to bump — the platform never forces the change.

**Why this works.**

- **Forwards is opt-in, never forced.** Teams migrate when they have time
- **Backwards is enforced.** A service can never silently weaken its policy — `pacto diff` flags removing or changing a policy ref as potentially breaking (classification `POTENTIAL_BREAKING`)
- **The version is the negotiation point.** Conversations about "should we require X?" become "should we publish v4 that requires X, with a six-month adoption window?"

**Two dials.** Pinning the policy's major version (above) makes each new bar opt-in and negotiated. Alternatively, a rule layer referenced *transitively* through the platform contract can be left unpinned: republishing that one schema propagates a new rule fleet-wide immediately, with no per-service bump — at the cost of lockfile drift, since every republish changes the resolved digest and forces services to re-lock ([`pacto lock --check`](../lockfile.md) fails until they do). Pinned is opt-in and negotiated; floating is instant and unilateral but forces re-locks.

**Coordinate with `pacto validate`.** When a service ref-bumps from `2.0.0` to `3.0.0`, `pacto diff` reports the changed policy ref; `pacto validate` resolves the new policy and fails *before* merge if the contract does not satisfy it — the team sees the gap and either fixes it or stays on `2.0.0`.

**Cross-links:** [`policies`](../contract-reference/sections.md#policies) · [Change classification — Policy](../contract-reference/diff.md#policy)

---
