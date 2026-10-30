---
# The three "Try it" pages are the fastest answer to "what is this?", so they
# outrank pages that merely mention a demo in passing.
search:
  boost: 3
---

# Live dashboard demo

This page runs the Pacto dashboard, Pacto's web page, in your browser. Pacto
and a set of demo contracts are compiled to WebAssembly, a format browsers run
directly. The demo needs no server, and all its data stays in the tab.

<a href="../../demo/#/fleet" class="md-button md-button--primary">Open the live dashboard demo →</a>

The first visit downloads about 4.7 MB (19.3 MB unpacked). On a slow
connection give it a moment: the loading strip counts the seconds, and the
browser keeps a copy afterwards.

## What to look at

- **Fleet overview**: thirty-eight made-up services and who calls whom: the
  thirty-six of the [CLI tour](demo-tour.md) plus two from a partner registry,
  a second domain. The page opens on its answer to **Is anything wrong?**
  and says some of the information is missing. Posture, ownership and data
  sources stay open below it. Pacto always reports what it could not see.
- **One service's dependency graph**: `payments-service` has six published
  versions. The graph shows which services call it and what a change would
  reach.
- **Change analysis**: the `v1.2.1 → v2.0.1` step removes the `/charges` API
  and shows which [consumers](../glossary.md#consumer) the break reaches.
- **Promises, schedules and environments**: `inventory-service` retires one
  interface past its sunset and one before it, and each consumer shows whether
  it gets the guarantees it requires: met, refused or not verified. Change
  analysis from 2.1.0 to 3.0.0 lists the consumers it refuses, and the
  operational graph marks the refused edges. `storefront-web` 1.2.0 checks three
  environments: one passes, one fails at a line of its values file and one is
  not verified. The guided tour stops on `inventory-service` before
  it hands off to the CLI tour.
- **Delivery, simulated**: press **Play the delivery story**. First
  `pacto init` gives a plain service a contract. Then a safe change ships on
  its own, a bad release is rolled back, a breaking change asks only the
  teams it would break, and every step reads back as a signed record. Last,
  the tour opens the **Autonomy** tab and an eval check signed by the runner
  that ran it (see below). **Switch person** lets you act as an AI agent
  (`pay-agent`), Alice or Carol, to see who may decide what.

The demo includes two oddities on purpose. `platform-app-config` appears
twice because two different services share that name, and Pacto tells services
apart by more than their name. One
history shows a gap and one record is signed by a key nobody trusts, so you
can see how Pacto reports them.

## What the delivery story shows

The delivery screens play the payments story, the scenario every demo shares.
Before the tour starts, the tab has already played its first steps, so the
screens show:

- **A new service that only watches.** `pacto init` wrote fraud's contract,
  and its Stages start on observe, the lowest level of the
  [autonomy ladder](../autonomy.md). GitOps shipped fraud 1.0.0 to test.
  Pacto moved nothing and noted it would have shipped it with nobody
  involved. That one verified version earns a proposal to move test up to
  advise: the demo sets the Stage to propose after one version, where the
  default is 10. **Propose** opens the raise, and only a person on team risk
  may answer it. Nobody in the demo is on team risk, so it stays a proposal.
- **A step down after a failure.** payments 2.4.0 shipped to prod with nobody
  involved and failed its health check, so Pacto rolled it back and stepped
  the Stage down one level, to auto for non-breaking changes. Lowering a level
  needs no approval. payments in test stepped down the same way when 2.4.2
  failed.
- **A version that names no prod environment.** payments 2.4.1 is the first
  version to declare [environments](../contract-reference/environments.md#environments-and-stages),
  and it declares only test. It shipped to test, but its promote to prod
  waited for team payments, and Alice rejected it. 2.4.2 declares every
  environment, and its values pass the same check.
- **A caller with no contract.** ledger-export runs in test with no contract
  and calls payments. So the breaking payments 3.0.0 waited in test until
  Alice accepted that unknown, and the Autonomy tab lists the missing
  contract as one thing that keeps payments in test from autonomous.
- **An agent with a signed eval.** pay-agent, the AI agent, is a service like
  any other: its contract ships its eval suite as a check. Its test Stage ran
  the suite through a runner that signs its count. 1.0.0 passed 2 of 2.
  1.1.0 passed 1 of 2, so it rolled back and test stepped down. The
  **Checks** on the change page say who ran the suite and signed the result.

## Limitations

The checks on running services and the delivery screens use recorded data, and every delivery screen says "simulated". The
plans, checks and history do come from Pacto's own code. Records are signed
with a key made in your tab, so nothing outside it can check them, and a
reload starts over. The eval runner is a stand-in in the tab whose key anyone
can derive, so its signature proves nothing outside the demo. On the
Autonomy tab, the share of services with a contract counts only the fleet
demo's services.

To run Pacto for real, the [Docker Compose demo](compose-demo.md) runs the same
dashboard on your machine and the [Kubernetes demo](kubernetes-demo.md) runs
delivery on a local cluster. The [guided tour](demo-tour.md) shows the same
facts from the command line, and the [Quickstart](../quickstart.md) has you
write a contract of your own. To run the dashboard against your own services,
see [Dashboard container](../dashboard-docker.md).
