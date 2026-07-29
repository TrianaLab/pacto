# Evidence security and tooling

This page covers the `pacto evidence` command group — how a remote environment
mints keys, signs a Pacto `EvidenceSet`, reports it and how a platform verifies
and ingests it — plus the security invariants the protocol upholds. For the wire
format itself, see the [external evidence protocol](evidence-protocol.md).

The commands split cleanly by side of the trust boundary:

| Command | Side | Purpose |
|---------|------|---------|
| `pacto evidence keygen` | producer | Mint an Ed25519 signing keypair. |
| `pacto evidence sign` | producer | Wrap an `EvidenceSet` in a signed envelope. |
| `pacto evidence send` | producer | Report a signed envelope outbound to an ingestion endpoint. |
| `pacto evidence verify` | either | Verify a signed envelope against a trust store. |
| `pacto evidence serve` | platform | Run the ingestion endpoint that accepts, verifies and evaluates envelopes. |

---

## Minting a keypair

```bash
pacto evidence keygen --out ./keys --key-id edge-eu-west-2026
```

This writes two files into `--out`:

- `<keyId>.key` — the base64 32-byte Ed25519 seed, written **`0600`** (owner
  read and write only). This is secret material. Keep it in the environment that
  produces evidence; never commit it, never ship it to the platform.
- `<keyId>.pub` — the base64 public key, written `0644`. Its base name is the
  trust-store key id. This is the file you hand to the platform operator.

With no `--key-id`, the key id defaults to a short fingerprint of the public key.

---

## Signing an EvidenceSet

```bash
pacto evidence sign evidence.json \
  --key ./keys/edge-eu-west-2026.key \
  --key-id edge-eu-west-2026 \
  --producer edge-eu-west \
  --ttl 24h > envelope.json
```

`sign` reads `evidence.json` — an `EvidenceSet` in its native shape (top-level
keys `Subject`, `ContractRef`, `Source`, `ObservedAt`, `Observations`; unknown
keys are rejected) — validates it, wraps it in an envelope and prints the signed
envelope JSON. Key flags:

- `--key` (required) the private-key file, `--key-id` the trust-store key id it
  maps to, `--producer` the environment id.
- `--ttl` the validity window (default 24h; `0` disables expiry).
- `--id` and `--issued-at` (RFC3339) pin those fields for a fully deterministic
  envelope; otherwise `id` defaults to a content hash of the evidence and
  `issued-at` to now.

The signed envelope is a wire artifact, so `sign` always emits exact JSON
regardless of `--output-format`.

---

## Reporting an envelope

```bash
pacto evidence send envelope.json --url https://ingest.example.com
```

`send` POSTs the signed envelope to `POST /api/evidence/v1/envelopes` at the
target host. This is the only outbound call a producer makes. The host verifies
and evaluates the envelope and returns `202 Accepted` on success or a status that
names the failure category (see the [protocol status codes](evidence-protocol.md#ingestion-api)).

---

## Verifying an envelope

```bash
pacto evidence verify envelope.json --trust ./trust
```

`verify` decodes an envelope and checks its signature, freshness and trust
against `--trust`, which is either a single `<keyId>.pub` file or a directory of
them. It exits non-zero when verification fails, so it drops straight into CI.
The output reports the envelope id, producer and key id on success, or the
sanitized failure reason on failure.

---

## Running the ingestion endpoint

```bash
pacto evidence serve --port 8686 --trust ./trust --local . --store ./evidence-store
```

`serve` starts the ingestion host: it loads the trust store from `--trust`,
resolves contract refs from `--local` bundle roots, persists accepted records
under `--store` and mounts the three protocol endpoints. It advertises the
trusted producer ids at `GET /api/evidence/v1/producers`. TLS termination is the
host's responsibility — run `serve` behind a TLS-terminating proxy or gateway.
Signature verification is always on and cannot be disabled.

---

## Security invariants

The protocol and its tooling hold a small set of non-negotiable invariants.

- **No secret material in logs or errors.** Verification failures return typed
  sentinel errors whose messages never contain private keys, public keys or raw
  signature bytes. The ingestion API echoes only a failure category, never the
  offending material.
- **Sanitized, categorised failures.** Every accept outcome maps to a stable
  HTTP status — `401` for signature, trust or freshness failures, `409` for
  replay, `422` for a malformed or unevaluable payload — with a generic message.
  A caller learns *what* class of thing went wrong, never internal detail it
  could exploit.
- **Bounded payloads.** A decoded envelope is capped at 1 MiB and 10,000
  observations, enforced with an `io.LimitReader` before parsing, so a hostile or
  runaway producer cannot exhaust memory at the ingestion boundary.
- **Mandatory verification.** An unsigned envelope, an unknown key, an
  unsupported algorithm or a bad signature is always rejected. Transport
  security (TLS) is the host's job and is additive; it never substitutes for
  signature verification.
- **Replay protection.** A duplicate id or a non-increasing per-producer
  sequence is rejected, so re-sent or reordered reports never regress a target to
  older state.
- **Private keys are `0600`.** `keygen` writes seeds owner-only. Treat the `.key`
  file as a secret; only the `.pub` crosses the trust boundary to the platform.

---

## See also

- [External evidence protocol](evidence-protocol.md) — the wire format, canonical bytes, freshness and the ingestion API
- [Operational graph](operational-graph.md) — how ingested evidence becomes an operational target
- [CLI reference](cli-reference.md) — the full command surface
