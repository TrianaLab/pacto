# Service Readiness Overview — Design

Date: 2026-06-15
Status: Proposed

## Goal

Add an aggregated dashboard page that shows the operational **readiness** of all
services at a glance — score, status, and check gaps — mirroring the UX and
architecture of the existing **Owners** aggregated view. Contract-first: the
dashboard surfaces what each contract *declares* and helps owners spot gaps. It
does not call Grafana/GCP/AWS or validate provider URLs.

## Key decision: build on the model that already exists

The repo already has a complete, validated readiness model. We reuse it as-is and
do **not** touch the contract schema, validation, or operator (confirmed with the
user). The prompt's hypothetical fields (per-check `owner`/`name`, status enum
`passed/failed/missing/...`, category `type`, evidence object) do **not** exist;
we map the requested UX onto the real fields below.

Real model (already surfaced end-to-end):

- `contract.Readiness{ minScore?, checks[] }`; check =
  `{ id, type, evidence(string), weight, expires(YYYY-MM-DD), description? }`.
- `type` is the **evidence kind**: `url | document | ticket | report | artifact |
  identifier | other`.
- `readiness.Evaluate()` derives, per check, a status from expiry only:
  **`Current | Expired | Invalid`**; and a rollup `score` (0–100),
  `minScore` gate, `passing` (score ≥ minScore), plus
  `currentWeight/totalWeight` and `currentCount/expiredCount/invalidCount`.
- Surfaced as `dashboard.ReadinessInfo` (+ `ReadinessCheckInfo`) on
  `ServiceDetails.Readiness`, populated from both contracts (`readinessFromContract`)
  and the k8s operator status (`readinessFromK8s`).
- There is **no** per-check owner (owner is the service-level `contract.Owner`,
  already used by the Owners view) and **no** `missing/failed/not_applicable`
  concept — a check is either declared or not.

### How the prompt's UX maps onto the real model

| Prompt asked for            | Implemented as                                              |
| --------------------------- | ---------------------------------------------------------- |
| `readiness.score`           | derived `ReadinessInfo.score`                               |
| `readiness.status`          | derived bucket (below) from `passing` + `score`            |
| check `status` passed/total | `Current` count / total checks                             |
| "missing / failed checks"   | the model's real gaps: **Expired** + **Invalid** counts    |
| check `type` filter         | evidence-kind enum (`url/document/ticket/...`)             |
| per-check `owner`           | service-level owner (Owners view already keys on this)     |
| evidence `{type,value}`     | `evidence` string + `type`; URL rendered as link           |
| `expires`                   | `expires` (YYYY-MM-DD), Expired/Invalid marked visually    |

### Status buckets (gate + score bands — confirmed)

- **Ready** — `passing` (score ≥ minScore gate)
- **Partial** — not passing AND `score >= 50`
- **Not Ready** — `score < 50`
- **Not configured / Unknown** — no readiness block (renders, never breaks)

## Architecture (mirror the Owners pattern)

Owners aggregates entirely **client-side** from a single `GET /api/services` call.
We do the same. The only gap: `ReadinessInfo` lives on `ServiceDetails` (detail
endpoint) but not on the list payload. So:

### Backend (Go) — one small change

- Add `Readiness *ReadinessInfo \`json:"readiness,omitempty"\`` to
  `ServiceListEntry` (`pkg/dashboard/model.go`).
- In `Server.listServices` (`pkg/dashboard/server.go`), set
  `entry.Readiness = d.Readiness` from the cached index (already a
  `*ServiceDetails` carrying `Readiness`). `omitempty` ⇒ services without
  readiness add nothing to the payload; checks are few and opt-in, so size is
  negligible. This gives the client everything (counts for columns/sorting,
  `checks[]` for type/status filters and inline expansion) from one call.
- Add a `server_test.go` assertion that an enriched entry carries `Readiness`.
  (100% coverage is enforced; the new line is exercised by the readiness
  fixture.)

No new endpoint. No changes to contract/schema/validation/operator.

### Frontend (Svelte 5 + TS)

