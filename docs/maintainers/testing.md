# Testing architecture

Pacto has eight test levels. **A test belongs to exactly one**, chosen by what it
proves — never by its filename, its language, or the feature that happened to
introduce it.

That rule is the whole architecture. Everything below follows from it: where a
level lives, which language owns it, what may be shared between harnesses, and
how you pick a home for a test you are about to write.

## The taxonomy

| # | Level | What it proves | Lives in | Language | Run with |
|---|-------|----------------|----------|----------|----------|
| 1 | Unit | One package, in isolation | beside the code (`*_test.go`, `*.test.ts`) | Go, TypeScript | `make test`, `make ci-ui` |
| 2 | Integration | Several real components wired together, nothing over a network a user could reach | `tests/integration/`, `integrations/kubernetes/test/` | Go | `make test-integration`, `make ci-e2e-envtest` |
| 3 | Architecture / invariant | Structural rules about the repository itself | `tests/architecture/` | Go | `make ci-gates` |
| 4 | Local acceptance, cluster-free | A whole user story, anywhere Go runs | `tests/acceptance/local/` | Shell + Go | `make test-acceptance-local` |
| 5 | Kind / system acceptance | The product against a real Kubernetes cluster | `tests/acceptance/kind/` | Shell + Go | `make test-acceptance-kind` |
| 6 | Browser acceptance, deterministic | A real shipped artifact over fixed data | `pkg/dashboard/frontend/e2e/` (dashboard), `pkg/dashboard/frontend/e2e-docs-site/` (documentation site) | TypeScript | `make test-browser`, `make test-browser-docs-site` |
| 7 | Live-browser acceptance | The real frontend against a real cluster | `pkg/dashboard/frontend/e2e-live/` | TypeScript | `make test-browser-live` |
| 8 | Release verification | The release system produces what it claims | `tests/release/`, `release/orchestrator/` | Go, Node | `make ci-gates`, `make release-dry-run` |

Two names in the tree predate the taxonomy and are kept for the convention of
the module that owns them. Both are **level 2, integration**, and are labelled as
such where they are invoked:

- `tests/integration/` drives the CLI in process against a real in-process OCI
  registry and a real plugin binary. It was called `e2e` for historical reasons
  only; the build tag is now `integration`.
- `integrations/kubernetes/test/e2e` runs the operator's acceptance matrix
  against an **envtest** control plane — a real API server, but no cluster and no
  kubelet. `test-e2e` is the kubebuilder convention inside that module.

Make target names state the level, not the feature history. The pre-existing
`e2e-*` names remain as temporary compatibility aliases so muscle memory and any
out-of-tree caller keep working; they are aliases, not second names.

## Which language owns which level, and why

**Go by default.** Anything with substantial non-browser logic — lifecycle,
retained state, retries, semantic assertions, diagnostics — is Go. It is typed,
it is testable, and a gate that decides whether a live cluster passed should
itself have tests. Two acceptance gates are Go programs for exactly that reason:

- `tests/acceptance/kind/productready` waits for the live Product API to prove
  the operational-graph fixture, and emits the canonical entity keys it
  discovered.
- `tests/acceptance/kind/obscheck` decodes the operator's Deployment wiring and
  the resulting Product snapshot for the observation scenario.

Both have their own unit suites, run by `make test-integration` (they live under
`/tests/`, which the coverage gate excludes, so they are tested explicitly rather
than implicitly). Neither could exist as `curl | grep`: the claims are about
typed structures, and a shell pipeline that "found the string" proves much less
than it appears to.

**TypeScript owns browser-visible workflows.** Playwright drives Chromium; a
journey through the real bundle cannot be expressed anywhere else.

**Shell is limited to genuinely thin process orchestration.** Bringing a cluster
up, building an image, installing a chart, forwarding a port, applying a
manifest. A short readable sequence of real commands is the clearest possible
description of "install this and see what happens", and rewriting it in Go would
buy nothing. The moment a harness starts *deciding* things — parsing JSON,
comparing structures, accumulating verdicts — that part moves to Go.

Go is also free to execute real `kind`, `helm`, `kubectl` and `docker` when those
boundaries are what the test proves. Language uniformity is not a goal: a large
shell file that owns real lifecycle responsibilities is fine, and a small one
that quietly makes semantic judgements is not.

