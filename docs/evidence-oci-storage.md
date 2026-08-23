# Evidence in the registry: OCI storage and operations

Accepted evidence is not stored by Pacto. It is stored by **your contract
registry**, as an OCI 1.1 [Referrers] artifact whose subject is the exact,
immutable contract revision the evidence reports on. There is no bucket, no PVC,
no database and no recovery engine — the Evidence Server is a stateless
verification and projection boundary in front of a registry you already run.

This page is the operations guide for that model. It complements the wire-level
[external evidence protocol](evidence-protocol.md) and the
[security and tooling](evidence-security.md) guide.

[Referrers]: https://github.com/opencontainers/distribution-spec/blob/main/spec.md#listing-referrers

## What is stored, and where

One accepted report is one untagged OCI artifact:

| Part | Value |
|------|-------|
| `subject` | the contract manifest at `oci://<repo>@sha256:<digest>` — exactly the digest in the signed `EvidenceSet.ContractRef` |
| `artifactType` | `application/vnd.pacto.evidence.record.v1+json` |
| layers | exactly one, same media type, carrying the record |
| payload schema | `pacto.dev/evidence-record/v1` |
| config | the empty JSON descriptor every image manifest requires |

The artifact lives in the **same repository as the contract**, because that is
what the Referrers API addresses. It carries no tag: it is discovered by asking
the registry `GET /v2/<repo>/referrers/<digest>`, never by guessing a name.

Publishing is deterministic. The manifest is built by hand rather than through a
packing helper that would stamp a creation timestamp, so republishing an
identical record yields an identical digest instead of a second copy.

## Registry requirements

**The native Referrers API is mandatory.** Pacto sets the referrers capability
explicitly, which disables the fallback that would otherwise emulate discovery
with a `sha256-<digest>` index tag. A registry without the endpoint makes the
Evidence Server permanently not-ready — it never silently degrades to the tag
scheme, because a store whose contents depend on a client-side convention is not
a store two clients agree about.

Verify a candidate registry before you configure it:

```bash
oras discover --distribution-spec v1.1-referrers-api <repo>@sha256:<digest>
```

Without `--distribution-spec` the ORAS CLI falls back to the tag scheme on its
own, so an unpinned `oras discover` that "works" proves nothing about the
endpoint Pacto uses. A conformant registry answers
`GET /v2/<name>/referrers/<digest>` with **HTTP 200** and an OCI image index,
empty if nothing refers to that digest. The spec is explicit that it must not
answer 404, because a client reads 404 as "not implemented" and falls back to the
tag scheme.

| Registry | Referrers API | How this was checked |
|---|---|---|
| zot | Yes | `200` + empty index |
| Docker Hub | Yes | `200` + empty index |
| Harbor, ECR, ACR, Artifactory | Documented by the vendor | vendor documentation, not probed here |
| **GHCR** | **No** | `404 MANIFEST_UNKNOWN` |
| CNCF distribution (`registry:2`, `registry:3`) | No | route absent; `oras discover` reports `unsupported` |

!!! warning "GHCR cannot host Pacto evidence"
    GitHub Container Registry returns `404 MANIFEST_UNKNOWN` from the referrers
    endpoint, so the Evidence Server stays permanently not-ready against it.
    This is worth calling out because GHCR is where the rest of this
    documentation publishes *contracts*, and it works fine for that — contracts
    are ordinary OCI artifacts and need no referrers support. Only evidence does.

    Note what that implies. A record is attached to its contract revision **in
    the same repository**, so this is not a matter of pointing evidence somewhere
    else: a contract you want to carry evidence has to be published to a
    conformant registry in the first place. Contracts you only validate, diff and
    resolve can stay in GHCR.

### Permissions

The Evidence Server needs, on every configured subject's repository:

| Operation | Needs |
|-----------|-------|
| resolve the contract revision, enumerate referrers, read payloads | **pull** |
| publish an accepted record | **push** |

Nothing else. It never deletes, never writes a tag and never touches a
repository it was not configured with.

**Registry write access is evidence write access.** The read path does not
re-verify producer signatures — the signature is checked once, at ingestion, and
the registry is the trust boundary from that point on. Anyone who can push to
these repositories can add a record Pacto will serve. Scope the credential to
the contract repositories and treat push rights there as you treat the right to
publish a contract.

### Authentication

Evidence reuses **Pacto's existing OCI credential policy** — the same resolution
`pacto push` and `pacto pull` use, in order: an explicit credential, the Pacto
credential store (`pacto login`), `GITHUB_TOKEN` for GHCR, and the Docker config.
There is no second login command, no evidence-specific credential file and no
separate registry-auth model.

In Kubernetes, point the chart at an **existing** `kubernetes.io/dockerconfigjson`
Secret:

```yaml
evidence:
  enabled: true
  trust:
    existingSecret: pacto-evidence-trust
  registry:
    credentialsSecret: my-registry-creds   # optional; must already exist
    subjects:
      - oci://registry.example.com/acme/checkout@sha256:<64 hex>
```

The chart never creates that Secret and never renders its contents; it is mounted
read-only. Omit it for anonymous or in-cluster registry access.

## Configuring subjects

