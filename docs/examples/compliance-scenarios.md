# Compliance scenarios: where each is proven

Pacto's compliance model has four runtime states (Compliant, NonCompliant,
Unknown, Invalid) plus the informational Warning, NotEvaluated and Reference
outcomes, evaluated per dimension (workload, persistence, availability,
interface, dependency, configuration, metrics, secrets) with stabilization
windows and recovery paths. Those behaviors are proven at three levels, each
with a different cost and fidelity. This page maps each scenario to the level
that proves it, so nothing is claimed where it is not actually exercised.

## The three proof surfaces

| Surface | What runs | Fidelity | Where |
|---|---|---|---|
| Offline WASM demo | Engine + static contracts in the browser | Contract structure, graph, diff, readiness — **no runtime states** | [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo) |
| kind acceptance | Packaged chart + real operator image on a real cluster | One live reconcile transition, real RBAC, chart lifecycle | [`tests/acceptance/kind/reconcile.sh`](https://github.com/TrianaLab/pacto/tree/main/tests/acceptance/kind/reconcile.sh) |
| Operator envtest | Controller against a real API server (no kubelet) | **Full per-dimension state matrix** | `make -C integrations/kubernetes test-e2e` |

## Scenario-to-proof map

| Scenario / dimension | Offline demo | kind | envtest |
|---|:---:|:---:|:---:|
| Contract graph, dependents, blast radius | ✅ | — | — |
| Version history + breaking-change diff | ✅ | — | — |
| Readiness scoring + gate | ✅ | — | — |
| Compliant (workload observed, matches) | — | ✅ | ✅ |
| Unknown (evidence missing / insufficient / collection failed) | — | ✅ | ✅ |
| Recovery Unknown → Compliant | — | ✅ | ✅ |
| NonCompliant (confirmed contradiction: workload / persistence / interface / dependency) | — | — | ✅ |
| Invalid (undeclared/malformed contract) | — | — | ✅ |
| Warning (optional non-conformance) | — | — | ✅ |
| NotEvaluated / Reference outcomes | — | — | ✅ |
| Stabilization window + beyond-window confirmation | — | — | ✅ |
| Configuration absent / mismatch | — | — | ✅ |
| Metrics verified / ServiceMonitor discovery | — | — | ✅ |
| Secret existence | — | — | ✅ |
| Chart install / upgrade / uninstall, real image, RBAC | — | ✅ | — |

- **Offline demo** deliberately proves none of the runtime states: there is no
  cluster observing workloads, so a runtime status would be fiction. See the
  demo's own [scope note](https://github.com/TrianaLab/pacto/tree/main/examples/demo#scope-what-this-demo-is-and-is-not).
- **kind** proves exactly the Compliant → Unknown → Compliant journey with the
  packaged artifacts (`release/proofs/kind-e2e.txt`) — it does not sweep the full
  matrix; that is the envtest suite's job.
- **envtest** is the exhaustive gate: the acceptance suite (91 subtests, per
  `release/proofs/kind-e2e.txt`) sweeps every dimension and state above.
