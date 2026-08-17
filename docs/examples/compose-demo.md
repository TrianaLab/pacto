# Runnable demo (Docker Compose)

A complete Pacto fleet running on your machine: an OCI registry holding real
published contract revisions, an Evidence Server ingesting a signed envelope
from a "remote" environment and the dashboard showing the operational graph
they add up to.

There is no repository to clone and nothing to build. The demo is distributed
as an immutable OCI artifact, so pulling it is the whole install:

```sh
mkdir pacto-demo && cd pacto-demo
oras pull ghcr.io/trianalab/pacto/demo:<version>
docker compose up -d --wait
```

Then open <http://localhost:8080/#/fleet>.

You need [Docker Compose](https://docs.docker.com/compose/) and
[ORAS](https://oras.land/docs/installation) — nothing else, not even the Pacto
CLI. The artifact carries its own `README.md` with the full reference: ports,
restart, cleanup, running offline and moving between versions.

The containers run as a non-root user and read the artifact from the directory
you pulled it into, so that directory has to be readable by them. A default
`umask` gives you that; if yours is restrictive and the `seed` service exits
immediately, `chmod o+rx .` in the run directory and start it again.

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
observation source and signed evidence. The one thing absent is the operational
targets a controller reconciles — Compose has no controller, so the demo claims
no running workload. Everything else is identical, and a parity test keeps it
that way.

## Pinning a version

Tags are a convenience; the digest is the identity. To pin the exact artifact
you ran, record the digest `oras pull` reports and use it:

```sh
oras pull ghcr.io/trianalab/pacto/demo@sha256:<digest>
```

Each version is independent. Pull it into its own directory and Compose gives it
its own containers and volumes, so you can keep the old one running, go back to
it, or remove it with `docker compose down -v` when you are done.

The images inside are pinned the same way. Both are named by digest rather than
by tag, so the artifact you pinned runs the bytes it was released with, however
long ago that was — and because those digests are multi-platform indexes, Docker
still picks the build that matches your machine.

## Offline

Once the artifact and those two images are on your machine, the demo needs no
external network:

```sh
docker compose up -d --wait --pull never
```

Acceptance proves that with the registry the artifact came from stopped and every
route out of the demo's Compose network refused: it starts from empty volumes and
serves the same fleet. The private network the registry, the Evidence Server and
the dashboard use to reach each other stays up, because they have to, and so do
the ports published to your machine.

## Credentials

There are none in the artifact. The Evidence Server generates its own signing
keypair the first time it starts and keeps it in a volume, so the demo signs as
an identity that only ever existed on your machine.

## The other demos

- [Live dashboard demo](dashboard-demo.md) — the same UI in your browser, with
  no Docker at all: the engine and a curated fleet compiled to WebAssembly.
- [Dashboard container](../dashboard-docker.md) — the dashboard against your own
  services rather than a fixture.
