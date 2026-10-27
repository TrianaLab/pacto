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

The first visit downloads about 4.6 MB (18.6 MB unpacked). On a slow
connection give it a moment: the loading strip counts the seconds, and the
browser keeps a copy afterwards.

## What to look at

- **Fleet overview**: eighteen made-up services and who calls whom: the
  sixteen of the [CLI tour](demo-tour.md) plus two from a partner registry,
  a second domain. A banner
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
  teams it would break, and every step reads back as a signed record. Last,
  the tour opens the **Autonomy** tab and an eval check signed by the runner
  that ran it (see below). **Switch person** lets you act as an AI agent
  (`pay-agent`), Alice or Carol, to see who may decide what.

Two oddities are deliberate. `platform-app-config` appears twice because two
different services share that name, and a name is not an identity. One
history shows a gap and one record is signed by a key nobody trusts, so you
can see how Pacto reports them.

## What the delivery story shows

The delivery screens play the payments story, the scenario every demo shares.
Before the tour starts, the tab has already played its first steps, so the
screens show:

- **A new service that only watches.** `pacto init` wrote fraud's contract,
  and its Stages start on observe, the lowest rung of the
  [autonomy ladder](../autonomy.md). GitOps shipped fraud 1.0.0 to test.
  Pacto moved nothing and noted it would have shipped it with nobody
  involved. That one verified version earns a proposal to move test up to
  advise: the demo sets the Stage to propose after one version, where the
  default is 10. **Propose** opens the raise, and only a person on team risk
  may answer it. Nobody in the demo is on team risk, so it stays a proposal.
- **A step down after a failure.** payments 2.4.0 shipped to prod with nobody
  involved and failed its health check, so Pacto rolled it back and stepped
  the Stage down one rung, to auto for non-breaking changes. Lowering a rung
  needs no approval. payments in test stepped down the same way when 2.4.2
  failed.
- **A caller with no contract.** ledger-export runs in test with no contract
  and calls payments. So the breaking payments 3.0.0 waited in test until
  Alice accepted that unknown, and the Autonomy tab lists the missing
  contract as one thing that keeps payments in test from autonomous.
- **An agent with a signed eval.** pay-agent, the AI agent, is a service like
  any other: its contract ships its eval suite as a check. Its test Stage ran
  the suite through a runner that signs its count. 1.0.0 passed 2 of 2.
  1.1.0 passed 1 of 2, so it rolled back and test stepped down. The change
  page says who ran the suite and signed the result.

## What it cannot show

Nothing here runs for real. The checks on running services and the delivery
screens use recorded data, and every delivery screen says "simulated". The
plans, checks and history do come from Pacto's own code. Records are signed
with a key made in your tab, so nothing outside it can check them, and a
reload starts over. The eval runner is a stand-in in the tab whose key anyone
can derive, so its signature proves nothing outside the demo. On the
Autonomy tab, the share of services with a contract counts the fleet demo's
services, not the ones in the delivery story.

For the real thing, the [Docker Compose demo](compose-demo.md) runs the same
dashboard on your machine and the [Kubernetes demo](kubernetes-demo.md) runs
delivery on a local cluster. The [guided tour](demo-tour.md) shows the same
facts from the command line, and the [Quickstart](../quickstart.md) has you
write a contract of your own. To run the dashboard against your own services,
see [Dashboard container](../dashboard-docker.md).
