# @pacto/core

## 3.3.0

### Minor Changes

- 771574b: Add `--root` to `pacto fleet`, `pacto tui` and `pacto impact`: a contract root
  whose whole dependency **closure** joins the snapshot.
  
  Every other definition source stops at what someone remembered to list. `--local`
  scans a directory and finds the bundles that happen to be in it; `--oci` pulls
  exactly the references you typed. So a bundle declaring a dependency on
  `oci://ghcr.io/acme/payments:2.1.0` left a dangling edge unless you also passed
  that reference yourself — and the graph reported a service with no dependents
  when the truth was that nobody had looked.
  
  `--root` resolves the root you name and then follows its declarations,
  transitively, the way `pacto mcp --root` already did. It is the same discovery
  through the same resolver, so a catalog session and a fleet snapshot cannot
  disagree about what a reference means. Roots and dependencies that fail to
  resolve stay visible as limitations rather than vanishing, so a short closure is
  never served as a whole one.
  
  ```bash
  pacto fleet graph payments --root ./orders          # follows orders' declarations
  pacto tui --root oci://ghcr.io/acme/platform:1.4.0  # the whole platform closure
  ```
  
  On `pacto mcp`, `--root` keeps its existing meaning — it selects the read-only
  catalog server, and stays mutually exclusive with `--fleet`.
  
  Fixes a related identity bug this exposed: `--local` hashed the raw directory
  while the lockfile, the catalog and a pushed artifact all hash the *packaged*
  file set. One developer's stray `.DS_Store` was therefore enough to make the same
  bundle look like two different revisions of one service at one version — a
  content conflict reported against a fleet where nothing had changed. Local
  revisions now hash the `.pactoignore`-filtered file set, like everywhere else.
- 771574b: Fix two ways a fleet snapshot could report itself healthier than it was, and
  report a contested lockfile instead of silently dropping its pins.
  
  A source that dropped or invalidated a record used to keep saying it was
  `available`, and the snapshot kept calling itself `complete`. The two halves of
  one collection were graded differently: a deployment target kept after one bad
  enum value was normalized marked its source partial, while a revision discarded
  outright — no immutable digest and a bundle that could not be hashed — left the
  source looking fully read, even as the snapshot's own limitations said a record
  was missing. Both now count. If a source could not deliver a record, every
  answer drawn from it carries the incomplete-knowledge envelope that fact
  deserves, so an absent service is never read as proof the service does not
  exist.
  
  Two sources that disagree about one revision's `pacto.lock` are now reported
  with a new `REVISION_LOCK_CONFLICT` limitation. A lock decides which bundle a
  declared dependency or reference actually resolves to, and the same revision can
  reach a snapshot from a registry and from a working copy with different lock
  bytes. Serving whichever arrived first would let source completion order change
  the resolved graph under an unchanged snapshot ID, so the pins are discarded —
  and the reference detail now says they were discarded because contributors
  disagreed, rather than reporting that the lockfile recorded no resolution at
  all. That distinction matters: the old wording sent operators off to re-run
  `pacto lock`, regenerate identical pins and watch nothing change. Locks are
  compared on the resolutions they record, so two contributors who produced
  byte-identical pins on different Pacto releases still agree.
  
  Two limitation codes are deprecated and no longer emitted by anything.
  `REVISION_CONTENT_CONFLICT` described two sources pinning one revision key to
  different contract bodies, which a content-addressed key rules out; the real
  disagreements it stood for are `REVISION_DOCUMENT_CONFLICT` and the new
  `REVISION_LOCK_CONFLICT`. `REVISION_CONTENT_MUTABLE` described a revision
  resolved through a tag or a path, which `REVISION_IDENTITY_UNRESOLVED` already
  says at the point the identity is derived. Both Go constants remain exported
  through v3 so existing code keeps compiling, and both are removed at v4.
- 771574b: Stop a warm OCI cache from answering for a registry it has not checked, and make
  `--no-cache` mean the same thing everywhere.
  
  **A cached tag is revalidated before it is served.** A digest names its own bytes,
  so an entry written under one can never go stale. A tag is mutable: the disk entry
  records what the tag pointed at when some earlier process wrote it, and re-pushing
  the tag left the cache handing back the old artifact under the new name. That is
  worse than stale — it is what disarmed `pacto lock`, whose whole job is to notice
  a dependency's bytes changing under a fixed reference. A developer with a warm
  cache got "no drift" on a dependency that had been republished.
  
  `PullPinned` now resolves the tag's current digest before serving a disk entry.
  It costs one manifest lookup and re-downloads nothing when the tag has not moved.
  A registry that cannot be reached is not evidence that it did, so the entry is
  still served and an offline reader keeps reading the cache. Only the disk leg
  asks: an in-memory hit was put there by this same process, which already observed
  the registry for that reference, so one command still resolves one tag to one
  artifact. `pacto pull --local-only` and the other offline readers are untouched
  and still never contact a registry.
  
  **`--no-cache` now reaches the fleet sources, and says so when it cannot help.**
  The flag was read straight off the command line by `pacto fleet`, `pacto tui` and
  `pacto impact`, so setting it through `PACTO_NO_CACHE` or through `no-cache: true`
  in the config file disabled the bundle store's cache but left the cached-bundle
  fleet source running — the disk cache the caller asked to ignore still contributed
  services to the snapshot. The resolved decision is now written back onto the flag,
  so all three spellings mean one thing.
  
  And a bundle store with no cache to disable is now an error rather than a silent
  no-op. Proceeding quietly ran the whole command against the very cache the caller
  asked it to ignore, and reported success.
- 771574b: Resolve a dependency from the contract that declared it, grade every interface
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
  
  Keeping them means keeping them honest, so two that had drifted from the code
  they now defer to are repaired rather than left to rot behind the marker:
  
  - `sbom.HasSBOM` skips directories, as `ParseFromFS` already did. A directory
    named `deps.spdx.json` used to make it answer true where `ParseFromFS` answers
    nil, which is precisely the question its deprecation note says the two settle
    the same way.
  - The ingestion store's `fleet.Source` adapter drops a record whose compliance is
    outside the canonical vocabulary and raises `SOURCE_RECORD_INVALID`, matching
    the live evidence source. It used to copy the status straight through, so the
    two disagreed about the same record and an uninterpretable one entered the
    graph as though it had been understood.
