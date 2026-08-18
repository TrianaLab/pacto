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
pacto evidence keygen --out ./keys --producer edge-eu-west --key-id edge-eu-west-2026
```

This writes two files into `--out`:

- `<keyId>.key` — the base64 32-byte Ed25519 seed, written **`0600`** (owner
  read and write only). This is secret material. Keep it in the environment that
  produces evidence; never commit it, never ship it to the platform.
- `<producer>__<keyId>.pub` — the base64 public key, written `0644`. The filename
  **binds the key to that producer** in the trust store, so the platform authorizes
  it only for evidence signed as that producer. This is the file you hand to the
  platform operator; you do not need to know or apply the filename convention by
  hand — `--producer` writes it for you.

With no `--producer`, the key is bound to a producer named after the key id and the
file is a bare `<keyId>.pub` (the single-producer default). With no `--key-id`, the
key id defaults to a short fingerprint of the public key. **Sign with the same
`--producer` and `--key-id` you minted the key with** — a mismatch is rejected.

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

`verify` decodes an envelope and checks its signature, freshness, producer
authorization and trust against `--trust`, which is either a single public-key
file or a directory of `<producerId>__<keyId>.pub` files (a bare `<keyId>.pub`
binds the producer to the key id). Each key is authorized for exactly one
producer, so a trusted key cannot sign as another. It exits non-zero when
verification fails, so it drops straight into CI.
The output reports the envelope id, producer and key id on success, or the
sanitized failure reason on failure.

### Structured trust configuration

The bare-`.pub` mode binds a key only to a **producer**; it cannot express the
per-key **subject** and **contract-repository** allowlists the verification and
ingestion layers already enforce. To configure those, point `--trust` at a
versioned YAML trust config instead (a file ending in `.yaml`/`.yml`):

```yaml
apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: edge-eu-2026
    producerId: edge-eu
    publicKeyFile: edge-eu-2026.pub   # resolved relative to this config's directory
    allowedSubjects:                  # path.Match globs; empty = any subject
      - payments-*
    allowedContractRepos:             # bare registry/repo prefixes; empty = any repo
      - ghcr.io/acme/contracts
```

Loading validates the schema version, the identifier grammar, duplicate key ids,
contradictory producer bindings for one key file, missing/traversing key files
and malformed subject/repo patterns. Because `publicKeyFile` resolves relative to
the config's directory, a Kubernetes Secret mounted as a directory containing the
config plus its `.pub` files can be pointed at directly. The bare-`.pub` mode
remains supported but stays deliberately limited to producer binding — it does not
enforce scopes it cannot express.

---

## Running the ingestion endpoint

```bash
pacto evidence serve \
  --trust ./trust \
  --subject oci://ghcr.io/acme/payments-api@sha256:1a2b…
```

`serve` starts the ingestion host: it loads the trust store from `--trust`,
resolves every `--subject` (repeatable, at least one required) in the contract
registry, then evaluates each accepted envelope against the contract its
`ContractRef` resolves to and publishes the result to that registry as an OCI 1.1
referrer of the subject. It holds nothing locally — there is no store directory,
bucket or data volume. It listens on `127.0.0.1:<--port>` (default 8686) or on
`--listen-address host:port`, advertises the producer ids configured with
`--producer` at `GET /api/evidence/v1/producers` and reports readiness at
`GET /api/evidence/v1/ready` (`503` until every subject resolves and answers
native Referrers discovery). Registry access uses Pacto's normal OCI credential
resolution — there is no evidence-specific login. TLS termination is the host's
responsibility — run `serve` behind a TLS-terminating proxy or gateway. Signature
verification is always on and cannot be disabled.

For registry requirements, permissions, retention and failure semantics, see
[evidence in the registry](evidence-oci-storage.md); for how the server is
deployed, see [deployment](evidence-protocol.md#deployment).

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
  older state. It is enforced inside the serialized commit, over a history
  re-enumerated from the registry every time, so it survives process and pod
  restarts and applies **globally across subjects** — a producer's sequence is the
  producer's, not the contract's (see
  [evidence in the registry](evidence-oci-storage.md#the-commit-protocol)).
- **Reads fail honest, writes fail closed.** A store that could not be read
  completely is reported as `partial` or `unavailable`, never as an authoritative
  empty result, and ingestion refuses while the accepted history cannot be fully
  reconstructed — a replay check over a partial history is not a replay check.
- **Single active writer.** Exactly one, enforced operationally (one replica plus
  the `Recreate` rollout strategy) rather than with a distributed lock, because
  the registry offers no compare-and-set two writers could agree on.
- **Registry write access is evidence write access.** The producer signature is
  verified once, at ingestion; the read path does not re-verify it. Anyone who can
  push to a configured contract repository can add a record Pacto serves, so scope
  that credential as tightly as the right to publish a contract.
- **Evidence is never deleted by Pacto.** It is the audit trail, and Pacto only
  ever pushes. Because the artifacts are deliberately untagged, a registry
  garbage-collection policy that prunes untagged manifests will remove them —
  exclude referrers of contract manifests from any such policy.
- **Trust store is a read-only mount.** In the operator-managed deployment the
  trust store is a Secret of `<producerId>__<keyId>.pub` keys mounted read-only
  (`evidence.trust.existingSecret`), each key bound to exactly one producer so a
  trusted key cannot sign as another. Distributing it stays an out-of-band operator
  responsibility; the server never fetches keys.
- **Private keys are `0600`.** `keygen` writes seeds owner-only. Treat the `.key`
  file as a secret; only the `.pub` crosses the trust boundary to the platform.

---

## See also

- [External evidence protocol](evidence-protocol.md) — the wire format, canonical bytes, freshness and the ingestion API
- [Operational graph](operational-graph.md) — how ingested evidence becomes an operational target
- [CLI reference](cli-reference.md) — the full command surface
