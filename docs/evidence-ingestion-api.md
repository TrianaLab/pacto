# Evidence ingestion API

The HTTP surface of the [external evidence protocol](evidence-protocol.md): the
endpoints an ingestion host mounts, the DTO it serves and the status codes it
answers with.

The ingestion host mounts five endpoints under `/api/evidence/v1`. TLS
termination is the host's responsibility — run the endpoint behind a
TLS-terminating proxy or gateway. Transport security is not what makes an
accepted envelope trustworthy: **signature verification against the trust store
is mandatory**, and is what the platform relies on regardless of transport.

| Method + path | Purpose | Success |
|---------------|---------|---------|
| `POST /api/evidence/v1/envelopes` | Accept, verify, de-duplicate, evaluate and store one envelope. | `202 Accepted` with `{ id, compliance, findings, acceptedAt }`, where `findings` is a count, not the findings themselves |
| `GET /api/evidence/v1/health` | Liveness. Independent of the registry. | `200 OK` with `{ "status": "ok" }` |
| `GET /api/evidence/v1/ready` | Readiness. `503` until every configured subject resolves and answers native Referrers discovery. | `200 OK` with `{ "status": "ready" }` |
| `GET /api/evidence/v1/producers` | List the trusted producer ids the host advertises. | `200 OK` with `{ "producers": [ … ] }` |
| `GET /api/evidence/v1/targets` | The latest accepted report per target — a target being one producer reporting on one subject — for a read-only HTTP evidence source. | `200 OK` with the versioned targets DTO (below). |

## The latest report per target

The `/targets` response is a versioned, self-describing DTO so a consumer can
reconstruct a faithful operational target — not a lossy summary — and can tell a
degraded store from a healthy one:

```json
{
  "schemaVersion": "pacto.dev/evidence-source/v2",
  "generatedAt": "2026-07-29T12:00:00Z",
  "health": { "status": "ready", "subjects": 3, "failedSubjects": 0, "invalidArtifacts": 0 },
  "truncated": false,
  "targets": [
    {
      "subject": "payments",
      "service": "payments-api",
      "domain": "registry.example.com/acme",
      "digest": "sha256:…",
      "producer": "prod-eu",
      "producerKeyId": "edge-eu-west-2026",
      "compliance": "Compliant",
      "coverage": { "evaluated": 3, "required": 5 },
      "findings": [ … ],
      "contractRef": "oci://registry.example.com/acme/payments@sha256:…",
      "evidenceAt": "2026-07-29T11:00:00Z",
      "acceptedAt": "2026-07-29T11:05:00Z"
    }
  ]
}
```

Each target carries its findings — the `findings` key is omitted entirely when
there are none — the immutable `contractRef` (so it links to a concrete
revision), both the evidence and accept timestamps, and producer provenance.
`service`, `domain` and `digest` are the *resolved* logical identity, read from
the contract `contractRef` resolved to, so a consumer attaches the target to the
right domain-qualified service and revision instead of inferring one from
`subject`; `domain` is everything in the resolved reference before its final path
segment, such as `registry.example.com/acme`.

## Completeness and health

`schemaVersion` is the compatibility contract: a
consumer that does not recognise it treats the source as unavailable rather than
misreading it. `health.status` is `ready` (every configured subject read
completely, so an empty target list is authoritative) or `partial` (evidence
exists that could not be read, so absence no longer is), with the counts behind
that verdict beside it. When nothing could be read at all, `/targets` does not
return this DTO — it answers `503` with `{ "code": "registry_unavailable" }`, so
a consumer must treat any non-200 as an unavailable source rather than an empty
one. `health` and `truncated` let a consumer mark the source *partial* — keeping
the usable targets while surfacing that the contribution is incomplete — instead
of presenting a full-looking graph. Both the target count and the per-target
findings count are bounded; `truncated` is set when either bound trims the
response.

## POST status codes

`POST` status codes map the accept outcome without leaking any secret material:

A non-2xx response is a JSON object `{ "code": <stable-code>, "message": <generic> }`.
The `code` is stable and safe to branch on; the `message` is generic and never
contains the underlying error text (resolver, storage or internal detail). Detailed
errors are logged server-side only. The codes and their statuses:

| Status | Code | When |
|--------|------|------|
| `202 Accepted` | — | Verified, evaluated and stored. |
| `400 Bad Request` | `invalid_envelope` | The body could not be read or the envelope could not be decoded. |
| `401 Unauthorized` | `unauthorized_producer` | Signature/trust/freshness failure, or the key is not authorized for this producer or subject. |
| `409 Conflict` | `replay` | A duplicate `id` or an out-of-sequence `sequence`. |
| `422 Unprocessable Entity` | `contract_ref_rejected` | The contract ref is not an approved immutable digest reference. |
| `422 Unprocessable Entity` | `invalid_evidence` | The `EvidenceSet` is invalid. |
| `502 Bad Gateway` | `contract_resolution_failed` | The referenced contract could not be resolved (upstream). |
| `503 Service Unavailable` | `store_not_ready` | The server has not yet resolved and enumerated its configured subjects. |
| `503 Service Unavailable` | `registry_unavailable` / `registry_incomplete` | The accepted history could not be read, or could not be read completely, so the replay check could not run. |
| `503 Service Unavailable` | `store_degraded` | The record could not be published to the registry. |
| `500 Internal Server Error` | `internal_error` | An unexpected failure. |

After a successful accept, the host can trigger a snapshot refresh so the new
target appears in the graph immediately.