- 771574b: Retire the dashboard's second ingestion stack, and make the static export and the
  published API document tell the truth about what they answer.
  
  The dashboard used to run two independent ways of turning references into services
  in one process: the original `DataSource` stack (`source_local.go`,
  `source_oci.go`, `source_cache.go`, `source_k8s.go`, `detect.go`, its own cache and
  its own multi-source resolver) and the operational graph in `pkg/fleet`, reached
  through `SetFleetProvider`. They disagreed about freshness, about partiality and
  about what "this source is available" means, and only one of them carried the
  completeness envelope every fleet answer is supposed to carry. The `DataSource`
  stack is gone. Every source the dashboard serves — local, OCI, cached, Kubernetes
  and observation — now arrives through `pkg/fleet`, so `/api/sources` reports the
  same health the fleet reports and an incomplete read is never rendered as an empty
  one.
  
  The contract-view types the static export and `pacto doc` render moved out to
  **`pkg/contractview`**, a leaf with no HTTP and no Huma in it. `pkg/dashboard`
  keeps every released name as an alias, so existing imports compile unchanged.
  
  **Breaking, and deliberate: the whole `pkg/dashboard` multi-source surface is
  removed.** Everything else in this release is additive, so this ships as a minor
  with the break called out here rather than held for v4. A shim is not possible
  for any of it: these names have no successor inside `pkg/dashboard` to forward
  to. What replaced them lives in `internal/fleetsrc`, behind `pkg/fleet`, and is
  reached through `SetFleetProvider`. A wrapper that accepted a `DataSource` and
  ignored it would turn a compile error into a dashboard that silently serves
  nothing, which is the worse failure.
  
  ```go
  // before
  srv := dashboard.NewServer(src, dashboard.EmbeddedUI())
  
  // after
  srv := dashboard.NewServer(dashboard.EmbeddedUI())
  srv.SetFleetProvider(func(ctx context.Context) (*fleet.Query, error) { ... })
  ```
  
  The full list, so nobody discovers it at compile time:
  
  - Constructors: `NewServer` loses its `DataSource` parameter, `NewResolvedServer`
    is removed.
  - `Server` methods: `SetResolver`, `SetCacheDir`, `SetCacheSource`,
    `SetOCISource`, `SetK8sRedetect`, `SetLazyEnrich`, `RefreshCacheSources`,
    `UpdateSourceInfo`, `WaitForVersionEnrich`. Source wiring, cache wiring and the
    enrichment handshake are all the fleet's job now.
  - The source interface and its implementations, with their methods:
    `DataSource`, `LocalSource`, `OCISource`, `K8sSource`, `K8sClient`,
    `CRDDiscovery`, `CacheSource`, `ResolvedSource`, and the constructors
    `NewLocalSource`,
    `NewOCISource`, `NewK8sSource`, `NewCacheSource`, `NewResolvedSource`,
    `BuildResolvedSource`, plus `ContractRefProviderFromSource` and
    `RepoProviderFromSource`.
  - The source-local cache: `Cache`, `CachedDataSource`, `NewMemoryCache`,
    `NewCachedDataSource`. The fleet snapshot is the cache now.
  - Detection: `DetectSources`, `RedetectK8s`, `CurrentKubeContext`,
    `DetectOptions`, `DetectResult`, and the diagnostics types
    `SourceDiagnostics`, `LocalDiagnostics`, `OCIDiagnostics`, `K8sDiagnostics`,
    `CacheDiagnostics`.
  - `ClassifyVersions` and `BundlePair`, which classified versions for a stack
    that no longer produces them.
  
  **`pacto dashboard --diagnostics` is removed**, with the `PACTO_DASHBOARD_DIAGNOSTICS`
  environment variable and the `DashboardConfig.Diagnostics` field behind it. The flag
  existed to register `/api/debug/sources` and `/api/debug/services`, which reported on
  the source stack; both endpoints went with it. The field is the one removal here from
  a type that survives, and it is not kept as an inert bool on purpose: `DashboardConfig`
  is published as a JSON Schema, so a retained field would advertise a diagnostics panel
  that no longer exists. `/api/sources` now carries the fleet's own health and
  completeness, which is what the panel was reading for. Scripts passing the flag will
  fail with an unknown-flag error rather than silently changing behaviour.
  
  `CRDDiscovery` in the list above is a re-export of an `internal/k8sclient` type, and
  it goes for a second reason beyond its stack: `pkg/dashboard` is now gated k8s-free by
  `tests/architecture/boundary_test.go`, so keeping the alias would pull client-go back
  into a package that must stay consumable without it.
  
  If you were importing any of these, you were driving the dashboard's private
  ingestion. Build a `fleet.Query` and hand it to `SetFleetProvider` instead; that
  is the same data with a completeness envelope attached.
  
  Everything else that moved kept its name. `ApplyLock`, `AggregatedService`,
  `SourceInfo`, `ServiceNameInput`, `ComputeDiff`, `DiffResultFromEngine`,
  `GraphFromResult` and `ComputeRuntimeDiff` are all still exported from
  `pkg/dashboard` with their v3 signatures, the last four marked deprecated
  because nothing in Pacto calls them any more.
  
  `ServiceDetails.SectionMeta` and the `Section*` vocabulary around it are
  deprecated. Their two writers went with the ingestion stack and the field has had
  no producer since; it is `omitempty`, so it is simply absent on the wire. The names
  stay through v3 and are removed at v4.
  
  **`get-service-graph` is removed from the published OpenAPI document.** Nothing
  answered it: the live host serves `/api/fleet/services/{name}/graph` and the
  offline export serves the global `/api/graph`. The generated TypeScript client's
  `serviceGraph` facade had no callers and goes with the operation. Three operations
  that the document listed and the export could not answer — a specific version, the
  per-source breakdown and a same-version diff — now have real fixtures instead, so
  the offline single-service app no longer meets a 501 on a call it makes itself.
  
  **Behaviour change in `pacto doc`.** Every service page now renders all eleven
  domain sections; an empty one says "None declared" instead of vanishing. The
  contents rail always listed all eleven, and the presence map it consulted had no
  writer, so it was offering jump targets that scrolled nowhere.
  
  Also in `pacto doc`: a dependency's display name is derived by parsing the
  reference rather than by scanning it for the last colon. A reference carrying a
  registry port rendered as `localhost`, and one with no tag at all rendered as
  `oci` — both now render the repository's last path component, like every other
  reference already did.
