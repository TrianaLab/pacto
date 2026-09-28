---
# See the note on the sibling "Try it" pages.
search:
  boost: 3
---

# Runnable demo (Docker Compose)

A complete Pacto fleet running on your machine. An OCI registry holds real
published contract revisions, an Evidence Server ingests a signed envelope
from a "remote" environment and the dashboard shows the operational graph
they add up to.

There is no repository to clone, nothing to build and no file to download. The
demo is published as an OCI artifact that Docker Compose owns and runs directly.

## Run it

You need [Docker Compose](https://docs.docker.com/compose/) 2.34 or newer —
nothing else, not even the Pacto CLI. That release added
`docker compose publish` and `-f oci://…`. Older versions cannot run this
artifact at all, and the application says so itself in
`x-pacto-demo.minimum-compose-version`. The demo's registry is public, so no
login is needed; against a private registry, `docker login <registry>` first.

Resolve the release you want to the digest it published, then run that digest:

```sh
DEMO=$(docker manifest inspect -v ghcr.io/trianalab/pacto/demo:3.3.3 \
  | sed -n 's/.*"digest": "\(sha256:[a-f0-9]*\)".*/\1/p' | head -1)

docker compose -f "oci://ghcr.io/trianalab/pacto/demo@$DEMO" \
  -p pacto-demo up -d --wait -y
```

Each release publishes the demo under one tag; what runs is the digest that tag
resolved to, never the tag itself. `-p pacto-demo` names the project, which is
how you operate the stack afterwards: `docker compose -p pacto-demo ps`, `logs`,
`stop`, `start` and `down -v` all address it by name.

`-y` answers the one question Compose asks before it runs a stack fetched from a
registry: it lists the variables the artifact declares (the three ports below)
and waits for a yes.

Then open <http://localhost:8080/#/fleet>.

## What you get

Three services. `checkout` has two published revisions — 1.1.0 drops an API path
1.0.0 exposed. `orders` declares `checkout` and is observed calling it, declares
`payments` and is never seen calling it. `payments` reaches the fleet as signed
evidence from a remote environment; `checkout` is observed calling it with no
contract declaring so.

The three edges are deliberately one of each: matched, declared but never
observed and observed but never declared. Follow the graph from `orders` to
`checkout`, open a revision to read its contract and compare `checkout` 1.0.0
with 1.1.0 to see a change analysed.

For the same moves at the command line — six user stories from "what is out
there" to "an agent reading the same server" — follow the
[guided tour](demo-tour.md).

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

The application itself is read from the registry by `-f oci://…` on every
invocation — Compose has no offline mode for it. So creating a project needs the
artifact registry reachable, once. After the project exists, operating it needs
nothing: `stop`, `start`, `restart`, `logs` and `down -v` address it by project
name, and the demo itself never leaves your machine. After the demo artifact and
its digest-pinned images have been pulled, the stack requires no external
network access. Its private Compose service network remains available because
the dashboard, Evidence Server and embedded registry must communicate with each
other.

Evidence is published as an OCI 1.1 referrer of the exact contract revision it
reports on, so the registry is the store and the Evidence Server keeps nothing.
The demo's registry is zot rather than CNCF distribution because the native
Referrers API is not optional here. `docker compose -p pacto-demo down -v`
removes the registry volume and the evidence with it.

## The other demos

- [Live dashboard demo](dashboard-demo.md) — the same UI in your browser, with
  no Docker at all: the engine and a curated fleet compiled to WebAssembly.
- [Dashboard container](../dashboard-docker.md) — the dashboard against your own
  services rather than a fixture.

Next: the [Quickstart](../quickstart.md) takes an empty directory to a published
contract in about five minutes.