Subjects are an explicit allow-list of exact contract revisions. At least one is
required whenever the Evidence Server is enabled — an empty configuration is an
install-time failure, not a server that starts and reports an authoritative empty
world.

```bash
pacto evidence serve \
  --subject oci://registry.example.com/acme/checkout@sha256:<64 hex> \
  --subject oci://registry.example.com/acme/payments@sha256:<64 hex> \
  --trust ./trust
```

Rejected, deliberately:

- **mutable tags** (`oci://repo:1.2.3`) — a tag can be moved onto another
  manifest, silently changing what the stored evidence reports on;
- **local paths and non-`oci://` refs** — there is nothing to attach a referrer
  to;
- **image references** that are not the contract revision;
- **inferred repositories and catalog-wide discovery** — Pacto never scans a
  registry looking for subjects it was not told about;
- **duplicates** — deduplicated before use, so one subject is scanned once.

A report whose `ContractRef` is not one of the configured subjects is rejected at
ingestion.

## Readiness, partial and unavailable

Readiness is honest about what could be read, and never renders an unreadable
store as an empty one.

| State | Meaning | `/ready` | `/targets` |
|-------|---------|----------|------------|
| **ready** | every subject resolved and every referrer was a valid Pacto record | `200` | `200`, authoritative |
| **partial**, from a bad artifact | every subject resolved, but some referrer was not a readable Pacto record | `200` | `200` with `health.status: partial`, the readable records still served |
| **partial**, from a bad subject | some subject failed, others were read | `503` | `200` with `health.status: partial`, the readable records still served |
| **unavailable** | no subject could be read at all | `503` | `503` `registry_unavailable` |

The two `partial` rows differ because the two probes ask different questions.
`/targets` reports what it managed to read. `/ready` is stricter: it re-resolves
**every** configured subject and fails on the first one that does not answer, so
a single unreachable subject takes the host out of rotation even though the
others are being served. That is deliberate — a host that cannot read its whole
history cannot prove an incoming report is not a replay, so it must stop
accepting writes.

`health` carries the counts behind the state — `subjects`, `failedSubjects`,
`invalidArtifacts` — so a consumer can distinguish "nothing was reported" from
"the store could not be read". Absence of evidence is not evidence of absence,
and the DTO is where that distinction is made machine-readable.

**Writes fail closed whenever reads are not complete.** A replay check run
against a history that could not be fully reconstructed is not a replay check, so
ingestion returns `503` `registry_unavailable` or `registry_incomplete` rather
than accepting a report it cannot prove is new. Fix the registry — or remove the
malformed artifact — and ingestion resumes with no operator action inside Pacto.

Unrelated artifacts attached to the same contract revision (signatures, SBOMs,
attestations) are ignored by `artifactType`. They never make a read partial.

## The commit protocol

There is exactly **one active writer**, enforced operationally: one replica, and
the `Recreate` rollout strategy so an upgrade never briefly runs two. The
registry offers no compare-and-set that would let two writers agree, and Pacto
does not invent a distributed lock on top of one.

A commit is one serialized logical operation:

1. verify the envelope signature, authorize the producer, the operational subject
   and the contract, and evaluate the contract;
2. enter the process-wide commit mutex;
3. enumerate **every** Referrers page for **every** configured subject, following
   pagination to the end;
4. rebuild the duplicate-envelope-id set and the per-producer maximum sequence
   from what was read;
5. reject a replay — globally across every subject, not per repository, because
   a producer's sequence is the producer's, not the contract's;
6. publish one untagged evidence artifact;
7. confirm the record is discoverable through the same Referrers API a later scan
   will use;
8. only then return `202`.

Step 7 is why a restart with no local state is uneventful: everything the replay
check knows was re-derived from the registry, so there is nothing to lose.

## Retention, backup and garbage collection

These are **registry responsibilities**, and Pacto does not duplicate them:

- **Retention.** Evidence lives as long as the registry keeps it. Pacto never
  deletes an evidence artifact.
- **Backup.** Backing up evidence is backing up the contract repositories.
- **Garbage collection.** A registry GC policy that removes untagged manifests
  will remove evidence, because evidence is deliberately untagged. Exclude
  referrers of contract manifests from any such policy, or scope it to
  repositories that hold no contracts.
- **Deleting a subject deletes its evidence.** Removing the contract manifest,
  or removing its referrers, removes the records attached to it. Registries
  differ in whether deleting a subject cascades to its referrers; either way the
  evidence stops being discoverable. Pacto reports the resulting state honestly
  (partial or unavailable) and does not reconstruct it.

## Interoperability

The store is a plain OCI registry, so it is legible without Pacto. Any client
that writes the documented media types and the `pacto.dev/evidence-record/v1`
payload writes evidence Pacto reads, and anything Pacto publishes is discoverable
by any OCI 1.1 client:

```bash
# what Pacto published, seen by a third party
oras discover --distribution-spec v1.1-referrers-api --format json \
  registry.example.com/acme/checkout@sha256:<64 hex>
```

Malformed input is malformed for everyone: the payload is decoded strictly, so an
unknown field, trailing JSON, the wrong schema version, more than one layer, a
wrong media type, an oversized blob or a record whose contract identity disagrees
with the subject it is attached to is rejected — making reads partial and writes
fail closed, never quietly accepted.
