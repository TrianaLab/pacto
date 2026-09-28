---
# The three "Try it" pages are the fastest answer to "what is this?", so they
# outrank pages that merely mention a demo in passing.
search:
  boost: 3
---

# Live dashboard demo

**Try Pacto without installing anything.** The dashboard runs entirely in your
browser, with the engine and a curated set of demo contracts compiled to
WebAssembly. No backend, no live registry, nothing leaves the tab.

<a href="../../demo/#/fleet" class="md-button md-button--primary">Open the live dashboard demo →</a>

The whole engine ships to your browser, so the first visit downloads about
11 MB of compiled Wasm (57 MB unpacked). Give it a moment on a slow connection —
the loading strip counts the seconds and names the size, and the browser caches
it afterwards.

Two things about the fixture are deliberate. **It opens on a degraded-source
banner** — one source unavailable, one partial, because a view that only shows
complete knowledge teaches you nothing about the day it isn't.
**`platform-app-config` appears twice** — two different services from two
sources that share a name. A name is not an identity, so the graph keeps them
apart.

The demo deliberately shows no runtime states. There is no cluster observing
workloads, so a runtime status would be fiction.

Look at:

- **Fleet overview** — sixteen services spanning edge, domain, infra, platform
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

To run the dashboard against your own services, see
[Dashboard container](../dashboard-docker.md). The source and build harness live
in [`examples/demo`](https://github.com/TrianaLab/pacto/tree/main/examples/demo).

Next: the [Docker Compose demo](compose-demo.md) runs the same UI against a real
registry and Evidence Server on your machine. The
[guided tour](demo-tour.md) reaches the same facts from the command line in
six offline user stories. Or go straight to the
[Quickstart](../quickstart.md) and publish a contract of your own.
