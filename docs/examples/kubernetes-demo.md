---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Try it on Kubernetes

This demo runs Pacto delivery on a real Kubernetes cluster on your machine
and follows one service, payments, through its releases. Use it to see every
part working before you try your own service. The parts
are real: the registry, Flux, delivery (which picks the version), the
recorder (which signs a record of every step), a health check, the node
agent (which sees calls between services), sign-in and the dashboard. The
services themselves, their traffic and the people signing in are made up
(see [What is generated](#what-is-generated)). Its Stages start on fixed
levels of the autonomy ladder: payments on autonomous in test and prod, the
other services on autonomous in test and advise in prod, and fraud on
observe. To see a Stage earn a proposal to move up a level, use the
[browser demo](dashboard-demo.md).

## Run it

You need Docker with a builder that can export OCI images
(`docker buildx create --use --driver docker-container`),
[kind](https://kind.sigs.k8s.io/), kubectl, Helm, Go, Node,
[oras](https://oras.land/), cosign, jq, openssl and GNU `timeout` (on macOS,
`brew install coreutils`), and a clone of the Pacto repository. It builds the
images from the clone. Host ports 3300, 8585, 5556, 8443 and 5602 must
be free.

```sh
make e2e-delivery-demo-up      # the cluster and everything on it
make e2e-delivery-demo-story   # play the story, checking every step
```

`up` takes about 20 minutes, most of it building the images; `story` about 8. `story` stops with payments 3.0.0
held for prod and prints what to do next. When you are done:

```sh
make e2e-delivery-demo-down
```

## What you see

The story starts with one service that has no contract yet. The guided path is five moments,
in the order the story plays them:

- **Pactify a service.** `pacto init --delivery` reads fraud's Dockerfile and
  manifests and writes its contract, its Pacto resource and its Stages.
  `--no-transparency-log` keeps the demo's throwaway key out of the public
  transparency log, and `--insecure-registry` lets Flux read its plain HTTP
  registry. The demo uses the files as init wrote them. Both of fraud's
  Stages start on observe: Flux applies the tag, running the image
  `pacto publish` built, pinned by digest, and Pacto only notes that it would
  have shipped 1.0.0 to test with nobody involved.
- **A safe change ships on its own.** payments 2.4.0 adds an endpoint and
  reaches prod with nobody involved, because checkout and orders accept any
  payments 2.x.
- **A bad release is caught and rolled back.** The prod error ratio rises after
  2.4.0, so the health check fails, cites its query and payments rolls back.
  2.4.2 calls fraud, which its contract never names, so it fails in test and
  never reaches prod.
- **A breaking change asks only the affected teams.** Publishing a removed
  field as 2.4.3 is refused: it needs a major version. 3.0.0 is held for prod,
  and only checkout and orders are asked. checkout 1.9 can ship first.
- **Every step is a signed record.** History reads each step back from signed
  records, and `pacto delivery record verify` checks them.

Each step is checked against `examples/story`, the scenario every demo shares,
and the demo stops with an error if the cluster does something else.

## Explore more

Open <http://127.0.0.1:3300> and sign in through Dex. Every password is
`password`.

- `carol@example.com` is on team orders, the team asked about payments 3.0.0.
- `alice@example.com` is on team payments and is never asked.
- `pay-agent@example.com` is an agent: it can look and propose. Only people approve.

`make e2e-delivery-demo-browser` plays the rest in a browser, once per
cluster, and checks it against `examples/story`:

- pay-agent ships checkout 1.9 first, and the held payments 3.0.0 then asks
  only orders.
- carol signs in again and approves.
- 3.0.0 sends too little telemetry, so its check says Unknown until carol
  accepts that. She then rolls 3.0.0 back, and automation holds off.
- The newest signed record of payments in prod is deleted from the registry,
  and the Stage freezes. A changed copy of a record would be ignored, since
  anyone who can push could attach one.
- pay-agent cannot unfreeze the Stage. alice does, once the record is back.
- ops pins a version by hand with break-glass rights. That shows as a
  witnessed record and freezes the Stage again.
- pay-agent ships as a service of its own, with its eval suite as the check:
  1.0.0 passes, and 1.1.0 swaps its model, passes 1 of its 2 evals and rolls
  back in test. The runner signs each count with its own key, the rollback
  cites that signed count, and `pacto delivery record verify` checks the
  runner's signature beside the recorder's.

`story` also prints the commands for `pacto delivery blame` and `undo` and for
the agent over remote MCP.

## Another cluster and Argo CD

`make e2e-delivery-demo-reach` adds a second cluster, prod-us. Its payments
Stage follows prod in the first cluster through prod's signed records, and
Argo CD applies what it promotes. Each controller works on its own:
prod-us reads prod's signed records from the registry. In the demo that
registry runs in the first cluster and prod-us reaches it through a NodePort.
It takes the newest version prod verified and refuses 2.4.0 because it failed
after release there, so run `story` before `reach`. Then prod-eu, a third
payments Stage in the first cluster, takes 2.4.0, which fails after release and
rolls back to 2.3.0; 2.3.0 then fails its re-check too. A Stage is rolled back
only once, so prod-eu freezes, and the freeze is a signed record naming 2.3.0.
A Stage in prod-us that follows prod-eu's records never takes 2.3.0, and alice
unfreezes prod-eu. See
[Argo CD and several clusters](../integrations/kubernetes/delivery-reach.md).

## What is generated

The payments workload is a stand-in (podinfo). Its telemetry comes from a span
generator in the cluster, and the call 2.4.2 makes to fraud comes from a sidecar
in that version. pay-agent's eval runner is a stand-in that answers fixed
counts and signs them with a key the demo makes. Sign-in uses the demo's own Dex and Keycloak. When the record is deleted and
restored, alice and ops act through admin credentials that impersonate them,
with a sign-in time the script makes up.

To see the operational graph on its own, run `make e2e-operational-graph-up`.
