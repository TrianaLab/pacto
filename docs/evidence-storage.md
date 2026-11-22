# Evidence storage

The Evidence Server stores every accepted report in your contract registry,
which is its only store. This page covers where a record goes on each kind of
registry and how a subject prefix accepts evidence for every revision under a
registry path. See [External evidence](evidence.md) for the protocol and
[Registry conformance](evidence.md#registry-conformance) for the permissions
and retention it needs.

## How evidence is stored

Pacto probes each registry once with the check delivery records use and picks where a new record goes:

| Registry | Referrers API | Evidence record |
|---|---|---|
| zot, Docker Hub, Harbor, ECR, ACR, Artifactory | Yes | A referrer of the contract revision |
| GHCR, CNCF distribution (`registry:2`, `registry:3`) | No | A write-once tag in `<prefix>pacto-evidence` |

The probed mode of each registry is the `Registry <host>` row in [Detected providers](operational-graph-providers.md#registry-rows). To check a registry yourself:

```bash
oras discover --distribution-spec v1.1-referrers-api <repo>@sha256:<digest>
```

A registry that serves Referrers answers `GET /v2/<name>/referrers/<digest>` with **HTTP 200** and an OCI image index; one that serves none answers `404`. When the probe cannot tell, the server stays not ready and refuses writes with `503`: an unknown answer never becomes a storage mode. Pacto never writes the shared `sha256-<digest>` referrers tag. Records in both placements are always read, so a registry that starts serving Referrers keeps its earlier records.

An exact subject on a registry without Referrers needs a subject prefix that covers it. Uncovered, the server stays not ready with `registry <host> serves no OCI Referrers: add a subject prefix covering <ref> to store its evidence as write-once tags in <prefix>pacto-evidence`.

---

## Subject prefixes

A subject prefix such as `oci://ghcr.io/acme/` accepts evidence for every immutable revision published under it, including revisions published after the server started, with no restart. Refs must still be `@sha256` digests. A prefix never covers a repository with a `pacto-inferred`, `pacto-targets`, `pacto-index` or `pacto-evidence` path segment.

The server keeps a ledger in `<prefix>pacto-evidence`: one write-once tag `evidence-<sha256 of the subject ref>` per revision that ever received evidence. On a registry without Referrers each record is a tag `evidence-<sha256 of the subject ref>-<12 hex of the record digest>` in the same repository. Tags are named by content, so a tag is never moved or reused. The ledger keeps the server stateless: after a restart it reads every subject that holds evidence back from the registry.

The registry credentials need push on `<prefix>pacto-evidence`. The ledger holds the refs and evidence of every revision under the prefix, so keep it private or no more visible than the most restricted repository under the prefix. Only the Evidence Server's credential and people allowed to see all of those repositories should read it. On GHCR the package takes its own visibility on the first push.

Keep your existing `subjects` when you add a prefix: records written before it have no ledger entry, so dropping a subject drops its history from the replay check. Set `allowedContractRepos` on each key. A key with none may report on every revision under the prefix, and the server names such keys in a startup warning:

```yaml
apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: adapter-2026
    producerId: platform-adapter
    publicKeyFile: platform-adapter__adapter-2026.pub
    allowedObservationKinds: [ResourceProvisioned]
    allowedContractRepos: [ghcr.io/acme/payments]
```