- 771574b: Add `pacto tui`, a full-screen terminal front-end over the CLI.
  
  `pacto dashboard` answers "what is the fleet doing" in a browser and only reads.
  `pacto tui` answers it in the terminal you are already in, and it also writes —
  because the point of a front-end over the CLI is that you do not have to leave it
  to run the command. It takes the same source flags as `pacto fleet` and navigates
  the same snapshot: services, revisions, targets, owners and sources, opening on
  the Services tab. Whatever row is highlighted becomes the argument, so you never
  type a path.
  
  Read verbs run in-process against the loaded snapshot — validate, explain, fleet
  explain, lock check, diff, impact and the neighborhood graph. Write verbs — push,
  pull, lock update and generate — shell out to this same binary so they own the
  terminal, and each names what it is about to change before it waits for a `y`:
  the directory a pull will overwrite, the resolved plugin binary and the output
  directory a generate will write. A successful write reloads the snapshot rather
  than leaving a confidently stale list on screen. `--read-only` hides the four
  write verbs entirely rather than refusing them at the last moment, and `y` copies
  the equivalent `pacto` command — shell-quoted, so a contract value carrying a
  space or a semicolon pastes as one argument — for anything the TUI does not
  offer.
  
  Building it made a gap in shell completion obvious: the closed vocabularies the
  code already owned were never declared to cobra. Seven flags now complete from
  their real source of truth — `--status` and `--compliance` from
  `fleet.CanonicalStatuses()`, `--workload` from the `contract.Workload*` constants,
  and `--direction`, `--ui`, `--transport` and `--output-format` from the values
  their own validators accept — and the four `pacto fleet` positionals no longer
  offer filenames for arguments that are never paths.
  
  **One behaviour change comes with that, and a script can trip over it.** `pacto
  doc`'s three mutual-exclusion checks are now
  `cmd.MarkFlagsMutuallyExclusive("serve", "ui", "output")` instead of hand-rolled
  value comparisons. Cobra tests whether a flag was *set*, not what it was set to,
  so all three of these now error where they used to be accepted:
  
  ```
  pacto doc --serve=false -o out.md
  pacto doc --ui= -o /tmp/o.md .
  pacto doc -o "" --serve .
  ```
  
  Nothing in the repo relied on any of those spellings. The one check cobra has no
  primitive for — `--interface` requires `--ui` — stays hand-rolled. `--ui`'s help
  string now names the closed set it enforces instead of advertising an open one.
- 771574b: Resolve every policy reference from the contract that declared it, and fail closed
  when the referenced schema cannot be read.
  
  **Relative references now resolve against their declarer, not the working
  directory.** A `ref:` written in a bundle three hops down the dependency chain was
  resolved from wherever `pacto` happened to be invoked, so the same contract
  validated differently depending on which directory you ran the command in. Worse,
  a contract pulled from a registry could name a local directory and have Pacto read
  a policy schema out of the invoking machine's filesystem. Resolution now carries
  the declaring origin: a reference reached from a registry bundle can only ever
  resolve to another registry bundle, and a local reference resolves relative to the
  contract file that wrote it.
  
  `validation.OriginBundleResolver` is the new port — `RootBase()` plus
  `ResolveBundleFrom(base, ref)` — mirroring `graph.OriginContractFetcher`. The
  widening is additive: `ResolveBundleFrom` is discovered at runtime, so
  `ValidateWithResolver` and `ResolvePoliciesWithResolver` keep their signatures and
  an existing `BundleResolver` implementation behaves exactly as it did. Cycle
  detection keys on the pair (declaring base, reference text) rather than the text
  alone, so a diamond in the reference graph is no longer misreported as a cycle.
  
  **A referenced bundle whose policy schema cannot be read is now an error.** Three
  conditions — an unreadable schema file, one that is not JSON and one that does not
  compile — returned `nil` and dropped the policy silently whenever the referenced
  bundle declared `policies[]`, while the sibling branch raised
  `POLICY_REF_UNRESOLVED` on exactly the same three. A platform bundle shipped
  without its declared schema therefore made every consumer's `pacto validate` and
  `pacto push` pass with zero policies enforced. All three now raise
  `POLICY_REF_UNRESOLVED`.
  
  **A contract that used to pass may now fail.** That is the point: it was passing
  because nothing was being enforced. If `pacto validate` starts reporting
  `POLICY_REF_UNRESOLVED` against a bundle that was green before, the referenced
  bundle is not shipping the schema its own `policies[]` block promises.
  
  **`service.version` must be a single safe path component.** `pacto pack`
  interpolates it straight into its output filename, so a version carrying a path
  separator or naming the parent directory let a contract authored in a pull request
  write its archive outside the build root. The pattern lands in the JSON Schema
  rather than in the pack command because every consumer runs the schema — a guard
  in `pack` alone would leave the operator, the dashboard and the MCP server open.
  Every shape semver produces still passes, prerelease and build metadata included,
  and so does a plain label like `latest`.

### Patch Changes

- 771574b: Give the dashboard a real document outline: a collapsible section's title is now
  a heading, not just a button.
  
  Every accordion on a service page — Overview, Interfaces, Dependencies,
  Configurations, Policies, Readiness and the rest — rendered its title as a bare
  `<button>`. Visually that reads as a section title; to a screen reader it was a
  control with no structural meaning, so the page went straight from its `<h1>` to
  the `<h3>`s buried inside a section body. Users who navigate by heading got a
  flat list of subsection names with nothing to say which section each belonged
  to, and the skipped level is a WCAG 1.3.1 failure. The toggle is now wrapped in
  an `<h2>`, the WAI-ARIA accordion pattern, so the outline reads h1 → section →
  subsection. Nothing moves on screen.
  
  Three heading levels that were only legal by accident are corrected with it:
  "Skills" and "Secret Keys" were `<h4>`s that read as valid only when some earlier
  section happened to supply the missing `<h3>`, and the empty services list titled
  itself `<h3>` directly under the page `<h1>`. A service page that fails to load —
  "Service not found", a failed remote resolve — now titles itself with an `<h1>`
  rather than leaving the page with no top-level heading at all.
  
  The route sweep that should have caught these was auditing an error state: it
  reached the non-Fleet views by telling the browser the host had no fleet, but the
  host it said that to answers no contract-view request, so every page under audit
  was a failed fetch. It now runs against a real `pacto doc --format html` export,
  which is the only place those views are served.