New route `#/readiness` (overview only). **No new per-service detail route** —
rows link to the existing `#/services/:name`, which already renders the full
readiness checks via `ReadinessSection.svelte`. Inline row expansion shows a
compact checks table for quick scanning (parallels Owners' inline expand).

Files:

- `lib/router.ts` — add `'readiness'` to `Route.view`; parse `#/readiness`;
  `navigate('readiness')`; `readinessUrl()`.
- `App.svelte` — import `ReadinessView`, dispatch the route, pass
  `{services} {initialLoading}` (same as Owners).
- `views/ServiceListView.svelte` — add a third CTA card "Service Readiness"
  next to Dependency Graph / Owners.
- `lib/format.ts` — add pure helpers (unit-tested), reusing existing
  `readinessStatusClass` / `readinessDaysLabel` / `complianceClass`:
  - `readinessBucket(svc) -> 'ready'|'partial'|'not-ready'|'unknown'`
  - `readinessBucketLabel(b)` and `readinessBucketClass(b)` →
    ok / warn / err / neutral badge classes (reuse existing tokens).
  - `summarizeReadiness(services) -> { total, ready, partial, notReady,
    notConfigured, configured, avgScore, totalCurrent, totalChecks,
    totalExpired, totalInvalid }` (avgScore over *configured* services only).
  - `isUrlEvidence(evidence) -> boolean` (starts with `http://`/`https://`).
- `views/ReadinessView.svelte` — the new page:
  - **Summary cards**: Total services, Ready, Partial, Not Ready,
    (Not configured), Avg score, Expired checks, Invalid checks.
  - **Filters**: Owner (dropdown), Readiness status bucket, Check type
    (evidence-kind), Check status (Current/Expired/Invalid). Type/status filters
    keep services with ≥1 matching check.
  - **Sort**: Score asc/desc, Expired count, Invalid count, Owner, Service name.
  - **Table** (one row per service): Service (links to detail), Owner, Score
    (colored via `complianceClass`), Status badge, Current/Total, Expired,
    Invalid, Last updated (best-effort; omitted if no field exists).
  - **Inline expand**: compact checks table — id+description, type pill, status
    badge (`readinessStatusClass`), weight, expires, remaining
    (`readinessDaysLabel`), evidence (clickable link if URL, `code` otherwise,
    "No evidence" if empty). Expired/Invalid rows visually marked.
  - **Empty states**: services without readiness fall in "Not configured"; the
    page renders correctly when *no* service declares readiness.

### Color system (reuse existing tokens; no new CSS vars)

- Buckets: Ready→`ok`(green), Partial→`warn`(amber), Not Ready→`err`(red),
  Not configured→`neutral`(slate).
- Check status: reuse `readinessStatusClass` already used on the detail page —
  Current→ok, Expired→err, Invalid→warn — so the same status looks identical
  across overview and detail (avoids divergent coloring).

## Tests

- `lib/format.test.ts` — `readinessBucket` (each bucket + no-readiness),
  `summarizeReadiness` (counts, avg over configured only, all-empty), and
  `isUrlEvidence`. Mirrors the existing `aggregateByOwner` test style.
- `lib/router.test.ts` — `#/readiness` parses to `{view:'readiness'}`.
- `pkg/dashboard/server_test.go` — enriched list entry carries `Readiness`
  for a service with a readiness block.

## Out of scope (YAGNI)

- No contract/schema/validation/operator changes.
- No new per-service readiness detail route (existing service page covers it).
- No readiness bar chart component (Owners has one; not requested here — the
  summary cards + distribution cover the need). Can be added later.
- No historical/trend tracking (model is point-in-time).
- No changes to the Owners view.

## File change summary

Backend: `pkg/dashboard/model.go`, `pkg/dashboard/server.go`,
`pkg/dashboard/server_test.go`.
Frontend: `lib/router.ts`, `lib/router.test.ts`, `lib/format.ts`,
`lib/format.test.ts`, `App.svelte`, `views/ServiceListView.svelte`,
new `views/ReadinessView.svelte`. Rebuild bundles via `npm run build`
(outputs to `pkg/dashboard/ui/`).
