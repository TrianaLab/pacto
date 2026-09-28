# External evidence

The **external evidence protocol** lets environments that cannot be watched — edge sites, air-gapped estates, customer tenants, ephemeral CI runners — participate in the operational graph. A remote environment produces and signs a Pacto `EvidenceSet` and reports it outbound. The platform verifies the signature, evaluates the evidence and exposes the result as an operational target.

The protocol is one-directional: Pacto never dials into a remote environment. The remote side initiates contact and only pushes. Accepted evidence is stored by your contract registry as an OCI 1.1 referrer. There is no bucket, no PVC and no database — the Evidence Server is stateless.

!!! warning "GHCR cannot host Pacto evidence"
    GitHub Container Registry does not serve the native OCI Referrers API, which the Evidence Server requires. GHCR works fine for contracts — only evidence needs the Referrers endpoint. Publish contracts you want to carry evidence to a conformant registry (zot, Docker Hub, Harbor, ECR, ACR, Artifactory).

**Pacto does not sign OCI bundles.** What it signs is evidence envelopes: every envelope is Ed25519-signed and verified against a trust store before ingestion.

---

## Wire format

An envelope carries exactly one `EvidenceSet` plus the identity, ordering and freshness metadata the platform needs to trust and place it.

| Field | Type | Meaning |
|-------|------|---------|
| `apiVersion` | string | Protocol version. Must be `pacto.dev/evidence/v1`. |
| `kind` | string | Must be `EvidenceEnvelope`. |
| `id` | string | Unique envelope id. The CLI defaults it to a `sha256:` hash over `apiVersion`, `producer.id`, `sequence` and the evidence, so two producers reporting identical evidence never collide. |
| `producer.id` | string | The environment that produced the envelope. Becomes the target's scope. |
| `producer.version` | string | Optional producer or collector version. |
| `producer.keyId` | string | Trust-store key id that signed this envelope. |
| `sequence` | uint64 | Monotonic per-producer counter for replay and ordering. |
| `issuedAt` | RFC3339 | When the envelope was signed. |
| `expiresAt` | RFC3339 | End of the validity window. Zero disables expiry. |
| `evidenceSet` | object | The Pacto `EvidenceSet` being reported. Top-level keys: `Subject`, `ContractRef`, `Source`, `ObservedAt`, `Observations`. |
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
    "ContractRef": "oci://registry.example.com/acme/payments-api@sha256:1a2b…",
    "Source": "edge-collector",
    "ObservedAt": "2026-07-29T09:59:00Z",
    "Observations": [ … ]
  },
  "signature": { "algorithm": "Ed25519", "value": "2Q6d…gyZhDw==" }
}
```

**Canonical bytes.** The signature covers envelope JSON with the `signature` field omitted, re-encoded so map keys are sorted and whitespace is normalized. Transport formatting never breaks verification.

**Immutable contract references.** A reported `ContractRef` must be an immutable `oci://…@sha256:…` digest reference. Local paths, bare refs and mutable tags are rejected.

**Bounds.** An envelope is capped at **1 MiB** and may carry at most **10,000 observations**. Oversized payloads are refused before parsing. Decoding rejects unknown fields and requires `apiVersion`, `kind`, `id`, `producer.id` and `producer.keyId`.

**Freshness and replay.** `issuedAt` and `expiresAt` bound the validity window. Verification rejects expired or not-yet-valid envelopes. A zero `expiresAt` disables the expiry check. Each producer stamps a strictly increasing `sequence`. Ingestion rejects a repeated `id` or a non-increasing `sequence`. Replay protection runs inside the serialized commit, over a duplicate-id set and a per-producer sequence maximum. Both are re-derived from the registry on every commit, so they survive process restarts.

---

## CLI commands