- 771574b: Fix a bundle scan reporting no services because one directory refused to open.
  
  Both filesystem-walking fleet sources aborted the whole walk on the first read
  error and marked themselves unavailable. For `--local` that meant `pacto tui`,
  `pacto fleet` and `pacto impact` answered "0 services" from a home directory:
  the walk reaches TCC-guarded paths like `~/Library/Accounts` within
  milliseconds, long before it reaches any contract, and gave up there. The same
  scan now finds 64 services on the machine this was found on. For `--cache` the
  same abort emptied the entire offline baseline when a single cache entry was
  unreadable, which is what one `sudo pacto pull` leaves behind.
  
  A refused directory is a gap, not a verdict. Both sources now record it as a
  `SOURCE_PARTIAL` limitation naming the path relative to the scan root, then step
  over it, exactly as an unparseable bundle already was. Past ten of them the rest
  are summarised as a count, so a home directory's hundred-odd privacy directories
  cannot bury the gaps a reader can act on. The walk root itself remains the one
  fatal case: nothing was read, so there is no partial answer to report.

## 3.2.9

### Patch Changes

- ddb6a46: Publish four documentation fixes that have been correct on `main` but absent from
  the site.
  
  `docs/examples/dashboard-demo.md` named `v1.2.0 → v2.0.0` as the breaking step of
  the demo fleet. OCI tags are immutable, so republishing that fixture under changed
  bytes renumbered it to `v1.2.1 → v2.0.1` and left the page pointing at two tags
  that no longer exist. The same stale literals were what broke the WASM demo smoke
  test and turned the docs pipeline red; that test now finds the breaking step by
  behaviour rather than by version, and this ships the half a reader can see.
  
  The rest of the batch rides along because `release.yml`'s `docs` job is the only
  publisher and it builds the whole site: the mkdocs-material 9.6.21 → 9.7.7 bump,
  the two `aria-labels.js` fixes that keep it accessible and a testing note. 9.7
  wraps every code block's buttons in an unnamed `<nav>` and labels the breadcrumb
  with the same string the primary navigation uses, so every page carrying two code
  blocks fails landmark-unique. The theme bump has never been published without its
  fixes, and shipping them in one transaction keeps it that way.

## 3.2.8

### Patch Changes

- a6fe2a3: A guided tour of what Pacto tells a human and an agent, and the five fixes
  building it uncovered.
  
  `docs/examples/demo-tour.md` walks a sixteen-service fleet as six user stories,
  in the order the questions arrive: I inherited this fleet, something says
  Unknown, I am about to ship a break, who do I have to tell, my diagram and my
  traffic disagree, and I want an agent on this without handing it write access.
  Every command on the page is real. The terminal transcripts are generated from a
  live run by `make gen-demo-transcripts`, snippet-included rather than pasted, and
  compared by the docs drift gate — so a page claiming output Pacto no longer
  produces fails CI. The two commands no generator can cover — an MCP server
  holding stdin open and a live dashboard — are copied in by hand, and the page
  says so. Every one of them runs in `tests/acceptance/local/demo-arc.sh` on each
  pull request, and both readers plus the generator share one argument table, so
  the page and the test can never drift apart on what was actually run. A release
  test asserts the two sets cover each other exactly: nothing runs in CI untaught,
  and nothing on the page includes a transcript that does not exist.
  
  Building it against a fleet large enough to be interesting surfaced five bugs
  that a three-service fixture never reaches:
  
  - `pacto diff` iterated Go maps directly in eleven collections, so its change
    list came out in a different order each run: twenty runs of one diff produced
    six distinct byte sequences in text, six in JSON and five in Markdown. A tool
    whose output people paste into review comments and gate CI on cannot print
    different bytes each time. It is now byte-deterministic.
  - `pacto impact` keyed the changed service by a ref classifier that disagreed
    with the one that actually loads the bundle: one treats a ref as local only on
    `.`, `/` or `~`, the other treats it as OCI only on `oci://`. A bare relative
    path loaded as a local bundle but was keyed under an invented OCI domain, so
    its identity never joined the fleet and every consumer silently vanished. The
    same two bundles found four consumers and exited 1 when passed as `./a ./b`,
    and reported none and exited 0 when passed as `a b`.
  - `pacto impact` left completeness untouched when the changed service was missing
    from the graph, so a breaking change printed "No affected consumers.", nothing
    on stderr and exit 0 — a silent all-clear that actually meant "I could not
    tell". The answer is now marked partial and says so. The exit code is
    deliberately unchanged; that interacts with the documented declared-only
    contract and is filed separately.
  - `pacto fleet` picked a service's owner by sorting revisions on `RevisionKey`,
    which is `ServiceKey@sha256:<content digest>` — digest order, not chronology.
    Editing any byte of any bundle could silently change who Pacto says to notify,
    which lands directly on the blast-radius answer. It now uses the semver-first
    revision chronology the package already documents for this exact hazard.
  - `pacto mcp --fleet` read `--traces` but never registered it: the flag was
    persistent on the `fleet` parent, and `mcp` is not its child, so the lookup
    error was discarded and the snapshot got nil. An agent could never see
    `provenance=observed` — the observed edges that find undeclared consumers were
    absent from the agent-facing surface entirely.
  
  The limitation that made the tour honest is now gone: AsyncAPI and gRPC
  interfaces are compared spec-against-spec, not just by ref. AsyncAPI channels
  and operations are deep-diffed so a payload property or a `required` entry
  surfaces on its own, and a `.proto` is scanned for services, rpcs, messages and
  fields. The proto scan is a text scan, not a compile, so a single pre-scan
  blanks comments and string-literal contents before anything is matched — a `//`
  or a `}` inside a string neither truncates a line nor closes a block early — and
  a field's inline `[...]` option block is parsed off and dropped, so adding
  `[deprecated = true]` is not a change while a retype behind one still is.
  `docs/contract-reference/diff.md` states what each comparison does and, in "What
  the proto comparison does not do", what it does not.
  
  The in-browser dashboard demo now teaches itself. Where the fixture notice was a
  banner and nothing more, it offers a guided tour: six steps, each naming one
  thing to do and each gated on the dashboard actually reaching the state that
  proves it was done. The gate is an observation, so no step is ever pressed to
  confirm a thing the tour just watched happen — it detects the action, holds the
  verdict until the gate has been quiet long enough that a search typed one
  character at a time is never interrupted mid-word, then says so.
  
  What follows depends on where the answer is. Where the result is read on the
  next screen, the tour moves itself after a moment's confirmation. Where the
  result IS the answer — the service in full, the field-by-field comparison that
  says Breaking, the neighborhood a change reaches — the next step navigates away
  from it, so those hold: the confirmation hands over a Continue the reader
  presses once they have finished reading, and the spotlight moves off the control
  onto what it produced. Showing someone a breaking change and taking it away a
  second later is the one outcome worse than making them press a button. The two
  steps with nothing to observe keep a real button throughout. Every gated step
  carries Skip, which performs the action for the reader rather than jumping past
  it — on a holding step that means they still get to read the result — and Exit
  leaves at any point. It is opt-in, and it ships only with the WebAssembly demo:
  `examples/demo/boot.js` is loaded by nothing else, so it cannot appear in front
  of a real fleet.
  
  The image tags a reader copy-pastes no longer age. The Compose demo page named
  `ghcr.io/trianalab/pacto/demo:3.2.7`, the dashboard page named its image five
  times and the operator's install page quoted a startup log naming
  `dashboard:3.2.1` — three releases behind — and nothing compared any of them to
  what was published. `apply-release-plan` now rewrites those coordinates the way
  it already rewrote the chart's `--version` pin, so the Version PR carries the
  new tag instead of leaving a command that resolves to nothing, and the
  idempotency proof covers the pages. That list is not the guarantee: the docs
  gate holds every page on the site to the tag its release unit published, so a
  page pinning a coordinate the rewriter has never heard of fails CI rather than
  rotting. Both pages now say the tag is the current release and that swapping it
  is how you run an older one, instead of repeating the number in prose where
  nothing can check it.
  
  The published `pacto-dashboard` bundle described half of its own API. The
  OpenAPI document it shipped was maintained by hand and had fallen to 18 paths
  against the 32 the server registers, so a consumer reading the contract could
  not see the fleet endpoints at all. `make gen-openapi` now generates it from the
  live Huma registrations, which makes the published bundle byte-identical to the
  drift-gated SDK contract instead of a second copy that ages on its own.
  
  Two fixture versions moved: `payments-service` 1.2.0 and 2.0.0 are already
  published to `ghcr.io/trianalab/pacto`, and the `capabilities[]` and `sbom/`
  additions this tour needs change their bytes, so they ship as 1.2.1 and 2.0.1
  and a published tag keeps meaning what it meant.
  
  CI now runs on the demo. `examples/demo/**`, its transcript generator, the five
  subsystems that produce the committed transcripts and the two those reach
  through are in the docs-check path filter, the transcripts are
  inside `generated_paths()` so the drift gate covers them, and an MCP integration
  test drives the demo fleet over a real stdio session — including the case where
  `pacto_fleet_status` called with no arguments returns a null item list, which an
  agent asking the obvious way reads as a clean bill of health. That is pinned as
  the trap it is, so it cannot change quietly before it is fixed.

