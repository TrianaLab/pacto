# Live dashboard demo

The Pacto dashboard runs entirely in your browser: the engine and a curated set
of demo contracts are compiled to WebAssembly, with no backend or live registry.

<a href="../../demo/#/fleet" class="md-button md-button--primary">Open the live dashboard demo →</a>

To run the dashboard against your own services, see
[Dashboard container](../dashboard-docker.md).

It showcases the full UI against a realistic set of services:

- **Operational overview & compliance** — eleven services across edge, domain,
  infra and external tiers.
- **Dependency graph** — resolved from each contract's declared dependencies,
  with blast-radius highlighting.
- **Change analysis** — `payments-service` spans six versions; the
  `v1.2.0 → v2.0.0` step is a breaking change that removes the `/charges` API,
  and the same screen shows which consumers that break reaches.
- **Readiness** — never a separate screen, because readiness is declared by a
  contract revision about itself. It is a *Needs attention* category, a
  distribution over every revision in the snapshot on the **Contract revisions**
  inventory (with a filter for each of its buckets) and a section on the revision
  itself. `payments-service` 2.1.1 declares a readiness gate that fails: a
  required check (the LLM-safety eval suite) is `not-done`, dropping its score to
  70, below the required 80 — while `orders-service` 1.2.0 passes, and still runs
  on a target observed to be non-compliant, which is the difference between
  declared preparedness and observed behaviour on one screen.

The source and build harness live in
[`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).
