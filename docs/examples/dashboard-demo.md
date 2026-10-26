---
# The three "Try it" pages are the fastest answer to "what is this?", so they
# outrank pages that merely mention a demo in passing.
search:
  boost: 3
---

# Live dashboard demo

**Try Pacto without installing anything.** The Pacto dashboard, its web page,
runs entirely in your browser here: Pacto and a set of demo contracts are
compiled to WebAssembly, a format browsers run directly. There is no server
behind it and nothing leaves the tab.

<a href="../../demo/#/fleet" class="md-button md-button--primary">Open the live dashboard demo →</a>

The first visit downloads about 4.3 MB (17.5 MB unpacked). On a slow
connection give it a moment: the loading strip counts the seconds, and the
browser keeps a copy afterwards.

## What to look at

- **Fleet overview**: eighteen made-up services and who calls whom. A banner
  at the top says some of the information is missing. That is on purpose:
  Pacto always tells you what it could not see instead of hiding it.
- **One service's dependency graph**: `payments-service` has six published
  versions. The graph shows which services call it and what a change would
  reach.
- **Change analysis**: the `v1.2.1 → v2.0.1` step removes the `/charges` API
  and shows which consumers, the services that call it, the break reaches.
- **Delivery, simulated**: press **Play the delivery story**. First
  `pacto init` gives a plain service a contract. Then a safe change ships on
  its own, a bad release is rolled back, a breaking change asks only the
  teams it would break, and every step reads back as a signed record.
  **Switch person** lets you act as an AI agent (`pay-agent`), Alice or
  Carol, to see who may decide what.

Two oddities are deliberate. `platform-app-config` appears twice because two
different services share that name, and a name is not an identity. One
history shows a gap and one record is signed by a key nobody trusts, so you
can see how Pacto reports them.

## What it cannot show

Nothing here runs for real. The checks on running services and the delivery
screens use recorded data, and every delivery screen says "simulated". The
plans, checks and history do come from Pacto's own code. Records are signed
with a key made in your tab, so nothing outside it can check them, and a
reload starts over.

For the real thing, the [Docker Compose demo](compose-demo.md) runs the same
dashboard on your machine and the [Kubernetes demo](kubernetes-demo.md) runs
delivery on a local cluster. The [guided tour](demo-tour.md) shows the same
facts from the command line, and the [Quickstart](../quickstart.md) has you
write a contract of your own. To run the dashboard against your own services,
see [Dashboard container](../dashboard-docker.md).