## 3.2.7

### Patch Changes

- 5046822: Make the dashboard move, and make what it draws honest.
  
  Motion is a closed system rather than a per-component decision. Four roles —
  feedback, flip, reveal, dismiss — are declared once in `tokens.css`, and a
  ration governs who may use them: only an error state may enter on its own and
  carry the alarm ring, a warning may enter but never rings, and every other tone
  stays still. The number of moving things on a screen is the number of things
  wrong with it. Every entrance respects `prefers-reduced-motion`.
  
  The graph stops being a picture and becomes an instrument. The subject of the
  page carries a standing halo, so arriving on a graph tells you what it is about;
  a click pins a directional spotlight and re-frames the camera without ever
  re-laying-out; edges answer the pointer; a fit can no longer zoom past the point
  where labels are text. The legend was a caption listing distinctions the canvas
  actually draws — every entry is now a toggle that dims exactly that distinction,
  because a dense neighborhood is read by taking things out of it. Picking from
  the accessible text list points the canvas at the same node, so the two halves
  of the screen can no longer describe different things, and a summary line states
  the counts, what the legend is hiding and what is selected.
  
  Three bespoke charts are gone. A treemap, a donut and a second bar chart are
  replaced by the two house forms already used elsewhere, and
  `cytoscape-expand-collapse` — a dependency behind a toolbar nothing rendered —
  is removed with it.
  
  Chart corrections, each a case of the drawing contradicting the data:
  
  - The priority quadrant measured every service against a fixed midpoint while
    the gate is per service, so a service scoring 60 against a threshold of 80 sat
    on the healthy side of the line while failing. Points are now plotted as
    distance from their own threshold, and the divider says what it is.
  - Dot radius encoded blast radius, which is already the y position — the same
    number drawn twice. One radius for every dot.
  - The version timeline positioned markers by date and drew no date axis, so the
    only way to read one was to hover it.
  - Compliance status was three separate tables — the row badge, the legend swatch
    and the graph node — and they disagreed. "Unknown" was a blue badge, an amber
    swatch and a grey node on one screen. One table now decides both the wording
    and the tone, and every surface reads it.
  - Distribution shares printed a decimal below a population of a hundred, stating
    "12.5%" where the smallest step the data can take is 12.5 points. The same
    rounding ran out at the other end: one invalid target in a fleet of three
    thousand printed as "1 (0% of 3000)", a row contradicting the count beside it,
    and it is exactly the row a triage page exists to surface. A non-zero share now
    prints as the bound it is under rather than as nought.
  
  A drawer opening beside the graph pushed the page off the right of the screen.
  Cytoscape writes its current pixel width onto a wrapper inside the canvas, so the
  graph's minimum width was whatever it was last laid out at, and no track holding
  it could shrink. The graph now contains its own inline axis and sizes from the
  outside in, which fixes every layout that embeds it rather than the one that
  happened to notice. A claim's source revision is abbreviated rather than printed
  as a full digest, and the responsive gate reads the body's scroll width as well
  as the document's — clipped overflow is invisible to the document — at desktop
  widths as well, where the drawer that started this actually opens.
  
  Change analysis joins the long pages on the shared "On this page" rail. A finished
  analysis runs several screens deep — the revision pickers, the change table, then
  the consumer table under it — and getting back up to compare a different pair was a
  scroll. It is the navigator the overview and the entity pages already use, so there
  is no second contents list, and it lists only what the page actually rendered: there
  is no "What it affects" entry until there is a result.
  
  The overview draws populations it can count. Nine operational targets on a
  proportional bar is a shape the reader has to convert back into nine things;
  drawn as nine marks, two of them red, the count is the picture. Past a hundred
  and twenty members the marks stop being countable and the proportion is the
  honest reading again, so the bar comes back — and a population that over-counts
  itself is never drawn as marks at all, because it has no individuals to draw. The
  prose around them is cut: a sentence under the posture bars restated two bucket
  values that were already on the screen, so there are no longer two copies of the
  same number to check against each other.
  
  Those marks are now sized from the population, so a small one is big: nine
  targets drawn as nine fixed sixteen-pixel squares was a smudge in a
  four-hundred-pixel column — countable in principle and nothing to look at — and
  the size steps down only as fast as it has to for the row to keep fitting. The
  gutter and the corner are fractions of the mark, so a field of nine and a field
  of ninety are the same drawing at two scales rather than two different charts.
  The three posture questions sit in three columns where there is room, instead of
  two and an orphan below an empty half-row. And every distribution now puts its
  picture first: the description used to sit between the heading and the graphic,
  so a band of three charts was read as six lines of caveat with drawings between
  them, and each figure announced itself to a screen reader by reciting the whole
  paragraph. The prose is a footnote to the drawing, so it is printed under it.
  
  The WebAssembly demo's notice can be dismissed. It floats over the bottom of the
  dashboard and never left, which on a short window is where the content is. A
  failed engine load brings it back: that is the one message the reader cannot be
  allowed to have closed.
  
  Ten scale limits are closed. Measured against a fleet of five hundred services,
  two thousand revisions, three thousand targets and eight thousand relationships,
  each of these was a place where the cost of an answer grew with the size of the
  fleet rather than with the size of the question:
  
  - Every concurrent request that missed a cold service index ran its own full
    serial resolution of the whole fleet. One rebuild is admitted at a time and the
    callers queued behind it return the index it stored, doing no work at all.
  - A Fleet host issued a whole-fleet `/api/services` it never reads, every two
    seconds. The capability probe used to race the load it decides the shape of, so
    the first pass always read "capabilities unknown", took the legacy branch and
    paid for an answer it threw away. It is probed first now, at the cost of one
    round trip on first load. The poll also no longer stacks a second pass on top
    of one still in flight, while a manual refresh is still never dropped.
  - The whole-fleet dependency graph had no node bound. It takes one, defaulting to
    the engine's ceiling, and every node carries the path taken to reach it — so an
    unbounded deep answer was quadratic on the wire, not linear.
  - `Meta` applied none of the envelope caps `ProductMeta` applies, so two answers
    from the same snapshot could disagree about how much they left out.
  - The per-target revision match rebuilt its candidate set from every revision in
    the fleet; it is grouped once per service and reused.
  - The bulk snapshot export round-tripped through a marshal-and-unmarshal
    defensive copy on its way to a wire it is then written to and dropped.
  - A truncated graph said only that it had been truncated. It now says how much is
    missing, and offers the next node budget — sixty, a hundred and fifty, five
    hundred, the same rungs the backend will honour, because a control that offers a
    step the server then clamps is a control that lies about what it just fetched.
    The budget is part of the URL, so a shared link reopens the graph being
    discussed rather than a smaller one.
  - A fit clamped at the legibility floor left part of the graph off screen and
    said nothing, and a cropped canvas looks exactly like a complete one. It says so.

