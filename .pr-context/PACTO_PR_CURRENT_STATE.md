# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-17
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`5fce48e6708cbd47f9ebe03244898647305fcca5`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

**That review ACCEPTED and CLOSED Phase 8.** After seven narrow reopens the
eighth review found no remaining counterexample: blockers B6 and B7 are closed
and join the frozen accepted behaviour (see "Accepted at this review"). The
Phase-8 live Kind Product acceptance — its vertical, its 14-fact two-snapshot
gate, journeys A–H and the whole cache-identity repair series — is settled.

**Phase 8B is ACCEPTED and CLOSED** at `93dca214`: test architecture and harness
consolidation, including the canonical scenario/projection boundary that TARGET
section 1B added and TARGET Phase 10B is blocked on. An independent review
verified both blockers left at `1a04807d` — the ambiguous deployment and the
untested fixture package — as closed by the repairs recorded in section 12.8.

**Phase 9 is ACCEPTED and CLOSED** at `5fce48e6`: real browser E2E against the
built MkDocs site, including the diagrams it renders. It was reopened once — the
runtime prerequisite the new hook demands was carried by the PR gate alone, so
the clean build and deployment paths bypassed it — and the review at `5fce48e6`
verified that repair (section 13.1) closed. Nothing in it is reopened by Phase 10.

**Phase 10 is a CANDIDATE** at `b2157a09`, implemented from `5fce48e6`: closing
the local Kind acceptance path where Docker Desktop's containerd image store
behaves differently from CI's classic Docker image store. It repairs the SHARED
Phase-8B harness boundary; it adds no second Kind suite and changes no
scenario's claims. Its record is section 14, its implementation acceptance and
main synchronization are section 14.1. It is not closed here; a phase is closed
by review, not by its author.

The Phase-10 IMPLEMENTATION passed independent review at `6c8ba9fe`, with the
shared `kindload` boundary verified on both image stores. Phase 10 stayed open
only because the PR was `CONFLICTING` / `DIRTY`, so GitHub produced no merge ref
and none of the required pull-request workflows could run. `b2157a09` is the
append-only merge from `origin/main` that removes that blocker: the PR is
`MERGEABLE` again and the full required suite, all six Kind shards included, has
now run green on that exact SHA. Section 14.1 records it.

Accepted Phase-8 behaviour is NOT reopened for stylistic improvement, theoretical
hardening or refactor convenience. A reopen requires a CONCRETE correctness,
security or data-loss counterexample against the accepted behaviour, stated as
inputs and the wrong observable result they produce.

Commits appended on top of the previously reviewed HEAD `837ef8bb`, oldest
first — all three are independently reviewed and inside the accepted state:

- `6be8719b` — a cache inventory enumerates generations, not pathnames (blockers
  B6 and B7)
- `6e3a3627` — persist the Phase-8 candidate after the seventh narrow reopen,
  and record the TARGET-STATE additions
- `5ffc72b3` — record the check and thread state at `6e3a3627`

Reviewed final SHA: `5ffc72b3`. At that SHA CI is green including all six Kind
shards. Merge-base with `origin/main` is unchanged. The CI result of `5ffc72b3`
was verified directly in GitHub; it is deliberately NOT re-recorded in a further
documentation-only commit, because recording the CI result of the commit that
records CI results is recursive bookkeeping with no reviewer value.

Commits appended on top of the earlier reviewed HEAD `797a49b3`:

- `b0020460` — a cache entry this version writes can never land on an older
  one's baseline (blocker B, B4's on-disk migration boundary; ACCEPTED and
  frozen at `837ef8bb`)
- `c9d52bb9` — persist the Phase-8 candidate after the sixth narrow reopen
- `837ef8bb` — record the check and thread state re-queried at `c9d52bb9`

### TARGET-STATE additions in this iteration — FUTURE TARGET ONLY

The doc pass also appended three things to `PACTO_PR_TARGET_STATE.md`. None of
them is implemented, started or scheduled in this repair; they are recorded so
the phase that owns them inherits them:

- **TARGET section 1B — "Declarative > imperative."** A repository-wide design
  PREFERENCE, not an absolute ban. Desired state, topology, fixtures, demo
  content and acceptance expectations expressed as data consumed by shared
  engines; imperative code limited to the irreducible execution boundary
  (invoking tools, waiting for declared conditions, collecting diagnostics,
  cleanup).
- **TARGET section 1B consequences** — declarative fixture/scenario manifests
  over step-by-step harness programs, ONE canonical scenario projected into
  Kind, local acceptance and future demo surfaces rather than semantically
  independent copies, assertions kept in the layer that owns their meaning, and
  an explicit warning against replacing readable thin orchestration with a
  speculative framework: inventory the imperative traces first. Also one
  canonical demo model with at least a Helm/Kubernetes and a Docker Compose
  projection, platform limitations explicit rather than silently simulated, with
  Compose as a parallel distribution surface and not a second product
  architecture.
- **TARGET Phase 10B** — the clone-free OCI-distributed Compose demo, with its
  requirements (digest-pinned immutable distribution and version metadata,
  multi-architecture where supported, provenance compatible with the existing
  release policy, no embedded secrets, deterministic overridable ports,
  observed-state readiness rather than sleeps, explicit offline/restart
  behaviour, cleanup and upgrade instructions, CI that pulls the
  published-shaped artifact into a clean environment, and semantic parity checks
  between the projections). It is BLOCKED on Phase 8B — which must first
  establish the canonical scenario/projection boundary, or the demo becomes
  another imperative duplicated harness — and sequenced after Phase 10, whose
  Docker Desktop work resolves the same container-runtime differences.

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
correctness, security or data-loss counterexample — stated as inputs and the
wrong observable result they produce. Style, symmetry, "theoretical hardening"
and refactor convenience are NOT counterexamples.

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

**Cache inventory as generations — the B6/B7 repair at `6be8719b`**

- a cache inventory enumerates coherent artifact GENERATIONS, not pathnames and
  not reference spellings;
- an inventory is COMPLETE over the generations present, PRESERVES different
  digests that share one reference, and DEDUPLICATES only complete
  `(Ref, Digest)` identities;
- the offline inventory path carries no network-capable store;
- fleet and dashboard both read bundle AND identity through the single
  `oci.ReadCacheEntry`; no production cache walker reads bundle and sidecar
  separately;
- a dashboard lazy read REJECTS a generation different from the one it indexed;
- the centralized OCI archive reader retains its traversal, entry-count,
  per-file-size, total-size and non-regular-entry protections.

**Phase-8 live Kind Product acceptance — accepted at `5ffc72b3`**

- the live vertical (operator, dashboard, Evidence Server, in-cluster OCI
  registry, reconciled digest-pinned Pacto CRs, managed observation source,
  external signed evidence);
- the coherent 14-fact / two-snapshot Product gate and its single-snapshot
  `adopt()` coherence rule;
- live browser journeys A–H over the real port-forwarded Product;
- the `pf` readiness-waiting port-forward behaviour;
- all six Kind scenario boundaries as scenario boundaries.

Phase 8B may RELOCATE, RENAME and DEDUPLICATE the code that implements the
above. It may not change what any of it proves.

**Phases**

- Phase 1 through Phase 8 are closed.

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

### Phase 8 — ACCEPTED and CLOSED at `5ffc72b3`

Scope as commissioned: canonical LIVE Kind PRODUCT acceptance. Upgrade the
EXISTING live Kind vertical from a deliberate browser SMOKE check into
representative live Product acceptance: real OCI contract revisions published to
the in-cluster registry, digest-pinned operator resolution, a managed observation
source in the SAME operator-managed dashboard, live declared+observed
reconciliation reaching `matched` against the real Product API, real Change
analysis over two canonical revisions, and the existing external signed-evidence
target preserved.

Not another Kind vertical. Not a test-architecture refactor. Not Phase 8B.

Implemented at `d18ca70e`, then narrowly reopened SEVEN times by successive
independent reviews and repaired each time — `6750c959` (two counterexamples),
`879724dc` (three blocker-B boundaries), `caf88050`/`622ed857`, `a1159be0`,
`234f01f8`, `d58a6f93`, `b0020460` and finally `6be8719b` (B6 and B7). The
eighth independent review, at `5ffc72b3`, found no remaining counterexample and
**ACCEPTED and CLOSED the phase**. Its behaviour is frozen in section 1
("Accepted at this review"). Reopening it requires a concrete correctness,
security or data-loss counterexample.

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
and was addressed in both walkers.

**Correction, entered at the seventh reopen.** This paragraph previously claimed
both walkers keyed their suppression on the COMPLETE recorded identity. At
`b0020460` that was true of `pkg/dashboard.CacheSource.buildIndex`, which
indexed `ref@digest` once, and NOT true of `internal/fleetsrc.cachedRefs`, which
deduped on the reference alone and therefore reported a reference once however
many DIFFERENT artifacts were filed under it. The claim overstated what was
proved. The implementation is corrected in the seventh reopen below, and this
paragraph now states what each walker actually did at `b0020460`.

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

#### Seventh narrow reopen at `837ef8bb` — B6 and B7, CANDIDATE

The review at `837ef8bb` accepted and froze the `_v2` namespace, read-only
legacy compatibility, the removal of destructive retirement, the two-order
upgrade counterexample and exact-reference dashboard indexing, and reopened
Phase 8 on ONE root boundary: **a cache inventory must enumerate coherent
artifact GENERATIONS, not pathnames and not reference spellings.** Two readers
still did.

**B6 — the Fleet inventory collapsed distinct generations sharing one
reference.** `internal/fleetsrc.cachedRefs` deduped on `ref` alone
(`seen := map[string]bool{}`) and returned `[]string`. The legitimate on-disk
state a legacy entry `(Ref R, Digest A)` beside a `_v2` entry `(Ref R, Digest
B)`, `A != B` — natural once a mutable tag has been republished and pulled by
the upgraded version, and neither destructively retirable — reported R ONCE, and
`PullCachedPinned(R)` afterwards selected by store lookup order, so calling it
twice read the same generation. The other artifact silently did not exist in the
fleet.

Closed by making the walk DISCOVER and nothing else. `cachedGenerations` reads
each entry where the walk found it, through `oci.ReadCacheEntry`, and carries
the bundle and the identity onward together; suppression is on the COMPLETE
recorded identity (`ref + "@" + digest`), so legacy and `_v2` copies of ONE
artifact still collapse to one revision while two generations of one reference
stay two. Each generation becomes its own canonical digest-pinned revision
through the shared `revisionOf`, so a published artifact keys identically
whether it reached the fleet from the registry or from disk. A pre-sidecar entry
still falls back to the path and still says the reference is approximate; an
entry the walk can see and the read cannot resolve whole is a
`SOURCE_RECORD_INVALID` limitation, not a silence.

`CacheSource` also lost its `BundleStore` and its `ResolveMode`. Network-freedom
was previously a mode argument passed to a resolver that also knows how to dial;
it is now structural — nothing on that path can dial at all. `internal/cli`'s
`--no-cache` wiring never publishes a cache source, so ignoring `skipDiskReads`
here is not a behaviour change.

**B7 — the dashboard walker could splice bundle A with sidecar B.**
`pkg/dashboard.CacheSource.buildIndex` loaded `bundle.tar.gz` by pathname and
then called `ReadCachedRef(filepath.Dir(path))` separately. An atomic directory
replacement between the two observations published generation A's contract, hash
and service name under generation B's reference and digest — an indexed version
describing an artifact that has never existed. Deduplicating on `ref@digest`
only deduped an already-spliced identity.

Closed with a generation-bound read: `oci.ReadCacheEntry` for the index walk and
for the deferred per-version load, following the `os.OpenRoot` /
installed-generation semantics already accepted in `pkg/oci`; a replacement is a
coherent miss the retry repairs. The lazy load also refuses an entry that no
longer holds the artifact that was indexed — the deferred read is a SECOND
observation of a shared directory, and serving its bytes under the first read's
published contract, hash and reference is the same splice, delayed. No
cache-generation concurrency logic was duplicated in the dashboard package.

`internal/cachehook` carries the ONE interleaving seam (`AfterBundleRead`), fired
inside `readCacheGeneration` between the bundle read and the identity read, so
every reader held to the coherence rule can prove it from wherever it lives.
Production never sets it; the zero value is a no-op. `readCacheEntry` is
exported as `oci.ReadCacheEntry` for the same reason: it is THE definition of a
cached generation.

**Walker inventory after the repair.** Every production reader of a cache entry
now goes through `oci.ReadCacheEntry`: `CachedStore.cachedEntry`
(`pkg/oci/cache.go:245`), `fleetsrc.cachedGenerations`
(`internal/fleetsrc/oci.go:282`), and the dashboard's `buildIndex`
(`pkg/dashboard/source_cache.go:159`) and `cachedVersion.loadBundle`
(`pkg/dashboard/source_cache.go:96`). No path-only or stale inventory is left.
`oci.ReadCachedRef` survives with ZERO production callers — only tests asserting
what a WRITER wrote — and its doc now says that pairing it with separately read
bytes is exactly the splice `ReadCacheEntry` prevents. It is public API on a
public package, so removing it is a deliberate later decision, not part of this
narrow repair. The dashboard's private `loadBundleTarGz` / `extractTar` /
`dotDotComponent` and its `maxBundle*` limits went with the pathname read; the
tar-bomb, traversal and non-regular-entry limits now have ONE implementation, in
`pkg/oci`, which is at 100% coverage and already tests each of them.

Preserved unchanged: the `_v2` namespace and its disjointness proof; read-only
legacy compatibility; no destructive retirement; the two-order upgrade
counterexample; exact-reference dashboard indexing and the version key derived
from the reference; the complete LocalOnly `CachedRef`; the cold/warm agreement
guard; RemoteAllowed miss-refetch-or-fail; resolve-once then pull-by-digest;
zero-network offline reads; B5; the 14-fact two-snapshot live gate; journeys
A–H.

Counterexamples, all under `-race`, each mutation-checked:

- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_TwoGenerationsOfOneReferenceAreTwoRevisions`: real
  disk entries, generation A for one exact reference in the LEGACY layout and
  generation B for the same exact reference under `_v2`, read by a cold offline
  `CacheSource`. Two revisions, no digest twice, each bundle matching its own
  digest's generation, each `ResolvedRef` pinned to its own digest and both
  carrying the shared `RequestedRef`.
- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_OneReferenceIsCollectedOnce`: same reference AND same
  digest in both layouts stays ONE revision.
- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_TheWalkAndTheReadAreOneGeneration` and
  `_IdentityComesFromTheGenerationThatServedTheBytes`: a competing writer
  installs a whole generation inside the read window (asserting the window was
  actually entered), and bundle, `RequestedRef`, `Domain` and `ResolvedRef` must
  all belong to the same generation.
- `internal/fleetsrc/oci_test.go` —
  `TestCacheSource_Collect_AnUnreadableEntryIsAGapNotASilence`.
- `pkg/dashboard/source_cache_generation_test.go` —
  `TestCacheSource_BuildIndex_ABundleAndItsIdentityAreOneGeneration`: install
  generation A; let the walker discover and open it; atomically install
  generation B before the identity read; assert the indexed record is wholly A,
  wholly B or coherently absent, never A's contract under B's reference or
  digest. Real disk entries throughout. It resolves to B, whole, via the retry.
- `pkg/dashboard/source_cache_generation_test.go` —
  `TestCacheSource_LazyLoad_RefusesTheGenerationItDidNotIndex`: the deferred
  read held to the same rule, plus a removed entry as a miss and the resident
  latest unaffected.
- `tests/e2e/fleet_cache_identity_test.go` —
  `TestFleetOfflineSeesEveryCachedGenerationOfOneReference`: the same B6 case in
  PRODUCTION WIRING — a real registry, real `CachedStore`s writing both
  generations, a real republished tag, the legacy generation placed at the
  pathname an older Pacto left it under, then a restart with a cold offline
  `app.Service.Fleet`. Both bundles come back under their own digests, and
  `deadRegistry` proves zero network calls.
- `tests/e2e/fleet_cache_identity_test.go` —
  `TestFleetOfflineCollapsesOneArtifactCachedTwice`: the same artifact filed
  under both pathnames is one revision, still with zero network calls.

Mutation checks (each applied to the production code, run, then reverted):

- `identity := ref` instead of `ref + "@" + digest` in `cachedGenerations`: the
  unit counterexample reports one revision instead of two, and the production
  e2e loses generation B entirely (`checkout has 1 revisions, want both cached
  generations of <host>/demo/checkout:1.0.0`).
- Resolve-by-reference AFTER the inventory (the walk records references, a
  second lookup finds the first entry for each): `digest sha256:aaa… carries the
  bytes of "gen-b", which is "gen-a"` — both digests get the same generation's
  bundle.
- Revert `buildIndex` to the separate pathname read: the dashboard
  counterexample fails loudly because the interleaving window is never entered
  at all.
- The same reverted implementation with the hook forced between its two reads:
  the actual splice, `service "checkout-a" is indexed under digest sha256:bbb…,
  which holds "checkout-b"`.
- Delete the `rec != v.rec` guard in `cachedVersion.loadBundle`: the lazy-load
  counterexample fails.

Files, `837ef8bb..6be8719b`: 12 files, +892 / -727.

| file | change |
| --- | --- |
| `internal/cachehook/cachehook.go` | new — the one interleaving seam (+20) |
| `internal/fleetsrc/oci.go` | `cachedRefs` -> `cachedGenerations`, shared `revisionOf`, store-free `CacheSource` |
| `internal/fleetsrc/oci_test.go` | the B6 counterexamples and the generation-window tests |
| `internal/app/fleet.go` | `NewCacheSource` loses its `BundleStore` argument |
| `internal/app/fleet_test.go` | the cache fixture writes a real archive, because the inventory now reads it |
| `pkg/dashboard/source_cache.go` | generation-bound index walk and lazy load; the private tar reader and its limits deleted |
| `pkg/dashboard/source_cache_generation_test.go` | new — the B7 deterministic counterexample (+184) |
| `pkg/dashboard/source_cache_test.go`, `coverage_gap_test.go` | the deleted reader's own tests removed (-360) |
| `pkg/oci/cache.go` | `readCacheEntry` exported; the seam moved to `internal/cachehook`; `ReadCachedRef` retargeted |
| `pkg/oci/cache_generation_test.go` | uses the shared seam |
| `tests/e2e/fleet_cache_identity_test.go` | the two production-wiring offline counterexamples (+188) |

Verification for this candidate is in section 8. Still disclosed and out of
scope: the CodeQL PR-ref findings and the Evidence Server read-only-cache
warning.

### Phase 8B — ACCEPTED and CLOSED

Test architecture & harness consolidation. See TARGET section 10. Phase 8B MUST
close before Phase 9 or Phase 10 add their new acceptance harnesses — it has,
so Phase 9 may add its harness INTO the taxonomy this phase established.

The independent review at `2126fdcc` accepted the taxonomy, the relocations, the
Make/CI wiring, the shared Kind harness, `obscheck`, the Product gate, the
browser split and the preserved acceptance coverage. It left two blockers, both
now repaired; the narrow closure is recorded in section 12.7.

A second independent review, at `1a04807d`, accepted that closure and left two
further blockers: a fixture declaring two deployed revisions was silently
collapsed to the first by every surface, and the scenario package's own tests
were reached by no target `make ci` depends on. Both are repaired; that closure
is recorded in section 12.8.

A third independent review, at `93dca214`, verified both of those repairs
closed and CLOSED Phase 8B. Nothing in it is reopened by Phase 9.

Phase 8B additionally owns the canonical scenario/projection boundary added to
TARGET section 1B ("Declarative > imperative"). TARGET Phase 10B — the canonical
demo model and the clone-free OCI-distributed Compose demo — is BLOCKED on it.
Phase 8B establishes only the BOUNDARY; it does not implement the Compose or
OCI-distributed projections.

Semantic coverage must stay equal or increase. Nothing accepted in section 1 may
change what it proves; Phase 8B may only relocate, rename and deduplicate the
code that proves it.

The transient inventory ledger for this phase lives in section 12 of this
document. It is migration bookkeeping and is deleted with this document in Phase
14; the durable repository documentation carries the resulting ARCHITECTURE, not
the ledger.

### Phase 9 — ACCEPTED and CLOSED (reopened once, repaired in 13.1)

Real built MkDocs browser E2E. What the existing gates prove, and what they do
not: `mermaid-check` proves every fenced diagram is renderable by mermaid-cli
OUTSIDE the site; `pkg/dashboard/frontend/e2e/mermaid.spec.ts` proves contract
documentation renders INSIDE the dashboard. Neither loads the real MkDocs
output, so neither can see the theme integration, the hook-injected integration
pages, or Material instant navigation. Phase 9 adds exactly that surface, into
the taxonomy Phase 8B established rather than beside it, and does not weaken
either existing gate.

Implemented, verified locally and green in GitHub CI. The record — the failing
test, the defect it exposed, the fix, the rejected alternatives, the mutation
proof and the workflow results per SHA — is section 13. An independent review at
`5fce48e6` verified the 13.1 repair and CLOSED the phase.

### Phase 10 — ACCEPTED and CLOSED at `f2a181b1`

Docker Desktop/containerd/local-registry Kind path. Close the local Kind path
where Docker Desktop's containerd image-store behaviour differs from CI's
classic `kind load`, by repairing the SHARED Phase-8B harness so the existing
six scenarios run through the same semantic boundaries in both places.

Blocked on Phase 8B, which is CLOSED, so the repair goes INTO the consolidated
test architecture rather than beside it. It does not create another Kind suite,
merge or re-scope the six scenarios, weaken `imagePullPolicy`, readiness, the
Product gate or the browser journeys, and does not start Phase 10B.

Its record — the reproduced failure, the identity/platform root cause, the
loading strategy and its rejected alternatives, the regression and mutation
proof, and the local plus GitHub verification per SHA — is section 14, and the
main synchronization that unblocked its PR workflows is section 14.1. An
independent review CLOSED the phase at `f2a181b1`. Its behaviour is frozen:
reopening it requires a concrete correctness, security or data-loss
counterexample.

### Phase 10B — IN PROGRESS

Canonical demo model and clone-free OCI-distributed Compose demo. ONE canonical
declarative demo model, projected into the EXISTING Helm/Kubernetes surface and
into a Docker Compose surface that is distributed as an immutable, digest-pinned
OCI artifact and runs without cloning this repository.

Unblocked: Phase 8B established the canonical scenario/projection boundary
(`tests/acceptance/scenario`), so the Compose surface is a sibling projection
over that value rather than another imperative duplicated harness; and Phase 10
resolved the container-runtime image-identity differences a locally run Compose
demo meets.

Out of scope, per target: a second product architecture, a Compose-only product
feature, a hosted demo service and any Helm-surface change made only to make the
Compose projection easier.

Its record is section 15.

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

Reviewed at exact HEAD `837ef8bb`. That review froze the `_v2` namespace and its
migration work and kept Phase 8 narrowly reopened on the cache-inventory
boundary (B6 and B7); see section 2.

### Seventh-reopen verification — self-reported at `6be8719b`

Not an independent review. Re-verify at the exact SHA before accepting it.
Phase 8 stays a CANDIDATE; only an independent review closes it.

- Locally at the exact tree: `make ci` green end to end — `ci-static` (fmt, vet,
  gocyclo, golangci-lint with 0 issues, `check-section`, CLI-docs drift,
  UI-build drift, dashboard-SDK drift, plus the operator's own `ci-static`),
  `ci-gates` (the architecture import-boundary gate and the release gates),
  `ci-engine` (`ci-test` at 100.0% total coverage under the race detector, the
  engine e2e, `demo-fleet`), `ci-dashboard` (Vitest 1232 passed in 67 files),
  `ci-integration-kubernetes` (envtest plus 62 chart tests), `ci-e2e-envtest`
  and `ci-oci`.
- `pkg/oci` now imports `internal/cachehook`. `ci-gates` covers the import
  boundary and is green: the forbidden edges are `k8s.io/`, `sigs.k8s.io/`, the
  Kubernetes integration module and `gocloud.dev/`; a module-internal package is
  allowed.
- Focused counterexamples with `-race`: the B6 unit cases in
  `internal/fleetsrc`, the B7 index and lazy-load cases in `pkg/dashboard`, and
  the two production-wiring cases in `tests/e2e` (`go test -tags e2e`).
- Five mutation checks, each applied to production code and reverted, are
  itemized in section 2. All five kill the counterexample they target, and two
  of them reproduce the exact defect text (`checkout has 1 revisions, want both
  cached generations`; `service "checkout-a" is indexed under digest
  sha256:bbb…, which holds "checkout-b"`).
- The first `TestCacheSource_BuildIndex_ABundleAndItsIdentityAreOneGeneration`
  run was probed to confirm it is NOT vacuous: it indexes generation B whole
  (`service "checkout-b"`, tag `2.0.0`, ref `ghcr.io/other/checkout:2.0.0`,
  digest B), so the assertion block runs rather than the coherent-miss early
  return.
- No authored frontend input changed, so the committed UI bundle was NOT
  rebuilt; the drift gates are clean against the existing one.
- Two incidental working-tree changes produced by the gates themselves were
  reverted rather than committed: `go.work.sum` additions from module-graph
  resolution, and a `helm-docs` rewrite of the authored
  `charts/pacto-dev-gateway/README.md`. Neither is part of this repair, and
  `make ci` is green without them.
- The six Kind shards were NOT attempted locally this iteration; no code they
  exercise changed beyond the cache readers already covered above, and
  `evidence` and `operational-graph` still cannot run here for the Docker
  Desktop containerd reason recorded at `ci.mk:88-90`. They run in CI.
- `6be8719b`, `6e3a3627` and this document's commit are APPENDS on top of
  `837ef8bb`; no amend, no rebase, no force-push, no history rewrite.
- GitHub CI at `6be8719b` did NOT finish: pushing this document's commit
  `6e3a3627` cancelled it. `ci.yml` sets `cancel-in-progress` on
  `pull_request`, so the second push superseded the first run mid-flight — 26
  success, 9 cancelled (the six Kind shards, `dashboard-e2e`, `release-dry-run`,
  `repowise`), 2 skipped, plus `CodeQL` and the `required` aggregate failing
  BECAUSE of the cancellations. Nothing there failed on its own merits, and the
  full matrix is recorded at the final SHA below instead.
- GitHub CI at `6e3a3627`, the final SHA and the tree the reviewer should
  verify: 39 check runs — 36 success, `build` and `auto-merge` skipped, `CodeQL`
  failure. Every workflow green on `run_attempt=1`, no reruns: `changes`,
  `ci-static`, `ci-gates`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-integration-kubernetes`, `dashboard-e2e`,
  `operator-build`, `artifact-drift`, `release-version-test`, `release-dry-run`,
  `required`, `bundle`, `docs-check`, `repowise`, `validate`, and all six Kind
  shards individually — `ci-e2e-kind (dashboard)`, `ci-e2e-kind (upgrade)`,
  `ci-e2e-kind (reconcile)`, `ci-e2e-kind (evidence)`, `ci-e2e-kind
  (observation)`, `ci-e2e-kind (operational-graph)`. The whole run is green
  except the explicitly carried CodeQL check.
- Security is green and stays a DIFFERENT claim from the CodeQL alert
  attribution. The Security WORKFLOW succeeded: `Trivy`, `Trivy (image)`,
  `govulncheck`, `govulncheck (Go)`, `PR security summary` and all four
  `Analyze` jobs — `actions`, `go`, `javascript-typescript`, `python`. The
  separate `CodeQL` CHECK, published by `github-advanced-security` rather than
  by that workflow, reports "8 new alerts including 8 high severity security
  vulnerabilities": the carried PR-ref path-expression findings on
  `pkg/oci/cache.go`. That item stays OPEN below and is NOT closed by the green
  Security workflow.
- Review threads re-queried at `6e3a3627` (paginated, all 199 fetched): 199
  total, 189 resolved, 10 unresolved — unchanged in every count from
  `837ef8bb`. Six are `github-code-quality` comments on the GENERATED minified
  Mermaid chunk `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`
  (one "Superfluous trailing arguments", five "Useless assignment to local
  variable"), unchanged because no authored frontend input changed and the
  bundle was not rebuilt. Four are `github-advanced-security` CodeQL comments on
  AUTHORED code at `pkg/oci/cache.go` lines 375, 394, 395 and 666 — the same
  four findings, re-anchored by this iteration's edits to that file.
- PR at `6e3a3627`: open, DRAFT, mergeable. Merge-base with `origin/main` is
  still `a56b69e375f1881d645d3b39f3366f23398e72cf`.

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

#### Check and thread state at this document's own commit `c9d52bb9`

- 39 check runs: 36 success, `build` and `auto-merge` skipped, `CodeQL` failure
  — the same shape as `b0020460`, with all six Kind shards green.
- NOT green on the first attempt. `ci-e2e-kind (evidence)` failed once on an
  infrastructure fault and was RERUN: the operator image build inside the shard
  could not verify two unrelated third-party `go.mod` files because
  `sum.golang.org` answered `stream error: ... INTERNAL_ERROR` for
  `github.com/danielgtaylor/shorthand/v2@v2.4.0` and
  `github.com/gin-contrib/sse@v1.1.1`, so `docker build` exited before a cluster
  ever existed. `c9d52bb9` is a documentation-only commit and the shard is green
  at `b0020460` with identical code. The rerun is disclosed here rather than
  reported as a clean first pass.
- Review threads re-queried at `c9d52bb9`: 199 total, 189 resolved, 10
  unresolved — the same 6 generated (`ganttDiagram-6RSMTGT7-i4uZHW8n.js`) and 4
  authored (`pkg/oci/cache.go`) as at `b0020460`.

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

*Superseded at `93dca214`: Phase 8B is CLOSED and Phase 9 is ACTIVE. The
Phase-8B brief below is kept because its nine boundaries still bind every later
phase — boundary 8 is now satisfied, not waived.*

*Superseded again: Phases 9 and 10 are CLOSED and Phase 10B is ACTIVE (section
15). Boundary 6 above is now satisfied rather than waived — Phase 8B did
establish the canonical data/projection boundary without building the Compose or
OCI-distributed demo, and Phase 10B builds them on top of exactly that boundary.
Boundaries 1 to 5, 7 and 9 still bind.*

**Phase 8B — test architecture and harness consolidation.** Phase 8 is closed
(section 2). The branch now carries eight distinct kinds of test spread across
directories named for their history rather than for what they prove, six Kind
harnesses that each re-implement the same cluster lifecycle, and one canonical
acceptance scenario duplicated across shell heredocs, Go gate flags and inline
CRs. Phase 8B fixes the ARCHITECTURE of that, changing nothing about what any of
it proves. Its detailed target is TARGET section 10.

Hard boundaries for the Phase-8B session:

1. semantic coverage stays equal or increases; a test or fixture may be removed
   only when its invariant is duplicated and still proved elsewhere, obsolete by
   an accepted architectural replacement, or temporary review residue;
2. do NOT reopen Phase 8 (or 1 through 7) for stylistic improvement or
   theoretical hardening; a reopen requires a concrete correctness, security or
   data-loss counterexample;
3. do NOT flatten deterministic WASM/browser acceptance and live-cluster browser
   acceptance into one suite;
4. do NOT mass-convert shell for language uniformity; audit each harness
   individually and keep genuinely thin process orchestration in shell;
5. do NOT build a speculative test framework; inventory the imperative traces
   first and extract only stable, repeated semantics;
6. do NOT implement TARGET Phase 10B's Docker Compose or OCI-distributed demo —
   establish only the canonical data/projection boundary they will share;
7. do NOT erase the open CodeQL item in section 8; it is out of Phase-8B scope
   unless a Phase-8B change directly touches the alerted code;
8. do NOT start Phase 9, Phase 10 or Phase 10B, and do not begin Phase 9 until
   Phase 8B is independently reviewed and closed;
9. do NOT publish the transient inventory ledger (section 12) as permanent
   repository documentation.

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

## 12. Phase 8B inventory ledger — TRANSIENT

Migration bookkeeping for the ACTIVE phase. Deleted with this document in Phase
14. The durable repository documentation (`docs/maintainers/testing.md`) carries
the resulting ARCHITECTURE, never this ledger.

No relocation, rewrite or deletion in Phase 8B exists without a row here.

### 12.1 Canonical taxonomy

Every test belongs to exactly ONE semantic level, chosen by what it PROVES —
never by historical filename or implementation language.

| # | Level | Proves | Home | Language |
|---|-------|--------|------|----------|
| 1 | unit | one package's behaviour in isolation | beside the code | Go, Vitest |
| 2 | integration | two or more real components wired together in one process | `tests/integration/`, `integrations/kubernetes/test/integration/`, `pkg/oci` | Go |
| 3 | architecture / invariant | a structural rule about the repository itself | `tests/architecture/` | Go |
| 4 | local acceptance, cluster-free | a whole user story through real binaries, no cluster | `tests/acceptance/local/` | Go behind thin shell |
| 5 | Kind / system acceptance | the product against a real Kubernetes cluster | `tests/acceptance/kind/` | Go assertions behind thin shell |
| 6 | browser acceptance, deterministic | the built UI in a real browser over fixed data | `pkg/dashboard/frontend/e2e/` | Playwright/TS |
| 7 | live-browser acceptance | the built UI in a real browser over a real cluster | `pkg/dashboard/frontend/e2e-live/` | Playwright/TS |
| 8 | release verification | the release system produces the artifacts it claims | `tests/release/`, `release/orchestrator/*.test.mjs` | Go, Node |

Levels 6 and 7 stay SEPARATE suites. Determinism and liveness are different
properties; a merged suite proves neither.

### 12.2 Inventory and disposition

Columns: responsibility / level / permanent invariant proved / overlap /
permanent value / current -> correct location / current -> correct language /
disposition / canonical scenario consumed.

**Root Go suites**

| Item | Level | Invariant proved | Overlap | Value | Location move | Language | Disposition |
|---|---|---|---|---|---|---|---|
| `tests/e2e/*.go` (20 files, `//go:build e2e`, package `e2e`) | 2 integration — they drive `internal/cli` IN PROCESS against an `httptest` registry; nothing is end-to-end about them | CLI surface behaviour over the real validator, differ, packer, OCI client, MCP server and lockfile | none; the only suite at this level for the engine | permanent | `tests/e2e/` -> `tests/integration/` | Go -> Go | MOVE + retag `e2e` -> `integration`. Reclassification mandated by TARGET section 10. |
| `tests/e2e/helpers_test.go`, `fixtures_test.go` | 2 | shared in-process registry, `chdir` serialization, bundle builders | none | permanent | moves with the suite | Go | MOVE |
| `tests/e2e/testplugin/` | 2 fixture | a real external plugin binary on PATH | none | permanent | `tests/integration/testplugin/` | Go | MOVE |
| `tests/architecture/*.go` (4) | 3 | core stays k8s-free; collector docs exist; fleet routes stay neutral; OTel path stays offline | none | permanent | unchanged | Go | KEEP |
| `tests/release/*.go` (13) | 8 | one publisher per artifact, DAG shape, plan idempotency, immutability, module path, demo refs, stale links, dockerfile, chart recovery, docs paths/versioning, source SHA, adapter parity | none | permanent | unchanged | Go | KEEP; `stale_links_test.go` path allowlist updated for the fixture move |
| `tests/scripts/check_section_test.go` | 3 | the U+00A7 gate script actually fails on a glyph, and reports path:line, commit sha and PR-title/body source | none | permanent | `tests/scripts/` -> `tests/architecture/` | Go | MOVE. **It is currently a gate that never runs**: `ci-gates` runs only `./tests/architecture/... ./tests/release/...`, `ci-test` excludes `/tests/`, and `make e2e` runs only the e2e suite and `productready`. Moving it into `tests/architecture/` wires it in BY CONSTRUCTION. Coverage increase, not a reorganization. |

**Acceptance harnesses**

| Item | Level | Invariant proved | Overlap | Value | Location move | Language | Disposition |
|---|---|---|---|---|---|---|---|
| `tests/e2e/fleet-graph.sh` (179 lines) | 4 | the whole fleet story with no cluster: graph assembly, signed evidence ingest + durable replay + sequence ordering, OTel observe, reconcile (matched and observed-not-declared), impact BREAKING with corroborated confidence and non-zero exit | conceptually overlaps the Kind vertical, but it is the ONLY cluster-free proof and runs anywhere Go runs | permanent | `tests/acceptance/local/fleet-graph.sh` | thin shell over real `pacto` — CORRECT as shell | MOVE. Genuinely thin: builds two binaries, runs real CLI commands, greps their output. |
| `tests/e2e/localregistry/` | 4 fixture | a real OCI registry process for the cluster-free path | none | permanent | `tests/acceptance/local/localregistry/` | Go | MOVE |
| `tests/e2e/kind/lib.sh` (101 lines) | 5 shared harness | `pf` readiness-waiting port-forward, `dump_diag`, `keep_or_teardown` | none — this is the ONE existing shared harness | permanent | `tests/acceptance/kind/lib.sh` | shell | MOVE + EXTEND. `pf` behaviour is accepted Phase-8 behaviour and is preserved byte-for-byte. |
| `tests/e2e/kind/run.sh` (133) | 5 | prev-chart install -> upgrade -> dashboard path -> real RBAC `can-i` -> Compliant/Unknown(EVIDENCE_MISSING)/Compliant -> `evaluationCoverage` -> `pacto fleet search --k8s` -> uninstall leaves nothing | distinct scenario boundary | permanent | `tests/acceptance/kind/reconcile.sh` | shell | MOVE + RENAME (`run.sh` names nothing) + refit onto shared harness |
| `tests/e2e/kind/dashboard-modes.sh` (152) | 5 | the operator does not crashloop across the four `dashboard.enabled` transitions; exactly one Running pod, zero restarts | distinct scenario boundary | permanent | `tests/acceptance/kind/dashboard-modes.sh` | shell | MOVE + refit |
| `tests/e2e/kind/v4-to-v5-upgrade.sh` (224) | 5 | a REAL cross-major chart + CRD migration: published v4 chart and CRDs, server-side apply, `helm upgrade` to v5, existing resources survive | distinct scenario boundary | permanent | `tests/acceptance/kind/upgrade-v4-v5.sh` | shell | MOVE + refit. The pinned `V4_DIGEST` fail-closed drift check is preserved. |
| `tests/e2e/kind/fixtures/pacto-operator-v4/` (29 files) | 5 fixture | byte-faithful published v4 chart — permanent cross-major compatibility value | none | permanent | `tests/acceptance/kind/fixtures/pacto-operator-v4/` | n/a | MOVE UNCHANGED. Provenance already documented in its `SOURCE.md`; the note stays next to it. `tests/release/stale_links_test.go` allowlists the new path. |
| `tests/e2e/kind/evidence.sh` (324) | 5 | operator-managed Evidence Server: Deployment/Service/RETAINED PVC, replicas=1, durable commit, Evidence source API, dashboard Fleet API, CLI over the same store, replay 409, newer sequence accepted, restart recovery, manifest projection physically rewritten, corrupt record -> degraded, disable retains PVC and drops the source, re-enable recovers | duplicates the in-cluster registry YAML, the trust keygen+Secret and push-bundle-and-extract-digest with `operational-graph.sh`, verbatim | permanent | `tests/acceptance/kind/evidence.sh` | shell | MOVE + refit; the three duplicated blocks move to `lib.sh` |
| `tests/e2e/kind/observation.sh` (410) | 5 | 8 claims: declared Helm observation sources become read-only mounts under their declared names; the ConfigMap and PVC sources each produce a stable Data Source identity; the observed `orders->checkout` edge is attributed to `orders-traces`; a symlink-ESCAPING source contributes nothing; a broken source is explicit `SOURCE_UNAVAILABLE` knowledge, not a silently empty graph; removing sources leaves no orphaned wiring | distinct scenario boundary | permanent | `tests/acceptance/kind/observation.sh` | shell + **three embedded `python3 -c` blocks doing semantic JSON assertion** -> shell + Go | MOVE + **REWRITE the assertions in Go**. This is the named Phase-8 debt ("no semantic JSON parsing in shell"). The eight claims are preserved exactly; only the language asserting them changes. |
| `tests/e2e/kind/operational-graph.sh` (369) | 5 + 7 driver | the full live vertical and the live browser leg; publishes four real bundles, wires the observation source, applies two digest-pinned CRs, signs and sends an EvidenceEnvelope | its four bundle heredocs are the canonical scenario, duplicated as flags into `productready` | permanent | `tests/acceptance/kind/operational-graph.sh` | shell | MOVE + refit; the four heredocs are REPLACED by a projection of the canonical scenario. Every semantic claim already lives in Go; that stays true. |
| `tests/e2e/kind/productready/` (`main.go` 641 + `main_test.go` 480) | 5 gate | the coherent 14-fact / two-snapshot Product gate; discovers every key through `/api/fleet/entities`, never constructs one; `adopt()` enforces single-snapshot coherence; emits the discovered-key fixture the live browser suite consumes | none | permanent | `tests/acceptance/kind/productready/` | Go | MOVE. Facts and the `adopt()` rule are accepted Phase-8 behaviour and unchanged; only the source of its EXPECTATIONS moves from flags to the canonical scenario. |

**Browser suites**

| Item | Level | Invariant proved | Overlap | Value | Location | Disposition |
|---|---|---|---|---|---|---|
| `pkg/dashboard/frontend/e2e/` (24 specs + 2 helpers, 5731 lines) | 6 | the built WASM demo in Chromium: a11y (axe), keyboard, mobile/responsive, typography, headings, page TOC, graph state and visuals, inventory, references, suggest, SWR, owners, place, product scale, novice journeys, Mermaid render, workspace geometry | none | permanent | unchanged | KEEP. Deterministic browser acceptance is NOT flattened into the live suite. |
| `pkg/dashboard/frontend/e2e-live/` (`fixture.ts` 58 + `product-journeys.spec.ts` 326) | 7 | live journeys A-H against the real port-forwarded Product, over keys DISCOVERED by `productready` | none | permanent | unchanged | KEEP; only the `tests/e2e/kind/...` path references in its comments are updated |
| `pkg/dashboard/frontend/src/**/*.test.ts` (67 files) | 1 | frontend unit behaviour | none | permanent | unchanged | KEEP |

**Kubernetes integration and release**

| Item | Level | Invariant proved | Value | Location | Disposition |
|---|---|---|---|---|---|
| `integrations/kubernetes/internal/**/*_test.go` (40) | 1 + 2 (envtest via `suite_test.go`) | controller, dashboard, evidence, observer, loader, credentials, prober, metrics behaviour | permanent | unchanged | KEEP |
| `integrations/kubernetes/test/e2e/` (`//go:build e2e`) | 2 | operator acceptance against envtest, no cluster | permanent | unchanged | KEEP. Already correctly located inside the module that owns it. |
| `integrations/kubernetes/test/integration/oci_test.go` | 2 | operator OCI loading against a real registry | permanent | unchanged | KEEP |
| `integrations/kubernetes/charts/pacto-operator/tests/*.yaml` (8) | 3 | helm-unittest template invariants | permanent | unchanged | KEEP |
| `release/orchestrator/detect.test.mjs` | 8 | transaction detection from a changeset state | permanent | unchanged | KEEP |
| `release/orchestrator/dry-run.sh`, `test-release-version.sh`, `verify-k8s-standalone.sh` | 8 | the real release simulation, the real `release:version` run, the external-consumer module proof | permanent | unchanged | KEEP. Genuinely thin process orchestration around real release tooling. |
| `examples/demo/source_embed_test.go` | 1 | the embedded demo source | permanent | unchanged | KEEP |

**Nothing is deleted in Phase 8B.** Every row above is keep, move, rename or
rewrite-in-place. No invariant loses a prover.

### 12.3 Duplicated shared concerns found across the six Kind harnesses

Counted by grep across `run.sh`, `dashboard-modes.sh`, `v4-to-v5-upgrade.sh`,
`evidence.sh`, `observation.sh` and `operational-graph.sh`.

| Concern | Copies today | Disposition |
|---|---|---|
| resolve the chart version out of `release-release-plan`/manifest via `python3 -c 'import json...'` | 4 | ONE `chart_version` in `lib.sh` |
| `docker build` the operator and dashboard images | 5 | ONE `build_images` |
| `helm package` the chart | 4 | ONE `package_chart` |
| `kind create cluster` if absent | 6 | ONE `ensure_cluster` |
| `kind load docker-image` | 6 | folded into `ensure_cluster` / `load_images` |
| `KUBECONFIG=$(mktemp)` + `kind get kubeconfig` | 6 | folded into `ensure_cluster` |
| in-cluster registry Deployment + Service YAML | 2, byte-identical | ONE `install_registry` |
| trust keypair + Secret | 2, identical | ONE `install_trust_key` |
| push a bundle and extract its digest | 2 + a variant | ONE `push_bundle` |
| `pass()` / `fail()` reporting | 4 | ONE pair in `lib.sh` |
| `wait_status` CR condition polling | 3 | ONE `wait_status` |
| `wait_ready` rollout polling | 4 | ONE `wait_ready` |
| ad-hoc `for i in $(seq ...)` eventually loops | many | ONE `eventually` |
| `go build -o "$(mktemp)" ./cmd/pacto` | 4 | ONE `build_pacto` |
| port-forward with readiness | already ONE (`pf`) | unchanged — accepted behaviour |
| diagnostics dump | already ONE (`dump_diag`) | unchanged |
| cleanup / keep-cluster | already ONE (`keep_or_teardown`) | unchanged |

Scenario-specific orchestration stays explicit in each harness. Only the stable
shared concern is centralized.

### 12.4 Canonical scenario duplication

The same commerce scenario is described FOUR times today:

1. four bundle heredocs in `operational-graph.sh` (payments, checkout 1.0.0,
   checkout 1.1.0 with `/cart` removed = Breaking, orders declaring the checkout
   dependency);
2. the expected facts, passed back into `productready` as `-domain`,
   `-checkout-a`, `-checkout-b`, `-snapshots` flags;
3. inline CR contracts in `observation.sh` and `reconcile.sh`;
4. a single checkout bundle in `evidence.sh`, and web/payments in
   `fleet-graph.sh`.

Phase 8B establishes ONE declarative Go scenario able to express services,
revisions, targets, sources, relationships, evidence, expected Product facts and
journey inputs, with the projections that ALREADY have a consumer:

- bundle materialization (replaces the heredocs);
- expected Product facts (replaces the `productready` flags);
- the discovered-key fixture handed to the live browser suite (already exists,
  now sourced from the same declaration).

The Helm and Compose projections TARGET Phase 10B needs are NOT implemented —
only the boundary they will share. No speculative framework: the scenario is
data plus the two projections that have a caller today.

### 12.5 Nomenclature

`make e2e` currently means "in-process CLI integration tests", `run.sh` names
nothing, and `demo-fleet` is the cluster-free acceptance. New names reveal the
level; the old names remain as aliases so nothing a contributor or a workflow
already invokes breaks.

| Old | New | Alias kept |
|---|---|---|
| `make e2e` | `make test-integration` | yes |
| `make demo-fleet` | `make test-acceptance-local` | yes |
| `make ci-e2e-kind-*` | `make test-acceptance-kind-*` | yes |
| `make e2e-dashboard-wasm` | `make test-browser` | yes |
| `make e2e-dashboard-kind-browser` | `make test-browser-live` | yes |
| `tests/e2e/kind/run.sh` | `tests/acceptance/kind/reconcile.sh` | n/a |
| `tests/e2e/kind/v4-to-v5-upgrade.sh` | `tests/acceptance/kind/upgrade-v4-v5.sh` | n/a |

Workflow JOB IDS are left alone: `required` is the single branch-protection
check and renaming ids risks silently unsatisfiable protection rules. Only the
human-facing `name:` fields and the invoked target names change. The inventory
does not demonstrate that a broad workflow rewrite is required, so there is not
one.

### 12.6 Disposition AS EXECUTED

Appended commits, oldest first. Every path that moved, was rewritten or
disappeared is accounted for below; nothing changed that is not in a row.

| Commit | Scope |
|---|---|
| `66d39eb2` | relocation + retag: `tests/e2e` -> `tests/integration`, `tests/e2e/kind` -> `tests/acceptance/kind`, `tests/e2e/fleet-graph.sh` + `localregistry` -> `tests/acceptance/local`, `tests/scripts` -> `tests/architecture`; Make/CI nomenclature and aliases |
| `a882f8ed` | shared harness consolidation into `tests/acceptance/kind/lib.sh` |
| `a9943c9d` | the embedded `python3` observation assertions become `tests/acceptance/kind/obscheck` |
| `174d41a7` | the canonical declarative scenario, `tests/acceptance/scenario` |
| `5d98222e` | the durable `docs/maintainers/testing.md` + nav entry |
| `cf75e5ea` | this section |
| `8a68ef1c` | verification fix: `ineffassign` on `obscheck`'s unreachable first timeout report (`ci-lint`) |
| `e1a73567` | verification fix: `fleet-graph.sh` moved one level deeper, so its `ROOT` resolved to `tests/` (`test-acceptance-local`) |

The last two are the matrix doing its job on the move itself: the relocation
changed a script's depth and the rewrite left a dead initializer, and both were
caught by `make ci` before the push rather than by a reviewer.

**Relocations (`66d39eb2`).** Git recorded them as renames, so history follows
the files.

| From | To | Similarity | Content change |
|---|---|---|---|
| `tests/e2e/*.go` (20 files + `e2e_test.go` -> `main_test.go`) | `tests/integration/` | 96-99% | build tag `e2e` -> `integration`, package `e2e` -> `integration` only |
| `tests/e2e/testplugin/main.go` | `tests/integration/testplugin/main.go` | 100% | none |
| `tests/e2e/fleet-graph.sh` | `tests/acceptance/local/fleet-graph.sh` | 98% | its own path references only |
| `tests/e2e/localregistry/main.go` | `tests/acceptance/local/localregistry/main.go` | 100% | none |
| `tests/e2e/kind/run.sh` | `tests/acceptance/kind/reconcile.sh` | 73% | rename + harness refit |
| `tests/e2e/kind/v4-to-v5-upgrade.sh` | `tests/acceptance/kind/upgrade-v4-v5.sh` | 86% | rename + harness refit |
| `tests/e2e/kind/{dashboard-modes,evidence,observation,operational-graph}.sh` | `tests/acceptance/kind/` | 80 / 74 / 59 / 56% | harness refit; observation and operational-graph additionally in `a9943c9d` / `174d41a7` |
| `tests/e2e/kind/fixtures/pacto-operator-v4/` (29 files) | `tests/acceptance/kind/fixtures/pacto-operator-v4/` | 100% except `SOURCE.md` (93%, its own path) | byte-identical chart; provenance note stays beside it |
| `tests/e2e/kind/productready/{main.go,main_test.go}` | `tests/acceptance/kind/productready/` | 77 / 83% | scenario-sourced expectations (`174d41a7`) |
| `tests/scripts/check_section_test.go` | `tests/architecture/check_section_test.go` | 99% | package + script path only |
| `tests/e2e/kind/lib.sh` | `tests/acceptance/kind/lib.sh` | recorded as delete + add (101 -> 345 lines) | superset: `pf`, `dump_diag`, `keep_or_teardown` preserved verbatim |

`tests/e2e/` and `tests/scripts/` no longer exist. Nothing was deleted: every
file has a destination row.

**Shared harness, one implementation each (`a882f8ed`).** `lib.sh` 101 -> 345
lines; the six scenarios 1612 -> 1301 lines. Every function it now owns, and
what it replaced:

| `lib.sh` | Replaced |
|---|---|
| `pass` / `fail` | 4 copies (`fail` -> stderr preserved) |
| `eventually` | many ad-hoc `for i in $(seq ...)` loops |
| `ensure_cluster`, `use_existing_cluster`, `load_images`, `delete_cluster`, `down_cluster` | 6 copies of create-if-absent + `KUBECONFIG=$(mktemp)` + `kind load` |
| `release_version` | 4 copies of `python3 -c 'import json...'` — now `node`, which the plan builder already requires, so the second interpreter is gone |
| `build_operator_images` | 5 copies |
| `package_chart` | 4 copies (each call packages into its own empty dir) |
| `build_pacto` | 4 copies |
| `wait_ready`, `deploy_exists`, `wait_managed_ready`, `pacto_status`, `wait_pacto_status` | 4 + 3 copies of rollout and CR-condition polling |
| `install_registry` | 2 byte-identical in-cluster registry manifests |
| `trust_keypair` | 2 identical keygen + Secret blocks |
| `push_bundle` | 2 copies + a variant |
| `dump_diag`, `pf`, `keep_or_teardown`, `helm_teardown` | already single; moved unchanged |

Scenario-specific orchestration stayed explicit in each harness: EXIT traps,
fixtures, assertions, and the `up/status/logs/down` subcommands. No helper
acquired a boolean parameter to serve two callers.

**Shell retained, and why each is thin (`a882f8ed`).** All six Kind harnesses
were audited individually; none was converted for uniformity.

| Harness | Lines | Why shell is correct |
|---|---|---|
| `dashboard-modes.sh` | 152 -> 144 | four `helm upgrade --set` transitions and a pod/restart count; no semantic decoding |
| `reconcile.sh` | 133 -> 121 | install, upgrade, `kubectl auth can-i`, uninstall; verdicts come from CR conditions |
| `upgrade-v4-v5.sh` | 224 -> 211 | a real chart+CRD migration is a command sequence; the pinned `V4_DIGEST` fail-closed check is preserved |
| `evidence.sh` | 324 -> 249 | lifecycle: PVC retention, restart recovery, disable/re-enable |
| `observation.sh` | 410 -> 310 | mounts and Helm wiring; every semantic assertion now goes through `obscheck` |
| `operational-graph.sh` | 369 -> 266 | brings the vertical up and hands off; every semantic assertion already went through `productready` |
| `local/fleet-graph.sh` | 179 | builds two binaries, runs real CLI commands, greps their output |
| `release/orchestrator/*.sh` | unchanged | real release tooling, untouched by Phase 8B |

**Rewrites, and the invariant each preserves.**

| Item | Preserved invariant |
|---|---|
| `obscheck/main.go` (440) + `main_test.go` (513), new | the eight observation claims, unchanged in meaning. They were ~90 lines of `python3` embedded as here-strings — untestable, so an assertion could stop asserting and only a passing cluster run would show it. Now a typed decode with a suite over recorded payloads. `grep -rn python3 tests/acceptance/` returns nothing. |
| `scenario/scenario.go` (340) + `operationalgraph.go` (126) + `scenario_test.go` (343) + `project/main.go` (54), new | ONE declaration of the operational-graph fixture. Projections: `Materialize` (the four bundle heredocs), `TraceExport` (the OTLP literal — verified byte-identical to the shell it replaced), `FactCount` (the gate's denominator). Two claims got STRONGER: the export can no longer name a service the contract does not depend on, and the fact count tracks the scenario instead of a hand-written `14`. |
| `productready/main.go` 641 -> 686, `main_test.go` 480 -> 557 | all 14 facts, the two-snapshot rule and the `adopt()` coherence logic are unchanged. Flags 17 -> 6; expectations now come from the scenario. `TestGateProvesWhatTheScenarioDeclares` mutation-checks the move with 7 scenario mutations that each must fail the gate. |
| `e2e-live/fixture.ts`, `product-journeys.spec.ts`, `playwright.live.config.ts` | path references only; journeys A-H untouched |
| `tests/release/stale_links_test.go` | fixture-path allowlist follows the move |
| `Makefile`, `ci.mk`, `.github/workflows/ci.yml`, `CONTRIBUTING.md`, four docs pages | target/path renames with aliases per 12.5; workflow job ids untouched |

**Coverage.** Semantic coverage is equal or greater everywhere. Nothing was
removed. Two additions: `check_section_test.go` now actually runs (it sat in
`tests/scripts/`, which no CI leg invoked), and `make test-integration` widened
from `./tests/e2e/kind/productready/` to `./tests/acceptance/kind/...`, so
`obscheck` is tested by construction.

### 12.7 Narrow closure at `2126fdcc` — two blockers, repaired

The independent Phase 8B review at `2126fdcc` accepted everything in 12.1
through 12.6 and left exactly two blockers. Neither reopens Phase 8, the
inventory, or any accepted relocation. Repaired append-only in three commits;
nothing was amended, rebased or force-pushed.

**What the review found.**

*A — the canonical scenario was not yet the ONLY semantic declaration.* The
scenario package removed the bundle heredocs and the OTLP literal, but one level
up the harness still knew the fixture on its own. `operational-graph.sh`
restated: four directory-to-tag `push` mappings; `OBS_SOURCE=orders-traces`; a
Pacto CR heredoc naming `checkout` and `orders` and choosing which checkout
revision was deployed; an evidence payload heredoc naming `payments` and
`remote-eu`. And one restatement DISAGREED with the declaration: the harness
signed `--producer demo` while `scenario.Evidence.Producer` said `remote-eu`.
Nothing failed, because `Producer` had no consumer — a declarative field that
was never read.

*B — a real security failure.* `govulncheck` reported seven reachable standard
library vulnerabilities on Go 1.26.5, all fixed in 1.26.6.

**The counterexamples that failed on `2126fdcc`.** Each names a value the
scenario declares and the harness independently restated, so changing the
declaration changed nothing the cluster saw. At `2126fdcc` the harness carried
each as a literal:

| Declaration | Restated at `2126fdcc` |
|---|---|
| repository | `push "$BDIR/checkout-a" checkout:1.0.0` (L100) |
| bundle directory / version | the same four `push` lines (L99-102) |
| deployed revision | `oci: ${REG_HOST}/demo/checkout@${CHECKOUT_A}` (L158) |
| observation source id | `OBS_SOURCE=orders-traces` (L35) |
| evidence subject | `"name": "payments"` (L179, L184) |
| evidence source environment | `"Source": "remote-eu"`, `"collector": "remote-eu"` (L181, L186) |
| signer producer | `--producer demo` (L190) — contradicting the declaration |

**The repairs.**

| Commit | Scope |
|---|---|
| `6a532459` | blocker B: all six Go declarations 1.26.5 -> 1.26.6 |
| `078554bf` | blocker A: `Plan` / `PactoCRs` / `EvidencePayloads`, the `bundles`/`cluster` projector, the plan-reading harness, `trust_keypair` |
| `e5c2eb8a` | the `dashboard-redesign-plan.md` build-tag correction |

*Blocker A (`078554bf`).* Three projections, in two phases because a digest does
not exist until the push has happened: `Plan` (before — what to publish, run,
mount and sign), `PactoCRs` and `EvidencePayloads` (after — pinned to the real
digests). The shell reads tab-delimited records; it never sources or evaluates
generated code. Every field is validated on the way out (non-empty, and free of
the tab, newline and carriage return that could forge a record) and each loop
re-checks its record's arity, so a record that grew or lost a field fails loudly
instead of shifting every value left.

Two identities were separated where one field had conflated them and the harness
had quietly picked a third value for the one that reached the wire:

- `Evidence.Source` — WHERE observations were collected. Payload data: the
  EvidenceSet's `Source` and each observation's collector.
- `Evidence.Signer{Producer, KeyID}` — WHO signed the envelope. The trust store
  binds the key to this producer, so signing as anyone else is rejected at
  ingestion rather than silently accepted.

Both are now consumed by the real signed envelope. `trust_keypair` takes the key
id and producer and reads the public key's FILENAME back off disk, because that
filename is the binding; reconstructing it is how a scenario installs one binding
and signs under another. `evidence.sh`'s call is unchanged by the defaults.

`Service.Workload{Name, Interface, Port}` is new: the CRs are marshalled from
typed Go values rather than interpolated into a heredoc, a zero port means the
workload is deliberately unexposed (no Service, no binding, so the operator's
only honest verdict on interface availability is Unknown), and which revision a
CR pins comes from the scenario's `Deployed` flag. The evidence payload reports
the workload type read back out of the SUBJECT'S OWN published contract, so an
envelope cannot claim something its bundle contradicts.

Scope held: runtime values stay runtime inputs (registry address, digests,
forwarded ports, temp directories), and the one-consumer Helm values for the
operator image, the insecure registry and the enabled components are NOT
projected — they have no counterpart in the fixture, so declaring them would be
uniformity for its own sake. `operational-graph.sh` 266 -> 285 lines: the
semantic restatements are gone, replaced by validated readers.

*Blocker B (`6a532459`).* All six declarations move together, because the
toolchain that matters is the one the PRODUCTION image is built with: `go.mod`,
`go.work`, `Dockerfile`, `integrations/kubernetes/go.mod`,
`integrations/kubernetes/Dockerfile`, and the consumer module
`release/orchestrator/verify-k8s-standalone.sh` synthesizes. A `go.mod` bump
alone would let a builder pinned to 1.26.5 satisfy the requirement by
downloading a newer toolchain in some contexts and not others, hiding the old
builder rather than replacing it. `git grep '1\.26\.5'` over the tracked tree
returns nothing, and every workflow resolves Go via `go-version-file: go.mod`.

**Verification.**

| Check | Result |
|---|---|
| `govulncheck` at `2126fdcc` (detached worktree, Go 1.26.5) | 7 error-level: GO-2026-5026, -5972, -6088, -6089, -6090, -6091, -6218 |
| `govulncheck` after the bump (Go 1.26.6) | 0 error-level |
| `git grep '1\.26\.5'` (tracked tree) | no match |
| `go test -race ./tests/acceptance/scenario/...` | ok — the 7 counterexamples plus the grammar, CR and payload suites |
| `go test -race ./tests/acceptance/...` | ok (`obscheck`, `productready`, `scenario`) |
| `go vet ./...` and `go vet -tags integration ./...` | clean |
| `make ci` | exit 0, re-run at `76ed7fee` after the filter change |
| `make artifact-drift` | OK |
| `make release-dry-run` | `RELEASE-DRY-RUN OK` — real artifacts to `localhost:5001`, digest idempotency + immutability + resume proven, `K8S-MODULE-STANDALONE OK` |
| `go test -tags integration ./tests/integration/...` | ok, 88.6s |
| `make test-browser` (offline WASM) | 219 passed, exit 0 |
| offline evidence chain | the projected payload signs under `remote-eu-collector__demo.pub` and verifies; the SAME trust store rejects an envelope claiming any other producer |
| plan reader | the real plan parses to the expected push / observation / workload / signer / evidence records with no `eval` and no sourcing |
| GitHub CI at `e5c2eb8a` (run 31782344077) | success — every job, including all six `ci-e2e-kind` shards (`operational-graph`, `dashboard`, `evidence`, `observation`, `reconcile`, `upgrade`), `dashboard-e2e`, `artifact-drift`, `release-dry-run` |
| GitHub Security at `e5c2eb8a` (run 31782344066) | success — `govulncheck (Go)`, `Trivy (image)`, `PR security summary` |
| tracked tree | no agent files (`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md` untracked) and no generated test artifacts |

The live browser journeys A-H and the six Kind shards run in GitHub CI; the
local Docker Desktop containerd store breaks `kind load`, which is why the live
vertical is proved there rather than here. The `operational-graph` shard is the
one that exercises the rewritten harness end to end, and it is green at the
final SHA.

**Out of scope and untouched by this repair:** the carried CodeQL
path-expression item, the generated Mermaid findings, Phase 9, Phase 10 and
Phase 10B. Phase 8 remains ACCEPTED and CLOSED. Phase 8B remains a CANDIDATE
pending independent verification of exactly these repairs.

### 12.8 Narrow closure at `1a04807d` — two blockers, repaired

A second independent Phase 8B review, at `1a04807d`, accepted the 12.7 closure
and left exactly two blockers. Neither reopens Phase 8, the inventory, or any
accepted relocation. Repaired append-only in four commits — the two repairs,
this ledger section, and the commit that recorded the workflow results this
section could only carry after they existed; nothing was amended, rebased or
force-pushed.

**What the review found.**

*A — an ambiguous deployment was resolved, unanimously and in silence.*
`Service.DeployedRevision` returned the FIRST revision flagged `Deployed`. Every
surface called it independently and every surface therefore agreed: a fixture
marking two revisions deployed produced a plan, a CR pinning the first, and a
Product gate proving that same first one. The second deployment was erased with
nothing disagreeing. Zero deployed revisions was the same failure from the other
side — a CR that would pin nothing — caught only by `Plan`, so `PactoCRs` and the
gate each resolved it their own way. And a service with no workload at all could
carry a `Deployed` flag indefinitely: no projection asks a workload-less service
what it deploys, so nothing ever looked.

*B — the fixture's own tests were in no CI leg.* `ci-test` excludes `/tests/`.
`test-integration` named `./tests/acceptance/kind/...` explicitly to keep the
live gates tested, but `tests/acceptance/scenario` — the canonical declaration
every surface projects from, plus `plan_test.go` beside it — sat one directory
over, reached by no target `make ci` depends on. The 12.7 counterexamples had
never executed in required CI.

**The counterexample that passed on `1a04807d`.** Set both `checkout` 1.0.0 and
1.1.0 to `Deployed: true`:

| Surface | Behavior at `1a04807d` | Behavior now |
|---|---|---|
| `Scenario.Plan` | no error | refused: *declares 2 deployed revisions (1.0.0, 1.1.0)* |
| `Scenario.PactoCRs` | no error; the emitted CR pins 1.0.0 | refused, same diagnostic |
| `Scenario.Validate` | did not exist | refused before any projection runs |
| Product gate | selected the first revision and accepted a target linked to it | refuses before it polls; `probe` reports rather than resolves |
| `Scenario.FactCount` | selected the first deployed revision and counted ONE target, silently erasing the second deployment | counts a target per WORKLOAD — one, derived rather than selected |

**The repairs.**

| Commit | Scope |
|---|---|
| `6e287a4f` | blocker A: `DeployedRevision` returns `(Revision, error)`, `Scenario.Validate`, both projections, `FactCount`, the Product gate, the counterexamples |
| `30535f43` | blocker B: `test-integration` runs `./tests/acceptance/...` under `-race` |
| `bf0b932e` | this ledger section |
| `93dca214` | the GitHub workflow results for this range, recorded once they existed |

*Blocker A (`6e287a4f`).* One rule, enforced where the fixture is read, not once
per caller. `DeployedRevision` now returns exactly one revision or an error
naming both the count and the versions, so the value that was being chosen can no
longer be chosen at all. `Scenario.Validate` runs that rule over every service
and adds the case no projection asks about — a service with no workload that
marks a revision deployed — because a projection only interrogates services it
projects, and that is precisely how deployment semantics were acquired
accidentally. `Plan` and `PactoCRs` both validate before projecting, so the two
phases cannot disagree about whether the fixture is answerable. The gate
validates before it polls and, inside `probe`, reports the error instead of
swallowing it. `FactCount` derives its target count from `svc.Workload != nil`
and never reads the flags: counting them would be the derivation resolving an
ambiguity on its own — the thing `Validate` exists to stop — and would hand the
gate a denominator no projection agreed to.

Written test-first, and the tests were proved to bite by mutation rather than
assumed to: relaxing `len(deployed) == 1` to `>= 1` fails
`TestPlan_RefusesADeploymentThatCannotMeanOneThing/two_deployed_revisions...`,
`TestDeployedRevision_IsExactlyOneOrAnError/two_deployed_revisions...`,
`TestValidate_TheFixtureMeansOneThing/two_deployed_revisions` and
`TestGateRefusesAnAmbiguousDeployment`; relaxing the workload-less branch fails
the other two subtests. Both mutations were reverted.

No fixture-validation framework was added. `Validate` is one loop over one rule;
everything else the fixture must satisfy is already proved by the
counterexamples beside it.

*Blocker B (`30535f43`).* The narrowest change that fixes the class rather than
the instance: the path widens from the two gate packages to the acceptance
subtree, so `scenario` and `scenario/project` are in required CI by construction
and so is any sibling added later — which is the failure that made this
necessary. `-race` was added to match every other test level, and the timeout
raised to suit it. `make ci` -> `ci-engine` -> `test-integration` is unchanged;
no workflow job was added, and no existing leg was weakened or removed.

**Verification.**

| Check | Result |
|---|---|
| `make -n ci-engine \| grep tests/acceptance` | `go test -race ./tests/acceptance/... -count=1 -timeout 180s` — the scenario package is inside `make ci` by construction |
| `go test -race ./tests/acceptance/scenario/...` | ok — 29 tests, 25 subtests, across the deployment, `Validate`, plan, CR, payload and grammar suites |
| focused adversarial run | all of `TestDeployedRevision_IsExactlyOneOrAnError` (6), `TestValidate_TheFixtureMeansOneThing` (4), `TestPlan_RefusesADeploymentThatCannotMeanOneThing` (3), `TestPactoCRs_FollowTheDeployedRevision`, `TestFactCount_TracksTheScenario` (7) pass |
| `TestGateRefusesAnAmbiguousDeployment` | pass — a fully-passing product plus an ambiguous scenario yields problems, not facts |
| mutation run (both relaxations) | exactly the intended failures, listed above; reverted |
| `go test -race ./tests/acceptance/...` | ok (`obscheck`, `productready`, `scenario`) |
| `make test-integration` | ok — integration 65.8s, then the acceptance subtree under `-race` |
| `make ci` | exit 0; total coverage 100.0%; the acceptance subtree runs inside it |
| `make artifact-drift` | `artifact-drift: OK` |
| `make release-dry-run` | `RELEASE-DRY-RUN OK` — real artifacts to `localhost:5001`, digest idempotency + immutability + resume proven, `K8S-MODULE-STANDALONE OK` |
| `make test-browser` (offline WASM) | 219 passed, exit 0 — the same full deterministic suite recorded in 12.7 |
| GitHub CI at `bf0b932e` (run 31806146823) | success — every job, including all six `ci-e2e-kind` shards (`operational-graph`, `dashboard`, `evidence`, `observation`, `reconcile`, `upgrade`), `dashboard-e2e`, `artifact-drift`, `release-dry-run`, `ci-e2e-envtest`, `ci-integration-kubernetes`, `release-version-test` |
| `required` at `bf0b932e` | SUCCESS |
| GitHub Security at `bf0b932e` (run 31806146827) | success — `govulncheck (Go)`, `Trivy (image)`, `PR security summary` |
| Docs check / Pacto Contract CI / Validate PR title / Repowise at `bf0b932e` | success |
| CodeQL at `bf0b932e` | failure, unchanged and carried: the same 8 high `go/path-injection` alerts in `internal/app/resolve.go` and `pkg/oci/cache.go`, byte-identical to `1a04807d`. Neither file is touched by these commits; `required` does not include CodeQL |

**Out of scope and untouched by this repair:** everything 12.7 lists — the
carried CodeQL path-expression item, the generated Mermaid findings, Phase 9,
Phase 10 and Phase 10B — plus TARGET itself, which is unmodified. The Go 1.26.6
bump and its history stand as recorded in 12.7. Phase 8 remains ACCEPTED and
CLOSED. This section records a repair, not a closure; the closure is the
independent review at `93dca214`, recorded in the Phase 8B section above.

## 13. Phase 9 record — CANDIDATE at `2124661b`

Real browser E2E over the built MkDocs site. Written test-first on top of the
Phase 8B taxonomy, appended in five commits on `93dca214`; nothing amended,
rebased or force-pushed. CANDIDATE, not CLOSED — only an independent review
closes a phase.

**What the existing gates could not see.** Three of them run today and all three
stop short of the artifact a reader actually opens:

| Gate | What it proves | What it cannot see |
|---|---|---|
| `make mermaid-check` | every fenced `mermaid` block parses and renders under mermaid-cli — 19/19 | renders OUTSIDE the site, with its own runtime and no theme |
| `pkg/dashboard/frontend/e2e/mermaid.spec.ts` | contract documentation renders inside the DASHBOARD | a different product, a different bundle, a different renderer path |
| `mkdocs build --strict` (inside `make docs-check`) | the site builds, links and anchors resolve, nav is complete | never loads a page in a browser; `superfences` emits `<pre class="mermaid">` for CLIENT-side rendering, so a built page carries diagram SOURCE and the build is happy either way |

The gap is exact: nothing asserted that the shipped HTML turns into diagrams.

**The failing test at `93dca214`.** `e2e-docs-site/mermaid.spec.ts` builds the
real site through a one-key overlay on the real `mkdocs.yml`, serves it over
localhost, and drives Chromium at three pages: a core page
(`/operational-graph/`, two diagrams), a page written by the integration hook
(`/integrations/kubernetes/overview/`, one diagram), and an instant navigation
from the core page to `/impact/`. Every off-site request is aborted at
`page.route`, so the site may use only what it ships.

All three failed, identically and for the intended reason:

```
Expected: 2   Received: 0     locator('div.mermaid svg')   (34 retries)
```

The declared-count assertions passed in the same runs — the spec reads
`<pre class="mermaid">` straight out of the served HTML and found 2, 1 and 2 —
so the failure was the runtime, not the harness. The one flaw the test found is
one no build-time gate can find: **the site had no Mermaid runtime of its own.**

**The defect.** Material's `mountMermaid`, extracted from the theme's own
sourcemap in the built site:

```ts
typeof mermaid === "undefined" || mermaid instanceof Element
  ? watchScript("https://unpkg.com/mermaid@11/dist/mermaid.min.js")
  : of(undefined)
```

With no global `mermaid`, every published page fetched an UNPINNED floating
`mermaid@11` from unpkg at read time. Online it looked fine; offline, behind a
proxy, or the day unpkg ships a breaking `11.x`, the diagrams are gone — and no
gate in the repository would have said so. Aborting cross-origin requests is what
turned that latent supply-chain dependency into a red test.

**The fix — the site brings its own runtime.** `ed0caa86`, 48 lines of hook plus
9 lines of `mkdocs.yml`:

- `release/scripts/mkdocs_mermaid_hook.py` — `on_files` appends
  `File.generated(config, "javascripts/mermaid.min.js", abs_src_path=...)`
  pointing at `pkg/dashboard/frontend/node_modules/mermaid/dist/mermaid.min.js`,
  the copy the frontend lockfile already pins (11.15.0). MkDocs copies it into
  the site. Missing runtime raises `PluginError` naming `make docs-build`,
  because a docs build that silently reverts to the CDN is the failure this
  phase exists to prevent.
- `mkdocs.yml` — `extra_javascript: [{path: javascripts/mermaid.min.js, defer: true}]`.
  Deferred puts the global in place after parsing and before `DOMContentLoaded`,
  which is when Material mounts diagrams and looks for it; finding one, the
  unpkg branch is never taken.

No new dependency, no vendored blob, no version to keep in sync: the runtime the
docs ship and the runtime the dashboard bundles are the same pinned artifact.

*No `startOnLoad` guard is needed, and this was verified rather than assumed.*
mermaid registers `window.addEventListener("load", contentLoaded)`, and
`contentLoaded` requires BOTH `mermaid.startOnLoad` and `getConfig().startOnLoad`.
Material calls `initialize({startOnLoad: false, ...})` at `DOMContentLoaded`,
strictly before `load`. On diagram-free pages the runtime is inert.

**Alternatives rejected.**

| Considered | Why not |
|---|---|
| Material's `privacy` plugin | still resolves the floating `mermaid@11` at BUILD time — pinned per build, unpinned across builds, and no gate would notice the change |
| vendor `mermaid.min.js` into the tree | a 3.3 MB generated blob in git that drifts from the lockfile the moment either moves |
| pre-render with `mmdc` at build time | needs puppeteer for `mkdocs serve` and freezes the diagrams against Material's palette switching |
| pinned CDN URL + SRI | still a network fetch at read time; still fails offline |
| rename the fence class and hand-roll the mount | loses the theme's `themeCSS` and its palette integration — a redesign, not a fix |
| inject the runtime only on pages that have diagrams | instant navigation does NOT re-execute `{% block scripts %}`; landing first on a diagram-free page would silently restore the CDN fallback |

**Instant navigation is proved to be instant navigation.** Material only
intercepts a click whose URL appears in `sitemap.xml`, and the sitemap is written
from `site_url`. Served at `127.0.0.1` against the production `site_url` every
entry is a different origin, no click is intercepted, and the case would have
"passed" while measuring two full page loads. Hence `mkdocs.test.yml`: `INHERIT:
mkdocs.yml` plus exactly one changed key, `site_url: http://127.0.0.1:4322/`
(and `site_dir: site-test`, gitignored). Theme, overrides, extensions, hooks and
`--strict` are the production ones. The test then plants a witness on `window`,
clicks the sidebar link, and asserts the witness SURVIVED the navigation — a full
load would have wiped it.

**What each diagram must do, in the browser.** Material renders into a CLOSED
shadow root, so an `addInitScript` forces `attachShadow({mode: 'open'})` before
any page script runs; the assertions read the real rendered output rather than
the source. Per page: declared `<pre class="mermaid">` count in the served HTML
equals the count the spec declares (an undeclared new diagram fails the suite
rather than sliding by uncovered); `div.mermaid svg` reaches that count — the
auto-retrying expectation IS the readiness signal, no sleep; zero leftover
`pre.mermaid` / `code.language-mermaid`; zero `.error-icon`. Per diagram: visible,
bounding box with width and height > 0, non-empty text once `<style>` is
stripped, no `Syntax error`, and every expected label present.

**The commits.**

| Commit | Scope |
|---|---|
| `4decda3c` | this document: Phase 8B CLOSED at `93dca214`, Phase 9 ACTIVE, and the two counts 12.8 got wrong |
| `35f5993a` | RED — `mkdocs.test.yml`, `playwright.docs-site.config.ts`, `e2e-docs-site/mermaid.spec.ts`, `/site-test/` ignored |
| `ed0caa86` | GREEN — the runtime hook and its `mkdocs.yml` registration |
| `c0c8a474` | wiring — `Makefile`, `ci.mk`, `.github/workflows/docs-check.yml`, `docs/maintainers/testing.md` |
| `2124661b` | `vite.config.js`: vitest stops collecting a Playwright spec it cannot run |
| this section | the Phase 9 record |

*Two counts 12.8 got wrong (`4decda3c`).* The old `FactCount` did not count two
targets — it selected the first deployed revision and counted ONE, silently
erasing the second; and the 12.8 repair range is FOUR commits, not three,
`93dca214` being the fourth. Both are non-blocking corrections to the narrative,
not to any repair. `PACTO_PR_TARGET_STATE.md` is untouched.

*Wiring (`c0c8a474`).* The gate joins level 6 of the Phase 8B taxonomy — a real
shipped artifact over fixed data — as a SECOND suite at that level, not a new
architecture: same pinned Playwright, same Chromium install, separate `testDir`,
separate config, separate product. `make test-browser-docs-site` — named for the
site rather than the brief's `test-browser-docs`, because `e2e-docs` already
means the dashboard's bundle-documentation spec and the two would be misread. It
depends on `$(MERMAID_RUNTIME)`, a real file target that runs `npm ci` only when
`package-lock.json` is newer, and `docs` / `docs-build` / `docs-check` now depend
on it too, so no path can build the site without the runtime it ships. The
workflow step is appended to the existing required Docs check job — not Kind, not
dashboard E2E, not a new job — and its path filter now covers the overlay, the
spec, the Playwright config, the hook, `package.json`, `package-lock.json`,
`Makefile` and `ci.mk`. `docs/maintainers/testing.md` records the two products at
level 6 and the ownership boundary between them.

*`2124661b`.* `vite.config.js` excluded `e2e/**` and `e2e-live/**` by name, so
vitest's default `**/*.spec.ts` collected the new suite and `ci-ui` failed with
`Playwright Test did not expect test.beforeEach() to be called here`. Added
`e2e-docs-site/**` to the same explicit list — kept explicit, so the list stays
the inventory of browser suites.

**Mutation proof.** Both mutations were applied to the GREEN tree and reverted
before committing:

| Mutation | Result |
|---|---|
| remove the `extra_javascript` runtime entry (disable the renderer) | all three tests fail on the SVG-count assertion — `Expected 2/1/2, Received 0` |
| remove `- navigation.instant` from `theme.features` | exactly ONE test fails, the instant-navigation one, on the witness assertion (`Expected: true, Received: undefined`); the other two still pass |

The second is the one that matters: it proves the instant-navigation case
measures client-side navigation and not two direct loads.

**Verification.**

| Check | Result |
|---|---|
| `make test-browser-docs-site` at `93dca214` (RED) | 3 failed — `div.mermaid svg` 0 of 2 / 0 of 1 / 0 of 2; declared-count assertions passed |
| `make test-browser-docs-site` after the fix (GREEN) | 3 passed in 4.3s; `GET /javascripts/mermaid.min.js 200`; zero requests to unpkg |
| `npx playwright test -c playwright.docs-site.config.ts` | 3 passed — same result direct, without Make |
| mutation runs (renderer, instant navigation) | exactly the intended failures, above; both reverted |
| `make docs-check` | 9/9 including `(c) mkdocs build --strict`; `check_mermaid: 19/19 mermaid blocks valid` |
| `make test-browser` | 219 passed — the dashboard suite, unchanged in count and content |
| `make ci` | exit 0; total coverage 100.0% |
| `make check-section` | exit 0 — zero U+00A7 in authored files |
| `git diff --check` | exit 0 |
| tracked tree | clean; no screenshots, traces, `site-test/` or temporary servers committed. `make ci` regenerated `integrations/kubernetes/charts/pacto-dev-gateway/README.md` (inherited helm-docs drift, out of scope) — inspected and restored with `git checkout --`; the four agent files (`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`) remain untracked |
| GitHub CI at `2124661b` (run 31964688735) | success — `required` SUCCESS, all six `ci-e2e-kind` shards, `dashboard-e2e`, `artifact-drift`, `release-dry-run`, `ci-e2e-envtest`, `ci-integration-kubernetes`, `release-version-test`, `ci-static`, `ci-engine`, `ci-oci`, `ci-dashboard`, `ci-gates`, `operator-build` |
| Docs check at `2124661b` (run 31964688756) | success — including the new `Browser acceptance over the built site` step |
| Security at `2124661b` (run 31964688863) | success — `govulncheck (Go)`, `Trivy (image)`, `PR security summary` |
| Pacto Contract CI / Validate PR title / Repowise at `2124661b` | success (runs 31964688758, 31964688759, 31964688724) |
| CodeQL at `2124661b` (run 31964686066 / 31964686092) | every `Analyze` job success (actions, go, javascript-typescript, python). The `CodeQL` results check is `failure`, CARRIED and byte-identical to `93dca214`: the same 8 high `go/path-injection` alerts in `internal/app/resolve.go` (#40-43, opened 2026-07-29) and `pkg/oci/cache.go` (#59-62, opened 2026-08-13). Neither file is touched by Phase 9; no alert names any Phase 9 file; `required` does not include CodeQL |
| review threads | **WRONG AS FIRST RECORDED — see 13.1.** Reported here as "199 total, 0 unresolved"; the fully paginated result at `2124661b` and at `ffdde98e` is 199 total, 189 resolved, 10 unresolved (6 inherited generated, 4 inherited CodeQL). The query read only the first page, and that page happens to hold no unresolved thread |
| PR state | open, draft, mergeable |

The workflow results above belong to `2124661b`, the implementation head. This
section is committed after them, so its own commit necessarily triggers a further
round; those run IDs are reported in the handoff rather than in a further
self-referential commit.

**Out of scope and untouched by Phase 9:** the carried CodeQL path-injection
alerts; the `pacto-dev-gateway/README.md` helm-docs drift; Phase 10, 10B and
everything after; `PACTO_PR_TARGET_STATE.md`. No existing gate was weakened,
replaced or removed — `mermaid-check`, the dashboard browser suite, required CI,
Security, artifact drift and the release gates all run exactly as before. Phase 8
and Phase 8B remain ACCEPTED and CLOSED.

## 13.1 Phase 9 — narrow reopen and repair, CANDIDATE again

An independent review at `ffdde98e` ACCEPTED the Phase 9 suite and reopened one
blocker. Accepted and frozen, not redesigned here: the `e2e-docs-site` Playwright
suite and its core, integration-injected and instant-navigation coverage; the
one-key `site_url` test overlay; the window witness that proves same-document
navigation; the MkDocs hook staging the lockfile-solved Mermaid 11.15.0 runtime;
`extra_javascript` with deferred loading; reuse of the existing frontend
Playwright/Mermaid toolchain; the level-6 taxonomy and the separate
dashboard/docs-site suites; existing Mermaid syntax, dashboard browser, CI,
Security and release gates. No dependency was added, no CDN restored, no Mermaid
vendored, no second test framework created. Phase 8 and Phase 8B remain CLOSED.
Phase 10 is not started.

### The blocker: the gate carried the prerequisite, the real paths did not

The hook fails closed unless `pkg/dashboard/frontend/node_modules/mermaid/dist/mermaid.min.js`
exists. Phase 9 hung that prerequisite off `docs-check` and `test-browser-docs-site`
— the two targets the PR gate runs — and nowhere else. The dependency graph at
`ffdde98e`: `docs-serve` had no prerequisite, `docs-deploy` depended only on
`docs-generate`, and `.github/workflows/docs.yml` invoked `mkdocs build --strict`
directly. So the PR gate went green while the post-merge validation and the
versioned release publisher both aborted before building a page.

Reproduced on clean checkouts of `ffdde98e` with no frontend `node_modules`:

```
$ mkdocs build --strict --site-dir "$(mktemp -d)"          # the docs.yml command
ERROR   -  diagram runtime missing: pkg/dashboard/frontend/node_modules/mermaid/dist/mermaid.min.js
Aborted with a BuildError!                                  # exit 1

$ make docs-deploy                                          # the release.yml command
==> mike deploy --push --update-aliases 3.1.4 latest (release)
ERROR   -  diagram runtime missing: pkg/dashboard/frontend/node_modules/mermaid/dist/mermaid.min.js
error: Command '['mkdocs', 'build', '--clean', ...]' returned non-zero exit status 1.
make: *** [docs-deploy] Error 1
```

The release counterexample is the stronger one: it is not a hypothetical about a
runner, it is `make docs-deploy` failing inside mike's own build.

### The repair: one prerequisite, expressed once in Make

`$(MERMAID_RUNTIME)` is a real file target whose recipe is the frontend `npm ci`,
so "present means installed" and it costs nothing once node_modules exists. Every
repository-owned MkDocs/Mike entry point now depends on it:

| Entry point | Before | After |
|---|---|---|
| `make docs` | had it | unchanged |
| `make docs-build` | had it | unchanged |
| `make docs-check` | had it | unchanged |
| `make test-browser-docs-site` | had it | unchanged |
| `make docs-serve` | **none** | `docs-serve: $(MERMAID_RUNTIME)` |
| `make docs-deploy` | `docs-generate` only | `docs-deploy: docs-generate $(MERMAID_RUNTIME)` |
| `.github/workflows/docs.yml` strict build | raw `mkdocs build --strict` | `make docs-build-strict`, a new target carrying the prerequisite |
| `.github/workflows/release.yml` docs job | `make docs-deploy` | unchanged — it inherits the guarantee |

The release job needed no edit at all, which is the point: the prerequisite
travels with the target instead of being re-implemented as an `npm ci` pasted
into each job. `docs-build-strict` exists so docs.yml has a target to call rather
than a second copy of the dependency. docs.yml's path filter gains
`release/scripts/mkdocs_mermaid_hook.py`, both frontend dependency manifests,
`Makefile` and `ci.mk`, so a change to the hook, to the lockfile that pins the
runtime bytes or to the Make wiring that installs them can trigger the post-merge
validation that these inputs decide the outcome of. The hook's fail-closed
behaviour is untouched.

### Permanent regression proof

Added to the existing release-architecture level — `tests/release/docs_versioning_test.go`,
the file that already owns "who may publish the docs site" — with no new
framework and no shell harness. It interrogates the dependency GRAPH, because
Makefile text is not the invariant and a laptop cannot see the failure at all:
`node_modules` is already there, so every entry point passes locally regardless.

- `TestEveryDocsEntryPointInstallsTheDiagramRuntime` — `make -n -B <target>` for
  all seven entry points must schedule the frontend install. `-B` forces every
  prerequisite out of date, so a present `node_modules` cannot hide a missing
  edge; `-n` prints without executing.
- `TestTheDiagramRuntimeIsInstalledBeforeTheSiteIsBuilt` — for the two paths the
  PR gate never exercises, the install must be scheduled BEFORE the builder:
  `docs-serve` before `mkdocs serve`, `docs-deploy` before `mike deploy`.
- `TestDocsWorkflowsBuildTheSiteThroughMake` — neither docs.yml nor release.yml
  may run `mkdocs` or `mike` as a shell command (comments and step names are
  skipped); docs.yml must reach the site through `make docs-build-strict` and
  release.yml through the guarded `make docs-deploy`; and the docs.yml path
  filter must cover the runtime inputs.

RED at `ffdde98e` (the new test file copied onto the unrepaired tree), GREEN
after the repair:

| Assertion | `ffdde98e` | repaired |
|---|---|---|
| `docs-serve` installs the runtime | FAIL | PASS |
| `docs-deploy` installs the runtime | FAIL | PASS |
| `docs-build-strict` installs the runtime | FAIL (no such target) | PASS |
| install scheduled before `mkdocs serve` | FAIL — install at -1 | PASS |
| install scheduled before `mike deploy` | FAIL — install at -1, mike at 460 | PASS |
| docs.yml does not invoke the builder directly | FAIL | PASS |
| docs.yml reaches the site via `make docs-build-strict` | FAIL | PASS |
| docs.yml path filter covers the runtime inputs | FAIL, all 5 | PASS |
| `docs`, `docs-build`, `docs-check`, `test-browser-docs-site` | PASS | PASS |

### Exact-origin correction in the browser gate

Everything the docs-site suite proves rests on one barrier — abort every request
the site did not serve — and the barrier compared strings. `url.startsWith(origin)`
admits `http://127.0.0.1:43220` for an origin of `http://127.0.0.1:4322`, and
every longer port sharing that prefix with it. Now parsed origins compared
exactly, with unparseable and opaque URLs treated as off-site.

The focused assertion drives the real route handler with a look-alike port rather
than testing the predicate in isolation. Mutation-verified: restoring
`startsWith` makes the probe be admitted as the site and never recorded, and the
new test fails on both attempts (`4 passed` becomes `1 failed, 3 passed`). The
mutation was reverted. The three accepted tests are unchanged.

### The review-thread ledger was wrong, and so was the query

Section 13 recorded "199 total, 0 unresolved". That is false. The fully paginated
GraphQL result at `ffdde98e` — and at `2124661b`, unchanged — is:

| | count |
|---|---|
| total | 199 |
| resolved | 189 |
| unresolved | 10 |
| unresolved inherited `github-code-quality`, generated Mermaid dashboard chunk `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` | 6 |
| unresolved inherited CodeQL, `pkg/oci/cache.go` (lines 375, 394, 395, 666) | 4 |
| introduced by Phase 9 | 0 |

The cause is mechanical and worth stating, because it will recur: `reviewThreads`
returns 100 per page, this PR has 199, and the first page contains no unresolved
thread at all. A single-page query therefore reports a perfect ledger with total
confidence. Every count must come from a loop that follows `pageInfo.hasNextPage`
until it is false and sums the pages; `totalCount` on page one is not permission
to stop reading. Reported per page, this run: page 1 — 100 nodes, 100 resolved, 0
unresolved; page 2 — 99 nodes, 89 resolved, 10 unresolved.

The corrected numbers match what the earlier passes recorded at `6e3a3627`,
`b0020460` and `c9d52bb9`: the same 199/189/10 with the same 6 generated and 4
authored-file inherited threads. Nothing changed; only the Phase 9 report of it
was wrong.

### Local verification

| Check | Result |
|---|---|
| clean checkout of `ffdde98e`, `mkdocs build --strict` | exit 1 — `diagram runtime missing` |
| clean clone of `ffdde98e`, `make docs-deploy` (mike, no push) | fails inside mike's `mkdocs build` |
| clean checkout of the repair, `make docs-build-strict` | exit 0 — `npm ci` (340 packages) then `Documentation built in 3.45 seconds` |
| clean clone of the repair, `make docs-deploy` (mike, no push) | full path succeeds: docs-generate, `npm ci`, `mike deploy`, `mike set-default` |
| the published snapshot carries the pinned runtime | `gh-pages:3.1.4/javascripts/mermaid.min.js` sha256 `70137e77bb27…65de`, byte-identical to `node_modules/mermaid/dist/mermaid.min.js` |
| `make -n -B docs-deploy` | `npm ci` at line 7, `mike deploy` at line 11 — install precedes publish |
| `make -n -B docs-serve` | `npm ci` at line 1, `mkdocs serve` at line 3 |
| new regression test | RED against `ffdde98e` on every arm above, GREEN after the repair |
| `CI=1 make test-browser-docs-site` | 4 passed in 4.8s — the three accepted tests plus the exact-origin assertion |
| mutation run (prefix comparison restored) | `1 failed, 3 passed`; the failure is the intended one; reverted |
| `make docs-check` | 9/9 including `(c) mkdocs build --strict`; `check_mermaid: 19/19 mermaid blocks valid` |
| `go test ./tests/release/...` | ok |
| `make artifact-drift` | OK — one-publisher gate plus apply-release-plan idempotency |
| `git diff --check` | exit 0 |

Deployment was validated with `--push` stripped by a local shim, in a throwaway
clone with its remote removed. Nothing was published to `gh-pages`.

**Phase 9 state: CANDIDATE, pending another independent review.** It is not
closed here; a phase is closed by review, not by the author of its repair. The
reopen was narrow: the accepted suite and its design are untouched, the hook
still fails closed and no gate was weakened, replaced or removed.

## 14. Phase 10 record — CANDIDATE at `de5432ba`

Phase 10 closes the local Kind acceptance path on a Docker Desktop workstation,
where the containerd image store behaves differently from CI's classic Docker
image store. It repairs the SHARED Phase-8B boundary every scenario already went
through. No second Kind suite exists, the six scenarios keep their own cluster
lifecycles and claims, `imagePullPolicy: Never` is unchanged, and nothing about
Phase 8, 8B or 9 is reopened. Phase 10B is not started.

Commits appended on top of the reviewed HEAD `5fce48e6`, oldest first:

- `79e2c3e5` — close Phase 9 at `5fce48e6`, open Phase 10 (this document only)
- `984bede3` — `tests/acceptance/kind/kindload`, the loader and its unit tests
- `4d88f452` — `load_images` delegates to it; architecture gates that keep every
  scenario on the one boundary
- `8d8fe368` — the stale `ci.mk` claim, the durable maintainer documentation, the
  now-inaccurate comment in `upgrade-v4-v5.sh`
- `de5432ba` — the loader's diagnostic ends as an error string (staticcheck
  ST1005), wording otherwise unchanged

### The failure, reproduced before any harness code was changed

| | |
|---|---|
| Docker Desktop / Engine | 29.6.2 |
| image store | containerd — `Driver: overlayfs`, `DriverStatus [["driver-type","io.containerd.snapshotter.v1"]]` |
| kind | v0.31.0 go1.25.5 darwin/arm64 |
| host architecture | arm64 |
| node architecture | `uname -m` inside the node: `aarch64`, so `linux/arm64` |
| failing command | `kind load docker-image registry:2 --name pacto-repro`, from `install_registry` |

```text
ctr: content digest sha256:46faa9a1ae6813194b53921a370f2f4f8c5e1aae228a89bceafef5847a6a3278: not found
```

`kind load docker-image` is `docker save` piped into
`ctr --namespace=k8s.io images import --all-platforms --digests --snapshotter=overlayfs -`
inside the node. The digest it cannot find is not a layer and not the image: it
is the linux/amd64 MANIFEST advertised by `registry:2`'s index.

The export census makes it exact. A full `docker save registry:2` on this host
carries 13 blobs; its `index.json` advertises the pulled tag's whole index — six
platform manifests plus six attestation manifests — while the amd64 manifest blob
`sha256:46faa9a1…` is simply absent, because this host never pulled that
platform. `--all-platforms` then demands what the archive promised.

`docker save --platform linux/arm64 registry:2` carries 12 blobs and advertises
only `sha256:fa647fc1…` (linux/arm64/v8) and its attestation manifest. It is
self-contained.

Failing scenarios and their boundary: `evidence.sh` and `operational-graph.sh`
through `install_registry` (the `registry:2` pull is multi-platform);
`dashboard-modes.sh`, `reconcile.sh`, `observation.sh` and `upgrade-v4-v5.sh`
through `load_images` with locally built single-platform images, which failed
less often but through the same call.

### Four identities, and the one the node actually reports

| identity | value for `registry:2` on this host | who reports it |
|---|---|---|
| index (manifest list) digest | `sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373` | `docker image inspect --format '{{.Id}}'` |
| platform manifest digest | `sha256:fa647fc1…a793` (arm64/v8), `sha256:46faa9a1…3278` (amd64) | the index's own descriptors |
| config digest | `sha256:33eeff39e0aaabe61ca826fd7502396183462451be0783133e1a8fa944fc7350` | `crictl images --output json` as `id` |
| Docker image ID | under the containerd store this IS the index digest above | `docker images` |

That table is the root cause. Under the containerd store the host keeps the
tag's multi-platform INDEX identity while materializing only its own platform.
kind decides whether the node already has an image by comparing the host's ID
with what the node reports; an index digest can never equal a config digest, so
the check never matches, kind always re-imports, and `--all-platforms` always
asks for platforms this host never fetched. Retagging changes nothing: the
underlying content and the advertised index are the same. The failure is
structural, not a stale cache.

### What the harness does now

`load_images` is still the single call every scenario makes. It delegates to
`go run ./tests/acceptance/kind/kindload`, which performs, per reference:

1. read the node's own architecture (`uname -m` in the node, mapped to
   `linux/amd64` or `linux/arm64`) — behaviour, not an OS label;
2. `docker save`, narrowed with `--platform` when `docker save --help` advertises
   the flag, plain otherwise, so an older CLI still works;
3. verify the exported archive is SELF-CONTAINED — walk `index.json` recursively
   and require every referenced manifest, config and layer blob to be present;
   a missing blob fails closed, naming the digest and its platform;
4. select the node's platform manifest (attestation manifests skipped) and read
   its config digest;
5. `kind load image-archive`, then read `crictl images --output json` on EVERY
   node and require the reference to be present with exactly that config digest.

Step 5 is what makes `imagePullPolicy: Never` a real guarantee instead of an
assumption: the kubelet's own view must report the image the harness intended,
under the intended name, by config digest. A different public image under the
same tag would be a different config digest and the scenario would never start.
`kind load image-archive` is used because it has no identity short-circuit — it
imports what the archive contains, so a narrowed archive is imported honestly.

Every image goes through this, including the ones `install_registry` needs, so
`registry:2` cannot later be re-pulled under a spelling that discards the
platform-safe artifact: `install_registry` still runs `docker pull registry:2`
and then `load_images registry:2`, and the narrowing happens at LOAD time, on
whatever the host has. There is no prepared artifact to overwrite.

Shell stays thin: `load_images` is now three lines. Archive parsing, platform
selection, self-containment and node verification are Go, unit-tested without a
cluster.

### Rejected alternatives

| alternative | why not |
|---|---|
| pre-flatten `registry:2` by hand (`docker pull --platform` + retag) | not durable — `install_registry` re-pulls, and it is a workstation-only manual setting |
| ask `docker image inspect --format '{{json .Manifests}}'` which platforms are local | returns `null` on this store; it cannot decide anything |
| try `kind load docker-image` and fall back on failure | failure as control flow: double work on every image, and it hides real errors behind a retry |
| parse the legacy `manifest.json` as a fallback | unnecessary — modern `docker save` always writes `index.json`; an archive without one now fails closed with a diagnostic |
| skip the affected scenarios when Docker Desktop is detected | forbidden and pointless: the scenarios must run |
| a general container-runtime abstraction | one consumer, no second one in sight |

### Permanent regression proof

Unit level, `tests/acceptance/kind/kindload/main_test.go` — a synthetic OCI tar
builder producing real sha256 digests, so the tests exercise the same parsing the
runtime does:

- `TestPartialMultiPlatformArchiveIsRejected` — the reproduced failure as a test:
  an index advertising amd64 and arm64 with the amd64 manifest blob omitted. The
  diagnostic must name `not self-contained`, the missing digest and
  `(linux/amd64)`.
- `TestMissingLayerContentIsRejected`, `TestArchiveWithoutIndexIsRejected`,
  `TestAmbiguousArchiveFailsClosed`, `TestArchiveWithNoNodePlatformFailsClosed` —
  the other fail-closed arms.
- `TestNarrowedMultiPlatformArchiveSelectsTheNodePlatform`,
  `TestCompleteMultiPlatformArchiveSelectsTheNodePlatform`,
  `TestSinglePlatformArchiveLoads` — selection, including a normal
  single-platform image, which is what CI's classic store produces.
- `TestSaveArgsNarrowOnlyWhenTheCLICanSupportIt` — narrowing is a capability read
  off `docker save --help`, not a version check.
- `TestVerifyNodeAcceptsTheLoadedImage`,
  `TestVerifyNodeRejectsADifferentImageUnderTheSameName`,
  `TestVerifyNodeRejectsAnAbsentImage`, `TestVerifyNodeRejectsUnreadableOutput` —
  the in-node identity gate, including the silent-substitution case.

Architecture level, `tests/architecture/kind_image_loading_test.go`, run by
`ci-gates` and `make test-integration`:

- `TestScenariosLoadImagesThroughTheSharedBoundary` — no scenario may call
  `kind load` itself; at least six scenarios must be found.
- `TestLoadImagesDelegatesToTheKindloadHelper` — `lib.sh` must reach the helper
  and must not contain `kind load docker-image`.
- `TestInstallRegistryLoadsThroughTheSharedBoundary` — `install_registry` must
  load through `load_images registry:2` with `imagePullPolicy: Never`, and must
  not tag, save or import around it.

RED against `5fce48e6`: `git checkout 5fce48e6 -- tests/acceptance/kind/lib.sh`
makes `TestLoadImagesDelegatesToTheKindloadHelper` fail with "load_images must
delegate to the kindload helper"; restoring the file makes it pass.

Mutation-checked, one at a time, each reverted and the file compared byte for
byte afterwards:

| mutation | killed by |
|---|---|
| `verifyComplete` always returns nil | `TestPartialMultiPlatformArchiveIsRejected`, `TestMissingLayerContentIsRejected` |
| `saveArgs` never passes `--platform` | `TestSaveArgsNarrowOnlyWhenTheCLICanSupportIt` |
| `verifyNode` always returns nil | `TestVerifyNodeRejectsADifferentImageUnderTheSameName`, `TestVerifyNodeRejectsAnAbsentImage` |
| `selectImage` ignores the node platform | `TestCompleteMultiPlatformArchiveSelectsTheNodePlatform`, `TestArchiveWithNoNodePlatformFailsClosed` |

### Local verification

| Check | Result |
|---|---|
| `kind load docker-image registry:2` on a fresh cluster at `5fce48e6` | `ctr: content digest sha256:46faa9a1…: not found` |
| `go test ./tests/acceptance/kind/kindload/...` | ok |
| `go test ./tests/architecture/...` | ok |
| `make test-integration` | ok — includes the kindload package and the acceptance Go layer |
| `make ci` | exit 0 |
| `make artifact-drift` | OK — one-publisher gate plus apply-release-plan idempotency |
| `make release-dry-run` | `RELEASE-DRY-RUN OK` and `K8S-MODULE-STANDALONE OK` |
| `make test-browser` | 219 passed |
| `make test-acceptance-kind` — all six scenarios, ONE invocation | exit 0 |
| `git diff --check` | exit 0 |
| tracked tree | clean; only `.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md` untracked |

The complete suite, in order, on a workstation with no pre-seeded images and with
every scenario cluster deleted first: `DASHBOARD-MODES E2E PASS`,
`V4-TO-V5-UPGRADE PASS`, `KIND E2E PASS` (reconcile),
`full in-cluster Evidence Server lifecycle acceptance PASSED`, the
operational-graph vertical including `PASS: the live Product API proves the
fixture, twice, across a refresh` (14 facts, two distinct snapshots) and
`8 passed` for live journeys A–H, then
`operator-managed observation packaging verified`.

In-node identity evidence, printed by the loader before any workload starts —
name, the platform actually imported, the node count that reported it, and the
config digest the kubelet sees:

```text
kindload: localhost:5001/pacto-dashboard:e2e-modes (linux/arm64) present on 1 node(s) as sha256:3f031b72…a29ea
kindload: localhost:5001/pacto-operator/pacto-controller:e2e-modes (linux/arm64) present on 1 node(s) as sha256:be76e525…eaf582
kindload: pacto/operator:5.0.0-e2e (linux/arm64) present on 1 node(s) as sha256:4e506d59…92db92
kindload: localhost:5001/pacto-dashboard:3.1.4 (linux/arm64) present on 1 node(s) as sha256:3f031b72…a29ea
kindload: localhost:5001/pacto-operator/pacto-controller:5.1.2 (linux/arm64) present on 1 node(s) as sha256:f4858e6b…194b7c
kindload: registry:2 (linux/arm64) present on 1 node(s) as sha256:33eeff39…fc7350
```

Three environment flakes were seen in earlier full-suite attempts, all in cluster
PROVISIONING and none at the image boundary: `kind create cluster` failing to
bind a randomly chosen API-server port already taken by an ephemeral connection;
the Docker daemon socket disappearing mid-run; and one `kind get clusters` that
did not list a running cluster, so `ensure_cluster` tried to create it and hit
"node(s) already exist". Each disappeared on re-run and the recorded complete run
is clean. They are reported rather than papered over with a sleep or a retry:
none of them is a claim this phase owns.

### GitHub state at the implementation head `8d8fe368`

Only CodeQL ran: `31973874581` (go, javascript-typescript, python — all success)
and `31973874168` (actions — success). The `CI`, `Docs check`, `Security`,
`Pacto Contract CI`, `Repowise` and `Validate PR title` workflows did NOT run,
because they are `pull_request`-triggered and the PR is currently
`mergeable: CONFLICTING`, `mergeStateStatus: DIRTY` — GitHub produces no merge
ref, so no `pull_request` run is created. The conflict is inherited and confined
to dependency metadata: `origin/main` advanced past the merge-base `a56b69e3`
with three Dependabot bumps (`go-containerregistry` 0.21.7 to 0.21.9,
`ginkgo/v2` 2.32.0 to 2.32.1, `otel/exporters/prometheus` 0.66.0 to 0.67.0) that
collide with this branch's `go.mod`/`go.sum`. Nothing in Phase 10 touches those
files. Resolving it needs a merge from `main` into the branch — the same
append-only merge this branch has taken three times before — which is a
maintenance decision outside Phase 10's mandate and is left to the reviewer.

The CodeQL check reports 8 high alerts, all `go/path-injection`, all in files
this phase does not touch: `pkg/oci/cache.go` (375, 394, 395, 666) and
`internal/app/resolve.go` (35, 43, 57, 67). Inherited, unchanged in count and
location.

Review threads, fully paginated until `hasNextPage` is false: page 1 — 100 nodes,
100 resolved, 0 unresolved; page 2 — 99 nodes, 89 resolved, 10 unresolved. Total
199 / 189 resolved / 10 unresolved: 6 inherited `github-code-quality` on the
generated `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and 4
inherited CodeQL on `pkg/oci/cache.go`. Phase 10 introduced none, resolved none
and published no comment.

**Phase 10 state: CANDIDATE, pending independent review.** The scenarios, their
claims, the Product gate, the live browser journeys and the external Evidence
Server vertical are untouched; the only behavioural change is how an image
reaches a kind node.

## 14.1 Phase 10 main synchronization — CANDIDATE at `b2157a09`

Section 14 records the Phase-10 implementation and closes with the reason it
could not be verified on GitHub: the PR was `mergeable: CONFLICTING`,
`mergeStateStatus: DIRTY`, so GitHub produced no merge ref and every
`pull_request`-triggered workflow — `CI` and its six Kind shards, `required`,
`Docs check`, `Security`, `Pacto Contract CI`, `Repowise`, `Validate PR title` —
simply never ran. This section records the append-only merge that removed that
blocker and the evidence produced on the merged SHA. It supersedes only the
"GitHub state at the implementation head `8d8fe368`" subsection of section 14.
Nothing else in section 14 changes.

### The implementation was accepted before this merge

The Phase-10 implementation at `6c8ba9fe` passed independent code review. The
shared `kindload` boundary was verified independently on BOTH image stores —
Docker Desktop with the containerd store, and Docker 28 with the classic
overlay2 store — and on each a disposable Kind cluster loaded `registry:2` with
`crictl` reporting the expected config digest
`sha256:33eeff39e0aaabe61ca826fd7502396183462451be0783133e1a8fa944fc7350`. The
focused race tests, the architecture and release gates and `go vet` passed. No
Phase-10 implementation blocker was found.

So the work recorded in section 14 was NOT reopened here. The only thing missing
was PR-workflow evidence, and the only thing standing between Phase 10 and that
evidence was a dependency-metadata conflict inherited from `main`.

### Main synchronization

`origin/main` was re-read at the start of this work and had NOT advanced: it is
still `83f2e66d5cd4fab56099991d39e64fc11f107b3d`, the same commit last observed.
No additional main-line commits had to be disclosed or absorbed. The branch also
still started exactly at `6c8ba9fe`, matching `origin/feat/operational-graph-fleet`.

The merge-base was `a56b69e375f1881d645d3b39f3366f23398e72cf`, and `main` carried
exactly three Dependabot commits past it:

- `319661c7` — `github.com/google/go-containerregistry` 0.21.7 to 0.21.9
- `cc0c49bc` — `go.opentelemetry.io/otel/exporters/prometheus` 0.66.0 to 0.67.0
- `83f2e66d` — `github.com/onsi/ginkgo/v2` 2.32.0 to 2.32.1

Merge commit: `b2157a09aee33be337b4674c603e10fcb8e82d60`, a normal `--no-ff`
merge with parents `6c8ba9fe` and `83f2e66d`. No rebase, no amend, no reset, no
force-push, no history rewrite. It is the branch's fourth such merge.

### Conflict resolutions

Git reported conflicts in the root `go.mod` and `go.sum` only. The Kubernetes
module's two files auto-merged. Each was resolved semantically, never as a union
of both sides:

| file | conflict | resolution |
|---|---|---|
| `go.mod` | one hunk: `klauspost/compress` 1.19.0 against 1.19.1, with the branch's `kylelemons/godebug` indirect adjacent | main's 1.19.1 taken, `kylelemons/godebug` kept |
| `go.sum` | four interleaved hunks | conflict markers discarded and the file REGENERATED with `go mod tidy`, so the sums follow the real module graph rather than a hand-picked union |
| `integrations/kubernetes/go.mod` | none — auto-merged | main's `go-containerregistry` 0.21.9, `ginkgo/v2` 2.32.1 and `otel/exporters/prometheus` 0.67.0 land on top of the branch's `go 1.26.6` |
| `integrations/kubernetes/go.sum` | none — auto-merged | byte-identical to `origin/main`, because the branch never touched this file |
| `go.work.sum` | none | three graph-only `go.mod` hashes the new closure needs for a readonly workspace build |

The coordinated Go state is preserved and consistent in all three places that
declare it: `go.work`, `go.mod` and `integrations/kubernetes/go.mod` all say
`go 1.26.6`. There is no `toolchain` directive anywhere, so nothing else had to
be reconciled. The branch's own accepted dependency work — `gocloud.dev` v0.46.0
and its closure for the Evidence Server, and `opencontainers/go-digest` promoted
to a direct requirement — is intact.

`go mod tidy` was deliberately NOT run in the Kubernetes module. That module
depends on `github.com/trianalab/pacto/v3 v3.1.4`, which the workspace redirects
to the working tree with `replace ... => .`; a plain tidy ignores the workspace,
resolves the published v3.1.4 and cannot see the packages this branch adds. Its
two files needed no repair anyway, and the correctness of leaving them alone is
proven independently below by `verify-k8s-standalone`, which builds that module
with `GOWORK=off` and no replace.

### The dependency diff is exactly the main-line upgrades

Against the branch tip `6c8ba9fe` the merge changes five files and no source
file at all:

```text
 go.mod                         |  6 +++---
 go.sum                         | 20 ++++++++++----------
 go.work.sum                    |  3 +++
 integrations/kubernetes/go.mod | 18 +++++++++---------
 integrations/kubernetes/go.sum | 36 ++++++++++++++++++------------------
```

Every version move is a main-line upgrade or its transitive consequence:
`go-containerregistry` 0.21.7 to 0.21.9, `docker/cli` 29.5.3 to 29.6.2,
`klauspost/compress` 1.19.0 to 1.19.1, `golang.org/x/mod` 0.37.0 to 0.38.0,
`golang.org/x/tools` 0.47.0 to 0.48.0, plus `ginkgo/v2` 2.32.1,
`otel/exporters/prometheus` 0.67.0, `prometheus/client_golang` 1.24.1 and
`prometheus/procfs` 0.21.1 in the Kubernetes module. No unrelated upgrade was
introduced and no generated file was regenerated.

Two incidental generated changes appeared during local verification and were
REVERTED rather than committed:

- `integrations/kubernetes/charts/pacto-dev-gateway/README.md` — the known
  helm-docs drift. The merge does not touch that chart's source, so the drift is
  not legitimate output of this change.
- nineteen further `go.work.sum` graph hashes written once by a tool-install and
  plugin-build step inside `make ci`. Re-running the root and module builds,
  vets and test compiles against the committed `go.work.sum` produces zero
  drift, so those entries are not required by the merged closure.

### Local verification at `b2157a09`

| Check | Result |
|---|---|
| `go test -race -count=1 ./tests/acceptance/kind/kindload/...` | ok |
| `go test -count=1 ./tests/architecture/... ./tests/release/...` | ok, both |
| `go vet ./...` | exit 0 |
| `make test-integration` | ok — the integration suite plus the whole `tests/acceptance` Go layer |
| `make ci` | exit 0, total coverage 100.0% |
| `make artifact-drift` | `artifact-drift: OK` |
| `make release-dry-run` | `RELEASE-DRY-RUN OK` and `K8S-MODULE-STANDALONE OK` |
| `make test-browser` | 219 passed |
| `make test-acceptance-kind` — all six scenarios, ONE invocation | exit 0 |
| `git diff --check` | exit 0 |
| tracked tree | clean; only `.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md` untracked |

`K8S-MODULE-STANDALONE OK` is the load-bearing line for the conflict resolution:
it is a throwaway module that `go get`s the Kubernetes integration with
`GOWORK=off` and no replace, so it proves the merged
`integrations/kubernetes/go.mod` and `go.sum` are complete and consistent on
their own, without the workspace.

The workstation is the Phase-10 hard case, unchanged: Docker Engine 29.6.2 with
the containerd image store (`overlayfs`,
`driver-type io.containerd.snapshotter.v1`), kind v0.31.0, arm64 host. The six
scenarios reported, in order: `DASHBOARD-MODES E2E PASS`, `V4-TO-V5-UPGRADE
PASS`, `KIND E2E PASS`, `full in-cluster Evidence Server lifecycle acceptance
PASSED`, the operational-graph vertical including `PASS: the live Product API
proves the fixture, twice, across a refresh` and `8 passed` for the live browser
journeys A to H, then `operator-managed observation packaging verified`. No
environment flake was seen in this run.

The in-node identity evidence, printed by the loader before any workload starts:

```text
kindload: localhost:5001/pacto-dashboard:e2e-modes (linux/arm64) present on 1 node(s) as sha256:3904042d4de59597ecb973069a98b92a52d490fb21d4bb32a32ffa7d5153b85c
kindload: localhost:5001/pacto-operator/pacto-controller:e2e-modes (linux/arm64) present on 1 node(s) as sha256:d45351154e0dd748a27f6f61738abefc486e5cc48e0b456e74e771cc1f1b6eb7
kindload: pacto/operator:5.0.0-e2e (linux/arm64) present on 1 node(s) as sha256:f304bdaf3c435e4ade12178a0d03833bbce6502a490cd297afa73e6063eba53f
kindload: localhost:5001/pacto-dashboard:3.1.4 (linux/arm64) present on 1 node(s) as sha256:3904042d4de59597ecb973069a98b92a52d490fb21d4bb32a32ffa7d5153b85c
kindload: localhost:5001/pacto-operator/pacto-controller:5.1.2 (linux/arm64) present on 1 node(s) as sha256:9596e19c3790a0c82050df3ce12edf9941f5972ac2bb92e915a9206c04bcbcd1
kindload: registry:2 (linux/arm64) present on 1 node(s) as sha256:33eeff39e0aaabe61ca826fd7502396183462451be0783133e1a8fa944fc7350
```

`registry:2` reports config digest `sha256:33eeff39...fc7350`, the same value the
independent review measured on both image stores. The image identity the node
actually sees is reproducible across hosts and stores, which is the whole claim
of Phase 10.

### GitHub state at `b2157a09`

The merge did what it was for. The PR is `mergeable: MERGEABLE` again;
`mergeStateStatus` is `BLOCKED`, which is the expected state for a DRAFT PR, not
a failing one. Every previously blocked `pull_request` workflow ran.

`CI` — run `32007273093`, conclusion **success**. All twenty jobs:

| job | conclusion |
|---|---|
| `changes` | success |
| `ci-static` | success |
| `ci-gates` | success |
| `ci-engine` | success |
| `ci-dashboard` | success |
| `dashboard-e2e` | success |
| `ci-integration-kubernetes` | success |
| `ci-e2e-envtest` | success |
| `ci-oci` | success |
| `operator-build` | success |
| `ci-e2e-kind (reconcile)` | success |
| `ci-e2e-kind (dashboard)` | success |
| `ci-e2e-kind (upgrade)` | success |
| `ci-e2e-kind (evidence)` | success |
| `ci-e2e-kind (operational-graph)` | success |
| `ci-e2e-kind (observation)` | success |
| `release-dry-run` | success |
| `release-version-test` | success |
| `artifact-drift` | success |
| `required` | success |

All six `ci-e2e-kind` shards are green on CI's classic Docker image store, which
together with the local containerd-store run is the two-store evidence Phase 10
needs.

The other workflow runs on the same SHA:

| workflow | run | jobs | conclusion |
|---|---|---|---|
| `Validate PR title` | `32007273059` | `validate` | success |
| `Pacto Contract CI` | `32007273122` | `bundle` | success |
| `Repowise (architecture health)` | `32007273132` | `repowise` | success |
| `Docs check` | `32007273163` | `docs-check` | success |
| `Security` | `32007273107` | `Trivy (image)`, `govulncheck (Go)`, `PR security summary` | success |
| `PR #291` (CodeQL default setup) | `32007270766` | `Analyze (actions)`, `Analyze (python)`, `Analyze (javascript-typescript)`, `Analyze (go)` | success |
| `Code Quality: PR #291` | `32007270518` | `Analyze (python)`, `Analyze (go)`, `Analyze (javascript-typescript)` | success |
| `Rebuild dashboard UI` | `32007273210` | — | skipped |
| `Auto-merge Dependabot PRs` | `32007273128` | — | skipped |

Across the whole commit, thirty-five check runs report success, two are skipped
and exactly one reports failure: the `github-advanced-security` `CodeQL` check
summary, check run `95319363719`.

### CodeQL: the check summary is not the alert inventory

The `CodeQL` check summary reads "8 new alerts including 8 high severity
security vulnerabilities". That title is wrong about "new" in two independent
ways, and it is not the inventory.

The code-scanning API, queried on `refs/pull/291/head` after the push, returns
NINE open alerts — the eight Go ones the check annotates plus one Python alert
the check does not surface at all:

| alert | severity | rule | location | first created |
|---|---|---|---|---|
| 40 | high | `go/path-injection` | `internal/app/resolve.go:35` | 2026-07-29 |
| 41 | high | `go/path-injection` | `internal/app/resolve.go:43` | 2026-07-29 |
| 42 | high | `go/path-injection` | `internal/app/resolve.go:57` | 2026-07-29 |
| 43 | high | `go/path-injection` | `internal/app/resolve.go:67` | 2026-07-29 |
| 59 | high | `go/path-injection` | `pkg/oci/cache.go:375` | 2026-08-13 |
| 60 | high | `go/path-injection` | `pkg/oci/cache.go:394` | 2026-08-13 |
| 61 | high | `go/path-injection` | `pkg/oci/cache.go:395` | 2026-08-13 |
| 62 | high | `go/path-injection` | `pkg/oci/cache.go:666` | 2026-08-13 |
| 38 | high | `py/incomplete-url-substring-sanitization` | `release/scripts/docs_check.py:197` | 2026-07-27 |

Delta introduced by this merge: **zero**. The same query run before the merge
returned the same nine alerts, the same alert numbers, the same rules and the
same paths and lines. Every one predates this work — the newest was created four
days before it. They are the inherited set section 14 already recorded, plus the
Python alert that section undercounted by reading the check summary instead of
the API.

The merge could not have introduced any of them: it changes five
dependency-metadata files and not one line of Go or Python. The check's own body
concedes the point — "Alerts not introduced by this pull request might have been
detected because the code changes were too large."

### Review threads at `b2157a09`

Fully paginated until `hasNextPage` is false: two pages, 199 nodes, matching the
reported `totalCount` of 199.

| dimension | count |
|---|---|
| total | 199 |
| resolved | 189 |
| unresolved | 10 |
| outdated | 187 |
| current (not outdated) | 12 |
| authored by a human | 0 |
| generated by `github-code-quality` | 182 |
| generated by `github-advanced-security` | 17 |

The ten unresolved threads are unchanged and all generated: six from
`github-code-quality` on the minified generated bundle
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, and four from
`github-advanced-security` on `pkg/oci/cache.go`, which are the same inherited
`go/path-injection` alerts 59 to 62 in review-thread form. This work introduced
no thread, resolved no thread, published no comment and changed no PR metadata.

**Phase 10 state: CANDIDATE, pending independent review.** The implementation was
independently accepted at `6c8ba9fe`; this section adds the main synchronization
and the PR-workflow evidence that the conflict had made impossible. Phase 10B is
not started and `PACTO_PR_TARGET_STATE.md` is untouched. A phase is closed by
review, not by its author.

### Phase 10 verdict — ACCEPTED and CLOSED at `f2a181b1`

The independent review of the candidate recorded above ACCEPTED it and CLOSED
Phase 10 at `f2a181b1`, the documentation commit on top of the merge. Nothing in
sections 14 and 14.1 is reopened by that verdict; they become the frozen record.
Reopening Phase 10 now requires a concrete correctness, security or data-loss
counterexample.

Two items were carried OUT of Phase 10 rather than fixed inside it:

- the nine open CodeQL alerts, all inherited, none introduced by Phase 10. They
  are not Phase-10B work either: Phase 10B re-queries them at its own final SHA
  and reports the delta it introduced, which is expected to be zero.
- a non-blocking warning about `release/orchestrator/verify-k8s-standalone.sh`:
  it builds an external consumer importing only `api/v1alpha1` and does not
  compile the complete Kubernetes module, despite its description implying it
  does. The complete standalone operator build IS performed, by
  `release/scripts/verify-standalone.sh` inside `release-dry-run`, so nothing is
  unproven — only the script's description overstates its own reach. Carried to
  Phase 14 unless a later phase has to touch that gate for its own reasons. It
  must not be repaired as an unrelated tidy-up.

## 15. Phase 10B record — OPENED

Phase 10 is CLOSED (section 14.1 above). This section opens Phase 10B and is
appended by a documentation-only commit, before any implementation commit.
`PACTO_PR_TARGET_STATE.md` is untouched; TARGET section 1B and TARGET
"Phase 10B — canonical demo model and clone-free OCI-distributed Compose demo"
bind this work unchanged.

### The commission

ONE canonical declarative demo model, projected into (1) the existing
Helm/Kubernetes surface and (2) a Docker Compose surface distributed as an
immutable OCI artifact that runs WITHOUT cloning this repository. The user
journey to satisfy end to end: obtain a pinned artifact with standard OCI
tooling; materialize its Compose manifest, fixture data and configuration; start
it with Docker Compose; wait on observed readiness; open the dashboard and
exercise the documented journeys; stop, clean up and upgrade to another pinned
version using documented commands.

### Inspection before choosing an architecture

The existing implementation was inspected first, and the findings below are what
the architecture is chosen against.

- **The canonical model already exists and already sanctions this direction.**
  `tests/acceptance/scenario` holds the Phase-8B value `OperationalGraph` and its
  projections `Materialize`, `TraceExport`, `Plan`, `PactoCRs`,
  `EvidencePayloads` and `FactCount`. The package doc states the rule directly:
  a Helm or Docker Compose projection "would be a sibling of Materialize over the
  same value". `docs/maintainers/testing.md` rule 2 says the same: a projection
  exists only when it has a consumer.
- **There is no Compose asset anywhere in the repository.** The Compose surface
  is greenfield; nothing is being migrated, and no existing demo is duplicated.
- **A cluster-free acceptance precedent already exists.**
  `tests/acceptance/local/fleet-graph.sh` proves the registry, evidence,
  observation and reconciliation vertical with NO cluster, including durable
  replay rejection across an evidence-server restart. The Compose surface needs
  no capability that this precedent has not already shown to work k8s-free.
- **Exactly ONE capability is platform-imposed.** Operational TARGETS require a
  controller that reconciles workloads and publishes deployed revisions. Compose
  has no controller, so it has no operational target. Everything else the
  fixture declares — registry and cache sources, revisions, an observation
  source, an evidence source, and the declared/observed/reconciled relationship
  — is reachable without Kubernetes.
- **Digests can be computed without a push.** `oci.BundleImage` produces the same
  deterministic digest a real push assigns, which is what the shipped evidence
  payloads need, since evidence ingestion requires an immutable digest reference.
- **The release machinery has exactly one sanctioned OCI publishing adapter.**
  `release/orchestrator/publish-oci-unit.sh`, used today by
  `dashboard-contract-bundle`, with `verify-oci.sh` deciding
  `absent|identical|adopt|conflict` and `ledger.sh` recording the result. The
  one-publisher gate in `tests/release` requires exactly one
  `pacto-publishes:` marker per manifest unit.

### Selected architecture

A sibling projection over the existing canonical value. No framework, no generic
runtime abstraction, no new dependency, no second fixture.

1. **Surface capability, declared as data.** The scenario gains an explicit
   surface/capability notion so the ONE platform-imposed difference is declared
   in the model, never hidden in imperative code. `FactCount` becomes
   surface-aware, and the existing live Product gate
   `tests/acceptance/kind/productready` gains a surface flag so it REPORTS the
   capability a surface does not have instead of silently skipping a check. The
   same gate binary proves both surfaces; there is no second gate.
2. **Helm projection.** The Kind harness today builds the observation
   `--set` strings inline. Those move into a projection on the scenario, with two
   consumers — the harness and the parity test — which is what justifies the
   projection existing at all. This is a change genuinely required to project the
   shared canonical model, not a Helm change made to ease Compose.
3. **Compose projection.** A sibling function over the same value emitting the
   Compose manifest, the environment defaults and the fixture data, plus a
   subcommand on the existing `tests/acceptance/scenario/project` tool. The
   stack: a registry, a one-shot local keygen, a one-shot publisher that pushes
   the plan's push records, an evidence server, a one-shot evidence seeder and
   the dashboard, wired with health checks and dependency conditions so
   readiness is observed and never slept on.
4. **Parity test.** An automated comparison that both projections express the
   same canonical scenario, and that the only divergence is the declared
   capability difference.
5. **Distribution.** A new release unit published through the SAME
   `publish-oci-unit.sh` adapter, so integrity, provenance, immutability, the
   ledger and the one-publisher policy are inherited rather than re-invented.
6. **Clone-free proof in CI.** A clean run directory containing only the pulled
   artifact and standard tooling. The execution path reads no fixture and no
   configuration from a repository checkout.

### Rejected alternatives

- **A standalone Compose fixture.** Rejected: it is a semantically independent
  second demo model, which the commission forbids and which is exactly the
  duplication Phase 8B removed.
- **Folding the Compose artifact into the existing `demo-bundles` unit.** Fewer
  files, but the release ledger keys by unit name, so a second
  `publish-oci-unit.sh` record under `demo-bundles` would collide with the record
  that job already writes, forcing a hand-rolled immutability and ledger path.
  That is a second publishing system. Rejected.
- **A second, Compose-specific product gate.** Rejected: the existing Product
  gate already encodes the semantics; a surface flag reuses it.
- **Marking the demo network `internal: true` to claim strong offline
  isolation.** Rejected: published host ports are not reliable on an internal
  network, and the stronger claim would not be honestly testable. The offline
  claim is narrowed to what CI can prove.
- **Baking any credential, key or token into the artifact.** Rejected by the
  commission and by design: the keypair is generated locally at run time into a
  volume that the artifact does not contain.

### Boundaries for this phase

- The current Helm surface stays intact except for changes genuinely required to
  project the shared canonical model.
- No second product architecture, no Compose-only product feature, no hosted demo
  service.
- No parallel test taxonomy: new permanent tests are wired into the existing Make
  and CI levels described in `docs/maintainers/testing.md`, and no current gate is
  weakened.
- Append-only history: no rebase, amend, reset, force-push or rewrite.
- Phase 10B ends as CANDIDATE. A phase is closed by review, not by its author.

## 15.1 Phase 10B implementation — CANDIDATE at `b91e534d`

Phase 10B is implemented. It remains a CANDIDATE: a phase is closed by review,
not by its author. `PACTO_PR_TARGET_STATE.md` is untouched.

### Commits appended, in order

| SHA | Subject |
|---|---|
| `589a672c` | docs: Phase 10 closed at f2a181b1, Phase 10B opened |
| `da3093c2` | test(acceptance): the canonical scenario grows a second surface |
| `2450a11d` | fix(scenario): the demo's state belongs to the directory it runs in |
| `4fb70daf` | test(acceptance): the Compose demo, proved the way a stranger meets it |
| `724da131` | build(release): the demo artifact is a release unit |
| `92cd9e10` | test(release): the dry run proves the demo artifact's crash window too |
| `8f8a6060` | test(scenario): the demo may not choose the host's architecture for it |
| `10dd6c36` | fix(e2e-live): journey C enters through the list the surface actually fills |
| `b91e534d` | fix(demo): the run directory has to be readable by the user the demo runs as |

Starting point `f2a181b1`. 41 files, +3046 / -77. No rebase, amend, reset,
force-push or rewrite: the branch is append-only from `f2a181b1` to `b91e534d`.

### The canonical model, and the two projections over it

`tests/acceptance/scenario` still holds exactly ONE demo fixture, the Phase-8B
value `OperationalGraph`. Phase 10B adds no second fixture and no framework.

- **`surface.go` — the platform difference, declared as data.** `Surface` and
  `Capability`: `SurfaceKubernetes` provides `CapabilityOperationalTarget`,
  `SurfaceCompose` provides nothing, and `Missing()` names the gap. `FactCount`
  became surface-aware and subtracts the facts that depend on a capability the
  surface does not have. Nothing anywhere branches on a platform name; every
  consumer asks the model what the surface provides.
- **`helm.go` — the Kubernetes projection.** The observation `--set` strings the
  Kind harness used to build inline moved onto the scenario. Two consumers, the
  harness and the parity test, which is what rule 2 of
  `docs/maintainers/testing.md` requires before a projection may exist. The Helm
  surface is otherwise untouched.
- **`compose.go` — the Compose projection.** A sibling over the same value,
  emitting `compose.yaml`, `.env` and the fixture data. Four services: the
  registry, the Evidence Server, a one-shot seed and the dashboard, wired with
  health checks and `depends_on` conditions.
- **`digest.go` — the digests the evidence payloads pin.** Computed from the
  bundle bytes with `oci.BundleImage`, so the shipped envelopes reference content
  that exists before any registry does.
- **`parity_test.go` — the comparison.** Both projections express the same
  scenario, and the ONLY divergence is the declared capability.

### The artifact, and what identifies it

`go run ./tests/acceptance/scenario/project demo -dir …` renders a run
directory; `oras push <ref> .` turns that directory into ONE tar+gzip layer.
14 files: `compose.yaml`, `.env`, `plan.tsv`, `seed.sh`, `README.md` and the
bundle directories the plan publishes.

The digest the registry assigns is the identity; the tag is the convenience. The
acceptance run publishes two versions, records both digests, asserts they differ
and materializes by `@sha256:…`, never by tag.

`plan.tsv` is the whole imperative surface: `seed.sh` reads it field by field,
checks every record's arity, and never sources or evaluates it. No service name,
repository, tag, subject, sequence or producer is written in the script, so the
demo cannot describe a fixture different from the one CI proves.

### Distribution: one publisher, no second system

`demo-compose` is a release unit (`release/units/demo-compose/package.json`,
coordinate `ghcr.io/trianalab/pacto/demo`), on the core line, published by ONE
job carrying `# pacto-publishes: demo-compose` through the SAME
`release/orchestrator/publish-oci-unit.sh` adapter every other OCI unit uses.
Integrity, provenance, immutability, the ledger and the one-publisher policy are
inherited, not re-invented.

`oras push` is not byte-deterministic and stamps its own layer, so the digest is
unknowable before the push. The unit is therefore adopted the way the buildx
images are: by the `org.opencontainers.image.revision` and `.version`
annotations, which `verify-oci.sh` reads from the OCI manifest when there is no
docker config Label. `dry-run.sh` ITEM 3b proves that path end to end — push the
artifact, lose the runner before the ledger record, then re-run the adapter with
`-- false` as the assertion that no re-push happens, and watch it adopt.

### The claims, and where each is proved

`tests/acceptance/local/compose-demo.sh`, eleven stages, no `sleep` anywhere:

| Claim | Stage |
|---|---|
| the run directory is outside the checkout, and empty | 0 |
| the demo pins the REAL production image | 1 |
| two pinned versions, two different digests | 3 |
| materialized BY DIGEST, byte-identical to what was pushed | 4 |
| no key, token or password in the artifact | 5 |
| observed readiness (`up -d --wait`), and the only bind mount is the run directory | 6 |
| the live Product API proves the canonical fixture on this surface | 7 |
| the documented browser journeys work on the pulled demo | 7 (`browser`) |
| restart persistence: a re-push is a no-op, a replayed envelope is a 409 | 8 |
| offline after the pull: fresh volumes, `--pull never` | 9 |
| upgrade: two pinned versions running at once on their own ports | 10 |
| cleanup leaves no container, no volume, nothing but the pulled images | 11 |

Nothing under the checkout is on the execution path. What runs from the checkout
is the JUDGE — the Product gate and the browser suite — talking to the demo over
HTTP, exactly as an outside observer would.

### Secrets, ports, readiness

- **Secrets: none.** The Evidence Server mints its own keypair at first start
  into a volume. Stage 5 greps the pulled tree for key material and key file
  names and fails on either.
- **Ports.** Deterministic defaults in `.env`, overridable by environment. Stage
  10 runs two versions at once on different ports and checks the first is still
  serving, which is the same claim observed rather than asserted.
- **Readiness.** Docker health checks and `depends_on` conditions; `--wait` is
  the clock. The dashboard starts only after the seed has completed
  successfully, so the first snapshot a user sees is not an empty fleet.
- **Project identity.** The Compose file pins no `name:`, so the project name
  comes from the run directory. That is what makes two pulled versions
  independent rather than one overwriting the other.

### Two failures found by running it, and what they were

Neither was found by reading. Both are recorded because the fix is only half the
evidence.

1. **Journey C entered through a list the Compose surface does not fill.** The
   first live browser run against the pulled demo failed journey C on a 60s
   locator timeout: it reached for "Revisions in use", which means "matched to a
   running target", on a surface that declares no operational target. The
   product already handles this — it opens "All revisions" in its place — so the
   journey was reporting an absent controller as a broken revision page. The
   entry path now follows the declared capability, the way journey D's skip
   already does; every assertion is unchanged and still runs on both surfaces.
   After: 7 passed, 1 skipped naming the missing capability, 0 failed. On
   Kubernetes the path is byte-for-byte the one the Kind vertical proves, which
   the local `test-acceptance-kind-operational-graph` run confirms with 8/8.
2. **The run directory's mode.** CI went red where local was green, which is the
   whole difference between the machines: Docker Desktop virtualizes bind-mount
   ownership and Linux does not. The containers run as the image's non-root
   `pacto` user, the acceptance script created the run directory with
   `mktemp -d` (0700), and on Linux busybox could not open the seed script —
   `can't open '<script>': Permission denied`, exit 2, reproduced directly. The
   script now creates the directory at 0755, which is what `mkdir pacto-demo`
   under a normal umask gives the user the documented journey describes. The
   artifact README and the docs page state the requirement and the one-line fix.
   `up_or_dump` now prints `compose ps -a` and every container log on a failed
   bring-up, because the original failure arrived as one line and nothing else.

### Mutation evidence (every mutation reverted, and verified reverted)

| Mutation | What bit |
|---|---|
| remove `# pacto-publishes: demo-compose` | `TestExactlyOnePublisherPerUnit`: `release unit "demo-compose" has NO publisher` |
| add a second `oras push …/demo:sneaky` to ci.yml | `TestNoDuplicateRegistryCoordinate` fails for `demo-compose` AND `demo-bundles` |
| add a `platform:` field to `composeService` | `TestCompose_LetsTheHostArchitectureDecide` fails on evidence, seed and dashboard |
| wrong revision annotation on a pushed artifact | `verify-oci.sh` answers `conflict`, exit 3 (matching pair answers `adopt`; no annotations answers `conflict`) |

Two more counterexamples were produced by the work itself rather than staged:
run `32031872043`'s first `ci-e2e-compose` attempt failed on the 0700 directory,
and the first live browser run failed journey C. Both are the counterfactual for
the fix that followed.

### Local verification at `b91e534d`

| Command | Result |
|---|---|
| `make ci` | pass (incidental helm-docs README drift reverted, not committed) |
| `make lint` | pass |
| `make test-browser` | 219 passed |
| `make test-browser-compose` | 11 stages pass; 7 journeys passed, 1 skipped, 0 failed |
| `make test-acceptance-kind-operational-graph` | pass, 14 facts, 8/8 live journeys |
| `make test-acceptance-kind-dashboard` / `-upgrade` / `-reconcile` | pass |
| `make test-acceptance-kind-evidence` / `-observation` | pass on a rerun; the first attempt failed on a local port-forward that never answered, with four leftover kind clusters competing for this workstation |
| `make artifact-drift` | OK |
| `make release-dry-run` | `RELEASE-DRY-RUN OK`, including ITEM 3b and 10/10 parallel records |
| `node --test release/orchestrator/*.test.mjs` | 16/16 |
| `git diff --check` | clean |

### GitHub state at `b91e534d`

| Run | Workflow | Conclusion |
|---|---|---|
| `32031872043` | CI | success |
| `32031871745` | Pacto Contract CI | success |
| `32031871987` | Security | success |
| `32031871741` | Docs check | success |
| `32031871734` | Repowise (architecture health) | success |
| `32031871992` | Validate PR title | success |
| `32031871959` | Rebuild dashboard UI | skipped |
| `32031872055` | Auto-merge Dependabot PRs | skipped |

Every CI job green, including all six Kind shards and the new `ci-e2e-compose`
leg, `required`, `release-dry-run` and `artifact-drift`. `ci-e2e-kind (upgrade)`
failed once on `kindload: … is not present after loading it` and passed on
rerun with no code change; the same scenario passes locally. One observation is
not a pattern, so nothing was changed for it — it is recorded here so a second
occurrence is recognised as a pattern rather than met fresh.

CodeQL analyses (`Analyze (go)`, `(javascript-typescript)`, `(python)`,
`(actions)`) all succeeded. The aggregate `CodeQL` check is red for the same
reason section 14.1 records: it summarizes open alerts, and there are nine, all
inherited.

### CodeQL delta introduced by Phase 10B: zero

The nine open alerts at `b91e534d` are the same nine numbers, rules and
locations as at `f2a181b1`: `#38` `py/incomplete-url-substring-sanitization` in
`release/scripts/docs_check.py:197`, `#40`–`#43` `go/path-injection` in
`internal/app/resolve.go`, `#59`–`#62` `go/path-injection` in `pkg/oci/cache.go`.
Nothing added, nothing removed. They were re-queried at this SHA rather than
inherited as work, exactly as the carry-out in section 14.1 requires.

### Review threads at `b91e534d`

199 threads, fully paginated (page one caps at 100 and hides every unresolved
one). 10 unresolved, all bot-authored and all inherited: five
`github-code-quality` findings on the committed mermaid asset bundle
`pkg/dashboard/ui/assets/ganttDiagram-*.js`, and four
`github-advanced-security` CodeQL path-expression notices on `pkg/oci/cache.go`,
which are four of the nine alerts above. No human review thread is unresolved
and Phase 10B opened none.

### Carried out of Phase 10B, unchanged

- The nine inherited CodeQL alerts. Re-queried here, delta zero, still not this
  phase's work.
- The `release/orchestrator/verify-k8s-standalone.sh` description warning,
  carried to Phase 14. Phase 10B did not touch that gate and did not repair it
  as a tidy-up.

### Phase 10B verdict

CANDIDATE. Not closed, not self-declared. Phase 11 is not started, no PR comment
was published, no review thread was resolved, no PR metadata was changed and the
PR remains a draft.

## 15.2 Phase 10B narrow closure repair — CANDIDATE at `76ed7fee`

Phase 10B was reopened for two blockers and repaired narrowly. It remains a
CANDIDATE: a phase is closed by review, not by its author. Nothing in this
section closes it, and Phase 11 is not started.

`PACTO_PR_TARGET_STATE.md` was not touched by this repair. It was extended by a
separate authored commit, `40a1f0ae` "docs: define OCI-native evidence referrers
phase", which landed on the branch during the verification window and is not
part of Phase 10B.

### The two blockers, as found

1. **Immutability stopped at the artifact.** The demo artifact is pulled by
   digest, and then ran images named by tag. One immutable demo digest could
   execute different bytes after a tag moved, so the immutability the artifact
   advertises was true of the artifact and not of what it runs.
2. **The offline stage proved something smaller than the claim.** It ran
   `docker compose up --pull never` on a runner that still had Internet egress,
   with the artifact-distribution registry still serving. That is evidence that
   Docker pulled no images. It is not evidence that the demo needs no network,
   and the documents said the larger thing.

### Commits appended, in order

| SHA | Subject |
|---|---|
| `ef385031` | fix(scenario): the demo artifact refuses an image a tag could move |
| `d44195d2` | style(scenario): the hex check is a Trim, not a loop |
| `49f5b654` | build(release): the demo pins the dashboard image this transaction published |
| `da358659` | test(acceptance): the offline claim is a network boundary, not a pull flag |
| `aa967b42` | docs: say exactly what the demo is tested to mean by offline |
| `76ed7fee` | fix(acceptance): a packet to the host itself never reaches DOCKER-USER |

`40a1f0ae`, the authored TARGET commit, sits between `aa967b42` and `76ed7fee`
and is not part of this repair.

Starting point `84435c23`, confirmed to be the remote head before the first
change and still the remote head immediately before the push. 10 files,
+734 / -53. No rebase, amend, reset, force-push, squash or rewrite: the branch
is append-only from `84435c23`.

| File | What changed |
|---|---|
| `tests/acceptance/scenario/compose.go` | `checkPinnedImage`, applied to both images; `ComposeDefaultRegistryImage` |
| `tests/acceptance/scenario/compose_test.go` | the adversarial cases below |
| `tests/acceptance/scenario/project/main.go` | `-registry-image` defaulting to the pinned index; `-pacto-image` with no default |
| `.github/workflows/release.yml` | the `demo-compose` step that resolves and requires the transaction's dashboard-image digest |
| `release/orchestrator/dry-run.sh` | ITEM 3b now publishes a dashboard image and pins the demo to the recorded digest |
| `tests/release/demo_image_pin_test.go` | new; four tests over the workflow and the dry run |
| `tests/acceptance/local/compose-demo.sh` | digest-pinned local path; stage 10 rewritten as a network boundary |
| `tests/acceptance/scenario/project/demo/README.md.tmpl` | the artifact's own offline section |
| `docs/examples/compose-demo.md` | the public offline section and the image-pinning paragraph |
| `docs/maintainers/testing.md` | the reasoning behind both claims |

### Blocker A — the images are bound to digests

`checkPinnedImage` refuses any reference that is not `…@sha256:` followed by 64
lower-case hex characters, and it refuses it where the projection is built, so
every consumer inherits it: the acceptance harness, the `project` CLI, the
release workflow and the dry run. `repo:tag@sha256:…` is accepted, because the
tag is then documentation and the digest still resolves the bytes. The message
names the offending field and says what a pinned reference looks like.

The registry image defaults to `registry:2@sha256:a3d8aaa6…`, which is the
published multi-platform INDEX. Its children are separate manifests —
`sha256:46faa9a1…` is the linux/amd64 one, `sha256:fa647fc1…` the linux/arm64 —
and pinning one of those would run a single architecture everywhere and emulate
it on the others. Because the pin is an index, Docker still selects the
host-native child, so no `platform:` key is emitted anywhere and none is needed.

In production the pin comes from the ledger. `demo-compose` reads
`release/orchestrator/ledger.sh digest "$PACTO_RELEASE_TXN" dashboard-image`,
which is where `publish-oci-unit.sh` recorded the index digest `imagetools
create` assembled in this same transaction, and projects
`ghcr.io/trianalab/pacto/dashboard@sha256:…`. No second lock format and no
second adapter: it is the same ledger every other unit reads, which is also why
a single-unit recovery dispatch, where no job output survives, resolves the
right image. The step fails closed — a transaction with no recorded
dashboard-image digest publishes no demo artifact at all, rather than one
pointing at an image this release never verified.

The local acceptance runs the same shape. It builds the production image from
the production Dockerfile, pushes it, re-pins to the digest the registry
assigned and pulls that back, so what is exercised is a digest pin and not a
convenient local tag.

### Blocker B — the boundary, stated and proved

The claim, as written in the artifact's README, the public page and the test:

> After the demo artifact and its digest-pinned images have been pulled, the
> stack requires no external network access. Its private Compose service network
> remains available because the dashboard, Evidence Server and embedded registry
> must communicate with each other.

Stage 10 proves that claim and no larger one:

1. `docker compose down -v`, then `create --pull never` — `create` is the only
   window in which a filter can precede the one-shot `seed`'s first packet,
   because before it runs the seed has no network namespace to filter.
2. A control probe from inside the demo's own network reaches the artifact
   registry through `host-gateway`. Without that the isolation below would be
   unfalsifiable.
3. Two chains refuse every packet leaving that network, with an
   `ESTABLISHED,RELATED` accept ahead of each so replies to the published ports
   still return: `DOCKER-USER` for what the host forwards, and `INPUT` for what
   is addressed to the host itself, which is never forwarded and so is invisible
   to `DOCKER-USER`. The backend is chosen by asking which of `iptables-nft` and
   `iptables-legacy` owns the `DOCKER-USER` chain, since writing to the other
   one would install a rule that filters nothing. The control probe is repeated
   and must now fail.
4. The artifact-distribution registry is stopped, and proved unreachable.
5. `up --pull never` from empty volumes, on observed readiness only — the demo's
   own health checks and `--wait`; no unconditional sleep was added anywhere.
6. The Product gate runs against the isolated stack and reaches the same 12
   facts, and in `browser` mode the same live journeys run against it.
7. The counterexample: one startup dependency is redirected at an endpoint
   outside the network, via a `run --rm --no-deps seed` override against the
   still-isolated stack, and is required to fail.

`internal: true` is still rejected, for the reason recorded in section 15: it
would take the published ports with it, and the boundary explicitly keeps them.

### Mutation evidence

Every mutation was applied to production or workflow code, run, and reverted;
the tree was confirmed identical to the commit afterwards in each case.

| # | Mutation | Test run | Result |
|---|---|---|---|
| A1 | `checkPinnedImage` always returns nil | `TestCompose_RefusesAnImageATagCouldMove`, `TestCompose_EmitsThePinnedReferencesUnchanged` | FAIL — every table case reported "the projection accepted …, so the demo is not pinned" |
| A2 | the registry-image call site removed, the pacto one kept | `TestCompose_RefusesAnImageATagCouldMove` | FAIL on the four registry cases: tag-only, no tag, `@md5:`, digest with no repository |
| A3 | the default registry pin switched from the index to its linux/amd64 child `sha256:46faa9a1…` | full Compose acceptance | FAIL at stage 5: "resolves to amd64 on a arm64 host — that pin is one architecture's manifest, not the multi-platform index" |
| A4 | `demo-compose` pins `…/dashboard:${core-version}` instead of the ledger digest | `TestDemoComposePinsTheImageTheTransactionPublished` | FAIL — "which a tag could move; it must be repo@sha256:…" |
| A5 | the fail-closed digest guard deleted from the workflow step | `TestDemoComposeRefusesToPublishWithoutTheDigest` | FAIL — "would publish an artifact pinning an unverified image" |
| A6 | none — found naturally | `TestDryRunPinsTheDemoImageToo` | FAIL against the dry run as it stood, which is what ITEM 3b repaired |
| A7 | the harness's own pin guard deleted, run with `PACTO_DEMO_IMAGE=ghcr.io/trianalab/pacto/dashboard:3.1.1` | full Compose acceptance | aborts at stage 3 with the projection's message, proving the refusal is the projection's and not the harness's |
| B1 | `deny_egress` made a no-op | full Compose acceptance | FAIL at stage 10: "the filter is not denying egress, so the isolation below would be a claim rather than a test" |
| B2 | the artifact registry left serving | full Compose acceptance | FAIL at stage 10: "the artifact registry is still serving, so this is not a cold start without it" |
| B5 | none — found by CI. The filter was `DOCKER-USER` only, which is FORWARD; on Linux `host-gateway` is a host address, delivered locally, so the control probe went straight through | `ci-e2e-compose` at `40a1f0ae`, job `95427465636` | FAIL at stage 10 with B1's message. The stage refused to claim an isolation it had not established, on a platform the author's machine is not. Repaired in `76ed7fee` by filtering `INPUT` by ingress interface as well, and the whole acceptance re-run locally |
| B4 | the counterexample redirected at an INTERNAL endpoint (`registry:5000/demo`) instead of an external one | full Compose acceptance | FAIL: "a startup dependency pointed outside the demo's network still succeeded, so stage 10 does not test what it says" — the counterexample discriminates on the endpoint being outside, not on there being a redirect |

A7 also exposed a defect in the harness itself: bash does not propagate
`errexit` out of a failing subshell inside a function called from a command
substitution, so a refused projection was pushed as an empty artifact and only
surfaced four stages later as a missing `compose.yaml`. `project_and_push` now
returns non-zero, and the run stops where the refusal happens.

### Local verification at `76ed7fee`

| Command | Result |
|---|---|
| `git diff --check` | clean |
| `gofmt -l` over the touched trees | clean |
| `shellcheck tests/acceptance/local/compose-demo.sh` | clean |
| `go test -race ./tests/acceptance/scenario/...` | ok |
| `go test -race ./tests/acceptance/kind/productready` | ok |
| `go test ./tests/release/...` | ok |
| `make ci` | exit 0 |
| `make artifact-drift` | OK — one-publisher gate and apply-release-plan idempotency |
| `make release-dry-run` | OK — "demo pinned to the dashboard image this transaction published (sha256:e0a0b43f…)", "no recorded dashboard-image digest -> no demo artifact (fail closed)", "demo artifact adopted from its manifest annotations (no re-push)" |
| `make test-browser` | exit 0 — 219 deterministic journeys |
| `make test-browser-compose` | exit 0 — all 12 stages, 7 live journeys against the pulled demo and the same 7 against the isolated one, journey D skipped for the absent `operational-target` capability |
| `python3 release/scripts/docs_check.py` | 9/9 |

The Compose acceptance was re-run end to end after the last code change, not
inherited from an earlier tree.

### Preserved, unchanged

The 14-vs-12 fact accounting and the explicit `operational-target` capability
gap; the semantic parity tests over rendered projections; digest-pinned
demo-artifact pulls; run directories outside the checkout; runtime-generated
credentials; restart and replay behaviour; deterministic overridable ports;
two-version independence; cleanup verification; all six Kind shards; the shared
Product gate and browser suite; release ledger, adoption and conflict behaviour;
the CodeQL carry-out; and the Phase 14 `verify-k8s-standalone.sh` wording
warning, which this repair did not touch.

### GitHub Actions at `76ed7fee`

Every workflow the branch runs, at the final code SHA:

| Run | Workflow | Result |
|---|---|---|
| `32044972187` | CI | success (attempt 2) |
| `32044972202` | Security | success |
| `32044972294` | Docs check | success |
| `32044972179` | Pacto Contract CI | success |
| `32044972172` | Repowise (architecture health) | success |
| `32044972176` | Validate PR title | success |
| `32044972242` | Rebuild dashboard UI | skipped (not a UI change) |
| `32044972188` | Auto-merge Dependabot PRs | skipped |

Jobs inside run `32044972187`, all success: `changes` `95436082426`,
`ci-static` `95436121027`, `ci-gates` `95436121396`, `ci-engine` `95436105145`,
`ci-dashboard` `95436105962`, `ci-oci` `95436103600`, `ci-e2e-envtest`
`95436116840`, `ci-integration-kubernetes` `95436107219`, `operator-build`
`95436110420`, `dashboard-e2e` `95436117695`, `artifact-drift` `95436082715`,
`release-dry-run` `95436082782`, `release-version-test` `95436108345`,
**`ci-e2e-compose` `95436082841`**, all six Kind shards — `dashboard`
`95436082224`, `operational-graph` `95436082286`, `evidence` `95436082661`,
`upgrade` `95436082725`, `observation` `95436109872`, `reconcile` `95436113361`
— and `required` `95438151161`.

Two attempts, and the reason matters for reading the evidence. GitHub was
returning `429 Too Many Requests` for action downloads for part of this window,
which fails jobs in `Set up job` before any repository code runs. At `40a1f0ae`
that took out most of the matrix; attempt 1 at `76ed7fee` lost two Kind shards
to it, `ci-e2e-kind (dashboard)` and `(operational-graph)`, both in `Set up job`.
Attempt 2 re-ran exactly those and both passed. No repository step was re-run to
turn a red into a green: the only substantive failure in this whole window was
`ci-e2e-compose` at `40a1f0ae`, which was a real defect in the new stage and is
recorded as mutation B5 above.

### CodeQL at `76ed7fee`

Nine open alerts, the same nine numbers, rules and locations as at `84435c23`
and as at `f2a181b1` before it: `#38` `py/incomplete-url-substring-sanitization`
in `release/scripts/docs_check.py:197`, `#40`–`#43` `go/path-injection` in
`internal/app/resolve.go`, `#59`–`#62` `go/path-injection` in `pkg/oci/cache.go`.
Delta from the starting SHA: zero added, zero removed. Re-queried at this SHA
rather than inherited.

### Review threads at `76ed7fee`

199 threads, fully paginated — page one caps at 100 and hides every unresolved
one. 10 unresolved, all bot-authored and all inherited: six
`github-code-quality` findings on the committed mermaid asset bundle
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` (one "superfluous
trailing arguments", five "useless assignment to local variable") and four
`github-advanced-security` path-expression notices on `pkg/oci/cache.go`, which
are four of the nine alerts above. Section 15.1 counted five code-quality
threads; six is the correct figure and the total of ten is unchanged. No human
review thread is unresolved, and this repair opened none.

### Diff hygiene

`git diff --check` clean. Working tree clean of tracked changes at
`76ed7fee`. The only untracked paths are the pre-existing local tool
directories `.claude/`, `.codex/`, `.mcp.json` and `AGENTS.md`, which were
untracked before Phase 10B opened and are not this branch's to add.

Two local side effects were observed and reverted rather than committed:
`go.work.sum` gained 19 indirect `/go.mod` hash lines during a toolchain
resolution, and a `helm-docs` pass regenerated
`integrations/kubernetes/charts/pacto-dev-gateway/README.md` over its
hand-written content. Neither is part of this repair. The second is worth a look
by whoever owns that chart: a local `make` run rewrites a tracked, hand-written
README.

### Phase 10B repair verdict

CANDIDATE. Not closed, not self-declared. Phase 11 is not started, no PR comment
was published, no review thread was resolved, no PR metadata was changed, the PR
remains an open draft, and `PACTO_PR_TARGET_STATE.md` was not altered by this
repair.

## 15.3 Independent review at `430b2159` — one blocker remains

The independent review accepts blocker A and most of blocker B, but does not
close Phase 10B. One falsifiable gap remains in the permanent proof of the
network boundary. Phase 10C is defined in TARGET and in its design document but
is not started.

### Repository and GitHub state independently verified

- PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE; its remote head is
  exactly `430b2159e96494f9b2f034edd03b3ed3382a5101` on
  `feat/operational-graph-fleet`.
- `84435c23` is an ancestor of that head. The range is the eight linear commits
  recorded in 15.2, including the separate documentation-only `40a1f0ae`; no
  parent changed and no rebase, amend, squash or force-push is present.
- Blocker A is accepted. Both image options are refused at the projection unless
  they are exact lower-case `sha256` digest references; the production demo
  reads the dashboard-image digest from the release ledger and fails closed
  without it; the dry run exercises the same path. The default registry
  reference is the multi-platform index, and the Compose projection emits
  neither rewritten references nor a `platform` override.
- The final CI run `32047603367` is success on attempt 1. All twenty jobs are
  green, including `ci-e2e-compose` `95439062238`, all six Kind shards,
  `release-dry-run`, `artifact-drift` and `required`. The Compose log shows the
  isolated stage starting from empty volumes, the registry stopped, 12 Product
  facts, both browser legs and the redirected dependency failing. Security
  `32047603288`, Docs check `32047603265`, Pacto Contract CI `32047603280`,
  Repowise `32047603418` and title validation `32047603453` are also success.
- Code scanning still has the same nine inherited alerts: `#38`, `#40`-`#43`
  and `#59`-`#62`; this range touches none of their three files. The aggregate
  CodeQL summary remains red and the analyses themselves succeed.
- Review threads were fully paginated: 199 total, 189 resolved and 10
  unresolved. All ten are inherited bot threads: six on the generated Mermaid
  bundle and four CodeQL path-expression threads in `pkg/oci/cache.go`.
- Focused independent checks pass: the scenario race suite, the release suite,
  shellcheck, gofmt and `git diff --check`. The tracked tree is clean; only the
  four pre-existing local agent paths are untracked.

### Accepted portion of blocker B

The repair correctly distinguishes the host-local path from forwarded traffic.
`INPUT` is necessary for `host-gateway`, `DOCKER-USER` is the Docker-supported
FORWARD hook, replies to published ports are preserved by an
`ESTABLISHED,RELATED` rule, the artifact registry is stopped, startup uses empty
volumes and `--pull never`, internal service communication and host access are
observed, and the Product gate plus live browser journeys run against the
isolated stack. The documentation now states the same bounded claim as the
harness.

### Remaining blocker — the FORWARD arm is asserted, not proved to bite

Every external control currently uses `pacto-outside:host-gateway`. On Linux
that destination is local to the host, so those packets traverse `INPUT`, never
FORWARD or `DOCKER-USER`. The post-filter control and the redirected-seed
counterexample use that same address. Consequently this mutation leaves the
whole stage green:

1. keep the two `INPUT` rules;
2. remove only the `DOCKER-USER` `ESTABLISHED,RELATED` and bridge-egress rules;
3. run stage 10 unchanged.

The host-gateway probe is still rejected by `INPUT`; the stack needs only its
private network; the Product and browser gates still pass; and the redirected
seed still fails at `INPUT`. But ordinary Internet-bound traffic is now
forwarded out of the bridge. The test therefore does not permanently establish
the larger claim that every route out is refused, and a future regression of
the FORWARD arm would be reported as offline success.

Closure requires two discriminating controls, both reachable before isolation
and both refused afterwards: one host-local path that proves `INPUT`, and one
genuinely forwarded path that proves `DOCKER-USER`. They must have independent
mutation proof: removing either arm alone makes the acceptance fail for that
arm. Prefer a deterministic local endpoint over a public Internet dependency;
reuse the existing throwaway registry if it can be reached through a distinct
forwarded route. Preserve the accepted boundary, the existing live Product and
browser proof, the exact documentation claim and all blocker-A work. Do not add
a generic networking framework.

### Verdict

**Phase 10B remains CANDIDATE.** Blocker A is CLOSED. Blocker B is narrowed to
the missing FORWARD/`DOCKER-USER` discriminator above. Phase 10C must not start
until an append-only repair is independently reviewed and this counterexample
is closed.

## 15.4 Phase 10B narrow closure repair, second pass — CANDIDATE at `4399c63a`

Two things were asked for and both are in this repair: the FORWARD/`DOCKER-USER`
discriminator that section 15.3 left open, and a correction that had nothing to
do with it — the Compose demo was being distributed as a generic ORAS-pushed
directory, and Docker Compose owns this OCI artifact type natively. They landed
together because the second one rewrites the very stage the first one repairs.

Phase 10B remains a CANDIDATE. A phase is closed by review, not by its author.
Nothing here closes it, `PACTO_PR_TARGET_STATE.md` was not touched, no PR comment
was published, no review thread was resolved, no PR metadata was changed, and
Phase 10C is NOT started.

### Commits appended, in order

| SHA | Subject |
|---|---|
| `65a4c192` | feat(acceptance): the demo artifact is one compose file, not a directory |
| `5a421228` | fix(acceptance): two netfilter hooks, and a demo run with no local files |
| `45abda33` | feat(release): demo-compose is published by Docker Compose, adopted by content |
| `19d64373` | test(release): gates that fail if the Compose demo stops owning its artifact |
| `c25b6125` | docs: the demo runs from the registry, and the isolation proof has two arms |
| `4399c63a` | fix(acceptance): pf must outlive a pod that is still being created |

Starting point `7c69ba43`, confirmed to be the remote head before the first
change. `7c69ba43` is an ancestor of `4399c63a`; the range is exactly those six
linear commits and every parent is the previous one. 22 files, +1931 / -775. No
rebase, amend, reset, force-push, squash or rewrite.

| File | What changed |
|---|---|
| `tests/acceptance/scenario/compose.go` | the application is the artifact: inline `configs` for every fixture input, long-form `ports`, named volumes for mutable state only, `x-pacto-demo.minimum-compose-version` |
| `tests/acceptance/scenario/compose_test.go` | the projection's own gates, including `TestCompose_BindMountsNothing`, `TestCompose_CarriesEveryFixtureInputInline` and `TestCompose_CarriesTheCanonicalBytes` |
| `tests/acceptance/scenario/scenario.go` | `MaterializeFiles` renders the bundle documents to bytes; `Materialize` writes those same bytes for the surface that still needs a directory |
| `tests/acceptance/scenario/digest.go` | `Digests` reads those bytes rather than a directory, because the Compose surface no longer has one |
| `tests/acceptance/scenario/digest_test.go`, `parity_test.go` | follow the two signatures |
| `tests/acceptance/scenario/project/main.go` | `demo -out <file>` replaces `demo -dir <dir>`; the README template and the chmod pass are gone with the directory |
| `tests/acceptance/scenario/project/demo/README.md.tmpl` | deleted (see "the artifact has no README", below) |
| `tests/acceptance/scenario/project/demo/seed.sh` -> `tests/acceptance/scenario/seed.sh` | renamed, byte-identical; there is no demo directory for it to belong to |
| `tests/acceptance/local/compose-demo.sh` | native publish + `-f oci://` execution; stage 11 rewritten with two independent netfilter controls |
| `.github/workflows/release.yml` | `demo-compose` publishes with `docker compose publish` and adopts by `PACTO_EXPECT_CONTENT` |
| `.github/workflows/ci.yml` | `ci-e2e-compose` and `release-dry-run` install a pinned Compose; ORAS removed from the compose job |
| `release/orchestrator/verify-oci.sh` | content-layer adoption, refusing to answer for any layer count but one |
| `release/orchestrator/publish-oci-unit.sh` | `PACTO_EXPECT_CONTENT` threaded through, asserted after the push |
| `release/orchestrator/dry-run.sh` | ITEM 3b rehearses the native publish, content adoption and a foreign-content refusal |
| `tests/release/compose_native_test.go` | new; the permanent regression gates |
| `tests/release/demo_image_pin_test.go` | one comment corrected |
| `docs/examples/compose-demo.md`, `docs/maintainers/testing.md`, `docs/maintainers/releases.md` | the native journey and the two-armed boundary |
| `Makefile` | comment only |
| `tests/acceptance/kind/lib.sh` | `pf` respawns (see "the one CI failure", below) |

### The RED, before anything was built

Three counterexamples, each run against the starting tree `7c69ba43` in a
separate worktree and each reverted.

1. **The missing FORWARD arm is not detected.** Section 15.3's mutation is to
   delete only the `DOCKER-USER` rules and keep `INPUT`. Run on this machine it
   behaves in MIRROR IMAGE, and the asymmetry is itself worth recording: Docker
   Desktop runs the engine in a VM where `host-gateway` is ROUTED, so there the
   surviving arm is `DOCKER-USER` and the mutation that leaves stage 10 green is
   deleting only `INPUT`. Either way the finding is the same and it is the
   finding that matters: with one shared probe, a whole arm of the firewall can
   be removed and the stage still passes. One probe cannot attribute a hook. The
   mirror mutation was run and the starting tree passed green with the `INPUT`
   arm gone.
2. **The old artifact is only publishable by a generic tool.** The starting tree
   assembles a directory and pushes it with `oras push`, which will package any
   local files at all. `docker compose publish` refuses that model outright: it
   loads the compose file more strictly than `up` does, and the short-form
   `ports` strings the old projection emitted are rejected before anything is
   uploaded.
3. **The old artifact cannot be executed the documented way.** An ORAS-pushed
   directory has `artifactType: application/vnd.unknown.artifact.v1`, and
   `docker compose -f oci://<repo>@sha256:<digest>` refuses to run it. The old
   journey therefore had no way to exist without `oras pull` and a materialized
   directory first — the thing the correction removes.

### The network boundary: two routes, two hooks

Stage 11 now installs two control endpoints and proves each one is only
reachable through the hook it names.

**Host-local, through `INPUT`.** A `registry:2` in the HOST's own network
namespace (`docker run --net host`), addressed from inside the demo at the
demo bridge's own gateway address. A packet whose destination is an address the
host owns is delivered locally: it traverses `INPUT` and never reaches `FORWARD`.
`deny_hostlocal_egress` refuses it with two `INPUT` rules, by INGRESS INTERFACE
rather than by destination, so it covers every host address and not the one the
probe happened to use.

**Forwarded, through `FORWARD`/`DOCKER-USER`.** A veth pair: `198.18.53.1/30`
stays in the host namespace, `198.18.53.2/30` is moved into the artifact
registry's namespace with `nsenter`. RFC 2544 benchmarking space, so it cannot
collide with anything real on a developer's machine or a runner. That address
belongs to no interface of the demo's bridge, so reaching it is a ROUTING
decision and the packet is seen by `FORWARD` and therefore by `DOCKER-USER`.
Docker's own `MASQUERADE` gives the return path; nothing else is wired.
`deny_forwarded_egress` refuses it with two `DOCKER-USER` rules, in on this
bridge and out somewhere else, so the four services keep talking to each other.

Both arms lead with an `ESTABLISHED,RELATED` accept and use `REJECT` rather than
`DROP`, so a demo that fails fails in seconds.

The order of the stage is the proof:

1. both endpoints are probed BEFORE any filter and must be reachable, else "it
   could not reach out" would be indistinguishable from "it never could";
2. `deny_forwarded_egress` alone: the forwarded endpoint must go AND the
   host-local one must remain — this is the independence proof, and it is also
   what establishes that the forwarded endpoint really is forwarded. A filter in
   `DOCKER-USER` that could close the host-local route would prove that route was
   never host-local;
3. `deny_hostlocal_egress`: the host-local endpoint goes too;
4. two counterexamples, one per route: the demo's one startup dependency that
   talks to a registry is redirected at each endpoint in turn and the stack must
   fail, so a filter that quietly stopped applying cannot leave the assertions
   above passing.

Every rule, the veth pair, the host-local container and both projects are
removed by the existing `cleanup` trap. A failed run leaves no host firewall
state: `allow_egress` tolerates each deletion separately so a rule that is
already gone cannot stop the shell before it removes the ones that are still
there.

### Docker Compose owns the artifact

**Publication.** `docker compose -f <projected file> publish -y <repo>:<version>`.
No `oras push`, and nothing is added to the manifest afterwards. The projection
stays digest-pinned on its own; `--resolve-image-digests` is not used.

**Identity.** The published artifact is
`artifactType: application/vnd.docker.compose.project` with exactly ONE
`application/vnd.docker.compose.file+yaml` layer holding the compose file
verbatim, so `sha256(the projected file)` IS the artifact's content identity. The
harness asserts that equality against the real manifest immediately after
publishing.

**Execution.** `docker compose -f oci://<repo>@sha256:<digest> -p <project> up -d
--wait`. There is no `oras pull`, no run directory and no local `compose.yaml`
anywhere on the path: the harness DELETES both projections before it runs
anything, and then asserts that the running project's only configuration file is
Compose's own cache of the digest it was asked for.

**Fixture inputs, without bind mounts.** Every document the demo reads — bundles,
plan, observation exports, evidence payloads, the seed script — is a Compose
`config` with inline `content`, projected from the one canonical scenario. The
running stack is asserted to have ZERO bind mounts. Named volumes carry only
mutable runtime state.

**The artifact has no README, and that is stated rather than worked around.** The
native format has one layer and it is the compose file; there is nowhere to put a
file a user could retrieve, and smuggling one into an extra layer would both
break the content identity above and be a lie about what the artifact exposes.
`docs/examples/compose-demo.md` records the limitation and holds the
authoritative instructions.

**Project names.** `pacto-demo` for the normal demo, `pacto-demo-next` for the
second version, both documented. Stage 12 runs two digest-pinned versions at
once and asserts separate projects, ports, containers and volumes, that starting
0.0.2 does not disturb 0.0.1, and that `down -v` on one leaves the other serving.
No top-level `name:` is emitted, so nothing collides between users or versions.

**The version floor is declared once.** `scenario.ComposeMinVersion` is `2.34.0`,
emitted into the artifact as `x-pacto-demo.minimum-compose-version`. The harness
and the dry run read it OUT OF THE PROJECTED ARTIFACT rather than restating it,
and fail early with an actionable message naming the release that added
`docker compose publish`. CI installs `docker/setup-compose-action` at `v5.5.0`
on both jobs that need it.

**Fetching the application is not pulling the images.** `-f oci://` contacts the
registry on every invocation and Compose has no offline mode for it; `--pull
never` refuses only IMAGE pulls. Stage 11 therefore fetches the application ONCE
with `--pull never` (proving no image moved), then empties the volumes, stops the
artifact registry, asserts the application can no longer be fetched at all, and
runs the rest of the stage against the already-created project by name.

**The release path.** `demo-compose` still goes through the one sanctioned
publisher, `publish-oci-unit.sh`, and still owns its ledger record. What changed
is the adoption handle. Compose stamps `org.opencontainers.image.created` into
the manifest, so two publishes of identical bytes have different manifest digests
and no digest can be precomputed; it emits no revision or version annotation, so
provenance adoption cannot fire either. `PACTO_EXPECT_CONTENT` carries the
content-layer digest instead: asserted after the push, and the crash-window
adoption rule. `verify-oci.sh` refuses to answer for an artifact with any layer
count but one, because a multi-layer artifact has no single content identity.
The occupied-tag conflict path is unchanged and still fails closed. No second
ledger and no new lock format.

### Permanent regression gates

`tests/release/compose_native_test.go`, plus `TestCompose_CarriesTheCanonicalBytes`
and the existing bind-mount and tag gates in `tests/acceptance/scenario/compose_test.go`.
They are structural, not greps: the workflow YAML is parsed and re-marshalled,
shell is reduced to command lines (continuations joined, comments dropped,
assignments stripped) and markdown to fenced blocks only, and the jobs on the
Compose path are found by RESOLVING THE MAKE GRAPH rather than by naming them, so
a new job that starts running the demo cannot skip the gate.

| Gate | Fails if |
|---|---|
| `TestDemoComposeIsPublishedByComposeItself` | the unit does not run the shared adapter, does not invoke `publish`, or carries no content key |
| `TestNothingOnTheComposeDemoPathTouchesOras` | the harness, the documentation or a workflow job on the path invokes `oras` |
| `TestTheDemoIsExecutedFromTheArtifactByDigest` | any non-publish `-f` is a local file or an `oci://` reference by tag |
| `TestEveryDemoProjectIsNamedExplicitly` | an `up` has no `-p`, or two versions share one project name |
| `TestCIRunsAComposeThatOwnsThisArtifact` | a CI job on the path pins a Compose below `scenario.ComposeMinVersion` |
| `TestTheDemoDocumentationTeachesTheNativeJourney` | the documentation stops teaching the native digest-pinned journey |
| `TestCompose_BindMountsNothing` | the published model contains a bind mount |
| `TestCompose_CarriesTheCanonicalBytes` | a fixture input is restated instead of projected from the canonical scenario |
| `TestCompose_RefusesAnImageATagCouldMove` | a service image is tag-only |

Two earlier drafts of these gates were thrown away for being grep-shaped: one
matched the text of a `fail` diagnostic that quotes the rule, another matched a
sentence of prose, and a third put `ledger-init` on the Compose path because a
COMMENT mentioned `dry-run.sh`. A gate that fires on documentation about the rule
is a gate nobody keeps.

### Mutation evidence (every mutation reverted, and verified reverted)

Sixteen mutations, each applied alone, each run, each reverted with the file's
`sha256` compared before and after. No general-purpose mutation framework was
added; the mutations were applied and reverted by hand.

| # | Mutation | Killed by |
|---|---|---|
| 1 | `oras push` instead of `docker compose publish` in the release unit | `TestDemoComposeIsPublishedByComposeItself`, `TestNothingOnTheComposeDemoPathTouchesOras` |
| 2 | `oras pull` before execution in the acceptance harness | `TestNothingOnTheComposeDemoPathTouchesOras` |
| 3 | `oras pull` in the public documentation | `TestNothingOnTheComposeDemoPathTouchesOras` |
| 4 | a bind mount in the published model | `TestCompose_BindMountsNothing` |
| 5 | execute by tag (`-f oci://repo:1.2.3`) | `TestTheDemoIsExecutedFromTheArtifactByDigest` |
| 6 | a local materialized `compose.yaml` on the executable journey | `TestTheDemoIsExecutedFromTheArtifactByDigest` |
| 7 | a service image pinned by tag only | `TestCompose_RefusesAnImageATagCouldMove` |
| 8 | the release unit bypasses the shared adapter | `TestDemoComposeIsPublishedByComposeItself` |
| 9 | one project name for both versions | `TestEveryDemoProjectIsNamedExplicitly` |
| 10 | a config dropped from the projection | `TestCompose_CarriesEveryFixtureInputInline` |
| 11 | a config whose content is a checkout path instead of the canonical bytes | `TestCompose_CarriesTheCanonicalBytes` |
| 12 | CI pinned to a Compose below the declared floor | `TestCIRunsAComposeThatOwnsThisArtifact` |
| 13 | delete only the `DOCKER-USER` rules | "the DOCKER-USER arm did not refuse the forwarded route to 198.18.53.2:5000" |
| 14 | delete only the `INPUT` rules | "the INPUT arm did not refuse the host-local route to <gateway>:15072" |
| 15 | point the forwarded control at a host-local address | "the DOCKER-USER arm did not refuse the forwarded route" — which is exactly section 15.3's counterexample, now fatal |
| 16 | disable isolation entirely | same assertion, at the first arm |

Mutations 8 and 11 are the two that mattered most, because both SURVIVED their
first gate and the gate had to be strengthened. 8 survived a
`strings.Contains` check that a `true `-prefixed line still satisfied — the gate
now requires the adapter to actually be RUN. 11 survived because nothing compared
a config's content to the canonical bytes the scenario materializes;
`TestCompose_CarriesTheCanonicalBytes` was written for it. A discarded seventeenth
mutation, replacing a config with a checkout path in a way that produced a
duplicate YAML key, was thrown out as a vacuous kill: it failed to compile, which
proves nothing about the gate.

### Local verification at `4399c63a`

All green.

- `bash tests/acceptance/local/compose-demo.sh browser` — the FULL Compose
  acceptance, rerun after the last change to any file it reads. 29 assertions,
  both live browser legs (7 passed, 1 skipped, twice), the two-armed isolation
  stage and the two-version stage. Local Compose is 5.3.1 against the artifact's
  declared floor of 2.34.0. The two published applications were
  `sha256:18013cb7…` (0.0.1) and `sha256:c73e0b60…` (0.0.2), each equal to the
  `sha256` of its projected file.
- `make ci` (`ci-static ci-gates ci-engine ci-dashboard ci-integration-kubernetes
  ci-e2e-envtest ci-oci`), `make test-browser` (219 passed), `make artifact-drift`,
  `make release-dry-run`.
- `go test ./tests/release/...`, `go test ./tests/acceptance/scenario/...`,
  `go test -race ./tests/acceptance/scenario/...`,
  `go test -race ./tests/acceptance/kind/productready`.
- `shellcheck tests/acceptance/local/compose-demo.sh` clean.
  `shellcheck tests/acceptance/kind/lib.sh` reports one pre-existing SC2153 info
  at an untouched line. `shellcheck release/orchestrator/dry-run.sh` reports three
  pre-existing SC2015 infos at untouched lines 105, 192 and 286.
- `bash scripts/check-section-sign.sh` — zero U+00A7 in authored files, and none
  in any commit message in this range.
- `gofmt`, `go vet` and the repository lint over every touched file;
  `git diff --check` clean.

### GitHub Actions at `4399c63a`

CI run `32082237095`, conclusion success on attempt 1, all 21 jobs green:

`changes` `95547324521`, `ci-static` `95547364444`, `ci-gates` `95547364520`,
`ci-engine` `95547364557`, `ci-dashboard` `95547364458`, `ci-oci` `95547364544`,
`ci-integration-kubernetes` `95547364375`, `ci-e2e-envtest` `95547364406`,
`ci-e2e-compose` `95547364433`, `release-dry-run` `95547364447`,
`artifact-drift` `95547364410`, `release-version-test` `95547364470`,
`operator-build` `95547364460`, `dashboard-e2e` `95547364495`, all six Kind
shards — `observation` `95547364509`, `upgrade` `95547364513`, `evidence`
`95547364521`, `dashboard` `95547364526`, `operational-graph` `95547364570`,
`reconcile` `95547364622` — and `required` `95550039392`.

Also green at this SHA: Security `32082236970`, Docs check `32082237014`,
Pacto Contract CI `32082236975`, Repowise `32082236990` and Validate PR title
`32082237045`. `Rebuild dashboard UI` and `Auto-merge Dependabot PRs` are
skipped, as they are on every push here.

**The one CI failure, disclosed and fixed rather than rerun.** The first push of
this repair, `c25b6125`, went red at `ci-e2e-kind (evidence)` `95544102722` in run
`32081089200` — "port-forward to svc/pacto-evidence never answered". It was not
rerun. The diagnostics show the replacement pod one second old and
`ContainerCreating` at the restart-recovery step, and `kubectl port-forward` to a
Service with no ready endpoint does not wait for one, it exits immediately. The
shared `pf` helper spawned once and broke out of its own sixty-second retry loop
the moment that child died, so the loop could not survive the single case it
exists for. `4399c63a` makes `pf` respawn inside the window. Proved by stubbing a
`kubectl` that exits for its first three spawns and then serves: the old `pf`
fails on the first, the new one connects on the fourth. The shard is green at
`4399c63a`. This is a pre-existing flake in the Kind harness, unrelated to the
Compose and netfilter work, fixed because it was this branch's CI that was red.

### CodeQL at `4399c63a`

Zero delta. The analyses succeed and report exactly what they reported at the
starting SHA: go 9, python 1, javascript-typescript 0, actions 0 — identical
counts at `7c69ba43` (`20:09:31Z`) and at `4399c63a` (`23:56:10Z`). The open-alert
inventory on the PR ref is the same nine inherited alerts: `#59`-`#62`
(`pkg/oci/cache.go`), `#40`-`#43` (`internal/app/resolve.go`) and `#38`
(`release/scripts/docs_check.py`), first seen between 2026-07-27 and 2026-08-13.
This range touches none of those three files. The aggregate CodeQL check remains
red for the same reason it was red before this repair: it reports the eight Go
path-injection alerts as "new alerts in code changed by this pull request".

### Review threads at `4399c63a`

199 threads, fully paginated (page one caps at 100 and hides every unresolved
one). 189 resolved, 10 unresolved — unchanged from `76ed7fee` and `430b2159`. All
ten are inherited bot threads: six `github-code-quality` findings on the
committed Mermaid asset bundle and four `github-advanced-security` path-expression
notices on `pkg/oci/cache.go`. No human review thread is unresolved and this
repair opened none.

### Diff hygiene

`git diff --check` clean across `7c69ba43..4399c63a`. The tracked tree is clean;
the only untracked paths are the four pre-existing local tool paths `.claude/`,
`.codex/`, `.mcp.json` and `AGENTS.md`.

One incidental local side effect was reverted rather than committed: a `helm-docs`
pass regenerated `integrations/kubernetes/charts/pacto-dev-gateway/README.md` over
its hand-written content, as it did during the previous repair. `go.work.sum` was
not touched this time. The chart README remains worth a look by whoever owns that
chart: a local `make` run rewrites a tracked, hand-written file.

### Preserved, unchanged

Observed readiness, empty-volume startup, `--pull never`, the Product gate, both
live browser runs, the exact bounded-offline claim, blocker A's digest pinning in
full, the multi-platform index default, the release transaction's ownership of the
production dashboard pin, crash recovery and adoption, conflict detection,
single-unit recovery, the one-publisher gate, clone-free execution,
runtime-generated credentials, restart and replay, two-version independence and
cleanup. No `internal: true`, no sleeps, no public-Internet dependency, no generic
networking framework, no new dependency. ORAS is untouched everywhere it still
has a legitimate consumer, including the release ledger.

### Phase 10B repair verdict

CANDIDATE. Not closed, not self-declared. Phase 10C is NOT started. Phase 11 is
not started. No PR comment was published, no review thread was resolved, no PR
metadata was changed, the PR remains an open draft, and
`PACTO_PR_TARGET_STATE.md` was not altered by this repair.

## 15.5 Independent review at `e6707c7e` — two narrow blockers remain

Reviewed independently on 2026-08-18. Phase 10B is not closed. The native
Compose correction and the two-hook network discriminator are accepted, but two
new concrete counterexamples remain in the release recovery and in the local
acceptance harness. Phase 10C is still NOT started.

### Repository and GitHub state independently verified

- PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE on
  `feat/operational-graph-fleet`; the remote head reviewed is exactly
  `e6707c7e76abe97a2c216e0bd2fc49886e0be01e`. `origin/main` is
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- `7c69ba43` is the merge-base and ancestor of the reviewed head. The range is
  exactly seven linear commits — `65a4c192`, `5a421228`, `45abda33`,
  `19d64373`, `c25b6125`, `4399c63a`, `e6707c7e` — with one parent each and no
  merge, rebase, amend, squash, force-push or rewritten parent. The diff is 23
  files, +2294/-775.
- The final-SHA CI run `32083615532` is success on attempt 1. All 21 jobs are
  green, including `ci-e2e-compose` `95551437902`, `release-dry-run`
  `95551437904`, all six Kind shards and `required` `95554079237`. Security
  `32083615533`, Docs check `32083615470`, Pacto Contract CI `32083615708`,
  Repowise `32083615632` and title validation `32083615567` are success.
- The aggregate CodeQL check remains red. The PR ref still has the same nine
  inherited alerts: `#38`, `#40`-`#43` and `#59`-`#62`; this range touches none
  of their three files. The CodeQL workflow analyses themselves are green.
- Review threads were fully paginated: 199 total, 189 resolved and 10
  unresolved. All ten are inherited bot threads: six on the generated Mermaid
  asset and four on `pkg/oci/cache.go`. No human thread is unresolved.
- Focused independent verification is green: `go test ./tests/release/...`,
  `go test ./tests/acceptance/scenario/...`, the scenario race suite,
  shellcheck of the changed release/acceptance scripts and `git diff --check`.
  The tracked tree is clean; the four pre-existing local agent paths remain
  untracked.

### Accepted work — do not reopen without a new counterexample

- Docker Compose now owns the application artifact end to end on the real path:
  `docker compose publish` creates it and digest-qualified `-f oci://...` reads
  it. The acceptance manifest proves the exact Compose `artifactType`, the one
  Compose layer/media type and byte equality with the projection. ORAS is absent
  from the Compose publication and execution paths; its legitimate ledger and
  generic-artifact consumers remain.
- The application is one projected Compose file with every immutable fixture
  input inline. Execution needs no checkout, extracted directory, local Compose
  file or bind mount. Project identity, pinned multi-platform images, readiness,
  restart/replay, two-version independence and cleanup remain proved.
- The bounded offline claim is now honest. The host-local control traverses
  `INPUT`; the veth control is genuinely forwarded and traverses
  `DOCKER-USER`. Each endpoint is reachable before isolation, removing either
  arm is fatal, the artifact registry is stopped, volumes are empty, service
  images are not pulled, internal traffic and published host access survive,
  and the Product/browser gates prove the isolated fleet.
- Compose `2.34.0` is declared once and both CI paths pin a newer version. The
  Make/CI wiring is intact and no existing required gate was weakened.

### Blocker A — content equality does not prove a Compose artifact

`release/orchestrator/verify-oci.sh:75-79` returns only the digest of a single
layer. Its adoption branch at lines 94-97 never checks the manifest's
`artifactType` or the layer's media type. `publish-oci-unit.sh:48,74-75` repeats
the same weaker post-push assertion. Therefore a tag occupied by a non-Compose
OCI artifact containing the expected bytes is adopted and recorded as a
successful `demo-compose` unit, even though `docker compose -f oci://...` cannot
run it.

This is reproduced, not inferred: a disposable registry received one layer via
ORAS with `artifactType: application/vnd.example.not-compose`, layer media type
`application/octet-stream`, and bytes whose digest was supplied as
`PACTO_EXPECT_CONTENT`. The unmodified `verify-oci.sh` returned `adopt`.
`dry-run.sh` only tests different content at an occupied tag, so it stays green
for this semantic substitution.

Closure requires content recovery to match the whole native Compose identity:
`application/vnd.docker.compose.project`, exactly one
`application/vnd.docker.compose.file+yaml` layer, and that layer's expected
digest. Apply the same tuple to crash-window adoption and to the post-push
assertion. Add an adversarial recovery test where the bytes match exactly but
the artifact type and/or layer media type is foreign; it must be `conflict`,
while a real `docker compose publish` artifact must still be adopted. Keep one
ledger and one shared publisher; do not introduce a generic artifact framework.

### Blocker B — the privileged harness can delete host resources it does not own

The forwarded discriminator is semantically sound, but its installer is unsafe
on a maintainer machine. `tests/acceptance/local/compose-demo.sh:146-148` claims
fixed host names and an address, and `wire_forwarded_route` unconditionally runs
`ip link del pactoout` before creating its pair (`:235-242`). If a developer,
VPN or another test already owns an interface with that name, the acceptance
deletes it and cleanup cannot restore it. The statement that RFC 2544 space
"cannot collide" is also too strong: reserved benchmarking space can be used by
local labs or VPN routes. The new host-local endpoint likewise force-removes a
fixed-name container before claiming it.

This is a direct destructive counterexample, so the full local Compose harness
was deliberately not rerun by the reviewer; exact final-SHA CI and the author's
full local run already establish its functional behavior, while running it does
not answer the ownership defect.

Closure requires every privileged resource to be uniquely owned by this run or
for the harness to fail closed before mutation. It must never delete, replace or
hijack a pre-existing interface, route or endpoint container. Cleanup must
remove only resources this invocation proved it created. Add adversarial proof
with pre-existing sentinel resources: the harness/preflight must refuse or pick
a non-conflicting resource, leave the sentinels byte-for-byte/identity intact,
and still remove its own resources on success and failure. Correct the RFC 2544
documentation claim. Preserve the accepted two independent hooks and do not add
a networking framework or public-Internet dependency.

### Verdict and exact next objective

**Phase 10B remains NARROWLY REOPENED / CANDIDATE.** Repair only blockers A and
B above, append-only from the reviewer state commit that follows this section,
then rerun the native publication/recovery, complete Compose acceptance, release
dry run and required CI at the exact final SHA. Phase 10C and Phase 11 must not
start until that repair is independently reviewed. No PR comment was published,
no review thread was resolved and no PR metadata was changed.

## 15.6 Phase 10B narrow closure repair, third pass — CANDIDATE at `3bd509b1`

The two blockers section 15.5 recorded, and nothing else. Blocker A: content
recovery adopted bytes rather than a Compose application. Blocker B: the
privileged local acceptance destroyed host resources it did not own.

Phase 10B remains a CANDIDATE. A phase is closed by review, not by its author.
Nothing here closes it, sections 15.4 and 15.5 are unchanged,
`PACTO_PR_TARGET_STATE.md` was not touched, no PR comment was published, no
review thread was resolved, no PR metadata was changed, the PR is still a draft,
and Phase 10C and Phase 11 are NOT started.

### Range

Starting point `603b729964a2249ee84e7bd4d3783ee886ec9344`, confirmed as the
remote head of `feat/operational-graph-fleet` before the first change and
confirmed append-only over the reviewed implementation
`e6707c7e76abe97a2c216e0bd2fc49886e0be01e`. Final SHA
`3bd509b1846f37eda6ed9d6aeef64ff22110be53`. `603b7299` is an ancestor of
`3bd509b1`; the range is exactly six linear commits, each parented on the
previous one. 9 files, +565 / -74. No rebase, amend, reset, force-push, squash
or rewrite; `main` was not touched.

| SHA | Subject |
|---|---|
| `88e70b53` | fix(release): the Compose adoption rule is the whole native identity |
| `eab85fa9` | test(release): matching bytes under foreign OCI semantics must be refused |
| `25ae4284` | fix(acceptance): the Compose harness owns only what it created |
| `a257ee61` | build(ci): run the harness ownership selftest before the Compose acceptance |
| `7bb2b91f` | docs: the adoption rule is an identity, and RFC 2544 is only where we look |
| `3bd509b1` | test(acceptance): the selftest's sentinels are veth, not dummy |

| File | What changed |
|---|---|
| `release/orchestrator/verify-oci.sh` | `content()` answers only when the whole native Compose identity holds; the conflict message names the tuple it wanted |
| `release/orchestrator/publish-oci-unit.sh` | its `manifest()`/`content()` helpers are gone; the post-push assertion asks `verify-oci.sh` instead of forming a second opinion |
| `release/orchestrator/dry-run.sh` | ITEM 3b gains four foreign-artifact refusals and an absent-tag pair that proves the assertion runs before the ledger records |
| `tests/release/compose_native_test.go` | `TestTheNativeComposeIdentityHasOneDefinition`: one definition of the Compose media types, in `verify-oci.sh`, and no manifest reader in the adapter |
| `tests/acceptance/local/compose-demo.sh` | per-invocation names, run-time `/30` selection, refuse-before-mutate, ownership-recorded cleanup, and the `selftest` mode that proves all of it |
| `Makefile` | `test-acceptance-compose-selftest` |
| `ci.mk` | `ci-e2e-compose` runs the selftest first |
| `docs/maintainers/releases.md` | the complete native identity behind `adopt`, and where the single definition lives |
| `docs/maintainers/testing.md` | the same tuple and the new dry-run negatives; RFC 2544 corrected; the ownership rule and its selftest |

### RED A, reproduced against the starting implementation

`git show 603b7299:release/orchestrator/verify-oci.sh` run against a disposable
`registry:2` on `127.0.0.1:15099` holding one ORAS-pushed artifact:

```
{"artifactType":"application/vnd.example.not-compose",
 "layers":[{"mediaType":"application/octet-stream",
            "digest":"sha256:b8537829ab292c04755c0653005bdbc4563511d744b46341306accb3b09e8918"}]}

starting  verify-oci.sh <ref> "" "" "" sha256:b8537829...8918  ->  'adopt'     exit 0
repaired  verify-oci.sh <ref> "" "" "" sha256:b8537829...8918  ->  'conflict'  exit 3
```

The repaired conflict says what it wanted: "not a
`application/vnd.docker.compose.project` artifact with one
`application/vnd.docker.compose.file+yaml` layer at sha256:b8537829...8918".

The post-push assertion had the same hole by construction, not by accident:
`603b7299:release/orchestrator/publish-oci-unit.sh:47-48` defined
`content()` as `.layers | length == 1` then `.layers[0].digest`, and line 74
compared only that. Two copies of one rule, the weaker one running last.

### RED B, reproduced against a sentinel this reproduction owned

`603b7299:tests/acceptance/local/compose-demo.sh:146-147` fixes
`VETH_HOST="pactoout"` and `VETH_PEER="pactoin"`, and line 236 — inside
`wire_forwarded_route`, before any create — is `ip link del $VETH_HOST`. A veth
sentinel was created under that name, carrying `203.0.113.1/30`, and the
starting function's body was then run verbatim against a real registry
container:

```
sentinel before: 1429: pactoout inet 203.0.113.1/30 scope global pactoout
sentinel after:  1432: pactoout inet 198.18.53.1/30 scope global pactoout
```

A different ifindex and a different address: the interface was deleted and a new
one put in its place. Nothing on the machine was at risk in the demonstration —
the sentinel was created by the reproduction and removed with it — but on a
developer's machine that is somebody's lab link, and cleanup cannot restore it.
The same shape applied to `docker rm -f "$REG_NAME"` at line 306 and
`docker rm -f "$HL_NAME"` at lines 561 and 616.

### Invariant A: adoption matches an identity, not a byte string

The native Compose identity is `artifactType`
`application/vnd.docker.compose.project`, exactly one layer, layer media type
`application/vnd.docker.compose.file+yaml`, layer digest `PACTO_EXPECT_CONTENT`.
`verify-oci.sh` returns the layer digest only when all four hold, so the
adoption branch and the post-push assertion both fail closed on anything else.

There is one copy of that rule. `publish-oci-unit.sh` now calls
`verify-oci.sh "$1" "" "" "" "$EXPECT_C"` and requires `adopt`, which deleted
its two local helpers — the repair is a smaller file than the defect was. Other
units are untouched: precomputed-digest recovery (bundles, chart) and
`revision`/`version` provenance recovery (images) run exactly as before, and no
artifact-policy abstraction was introduced.

`make release-dry-run` proves the six required cases for real against the
staging registry, all in ITEM 3b:

```
real Compose publication adopted from its native compose layer sha256:1a4b05c2... (no re-push)
different bytes under the native types refused (fail closed)
same bytes, one layer, foreign artifactType      -> conflict
same bytes, native artifactType, foreign layer   -> conflict
native types, the compose layer plus an extra    -> conflict
native artifactType with no compose layer at all -> conflict
absent -> published by Compose, native identity asserted, recorded complete
absent -> a push of the same bytes as a non-Compose artifact records nothing
```

Each negative asserts the verdict `conflict`, not merely a non-zero exit, so a
crash or a missing tool cannot pass for a refusal. ORAS builds the deliberately
foreign fixtures and appears nowhere on the real publication or execution path.

### Invariant B: the harness owns only what it created

Every host resource carries a per-invocation `RUN_ID` (six hex digits from
`/dev/urandom`): the registry, the host-local endpoint, the netfilter image and
both veth ends. Interface names are checked against Linux's 15-character limit
where the naming scheme is, not two minutes into a run. Nothing is force-claimed:
`run_owned`, `build_netfilter_image` and `wire_forwarded_route` call
`refuse_existing` first and stop before mutating anything.

The forwarded link's `/30` is chosen at run time. 32 candidates are derived from
`RUN_ID` and the first one this host is demonstrably not using is taken — no
route matching it (the default route excluded, since it matches everything and
claims nothing), no route inside it, no local address in it. If none is free the
harness refuses rather than routing over somebody else's prefix, which would
also have invalidated the proof: the probe would be answered by their route.

Cleanup reads only `OWNED_CONTAINERS`, `OWNED_VETH` and `OWNED_IMAGE`, each
recorded after the kernel or the daemon confirmed the create. No fixed name and
no `pacto-demo-*` sweep survives anywhere in the teardown path, so it cannot
reach a resource this invocation did not make. It runs on success and on failure.

`compose-demo.sh selftest` (`make test-acceptance-compose-selftest`, first in
`ci-e2e-compose`) is the durable proof. Its sentinels are veth pairs rather than
dummies, because veth is the link type Docker itself runs on and is therefore
loaded wherever this can run at all. Reviewer items 1 to 6:

```
S1/S4: pactoout-1a15d1 was refused, not deleted, and no filter was touched
S2: pacto-demo-hostlocal-endpoint-1a15d1 was refused, not force-removed
S3: 198.18.87.69/30 stayed with its owner; the link moved to 198.18.87.73
S5: it removed its own container, interface and image (...-fe138a, pactoout-fe138a, pacto-demo-netfilter:fe138a)
S6: it removed its own container, interface and image (...-39040e, pactoout-39040e, pacto-demo-netfilter:39040e)
S5/S6: a harness-shaped container neither run created was left alone
```

S1 compares the sentinel's ifindex and addresses across the refusal, so a
delete-and-recreate cannot pass for "untouched", and it compares `DOCKER-USER`
and `INPUT` verbatim across the same refusal, so a harness that had begun
mutating before it refused shows up whatever rules the machine already had. S6
induces the failure with everything installed and watches the real EXIT trap.
The decoy is named the way this harness names its own containers and is created
by neither child, so a cleanup that ever went back to a fixed name or a prefix
sweep would take it.

Reviewer items 7, 8 and 9 are the acceptance itself, run in full locally after
the repair (`make ci-e2e-compose`, exit 0, 36 passes):

```
both routes out of the demo's network are open, and each is a different netfilter path
FORWARD/DOCKER-USER refused: the forwarded route is closed, the host-local one is still open
INPUT refused: the host-local route is closed too
a startup dependency redirected over either route fails the stack
the documented browser journeys work on the published demo
empty volumes, no image pulls, no registry, no route out — the same fleet
```

### Mutation evidence

Each mutation was applied to the repaired tree, observed to fail the gate it was
supposed to fail, and reverted.

| Mutation | Result |
|---|---|
| a second copy of the media-type tuple added to `publish-oci-unit.sh` | `TestTheNativeComposeIdentityHasOneDefinition` FAILS |
| `publish-oci-unit.sh` reads the manifest itself with `crane manifest` | same test FAILS |
| `dry-run.sh` stops building the deliberately foreign negative | same test FAILS |
| `ip link del $VETH_HOST` restored before the create | selftest FAILS: "the veth host end: the harness went ahead instead of refusing" |
| `docker rm -f` restored before claiming the endpoint name | selftest FAILS: "the host-local endpoint: the harness went ahead instead of refusing" |
| the fixed `198.18.53.x` pair restored in place of the picker | selftest FAILS: "the harness chose 198.18.53.1 again with that /30 already in use" |
| cleanup stops removing what it recorded | selftest FAILS: "S5: the container ... survived its own cleanup" |

One earlier draft of the Go gate did NOT bite and is recorded because it is the
reason the gate is shaped the way it is: it asserted a regex
`verify-oci\.sh.*EXPECT_C`, which an unrelated pre-existing line in
`publish-oci-unit.sh` already satisfied, so replacing the whole assertion with
`is_expected_content() { true; }` still passed. The gate now forbids the adapter
from reading a manifest at all — a structural property, not a spelling.

### Local verification, all green

`git diff --check`; `gofmt -l tests/ release/` empty; `shellcheck` clean on
`compose-demo.sh`, `verify-oci.sh`, `publish-oci-unit.sh` and `dry-run.sh` apart
from three pre-existing SC2015 infos in `dry-run.sh` (lines 105, 192, 336, none
of them in this range); `make check-section`; `go test ./tests/release/...`;
`go test ./tests/architecture/...`; `go test -count=1 ./tests/acceptance/scenario/...`
and the same suite with `-race`; `make test-acceptance-compose-selftest`;
`make release-dry-run`; `make artifact-drift`; `make ci`; `make test-browser`
(219 passed); `make ci-e2e-compose` in full, browser journeys included.

### GitHub at the exact final SHA `3bd509b1`

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE, head
`3bd509b1846f37eda6ed9d6aeef64ff22110be53`. Run `32107259844` (CI) is success on
attempt 1; all 20 jobs are green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `95619109742` | success |
| `ci-static` | `95619153003` | success |
| `ci-gates` | `95619152996` | success |
| `ci-engine` | `95619152887` | success |
| `ci-dashboard` | `95619152915` | success |
| `dashboard-e2e` | `95619152918` | success |
| `ci-integration-kubernetes` | `95619152940` | success |
| `ci-e2e-envtest` | `95619152916` | success |
| `ci-e2e-compose` | `95619152979` | success |
| `ci-oci` | `95619152983` | success |
| `operator-build` | `95619153009` | success |
| `artifact-drift` | `95619153010` | success |
| `release-version-test` | `95619153002` | success |
| `release-dry-run` | `95619153059` | success |
| `ci-e2e-kind (dashboard)` | `95619153028` | success |
| `ci-e2e-kind (upgrade)` | `95619153029` | success |
| `ci-e2e-kind (reconcile)` | `95619153064` | success |
| `ci-e2e-kind (evidence)` | `95619153082` | success |
| `ci-e2e-kind (operational-graph)` | `95619153083` | success |
| `ci-e2e-kind (observation)` | `95619153088` | success |
| `required` | `95622611419` | success |

Other workflows at the same SHA: Security `32107260136`, Docs check
`32107259848`, Pacto Contract CI `32107259846`, Repowise `32107259803`, Validate
PR title `32107259845`, Code Quality `32107256569` and PR review `32107256236`
are all success; Rebuild dashboard UI `32107259866` and Auto-merge Dependabot
`32107259809` skipped. Of the 40 rollup entries, 37 are success, two are skipped
(`auto-merge`, `build`) and one is the aggregate `CodeQL` check.

The `ci-e2e-compose` job log confirms the ownership selftest runs on the Linux
runner as well as locally, with veth sentinels and a run-time `/30`
(`198.18.87.69/30` there), before the acceptance starts.

**CodeQL delta: none.** The PR ref has the same nine inherited open alerts as
the reviewed baseline — `#38` (`release/scripts/docs_check.py`), `#40`-`#43`
(`internal/app/resolve.go`) and `#59`-`#62` (`pkg/oci/cache.go`). This range
touches none of those three files. The three Analyze jobs are success; the
aggregate check is red for the inherited alerts exactly as section 15.5 recorded.

**Review threads, fully paginated** (two pages of 100): 199 total, 189 resolved,
10 unresolved — byte-identical to the reviewed baseline. All ten are inherited
bot threads: six from `github-code-quality` on the generated Mermaid asset
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four from
`github-advanced-security` on `pkg/oci/cache.go`. No human thread is unresolved,
none was resolved here and no comment was published.

### Hygiene and incidental side effects

The tracked tree is clean at `3bd509b1`; the four pre-existing local agent paths
(`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`) remain untracked and
unmodified. Two files were touched by local tooling and reverted rather than
committed, both unrelated to this repair: `go.work.sum` gained module-graph
hashes while running `go test`, and `integrations/kubernetes/charts/pacto-dev-gateway/README.md`
was rewritten by the `helm-docs` step of `make ci` (that chart's committed README
is hand-written; its drift gate covers the operator chart, and `make ci` passed).
Every reproduction and mutation resource created on the host was removed and its
absence verified: no `pacto-red*` or `pacto-demo-*` container, no
`pacto-demo-netfilter`/`pacto-red*` image, no `pactoout`/`pactoin`/`pdsen`/`pdpeer`
interface remains.

### Verdict

**Phase 10B remains CANDIDATE.** Both blockers from section 15.5 are repaired
and proved, the accepted work from that section is intact, and the next step is
an independent review of `3bd509b1` — not a closure by its author. Phase 10C and
Phase 11 were not started.

## 15.7 Independent review at `3bd509b1` — ownership blocker remains

Reviewed independently on 2026-08-18. Blocker A from section 15.5 is CLOSED.
The functional network discriminator remains accepted. Blocker B is not closed:
the new ownership selftest can itself destroy a project it did not create, a
failed container start escapes the ownership ledger, and the veth failure path
still contains a check-then-delete race. Phase 10B therefore remains narrowly
reopened; Phase 10C and Phase 11 are still NOT started.

### Repository and GitHub state independently verified

- PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE on
  `feat/operational-graph-fleet`. The implementation reviewed is exactly
  `3bd509b1846f37eda6ed9d6aeef64ff22110be53`; the branch head is its
  documentation-only candidate record
  `907db2bbf3a5868d2db75031d60994ced887a063`. `origin/main` remains
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- `603b7299` is an ancestor of `907db2bb`. The range is exactly the seven linear
  commits recorded in 15.6 plus its ledger commit, every parent is the previous
  SHA, and there is no merge, rebase, amend, reset, squash, force-push or
  rewritten parent.
- Exact implementation-SHA CI `32107259844` is success on attempt 1. All 21
  jobs are green, including `ci-e2e-compose` `95619152979`,
  `release-dry-run` `95619153059`, all six Kind shards and `required`
  `95622611419`. Security `32107260136`, Docs check `32107259848`, Pacto
  Contract CI `32107259846`, Repowise `32107259803` and title validation
  `32107259845` are success. The same suite is green at ledger head `907db2bb`
  in CI run `32109523745`, including `required` `95629033781`.
- CodeQL has zero delta: the PR ref still reports the same nine inherited alerts
  `#38`, `#40`-`#43` and `#59`-`#62`, at the same three untouched files. The
  analyses succeed and the aggregate check remains red for those inherited
  alerts.
- Review threads were fully paginated: 199 total, 189 resolved and 10
  unresolved. All ten are inherited bot threads: six `github-code-quality` on
  the generated Mermaid asset and four `github-advanced-security` on
  `pkg/oci/cache.go`. No human thread is unresolved.
- Focused independent checks pass: release tests, scenario tests and race suite,
  shellcheck apart from the three disclosed inherited SC2015 infos, and
  `git diff --check`. The tracked tree remains clean; only the four pre-existing
  local agent paths are untracked.

### Blocker A accepted and closed

Content recovery now matches the complete native Compose identity in the one
load-bearing place: exact `application/vnd.docker.compose.project` artifact
type, exactly one `application/vnd.docker.compose.file+yaml` layer, and its
expected digest. `publish-oci-unit.sh` asks `verify-oci.sh` for the same verdict
before recording an absent-tag publication, so there is no second weaker tuple.
The release dry run constructs real foreign artifacts with the same bytes and
refuses wrong artifact type, wrong layer media type, extra layer, no compose
layer and wrong bytes, while a real native publication still adopts. Other OCI
units and the real Compose publication/execution path are unchanged. Do not
reopen this without a new concrete counterexample.

### Accepted portion of blocker B

The random per-invocation names, interface-length guard and runtime `/30`
selection remove the old fixed `pactoout`/`pactoin` and fixed-address collision.
The picker detects an occupied local prefix and steps over it. The full
acceptance and final-SHA CI still prove the two independent hooks: removing
`DOCKER-USER` kills the forwarded control while host-local remains reachable;
`INPUT` then kills the host-local control; the redirected dependency fails over
both routes; the isolated Product/browser journeys remain green. This semantic
network proof is accepted and must not be redesigned.

### Remaining blocker B1 — `selftest` destroys an unrelated Compose project

`cleanup()` is installed for every mode and unconditionally calls
`down_quiet "$PROJ1"` and `down_quiet "$PROJ2"`. The new `selftest` and
`own-and-exit` modes exit before the normal stage-0 ownership preflight, so their
EXIT trap runs `docker compose -p pacto-demo down -v --remove-orphans` against
whatever a user already has under the documented project name.

This was reproduced with a reviewer-owned sentinel, not inferred. Before the
selftest, project `pacto-demo` had container
`f0a959aaccdb039335e22a061217fb80c871616c8f5c7abcb63e97bc7dffe661`
and volume `pacto-demo_sentinel-data`. The unmodified
`compose-demo.sh selftest` exited 0 and printed `SELFTEST OK`; afterwards both
the container and volume were gone. The selftest therefore reports that it
leaves foreign resources alone while its generic trap has just deleted them.

Closure requires helper modes to have no project-cleanup authority. More
generally, project teardown may run only for a normal/browser invocation that
has established ownership of those project names. Add a sentinel Compose
project/volume case that survives `selftest` and both `own-and-exit` success and
failure children unchanged.

### Remaining blocker B2 — `run_owned` records too late

`run_owned` executes `docker run -d --name ...` and appends the name to
`OWNED_CONTAINERS` only after that command returns success. Docker can create a
container and then fail to start it. Reproduced with a reviewer-owned port
collision: the second `docker run` exited 125 but left its named container in
state `created`. In `run_owned`, `set -e` exits before the append, so the EXIT
trap has no record and leaks the container.

Closure requires create and start to be separate: atomically create, record the
returned container identity immediately, then start. A start failure must be
removed by the real EXIT trap. Track immutable container IDs, or verify the ID
before deletion, so later name reuse can never make cleanup delete a different
container. The early stage-11 removal of the host-local endpoint must either
retain ownership (for example by stopping it) or retire its ownership record at
the same time; cleanup must not later delete a new container that reused the
freed name. Add an induced start-failure case and a name-reuse sentinel.

### Remaining blocker B3 — the veth failure cleanup can delete a racing owner

`wire_forwarded_route` checks both interface names, then performs route selection,
then attempts `ip link add`. If another process creates `VETH_HOST` in that
window, `ip link add` fails because the foreign interface exists. The outer
failure handler nevertheless runs `ip link del $VETH_HOST`, deleting the racing
owner. The comment that the names "were free a moment ago" is precisely the
TOCTOU gap; past freedom is not ownership.

Closure requires the privileged create sequence to arm cleanup only after this
invocation's `ip link add` has succeeded. Failure before successful creation
must never delete by candidate name. Failure after creation must remove the
pair this invocation created. Add a deterministic adversarial hook/test for a
sentinel appearing after preflight but before create, plus a later-step failure
that proves the invocation still removes its own partially configured pair.

### Verdict and exact next objective

**Phase 10B remains NARROWLY REOPENED / CANDIDATE.** Blocker A is CLOSED.
Blocker B is narrowed to the three ownership/cleanup defects above. Repair them
append-only from the reviewer state commit that follows this section, preserving
the accepted native Compose identity and the two-hook network semantics. Rerun
the ownership selftest, full Compose/browser acceptance, release dry run and
exact-final-SHA CI. Phase 10C and Phase 11 must not start until that narrow
repair is independently reviewed. No PR comment was published, no review thread
was resolved and no PR metadata was changed.

## 15.8 Phase 10B ownership repair, fourth pass — CANDIDATE at `b63ea2ed`

The three ownership defects section 15.7 narrowed blocker B to, and nothing
else. Blocker A stays closed and untouched; the two-hook network semantics, the
per-invocation naming, the run-time `/30` selection and the isolated Product and
browser journeys are unchanged.

Phase 10B remains a CANDIDATE. A phase is closed by review, not by its author.
Sections 15.4 through 15.7 are unchanged, `PACTO_PR_TARGET_STATE.md` was not
touched, no PR comment was published, no review thread was resolved, no PR
metadata was changed, the PR is still a draft, and Phase 10C and Phase 11 are
NOT started.

### Range

Starting point `d48b296e922ee02f1e1846e8178d2ee3b00a5d8e`, the branch head
carrying the section 15.7 review record, confirmed append-only over the reviewed
implementation `3bd509b1846f37eda6ed9d6aeef64ff22110be53`. Final SHA
`b63ea2edbdce768e629161fc70570fb0bd9184b3`. Both `3bd509b1` and `d48b296e` are
ancestors of `b63ea2ed`; the range is exactly five linear commits, each parented
on the previous one. 2 files, +283 / -47. No rebase, amend, reset, force-push,
squash or rewrite. `origin/main` is still
`83f2e66d5cd4fab56099991d39e64fc11f107b3d` and was not touched.

| SHA | Subject |
|---|---|
| `6f7ee084` | fix(acceptance): only a run that claimed a project may tear it down |
| `affa4b63` | fix(acceptance): own containers by id, recorded before they start |
| `9e5b5792` | fix(acceptance): the veth pair is ours from `ip link add`, not before |
| `2d7228ab` | test(acceptance): the selftest exercises the paths it claims to |
| `b63ea2ed` | docs: what the harness owns, and from which instant |

| File | What changed |
|---|---|
| `tests/acceptance/local/compose-demo.sh` | `claim_projects` and a project-teardown list the helper modes never fill; `run_owned` split into create / record / start and recording immutable ids; the veth create split out so ownership begins at `ip link add`; five new selftest cases and the sentinels they need |
| `docs/maintainers/testing.md` | the three instants "this invocation created it" is not what it looks like, where project-teardown authority comes from, and what the eleven selftest cases cover |

One correction to the record, which append-only discipline forbids fixing in
place: the body of commit `6f7ee084` names the second documented project
`pacto-demo-consumer`. It is `pacto-demo-next`, as `compose-demo.sh:72` and every
test output below say. The change itself reads both names from `PROJ1`/`PROJ2`
and hard-codes neither.

### RED B1, reproduced against the starting implementation

A Compose project planted under the documented name `pacto-demo` by a reviewer
who is not the harness, then `d48b296e`'s harness run in its `selftest` mode —
the mode that touches no project at all:

```
planted   container 312c7f8cefb17cfb8962f589e1d13dad179e4e8bedd443b298ac58bec32652fc
          volume    pacto-demo_sentinel-data

starting  bash compose-demo.sh selftest  ->  "SELFTEST OK ... gives all of it back"   exit 0

after     containers: []
          volumes:    []
```

The mode that owns nothing reported that it gives everything back while its
generic EXIT trap ran `docker compose -p pacto-demo down -v --remove-orphans`
over somebody else's project.

### RED B2, reproduced against resources this reproduction owned

Two separate defects, both reproduced with the starting `run_owned` and
`release_owned` lifted verbatim out of `d48b296e`.

A container created and then refused a start, on a host port this reproduction
had already published:

```
docker: Error response from daemon: ... Bind for 0.0.0.0:15141 failed: port is already allocated
TRAP sees OWNED_CONTAINERS=[ pacto-red-occupier-13089]
pacto-red-clasher-13089   created   52487dd1783c
```

`docker run -d` returned non-zero, `set -e` left before the append, and the
container the daemon really did create was left behind in state `created` with
no record of it anywhere.

A name freed by an owned container and taken by somebody else, which is exactly
the shape of stage 11 removing the host-local endpoint early:

```
owned:   pacto-red-reuse-13116 id=6f357b1c09b7bc830c9f5011e1ee2eb8195f0e96fcd75befe7a4f12878ba97ca
foreign: pacto-red-reuse-13116 id=3cfe5125a5f691208ea8add7c3198f4fe14e64e23364abfd0c21c6cfa7f67f3b
RESULT: the foreign container was DELETED by cleanup
```

### RED B3, reproduced against a sentinel this reproduction owned

`wire_forwarded_route` lifted verbatim out of `d48b296e`, with its interface
preflight emptied to stand in for a name taken in the window the preflight
opens, and a veth sentinel created under that name first:

```
sentinel before: 1910: pactoout-abc123    inet 203.0.113.1/30 scope global pactoout-abc123
RTNETLINK answers: File exists
wire_forwarded_route failed, as expected (rc=1)
sentinel after:  GONE
```

`ip link add` failed *because* the interface belonged to somebody else, and the
failure handler deleted it on the way out. Nothing on the machine was at risk in
the demonstration — the sentinel was created by the reproduction — but on a
developer's machine that is the interface that won the race.

### The ownership design, in three sentences

**A name is a lease; an id is a token.** Containers are recorded by their
immutable 64-hex id and never by name, so a name given up by an owned container
carries no authority over whoever takes it next, and cleanup holds no fixed name
and sweeps no prefix. The one place the run gives a listener up early — stage 11
shutting the host-local endpoint down — now stops the container instead of
removing it, so the name stays held until the trap removes it by id.

**Created and started are two events, and the record goes between them.**
`run_owned` calls `docker create`, appends the returned id to
`OWNED_CONTAINERS`, and only then calls `docker start`. A start that fails is a
container this invocation is still responsible for, and the real EXIT trap
removes it.

**Ownership of a host resource begins at the atomic operation that establishes
it.** For the veth pair that is `ip link add`, so the create is on its own line
and `OWNED_VETH` is assigned immediately after it: a failure before it deletes
nothing, and a failure in the rest of the wiring gives the pair back through the
same `unwire_forwarded_route` the trap uses. For the two documented Compose
project names it is stage 0's preflight, so `claim_projects` fills
`OWNED_PROJECTS` only after finding both names holding no container and no
volume — and the helper modes, which exit long before stage 0, leave that list
empty and tear nothing down. The interface preflight is kept for its diagnostic;
nothing depends on its answer still being true, which it cannot promise.

No framework was added. `claim_projects` is the stage-0 loop that was already
there, given a name and one extra volume check; the rest is a smaller
`wire_forwarded_route` failure branch, one extra variable and two `docker`
commands where there had been one.

### Selftest: eleven cases, and what each is for

`bash tests/acceptance/local/compose-demo.sh selftest`, run by
`make test-acceptance-compose-selftest` ahead of the Compose acceptance.

| Case | What it proves |
|---|---|
| S1/S4 | an interface already holding a candidate name is refused, not deleted, and no netfilter rule moves on the way out |
| S2 | a container already holding the endpoint name is refused, not force-removed or restarted |
| S3 | an occupied `/30` in the benchmarking range is stepped over, not hijacked |
| S8 | **new** — with the preflight emptied, an interface that appears before `ip link add` survives the create that loses the race to it |
| S9 | **new** — a failure *after* `ip link add` succeeded removes that pair and leaves the sentinel interface beside it alone |
| S5 | a child run that succeeds has its container, interface and image taken back by the real EXIT trap |
| S6 | a child run that fails after creating everything does the same |
| S7 | **new** — a child whose container the daemon creates and refuses to start has it removed by the real EXIT trap, by id |
| S5/S6/S7 | a harness-shaped container none of those three children created is still there afterwards |
| S10 | **new** — a container removed early frees its name, a stranger takes it, and cleanup removes the id it recorded and leaves the stranger alone |
| S11 | **new** — two projects planted under the documented names still hold every container, network and volume they did |

The planted projects are assembled out of plain `docker volume create`,
`docker network create` and `docker create` carrying Compose's labels rather
than out of a compose file. That is the accurate shape, not a shortcut:
`down -v --remove-orphans` reads labels and nothing else. It also keeps the
demo's own journey digest-only, which
`TestTheDemoIsExecutedFromTheArtifactByDigest` requires — an earlier draft using
`-f -` failed that test. `sentinel_project` asks Compose whether it can see the
plant (`docker compose -p NAME ps -aq` non-empty) and fails loudly if not, so
label drift in a future Compose cannot silently void the case.

The selftest owns what it plants: it calls `claim_projects` on both names first,
which is the same gate stage 0 uses and the reason a real demo makes the
selftest refuse rather than destroy. The children under test claim nothing.
Reviewer leftovers go through the harness's own ownership list rather than a
`docker rm -f` at the end — the earlier draft's manual removal of the decoy
container would have deleted the very evidence the case above it depends on.

Two seams, both the smallest available and both confined to the subshell
`refuses_to_touch` already runs its argument in, where a `fail` ends the attempt
and not the selftest: `raced_wire` redefines `refuse_existing` to a no-op, and
`bad_address_wire` redefines `pick_forwarded_net` to an address the kernel will
not take. No fault-injection framework, no production code path aware of being
tested.

### GREEN B1, from both directions

With a foreign `pacto-demo` project planted (container
`4fe2fa3896b0396145d3c75a60bcf895aeef643bc42e3fb4a7caf1826ca0264a`, volume
`pacto-demo_sentinel-data`), against the repaired harness:

```
own-and-exit  exit 0   OWNED image=... container=de35f71a9260... veth=pactoout-fa2d0e
              the project is byte-identical afterwards

selftest      exit 1   FAIL: project pacto-demo already has containers;
                             run `docker compose -p pacto-demo down -v` first
              the project is byte-identical afterwards
```

The helper mode does its work and leaves; the mode that would have claimed the
name refuses instead of taking it. Both are the opposite of the RED above.

### Mutation evidence

Each mutation was applied to a copy of the repaired harness, run, observed to
fail at exactly the case meant to catch it, and discarded. The repository tree
was never mutated.

| Mutation | Selftest result |
|---|---|
| M1 — the trap tears both documented projects down for every mode again | `FAIL: S11: something tore down pacto-demo: '74304fcba192... \|5f17c6449fa1 \|pacto-demo_sentinel-data ' -> '\|\|'` |
| M2 — one `docker run -d`, recorded after it returns | `FAIL: S7: the child exited 127, expected 1`, and the run leaked `pacto-demo-stuck-b99198` in state `created` — the defect itself |
| M3 — create and start split, but the *name* recorded | `FAIL: S10: cleanup deleted d7d19abd290a..., which had done nothing but take the freed name pacto-demo-hostlocal-endpoint-7fb9ab` |
| M4 — `OWNED_VETH` assigned before `ip link add` is asked for it | `FAIL: S8 the raced veth create: pactoout-7c1c59 changed: '2002: ... inet 203.0.113.1/30 ...' -> 'GONE'` |
| M5 — a failure after the pair exists no longer gives it back | `FAIL: S9: pactoout-419886 outlived the step that failed after it was created` |

Every mutation was reverted by discarding the copy; `git status --short` before
and after shows only the four pre-existing untracked agent paths, and
`tests/acceptance/local/compose-demo.sh` is byte-identical to `b63ea2ed`.

A deliberate limit, recorded rather than hidden: a selftest that FAILS stops at
the failing case, so that case's sentinel interface stays on the machine for
inspection (observed under M5, removed by hand afterwards). Sentinel names carry
`RUN_ID`, so a leftover cannot collide with a later run; the passing path removes
everything it made.

### Local verification, all green

`git diff --check`; `shellcheck -s bash` clean on `compose-demo.sh` at every one
of the five commits and `bash -n` at each; `make check-section`;
`go test ./tests/release/...` at each of the five commits;
`go test -count=1 ./tests/acceptance/scenario/...` and the same suite with
`-race`; `make test-acceptance-compose-selftest` on the committed tree, all
eleven cases; `make ci-e2e-compose` in full with the browser journeys; `make
release-dry-run` (`RELEASE-DRY-RUN OK`); `make artifact-drift`; `make ci`;
`make test-browser` (219 passed). No `shfmt` is installed on this machine and no
Make target or workflow gates shell formatting; the file's inline shellcheck
directives are unchanged.

`make ci-e2e-compose`, `make release-dry-run`, `make artifact-drift`, `make ci`
and `make test-browser` were run against a working tree whose only difference
from `b63ea2ed` is a two-line comment above `OWNED_ID`, reworded afterwards. The
selftest, the release tests and both scenario suites were re-run on the
committed bytes, and CI below runs everything at the exact final SHA.

### GitHub at the exact final SHA `b63ea2ed`

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE, head
`b63ea2edbdce768e629161fc70570fb0bd9184b3`. Run `32124980402` (CI) is success on
attempt 1; all 21 jobs are green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `95673412259` | success |
| `ci-static` | `95673463686` | success |
| `ci-e2e-envtest` | `95673463734` | success |
| `dashboard-e2e` | `95673463737` | success |
| `ci-integration-kubernetes` | `95673463760` | success |
| `ci-engine` | `95673463776` | success |
| `ci-dashboard` | `95673463777` | success |
| `ci-oci` | `95673463831` | success |
| `ci-e2e-compose` | `95673463838` | success |
| `operator-build` | `95673463839` | success |
| `artifact-drift` | `95673463844` | success |
| `release-dry-run` | `95673463898` | success |
| `ci-e2e-kind (reconcile)` | `95673463905` | success |
| `ci-gates` | `95673463915` | success |
| `ci-e2e-kind (evidence)` | `95673463929` | success |
| `release-version-test` | `95673463946` | success |
| `ci-e2e-kind (operational-graph)` | `95673463962` | success |
| `ci-e2e-kind (upgrade)` | `95673463970` | success |
| `ci-e2e-kind (dashboard)` | `95673463990` | success |
| `ci-e2e-kind (observation)` | `95673464078` | success |
| `required` | `95677494718` | success |

Other workflows at the same SHA: Security `32124980469` (govulncheck, Trivy and
the PR security summary all success), Docs check `32124980444`, Pacto Contract CI
`32124980480`, Repowise `32124980373`, Validate PR title `32124980514`, Code
Quality `32124977194` and PR review `32124977102` are all success; Rebuild
dashboard UI `32124980550` and Auto-merge Dependabot `32124980579` skipped. Of
the 40 rollup entries, 37 are success, two are skipped (`auto-merge`, `build`)
and one is the aggregate `CodeQL` check — the same shape sections 15.6 and 15.7
recorded.

The `ci-e2e-compose` job log confirms all eleven cases run on the Linux runner
and not only on the author's machine, with a run-time `/30` (`198.18.52.165/30`
there):

```
PASS: S8: the create lost the race for pactoout-56cd29 and the winner kept its interface
PASS: S9: the half-wired pair was taken back down and the interface beside it was not
PASS: S7: a container created but refused a start was still removed by the real EXIT trap (1db509d6604f...)
PASS: S10: cleanup removed the container it recorded and left the name's next holder alone
PASS: S11: pacto-demo and pacto-demo-next kept every container, network and volume they had
SELFTEST OK
```

**CodeQL delta: none.** The PR ref reports the same nine inherited open alerts as
the starting SHA — `#38` (`release/scripts/docs_check.py`), `#40`-`#43`
(`internal/app/resolve.go`) and `#59`-`#62` (`pkg/oci/cache.go`). This range
touches neither of those three files nor any Go, Python, JavaScript or workflow
file at all. Four analyses uploaded at `b63ea2ed` (`go`,
`javascript-typescript`, `python`, `actions`); the aggregate check remains red
for the inherited alerts exactly as sections 15.5 through 15.7 recorded.

**Review threads, fully paginated** (two pages of 100): 199 total, 189 resolved,
10 unresolved — byte-identical to the reviewed baseline. All ten are inherited
bot threads: six from `github-code-quality` on the generated Mermaid asset
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four from
`github-advanced-security` on `pkg/oci/cache.go`. No human thread is unresolved,
none was resolved here and no comment was published.

### Hygiene and incidental side effects

The tracked tree is clean at `b63ea2ed`; the four pre-existing local agent paths
(`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`) remain untracked and
unmodified, and no unrelated user change was disturbed. `git diff --check` is
silent across the range. During `make ci`,
`integrations/kubernetes/charts/pacto-dev-gateway/README.md` was rewritten by the
`helm-docs` step and `go.work.sum` gained module-graph hashes; both are unrelated
to this repair, both were reverted rather than committed, and `make ci` passed.

Every reproduction, mutation and sentinel resource created on the host was
removed and its absence verified: no `pacto-demo-*`, `pacto-red-*` or sentinel
container, no `pacto-demo-*` network or volume, no `pacto-demo-netfilter` image,
and no `pactoout`/`pactoin`/`pdsen`/`pdpeer`/`pdred` interface remains. The
machine's container count is the same before and after. The demo images
(`pacto-demo:acceptance`, `pacto-demo-local:dev`, the six
`ghcr.io/trianalab/pacto-demo/*` service images) predate this session and are
what the acceptance is documented to leave behind.

### Verdict

**Phase 10B remains CANDIDATE.** All three defects section 15.7 named are
repaired, each proved RED against `d48b296e` first and each guarded by a
selftest case proved to bite by mutation. Blocker A stays closed and untouched,
the accepted network semantics are unchanged, and the next step is an
independent review of `b63ea2ed` — not a closure by its author. Phase 10C and
Phase 11 were not started.

## 15.9 Independent review at `b63ea2ed` — network-only project ownership blocker remains

Reviewed independently on 2026-08-18. Blocker A remains CLOSED. The container
identity/start repair and the veth create/failure repair close B2 and B3 from
section 15.7. The helper-mode part of B1 is also repaired: helper modes no
longer receive unconditional authority to tear down the two documented Compose
projects. B1 is not fully closed, however, because the new project claim omits
one of the resource classes that `docker compose down` destroys. Phase 10B
therefore remains narrowly reopened; Phase 10C and Phase 11 are still NOT
started.

### Repository and GitHub state independently verified

- PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE on
  `feat/operational-graph-fleet`. The implementation independently reviewed is
  exactly `b63ea2edbdce768e629161fc70570fb0bd9184b3`; the branch head carrying its
  author-written candidate record is
  `ba2718553a7c1971241db82cfd14632393eeb7f0`. `origin/main` remains
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- `d48b296e` is an ancestor of `ba271855`. The range is exactly the six linear
  append-only commits recorded in section 15.8, every parent is the previous
  SHA, and the merge-base with `origin/main` is unchanged. No merge, rebase,
  amend, reset, squash, force-push or rewritten parent was found.
- Exact implementation-SHA CI `32124980402` is success on attempt 1. All 21
  jobs are green, including `ci-e2e-compose` `95673463838`,
  `release-dry-run` `95673463898`, all six Kind shards and `required`
  `95677494718`. Security `32124980469`, Docs check `32124980444`, Pacto
  Contract CI `32124980480`, Repowise `32124980373`, title validation
  `32124980514`, Code Quality `32124977194` and PR review `32124977102` are
  success. Ledger-head CI `32126921988` is also success on attempt 1, including
  `required` `95683189423`.
- This range changes only the Compose acceptance shell, its maintainer
  documentation and the candidate ledger. It introduces no CodeQL delta. The
  inherited aggregate CodeQL check remains red; no file carrying its existing
  findings is touched here.
- Review threads were fully paginated: 199 total, 189 resolved and 10
  unresolved. All ten are inherited bot threads: six `github-code-quality`
  threads on the generated Mermaid asset and four `github-advanced-security`
  threads on `pkg/oci/cache.go`. No human thread is unresolved.
- Independent focused checks pass on the committed bytes: the complete
  eleven-case ownership selftest, `go test ./tests/release/...`, the scenario
  suite, `bash -n`, `shellcheck`, and `git diff --check`. The tracked tree is
  clean; only the four pre-existing local agent paths remain untracked.

### B2 accepted and closed

`run_owned` now separates creation from start, records the immutable container
ID between those events, and cleanup addresses only those IDs. An induced start
failure is removed by the real EXIT trap, and the name-reuse case leaves the
replacement container intact. Stage 11 stops rather than removes its
host-local endpoint, so the normal path also retains the name until ID-based
cleanup. The independent selftest run passed S7 and S10. Do not reopen this
without a new concrete counterexample.

### B3 accepted and closed

The veth create is a separate `ip link add`; `OWNED_VETH` is armed only after
that atomic operation succeeds. A losing create therefore deletes nothing, and
a later configuration failure removes the pair this invocation created. The
independent selftest run passed S8 and S9, including unchanged sentinel-link and
netfilter state. Do not reopen the accepted two-hook network semantics or this
failure boundary without a new concrete counterexample.

### Remaining B1 — a network-only Compose project is declared free and destroyed

`claim_projects` checks `docker compose -p NAME ps -aq` and labelled volumes,
then puts the project name in `OWNED_PROJECTS`. It never checks labelled Compose
networks. But `down_quiet` later executes
`docker compose -p NAME down -v --remove-orphans`, and that command removes a
project network even when the project has no container and no volume. The
preflight can therefore grant teardown authority over a resource this
invocation did not create.

This was reproduced independently against a reviewer-created, labelled network
and the unmodified committed selftest:

```text
network: pacto-demo_default
before:  0f80365bab432055878cb987e2abf52c27fd2db3db8efdc0334765fc83fb9080
selftest exit: 1
error: Error response from daemon: network with name pacto-demo_default already exists
after:   GONE
```

At entry, the project had no container and no volume, so `claim_projects`
accepted it and armed `OWNED_PROJECTS`. `sentinel_project` then failed when it
met the pre-existing network. The real EXIT trap ran `down_quiet` and deleted
that network. The reviewer created the network solely for this reproduction and
removed all remaining test resources afterwards.

S11 does not cover this counterexample. It plants a container, network and
volume together only *after* the outer selftest has already claimed the empty
project names; it then proves that child helper modes do not tear that mixed
project down. A separate handoff reproduction with a pre-existing mixed project
also stops at the container check. Neither path asks whether a network by itself
prevents the stage-0 claim. The maintainer statement that stage 0 found the
names "holding nothing" is consequently stronger than the implementation.

### Verdict and exact next objective

**Phase 10B remains NARROWLY REOPENED / CANDIDATE.** Blocker A, B2, B3, the
native Compose identity, the two-hook network semantics and helper-mode cleanup
are accepted and must not be redesigned. Repair only B1's remaining project
claim: no normal, browser or selftest invocation may acquire project-teardown
authority while any Compose resource that `down -v --remove-orphans` can remove
already exists under either documented project name. At minimum this includes
containers, named volumes and networks.

Add a deterministic adversarial case with a network-only project under each
documented name. It must enter through the real claim/EXIT path, fail closed,
preserve the exact network ID, leave `OWNED_PROJECTS` unarmed and prove the
current code bites before the repair. Keep the existing eleven ownership cases,
full Compose/browser acceptance, release dry run and exact-final-SHA CI green.
Update the maintainer wording to match the actual complete claim. Phase 10C and
Phase 11 must not start until this one-resource-class repair is independently
reviewed. No PR comment was published, no review thread was resolved and no PR
metadata was changed.

## 15.10 Phase 10B network-ownership repair — CANDIDATE at `bec4cb33`

The one remaining blocker section 15.9 named — B1's incomplete project claim —
and nothing else. Blocker A, B2, B3, the native Compose identity and digest
pinning, the two independent `DOCKER-USER` and `INPUT` controls, the run-time
`/30`, per-invocation resource names, container ownership by id, the
create/record/start split, veth ownership from `ip link add`, helper modes
without project-teardown authority and the eleven existing selftest cases are
untouched.

Phase 10B remains a CANDIDATE. A phase is closed by review, not by its author.
Sections 15.1 through 15.9 are unchanged, `PACTO_PR_TARGET_STATE.md` was not
touched, no PR comment was published, no review thread was resolved, no PR
metadata was changed, the PR is still a draft, and Phase 10C and Phase 11 are
NOT started.

### Range

Starting point `c86260301f5912158708f1a900a6a6b75ac078a1`, the branch head
carrying the section 15.9 review record. Final SHA
`bec4cb33c5ffe09acfbeb97a66f6efed3ce3dc9e`. `c8626030` is an ancestor of
`bec4cb33`; the range is exactly three linear commits, each parented on the
previous one. 2 files, +151 / -20. No rebase, amend, reset, force-push, squash
or rewrite. `origin/main` is still
`83f2e66d5cd4fab56099991d39e64fc11f107b3d` and was not touched.

| SHA | Subject |
|---|---|
| `812b74fb` | fix(acceptance): a network alone is a project the harness must not claim |
| `c4532a62` | test(acceptance): a network-only project under either documented name |
| `bec4cb33` | docs: "holding nothing" names every class the teardown removes |

| File | What changed |
|---|---|
| `tests/acceptance/local/compose-demo.sh` | `claim_projects` reads labelled networks as well as containers and volumes; `sentinel_network`, the `claim-and-exit` child mode, `refuses_network_only`, selftest cases S12 and S13, and an unrelated bystander project the whole selftest watches |
| `docs/maintainers/testing.md` | what "holding nothing" means, why the network is the class that can be there alone, that the authority is armed in one assignment, and the two new cases in the selftest paragraph |

### RED, reproduced against the starting implementation

A Compose-labelled network planted under a documented project name by something
that is not the harness, then `c8626030`'s harness in `selftest` mode. Under the
first documented name:

```text
network: pacto-demo_default
before:  f5c072eb1cb5d43e7af6818997146217593fa4eedede49092018ec3907076a34
claim sees: containers=[] volumes=[]
selftest exit: 1
error: Error response from daemon: network with name pacto-demo_default already exists
after:   GONE
```

And with the FIRST name free and only the second occupied, which is the ordering
section 15.9 asked for:

```text
network: pacto-demo-next_default   (pacto-demo itself is free)
before:  3f30bfeba976d00418dc6b5986c9a465897206e01919765d431fc124399ebf0d
selftest exit: 1
error: Error response from daemon: network with name pacto-demo-next_default already exists
after:   GONE
```

Both are the same mechanism the reviewer recorded. The claim read no container
and no volume, called the project empty, filled `OWNED_PROJECTS`, and the real
EXIT trap ran `docker compose -p NAME down -v --remove-orphans` over a network
this invocation never made. Both networks were created solely for these
reproductions; nothing else on the machine was at risk, and the leftovers were
verified gone afterwards.

### The repair

One more read in the loop that was already there, with the same label filter
`proj_state` already uses for networks:

```sh
[ -z "$(docker network ls -q --filter "label=com.docker.compose.project=$p")" ] ||
	fail "project $p already has networks; run \`docker compose -p $p down -v\` first"
```

`down -v --remove-orphans` removes three classes — containers, named volumes and
networks — and the claim now reads all three. The network is the one that can be
there alone: `up` creates it before anything else, `down` without `-v` leaves it,
and a stack whose containers have all been removed by hand still has one, so a
project that is nothing but a network is the exact state the two-class claim
mistook for empty.

The single `OWNED_PROJECTS="$*"` after the loop was already there and is now
load-bearing rather than incidental: arming is all or nothing, so a refusal on
the second name cannot leave the first one claimed. It is commented as such.

No framework, no new helper on the production path, no behaviour change to any
mode that already refused.

### Selftest: two new cases, thirteen in total

| Case | What it proves |
|---|---|
| S12 | **new** — the first documented name holding nothing but a Compose network is refused by a real invocation, which keeps that exact network and arms nothing |
| S13 | **new** — the same for the second name with the first FREE, which is where a claim that armed itself as it went would already have taken `pacto-demo` |
| S11 | extended — the unrelated bystander project is byte-identical at the end of the whole run, alongside the two planted demo projects |

S1 through S11 are unchanged and all still run.

Three pieces carry the two cases, each the smallest thing that would do:

- `sentinel_network PROJECT` plants a real `docker network create` carrying
  `com.docker.compose.project` and `com.docker.compose.network`, then asserts the
  project holds no container and no volume. That is the vacuity guard: without
  it, the case could pass on the container or the volume arm and prove nothing
  about the network. `sentinel_project` now takes its network from this function,
  so the two sentinels cannot drift apart on what a Compose network looks like.
- `claim-and-exit` is a child mode beside the existing `own-and-exit`: it runs
  the same `claim_projects` stage 0 runs, prints `CLAIMED <projects>` if that
  succeeded, and leaves through the same `cleanup` EXIT trap. What is under test
  is therefore the production claim and the production teardown, not a re-reading
  of either, and a refusal is observable without standing a demo up.
- `refuses_network_only LABEL PROJECT` spawns that child and requires: a non-zero
  exit; NO `CLAIMED` line at all — not armed, rather than not fully armed; the
  exact network id still present; and `proj_state` byte-identical for both
  documented names AND for an unrelated bystander project
  (`pacto-demo-bystander-$RUN_ID`, planted with a container, a network and a
  volume). It ends by running `down_quiet` on the sentinel this selftest does
  own and requiring the network to be gone — which demonstrates rather than
  asserts that the refusal saved something `down -v --remove-orphans` really
  would have taken.

The ordering inside `run_selftest` is what makes the cases discriminating: the
outer selftest claims both documented names and the bystander first, plants the
bystander, runs S12 and S13 while the documented names can still be made to hold
a network and nothing else, and only then plants the mixed sentinel projects S11
watches. S11 could not have caught this: it plants its container, network and
volume only after the claim has already happened.

### GREEN

```text
== S12. a documented name holding only a Compose network is not claimed ==
  PASS: S12: pacto-demo held nothing but a network, and the invocation refused it and kept it
== S13. the second documented name is read even when the first is free ==
  PASS: S13: pacto-demo-next was refused with pacto-demo free, and nothing was armed on the way
...
  PASS: S11: pacto-demo and pacto-demo-next kept every container, network and volume they had
  PASS: S11: the unrelated pacto-demo-bystander-6d1dc6 kept everything it had too
SELFTEST OK: the harness owns what it creates, refuses what it does not, and gives all of it back
```

### Mutation evidence

Each mutation was applied to the repaired file, run, observed to fail at exactly
the case meant to catch it, then discarded and the file verified byte-identical
(`sha256 faf169ccac063973a2b714d7e16483e54e4b98ae997b2b067ac347de89af890e` before
and after both).

| Mutation | Selftest result |
|---|---|
| M6 — the network check deleted from `claim_projects` | child printed `CLAIMED pacto-demo pacto-demo-next`; `FAIL: S12 the first documented name: the invocation claimed pacto-demo instead of refusing it` |
| M7 — `OWNED_PROJECTS="$*"` moved INSIDE the loop, so the claim arms as it goes | S12 still passes; `FAIL: S13 the second documented name: the network b92bf3a4eaaff8404137078aff300f7168f97ca7bdbe28cb369e900f315398f8, which was all pacto-demo-next had, did not survive the refusal` (exit 1) |

M7 is the "bypasses the check" shape and is why S13 exists as a separate case
from S12: with the arming moved inside the loop, the network check still runs and
still refuses, but the first name is already claimed by then and the trap tears
the second one's network down on the way out. S12, where the FIRST name is the
occupied one, cannot see that.

### Local verification, all green

`bash -n` and `shellcheck -s bash` on `compose-demo.sh` at each of the three
commits; `git diff --check`; `make check-section` (zero U+00A7);
`make test-acceptance-compose-selftest` on the committed tree, all thirteen cases;
`go test ./tests/release/...`; `go test -race ./tests/acceptance/... -count=1`
(the scenario suite and the three Kind gates, race detector on);
`make ci-e2e-compose` in full including the live Playwright journeys against the
published demo (7 passed twice, `clone-free Compose demo acceptance PASSED`);
`make release-dry-run` (`K8S-MODULE-STANDALONE OK`, `RELEASE-DRY-RUN OK`);
`make artifact-drift` (`artifact-drift: OK`); `make ci` (exit 0);
`make test-browser` (219 passed). No `shfmt` is installed and no Make target or
workflow gates shell formatting; the file's inline shellcheck directives are
unchanged.

### GitHub at the exact final SHA `bec4cb33`

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE, head
`bec4cb33c5ffe09acfbeb97a66f6efed3ce3dc9e`, base `main`. Run `32134424578` (CI)
is success on attempt 1; all 21 jobs are green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `95702376046` | success |
| `ci-oci` | `95702439116` | success |
| `ci-dashboard` | `95702439161` | success |
| `ci-engine` | `95702439167` | success |
| `dashboard-e2e` | `95702439180` | success |
| `ci-static` | `95702439195` | success |
| `ci-integration-kubernetes` | `95702439239` | success |
| `operator-build` | `95702439272` | success |
| `ci-e2e-envtest` | `95702439332` | success |
| `ci-e2e-compose` | `95702439336` | success |
| `release-dry-run` | `95702439352` | success |
| `ci-e2e-kind (observation)` | `95702439363` | success |
| `artifact-drift` | `95702439371` | success |
| `release-version-test` | `95702439424` | success |
| `ci-gates` | `95702439425` | success |
| `ci-e2e-kind (reconcile)` | `95702439443` | success |
| `ci-e2e-kind (evidence)` | `95702439516` | success |
| `ci-e2e-kind (operational-graph)` | `95702439529` | success |
| `ci-e2e-kind (upgrade)` | `95702439593` | success |
| `ci-e2e-kind (dashboard)` | `95702439608` | success |
| `required` | `95706665808` | success |

Other workflows at the same SHA: Security `32134424527` (govulncheck
`95702375923`, Trivy `95702375958`, PR security summary `95702932858`, all
success), Docs check `32134424552`, Pacto Contract CI `32134424516`, Repowise
`32134424525`, Validate PR title `32134424600`, Code Quality `32134419825` and
PR review `32134419938` are all success; Rebuild dashboard UI `32134424569` and
Auto-merge Dependabot `32134424524` skipped. Of the 37 rollup entries, 34 are
success, two are skipped (`auto-merge`, `build`) and one is the aggregate
`CodeQL` check — the same shape sections 15.6 through 15.8 recorded.

The `ci-e2e-compose` job log confirms both new cases run on the Linux runner and
not only on the author's machine:

```text
PASS: S12: pacto-demo held nothing but a network, and the invocation refused it and kept it
PASS: S13: pacto-demo-next was refused with pacto-demo free, and nothing was armed on the way
PASS: S3: 198.18.118.205/30 stayed with its owner; the link moved to 198.18.118.209
SELFTEST OK: the harness owns what it creates, refuses what it does not, and gives all of it back
```

**CodeQL delta: none.** The PR ref reports the same nine inherited open alerts as
the starting SHA — `#38` (`release/scripts/docs_check.py:197`), `#40`-`#43`
(`internal/app/resolve.go`) and `#59`-`#62` (`pkg/oci/cache.go`). This range
touches one shell file and one Markdown file and no Go, Python, JavaScript or
workflow file at all. All four analyses at `bec4cb33` (`actions`, `go`,
`javascript-typescript`, `python`) are success; the aggregate check remains red
for the inherited alerts exactly as sections 15.5 through 15.9 recorded.

**Review threads, fully paginated** (two pages of 100): 199 total, 189 resolved,
10 unresolved — byte-identical to the reviewed baseline. All ten are inherited
bot threads: six from `github-code-quality` on the generated Mermaid asset
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four from
`github-advanced-security` on `pkg/oci/cache.go`. No human thread is unresolved,
none was resolved here and no comment was published.

### Hygiene and incidental side effects

The tracked tree is clean at `bec4cb33`; the four pre-existing local agent paths
(`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`) remain untracked and
unmodified. `git diff --check` is silent across the range. During `make ci`,
`integrations/kubernetes/charts/pacto-dev-gateway/README.md` was again rewritten
by the `helm-docs` step; it is unrelated to this repair, it was reverted rather
than committed, and `make ci` passed. `go.work.sum` was not touched this time.

Every network, project, container, volume, image and interface created by the two
reproductions, the two mutation runs and the passing runs was removed and its
absence verified: no `pacto-demo*` network or volume, no `pacto-demo-*` or
`pacto-demo-bystander-*` container, no `pacto-demo-netfilter` image. The one
`pacto*` container left on the machine is `pacto-evidence-control-plane`, a Kind
node created on 2026-08-17, before this session, and untouched by it. The demo
images the Compose acceptance is documented to leave behind are unchanged.

### Verdict

**Phase 10B remains CANDIDATE.** The one blocker section 15.9 left open is
repaired: no normal, browser or selftest invocation can now acquire
project-teardown authority while a container, a named volume OR a network exists
under either documented project name, and arming is all or nothing. It was proved
RED against `c8626030` under both names first, is guarded by two selftest cases
each proved to bite by its own mutation, and the maintainer wording now matches
the implementation. Everything section 15.9 accepted stays accepted and
untouched. The next step is an independent review of `bec4cb33` — not a closure
by its author. Phase 10C and Phase 11 were not started.

## 15.11 Independent review at `bec4cb33` — Phase 10B ACCEPTED and CLOSED

Reviewed independently on 2026-08-18. The network-only project counterexample
from section 15.9 is closed. The accepted native Compose artifact identity,
digest pins, clone-free execution, two-hook network boundary, canonical
scenario projections, browser/Product journeys and earlier ownership repairs
remain intact. No Phase 10B blocker remains.

### Repository and GitHub state independently verified

- PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE on
  `feat/operational-graph-fleet`. The implementation independently reviewed is
  exactly `bec4cb33c5ffe09acfbeb97a66f6efed3ce3dc9e`; the branch head carrying
  its author-written candidate record is
  `7ae07d237c16ab6bb00b3f97562e8499a571dab3`. `origin/main` and the merge-base
  remain `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- `c8626030` is an ancestor of `7ae07d23`. The range is exactly the four linear
  append-only commits in section 15.10, each parented on the previous one. No
  merge, rebase, amend, reset, squash, force-push or rewritten parent was found.
  `PACTO_PR_TARGET_STATE.md` is untouched.
- Exact implementation-SHA CI `32134424578` is success on attempt 1. All 21
  jobs are green, including `ci-e2e-compose` `95702439336`,
  `release-dry-run` `95702439352`, all six Kind shards and `required`
  `95706665808`. Security `32134424527`, Docs check `32134424552`, Pacto
  Contract CI `32134424516`, Repowise `32134424525`, title validation
  `32134424600`, Code Quality `32134419825` and PR review `32134419938` are
  success. Ledger-head CI `32136215491` is also success on attempt 1, including
  `ci-e2e-compose` `95708089263`, `release-dry-run` `95708089240` and
  `required` `95711973791`.
- The range changes only the Compose acceptance shell, its maintainer
  documentation and the candidate ledger. It introduces no CodeQL delta. The
  aggregate CodeQL check remains red for the inherited findings recorded in
  sections 15.5 through 15.10; no file carrying one is touched here.
- Review threads were fully paginated: 199 total, 189 resolved and 10
  unresolved. All ten are inherited bot threads: six `github-code-quality`
  threads on the generated Mermaid asset and four `github-advanced-security`
  threads on `pkg/oci/cache.go`. No human thread is unresolved.

### The final counterexample is closed

`claim_projects` now reads all three resource classes its cleanup can remove:
containers, labelled named volumes and labelled networks. It fills
`OWNED_PROJECTS` once, after every requested name has passed, so a refusal on
the second name arms no partial teardown authority.

The reviewer independently planted a Compose-labelled network under
`pacto-demo-next` while `pacto-demo` was free and ran the real
`claim-and-exit` path. The exact result was:

```text
exit:   1
before: 01fadddea6938afbccd7ac308fe1c87cd5a1883b01018296dbdc179a187dd4a9
after:  01fadddea6938afbccd7ac308fe1c87cd5a1883b01018296dbdc179a187dd4a9
error:  project pacto-demo-next already has networks
```

That is the same ordering and resource shape that section 15.9 proved the old
code destroyed. It now fails closed and preserves identity. The reviewer-owned
network was removed afterwards.

S12 and S13 are permanent, non-vacuous guards: they require network-only state,
exercise the real child claim and EXIT trap under both documented names, compare
exact project/network state, and keep an unrelated bystander project unchanged.
Removing the network read makes S12 fail; moving the all-project assignment into
the loop makes S13 fail. The production repair is the one additional native
Docker label query in the existing loop; no abstraction, dependency or parallel
ownership system was added.

### Independent local verification and hygiene

On the committed bytes: `bash -n`, `shellcheck`, `git diff --check`,
`make check-section`, the complete thirteen-case ownership selftest,
`go test ./tests/release/...` and
`go test -race ./tests/acceptance/... -count=1 -timeout 180s` all pass. The
exact implementation CI log also contains PASS for S12 and S13 followed by
`SELFTEST OK`, proving the cases execute on the Linux runner.

The tracked tree is clean. Only the four pre-existing local agent paths remain
untracked and untouched. No reviewer or test resource remains: no documented
project container, labelled network or volume, netfilter image, or selftest
interface is present. No PR comment was published, no thread was resolved and
no PR metadata was changed.

### Phase 10B verdict

**Phase 10B is ACCEPTED and CLOSED at `bec4cb33`, with candidate ledger head
`7ae07d23`.** Reopening it now requires a new concrete correctness, security,
data-loss or user-journey counterexample. The following are frozen accepted
boundaries and must not be redesigned incidentally by later phases:

- one canonical declarative scenario with Helm and Compose projections;
- native Docker Compose OCI publication and digest-addressed execution;
- immutable Compose and service-image identity;
- clone-free Product and live-browser acceptance;
- the explicit offline/restart/upgrade semantics and two independent netfilter
  controls;
- per-invocation host-resource ownership and complete project preflight over
  containers, named volumes and networks.

The nine inherited code-scanning findings and ten inherited bot review threads
are carried to Phase 14 unless a later phase touches their code for its own
in-scope reason. They are not Phase 10C scope by default.

## 16. Phase 10C record — OPENED

Phase 10B is CLOSED above. This section opens Phase 10C and is appended before
any Phase 10C implementation commit. `PACTO_PR_TARGET_STATE.md` is untouched.
TARGET "Phase 10C — OCI-native evidence referrers and stateless ingestion" and
`docs/superpowers/specs/2026-08-17-oci-native-evidence-referrers-design.md` are
the binding approved architecture.

### Commission and exact next objective

Completely replace the custom evidence persistence engine with OCI 1.1 native
Referrers. Every accepted record becomes a versioned Pacto evidence artifact
attached to the exact immutable contract digest in
`EvidenceSet.ContractRef`. The configured contract registry becomes the only
durable evidence store, and the Evidence Server remains the stateless security,
evaluation, replay and bounded-DTO boundary.

The implementation starts append-only from the reviewer state commit following
this section. It must follow the approved design end to end, including:

- exact, non-empty configured contract-digest subjects and bounded discovery;
- native Referrers pagination with `oras.land/oras-go/v2`, no legacy tag
  fallback and no registry catalog scan;
- strict versioned artifact codec, subject binding and size/count limits;
- Ed25519 verification, authorization, evaluation and producer-global replay
  protection reconstructed from all configured subjects;
- serialized single-writer scan/publish/read-after-write semantics;
- honest ready/partial/unavailable health and the v2 `/targets` DTO;
- removal of `pkg/evidencestore`, bucket recovery/repair, bucket configuration,
  evidence PVCs, storage inspection and `gocloud.dev`;
- stateless CLI, operator, Helm, Kind and Compose projections derived from the
  canonical scenario;
- registry-volume restart persistence, ORAS interoperability and the full
  deletion/documentation/acceptance criteria in design section 13.

There is no hybrid period, dual write, bucket fallback, migration engine,
mutable subject, image subject, multi-writer emulation or direct Dashboard
registry access. Existing PVCs are not deleted automatically. Phase 10C ends as
a CANDIDATE and is closed only by a later independent review.

### Current phase map

- Phases 1 through 10B: ACCEPTED and CLOSED.
- Phase 10C: ACTIVE / OPENED; implementation not started at this state commit.
- Phases 11 through 14: NOT STARTED.

Phase 11 must not start while Phase 10C is only a candidate. The PR remains an
open draft, and the append-only/no-history-rewrite and independent-review
protocol continues unchanged.

## 16.1 Phase 10C implementation — CANDIDATE at `a79aa9fa`

The commission of section 16, implemented end to end: the custom evidence
persistence engine is gone and every accepted evidence record is an OCI 1.1
referrer of the exact immutable contract digest in `EvidenceSet.ContractRef`.
The configured contract registry is the only durable evidence store. The
Evidence Server keeps the boundary work — strict decoding, Ed25519
verification, producer/subject/contract authorization, contract evaluation,
producer-global replay protection, latest-target projection, bounded findings
and targets, and the Dashboard/Fleet/CLI DTOs — and keeps nothing else.

There is no hybrid period, no dual write, no bucket fallback and no migrator.

Phase 10C remains a CANDIDATE. A phase is closed by review, not by its author.
Sections 1 through 16 are unchanged, `PACTO_PR_TARGET_STATE.md` was not touched,
no PR comment was published, no review thread was resolved, no PR metadata was
changed, the PR is still an open draft, and Phase 11 is NOT started.

### Range

Starting point `9d2e1a43d3004f2230ed425fa04f0dec2765e050`, the branch head
carrying the section 16 commission. Final SHA
`a79aa9faa522e85a19e75688673e5e00de492c90`. `9d2e1a43` is an ancestor of
`a79aa9fa`; the range is exactly fifteen linear commits, each parented on the
previous one. 83 files, +4906 / -3946. No rebase, amend, reset, force-push,
squash or rewrite. `origin/main` is still
`83f2e66d5cd4fab56099991d39e64fc11f107b3d` and was not touched.

| SHA | Subject |
|---|---|
| `2dbf535a` | feat(evidence): an evidence record is an OCI referrer of its contract revision |
| `dd3960a3` | feat(evidence): read every evidence referrer of a contract revision, or fail |
| `02d65c85` | feat(evidence): one serialized commit, over history re-read from the registry |
| `ac5ed605` | feat(evidence)!: the Evidence Server is the boundary, the registry is the store |
| `80471a2b` | feat(operator)!: evidence subjects replace the evidence bucket |
| `7c6bb31f` | test(acceptance): the store is a plain OCI registry, and ORAS says so |
| `5788b345` | docs: evidence lives in the registry, and nothing claims otherwise |
| `7134530d` | style(operator): preallocate the evidence serve argv |
| `d1c7779e` | test(chart): the no-subject gate reads the same on either schema validator |
| `8d94a350` | fix(acceptance): read either shape `oras discover --format json` emits |
| `5a84abb4` | fix(acceptance): the cluster projection also names the evidence subjects |
| `b2749076` | fix(scenario): the seed cannot wait for readiness only the seed can cause |
| `48b5f73f` | fix(acceptance): the recreate keeps the completed-dependency declaration |
| `12e367e4` | build(k8s): lint with its own analysis cache |
| `a79aa9fa` | fix(acceptance): tell ORAS the absolute attach paths are deliberate |

New packages and files:

| Path | What it is |
|---|---|
| `internal/evidenceoci/artifact.go` | the versioned artifact codec: deterministic manifest and payload construction, strict validation of layer count, media types, schema, size and subject identity |
| `internal/evidenceoci/subject.go` | exact `oci://...@sha256:<64 lowercase hex>` subject parsing and the non-empty deduplicated configured set |
| `internal/evidenceoci/repository.go` | the `oras-go/v2` transport, Pacto's existing credential policy, and the refusal to enable the legacy referrers-tag capability |
| `internal/evidenceoci/scan.go` | complete Referrers pagination, classification by the manifest's own artifactType, partial-read accounting |
| `internal/evidenceoci/store.go` | the serialized logical commit: scan every subject, reconstruct duplicate-id and max-sequence state, publish, confirm read-after-write |
| `internal/testutil/ocireferrers.go` | the protocol-faithful paginated Referrers server the unit tests drive |
| `docs/evidence-oci-storage.md` | the OCI evidence operations page that replaced the storage-and-recovery page |

Modified, by area: `internal/app` and `internal/cli` (repeatable
`--subject`, the removal of the bucket flags and of `pacto evidence inspect`),
`pkg/evidenceingest`, `internal/fleetsrc`, `pkg/oci`, `cmd/pacto`,
`integrations/kubernetes/internal/evidence` and its chart templates, values,
schema, unit tests and generated reference, `tests/acceptance/scenario`,
`tests/acceptance/kind`, `tests/acceptance/local`, `tests/architecture`, the
protocol/security/operational-graph/Compose/upgrade documentation, `go.mod` and
`go.sum`.

### Architecture, mapped to the approved design

1. **Exact subjects, non-empty and deduplicated.** `subject.go` accepts only
   `oci://<repo>@sha256:<64 lowercase hex>`; the configured set is required to
   be non-empty and is deduplicated on load. Helm's `evidence.registry.subjects`
   carries `minItems: 1` in the values schema.
2. **Everything else refused.** Mutable tags, local paths, image subjects,
   unconfigured subjects, inferred repositories and catalog-wide discovery are
   all rejected at parse or lookup time. There is no code path that discovers a
   subject the operator did not configure.
3. **`oras.land/oras-go/v2`** is the only registry client: artifact
   construction and publication, transport, native Referrers enumeration and
   complete pagination.
4. **Exact media types.** `artifactType` and the single payload layer are both
   `application/vnd.pacto.evidence.record.v1+json`; the payload is the strict
   `pacto.dev/evidence-record/v1` schema, decoded with `pkg/strictjson` so an
   unknown field or trailing JSON is an error.
5. **One layer, one exact subject.** `ValidateManifest` requires exactly one
   payload layer and a subject descriptor whose digest equals the configured
   subject's, and rejects wrong schemas, wrong media types, oversized payloads,
   mismatched contract identity and malformed Pacto artifacts.
6. **One credential policy.** The transport reuses Pacto's existing OCI
   credential resolution. No second login command, credential file or
   registry-auth model was added.
7. **The serialized logical commit.** Verify and evaluate; take the
   process-wide mutex; enumerate every Referrers page of every configured
   subject; reconstruct duplicate-id and maximum-sequence state; reject replay
   globally across subjects; publish one untagged artifact; confirm it is
   discoverable through native Referrers; only then return the accepted
   response. The ingestion request and accepted response are unchanged.
8. **Fail closed.** Incomplete, unsupported, malformed or unavailable discovery
   fails the write and marks the read partial or unavailable. The legacy
   referrers-tag fallback is never enabled.
9. **One writer.** One replica and the `Recreate` strategy, asserted in the
   chart unit tests and in the Kind acceptance. No distributed locking was
   invented.
10. **Honest health.** The v2 `/targets` DTO carries `ready`, `partial` and
    `unavailable`, with `subjects`, `failedSubjects` and `invalidArtifacts`
    counts. Unavailable evidence is never rendered as authoritative emptiness.
11. **The Dashboard stays behind the HTTP DTO.** It was given no registry
    access and no credentials.

Helm exposes exactly `evidence.registry.subjects` (required) and
`evidence.registry.credentialsSecret` (optional, an existing Docker config
Secret, mounted read-only, never rendered into generated output). Compose lost
the Evidence Server data volume and `--bucket-url` and kept the registry and
trust/key volumes.

### Deletion audit

Deleted outright: `pkg/evidencestore/{store,blob,drivers}.go` and their tests,
`internal/app/evidencestore.go` and its test, `cmd/pacto/blob_drivers.go`,
`docs/evidence-storage-recovery.md`. With them went the bucket append logs and
materialized manifests, the recovery/repair/cold-start machinery, the
`file://`/S3/GCS/Azure configuration, the bucket URL and prefix flags,
`pacto evidence inspect`, the evidence PVC reconciliation, its RBAC, values and
mounts, the Evidence Server data volumes and gocloud temporary volumes, and the
provider-specific blob-driver registration.

Audited on the final tree: `gocloud.dev` appears in neither `go.mod`, `go.sum`,
the Kubernetes module's `go.mod`/`go.sum`, nor `go list -m all`. A tree-wide
search for `gocloud.dev`, `evidencestore`, `bucket-url` and `evidence inspect`
returns three classes of hit and nothing else: a NEGATIVE assertion in
`tests/acceptance/scenario/compose_test.go` that the projected command must not
contain `--bucket-url`; the historical status log in
`docs/architecture/dashboard-redesign-plan.md`, whose storage ADR is annotated
as superseded rather than rewritten; and the design spec and this ledger, which
are records rather than claims about the tree.

No migrator ships. No existing PVC is deleted automatically; manual retirement
after any desired backup is documented. A fresh installation creates no
evidence PVC and no bucket resource, asserted by the Kind acceptance and by a
chart unit test.

### RED, GREEN and mutation evidence

Each behavioural commit introduced its tests with the behaviour, red-green-
refactor, inside the repository's existing test taxonomy; the durable proof
kept for this record is the mutation table below rather than verbatim RED
transcripts, which were not preserved as files for the earliest commits.

Permanent coverage now includes: deterministic artifact and payload
construction; exact immutable subject binding; strict JSON, schema, layer,
media-type and size validation; complete Referrers pagination against a
protocol-faithful paginated server and against a real registry with a small
page size; unrelated artifact types ignored; malformed Pacto artifacts making
reads partial and writes fail closed; producer-global replay across different
contract subjects; duplicate envelope ids; in-process concurrent ingestion
serialization; latest-target ordering and the existing bounds; every existing
credential source with no secret leakage; native Referrers absence failing
closed; non-empty exact subject configuration; read-after-write
discoverability; ORAS discovery of Pacto-published artifacts; Pacto ingestion of
an equivalent ORAS-published artifact; restart with no local evidence
directory; registry unavailability distinguished from an authoritative empty
state; a Helm installation producing no evidence PVC; Helm and Compose
projecting the same canonical subjects; and unchanged Product facts and browser
journeys.

Six mutations were applied, observed to fail a permanent test, and reverted:

| Mutation | What bit |
|---|---|
| M1 remove the exact-subject comparison | `TestValidateManifest_Rejects/foreign_subject`: `error = <nil>, want ErrInvalidArtifact` |
| M2 stop after the first Referrers page | `TestScanSubject_FollowsEveryPage`: `read 2 records across pages, want 7` |
| M3 relax producer sequence ordering | three independent failures: `TestStore_ReplayIsProducerGlobalAcrossSubjects`, `TestStore_ConcurrentCommitsSerialize` (`6 of 6 concurrent commits on the same sequence were accepted, want exactly 1`) and `TestStore_ReplayProtectionSpansEveryPage` |
| M4 enable the legacy referrers-tag fallback | `TestScanSubject_FailsClosedWithoutReferrersAPI`: `scanning a registry with no Referrers API succeeded; it must fail closed` |
| M5 reintroduce an evidence PVC | `TestDeploymentAC_MountsNothingWritable`: `expected only the trust mount, got map[data:/var/lib/pacto/evidence trust:/etc/pacto/trust]` |
| M6 trust only the listing descriptor's artifactType | `TestScanSubject_ClassifiesByManifestNotListingDescriptor`: `read 0 records, want 2 even though the listing mislabels them` |

M5 is the one that found a second defect. Its Go half bit immediately, but the
chart half passed 11 of 11: helm-unittest 1.0.3 populates the raw document only
for non-YAML renders, so `notMatchRegexRaw` on a Deployment or a ClusterRole
asserts nothing. Probed directly, an assertion that a ClusterRole does NOT match
`ClusterRole` also passed. Five call sites were vacuous — two written in this
phase and three pre-existing in `deployment_test.yaml` — and all five were
replaced with forms proven to bite (`notMatchRegex` over an args or env path,
`notExists` over a JSONPath selecting the resource). With the repaired gate the
M5 chart mutation fails `asserts[0] notExists fail`. Both halves reverted; the
chart suite is green at 63 of 63.

### Local verification, all green

`make ci` (exit 0; engine and Kubernetes module both at 100.0% total coverage,
race detector on, zero lint issues, `check-section` clean);
`make artifact-drift` (exit 0); `make release-dry-run`
(`K8S-MODULE-STANDALONE OK`, `RELEASE-DRY-RUN OK`);
`make test-acceptance-compose-selftest` (all thirteen ownership cases,
`SELFTEST OK`); `make test-browser-compose` in full, including the live
Playwright Product journeys against the published demo and the Evidence Server
recreate stage (`clone-free Compose demo acceptance PASSED`);
`make test-acceptance-kind-evidence` (32 PASS, exit 0, every ORAS
interoperability leg included); `make test-acceptance-kind-operational-graph`
with the live browser leg (11 PASS, exit 0); `make test-acceptance-local`
(`operational-graph acceptance PASSED`); `make test-browser` (219 passed);
`govulncheck ./...` (`No vulnerabilities found`); `git diff --check` clean;
`make check-section` (zero U+00A7); `bash -n` and `shellcheck` on the changed
harnesses.

Four failures were found and fixed locally before the final push, and are worth
recording because three of them were the harness and one was the demo:

- The ORAS CLI keys `discover --format json` differently across versions —
  `manifests` in 1.2, `referrers` in 1.3 — and CI installs whichever
  `setup-oras` resolves to. The observer now reads both.
- The Kind operational-graph harness installed the chart without
  `evidence.registry.subjects` and the schema's `minItems` refused the release.
  The cluster projection now emits those values from the same digest map that
  pins the CRs.
- The Compose demo deadlocked: readiness needs the subjects the seed publishes,
  and the seed waited for the server to be healthy. The dependency inverted to
  `service_started` and the seed does its own readiness wait between publish and
  send. This was a real Phase 10C ordering defect in the shipped projection, not
  a harness artifact.
- `oras attach` refuses an absolute file argument unless told the path is
  deliberate, which failed all three attach legs of the evidence acceptance.

Two further local failures were MY OWN measurement error and are recorded as
such: `tests/acceptance/local/fleet-graph.sh` and the Compose demo both bind
port 15071, and I ran them concurrently. Re-run in isolation, both pass.

### GitHub at the exact final SHA `a79aa9fa`

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE, head
`a79aa9faa522e85a19e75688673e5e00de492c90`, base `main`. Eight workflow runs
were triggered; six succeeded and two were skipped by their own conditions.

| Workflow | Run ID | Conclusion |
|---|---|---|
| CI | `32193808478` | success |
| Security | `32193808430` | success |
| Docs check | `32193808421` | success |
| Pacto Contract CI | `32193808385` | success |
| Repowise (architecture health) | `32193808463` | success |
| Validate PR title | `32193808496` | success |
| Rebuild dashboard UI | `32193808353` | skipped |
| Auto-merge Dependabot PRs | `32193808505` | skipped |

Run `32193808478` (CI) is success on attempt 1; all 21 jobs are green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `95893603732` | success |
| `operator-build` | `95893659767` | success |
| `ci-engine` | `95893659773` | success |
| `ci-dashboard` | `95893659785` | success |
| `ci-integration-kubernetes` | `95893659811` | success |
| `ci-static` | `95893659814` | success |
| `ci-e2e-envtest` | `95893659830` | success |
| `ci-gates` | `95893659834` | success |
| `dashboard-e2e` | `95893659864` | success |
| `release-version-test` | `95893659900` | success |
| `artifact-drift` | `95893659915` | success |
| `ci-e2e-kind (upgrade)` | `95893659924` | success |
| `release-dry-run` | `95893659930` | success |
| `ci-e2e-kind (evidence)` | `95893659941` | success |
| `ci-e2e-kind (dashboard)` | `95893659944` | success |
| `ci-oci` | `95893659956` | success |
| `ci-e2e-kind (observation)` | `95893660004` | success |
| `ci-e2e-compose` | `95893660036` | success |
| `ci-e2e-kind (operational-graph)` | `95893660047` | success |
| `ci-e2e-kind (reconcile)` | `95893660049` | success |
| `required` | `95895996044` | success |

`ci-static` deserves a note. At `d1c7779e` it failed with three SA5011
"possible nil pointer dereference" reports against `t.Fatal` nil-guards in the
Kubernetes module, on code the phase did not touch. It was not reproducible
locally under a warm cache, a cold `GOLANGCI_LINT_CACHE`, `GOOS=linux
GOARCH=amd64`, a fresh `linux/amd64` `golang:1.26.6` container, or the CI
ordering with both linter binaries sharing one fresh cache — every variant
reported zero issues. The two modules lint with different binaries (the module
uses a custom build carrying the logcheck plugin) and shared one analysis cache
directory under `$HOME` that CI additionally restores from an earlier commit,
so the report depended on which binary wrote the cache first. `12e367e4` gives
the module its own cache directory; `ci-static` is green at `a79aa9fa`. The
approximately fifty correct `t.Fatal` guards were NOT edited: weakening them to
satisfy a non-reproducible report would have been the wrong fix, and
`max-same-issues: 3` made it unsafe besides.

### CodeQL and review threads

Nine code-scanning alerts are open on `refs/pull/291/head`: eight
`go/path-injection` (four in `internal/app/resolve.go`, four in
`pkg/oci/cache.go`) and one `py/incomplete-url-substring-sanitization` in
`release/scripts/docs_check.py`. The newest was created on 2026-08-13, before
Phase 10C opened. The Phase 10C delta is ZERO new alerts.

All 199 review threads were paginated (the API caps a page at 100; page one
alone hides every unresolved one). Ten are unresolved: six on a generated
mermaid bundle under `pkg/dashboard/ui/assets/`, and four on `pkg/oci/cache.go`
at lines 375, 394, 395 and 666 — the same four locations as the `pkg/oci`
CodeQL alerts. Neither file was touched by Phase 10C. No thread was resolved and
no comment was published.

### Hygiene and disclosures

- `02d65c85` unintentionally absorbed four already-staged deletions that
  belonged with the commit before it. Under the append-only rule it cannot be
  amended, so it is disclosed rather than corrected. The tree at the final SHA
  is correct; only the attribution of those four deletions to a commit boundary
  is off by one.
- `make docs-generate` overwrites the hand-written
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md` with helm-docs
  output. It was reverted rather than committed, as in previous phases. The
  generator quirk predates this phase and is not fixed here.
- `go.work.sum` gains hashes from ordinary local `go` invocations. Reverted; the
  committed file is sufficient for a cold build, verified by building all
  packages against it.
- An attempt to delete two GitHub Actions cache entries, while chasing the
  SA5011 report, was refused by the permission layer as an irreversible deletion
  of shared state on an unconfirmed hypothesis. It was not retried or worked
  around; the in-tree cache isolation in `12e367e4` is the fix that shipped.
- The untracked agent files (`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`)
  are untouched and still untracked.
- One kind cluster left behind by the failed evidence run was deleted before the
  re-run, which is what a passing run's own teardown does.

### Verdict

Phase 10C is a CANDIDATE at `a79aa9fa`, awaiting independent review. It is NOT
closed — closing it is the reviewer's act, not the author's. Phase 11 has NOT
started.

## 16.2 Phase 10C independent review — REMAINS CANDIDATE

Independent review of implementation final
`a79aa9faa522e85a19e75688673e5e00de492c90`, with candidate ledger head
`bf4e561838c4c6630fd88fd98c10e7fc7741f44e`, against TARGET and the approved
`docs/superpowers/specs/2026-08-17-oci-native-evidence-referrers-design.md`.
This is a review record, not an implementation commit. TARGET is untouched and
Phase 11 has not started.

### Proven range and external state

The remote PR is OPEN, DRAFT and MERGEABLE on
`feat/operational-graph-fleet`; its exact head is `bf4e5618` and its base is
`main`. `9d2e1a43` is an ancestor of `a79aa9fa`, and `a79aa9fa` is an ancestor
of `bf4e5618`. The implementation range is exactly the fifteen linear commits
listed in section 16.1 and the only later commit is that section's ledger
record. The merge-base and `origin/main` remain
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`. No rewrite or base movement was
found.

The broad architecture is real, not just asserted by the handoff. The complete
`pkg/evidencestore` package, its app adapter, the blob-driver registration and
the recovery document are deleted. Production and current operational docs
contain no evidence bucket URL, prefix, recovery engine or managed evidence
PVC. `gocloud.dev` is absent from both module graphs and `go list -m all`.
The operator creates one stateless `Recreate` Deployment and a Service, with no
evidence data volume; Compose retains the registry and trust material but no
Evidence Server data volume. Native Docker Compose publication and execution
remain the Compose artifact boundary. ORAS is used for the evidence transport
library and as an independent interoperability observer, not as a substitute
Compose publisher.

Exact configured contract digests, native Referrers, complete pagination,
global replay reconstruction, in-process commit serialization, read-after-write
discovery, v2 health, restart persistence and the canonical Helm/Compose
subject projection are all present and exercised. Those accepted parts are not
being reopened by the findings below.

### Blocker A — the fetched OCI manifest is not validated against the fixed artifact contract

The approved contract fixes OCI schema version 2 and the empty config
`application/vnd.oci.empty.v1+json`. `BuildArtifact` emits both correctly, but
`ValidateManifest` checks neither `m.SchemaVersion` nor `m.Config`; it validates
only the manifest media type, artifact type, payload layer and subject. A
third-party registry writer can therefore publish something labelled as a
Pacto evidence artifact with schema version 1 or an arbitrary config media type,
and Pacto counts it as a valid record rather than a malformed Pacto artifact.

The reviewer added a temporary package-local counterexample that changed only
those two fields on a manifest produced by `BuildArtifact`. Both cases failed
the desired assertion:

```text
wrong_schema_version: accepted a manifest outside the fixed Phase 10C artifact contract
wrong_config_media_type: accepted a manifest outside the fixed Phase 10C artifact contract
```

The temporary test was removed. The permanent rejection table in
`internal/evidenceoci/artifact_test.go` covers wrong manifest and layer media
types but has no schema-version or config-descriptor case. Section 16.1's claim
that `ValidateManifest` rejects wrong schemas and all fixed media types is
therefore too broad.

Closure requires one canonical strict manifest validator that rejects any
schema version other than 2 and any config descriptor other than the fixed OCI
empty-JSON descriptor, with permanent adversarial tests proved non-vacuous by
mutation. Do not add a second codec or compatibility fallback.

### Blocker B — registry records bypass the envelope structure and bounds preserved by 10C

The ingestion boundary strictly decodes at most 1 MiB and rejects a wrong
`apiVersion`, wrong `kind`, missing producer key identity and more than 10,000
observations before signature verification. The approved design explicitly
says those existing envelope limits stay in force when the record is recovered
from OCI.

`DecodePayload` instead applies the 8 MiB record cap and then calls
`validateRecord`. That helper checks an envelope ID and producer ID but never
applies `evidenceenvelope` structural validation or its envelope/observation
bounds. The reviewer changed a valid stored record to
`apiVersion: pacto.dev/evidence/v999`; `DecodePayload` accepted it even though
the public ingestion endpoint necessarily rejects the same envelope. Missing
`kind` or `producer.keyId`, excessive observations and an embedded envelope over
the protocol cap follow the same path. Such a record can enter replay state and
the Product projection through ORAS interoperability or any other authorized
repository writer.

Closure requires the stored-record codec to reuse the canonical envelope
decoder/validator and its size/count constants, rather than hand-copying a
subset. Permanent tests must cover at least wrong version, missing key identity,
too many observations and an oversized embedded envelope, and mutation must
show the gate bites. Signature re-verification on registry reads is not being
requested: the approved repository-write trust boundary remains unchanged.

### Blocker C — readiness preflight is not time-bounded

The approved design requires a bounded registry preflight. The ORAS repository
uses `retry.DefaultClient`, whose `http.Client.Timeout` is zero. `Store.Ready`
can be bounded by its caller's context, but `buildEvidenceHost` passes the
long-lived server context into the readiness closure. `handleReady` does not
pass the HTTP request context because the callback is `func() bool`.

A registry that accepts the connection and never returns headers can therefore
hold a `/ready` handler indefinitely. Kubernetes' probe timeout closes its own
request but does not cancel the registry request made with the server-lifetime
context; repeated startup/readiness probes can accumulate stuck handlers. Page
count limits do not bound this case.

Closure requires one explicit whole-preflight deadline that reaches every
resolve and Referrers call and is independent of the server lifetime. A
deterministic test registry must accept a request and withhold its response,
then prove `/ready` returns `503` inside the configured budget and the blocked
registry request is cancelled. Do not solve this with liveness restarts,
background readiness state, retries without a deadline or new infrastructure.

### Independent verification and GitHub evidence

On the committed bytes, all existing focused suites are green:

- `go test -race ./internal/evidenceoci ./pkg/evidenceingest ./internal/fleetsrc -count=1`;
- the evidence/serve slice of `internal/app`;
- `integrations/kubernetes/internal/evidence`;
- all 63 Helm unit tests;
- `git diff --check`.

This confirms the findings are missing invariants, not pre-existing red tests.
After the temporary counterexamples the tracked tree was restored clean. Only
the four inherited untracked agent paths remain and were not touched.

At exact ledger head `bf4e5618`, CI run `32194918809` is successful: all 21
jobs pass, including all six Kind shards, `ci-e2e-compose`,
`dashboard-e2e`, `ci-oci`, `artifact-drift`, `release-dry-run` and `required`
`95899273709`. Security `32194918827`, Docs check `32194918871`, Pacto Contract
CI `32194918784`, Repowise `32194918839`, title validation `32194918799`, Code
Quality `32194916490` and PR CodeQL workflow `32194916240` are successful; the
two conditional workflows are skipped. The aggregate CodeQL check remains red
because the same nine inherited PR alerts remain. None was created by Phase
10C, and none of their three files is in the Phase 10C range.

Review threads were independently paginated again: 199 total, 10 unresolved.
All ten are the inherited bot threads recorded in section 16.1, on the generated
Mermaid asset and `pkg/oci/cache.go`; neither file is touched here. No comment
was published, no thread resolved and no PR metadata changed.

### Verdict and narrow next objective

**Phase 10C REMAINS CANDIDATE. It is not CLOSED.** The architectural pivot is
substantially implemented and the deletion/infrastructure/interop/CI evidence is
accepted, but the strict artifact trust boundary and bounded readiness criterion
have three concrete counterexamples. Green CI cannot close invariants it does
not test.

The next implementation is a narrow Phase 10C closure repair only:

1. make manifest validation enforce OCI schema 2 and the canonical empty-JSON
   config descriptor;
2. make stored payload validation reuse the existing envelope structure and
   size/count limits without re-verifying signatures;
3. give readiness one explicit end-to-end deadline and cancellation proof;
4. add permanent adversarial tests and mutation evidence for all three;
5. run focused, full local and exact-SHA CI evidence, then append a candidate
   repair record for another independent review.

Preserve the accepted OCI-native architecture, exact contract subjects,
single-writer model, replay semantics, DTOs, deletion set, canonical scenario
and native Docker Compose publication. Do not start Phase 11, redesign 10C,
restore any bucket/PVC path, add migration or multi-writer machinery, touch
TARGET, rewrite history, publish PR comments or resolve threads.

### Current phase map

- Phases 1 through 10B: ACCEPTED and CLOSED.
- Phase 10C: CANDIDATE, blocked on A/B/C above.
- Phases 11 through 14: NOT STARTED.

## 16.3 Phase 10C narrow closure repair — CANDIDATE at `c9bbdfd7`

Narrow closure repair of the three blockers in section 16.2. Nothing was
redesigned, Phase 11 has not started, TARGET is untouched and no historical
section was edited.

### Range

- Starting SHA (independent-review ledger head): `823aab1190b96eee42af92297e88d47b6b916398`.
- Implementation final SHA: `c9bbdfd7a3516a8f1facfb2a76e96c90f9457ac7`.
- `origin/main` and merge-base, unchanged: `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- PR 291 stayed OPEN, DRAFT and MERGEABLE throughout; its head is `c9bbdfd7`.
- History is append-only: three new commits on top of `823aab11`, no rebase,
  amend, squash, reset or force-push. `823aab11` remains an ancestor of the head.

Commits, in order:

1. `c2070b5d` fix(evidence): validate the whole OCI manifest contract, not part of it
2. `fdbf7d87` fix(evidence): re-validate a stored envelope with the canonical decoder
3. `c9bbdfd7` fix(evidence): bound the readiness preflight on the request that asked

Changed files across the range (449 insertions, 37 deletions):
`internal/evidenceoci/artifact.go`, `internal/evidenceoci/artifact_test.go`,
`internal/evidenceoci/store.go`, `internal/evidenceoci/store_test.go`,
`internal/app/evidence.go`, `internal/app/evidence_test.go`,
`pkg/evidenceingest/ingest.go`, `pkg/evidenceingest/ingest_test.go`.

### Blocker A — strict OCI manifest contract

`ValidateManifest` now has two leading cases ahead of the media-type,
artifact-type, layer and subject cases it already had:

- `m.SchemaVersion != manifestSchemaVersion`, where `manifestSchemaVersion = 2`
  is one new package constant shared by the writer and the reader.
  `specs.Versioned.SchemaVersion` carries no `omitempty`, so an absent field
  decodes to zero and this single comparison rejects both a Docker v1 manifest
  and a manifest with no version at all.
- the config descriptor compared field for field against
  `ocispec.DescriptorEmptyJSON` (media type, digest, size). Field comparison
  rather than `==` because `ocispec.Descriptor` contains a slice and a map and
  will not compile under equality.

`BuildArtifact` no longer hand-builds that descriptor either: it takes
`ocispec.DescriptorEmptyJSON` from image-spec and clones its inline `Data`, so
the writer and the validator cannot drift. The value is byte-identical to what
the writer emitted before, so published manifest digests are unchanged and no
already-stored record is invalidated. There is one validation path and one
codec; nothing was added alongside them.

RED, before the fix, with the five new permanent cases in
`TestValidateManifest_Rejects`:

```text
--- FAIL: TestValidateManifest_Rejects/schema_version_1 (0.00s)
--- FAIL: TestValidateManifest_Rejects/missing_schema_version (0.00s)
--- FAIL: TestValidateManifest_Rejects/wrong_config_media_type (0.00s)
--- FAIL: TestValidateManifest_Rejects/wrong_config_digest (0.00s)
--- FAIL: TestValidateManifest_Rejects/wrong_config_size (0.00s)
```

each reporting the manifest was accepted (`err = <nil>`). GREEN after the fix,
with `internal/evidenceoci` still at 100.0% statement coverage.
`TestBuildArtifact_Shape` additionally pins the emitted config's media type,
digest and size against `ocispec.DescriptorEmptyJSON`.

Mutations applied one at a time and reverted, each caught by exactly the case
it was meant to be caught by:

| Mutation | Failing test |
|---|---|
| drop the `SchemaVersion` case | `schema_version_1`, `missing_schema_version` |
| drop the config media-type disjunct | `wrong_config_media_type` |
| drop the config digest disjunct | `wrong_config_digest` |
| drop the config size disjunct | `wrong_config_size` |

### Blocker B — canonical envelope structure and bounds on the read path

`validateRecord` now ends in a new `validateEnvelope`, which runs the canonical
ingestion validators over a stored record rather than a hand-picked subset:

- `evidence.ValidateEvidenceSet` first, because `Observation.MarshalJSON`
  returns an error for a structurally invalid observation; zero errors from it
  is what makes the encode below unable to fail, which keeps the
  `_ = enc.Encode(env)` idiom honest under the 100% gate;
- then the embedded envelope is serialized deterministically with
  `SetEscapeHTML(false)` and passed through `evidenceenvelope.Decode`, the same
  entry point the producer's own bytes went through. That carries
  `apiVersion`, `kind`, envelope ID, producer ID, producer key ID,
  `MaxObservations` and `MaxEnvelopeBytes` without restating any of them.
  HTML escaping is off so expanding one `<` into six bytes cannot push an
  envelope that is legitimately just under `MaxEnvelopeBytes` over the limit.

Three checks were REMOVED from `validateRecord` (empty envelope ID, empty
producer ID, empty target subject name) because the canonical validators now
subsume them. That is what makes "one canonical path" real rather than nominal.
No test, script or document asserted their messages.

Signatures are deliberately NOT reverified on registry reads, as instructed:
they are verified once at ingestion, and write authorization on the contract
repository remains the trust boundary. The reason is recorded in the function's
doc comment, so a later reader does not mistake it for an omission.

RED, before the fix: all eight cases of the new
`TestDecodePayload_RejectsInvalidEnvelope` accepted their record (`err = <nil>`)
— unknown `apiVersion`, missing `kind`, wrong `kind`, empty `producer.keyId`,
over `MaxObservations`, over `MaxEnvelopeBytes`, an observation with no instant
and an unattributable observation. Each case also asserts `BuildArtifact`
refuses the same record, so the writer and the reader agree.

One honest detail worth recording: with today's constants,
`MaxObservations` (10000) is unreachable before `MaxEnvelopeBytes` (1 MiB) — no
observation serializes in the ~104 bytes that would let 10001 of them fit — so
the over-`MaxObservations` case expects `ErrTooLarge` rather than
`ErrTooManyObs`, with a comment saying why. The bound bites either way.

The store-level counterexample, also RED before the fix:

```text
TestStore_CommitFailsClosedOnInvalidStoredEnvelope
  health = {Status:ready InvalidArtifacts:0 ...}, want partial with 1 invalid artifact
  commit over an invalid stored envelope = <nil>, want ErrRegistryIncomplete
```

It pushes a Pacto artifact whose stored envelope has no producer key id, then
proves the read is `partial` with one invalid artifact, the record is kept out
of the projection and the next `Commit` fails closed with
`ErrRegistryIncomplete`. GREEN after the fix, coverage still 100.0%.

Mutations applied and reverted:

| Mutation | Effect |
|---|---|
| `return validateEnvelope(rec.Envelope)` becomes `return nil` | 12 sub-tests plus the store test fail |
| `len(errs) > 0` becomes `len(errs) < 0` | 3 tests fail (`no_target_subject`, `no_observation_instant`, `unattributable_observation`) |
| delete the `evidenceenvelope.Decode` guard | 9 sub-tests fail |

An earlier attempt at the second mutation deleted the whole
`ValidateEvidenceSet` block, which left two imports unused and produced
`[build failed]` — a weaker signal than a failing assertion. It was re-run as
the compiling semantic mutation above so the evidence is a real test failure.

### Blocker C — genuinely bounded readiness

Three coordinated changes, all small:

- `evidenceingest.Handler.ready` is now `func(context.Context) bool`, and both
  gated paths (`handleReady` and `handleEnvelope`) hand it `r.Context()`. This
  is the signature adjustment section 16.2 allowed; nothing else about the
  handler changed.
- `internal/app.buildEvidenceHost` no longer takes a context at all. Its
  readiness closure derives `context.WithTimeout(reqCtx, readinessTimeout)` from
  the asking request, so ONE budget covers every configured subject's resolve
  and Referrers walk, and the ORAS calls die either when the caller hangs up or
  when the budget expires. `readinessTimeout` is an unexported package var
  (3 seconds), inside the readiness probe's five-second period so probes cannot
  pile up, and shortened by the tests. No new public configuration was added.
- `subjectRepo.resolve` releases its memoization mutex before the network call
  and re-takes it only to store the descriptor. A mutex honours no deadline, so
  the old code let a second caller wait past its own budget behind a first
  caller stuck in a resolve. Two callers racing now issue one duplicate request
  for the same immutable digest, which is the cheap half of the trade.

There is no background readiness polling, no readiness cache, no permanent
goroutine and no liveness-driven restart. `/health` is untouched and remains an
unconditional 200.

The deterministic test registry is `stalledRegistry` in
`internal/app/evidence_test.go`: it proxies a real `testutil.NewReferrersRegistry`
until it is stalled, after which it accepts the request, signals `arrived`,
blocks on `<-r.Context().Done()` and then signals `cancelled`. No sleeps are
used for coordination; the only durations in the tests are the budget itself and
generous fatal timeouts.

`TestServeEvidence_ReadinessIsBounded` fires two concurrent probes at a stalled
registry and proves: both reach the registry (nothing serializes them behind an
uninterruptible lock), `/health` answers 200 while they are stuck, both return
`503` well inside the budget, and both abandoned registry requests observe
cancellation. RED before the fix:

```text
--- FAIL: TestServeEvidence_ReadinessIsBounded (10.06s)
    ready never came back: Get "http://127.0.0.1:61997/api/evidence/v1/ready":
    context deadline exceeded (Client.Timeout exceeded while awaiting headers)
```

`TestServeEvidence_ReadinessFollowsTheCaller` covers the other half of bounded:
with a budget far longer than the test, the caller cancels its own request and
the registry request must be cancelled with it.

`TestHandler_Ready` in `pkg/evidenceingest` now asserts inside the callback that
the context it receives is live and cancellable, so a future `context.Background()`
shortcut in any host is caught at the boundary rather than three packages away.

Mutations applied and reverted:

| Mutation | Failing test |
|---|---|
| remove `context.WithTimeout`, pass the request context straight through | `ReadinessIsBounded` — the probe never returns, the client's own patience ends it |
| parent the budget on `context.Background()` instead of the request | `ReadinessFollowsTheCaller` — "the registry request outlived the caller" |
| re-hold `subjectRepo.mu` across the network resolve | `ReadinessIsBounded` — "only 1 of 2 registry requests did reach the registry within 10s" |

### Local verification

All run on the committed bytes at `c9bbdfd7`:

- `go test -race ./internal/evidenceoci ./pkg/evidenceingest ./internal/fleetsrc ./internal/app ./pkg/evidenceenvelope ./pkg/evidence -count=1` — ok.
- `internal/app`, `pkg/evidenceingest` and `internal/evidenceoci` each at 100.0% statement coverage.
- `integrations/kubernetes/internal/evidence` — ok, 100.0%.
- All 63 Helm unit tests across 8 suites — passed.
- `make ci` — exit 0 (`ci-static`, `ci-gates`, `ci-engine`, `ci-dashboard`, `ci-integration-kubernetes`, `ci-e2e-envtest`, `ci-oci`), which includes `check-section` (zero U+00A7 in authored files) and `test-acceptance-local`.
- `make artifact-drift` — OK.
- `make release-dry-run` — OK, plus `STANDALONE-VERIFY OK` for the k8s module.
- `make test-acceptance-local` — operational-graph acceptance PASSED.
- `make test-acceptance-compose-selftest` — OK.
- `make test-browser-compose` — clone-free Compose demo acceptance PASSED.
- `make test-acceptance-kind-evidence` — full in-cluster Evidence Server lifecycle acceptance PASSED.
- `make test-acceptance-kind-operational-graph` — the full operational-graph vertical is UP.
- `make test-browser` — 219 passed.
- `govulncheck ./...` — no vulnerabilities found.
- `git diff --check` — clean.

The two suites that compete for the same ports were run sequentially. The
`integrations/kubernetes/charts/pacto-dev-gateway/README.md` regeneration churn
appeared once during `make ci` and was reverted; it is not part of the repair.
No `go.work.sum` churn survived. The four inherited untracked agent paths
(`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`) were never touched.

### GitHub Actions at `c9bbdfd7`

CI run `32227315064` is successful: all 21 jobs pass — `changes`, `ci-static`,
`ci-gates`, `ci-engine`, `ci-dashboard`, `ci-integration-kubernetes`,
`ci-e2e-envtest`, `ci-oci`, `operator-build`, `release-version-test`,
`artifact-drift`, `release-dry-run`, `dashboard-e2e`, `ci-e2e-compose`, all six
`ci-e2e-kind` shards (`dashboard`, `evidence`, `observation`,
`operational-graph`, `reconcile`, `upgrade`) and `required`.

The other workflows at the same SHA: Security `32227315037` success (including
`govulncheck (Go)` and Trivy), Docs check `32227315021` success, Pacto Contract
CI `32227315116` success, Repowise (architecture health) `32227315167` success,
Validate PR title `32227315196` success, Code Quality `32227312312` success and
the PR CodeQL workflow `32227312040` success. The two conditional workflows,
Rebuild dashboard UI `32227315056` and Auto-merge Dependabot PRs `32227315121`,
are skipped as usual.

### CodeQL and review threads

The aggregate GitHub Advanced Security check is still red, for the same
inherited reason as at `bf4e5618`. Nine open alerts on the PR ref, none created
by this repair and none in a file it touches:

| Alert | Rule | File | Created |
|---|---|---|---|
| 38 | py/incomplete-url-substring-sanitization | `release/scripts/docs_check.py` | 2026-07-27 |
| 40-43 | go/path-injection | `internal/app/resolve.go` | 2026-07-29 |
| 59-61 | go/path-injection | `pkg/oci/cache.go` | 2026-08-13 |
| 62 | go/path-injection | `pkg/oci/cache.go` | 2026-08-13 |

Every one predates this session, the newest by six days. The CodeQL delta for
this repair is zero: nothing added, nothing removed. `internal/app/resolve.go`
shares a package with a changed file but is not itself changed.

Review threads were fully paginated: 199 total, 10 unresolved. They are the same
ten inherited bot threads recorded in sections 16.1 and 16.2 — six on the
generated Mermaid asset `pkg/dashboard/ui/assets/ganttDiagram-*.js` and four
CodeQL path-injection threads on `pkg/oci/cache.go`. Neither file is touched
here, so no in-scope obligation was created. No comment was published, no thread
resolved and no PR metadata changed.

### Status

**Phase 10C REMAINS CANDIDATE.** It is not CLOSED: the three blockers from
section 16.2 have concrete counterexamples, minimal fixes, mutation evidence and
green local and remote verification, but closure requires another independent
review. Phase 11 has not started, `PACTO_PR_TARGET_STATE.md` is untouched and no
earlier section of this document was edited.

### Current phase map

- Phases 1 through 10B: ACCEPTED and CLOSED.
- Phase 10C: CANDIDATE, repaired at `c9bbdfd7`, awaiting independent review.
- Phases 11 through 14: NOT STARTED.

## 16.4 GitHub Actions at the ledger head `3aa9ab6c`

Section 16.3 was written before its own commit could be pushed, so it records
the remote state at the implementation head `c9bbdfd7`. This section records the
remote state at the branch head that carries it.

CI run `32228879022` at `3aa9ab6c` first failed in two jobs, `ci-e2e-kind
(reconcile)` (95994336772) and `required` (95996640029). The cause is a Go
module proxy transient inside the operator image build, not a regression:

```text
#13 2.461 go: github.com/klauspost/compress@v1.19.1: reading
    https://proxy.golang.org/github.com/klauspost/compress/@v/v1.19.1.zip: 403 Forbidden
#13 2.461 go: github.com/segmentio/encoding@v0.5.4: reading
    https://proxy.golang.org/github.com/segmentio/encoding/@v/v0.5.4.zip: 403 Forbidden
#13 ERROR: process "/bin/sh -c go mod download" did not complete successfully: exit code: 1
```

`3aa9ab6c` changes one file, `.pr-context/PACTO_PR_CURRENT_STATE.md`, and the
same shard passed at `c9bbdfd7` minutes earlier. The two failed jobs were re-run
and both pass. Run `32228879022` is now successful with all 21 jobs green:
`changes`, `ci-static`, `ci-gates`, `ci-engine`, `ci-dashboard`,
`ci-integration-kubernetes`, `ci-e2e-envtest`, `ci-oci`, `operator-build`,
`release-version-test`, `artifact-drift`, `release-dry-run`, `dashboard-e2e`,
`ci-e2e-compose`, all six `ci-e2e-kind` shards (`dashboard`, `evidence`,
`observation`, `operational-graph`, `reconcile`, `upgrade`) and `required`.

The other workflows at `3aa9ab6c`: Security `32228878820` success, Docs check
`32228878832` success, Pacto Contract CI `32228878897` success, Repowise
(architecture health) `32228878830` success, Validate PR title `32228878884`
success, Code Quality `32228875861` success and the PR CodeQL workflow
`32228875941` success. Rebuild dashboard UI `32228879010` and Auto-merge
Dependabot PRs `32228878853` are skipped as usual.

CodeQL and review threads are unchanged from section 16.3: nine open alerts, all
inherited, delta zero; 199 threads, 10 unresolved, all inherited. No comment was
published, no thread resolved and no PR metadata changed. PR #291 is OPEN and
DRAFT at `3aa9ab6c`.

**Phase 10C REMAINS CANDIDATE.** Phase 11 has not started.

## 16.5 Independent review of the Phase 10C closure repair at `a017b63a`

Independent, read-only review of the candidate described in sections 16.3 and
16.4. The handoff was treated only as a claim source. The tracked PR context,
approved OCI-native design, maintainer testing taxonomy, implementation, tests,
history and GitHub state were inspected directly. Temporary adversarial tests
were removed before the official suites ran. No PR comment was published, no
thread was resolved and no PR metadata was changed.

### Proven range and hygiene

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE. Its remote head and the
local branch `feat/operational-graph-fleet` were both exactly
`a017b63a5a0c420fea89339feb12c0e0d2e4ee83` when reviewed. The merge-base and
`origin/main` remain
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`.

The range from the preceding independent-review ledger head `823aab11` is five
linear, single-parent commits: `c2070b5d`, `fdbf7d87`, `c9bbdfd7`, `3aa9ab6c`
and `a017b63a`. The start remains an ancestor of the head. There is no rebase,
amend, merge, force-push or merge-base movement. The range changes exactly the
nine files recorded in the handoff, with 772 insertions and 37 deletions.

The tracked tree was clean before review except for the four inherited untracked
agent paths (`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`). `make ci`
reproduced the already-disclosed `pacto-dev-gateway/README.md` helm-docs churn;
it was restored to the committed bytes and is not part of this review.

### Accepted repair B — canonical stored-envelope validation

The manual subset in `validateRecord` is gone. `validateEnvelope` first applies
`evidence.ValidateEvidenceSet`, then serializes the typed envelope without HTML
expansion and passes those canonical bytes through `evidenceenvelope.Decode`.
That reuses the version, kind, required identity, observation count and envelope
size rules without adding signature verification to the registry read path.
The writer and reader both call the same helper. The permanent invalid-envelope
table, the store-level fail-closed test and the recorded mutations exercise the
claimed boundary.

A temporary whitespace-only representation larger than `MaxEnvelopeBytes` was
also tried. The stored payload accepts it after canonical compaction. This is
not a closure blocker: whitespace is not envelope domain data, the canonical
envelope remains under its protocol cap, and the enclosing untrusted registry
payload is independently bounded by `maxPayloadBytes`. Requiring a second raw
subdocument extractor would add machinery without strengthening the bounded
trust model.

**Blocker B is CLOSED.**

### Accepted repair C — readiness has one real request-scoped budget

Both `/ready` and the ingestion gate pass the live HTTP request context into the
readiness callback. `buildEvidenceHost` derives one three-second deadline from
that context for the complete multi-subject preflight, and every resolve and
Referrers request receives it. `subjectRepo.resolve` no longer holds its mutex
across network I/O, so one stalled caller cannot make another wait outside its
own context budget. Liveness remains unconditional and no background poller,
cache, retry loop or restart coupling was introduced.

The stalled-registry tests prove two simultaneous requests reach the registry,
return `503` on the server budget, cancel the abandoned registry calls and also
follow an earlier caller cancellation. The implementation and mutations match
the approved narrow repair.

**Blocker C is CLOSED.**

### Remaining blocker A — the validator still accepts internally false OCI descriptors

The repair correctly rejects a non-2 manifest schema and config media type,
digest or size different from `ocispec.DescriptorEmptyJSON`. It does not,
however, validate the rest of either descriptor whose content it does not fetch.
This leaves two concrete counterexamples:

1. Starting from `BuildArtifact`, replace only `manifest.Config.Data` with
   `{"not":"empty"}` while retaining the canonical empty-JSON media type,
   digest and size. `ValidateManifest` returns success. OCI Image Spec 1.1.1
   requires decoded inline `data` to be identical to the content named by the
   descriptor and says consumers should verify it against digest and size. The
   accepted descriptor claims two-byte `{}` while carrying different bytes.
2. Starting from the same manifest, retain the exact configured subject digest
   but change `manifest.Subject.MediaType` and `manifest.Subject.Size` so they no
   longer describe the subject descriptor the registry just resolved.
   `ValidateManifest` again returns success. The digest still points at the
   configured revision, but the OCI descriptor as a whole is false and the
   validator already has the resolved descriptor available in the scan.

Both package-local temporary tests failed on the committed implementation:

```text
TestReviewCounterexample_ConfigInlineDataMustMatchCanonicalEmptyJSON
  ValidateManifest accepted a config descriptor whose inline data contradicts its digest and size
TestReviewCounterexample_SubjectDescriptorMustMatchResolvedSubject
  ValidateManifest accepted subject metadata that contradicts the resolved subject descriptor
```

The tests were removed and the tracked implementation restored before official
verification. This is not a request to fetch or interpret the empty config, add
a compatibility mode or build a second codec. The fixed config value already
comes from `ocispec.DescriptorEmptyJSON`, ORAS Go emits that same descriptor,
and `scanSubject` already owns the subject descriptor returned by `Resolve`.
Closure requires the one manifest validator to verify the descriptor fields it
accepts: the config must be the canonical empty-JSON descriptor including
inline data and absence of contradictory extras, and the subject's core
media-type/digest/size must agree with the already-resolved contract descriptor.
Any inline data that is accepted must agree with its digest and size. Permanent
tests and compiling semantic mutations must prove both cases bite.

**Blocker A REMAINS OPEN.**

### Independent verification and GitHub evidence

On the committed bytes, all official tests run by the reviewer are green:

- `go test -race ./internal/evidenceoci ./pkg/evidenceingest ./internal/app -count=1`;
- full `make ci`, including lint, architecture/release tests, 100.0% aggregate
  engine and Kubernetes coverage, race tests, integration, the complete
  acceptance subtree, operational-graph local acceptance, frontend lint and
  1,232 tests, 63 Helm tests, envtest and OCI tests;
- `git diff --check` and final tracked-tree hygiene.

At exact head `a017b63a`, CI run `32230392088` is successful with all 21 jobs
green, including all six Kind shards, Compose, dashboard E2E, OCI,
artifact-drift, release-dry-run and `required` (`96001118666`). Security
`32230392115`, Docs check `32230391905`, Pacto Contract CI `32230391942`,
Repowise `32230391986`, title validation `32230391922`, Code Quality
`32230388639` and the PR CodeQL workflow `32230388393` all succeed. The two
conditional workflows are skipped.

The aggregate PR CodeQL check remains red because the same nine inherited open
alerts remain: eight `go/path-injection` findings in `internal/app/resolve.go`
and `pkg/oci/cache.go`, plus the inherited Python URL-substring finding in
`release/scripts/docs_check.py`. All nine predate Phase 10C and none of those
three files is in this repair range. The Phase 10C CodeQL delta is zero.

Review threads were fully paginated: 199 total, 10 unresolved. Six are
bot-authored on the generated Mermaid bundle and four are GitHub Advanced
Security threads on `pkg/oci/cache.go`; both paths are outside this range. No
new actionable thread belongs to the repair.

### Verdict and narrow next objective

**Phase 10C REMAINS CANDIDATE. It is not CLOSED.** Repairs B and C are accepted
and must not be reopened. Repair A closes the originally demonstrated fields
but still lets an invalid OCI descriptor cross the strict artifact trust
boundary. Green CI does not cover those descriptor counterexamples.

The next implementation is one final, narrow Phase 10C manifest-validator
repair only: validate the canonical config descriptor completely, compare the
manifest subject's core descriptor with the already-resolved contract
descriptor, add permanent adversarial tests and mutation proof, run focused and
full local gates, record exact-SHA GitHub evidence and return the phase as a
candidate for another independent review. Do not start Phase 11, redesign 10C,
change the accepted envelope or readiness repairs, restore storage
infrastructure, add a second codec, touch TARGET, rewrite history, publish PR
comments or resolve threads.

Current phase map:

- Phases 1 through 10B: ACCEPTED and CLOSED.
- Phase 10C: CANDIDATE, blocked only on the remaining descriptor-validation
  portion of A above.
- Phases 11 through 14: NOT STARTED.

## 16.6 Phase 10C final descriptor-validation repair — CANDIDATE at `bcd2d2b1`

The one narrow repair section 16.5 asked for: the single manifest validator now
verifies the descriptor fields it accepts. Nothing was redesigned, no accepted
repair was reopened, Phase 11 has not started, TARGET is untouched and no
historical section of this document was edited.

### Range

- Starting SHA (independent-review ledger head): `30267affffa292f25f50dc46b3ec48f277597045`.
- Implementation final SHA: `bcd2d2b1fb3fbcea7c7b135a22b12d83e33c56df`.
- Ledger SHA: the commit carrying this section, recorded exactly in section 16.7.
- `origin/main` and merge-base, unchanged: `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- PR 291 stayed OPEN, DRAFT and MERGEABLE throughout; its head is `bcd2d2b1`.
- History is append-only: two new commits on top of `30267aff`, no rebase,
  amend, merge, squash, reset or force-push. `30267aff` remains an ancestor of
  the head.

Commits, in order:

1. `bc0bb0dc` fix(evidence): require the canonical empty-JSON config, not three of its fields
2. `bcd2d2b1` fix(evidence): validate the manifest subject against the descriptor already resolved

Changed files across the range (5 files, 233 insertions, 21 deletions):

| File | Change |
|---|---|
| `internal/evidenceoci/artifact.go` | +98/-11 — whole-descriptor config rule, `validateSubjectBinding`, `inlineDataAgrees`, `descriptorJSON`, `BuildArtifact` clone fix |
| `internal/evidenceoci/artifact_test.go` | +91/-2 — eight new reject cases, config-shape assertions, ORAS interoperability guard |
| `internal/evidenceoci/scan.go` | +6/-4 — thread the already-resolved subject descriptor through `readReferrer` |
| `internal/evidenceoci/scan_test.go` | +30/-0 — canonical config in two fixtures, subject-mismatch pusher |
| `internal/evidenceoci/store_test.go` | +25/-0 — store-level fail-closed test |

The two commits are separately buildable: `bc0bb0dc` was built, tested and
proved 100.0% coverage on its own before the subject repair was applied on top.

### Repair A, part one — the canonical config descriptor, whole

`ValidateManifest` compared three fields of the config descriptor against
`ocispec.DescriptorEmptyJSON`. Three matching fields still admit a descriptor
that contradicts itself: canonical media type, digest and size carried over
inline `data` that is not `{}`. They also admit optional fields nothing in this
reader interprets, so a manifest could assert an artifact type, download URLs or
annotations on a blob Pacto neither fetches nor means.

The three-field comparison is replaced by one whole-value comparison:

```go
var canonicalConfigJSON = descriptorJSON(ocispec.DescriptorEmptyJSON)

case !bytes.Equal(descriptorJSON(m.Config), canonicalConfigJSON):
```

`descriptorJSON` marshals a descriptor to JSON. Go emits struct fields in
declaration order and map keys sorted, so two descriptors are equal exactly when
those bytes are; `ocispec.Descriptor` contains a slice and a map, so it cannot be
compared with `==` and a field-by-field list would have to be extended by hand
every time image-spec adds a field. One comparison covers every field that
exists today and every field added later, including the inline copy of the two
canonical bytes. The canonical value is still taken from image-spec rather than
spelled out, so the writer and the reader cannot drift.

No second config blob is fetched and nothing is interpreted: the rule is that
the descriptor must be the fixed canonical one, which is precisely why its
content never needs reading. There is still one validator and one codec, and no
compatibility path was added.

`BuildArtifact` also gained one `bytes.Clone`. The struct copy of
`ocispec.DescriptorEmptyJSON` left `Data` aliasing image-spec's package-level
slice, so a caller writing through a returned `Artifact` could have corrupted
every later build in the process. The emitted bytes are unchanged, so published
manifest digests are unchanged and no already-stored record is invalidated.

### Repair A, part two — the subject against the descriptor already resolved

`ValidateManifest` checked only that the subject digest equalled the configured
revision. A manifest could keep that digest and restate `mediaType` or `size` so
the descriptor contradicted the very one the registry had just resolved and the
listing was being enumerated against.

`ValidateManifest` now takes the resolved descriptor as a third parameter and
delegates the subject to `validateSubjectBinding`, which requires:

- the subject digest equals the configured `Subject.Digest`. This is the
  authorization question — may this store hold evidence for this revision at all
  — and it is deliberately still answered against the configured value, not
  against a descriptor a caller supplied;
- the subject's media type, digest and size equal the resolved contract
  descriptor. This is the identity question;
- any inline `data` the subject carries is the content it addresses:
  `len(data) == size` and `sha256(data) == digest`. image-spec 1.1.1 requires a
  consumer that accepts `data` to verify it, so a descriptor carrying other bytes
  is contradicting itself whether or not this reader would have gone on to read
  them. A descriptor with no inline data agrees vacuously.

Annotations and the remaining optional subject fields are deliberately NOT
compared: OCI permits them, nothing here reads them and requiring equality would
reject harmless metadata another tool legitimately adds.

The descriptor is threaded, not re-resolved. `scanSubject` already holds the
descriptor `Resolve` returned and already passes it to `repo.Referrers`; it now
passes the same value down through `readReferrer` to `ValidateManifest`. There is
no second registry round trip, and no window in which a manifest is checked
against a different answer than the one the scan is enumerating. `ValidateManifest`
has exactly one production caller, so this is a small internal signature
adjustment rather than a new abstraction.

`validateSubjectBinding` is a separate function because inlining the three cases
took `ValidateManifest` to cyclomatic complexity 16, one over the `ci-cyclo`
limit of 15. Split, they are 12 and 6.

### Adversarial tests

Eight new permanent cases in `TestValidateManifest_Rejects`, each mutating one
field of a real `BuildArtifact` manifest and requiring `ErrInvalidArtifact`:

| Case | What it pins |
|---|---|
| `config inline data is not the empty JSON` | counterexample 1 of section 16.5, verbatim |
| `config carries no inline data` | the canonical descriptor includes its `data`, so omitting it is not canonical |
| `config carries annotations` | contradictory or uninterpreted extra fields |
| `config carries an artifact type` | as above |
| `config carries download urls` | as above |
| `subject media type is not the resolved one` | counterexample 2 of section 16.5 |
| `subject size is not the resolved one` | counterexample 2 of section 16.5 |
| `subject inline data contradicts its digest` | inline data accepted on a subject must agree with its digest and size |

The pre-existing `wrong subject digest` case is retained unchanged.
`TestBuildArtifact_Shape` gained two rows pinning the emitted config's inline
data to `{}` and the whole emitted descriptor to `canonicalConfigJSON`, plus a
pointer-identity assertion that neither the returned config blob nor the
descriptor's inline copy aliases image-spec's package-level slice.

`TestValidateManifest_AcceptsOrasPackedArtifact` is the interoperability guard:
it packs an equivalent evidence artifact with `oras.PackManifest` at
`PackManifestVersion1_1` over a memory store and requires `ValidateManifest` to
accept it. ORAS Go's `packManifestV1_1` sets the config to
`ocispec.DescriptorEmptyJSON` including its inline data, and
`remote.manifestStore` returns resolved descriptors carrying media type, digest
and size only, so both new rules are satisfied by an ORAS-produced artifact
rather than merely believed to be. The valid Pacto-produced artifact continues to
pass through the pre-existing accept test.

`TestStore_CommitFailsClosedOnSubjectDescriptorMismatch` proves the rule at the
store level, not only at the validator: a Pacto artifact whose subject size
contradicts the resolved descriptor makes the read report `partial` with
`InvalidArtifacts` 1, keeps the record out of the projection and makes the next
`Commit` fail closed with `ErrRegistryIncomplete`.

Two existing fixtures in `scan_test.go` gained a canonical `config.Data`. Without
it the strict config rule would have rejected them before they reached the rule
they exist to test, silently hollowing out
`TestScanSubject_CountsMalformedPactoManifests` and the oversized-layer case.
No coverage was duplicated to raise the test count.

### RED

With the pre-fix validator restored (the three-field config comparison back, the
resolved-descriptor and inline-data cases removed) and the permanent tests as
committed:

```text
--- FAIL: TestValidateManifest_Rejects/config_inline_data_is_not_the_empty_JSON
--- FAIL: TestValidateManifest_Rejects/config_carries_no_inline_data
--- FAIL: TestValidateManifest_Rejects/config_carries_annotations
--- FAIL: TestValidateManifest_Rejects/config_carries_an_artifact_type
--- FAIL: TestValidateManifest_Rejects/config_carries_download_urls
--- FAIL: TestValidateManifest_Rejects/subject_media_type_is_not_the_resolved_one
--- FAIL: TestValidateManifest_Rejects/subject_size_is_not_the_resolved_one
--- FAIL: TestValidateManifest_Rejects/subject_inline_data_contradicts_its_digest
--- FAIL: TestStore_CommitFailsClosedOnSubjectDescriptorMismatch
```

each reporting `artifact_test.go:471: error = <nil>, want ErrInvalidArtifact`.
GREEN on the committed bytes, with `internal/evidenceoci` still at 100.0%
statement coverage.

### Mutation evidence

Six compiling semantic mutations, applied one at a time and reverted. None was a
build failure; every one produced failing assertions in named permanent tests.

| Mutation | Failing permanent tests |
|---|---|
| M1: config inline-data validation removed (the pre-change three-field comparison) | `TestValidateManifest_Rejects/config_inline_data_is_not_the_empty_JSON`, `/config_carries_no_inline_data`, `/config_carries_annotations`, `/config_carries_an_artifact_type`, `/config_carries_download_urls` |
| M2: subject media-type comparison removed | `TestValidateManifest_Rejects/subject_media_type_is_not_the_resolved_one` |
| M3: subject size comparison removed | `TestValidateManifest_Rejects/subject_size_is_not_the_resolved_one`, `TestStore_CommitFailsClosedOnSubjectDescriptorMismatch` |
| M4: subject inline-data validation removed | `TestValidateManifest_Rejects/subject_inline_data_contradicts_its_digest` |
| M5: the whole resolved-descriptor case removed | `TestValidateManifest_Rejects/subject_media_type_is_not_the_resolved_one`, `/subject_size_is_not_the_resolved_one`, `TestStore_CommitFailsClosedOnSubjectDescriptorMismatch` |
| M6: the `BuildArtifact` config clone removed, restoring the alias | `TestBuildArtifact_Shape` |

M1, M2 and M3 are the three the review required. M4, M5 and M6 cover the parts of
the repair the required three do not reach.

### What was deliberately not done

The accepted repairs are untouched. No signature verification was added to the
registry read path, replay semantics are unchanged, there is no background
readiness polling, cache or restart coupling, no bucket, PVC, recovery machinery
or `pkg/evidencestore`, no tag fallback, no second codec and no compatibility
storage, the single-writer `Recreate` model is unchanged, the Evidence DTOs and
the canonical scenario are unchanged, Compose still publishes natively and ORAS
remains only the transport library and the interoperability observer. No CI
wiring was weakened: the range touches no file under `.github/`, no `Makefile`,
no `release/` and no `scripts/`.

### Local verification

All run on the committed bytes at `bcd2d2b1`:

- `go test -race ./internal/evidenceoci ./pkg/evidenceingest ./internal/app -count=1` — ok.
- `internal/evidenceoci` at 100.0% statement coverage, at `bc0bb0dc` and at `bcd2d2b1`.
- `make ci` — exit 0, both modules at 100.0% total coverage, including
  `check-section` (zero U+00A7 in authored files), `ci-cyclo`, `ci-arch` and
  `test-acceptance-local`.
- `make artifact-drift` — OK.
- `make release-dry-run` — OK, with `STANDALONE-VERIFY OK` and `K8S-MODULE-STANDALONE OK`.
- `make test-acceptance-local` — operational-graph acceptance PASSED.
- `make test-acceptance-compose-selftest` — S12 and S13 PASS, SELFTEST OK.
- `make test-browser-compose` — both live legs 7 passed, clone-free Compose demo acceptance PASSED.
- `make test-acceptance-kind-evidence` — full in-cluster Evidence Server lifecycle acceptance PASSED.
- `make test-acceptance-kind-operational-graph` — 8 live product journeys passed, the full operational-graph vertical is UP.
- `make test-browser` — 219 passed.
- `govulncheck ./...` — no vulnerabilities found.
- `git diff --check` — clean.
- `make check-section` — zero U+00A7.

The suites that compete for the same ports were run sequentially. The
`integrations/kubernetes/charts/pacto-dev-gateway/README.md` helm-docs
regeneration appeared once during `make ci` and was restored, along with
`go.work.sum` churn; neither is part of the repair. The four inherited untracked
agent paths (`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`) were never touched.

### GitHub Actions at `bcd2d2b1`

CI run `32238379017` is successful with all 21 jobs green:

| Job | ID |
|---|---|
| changes | 96023620330 |
| ci-static | 96023680527 |
| ci-gates | 96023680825 |
| ci-engine | 96023680576 |
| ci-dashboard | 96023680658 |
| ci-integration-kubernetes | 96023680807 |
| ci-e2e-envtest | 96023680624 |
| ci-oci | 96023680824 |
| operator-build | 96023680667 |
| release-version-test | 96023680783 |
| artifact-drift | 96023680761 |
| release-dry-run | 96023680804 |
| dashboard-e2e | 96023680587 |
| ci-e2e-compose | 96023681115 |
| ci-e2e-kind (dashboard) | 96023680765 |
| ci-e2e-kind (evidence) | 96023680728 |
| ci-e2e-kind (observation) | 96023680822 |
| ci-e2e-kind (operational-graph) | 96023680756 |
| ci-e2e-kind (reconcile) | 96023680904 |
| ci-e2e-kind (upgrade) | 96023680865 |
| required | 96026581789 |

The other workflows at the same SHA: Security `32238379038` success (Trivy,
`Trivy (image)`, `govulncheck (Go)` and the PR security summary), Docs check
`32238379004` success, Pacto Contract CI `32238379071` success, Repowise
(architecture health) `32238379108` success, Validate PR title `32238379064`
success and the two CodeQL analysis runs `32238376007` and `32238376249` success
across all four languages (`actions`, `go`, `javascript-typescript`, `python`).
Rebuild dashboard UI `32238379016` and Auto-merge Dependabot PRs `32238378993`
are skipped as usual. The `github-code-quality` app produced no check run at this
SHA; it did at earlier heads, and its absence is on GitHub's side, not in the
workflow files, which this range does not touch.

Every check run at `bcd2d2b1` is success or skipped except one: the aggregate
`CodeQL` check from `github-advanced-security`, which is red for the inherited
reason below.

### CodeQL and review threads

Nine open alerts on the PR ref, the same nine as at `a017b63a`, none created by
this repair and none in a file it touches:

| Alert | Rule | File |
|---|---|---|
| 38 | py/incomplete-url-substring-sanitization | `release/scripts/docs_check.py:197` |
| 40-43 | go/path-injection | `internal/app/resolve.go:35,43,57,67` |
| 59-62 | go/path-injection | `pkg/oci/cache.go:375,394,395,666` |

The Phase 10C CodeQL delta for this repair is zero: nothing added, nothing
removed, and no alert anywhere under `internal/evidenceoci`.

Review threads were fully paginated, two pages: 199 total, 189 resolved, 10
unresolved. All ten are inherited and bot-authored — six from
`github-code-quality` dated 2026-08-12 on the generated Mermaid bundle
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, and four from
`github-advanced-security` dated 2026-08-13 on `pkg/oci/cache.go` lines 375, 394,
395 and 666. Neither path is in this range, so the Phase 10C thread delta is
zero. No comment was published, no thread resolved and no PR metadata changed.

### Status

**Phase 10C REMAINS CANDIDATE.** It is not CLOSED. Both counterexamples from
section 16.5 are fixed with permanent adversarial tests and compiling mutation
evidence, and local and remote verification are green, but closure requires
another independent review. Phase 11 has not started,
`PACTO_PR_TARGET_STATE.md` is untouched and no earlier section of this document
was edited.

### Current phase map

- Phases 1 through 10B: ACCEPTED and CLOSED.
- Phase 10C: CANDIDATE, repaired at `bcd2d2b1`, awaiting independent review.
- Phases 11 through 14: NOT STARTED.

## 16.7 GitHub Actions at the ledger head `a1ec9f13`

Section 16.6 records the remote state at the implementation head `bcd2d2b1`,
which is where it was written. This section names its own commit and records the
remote state at the branch head that carries it, as section 16.4 did for
section 16.3.

- Ledger SHA for section 16.6: `a1ec9f13e91337f76dd67a5c7a8edc81cb7737dd`.
- It changes exactly one file, `.pr-context/PACTO_PR_CURRENT_STATE.md`, appending
  section 16.6 and this section. No earlier section was edited and TARGET is
  untouched.
- PR 291 is OPEN, DRAFT and MERGEABLE at `a1ec9f13`.

CI run `32240250470` at `a1ec9f13` is successful with all 21 jobs green on the
first attempt, with no re-runs:

| Job | ID |
|---|---|
| changes | 96029013490 |
| ci-static | 96029082655 |
| ci-gates | 96029082806 |
| ci-engine | 96029082583 |
| ci-dashboard | 96029082530 |
| ci-integration-kubernetes | 96029082698 |
| ci-e2e-envtest | 96029082811 |
| ci-oci | 96029082759 |
| operator-build | 96029082827 |
| release-version-test | 96029082830 |
| artifact-drift | 96029082775 |
| release-dry-run | 96029082744 |
| dashboard-e2e | 96029082677 |
| ci-e2e-compose | 96029082700 |
| ci-e2e-kind (dashboard) | 96029082772 |
| ci-e2e-kind (evidence) | 96029082788 |
| ci-e2e-kind (observation) | 96029082792 |
| ci-e2e-kind (operational-graph) | 96029082771 |
| ci-e2e-kind (reconcile) | 96029082819 |
| ci-e2e-kind (upgrade) | 96029082866 |
| required | 96031941042 |

The other workflows at `a1ec9f13`: Security `32240250713` success, Docs check
`32240250446` success, Pacto Contract CI `32240250675` success, Repowise
(architecture health) `32240250418` success, Validate PR title `32240250466`
success and the two CodeQL analysis runs `32240245610` and `32240245700` success.
Rebuild dashboard UI `32240250490` and Auto-merge Dependabot PRs `32240250540`
are skipped as usual.

Exactly one check run at `a1ec9f13` is not success or skipped: the aggregate
`CodeQL` check from `github-advanced-security`, red for the same inherited nine
alerts (38, 40, 41, 42, 43, 59, 60, 61, 62). Review threads re-paginated at this
head are unchanged: 199 total, 189 resolved, 10 unresolved, six on the generated
Mermaid bundle and four on `pkg/oci/cache.go`. No comment was published, no
thread resolved and no PR metadata changed.

**Phase 10C REMAINS CANDIDATE.** Phase 11 has not started.

## 16.8 Independent review — Phase 10C ACCEPTED and CLOSED

Independent review range: `30267affffa292f25f50dc46b3ec48f277597045..40b602cb31907261fa216ad7db895092d6f45766`.

### Repository and history

- PR 291 is OPEN, DRAFT and MERGEABLE. Its remote head and the local branch are
  exactly `40b602cb31907261fa216ad7db895092d6f45766` on
  `feat/operational-graph-fleet`.
- `origin/main` and the merge-base remain
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
- `30267aff` remains an ancestor. The range is exactly four linear,
  single-parent commits: `bc0bb0dc`, `bcd2d2b1`, `a1ec9f13`, `40b602cb`.
  There is no merge, rebase, amended boundary or force-push evidence.
- The implementation portion changes exactly the five files declared in
  section 16.6, 233 insertions and 21 deletions. The two following commits only
  append the candidate and remote-evidence ledger. TARGET is unchanged.
- The worktree was clean apart from the four inherited untracked agent paths.
  The known helm-docs overwrite of the development-gateway README was produced
  by the local gate and restored; it is not part of this review commit.

### Findings

No blocking finding remains.

The two counterexamples from section 16.5 are closed at the actual read trust
boundary:

1. The config is compared as the complete canonical
   `ocispec.DescriptorEmptyJSON`, including inline data and optional descriptor
   fields. A descriptor with the old matching media type, digest and size but
   contradictory data or extra assertions is rejected.
2. The manifest subject is compared with both the configured immutable digest
   and the core media type, digest and size of the same contract descriptor
   already resolved and passed to `Referrers`. Contradictory inline data is also
   rejected. The scan threads that descriptor through; it does not re-resolve
   it or introduce a second registry round trip.

The validator remains one small codec and fails closed at store level: a
subject-descriptor mismatch is excluded from the read projection, makes health
partial, increments invalid artifacts and blocks the next commit with an
incomplete replay index. ORAS 1.1 packing remains accepted, so the stricter
rules do not create a Pacto-only artifact dialect. The permanent reject cases
and store-level test cover the two former blockers, and the compiling mutation
record names the tests that fail when each rule is removed.

One additional adversarial probe supplied `BuildArtifact` with a hand-crafted
descriptor whose digest matched the configured subject but whose optional
inline data contradicted it. That internal helper can then emit a manifest the
reader rejects. This is not a production blocker: its documented argument is
the registry-resolved contract descriptor, its sole production caller passes
the descriptor returned by the ORAS remote repository, and that resolver
constructs media type, digest and size from the registry response without
inline data. The probe was removed after observation and no product file was
changed. If `BuildArtifact` ever acquires another caller accepting arbitrary
descriptors, that caller must preserve the same precondition or move the
subject-binding check into the builder.

### Independent verification

- `go test -race ./internal/evidenceoci ./pkg/evidenceingest ./internal/app -count=1`
  passed.
- `make ci` passed on the reviewed bytes, including formatting, vet, cyclomatic
  complexity, both linters, architecture and release tests, 100.0% aggregate
  coverage in both Go modules, the complete integration suite, the whole
  acceptance subtree, the local operational-graph vertical, 1,232 frontend
  tests, Kubernetes envtest, 63 Helm tests, Kubernetes E2E and release
  orchestrator tests.
- `git diff --check` passed after restoring only the known generated README
  churn.
- At exact remote head `40b602cb`, CI run `32241896294` is successful: all 21
  jobs are green, including all six Kind shards, Compose E2E,
  artifact-drift, release-dry-run and the required aggregate. Security
  `32241896313`, Docs check `32241896290`, Pacto Contract CI `32241896250`,
  Repowise `32241896330`, Validate PR title `32241896198` and both CodeQL
  analysis workflows `32241889567` and `32241890372` are successful. The two
  expected workflows are skipped.
- The only failed check is the aggregate GitHub Advanced Security `CodeQL`
  check. The code-scanning API independently returns the same nine inherited
  alerts: 38, 40-43 and 59-62, in `release/scripts/docs_check.py`,
  `internal/app/resolve.go` and `pkg/oci/cache.go`. None is in this range; the
  Phase 10C security delta is zero.
- Review threads were independently paginated: 199 total, 189 resolved and 10
  unresolved. All ten are inherited and bot-authored: six on the generated
  Mermaid bundle and four on `pkg/oci/cache.go`. Neither path is touched here.
  No comment was published, no thread resolved and no PR metadata changed.

### Verdict and phase map

**Phase 10C is ACCEPTED and CLOSED.** The final descriptor-validation repair
closes blocker A without reopening the already accepted stored-envelope and
bounded-readiness repairs, weakening CI, reviving bucket/PVC persistence or
starting later work.

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11 — MCP catalog core: NOT STARTED and next.
- Phases 12 through 14: NOT STARTED.

## 17. Phase 11 record — OPENED

Phase 10C is ACCEPTED and CLOSED in section 16.8 above. This section opens
Phase 11 and is appended before any Phase 11 implementation commit.
`PACTO_PR_TARGET_STATE.md` is untouched. The starting SHA is `f466ca46`,
`origin/main` is `83f2e66d` and the merge-base is `83f2e66d`.

### Commission and exact next objective

Build the framework-independent **catalog core**: a bounded, immutable,
multi-root catalog over an explicitly supplied set of Pacto contract roots and
their dependency closure. The thesis being proved is that any finite set of
Pacto contract roots plus their dependency closure becomes a bounded,
discoverable, machine-readable catalog.

This phase defines and proves the catalog model only. It exposes nothing: no
MCP tools, no MCP resources, no CLI flag, no server route, no protocol E2E and
no public discovery-server documentation. Those belong to Phase 12. The
existing authoring, capability and operational Fleet MCP tools are unchanged.

The catalog is not another spelling of the operational Fleet. Fleet describes
runtime targets, observations and reconciliation for a deployment. The catalog
describes contract discovery from explicitly supplied roots. They share
completeness vocabulary and identity discipline; they do not share a model.

The core must deliver, and prove with permanent tests:

- explicit bounded roots, arbitrary supported root kinds, every requested root
  reference preserved, every root resolved to an immutable content identity,
  and invalid roots reported rather than silently dropped;
- canonical immutable identity that keeps the revision, the service identity,
  the requested reference and the resolved immutable reference distinct, with
  the exact OCI digest or a deterministic local content identity as the only
  content identity, never a mutable tag, bare version or service name;
- structured traversal provenance preserving every retained root-to-revision
  path, including diamonds and multi-root reachability, with structured
  identity components and structured path steps so hostile names containing
  `/`, `:`, `%` or arbitrary UTF-8 cannot collide, and a deterministic best
  rank of root, direct or transitive that never deletes a transitive path;
- explicit graph truth: resolved edges, unresolved dependencies with reasons,
  version and content conflicts, cycles, shared revisions, completeness and
  limitations, with partial knowledge distinct from both empty and complete;
- hard bounds on roots, revisions, edges, depth, retained paths, path length,
  unresolved entries, conflicts and limitations, which stop actual resolution
  work rather than slicing an unbounded result, and which are proven to stop it
  by counting resolver calls;
- immutable session semantics: every mutable reference resolved exactly once
  during construction, pure and network-free queries afterwards, deep copies at
  the boundary in both directions, deterministic ordering, an injected clock,
  and a `catalogId` stable for identical resolved content and topology
  regardless of `generatedAt` or root input order.

Existing responsibilities are reused, not re-created: contract parsing and the
bundle model, local and OCI reference parsing, OCI credential and cache policy,
immutable digest resolution, the graph and lock identity lessons, and the Fleet
completeness vocabulary. No second OCI configuration format, no registry
crawler, no persistent catalog database, no new Pacto configuration file, no
discovery daemon, no IDP adapter, no authorization policy, no execution or
proxy behaviour, no vector search, no marketplace and no extension framework.
Discovery is not authorization. Discovery is not execution.

An architecture test must make the boundary structural: the catalog core cannot
reach `internal/mcp`, `internal/cli`, `pkg/dashboard` or the Kubernetes
integration packages. The work joins the existing required local and GitHub CI
path with no parallel harness, no weakened job, no relaxed race detection,
timeout or coverage requirement.

### Current phase map

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11: ACTIVE / OPENED; implementation not started at this state commit.
- Phases 12 through 14: NOT STARTED.

Phase 12 must not start while Phase 11 is only a candidate. The PR remains an
open draft, and the append-only, no-history-rewrite and independent-review
protocol continues unchanged.

## 18. Phase 11 implementation — CANDIDATE at `6a0c198d`

The commission of section 17, implemented end to end. `pkg/catalog` is a new
public package that turns a finite, explicitly supplied set of Pacto contract
roots plus their dependency closure into a bounded, immutable, discoverable
catalog. It imports the contract model, `go-digest` and the standard library,
and nothing else. `internal/app` supplies the one adapter that gives it real
local and OCI resolution.

Nothing is exposed. There is no MCP tool, no MCP resource, no CLI flag, no
server route, no protocol E2E and no public discovery-server documentation.
The authoring, capability and operational Fleet MCP tools are byte-identical.

Phase 11 remains a CANDIDATE. A phase is closed by review, not by its author.
Sections 1 through 17 are unchanged, `PACTO_PR_TARGET_STATE.md` was not
touched, no PR comment was published, no review thread was resolved, no PR
metadata was changed, the PR is still an open draft, and Phase 12 is NOT
started.

### Range

Starting point `f466ca4669b43e673a934b84173ee41e374c65c2`, the branch head
carrying the section 17 commission, which was also the branch head on the
remote when work began. Final SHA `6a0c198de8f001a24650f29501b8fe2e831411de`.
`f466ca46` is an ancestor of `6a0c198d`; the range is exactly six linear
commits, each parented on the previous one, zero merges. 16 files, +3816 / -2,
of which the ledger commit is 1 file and +77. No rebase, amend, reset,
force-push, squash or rewrite. `origin/main` is still
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`, the merge-base is unchanged, and
main was not touched.

| SHA | Subject | Files | Delta |
|---|---|---|---|
| `26534f98` | docs: open the Phase 11 catalog-core record before implementation | 1 | +77 |
| `d14884a4` | feat(catalog): bounded immutable multi-root contract catalog core | 11 | +3006 |
| `3f72451c` | test(architecture): gate the catalog core against delivery-mechanism imports | 1 | +38 |
| `741205b1` | fix(catalog): resolve a reference under the constraint that declared it | 4 | +93 / -8 |
| `5a911669` | fix(oci): make an unsatisfiable tag request a sentinel, not prose | 1 | +9 / -2 |
| `6a0c198d` | feat(app): resolve catalog references through the existing OCI and local paths | 2 | +601 |

New files:

| Path | Lines | What it is |
|---|---|---|
| `pkg/catalog/catalog.go` | 209 | package doc, `SchemaVersion`, the completeness trio, twelve limitation codes, eight reason codes, `Limitation`, `Reason`, `ResolveError`, and the `Resolver` port with `ResolveRequest` / `Resolution` |
| `pkg/catalog/identity.go` | 151 | `ContentScheme`, `ContentID`, `ServiceID`, `RootID`, `DeclarationID`, `Path`, `Rank` and their total orders |
| `pkg/catalog/model.go` | 227 | `Root`, `Revision`, `Edge`, `Unresolved`, `Conflict`, `Cycle`, `Meta`, and the frozen `Catalog` with deep-copying accessors |
| `pkg/catalog/bounds.go` | 90 | nine bounds, their defaults, their ceilings and the clamp that produces the effective set |
| `pkg/catalog/build.go` | 605 | `Build`, the memoized breadth-first walk over arrivals, retention, expansion, cycle and conflict detection, and the freeze |
| `pkg/catalog/fingerprint.go` | 158 | the length-prefixed canonical encoding behind `Meta.CatalogID` |
| `pkg/catalog/harness_test.go` | 179 | the scripted resolver fake, call counters and construction helpers |
| `pkg/catalog/identity_test.go` | 227 | identity validation, ordering and encoding tests |
| `pkg/catalog/catalog_test.go` | 271 | immutability, determinism, fingerprint and concurrency tests |
| `pkg/catalog/build_test.go` | 733 | the adversarial acceptance cases |
| `pkg/catalog/walk_test.go` | 241 | second-route, constraint, reporting-bound and cycle-identity tests |
| `internal/app/catalog.go` | 153 | the adapter: reference parsing, registry access, credentials, caching, local bundle loading, content hashing and error categorisation |
| `internal/app/catalog_test.go` | 448 | adapter unit tests plus two end-to-end catalogs over a real registry and real directories |

Modified: `tests/architecture/boundary_test.go` (+38, the structural boundary
gate) and `pkg/oci/resolve.go` (+9 / -2, the `ErrNoMatchingTag` sentinel).

### The model, and why each piece exists

**Package boundary.** `pkg/catalog` is public and framework-independent by
construction. Its only non-standard imports are `pkg/contract` and
`github.com/opencontainers/go-digest`. Everything the catalog deliberately does
not own -- reference syntax, credentials, caching, registry access, filesystem
access -- lives behind the `Resolver` port. The repository already had a place
for exactly that adapter, so no new boundary was invented: `internal/app`
already owns the bundle store, the credential policy and the local-path
resolution the lock builder uses, and `Service.CatalogResolver()` reuses them.
A catalog and a lockfile therefore cannot disagree about what a reference
resolves to.

**Explicit roots.** `Build` takes `Request.Roots`, clones it and rejects an
empty set with `ErrNoRoots`. Nothing infers a root, and nothing lists a registry
catalog: the only registry calls the adapter can make are `ResolveRef` on a
reference a contract or a caller named, and `Pull` on the reference that
resolved. Every requested root within `MaxRoots` appears in `Roots()` whether or
not it resolved, carrying its verbatim `RequestedRef` and, when it failed, its
`Reason`. `Meta.RequestedRoots` always reports the true request size, so even a
root the bound refused to resolve is counted rather than hidden.

**Identity, in three separate layers.** `RequestedRef` is the text a caller or a
contract used. `ResolvedRef` is the immutable reference the resolver returned --
a digest-pinned registry reference. `ContentID` is the only identity: a
comparable struct of a closed-enum `ContentScheme` (`oci` or `local`) and a
digest validated through `digest.Parse`. A tag, a bare version and a service
name are structurally incapable of reaching it, and `project` rejects any
resolution whose content identity does not validate, with
`INVALID_IDENTITY`. `ServiceID` is domain-qualified and explicitly descriptive:
two revisions sharing a name and a version but not their bytes stay two
revisions and become a `content` conflict.

**Deduplication without provenance loss.** Revisions are keyed on `ContentID`.
The same bytes reached through two roots, two reference texts or two routes are
one `Revision` accumulating every `RequestedRefs` entry, every `ResolvedRefs`
entry, every reaching `Roots` ordinal and every retained `Path`.

**Structured provenance.** A `Path` is a `RootID` plus an ordered slice of
`DeclarationID`, and a `DeclarationID` is the declaring `ContentID` plus the
declaration's index in that contract's declared order. Nothing anywhere joins
user-controlled text with a delimiter to make an identity or a path step, so a
service, domain or reference containing `/`, `:`, `%` or arbitrary UTF-8 cannot
collide with its neighbour. The walk is over arrivals rather than revisions,
which is what makes both branches of a diamond survive. `Rank` is derived from
`MinDepth` across every retained path -- root, direct, transitive -- so a
revision reachable both ways ranks direct while keeping the transitive path.

**Graph truth.** `Edges()` carries the declaration, its declared name, reference,
constraint and required flag, and the content it resolved to, kept separate from
the revision because one revision can be declared by many contracts under many
names. `Unresolved()` is knowledge about a gap, with a sanitized reason code.
`Conflicts()` reports three distinct shapes and resolves none of them: `version`
(one service at several versions), `content` (one service and version at several
contents) and `declaration` (one declaration resolving to several contents).
`Cycles()` records loops rotated to their smallest identity, so one loop found
from two entry points is one cycle; the closing edge is kept so the loop stays
visible in the graph while the walk stops instead of following it.
`Completeness` is `complete` only when nothing failed and no bound stopped any
work; anything that produced a limitation makes it `partial`. `empty` exists as
a value and is never produced by `Build`, precisely so partial knowledge can
never be served as an authoritative empty result.

**Bounds that stop work.** Nine bounds, each with a default and a ceiling, all
reported back as `Meta.Bounds` so what actually applied is reviewable. Six of
them stop work before it is paid for: `MaxRoots` truncates the queue before any
root is resolved; `MaxRevisions` and `MaxEdges` are checked in `mayResolve`,
which runs before `callResolver`; `MaxDepth` and `MaxPathLength` stop `expand`
from ever enqueueing the deeper arrivals; `MaxPaths` stops a revision being
entered again, so its subtree is not re-walked. Each emits a structured
limitation naming the bound and marks the answer partial. The remaining three --
`MaxUnresolved`, `MaxConflicts`, `MaxLimitations` -- bound derived reporting
only, and the file says why: refusing to resolve because other references
already failed would hide healthy parts of the closure. Every work bound has a
permanent test that counts resolver calls.

**Immutable session.** Construction resolves each distinct `(Base, Ref,
Constraint)` triple at most once, so a mutable tag is read once and a registry
that moves afterwards does not move the catalog. `Build` clones the root slice
on the way in; `project` keeps only durable values and drops the contract
pointer and any filesystem view; every accessor deep-copies on the way out. The
clock is injected through `Request.Clock`. `Meta.CatalogID` fingerprints what
the catalog found -- completeness, effective bounds, root outcomes, revisions,
edges, unresolved entries, conflicts, cycles and limitation codes -- and
deliberately excludes generation time, root ordinals, retained paths, requested
references and local base directories, so it is stable across root permutations,
across injected times and across the same content living at a different path.
Every field is length-prefixed with an eight-byte big-endian length before
hashing, so no two field sequences can produce one byte stream.

**Nothing speculative was built.** No second OCI configuration format, no
registry crawler, no persistent database, no new Pacto configuration file, no
daemon, no IDP adapter, no authorization policy, no execution or proxy
behaviour, no vector search, no marketplace and no extension framework. The
package doc states the two exclusions the commission named: discovery is not
authorization and discovery is not execution.

### Two defects the work found, and their root-cause fixes

`741205b1`. The port as first written passed only `(Ref, Base)` to the resolver.
A dependency reference that names no version -- the ordinary shape of an OCI
dependency in this repository -- would then have resolved to whatever tag ranked
highest rather than to what the declaring contract accepts, and two declarations
that genuinely disagree about a version range would have collapsed into one
silently selected answer instead of surfacing as a conflict. `Constraint` is now
part of the request and part of the memo key.
`TestTheDeclaringConstraintReachesTheResolver` proves the constraint arrives and
that a root, which nobody declares, arrives with none;
`TestTheSameBareReferenceUnderTwoConstraintsIsTwoQuestions` proves the same bare
reference under two constraints is two questions, two revisions and one visible
`version` conflict.

`5a911669`. `oci.BestTag` reported tag exhaustion as an untyped `fmt.Errorf`, so
the adapter could only have told "the registry holds nothing for you" from "the
registry could not be asked" by matching message text. The fix is a sentinel,
`oci.ErrNoMatchingTag`, wrapped by both exhaustion returns; the adapter uses
`errors.Is`. This was fixed where every caller routes through rather than
string-matched at the one call site. The only existing test that mentions the
message text constructs its own error and is unaffected.

### The adapter, and the boundary it holds

`internal/app/catalog.go` parses a reference with the existing
`graph.ParseDependencyRef`, then splits. A registry reference goes through the
existing `resolveDigest` plus `BundleStore.Pull`, yielding
`ContentID{Scheme: oci, Digest: <manifest digest>}` and a digest-pinned
`ResolvedRef`. A local reference goes through the existing `resolveLocalPath`
and `loadLocalBundle`, and its identity is `lock.HashFS` over the bundle's whole
file set -- the same hash the lockfile uses -- so two byte-identical directories
are one revision and two directories claiming the same name and version but
different bytes are two.

The `Base` a resolution reports is the context its own relative references
resolve against, and it carries a security decision. An OCI resolution reports
the constant `oci://`. `catalogLocalDir` refuses any local reference whose base
is that constant, so a contract fetched over the network cannot make the catalog
read a local directory of its choosing -- relative or absolute -- exactly as the
lock builder already refuses. A local base is always an absolute path, so the
two can never be confused. A root arrives with an empty base and its relative
path resolves against the working directory.
`TestCatalogResolverRefusesALocalReferenceDeclaredByARegistryBundle` covers
`../../../etc`, `./neighbour`, an absolute directory and a `file://` reference;
`TestCatalogRefusesALocalPathDeclaredByARegistryRoot` proves the same at the
catalog level, where the result is partial with one `INVALID_REFERENCE`
unresolved entry rather than a traversal.

`catalogFailure` reduces every registry failure to one of the catalog's reason
categories and never echoes the underlying error, because a registry error
carries the host, the repository path and, on a rejected credential, the account
it was rejected for. `TestCatalogResolverCategorisesRegistryFailures` drives all
seven categories through a table and then asserts the sanitized message contains
none of the hosts, none of the account text and none of the transport prose.
`pkg/catalog` enforces the same rule from its side: any error that is not a
`*ResolveError` is reduced to a fixed generic reason, proved by
`TestUnsanitizedResolverErrorsAreNotEchoed`.

### Adversarial acceptance cases, all permanent

| Commissioned case | Permanent test |
|---|---|
| 1. two independent roots, distinct direct dependencies | `TestTwoIndependentRootsKeepTheirOwnDependencies` |
| 2. two roots sharing one immutable transitive revision | `TestSharedTransitiveRevisionKeepsBothProvenancePaths` |
| 3. a diamond preserving every path | `TestDiamondPreservesEveryPath` |
| 4. a cycle that terminates and stays visible | `TestCycleTerminatesAndStaysVisible`, `TestOneLoopFoundFromTwoRootsIsOneCycle`, `TestSeparateLoopsAreSeparateCycles` |
| 5. unresolved root and unresolved transitive dependency are partial | `TestUnresolvedRootAndDependencyArePartialNotEmpty`, with `TestFullyResolvableClosureIsComplete` as its complement |
| 6. same name and version, different digests, still distinct | `TestSameNameAndVersionWithDifferentDigestsStayDistinct` |
| 7. tag and digest pin for one content deduplicate, both references visible | `TestTagAndDigestForOneContentDeduplicateButKeepBothReferences` |
| 8. a tag whose registry answer changes after construction | `TestMutableTagIsResolvedOnceAndTheSessionDoesNotMove` |
| 9. the same relative text from two local roots resolves against its declarer | `TestRelativeDependencyResolvesAgainstItsDeclaringBase`, `TestCatalogResolverResolvesARelativeReferenceAgainstItsDeclarer` |
| 10. hostile names with `/`, `:`, `%` and UTF-8 | `TestHostileNamesDoNotCollide`, `TestHostileDeclarationsAndPathsDoNotCollide`, `TestEncodeCannotBeForgedByFieldContents` |
| 11. direct and transitive reachability ranks direct, keeps every path | `TestRevisionReachableDirectlyAndTransitivelyRanksDirect`, with `TestTransitiveRankIsReachable` |
| 12. root, revision, edge, depth and path limits stop resolver work | `TestRootBoundStopsResolverWork`, `TestRevisionBoundStopsResolverWork`, `TestEdgeBoundStopsResolverWork`, `TestDepthBoundStopsResolverWork`, `TestPathLengthBoundStopsResolverWork`, `TestRetainedPathBoundStopsResolverWork`, `TestTheEdgeBoundAlsoStopsEdgesThatCostNoResolution` |
| 13. caller mutation of inputs and of returned values | `TestCallerCannotMutateTheCatalogThroughItsInputs`, `TestCallerCannotMutateTheCatalogThroughReturnedValues` |
| 14. deterministic ordering, stable `catalogId` across permutations and times | `TestOrderingIsDeterministic`, `TestCatalogIDIgnoresRootOrderAndGenerationTime`, `TestCatalogIDDistinguishesDifferentCatalogs` |
| 15. conflicting constraints or resolutions stay visible | `TestConflictsStayVisible`, `TestConflictingConstraintsStayAttachedToTheirDeclaration`, `TestTheSameBareReferenceUnderTwoConstraintsIsTwoQuestions` |

Each of the six work bounds is proved by counting resolver calls, not by
measuring output size. The three reporting bounds are proved to cap their list
AND to say they did: `TestTheUnresolvedBoundCapsReportingWithoutHidingThatItDid`,
`TestTheConflictBoundCapsReportingAndSaysSo` and
`TestTheLimitationBoundLeavesRoomForItsOwnMarker`.

Beyond the commissioned list, permanent coverage also includes: safety for
concurrent readers under the race detector; the completeness vocabulary asserted
equal to `pkg/fleet`'s, so the two cannot drift apart silently; rejection of a
request with no roots and of one with no resolver; cancellation recorded as
partial knowledge; an empty reference rejected without calling the resolver;
resolver output validated before it can become identity; the effective bounds
reported in `Meta`; and a revision lookup miss distinguished from an empty
revision.

The `internal/app` side adds two catalogs built over a real in-process registry
and real temporary directories: `TestCatalogOverARealRegistryAndRealDirectories`
builds a complete four-revision catalog from a local root and an OCI root that
share one transitive library, and asserts the shared revision carries two roots,
two paths and rank direct.

### Mutation evidence

Six mutations were applied one at a time, observed to fail a permanent test,
and reverted byte-identically.

| Mutation | What bit |
|---|---|
| M1 drop canonical deduplication (never reuse an existing revision) | eight failures: `TestSharedTransitiveRevisionKeepsBothProvenancePaths`, `TestDiamondPreservesEveryPath`, `TestTagAndDigestForOneContentDeduplicateButKeepBothReferences`, `TestRelativeDependencyResolvesAgainstItsDeclaringBase`, `TestRevisionReachableDirectlyAndTransitivelyRanksDirect`, `TestRetainedPathBoundStopsResolverWork`, `TestASecondRouteDoesNotDuplicateEdgesOrGaps`, `TestOneLoopFoundFromTwoRootsIsOneCycle` |
| M2 keep only the first path per revision | six failures: `TestSharedTransitiveRevisionKeepsBothProvenancePaths`, `TestDiamondPreservesEveryPath`, `TestTagAndDigestForOneContentDeduplicateButKeepBothReferences`, `TestRelativeDependencyResolvesAgainstItsDeclaringBase`, `TestRevisionReachableDirectlyAndTransitivelyRanksDirect`, `TestASecondRouteDoesNotDuplicateEdgesOrGaps` |
| M3 write the memo but never read it | four failures: `TestSharedTransitiveRevisionKeepsBothProvenancePaths`, `TestMutableTagIsResolvedOnceAndTheSessionDoesNotMove`, `TestASecondRouteDoesNotDuplicateEdgesOrGaps`, `TestOneReferenceRequestedTwiceIsOneLimitation` |
| M4 turn the revision bound into a post-hoc slice, still emitting its limitation | `TestRevisionBoundStopsResolverWork` only, and only on its two work-proving assertions: `resolver calls = 3, want 2: the third root is refused before any fetch` and `root 2 = {... Resolved:true ...}, want it reported as stopped by a bound` |
| M5 call an answer complete unless it is also empty | three failures: `TestUnresolvedRootAndDependencyArePartialNotEmpty`, `TestRootBoundStopsResolverWork`, `TestTheEdgeBoundAlsoStopsEdgesThatCostNoResolution` |
| M6 return the internal revision slice instead of a deep copy | `TestCallerCannotMutateTheCatalogThroughReturnedValues` |

M4 is the one worth reading twice. Under it the revision count was still right
and the `REVISION_LIMIT_EXCEEDED` limitation was still emitted -- a suite that
only inspected output would have stayed green. What failed was the resolver call
count and the root's reported outcome. That is the difference between a bound
that stops work and a bound that slices a result already paid for, and it is
exactly the distinction the commission asked to be proven.

### Architecture gate

`tests/architecture/boundary_test.go` gained
`TestCatalogCoreIsFrameworkIndependent`, which walks the real dependency graph
of `pkg/catalog/...` and fails on any import of `internal/mcp`, `internal/cli`,
`pkg/dashboard`, either `integrations/` prefix, `k8s.io/` or `sigs.k8s.io/`, and
additionally on any in-repository package outside an explicit allow-map of
`pkg/catalog` and `pkg/contract`. The allow-map is the stricter half: a new
in-repo import fails the gate even if it is not on the forbidden list, so the
boundary cannot erode by accident. `pkg/catalog/...` was also added as the first
entry of the existing `corePackages` list, so it is covered by
`TestCorePackagesHaveNoKubernetesOrIntegrationDeps` too.

The work runs inside the existing required local and GitHub CI path. No job,
harness, timeout, race setting or coverage threshold was added, weakened or
bypassed: both new packages are covered by the single workspace `-race` run that
enforces exactly 100.0% total coverage, and the architecture test runs in the
existing `ci-gates` job.

### Local verification, all green

`go test -race ./pkg/catalog/` (ok, coverage 100.0%); `go test -race` across
`./internal/app/`, `./pkg/oci/`, `./pkg/catalog/`, `./pkg/lock/`, `./pkg/graph/`
and `./pkg/fleet/` (all ok; `internal/app` at 100.0%); `make ci-test`
(`total coverage: 100.0%`, plus `DEMO-CONTRACTS VALID: 24/24`); `gofmt -l`
clean; `golangci-lint run ./internal/app/... ./pkg/oci/... ./pkg/catalog/...`
(0 issues); `gocyclo -over 15 ./pkg/catalog` (exit 0); `make ci` (exit 0);
`make artifact-drift` (`artifact-drift: OK`); `make release-dry-run`
(`K8S-MODULE-STANDALONE OK`, `RELEASE-DRY-RUN OK`); `govulncheck ./...`
(`No vulnerabilities found`); `git diff --check` clean; `make check-section`
(zero U+00A7 in authored files).

### GitHub at the exact final SHA `6a0c198d`

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE, head
`6a0c198de8f001a24650f29501b8fe2e831411de`, base `main`. Eight workflow runs
were triggered; six succeeded and two were skipped by their own conditions.

| Workflow | Run ID | Attempt | Conclusion |
|---|---|---|---|
| CI | `32259731356` | 1 | success |
| Security | `32259731600` | 1 | success |
| Docs check | `32259730428` | 1 | success |
| Pacto Contract CI | `32259730554` | 1 | success |
| Repowise (architecture health) | `32259730045` | 1 | success |
| Validate PR title | `32259729778` | 1 | success |
| Rebuild dashboard UI | `32259731821` | 1 | skipped |
| Auto-merge Dependabot PRs | `32259730347` | 1 | skipped |

Run `32259731356` (CI) is success on attempt 1; all 21 jobs are green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `96089831140` | success |
| `ci-static` | `96089912947` | success |
| `operator-build` | `96089912979` | success |
| `dashboard-e2e` | `96089913028` | success |
| `ci-integration-kubernetes` | `96089913070` | success |
| `ci-e2e-envtest` | `96089913072` | success |
| `release-dry-run` | `96089913078` | success |
| `ci-e2e-compose` | `96089913113` | success |
| `ci-e2e-kind (dashboard)` | `96089913132` | success |
| `ci-e2e-kind (observation)` | `96089913136` | success |
| `ci-engine` | `96089913175` | success |
| `ci-oci` | `96089913192` | success |
| `ci-e2e-kind (evidence)` | `96089913193` | success |
| `release-version-test` | `96089913215` | success |
| `ci-gates` | `96089913228` | success |
| `ci-e2e-kind (operational-graph)` | `96089913233` | success |
| `artifact-drift` | `96089913237` | success |
| `ci-e2e-kind (upgrade)` | `96089913244` | success |
| `ci-dashboard` | `96089913254` | success |
| `ci-e2e-kind (reconcile)` | `96089913272` | success |
| `required` | `96093570489` | success |

The Security workflow's three jobs are green: `govulncheck (Go)`
`96089831612`, `Trivy (image)` `96089831849` and `PR security summary`
`96090360488`.

The isolated `ci-e2e-kind (dashboard)` runner failure disclosed against the
Phase 10C ledger head did NOT reproduce: that job is success on attempt 1 here.
Nothing about the image-presence verification was touched.

### CodeQL and review threads

Nine code-scanning alerts are open on `refs/pull/291/head`, and they are exactly
the nine inherited ones: `38` (`py/incomplete-url-substring-sanitization`,
`release/scripts/docs_check.py:197`), `40` through `43` (`go/path-injection`,
`internal/app/resolve.go` lines 35, 43, 57 and 67) and `59` through `62`
(`go/path-injection`, `pkg/oci/cache.go` lines 375, 394, 395 and 666). The
newest was created on 2026-08-13. **The Phase 11 delta is ZERO new alerts**, and
in particular the new filesystem handling in `internal/app/catalog.go` raised
none. Every individual `Analyze (...)` check run on this SHA is success; the
aggregate `CodeQL` check from `github-advanced-security` is still failure,
because it aggregates those nine pre-existing alerts. That is the inherited
condition and is not claimed as a Phase 11 finding.

All 199 review threads were paginated, since the API caps a page at 100 and page
one alone hides every unresolved one. The totals are unchanged from the Phase
10C record: 199 total, 189 resolved, 10 unresolved. The ten are six on a
generated mermaid bundle under `pkg/dashboard/ui/assets/` and four on
`pkg/oci/cache.go` at lines 375, 394, 395 and 666, the same locations as the
`pkg/oci` CodeQL alerts. Neither file was touched by Phase 11. **The Phase 11
review-thread delta is ZERO.** No thread was resolved and no comment was
published.

### Hygiene and disclosures

- `make ci` regenerates `integrations/kubernetes/charts/pacto-dev-gateway/README.md`
  from helm-docs, overwriting the hand-written file. As in Phase 10C it was
  reverted rather than committed. The generator quirk predates this phase and is
  not fixed here.
- `go.work.sum` gained hashes from ordinary local `go` invocations, including
  running `gocyclo` through `go run`. Reverted twice; the committed file is
  unchanged and sufficient.
- A stale editor diagnostic claimed an undefined test helper in
  `pkg/catalog/build_test.go`. A clean `go test ./pkg/catalog/` proved it stale;
  nothing was changed on its account.
- The push required switching the active `gh` account to the one with write
  access. No other GitHub state was touched: no comment, no thread resolution,
  no metadata change, no label, no review request.
- `git fetch` brought down a new `integrations/kubernetes/v5.1.2` tag published
  from main. It is unrelated to this branch and nothing was done with it.
- The untracked agent files (`.claude/`, `.codex/`, `.mcp.json`, `AGENTS.md`)
  are untouched and still untracked. `git status --short` at the final SHA lists
  those four and nothing else.

### Deliberately not done

No MCP catalog tool, no MCP resource registration, no `pacto mcp --root` wiring,
no stdio or protocol E2E and no public discovery-server documentation: all of
that is Phase 12. The existing authoring, capability and operational Fleet MCP
tools were not read from, changed or removed. Phase 10C was not reopened and
evidence storage was not modified; no new correctness, security or data-loss
counterexample against it was found.

### Verdict

Phase 11 is a CANDIDATE at `6a0c198d`, awaiting independent review. It is NOT
closed -- closing it is the reviewer's act, not the author's. Phase 12 has NOT
started.

### Current phase map

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11: CANDIDATE at `6a0c198d`, awaiting independent review.
- Phases 12 through 14: NOT STARTED.

Phase 12 must not start while Phase 11 is only a candidate. The PR remains an
open draft, and the append-only, no-history-rewrite and independent-review
protocol continues unchanged.

## 18.1 GitHub Actions at the ledger head `42ed66ea`

`42ed66ea5f407583a72ab787f2af8c9ddfad517b` adds section 18 above and changes
nothing else: one file, `.pr-context/PACTO_PR_CURRENT_STATE.md`. It is recorded
here for the reviewer because the section it contains could not describe its own
commit.

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE at this head, base `main`.
Eight workflow runs were triggered; six succeeded and two were skipped.

| Workflow | Run ID | Attempt | Conclusion |
|---|---|---|---|
| CI | `32261859844` | 1 | success |
| Security | `32261859813` | 1 | success |
| Docs check | `32261859857` | 1 | success |
| Pacto Contract CI | `32261859861` | 1 | success |
| Repowise (architecture health) | `32261859805` | 1 | success |
| Validate PR title | `32261859842` | 1 | success |
| Rebuild dashboard UI | `32261859843` | 1 | skipped |
| Auto-merge Dependabot PRs | `32261859848` | 1 | skipped |

Run `32261859844` (CI) is success on attempt 1, all 21 jobs green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `96096756301` | success |
| `ci-static` | `96096814606` | success |
| `ci-integration-kubernetes` | `96096814615` | success |
| `release-dry-run` | `96096814692` | success |
| `dashboard-e2e` | `96096814722` | success |
| `ci-e2e-envtest` | `96096814731` | success |
| `ci-engine` | `96096814751` | success |
| `ci-e2e-compose` | `96096814783` | success |
| `ci-dashboard` | `96096814787` | success |
| `release-version-test` | `96096814820` | success |
| `operator-build` | `96096814835` | success |
| `ci-gates` | `96096814886` | success |
| `ci-e2e-kind (observation)` | `96096814888` | success |
| `artifact-drift` | `96096814891` | success |
| `ci-e2e-kind (dashboard)` | `96096814900` | success |
| `ci-oci` | `96096814944` | success |
| `ci-e2e-kind (operational-graph)` | `96096814968` | success |
| `ci-e2e-kind (reconcile)` | `96096814978` | success |
| `ci-e2e-kind (upgrade)` | `96096815061` | success |
| `ci-e2e-kind (evidence)` | `96096815102` | success |
| `required` | `96100219052` | success |

Security's three jobs are green: `Trivy (image)` `96096755793`,
`govulncheck (Go)` `96096756267` and `PR security summary` `96097227764`.

Code scanning on `refs/pull/291/head` still shows exactly the nine inherited
alerts `38`, `40` through `43` and `59` through `62`, so the delta remains ZERO.
Review threads, fully paginated, remain 199 total, 189 resolved and 10
unresolved, unchanged and untouched.

Phase 11 remains a CANDIDATE at implementation SHA `6a0c198d`, with this ledger
head at `42ed66ea`. Phase 12 has NOT started.

## 18.2 Independent review at `bf9070a0` — Phase 11 narrowly reopened

Independent review date: 2026-08-19. Reviewed implementation range:
`f466ca4669b43e673a934b84173ee41e374c65c2..6a0c198de8f001a24650f29501b8fe2e831411de`.
Reviewed remote ledger head:
`bf9070a04f160eca61c8c852bedc659fd34bee87`.

### Repository, history and external state

- PR 291 is OPEN, DRAFT and MERGEABLE on `feat/operational-graph-fleet`; local
  HEAD, the remote branch and the PR head were exactly `bf9070a0` before this
  review record.
- `origin/main` and the merge-base remain
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`. `f466ca46` remains an ancestor.
  The range is exactly eight linear, single-parent commits, in the order
  recorded by the handoff: six implementation/commission commits followed by
  the candidate ledger and its CI record. There is no merge or evidence of an
  amended, rebased or force-pushed boundary.
- The implementation range changes 16 files, 3,816 insertions and two
  deletions. The complete range through `bf9070a0` changes the same 16 product
  and test files plus 566 appended ledger lines, 4,305 insertions and two
  deletions. TARGET, Make and workflow files are untouched.
- CI run `32263484274` at exact head `bf9070a0` succeeded on attempt 1: all 21
  jobs, including `ci-engine`, `ci-gates`, Compose, every Kind shard,
  artifact-drift, release-dry-run and `required`, are green. Security
  `32263484758`, Docs check `32263486167`, Contract CI `32263484459`, Repowise
  `32263485004`, PR-title `32263484340` and every individual CodeQL analysis
  are also successful; the two conditional workflows are skipped.
- The code-scanning API independently returns the same nine inherited alerts:
  38, 40 through 43 and 59 through 62. None is in the Phase-11 range. The
  aggregate CodeQL check remains failed for those inherited alerts.
- Review threads were independently paginated: 199 total, 189 resolved and 10
  unresolved. The ten remain the six generated-Mermaid threads and four
  `pkg/oci/cache.go` threads; the Phase-11 delta is zero.

### Accepted implementation — do not redesign without a new counterexample

The broad shape is correct and is not reopened:

- `pkg/catalog` is a framework-independent core behind one small Resolver port;
  the architecture gate enforces the intended import boundary.
- `internal/app` reuses the existing local/OCI parsing, credential, cache,
  digest-pinning and lock content-hash responsibilities. Registry contracts
  cannot cause local filesystem reads, and failure text is reduced to typed,
  sanitized reasons.
- requested reference, resolved reference and immutable content are separate;
  declaration occurrences and paths are structured rather than delimiter
  identities; diamond and multi-root paths, cycles, unresolved entries,
  ordinary version/content/declaration conflicts, ranks and deep-copy session
  immutability are all permanently tested.
- mutable `(Base, Ref, Constraint)` questions are memoized once per build;
  queries after construction are pure and network-free. The declaring
  constraint reaches the adapter, and the new `oci.ErrNoMatchingTag` sentinel
  is the correct small shared fix.
- roots, revisions, depth, path length and retained-path bounds have genuine
  call-count proofs for the cases the suite covers. Reporting bounds remain
  explicit and honest. No Phase-12 delivery mechanism has been started.

Those accepted pieces do not overcome the following three concrete blockers.

### Blocker A — `MaxEdges` does not bound failing dependency work

Counterexample reproduced with a temporary test against the actual core:

1. one resolvable root declares four distinct dependencies;
2. every dependency returns `NOT_FOUND`;
3. build with `Bounds{MaxEdges: 2}`.

The observable result is five resolver calls -- the root plus all four failed
dependencies -- rather than at most three. No `EDGE_LIMIT_EXCEEDED` limitation
is emitted. `mayResolve` compares `len(b.edges)` with `MaxEdges`, but only a
successful resolution reaches `recordEdge`; failures never consume the budget.
`MaxRevisions` does not help because failed references add no revision. A
single contract with an arbitrarily large failing fan-out therefore causes an
arbitrarily large number of registry calls despite the advertised hard bounds.
`expand` also materializes every arrival before the limit can reject any, so
queue allocation and dequeue work follow the unbounded declaration count.

This contradicts the Phase-11 invariant that hard bounds stop actual work and
the package claim that a hostile closure performs a bounded number of
resolutions. The permanent successful-edge test misses it because every
admitted dependency increments `b.edges`.

The repair must budget dependency work independently of successful outcomes
and must avoid first constructing an unbounded surplus queue. A permanent
adversarial test must use distinct failing dependencies, count resolver calls,
prove the surplus is never attempted, prove the answer is partial and prove the
limitation says work was stopped. Preserve the existing memo-hit edge-bound
case and the distinction between work bounds and reporting bounds.

### Blocker B — content-only deduplication makes service domain depend on root order

Counterexample reproduced twice: first with the core resolver fake, then with
Pacto's real adapter and a real in-process OCI registry.

1. publish the same deterministic Pacto bundle as
   `<registry>/alpha/api:1.0.0` and `<registry>/beta/api:1.0.0`;
2. both repositories resolve to the same OCI manifest digest;
3. build once with roots `[alpha, beta]` and once with `[beta, alpha]`.

`builder.revs` is keyed only by `ContentID`, so the two domain-qualified
services collapse into one revision. `Revision.Service` is never reconciled
after the first arrival: the first build says the revision belongs to domain
`<registry>/alpha`, the second says `<registry>/beta`. The two builds also
produce different `catalogId` values despite differing only by root order.

This violates three already-settled invariants at once: Service identity is
domain-qualified end to end, one Contract Revision belongs to one Service, and
root permutation cannot change catalog truth or `catalogId`. OCI digests are
content identities; publishing identical content in another repository is a
normal mirror, not authority to erase its domain-qualified Service identity.

The repair must define canonical revision identity so content deduplication
never cross-contaminates distinct `ServiceID`s. It may refine the identity
model, but must keep content identity itself as the exact digest/hash and must
not regress same-service tag-plus-digest deduplication. Permanent tests must
include the real two-repository registry case, both root orders, stable output
truth and stable `catalogId`; the two domains must remain distinguishable and
must not be reported as a same-service conflict.

### Blocker C — different unresolved root sets share one `catalogId`

Counterexample reproduced against the core:

- catalog A requests only `reg/missing-a:1` and receives `NOT_FOUND`;
- catalog B requests only `reg/missing-b:1` and receives `NOT_FOUND`.

Both partial catalogs receive the exact same `catalogId`. For an unresolved
root the requested reference is the only identity available, but the
fingerprint excludes it; root outcomes encode only `Resolved=false`, zero
content and the reason code, and limitation references are excluded too.
These are not identical catalogs or identical unresolved topology merely
because both failed in the same category.

The repair must make distinct unresolved root multisets produce distinct IDs
without losing root-order invariance. Preserve the intended stability when two
different requested references genuinely resolve to the same immutable
same-service catalog. Permanent tests must cover different unresolved roots,
permutations of the same unresolved set and the existing resolved tag/digest
case.

### Independent local verification and hygiene

- The temporary counterexample tests failed exactly as described and were
  deleted after observation. A real-registry mirror produced one deterministic
  digest in two repositories and reproduced the order-dependent Service domain
  and `catalogId`; no product or test file from those probes remains.
- `go test -race ./pkg/catalog ./internal/app ./pkg/oci -count=1` passed after
  removing the probes.
- Full `make ci` passed on the reviewed product bytes: architecture/release
  gates, aggregate 100.0% Go coverage with race detection, complete integration
  and acceptance subtrees, the local vertical, 1,232 frontend tests, 100.0%
  Kubernetes-module coverage, 63 Helm tests, envtest and release orchestrator.
  The known helm-docs overwrite of the development-gateway README was restored
  and is not part of this review record.
- `git diff --check` and `make check-section` passed. The worktree returned to
  only the four inherited untracked agent paths before this ledger update.

### Verdict and exact next objective

**Phase 11 is NARROWLY REOPENED and is not closed.** The implementation is a
strong candidate, but a supposedly bounded catalog can perform unbounded
failing resolution work, domain-qualified Service truth is order-dependent for
a real OCI mirror, and `catalogId` aliases distinct unresolved root sets.

The next iteration is a Phase-11-only repair of blockers A, B and C. It must
begin from this review ledger head, retain every accepted boundary above, add
permanent red/green adversarial tests plus mutation proof for each former
failure, run the complete required verification, append a new CANDIDATE record
and stop. Phase 12 must not start.

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11: NARROWLY REOPENED on blockers A, B and C.
- Phases 12 through 14: NOT STARTED.

## 18.3 Phase 11 narrow closure repair — CANDIDATE at `ee9b14df`

The three blockers of section 18.2, repaired, and nothing else. Blocker A is a
bound that did not bound. Blocker B is an identity one field short. Blocker C
is a fingerprint with nothing to say about a gap. Each was first reproduced as
a permanent adversarial test that failed against the exact starting
implementation for the intended reason, then fixed at its root, then attacked
with compiling mutations that reintroduce the defect.

Everything section 18.2 accepted is intact: the framework-independent
`pkg/catalog` boundary and its Resolver port, the architecture gate,
`internal/app`'s reuse of the existing parsing, credential, cache,
digest-pinning and lock content-hash paths, the refusal of a local reference
declared by a registry contract, sanitized typed failures, the three identity
layers, structured declaration and path provenance, diamonds, multi-root paths,
cycles, ranks, ordinary conflict reporting, memoization by `(Base, Ref,
Constraint)`, the immutable network-free session, the deep-copy boundaries, the
existing valid-root, revision, depth, path-length, retained-path and reporting
bound behaviour, `oci.ErrNoMatchingTag` and all of Phase 10C.

Phase 11 remains a CANDIDATE. `PACTO_PR_TARGET_STATE.md` was not touched,
sections 1 through 18.2 are unchanged, no PR comment was published, no review
thread was resolved, no PR metadata was changed, the PR is still an open draft,
and Phase 12 is NOT started.

### Range

Starting point `77037e7f2cb1024141334918fbad83c5256168f3`, the commit carrying
the section 18.2 review, which was also the remote branch head and the PR head
when this work began. Final SHA `ee9b14df44ecc82cd7003ec64d3549d557e41088`.
`77037e7f` is an ancestor of `ee9b14df`; the range is exactly four linear,
single-parent commits, zero merges. 10 files, +631 / -191. No rebase, amend,
reset, force-push, squash or rewrite; the push was the fast-forward
`77037e7f..ee9b14df`. `origin/main` and the merge-base are still
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`, and main was not touched.

| SHA | Subject | Files | Delta |
|---|---|---|---|
| `6fe9628e` | fix(catalog): bound dependency work whether or not it resolves | 2 | +131 / -18 |
| `3837031b` | fix(catalog): keep a mirrored bundle two services, not one revision | 10 | +420 / -174 |
| `f620cb8a` | fix(catalog): tell two catalogs apart when their unresolved roots differ | 2 | +78 / -4 |
| `ee9b14df` | test(app): split the mirrored-registry assertions into a helper | 1 | +16 / -9 |

Changed files across the whole range:

| Path | Delta | Why it changed |
|---|---|---|
| `pkg/catalog/build.go` | +111 / -58 | the edge-work budget, and `RevisionID` through the walk |
| `pkg/catalog/identity.go` | +36 / -11 | `RevisionID`, its order, and `DeclarationID.From` |
| `pkg/catalog/model.go` | +23 / -18 | `Root.Revision`, `Revision.ID`, `Edge.To`, `Conflict.Revisions`, `Cycle.Revisions`, lookup |
| `pkg/catalog/fingerprint.go` | +34 / -16 | the unresolved-root reference, and revision framing |
| `pkg/catalog/build_test.go` | +192 / -43 | the A and B adversarial cases, plus fixture migration |
| `pkg/catalog/catalog_test.go` | +84 / -15 | the C adversarial cases, plus fixture migration |
| `pkg/catalog/identity_test.go` | +23 / -7 | `compareRevisionID` order, and the renamed declaration-order test |
| `pkg/catalog/walk_test.go` | +16 / -16 | fixture migration |
| `pkg/catalog/harness_test.go` | +25 / -7 | `at`, `atLocal`, `inDomain` and `RevisionID`-typed helpers |
| `internal/app/catalog_test.go` | +87 / -0 | the real two-repository mirror case, and its assertion helper |

No production file outside `pkg/catalog` changed. `internal/app/catalog.go` is
byte-identical: the adapter already reported the publishing domain, and the
core was the side that discarded it. `tests/architecture/boundary_test.go`,
every workflow, every Makefile and every document other than this ledger are
untouched. The only importer of `pkg/catalog` anywhere in the repository is
`internal/app`, so the model changes below have exactly one consumer, and it is
in this range.

### Blocker A -- a bound that did not bound

**Root cause.** `mayResolve` compared `len(b.edges)` against `MaxEdges`, and
`b.edges` was written only by `recordEdge`, which only a successful resolution
reaches. A dependency that fails adds no edge, so it cost the budget nothing: a
contract with a large failing fan-out was resolved in full. `expand` also built
one arrival per declaration before any bound could look at them, so the queue
itself grew with the declaration count.

**Repair.** The budget is charged against the WORK, not against the result.
`edgeWork{decl, base, ref, constraint}` names one unit of dependency work --
one declaration asking one question -- and `admitEdgeWork` charges it inside
`expand`, before the arrival exists. A refused declaration is never queued,
never dequeued and never resolved; it emits `EDGE_LIMIT_EXCEEDED` and one
`Unresolved` entry carrying `BOUND_EXCEEDED`, so the answer is partial and says
which reference was dropped. `out` is allocated at `min(len(p.deps),
MaxEdges)`, so a contract declaring a million dependencies allocates the
budget, not the list. The now-dead post-hoc checks in `mayResolve` and
`recordEdge` are gone.

Charging the key rather than the arrival is what preserves the memoized-repeat
behaviour: two routes reaching the same declaration ask the same question and
pay once, so a diamond still costs one edge, while the same reference declared
twice, or declared from two bases, still costs two.

`recordUnresolved` was split out of `recordFailure` for one reason: a
declaration the bound refused has no arrival to fail, and inventing one purely
to report it would be the same mistake as walking it. The split also removes a
double limitation, because a bound that already named itself no longer also
emits `UNRESOLVED_DEPENDENCY`.

**RED, against the exact starting implementation `77037e7f`.**
`TestEdgeBoundStopsFailingDependencyWorkToo`: one resolvable root declaring
four distinct dependencies that all return `NOT_FOUND`, built with
`Bounds{MaxEdges: 2}`.

```
--- FAIL: TestEdgeBoundStopsFailingDependencyWorkToo
    build_test.go: resolver calls = 5, want 3: the root plus at most MaxEdges dependencies
    build_test.go: reg/d2:1 was fetched 1 times; the edge bound should have refused it before any call
    build_test.go: reg/d3:1 was fetched 1 times; the edge bound should have refused it before any call
    build_test.go: limitations = [UNRESOLVED_DEPENDENCY UNRESOLVED_DEPENDENCY UNRESOLVED_DEPENDENCY UNRESOLVED_DEPENDENCY], want an EDGE_LIMIT_EXCEEDED among them
```

`TestEdgeBoundSpendsOneBudgetOnSuccessAndFailureAlike`: one root, one
resolvable dependency and one failing dependency, `Bounds{MaxEdges: 1}`.

```
--- FAIL: TestEdgeBoundSpendsOneBudgetOnSuccessAndFailureAlike
    build_test.go: resolver calls = 4, want 3
    build_test.go: edges = 2, want 1
```

**GREEN.** Both pass after the repair, alongside the pre-existing
`TestEdgeBoundStopsResolverWork`,
`TestTheEdgeBoundAlsoStopsEdgesThatCostNoResolution` and every other bound
test, at 100.0% package coverage.

**Mutations.**

| Mutation | What failed |
|---|---|
| A1 -- charge the bound against `len(b.edges)` again, keeping the new call site | `TestEdgeBoundStopsFailingDependencyWorkToo`, on the resolver-call count and the missing `EDGE_LIMIT_EXCEEDED`: exactly the original RED |
| A2 -- build every arrival first, then slice the surplus away after the loop | resolver counts come out right, and `limitations = []`, `unresolved = []`, `completeness: complete`; `TestEdgeBoundStopsFailingDependencyWorkToo` fails on all three |

A2 is the shape the commission forbade, and it is the one worth reading twice:
a suite that only counted resolver calls would have passed it. What failed is
the honesty of the answer. Both mutations were reverted byte-identically,
verified with `shasum -a 256 -c` against pre-mutation hashes.

### Blocker B -- an identity one field short

**Root cause.** `builder.revs` was keyed by `ContentID` alone, and
`DeclarationID.From`, `Edge.To`, path steps, cycles, conflicts, the lookup map
and the fingerprint all followed it. Mirroring publishes one bundle into two
repositories; the manifest digest is identical by construction, so two
domain-qualified services collapsed into one revision, and whichever root
arrived first decided whose service it claimed to be. Two builds differing only
by root order therefore disagreed about the domain AND produced two different
`catalogId` values.

**Repair.** `RevisionID{Service, Content}` is the canonical revision identity,
threaded end to end: `builder.revs`, `revOrder`, `arrival.chain`, `edgeKey.to`,
`DeclarationID.From`, `Root.Revision`, `Edge.To`, `Conflict.Revisions`,
`Cycle.Revisions`, `Catalog.byRevision`, `Catalog.Revision`, the cycle key and
every fingerprint framing. Every map key and provenance type that had assumed
content alone identifies a revision was audited and converted; `Revision.ID()`
and `projection.id()` are the only two places the pair is formed. The one
deliberate non-conversion is `ContentID` itself, which is unchanged.

Both halves stay comparable structs, so the pair is a map key without ever
joining user-controlled text with a delimiter. `compareRevisionID` orders by
content first and by service only to break the tie mirroring creates, so
revisions still sort by content identity and two mirrors sit adjacent in a
stable order. Content identity is still the exact OCI manifest digest or the
exact `lock.HashFS` content hash. Same-service tag-plus-digest deduplication is
untouched, because both references yield the same service AND the same content.
Two mirrors are two revisions of two different services, so they are correctly
NOT reported as a same-service conflict. The cycle key moved from
`content.String() + "|"` to the same length-prefixed `encode` framing the
fingerprint uses, so the service text a revision now carries cannot forge or
split a key.

**RED, against `77037e7f`.** In the core, with the resolver fake:

```
--- FAIL: TestMirroredContentInTwoDomainsStaysTwoServices
    build_test.go:296: revisions holding the mirrored content = 1, want 2: one per publishing domain
```

In `internal/app`, against a real in-process OCI registry with one
deterministic bundle pushed to both `alpha/api:1.0.0` and `beta/api:1.0.0`.
The test asserts the two manifest digests are identical before it asserts
anything else, so it is a real mirror rather than a staged one:

```
--- FAIL: TestCatalogKeepsMirroredRegistryContentAsTwoServices
    catalog_test.go:471: the mirrored digest belongs to services [127.0.0.1:57258/alpha], want [127.0.0.1:57258/alpha 127.0.0.1:57258/beta]
    catalog_test.go:474: revisions = 2, want both mirrors plus the shared lib
    catalog_test.go:484: edges = [one edge], want one per mirror from two distinct declarations
    catalog_test.go:491: lib paths = [two identical steps]
    catalog_test.go:498: catalogId = sha256:c36731ee... asking beta first, sha256:2a87d482... asking alpha first
```

**GREEN.** Both pass after the repair, in both root orders, with one stable
`catalogId`, two distinct revisions, two distinct declarations, two distinct
edges into the shared library and two distinct path steps. The core test also
proves that mirroring the same content into a THIRD domain changes the
fingerprint, so the identifier is sensitive to WHICH services published the
content, not merely to how many did.

**Mutation.**

| Mutation | What failed |
|---|---|
| B1 -- `projection.id()` returns `RevisionID{Content: p.content}`, dropping the service half while every call site keeps compiling | 13 permanent tests across `pkg/catalog` and `internal/app`, including both intended mirrored assertions |

Reverted byte-identically, verified with `shasum -a 256 -c`.

### Blocker C -- a fingerprint with nothing to say about a gap

**Root cause.** A root that did not resolve contributed only `Resolved=false`,
a zero content identity and its reason code. Two catalogs that asked for
different references and were told `NOT_FOUND` about both were therefore
indistinguishable, even though the requested reference is the only identity an
unresolved root has.

**Repair.** Four lines in `fingerprint`: when, and only when, a root did not
resolve, that root's `RequestedRef` is appended to its outcome fields. It is a
hashed input, never a rendered one, so no root reference is exposed as
plaintext through `catalogId`. The scoping is the whole point. A root that DID
resolve still contributes only what it resolved to, so a tag and the digest it
pins still fingerprint alike. Root ordinals are still excluded, so permutations
are still stable, and generation time is still excluded.

The framing is injective against hostile text because it is the same
length-prefixed encoding the rest of the fingerprint uses: `encode` writes an
eight-byte big-endian length before each field, so `["ab", "c"]` and
`["a", "bc"]` cannot produce one byte stream.

**RED, against `77037e7f`.**

```
--- FAIL: TestCatalogIDDistinguishesDifferentUnresolvedRoots
    catalog_test.go:241: two catalogs that failed at different references share catalogId sha256:f48f8bd7...
    catalog_test.go:249: [ab c] and [a bc] fingerprinted alike; the root encoding is joining rather than framing
```

**GREEN.** The permanent test proves five things at once: different unresolved
roots give different identifiers, the `["ab","c"]` versus `["a","bc"]` framing
pair does not collide, permuting the same unresolved multiset gives the same
identifier, two unresolved roots do not fingerprint the same as one, and the
identifier carries no plaintext of the requested references -- it asserts the
value parses as a digest and that the reference text does not appear inside it.
A guard also fails the fixture if it ever resolves anything, so the assertion
cannot quietly stop being about unresolved roots.
`TestCatalogIDIsTheSameForATagAndTheDigestItPins` is the complement, and it
carries its own vacuity guard.

**Mutations.**

| Mutation | What failed |
|---|---|
| C1 -- delete the unresolved-root block entirely | reproduces the original RED exactly: both assertions of `TestCatalogIDDistinguishesDifferentUnresolvedRoots` |
| C2 -- append `RequestedRef` unconditionally, resolved or not | `TestCatalogIDIsTheSameForATagAndTheDigestItPins`: a tag and the digest it pins stop fingerprinting alike |

C2 is the interesting half. It fixes blocker C and breaks the invariant blocker
C was not allowed to cost, which is exactly why the repair is conditional. Both
were reverted byte-identically, verified with `shasum -a 256 -c`.

### What the repair deliberately did not do

No generic validation framework, no new storage system, no new configuration
model, no speculative abstraction. No new bound, no new limitation code, no new
reason code and no new public type beyond `RevisionID`, which is the identity
blocker B required. Nothing is resolved first and sliced afterwards. Reporting
bounds are still reporting bounds and work bounds are still work bounds; the
nine-bound table of section 18 is unchanged in shape.

`RevisionID.Zero()` was written and then deleted before the first commit: no
caller needed it, and the 100.0% coverage gate correctly refuses unreachable
code. The gate did the job it exists for and was not weakened.

### Local verification, all green at `ee9b14df`

`go test -race -count=1 ./pkg/catalog/` (ok, coverage 100.0%);
`go test -race -count=1 ./internal/app/ ./pkg/oci/` (ok, `internal/app` at
100.0%); `go test -race -count=1 ./tests/architecture/` (ok, including
`TestCatalogCoreIsFrameworkIndependent` and
`TestCorePackagesHaveNoKubernetesOrIntegrationDeps`); `make ci-test` (exit 0,
`total coverage: 100.0%`); `make ci` (exit 0: fmt, vet, gocyclo, lint, CLI-docs
drift, UI build and drift, Kubernetes-module fmt/vet/lint, the race unit suite,
examples, `DEMO-CONTRACTS VALID: 24/24`, frontend lint and tests, Helm
lint/render/unit/schema/helm-docs, generated-docs drift); `make artifact-drift`
(`artifact-drift: OK`); `make release-dry-run` (`K8S-MODULE-STANDALONE OK`,
`RELEASE-DRY-RUN OK`); `govulncheck ./...` (`No vulnerabilities found`);
`git diff --check` clean; `make check-section` (zero U+00A7 in authored files).

Both new catalog packages run inside the single existing workspace `-race`
invocation that enforces exactly 100.0% aggregate coverage, and the boundary
gate runs in the existing `ci-gates` job. No job, harness, timeout, race
setting or coverage threshold was added, weakened or bypassed.

### GitHub at the exact final SHA `ee9b14df`

PR `TrianaLab/pacto#291` is OPEN, DRAFT and MERGEABLE, head
`ee9b14df44ecc82cd7003ec64d3549d557e41088`, base `main`, and
`git ls-remote origin feat/operational-graph-fleet` returns that same SHA. Ten
workflow runs exist at this SHA: the eight recorded in previous phases, plus
the two `dynamic` CodeQL runs that GitHub schedules against
`refs/pull/291/head` and that earlier records did not enumerate separately.

| Workflow | Run ID | Attempt | Conclusion |
|---|---|---|---|
| CI | `32278479689` | 2 | success |
| Security | `32278479849` | 1 | success |
| Docs check | `32278479704` | 1 | success |
| Pacto Contract CI | `32278479664` | 1 | success |
| Repowise (architecture health) | `32278479779` | 1 | success |
| Validate PR title | `32278479670` | 1 | success |
| Code Quality: PR #291 (dynamic) | `32278475365` | 1 | success |
| PR #291 (dynamic) | `32278475389` | 1 | success |
| Rebuild dashboard UI | `32278479656` | 1 | skipped |
| Auto-merge Dependabot PRs | `32278479660` | 1 | skipped |

Run `32278479689` (CI) is success on attempt 2; all 21 jobs are green:

| Job | ID | Conclusion |
|---|---|---|
| `changes` | `96181936889` | success |
| `ci-e2e-compose` | `96181937003` | success |
| `ci-e2e-kind (operational-graph)` | `96181937907` | success |
| `ci-e2e-kind (reconcile)` | `96181937912` | success |
| `ci-e2e-envtest` | `96181937959` | success |
| `operator-build` | `96181938017` | success |
| `release-dry-run` | `96181938079` | success |
| `artifact-drift` | `96181938101` | success |
| `ci-gates` | `96181938136` | success |
| `ci-integration-kubernetes` | `96181938137` | success |
| `ci-static` | `96181938141` | success |
| `ci-oci` | `96181938202` | success |
| `ci-engine` | `96181938235` | success |
| `dashboard-e2e` | `96181938307` | success |
| `release-version-test` | `96181938355` | success |
| `ci-dashboard` | `96181938427` | success |
| `ci-e2e-kind (dashboard)` | `96181938749` | success |
| `ci-e2e-kind (upgrade)` | `96181938791` | success |
| `ci-e2e-kind (observation)` | `96181938954` | success |
| `ci-e2e-kind (evidence)` | `96181938969` | success |
| `required` | `96185779639` | success |

Why attempt 2 exists is disclosed in full below; nothing was pushed, amended or
changed between the two attempts, and both attempts ran the identical tree at
`ee9b14df`.

Security's three jobs are green: `govulncheck (Go)` `96151370958`,
`Trivy (image)` `96151371197` and `PR security summary` `96151853629`.
`Docs check` is one job, `docs-check` `96151369761`. `Pacto Contract CI` is one
job, `bundle` `96151370101`. `Repowise` is one job, `repowise` `96151370689`.
`Validate PR title` is one job, `validate` `96151369924`. The two dynamic runs
carry the seven CodeQL analyses: `Analyze (python)` `96151361252`,
`Analyze (go)` `96151361291` and `Analyze (javascript-typescript)`
`96151361406` in `32278475365`, and `Analyze (javascript-typescript)`
`96151361417`, `Analyze (python)` `96151361716`, `Analyze (actions)`
`96151361753` and `Analyze (go)` `96151361984` in `32278475389`. Every one is
success.

Reading the head commit's check runs directly gives the same answer: 40 check
runs, 39 success or skipped and one failure, the aggregate `CodeQL` check from
`github-advanced-security`. That aggregate is the inherited condition described
next; the `govulncheck` and `Trivy` checks from the same app are both success.

### CodeQL and review threads

Nine code-scanning alerts are open on `refs/pull/291/head`, and they are exactly
the nine inherited ones: `38` (`py/incomplete-url-substring-sanitization`,
`release/scripts/docs_check.py:197`), `40` through `43` (`go/path-injection`,
`internal/app/resolve.go` lines 35, 43, 57 and 67) and `59` through `62`
(`go/path-injection`, `pkg/oci/cache.go` lines 375, 394, 395 and 666).
**The delta for this repair is ZERO new alerts.**

The individual analyses confirm it numerically rather than by reputation. At
`ee9b14df` the four analyses report `go` 9 results, `python` 1, `actions` 0 and
`javascript-typescript` 0. At the starting SHA `77037e7f` the same four
analyses report the same four numbers. The aggregate `CodeQL` check remains
failure because it aggregates those nine pre-existing alerts, exactly as
recorded in sections 18 and 18.1. It is not claimed as a finding of this
repair, and none of the nine is in this range.

All review threads were paginated in full, since the API caps a page at 100 and
page one alone hides every unresolved one. The totals are unchanged: **199
total, 189 resolved, 10 unresolved**, and the ten are the same six threads on
the generated Mermaid bundle
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and the same four on
`pkg/oci/cache.go`. Neither file is in this range. **The review-thread delta is
ZERO.** No thread was resolved, no comment was published and no PR metadata was
changed.

### Hygiene and disclosures

- **Two CI jobs wedged on attempt 1 and the run was re-run.** In attempt 1,
  `ci-e2e-compose` (`96151802261`) and `ci-e2e-kind (operational-graph)`
  (`96151802364`) both started at 16:54:49Z, both entered their single `make`
  step, and both then produced nothing for 97 minutes; the run's `updated_at`
  froze at 16:55:30Z and no logs were retrievable. Those two jobs take about
  five and seven minutes on the three immediately preceding heads
  (`6a0c198d`, `42ed66ea`, `bf9070a0`), neither job's harness is touched by this
  range, and the two harnesses share nothing but Docker, so this reads as the
  same class of isolated runner failure already disclosed against the Phase 10C
  and Phase 11 heads. GitHub reported no incident and the jobs carry no
  `timeout-minutes`, so the default six-hour ceiling was the only thing that
  would have ended them. The run was cancelled and re-run with
  `rerun-failed-jobs`; GitHub re-ran all 21 jobs as attempt 2 because the
  cancelled jobs depend on `changes`. Attempt 2 is green end to end, with
  `ci-e2e-compose` at 12m36s and `ci-e2e-kind (operational-graph)` at 9m14s.
  **No source byte changed between the attempts**: both ran the identical tree
  at `ee9b14df`, and nothing was pushed, amended or force-pushed. Cancelling and
  re-running a wedged run is the only GitHub state this session changed, and it
  is disclosed here rather than left for a reviewer to notice as an attempt
  number.
- `make ci` again regenerated
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md` from helm-docs,
  overwriting the hand-written file (+16 / -41). As in Phase 10C and section 18
  it was restored with `git checkout --` rather than committed. The generator
  quirk predates this phase and is not fixed here.
- `make ci` failed once, at `ci-cyclo`, before the final commit:
  `19 app TestCatalogKeepsMirroredRegistryContentAsTwoServices
  ./internal/app/catalog_test.go:427:1`, against the limit of 15. The fix is
  commit `ee9b14df`, which extracts `assertMirroredPair` from that test. The
  gate was not raised, excluded or bypassed, and the following `make ci` is
  exit 0.
- Commit `3837031b` was staged deliberately so that its `fingerprint.go` and
  `catalog_test.go` contain the blocker-B ripple only, with the blocker-C fix
  held back for `f620cb8a`. That intermediate tree was built and tested before
  committing: green, with 100.0% coverage in both `pkg/catalog` and
  `internal/app`. Every commit in the range therefore compiles and passes on its
  own.
- A stale editor diagnostic again claimed an undefined test helper in
  `pkg/catalog/build_test.go`; a real `go test ./pkg/catalog/` disproved it and
  nothing was changed on its account.
- The untracked agent paths `.claude/`, `.codex/`, `.mcp.json` and `AGENTS.md`
  are inherited, untouched and still untracked. `git status --short` at the
  final SHA lists those four and nothing else.
- No literal U+00A7 was authored in any file, commit message or PR field, and
  `make check-section` confirms it.

### Deliberately not done

No MCP catalog tool, no MCP resource registration, no `pacto mcp --root`
wiring, no CLI surface, no server route, no stdio or protocol E2E and no public
discovery-server documentation: all of that remains Phase 12. The authoring,
capability and operational Fleet MCP tools are byte-identical. Phase 10C was not
reopened. No prior ledger record was rewritten, and no phase was marked closed.

### Verdict

Blockers A, B and C are repaired at their root, each with a permanent
adversarial test that failed against `77037e7f` for the intended reason and
each with at least one compiling mutation that reintroduces the defect and is
caught. **Phase 11 is a CANDIDATE at `ee9b14df`, awaiting independent review.**
It is NOT closed; closing it is the reviewer's act, not the author's. Phase 12
has NOT started.

### Current phase map

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11: CANDIDATE at `ee9b14df` after the narrow reopening of section 18.2,
  awaiting independent review.
- Phases 12 through 14: NOT STARTED.

The PR remains an open draft, and the append-only, no-history-rewrite and
independent-review protocol continues unchanged.

## 19.4 Independent review at `544eeb30` -- repair ACCEPTED and CLOSED

Independent review covered
`57953f5bff9e0b498fdd280829726448e34d975e..544eeb30aa0506fbe91ba718648ba4be2e03dfbf`,
with implementation SHA `e639f4b0a29d5a30af80057334c32080777c3e49`.
Both commits are linear, single-parent descendants of the section 19.2 review
head. `57953f5b`, `66a25b7d` and `3c3665a1` remain ancestors; `origin/main` and
the merge-base remain `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.
The remote branch and local HEAD were exactly `544eeb30` at review start. The PR
was OPEN, DRAFT and MERGEABLE.

### Blocker A is closed exactly at its boundary

The implementation delta is one line in one file:

```diff
-b, err := os.ReadFile(path) //nolint:gosec // a path this test computed
+b, err := os.ReadFile(path)
```

There is no replacement suppression, helper, abstraction, configuration change
or read-path redesign. The accepted action pin, binary pin, ten SA9010 cleanup
changes and structural test are byte-identical to the tree reviewed in section
19.2. Section 19.3 also corrects section 19's inaccurate broad no-suppression
claim without rewriting the historical record.

The older `//nolint:gosec` in
`tests/architecture/kind_image_loading_test.go` predates this inter-phase repair,
is outside its reviewed delta and belongs to already closed Phase 10 work. Its
disclosure is accurate and it is not a closure blocker here.

### Independent evidence

- golangci-lint v2.13.0 over `./tests/architecture/...`, with a dedicated
  review cache -- `0 issues.`, exit 0.
- `go test -race -count=1 ./tests/architecture/...` -- pass.
- `make check-section` -- zero U+00A7 in authored files.
- `git diff --check` over the reviewed range -- clean.
- Independent inspection confirms that the implementation range contains only
  the suppression deletion and that no linter configuration changed.
- At exact implementation SHA `e639f4b0`, CI run `32345841250` is attempt 1,
  success, 21/21 jobs green. Its `ci-static` job `96354363210` records
  `version: v2.13.0`, installs v2.13.0, reports `0 issues.` and passes the
  authored-content gate. Security, Docs check, Pacto Contract CI, Repowise,
  PR-title and both dynamic CodeQL workflows also succeed.
- At exact ledger SHA `544eeb30`, CI run `32347548200` is attempt 1, success,
  again 21/21 jobs green, including all six Kind shards, Compose and `required`.
  The same auxiliary workflows succeed.
- Each exact SHA has 40 check runs: 37 success, two expected skips and one
  inherited aggregate CodeQL failure. The code-scanning API still returns the
  same nine open alerts -- 38, 40 through 43 and 59 through 62 -- so this
  repair's CodeQL delta is zero.
- Fully paginated review threads remain 199 total, 189 resolved and 10
  unresolved: six on the generated Mermaid bundle and four on
  `pkg/oci/cache.go`. The thread delta is zero.

### Verdict and phase map

**The inter-phase required-CI determinism repair is ACCEPTED and CLOSED at
implementation SHA `e639f4b0`.** The required linter binary is immutable, its
new analyzer findings are correctly repaired, the structural gate fails on a
floating pin, and the sole boundary violation found in section 19.2 is now
deleted without replacement. There is no remaining counterexample in this
repair's scope.

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED at
  `e639f4b0`, independently reviewed through ledger head `544eeb30`.
- Phase 12: NOT STARTED and now unblocked.
- Phases 13 and 14: NOT STARTED.

## 18.4 Independent review at `a5fb3ecd` -- Phase 11 remains narrowly reopened

Independent review date: 2026-08-19. Reviewed repair range:
`77037e7f2cb1024141334918fbad83c5256168f3..ee9b14df44ecc82cd7003ec64d3549d557e41088`.
Reviewed remote ledger head:
`a5fb3ecdeb302287ae25f45ba84d3f96d129f691`.

### Repository, history and external state

- PR 291 is OPEN, DRAFT and MERGEABLE on
  `feat/operational-graph-fleet`; local HEAD, the remote branch and the PR head
  were exactly `a5fb3ecd` before this review record.
- `origin/main` and the merge-base remain
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`. `77037e7f` remains an
  ancestor. The range to `a5fb3ecd` is exactly five linear, single-parent
  commits: the four implementation commits recorded in section 18.3 followed
  by its ledger commit. There is no merge or evidence of an amended, rebased or
  force-pushed boundary.
- The implementation range changes the ten files and the +631 / -191 delta
  recorded in section 18.3. The ledger adds only
  `.pr-context/PACTO_PR_CURRENT_STATE.md`; TARGET, the iteration protocol,
  testing documentation, Make and every workflow remain untouched.
- At exact implementation SHA `ee9b14df`, CI run `32278479689` succeeded on
  attempt 2 with all 21 jobs green. The API confirms that attempt 1 had exactly
  the two disclosed cancelled jobs, `ci-e2e-compose` and
  `ci-e2e-kind (operational-graph)`, followed by the aggregate `required`
  failure; attempt 2 contains 21 successes on the identical tree. Security,
  Docs check, Pacto Contract CI, Repowise, PR-title and every individual CodeQL
  analysis are also successful.
- At ledger head `a5fb3ecd`, CI run `32290080859` finished failed on attempt 1.
  Nineteen functional jobs passed; `ci-e2e-kind (reconcile)` failed because
  `kindload` reported that the dashboard image was not present after loading,
  and `required` consequently failed. This docs-only commit does not touch that
  harness or image path, and the same shard passed at `ee9b14df`, so the result
  is not evidence against the catalog repair. It is nevertheless the truthful
  exact-head state and is not called green.
- The code-scanning API returns the same nine inherited alerts: 38, 40 through
  43 and 59 through 62. None is in this repair range. Review threads were
  independently paginated: 199 total, 189 resolved and 10 unresolved, still
  the same six generated-Mermaid threads and four `pkg/oci/cache.go` threads.
  The repair delta is zero in both sets.

### Accepted repair -- blockers B and C are closed

Blocker B is repaired at the root. `RevisionID{Service, Content}` is a
comparable structural identity, and it now reaches revision storage and lookup,
root outcomes, declaration occurrences, edges, paths, cycles, conflicts and the
fingerprint. The cycle key uses length-prefixed framing rather than a delimiter.
The core test and the real in-process OCI registry test both prove that one
manifest digest mirrored into two repository domains remains two revisions of
two domain-qualified services, with two distinct declarations and paths, no
false same-service conflict and one `catalogId` under either root order.
Same-service tag-plus-digest deduplication remains green.

Blocker C is also repaired at the root. An unresolved root contributes its
requested reference as a length-prefixed hashed field because it has no resolved
identity; a resolved root still excludes that provenance. The permanent tests
prove different unresolved multisets differ, permutations agree, framing is
injective, multiplicity matters and a tag still fingerprints like the immutable
digest it resolved to.

These two repairs, and all the Phase-11 work accepted in section 18.2, must not
be redesigned without a new concrete counterexample.

### Remaining blocker A -- the network calls are bounded, the surplus walk is not

Charging `edgeWork` before constructing an arrival correctly fixes the original
observable failure: failed dependencies spend budget, resolver calls stop at
`MaxEdges`, the surplus never enters the queue, and the answer becomes partial
with `EDGE_LIMIT_EXCEEDED`. The permanent tests prove those facts.

The implementation nevertheless keeps iterating through every declaration
after the first new edge-work item is refused. For every surplus declaration,
`expand` calls both `limit` and `recordUnresolved`. Each helper inserts the
reference into its deduplication map before applying the corresponding reporting
bound. The visible `Unresolved` and `Limitations` slices are capped, but
`unresolvedSeen`, `limitSeen` and loop work still grow linearly with the hostile
fan-out that `MaxEdges` claims to stop. No resolver call or queue entry is made,
but the catalog core still performs and retains unbounded surplus bookkeeping.

This was reproduced against the actual builder with a temporary package-local
probe: 1,000 distinct declarations, `MaxEdges=1`, `MaxUnresolved=1` and
`MaxLimitations=1` admitted one arrival and one edge-work key, but left
`unresolvedSeen=999` and `limitSeen=1000`. The probe failed on those exact
counts and was then deleted; no product or probe file remains. The existing
`TestEdgeBoundStopsFailingDependencyWorkToo` currently expects every declaration
past the work bound to become a separate bounded unresolved result, so it
codifies the surplus scan rather than catching it.

A work bound cannot require walking and remembering the entire work it refused.
Once declaration order reaches the first unseen item that `MaxEdges` cannot
admit, later declarations of that same expansion cannot be admitted either.
The core should record one honest bound event/gap and stop that expansion rather
than inspect every remaining declaration. A permanent adversarial test must
make the internal work observable -- not merely count resolver calls or output
slices -- and prove that increasing a large hostile tail does not grow surplus
bookkeeping beyond a constant derived from the admitted work and the single
boundary report.

The narrow repair must also correct two now-false public comments rather than
leave the model saying two things: `ContentID` is the immutable content identity,
not "the only thing the catalog treats as identity" now that `RevisionID` is
canonical revision identity; and `Bounds.MaxEdges` budgets dependency work,
including failed attempts, not only successfully recorded edges. This is
documentation alignment for the same blocker, not a new design request.

### Independent local verification and hygiene

- `go test -race -count=1 ./pkg/catalog ./internal/app ./pkg/oci
  ./tests/architecture` passed after removing the temporary probe.
- `git diff --check` passed. The worktree returned to only the four inherited
  untracked agent paths `.claude/`, `.codex/`, `.mcp.json` and `AGENTS.md`
  before this review record.
- No product file, test, workflow, PR comment, review thread or PR metadata was
  changed by this review. TARGET and prior ledger records remain untouched.

### Verdict and exact next objective

**Phase 11 remains NARROWLY REOPENED and is not closed.** Blockers B and C are
accepted and closed. Blocker A is materially improved and its original network
and queue counterexample is closed, but `MaxEdges` still permits work and memory
bookkeeping proportional to every refused declaration after the boundary.

The next iteration is a Phase-11-only closure repair of this one residual
counterexample. It must start from this review ledger head, stop the expansion at
the first newly refused edge-work item, preserve one honest partial/bound report,
add a permanent non-vacuous scaling proof and mutation proof, align the two
public comments, run the complete verification at the exact final SHA, append a
new CANDIDATE record and stop. Phase 12 must not start.

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11: NARROWLY REOPENED on the residual MaxEdges surplus walk only.
- Phases 12 through 14: NOT STARTED.

## 18.5 Phase 11 residual edge-bound closure repair -- CANDIDATE at `1cc6a3aa`

Commissioned by section 18.4's remaining blocker A, and by nothing else. Blockers
B and C stay closed, section 18.2's accepted implementation stays closed, and
Phase 12 was not started. This record is a CANDIDATE and does not close Phase 11.

### Range

- Starting SHA (section 18.4's ledger head): `9a737191bb02ce689541bd8485093fe3a4e94977`
- Implementation SHA: `1cc6a3aa1d82345333c7f658107137d1e5a1c3de`
- Merge-base with `origin/main`: `83f2e66d5cd4fab56099991d39e64fc11f107b3d`, unchanged
- Commits appended, in order, no amend, rebase, squash, reset or force-push:
  1. `1cc6a3aa` `fix(catalog): stop reading the declarations the edge bound refused`
  2. this document's own commit, `docs(pr): record the Phase 11 edge-bound
     closure repair as a candidate`, whose GitHub state is recorded in section
     18.6 once it lands

`1cc6a3aa` is five files, +126 / -19, all under `pkg/catalog`:

| file | change |
| --- | --- |
| `pkg/catalog/build.go` | `newBuilder` extracted from `Build`; `expand` stops at the first refused edge-work item instead of continuing |
| `pkg/catalog/build_test.go` | the permanent scaling counterexample and its probe; `TestEdgeBoundStopsFailingDependencyWorkToo` corrected |
| `pkg/catalog/bounds.go` | `Bounds.MaxEdges` comment: it budgets dependency WORK, failed attempts included |
| `pkg/catalog/identity.go` | `ContentID` comment: content identity, one half of the canonical `RevisionID` |
| `pkg/catalog/catalog.go` | `LimitationEdgeLimit` comment: the refused remainder, with `Ref` as a representative |

No workflow, Make target, gate, timeout, coverage threshold, complexity limit or
job changed. No production observability API or test hook was added.

### The blocker -- a bound that walked everything it refused

`admitEdgeWork` already charged failed attempts, stopped the resolver at
`MaxEdges` and kept the surplus out of the queue. What it did not do was stop
the loop. `expand` continued through every remaining declaration, and both
`limit` and `recordUnresolved` insert into `limitSeen` / `unresolvedSeen` BEFORE
their reporting bounds apply, so a hostile fan-out still bought one map entry
per refused declaration -- the exact work `MaxEdges` exists to prevent, paid in
memory instead of network.

The repair is three lines of control flow and a comment. At the first previously
unseen work item the budget cannot admit, record one `EDGE_LIMIT_EXCEEDED`
limitation and one representative `BOUND_EXCEEDED` unresolved entry, then
`break`.

`break` is admission-equivalent, and that is what makes it the smallest correct
repair rather than a heuristic. `admitEdgeWork` returns `true` for a key it has
already seen, so `false` only ever means a previously unseen key. Declaration
indexes are unique within one expansion, so the keys admitted from one contract
form a prefix of its declared order: if index `i` is unseen and refused, every
later index is unseen too and would be refused as well. Nothing that would have
been admitted is lost.

Preserved exactly: declaration-order admission, failed attempts spending edge
budget, memoized repeats paying once, distinct declarations and distinct bases
paying separately, no resolver call past the budget, no surplus queue
allocation, partial completeness, explicit edge-bound reporting, and the
separation between work bounds and reporting bounds.

### The permanent counterexample

`TestTheEdgeBoundDoesNotWalkTheDeclarationsItRefused` builds one root declaring
a tail of distinct failing dependencies under `Bounds{MaxEdges: 1}`, at two tail
sizes two orders of magnitude apart -- 10 and 1,000 -- and asserts, for each:
exactly one admitted unit of dependency work; exactly two resolver calls, the
root and the one dependency the bound admitted; a partial catalog; the
`EDGE_LIMIT_EXCEEDED` limitation present; and exactly one dependency reported
with `BOUND_EXCEEDED`. It then asserts across the two sizes that a hundredfold
larger hostile tail buys no extra work and no extra memory, and that what is
retained is `admitted work + 1` unresolved keys and 2 limitation keys -- a
function of the admitted work and the single boundary report, never of how many
declarations were refused.

It drives the builder through the unexported `newBuilder` / `walk` / `finish`
rather than through `Build`, because the counts that matter are internal by
design: the refused declarations reach no resolver, enter no queue and leave no
edge, so no accessor can see them, and adding one would be a production hook
that exists only for a test. This is a package-local test of a package-local
defect; the exported surface is unchanged.

`TestEdgeBoundStopsFailingDependencyWorkToo` was corrected in the same commit.
It previously required every declaration past the work bound to become its own
bounded unresolved result, which codified the surplus scan rather than catching
it. It now expects the two gaps the bound paid to discover plus one
representative for the remainder.

The reporting bounds `MaxUnresolved` and `MaxLimitations` are deliberately left
at their defaults in the new test. Section 18.4's probe used `1` for both; under
`MaxLimitations: 1`, `finalLimitations` truncates to `limits[:0]` and emits only
`LIMITATION_LIMIT_EXCEEDED`, which would have hidden the `EDGE_LIMIT_EXCEEDED`
the test has to prove. The scaling claim is about work bounds, so the reporting
bounds are held out of it.

### RED, before the repair

With the new test and the `newBuilder` extraction in place but the original
expansion logic untouched:

```
--- FAIL: TestTheEdgeBoundDoesNotWalkTheDeclarationsItRefused (0.00s)
    build_test.go:698: tail 10: 9 dependencies reported as refused by the bound, want one representative
    build_test.go:698: tail 1000: 200 dependencies reported as refused by the bound, want one representative
    build_test.go:706: bookkeeping grew with the refused tail: work 1 then 1, unresolved keys 10 then 1000, limitation keys 10 then 1000
    build_test.go:712: retained 1000 unresolved keys and 1000 limitation keys, want the admitted work plus one refusal, and one limitation each for the admitted gap and the bound
FAIL	github.com/trianalab/pacto/v3/pkg/catalog	1.066s
```

The 999-to-1 growth in `unresolvedSeen` and the 1000-to-1 growth in `limitSeen`
are the same numbers section 18.4 reported from its temporary probe, reproduced
by a permanent test instead of a deleted one.

### GREEN

```
ok  github.com/trianalab/pacto/v3/pkg/catalog         2.022s  coverage: 100.0% of statements
ok  github.com/trianalab/pacto/v3/internal/app       13.455s  coverage: 100.0% of statements
ok  github.com/trianalab/pacto/v3/pkg/oci            24.639s  coverage:  99.8% of statements
ok  github.com/trianalab/pacto/v3/tests/architecture  6.466s  coverage: [no statements]
```

### Mutation evidence

Both mutations compile. Each was applied to the committed implementation, run,
then reverted, and the revert was verified byte-for-byte with
`shasum -a 256 -c` over all five changed files before continuing.

**M1 -- replace stop-after-first-refusal with continue-through-the-tail**
(`break` back to `continue`). The scaling test fails for the intended reason,
and the corrected companion test fails with it:

```
--- FAIL: TestEdgeBoundStopsFailingDependencyWorkToo (0.00s)
    build_test.go:635: unresolved = [d0 NOT_FOUND, d1 NOT_FOUND, d2 BOUND_EXCEEDED, d3 BOUND_EXCEEDED], want the two attempted gaps plus one declaration attributed to the bound
--- FAIL: TestTheEdgeBoundDoesNotWalkTheDeclarationsItRefused (0.00s)
    build_test.go:701: tail 10: 9 dependencies reported as refused by the bound, want one representative
    build_test.go:701: tail 1000: 200 dependencies reported as refused by the bound, want one representative
    build_test.go:709: bookkeeping grew with the refused tail: work 1 then 1, unresolved keys 10 then 1000, limitation keys 10 then 1000
    build_test.go:715: retained 1000 unresolved keys and 1000 limitation keys, want the admitted work plus one refusal, and one limitation each for the admitted gap and the bound
```

**M2 -- stop reporting the boundary** (delete the `limit` and `recordUnresolved`
calls, keep the `break`). It fails all three required families -- partial
completeness, `EDGE_LIMIT_EXCEEDED`, and the representative bounded unresolved
entry -- across five tests in two files:

```
--- FAIL: TestEdgeBoundStopsResolverWork
    build_test.go:587: limitations = [], want the edge bound named
    build_test.go:591: unresolved = [], want the refused dependency reported as bounded
--- FAIL: TestEdgeBoundStopsFailingDependencyWorkToo
    build_test.go:620: limitations = [UNRESOLVED_DEPENDENCY reg/d0:1, UNRESOLVED_DEPENDENCY reg/d1:1], want the edge bound named
    build_test.go:635: unresolved = [d0 NOT_FOUND, d1 NOT_FOUND], want the two attempted gaps plus one declaration attributed to the bound
--- FAIL: TestTheEdgeBoundDoesNotWalkTheDeclarationsItRefused
    build_test.go:692: tail 10: limitations = [UNRESOLVED_DEPENDENCY reg/d0:1], want the edge bound named
    build_test.go:701: tail 10: 0 dependencies reported as refused by the bound, want one representative
    build_test.go:692: tail 1000: limitations = [UNRESOLVED_DEPENDENCY reg/d0:1], want the edge bound named
    build_test.go:701: tail 1000: 0 dependencies reported as refused by the bound, want one representative
    build_test.go:715: retained 1 unresolved keys and 1 limitation keys, want the admitted work plus one refusal, and one limitation each for the admitted gap and the bound
--- FAIL: TestEdgeBoundSpendsOneBudgetOnSuccessAndFailureAlike
    build_test.go:744: limitations = [UNRESOLVED_DEPENDENCY reg/gone:1], want the edge bound named
--- FAIL: TestTheEdgeBoundAlsoStopsEdgesThatCostNoResolution
    walk_test.go:113: meta = {Completeness:complete Limitations:[]}, want a partial answer naming the edge bound
```

After each revert: `shasum -a 256 -c` reported OK for all five files, and
`go test -race -count=1 -cover ./pkg/catalog/` returned
`ok ... 1.394s coverage: 100.0% of statements`.

### Comment alignment, commissioned by the same blocker

Three comments, no code:

- `ContentID` is "the immutable content identity of one contract revision ...
  one half of `RevisionID`, which is the canonical identity of a revision,
  because mirroring publishes one content under two services". The old text said
  it was "the only thing the catalog treats as identity", which blocker B's
  accepted repair made false.
- `Bounds.MaxEdges` "caps distinct units of dependency WORK -- one declaration
  asking one question -- not the edges that work succeeds in recording", and now
  also states that the rest of that contract's declarations are refused with the
  first rather than inspected.
- `LimitationEdgeLimit` says the limitation covers "a dependency and the
  declarations after it", with `Ref` naming "the first one refused, as a
  representative rather than an inventory". Left unchanged it would have been a
  new false claim introduced by this repair.

### Local verification, all green at `1cc6a3aa`

- `go test -race -count=1 ./pkg/catalog ./internal/app ./pkg/oci ./tests/architecture` -- all four ok.
- `make ci-test` -- `total coverage: 100.0%`, example tests ok, `DEMO-CONTRACTS VALID: 24/24`.
- `make ci` -- exit 0 end to end: `ci-static` (fmt, vet, gocyclo, golangci-lint
  `0 issues`, `check-section`, CLI-docs drift, UI-build drift, dashboard-SDK
  drift, plus the operator module's own `ci-static` at `0 issues`), `ci-gates`,
  `ci-engine` (100.0% coverage under the race detector, the engine e2e suite,
  `operational-graph acceptance PASSED`), `ci-dashboard` (Vitest 1232 passed in
  67 files), `ci-integration-kubernetes` (envtest at 100.0% coverage, chart lint,
  template renders, Helm unit tests, chart schema, helm-docs drift, generated-docs
  drift), `ci-e2e-envtest`, `ci-oci`, and the release orchestrator's 16 Node
  tests.
- `make artifact-drift` -- `artifact-drift: OK`.
- `make release-dry-run` -- `RELEASE-DRY-RUN OK: real artifacts to
  localhost:5001, digest idempotency + immutability + resume proven, no
  production coordinate`, and `K8S-MODULE-STANDALONE OK`.
- `govulncheck ./...` -- `No vulnerabilities found.`
- `git diff --check` -- clean. `make check-section` -- `zero U+00A7 in authored
  files`.
- `gocyclo -over 15 ./pkg/catalog` and `gofmt -l pkg/catalog` -- no output.
- The six Kind shards were not attempted locally, for the Docker Desktop
  containerd reason recorded at `ci.mk:88-90`; nothing this repair touches is
  exercised by them. They run in CI, below.

### GitHub at the exact implementation SHA `1cc6a3aa`

Forty check runs, and not one of them a rerun -- every workflow is
`run_attempt=1`. Thirty-seven success, two skipped (`build`, `auto-merge`) and
one failure, the aggregate `CodeQL` check, which is the inherited-alerts item
carried since section 8 and is not a finding of this repair.

- **CI**, run `32298985102`, attempt 1, success -- all 21 jobs green:
  `changes`, `ci-static`, `ci-gates`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-e2e-compose`, `ci-integration-kubernetes`,
  `dashboard-e2e`, `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run`, `required`, and all six Kind shards individually --
  `ci-e2e-kind (dashboard)`, `(upgrade)`, `(reconcile)`, `(evidence)`,
  `(observation)`, `(operational-graph)`.
- **Security**, run `32298985065`, attempt 1, success: `Trivy`, `Trivy (image)`,
  `govulncheck`, `govulncheck (Go)`, `PR security summary` and all four `Analyze`
  jobs -- `actions`, `go`, `javascript-typescript`, `python`.
- **Docs check** `32298985205`, **Pacto Contract CI** `32298985109` (job
  `bundle` green), **Repowise (architecture health)** `32298984906` and
  **Validate PR title** `32298984965` -- all attempt 1, all success.
- The two dynamic CodeQL runs, **PR #291** `32298978941` and **Code Quality: PR
  #291** `32298978954`, both attempt 1, both success. The `CodeQL` CHECK is
  published by `github-advanced-security`, not by those runs, and it reports "8
  new alerts including 8 high severity security vulnerabilities" -- the same
  wording and the same population as at `ee9b14df`. A green Security workflow
  and the CodeQL alert attribution stay two different claims.
- **Auto-merge Dependabot PRs** `32298985180` and **Rebuild dashboard UI**
  `32298985066` -- skipped, as expected on this PR.
- The isolated `ci-e2e-kind (reconcile)` failure recorded at the docs-only SHA
  `a5fb3ecd`, where `kindload` could not find the dashboard image after loading,
  did NOT recur: that shard is green here on the first attempt, as are the other
  five and `ci-e2e-compose`. Nothing in this repair touches that harness or
  image path, and this pass was not broadened into Kind or image-loading work.

### CodeQL and review threads

Both populations were re-queried independently at `1cc6a3aa`, and the delta
introduced by this repair is ZERO in each.

- Code-scanning API, `state=open` on `refs/pull/291/head`: the same nine
  inherited alerts, at the same lines as at `a5fb3ecd` -- 38
  (`py/incomplete-url-substring-sanitization`, `release/scripts/docs_check.py:197`),
  40 through 43 (`go/path-injection`, `internal/app/resolve.go` 35, 43, 57, 67)
  and 59 through 62 (`go/path-injection`, `pkg/oci/cache.go` 375, 394, 395,
  666). None is in this repair's range; `pkg/catalog` carries no alert. None was
  added, silenced or dismissed here, and the whole item stays OPEN for
  independent triage before Phase 14 readiness.
- Review threads, fully paginated -- the API caps a page at 100 and page one
  hides every unresolved thread, so both pages were fetched: 199 total, 189
  resolved, 10 unresolved. The unresolved ten are unchanged: six
  `github-code-quality` comments on the GENERATED minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js`, unchanged because
  no authored frontend input changed and the bundle was not rebuilt, and four
  `github-advanced-security` comments on `pkg/oci/cache.go`. No thread was
  resolved, replied to or opened by this pass.

### Hygiene and disclosures

- The PR is OPEN and DRAFT throughout. No PR comment was published, no review
  thread was resolved or replied to, and no PR metadata -- title, body, labels,
  reviewers, draft state -- was changed.
- Append-only: `1cc6a3aa` and the ledger commit sit directly on top of
  `9a737191`. No amend, rebase, squash, reset, cherry-pick or force-push. The
  merge-base with `origin/main` is unchanged.
- `PACTO_PR_TARGET_STATE.md` was not modified. No previous section of this
  document was rewritten; section 18.5 is an append.
- The four inherited untracked paths `.claude/`, `.codex/`, `.mcp.json` and
  `AGENTS.md` are preserved and uncommitted.
- `make ci` regenerated the hand-written
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md`, as it has in
  every prior pass. It was restored with `git checkout --` and not committed;
  the helm-docs drift check itself passes. No `go.work.sum` drift appeared this
  pass. Outside those, the worktree held nothing but the five changed files.
- No authored frontend input changed, so the committed UI bundle was NOT
  rebuilt, and `ci-ui-drift` and `check-dashboard-sdk-drift` are clean against
  the existing one.
- Editor diagnostics again claimed `undefined: revFields` in `build.go` and
  `undefined: at` in `build_test.go`. Both symbols live in the same package
  (`fingerprint.go` and `harness_test.go`), every real `go test` run compiles and
  passes, and nothing was changed on the diagnostics' account. This is the third
  pass in which that stale diagnostic appeared; it is recorded, not acted on.
- **Incomplete preparatory read, disclosed.** `PACTO_ITERATION_PROTOCOL.md` and
  `PACTO_PR_TARGET_STATE.md` were read in full. Of this document, lines 1 through
  2019 and 7340 through the end -- which is the whole Phase 11 record, sections
  17, 18, 18.1, 18.2, 18.3 and 18.4, plus sections 1 through 11 -- were read in
  full. Lines 2020 through 7339, which are section 12's transient Phase-8B
  inventory ledger and the Phase 9, 10, 10B and 10C records, were NOT read in
  full in this iteration. A grep of that range for `catalog`, `MaxEdges` and
  `EDGE_LIMIT` returns three incidental hits, none of which governs this repair:
  two are Phase 10C OCI-referrer text about registry catalog scans, and one is a
  phase-map line. The omission is disclosed rather than papered over.

### Deliberately not done

- No validation framework, iterator abstraction, second budget or speculative
  mechanism. The repair is a `break`, a comment and a corrected expectation.
- No production test hook and no public observability API. The scaling test uses
  the unexported `newBuilder`, which is an extraction of code `Build` already
  ran, not a seam added for testing.
- No redesign of anything accepted in section 18.2, and no reopening of blockers
  B or C.
- No MCP catalog server, tool or resource registration, no `pacto mcp --root`
  wiring, no CLI work, no protocol E2E and no Phase 12 documentation.
- No change to the nine inherited CodeQL alerts, which stay OPEN and outside this
  repair's scope.

### Verdict

**Phase 11 remains a CANDIDATE.** Section 18.4's remaining blocker A is
addressed by a permanent, non-vacuous, mutation-proved scaling counterexample
and the smallest control-flow repair that satisfies it, with the three
commissioned comment corrections. This record does not close Phase 11; only an
independent review at `1cc6a3aa` or later can.

### Current phase map

- Phases 1 through 10C: ACCEPTED and CLOSED.
- Phase 11: CANDIDATE at `1cc6a3aa`, awaiting independent review of the residual
  edge-bound repair.
- Phases 12 through 14: NOT STARTED.

## 18.6 GitHub Actions at the ledger head `16b7c9a3`

Section 18.5's own commit. The tree differs from the implementation SHA
`1cc6a3aa` by exactly one file -- this document -- so nothing here is evidence
about the catalog repair either way. The state below was queried at
`2026-08-20T00:19:37Z` and is reported as observed, not as a verdict; re-query
it at the exact SHA before accepting this handoff.

Thirty-nine check runs, every workflow at `run_attempt=1`, no reruns requested:

- Thirty-six completed success, including `ci-static`, `ci-gates`, `ci-engine`,
  `ci-oci`, `ci-dashboard`, `ci-e2e-envtest`, `ci-e2e-compose`,
  `ci-integration-kubernetes`, `dashboard-e2e`, `operator-build`,
  `artifact-drift`, `release-version-test`, `release-dry-run`, `changes`,
  `bundle`, `docs-check`, `repowise`, `validate`, the whole Security workflow
  (`Trivy`, `Trivy (image)`, `govulncheck`, `govulncheck (Go)`, `PR security
  summary`, and `Analyze` for `actions`, `go`, `javascript-typescript` and
  `python`), and five of the six Kind shards -- `dashboard`, `upgrade`,
  `reconcile`, `evidence`, `observation`.
- `build` and `auto-merge`: skipped, as always on this PR.
- `CodeQL`: failure -- the inherited-alerts aggregate carried since section 8,
  unchanged and not a finding of this repair.
- **`ci-e2e-kind (operational-graph)`: still `in_progress`, and disclosed rather
  than waited out.** It started at `2026-08-19T21:10:58Z` and was still inside
  `make test-acceptance-kind-operational-graph` three hours later. On the
  identical product tree at `1cc6a3aa` the same shard finished green in 13
  minutes 30 seconds (`20:31:45Z` to `20:45:15Z`). The job declares no
  `timeout-minutes`, so GitHub will terminate it at the 360-minute default. In
  consequence, CI run `32302431053` and the `required` aggregate had not
  concluded at the observation time.

Two things follow, and neither is an inference about `pkg/catalog`. First, the
full green matrix for this repair is the one at the implementation SHA
`1cc6a3aa` in section 18.5, where all 21 CI jobs -- including this same shard --
passed on the first attempt. Second, this hang was NOT investigated or worked
around: the commission forbids broadening into Kind or image-loading work
without a reproducible source regression, one markdown file cannot be that
regression, and no rerun, cancellation or harness change was made. It joins the
disclosed harness-flake history for this shard family, alongside the
`ci-e2e-kind (reconcile)` failure recorded at the docs-only SHA `a5fb3ecd` in
section 18.4 -- which, for the record, did not recur at either SHA in this pass.

Phase 11 remains a CANDIDATE at `1cc6a3aa`. Phase 12 is not started.

## 18.7 The lint gate turned red at `2a6c953a`, and why it is not this repair

Section 18.6's own commit is the head `2a6c953a`. Its tree differs from the
implementation SHA `1cc6a3aa` by exactly one file -- this document. State below
queried at `2026-08-20T01:04Z`.

Forty check runs: thirty-five success, two skipped (`build`, `auto-merge`) and
three failure -- `ci-static`, the `required` aggregate that reads it and the
inherited `CodeQL` alerts carried since section 8. All six Kind shards passed,
`ci-e2e-kind (operational-graph)` among them in 7 minutes 7 seconds
(`00:21:14Z` to `00:28:21Z`), so the three-hour hang disclosed in section 18.6
did not recur and is now on the record as a one-off.

`ci-static` failed in `make ci-lint`, with three staticcheck findings in files
this branch does not touch:

```
internal/cli/login_test.go:193:2:  SA9010: deferred return function not called
internal/cli/logout_test.go:185:2: SA9010: deferred return function not called
internal/cli/logout_test.go:382:2: SA9010: deferred return function not called
        defer oci.SetUserHomeDirFn(old)
```

**Cause: the linter version floated, the source did not.** `.github/actions/ci`
pins the *action* by commit (`golangci/golangci-lint-action` v9.2.0) but passes
`install-only: true` with no `version:`, so the *binary* is whatever `latest`
resolves to at run time. The job log says so directly: `1cc6a3aa` at
`2026-08-19T20:32:56Z` used **v2.12.2**, `2a6c953a` at `2026-08-20T00:21:51Z`
used **v2.13.0**, which was published at `2026-08-19T23:29:17Z` -- between the
two runs. The analysis-cache key carries the same fact (`golangci-lint.cache-
Linux-2954-...` hit, then `...-2955-...` miss).

Isolated locally at this exact head, each with its own cold cache directory so
no warm cache could mask a finding:

| Run | Result |
|---|---|
| v2.12.2, `./internal/cli/...` | `0 issues.` |
| v2.13.0, `./internal/cli/...` | the identical three SA9010 findings |
| v2.13.0, `./pkg/catalog/...` | `0 issues.` |

So the new linter finds nothing in the repair, and the version -- not a stale
cache and not this branch -- is what changed the verdict.

**Provenance: inherited from `main`.** `git diff 83f2e66d...HEAD` reports no
change to either test file, the three sites were authored on 2026-03-04 in
`a8ff7c26`, and `SetUserHomeDirFn`'s value-returning signature already exists at
the merge base. `main` will fail its next lint run the same way; nothing about
that is specific to this PR.

**And the finding is a false positive for this idiom.** SA9010 exists for
`defer f()` where `f` *returns* the cleanup you meant to run. Here the returned
value is the hook being replaced, not a cleanup: `defer oci.SetUserHomeDirFn(old)`
restores the old hook and discards the older one, which is exactly right. A
repair would be a reasoned `nolint` or `defer func() { _ = oci.SetUserHomeDirFn(old) }()`
-- but the load-bearing one is to pin the linter version in the action, so
`latest` cannot move a required gate underneath an open review again.

**None of that was done here, deliberately.** It is unrelated to Phase 11, it
touches files this repair has no business in, and it belongs on `main` rather
than smuggled into this branch as drift. No gate was weakened to get past it:
nothing was disabled, excluded, `nolint`-ed or downgraded, and the red check is
left red and named. In consequence this section's own commit will produce a head
whose `ci-static` fails identically until the linter question is settled on
`main`; the full green matrix for the repair remains the one at `1cc6a3aa` in
section 18.5.

Phase 11 remains a CANDIDATE at `1cc6a3aa`. Phase 12 is not started.

## 18.8 Final CI record at the ledger head `65a5d444`

Section 18.7's own commit. Its tree still differs from the implementation SHA
`1cc6a3aa` by exactly one file -- this document. This is the LAST CI-state
section: each one produces a new head with a new state, and documenting that
forever is a regress. Re-query at the head instead.

Forty check runs at `run_attempt=1`, no reruns requested: thirty-four success,
two skipped (`build`, `auto-merge`) and four failure -- `ci-static`,
`ci-e2e-kind (dashboard)`, the `required` aggregate that reads both and the
inherited `CodeQL` alerts carried since section 8.

**`ci-static`: the same cause as 18.7, with one detail 18.7 could not know.**
Again v2.13.0, again three SA9010 findings, but naming different files --
`pkg/oci/cache_test.go:217`, `:321`, `:470` instead of the two `internal/cli`
tests. The report is truncated, not moved. There is no root golangci-lint
configuration (only `integrations/kubernetes/.golangci.yml`), so the defaults
apply and `max-same-issues: 3` cuts the list; which three survive depends on
package order and cache warmth. Run at this head with the caps lifted
(`--max-same-issues=0 --max-issues-per-linter=0`), v2.13.0 reports **ten**, every
one the same `defer oci.SetUserHomeDirFn(old)` idiom, across
`internal/cli/login_test.go`, `internal/cli/logout_test.go`,
`internal/update/check_test.go`, `pkg/oci/cache_test.go` and
`pkg/oci/credentials_test.go`.

All ten are inherited. `git grep -c` counts ten at the merge base `83f2e66d` and
ten at this head, and the branch diff adds none and removes none. The tests this
branch did add to `pkg/oci/cache_test.go` use `t.Cleanup(func() {
oci.SetUserHomeDirFn(old) })`, which SA9010 does not flag -- so the branch, where
it wrote this idiom at all, already wrote the form the new linter accepts. The
diagnosis and the deliberate non-repair in 18.7 stand unchanged.

**`ci-e2e-kind (dashboard)`: the image-load flake, recurring.** It failed after
3 minutes 31 seconds inside `make test-acceptance-kind-dashboard`, in the
harness and before any test ran:

```
kindload: localhost:5001/pacto-dashboard:e2e-modes: node pacto-mono-control-plane:
localhost:5001/pacto-dashboard:e2e-modes is not present after loading it
```

That is the loader's own post-load verification -- the check added in Phase 10 so
a silent partial load could not pass as success -- reporting the image absent on
the node. The same shard passed at `1cc6a3aa` and at `2a6c953a` on a product tree
identical to this one, so no source regression can explain it, and one markdown
file certainly cannot. It is disclosed as a recurrence of the image-load flake
family, not diagnosed further: the commission forbids broadening into Kind or
image-loading work absent a reproducible source regression at the exact SHA, and
there is none. It was not rerun either -- `rerun-failed-jobs` restarts all 21
jobs because they gate on `changes`, and no rerun could turn `required` green
while `ci-static` stands.

Neither red is a finding about `pkg/catalog`, and v2.13.0 run against
`./pkg/catalog/...` reports `0 issues.` The full green matrix for this repair
remains the one at `1cc6a3aa` in section 18.5.

Phase 11 remains a CANDIDATE at `1cc6a3aa`. Phase 12 is not started.

## 18.9 Independent review at `0348012a` -- Phase 11 ACCEPTED and CLOSED

Independent review date: 2026-08-20. Reviewed implementation range:
`9a737191bb02ce689541bd8485093fe3a4e94977..1cc6a3aa1d82345333c7f658107137d1e5a1c3de`.
Reviewed remote ledger head:
`0348012ace175ef9323e14d4c6c2edcdf68816df`.

### Repository, history and scope

- PR 291 is OPEN, DRAFT and MERGEABLE on
  `feat/operational-graph-fleet`; local HEAD, the remote branch and the PR head
  were exactly `0348012a` before this review record.
- `origin/main` and the merge-base remain
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`. `9a737191` remains an
  ancestor. The range is exactly five linear, single-parent commits: the one
  implementation commit `1cc6a3aa` followed by four additive ledger commits.
  There is no merge or evidence of amend, rebase, squash, reset or force-push.
- The implementation changes exactly five files under `pkg/catalog`, +126 / -19.
  The remaining range changes only `.pr-context/PACTO_PR_CURRENT_STATE.md`.
  TARGET, the iteration protocol, testing documentation, Make and workflows are
  untouched. Phase 12 has not started.

### Residual blocker A is closed

The repair is the smallest correct control-flow change. `expand` still admits
dependency work in declaration order, and the first previously unseen work item
that `MaxEdges` refuses produces one `EDGE_LIMIT_EXCEEDED` limitation and one
representative `BOUND_EXCEEDED` unresolved entry. It then breaks instead of
walking the refused tail.

That break does not discard work that could have been admitted. A declaration's
structured identity includes its declaring `RevisionID` and unique index;
`admitEdgeWork` returns false only for a previously unseen key after the global
budget is full. Within one expansion, later indexes are also previously unseen
and cannot become admissible. Repeated routes still pass the already-admitted
prefix, distinct bases still spend distinct work, failed attempts still spend
budget, and neither memoization nor path provenance changed.

The package-local test is non-vacuous. It runs tails of 10 and 1,000 distinct
dependencies through the real builder and proves the same one admitted work
item, two resolver calls, partial result, edge-bound limitation, representative
gap and constant internal `edgeWork`, `unresolvedSeen` and `limitSeen` sizes.
The unexported `newBuilder` extraction is a direct reuse of `Build`'s existing
initialization, not a framework, public hook or second implementation.

The reviewer independently changed the committed `break` back to `continue`.
`TestEdgeBoundStopsFailingDependencyWorkToo` and
`TestTheEdgeBoundDoesNotWalkTheDeclarationsItRefused` both failed for the
intended reason: the refused tail returned to 10 / 1,000 bookkeeping keys and
multiple bounded unresolved records. The mutation was restored with
`apply_patch`; the focused tests passed again and `git diff --exit-code` proved
that no product byte from the probe remained.

The three comment corrections now agree with the accepted model: `ContentID`
is content identity and one half of canonical `RevisionID`; `MaxEdges` budgets
dependency work, including failures; and the edge limitation names the first
refused declaration as a representative of the unvisited remainder.

Blockers B and C remain closed exactly as accepted in section 18.4. No accepted
Phase-11 or Phase-10C responsibility was reopened.

### Independent verification and external state

- `go test -race -count=1 ./pkg/catalog ./internal/app ./pkg/oci
  ./tests/architecture` passed. The focused edge-bound suite also passed under
  the race detector before the independent mutation.
- `git diff --check` and `make check-section` passed. The worktree returned to
  only the four inherited untracked agent paths `.claude/`, `.codex/`,
  `.mcp.json` and `AGENTS.md` before this ledger append.
- At exact implementation SHA `1cc6a3aa`, CI run `32298985102` succeeded on
  attempt 1 with all 21 jobs green, including all six Kind shards, Compose,
  `ci-static`, `ci-engine`, `ci-gates`, artifact drift, release dry-run and
  `required`. Security, Docs check, Pacto Contract CI, Repowise, PR-title and
  both dynamic CodeQL workflows are also successful on attempt 1. Its 40 check
  runs are 37 success, two expected skips and one failure: the inherited
  aggregate CodeQL condition.
- At current docs-only head `0348012a`, CI run `32319941292` completed on
  attempt 1. Nineteen functional jobs passed, including all six Kind shards and
  Compose. `ci-static` failed and `required` consequently failed. The exact-head
  check-run population is 35 success, two skipped and three failure:
  `ci-static`, `required` and the inherited aggregate CodeQL condition.
- The code-scanning API still returns exactly the nine inherited alerts 38, 40
  through 43 and 59 through 62. None is in `pkg/catalog` or this repair range.
  Review threads were fully paginated: 199 total, 189 resolved and 10
  unresolved, still six on the generated Mermaid bundle and four on
  `pkg/oci/cache.go`. Both deltas are zero.

### Separate inter-phase CI blocker -- the linter binary is floating

The current required-gate failure does not reopen Phase 11, because the exact
implementation SHA passed the complete matrix and the later heads add only this
ledger. It is nevertheless a real branch-state blocker that must be repaired
before Phase 12 starts.

`.github/actions/ci/action.yml` pins `golangci/golangci-lint-action` by commit
but configures only `install-only: true`; it supplies no `version`. The installed
binary therefore moved from v2.12.2 at the green implementation SHA to v2.13.0
at the later ledger heads. v2.13.0 reports SA9010 on the existing
`defer oci.SetUserHomeDirFn(old)` restore idiom. The ten occurrences are already
present at merge-base `83f2e66d`, and the Phase-11 repair adds none. The setter
returns the previous hook; the deferred call itself performs the restore, so
discarding its return value is intentional.

Leaving the installer on `latest` means every Phase-12 commit will inherit a red
required gate and the toolchain can move again mid-review. The next iteration is
therefore a narrow inter-phase CI determinism repair, not Phase 12: pin one
explicit current golangci-lint binary version, express the ten intentional test
cleanups in a form the pinned analyzer understands without `nolint`, and add the
smallest structural proof that removing the version pin is caught. Do not lower,
disable or exclude the analyzer, and do not fix unrelated findings or Kind
flakes. Once that exact head is green and independently reviewed, Phase 12 may
open.

### Verdict and phase map

**Phase 11 is ACCEPTED and CLOSED at implementation SHA `1cc6a3aa`, reviewed
through ledger head `0348012a`.** Its catalog core is bounded for the residual
hostile fan-out, and all three blockers from sections 18.2 and 18.4 are closed.

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: REQUIRED NEXT.
- Phases 12 through 14: NOT STARTED.

No PR comment, review thread or metadata was changed by this review. TARGET and
all previous ledger records remain untouched.

## 19 Inter-phase required-CI determinism repair -- CANDIDATE at `e47e3bb4`

Commissioned by section 18.9's "Separate inter-phase CI blocker", and by nothing
else. This is NOT Phase 12, and Phase 12 was not started. Phase 11 stays
ACCEPTED and CLOSED at `1cc6a3aa`; `pkg/catalog` is not touched by one byte.
This record is a CANDIDATE and closes nothing on its own.

### Range

- Starting SHA (section 18.9's ledger head): `3c3665a17cf6fe355e3099ff10f3e9a1fdab22d7`
- Implementation SHA: `e47e3bb4728cbcbc7c8509a67e054f7755e506d8`
- Merge-base with `origin/main`: `83f2e66d5cd4fab56099991d39e64fc11f107b3d`, unchanged
- Commits appended, in order, no amend, rebase, squash, reset or force-push:
  1. `e47e3bb4` `fix(ci): pin the golangci-lint binary the required gate runs`
  2. this document's own commit, `docs(pr): record the inter-phase CI
     determinism repair as a candidate`

`e47e3bb4` is seven files, +160 / -10:

| file | change |
| --- | --- |
| `.github/actions/ci/action.yml` | `version: v2.13.0` added to the golangci-lint install step, with a comment naming the gate that keeps it there |
| `tests/architecture/golangci_lint_pin_test.go` | NEW -- the structural gate over that step, plus a table proving which spellings the version rule refuses |
| `internal/cli/login_test.go` | 1 hook restore moved from `defer` to `t.Cleanup` |
| `internal/cli/logout_test.go` | 2 hook restores moved from `defer` to `t.Cleanup` |
| `internal/update/check_test.go` | 2 hook restores moved from `defer` to `t.Cleanup` |
| `pkg/oci/cache_test.go` | 3 hook restores moved from `defer` to `t.Cleanup` |
| `pkg/oci/credentials_test.go` | 2 hook restores moved from `defer` to `t.Cleanup` |

No Make target, gate, timeout, coverage threshold, complexity limit, job or
linter configuration changed. No `nolint`, no analyzer exclusion, no
`.golangci.yml` at the root, no second version source, no lock file, no
`.tool-versions`, no new dependency. No production file changed:
`pkg/oci/credentials.go` and `SetUserHomeDirFn` are exactly as they were.

### The blocker -- a required gate with two halves and only one pin

`.github/actions/ci/action.yml` pinned `golangci/golangci-lint-action` by full
commit but passed only `install-only: true`. The action is two things: code that
runs on the runner, and an installer that fetches a linter binary. Only the
first half was pinned. With no `version`, the installer resolves whatever is
latest that day.

That is what turned the branch red between two commits that changed no product
source. Section 18.7 recorded the transition; the repair confirms the mechanism.
v2.13.0's staticcheck added SA9010, "deferred return function not called", and
it fires on the compact hook-restore idiom:

```go
old := oci.SetUserHomeDirFn(func() (string, error) { return "", errNoHome })
defer oci.SetUserHomeDirFn(old)
```

`SetUserHomeDirFn` returns the hook it replaced. The deferred call IS the
restore; its return value is the now-displaced test hook and is intentionally
discarded. The runtime behaviour was always correct. SA9010 sees a deferred
call to a function that returns a function, assumes the returned function is the
cleanup, and reports that it was never invoked.

Ten such occurrences exist, all of them present at merge-base `83f2e66d` and
none of them added by any phase of this PR.

### RED 1 -- the structural gate against the unpinned action

The gate was written and run before `action.yml` was touched. The rule's own
table passed; the gate over the real file failed, naming the missing pin:

```
--- PASS: TestTheLintVersionRuleRejectsEveryFloatingSpelling (0.00s)
    --- PASS: /no_version_key   --- PASS: /empty_value
    --- PASS: /latest           --- PASS: /minor_only
    --- PASS: /major_only       --- PASS: /full_version
=== RUN   TestTheCIActionInstallsOnePinnedGolangciLintBinary
    golangci_lint_pin_test.go:138: .../.github/actions/ci/action.yml installs a
    floating golangci-lint binary: the step sets no `version`, so the installer
    takes whatever is latest that day. The same tree lints clean on one release
    and red on the next, which makes a required check depend on the calendar
--- FAIL: TestTheCIActionInstallsOnePinnedGolangciLintBinary (0.00s)
FAIL	github.com/trianalab/pacto/v3/tests/architecture	0.840s
```

### RED 2 -- v2.13.0 on the unrepaired tree, and the permanent counterexample

v2.13.0 was installed into an isolated prefix, `/tmp/glci2130/bin`, precisely so
that the pre-existing `~/go/bin/golangci-lint` v2.12.2 could not be used by
accident. Both binaries were then run over the SAME unrepaired tree with cold
caches and the issue caps lifted, because the default `max-same-issues: 3`
truncates the SA9010 group to three and there is no root golangci-lint
configuration to change that:

```
$ GOLANGCI_LINT_CACHE=/tmp/glcicache-red2 /tmp/glci2130/bin/golangci-lint run \
    --max-same-issues=0 --max-issues-per-linter=0
internal/cli/login_test.go:193:2: SA9010: deferred return function not called (staticcheck)
	defer oci.SetUserHomeDirFn(old)
	^
internal/cli/logout_test.go:185:2   SA9010 ...
internal/cli/logout_test.go:382:2   SA9010 ...
internal/update/check_test.go:380:2 SA9010 ...
internal/update/check_test.go:392:2 SA9010 ...
pkg/oci/cache_test.go:217:2         SA9010 ...
pkg/oci/cache_test.go:321:2         SA9010 ...
pkg/oci/cache_test.go:470:2         SA9010 ...
pkg/oci/credentials_test.go:252:2   SA9010 ...
pkg/oci/credentials_test.go:377:2   SA9010 ...
10 issues:
* staticcheck: 10
exit=1

$ GOLANGCI_LINT_CACHE=/tmp/glcicache-red-old ~/go/bin/golangci-lint run \
    --max-same-issues=0 --max-issues-per-linter=0
0 issues.
exit=0        # v2.12.2, identical bytes
```

Same tree, same flags, same cold-cache conditions: 10 issues under one release
and 0 under the previous one. The variable is the binary, and the binary had no
pin.

### The repair

Two lines of workflow and ten one-line test edits.

The installer step now pins both halves -- the action by its existing 40-hex
commit, the binary by `version: v2.13.0` -- with a comment naming the gate that
keeps it there.

Each of the ten sites became:

```go
t.Cleanup(func() { _ = oci.SetUserHomeDirFn(old) })
```

Behaviour is preserved exactly. The previous hook is still restored once per
test, the captured `old` is the same value, and no hook leaks between tests.
None of the five affected files contains `t.Parallel()`, so restores still
complete before the next test in the package starts. Relative ordering against
`t.Setenv` and `t.TempDir` cleanups is unchanged in every case except
`TestCachedStore_Pull_SaveErrorIgnored`, where a later-registered `os.Chmod`
cleanup now runs before the hook restore instead of after; neither reads the
home-dir hook, and the test passes under the race detector.

`SetUserHomeDirFn` itself is untouched. So is every one of the twelve
pre-existing `t.Cleanup` call sites, which SA9010 does not flag.

### The shape of the new gate

`tests/architecture/golangci_lint_pin_test.go` is a level-3 architecture test:
a rule about the repository, not about the product. It sits where `ci-gates`
already runs `./tests/architecture/...`, and `/tests/` is excluded from the
100 percent coverage gate, so it adds no coverage surface.

It parses `.github/actions/ci/action.yml` with the root module's already-direct
`gopkg.in/yaml.v3` -- no new dependency -- and resolves the path from
`runtime.Caller(0)` rather than the working directory, the same idiom
`check_section_test.go` uses. Parsing rather than grepping is load-bearing
twice: the `uses:` scalar carries a trailing `# v9.2.0` comment that a regex over
raw text would swallow into the reference, and a step is a structure, not a
spelling.

It asserts four things about the one installer step: that there is EXACTLY one
step using the action, in both directions, so the gate can neither be silenced
by renaming the step nor satisfied while a second, disagreeing version source
exists; that the action stays pinned to a 40-hex commit; that `with.version` is
present and a full `vX.Y.Z`; and that `install-only: true` survives, because
that is what keeps `make ci-lint` the single invocation shared by CI and local
runs.

Which spellings the version rule refuses is proved by a table over the pure
validator -- absent key, empty value, `latest`, `v2.13`, `v2` refused; `v2.13.0`
accepted -- rather than by mutating the workflow once per spelling. This follows
the lesson recorded for the compose and Kind gates: forbid a structural
property, and do not let a rule pass because some unrelated line happens to
match its regex.

### Mutation evidence

Four mutations, applied one at a time, each observed failing for the intended
reason, each reverted and proved byte-for-byte with `shasum -a 256 -c` over all
seven files.

1. `version: v2.13.0` -> `version: latest`:
   `TestTheCIActionInstallsOnePinnedGolangciLintBinary` FAILED with
   "`version: latest` is not a full immutable version like v2.13.0".
2. the `version` key removed entirely: the same test FAILED with "the step sets
   no `version`, so the installer takes whatever is latest that day". This is
   exactly the pre-repair state, reproduced by the gate.
3. the action reference changed to `golangci/golangci-lint-action@v9.2.0` with
   the version pin left correct: the same test FAILED with "pins the action as
   ...; it must stay `golangci/golangci-lint-action@<40-hex commit>`". The two
   halves are gated independently.
4. one converted site restored to `defer oci.SetUserHomeDirFn(old)`
   (`internal/cli/login_test.go:193`): v2.13.0 with a cold cache and the caps
   lifted reported `internal/cli/login_test.go:193:2: SA9010: deferred return
   function not called (staticcheck)`, `1 issues`, exit 1.

After the last revert the seven checksums matched and a cold v2.13.0 run over
the clean tree returned `0 issues.`, exit 0.

### Local verification, all green at `e47e3bb4`

Every golangci-lint invocation below used the isolated `/tmp/glci2130/bin`
binary, confirmed by `command -v golangci-lint` resolving to
`/tmp/glci2130/bin/golangci-lint` and by `golangci-lint version` printing
`golangci-lint has version 2.13.0`.

- `go test -race -count=1 ./tests/architecture` -- ok, 5.129s. Both new tests
  pass, and so does the rest of the package.
- `go test -race -count=1 ./internal/cli ./internal/update ./pkg/oci` -- all
  three ok (30.9s, 2.3s, 22.3s).
- cold-cache `golangci-lint run --max-same-issues=0 --max-issues-per-linter=0`
  under v2.13.0 -- `0 issues.`, exit 0.
- `make ci-test` -- `total coverage: 100.0%`, example tests ok,
  `DEMO-CONTRACTS VALID: 24/24`. `pkg/oci` again reports 99.8 percent in its own
  per-package line and 100.0 percent in the merged profile the gate reads, the
  same figures recorded in section 18.5.
- `make ci` with `/tmp/glci2130/bin` first in PATH -- exit 0 end to end:
  `ci-static` (fmt, vet, gocyclo, `golangci-lint run` -> `0 issues.`,
  `check-section`, CLI-docs drift, UI-build drift, dashboard-SDK drift, plus the
  operator module's own `ci-static` at `0 issues.` using its separately pinned
  v2.8.0 plugin build), `ci-gates` (`tests/architecture` ok 8.882s,
  `tests/release` ok 9.440s), `ci-engine`
  (`operational-graph acceptance PASSED`), `ci-dashboard`,
  `ci-integration-kubernetes`, `ci-e2e-envtest`, `ci-oci`, and the release
  orchestrator's 16 Node tests.
- `make artifact-drift` -- `artifact-drift: OK`.
- `make release-dry-run` -- `RELEASE-DRY-RUN OK: real artifacts to
  localhost:5001, digest idempotency + immutability + resume proven, no
  production coordinate`, and `K8S-MODULE-STANDALONE OK`.
- `govulncheck ./...` -- `No vulnerabilities found.`
- `git diff --check` -- clean. `make check-section` -- `zero U+00A7 in authored
  files`. `gofmt -l` over the six changed Go files -- no output.
- The six Kind shards were not attempted locally, for the Docker Desktop
  containerd reason recorded at `ci.mk:88-90`. They run in CI, below.

### GitHub at the exact implementation SHA `e47e3bb4`

Forty check runs, every workflow at `run_attempt=1`, no rerun anywhere.
Thirty-seven success, two skipped (`build`, `auto-merge`) and one failure, the
aggregate `CodeQL` check, which is the inherited-alerts item carried since
section 8 and is not a finding of this repair. This is the identical population
and the identical single failure recorded at `1cc6a3aa` in section 18.5.

- **CI**, run `32339908832`, attempt 1, success -- all 21 jobs green:
  `changes`, `ci-static`, `ci-gates`, `ci-engine`, `ci-oci`, `ci-dashboard`,
  `ci-e2e-envtest`, `ci-e2e-compose`, `ci-integration-kubernetes`,
  `dashboard-e2e`, `operator-build`, `artifact-drift`, `release-version-test`,
  `release-dry-run`, `required`, and all six Kind shards individually --
  `ci-e2e-kind (dashboard)`, `(upgrade)`, `(reconcile)`, `(evidence)`,
  `(observation)`, `(operational-graph)`. No shard hit the disclosed
  image-load family, so no Kind or image-loading code was investigated or
  changed.
- **`ci-static` ran exactly v2.13.0, not `latest` and not v2.12.2.** Its job log
  (`96336783067`) shows the action resolved from the pinned commit
  `1e7e51e771db61008b38414a730f564565cf7c20`, then:

  ```
  ##[group]Run golangci/golangci-lint-action@1e7e51e771db61008b38414a730f564565cf7c20
    version: v2.13.0
  ...
  Finding needed golangci-lint version...
  Installation mode: binary
  Installing golangci-lint binary v2.13.0...
  Downloading binary https://github.com/golangci/golangci-lint/releases/download/v2.13.0/golangci-lint-2.13.0-linux-amd64.tar.gz ...
  Installed golangci-lint into /home/runner/golangci-lint-2.13.0-linux-amd64/golangci-lint
  ...
  ==> Running linter...
  golangci-lint run
  0 issues.
  ```

  The version now appears in the rendered step inputs, which is the observable
  difference from the runs section 18.7 diagnosed. The second `0 issues.` later
  in the same job is the Kubernetes module's own v2.8.0 build, unchanged.
- **Security**, run `32339908839`, attempt 1, success: `Trivy (image)`,
  `govulncheck (Go)` and `PR security summary`. The path-filtered `Trivy` and
  `govulncheck` variants did not materialize as separate jobs for this diff.
- **Docs check** `32339908700` (job `docs-check`), **Pacto Contract CI**
  `32339908674` (job `bundle`), **Repowise (architecture health)** `32339908724`
  (job `repowise`) and **Validate PR title** `32339908682` -- all attempt 1, all
  success.
- The two dynamic CodeQL runs, **PR #291** `32339904891` (`Analyze` for `go`,
  `python`, `javascript-typescript`, `actions`) and **Code Quality: PR #291**
  `32339905032` (`Analyze` for `python`, `go`, `javascript-typescript`), both
  attempt 1, both success. The failing `CodeQL` CHECK is published by
  `github-advanced-security`, not by those runs, and reports the same "8 new
  alerts including 8 high severity security vulnerabilities" wording as at
  `1cc6a3aa`. A green Security workflow and the CodeQL alert attribution remain
  two different claims.
- **Auto-merge Dependabot PRs** `32339908939` and **Rebuild dashboard UI**
  `32339908782` -- skipped, as expected on this PR.

### CodeQL and review threads

Both populations were re-queried at `e47e3bb4`, and the delta introduced by this
repair is ZERO in each.

- Code-scanning API, `state=open` on `refs/pull/291/head`: the same nine
  inherited alerts at the same lines -- 38
  (`py/incomplete-url-substring-sanitization`, `release/scripts/docs_check.py:197`),
  40 through 43 (`go/path-injection`, `internal/app/resolve.go` 35, 43, 57, 67)
  and 59 through 62 (`go/path-injection`, `pkg/oci/cache.go` 375, 394, 395,
  666). None was added, silenced or dismissed here. The whole item stays OPEN
  for independent triage before Phase 14 readiness. `pkg/oci/cache_test.go`
  changed in this repair; `pkg/oci/cache.go`, which carries four of the alerts,
  did not.
- Review threads, fully paginated across both pages, since the API caps a page
  at 100 and page one hides every unresolved thread: 199 total, 189 resolved,
  10 unresolved. The unresolved ten are unchanged -- six `github-code-quality`
  comments on the generated minified Mermaid chunk
  `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four
  `github-advanced-security` comments on `pkg/oci/cache.go`. No thread was
  resolved, replied to or opened by this pass.

### Hygiene and disclosures

- The PR is OPEN and DRAFT and MERGEABLE throughout. No PR comment was
  published, no review thread was resolved or replied to, and no PR metadata --
  title, body, labels, reviewers, draft state -- was changed.
- Append-only: `e47e3bb4` and this document's commit sit directly on top of
  `3c3665a1`, which remains an ancestor of HEAD. No amend, rebase, squash,
  reset, cherry-pick or force-push. The merge-base with `origin/main` is
  unchanged. The branch ref and HEAD were confirmed identical before the push.
- `PACTO_PR_TARGET_STATE.md` was not modified. No previous section of this
  document was rewritten; section 19 is an append. Section 18.9 is untouched and
  Phase 11 remains ACCEPTED and CLOSED.
- All three tracked `.pr-context/` files and `docs/maintainers/testing.md` were
  read in full before anything changed.
- The four inherited untracked paths `.claude/`, `.codex/`, `.mcp.json` and
  `AGENTS.md` are preserved and uncommitted.
- `make ci` again regenerated the hand-written
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md`. It was restored
  with `git checkout --` and not committed; the helm-docs drift check itself
  passes. No `go.work.sum` drift appeared this pass -- the isolated v2.13.0
  install was performed from `/tmp` with `GOWORK=off` for exactly that reason.
  Outside those, the worktree held nothing but the seven changed files.
- No authored frontend input changed, so the committed UI bundle was NOT
  rebuilt, and `ci-ui-drift` and `check-dashboard-sdk-drift` are clean against
  the existing one.
- **Idiom inconsistency, disclosed rather than tidied.** The commissioned form
  is `t.Cleanup(func() { _ = oci.SetUserHomeDirFn(old) })`, and that is what the
  ten converted sites use. The twelve pre-existing `t.Cleanup` sites in
  `pkg/oci/cache_test.go`, `pkg/oci/cache_coherence_test.go` and
  `pkg/oci/matrix_test.go` write the same call without the `_ =`. SA9010 does
  not flag either form -- that is why only 10 of the 22 total call sites were
  ever red -- so the blank identifier here is explicitness, not an analyzer
  requirement. Aligning the other twelve would be the unrelated test refactor
  this repair was told not to perform, so they were left alone.
- **The stale `ci.mk` comment was left alone.** The comment above `ci-gates` at
  `ci.mk:25-31` enumerates the architecture gates and already omits the Kind and
  catalog gates added in earlier phases; it now also omits this one. Correcting
  it is a real but separate tidy, and widening this repair to reach it was
  judged worse than disclosing it.

### Deliberately not done

- No linter, analyzer or check was suppressed, disabled, excluded or downgraded.
  No `nolint` directive was added for SA9010, no root `.golangci.yml` was
  created, and no threshold, timeout, race, coverage or complexity setting was
  relaxed.
- No second source of truth for the linter version: no lock file, no
  `.tool-versions`, no `.golangci-lint-version`, no Make variable. The gate
  actively forbids one by requiring exactly one installer step.
- No new dependency. The gate uses `gopkg.in/yaml.v3`, already a direct
  requirement of the root module.
- No change to `SetUserHomeDirFn` or any production file, and no change to the
  twelve pre-existing cleanup sites.
- No change to `pkg/catalog`, no reopening of Phase 11, and no Phase 12 work --
  no MCP server, tool, resource, CLI wiring, protocol E2E or documentation.
- No Kind or image-loading investigation: all six shards passed on attempt 1, so
  nothing in this repair gave cause to touch that harness.
- No change to the nine inherited CodeQL alerts, which stay OPEN and outside
  this repair's scope.

### Verdict

**The inter-phase required-CI determinism repair is a CANDIDATE at
`e47e3bb4`.** The required gate is green again, and it is green for a reason
that a future upstream release cannot quietly undo: the binary is pinned, and a
structural architecture test fails if the pin is removed, emptied, loosened to
`latest` or to a minor-only version, duplicated into a second source, or if the
action itself drifts back to a tag. This record does not close the repair; only
an independent review at `e47e3bb4` or later can.

### Current phase map

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: CANDIDATE at `e47e3bb4`, awaiting
  independent review.
- Phases 12 through 14: NOT STARTED.

## 19.1 GitHub Actions at the ledger head `c87d945e`

Section 19's own commit. The tree differs from the implementation SHA
`e47e3bb4` by exactly one file -- this document -- so nothing here is evidence
about the repair's product behaviour. It is recorded because a docs-only head is
precisely the case that exposed the blocker in the first place: sections 18.6
through 18.8 describe ledger commits that turned `ci-static` and `required` red
while the implementation SHA below them was green.

That no longer happens.

At `c87d945ea27b835f58c4b1224897639a96c4a05c`, CI run `32341429604` succeeded on
attempt 1 with all 21 jobs green, including `ci-static`, `ci-gates`, all six
Kind shards, Compose, artifact drift, release dry-run and `required`. Its
`ci-static` job log (`96341204308`) again shows `version: v2.13.0` in the
rendered step inputs, `Installing golangci-lint binary v2.13.0...` and
`0 issues.` -- the same binary the implementation SHA used, on a different day's
run, which is the whole point of the pin.

Security `32341429605`, Docs check `32341429584`, Pacto Contract CI
`32341429692`, Repowise `32341429562`, Validate PR title `32341429603` and both
dynamic CodeQL runs (`32341423941`, `32341424369`) are all attempt 1 and all
successful. Rebuild dashboard UI and Auto-merge Dependabot PRs are skipped as
expected.

The check-run population is identical to the implementation SHA's: 40 runs, 37
success, two skipped (`build`, `auto-merge`) and one failure, the inherited
aggregate `CodeQL` condition.

The PR is OPEN, DRAFT and MERGEABLE at this head; `origin/main` and the
merge-base are still `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.

## 19.2 Independent review at ledger head `66a25b7d` -- NARROWLY REOPENED

Independent review covered the append-only range
`3c3665a17cf6fe355e3099ff10f3e9a1fdab22d7..66a25b7d5c1f700d5b9e46b157351dbb9e93ff6c`,
with implementation tree `e47e3bb4728cbcbc7c8509a67e054f7755e506d8`.
The three commits are linear, single-parent descendants of `3c3665a1`; the
merge-base with `origin/main` remains
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`. The PR remains OPEN, DRAFT and
MERGEABLE, with remote head exactly `66a25b7d`. `PACTO_PR_TARGET_STATE.md`,
Phase 11 product code and Phase 12 are untouched.

### Accepted parts

- `.github/actions/ci/action.yml` keeps the action pinned to the existing
  40-hex commit and now pins the installed binary to `v2.13.0`; there is no
  second version source and `install-only: true` remains intact.
- The ten inherited SA9010 sites were changed only from an uncalled deferred
  return function to explicit `t.Cleanup` callbacks. No production behaviour or
  linter configuration changed. Focused race tests for `tests/architecture`,
  `internal/app` and `pkg/oci` pass.
- The structural gate parses the composite action, requires exactly one
  installer, a commit-pinned action, a full `vX.Y.Z` binary version and
  `install-only: true`. Independent mutation of `version: v2.13.0` to
  `version: latest` makes the permanent gate fail for the intended reason;
  after restoration the tracked tree is clean.
- GitHub independently corroborates the runtime pin. In implementation run
  `32339908832`, job `96336783067` logs `version: v2.13.0`, installs exactly
  v2.13.0 and reports `0 issues.`. All 21 CI jobs pass at `e47e3bb4`, and all
  21 also pass at ledger head `66a25b7d` in run `32342315009`, including
  `ci-static`, all six Kind shards, Compose and `required`. The other expected
  workflows pass; the aggregate CodeQL check remains the inherited failure.
- Review-thread pagination still yields 199 total, 189 resolved and 10
  unresolved: six on the generated Mermaid bundle and four on
  `pkg/oci/cache.go`. None belongs to this repair.

### Blocker A -- the repair introduces a prohibited and unnecessary linter suppression

`tests/architecture/golangci_lint_pin_test.go:53` adds:

```go
b, err := os.ReadFile(path) //nolint:gosec // a path this test computed
```

This contradicts the narrow commission's explicit requirement that no
`nolint`, analyzer exclusion or other linter suppression be introduced. It
also makes section 19's claims that no linter was suppressed and no `nolint`
directive was added materially false: the directive targets `gosec` rather
than SA9010, but it is still a new linter suppression in this repair.

The suppression is not required. The reviewer removed only the trailing
`//nolint:gosec` comment temporarily and ran the exact commissioned binary,
golangci-lint v2.13.0, over `./tests/architecture/...` with a fresh dedicated
cache. The result was `0 issues.`, exit 0. The original file was then restored
byte-for-byte and the tracked tree returned clean. Therefore the narrow repair
is simply to delete that directive; no read-path redesign, configuration
change, exclusion or replacement suppression is justified.

### Independent verification

- `go test -race ./tests/architecture/... ./internal/app/... ./pkg/oci/...` --
  all pass.
- golangci-lint v2.13.0 over `./tests/architecture/...` after temporary removal
  of the directive -- `0 issues.`, exit 0.
- `git diff --check` over the reviewed range -- clean.
- PR head checks at `66a25b7d`: CI 21/21 success; Security, Docs check, Pacto
  Contract CI, Repowise, PR-title and both dynamic CodeQL workflows success;
  expected rebuild and auto-merge jobs skipped; inherited aggregate CodeQL
  condition still failing with no evidence of a repair delta.

### Verdict and phase map

**The inter-phase required-CI determinism repair is NARROWLY REOPENED on
Blocker A.** Its functional substance is accepted: the binary is deterministic,
the SA9010 findings are correctly repaired, the architecture gate bites and
required CI is green on implementation and ledger heads. Closure is withheld
solely because the repair violates its explicit no-suppression boundary and
then records the opposite claim. Remove the unnecessary directive, append a
truthful repair record, prove the exact final SHA locally and on GitHub, and
return for independent review.

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: NARROWLY REOPENED on Blocker A.
- Phases 12 through 14: NOT STARTED.

## 19.3 Blocker A closure -- the gosec suppression removed, CANDIDATE at `e639f4b0`

Section 19.2's Blocker A, repaired, and nothing else. Sections 19, 19.1 and 19.2
are unchanged; this record is an append, and it corrects the earlier claim rather
than editing it.

### The correction section 19 owes the record

**The new `gosec` suppression that independent review identified has been
removed.** `tests/architecture/golangci_lint_pin_test.go:53` read

```go
b, err := os.ReadFile(path) //nolint:gosec // a path this test computed
```

and now reads

```go
b, err := os.ReadFile(path)
```

**Section 19's broad claim was inaccurate.** It stated that the repair
introduced no `nolint`, no analyzer exclusion and no linter suppression, and it
repeated the narrower claim that no `nolint` directive was added for SA9010. The
narrow SA9010 statement was true. The broad statement was not: the repair did
add a linter suppression, aimed at `gosec` rather than at SA9010, in the very
test file it introduced. Section 19.2 was right to reopen on it, and this
section records the inaccuracy explicitly rather than leaving the two statements
to be reconciled by a reader.

**No replacement suppression or linter relaxation was introduced.** Nothing took
the directive's place: no second `nolint` in any spelling, no analyzer
exclusion, no root `.golangci.yml` (there still is none; the only linter
configuration in the repository remains
`integrations/kubernetes/.golangci.yml`, byte-identical), no severity or
`max-issues` change, no wrapper, helper, abstraction or read-path redesign. The
`os.ReadFile(path)` expression, the `runtime.Caller(0)` path resolution above it
and the error handling below it are untouched.

**The pinned binary is clean without the directive.** golangci-lint **v2.13.0**
-- the exact version the accepted pin installs, not `latest` and not the v2.12.2
that happens to be first on this machine's PATH -- was run over
`./tests/architecture/...` on a freshly created, empty, dedicated cache
directory. It reported `0 issues.` and exited 0. The same binary on a second
cold cache reported `0 issues.` over the whole root module, which is the
statement that also covers the ten SA9010 conversions.

**Everything section 19.2 accepted is unchanged.** `.github/actions/ci/action.yml`
is byte-identical: the action stays pinned to its existing 40-hex commit, the
binary stays pinned to `v2.13.0`, `install-only: true` stays. The ten SA9010
`t.Cleanup` conversions in `internal/cli/login_test.go`,
`internal/cli/logout_test.go`, `internal/update/check_test.go`,
`pkg/oci/cache_test.go` and `pkg/oci/credentials_test.go` are byte-identical.
The structural gate's design is unchanged -- the same two tests, the same
`compositeStep` parse, the same commit-pin, version and `install-only` rules,
the same table of refused floating spellings.

### Range

- Starting SHA (section 19.2's ledger head): `57953f5bff9e0b498fdd280829726448e34d975e`.
- Implementation SHA: `e639f4b0a29d5a30af80057334c32080777c3e49`.
- Ledger SHA: this document's own commit, whose remote state is reported in the
  handoff rather than in a further section, because each CI-state section
  produces a new head with a new state.
- `origin/main` and the merge-base with it remain
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`, unchanged.
- Append-only: `e639f4b0` is a single-parent child of `57953f5b`, pushed as the
  fast-forward `57953f5b..e639f4b0`. No amend, rebase, squash, reset,
  cherry-pick or force-push. `66a25b7d` and `3c3665a1` both remain ancestors.
- PR `TrianaLab/pacto#291` stayed OPEN, DRAFT and MERGEABLE throughout.

`e639f4b0` is one file, +1 / -1:

| File | Change |
|---|---|
| `tests/architecture/golangci_lint_pin_test.go` | the trailing `//nolint:gosec` directive deleted from line 53 |

No production file changed. No workflow, Make target, script, threshold,
timeout, coverage requirement or linter configuration changed.

### Local verification at `e639f4b0`

- The removed directive is absent from the tree and from the reviewed delta:
  `git diff 57953f5b..e639f4b0` is exactly the one-line deletion shown above.
- `grep -rn nolint tests/architecture/` returns one line, and it is not this
  file -- see the disclosure below.
- `go test -race -count=1 ./tests/architecture/...` -- ok, 7.082s. The permanent
  gate `TestTheCIActionInstallsOnePinnedGolangciLintBinary` and its companion
  `TestTheLintVersionRuleRejectsEveryFloatingSpelling` both pass.
- golangci-lint **v2.13.0** over `./tests/architecture/...`, `GOLANGCI_LINT_CACHE`
  pointed at a directory created empty for the run (0 files before, 686 after,
  so it was genuinely cold) -- `0 issues.`, exit 0.
- golangci-lint **v2.13.0** over the whole root module, second cold dedicated
  cache -- `0 issues.`, exit 0.
- `git diff --check` -- clean.
- `make check-section` -- `zero U+00A7 in authored files`.
- `make ci` -- exit 0 end to end: `ci-static` (fmt, vet, gocyclo, lint
  `0 issues.`, `check-section`, CLI-docs drift, UI build and drift,
  dashboard-SDK drift, plus the Kubernetes module's own fmt, vet and lint at
  `0 issues.`), `ci-gates`
  (`go test ./tests/architecture/... ./tests/release/...`, both ok),
  `ci-engine` (`total coverage: 100.0%` under the race detector, example tests,
  `DEMO-CONTRACTS VALID: 24/24`), `ci-dashboard`, `ci-integration-kubernetes`
  (`total coverage: 100.0%`, chart lint, template renders, Helm unit tests,
  chart schema, helm-docs drift, generated-docs drift), `ci-e2e-envtest` and
  `ci-oci`.
- `make artifact-drift` -- `artifact-drift: OK`.

The six Kind shards, `ci-e2e-compose` and `dashboard-e2e` were not attempted
locally, for the Docker Desktop containerd reason recorded at `ci.mk:88-90`.
They ran in GitHub CI and are green there, below. No gate was weakened, skipped
or reconfigured to reach any of these results.

### GitHub Actions at the exact implementation SHA `e639f4b0`

Forty check runs: thirty-seven success, two skipped and one failure, the
inherited aggregate `CodeQL` check described below. Every workflow ran at
`run_attempt=1`; no rerun was requested and none was needed.

CI run `32345841250`, attempt 1, **success**, all 21 jobs green:

| Job | ID |
|---|---|
| `changes` | `96354310966` |
| `ci-static` | `96354363210` |
| `ci-engine` | `96354363239` |
| `ci-integration-kubernetes` | `96354363244` |
| `ci-dashboard` | `96354363276` |
| `ci-e2e-envtest` | `96354363297` |
| `release-dry-run` | `96354363343` |
| `artifact-drift` | `96354363350` |
| `dashboard-e2e` | `96354363354` |
| `release-version-test` | `96354363428` |
| `ci-gates` | `96354363443` |
| `ci-oci` | `96354363450` |
| `operator-build` | `96354363458` |
| `ci-e2e-kind (evidence)` | `96354363483` |
| `ci-e2e-kind (dashboard)` | `96354363489` |
| `ci-e2e-kind (operational-graph)` | `96354363504` |
| `ci-e2e-kind (upgrade)` | `96354363519` |
| `ci-e2e-compose` | `96354363525` |
| `ci-e2e-kind (reconcile)` | `96354363588` |
| `ci-e2e-kind (observation)` | `96354363686` |
| `required` | `96357076041` |

`ci-static` (`96354363210`) corroborates the pin at runtime rather than by
inspection. Its log carries `version: v2.13.0`, then
`Installing golangci-lint binary v2.13.0...`, then
`Installed golangci-lint into /home/runner/golangci-lint-2.13.0-linux-amd64/golangci-lint`,
then `0 issues.` for the root module, `check-section: zero U+00A7 in authored
files` and a second `0 issues.` for the Kubernetes module.

The other workflows at the same SHA:

| Workflow | Run | Jobs | Conclusion |
|---|---|---|---|
| Security | `32345841253` | `govulncheck (Go)` `96354310531`, `Trivy (image)` `96354310861`, `PR security summary` `96354642093` | success |
| Docs check | `32345841260` | `docs-check` `96354310302` | success |
| Pacto Contract CI | `32345841303` | `bundle` `96354310593` | success |
| Repowise (architecture health) | `32345841204` | `repowise` `96354310542` | success |
| Validate PR title | `32345841256` | `validate` `96354310114` | success |
| PR #291 (dynamic CodeQL) | `32345838425` | `Analyze` for `actions` `96354305211`, `python` `96354305441`, `javascript-typescript` `96354305455`, `go` `96354305476` | success |
| Code Quality: PR #291 (dynamic CodeQL) | `32345838338` | `Analyze` for `python` `96354305415`, `go` `96354305583`, `javascript-typescript` `96354305637` | success |
| Rebuild dashboard UI | `32345841234` | -- | skipped |
| Auto-merge Dependabot PRs | `32345841277` | -- | skipped |

### CodeQL and review threads

The aggregate `CodeQL` check from `github-advanced-security` is the single
failure, reporting "8 new alerts including 8 high severity security
vulnerabilities". That is the inherited condition carried since section 8, and a
green Security workflow with seven green `Analyze` jobs remains a different
claim from it.

The code-scanning API returns nine open alerts on `refs/pull/291/head`, and they
are exactly the nine inherited ones -- `38`
(`py/incomplete-url-substring-sanitization`, `release/scripts/docs_check.py:197`),
`40` through `43` (`go/path-injection`, `internal/app/resolve.go` lines 35, 43,
57 and 67) and `59` through `62` (`go/path-injection`, `pkg/oci/cache.go` lines
375, 394, 395 and 666). The analyses at this SHA report `go` 9, `python` 1,
`javascript-typescript` 0 and `actions` 0, the same four numbers recorded at
earlier heads. **The CodeQL delta for this repair is ZERO**: none added, none
removed, none dismissed. None of the nine is in this repair's one-file range.

Review threads were paginated in full, both pages, because the API caps a page
at 100 and page one hides every unresolved one: **199 total, 189 resolved, 10
unresolved**. The ten are unchanged and inherited -- six `github-code-quality`
threads on the generated Mermaid bundle
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four
`github-advanced-security` threads on `pkg/oci/cache.go`. **The thread delta is
ZERO.** No comment was published, no thread resolved or replied to and no PR
metadata -- title, body, labels, reviewers, draft state -- changed.

### Hygiene and disclosures

- **One `//nolint:gosec` remains in `tests/architecture/`, and it is not this
  one.** `tests/architecture/kind_image_loading_test.go:31` carries
  `raw, err := os.ReadFile(path) //nolint:gosec // a path this test computed`.
  It was authored in Phase 10, which is ACCEPTED and CLOSED, it is outside the
  reviewed delta of the CI-determinism repair and section 19.2's Blocker A named
  only `golangci_lint_pin_test.go:53`. Removing it would be unrequested drift
  into closed work, so it is disclosed here rather than touched.
- `make ci` again regenerated
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md` from helm-docs
  (+16 / -41), as it has in every prior pass. It was restored with
  `git checkout --` and not committed; the helm-docs drift check itself passes.
  The generator quirk predates this repair and is not fixed here.
- No `go.work.sum` drift appeared this pass. golangci-lint v2.13.0 was installed
  into an isolated temporary `GOBIN` outside the repository, with `GOWORK=off`
  and from outside the working tree, so neither the workspace nor the module
  files were touched by it.
- No authored frontend input changed, so the committed UI bundle was not
  rebuilt, and `ci-ui-drift` and the dashboard-SDK drift check are clean against
  the existing one.
- The four inherited untracked agent paths `.claude/`, `.codex/`, `.mcp.json`
  and `AGENTS.md` were never touched and are still untracked.
  `git status --short` at the implementation SHA lists those four and nothing
  else.
- No literal U+00A7 was authored in any file or commit message, and
  `make check-section` confirms it.
- All three tracked files under `.pr-context/` -- this document,
  `PACTO_PR_TARGET_STATE.md` and `PACTO_ITERATION_PROTOCOL.md` -- were read in
  full before any change. `PACTO_PR_TARGET_STATE.md` was not modified.
  `docs/maintainers/testing.md` was also read in full; this file remains a
  level-3 architecture test run by `ci-gates`, which is unchanged.

### Deliberately not done

- No replacement suppression, exclusion, configuration change, wrapper, helper,
  abstraction or read-path redesign, and no `errcheck`-style rewrite of the read.
- No revisiting of the accepted action pin, the accepted `v2.13.0` binary pin,
  the ten SA9010 cleanup conversions or the structural test design.
- No production code change, no dependency added, no Make target, workflow
  topology, threshold or linter configuration touched.
- No work on the nine inherited CodeQL alerts or the ten inherited review
  threads, which stay OPEN and outside this repair's scope.
- Phase 12 was not started, and no Phase 12 surface -- MCP tool, MCP resource,
  CLI flag, server route, protocol E2E or discovery documentation -- exists.

### Verdict

**The inter-phase required-CI determinism repair remains a CANDIDATE, now at
`e639f4b0`, and is NOT self-declared CLOSED.** Section 19.2's Blocker A is
closed by deletion, the false half of section 19's claim is corrected on the
record above, and local and remote verification at the exact implementation SHA
are green. Closing the repair is the reviewer's act, not the author's.

### Current phase map

- Phases 1 through 11: ACCEPTED and CLOSED. Phase 11 in particular remains
  ACCEPTED and CLOSED, exactly as section 18.9 left it.
- Inter-phase required-CI determinism repair: CANDIDATE at `e639f4b0`, awaiting
  independent review of this deletion.
- Phases 12 through 14: NOT STARTED.

The PR remains an open draft, and the append-only, no-history-rewrite and
independent-review protocol continues unchanged.

## 20 Phase 12 -- MCP contract catalog discovery, CANDIDATE at `533445a5`

Phase 12 gives the accepted Phase 11 catalog a protocol surface. An agent host
can now start `pacto mcp --root <ref> [--root <ref>]`, read what the catalog is
and how complete it is, read what it contains, and look one revision up by its
full identity. Nothing else was added: no configuration file, no crawler, no
repository-name inference, no persistent store, no daemon, no refresh loop, no
IDP adapter, no marketplace, no vector search, no authorization layer, no
execution proxy and no dynamic activation. No second OCI client, credential
format or cache exists; the roots resolve through the service's existing
catalog resolver, so reference parsing, registry credentials, the OCI cache,
digest pinning and local hashing are the ones the rest of the CLI already uses.

### Range

- Starting SHA (section 19.4's ledger head): `6768ddedf0cb2fba1023549af7b4cfbda5b449bd`.
- Implementation SHAs, in order:
  - `a7b72e55` -- `feat(mcp): a contract catalog, served read-only from a frozen session`
  - `8e0221d2` -- `feat(cli): --root builds the catalog once, before anything can query it`
  - `145b524a` -- `test(integration): catalog discovery, spoken to as an agent host would`
  - `d1d68ad2` -- `docs: discovery is not the fleet, and a session is not a store`
  - `533445a5` -- `test(integration): name the test the comment is describing`
- Implementation head: `533445a55484a662de448b79e37dbb77f2839344`.
- Ledger SHA: this document's own commit, whose remote state is reported in the
  handoff rather than in a further section.
- `origin/main` and the merge-base with it remain
  `83f2e66d5cd4fab56099991d39e64fc11f107b3d`, unchanged.
- Append-only: all five commits are single-parent descendants of `6768dded`,
  pushed as the fast-forwards `6768dded..d1d68ad2` and `d1d68ad2..533445a5`. No
  amend, rebase, squash, reset, cherry-pick or force-push. `e639f4b0`,
  `544eeb30` and `6768dded` all remain ancestors.
- PR `TrianaLab/pacto#291` stayed OPEN, DRAFT and MERGEABLE throughout, and its
  title, body, labels, reviewers and draft state were not touched.

`6768dded..533445a5` is nine files, +2045 / -19:

| File | Change |
|---|---|
| `internal/mcp/catalog.go` | new, 207 lines -- the entire protocol surface |
| `internal/mcp/catalog_test.go` | new, 853 lines -- 16 focused tests over a real in-memory MCP client |
| `internal/cli/mcp.go` | `--root`, mode dispatch, mode exclusivity, catalog construction (+106 / -13) |
| `internal/cli/mcp_catalog_test.go` | new, 306 lines -- 8 tests over the real flag plumbing |
| `tests/integration/mcp_catalog_test.go` | new, 437 lines -- protocol E2E against the built binary over stdio |
| `cmd/gendocs/main.go` | a `Server modes` table and the `--root` paragraph in the generated CLI reference |
| `docs/cli-reference.md` | regenerated by `make gen-cli-docs`, never hand-edited |
| `docs/architecture.md` | a `pkg/catalog` section, and `internal/mcp` rewritten as a four-mode table |
| `docs/mcp-integration.md` | a `Contract catalog discovery` section |

No production file outside `internal/mcp` and `internal/cli` changed. `pkg/catalog`
is byte-identical to the tree section 18.9 accepted: Phase 11 semantics were not
bent to make projection easier. `pkg/fleet` is untouched, `go.mod` and
`go.sum` are untouched, no dependency was added, and no workflow, Make target,
script, threshold, timeout, coverage requirement or linter configuration changed.

### The protocol surface, and why it is the smallest coherent one

Two fixed-URI resources, zero resource templates and exactly one tool:

| Surface | URI or name | Answers |
|---|---|---|
| Resource | `pacto://catalog` | schema version, catalog id, generation time, the bounds that applied, the completeness of the whole answer and every requested root -- including the roots that did not resolve, with their classified reason |
| Resource | `pacto://catalog/closure` | deduplicated revisions with rank and every retained root-to-revision path, resolved dependency edges, unresolved dependencies, conflicts and cycles |
| Tool | `pacto_catalog_revision` | one revision by full identity: service name, domain, content scheme and content digest |

The split between the two resources is by question, not by convenience, and
they share no field. The cheap half carries completeness, which is the half an
agent must never skip; serving it only inside the large half would make
"how much of this is known" expensive to check.

The single tool has to earn its place, and it does, for a reason that is an
identity argument rather than an ergonomic one. A revision's identity is four
structured fields that the Phase 11 core deliberately never joins into a
string, and a domain or a service name may contain `/`, `:`, `%` or arbitrary
UTF-8. A resource template would force exactly the ambiguous encoding and ad
hoc re-parsing that the identity discipline exists to prevent, so the lookup
takes structured tool inputs instead. `TestCatalogRevisionToolCannotConfuseHostileIdentities`
is the standing proof: identities that differ only in where a `/` falls do not
collide, and a slash-left spelling misses rather than matching its neighbour.

A miss carries the catalog's completeness beside it, because a miss means two
different things: in a complete catalog the revision is not there, and in a
partial one it is unknown. The content identity is validated rather than
trusted, so a tag or a version arriving in the `digest` field is refused instead
of quietly missing. Absent collections render as `[]` rather than `null`, so
"the catalog holds none of these" reads the same as any other list.

The server instructions say what the surface is not, in the words a host will
actually read: not the fleet, not authorization and not execution. Discovering
a revision does not mean the agent may read, deploy or call it.
`TestCatalogInstructionsSeparateDiscoveryFromTheFleet` holds that text to it.

### CLI mode rules

`pacto mcp` now selects one of four servers, and the rules are explicit:

- The mode is chosen by `--root` being **present**, not by the values
  surviving. pflag round-trips a repeated flag through CSV, so a lone
  `--root ""` arrives as zero roots; `cmd.Flags().Changed("root")` is what
  decides, and an empty root set then fails closed with
  `--root needs a reference: a local bundle path or an oci:// reference`.
  An empty catalog would otherwise be published as an authoritative
  "there is nothing here".
- A bundle reference, `--root` and `--fleet` each select a different server,
  so combining any two is an error naming both, never a silent choice.
- The three pre-existing modes still build unchanged.

### Once-only construction

The catalog is resolved before the server exists, and no handler can resolve,
refresh or rebuild it. Three independent proofs:

- `TestMCPCatalogMode_ResolvesOnceBeforeServing` -- the stub resolver counts
  calls; the count after construction equals the count after every resource
  read and tool call.
- `TestCatalogQueriesResolveNothingAfterTheSessionIsBuilt` -- the same claim at
  the server layer, over a real MCP client session.
- `TestCatalogAnswersAreDeterministicAndCannotMutateTheSession` -- repeated
  reads are byte-identical, and a tool call in between cannot change what the
  closure resource returns.

### Protocol E2E evidence

`tests/integration/mcp_catalog_test.go` builds the real `cmd/pacto` binary, runs
it as a child process over stdio with `CommandTransport` and a private
`XDG_CACHE_HOME`, and speaks MCP to it. It uses two roots at once -- one
`oci://` reference against a live test registry and one local bundle directory
-- over a diamond (`platform` reaches `shared` directly and through `orders`)
plus one dependency that was never published.

It proves, at the protocol level:

- capabilities advertise resources and tools; the instructions point at
  `pacto://catalog`;
- the listed resources are exactly the two URIs, the resource-template list is
  empty and the only catalog tool is `pacto_catalog_revision`; no `pacto_fleet`
  tool appears;
- requested ref, resolved ref and content identity are three different things
  and all three survive the wire: the registry root's `resolvedRef` has its tag
  replaced by an `@sha256:` pin, and the local root has a local content identity
  and no registry ref at all;
- the shared revision is one revision reached from both roots by three routes,
  with all three paths retained, rank `direct`, `minDepth` 1 and the digest the
  tag resolved to;
- the never-published dependency appears once in `unresolved`, with reason
  `NOT_FOUND` and the declaration attributed to `catalog-orders`, and the whole
  answer is `partial` with an `UNRESOLVED_DEPENDENCY` limitation -- partial, not
  empty and not complete;
- the identity lookup returns that revision by its four structured fields;
- **the session is frozen**: after the `shared` tag is moved to different
  content, the local root directory is deleted and the registry is shut down,
  both resources return byte-identical text. A server that resolved per request
  would have nothing left to resolve against;
- the child exits on session close without being signalled, and it wrote into
  the cache directory it was given and nowhere else.

`TestMCPCatalogEmptyRootFailsClosed` runs the shipped binary with `--root ""`
and requires a non-zero exit whose output names the flag.

### Mutation evidence

Six adversarial mutations were injected into the final tree and each was caught.
All six were reverted from a saved pristine copy, and `grep -rn MUTATION` over
the authored trees is clean.

| # | Mutation | Caught by |
|---|---|---|
| M1 | resolve the mutable tag per request instead of once at startup | `TestMCPCatalogMode_ResolvesOnceBeforeServing`; E2E `pacto://catalog changed after the tag moved` and the closure assertion beside it |
| M2 | report a partial catalog as complete | `TestCatalogResourceKeepsAnUnresolvedRootVisibleWithItsReason`, `TestCatalogResourceReportsMetadataBoundsAndEveryRequestedRoot`, `TestMCPCatalogMode_RegistryRootKeepsItsSanitizedReason`, `TestMCPCatalogMode_CancelledStartupStaysPartial` |
| M3 | drop roots that did not resolve from the overview | `TestCatalogResourceKeepsAnUnresolvedRootVisibleWithItsReason` (`requestedRoots = 2` with 1 root), `TestMCPCatalogMode_RegistryRootKeepsItsSanitizedReason` |
| M4 | flatten identity to `domain + "/" + name` and re-split with `strings.Cut` | `TestCatalogRevisionToolCannotConfuseHostileIdentities` |
| M5 | build a second resolver in `internal/cli` instead of using `svc.CatalogResolver()` | `TestMCPCatalogMode_RegistryRootKeepsItsSanitizedReason` (`NOT_FOUND` where `UNAVAILABLE` is correct) and the E2E registry-root assertion |
| M6 | let the tool hand back a slice the closure resource still shares | `TestCatalogAnswersAreDeterministicAndCannotMutateTheSession` |

### Local verification

At `d1d68ad2`, whose Go tree is identical to `533445a5` apart from one doc
comment:

- `make ci` -- exit 0 end to end: `ci-static` (fmt, vet, gocyclo, lint
  `0 issues.`, `check-section`, CLI-docs drift, UI build and drift,
  dashboard-SDK drift, plus the Kubernetes module's own fmt, vet and lint at
  `0 issues.`), `ci-gates` (`tests/architecture` and `tests/release`, both ok,
  which includes the framework-independence boundary test over `pkg/catalog`),
  `ci-engine` (`total coverage: 100.0%` under the race detector, example tests,
  `DEMO-CONTRACTS VALID: 24/24`), `ci-dashboard`, `ci-integration-kubernetes`
  (`total coverage: 100.0%`), `ci-e2e-envtest` and `ci-oci`.
- `make test-integration` -- exit 0, `tests/integration` ok in 53.2s with
  `TestMCPCatalogDiscoveryOverStdio` and `TestMCPCatalogEmptyRootFailsClosed`
  both passing inside the full parallel suite, plus every acceptance package.
- `make artifact-drift` -- `artifact-drift: OK`.
- `make release-dry-run` -- `RELEASE-DRY-RUN OK`, including
  `STANDALONE-VERIFY OK` and `K8S-MODULE-STANDALONE OK`.
- `govulncheck ./...` -- `No vulnerabilities found.`
- `make docs-check` -- 9/9.
- `git diff --check` -- clean.
- `go test -race` over the changed Go packages `./internal/mcp/` and
  `./internal/cli/` -- pass.

Re-run at `533445a5`:

- `make ci-fmt ci-vet ci-cyclo check-section` -- exit 0, `zero U+00A7 in
  authored files`.
- `make ci-lint` -- `0 issues.`
- `make docs-check` -- 9/9.
- `go test -tags integration -run TestMCPCatalog ./tests/integration/` -- ok.
- `git diff --check` -- clean.

The six Kind shards, `ci-e2e-compose` and `dashboard-e2e` were not run locally
in this pass. They are cluster-level legs that Phase 12 does not touch -- it
adds no controller, chart, image or dashboard surface -- and they ran in GitHub
CI and are green at both SHAs, below. No gate was weakened, skipped or
reconfigured to reach any of these results.

An earlier draft of `internal/mcp/catalog_test.go` failed `make ci-cyclo`: four
test functions were over the threshold of 15. They were split into named
assertion helpers rather than having the threshold raised, and every test name
was preserved. Both new integration tests also failed at first inside the full
parallel suite only, because `filepath.Abs("../..")` was evaluated while a
sibling test held a chdir'd working directory; the repository root is now
resolved once at package init and the child process is given its own directory.

### GitHub Actions at implementation SHA `d1d68ad2`

Forty check runs: 37 success, two expected skips and one failure, the inherited
aggregate `CodeQL` check described below. Every workflow ran at attempt 1; no
rerun was requested and none was needed.

CI run `32366422516`, attempt 1, **success**, all 21 jobs green:
`changes` `96416808511`, `ci-static` `96416852436`, `ci-engine` `96416852442`,
`ci-dashboard` `96416852465`, `ci-e2e-envtest` `96416852496`, `operator-build`
`96416852524`, `dashboard-e2e` `96416852530`, `ci-oci` `96416852550`,
`ci-integration-kubernetes` `96416852571`, `artifact-drift` `96416852615`,
`ci-gates` `96416852629`, `ci-e2e-kind (operational-graph)` `96416852664`,
`release-dry-run` `96416852666`, `ci-e2e-compose` `96416852667`,
`release-version-test` `96416852674`, `ci-e2e-kind (observation)` `96416852685`,
`ci-e2e-kind (dashboard)` `96416852712`, `ci-e2e-kind (reconcile)`
`96416852713`, `ci-e2e-kind (upgrade)` `96416852720`, `ci-e2e-kind (evidence)`
`96416852786` and `required` `96419967520`.

| Workflow | Run | Jobs | Result |
|---|---|---|---|
| Pacto Contract CI | `32366421402` | `bundle` `96416806935` | success |
| Security | `32366421452` | `Trivy (image)` `96416807343`, `govulncheck (Go)` `96416807617`, `PR security summary` `96417168452` | success |
| Docs check | `32366421363` | `docs-check` `96416806236` | success |
| Repowise (architecture health) | `32366421325` | `repowise` `96416806196` | success |
| Validate PR title | `32366421356` | `validate` `96416805883` | success |
| PR #291 (dynamic CodeQL) | `32366414800` | `Analyze` for `actions` `96416789144`, `javascript-typescript` `96416789428`, `go` `96416789440`, `python` `96416789559` | success |
| Code Quality: PR #291 (dynamic CodeQL) | `32366415019` | `Analyze` for `python` `96416790394`, `javascript-typescript` `96416790554`, `go` `96416790588` | success |
| Rebuild dashboard UI | `32366421394` | -- | skipped |
| Auto-merge Dependabot PRs | `32366421368` | -- | skipped |

### GitHub Actions at implementation head `533445a5`

The same forty check runs, the same 37 success, two skipped and one inherited
aggregate CodeQL failure, all at attempt 1.

CI run `32368296245`, attempt 1, **success**, all 21 jobs green:
`changes` `96422720365`, `ci-integration-kubernetes` `96422768648`,
`dashboard-e2e` `96422768663`, `ci-static` `96422768669`, `ci-e2e-envtest`
`96422768688`, `ci-gates` `96422768708`, `ci-engine` `96422768732`,
`operator-build` `96422768737`, `release-dry-run` `96422768753`, `ci-dashboard`
`96422768769`, `ci-e2e-compose` `96422768801`, `artifact-drift` `96422768809`,
`ci-e2e-kind (evidence)` `96422768838`, `ci-oci` `96422768845`,
`ci-e2e-kind (observation)` `96422768861`, `release-version-test`
`96422768877`, `ci-e2e-kind (operational-graph)` `96422768916`,
`ci-e2e-kind (upgrade)` `96422768955`, `ci-e2e-kind (dashboard)` `96422768963`,
`ci-e2e-kind (reconcile)` `96422768983` and `required` `96425216179`.

| Workflow | Run | Jobs | Result |
|---|---|---|---|
| Pacto Contract CI | `32368296267` | `bundle` `96422720862` | success |
| Security | `32368296165` | `Trivy (image)` `96422719806`, `govulncheck (Go)` `96422720213`, `PR security summary` `96423129747` | success |
| Docs check | `32368296225` | `docs-check` `96422719965` | success |
| Repowise (architecture health) | `32368296266` | `repowise` `96422720962` | success |
| Validate PR title | `32368296183` | `validate` `96422720193` | success |
| PR #291 (dynamic CodeQL) | `32368293577` | `Analyze` for `go` `96422714626`, `python` `96422714779`, `actions` `96422714851`, `javascript-typescript` `96422714858` | success |
| Code Quality: PR #291 (dynamic CodeQL) | `32368293332` | `Analyze` for `javascript-typescript` `96422713844`, `python` `96422714077`, `go` `96422714154` | success |
| Rebuild dashboard UI | `32368296257` | -- | skipped |
| Auto-merge Dependabot PRs | `32368296231` | -- | skipped |

### CodeQL and review threads

The aggregate `CodeQL` check from `github-advanced-security` is the single
failure at both SHAs, reporting "8 new alerts including 8 high severity security
vulnerabilities". That is the inherited condition carried since section 8, and a
green Security workflow with seven green `Analyze` jobs remains a different
claim from it.

The code-scanning API returns nine open alerts on `refs/pull/291/head`, and they
are exactly the nine inherited ones -- `38`
(`py/incomplete-url-substring-sanitization`, `release/scripts/docs_check.py:197`),
`40` through `43` (`go/path-injection`, `internal/app/resolve.go` lines 35, 43,
57 and 67) and `59` through `62` (`go/path-injection`, `pkg/oci/cache.go` lines
375, 394, 395 and 666). The analyses at both `d1d68ad2` and `533445a5` report
`go` 9, `python` 1, `javascript-typescript` 0 and `actions` 0, the same four
numbers recorded at every earlier head. **The CodeQL delta for Phase 12 is
ZERO**: none added, none removed, none dismissed. None of the nine is in Phase
12's range, and none was worked on.

Review threads were paginated in full, both pages, because the API caps a page
at 100 and page one hides every unresolved one: **199 total, 189 resolved, 10
unresolved**, measured at both SHAs. The ten are unchanged and inherited -- six
`github-code-quality` threads on the generated Mermaid bundle
`pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four
`github-advanced-security` threads on `pkg/oci/cache.go`. **The thread delta is
ZERO.** No comment was published, no thread resolved or replied to and no PR
metadata changed.

### Hygiene and disclosures

- **No changeset was added, deliberately.** `.changeset/` still holds only
  `operational-graph-fleet.md` (`@pacto/core: minor`), which is the changeset
  the whole PR already carries. Phase 11 added none either, no CI gate requires
  one, and `PACTO_PR_TARGET_STATE.md` places finalization and release readiness
  in Phase 14. Phase 12 is a minor, additive surface on the same module, so the
  existing entry covers it; if a reviewer wants a per-phase changeset, that is a
  Phase 14 decision rather than a silent one taken here.
- `docs/cli-reference.md` is generated. It was produced by `make gen-cli-docs`
  from the `cmd/gendocs` change and committed, never hand-edited, and the
  drift check `(b)` in `docs-check` confirms it matches.
- `make ci` again regenerated
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md` from helm-docs
  (+16 / -41), as it has in every prior pass. It was restored with
  `git checkout --` and not committed; the helm-docs drift check itself passes.
  The generator quirk predates Phase 12 and is not fixed here.
- No authored frontend input changed, so the committed UI bundle was not
  rebuilt, and `ci-ui-drift` and the dashboard-SDK drift check are clean against
  the existing one.
- `## Three tool families and their boundaries` in `docs/mcp-integration.md` was
  deliberately **not** renamed even though the catalog makes it a fourth family:
  `docs/operational-graph.md:321` and `docs/impact.md:142` link that anchor and
  `mkdocs build --strict` would fail on the rename. A bridge line under the
  heading points at the new section instead. Renaming it with its inbound links
  is a docs-wide change that belongs to Phase 14.
- The four inherited untracked agent paths `.claude/`, `.codex/`, `.mcp.json`
  and `AGENTS.md` were never touched and are still untracked. `git status
  --short` at the implementation head lists those four and nothing else.
- No literal U+00A7 was authored in any file or commit message, and
  `make check-section` confirms it.
- All three tracked files under `.pr-context/` were read before any change.
  `PACTO_PR_TARGET_STATE.md` was not modified.

### Deliberately not done

- No new configuration file, registry crawler, repository-name inference,
  persistent catalog database, daemon state, background refresh, IDP adapter,
  marketplace, vector search, authorization layer, execution proxy or dynamic
  activation system.
- No second OCI client, credential format or cache. Existing credentials and
  cache policy are reused unchanged.
- No change to `pkg/catalog`, and no change to Phase 11 semantics to make
  projection easier. No conflation of the catalog with `pkg/fleet`, which is
  untouched: catalog mode exposes no fleet tool and fleet mode exposes no
  catalog surface, and `TestAuthoringAndFleetServersExposeNoCatalogSurface`
  holds that line.
- No speculative query language, pagination framework or extension interface.
  No persistent state and no refresh loop.
- No work on the evidence-storage architecture, the nine inherited CodeQL alerts
  or the ten inherited review threads, which stay OPEN and outside this phase's
  scope.
- Phase 13 was not started, and no Phase 13 surface exists.

### Verdict

**Phase 12 is a CANDIDATE at implementation head `533445a5`, and is NOT
self-declared CLOSED.** The discovery surface is the smallest one that answers
the question without lying about identity or completeness, it is built once and
frozen, it is proven at the protocol level against the shipped binary, and six
adversarial mutations were each caught by a named test. Closing Phase 12 is the
reviewer's act, not the author's.

### Current phase map

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED at
  `e639f4b0`, independently reviewed through ledger head `544eeb30`.
- Phase 12: CANDIDATE at `533445a5`, awaiting independent review.
- Phases 13 and 14: NOT STARTED.

The PR remains an open draft, and the append-only, no-history-rewrite and
independent-review protocol continues unchanged.

## 20.1 Independent review at `a22e6d36` -- Phase 12 NARROWLY REOPENED

Independent review covered the append-only range
`6768ddedf0cb2fba1023549af7b4cfbda5b449bd..a22e6d36d639f9d11fb36eb3603c7ee327565f6f`,
with implementation head `533445a55484a662de448b79e37dbb77f2839344`.
The six commits are linear, single-parent descendants of the section 19.4
review head. The merge-base and `origin/main` remain
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`. Local HEAD, the remote branch and
the PR head were exactly `a22e6d36` at review start; the PR was OPEN, DRAFT and
MERGEABLE. Phase 13 has not started.

### Accepted parts

- `--root` is repeatable, distinguishes presence from an empty value, rejects
  empty roots and rejects combinations with a positional capability bundle or
  `--fleet`. Catalog construction delegates to `svc.CatalogResolver()` and
  occurs before the server is returned.
- The accepted `pkg/catalog` core is untouched. Local and OCI identities,
  requested and resolved references, paths, ranks, unresolved dependencies,
  conflicts and cycles cross the protocol as their existing structured types.
- Two fixed resources avoid an identity-bearing URI template, and the one
  revision lookup tool accepts the four identity components separately. The
  hostile-identity and moved-tag cases are substantive and pass.
- The shipped-binary stdio E2E uses both local and real-registry roots, proves a
  diamond/shared revision, exposes a classified unresolved dependency, moves a
  tag, removes the local root and stops the registry before confirming that the
  frozen resources remain byte-identical.
- Focused independent runs pass: race tests for `internal/mcp` and
  `internal/cli`, both `TestMCPCatalog*` integration tests, `make
  check-section`, and `git diff --check`.
- GitHub corroborates the reported green state. CI is 21/21 at implementation
  head `533445a5` in run `32368296245` and at ledger head `a22e6d36` in run
  `32369451579`, both attempt 1. At each SHA the full check population is 37
  success, two expected skips and the inherited aggregate CodeQL failure.
  Code-scanning still returns the same nine alerts, 38, 40 through 43 and 59
  through 62. Fully paginated review threads remain 199 total, 189 resolved and
  10 unresolved. Both deltas are zero.

### Blocker A -- catalog mode is not read-only

`NewCatalogServer` calls `newServer`, and `newServer` unconditionally registers
the four authoring tools. Consequently a real catalog-mode MCP session lists:

```text
pacto_catalog_revision pacto_check pacto_create pacto_edit pacto_schema
```

`pacto_create` and `pacto_edit` write contract files. A server exposing them is
not the read-only discovery server promised by the Phase 12 target, CLI help,
public documentation, commit subjects and section 20. The issue is not merely
wording: a client that selected catalog mode can mutate the filesystem through
the same session.

The permanent server test encodes the bug by requiring all four authoring tools
to remain registered. The shipped-binary E2E only counts tools whose names start
with `pacto_catalog`, so it reports "exactly one" while ignoring the other four.
The reviewer temporarily inverted that assertion to enforce a read-only catalog
surface; `TestCatalogServerExposesTheWholeDiscoverySurfaceAndNothingElse` failed
on all four leaked tools. The mutation was restored and the tracked tree is
clean.

Catalog mode needs a catalog-only server constructor/instruction set and a
permanent exact tool-list assertion. Existing authoring, capability and Fleet
modes remain separate accepted behaviour and must not be redesigned as part of
this repair.

### Blocker B -- the closure resource can present partial unknown knowledge as authoritative empty data

`pacto://catalog/closure` contains only `revisions`, `edges`, `unresolved`,
`conflicts` and `cycles`. It carries neither `Meta` nor any catalog ID,
completeness or limitations. The record justifies this by saying a client must
read `pacto://catalog` first, but MCP resource reads are independent; prose
cannot make that ordering a protocol invariant.

The counterexample is an explicit non-empty root set in which every root fails
to resolve. The overview truthfully says `partial` and retains the unresolved
root, while a direct closure read returns exactly:

```json
{
  "revisions": [],
  "edges": [],
  "unresolved": [],
  "conflicts": [],
  "cycles": []
}
```

That payload is indistinguishable from authoritative empty closure data and
violates the accepted rule that partial is neither empty nor complete. The
reviewer added this all-unresolved probe temporarily; it failed because the
closure JSON had no `meta`, then the probe was removed and the tracked tree was
restored clean.

Keep the accepted two-resource split, but make every closure response
self-describing with the accepted catalog standing. Reusing the complete
`catalog.Meta` is simpler and safer than inventing another reduced status DTO.
Add a permanent all-roots-unresolved protocol test that reads the closure
directly, without first reading the overview, and proves partial completeness
and its limitations travel with the empty collections. Update documentation and
section 20's "share no field" rationale through an appended correction; do not
rewrite history.

### Verdict and phase map

**Phase 12 is NARROWLY REOPENED on Blockers A and B.** Its resolver reuse,
structured identity, frozen-session construction, local/OCI E2E and most of the
protocol projection are accepted. Closure is withheld because the selected
catalog server still exposes filesystem-mutating tools and because one of its
independently readable resources loses the completeness needed to interpret an
empty result.

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED.
- Phase 12: NARROWLY REOPENED on Blockers A and B.
- Phases 13 and 14: NOT STARTED.

## 20.2 Phase 12 repair at `2b8131c3` -- catalog mode made read-only, closure made self-describing

Blockers A and B from section 20.1 are repaired at implementation head
`2b8131c3f6460b5e19c37016ca6ca2d303bc0b50`, a linear single-parent child of the
review head `4c83912e7cb4e51df51427fbc91e44ef92228882`. The append-only
ancestry through `6768dded`, `533445a5`, `a22e6d36` and `4c83912e` is intact.
Nothing was amended, rebased, squashed, reset, rewritten or force-pushed. The
merge-base and `origin/main` remain
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`. The PR is still OPEN, DRAFT and
MERGEABLE. Phase 13 has not started and no Phase 13 surface exists.

The repair is deliberately narrow. Everything section 20.1 accepted is
untouched and was not redesigned: repeatable `--root` with fail-closed empty
values and mutual exclusion against a positional capability bundle and
`--fleet`; construction through `svc.CatalogResolver()` exactly once before the
server is returned; the frozen session; local and OCI roots; mutable-tag
pinning at startup; the two fixed resource URIs; no resource templates; the
four-field structured revision identity; the single `pacto_catalog_revision`
lookup; and the shipped-binary local-plus-OCI stdio E2E. `pkg/catalog` is
byte-identical, and no second query surface was added.

Eight files changed, +192 / -63: `internal/mcp/server.go`,
`internal/mcp/catalog.go`, `internal/mcp/catalog_test.go`,
`tests/integration/mcp_catalog_test.go`, `cmd/gendocs/main.go`,
`docs/cli-reference.md` (generated), `docs/architecture.md` and
`docs/mcp-integration.md`.

### Three claims in the earlier record were wrong

Sections 20 and 20.1 are not rewritten. As in section 19.3, the correction is
made here, in an appended section:

1. **Section 20 recorded that catalog mode exposes exactly one tool. It did
   not.** Every real catalog session also listed `pacto_check`, `pacto_create`,
   `pacto_edit` and `pacto_schema`, because `NewCatalogServer` went through
   `newServer`.
2. **Section 20 recorded the catalog server as read-only. It was not.**
   `pacto_create` and `pacto_edit` write contract files to disk, so a client
   that asked for discovery could modify the filesystem in the same session.
   The claim was true of the intent and false of the code.
3. **Section 20 justified the two-resource split by saying the resources share
   no field, and required a client to read `pacto://catalog` first.** MCP
   resource reads are independent, so that ordering was prose and never a
   protocol invariant. The two resources now deliberately share `catalog.Meta`,
   and reading the overview first is a cost recommendation rather than a
   correctness precondition.

Section 20's own tool-count and read-only statements should be read through
this section from here on.

### Blocker A -- catalog mode is now read-only

**Root cause.** `NewCatalogServer` called `newServer`, and `newServer`
unconditionally calls `registerTools`, which registers the four authoring
tools. There was no way to build a server that shared the one MCP
implementation identity and the one options struct without also inheriting the
authoring surface.

**Repair.** `internal/mcp/server.go` gains `newBareServer(version,
instructions)`: it constructs the `mcpsdk.Server` from the same
`mcpsdk.Implementation` and `mcpsdk.ServerOptions` and registers nothing.
`newServer` is now `newBareServer` followed by `registerTools`, so authoring,
capability and fleet modes are behaviourally unchanged -- same tools, same
registration order, same instructions. `NewCatalogServer` calls `newBareServer`
and then `registerCatalogSurface`. That is the entire mechanism: one extracted
constructor, no new type, no options plumbing, no registry and no duplicated
implementation metadata.

`catalogInstructions` were rewritten to stand alone. They previously extended
the authoring instructions and named authoring tools; a server that does not
register those tools must not tell a client to call them. The new text states
that the server has no contract-authoring tools, that nothing reachable there
creates, edits or writes anything, and keeps the catalog-is-not-the-fleet,
identity and not-authorization boundaries.

**The complete catalog-mode surface, now asserted rather than described:**

```text
tools:              pacto_catalog_revision
resources:          pacto://catalog   pacto://catalog/closure
resource templates: (none)
```

**RED against the starting implementation.** Mutation 1 below restores exactly
the starting implementation -- `NewCatalogServer` calling `newServer` -- and
three permanent tests fail on it, including the shipped binary. The tests that
encoded the bug were corrected, not extended:

- `assertCatalogToolSurface` (`internal/mcp/catalog_test.go:291`) now compares
  the sorted complete tool list against `[pacto_catalog_revision]`. It no
  longer counts names with a `pacto_catalog` prefix, which is what let four
  extra tools pass unnoticed.
- `TestCatalogServerExposesTheWholeDiscoverySurfaceAndNothingElse`
  (`catalog_test.go:299`) no longer asserts that the authoring tools remain
  registered.
- `TestCatalogModeCannotReachTheAuthoringTools` (`catalog_test.go:330`) is new
  and proves unavailability rather than absence from a list: it calls
  `pacto_create`, `pacto_edit`, `pacto_check` and `pacto_schema` over a real
  session and requires each call to fail.
- `TestCatalogInstructionsSeparateDiscoveryFromTheFleet`
  (`catalog_test.go:854`) previously required the instructions to mention
  `pacto_create`. It now requires the opposite: none of the four authoring tool
  names may appear in text served by a server that does not have them.
- `TestMCPCatalogDiscoveryOverStdio` (`tests/integration/mcp_catalog_test.go`)
  asserts the same exact complete list against the shipped binary over stdio.
- `TestAuthoringAndFleetServersExposeNoCatalogSurface` is unchanged and still
  holds the line in the other direction.

### Blocker B -- the closure resource carries its own epistemic standing

**Root cause.** `catalogClosure` held only `revisions`, `edges`, `unresolved`,
`conflicts` and `cycles`. Section 20.1's counterexample is exact: an explicit,
non-empty root set in which every root fails to resolve produced a closure
payload indistinguishable from an authoritative empty answer.

**Repair.** `catalogClosure` gains `Meta catalog.Meta` as its first field,
populated from `cat.Meta()` on every read -- the accepted metadata type, not a
second reduced completeness DTO. The two resources are still two resources with
their own URIs and their own halves of the answer; the overview and its roots
are unchanged; absent collections are still encoded as `[]` by `orEmpty`.

The rationale comment on `registerCatalogSurface` was rewritten. The split
remains by question -- "what is this catalog and how much of it is known"
against "what is in it" -- and the overview remains the cheaper first read. The
repetition of the metadata is now stated as deliberate: a resource can be read
on its own, in any order, so every independently readable payload states its
own epistemic standing instead of borrowing it from a read that may never
happen.

**The direct all-roots-unresolved closure read, captured from a real in-memory
MCP session:**

```json
{
  "meta": {
    "schemaVersion": "pacto.dev/catalog/v1",
    "catalogId": "sha256:ca3ac0512dfc584ae2856a5342a00294856f5a84a4878f39cf39fd396792e7c6",
    "generatedAt": "2026-08-20T09:00:00Z",
    "completeness": "partial",
    "bounds": {
      "maxRoots": 50, "maxRevisions": 500, "maxEdges": 2000, "maxDepth": 10,
      "maxPaths": 20, "maxPathLength": 10, "maxUnresolved": 200,
      "maxConflicts": 200, "maxLimitations": 100
    },
    "requestedRoots": 2,
    "limitations": [
      { "code": "ROOT_UNRESOLVED", "ref": "oci://reg.example/one", "message": "the requested root did not resolve" },
      { "code": "ROOT_UNRESOLVED", "ref": "oci://reg.example/two", "message": "the requested root did not resolve" }
    ]
  },
  "revisions": [], "edges": [], "unresolved": [], "conflicts": [], "cycles": []
}
```

**RED against the starting implementation.** Mutation 2 below removes the field
and the assignment, restoring the starting implementation, and the new test
fails on four separate assertions plus the shipped-binary E2E.

- `TestCatalogClosureCarriesItsOwnCompleteness` (`catalog_test.go:895`) is new.
  It reads `pacto://catalog/closure` **first**, without reading the overview at
  all, on a catalog whose two requested roots both failed to resolve. It proves
  every collection is empty and encoded as `[]` rather than `null`
  (`assertClosureHoldsNothing`, `catalog_test.go:875`), that
  `meta.completeness` is `partial`, that both `ROOT_UNRESOLVED` limitations are
  present with their refs, that `requestedRoots` is 2 and the effective bounds
  survive (`assertSessionMeta`), and finally -- only then -- that the metadata
  is `reflect.DeepEqual` to the overview's. `catalog.Meta` holds a slice, so it
  is not comparable with `==`.
- `TestMCPCatalogDiscoveryOverStdio` now decodes `meta` from the closure of the
  normal partial-dependency E2E and requires it to equal the overview's,
  against the shipped binary.

### Documentation corrected

- `cmd/gendocs/main.go`, "Server modes": `pacto mcp --fleet` is now described
  as "authoring tools plus read-only operational-graph query tools", which is
  its actual accepted behaviour; the previous wording implied fleet mode
  contained only fleet tools. The catalog row states that catalog mode
  registers no authoring tools. **No fleet behaviour changed** -- this is a
  documentation correction only.
- `docs/cli-reference.md` regenerated by `make gen-cli-docs` and committed; the
  only diff is those two rows.
- `docs/architecture.md`, `internal/mcp` mode table: capability and fleet rows
  now read "Authoring tools plus ..."; the catalog row is "this surface only",
  and the paragraph below records that catalog mode is the one mode that does
  not register the authoring tools, and why.
- `docs/mcp-integration.md`: the fourth-server-mode paragraph now states
  catalog mode is the only mode without the authoring tools; the surface table
  notes the closure travels under the same catalog metadata; and the false
  "read `pacto://catalog` first" correctness rule is replaced by an explanation
  that it is the cheaper read, that both resources carry the same metadata,
  that the repetition is deliberate, and what an all-roots-unresolved closure
  would otherwise look like.

### Mutation evidence

Both mutations were run twice: once mid-flight against the uncommitted tree,
and once against the committed tree at `2b8131c3`. Every line number below is
from the second run, so it is the shipped one. Each mutation was reverted
immediately and the tree confirmed identical to `HEAD` (`git diff --stat HEAD`
empty, `git status --short` listing only the four inherited untracked agent
paths).

| # | Mutation (the starting implementation, restored) | Tests that failed |
|---|---|---|
| 1 | `NewCatalogServer` calls `newServer` instead of `newBareServer` | `TestCatalogServerExposesTheWholeDiscoverySurfaceAndNothingElse` -- `catalog_test.go:322: tools = [pacto_catalog_revision pacto_check pacto_create pacto_edit pacto_schema], want exactly [pacto_catalog_revision]`; all four subtests of `TestCatalogModeCannotReachTheAuthoringTools` at `catalog_test.go:345`; shipped-binary `TestMCPCatalogDiscoveryOverStdio` with the identical message at `mcp_catalog_test.go:265` |
| 2 | `Meta` field and `Meta: cat.Meta()` removed from `catalogClosure` | `TestCatalogClosureCarriesItsOwnCompleteness` at `catalog_test.go:908` (`completeness = "", want partial`), `:914` (both `ROOT_UNRESOLVED` limitations), `:919` (schema version, catalog id, generation time, requested roots and effective bounds, via `assertSessionMeta`) and `:924` (the closure metadata disagreeing with the overview's); shipped-binary `TestMCPCatalogDiscoveryOverStdio` at `mcp_catalog_test.go:322` |

Disclosure on method: during the mid-flight run, mutation 2 was reverted with
`git checkout --`, which reset the file to `HEAD` and destroyed the repair,
because the working tree was then ahead of `HEAD`. A `shasum -a 256 -c`
manifest over the eight changed files caught it immediately and the file was
rebuilt and re-verified. A mutation in an uncommitted tree must be reverted by
the inverse edit; `git checkout --` is only safe once the tree matches `HEAD`,
which is why it was used for the second run.

### The new assertions run on the existing required path

No parallel or manually invoked gate was created. `ci` runs `ci-engine`
(`ci.mk`), `ci-engine` runs `ci-test` and `test-integration`, `ci-test` covers
`./internal/mcp/...` under the 100% coverage gate, and `test-integration` runs
`go test -tags integration ./tests/integration/`, which is where
`TestMCPCatalogDiscoveryOverStdio` lives. The `required` job depends on the
whole set.

### Local verification at `2b8131c3`

| Command | Result |
|---|---|
| `go test -race -count=1 ./internal/mcp/... ./internal/cli/...` | ok, both packages |
| `go test -tags integration -run TestMCPCatalog ./tests/integration/` | ok |
| `make ci` | exit 0, `total coverage: 100.0%` |
| `make artifact-drift` | `artifact-drift: OK`, exit 0 |
| `make release-dry-run` | `K8S-MODULE-STANDALONE OK` and `RELEASE-DRY-RUN OK`, exit 0 |
| `make docs-check` | `docs-check: 9/9 checks passed`, exit 0 |
| `make gen-cli-docs` + `git diff --exit-code docs/cli-reference.md` | clean (via `ci-docs`) |
| `govulncheck ./...` | No vulnerabilities found |
| `git diff --check` | clean |
| `make check-section` | zero section-sign characters in authored files |

The review request named `make ase-dry-run`. **No such target exists in this
repository**; the release dry run is `make release-dry-run` (`ci.mk`), and that
is what was run rather than inventing a target to match the name.

Two `make ci` failures were fixed during the pass rather than worked around:
`ci-cyclo` flagged the new test at cyclomatic complexity 16 (threshold 15),
fixed by extracting `assertClosureHoldsNothing` in the file's existing
named-assertion style; and `ci-docs` failed because it runs
`git diff --exit-code docs/cli-reference.md`, so regenerated-but-uncommitted
docs read as drift -- fixed by committing, after which `make ci` is green.

### GitHub Actions at implementation head `2b8131c3`

Forty check runs, 37 success, two expected skips (`build`, `auto-merge`) and
the one inherited aggregate CodeQL failure -- the same population as every
earlier head. Every workflow at attempt 1.

CI run `32384091405`, attempt 1, **success**, all 21 jobs green:
`changes` `96474158538`, `ci-integration-kubernetes` `96474221887`,
`ci-engine` `96474221918`, `ci-e2e-envtest` `96474221970`, `ci-oci`
`96474221989`, `ci-static` `96474222030`, `ci-e2e-compose` `96474222043`,
`dashboard-e2e` `96474222056`, `release-dry-run` `96474222063`,
`artifact-drift` `96474222103`, `operator-build` `96474222104`, `ci-gates`
`96474222112`, `release-version-test` `96474222117`,
`ci-e2e-kind (observation)` `96474222166`, `ci-e2e-kind (reconcile)`
`96474222201`, `ci-e2e-kind (dashboard)` `96474222213`,
`ci-e2e-kind (operational-graph)` `96474222222`, `ci-e2e-kind (upgrade)`
`96474222261`, `ci-dashboard` `96474222265`, `ci-e2e-kind (evidence)`
`96474222288` and `required` `96477950786`.

| Workflow | Run | Jobs | Result |
|---|---|---|---|
| Pacto Contract CI | `32384091443` | `bundle` `96474158375` | success |
| Security | `32384091501` | `Trivy (image)` `96474158390`, `govulncheck (Go)` `96474158785`, `PR security summary` `96474621271` | success |
| Docs check | `32384091409` | `docs-check` `96474158195` | success |
| Repowise (architecture health) | `32384091455` | `repowise` `96474158668` | success |
| Validate PR title | `32384091491` | `validate` `96474158039` | success |
| PR #291 (dynamic CodeQL) | `32384086876` | `Analyze` for `go` `96474150172`, `python` `96474150258`, `javascript-typescript` `96474150342`, `actions` `96474150471` | success |
| Code Quality: PR #291 (dynamic CodeQL) | `32384086872` | `Analyze` for `python` `96474149783`, `javascript-typescript` `96474150121`, `go` `96474150220` | success |
| Rebuild dashboard UI | `32384091480` | -- | skipped |
| Auto-merge Dependabot PRs | `32384091352` | -- | skipped |

All six Kind shards, Compose, `required`, Security, Docs check, Pacto Contract
CI, Repowise and Validate PR title are green. Nothing was called green before
GitHub finished: `release-dry-run` was still `in_progress` on the first poll
and was re-polled to completion.

### CodeQL and review threads

The aggregate `CodeQL` check `96474338475` from `github-advanced-security` is
the single failure, reporting "8 new alerts including 8 high severity security
vulnerabilities" -- the inherited condition carried since section 8, and still
a different claim from the seven green `Analyze` jobs.

Code scanning returns nine open alerts on `refs/pull/291/head`, exactly the
inherited nine: `38` (`py/incomplete-url-substring-sanitization`,
`release/scripts/docs_check.py:197`), `40` through `43` (`go/path-injection`,
`internal/app/resolve.go` lines 35, 43, 57 and 67) and `59` through `62`
(`go/path-injection`, `pkg/oci/cache.go` lines 375, 394, 395 and 666). The
analyses at `2b8131c3` report `go` 9 (`1647975095`), `python` 1
(`1647965980`), `javascript-typescript` 0 (`1647967942`) and `actions` 0
(`1647964348`). **The CodeQL delta for this repair is ZERO**: none added, none
removed, none dismissed, none worked on.

Review threads were paginated in full -- page one returned 100 with
`hasNextPage: true` and page two the remaining 99, and page one hides every
unresolved thread: **199 total, 189 resolved, 10 unresolved**. The ten are the
inherited set: six `github-code-quality` threads on the generated Mermaid
bundle `pkg/dashboard/ui/assets/ganttDiagram-6RSMTGT7-i4uZHW8n.js` and four
`github-advanced-security` threads on `pkg/oci/cache.go` lines 375, 394, 395
and 666. **The thread delta is ZERO.** No comment was published, no thread
resolved or replied to, and no PR comment, review thread or metadata was
changed.

### Hygiene and disclosures

- No changeset was added, for the same reason recorded in section 20:
  `.changeset/` still holds only `operational-graph-fleet.md`, and per-phase
  changesets are a Phase 14 decision.
- `docs/cli-reference.md` is generated, produced by `make gen-cli-docs` and
  committed, never hand-edited.
- `make ci` again regenerated
  `integrations/kubernetes/charts/pacto-dev-gateway/README.md` from helm-docs,
  as in every prior pass. It was restored with `git checkout --` and not
  committed; the drift check itself passes. The generator quirk predates this
  repair and is not fixed here.
- No authored frontend input changed, so the committed UI bundle was not
  rebuilt and the UI drift checks are clean against the existing one.
- `## Three tool families and their boundaries` in `docs/mcp-integration.md`
  remains un-renamed for the reason given in section 20:
  `docs/operational-graph.md:321` and `docs/impact.md:142` link that anchor.
- The four inherited untracked agent paths `.claude/`, `.codex/`, `.mcp.json`
  and `AGENTS.md` were never touched and remain untracked. `git status
  --short` at the implementation head lists those four and nothing else.
- No literal section-sign character was authored in any file or commit message,
  and `make check-section` confirms it.
- All three tracked files under `.pr-context/` were read in full before any
  change. `PACTO_PR_TARGET_STATE.md` was not modified.

### Deliberately not done

- No change to `pkg/catalog` and no change to Phase 11 semantics.
- No change to the accepted two-resource, one-tool design beyond the two
  blockers: the resources were not merged, no resource template was added, no
  tool or query language was added, and no second completeness DTO was
  invented.
- No change to OCI resolution, credentials or caching; no persistent state,
  refresh, crawling, authorization or execution.
- No change to authoring, capability or fleet **behaviour**. Those modes still
  go through `newServer` and register exactly what they registered before; only
  their documentation was corrected.
- No work on the nine inherited CodeQL alerts or the ten inherited review
  threads, which stay OPEN and outside this repair's scope.
- Phase 13 was not started.

### Verdict

**Phase 12 remains a CANDIDATE, now at implementation head `2b8131c3`, and is
NOT self-declared CLOSED.** Catalog mode is a read-only surface whose complete
tool list is asserted by the unit tests and by the shipped binary, and whose
authoring tools are proven unreachable rather than merely unlisted. Each of its
two independently readable resources now states its own epistemic standing, so
an empty closure can no longer pass for an authoritative one. Both blockers are
backed by a mutation that restores the starting implementation and by the named
tests that fail on it. Closing Phase 12 is the reviewer's act, not the
author's.

### Current phase map

- Phases 1 through 11: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED.
- Phase 12: CANDIDATE at `2b8131c3` after the Blocker A and Blocker B repair,
  awaiting independent review.
- Phases 13 and 14: NOT STARTED.

The PR remains an open draft, and the append-only, no-history-rewrite and
independent-review protocol continues unchanged.

## 20.3 Independent review at `ca039861` -- Phase 12 ACCEPTED and CLOSED

This review independently examined the Phase 12 narrow closure repair from
review ledger `4c83912e7cb4e51df51427fbc91e44ef92228882` through candidate ledger
`ca0398612b70bd4eaf5844e00b4fb5030a82e55a`, with implementation head
`2b8131c3f6460b5e19c37016ca6ca2d303bc0b50`.

### History and scope

The candidate is an append-only, two-commit fast-forward over the previous
review ledger: implementation commit `2b8131c3` has `4c83912e` as its sole
parent, and candidate-ledger commit `ca039861` has `2b8131c3` as its sole
parent. The remote branch and PR head were exactly `ca039861` at review time.
The PR remained OPEN, DRAFT and MERGEABLE. `origin/main` and the merge-base
both remained `83f2e66d5cd4fab56099991d39e64fc11f107b3d`.

The implementation changed only the MCP construction/catalog surface, its
tests and generated/public documentation. The candidate-ledger commit changed
only this file. `PACTO_PR_TARGET_STATE.md`, `pkg/catalog`, the CLI mode wiring,
OCI resolution, credentials and caching were untouched.

### Blocker A -- CLOSED: catalog mode is actually read-only

`newBareServer` now creates the protocol server without registering the four
authoring tools. Existing authoring, capability and fleet modes continue to
use `newServer`, which delegates to `newBareServer` and then registers the
unchanged authoring surface. `NewCatalogServer` alone starts from the bare
server and registers exactly the catalog surface.

The complete catalog-mode surface is asserted, not prefix-filtered: one tool
(`pacto_catalog_revision`), two fixed resources (`pacto://catalog` and
`pacto://catalog/closure`) and zero resource templates. A separate test calls
each of `pacto_create`, `pacto_edit`, `pacto_check` and `pacto_schema` through
catalog mode and requires protocol errors. The shipped-binary stdio E2E also
compares the complete tool list.

The reviewer independently restored the forbidden construction by changing
`NewCatalogServer` back to `newServer`. The permanent tests failed: the exact
surface exposed all four authoring tools and all four supposedly unreachable
calls succeeded. The mutation was reverted and the clean implementation
passed again. This proves catalog mode is read-only by reachability, not merely
by documentation or selective listing.

### Blocker B -- CLOSED: closure carries its own epistemic status

`catalogClosure` now contains `Meta catalog.Meta`, and the closure handler
copies `cat.Meta()` into every response. Because resources are independently
readable, an empty closure can now distinguish a complete empty catalog from
an all-unresolved or otherwise partial catalog without first reading the
overview resource.

The focused test reads the closure first for two unresolved roots and proves
that the structural arrays are empty while `Meta` is partial, contains both
`ROOT_UNRESOLVED` limitations, reports two requested roots and retains the
configured bounds. It also requires the closure metadata to equal the overview
metadata. The shipped-binary E2E repeats the cross-resource equality check.

The reviewer independently removed the `Meta: cat.Meta()` assignment. The
permanent test then failed on completeness, limitations, schema version,
catalog identity, generation time, requested-root count, bounds and equality
with the overview. The mutation was reverted and the repaired test passed.
This proves the response cannot silently regress to an authoritative-looking
empty closure.

### Verification and external state

Independent local verification passed:

- `go test -race -count=1 ./internal/mcp/... ./internal/cli/...`;
- the Phase 12 integration tests selected by `TestMCPCatalog` under the
  `integration` build tag;
- `make check-section`; and
- `git diff --check` with a clean tracked tree after both mutations.

GitHub was checked at both `2b8131c3` and `ca039861`. Each SHA has 40 checks:
37 successful, two expected skips and the inherited aggregate CodeQL failure.
CI runs `32384091405` and `32386837387` completed on attempt 1 with all 21 jobs
successful. Contract CI, Security, Docs check, Repowise and PR-title validation
also succeeded at both heads.

Code scanning still contains exactly the nine inherited open alerts: eight
`go/path-injection` alerts in `internal/app/resolve.go` and `pkg/oci/cache.go`,
plus one Python alert in `release/scripts/docs_check.py`. None of those files
is part of the repair, so the CodeQL delta is ZERO. Review threads were fully
paginated: 199 total, 189 resolved and the same ten inherited unresolved
threads (six on the generated Mermaid bundle and four on `pkg/oci/cache.go`).
The thread delta is ZERO. This review published no PR comment, resolved no
thread and changed no PR metadata.

### Verdict and phase map

**Phase 12 is ACCEPTED and CLOSED at implementation head `2b8131c3`, reviewed
through ledger head `ca039861`.** Both previously identified counterexamples
are structurally excluded, are covered at unit and shipped-binary boundaries,
and were independently shown to make their named tests fail when restored.
No Phase 12 blocker remains.

- Phases 1 through 12: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED.
- Phase 13: unblocked and NOT STARTED.
- Phase 14: NOT STARTED.

The PR remains an open draft. Phase 13 may now begin under the same append-only,
candidate-ledger and independent-review protocol.

---

## 21 Phase 13 -- normative invariants, CANDIDATE at `487cd250`

Phase 13 turns the model distinctions Pacto refuses to collapse into durable
executable and documented invariants. It introduces no ontology package, no
validation framework, no new product behavior and no change to any accepted
domain model.

### History and scope

The phase starts at `9157780256d53afaa6cd841f6cc8018b05e7bdc0`, the ledger head
at which Phase 12 was accepted and closed. Three implementation commits form an
append-only fast-forward, each with exactly one parent:

| Order | SHA | Subject |
|-------|-----|---------|
| 1 | `80f1a130c2064ff418b5a03c195b58efb667c3f5` | `test(architecture): forbid the fleet-to-catalog and evidence-to-finding edges` |
| 2 | `560b86260c94fd8f90aa91da806ce3397f59414d` | `test(dashboard): bind the contract-status vocabulary to the fleet's` |
| 3 | `487cd250eeaaebe41ac1c82150acb25330d1cc8a` | `docs: index the distinctions Pacto refuses to collapse` |

The complete diff `91577802..487cd250` is nine files, 353 insertions and two
deletions:

```
 docs/architecture.md                      |   5 +
 docs/concepts.md                          | 196 ++++++++++++++++++++++++++++++
 docs/impact.md                            |   2 +
 docs/index.md                             |   4 +-
 docs/operational-graph.md                 |   2 +
 mkdocs.yml                                |   1 +
 pkg/dashboard/status_vocabulary_test.go   |  77 ++++++++++++
 tests/architecture/boundary_test.go       |  65 ++++++++++
 tests/architecture/collector_docs_test.go |   3 +-
```

No production Go code changed. No frontend code changed, so the browser and
generated-asset paths were not in scope. No Kubernetes, Compose, OCI or release
surface changed. `origin/main` and the merge-base both remain
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`. `PACTO_PR_TARGET_STATE.md` is
untouched. No existing commit was amended, rebased, squashed or force-pushed.
The PR remains OPEN, DRAFT and MERGEABLE with head exactly `487cd250`.

### The inventory: what was already durably protected

Every required invariant family was first traced to its canonical semantic
owner and its existing executable protection. The following families are
already enforced by named adversarial tests, so per the commission they
received no new machinery and are recorded here as evidence instead.

| Distinction | Canonical owner | Existing named protection |
|-------------|-----------------|---------------------------|
| Service, revision and operational target are three identities | `pkg/fleet` keys | `TestCrossDomain_DistinctServices`, `TestCrossDomain_RevisionsAndTargetsIsolated`, `TestRevisionDocument_SameNameDifferentDomainsCannotCrossRead` |
| A requested ref is not a resolved identity | `pkg/fleet/ref.go`, `matchrevision` | `TestClassifyContentIdentity`, `TestMatchRevision_MutableTagUnique_Inferred`, `TestMatchRevision_MutableTagMultiple_Ambiguous`, `TestMatchRevision_DigestRefMismatch_NeverExact` |
| An exact match is not retrievable content | `pkg/fleet` | `TestTargetIdentity_ExactMatch_NonRetrievable`, `TestRevisionDocument_UnavailableIsExplicitNeverEmptyContent`, `TestDocDigest_UnopenablePathHasNoFingerprint` |
| Ambiguous is not unresolved | `pkg/fleet` | `TestMatchRevision_ExactIdentityNoMatchingRevision_Unresolved`, `TestEntityDetail_TargetAmbiguous`, `TestEntityDetail_TargetUnresolved`, `TestExplain_AmbiguousPropagates` |
| Declared ownership, canonical owner identity and contact point are three things | `pkg/fleet` owner key | `TestOwnershipIdentity_SameTeamDifferentDRIIsOneCanonicalOwner`, `TestOwnershipIdentity_ARepeatedContactPointIsNotASecondClaim`, `TestRevisionOwnership_CarriesTheDeclaredContactsWithoutInventingAnIdentity`, `TestOwnerKey_CanonicalDrillDownExcludesTheSubstringCollider` |
| Declared is not observed; declared-not-observed is not confirmed absence | `pkg/fleet` build and reconcile | `TestBuild_ObservedRelationships`, `TestQuery_HasObserved`, `TestReconcileDeclared`, `TestReconcileDeclared_InsufficientWithoutObservation`, `TestProjection_ObservedLimitation` |
| Absence of evidence is not evidence of absence | `pkg/evidence` outcome set, `pkg/finding` categories | `TestEvidenceWindow_NoEvidenceIsNotStaleEvidence`, `TestEvaluate_UnknownCodesAndMessages`, `TestTargetFrom_DefaultComplianceUnknown_MissingEvidence` |
| Readiness is not compliance | `pkg/fleet` inventory | `TestReadinessAndCompliance_APassingRevisionCanRunOnANonCompliantTarget`, `TestReadinessTally_ExpiredIsNotBelowThreshold` |
| Unknown, empty, partial and complete are four claims | `pkg/fleet` build completeness | `TestClassifyCompleteness`, `TestBuild_CollectionLimitations_MarkSourcePartial`, `TestBuild_FailingSource_DisallowPartial`, `TestRuntimePreviewEmpty`, `TestRuntimePreview_ExactTotalWhenComplete` |
| Stale is not unavailable | `pkg/fleet` source state | `TestBuild_SuppliedSourceState_Stale`, `TestSourceStateFor_DerivedAvailable`, `TestObservedStaleAndWindows` |
| A bounded list is not the population; an authoritative zero is not a missing total | `pkg/fleet` product bounds | `TestNeighborhood_Truncation`, `TestProductMeta_SourceCountsSpanThePopulationTheListIsCutFrom`, `TestProductMeta_SourceCountsDoNotAbsorbAnUnknownStatus`, `TestOverview_EvidenceTruncation`, `TestRuntimePreview_QueryWorkIsBounded` |
| Direct source records are not contributed product entities | `pkg/fleet` | `TestSourceDetail_CountsRawRecordsAndContributedEntitiesSeparately`, `TestSourceDetail_ContributionSurvivesThePreviewBound` |
| An unresolved root is partial knowledge, not an empty closure | `pkg/catalog` | `TestUnresolvedRootAndDependencyArePartialNotEmpty`, `TestCancellationIsPartialKnowledge`, `TestRevisionLookupMissIsNotAnEmptyRevision` |
| Domain-qualified revision identity survives mirrored content | `pkg/catalog` | `TestMirroredContentInTwoDomainsStaysTwoServices`, `TestCompareServiceIDOrdersDomainThenName` |
| Discovery is not authorization and is not execution | `internal/mcp` | `TestCatalogServerExposesTheWholeDiscoverySurfaceAndNothingElse`, `TestCatalogModeCannotReachTheAuthoringTools`, `TestAuthoringAndFleetServersExposeNoCatalogSurface`, `TestSafety_ReadOnly`, `TestRegisterCapabilities_ReadOnlyListAndInvoke` |
| Presentation may simplify presentation, never meaning | `pkg/dashboard/frontend/src/lib/knowledgeState.ts` | the `snapshotKnowledge` suite in `knowledgeState.test.ts`, which already covers strictest-signal precedence, missing metadata and the unavailable-over-stale-over-partial order |
| Core stays framework-independent; evidence consumers never reach the registry store | `tests/architecture` | `TestCorePackagesHaveNoKubernetesOrIntegrationDeps`, `TestCatalogCoreIsFrameworkIndependent`, `TestEvidenceConsumersNeverReachTheRegistryStore` |

### The four unprotected seams, and why each guardrail sits where it does

**Seam 1 -- the fleet could import the catalog.**
`TestCatalogCoreIsFrameworkIndependent` guards one direction with an allow-list
of `pkg/catalog` plus `pkg/contract`. The opposite direction was unguarded, so
`pkg/fleet` could have read a frozen declaration-only discovery session and let
a complete catalog closure be presented as a complete fleet snapshot. This is a
genuine cross-package boundary with no in-package owner, which is exactly the
case the architecture level exists for.
Permanent test: `TestTheFleetNeverReachesTheCatalog` in
`tests/architecture/boundary_test.go`.

**Seam 2 -- evidence and finding were kept apart only by convention.**
`pkg/evidence` documents that there is no `OutcomeAbsent`, and `pkg/finding`
separates `CategoryMissingEvidence` from `CategoryInconclusive`, but nothing
prevented either package from importing the other and letting a finding carry
an observation payload or a collector emit a verdict. `validation.Evaluate` is
meant to be the single bridge. Again a cross-package rule with no single
in-package owner, so it belongs at the architecture level.
Permanent test: `TestEvidenceAndFindingNeverImportEachOther` in
`tests/architecture/boundary_test.go`.

**Seam 3 -- the contract-status vocabulary had no Go-level parity check.**
The seven statuses are declared three times: as typed constants in
`pkg/dashboard`, as bare strings in `pkg/fleet` (declared there so the fleet
layer does not import the dashboard) and as a kubebuilder `Enum` marker in the
operator CRD. Only the CRD copy was enforced. A drift between the first two is
silent, because `NormalizeContractStatus` folds any unrecognized value into
`Unknown`, and `Unknown` is a member of every emitted enum domain, so the
runtime OpenAPI conformance test in `enum_conformance_test.go` cannot see it.
The pre-existing `TestNormalizeContractStatus` cannot stand in either: its table
covers five of the seven statuses (`Invalid` and `NotEvaluated` are absent) and
it compares each constant against itself rather than against the fleet's value.
The guardrail is a focused domain test in the package that owns the typed
vocabulary, asserting value equality in both directions, round-tripping every
canonical fleet status through `NormalizeContractStatus` and requiring one
meaning per value.
Permanent test: `TestContractStatusVocabularyMatchesFleet` in
`pkg/dashboard/status_vocabulary_test.go`.

**Seam 4 -- the new concepts page sat outside the documentation gate.**
`TestDocsDoNotConflateOrOverclaimCollectors` scans a fixed list of public pages
for two errors: calling the Kubernetes collector "the engine" and presenting an
unimplemented collector as shipped. `docs/concepts.md` states the data-source
versus collector boundary, so it must be inside that scan or the page becomes
the one place the rule is unenforced. The fix is one entry in the existing list,
not a new mechanism.
Permanent test: `TestDocsDoNotConflateOrOverclaimCollectors` in
`tests/architecture/collector_docs_test.go`, now covering `docs/concepts.md`.

### Documentation

`docs/concepts.md` is a new public page that indexes the distinctions in four
sections: Identity, Knowledge, Declaration-observation-judgement and
Boundaries. It states each distinction in one bolded sentence, names what breaks
if the two sides are conflated and links to the page that explains the
mechanics. It duplicates no mechanics of its own. It uses user-facing language
and current terminology rather than internal package names, and it carries the
adversarial examples the commission asked for: the same name in two domains,
the same content mirrored into two domains, a mutable tag against a resolved
digest, an exact match whose content is not retrievable, a partial result with
zero rows and unknown against not-evaluated.

The only genuinely new explanatory content is the exact-match-versus-
retrievability passage, which sets match certainty (`exact`, `inferred`,
`ambiguous`, `unresolved`) against content retrievability as two independent
facts about one target, and states that both can hold at once without
contradiction.

The page is added to the mkdocs navigation between Manifesto and Get started,
and is cross-linked from `docs/index.md`, `docs/architecture.md`,
`docs/operational-graph.md` and `docs/impact.md`. No existing anchor was
renamed or removed. Every outbound link and anchor was proved by
`mkdocs build --strict`, under which the configured `nav` and link validation
warnings are errors.

### Deliberately not added, after investigation

- **A `severityRank` distinct-ranks test.** The rank integers in `pkg/fleet` are
  consumed only by `ValidStatus`, so asserting their values would be an
  implementation-detail assertion of dead data rather than a semantic invariant.
- **An operator-side vocabulary parity test.** Importing `pkg/dashboard` from
  `integrations/kubernetes/api/v1alpha1` would put the standalone-module proof
  at risk for no semantic gain: the CRD's `+kubebuilder:validation:Enum=` marker
  already pins that third copy at the API server.
- **Named types for the `Provenance`, `Reconciliation` and `Difference` string
  fields.** Converting them is a wire-model redesign spanning fleet, dashboard,
  MCP, the OpenAPI document and the frontend client, and no invariant violation
  was demonstrated. The commission forbids redesigning accepted domain models
  without one.
- **Any repository-wide text search standing in for a typed or behavioral test.**
  The three Go guardrails are import-graph and constant assertions. The one
  text-based gate is the pre-existing documentation scan, extended by a single
  filename.

### Mutation evidence

Each permanent test was proved to bite by restoring the prohibited semantics,
never by introducing a syntax error or a forced panic. Every mutation was
reverted and the revert confirmed byte-identical with `shasum -a 256 -c`.

**A -- collapse the fleet into the catalog.** `pkg/fleet/fleet.go` was made to
import `pkg/catalog` and define
`CompletenessComplete = Completeness(catalog.CompletenessComplete)`. The code
compiled. `TestTheFleetNeverReachesTheCatalog` failed with its named boundary
message. `pkg/fleet`, `pkg/dashboard` and the whole pre-existing architecture
suite stayed green. The only incidental prior signal was an "import cycle not
allowed in test" build failure in `pkg/catalog`, caused by `catalog_test.go`
importing `pkg/fleet`; that signal is accidental, blind to direction and
carries no explanation of the rule.

**B -- let a finding carry an observation.** `pkg/finding/finding.go` was made
to import `pkg/evidence` and `EvidenceRef` gained
`Observation *evidence.Observation`. The code compiled and **every pre-existing
test in the repository stayed green**. Only
`TestEvidenceAndFindingNeverImportEachOther` failed.

**C -- fold a canonical status into Unknown.** The `StatusNotEvaluated` case was
removed from the `NormalizeContractStatus` switch in `pkg/dashboard/model.go`.
The code compiled. Exactly one test in all of `pkg/dashboard` failed:
`status_vocabulary_test.go:61: NormalizeContractStatus("NotEvaluated") =
"Unknown", want "NotEvaluated"`. `tests/architecture`, `tests/release`,
`pkg/fleet/...` and `internal/...` all stayed green, confirming this seam had no
other cover.

**D -- overclaim a collector on the new page.** The sentence "The Terraform
collector is shipped today." was planted in `docs/concepts.md`. With the
scan-list entry stashed the gate passed, which is the pre-Phase-13 state. With
the entry restored it failed: "docs/concepts.md presents an unimplemented
collector (ECS/Nomad/Terraform) as shipped/supported". This proves the fourth
seam was real and is now closed.

### Local verification

All green on the implementation head:

- `make ci`, exit 0: `ci-static`, `ci-gates`, `ci-engine` (race detector, with
  `pkg/dashboard` at 100.0% coverage), `ci-dashboard`,
  `ci-integration-kubernetes`, `ci-e2e-envtest` and `ci-oci`;
- `-race` reruns of `tests/architecture/...` and `pkg/dashboard/...`;
- `make artifact-drift`, OK;
- `make release-dry-run`, `RELEASE-DRY-RUN OK` plus `K8S-MODULE-STANDALONE OK`;
- `make docs-check`, 9 of 9 checks including generated-docs no-drift,
  `mkdocs build --strict`, 18 of 18 fenced contracts, 19 of 19 mermaid blocks
  and the deterministic-twice rebuild;
- `make check-section`, zero occurrences of the forbidden character;
- `govulncheck ./...`, no vulnerabilities found;
- `git diff 91577802..HEAD --check`, clean.

No gate was weakened, skipped or removed.

### Required-CI wiring

The expanded Make targets and the workflow path filters were read, not assumed.
`ci-gates` runs `go test ./tests/architecture/... ./tests/release/...` and its
job carries no `if:`, so the two new architecture tests and the extended
documentation gate always execute. `ci-engine` is selected by the `engine`
filter `'!(integrations|docs|overrides)/**'`, which this change set satisfies,
so `TestContractStatusVocabularyMatchesFleet` executes there. The `docs-check`
workflow filter already includes `docs/**` and `mkdocs.yml`. Every new permanent
test therefore runs inside required CI, not merely locally.

The `release` filter (`release/**`, `.github/workflows/**`, `examples/demo/**`)
is not triggered by this change set, so `artifact-drift` and `release-dry-run`
could legitimately have been skipped in CI. Both were run locally, and in the
event both also ran and passed on GitHub.

### GitHub evidence at `487cd250`

The remote branch head and the PR head are both exactly
`487cd250eeaaebe41ac1c82150acb25330d1cc8a`. Ten workflow runs exist at that SHA:

| Run | Workflow | Result |
|-----|----------|--------|
| `32413092137` | CI | success on attempt 2 |
| `32413091831` | Docs check | success |
| `32413091947` | Security | success |
| `32413092110` | Pacto Contract CI | success |
| `32413091869` | Repowise (architecture health) | success |
| `32413091729` | Validate PR title | success |
| `32413088196`, `32413088665` | CodeQL | success |
| `32413091756` | Rebuild dashboard UI | skipped (expected, no frontend change) |
| `32413092009` | Auto-merge Dependabot PRs | skipped (expected) |

CI run `32413092137` attempt 1 failed in one job only, `ci-e2e-kind (reconcile)`
(job `96567912937`). The cause is infrastructure, and the log names it exactly:

```
go: github.com/gofiber/fiber/v3@v3.4.0: verifying go.mod:
  reading https://sum.golang.org/tile/8/0/x221/291:
  stream error: stream ID 319; INTERNAL_ERROR; received from peer
```

That is a transient HTTP/2 failure from the Go checksum database during
`go mod download all` inside the operator image build, before the Kind cluster
existed -- which is why every subsequent diagnostic in that log reports a
refused connection to the API server. Phase 13 changed no module file, no
operator code and no Dockerfile, and the other five Kind shards, which perform
the same image build, all passed on attempt 1. The failed job was re-run.
Attempt 2 completed with all 21 jobs successful: `changes`, `ci-static`,
`ci-gates`, `ci-engine`, `ci-dashboard`, `ci-integration-kubernetes`,
`ci-e2e-envtest`, `ci-oci`, `ci-e2e-compose`, `operator-build`, `dashboard-e2e`,
`artifact-drift`, `release-dry-run`, `release-version-test`, the six
`ci-e2e-kind` shards and the `required` aggregate.

Across the whole PR head the check totals are 34 success, two expected skips and
one failure. That single failure is the aggregate `CodeQL` check, which is the
inherited red recorded in every prior phase ledger: both CodeQL analysis runs
themselves succeeded, and the aggregate reports failure because open alerts
exist on the ref.

### CodeQL and review-thread deltas

Code scanning was queried on `refs/pull/291/head` after the analysis re-ran at
the pushed SHA. It returns exactly the nine inherited open alerts and nothing
else: `#38` (`py/incomplete-url-substring-sanitization`,
`release/scripts/docs_check.py:197`), `#40` through `#43` (`go/path-injection`,
`internal/app/resolve.go`) and `#59` through `#62` (`go/path-injection`,
`pkg/oci/cache.go`). This is identical to the starting-SHA baseline, so the
**CodeQL delta is ZERO**: none added and none removed. None of those files is
part of Phase 13. Alerts `#65` through `#81` remain on `refs/heads/main` rather
than on this PR ref and are likewise inherited.

Review threads were fully paginated rather than read from a single page: 199
total, 189 resolved and 10 unresolved. This is identical to the starting-SHA
baseline, so the **thread delta is ZERO**. All ten unresolved threads are
inherited bot findings on code Phase 13 does not touch: six on the generated
Mermaid vendor chunk `pkg/dashboard/ui/assets/ganttDiagram-*.js` and four
`go/path-injection` notices on `pkg/oci/cache.go`. This phase published no PR
comment, replied to and resolved no thread and changed no PR metadata.

### Repository hygiene

No generated file was hand-edited and no generated output drifted:
`make artifact-drift` and the docs no-drift check both pass, and the committed
dashboard UI bundle was not rebuilt because no frontend source changed. Two
known local-only rewrites were restored and deliberately not committed: the
helm-docs rewrite of
`integrations/kubernetes/charts/pacto-dev-gateway/README.md` and `go.work.sum`
churn. The working tree is otherwise clean apart from the inherited untracked
agent files `.claude/`, `.codex/`, `.mcp.json` and `AGENTS.md`, which were not
touched. No temporary checklist or audit scratchpad was committed; the inventory
above is the only record of that work.

### Explicit exclusions

Nothing in the following list was attempted, in line with the commission: no
second ontology package or generic validation engine, no new MCP tool, resource,
template or query language, no authorization, execution, registry crawling,
refresh, persistence, activation or marketplace behavior, no change to Phase 11
catalog resolution semantics or Phase 12 discovery semantics, no backend
semantic reasoning moved into the browser, no work on inherited CodeQL alerts or
inherited review threads, no edit to `PACTO_PR_TARGET_STATE.md` and no change to
PR metadata or state.

### Deferred to Phase 14

The repository-wide ontology audit, the PR-body rewrite, review-thread cleanup,
the ready-for-review transition and the final changeset decision all remain
Phase 14 work and were not begun.

### Status

**Phase 13 is a CANDIDATE at implementation head `487cd250`, awaiting
independent review. It is not self-declared closed.**

- Phases 1 through 12: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED.
- Phase 13: CANDIDATE, awaiting independent review.
- Phase 14: NOT STARTED.

The PR remains an open draft.

## 21.1 Independent review at `8c83b387` -- Phase 13 NARROWLY REOPENED

Independent review covered the complete Phase 13 interval from accepted Phase
12 ledger `9157780256d53afaa6cd841f6cc8018b05e7bdc0` through candidate ledger
`8c83b387f31357c4fe4b31b143eb66656060fa5f`, with implementation head
`487cd250eeaaebe41ac1c82150acb25330d1cc8a`.

### History, scope and accepted work

The history is an append-only four-commit fast-forward. Every commit has exactly
one parent, the first implementation commit has `91577802` as its parent, and
the candidate ledger has `487cd250` as its parent. At review time local HEAD,
the remote branch and PR head were exactly `8c83b387`. The PR remained OPEN,
DRAFT and MERGEABLE. `origin/main` and the merge-base both remained
`83f2e66d5cd4fab56099991d39e64fc11f107b3d`.

The implementation diff is correctly narrow: nine files, 353 insertions and
two deletions, with no production Go, frontend, Kubernetes, Compose, OCI or
release change. The ledger commit changes only this file. The target is
untouched.

The two import-graph gates are accepted. They reuse the existing `go list
-deps` architecture mechanism and close real missing directions without adding
a validation framework: fleet cannot reach catalog knowledge, and evidence and
finding cannot reach each other around `validation.Evaluate`. The existing
collector-overclaim gate is also correctly extended by one filename rather than
duplicated. Focused architecture tests pass under the race detector.

The Concepts page is a useful durable index, is correctly integrated into the
MkDocs navigation and existing public pages, and passes the strict documentation
build. Most of its distinctions agree with their existing semantic owners.
Those accepted parts do not need redesign. Two narrow blockers remain.

### Blocker A -- the normative page makes two claims stronger than the model

`docs/concepts.md` lines 26 through 29 state globally that Pacto resolves a
mutable requested reference to an immutable identity "once". That is true for a
frozen catalog session, but not for Pacto as a whole: the Kubernetes operator's
`ResolutionPolicyLatest` explicitly resolves the highest semver tag on every
reconciliation. The durable invariant is that a mutable requested ref must be
resolved to an immutable identity before it is treated as exact content, not
that every Pacto surface resolves it only once forever.

More importantly, lines 93 through 95 state that every bounded list carries the
true total. The accepted product model deliberately disproves that absolute.
`RuntimePreview.Total` and `RelationshipsPreview.Total` are pointers and are
omitted when bounded work stopped before the true total could be known. In that
case `Truncated` is true and `Scanned` or `Count` is only a lower bound. The
permanent tests `TestRuntimePreviewBounded` and
`TestRuntimePreview_QueryWorkIsBounded` explicitly require `Total == nil` for
that case; both passed during this review. The next paragraph on the same page
correctly says an authoritative zero is different from a missing total, so the
absolute sentence also contradicts the page's own invariant.

This is blocking because Phase 13's deliverable is the durable normative
documentation. A Concepts page that upgrades unknown cardinality to an exact
total, or a per-session resolution rule to a global one-time rule, teaches the
semantic collapse the phase exists to prevent. The repair should qualify both
statements to the real boundary: immutable before exact use, and exact total
when knowable; otherwise an explicitly absent total plus truncation/lower-bound
metadata.

### Blocker B -- the vocabulary test is pair equality, not set equality

`TestContractStatusVocabularyMatchesFleet` lines 39 through 76 manually lists
the seven currently known pairs. It proves that those seven values agree and
that each survives normalization. It does not prove the stronger claims in its
name, comments and ledger that neither vocabulary can gain a status the other
does not know.

The reviewer independently added a new dashboard constant
`StatusDeferred = "Deferred"` and accepted it in `NormalizeContractStatus`,
without adding it to `pkg/fleet`. Both
`TestContractStatusVocabularyMatchesFleet` and the pre-existing
`TestNormalizeContractStatus` passed. The mutation compiled and represented a
canonical dashboard status that the fleet rejected, exactly the one-sided gain
the new test says it prevents. The mutation was then fully reverted and the
tracked tree returned clean.

This is not a request for a shared ontology framework. The narrow repair is to
make the finite canonical sets discoverable from the same structures production
normalization/validation use, and compare those complete sets, or use an equally
small structural design in which one-sided addition cannot pass. The permanent
mutation must add an eighth canonical status to either side and prove that the
parity guard fails until the other side is aligned. Existing-value spelling
drift and `Unknown` versus `NotEvaluated` must remain covered.

### Independent verification and GitHub state

Independent local checks passed on the unmodified candidate after the mutation
was reverted:

- `go test -race -count=1 ./tests/architecture/...`;
- `go test -race -count=1 ./pkg/dashboard/...`;
- focused fleet tests for known versus unknown preview totals;
- `make docs-check`, all nine checks including `mkdocs build --strict`;
- `make check-section`; and
- `git diff --check` with no tracked change.

GitHub evidence matches the candidate except for one ledger arithmetic error.
At both `487cd250` and `8c83b387` there are 40 check runs: **37 success, two
expected skips and one inherited aggregate CodeQL failure**, not 34 successes as
section 21 states for the implementation head. CI run `32413092137` is green on
attempt 2 with all 21 jobs after the recorded checksum-database transient; CI
run `32415864315` is green on attempt 1 with all 21 jobs at the ledger head.
Docs check, Security, Contract CI, Repowise, PR-title validation and both dynamic
CodeQL workflows are successful at both SHAs.

Code scanning still reports exactly the nine inherited open alerts: one in
`release/scripts/docs_check.py`, four in `internal/app/resolve.go` and four in
`pkg/oci/cache.go`. The CodeQL delta is ZERO. Review threads were independently
paginated in full: 199 total, 189 resolved and the same ten inherited unresolved
bot threads (six on the generated Mermaid bundle and four on
`pkg/oci/cache.go`). The thread delta is ZERO. This review published no PR
comment, resolved no thread and changed no PR metadata.

### Verdict and phase map

**Phase 13 is NARROWLY REOPENED at candidate ledger `8c83b387`.** The import
boundaries, documentation structure and existing seven status correspondences
are accepted. Closure requires only the truthful qualification of the two
normative documentation claims and an actually exhaustive fleet/dashboard
status-vocabulary invariant with a one-sided-addition mutation.

- Phases 1 through 12: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED.
- Phase 13: NARROWLY REOPENED on Blockers A and B above.
- Phase 14: NOT STARTED and remains blocked.

The PR remains an open draft. Repair only these two blockers, record the repair
as a candidate and stop for independent review.

## 21.2 Phase 13 narrow closure repair at `3f477dc0` -- CANDIDATE

The two blockers recorded in section 21.1 are repaired. Nothing else in Phase 13
was redesigned: the two import-graph gates, the extended collector-overclaim
gate, the Concepts page structure and navigation, the seven existing
fleet/dashboard status correspondences and the decision not to build an ontology
package all stand as accepted.

### History and scope

Append-only two-commit fast-forward from the independent-review ledger.

- `48ea47387100bad3b260d883f17fc01d08192ee7` -- independent review, the starting
  SHA and the parent of the first repair commit.
- `e218ce90ffd27c37355424664cd3c29676e89164` -- Blocker B.
- `3f477dc0aa96088da332e6092e40cfcb30598bda` -- Blocker A, and the head this
  record was written against.

Every commit has exactly one parent, `48ea4738` is an ancestor of HEAD, and no
commit was amended, rebased, squashed or force-pushed. `origin/main` and the
merge-base both remain `83f2e66d5cd4fab56099991d39e64fc11f107b3d`. The whole
repair is six files, 122 insertions and 53 deletions: two production Go files,
three Go test files and one documentation page. No frontend, Kubernetes,
Compose, OCI, release, browser or acceptance machinery changed, and the target
document is untouched.

### Blocker B -- the vocabulary comparison is now set equality

The counterexample was independently reproduced before anything was changed.
Adding `StatusDeferred ContractStatus = "Deferred"` to `pkg/dashboard/model.go`
and accepting it in `NormalizeContractStatus`, without telling `pkg/fleet`, left
both `TestContractStatusVocabularyMatchesFleet` and `TestNormalizeContractStatus`
passing. The reported defect is real: the test compared seven remembered pairs,
which says nothing about a value that exists on one side only.

The repair makes each side's finite canonical set readable from the structure
production already uses, so there is nothing to compare against except the real
domain and no third list to drift.

- `pkg/dashboard/model.go` -- the normalization switch is replaced by
  `canonicalStatuses`, a package-level list of the seven `ContractStatus`
  constants. `NormalizeContractStatus` returns its argument when
  `slices.Contains(canonicalStatuses, s)` and folds everything else into
  `StatusUnknown`, exactly as before. Because the normalizer reads the list, the
  dashboard vocabulary cannot grow anywhere the parity test does not see.
- `pkg/fleet/fleet.go` -- `CanonicalStatuses()` returns
  `slices.Sorted(maps.Keys(severityRank))`. That is the same table `ValidStatus`
  accepts from, so the enumeration cannot be a subset of what the fleet
  validates, and a status added without a severity is not canonical at all.
- `pkg/dashboard/status_vocabulary_test.go` -- the test now compares the two
  complete sets in both directions, asserts the dashboard set contains no
  duplicate value, asserts `fleet.ValidStatus` (the predicate production
  filtering calls, not only the enumeration) accepts every dashboard status, and
  asserts every canonical fleet status normalizes to ITSELF rather than merely
  being recognized.
- `pkg/fleet/keys_test.go` -- `TestValidStatus` hoists the canonical list it
  already contained and additionally asserts `CanonicalStatuses()` equals it
  sorted. This is what covers the new accessor inside its own package; `ci-test`
  runs without `-coverpkg`, so a cross-package caller would have left it at zero
  under the 100.0 percent gate. No new list was introduced.
- `pkg/fleet/aggregate_test.go` -- the tally test's hand-maintained seven-status
  population is deleted and read from `CanonicalStatuses()` instead. This
  removes precisely the kind of test-only list the review forbade.

Wire values, `ContractStatus` constants, folding behaviour for genuinely foreign
input and every other public shape are unchanged. `Unknown` and `NotEvaluated`
remain two values with two meanings, and the duplicate-value assertion now makes
collapsing them a failure rather than a silent narrowing.

### Mutation evidence

Four mutations were applied one at a time and each was fully reverted. Byte
identity of `pkg/dashboard/model.go` and `pkg/fleet/fleet.go` was confirmed with
a SHA-256 manifest after every revert, and the working tree returned to the
committed state.

1. **Canonical status added to the dashboard only.** `StatusDeferred` added as a
   constant and to `canonicalStatuses`; `pkg/fleet` untouched. This is the
   review's exact counterexample.
   `TestContractStatusVocabularyMatchesFleet` FAILS on direction one: `the
   dashboard declares "Deferred" canonical, but the fleet vocabulary is
   [Compliant Invalid NonCompliant NotEvaluated Reference Unknown Warning]`, and
   `the fleet does not accept the dashboard status "Deferred" as canonical`.
2. **Canonical status added to the fleet only.** `StatusDeferred` added to the
   fleet constants and to `severityRank`; the dashboard untouched.
   `TestContractStatusVocabularyMatchesFleet` FAILS on direction two: `the fleet
   declares "Deferred" canonical, but the dashboard vocabulary does not know it`,
   and `NormalizeContractStatus("Deferred") = "Unknown", want "Deferred"`. Under
   the same mutation the fleet's own guards also fail, `TestValidStatus` on the
   enumeration and `TestComplianceTally_EveryCanonicalStatusHasItsOwnBucket` on
   the population.
3. **Existing value respelled on one side.** `StatusNotEvaluated` changed to
   `"Not-Evaluated"` in the dashboard only. FAILS in both directions at once.
4. **`StatusNotEvaluated` dropped from normalization.** Removed from
   `canonicalStatuses`, so the constant still exists but the normalizer folds the
   status into `Unknown`. FAILS on direction two and on the round-trip
   assertion. This is the collapse section 21 already claimed to catch, and it
   still bites after the rewrite.

### Blocker A -- the normative page is qualified to the real boundary

`docs/concepts.md` only. No behavioural change, and no prose-matching test was
added: the semantics are already owned by the bounds tests in `pkg/fleet`, which
remain the enforcement.

**Requested versus resolved.** The absolute "Pacto resolves a requested
reference to an immutable identity once" is gone. The invariant is restated as
the part that is actually invariant -- a requested reference must be resolved to
an immutable identity before anything treats it as exact content, and a mutable
reference on its own is never a revision identity. A new adjacent distinction,
"A resolution has a lifetime, and the lifetime belongs to the boundary that made
it", states that resolving is not one global event: a catalog discovery session
resolves every root as it is built and then freezes the result, so a tag that
moves in a registry does not move that catalog, while the operator's `Latest`
resolution policy resolves the highest semver tag on every reconciliation and
`PinnedTag`/`PinnedDigest` do not. Both obey the same rule at different
lifetimes; a resolution is exact for the snapshot, session or reconciliation
that produced it and nothing carries it past that boundary. No single global
resolution lifetime is invented, and the existing cross-link is kept.

**Bounded results and unknown totals.** "Every bounded list carries the true
total" is gone. "A bounded list is not the population" now says only what is
always true -- the rows are one slice, `truncated` says whether more exist, and a
count from visible rows is a floor -- and keeps its existing cross-link. A new
distinction, "An unknown total is not a total of zero", says the total is
carried whenever it is knowable and omitted rather than estimated when the
counting work itself was bounded, followed by a four-row table separating: a
`total` equal to the row count; a `total` above the row count with `truncated`;
an authoritative `total` of zero; and no `total` with `truncated` and a count
that is only a lower bound. The page's existing "An authoritative zero is not a
missing total" distinction is kept and strengthened with the concrete failure:
rendering an absent total as `0` turns "we stopped counting" into "we counted,
and there are none".

### Ledger arithmetic correction

Section 21 states 34 successful check runs at the implementation head. That is
wrong and section 21.1 corrected it. The independently verified population at
`487cd250`, at `8c83b387` and again at this repair head `3f477dc0` is **40 check
runs: 37 success, two expected skips and one inherited aggregate CodeQL
failure**. Sections 21 and 21.1 are left exactly as written; this paragraph is
the correction.

### Local verification

All run at `3f477dc0` unless noted.

- Focused status-vocabulary tests under the race detector, `pkg/dashboard` and
  `pkg/fleet`: pass.
- `go test -race -count=1 ./pkg/dashboard/... ./pkg/fleet/...`: pass.
- `go test -race -count=1 ./tests/architecture/...`: pass.
- Focused fleet preview-total tests under the race detector:
  `TestRuntimePreviewBounded`, `TestRuntimePreview_ExactTotalWhenComplete`,
  `TestRuntimePreview_QueryWorkIsBounded`, `TestPreviews_BoundAboveEveryMaximum`,
  `TestServiceDetailRelationshipsTruncationHonest`,
  `TestServiceDetailRelationshipsKnownTotal` and the full `TestNeighborhood_*`
  family: pass.
- `make docs-check`: 9 of 9, including `mkdocs build --strict`, generated-docs
  drift, 19 of 19 Mermaid blocks and the determinism re-run.
- `make ci`: pass, exit 0. The `ci-test` coverage gate reports total 100.0
  percent, with `NormalizeContractStatus`, `ValidStatus` and the new
  `CanonicalStatuses` each at 100.0 percent.
- `make artifact-drift`: OK. `make release-dry-run`: OK, including
  K8S-MODULE-STANDALONE.
- `govulncheck ./...`: no vulnerabilities found.
- `make check-section`: zero U+00A7 in authored files.
- `git diff --check`: clean.

`make release-dry-run` regenerates `pacto-dev-gateway/README.md` from a local
helm-docs, which is a pre-existing local-only side effect unrelated to this
repair; it was reverted and never committed. The tracked tree is otherwise clean
and the only untracked entries remain the inherited agent files.

### Required-CI wiring

The repaired tests run inside jobs that are already required, with no new
workflow, job or path filter. `pkg/dashboard/status_vocabulary_test.go`,
`pkg/fleet/keys_test.go` and `pkg/fleet/aggregate_test.go` are unit tests beside
their code and execute in `ci-engine` through `ci-test`, under the race detector
and the 100.0 percent coverage gate. `tests/architecture` continues to run in
`ci-gates`, which has no path filter. `docs/concepts.md` is validated by the
`docs-check` workflow and by the collector-overclaim gate in
`tests/architecture/collector_docs_test.go`, which already names the file.

### GitHub evidence at `3f477dc0`

Local HEAD, the branch ref, the remote branch and PR head are all exactly
`3f477dc0aa96088da332e6092e40cfcb30598bda`. PR 291 remains OPEN and DRAFT. The
statuscheck rollup at this head is 40 check runs: 37 success, two expected skips
(`build` on Rebuild dashboard UI, `auto-merge` on Auto-merge Dependabot PRs) and
the one inherited aggregate CodeQL failure. Every workflow succeeded on attempt
1; no run was retried.

- CI, run `32422947222`, attempt 1, success, all 21 jobs: `changes`,
  `ci-dashboard`, `ci-integration-kubernetes`, `ci-e2e-envtest`,
  `dashboard-e2e`, `ci-static`, `ci-oci`, `release-version-test`,
  `artifact-drift`, `operator-build`, `ci-e2e-compose`, `ci-engine`,
  `release-dry-run`, `ci-gates`, the six sharded `ci-e2e-kind` legs (reconcile,
  upgrade, dashboard, operational-graph, evidence, observation) and `required`
  (job `96601153513`).
- Security, run `32422947218`, attempt 1, success: Trivy image, govulncheck and
  the PR security summary.
- Docs check, run `32422947170`, attempt 1, success.
- Pacto Contract CI, run `32422947198`, attempt 1, success.
- Repowise architecture health, run `32422947128`, attempt 1, success.
- Validate PR title, run `32422947288`, attempt 1, success.
- Rebuild dashboard UI, run `32422947183`, skipped as expected.
- Auto-merge Dependabot PRs, run `32422947129`, skipped as expected.

### CodeQL and review-thread deltas

Code scanning on `refs/pull/291/head` reports exactly the same nine inherited
open alerts as the starting SHA: `#38` in `release/scripts/docs_check.py`,
`#40` through `#43` in `internal/app/resolve.go` and `#59` through `#62` in
`pkg/oci/cache.go`. The aggregate CodeQL check-run failure is the same inherited
"8 new alerts ... alerts not introduced by this pull request might have been
detected because the code changes were too large" summary. The **CodeQL delta is
ZERO**; this repair touches none of those files.

Review threads were paginated in full over two pages: 199 total, 189 resolved
and the same ten inherited unresolved bot threads, six on the generated
`pkg/dashboard/ui/assets/ganttDiagram-*.js` bundle and four on
`pkg/oci/cache.go`. The **thread delta is ZERO**.

### Repository hygiene

No PR comment was published, no thread was resolved and no PR metadata was
changed. No commit was amended, rebased, squashed or force-pushed; the branch
advanced by fast-forward only. `PACTO_PR_TARGET_STATE.md` is untouched. No
literal U+00A7 character was added and `make check-section` confirms it. The
inherited untracked agent files (`.claude/`, `.codex/`, `.mcp.json`,
`AGENTS.md`) are preserved and remain untracked.

### Explicit exclusions

- The operator CRD's kubebuilder `Enum` marker is a third copy of the status
  vocabulary, enforced by the API server on admission. Section 21.1 scoped this
  blocker to the fleet/dashboard pair and the operator was deliberately not
  pulled in.
- No generic enum framework, shared ontology package or validation framework was
  created.
- No prose-matching test was added to pin the Concepts wording; the behavioural
  bounds tests in `pkg/fleet` remain the owners of those semantics.
- No new acceptance machinery was added. No browser, Kubernetes or Compose
  behaviour changed.

### Status

**Phase 13 remains a CANDIDATE at `3f477dc0`, awaiting independent review. It is
not self-declared closed.**

- Phases 1 through 12: ACCEPTED and CLOSED.
- Inter-phase required-CI determinism repair: ACCEPTED and CLOSED.
- Phase 13: CANDIDATE with Blockers A and B repaired, awaiting independent
  review.
- Phase 14: NOT STARTED.

The PR remains an open draft.
