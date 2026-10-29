---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Runnable demo (Docker Compose)

This demo runs a small set of services and Pacto on your machine with Docker.
Use it to see Pacto work against real parts:

- a container registry holds published contracts;
- an [Evidence Server](../glossary.md#evidence-server) holds a signed report
  from a pretend remote environment;
- the dashboard shows all of it as one graph of who calls whom;
- `pacto agent` watches the containers' network connections and reports a
  service that says it is up but serves nothing.

It shows contracts and checks on running services. For the autonomy ladder,
see the [browser demo](dashboard-demo.md). For delivery on a real cluster, see
the [Kubernetes demo](kubernetes-demo.md).

The demo is published as an OCI artifact that Docker Compose runs directly from
the registry.

## Run it

You need only [Docker Compose](https://docs.docker.com/compose/) 2.34 or
newer. That release added `docker compose publish` and `-f oci://…`. Older
versions cannot run this artifact, and the application says so itself in
`x-pacto-demo.minimum-compose-version`. The demo's registry is public, so no
login is needed; against a private registry, `docker login <registry>` first.

Resolve the release you want to the digest it published, then run that digest:

```sh
DEMO=$(docker manifest inspect -v ghcr.io/trianalab/pacto/demo:4.1.0 \
  | sed -n 's/.*"digest": "\(sha256:[a-f0-9]*\)".*/\1/p' | head -1)

docker compose -f "oci://ghcr.io/trianalab/pacto/demo@$DEMO" \
  -p pacto-demo up -d --wait -y
```

Each release publishes the demo under one tag, and you run the digest that tag
resolved to. `-p pacto-demo` names the project, which is
how you operate the stack afterwards: `docker compose -p pacto-demo ps`, `logs`,
`stop`, `start` and `down -v` all address it by name.

`-y` answers the one question Compose asks before it runs a stack fetched from a
registry: it lists the variables the artifact declares (the three ports below)
and waits for a yes.

Then open <http://localhost:8080/#/fleet>.

## What you get

The demo has four services. `checkout` has two published revisions, and 1.1.0
drops an API path 1.0.0 exposed. `orders` declares `checkout` and is observed calling it, declares
`payments` and is never seen calling it. `payments` reaches the fleet as signed
evidence from a remote environment; `checkout` is observed calling it with no
contract declaring so.

The three edges show one of each on purpose: matched, declared but never
observed and observed but never declared. The observed calls come from a
recorded trace file. Follow the graph from `orders` to
`checkout`, open a revision to read its contract and compare `checkout` 1.0.0
with 1.1.0 to see a change analysed.

### The cold start, step by step

Every Pacto rollout starts the same way. Every service in this demo already
has a contract, so it shows the moves on a contracted fleet:

1. **Watch what runs.** Pacto reads the contracts it has and what it sees
   running, and changes nothing.
2. **Find what no contract declares.** `checkout` calls `payments`, but
   `checkout`'s contract does not say so. The graph marks that call as observed
   but not declared.
3. **Pactify a service.** `pacto init` writes a first contract from what a
   service already has. The demo shows it on a plain copy of `checkout`, since
   every running service has a contract (see
   [Try the CLI in the demo](#try-the-cli-in-the-demo)).

The second half is delivery, where Pacto ships changes and a person confirms
what it may do on its own. Delivery runs on Kubernetes only; the
[Kubernetes demo](kubernetes-demo.md) shows it.

For the same moves at the command line, follow the
[guided tour](demo-tour.md). Its six stories go from listing a fleet to an
agent reading the same server.

## Try the CLI in the demo

The dashboard container has the Pacto CLI, so these commands run inside it.

Ask which version `checkout` 1.1.0 should have been. It drops a path 1.0.0
served, so Pacto answers `Required bump: major` (and exits non-zero, as a CI
step would):

```sh
docker compose -p pacto-demo exec dashboard pacto diff \
  oci://registry:5000/demo/checkout:1.0.0 oci://registry:5000/demo/checkout:1.1.0
```

Pactify a plain `checkout` that has only a Dockerfile. `--plan` shows the
contract `pacto init` would write and changes nothing:

```sh
docker compose -p pacto-demo exec dashboard sh -c \
  'mkdir -p /tmp/checkout && printf "FROM nginx:1.27\nEXPOSE 8080\n" >/tmp/checkout/Dockerfile &&
   pacto init --plan --owner commerce /tmp/checkout'
```

## The live runtime check

`inventory` is the live part. Its container is running and labelled with its
contract and the port its API is bound to (`dev.pacto.contract`,
`dev.pacto.bind.api=8080`), but nothing in it listens there. The `agent` service
runs [`pacto agent`](../runtime-agent.md) with `--sink evidence --docker`: it
reads those labels, watches every container's TCP sockets with eBPF, and sends
what it sees, signed with its own key, to the Evidence Server. About half a
minute after start, `inventory` shows `INTERFACE_ABSENT` on `api`, seen by
`pacto-agent+ebpf` (the kernel). Docker reports only that the container is
up, so the kernel view is what finds this.

The agent needs more access than the rest of the demo: a privileged container in
the host's PID and cgroup namespaces, running as root, with the Docker socket
and `/sys/fs/cgroup` mounted. Those two paths are the only host paths the demo
mounts. On Linux that means kernel 5.8 or later with BTF and cgroup v2. On
Docker Desktop (macOS and Windows) the "host" is Docker's own Linux VM. On macOS
its current kernel ships BTF and cgroup v2. Where the kernel cannot load the
program, the agent says so in `docker compose -p pacto-demo logs agent` and
`inventory` stays without that finding; everything else works unchanged.

## Ports

`8080` (dashboard), `8686` (Evidence Server) and `5051` (the demo's registry).
Override with `PACTO_DEMO_DASHBOARD_PORT`, `PACTO_DEMO_EVIDENCE_PORT` and
`PACTO_DEMO_REGISTRY_PORT`:

```sh
PACTO_DEMO_DASHBOARD_PORT=8081 PACTO_DEMO_EVIDENCE_PORT=8687 \
PACTO_DEMO_REGISTRY_PORT=5052 \
  docker compose -f "oci://ghcr.io/trianalab/pacto/demo@$DEMO" \
    -p pacto-demo-next up -d --wait -y
```

Two project names, two sets of ports: both run at once, and `down -v` on one
leaves the other untouched.

## Offline

The service images are pulled once and cached by Docker, so `--pull never` runs
the demo without reaching for them again:

```sh
docker compose -f "oci://ghcr.io/trianalab/pacto/demo@$DEMO" \
  -p pacto-demo up -d --wait -y --pull never
```

Creating the project reads the artifact from the registry, once. After that
`stop`, `start`, `restart`, `logs` and `down -v` work by project name with no
network beyond the stack's own.

Evidence is published as an OCI 1.1 referrer of the exact contract revision it
reports on, so the registry is the store and the Evidence Server keeps nothing.
The demo's registry is zot because the demo needs the native Referrers
API. `docker compose -p pacto-demo down -v`
removes the registry volume and the evidence with it.

## The other demos

- [Live dashboard demo](dashboard-demo.md): the same UI in your browser,
  without Docker. The engine and a curated fleet are compiled to WebAssembly.
- [Dashboard container](../dashboard-docker.md): the dashboard against your own
  services.
- [Kubernetes demo](kubernetes-demo.md): delivery: promotions, approvals,
  rollbacks and signed records. This demo has no cluster and no sign-in, so its
  dashboard shows no Delivery tab.

Next: the [Quickstart](../quickstart.md) takes an empty directory to a validated
contract and a caught breaking change in about five minutes.
