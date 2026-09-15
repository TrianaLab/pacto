---
# Contributor-internal. It is thorough, so it keeps outranking the reader
# pages on general queries; halve it rather than thin the page.
search:
  boost: 0.5
---

# The Compose demo's three claims

`tests/acceptance/local/compose-demo.sh` proves the demo the way a stranger
meets it. Three of its claims are stated here as narrowly as they are tested.

## Docker Compose owns this artifact, end to end

`docker compose publish` writes the demo and
`docker compose -f oci://…@sha256:… -p <name> up` runs it, with no generic OCI
tool on either path — neither the harness nor CI installs ORAS. The published
application is one layer holding the projected compose file verbatim, so
`sha256` of what the projector emitted is the artifact's content identity; the
acceptance asserts exactly that equality against the manifest it reads back.
There is no run directory, no local
`compose.yaml` on the execution path and no bind mount in the published model:
every immutable fixture input — the plan, the seed script, the bundle documents,
the observation fixture — travels inside the application as a Compose `config`
with inline `content`, and only the mutable runtime state (the embedded
registry, the Evidence Server's keys) lives in a named volume. Project identity
is the user's: an explicit `-p` per copy, never a top-level `name:` that would
make two versions collide.

### Publishing it in the release

That ownership reaches the release. `demo-compose` publishes through the same
`publish-oci-unit.sh` adapter as every other OCI unit, with
`docker compose publish` as its push command. Compose stamps
`org.opencontainers.image.created` into the manifest and writes no
revision/version annotations, so neither a precomputed digest nor provenance
adoption can recover its crash window — which is why the adapter learned
`PACTO_EXPECT_CONTENT`. What that key matches is the whole native Compose
identity, never the bytes on their own: `artifactType`
`application/vnd.docker.compose.project`, exactly one layer, layer media type
`application/vnd.docker.compose.file+yaml`, layer digest the expected one. The
same tuple is the adoption rule and the post-push assertion, because it is
literally the same code: `publish-oci-unit.sh` asks `verify-oci.sh` rather than
keeping its own copy, which had drifted down to a layer count and a digest that
would adopt an `application/vnd.example.not-compose` artifact carrying the same
file. `dry-run.sh` ITEM 3b exercises all of it for real against the staging
registry: a genuine `docker compose publish` is adopted, and the same bytes
under a foreign artifact type, under a foreign layer media type, with an extra
layer, with no compose layer, or with different bytes are each refused — on the
crash-window path and on the absent-tag path, where the assertion has to run
before the ledger records anything.

## Immutability reaches the images

The artifact is published once and pulled by digest, which is worth nothing
while the compose file inside it names its images by tag: the tag moves, and the
same artifact digest starts executing different bytes. `scenario.Compose`
therefore refuses any reference without an `@sha256:` of 64 lower-case hex
characters, so a tag fails at projection. In the release, the dashboard pin is
the digest the ledger recorded for the `dashboard-image` unit — read back with
`ledger.sh digest`, and `demo-compose` fails closed if the transaction has none,
because a narrowed recovery must not publish an artifact naming an image nothing
verified. The registry pin is `scenario.ComposeDefaultRegistryImage`, a constant
this repository updates by hand.

Both are **index** digests, for the same reason as
[`kind load`](testing-harness.md#loading-an-image-into-the-kind-node): an index
digest still lets Docker resolve the child that matches the host, while a
per-platform manifest digest is the same string shape and would emulate
everywhere else. The acceptance resolves every pin the pulled artifact names and
asserts each one came back as the host's own architecture, which a child digest
could not.

## The network boundary is a boundary, not an absence of pulls

The claim is: *after the demo artifact and its digest-pinned images have been
pulled, the stack requires no external network access; its private Compose
service network remains available because the dashboard, Evidence Server and
embedded registry must communicate with each other.* `docker compose up --pull
never` on its own proves only the first four words of that, on a runner that
still has the whole Internet and still has the registry the artifact came from.
So stage 11 removes both: the artifact-distribution registry is stopped, and
rules keyed to the project's own bridge refuse anything that leaves it.

### Two chains, two endpoints

Two chains, because a packet leaving the demo can leave by two paths that share
no netfilter hook: `INPUT` for what is addressed to the host itself, `FORWARD` —
and so `DOCKER-USER` — for what the host routes onward. A rule in one is
invisible to the other, so a stage that installed rules in both while only ever
probing a host address would stay green with the whole `DOCKER-USER` arm
deleted. The stage therefore builds one deliberately different endpoint per hook
and probes each:

- **host-local** — a `registry:2` in the *host's* network namespace, addressed at
  the demo bridge's own gateway. A packet to a host address is delivered locally
  and never reaches `FORWARD`.
- **forwarded** — the artifact registry again, reached over a veth pair whose
  `/30` is chosen at run time out of `198.18.0.0/15`. That address is nobody's
  local address, so getting to it is routing, which is `FORWARD` and therefore
  `DOCKER-USER`. Docker's own MASQUERADE makes the reply path work without a
  second rule. `198.18.0.0/15` is RFC 2544's reserved benchmarking space: nothing
  on the public Internet routes it, which is a statement about the Internet and
  not about this machine. A lab, a VPN or a second copy of this harness can
  perfectly well have a route into it here, so the range is only where the
  harness *looks* — it derives candidate `/30`s from its run id and takes the
  first one no local route and no local address already claims, and fails rather
  than picking one if all of them are taken.

Both are proved reachable before any filter — otherwise "could not reach out" and
"was never able to" are the same observation. Then the `DOCKER-USER` arm goes in
alone and the forwarded route must close **while the host-local one stays open**,
which is simultaneously the independence proof and the proof that the forwarded
endpoint really is forwarded. Then the `INPUT` arm closes the other one. An
`ESTABLISHED,RELATED` accept leads both chains so the reply leg of a
host-to-published-port connection survives. Two counterexamples follow, one per
route: the one startup dependency that talks to a registry is redirected at each
endpoint in turn and the run must fail, so a filter that silently stopped applying
cannot leave the assertions above passing. `internal: true` is not used — it takes
the published ports away with the egress, which would contradict the second half
of the claim.

### Fetching the application versus pulling the images

The stage keeps two operations apart. **Fetching the application** is a registry
read that `-f oci://…` performs on every invocation; Compose has no offline mode
for it, and the stage proves that by asserting the application becomes
unreadable the moment its registry stops. **Pulling the service images** is a
separate daemon operation that `--pull never` refuses. So the application is
fetched once, online, with `--pull never` proving no image moved; the volumes
are then emptied, the arms installed, the registry stopped, and the project
restarted **by project name** — which is why a project created online can be
operated offline. The Product gate and the live browser journeys then run
against that isolated stack.

Everything the stage installs lives in the host's network namespace and outlives
the script if it dies, so the exit trap takes the rules, the veth pair, the
host-local endpoint, both projects and the Compose OCI cache entries back down.
