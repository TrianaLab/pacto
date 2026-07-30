# External evidence protocol

Most runtime evidence reaches Pacto from a cluster the operator watches. Some
environments cannot be watched: an edge site, an air-gapped estate, a customer
tenant, a CI runner that only exists for ninety seconds. The **external evidence
protocol** lets those environments participate anyway, without Pacto ever
reaching into them. A remote environment produces and signs a Pacto `EvidenceSet`
and reports it outbound to an ingestion endpoint. The platform verifies it,
evaluates it against the declared contract and exposes the result as an
operational target in the [operational graph](operational-graph.md).

The wire format is a versioned, Ed25519-signed envelope — `pacto.dev/evidence/v1`
— defined once in `pkg/evidenceenvelope` and shared by producers, the CLI and the
ingestion host. This page is the protocol reference. For key handling and the
CLI, see [evidence security and tooling](evidence-security.md).

---

## Outbound-only reporting

The protocol is deliberately one-directional. Pacto never dials into a remote
environment, opens a tunnel or holds a credential for it. The remote side is the
only party that initiates contact, and it only ever pushes:

1. A collector in the environment observes reality and produces an `EvidenceSet`.
2. The environment wraps it in an envelope and signs it with its own private key.
3. It reports the envelope outbound to the platform's ingestion endpoint.
4. The platform verifies the signature against a trust store, evaluates the
   carried evidence against the resolved contract revision and stores the result.
5. The operational graph shows that environment as a target with real findings,
   freshness and provenance.

Because reporting is outbound and periodic, **absence of a new report is not
absence of the target**. A target that stops reporting goes `stale`, then its
source goes `unavailable` — it is never silently deleted and never rendered as an
empty result. This is the same invariant the operational graph applies
everywhere: an environment that goes quiet is *missing*, not *gone*.

```mermaid
sequenceDiagram
    participant Env as Disconnected environment
    participant Col as Collector
    participant CLI as pacto evidence sign/send
    participant Host as Ingestion host (TLS)
    participant Plat as Pacto platform
    participant Graph as Operational graph

    Env->>Col: observe reality
    Col->>CLI: EvidenceSet (JSON)
    CLI->>CLI: wrap + Ed25519 sign
    CLI->>Host: POST /api/evidence/v1/envelopes (outbound only)
    Host->>Plat: verify signature + freshness + replay
    Plat->>Plat: Evaluate(contract, evidenceSet)
    Plat->>Graph: store record, project to target
    Note over Graph: stops reporting → stale → unavailable, never deleted
```

---

## The envelope

An envelope carries exactly one `EvidenceSet` plus the identity, ordering and
freshness metadata the platform needs to trust and place it.

| Field | Type | Meaning |
|-------|------|---------|
| `apiVersion` | string | Protocol version. Must be `pacto.dev/evidence/v1`. |
| `kind` | string | Must be `EvidenceEnvelope`. |
| `id` | string | Unique envelope id. The CLI defaults it to a `sha256:` content hash of the evidence. |
| `producer.id` | string | The environment that produced the envelope. Becomes the target's scope. |
| `producer.version` | string | Optional producer or collector version. |
| `producer.keyId` | string | Trust-store key id that signed this envelope. |
| `sequence` | uint64 | Monotonic per-producer counter for replay and ordering. |
| `issuedAt` | RFC3339 | When the envelope was signed. |
| `expiresAt` | RFC3339 | End of the validity window. Zero disables expiry. |
| `evidenceSet` | object | The Pacto `EvidenceSet` being reported (see [collectors](collectors.md)). |
| `signature` | object | `{ "algorithm": "Ed25519", "value": "<base64>" }` over the canonical bytes. |

A full envelope on the wire:

```json
{
  "apiVersion": "pacto.dev/evidence/v1",
  "kind": "EvidenceEnvelope",
  "id": "sha256:8f3c…",
  "producer": { "id": "edge-eu-west", "version": "1.4.2", "keyId": "edge-eu-west-2026" },
  "sequence": 42,
  "issuedAt": "2026-07-29T10:00:00Z",
  "expiresAt": "2026-07-30T10:00:00Z",
  "evidenceSet": {
    "Subject": { "kind": "service", "name": "payments-api" },
    "ContractRef": "oci://ghcr.io/acme/payments-api@sha256:1a2b…",
    "Source": "edge-collector",
    "ObservedAt": "2026-07-29T09:59:00Z",
    "Observations": [
      {
        "kind": "WorkloadObserved",
        "subject": { "kind": "service", "name": "payments-api" },
        "outcome": "Observed",
        "value": { "type": "service" },
        "provenance": { "collector": "edge-collector", "detectedAt": "2026-07-29T09:59:00Z" }
      }
    ]
  },
  "signature": { "algorithm": "Ed25519", "value": "MEUCIQ…" }
}
```

