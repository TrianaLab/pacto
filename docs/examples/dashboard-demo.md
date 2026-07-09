# Live dashboard demo

The Pacto dashboard runs entirely in your browser: the engine and a curated set
of demo contracts are compiled to WebAssembly, with no backend or live registry.

<a href="../../demo/" class="md-button md-button--primary">Open the live dashboard demo →</a>

It showcases the full UI against a realistic fleet:

- **Fleet & compliance** — eleven services across edge, domain, infra and
  external tiers.
- **Dependency graph** — resolved from each contract's declared dependencies,
  with blast-radius highlighting.
- **Version history & diff** — `payments-service` spans five versions; the
  `v1.2.0 → v2.0.0` step is a breaking change that removes the `/charges` API.
- **Readiness** — `payments-service` 2.1.0 declares a readiness gate that fails:
  a required check (the LLM-safety eval suite) is `not-done`, dropping its score
  to 70, below the required 80 — while `orders-service` 1.2.0 passes.

The source and build harness live in
[`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).
