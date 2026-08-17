# Runnable demo (Docker Compose)

A complete Pacto fleet running on your machine: an OCI registry holding real
published contract revisions, an Evidence Server ingesting a signed envelope
from a "remote" environment and the dashboard showing the operational graph
they add up to.

There is no repository to clone, nothing to build and no file to download. The
demo is published as an OCI artifact that Docker Compose owns and runs directly:

```sh
docker compose -f oci://ghcr.io/trianalab/pacto/demo@sha256:<digest> \
  -p pacto-demo up -d --wait
```

Then open <http://localhost:8080/#/fleet>.

You need [Docker Compose](https://docs.docker.com/compose/) 2.34 or newer —
nothing else, not even the Pacto CLI. 2.34 is the release that added
`docker compose publish` and `-f oci://…`; older versions cannot run this
artifact at all, and the application says so itself in
`x-pacto-demo.minimum-compose-version`.

The demo's registry is public, so no login is needed. Against a private
registry, `docker login <registry>` first.

## The digest

The artifact is immutable and addressed by digest — that is what makes "the demo
you ran" a thing that can be named. Each release publishes its digest in the
release notes, and Docker resolves it from the tag for you:

```sh
docker manifest inspect -v ghcr.io/trianalab/pacto/demo:<version>
```

The `Descriptor.digest` it prints is the value to pass to `-f oci://…@`. Run the
demo by digest, not by tag: a tag is a publication convenience and can be moved,
and the whole point of this artifact is that it cannot.

The images inside are pinned the same way. Both are named by digest rather than
by tag, so the artifact you pinned runs the bytes it was released with, however
long ago that was — and because those digests are multi-platform indexes, Docker
still picks the build that matches your machine.

## The project name

`-p pacto-demo` above is not decoration. It names the containers, the network
and the volumes, and it is how you talk to the demo afterwards — no `-f`, no
file, no directory:

```sh
docker compose -p pacto-demo ps
docker compose -p pacto-demo logs seed
docker compose -p pacto-demo stop
docker compose -p pacto-demo start
docker compose -p pacto-demo down -v      # containers and volumes, all of it
```

Every version and every user picks their own project name, so two of anything
never collide.

## What you get

| | |
|---|---|
| `checkout` | two published revisions, one of them deployed |
| `orders` | declares a dependency on `checkout`, and is observed calling it |
| `payments` | never published to this fleet — it arrives as signed evidence from another environment |

Follow the graph from `orders` to `checkout`, open a revision to read its
contract, and compare `checkout` 1.0.0 with 1.1.0 to see a change analysed.

The fixture is the same canonical scenario Pacto's acceptance suite runs against
a real Kubernetes cluster: the same services, revisions, dependency edge,
observation source and signed evidence. Every fixture input travels inside the
application itself as a Compose `config`, so there is nothing on your disk for
the demo to read and nothing for you to keep. The one thing absent is the
operational targets a controller reconciles — Compose has no controller, so the
demo claims no running workload. Everything else is identical, and a parity test
keeps it that way.

## Ports

`8080` (dashboard), `8686` (Evidence Server) and `5051` (the demo's registry).
Override any of them with `PACTO_DEMO_DASHBOARD_PORT`,
`PACTO_DEMO_EVIDENCE_PORT` and `PACTO_DEMO_REGISTRY_PORT`, which is what a second
copy needs:

```sh
PACTO_DEMO_DASHBOARD_PORT=8081 PACTO_DEMO_EVIDENCE_PORT=8687 \
PACTO_DEMO_REGISTRY_PORT=5052 \
  docker compose -f oci://ghcr.io/trianalab/pacto/demo@sha256:<other-digest> \
    -p pacto-demo-next up -d --wait
```

Two digests, two project names, two sets of ports: both run at once, and
`down -v` on one leaves the other untouched. That is also how you move between
versions — start the new one beside the old, compare them, then remove the one
you do not want.

## Offline

Two different things get fetched, and only one of them can be skipped.

The **service images** are pulled once and then cached by Docker, so
`--pull never` runs the demo without reaching for them again:

```sh
docker compose -f oci://ghcr.io/trianalab/pacto/demo@sha256:<digest> \
  -p pacto-demo up -d --wait --pull never
```

The **application** is read from the registry by `-f oci://…` on every
invocation, cache or no cache — Compose has no offline mode for it. So creating
a project needs the artifact registry reachable, once. After the project exists,
operating it needs nothing: `stop`, `start`, `restart`, `logs` and `down -v`
address it by project name, and the demo itself never leaves your machine.

Acceptance proves the strong form of that: with the registry the artifact came
from stopped, both routes out of the demo's Compose network refused (host-local
and forwarded, independently) and the volumes emptied first, the fleet comes up
and serves the same facts. After the demo artifact and its digest-pinned images
have been pulled, the stack requires no external network access. Its private
Compose service network remains available because the dashboard, Evidence Server
and embedded registry must communicate with each other.

## Credentials

There are none in the artifact. The Evidence Server generates its own signing
keypair the first time it starts and keeps it in a volume, so the demo signs as
an identity that only ever existed on your machine.

## Where the instructions live

A Compose OCI application is exactly one compose file — there is no second layer
to carry a README, and Compose offers no way to hand you one. So this page is the
reference, and the application points at it: `x-pacto-demo.documentation` in the
file you just ran is the URL of this document. Nothing in the artifact pretends
to expose a file you cannot get out of it.

## The other demos

- [Live dashboard demo](dashboard-demo.md) — the same UI in your browser, with
  no Docker at all: the engine and a curated fleet compiled to WebAssembly.
- [Dashboard container](../dashboard-docker.md) — the dashboard against your own
  services rather than a fixture.
