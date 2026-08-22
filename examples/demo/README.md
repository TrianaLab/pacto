# Pacto dashboard — WebAssembly demo

A static, browser-only build of `pacto dashboard`. The Pacto engine and a curated
set of demo contracts are compiled to WebAssembly and baked in, so the whole
dashboard — fleet, dependency graph, version history, version diff, readiness —
runs client-side with no backend and no live registry.

**Live site:** https://pacto.run/latest/demo/

## Scope: what this demo is (and is not)

This is an **offline contract, graph and dashboard showcase**. Everything you see —
the fleet, the dependency graph, version history, version diff and readiness — is
derived from static contracts compiled into the page. It runs with no backend, no
live registry and no Kubernetes.

It is **not** a Kubernetes runtime compliance demonstration. There is no operator,
no reconciliation and no runtime 4-state evaluation (Compliant / NonCompliant /
Unknown / Invalid) here — those need a live cluster observing real workloads.

For the runtime compliance story, see the real end-to-end journeys, which reconcile
a contract on a live cluster and drive its status through a Compliant → Unknown →
Compliant transition:

- kind acceptance harness: [`tests/acceptance/kind/reconcile.sh`](../../tests/acceptance/kind/reconcile.sh)
- operator envtest acceptance suite: `make -C integrations/kubernetes test-e2e`
- scenario-to-proof map: [`docs/examples/compliance-scenarios.md`](../../docs/examples/compliance-scenarios.md)

## How it works

```
index.html ─► assets/*          the dashboard's Svelte UI, rebuilt with base=/pacto/demo/
   ├─ wasm_exec.js              Go's wasm runtime glue
   └─ boot.js                   loads app.wasm; routes /api,/health,/metrics into wasm
                                     │
   app fetch('/api/services') ──────┘  (a fetch shim intercepts only those paths)
                                     ▼
                                app.wasm: httptest → Pacto's Huma handlers
                                     │
                                EmbedSource  (dashboard.DataSource over //go:embed bundles)
```

- `source_embed.go` — indexes the embedded bundles and implements `dashboard.DataSource`.
- `source_fleet.go` — builds the operational graph the product UI reads: the embedded
  revisions plus five sources, so the demo shows a real estate rather than a clean
  one. A registry (what was declared), a cluster collector (what is running), a
  telemetry collector that corroborates two of those targets and contributes the
  observed call edges, a partial registry mirror and an unreachable edge cluster.
  Between them the targets cover three compliance verdicts (Compliant,
  NonCompliant and Unknown), exact and inferred revision matches alongside targets
  with nothing to match against, fresh / stale / never-observed evidence, one
  service running in two scopes, and the labels and observed runtime values a
  target page exists to show. Nothing is invented: a target with no evidence stays Unknown, and
  the only EXACT match pins the real content digest of an embedded revision.
- `main_wasm.go` — builds the dashboard's Huma API in memory and exposes `__pactoServe`.
- `boot.js` — loads the wasm engine and shims `fetch` for `/api`, `/health`, `/metrics`.

The graph, dependents and cross-reference views derive from each contract's
declared dependencies (every referenced service is embedded), so no OCI resolver
is needed.

## Demo lockfiles

The dependency-bearing demo bundles ship a committed `pacto.lock` (plus a
`.pactoignore` that re-includes it with `!pacto.lock`), so the demo dashboard
shows resolved dependency/reference pins offline. `pacto.lock` is default-ignored
when packing a bundle; `.pactoignore` opts it back in so the lock travels with the
embedded bundle. Only dep-bearing bundles get a lock — leaf bundles (postgresql,
redis, stripe-api, email-provider, platform-*) carry none.

Regenerate with:

```bash
make demo-locks            # runs ./genlocks, then commit the result
```

`genlocks` is OFFLINE and DETERMINISTIC: the demo's `oci://ghcr.io/...` refs don't
resolve without a live registry, so it resolves the closure BY SERVICE NAME within
`./bundles` (every referenced service is present). The lockfile `digest` is a
**content-derived pin for the offline demo, not a live registry digest**: it is
the `lock.HashFS` content hash of the target bundle (computed over the default
ignore set, so the lock files never feed their own hash). Re-running `make
demo-locks` is byte-identical, so the committed demo data stays stable. The
original declared `oci://` ref is preserved verbatim in each entry's `ref`. There
is no k8s runtime in the demo, so no drift is asserted — pins are shown without a
drift status, which is correct offline.

## Build

Requirements: Go 1.26+, Node 22+, make.

```bash
make build                 # builds dist/ at base /pacto/demo/
make build BASE=/          # build at site root for local preview
make smoke                 # exercise the built wasm engine in Node
make serve                 # preview at http://localhost:8088/pacto/demo/
```

`make build` rebuilds the dashboard UI from `../../pkg/dashboard/frontend` into a
scratch dir and copies it into `dist/`; it never modifies the committed
`pkg/dashboard/ui` (which the real `pacto dashboard` server uses).

## Explore the contracts on the CLI (offline)

These work on the local bundles with no registry. Build the CLI first with
`make build` at the repo root.

```bash
# A breaking change: payments-service v1.2.0 -> v2.0.0 (drops /charges).
make breaking-change

# A non-breaking evolution: v1.0.0 -> v1.1.0.
make evolution

# Inspect any contract.
make explain REF=bundles/payments-service/v2.1.0
```

Full contract validation (`pacto validate`) and live graph resolution
(`pacto graph`) resolve refs over the network and require the contracts to be
published to a registry; this demo publishes nothing, so use the live dashboard
above for the dependency graph.

## Size

`app.wasm` is ≈52 MB raw / ~9.6 MB gzipped: it links Kubernetes and OCI client
init code that the demo never calls, which dead-code elimination can't drop.
Acceptable for a cached static asset.

## Don't use sudo

This build never needs root. A single `sudo make` leaves root-owned artifacts
that wedge later non-sudo builds; the Makefile's preflight detects this and
prints a one-time fix.
