---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Try it on Kubernetes

The payments story on a real cluster on your machine. Here everything is real:
the registry, Flux, the delivery promoter, the recorder that signs every step,
the health check, the node agent that sees calls, sign-in and the dashboard. The
browser demo shows the same story over fixed data.

## Run it

You need Docker, [kind](https://kind.sigs.k8s.io/), kubectl, helm, Go, Node,
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

It starts with one service not yet pactified. The guided path is five moments,
in the order the story plays them:

- **Pactify a service.** `pacto init --delivery` reads fraud's Dockerfile and
  manifests and writes its contract, its Pacto resource and its Stages. The
  demo patches two things init wrote: offline signing and the plain-HTTP
  registry. fraud ships to test.
- **A safe change ships on its own.** payments 2.4.0 adds an endpoint and
  reaches prod with nobody involved, because checkout and orders accept
  payments ^2.
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
and the demo stops loudly if the cluster does something else.

## Explore more

Open <http://127.0.0.1:3300> and sign in through Dex. Every password is
`password`.

- `carol@example.com` is on team orders, the team asked about payments 3.0.0.
- `alice@example.com` is on team payments and is never asked.
- `pay-agent@example.com` is an agent: it may look and propose, never approve.

`make e2e-delivery-demo-browser` plays and checks B6 to B9 (B7 to B9 in
Chromium) against `examples/story`, once per cluster: the agent ships checkout 1.9 first
and the held change then asks only orders; carol approves after signing in
again; 3.0.0 sends too little telemetry, so its check says Unknown until carol
accepts that; she rolls 3.0.0 back and automation holds off. The tampered
record of beat B10 is not played here. `story` also prints the commands for
`pacto delivery blame` and `undo` and for the agent over remote MCP.

## What is generated

The payments workload is a stand-in (podinfo). Its telemetry comes from a span
generator in the cluster, and the call 2.4.2 makes to fraud comes from a sidecar
in that version. Sign-in uses the demo's own Dex and Keycloak, not a company
login. For the operational graph on its own, see `make e2e-operational-graph-up`.
