# Contract catalog discovery

`pacto mcp --root <ref>` starts a **read-only contract catalog**: the roots you
name, plus their dependency closure, resolved once at startup and then frozen for
the life of the process.

```bash
# One published platform, one contract you are still working on
pacto mcp \
  --root oci://ghcr.io/acme/platform:1.4.0 \
  --root ./experimental-platform
```

`--root` is repeatable and takes either a local bundle directory or an `oci://`
reference. Nothing is discovered that you did not name: Pacto does not crawl a
registry, guess repository names or read a catalog file.

## The surface

| URI / tool | What it answers |
|------------|-----------------|
| `pacto://catalog` | What this catalog is: schema version (`pacto.dev/catalog/v1`), catalog id, generation time, the bounds that applied, the completeness of the whole answer, and every requested root — including roots that did not resolve, and why. |
| `pacto://catalog/closure` | What is in it: every deduplicated revision with its content identity, rank and retained paths; every resolved dependency edge; every dependency that did not resolve; and the conflicts and cycles left visible rather than resolved — under the same catalog metadata. |
| `pacto_catalog_revision` | One revision by its full identity — service name, domain, content scheme and content digest. |

That is the whole surface. Catalog mode registers no authoring tools: a server
started for read-only discovery must not be a way to modify a contract.

The two resources are named `pacto_catalog` and `pacto_catalog_closure` — the
URIs above are what you read, the names are what a client-side allow-list keys
on.

`pacto://catalog` is the cheaper read, so reading it first is the recommended
order — but it is not a precondition: both resources carry the same catalog
metadata, so either one is safe to read alone, in any order. That repetition is
deliberate. Ask for two roots that both fail to resolve and the closure is empty
in every collection — without the metadata travelling with it, a payload
carrying only data would be indistinguishable from an authoritative answer. The
metadata is what says `partial`, names each `ROOT_UNRESOLVED`, and keeps the two
roots you actually requested visible.

### The revision lookup

The lookup is a tool rather than a URI template because a revision's identity is
four structured fields, and a service name or domain may contain `/`, `:`, `%` or
arbitrary UTF-8. Encoding that into a path segment would mean re-parsing it at
the other end, and two different identities could arrive as one.

Those four fields are the tool's arguments, and they are named `name`, `domain`,
`scheme` and `digest` — not `service`, and not `ref`. `name`, `scheme` and
`digest` are required; `domain` is omitted for a local revision, which has none.
Take them from a revision's own `service` and `content` objects in
`pacto://catalog/closure` rather than composing them by hand:

```json
{ "name": "payments", "domain": "ghcr.io/acme", "scheme": "oci",
  "digest": "sha256:…" }
```

!!! warning "A wrong argument name reads as a proven absence"
    The identity is matched, not validated as a whole. A call that misnames or
    omits `name` is answered `{"found": false, "completeness": "complete"}` —
    the same answer as a revision that genuinely is not in the catalog. An agent
    that trusts `completeness` will conclude the revision does not exist. Echo
    `requested` back and check it says what you meant before you act on
    `found: false`. (`scheme` and `digest` *are* rejected when malformed, so the
    leniency is specific to `name`.)

## What it is not

- **A catalog is not the fleet.** The catalog describes *contracts* reachable
  from the roots you named. It says nothing about deployments, environments,
  runtime targets or observed state — those are [Fleet query tools](mcp-integration.md#three-tool-families-and-their-boundaries)
  over the [Operational Graph](operational-graph.md). A requested root is an
  input to discovery, never a runtime target.
- **Discovery is not authorization.** Learning that a revision exists says
  nothing about whether you may read, deploy or call it. Authorization stays with
  your policy and IAM systems.
- **Discovery is not execution.** Nothing in this surface invokes anything. If
  you want a bundle's operations as callable tools, that is the separate
  [Agent capabilities](mcp-agent-capabilities.md) mode.
- **It is a session, not a store.** There is no database, no daemon state and no
  background refresh. The catalog lives in the process and disappears with it.

## Partial is not empty, and not complete

Every catalog reports its `completeness`. A root or a dependency that could not
be resolved stays visible — with a category such as `NOT_FOUND`, `AUTH_FAILED` or
`UNAVAILABLE`, never a raw registry error — and the whole answer is marked
`partial`.

Treat the three states as different facts:

- `complete` — everything reachable from the roots resolved.
- `partial` — some of it did not. A revision you cannot find here is *unknown*,
  not proven absent.
- An empty catalog is never started at all: `pacto mcp --root ""` fails rather
  than serve an authoritative "there is nothing here".

## Requested, resolved, identity

Three things that look alike and are not:

| | Example | Stability |
|---|---|---|
| Requested reference | `oci://ghcr.io/acme/platform:1.4.0` | A tag. It can move. |
| Resolved reference | `ghcr.io/acme/platform@sha256:…` | Immutable, as of startup. |
| Content identity | scheme `oci` + digest `sha256:…` | What the bytes *are*. This is identity. |

Local roots work the same way, with a content hash over the bundle's files in
place of a registry digest: two byte-identical directories are one revision, and
a path is never an identity. Local and registry roots go through the same
reference parsing, credentials and cache the rest of the CLI uses.
