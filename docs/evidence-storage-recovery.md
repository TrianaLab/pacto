# Evidence store: storage, recovery and consistency

This is the design record (ADR) for how the durable Evidence Server stores
accepted evidence, recovers it after a restart, and what consistency it
guarantees on each backend. It complements the wire-level
[external evidence protocol](evidence-protocol.md) and the
[security and tooling](evidence-security.md) guide.

The store is deliberately infrastructure-light: the default deployment needs only
a `file://` bucket on a ReadWriteOnce PVC — no database, object-storage service,
cloud account, cache or coordination service. Cloud buckets (`s3://`, `gs://`,
`azblob://`) run the *same* logic through the Go Cloud Development Kit.

## Object states

Every object under the store's prefix is exactly one of four kinds, and only the
first is authoritative:

| State | Location | Meaning |
|-------|----------|---------|
| **Durably accepted record** | `<prefix>/envelopes/<producer>/<seq>-<id>.json` | The immutable, authoritative unit. Written once, never modified. Its existence **is** acceptance. |
| **Rebuildable manifest projection** | `<prefix>/materialized/manifest.json` | The one materialized projection: a record-count summary read back and verified against the log on recovery to detect a crash between an accepted write and its manifest update. Never a source of truth; always reconstructable from the accepted records. No per-target projection is persisted — the per-target latest is served from the in-memory index (rebuilt from the log), so no write-only derived state carries an unverified correctness guarantee. |
| **Immutable uploaded object** | `<prefix>/envelopes/…` | A blob the driver reports as present. It becomes a *durably accepted record* only if it passes recovery validation (below); a torn or tampered upload is reported as corruption, never indexed. |
| **Orphan upload** | anywhere | A partial/torn write from an interrupted commit. Detected as corruption on recovery and excluded; it never affects replay or latest-target state. |

## The commit protocol

There is exactly **one active writer** per `(bucket URL + prefix)`. This is
enforced operationally — Helm `replicaCount: 1`, a values schema that rejects
more, a startup warning, and a unique instance id in diagnostics — **not** with a
fake distributed lock built from blob writes.

A commit runs, under a single in-process critical section:

1. Stamp store-owned metadata (record schema version, evidence digest).
2. Reject a duplicate envelope id, a tainted producer, or a non-increasing
   sequence (replay protection) from the in-memory index.
3. **Write the immutable record. This write is the commit point.**
4. Reserve the envelope id and sequence in the in-memory index.
5. Best-effort write the rebuildable manifest projection.

Read-after-write within the single writer is always served from the in-memory
index, **never** from `Bucket.List` — so a commit is immediately visible to the
next read regardless of any list-consistency lag.

### Crash outcomes

A crash at every boundary has a defined, safe outcome:

| Crash point | Outcome on restart |
|-------------|--------------------|
| Before the immutable write | Not accepted; nothing persisted. The producer re-sends the same sequence and it is accepted then. |
| During the immutable write | The blob driver's `WriteAll` is atomic per object: the object is either fully present or absent. A partial/torn object fails recovery validation → reported as corruption, **not** indexed → **not** accepted. |
| After the immutable write, before indexing | The record is durable. Recovery re-indexes it from the immutable object → accepted, replay high-water restored. |
| After indexing, before the manifest write | Accepted (the immutable record exists). The manifest is missing or stale; on the next start recovery **detects the drift** (the persisted manifest disagrees with the rebuilt-from-log truth) and the single writer **physically rewrites** the manifest via `RepairProjections`, restoring `ready` with no operator action. Replay stays enforced throughout. |
| During/after the manifest write | Accepted. The manifest is rebuildable and non-authoritative. |

The immutable write being the commit point is the invariant: a derived-state
failure **after** it degrades the store, it never loses or re-accepts the
envelope.

## Recovery

On startup the store rebuilds its replay and latest-target indexes **from the
immutable records alone** (`List` under `<prefix>/envelopes/`), and validates
every record before it re-enters the index. A record is reported as corruption —
tainting its producer, whose future writes are then refused — when it fails any
of:

- unknown record schema version;
- empty producer or envelope id;
- an object key that does not bind its `(producer, envelope id, sequence)` —
  catching a moved, re-producered, re-sequenced or re-identified object;
- an evidence digest that does not match the digest recomputed over the carried
  `EvidenceSet` — catching a tampered evidence body;