## 3.2.6

### Patch Changes

- e36b181: Reframe the README, the pacto.run homepage and the pages that define what Pacto
  is around a positive category noun: **operational contract system**.
  
  The definitional slot on both front doors was held by an analogy
  ("Pacto is to service operations what OpenAPI is to HTTP APIs") and, on the
  homepage, by an eyebrow reading "Open contract standard". The first installed
  "file format" as the category and discarded the engine; the second promised
  governance and a second implementation that do not exist. Both are replaced by
  a definition that states what a contract records, how it is published and what
  it is compared against.
  
  The category noun says what Pacto is; one sentence beside it now says what that
  is for. Pacto gives software a machine-readable operational interface — a
  versioned description of what a service is, what it exposes, what it depends on
  and what it promises — so platforms, CI systems, controllers, automation and
  agents consume the same interface instead of each reconstructing operational
  knowledge from deployment files, documentation and runtime state.
  
  Structural changes:
  
  - `README.md` leads with the category, then the problem (operational facts with
    nowhere to live, and a dependency edge that carries no version range), then
    the mechanism, then who reads a contract, then why software operating software
    raises the price of not having one. "What Pacto is NOT" drops from four
    bullets to three and moves behind all of it.
  - `docs/index.md` moves "What Pacto is not" below "The problem" — non-goals
    disambiguate a model the reader already holds and cannot build one. The
    heading and its anchor are unchanged, so `#what-pacto-is-not` still resolves.
  - The hero's only jump link now points at `#what-is-pacto` rather than at the
    list of exclusions.
  - The IDP contrast is cut back to a single non-goal bullet. What replaces it as
    the differentiator is version shape: a catalog entry records that an edge
    exists, a Pacto dependency records the range it accepts and pins the closure
    by digest.
  
  Corrections found while verifying the copy against the implementation:
  
  - `Diff · Graph · Enforce · Verify` becomes `Diff · Graph · Validate · Verify`.
    There is no `pacto enforce`; policy is Layer 3 inside `validate`, and
    `MANIFEST.md` disowned "enforcement" eight lines below the slogan.
  - `docs/contract-reference/sections.md` claimed a `configurations[].ref` is
    resolved from the referenced bundle at the fixed path
    `configuration/schema.json`. No code reads that path. The reference is
    validated as well-formed, recorded as a reference edge and pinned in
    `pacto.lock`; the recursive-resolution claim is scoped to the lockfile, which
    is the surface that actually walks the closure.
  - `docs/index.md` said `pacto generate` produces deployment artifacts. It
    invokes a `pacto-plugin-<name>` binary you supply; Pacto ships no generators.
  - "Blast-radius analysis" as an MCP capability becomes impact analysis, the
    feature that exists.
  - The Kubernetes overview stated "never modifies" and then retracted it. It now
    leads with the actual grant — `get`, `list`, `watch` on watched workloads —
    and keeps the managed-component escalation as the second half of the same
    paragraph rather than as a retraction.

