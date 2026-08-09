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
- **Change analysis** — `payments-service` spans five versions; the
  `v1.2.0 → v2.0.0` step is a breaking change that removes the `/charges` API,
  and the same screen shows which consumers that break reaches.
- **Readiness** — surfaced as a *Needs attention* category and on the revision
  itself, never as a separate screen. `payments-service` 2.1.0 declares a
  readiness gate that fails:
  a required check (the LLM-safety eval suite) is `not-done`, dropping its score
  to 70, below the required 80 — while `orders-service` 1.2.0 passes.

The source and build harness live in
[`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).
