---
# The three "Try it" pages are the fastest answer to "what is this?", so they
# outrank pages that merely mention a demo in passing.
search:
  boost: 3
---

# Live dashboard demo

**Try Pacto without installing anything.** The dashboard runs entirely in your
browser: the engine and a set of demo contracts are compiled to WebAssembly (a
format browsers run directly). No backend, no live registry, nothing leaves the
tab.

<a href="../../demo/#/fleet" class="md-button md-button--primary">Open the live dashboard demo →</a>

The whole engine ships to your browser, so the first visit downloads about
4 MB of compiled Wasm (17 MB unpacked). Give it a moment on a slow connection —
the loading strip counts the seconds and names the size, and the browser caches
it afterwards.

Two things about the fixture are deliberate. **It opens on a degraded-source
banner** — one source unavailable, one partial, because a view that only shows
complete knowledge teaches you nothing about the day it isn't.
**`platform-app-config` appears twice** — two different services from two
sources that share a name. A name is not an identity, so the graph keeps them
apart.

## What to look at

- **Fleet overview** — eighteen services spanning edge, domain, infra, platform
  and external tiers. Open it on the degraded sources and the duplicate name
  described above.
- **One service's dependency graph** — `payments-service` spans six published
  revisions. Resolves from each contract's declared dependencies and highlights
  what a change would reach.
- **Data sources panel** — shows one source degraded and one unavailable. The
  operational graph reports what it knows and what it cannot know, and the day
  everything reports complete is not every day.
- **Change analysis** — the `v1.2.1 → v2.0.1` step removes the `/charges` API
  and shows which consumers the break reaches.
- **Delivery, simulated** — press **Play the delivery story** for the payments
  story in two acts. First `pacto init` pactifies a plain service. Then a safe
  change ships on its own, a bad release is rolled back, a breaking change asks
  only the teams whose declared range it breaks and every step reads back as a
  signed record. **Switch person** acts as `pay-agent`, Alice or Carol, so you
  can see who may decide and the fresh sign-in a decision needs.

## What it cannot show

There is no cluster behind the demo. The compliance states on its targets are
recorded fixture data. It shows
[runtime conformance](../integrations/kubernetes/runtime-conformance.md) (what a
service really does on the network) as one recorded finding, not a live check:
the staging `orders-service` target carries `INTERFACE_ABSENT`, and its evidence
line says "seen by: Kubernetes + kernel". A live check needs the node agent on a
Kubernetes cluster or the [Docker Compose demo](compose-demo.md).

Delivery is simulated, as the strip says on every delivery screen. Plans,
checks, history, blame and undo come from Pacto's own code on fixed data. The
health check results are recorded and the `pacto init` plan is recorded output
that a test keeps current. Records are signed with a key made in your tab, so
nothing outside it can check them, and a reload starts over. With no promoter,
Flux or real sign-in, nothing it approves runs anywhere. One
history shows a gap, one record signed by a key nobody trusts and one Stage
read from status only: these look like bugs and are deliberate. See
[delivery](../integrations/kubernetes/delivery.md) for the real thing on a cluster.

To run the dashboard against your own services, see
[Dashboard container](../dashboard-docker.md). The source and build harness live
in [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).

Next: the [Docker Compose demo](compose-demo.md) runs the same UI against a real
registry and Evidence Server on your machine. The
[guided tour](demo-tour.md) reaches the same facts from the command line in
six offline user stories. Or go straight to the
[Quickstart](../quickstart.md) and write a contract of your own.