The `evidenceSet` object uses the `EvidenceSet` shape verbatim, so its top-level
keys are `Subject`, `ContractRef`, `Source`, `ObservedAt` and `Observations`.
`pacto evidence sign` reads a file in exactly this shape and rejects unknown keys.

---

## Canonical bytes and signing

The signature covers the envelope's **canonical bytes**: the envelope JSON with
the `signature` field omitted, re-encoded through a generic decode so map keys
are sorted and whitespace is normalized. A decode then re-encode round trip on
either side reproduces identical bytes, so transport whitespace or field-order
changes never break verification.

Signing and verification are Ed25519. `Sign` fills the `signature` object with
the standard-base64 signature over the canonical bytes; the signer is
responsible for setting `producer.keyId`. `Verify` recomputes the canonical
bytes, resolves `producer.keyId` in the trust store and checks the signature,
then the freshness window. The verification result is a sentinel error whose
message never contains key or signature material.

---

## Key ids and the trust store

`producer.keyId` selects a public key in the verifier's **trust store**. A trust
store is either a single public-key file or a directory of `<keyId>.pub` files,
each a base64 Ed25519 public key. The file base name **is** the key id.

- An envelope whose `keyId` is not in the trust store is rejected as an unknown
  key. Signature verification is mandatory: an unsigned envelope, an unsupported
  algorithm or a bad signature is rejected.
- Rotating a key means adding a new `<keyId>.pub`, signing with the new `keyId`
  and removing the old `.pub` once producers have moved.
- Distributing the trust store is an out-of-band, operator responsibility. The
  protocol verifies against whatever keys the host trusts; it does not fetch keys.

---

## Freshness, replay and bounds

**Expiry.** `issuedAt` and `expiresAt` bound the validity window. `pacto evidence
sign --ttl` sets `expiresAt = issuedAt + ttl` (default 24h; `--ttl 0` disables
expiry). Verification rejects an envelope after `expiresAt` (expired) or before
`issuedAt` (not yet valid). A zero `expiresAt` means no expiry check.

