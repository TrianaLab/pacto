# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-13
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`797a49b32d338e597c78b7834a3fd256e4ab648c`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

That review kept Phase 8 NARROWLY REOPENED a sixth time, on B4's ON-DISK
MIGRATION boundary (section 2, "Sixth narrow reopen"). The complete LocalOnly
`CachedRef` propagation, the cold/warm reference-agreement guard, the
RemoteAllowed miss-refetch-or-fail behaviour and resolve-once / pull-by-digest
are independently ACCEPTED and frozen, B5 is ACCEPTED and frozen, blocker A
stays CLOSED, the 14-fact two-snapshot live gate and journeys A–H stay accepted,
Phase 8B stays NOT STARTED, and every other Phase-8 acceptance stays frozen.

Commits appended on top of the reviewed HEAD `797a49b3`, oldest first:

- `b0020460` — a cache entry this version writes can never land on an older
  one's baseline (blocker B, B4's on-disk migration boundary)
- this document's own commit — persist the Phase-8 candidate after the sixth
  narrow reopen

Commits appended on top of the earlier reviewed HEAD `41fa3c02`:

- `d58a6f93` — a cache entry belongs to the reference it says it does (blocker
  B, B4's RemoteAllowed half; ACCEPTED and frozen at `797a49b3`)
- `797a49b3` — persist the Phase-8 candidate after the fifth narrow reopen

Commits appended on top of the earlier reviewed HEAD `80c0e92f`:

- `234f01f8` — a cached revision is named by the generation that served it
  (blocker B, B4's LocalOnly half; ACCEPTED and frozen at `41fa3c02`)
- `41fa3c02` — persist the Phase-8 candidate after the fourth narrow reopen

Commits appended on top of the earlier reviewed HEAD `1741318d`:

- `a1159be0` — a reader gets one cache generation, and the cache's life is
  three facts (blocker B, boundaries 4 and 5)
- `60fe9919` — persist the Phase-8 candidate after the third narrow reopen
- `80c0e92f` — record the review-thread and check state re-queried at `60fe9919`

Commits appended on top of the earlier reviewed HEAD `879724dc`:

- `6049f44e` — a cache entry is the bundle and its identity, bound to the bytes
  pulled (blocker B, boundaries 1 and 2)
- `caf88050` — the live gate proves the cache contributed, not that time passed
  (blocker B, boundary 3)
- `622ed857` — a cache the pulls fill is a source, whatever was in it at startup
  (the product defect boundary 3's gate caught; see section 8)
- `1741318d` — persist the Phase-8 candidate after the second narrow reopen

Commits appended on top of the earlier reviewed HEAD `6750c959`:

- `d8ef5d5a` — one published artifact is one revision, whatever found it
- `0cf0c69b` — a round of facts is only a fact if one snapshot answered all of it
- `879724dc` — persist the Phase-8 state after the narrow reopen

For completeness, the full appended range since the reviewed HEAD `5f3d4ebb`:

- `d098044b` — persist the independently reviewed PR state at `5f3d4ebb`
- `8b0f26b0` — a data source is exactly the one thing it declares itself to be
- `fa76b69a` — every value the chart accepts survives the trip to the dashboard
- `ae29a13d` — the source boundary is written down, and Kind proves it
- `eedab3f7` — the escaping source gets its own claim
- `4ba54a13` — persist the independently reviewed PR state at `eedab3f7`
- `b3075ece` — a refused identity is not a process that failed to start
- `de13fb2a` — the roadmap names the test-architecture debt before it grows
- `1a79ec32` — the fleet sees the contract revisions the cluster resolved
- `59632e22` — a controlled plain-HTTP registry is reachable by what the operator
  manages
- `0c67164d` — the newest published revision is content the product can analyze
- `f0cf50a6` — the live vertical publishes the revisions the product must reason
  about
- `731b9692` — the browser proves the product, not that a page rendered
- `a6755934` — the insecure-registry list iterates without materializing a slice
- `cfeebce8` — the generated helm reference names the insecure-registry value
- `3405d762` — a declared interface needs something to be observed against
- `2a622eb3` — the reference provider sizes its buffers without arithmetic
- `9bdab0b0` — a plain-HTTP registry answers which versions it holds
- `b5be4f4e` — a disclosure has no accessible name, so the test id is its handle
- `b8424175` — rebuild the UI bundle (generated)
- `7ffdf884` — an exact revision match over a scheme-less pin is not retrievable
  content
- `2c5034d8` — persist the Phase 8 candidate state and what it does not close
- `d18ca70e` — a port-forward is ready when it answers, not two seconds later
- `6750c959` — the Phase-8 candidate is verified on the fixed harness
- `d8ef5d5a`, `0cf0c69b`, `879724dc`, `6049f44e`, `caf88050`, `622ed857`,
  `1741318d`, `a1159be0`, `60fe9919`, `80c0e92f`, `234f01f8`, `41fa3c02`,
  `d58a6f93`, `797a49b3`, `b0020460` and this document's commit, as listed above

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

### Handoff discipline — still in force

Every handoff MUST enumerate EVERY commit between the reviewed starting SHA and
the final SHA of the iteration, including coordination-state and
generated-bundle commits. A commit that only touches `.pr-context/` is still a
commit an independent reviewer has to account for.

### Accepted at this review — do NOT reopen

The following are settled. Do not redesign them, and do not "improve" them as a
side effect of the current phase. Reopening any of them requires a NEW concrete
counterexample.

**Ownership identity and discovery**

- `OwnerKey` and the exact-versus-fuzzy owner semantics;
- contacts-only ownership without a fabricated canonical identity;
- the shared `OwnershipFact` presentation across Service, Revision and
  Operational Target detail;
- Revision contact inspection;
- contact-order and exact-duplicate normalization (`Owner.ContactSet`);
- owner ranking namespace ambiguity;
- the ownership aggregate semantics.

**Distribution primitive**

- `DistributionBar` under-count behaviour;
- `DistributionBar` ordinary over-count behaviour;
- `DistributionBar` authoritative-zero behaviour.

**Product surfaces**

- the Operational Overview dashboard generally;
- Data Sources as a secondary Product surface (four primary tabs unchanged);
- the Data Sources Overview section and its `PageToc` integration;
- source chips as exact links;
- the Data Sources inventory;
- selected-source health versus Fleet knowledge as distinct concepts;
- the Source-detail Product hierarchy;
- raw source records versus contributed Product entities;
- `SourceContribution` complete-population semantics;
- `PageToc`;
- Services aggregates;
- Readiness;
- typography;
- progressive disclosure;
- Graph / Change-analysis geometry.

**Model**

- lock v3.

**Knowledge population**

- `ProductMeta.SourceCounts` is the authoritative complete population for
  source-health arithmetic; `ProductMeta.Sources` is a bounded named preview.
  The Product renders one answer for one snapshot, at every level (Data Sources
  tally, `KnowledgeBanner`, knowledge severity).

**Cache identity — the LocalOnly repair at `234f01f8`**

- the bundle and the COMPLETE `CachedRef` come from one cache generation;
- a cold (disk) read and a warm (memory) read both preserve `Ref` and `Digest`;
- `CacheSource` derives bundle, `Digest`, `RequestedRef`, `Domain` and
  `ResolvedRef` from that one generation.

**Cache identity — the reference-agreement repair at `d58a6f93`**

- a cache entry states its own identity, and a hit requires it to AGREE with the
  reference asked for, on the cold disk read and on the warm memory read alike;
- under `RemoteAllowed` a disagreeing entry is a MISS that refetches, or an
  honest failure when the registry is unreachable — never mixed content;
- resolve-once then pull-by-digest.

**Phases**

- Phase 1 through Phase 7 are closed.

## 2. Current phase status

### Phase 1 — COMPLETE

### Phase 2 — COMPLETE

### Phase 3 — COMPLETE

### Phase 4 — COMPLETE

### Phase 5 — COMPLETE

### Phase 6 — COMPLETE

### Phase 7 — COMPLETE

Target delivered:

**operator-managed OFFLINE observation/trace-source configuration**

The offline pipeline (OTLP/JSON trace file, offline `pkg/otelobserver`, observed
edges, Fleet observation source, reconciliation) pre-existed and is accepted.
Phase 7 added the declarative, operator-managed way to package, configure and
mount those observation sources, with stable Data Source identity that does not
depend on list position. Phase 7 was NOT an OTLP receiver, a Collector, a trace
database or any live ingestion.

Everything about Phase 7 is closed and must not be redesigned without a new
counterexample: the offline analyzer boundary, the operator-managed
observation-source concept, PVC + ConfigMap as the only backings, read-only
mounts, no `hostPath`, deterministic sorting, reordering changing neither
identity nor pod template, complete removal, source failure as explicit
Fleet/Product knowledge, source health separate from observation freshness, the
retained ad-hoc `pacto dashboard --traces`, the focused Kind
observation-packaging scenario, and the existing architecture gates.

#### Blockers A, B and C — independently CLOSED at `eedab3f7`

The three defects that reopened Phase 7 at `5f3d4ebb` were independently
reviewed at `eedab3f7` and are accepted. They must NOT be reopened, and the
implementations behind them must NOT be redesigned:

- **A — whole-Fleet Data Source identity uniqueness.** `checkSourceIDsAreUnique`
  over the FINAL assembled source set, failing closed in `Service.Fleet` before
  `fleet.Build`, so no ambiguous Product Data Source key is ever published.
- **B — rooted observation-file reads / symlink escape prevention.**
  `os.OpenRoot` / `Root.ReadFile` against the declared source root, the
  single-segment managed file contract, projected-ConfigMap internal symlinks
  still working, no Kubernetes dependency in the offline parser.
- **C — Helm values -> operator flag -> parser configuration-wire fidelity.**
  The restricted lexical space, `ParseObservationSource`, the backing-name
  validation, and the Helm-rendering test that parses the ACTUAL rendered
  argument.

#### Final Phase-7 blocker — CLOSED at `b3075ece`

The public documentation claimed that on a Data Source identity collision
"Pacto refuses to start". That was never the implemented behavior, and the
runtime behavior was correctly NOT changed to make the prose true. The prose was
corrected to the implemented and accepted lifecycle:

- `Service.Fleet` detects duplicate source ids and returns an error BEFORE
  `fleet.Build`, so no ambiguous `FleetSnapshot` is ever published;
- `fleet.Manager.Start` performs `Refresh`, and a refresh failure is not
  process-fatal: the last good snapshot is retained when one exists, there is no
  snapshot when the first refresh has never succeeded, and the manager keeps
  running and can retry;
- the dashboard HTTP host itself stays alive.

"The process failed to start" is not equivalent to "an ambiguous Product identity
was refused publication". The second is the invariant Phase 7 requires and
implements, and it is what the documentation now says.

#### Accepted scoped deviation — carried into Phase 8

The live Kind observation scenario asserts the observed edge under its declared
identity and that it names the same pair the operator reconciled as declared,
but not the snapshot's `reconciliation: "matched"` verdict, which needs a
contract-REVISION source the operator-managed dashboard does not have in that
scenario (the live Kubernetes source projects deployed targets, not revisions).
That verdict over an observation source stays proven hermetically in
`internal/app` and by `make demo-fleet`. The fully live declared+observed
Product reconciliation is **Phase 8 work**.

### Phase 8 — CANDIDATE, NOT independently reviewed

Scope as commissioned: canonical LIVE Kind PRODUCT acceptance. Upgrade the
EXISTING live Kind vertical from a deliberate browser SMOKE check into
representative live Product acceptance: real OCI contract revisions published to
the in-cluster registry, digest-pinned operator resolution, a managed observation
source in the SAME operator-managed dashboard, live declared+observed
reconciliation reaching `matched` against the real Product API, real Change
analysis over two canonical revisions, and the existing external signed-evidence
target preserved.

Not another Kind vertical. Not a test-architecture refactor. Not Phase 8B.

Implemented at `d18ca70e`, NARROWLY REOPENED by the independent review at
`6750c959` on two counterexamples, re-implemented at `0cf0c69b`, still narrowly
reopened by the review at `879724dc` on three blocker-B boundaries, and
re-implemented at `caf88050` and `622ed857`. This is a Claude self-report and closes nothing:
the phase is a CANDIDATE until an independent review says otherwise.

#### Narrow reopen at `6750c959` — blockers A and B, closed at `0cf0c69b`

Everything else accepted in Phase 8 was FROZEN for this pass and is unchanged:
the real checkout A/B, orders and payments bundles; the digest-pinned Pacto CRs;
per-refresh Kubernetes-to-OCI discovery; the controlled insecure-registry
plumbing; the real checkout interface binding; the named managed observation
source; backend-authoritative matched reconciliation; the real checkout A-to-B
Change analysis and orders impact; the external Evidence Server target; the
Go/Playwright responsibility split; journeys A-H; and the shared port-forward
readiness fix.

**Blocker A — `productready` did not prove ONE coherent snapshot.** The prober
made a dozen Product requests, discarded every response's `Meta.SnapshotID` and
read an Overview id only after the semantic checks were already done. A Manager
refresh between any two requests spliced facts from different snapshots, the
gate passed on a fleet that never existed, and `PW_FIXTURE.snapshotId` named a
snapshot that had proved nothing.

Closed at `0cf0c69b`. A round now ADOPTS the id of its first response, and every
later response in that round must repeat it; a different id, or no id at all,
discards the whole round and retries, and a discarded round cannot emit fixture
keys. Snapshot coherence is fact 13 of 13. The invariant is pinned by adversarial
Go tests over a controlled Product server in
`tests/e2e/kind/productready/main_test.go`: one coherent id passes; a changed id
on a list, on a detail, and on the neighborhood each fail; a response naming no
snapshot fails; a first-response outlier fails; a spliced round fails even when
every individual fact holds; and a failed mixed round emits no keys. No sleep and
no timing assumption is part of the invariant.

**Blocker B — `IncludeCache` plus OCI published two identities for one real
revision.** Once a registry pull had populated the pod cache, `CacheSource`
reconstructed a reference from the cache PATH — and `cachePath` maps every `:`
to `/`, so the path cannot say where a registry host ends, nor whether `:1.0.0`
is a tag. The reconstruction invented a domain and carried no manifest digest,
so `fleet.revisionFrom` keyed it by a derived content digest while `OCISource`
supplied the SAME published artifact under its manifest digest: two
`RevisionKey`s, the second an unresolved shadow.

Closed at `0cf0c69b`. Identity is RECORDED at pull time in a `ref.json` sidecar
beside the bundle (`oci.CachedRef`), so the cache reader agrees with the registry
instead of guessing from a filename, and both sources emit one `RevisionKey`
whose `Sources` union names both. The sidecar is written BEFORE `bundle.tar.gz`,
because a cache walker keys on the bundle: an artifact can never be visible
before the record of what it is. Offline stays offline — the digest is read from
disk, and a disconnected build makes zero registry calls. Nothing is collapsed by
service and version: two genuinely different immutable digests declaring the same
version remain two revisions, because that is a re-published tag, not a
collision. Pinned by production-level tests in
`tests/e2e/fleet_cache_identity_test.go` (real registry, real `pacto push`, real
`CachedStore`, real `app.Service.Fleet`): a cold build plants no shadow, a warm
cache-plus-OCI build yields exactly one canonical revision per artifact with both
sources and an `oci://...@sha256:` pinned ref, two distinct digests at one version
stay two revisions, and a cache-only build is available, network-free and still
digest-exact.

The gate enforces the same invariant live: each fixture service/version must
resolve to EXACTLY ONE revision, which must then be `exact` and retrievable. Two
revisions fail the round; the gate never picks the first retrievable match.

The Kind vertical now runs `productready -snapshots 2`, so the browser layer only
starts after TWO DISTINCT snapshots have each proved all thirteen facts. The pod's
OCI cache starts empty and the first refresh's pulls are what fill it, so only a
later snapshot has the registry and the now-populated cache contributing the same
artifacts — the post-cache state blocker B lives in. It is reached by observing
it, not by sleeping. A pod restart would be the wrong lever: the operator mounts
the dashboard cache as an `emptyDir`, so restarting ERASES the state under test.

One CI-reachability defect was fixed with them: the gate is a Go program under
`/tests/`, which the coverage leg excludes and the e2e leg did not match, so its
tests never ran anywhere. `make e2e` runs them now.

What the live cluster now proves, in the cluster and in the browser:

- four bundles published to the in-cluster registry through the real `pacto push`
  (payments, checkout 1.0.0, checkout 1.1.0, orders), each resolved to an
  immutable manifest digest;
- both Pacto CRs resolve `contractRef.oci` at a digest, so the operator's
  `status.contract.resolvedRef` is a real resolved contract identity and the
  dashboard reaches the SAME content back through the registry;
- `tests/e2e/kind/productready` (Go) gates the browser layer on thirteen facts
  re-checked every round against the live Product API — and, since `0cf0c69b`,
  on TWO distinct snapshots each proving all of them — and emits the keys it
  DISCOVERED as `PW_FIXTURE`, so no browser journey constructs an identity;
- eight live Product journeys (A–H) in `pkg/dashboard/frontend/e2e-live/`, all
  passing on the Kind `operational-graph` shard at `d18ca70e` and again at
  `0cf0c69b`, where they run only after the post-cache snapshot.

Shell classification, as section 12 of the commission requires. Every shell
change in Phase 8 is thin orchestration; none of it parses JSON, decides
ontology or owns retry semantics — those live in `tests/e2e/kind/productready`
(Go) and in the Playwright specs:

- the fixture bring-up added to `tests/e2e/kind/operational-graph.sh` (four real
  `pacto push` invocations, two Pacto CRs, one ConfigMap, Helm values) is a
  sequence of real CLI calls;
- `pf` moved to `tests/e2e/kind/lib.sh` at `d18ca70e` and now waits for the
  forward to answer instead of sleeping two seconds. This is a bug fix, not the
  Phase 8B consolidation: `pf` was the one helper duplicated verbatim in three
  scripts AND the direct cause of a shard failure, so it was fixed at the root
  rather than patched in the one script that happened to fail. The rest of the
  duplicated lifecycle named in TARGET section 10 — cluster setup, Helm
  invocation, eventually-helpers, cleanup, diagnostics — is UNTOUCHED and remains
  Phase 8B's inventory to do properly.

Disclosed, NOT fixed, and NOT in Phase 8 scope — for independent triage:

- every Kubernetes target's Content badge reads `Local reference (not
  retrievable)` even when the operator pinned a digest, because the operator
  writes `status.contract.resolvedRef` WITHOUT the `oci://` scheme and
  `classifyOCIRef` will not read a scheme-less string as a canonical ref. The
  revision-match dimension is unaffected (`exact`), the pairing is documented in
  `pkg/fleet/ref.go` and pinned by `TestTargetIdentity_ExactMatch_NonRetrievable`
  with that exact operator shape, and journey D now asserts the true live state.
  Whether the operator SHOULD emit a canonical `oci://` ref is a product question,
  not a Phase-8 change.
- `pkg/dashboard/frontend/e2e-live/` is NOT type-checked: `tsconfig.json`
  `include` is `src/**`, so `npm run lint` never sees the live specs. A type error
  there surfaces only when Playwright transpiles it inside a Kind shard.
- `helm-docs-check` rewrites `charts/pacto-dev-gateway/README.md` as a side
  effect of running; the file must be restored before committing.

The fourth disclosure carried at `d18ca70e` — `IncludeCache` plus the OCI source
minting a second `RevisionKey` for the same content — became blocker B of the
narrow reopen and is closed above. The shell classification is unchanged by this
pass: `tests/e2e/kind/operational-graph.sh` gained one flag and a comment, still
thin orchestration, and no new harness was created.

#### Second narrow reopen at `879724dc` — blocker A CLOSED, three blocker-B boundaries, re-implemented at `caf88050`

The review at `879724dc` CLOSED blocker A independently: `productready`'s
same-round `SnapshotID` coherence is settled and was NOT redesigned in this
pass. Every other accepted Phase-8 behaviour stayed frozen, Phase 8B was not
begun, and no new shell harness, fixture-only Product shortcut, fixed sleep,
pod restart or Playwright re-derivation was added.

Blocker B was left open on three boundaries. The `0cf0c69b` sidecar made the
cache reader agree with the registry; it did not make the ENTRY coherent, did
not bind the recorded digest to the bytes that were actually downloaded, and
the live gate proved neither.

**B1 — a cache entry could be half a thing.** `CachedStore.Pull` discarded the
errors from `saveRefRecord` and `saveToCache` and wrote both files in place. A
sidecar that could not be written — `ref.json` already exists as a directory —
still let `bundle.tar.gz` be published, and an interruption between the two
paired a fresh sidecar with the previous pull's bytes. Either shape puts a
walker-visible bundle next to an identity that is missing or wrong, which is
exactly the guess-from-the-path duplicate blocker B is about.

Closed at `6049f44e`. `writeCacheEntry` stages both files in a temporary
directory under the cache's PARENT — outside the tree walkers scan — and
commits with one `os.Rename`. A reader sees the old entry or the new entry,
never a mixture; any failure leaves the entry ABSENT, an ordinary miss the next
pull repairs, and never corrupts a coherent entry that was already there. The
persistence failure is now logged instead of dropped. `writeBundleFile` reports
`Close`, so a gzip flush that fails cannot commit a truncated archive under a
valid identity.

**B2 — the digest was a second observation of a moving target.** `Pull(tag)`
followed by `Resolve(tag)`, and a later `Resolve(tag)` again, are separate
questions to a MUTABLE reference. Re-pushed in between, the resolve answers
with the digest of an artifact this snapshot never read, and the revision then
claims an immutable identity for content that does not have it.

Closed at `6049f44e`. `resolveAndPull` resolves the reference ONCE and fetches
the content from the digest-pinned reference that resolve named, so digest and
bytes agree by construction; `CachedStore.PullPinned` and
`Resolver.ResolvePinned` return the two together, and `fleetsrc` uses the
digest that came back WITH the bundle instead of asking again. A memory or disk
hit reports the digest RECORDED with those bytes. The originally requested ref
is preserved as provenance: it remains the cache key and the sidecar's `Ref`,
so pinning changes what is fetched, never what the entry is called. A registry
that will not answer leaves the identity unknown and the tag is fetched as
written — real content, no claimed digest. `LocalOnly` reports no digest of its
own, so the offline `CacheSource` stays network-free and digest-exact, and two
genuinely distinct digests declaring one version remain two revisions.

**B3 — `-snapshots 2` could not prove the post-cache state.** A `SnapshotID`
hashes the generation time, so distinct ids prove refreshes happened, not that
the cache contributed anything.

Closed at `caf88050`. The gate now requires the STATE: the cache source present
and available, and each of checkout 1.0.0, checkout 1.1.0 and orders 1.0.0
naming BOTH the configured OCI source and the cache source in
`Revision.Provenance.Sources` while staying exactly one canonical, exact,
retrievable revision. That union cannot hold unless both sources resolved the
same published artifact to the same identity. `-snapshots 2` remains as a
stability requirement on top, not as the proof. Same-round `SnapshotID`
coherence is untouched and still covers every response, now over fourteen
facts.

Adversarial tests, each of which fails if ONLY the production correction is
reverted:

- `pkg/oci/cache_coherence_test.go` — a failed commit publishes no bundle
  without its identity; a failed overwrite leaves the coherent entry that was
  already there rather than pairing a new sidecar with old bytes; a tag that
  moves between the pull and the question yields the digest of the bytes that
  were read; a cache hit reports the recorded digest; and the pinned resolver
  carries the binding the store made.
- `pkg/oci/cache_internal_test.go` — every way the commit can fail is reported,
  including a sidecar path that cannot be created and a bundle that cannot be
  staged, and a failed staging publishes nothing.
- `tests/e2e/fleet_cache_identity_test.go` —
  `TestFleetMovingTagBindsDigestToTheBytesPulled` uses a REAL registry and a
  real `pacto push --force` that moves the tag during the build: the fleet
  still holds exactly one checkout revision, its digest names the artifact the
  revision actually carries, its `ResolvedRef` is pinned to that digest, its
  requested ref is preserved, and a later offline build over a dead registry
  reports the same digest and the same content.
- `tests/e2e/kind/productready/main_test.go` — arbitrarily many distinct
  coherent snapshots without cache provenance cannot pass the gate, and a
  coherent post-cache snapshot naming both sources does.

**What B3's gate then caught — a real product defect, fixed at `622ed857`.**
The first CI run of the new gate FAILED, on the product, not on the harness:
`data source "cache" is not in the snapshot (present: evidence-http, k8s, oci,
orders-traces)`. `internal/cli/dashboard.go` decided `IncludeCache` from what
startup DETECTION found on disk, and an operator-managed dashboard's cache is an
`emptyDir` created with the pod. Empty once meant absent for the life of the
process, so the offline baseline the dashboard's own pulls were filling never
contributed a single revision, and the registry stayed the only thing that could
answer. `withClusterContractRefs` now treats the cache as a property of the
REFRESH: whenever there is an OCI ref to pull, the directory those pulls write
into is asked. An absent or empty cache collects nothing and reports no error,
so asking when there is nothing there costs nothing.
`internal/cli/fleet_wiring_test.go` covers the four cases — refresh-discovered
refs enable it, explicitly configured refs enable it with no discovery, no OCI
refs must NOT add a cache source, and an already-enabled cache stays enabled.

This is the direct evidence that boundary 3 was worth its own blocker: two
distinct coherent snapshots had been passing for the whole previous pass while
the product never read its own cache. `-snapshots 2` could not see it. The
state requirement saw it on the first run.

The shell classification is unchanged again: `tests/e2e/kind/operational-graph.sh`
gained only a rewritten rationale comment. The Kind vertical, its thin
orchestration and `-snapshots 2` all stand.

#### Third narrow reopen at `1741318d` — two blocker-B boundaries, CANDIDATE

The review at `1741318d` kept blocker A CLOSED, accepted the registry
resolve-once / pull-by-digest binding and accepted the direct 14-fact post-cache
gate. It left two boundaries open. Phase 8B was not begun, `productready`'s
provenance checks and the `-snapshots 2` stability requirement were not touched,
and no sleep, shell harness, pod restart, fixture shortcut or Playwright-side
derivation was added.

**B4 — a reader could still mix two coherent generations.** `writeCacheEntry`
commits a generation whole, but that makes each GENERATION coherent, not each
READER. Two files opened by pathname are two observations, and the disk cache is
shared between processes and between store instances, so a mutex on one
`CachedStore` proves nothing. Two deterministic counterexamples: `pullCached`
finished reading bundle A, another writer installed generation B, and the
subsequent `ReadCachedRef` returned bundle A under digest B; and `CacheSource`
walked the cache recording sidecar A, a writer installed B, the LocalOnly
resolution read bundle B, and `collectRefs` then overwrote its digest with the
walk's entry.Digest A — the same lie in the other direction.

Closed by reading ONE generation through ONE directory handle. `readCacheEntry`
opens the entry directory with `os.OpenRoot` and takes both the bundle and the
sidecar from that handle. A generation swapped out from under the handle is
unlinked, not rewritten: its files keep answering, and once `RemoveAll` has taken
them they read as ABSENT rather than replaced, which `os.SameFile` against the
installed directory distinguishes from an entry that simply never had a sidecar
— so pre-sidecar entries stay readable and identity-less, exactly as intended.
Absent is coherent: retry the successor, at most `cacheEntryAttempts` times, and
a writer that keeps winning yields a cache MISS, never a mixture. The inverse
case is closed at the source: `Resolver.ResolvePinned` in `LocalOnly` now
reports the digest the store read back WITH the bytes
(`CachedStore.PullCachedPinned`), and `collectRefs`'s walk-time digest override
is deleted. `cachedRefs` now says only WHICH entries exist; what they are and
what they hold arrive together, later, from one generation.

Preserved unchanged: best-effort persistence after a successful registry pull, no
walker-visible partial entry, network-free offline reads, digest-bound
mutable-tag behaviour, and old-cache compatibility.

- `pkg/oci/cache_generation_test.go` — barrier-driven, two real `CachedStore`
  instances over one real disk cache: the writer commits generation B exactly
  between the reader's bundle read and its identity read, and the reader must
  answer B/B; a writer that wins three times running gives a miss, with every
  attempt consumed; a sidecar-less entry is compatible, not a swapped
  generation. With the staging-directory writer alone — the sidecar re-read by
  pathname — the first test fails with `the reader returned the bytes of "gen-a"
  under digest sha256:bbb, which holds "gen-b"`.
- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_TheWalkAndTheReadAreOneGeneration` drives the inverse
  interleaving through the real walk and the real disk read: generation B is
  committed by a second `CachedStore` after the walk has recorded A. Restoring
  the walk-time override fails it with `the revision carries the bytes of
  "gen-b" under digest sha256:aaa…, which holds "gen-a"`.

**B5 — one expression stood for three different facts.**
`opts.IncludeCache = opts.IncludeCache || len(opts.OCIRefs) > 0` is a
discoverability test doing a lifecycle's job. In the operator-startup path a
later Kubernetes failure or empty result takes the offline baseline away exactly
when it is the only thing that can answer; and the same expression turns
`CacheSource` on for explicit OCI refs under `--no-cache`, publishing a source
over entries the store then refuses to read — a partial baseline made of
limitations.

Closed by representing the three facts separately. `cacheLifecycle` carries
`permitted` (`--no-cache` excluded whatever the cache already held), `baseline`
(startup detection found content) and `materialized` (this process has committed
an entry, which `CachedStore.Materialized` reports and a pod's emptyDir cache
cannot); `contributes()` is `permitted && (baseline || materialized())`.
Discovery is no longer part of the answer.

- `internal/cli/fleet_wiring_test.go` — sequential, production-wired:
  A. a startup-empty cache, refs that appear on refresh 2, real pulls that fill
  it, then a registry taken offline and a refresh that discovers nothing — the
  cache source is still present, available and contributing revisions offline,
  and a cold second `CachedStore` over the same directory proves the entries are
  on disk; B. `--no-cache` over pre-existing entries publishes neither those
  entries nor a partial source, while this session's own pull is still served,
  keeping the documented cold-start behaviour; C. a dashboard whose store cannot
  cache invents no cache source, even with a discovered ref. All four
  `cacheLifecycle` cases plus all three scenarios fail against the old
  expression.

#### Fourth narrow reopen at `80c0e92f` — B4's remaining boundary, CANDIDATE

The review at `80c0e92f` accepted B5 independently and froze it, and left B4
open on ONE boundary: the reader bound bundle bytes and `Digest` to a single
directory generation, but the sidecar's `Ref` from that same generation never
reached the fleet. `PullCachedPinned` exposed bundle + digest and dropped
`CachedRef.Ref`, so `collectRefs` still built `RequestedRef`, `Domain` and the
canonical `ResolvedRef` from the reference the WALK had recorded, while the
digest came from the later read.

`cachePath` maps every ':' to '/', so it is not injective, and these two
references name one entry directory:

- `localhost:5000/demo/checkout:1.0.0`
- `localhost/5000/demo/checkout:1.0.0`

Install the first, let `cachedRefs` record its sidecar, install the second
through a second real `CachedStore`, and the cold reader answered with the
second generation's bytes and digest under the first generation's repository,
domain and canonical revision key: a revision belonging to no published
artifact. The existing generation test could not see it because both of its
generations used the same reference and it asserted only bundle/digest
coherence.

Closed by carrying the WHOLE record from the generation that supplied the bytes.
`CachedStore.PullCachedPinned` and `Resolver.ResolvePinned` in `LocalOnly` now
return the `CachedRef` (`Ref` and `Digest`) read through the one directory
handle; the in-memory pull cache stores that record beside the bundle instead of
a bare digest, so a warm hit answers with what the entry said and never with the
key it was looked up under; and `collectRefs` derives `RequestedRef`, `Domain`
and the pinned `ResolvedRef` from it whenever the entry states a reference. The
walk is left doing only what it can do honestly — discovering which entries
exist. A record with no reference is impossible to pair with a digest
(`parseCachedRef` rejects a sidecar without `ref`), so the mixed revision has no
remaining path: an entry either states its identity or is identity-less.

Preserved unchanged: pre-sidecar entries stay readable, identity-less and
path-approximate; remote resolution still reports the digest of the one pull
that fetched the bytes, and its `CachedRef.Ref` is empty because no cache
generation is claiming a different reference; offline reads make no network
call; mutable tags stay bound to the digest recorded at pull time; B5 and every
accepted live Product acceptance are untouched.

- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_IdentityComesFromTheGenerationThatServedTheBytes`:
  the two-reference counterexample above on a real disk cache with two real
  `CachedStore` instances, asserting the COMPLETE identity — bundle, `Digest`,
  `RequestedRef`, `Domain`, `ResolvedRef` — belongs to one generation. On the
  production code at `80c0e92f` it fails with `RequestedRef =
  "localhost:5000/demo/checkout:1.0.0", but the bytes came from the generation
  pulled as "localhost/5000/demo/checkout:1.0.0"`, plus the matching `Domain`
  and `ResolvedRef` failures.
- `pkg/oci/cache_generation_test.go` —
  `TestPullCachedPinned_AWarmReadKeepsTheRecordedRef`: an entry installed under
  one spelling and read under the other returns the complete record on the cold
  (disk) read AND on the warm (memory) read. Reducing the memory hit to
  `CachedRef{Digest: e.rec.Digest}` fails the warm leg alone, which is the
  regression this leg exists to catch.

Verification for this candidate: the two counterfactuals with `-race`; `go test
-race ./...`; `make lint`; `make coverage` at 100.0%; `make e2e`; `make
demo-fleet`. The six Kind shards run in CI. Still disclosed and out of scope:
the CodeQL PR-ref findings and the Evidence Server read-only-cache warning.

#### Fifth narrow reopen at `41fa3c02` — B4's RemoteAllowed half, CANDIDATE

The review at `41fa3c02` accepted the LocalOnly repair independently and froze
it, and left B4 open on its other half: `RemoteAllowed` still published a mixed
revision through the same aliased entry directory.

`OCISource(refA)` → `Resolver.ResolvePinned(RemoteAllowed)` →
`CachedStore.PullPinned(refA)` → `pullCached(refA)` reads bundle B and
`CachedRef{Ref: refB, Digest: digestB}` from the shared directory → `PullPinned`
returns the digest alone → `resolveWithFetch` has no `Ref` to carry, so it
reports `CachedRef{Digest: digestB}` → `collectRefs` keeps `refA` and publishes
B's bytes, B's digest and B's canonical key under A's `RequestedRef` and A's
`Domain`. The resolver comment claiming that RemoteAllowed bytes came from the
registry under the reference the call named was false on a `CachedStore` hit.

Closed by two independent mechanisms, because the alias is a root cause and the
entries it already wrote are still on disk.

The on-disk key is now INJECTIVE. `CachedStore.entryDir` spells only a TAG's
':' as a separator; every other one — a registry port, a digest algorithm — is
escaped inside its path segment along with the '%' the escaping uses, and the
tag always gets a segment of its own (`%00` when there is none). Distinct
references can no longer name one directory, so pulling either of an aliasing
pair no longer destroys the other's offline baseline. A reference with neither a
port nor a digest keeps the path it has always had, so no existing entry is
stranded or duplicated; `legacyEntryDir` is still READ, and an entry
re-committed under the new key retires its own superseded directory —
`retireLegacyEntry` never touches one whose sidecar names a different reference.

And a lookup reports a hit only when the entry's recorded reference AGREES with
the one asked for. Disagreement is a MISS: online the registry is asked for the
artifact actually wanted, offline the caller is told it is not cached, and
either way no revision is published for bytes that belong to something else. An
entry that states nothing — written before the sidecar existed — contradicts
nothing and is served identity-less, exactly as before. The guard sits in
`pullCached`, above both the memory and the disk leg, so a warm read can never
answer differently from the cold read that filled it.

The second cache walker was inventoried and repaired with it:
`pkg/dashboard.CacheSource.buildIndex` derived repository and tag from the path
alone, which under the new key spells a ported registry as
`localhost%3A5000/org/name`. It now prefers the recorded reference, and falls
back to the path only for an entry that records no tag. The full inventory of
cache-path readers, writers and walkers is `pkg/oci/cache.go` (writer and
reader), `internal/fleetsrc.cachedRefs` (sidecar-first already),
`pkg/dashboard.CacheSource.buildIndex` (now sidecar-first); `internal/app`,
`internal/cli/dashboard.go` and `pkg/dashboard/detect.go` only resolve the cache
DIRECTORY, and `internal/update/check.go` is an unrelated update-check file.

Preserved unchanged: resolve-once then pull-by-digest; the complete LocalOnly
`CachedRef`; zero-network offline reads; a MATCHING cache hit still asks no
registry; the digest recorded at pull time still binds a mutable tag; ordinary
remote `RequestedRef` behaviour; B5 and every accepted live Product behaviour.

- `internal/fleetsrc/oci_test.go` —
  `TestOCISource_Collect_ACachedAliasIsNotThisReferencesRevision`: the
  production-wired counterexample. A real disk cache, a separate real
  `CachedStore` installing B under `refB`, an `OCISource` for `refA` in
  `RemoteAllowed`, and a real registry holding A under `refA`; bundle, `Digest`,
  `RequestedRef`, `Domain` and `ResolvedRef` must all describe A, and B's
  baseline is asserted intact afterwards. Without the guard it fails with
  `bundle is "gen-b"` and B's digest and canonical key under `refA`.
- `pkg/oci/cache_alias_test.go` — the alias entry is a miss on the cold disk
  read AND on the warm memory read (the disk entry is removed between the two,
  so only memory can answer; moving the guard onto the disk leg alone fails the
  warm leg by itself); `RemoteAllowed` refetches from the registry; a MATCHING
  hit asks no registry at all; a mismatch with the registry unreachable fails
  honestly rather than mixing; two aliasing references keep separate baselines
  across a cold `CachedStore` restart; the superseded legacy entry is retired,
  and a retirement that cannot happen costs a warning, not the pull.
- `pkg/oci/cache_internal_test.go` — nine references, no two of which spell to
  one directory, plus the proof that a port-free reference did not move.
- `pkg/dashboard/source_cache_test.go` — the index takes the ported reference
  from the sidecar and keeps the path only for an entry recording no tag.

Verification for this candidate: the counterfactuals with `-race` (including the
two mutation checks above); `go test -race ./...`; `make lint`; `make coverage`
at 100.0%; `make e2e`; `make demo-fleet`. The six Kind shards run in CI. Still
disclosed and out of scope: the CodeQL PR-ref findings and the Evidence Server
read-only-cache warning.

#### Sixth narrow reopen at `797a49b3` — B4's on-disk migration boundary, CANDIDATE

The review at `797a49b3` accepted the whole runtime half of B4 and froze it, and
left B4 open where the fix meets an EXISTING cache on disk. `entryDir` had been
made injective among the keys THIS version writes; its range still overlapped
the LEGACY namespace, so a new key could name another reference's old entry:

```
refA = localhost:5000/demo/checkout:1.0.0
refB = localhost/5000/demo/checkout:1.0.0
legacyEntryDir(refA) == entryDir(refB)
```

An upgrade destroyed a baseline nothing could see coming. A real legacy entry
for `refA` — complete sidecar and bundle — sits at `legacyEntryDir(refA)`; the
new implementation starts; the first pull of `refB` writes to `entryDir(refB)`,
which is that same directory, and re-commits it as `refB` before
`retireLegacyEntry` is ever consulted. A cold offline reader can no longer read
`refA` at all. The pre-existing persistence test proved only the opposite order
(a NEW `refB` entry, then `refA` written), which the injective key already
covered.

Closed by making the namespaces DISJOINT rather than each injective, and by
deleting the destructive retirement instead of trying to make it safe.

Every entry this version writes now lives under a reserved `_v2` segment
(`entryNamespace`). A legacy key is the reference with ':' spelled '/', so its
first segment is the reference's first component, and an OCI reference component
must begin with an alphanumeric: no valid reference can produce a legacy key
under a segment starting with '_'. Nothing this version commits can therefore
land on a baseline an earlier one left, for ANY pair of references, not just the
ones a test enumerates. The same reservation already backs the `_invalid`
sentinel in `CachedStore.contained`. Backward compatibility comes from still
READING the legacy path (`entryDirs` returns the new directory and then the
legacy one), never from writing to it.

Nothing retires a legacy entry any more. Retirement read a sidecar BY PATHNAME
and later called `RemoveAll` on that pathname, and the disk cache is shared:
between the two operations another process installs a whole generation, and the
removal takes a foreign one the read never saw. A sidecar-less legacy entry is
worse — its owner is unknowable, so "it must be ours" is a guess that costs a
stranger their only offline baseline. No destructive eager retirement is the
narrow-fix choice the review asked for; stale bytes stay until the cache is
cleared, which is what a cache is for.

The duplicate a surviving legacy entry would otherwise cause is a WALKER problem
and is solved in both walkers, keyed on the COMPLETE recorded identity
(reference plus digest): `internal/fleetsrc.cachedRefs` reports a reference once
however many entries hold it, and `pkg/dashboard.CacheSource.buildIndex` indexes
`ref@digest` once.

`buildIndex` was also still rebuilding the reference from the encoded path,
which for a real untagged entry (`%00`) emitted `repo:%00`. The exact recorded
`Ref` is now carried on the cached version separately from the display/version
key, and the key is derived FROM that reference — digest for a digest pin, tag
for a tagged reference, the contract's own version for a bare repository — never
from the path.

Preserved unchanged: the complete LocalOnly `CachedRef`; the cold/warm agreement
guard; RemoteAllowed miss-refetch-or-fail; resolve-once then pull-by-digest;
zero-network offline reads; B5; the 14-fact two-snapshot live gate; journeys
A–H.

- `pkg/oci/cache_alias_test.go` —
  `TestCachedStore_AnUpgradePullDoesNotDestroyALegacyBaseline`: the upgrade
  counterexample in BOTH migration orders (legacy A then B pulled, legacy B then
  A pulled), with real `CachedStore`s, a real seeded legacy entry and cold
  offline readers after a restart; both references must come back with their own
  bundle AND their own digest. It fails on `d58a6f93` in the first order
  (`refA`'s baseline is gone: `not cached`), which is the exact sequence the
  review specified.
- `pkg/oci/cache_alias_test.go` —
  `TestEntryDir_TheNewNamespaceIsDisjointFromEveryLegacyKey`: a cross-product
  over ten valid references (ported, port-free, nested, untagged, digest-pinned,
  prerelease-tagged, and both spellings of the aliasing pair) proving
  `entryDir(a) != legacyEntryDir(b)` for every pair INCLUDING `a == b`, and
  `entryDir(a) != entryDir(b)` for distinct references.
- `pkg/oci/cache_internal_test.go` — `entryDirs` returns the new entry first and
  the legacy one it must still read second.
- `pkg/dashboard/source_cache_test.go` —
  `TestCacheSource_ScanReportsTheRecordedReference` over entries produced by a
  REAL `CachedStore`: tagged, ported, untagged and digest-pinned. Each asserts
  the exact recorded `Ref` and the derived version key (`1.0.0`, `2.0.0`, the
  contract's `9.9.9` for the bare repository, `sha256:abc123` for the pin).
  `TestCacheSource_OneArtifactIsIndexedOnce` covers the walker dedupe.
- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_OneReferenceIsCollectedOnce`: the same artifact under
  the `_v2` key and the legacy key yields exactly one revision.
- `pkg/oci/cache_test.go` — the unused `legacyCachedDir` helper that failed
  `ci-static` at `797a49b3` is gone.

Verification for this candidate is in section 8. Still disclosed and out of
scope: the CodeQL PR-ref findings and the Evidence Server read-only-cache
warning.

### Phase 8B — NOT STARTED

Test architecture & harness consolidation. See TARGET section 10. Phase 8B MUST
close before Phase 9 or Phase 10 add their new acceptance harnesses.

### Phase 9 — NOT STARTED

Real built MkDocs browser E2E.

### Phase 10 — NOT STARTED

Docker Desktop/containerd/local-registry Kind path.

### Phase 11 — NOT STARTED

MCP catalog core: bounded multi-root catalog semantics over arbitrary Pacto
contract roots. Distinct from the operational Fleet MCP tools this branch
already ships.

### Phase 12 — NOT STARTED

MCP catalog discovery server/CLI/E2E/docs.

### Phase 13 — NOT STARTED

Normative invariants finalization.

### Phase 14 — NOT STARTED

Finalization, final ontology audit, repository hygiene, PR body, exact SHA, readiness.

## 3. Branch-hygiene regression — CLOSED

The four local-agent tooling files (`.claude/CLAUDE.md`, `.codex/config.toml`,
`.mcp.json`, `AGENTS.md`) were untracked by the append-only commit `ef67f494`
and remain locally as untracked tooling. The incidental `go.work.sum` entries
were reverted by `fffd9a39`, and the 47MB tracked build output by `ccf2ae0e`.
No history was rewritten.

The rule stands for every later phase: local/agent tooling, caches, plans and
workstation paths must never become tracked files of this PR.

## 4. Accepted Product architecture

Do not reopen these decisions without a new concrete counterexample.

### Canonical entities

- Service
- Contract Revision
- Operational Target

They remain distinct.

### User-facing terminology

- `target` internal kind -> **Operational target**
- `source` user-facing -> **Data source**
- Collector remains a distinct narrower evidence-producer concept
- user-facing "Fleet" jargon removed where it assumes internal knowledge
- internal `pkg/fleet`, `/fleet/...` and CLI/internal identifiers may remain

### Primary Product navigation

- Overview
- Services
- Operational graph
- Change analysis

Secondary/contextual:

- Needs attention
- Owners
- Data sources
- Readiness through revision/attention context

### Readiness

Readiness is per Contract Revision.

Fleet-host standalone legacy Readiness is retired/canonicalized.

### Change analysis

Legacy Compare + Product Impact were consolidated.

One canonical Product workspace answers what changed and what it affects. Detailed semantic field diff is preserved.

### Product entity responsibility

**Service**
- logical identity + aggregate operational posture.

**Revision**
- contract inspector.

**Operational Target**
- runtime/evidence inspector.

**Data source**
- provenance inspector: what this source is, whether it is healthy and current,
  what it supplied and what is attributable to it.

## 5. Accepted information-parity work

Earlier Product migration accidentally lost significant V1 inspection capability.

Subsequent work restored the intended model.

### Revision inspector

Supported information is reachable for:

- interfaces;
- operations;
- configurations;
- policies;
- capabilities/tools;
- skills;
- dependencies;
- readiness;
- docs;
- SBOM/software inventory;
- validation;
- identity/provenance;
- declared owner contact metadata;
- revision history;
- Change analysis entry.

### Markdown

Product Revision can again read bundled Markdown.

Current accepted architecture:

- docs listed in Revision detail;
- body fetched lazily;
- canonical RevisionKey + path boundary;
- bounded resource read;
- Markdown rendered safely;
- Mermaid rendered;
- path traversal rejected;
- same-name different-domain isolation.

### Immutable document correction

- Build records SHA-256 fingerprint for listed document content;
- lazy read verifies bytes against fingerprint;
- changed/deleted/unreadable body becomes explicit unavailable;
- 512 KiB bound remains;
- source conflicts do not silently pick arbitrary bytes.

## 6. Accepted UI/interaction corrections

- Services search autocomplete: debounced, bounded, canonical identity,
  same-name domain disambiguation, keyboard/pointer, stale-response protection.
- Same-query stale-while-revalidate: first load / changed identity may load;
  same-query refresh retains last valid data; refresh failure retains stale data
  with an honest notice; a changed query never presents old rows as the answer.
- Scroll restoration: scroll belongs to a history entry, not only a URL; push
  starts fresh; Back/Forward restores the exact entry; hard reload restores the
  current entry; canonical replace preserves the entry; state is bounded; user
  scroll input can cancel pending restoration.
- Graph spatial state: same-query refresh preserves layout; reload restores
  spatial state; query identities isolate state; semantic data still refreshes;
  `Fit` and `Reset layout` are different.

## 7. Accepted visualization direction

Accepted principle:

> lists/tables for exact inspection; visualizations for system comprehension.

Any chart must answer a real question, use authoritative aggregate data, not
infer global truth from a page, preserve exact values, expose
uncertainty/completeness and remain accessible/mobile/light/dark.

## 8. Latest verification snapshot

Reviewed at exact HEAD `797a49b3`. That review froze the whole runtime half of
B4 and kept Phase 8 narrowly reopened on its on-disk migration boundary; see
section 2.

### Sixth-reopen verification — self-reported at `b0020460`

Not an independent review. Re-verify at the exact SHA before accepting it.
Phase 8 stays a CANDIDATE; only an independent review closes it.

- GitHub CI at `b0020460`: 39 check runs — 36 success, `build` and `auto-merge`
  skipped, `CodeQL` failure. Green on the first attempt, no reruns: `changes`,
  `ci-gates`, `ci-static` (the failure this iteration was commissioned to fix),
  `ci-engine`, `ci-oci`, `ci-dashboard`, `ci-e2e-envtest`,
  `ci-integration-kubernetes`, `dashboard-e2e`, `operator-build`,
  `artifact-drift`, `release-version-test`, `release-dry-run`, `required`,
  `bundle`, `docs-check`, `repowise`, `validate`, and all six Kind shards —
  `dashboard`, `upgrade`, `reconcile`, `evidence`, `observation`,
  `operational-graph`. The whole run is green except the explicitly carried
  CodeQL check.
- Security is green and stays a DIFFERENT claim from the CodeQL alert
  attribution: Trivy, Trivy (image), govulncheck, govulncheck (Go), PR security
  summary and all four `Analyze` jobs pass. The `CodeQL` check itself reports
  "8 new alerts including 8 high severity security vulnerabilities" (10 at
  `797a49b3`) — the carried PR-ref path-expression findings on
  `pkg/oci/cache.go`. That item stays OPEN below and is NOT closed by the green
  Security workflow.
- Locally at the same tree: `make ci-static` clean (fmt, vet, gocyclo, lint,
  `check-section`, CLI-docs drift, UI-build drift, dashboard-SDK drift, plus the
  operator's own `ci-static`); `make ci-test` — 100.0% total coverage under the
  race detector, plus the example tests and the 24/24 offline demo-contract
  validation; `make ci-gates`; `make ci-engine` (engine e2e and `demo-fleet`,
  all sections PASS); `make ci-dashboard` — Vitest 1232 passed in 67 files;
  `make ci-oci`; `make ci-integration-kubernetes`; `make ci-e2e-envtest`.
- Focused counterfactuals with `-race -count=5`: `pkg/oci` (alias, `entryDir`,
  `pullCached`, `pullPinned`, `readCacheEntry`), `pkg/dashboard -run
  TestCacheSource_`, `internal/fleetsrc -run TestCacheSource_`. And `go test
  -race ./...` over the whole module.
- The upgrade counterexample was run against `d58a6f93` before the fix: it fails
  there in the "legacy A, then B is pulled" order, with `refA` no longer cached
  at all after the restart. The opposite order already passed there, and both
  orders are asserted now regardless.
- No authored frontend input changed, so the committed UI bundle was NOT
  rebuilt; the drift gates are clean against the existing one.
- The six Kind shards were attempted locally. Four PASS — `dashboard`,
  `upgrade`, `reconcile`, `observation`. `evidence` and `operational-graph`
  cannot run here for the reason recorded at `ci.mk:88-90`: both load
  `registry:2`, and under Docker Desktop's containerd image store `kind load`
  fails with `ctr: content digest sha256:46faa9a1… not found` while importing
  `--all-platforms`, so the cluster never gets its images. Both are green in
  CI's clean Docker at `b0020460`.
- PR at `b0020460`: open, DRAFT, mergeable. No amend, no rebase, no force-push,
  no history rewrite — `b0020460` and this document's commit are appends on top
  of `797a49b3`.
- Review threads re-queried at `b0020460` (paginated, all 199 fetched): 199
  total, 189 resolved, 10 unresolved. Six are `github-code-quality` comments on
  the GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, unchanged because
  the bundle was not rebuilt. Four are `github-advanced-security` CodeQL
  comments on AUTHORED code, now at `pkg/oci/cache.go` lines 367, 386, 387 and
  654. So: 4 unresolved authored, 6 unresolved generated. At `797a49b3` the same
  query returned 199 total, 187 resolved, 12 unresolved (6 generated, 6
  authored); two authored CodeQL threads resolved themselves as their lines
  moved with this commit. The unresolved authored population is the same carried
  item, not a new one.

### Third-reopen verification — self-reported at `a1159be0`

Not an independent review. Re-verify at the exact SHA before accepting it.
Phase 8 stays a CANDIDATE; only an independent review closes it.

- GitHub CI run `31690665496` at `a1159be0`: every job green on the first
  attempt, no reruns — `changes`, `ci-gates`, `ci-static`, `ci-engine`,
  `ci-oci`, `ci-dashboard`, `ci-e2e-envtest`, `ci-integration-kubernetes`,
  `dashboard-e2e`, `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run`, `required`, and all six Kind shards — `dashboard`,
  `upgrade`, `reconcile`, `evidence`, `observation`, `operational-graph`.
- All 39 check runs at `a1159be0`: 36 success, `build` and `auto-merge`
  skipped, and `CodeQL` failure — the carried-forward alerts item, re-queried
  below. Security (Trivy, govulncheck, all four CodeQL `Analyze` jobs), Docs
  check, Pacto Contract CI, Repowise and Validate PR title are green. The green
  Security workflow is a DIFFERENT claim from the CodeQL alert attribution and
  does not close that item.
- Locally at the same tree: `make ci-static` (fmt, vet, gocyclo, lint,
  `check-section`, CLI-docs drift, UI-build drift, dashboard-SDK drift, plus the
  operator's own `ci-static`) clean, `0 issues`; `make ci-gates`; `make
  ci-engine` — 100.0% total coverage with the race detector, the engine e2e
  suite, and `make demo-fleet` all sections PASS; `make ci-dashboard` — Vitest
  1232 passed in 67 files; `make ci-oci`; `make ci-integration-kubernetes`;
  `make ci-e2e-envtest`. Focused with `-race`: `./internal/cli/`, `./pkg/oci/`,
  `./internal/fleetsrc/` clean. No authored frontend input changed, so the
  committed UI bundle was NOT rebuilt and the drift gates are clean against the
  existing one.
- The six Kind shards were attempted locally this time. Four PASS —
  `dashboard`, `upgrade`, `reconcile`, `observation`. `evidence` and
  `operational-graph` cannot run here: both load the single-platform
  `registry:2`, and `kind load` under Docker Desktop's containerd image store
  fails with `ctr: content digest sha256:46faa9a1… not found` while importing
  `--all-platforms`, so the cluster never gets its images. Flattening
  `registry:2` to a single-platform image locally does not defeat it — the
  scenario re-pulls the multi-platform tag itself. This is the limitation
  recorded at `ci.mk:88-90`; those two shards are verified in CI, where both are
  green at `a1159be0`.
- PR at `a1159be0`: open, DRAFT, mergeable. No rebase, no amend, no history
  rewrite, no force-push — `a1159be0` and this document's commit are appends on
  top of `1741318d`.
- Review threads re-queried at `a1159be0` (paginated and de-duplicated by
  thread id, 196 threads): 186 resolved, 10 unresolved, and the 10 unresolved
  are the CURRENT ones. Re-queried again after this document's commit, at
  `60fe9919`: 197 threads, 187 resolved, 10 unresolved, all 10 still CURRENT.
  The total moved by one because CodeQL closed its thread on `cache.go` 255 and
  opened one on `cache.go` 481, which is the alert churn recorded below; the
  unresolved population did not change size. Six are `github-code-quality`
  comments on the GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, unchanged because
  the bundle was not rebuilt. Four are `github-advanced-security` CodeQL
  comments on AUTHORED code, now at `pkg/oci/cache.go` lines 322, 341, 342 and
  481. So: 4 unresolved authored, 6 unresolved generated.
- All 38 check runs at `60fe9919` (this document's own commit) have the same
  shape as at `a1159be0`: everything green including all six Kind shards, with
  `build` and `auto-merge` skipped and `CodeQL` failing as the carried item.

### Second-reopen verification — self-reported at `622ed857`

Not an independent review. Re-verify at the exact SHA before accepting it.

- GitHub CI run `31678484400` at `622ed857`: every job green — `changes`,
  `ci-gates`, `ci-static`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-integration-kubernetes`, `dashboard-e2e`,
  `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run`, `required`, and all six Kind shards — `reconcile`,
  `dashboard`, `upgrade`, `evidence`, `observation`, `operational-graph`. No
  reruns: green on the first attempt.
- Other workflows at `622ed857`: Security (including Trivy and all four CodeQL
  `Analyze` jobs), Docs check, Pacto Contract CI, Repowise and Validate PR title
  all green. The `CodeQL` check run itself fails — that is the carried-forward
  alerts item below, re-queried there.
- The `operational-graph` shard (job `94378336340`) is the direct evidence for
  boundary 3, and it is worth reading as a sequence rather than as a verdict.
  The gate first waited on `data source "oci" is not in the snapshot`, then on
  `revision …/checkout 1.0.0 was not contributed by "cache" (sources: oci)` for
  six rounds while the registry answered and the disk cache had not yet been
  filled, then passed: snapshot `sha256:9ad5bcb9…` proved all 14 facts on round
  9, and snapshot `sha256:7d81cfc7…` proved all 14 again on round 15 — two
  distinct coherent snapshots across a refresh, each with checkout 1.0.0,
  checkout 1.1.0 and orders 1.0.0 naming BOTH `oci` and `cache` while remaining
  exactly one canonical, exact, retrievable revision. The eight live Chromium
  journeys then passed against that state. Those intermediate `not contributed
  by "cache"` lines are the gate doing the thing `-snapshots 2` could not.
- Locally at the same tree: `make ci-test` — 100.0% total coverage across every
  package plus the example tests and the offline demo-contract validation
  (24/24); `make ci-static` — fmt, vet, gocyclo, lint, `check-section`,
  CLI-docs drift, UI-build drift, dashboard-SDK drift, and the operator's own
  `ci-static`, all clean, `0 issues`; `go test -race ./internal/cli/...` clean
  over the changed package. No authored frontend input changed, so the committed
  UI bundle was NOT rebuilt and `ci-ui-drift` is clean against the existing one.
- **The six Kind shards cannot be run on this machine.** Docker Desktop's
  containerd image store reports an image's `.Id` as the multi-platform INDEX
  digest while the kind node reports the CONFIG digest, so `kind load`'s presence
  check can never match and it re-imports `--all-platforms`, which fails on a
  single-platform local `registry:2`. This is the limitation already recorded at
  `ci.mk:88-90`; a pre-seed of the node via `docker save` plus `ctr import` was
  tried and does not defeat it. The shards are therefore verified in CI only,
  which is where the boundary-3 failure at `caf88050` and its fix at `622ed857`
  were both observed.
- PR at `622ed857`: open, DRAFT, mergeable. No rebase, no amend, no history
  rewrite, no force-push — `6049f44e`, `caf88050` and `622ed857` are appends on
  top of `879724dc`, and this document's commit appends on top of them.
- Review threads re-queried at `622ed857` (paginated and de-duplicated, 196
  threads): 186 resolved, 10 unresolved, and the 10 unresolved are the CURRENT
  ones. The earlier "596" was the un-de-duplicated page total — the same threads
  counted once per page of the paginated query — and is corrected here. Six are
  `github-code-quality` comments on the GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, unchanged because
  the bundle was not rebuilt. Four are `github-advanced-security` CodeQL comments
  on AUTHORED code — `pkg/oci/cache.go` lines 255, 285, 304 and 305 — recorded in
  the carried CodeQL item below. So: 4 unresolved authored, 6 unresolved
  generated. The two threads reported at `0cf0c69b` on `cache.go` 260 and 261 are
  gone because the code they pointed at, the in-place sidecar write, was deleted
  by boundary 1.

**Disclosed, NOT fixed — outside the frozen blocker-B scope.** Boundary 1 made
the cache-persistence failure loud instead of silent, and the first thing it
said, in the failure diagnostics of the red run at `caf88050`, was that the
`pacto-evidence` pod logs `could not cache the pulled bundle … read-only file
system` on every pull: its cache directory sits on a read-only mount. The
evidence component therefore has no offline baseline of its own. That is a real
deployment defect, it is not in any of the three blocker-B boundaries, and
nothing in this pass changed it. It is only visible in a shard's diagnostics
dump, which a green run does not produce, so it was not re-observed at
`622ed857`.

### Post-reopen verification — self-reported at `0cf0c69b`

Not an independent review. Re-verify at the exact SHA before accepting it.

- GitHub CI run `31625253138` at `0cf0c69b`: every job green, including
  `ci-gates`, `ci-static`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-integration-kubernetes`, `dashboard-e2e`,
  `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run`, and all six Kind shards — `reconcile`, `dashboard`,
  `upgrade`, `evidence`, `observation` and `operational-graph`.
- `ci-e2e-envtest`, `ci-integration-kubernetes` and the `bundle` job of Pacto
  Contract CI (run `31625253128`) each failed on the FIRST attempt with a
  `503 Service Unavailable` from `github.com/.../releases` while downloading a
  third-party binary (envtest 1.36.2, helm-unittest, syft 1.42.3). None of the
  three touches the changed packages. `gh run rerun --failed` on both runs made
  them green with no code change; both runs are green at `0cf0c69b`.
- Other workflows at `0cf0c69b`: Security, Docs check, Repowise, Validate PR
  title and Pacto Contract CI all green. `CodeQL` reports fail — the
  carried-forward item below, with two new alerts from this pass, disclosed
  there.
- The `operational-graph` shard (job `94209933398`) shows the post-cache state
  reached by observation: snapshot `sha256:24bad63f...` proved 13 facts on round
  4, then snapshot `sha256:2ef5f486...` proved all 13 again on round 10 — a
  second, distinct snapshot after a dashboard refresh, whose facts include
  exactly one canonical, exact, retrievable revision for each of checkout 1.0.0,
  checkout 1.1.0 and orders 1.0.0 with the cache already populated. The eight
  live Chromium journeys (A–H) then passed against that state.
- Locally at the same tree: `ci-static-engine` (fmt, vet, gocyclo, lint,
  check-section, CLI-docs drift, UI-build drift, dashboard-SDK drift) clean;
  `ci-test` 100.0% total coverage with the race detector; `ci-gates`;
  `make e2e` (the engine e2e suite, including the three new
  `fleet_cache_identity` production tests, plus the productready gate's own
  tests, which `make e2e` now runs at all); `make demo-fleet` (cluster-free
  operational-graph acceptance, all sections PASS); `ci-ui` — Vitest 1232 passed
  in 67 files.
- No authored frontend input changed in this pass, so the committed UI bundle
  was NOT rebuilt; `ci-ui-drift` and `check-dashboard-sdk-drift` are clean
  against the existing bundle.
- PR at `0cf0c69b`: open, DRAFT, mergeable. No rebase, no amend, no history
  rewrite, no force-push: `d8ef5d5a` and `0cf0c69b` are appends on top of
  `6750c959`, and this document's commit appends on top of them.
- Review threads re-queried at `0cf0c69b` (paginated, 192 threads): 184
  resolved, 8 unresolved, all CURRENT (none outdated). Six are
  `github-code-quality` comments on the GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, unchanged because
  the bundle was not rebuilt. Two are NEW `github-advanced-security` CodeQL
  comments on AUTHORED code — `pkg/oci/cache.go` lines 260 and 261, the sidecar
  write added by blocker B's fix — and are recorded in the carried CodeQL item
  below. So: 2 unresolved authored, 6 unresolved generated.

### Phase-8 candidate verification — self-reported at `d18ca70e`

Not an independent review. Re-verify at the exact SHA before accepting it.

- GitHub CI run `31612913647` at `d18ca70e`: every job green, including
  `ci-gates`, `ci-static`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-integration-kubernetes`, `dashboard-e2e`,
  `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run` and all six Kind shards (`dashboard`, `evidence`,
  `observation`, `operational-graph`, `reconcile`, `upgrade`).
- Security, Docs check, Pacto Contract CI, Repowise and Validate PR title: green
  at the same SHA. `CodeQL` reports fail — that is the carried-forward item
  below, unchanged.
- Locally at the same tree: `ci-static-engine` (fmt, vet, gocyclo, lint,
  check-section, CLI-docs drift, UI-build drift, dashboard-SDK drift),
  `ci-engine`, `ci-gates`, `ci-dashboard`; frontend `svelte-check` 0 errors /
  15 warnings across 799 files, Vitest 1232 passed in 67 files, offline WASM
  Playwright 219 passed.
- The committed UI bundle was rebuilt COLD via `make ui-build` and committed as
  `b8424175`; `ci-ui-drift` is clean at `d18ca70e` and the tree is unchanged by
  a rebuild.
- PR at `d18ca70e`: open, DRAFT, mergeable, no history rewrite, no force-push.
- Review threads re-queried at `d18ca70e` (paginated, 190 threads): 184
  resolved, 6 unresolved. All 6 are `github-code-quality` bot comments on the
  GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, all CURRENT (not
  outdated) because the bundle rebuild moved the path. 0 unresolved authored
  threads. Generated assets are not hand-edited.

One earlier candidate SHA, the docs-only `2c5034d8`, failed its
`operational-graph` shard. That was a harness defect, not a product one, and it
is the defect `d18ca70e` fixes: `pf` slept two seconds instead of waiting for the
port-forward, and the registry push piped its output into `grep`, so under
`set -o pipefail` the script died at a push that had nothing to connect to, with
the error text already eaten by the pipe and no `FAIL:` line printed at all. A
green run before that flake is not what this section reports; `d18ca70e` is a
full matrix on the fixed harness.

### Cross-cutting PRE-MERGE SECURITY item — OPEN, carried forward

Claude reports open CodeQL alerts on `refs/pull/291/head` rather than on `main`:
seven Go path-injection alerts plus one Python alert also present on `main`.

The exact inventory is **NOT** independently verified: the reviewing GitHub
integration cannot reach the code-scanning alerts API. The current source
inspection makes the reported false-positive explanation plausible, but plausible
is not resolved.

Therefore:

- do not describe these alerts as resolved;
- do not describe them as main-lineage;
- the Security workflow's own green status is a DIFFERENT claim from CodeQL alert
  attribution and does not close this;
- the alerts must remain visible and must be independently triaged, fixed or
  explicitly dismissed with evidence before Phase 14 readiness;
- do NOT make unrelated security-code changes in an intervening phase unless a
  NEW real counterexample proves the current explanation false.

This item is cross-cutting. It must not be pulled into a feature phase's scope,
and no feature phase must be blocked on it.

Re-verify both populations at the exact final SHA of every later pass; the
generated asset path changes whenever the UI bundle is rebuilt.

Generated Mermaid/minified assets must not be hand-edited. After the FINAL
generated bundle of a pass is committed, review threads must be queried again
and reported as unresolved authored / unresolved generated / current versus
outdated, with the exact current generated path.

The Security workflow's own status is a different claim from CodeQL alert
attribution. Do not describe CodeQL alert provenance as independently
established without inspecting the underlying alert records and their base/head
evidence.

Counts in a handoff are not evidence.

Re-queried at `d18ca70e` (still a Claude report, still NOT independently
verified, still OPEN): 8 open alerts on `refs/pull/291/head`, unchanged in
population — 7 `go/path-injection` (`internal/app/resolve.go` 35, 43, 57, 67;
`pkg/oci/cache.go` 230, 250, 254) and 1 `py/incomplete-url-substring-sanitization`
(`release/scripts/docs_check.py:197`). No security-code changes were made in
Phase 8; none of these are described as resolved, dismissed or main-lineage.

Re-queried again at `0cf0c69b` (same caveats, still OPEN): 10 open alerts on
`refs/pull/291/head` — 9 `go/path-injection` and the same 1 Python alert. The
population GREW BY TWO in this pass, and that growth is disclosed here rather
than folded into the existing item:

- alerts 40, 41, 42, 43 (`internal/app/resolve.go` 35, 43, 57, 67) — unchanged;
- alerts 45, 46, 47 (`pkg/oci/cache.go` 301, 321, 325) — the same three
  previously reported at 230, 250, 254; the lines moved because blocker B's fix
  inserted code above them, and no security code was changed;
- alerts 56, 57 (`pkg/oci/cache.go` 260, 261) — NEW, on the `MkdirAll` and
  `WriteFile` of the `ref.json` sidecar that blocker B's fix added;
- alert 38 (`release/scripts/docs_check.py:197`) — unchanged.

The two new alerts are the SAME family, behind the same barrier, as 45/46/47:
their path is `filepath.Dir(c.cachePath(ref))`, and `cachePath` already contains
the result to the cache directory with an explicit `filepath.Rel` plus `..`
check, returning a fixed `_invalid` path otherwise. CodeQL does not model that
barrier. That explanation is PLAUSIBLE, NOT VERIFIED, and it is exactly the
explanation this item refuses to accept without inspecting the alert records. So:
the two new alerts are OPEN, are not described as resolved, dismissed or
main-lineage, and are added to the population that must be independently triaged
before Phase 14 readiness. No attempt was made to silence them, and no unrelated
security code was touched.

Re-queried again at `622ed857` (same caveats, still OPEN): 10 open alerts on
`refs/pull/291/head` — 9 `go/path-injection` and the same 1 Python alert. The
population SIZE is unchanged, but its membership is not, and the churn is
disclosed rather than folded in:

- alerts 40, 41, 42, 43 (`internal/app/resolve.go` 35, 43, 57, 67) — unchanged;
- alert 45 (`pkg/oci/cache.go` 371, `loadFromCache`'s `os.Open`) — the same alert
  previously reported at 301; the line moved because boundary 1 inserted code
  above it, and no security code was changed;
- alerts 46, 47 and 56, 57 — CLOSED, not dismissed and not suppressed: they were
  on `saveToCache`, `saveRefRecord` and the in-place sidecar write, all four of
  which boundary 1 DELETED;
- alerts 58, 59, 60, 61 (`pkg/oci/cache.go` 255, 285, 304, 305) — NEW, on
  `ReadCachedRef`'s `os.ReadFile` and on `writeCacheEntry`'s `MkdirAll`,
  `RemoveAll` and `Rename`, i.e. the atomic commit boundary 1 added;
- alert 38 (`release/scripts/docs_check.py:197`) — unchanged.

The four new alerts are the SAME family, behind the same barrier, as 45: every
one of those paths derives from `cachePath`, which contains its result inside
the cache directory with an explicit `filepath.Rel` plus `..` check and returns
a fixed `_invalid` path otherwise. CodeQL does not model that barrier. That
explanation is PLAUSIBLE, NOT VERIFIED, and it is exactly the explanation this
item refuses to accept without inspecting the alert records. So: the four new
alerts are OPEN, are not described as resolved, dismissed or main-lineage, and
join the population that must be independently triaged before Phase 14
readiness. No attempt was made to silence them, and no unrelated security code
was touched.

Re-queried again at `a1159be0` (same caveats, still OPEN): 9 open alerts on
`refs/pull/291/head` — 8 `go/path-injection` and the same 1 Python alert. The
population SHRANK BY ONE and its membership moved again; the churn is disclosed
rather than folded in:

- alerts 40, 41, 42, 43 (`internal/app/resolve.go` 35, 43, 57, 67) — unchanged;
- alerts 59, 60, 61 (`pkg/oci/cache.go` 322, 341, 342) — the same three
  previously reported at 285, 304 and 305 on `writeCacheEntry`'s `MkdirAll`,
  `RemoveAll` and `Rename`; the lines moved because boundary 4 inserted code, and
  no security code was changed;
- alert 62 (`pkg/oci/cache.go` 481, `heldGenerationIsInstalled`'s `os.Stat`) —
  NEW, on the installed-directory comparison boundary 4 added;
- alert 58 (`ReadCachedRef`'s `os.ReadFile`, previously 255) and alert 45
  (`loadFromCache`'s `os.Open`, previously 371) — reported by the API as FIXED,
  not dismissed and not suppressed. `loadFromCache` was replaced by
  `readCacheGeneration`, which opens the entry through `os.OpenRoot` and reads
  both files from that handle, so those two flows no longer exist as written. A
  `state: fixed` alert is CodeQL's own report, not a triage verdict, and it is
  recorded here as such;
- alert 38 (`release/scripts/docs_check.py:197`) — unchanged.

The one new alert is the SAME family, behind the same barrier, as 59/60/61:
every one of those paths derives from `cachePath`, which contains its result
inside the cache directory with an explicit `filepath.Rel` plus `..` check and
returns a fixed `_invalid` path otherwise. CodeQL does not model that barrier.
That explanation is PLAUSIBLE, NOT VERIFIED, and it is exactly the explanation
this item refuses to accept without inspecting the alert records. So: the new
alert is OPEN, is not described as resolved, dismissed or main-lineage, and
joins the population that must be independently triaged before Phase 14
readiness. No attempt was made to silence any of them, and no unrelated security
code was touched. The `CodeQL` check run at `a1159be0` still fails; the Security
workflow at the same SHA is green, and those remain two different claims.

Re-queried again at `b0020460` (same caveats, still OPEN): 9 open alerts on
`refs/pull/291/head` — 8 `go/path-injection` and the same 1 Python alert. The
population is UNCHANGED in membership; only lines moved:

- alerts 40, 41, 42, 43 (`internal/app/resolve.go` 35, 43, 57, 67) — unchanged;
- alerts 59, 60, 61 (`pkg/oci/cache.go` 367, 386, 387) — the same three
  previously reported at 322, 341, 342 on `writeCacheEntry`'s `MkdirAll`,
  `RemoveAll` and `Rename`; the lines moved because this pass's comments and the
  `entryNamespace` change sit above them, and no security code was changed;
- alert 62 (`pkg/oci/cache.go` 654, `heldGenerationIsInstalled`'s `os.Stat`) —
  the same alert previously reported at 481, moved for the same reason;
- alert 38 (`release/scripts/docs_check.py:197`) — unchanged.

No alert was added, silenced or dismissed in this pass, and no unrelated
security code was touched. The `CodeQL` check run at `b0020460` still fails,
reporting 8 new high-severity alerts (10 at `797a49b3` — the number the check
attributes to the diff moved, the underlying open population did not). The
Security workflow at the same SHA is green, and those remain two different
claims. The whole item stays OPEN and must be independently triaged, fixed or
dismissed with evidence before Phase 14 readiness.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

**Phase 8 — canonical live Kind Product acceptance.** The branch already carries
a full live vertical (operator, dashboard, Evidence Server, in-cluster OCI
registry, reconciled Pacto CRs, external signed evidence, live HTTP Product API,
Playwright over Chromium). Phase 8 makes that EXISTING vertical rich enough that
a representative live Product journey has actual topology, revisions, targets,
observed evidence and reconciliation. Its detailed target is TARGET section 10,
"Phase 8 — live Kind Product acceptance breadth".

At `0cf0c69b` that vertical exists and the two counterexamples the review raised
against it are closed (section 2). The next iteration objective is therefore the
INDEPENDENT REVIEW of the Phase-8 candidate at its exact final SHA — not new
Phase-8 breadth, and not Phase 8B, which remains NOT STARTED.

Hard boundaries for the Phase-8 session:

1. do NOT create a new large `.sh` acceptance harness; extend the existing
   `tests/e2e/kind/operational-graph.sh` vertical and classify every shell
   addition as thin orchestration or explicitly deferred Phase-8B debt;
2. do NOT begin Phase 8B; its target is persisted in TARGET section 10 and must
   not be implemented in the Phase-8 pass;
3. do NOT add a fixture-only Product shortcut; the dashboard must discover OCI
   revisions through the actual operator status path;
4. do NOT re-derive reconciliation in Playwright; the backend value is
   authoritative and the browser proves consistent presentation;
5. do NOT erase the open CodeQL item in section 8;
6. Phases 1 through 7 must not be reopened or redesigned as a side effect.

## 11. Final-phase requirements already agreed

When the whole plan reaches the final phase, two deep audits are mandatory.

### Final ontology audit

Very thoroughly compare:

**intended ontology <-> code/types/behavior <-> docs/API/UI**

Must validate all first-class concepts, identities, relationships, epistemic states, absence semantics, provenance and aggregation semantics.

Do not declare merge-ready if ambiguity could produce a false operational claim.

### Final repository hygiene audit

Review complete `base...HEAD`.

Classify every added file.

Remove implementation-only artifacts such as internal plans, ChatGPT/Claude instructions, handoffs, temporary ledgers, review screenshots, traces/reports, one-off scripts, caches/logs, orphan fixtures, accidental generated output and local config/paths/secrets.

The three PR coordination documents are temporary branch state and MUST be deleted in Phase 14 before merge readiness.