## 3.2.5

### Patch Changes

- 840a183: Move both Go modules onto the Kubernetes 0.37.0 library line and patch the
  runtime image's OpenSSL.
  
  `k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` are now v0.37.0 in
  `go.mod` and `integrations/kubernetes/go.mod`, together with the transitive
  `k8s.io/kube-openapi`, `k8s.io/utils`, `k8s.io/streaming`,
  `sigs.k8s.io/structured-merge-diff/v6` and `go-openapi/swag` moves the line
  pulls in. The three library modules only work in lockstep, so bumping them
  one at a time — as the individual Dependabot pull requests did — leaves
  `k8s.io/api` behind and fails to compile.
  
  The runtime stage of the CLI/dashboard image now runs `apk upgrade` before
  installing its packages, so the image picks up the fixed `libssl3`/`libcrypto3`
  3.5.8-r0 instead of the 3.5.7-r0 baked into the `alpine:3.22` tag (CVE-2026-14456,
  HIGH).
  
  No API or behaviour change.

## 3.2.4

### Patch Changes

- 3987568: Restructure the documentation as one information system.
  
  An editorial and information-architecture pass over the whole surface: the nav
  is ordered as a reader's path, each concept has one canonical home, duplicated
  worked examples and repeated statements of the thesis are gone, and development
  history is out of the product pages. Three pages were split out of pages that
  were carrying two subjects — the Pacto model, dashboard architecture and
  observation sources. The published surface loses about 1,500 words while staying
  roughly the same length in lines: the prose is tighter and the split-out pages
  add the structure back. No technical claim was dropped, and no file changed path,
  so every existing URL still resolves.
  
  Docs-only; no functional change to the engine, CLI or dashboard. This core patch
  is the release that redeploys the site, which `docs.yml` deliberately does not do.

## 3.2.3

### Patch Changes

- b11de31: Move both Go modules onto the Kubernetes 0.36.4 library line.
  
  `k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` are now v0.36.4 in
  `go.mod` and `integrations/kubernetes/go.mod`. The bumps landed on `main`
  without a changeset, so neither the core line nor the kubernetes line would
  have shipped them — this patch is what actually publishes a core module and an
  operator image built against 0.36.4.
  
  No API or behaviour change: the 0.36.4 patch releases only refresh the
  `golang.org/x` dependencies underneath.

## 3.2.2

### Patch Changes

- 9f27024: Keep date scalars verbatim across generic YAML round-trips.
  
  `pacto_edit` could not edit a pristine `pacto init` scaffold. Edit reads
  pacto.yaml into a `map[string]any`, and yaml.v3 resolves an unquoted
  `readiness.expires: 2099-12-31` to a `time.Time`, so re-encoding wrote
  `2099-12-31T00:00:00Z` and the tool rejected the contract it had just produced.
  The same round-trip happens in `pkg/override` (`pacto pack --set`) and in the
  structural validator, which was handing the JSON Schema layer an RFC3339 string
  for a value the document spells as a bare date.
  
  The three sites now decode through `contract.DecodeYAML`, which retags
  `!!timestamp` scalars as `!!str` before decoding, so the text the author wrote
  survives untouched — the same thing `contract.Parse` has always done by decoding
  dates into string fields. Nothing is reformatted: a non-canonical `2099-1-1`
  stays rejectable instead of being canonicalised by an unrelated edit, an explicit
  `2024-01-15T00:00:00Z` keeps its time instead of being truncated to a date, and
  the schema layer never checks a value that is not in the file.

## 3.2.1

### Patch Changes

- c230de9: Make a demo-fixture edit unable to half-ship a release.
  
  Release run 32560058692 published four irreversible units and then died. Two
  independent defects had to line up for that, and both are closed here.
  
  The demo bundles publish to immutable tags. `payments-service` 2.1.0 was edited
  in place — a mermaid diagram added to a version already published — so the
  byte-exact gate correctly refused the tag, but it refused it mid-release,
  because nothing ran that gate before the release. The fixture is restored to its
  published bytes and the diagram ships as a new `payments-service` 2.1.1, and
  `publish-demo-bundles.sh --check` now runs the identical gate read-only at PR
  time as the `demo-bundle-immutability` CI leg.
  
  Separately, the `demo-compose` job lost its ORAS install when the unit moved to
  `docker compose publish`, on the reasoning that ORAS stayed where the ledger
  used it — while that job still read and wrote the ledger, which *is* the ORAS
  user. `ledger.sh` returned the empty string for a missing binary, the empty
  string means "nothing recorded", and the unit failed closed. `ledger.sh` now
  refuses to run without its tools and distinguishes a 404 from an unreadable
  registry; the two `if [ "$(ledger.sh …)" ]` call sites that discarded its exit
  status now assign first; and a new gate walks every job's shell through its make
  targets and scripts and fails when a job can reach a CLI it never installed.
  That gate found a second, quieter instance: the release dry run was rehearsing
  without `syft`, silently skipping the SBOM the real release produces.

## 3.2.0

### Minor Changes