**Replay and sequence.** Each producer stamps a strictly increasing `sequence`.
The ingestion replay guard rejects a repeated envelope `id` and any `sequence`
not greater than the producer's last accepted value. Re-sent or reordered
reports are therefore safe: the platform keeps the latest report per target and
never regresses to an older one. Replay protection is enforced by the durable
store atomically with each immutable write, and its duplicate-id set and
per-producer sequence high-water mark are rebuilt from the immutable records at
startup — so replay protection survives process and pod restarts (see
[durable storage and recovery](#durable-storage-and-recovery)).

**Size and observation bounds.** A decoded envelope is capped at **1 MiB**, and a
single envelope may carry at most **10,000 observations**. The HTTP handler reads
the body through an `io.LimitReader` of one byte past the cap, so an oversized
payload is refused before it can exhaust memory at the boundary.

**Strict decoding.** Decoding rejects unknown JSON fields and requires
`apiVersion`, `kind`, `id`, `producer.id` and `producer.keyId`. `apiVersion` must
be `pacto.dev/evidence/v1` and `kind` must be `EvidenceEnvelope`. A malformed or
unknown-field payload is refused before any signature work.

---

## Ingestion API

The ingestion host mounts five endpoints under `/api/evidence/v1`. TLS
termination is the host's responsibility — run the endpoint behind a
TLS-terminating proxy or gateway. Transport security is not what makes an
accepted envelope trustworthy: **signature verification against the trust store
is mandatory**, and is what the platform relies on regardless of transport.

| Method + path | Purpose | Success |
|---------------|---------|---------|
| `POST /api/evidence/v1/envelopes` | Accept, verify, de-duplicate, evaluate and store one envelope. | `202 Accepted` with `{ id, compliance, findings, acceptedAt }` |
| `GET /api/evidence/v1/health` | Liveness. Independent of recovery. | `200 OK` with `{ "status": "ok" }` |
| `GET /api/evidence/v1/ready` | Readiness. `503` until the durable store finishes recovery. | `200 OK` with `{ "status": "ready" }` |
| `GET /api/evidence/v1/producers` | List the trusted producer ids the host advertises. | `200 OK` with `{ "producers": [ … ] }` |
| `GET /api/evidence/v1/targets` | The latest accepted target per producer, for a read-only HTTP evidence source. | `200 OK` with `{ "targets": [ … ] }` |

`POST` status codes map the accept outcome without leaking any secret material:

| Status | When |
|--------|------|
| `202 Accepted` | Verified, evaluated and stored. |
| `400 Bad Request` | The request body could not be read. |
| `401 Unauthorized` | Signature, trust or freshness failure — unsigned, unknown key, unsupported algorithm, bad signature, expired or not yet valid. |
| `409 Conflict` | Replay — a duplicate `id` or an out-of-sequence `sequence`. |
| `422 Unprocessable Entity` | Malformed or oversized envelope, unknown fields, unsupported version or kind, an invalid `EvidenceSet` or an unresolvable contract ref. |

After a successful accept, the host can trigger a snapshot refresh so the new
target appears in the graph immediately.

---

## Durable storage and recovery

Accepted evidence is durable. Behind the ingestion host is the **Evidence
Server** and its store (`pkg/evidencestore`), which persists every accepted
envelope so replay protection and latest-target state survive a process or pod
restart. The store is deliberately infrastructure-light: the default bucket is
`file:///var/lib/pacto/evidence` on a ReadWriteOnce PVC — no database, cache or
coordination service. The same store logic runs unchanged over cloud buckets
(`s3://`, `gs://`, `azblob://`) through gocloud.dev when you point `--bucket-url`
at one.

**One source of truth, rebuildable projections.** Immutable accepted-evidence
records under `<prefix>/envelopes/` are the sole source of truth — once written,
a record is never overwritten. Materialized target and manifest projections under
`<prefix>/materialized/` are performance optimizations that can be rebuilt from
the records, never authoritative. The immutable write is the commit point: an
envelope is durably accepted the moment its record lands, even if a later
projection write fails.

**Read-after-write without List consistency.** The active writer serves every
read from an in-memory index rather than from a bucket `List`, so a just-accepted
target is visible immediately and correctness never depends on the bucket's list
consistency.

**Recovery and readiness.** At startup the store opens its bucket and replays the
immutable records to rebuild the replay indexes (the accepted duplicate-id set
and the per-producer sequence high-water mark) and the latest-target state.
Because that state is reconstructed from the records themselves, replay
protection — a duplicate `id` or a non-increasing producer `sequence` — survives
process and pod restarts rather than resetting. Readiness gates on recovery:
`GET /api/evidence/v1/ready` reports `503` until recovery completes, while
liveness (`GET /api/evidence/v1/health`) is independent and always answers.

**Single active writer, no distributed lock.** There is exactly one active writer
per (bucket URL + prefix). This is enforced operationally — one replica, with the
chart schema rejecting more — not with a distributed lock built from blob writes.
Sharing one bucket across installations is safe only through distinct prefixes.

**Retention.** Accepted immutable envelopes are never auto-deleted; they are the
audit trail and the recovery source. A bucket lifecycle policy must not delete
anything under `<prefix>/envelopes/`. Only the rebuildable `materialized/`
projections are safe to drop.

---

## Deployment

The Evidence Server, the dashboard and the operator are three independent
processes with one clean responsibility split — the Evidence Server owns
ingestion, verification, recovery and storage, the dashboard consumes the
server's read-only contribution and the operator manages the Kubernetes
lifecycle.

- **In Kubernetes** it is an optional operator-managed component of the single
  `pacto-operator` Helm chart: set `evidence.enabled=true`. The operator
  reconciles a separate Evidence Server Deployment, an internal Service and a
  retained PVC. There is no standalone evidence chart and no subchart. When both
  `dashboard.enabled` and `evidence.enabled` are set, the operator auto-wires the
  dashboard to the internal server via `PACTO_EVIDENCE_SOURCE_URL`, and the
  dashboard consumes it read-only over HTTP without ever touching the bucket.
- **Outside Kubernetes** the same component runs via `pacto evidence serve` — see
  [evidence security and tooling](evidence-security.md#running-the-ingestion-endpoint).

The PVC is retained on purpose and always: it carries no owner reference, so it
survives disabling the component, chart upgrades and uninstall — accepted evidence
is never garbage-collected with the operator. There is deliberately no delete
toggle; to remove persisted evidence, delete the PVC manually
(`kubectl delete pvc pacto-evidence-data`).

---

## Trust boundary

Pacto ships two things that look adjacent but are not the same trust level.

- The **offline target-state fixture** (`pacto fleet --target-state`,
  `pacto mcp --fleet --target-state`) is a demo and test adapter. It supplies
  targets from a local file with **no signature and no verification**. It exists
  to run cluster-free demos and tests. It is not this protocol and carries none
  of its guarantees.
- The **signed EvidenceSet envelope** described here is the real external-evidence
  boundary. Every envelope is Ed25519-signed and verified against a trust store,
  bounded in size, protected against replay and evaluated against the declared
  contract before it becomes a target.

When you need to actually trust evidence from an environment you do not control,
use this protocol, not the fixture.

---

## See also

- [Operational graph](operational-graph.md) — where ingested targets appear and how freshness and completeness work
- [Evidence security and tooling](evidence-security.md) — keygen, sign, verify, serve, send and key handling
- [Collectors and the evidence boundary](collectors.md) — how an `EvidenceSet` is produced and evaluated