## Shared harness code

Every stable shared concern has **one** implementation.

`tests/acceptance/kind/lib.sh` is that implementation for the cluster scenarios.
It owns process execution and pass/fail reporting, eventually-conditions and
timeouts, cluster lifecycle and teardown, image loading, chart packaging and
Helm invocation, port-forwarding, readiness waits, the in-cluster registry and
trust keypair, bundle publishing, and failure diagnostics. Every `*.sh` under
`tests/acceptance/kind/` sources it.

What stays in the scenarios is **scenario-specific orchestration**: their EXIT
traps, their fixtures, their own assertions, and anything a single scenario needs
in a form no other scenario shares. Sharing something two harnesses merely
resemble each other in is how a helper acquires five boolean parameters.

Two behaviours in `lib.sh` are load-bearing and documented at their definition —
change them only with a reason:

- `pf` waits for the forward to be *ready*, not merely started, and reports on
  stderr. A port-forward that has not bound yet fails the next command with a
  connection error that reads like a product bug.
- `fail` writes to stderr for the same reason: call sites routinely silence a
  helper's chatty stdout, and a reason written to stdout would be silenced with
  it, leaving a run that exits 1 saying nothing.

`tests/acceptance/local/` has no cluster to manage and keeps its own small
helpers; `integrations/kubernetes/test/utils` serves the operator module.

## Declarative scenarios and their projections

When several surfaces describe the *same* fixture, the fixture is declared once
as data and each surface becomes a projection of it.

`tests/acceptance/scenario` is that declaration for the operational-graph
vertical. It expresses services, revisions, targets (as the deployed revision),
data sources, relationships, evidence, and which service plays which part in the
browser journeys. Its projections:

| Projection | Consumer |
|------------|----------|
| `Materialize` | the contract bundle directories the Kind harness publishes |
| `TraceExport` | the OTLP export the operator mounts as an observation source |
| `FactCount` | the denominator the Product gate reports progress against |

The **expected Product facts are not a separate document — they are the
scenario**. `productready` walks the value: a declared revision must be one
canonical retrievable revision, a deployed one must have exactly one operational
target linking to it, a relationship with an `ObservedBy` must be declared,
observed and reconciled.

Three rules keep this from becoming a framework:

1. **Bundle content stays literal.** A fixture contract is meant to be read, and
   a generator for it would be a second, untested implementation of the contract
   schema. The package's tests materialize the literals and parse them back with
   the real `contract.Parse`, proving the declared identity and the file agree.
2. **A projection exists only when it has a consumer.** The Pacto CRs and the
   Helm `--set` block are deliberately *not* projected: they must carry digests
   that exist only after the push, and they have one consumer each. A Helm or
   Docker Compose projection, when something needs one, is a sibling function
   over the same value.
3. **Journey inputs are discovered, not declared.** A `ServiceKey` is
   domain-escaped and a `RevisionKey` carries a content id. Reconstructing those
   escapes in a test would be a second implementation of the identity rules that
   could agree with itself while disagreeing with the product. The gate publishes
   the keys the Product returned, and the browser suite addresses exactly those.

Not every fixture belongs here. `observation.sh` keeps its own literal exports
because a malformed export and identity-escaping service names are the *subject*
of that scenario, not incidental data. `tests/acceptance/local/fleet-graph.sh`
describes a different story with different services and is not a projection of
anything.

## Deterministic browser tests versus live-browser tests

These are levels 6 and 7, and they stay apart. Determinism and liveness are
different properties, and a merged suite proves neither.

**Level 6, `pkg/dashboard/frontend/e2e/`** runs against the WASM demo build
(`examples/demo`): the real frontend bundle over an in-browser backend seeded
with fixed data. It is hermetic, needs no cluster, and is the right home for
anything about rendering, layout, accessibility, keyboard interaction,
responsiveness, graph behaviour and visual state. Because the data cannot move,
it can assert exact content.

**Level 7, `pkg/dashboard/frontend/e2e-live/`** runs against the port-forwarded
dashboard of a real Kind cluster, on keys discovered from the live Product API.
It proves the real bundle, real HTTP API and real operator data render together.
It cannot assert fixed content — the fixture's identities are discovered at run
time — so it asserts the *journeys*.