| Command | Side | Purpose |
|---------|------|---------|
| `pacto evidence keygen` | producer | Mint an Ed25519 signing keypair. Writes `<keyId>.key` (secret, `0600`) and the public key. `--producer` is optional: with it the public key is `<producer>__<keyId>.pub`, binding the key to that producer; without it, a bare `<keyId>.pub` binds it to a producer named after the key id. |
| `pacto evidence sign` | producer | Wrap an `EvidenceSet` in a signed envelope. Reads the EvidenceSet JSON, validates it, wraps it and signs with `--key`, `--key-id`, `--producer`, `--sequence`. Default `--ttl 24h`; `--ttl 0` disables expiry. |
| `pacto evidence send` | producer | Report a signed envelope outbound to an ingestion endpoint. POSTs to `POST /api/evidence/v1/envelopes`. |
| `pacto evidence verify` | either | Verify a signed envelope against a trust store. Checks signature, freshness, producer authorization and trust. Exits non-zero on failure. |
| `pacto evidence serve` | platform | Run the ingestion endpoint. Loads trust store from `--trust`, resolves every `--subject` (at least one required), verifies and evaluates envelopes and publishes results to the registry as OCI 1.1 referrers. Listens on `127.0.0.1:8686` or `--listen-address`. |

---

## Ingestion HTTP API

The ingestion host mounts five endpoints under `/api/evidence/v1`.

| Method + path | Purpose | Success |
|---------------|---------|---------|
| `POST /api/evidence/v1/envelopes` | Accept, verify, de-duplicate, evaluate and store one envelope. | `202 Accepted` with `{ id, compliance, findings, acceptedAt }` |
| `GET /api/evidence/v1/health` | Liveness. Independent of the registry. | `200 OK` with `{ "status": "ok" }` |
| `GET /api/evidence/v1/ready` | Readiness. `503` until every configured subject resolves and answers native Referrers discovery. | `200 OK` with `{ "status": "ready" }` |
| `GET /api/evidence/v1/producers` | List the trusted producer ids the host advertises. | `200 OK` with `{ "producers": [ … ] }` |
| `GET /api/evidence/v1/targets` | The latest accepted report per target for a read-only HTTP evidence source. | `200 OK` with the versioned targets DTO (schema `pacto.dev/evidence-source/v2`) |

**POST status codes.** A non-2xx response is a JSON object `{ "code": <stable-code>, "message": <generic> }`. The `code` is stable and the `message` is generic — neither ever contains the underlying error text, keys or signature bytes. Verification failures and server-side errors are logged but never exposed to the caller. TLS termination is the host's responsibility. Signature verification is always on and cannot be disabled.

| Status | Code | When |
|--------|------|------|
| `202 Accepted` | — | Verified, evaluated and stored. |
| `400 Bad Request` | `invalid_envelope` | The body could not be read or the envelope could not be decoded. |
| `401 Unauthorized` | `unauthorized_producer` | Signature, trust, freshness failure or the key is not authorized for this producer or subject. |
| `409 Conflict` | `replay` | A duplicate `id` or an out-of-sequence `sequence`. |
| `422 Unprocessable Entity` | `contract_ref_rejected` | The contract ref is not an approved immutable digest reference. |
| `422 Unprocessable Entity` | `invalid_evidence` | The `EvidenceSet` is invalid. |
| `502 Bad Gateway` | `contract_resolution_failed` | The referenced contract could not be resolved. |
| `503 Service Unavailable` | `store_not_ready` | The server has not yet resolved and enumerated its configured subjects. |
| `503 Service Unavailable` | `registry_unavailable`, `registry_incomplete` | The accepted history could not be read or could not be read completely, so the replay check could not run. |
| `503 Service Unavailable` | `store_degraded` | The record could not be published to the registry. |
| `500 Internal Server Error` | `internal_error` | An unexpected failure. |

---

## Registry conformance

**Native Referrers API is mandatory.** Verify before configuring:

```bash
oras discover --distribution-spec v1.1-referrers-api <repo>@sha256:<digest>
```

A conformant registry answers `GET /v2/<name>/referrers/<digest>` with **HTTP 200** and an OCI image index. A non-conformant one answers `404 MANIFEST_UNKNOWN`.

| Registry | Referrers API |
|---|---|
| zot, Docker Hub, Harbor, ECR, ACR, Artifactory | Yes |
| **GHCR**, CNCF distribution (`registry:2`, `registry:3`) | **No** |