- 8352060: Add the Pacto operational graph: what is declared, what is actually running and how the two differ.
  
  Pacto could describe a contract. It could not describe a fleet. This release adds
  the read model for that, and the surfaces on top of it.
  
  `pkg/fleet` composes many contracts, contract revisions and operational targets
  into an immutable, deterministic `FleetSnapshot` with a pure, network-free
  `Query` over it. It keeps three identities distinct — the logical service, the
  contract revision and the operational target — and it keeps them
  domain-qualified, so two teams may own a `checkout` without becoming one node. It
  makes incompleteness explicit: every snapshot and every answer carries an as-of
  time, a completeness and structured limitations, so an unreachable source is
  reported as an `unavailable` source that turns the answer's `completeness` into
  `partial` — surfacing in the dashboard as `unavailable` knowledge, taken from the
  worst source health — and never as an authoritative empty graph. `unknown` stays
  a distinct state, for when there is no completeness envelope at all.
  
  Around that read model:
  
  - **Evidence reporting.** `pacto evidence serve` accepts signed evidence sets
    from environments Pacto cannot reach, verifies the producer signature, checks
    the report against the resolved contract revision and records the result. An
    environment that stops reporting goes stale rather than disappearing.
    `pkg/evidenceenvelope` is the signed wire format and `pkg/evidenceingest` the
    accept pipeline.
  - **Evidence lives in the registry.** An accepted record is stored as an OCI 1.1
    referrer of the exact contract digest it is about, so the registry that already
    holds the contract is the only durable evidence system. No bucket, no database,
    no second persistence path.
  - **Contract catalog.** `pkg/catalog` answers what a set of contract roots and
    their closure contain, bounded and free of any delivery mechanism. It reaches
    agents over MCP as exactly two fixed read-only resources, `pacto://catalog` and
    `pacto://catalog/closure`, plus one tool, `pacto_catalog_revision` — and no
    resource templates, because a revision identity is four structured fields and a
    URI template would force the ad hoc encoding that identity discipline exists to
    prevent. The session is frozen, so a catalog answer cannot change underneath a
    conversation.
  - **Change impact.** `pkg/impact` and `pacto impact` answer who is affected by a
    change, computed over canonical identities and refusing a mutable reference.
  - **Reconciliation and observation.** `pkg/reconcile` compares the declared graph
    with the observed one; `pkg/otelobserver` reads an OpenTelemetry span export to
    discover calls nobody declared.
  - **CLI.** New `pacto fleet` (with `fleet reconcile`), `pacto evidence`,
    `pacto impact` and `pacto otel` command groups, all backed by the same read
    model.
  - **Dashboard.** A product-shaped interface over the graph: services, revisions,
    targets, owners and sources as first-class pages with canonical links between
    them, an attention view that ranks what is actually wrong, and a graph view
    that stays readable at fleet size. The wire contract is generated from OpenAPI
    end to end, so the frontend cannot invent semantics the backend does not have.
  
  Everything reports what it does not know. Evidence that is absent, stale, partial
  or unreadable is reported as such and is never rendered as a passing result.
  
  Backwards compatible: no existing flag, API or JSON shape changes.

## 3.1.4

### Patch Changes

- e5f696f: Fix the docs version selector so it opens on click. After the previous fix it no
  longer opened on hover (intended) but a `:focus-within` rule out-ranked the open
  class on click, so the dropdown stayed collapsed. Gated the hover/focus suppress
  rules with `:not(.md-version--open)` and raised the open rule's specificity.
  Docs-only; this core patch is the release that redeploys pacto.run/latest.

## 3.1.3

### Patch Changes

- f8aef8f: Deploy the docs version-selector fix to the live site: the mike version dropdown
  now opens on click, not hover, so it no longer pops over the nav tabs and swallows
  their clicks. Docs-only change (PR #286); no functional change to the engine, CLI,
  or dashboard. This core patch is the release that redeploys pacto.run/latest.

## 3.1.2

### Patch Changes

- bbc7b9c: Rebuild the operator and dashboard container images through the new native per-arch
  build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
  merged into the multi-arch manifest. No functional change to the engine, operator, or
  dashboard — this release ships and validates the faster image pipeline.

## 3.1.1

### Patch Changes

- dd4dab1: Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
  breaking-change diff false-negatives — path-item-level parameters are now diffed,
  the request body is deep-diffed so a newly required property is BREAKING, and
  optional→required / added-required parameters are BREAKING — so a BREAKING-only
  release gate can no longer be bypassed. Further security and dashboard fixes land
  in the same PR.

  As the first release since v3.1.0, this also ships the previously-merged but
  unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.

## 3.1.0

### Minor Changes

- b58778a: Unify all published OCI artifacts under the monorepo `ghcr.io/trianalab/pacto/*` namespace.

  - operator image → `ghcr.io/trianalab/pacto/operator`
  - operator chart → `ghcr.io/trianalab/pacto/charts/pacto-operator`
  - dashboard image → `ghcr.io/trianalab/pacto/dashboard`
  - dashboard contract bundle → `ghcr.io/trianalab/pacto/dashboard-contract`
  - demo bundles already live under `ghcr.io/trianalab/pacto/*`

  All packages are now created and owned by this repository. The chart name
  `pacto-operator` and the Artifact Hub repository are preserved (re-point the AH
  repository URL to the new chart coordinate). The previous coordinates remain as
  historical — their already-published versions are unaffected. Go module paths
  (`/v3`, `/v5`) are unchanged.

## 3.0.1

### Patch Changes

- d09f2cc: Publish the demo bundles to monorepo-owned OCI coordinates.

  The demo bundles previously targeted `ghcr.io/trianalab/pacto-demo/*`, packages
  owned by the old `pacto-demo` repository that the monorepo cannot write. They now
  publish to `ghcr.io/trianalab/pacto/*`, created and owned by this repo. This also
  re-cuts the dashboard contract bundle (its publisher now installs the pacto CLI +
  plugins) and folds in the cel-go 0.29.0 bump. The core fixed group advances one
  patch (core, cli, dashboard-image, demo-bundles, dashboard-contract-bundle).

## 3.0.0

### Major Changes

- 045f11e: Pacto 2.0 — breaking contract-model, engine and module-path changes.

  - The Go module path becomes `github.com/trianalab/pacto/v3` (was `.../v2`).
    Consumers must update their import paths.
  - The contract schema is v2 only (`pactoVersion "2.0"`); v1 fields
    (`runtime.*`, interface `port`, `scaling`, `service.image`) are removed.
  - New pure engine: `pkg/evidence` + `pkg/finding` + `Evaluate(contract,
evidence)`; `ValidateRuntime` and the v1 declaration-side runtime types are
    gone.
  - Releasing is driven by an explicit release transaction, not a manifest-file
    diff.