A test that could pass without a cluster belongs in level 6. Only journeys that
need real operator data belong in level 7, and level 7 does not duplicate what
level 6 already covers.

## Two products at level 6

Level 6 has two suites because Pacto ships two things a browser opens: the
dashboard and the documentation site. They are one level and two ownerships, and
the boundary between them is the artifact under test — never the subject matter.

**`pkg/dashboard/frontend/e2e/`** owns the dashboard bundle, run by
`make test-browser`, gated by the `dashboard-e2e` job in CI. `e2e/mermaid.spec.ts`
lives here and proves that *bundle documentation renders inside the dashboard*.

**`pkg/dashboard/frontend/e2e-docs-site/`** owns the MkDocs output, run by
`make test-browser-docs-site`, gated by the Docs check workflow. It builds the
real site with `mkdocs build --strict` through `mkdocs.test.yml` — an `INHERIT`
overlay on the real `mkdocs.yml` — serves it over HTTP and drives Chromium
against it.

Sharing stops at the pinned Playwright and Chromium installation, which is why
the second suite lives in the frontend package rather than growing a second
browser toolchain somewhere else. Config, `testDir`, project name, Make target
and CI job are all separate: a dashboard regression and a documentation
regression must not be able to mask each other.

Four things about the docs-site suite are load-bearing:

- **The overlay changes exactly one key that matters.** Material's instant
  navigation only intercepts a click whose URL appears in `sitemap.xml`, and the
  sitemap is written from `site_url`. Served at `127.0.0.1` against the
  production `site_url`, nothing is ever intercepted and the instant-navigation
  case would silently measure two ordinary page loads. The overlay points
  `site_url` at the test origin, so the port in `mkdocs.test.yml` and the one in
  `playwright.docs-site.config.ts` have to agree.
- **Every cross-origin request is aborted.** The site has to be self-sufficient.
  This is what keeps the diagram runtime pinned: Material's own fallback fetches
  an unpinned `mermaid@11` from unpkg, and `release/scripts/mkdocs_mermaid_hook.py`
  stages the lockfile-resolved copy into the site instead. Delete the hook or its
  `extra_javascript` entry and all three tests fail.
- **Rendered output is asserted, not source text.** Material renders each diagram
  into a *closed* shadow root, so the suite forces open mode via an init script —
  the encapsulation flag changes, nothing else does — then asserts a non-empty
  SVG with a real layout box and the labels a reader would read.
- **Coverage is declared, so it cannot rot.** Each covered page lists its
  diagrams and their expected labels, and the count is checked against the built
  HTML. Adding a diagram to a covered page fails the gate until it is declared.

The site's diagrams as a whole are still covered by `make mermaid-check`, which
parses every fence in the repository. That is the syntax gate; this is the
behaviour gate, on the pages worth driving a browser through: a core page, a page
the integration hook injects, and an instant navigation between two of them.

## Choosing a home for a new test

Ask, in order:

1. **Is it a rule about the repository rather than the product?** ("core must
   stay Kubernetes-free", "generated artifacts are current") → level 3,
   `tests/architecture/`.
2. **Is it about the release system?** → level 8, `tests/release/`.
3. **Can one package prove it?** → level 1, beside the code. Prefer this. The
   100% coverage gate applies here.
4. **Does it need several real components, but nothing a user could reach over a
   network?** → level 2, `tests/integration/` (engine) or
   `integrations/kubernetes/test/` (operator, envtest).
5. **Is it a whole user story that needs no cluster?** → level 4,
   `tests/acceptance/local/`.
6. **Does it need a real Kubernetes cluster?** → level 5,
   `tests/acceptance/kind/`, as one of the existing scenarios or a new one.
7. **Is it browser-visible?** → level 6 if fixed data can prove it, level 7 only
   if it genuinely needs live cluster data. At level 6, pick the suite by the
   artifact under test: the dashboard bundle or the built documentation site.

Two more rules once you have picked:

- **Each Kind scenario is one boundary.** They are not merged: a merged cluster
  run cannot say which boundary broke, and cannot be sharded across CI. If your
  test is a new boundary, it is a new scenario with a new `make` target.
- **A new semantic assertion goes in Go.** If you find yourself reaching for
  `jq`, `grep` or an embedded interpreter inside a harness, the assertion belongs
  in that scenario's Go gate — where it can have a test of its own.
