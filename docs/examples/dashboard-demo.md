---
# The two "Try it" pages are the fastest answer to "what is this?", so they
# outrank pages that merely mention a demo in passing.
search:
  boost: 3
---

# Live dashboard demo

**Nothing to install.** The Pacto dashboard runs entirely in your browser: the
engine and a curated set of demo contracts are compiled to WebAssembly, with no
backend or live registry.

Two things about the fixture, before you open it — both are deliberate, and both
look like bugs if nobody tells you:

- **It opens on a degraded-source banner.** Of the fixture's six data sources one
  is unavailable and one is partial, because a fleet view that only ever shows
  complete knowledge teaches you nothing about the day it isn't.
- **`platform-app-config` appears twice.** Two different services, from two
  different sources, that happen to share a name. A name is not an identity (see
  [three identities](../operational-graph.md#three-identities-never-flattened)),
  so the graph keeps them apart instead of merging them into one row.

The whole engine ships to your browser, so the first visit downloads about
11 MB of compiled WebAssembly (57 MB unpacked). Give it a moment on a slow
connection; the browser caches it afterwards. Nothing you do in it leaves the
tab — there is no backend to leave for.

<a href="../../demo/#/fleet" class="md-button md-button--primary">Open the live dashboard demo →</a>

To run the dashboard against your own services, see
[Dashboard container](../dashboard-docker.md).

It showcases the full UI against a realistic set of services:

- **Operational overview & compliance** — a fleet spanning edge, domain, infra,
  platform and external tiers, opening on the degraded sources and the duplicate
  name described above.
- **Dependency graph** — resolved from each contract's declared dependencies,
  with blast-radius highlighting.
- **Change analysis** — `payments-service` spans six versions; the
  `v1.2.0 → v2.0.0` step is a breaking change that removes the `/charges` API,
  and the same screen shows which consumers that break reaches.
- **Readiness** — never a separate screen, because readiness is declared by a
  contract revision about itself. It is a *Needs attention* category, a
  distribution over every revision in the snapshot on the **Contract revisions**
  inventory (with a filter for each of its buckets) and a section on the revision
  itself. `payments-service` 2.1.1 declares a
  [readiness gate](../contract-reference/sections.md#readiness) that fails: one
  claim worth 30 of the 100 declared weight — the large-language-model safety
  evaluation suite — is `not-done`, so the revision scores 70 against its
  `minScore` of 80. `orders-service` 1.2.0 passes its own gate and still runs on
  a target observed to be non-compliant, which is the difference between declared
  preparedness and observed behaviour on one screen.

The source and build harness live in
[`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).
