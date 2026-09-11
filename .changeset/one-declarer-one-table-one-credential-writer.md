---
"@pacto/core": minor
---

Resolve a dependency from the contract that declared it, grade every interface
difference from one table, and stop `pacto login` from failing open.

**Relative dependency references now resolve against their declarer.** The graph
resolver built one base directory from the root and used it at every depth, so
`./b` written by a bundle one level down pointed at the root's sibling rather
than at its own. A root pulled from a registry got no directory at all, so its
relative references resolved against the process working directory — a remote
contract choosing which local files Pacto reads. A local reference declared
inside a registry bundle now fails closed, under the same rule the catalog
resolver already applied. `graph.OriginContractFetcher` is the additive port that
carries the declaring origin; a plain `ContractFetcher` still resolves exactly
the graph it always did.

Node identity moves with it. Two contracts in different directories can both
declare `./shared` and mean different bundles, and two can declare one registry
reference under `^1.0.0` and `^2.0.0` and mean different versions. The visited
map is keyed on the declaring base, the reference and the constraint, so those no
longer collapse into a single node with the second declaration discarded.

**`pacto login` no longer fails open on a config it cannot read.** It treated an
unreadable credentials file as an empty one and rewrote from a zero value, so a
transient permission or I/O error deleted every other stored credential and
reported success. `pacto logout` failed closed on the identical condition and
skipped the chmod login applied. Both go through `oci.SetCredential` and
`oci.RemoveCredential` now — new writers beside the reader that already owned the
format — with one error policy and one chmod.

**A lock entry pins a digest to the bytes it names.** Building an entry asked the
store what a tag pointed at and separately downloaded that tag. Those are two
observations, and a warm cache plus a re-pushed tag made them disagree
deterministically, after which `pacto lock --check` certified the result clean.
The lock builder and the catalog resolver both take the bundle and its digest
from one `oci.PullPinned` call now. An artifact whose identity the registry will
not state is recorded as unresolved rather than pinned to an empty digest.

**A lock error reports its own code.** The machine-readable code CI and the
operator branch on was derived by splitting the rendered message at its first
colon, so a file-read failure produced codes like `open /home/me/svc/pacto` and a
YAML parser's prose produced whatever it happened to say. Every `pkg/lock` error
type has a `Code()` method, `Error()` renders it, and anything carrying no code
is `LOCK_ERROR`.

**Responses are deep-diffed, and every classification resolves through one
table.** Request bodies were walked field by field while responses were not, so a
status code present on both sides reported one opaque `POTENTIAL_BREAKING` no
matter what changed inside it. Classification itself was written in three places
and the copies had drifted, and `.schema` was hardcoded as a modification, which
made the added and removed rules for that field unreachable — dropping a schema
graded as if it had merely been edited. Two invariants are pinned by tests:
removing part of a thing never grades worse than removing the whole thing, and a
schema reached through an `example` is documentation rather than the payload's
field set. `docs/contract-reference/diff.md` gains the rows that could not fire
and loses the two that never described real behaviour.

**Every MCP tool call is validated against its declared schema.** The SDK
documents that as the caller's job and nothing did it, so a declared schema was
decoration: `{"max_depth":"3"}` — a string where the schema says integer, which
models emit constantly — decoded to 0, and `pkg/fleet` reads 0 as unbounded, so a
caller asking to bound a traversal got an unbounded one. `required` was equally
advisory. Every tool registers through the typed generic form now,
bundle-derived capability tools included, and a schema that will not resolve is
registered as a visible unavailable tool rather than dropped or left to panic the
server. `pacto_check` runs the resolving validator the CLI runs, so an agent
looping until it reports valid can no longer terminate on a contract CI rejects.

**A registry's tag list is no longer memoized for the life of the process.**
`ListTags` cached a mutable registry fact forever, six lines below a `Resolve`
that documents why it deliberately does not. A dashboard rediscovers on a loop,
so every pass after the first answered from the first observation: a release
published after startup stayed invisible, and `/api/versions` with `fetch:true`
pulled nothing while reporting success. The memo expires on half the rediscovery
interval, so an entry written just after one pass cannot survive the next.

**Smaller, and visible from outside:** snapshot limitations print to stderr
rather than stdout, so a partial answer no longer contaminates piped output;
markdown table cells built from contract-controlled strings are escaped, so a
pipe in a config pattern no longer breaks the table around it; and `fleet.Build`
fills the freshness timestamps a source's own declared state left unset, so a
source that reports its health no longer reaches the snapshot looking like it
never synced.

Additive API: `oci.CacheLocator`, `oci.CacheDisabler` and `oci.CacheObserver`
name the three capabilities a bundle store may extend `BundleStore` with. They
were anonymous interface assertions, so a store that missed one silently did
nothing — which is how `--no-cache` came to be accepted and ignored. Also
`oci.PullPinned`, `oci.SetCredential`, `oci.RemoveCredential` and
`graph.OriginContractFetcher`.

Exported symbols left with no production caller are marked `Deprecated` with
their replacement and `Removed at v4` rather than deleted, because v3 is
published and this ships as a minor: `sbom.HasSBOM`, `oci.SetUserHomeDirFn`, the
`LocalOnly` resolver surface, the standalone sidecar reader and the ingestion
store's own `fleet.Source` adapter among them.