| Operation | Requirement |
|-----------|-------------|
| **Permissions** | Pull (resolve contract, enumerate referrers, read payloads) and push (publish accepted record) on every configured subject's repository. Never deletes, never writes tags, never touches unconfigured repositories. |
| **Authentication** | Pacto OCI credential policy: explicit credential, `pacto login`, `GITHUB_TOKEN` for GHCR or Docker config. In Kubernetes: `evidence.registry.credentialsSecret` names an existing `kubernetes.io/dockerconfigjson` Secret. |
| **Subjects** | Explicit allow-list of exact immutable contract revisions: `--subject oci://registry.example.com/acme/checkout@sha256:<64 hex>`. Mutable tags, local paths, non-`oci://` refs and catalog-wide discovery rejected. At least one required. |
| **Artifact format** | One untagged OCI manifest per accepted report. `subject`: contract digest from `ContractRef`. `artifactType`: `application/vnd.pacto.evidence.record.v1+json`. One layer carrying `pacto.dev/evidence-record/v1`. Deterministic build. |
| **Writer concurrency** | Single active writer: one replica, `Recreate` rollout strategy. Registry offers no compare-and-set. Commit: verify, mutex, enumerate all referrers, rebuild replay state, check replay globally, publish, confirm discoverable, return `202`. State re-derived from registry. |
| **Retention and GC** | Pacto never deletes evidence. Retention, backup and GC are registry responsibilities. Exclude referrers of contract manifests from GC policies that prune untagged manifests. Deleting a subject deletes its evidence. |

**Registry write access is evidence write access.** The signature is checked once at ingestion; the read path does not re-verify it.

**Readiness and partial state** — writes fail closed when reads are not complete.

| State | Meaning | `/ready` | `/targets` |
|-------|---------|----------|------------|
| **ready** | Every subject resolved, every referrer valid. | `200` | `200`, authoritative |
| **partial** (bad artifact) | Every subject resolved, some referrer unreadable. | `200` | `200` with `health.status: partial` |
| **partial** (bad subject) | Some subject failed, others read. | `503` | `200` with `health.status: partial` |
| **unavailable** | No subject readable. | `503` | `503` `registry_unavailable` |

Beside `status`, the `health` block carries the counts that say which half of a `partial` you have: `subjects`, `failedSubjects` and `invalidArtifacts`.

---

## Trust store and trust config

A trust store is a single `.pub` file or a directory of `.pub` files (base64 Ed25519 public keys). The filename binds the producer: `<producerId>__<keyId>.pub` authorizes key `keyId` for producer `producerId`, while a bare `<keyId>.pub` authorizes it for the producer whose id equals the key id.

**Authentication and authorization are separate.** An envelope whose `keyId` is not in the trust store is rejected as an unknown key. An unsigned envelope, an unsupported algorithm or a bad signature is rejected. After the signature checks out, the envelope's `producer.id` must match the key's bound producer, or it is rejected — a trusted key cannot sign as another producer.

**Rotation.** Add a new `<producerId>__<newKeyId>.pub`, sign with the new `keyId` and remove the old entry. The producer identity survives the key change.

**Structured trust configuration** for per-key subject and contract-repository allowlists:

```yaml
apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: edge-eu-west-2026
    producerId: edge-eu-west
    publicKeyFile: edge-eu-west__edge-eu-west-2026.pub
    allowedSubjects: [payments-*]                  # path.Match globs; empty = any
    allowedContractRepos: [registry.example.com/acme/contracts]  # prefixes; empty = any
```

Loading validates the schema version, identifier grammar, duplicate key ids, contradictory producer bindings, missing or traversing key files and malformed patterns.

**Distributing the trust store is an out-of-band operator responsibility.** The protocol verifies against whatever keys the host trusts and never fetches them. In Kubernetes the trust store is a Secret mounted read-only (`evidence.trust.existingSecret`).

For Kubernetes deployment (trust store creation, subject configuration, registry credentials), see [The Evidence Server](integrations/kubernetes/evidence-server.md).

---

## See also

- [Operational graph](operational-graph.md) — where ingested targets appear and how freshness and completeness work
- [Collectors and the evidence boundary](model.md#collectors-and-the-evidence-boundary) — how an `EvidenceSet` is produced and evaluated
- [CLI reference](cli-reference.md) — the full command surface