- a contract reference inconsistent with the one inside the evidence set;
- an **impossible history** — two committed records at the same producer
  sequence (a fork), or an envelope id committed more than once. Correct
  single-writer operation (strictly increasing per-producer sequences,
  commit-once envelopes) can never produce these, so their presence means the
  immutable log was tampered with or two writers forked it. The corruption reason
  never echoes a producer or envelope id.

Recovery also compares the persisted manifest projection against the
just-rebuilt-from-log truth. A manifest that is missing while records exist, is
unparsable, or whose record count disagrees with the log means the manifest
drifted (e.g. a crash between the immutable write and the manifest, across a
restart). The single active writer then **physically rewrites** the manifest from
the recovered index, so the store returns to `ready` without operator action.
There is no per-target projection to verify or repair: the per-target latest is
served from the in-memory index the recovery step just rebuilt from the log, so it
is authoritative-by-construction rather than a separately-persisted copy that
could silently drift. Read-only consumers (the fleet source, `pacto evidence
inspect`) never repair — only the writer that owns the bucket does, so a transient
reader cannot race it.

Readiness is gated on recovery: the server answers `/ready` = 503 and refuses
ingestion until recovery reaches `ready` (or `degraded`); liveness is independent.
Replay protection therefore **survives process and pod restarts**, because the
per-producer sequence high-water mark is rebuilt from the immutable records.

## Trust boundary

The bucket is a **single-writer trust boundary**. Concretely:

- The producer's envelope **signature is verified at ingest** (against the trust
  store), before the record is ever written.
- Recovery re-verifies **structural and identity integrity** and the **evidence
  digest** (above). It does **not** re-verify the envelope signature (that needs
  the trust store at recovery time — a future option) and does not re-derive the
  acceptor's own evaluation (findings, coverage, compliance) or the derived target
  key: those are **trusted from the bucket**.
- The bucket is private: a ReadWriteOnce PVC for `file://`, an IAM-scoped bucket
  for cloud backends. A full bucket-compromise adversary — one who can rewrite an
  object in place at its own correct key — is **out of this trust model**. (Note
  that tampering the evidence body is still caught by the evidence-digest check,
  and moving/re-keying an object by the key-binding check; the residual is an
  in-place rewrite of the acceptor's evaluation or target key by a party who
  already controls the bucket.)

## Consistency contract per backend

Recovery lists the immutable prefix once on a cold start. The single-writer model
means recovery never depends on `List` reflecting a write made by a *different*
process; in-process read-after-write is served from memory.

- **`file://` (default) — guaranteed.** A local filesystem is strongly
  consistent. Cold-restart recovery lists and reads the immutable records with no
  visibility lag, so durable recovery and replay-after-restart are correct without
  qualification. This is the shipped, tested-in-CI path (`mem://` and `file://`).
- **`s3://`, `gs://`, `azblob://` — supported, consistency delegated to the
  provider.** The identical List-based recovery is correct **because** Amazon S3,
  Google Cloud Storage and Azure Blob Storage all provide strong read-after-write
  **and** list-after-write consistency (S3 since December 2020; GCS and Azure are
  strongly consistent by design). Pacto does **not** run a live-cloud integration
  test in CI, so treat cloud backends as **beta**: correctness relies on the
  provider's documented strong consistency and on the single-active-writer rule.
  Never run more than one active writer per bucket + prefix.

### Alternatives considered and rejected

- **A provider-independent durable commit index / checkpoint.** Unnecessary:
  in-process read-after-write is already served from memory, and every advertised
  backend is strongly consistent, so a cold-start `List` over the immutable
  records is sufficient. It would add a second source of truth to keep consistent
  with the immutable records — complexity without a correctness gain under the
  single-writer model.
- **Provider-specific recovery code paths.** The gocloud abstraction plus the
  single-writer model make per-provider branches unnecessary; adding them would
  fork the tested `file://`/`mem://` path.

## Retention

Immutable accepted records are **never auto-deleted**. Any object-lifecycle policy
configured on the bucket **must not** delete anything under `<prefix>/envelopes/`
— doing so destroys the source of truth and corrupts replay/latest state on the
next recovery. The rebuildable manifest under `<prefix>/materialized/` may be
expired safely; recovery reconstructs it.
