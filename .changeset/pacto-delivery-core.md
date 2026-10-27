---
"@pacto/core": minor
---

Add Pacto delivery, the opt-in part of Pacto that decides which version each
environment runs and earns autonomy one confirmed level at a time, plus
`pacto publish`, contract format 2.1 and a `pacto init` that pactifies existing
services, one folder, several or a whole cluster. Nothing changes for a project that does not
use them, with one exception: a consumer on an older Pacto or operator cannot
read a provider's contract once the provider moves to format 2.1 (see below).

Delivery is Alpha. The new packages (`pkg/delivery`, `pkg/publish`,
`pkg/signing`, `pkg/deliveryrecord` and `pkg/ocireferrer`) are experimental and
not covered by semantic versioning, so their API may change in a minor release.

**Pactify a service.** `pacto init [dir]` now reads a folder that already holds
a service instead of failing. It fills in the contract from the Dockerfile and
Kubernetes manifests: name, version and registry from the workload and its
image, the owner from `--owner` or a `team` label, the interface from an
OpenAPI file and a `publish` section that builds the image and ships the
manifests with the workload's image swapped for the one each version builds,
pinned by digest in every manifest that runs it, so nobody raises the image tag by hand. A bare image name such as `checkout`, a Docker Hub official image, leaves the registry Unknown. On a folder whose own contract has no `publish` section, init says how to add one. Services the workload's environment points at are listed as
dependencies, commented out until you set the versions you accept; `--fleet
<dir>` also lists the services that declare this one. Anything it cannot tell
is reported as Unknown, never guessed. A file holding Secret values, or a
kustomization with a `secretGenerator`, is never shipped. `--delivery` also
writes the cluster pieces: the Pacto resource and, for each environment
(`--env`, default test and prod), the Flux `OCIRepository` objects, a
Kustomization that waits for the rollout and the Stage. Every Stage starts on
`observe` and its OCIRepository stays pinned to the tag you deploy, so nothing
changes until a person raises it. The last environment's Stage asks a person
to approve every promote until you add a health check there and delete its
`approval`; `--insecure-registry`
marks those Flux objects for a plain-HTTP registry, `--registry-secret <name>`
gives them and the Pacto resource the login for a private registry
(`secretRef` and `pullSecretRef`) and `--no-transparency-log`
writes `transparencyLog: false` for a key whose signatures stay out of Rekor.
`--plan` prints exactly what would be added and writes nothing. `pacto init` only adds files and
missing `.pactoignore` lines, so running it twice is safe and it never
overwrites your edits. An empty folder still gets the blank contract. Agents get
the same through `pacto_create` with `pactify: true` (plus `delivery`,
`registry`, `environments`, `fleet_roots`, `no_transparency_log`,
`insecure_registry`, `registry_secret` and `dry_run`).

**Pactify many services.** `pacto init` takes several folders and ends with a
summary table. `pacto init --cluster [--namespace <ns>]... --out <dir>` drafts
a contract for every workload in the current kubeconfig context that no Pacto
describes, in `<dir>/<namespace>/<name>`. With the node agent running, the
services a workload was seen calling become dependencies pinned to the version
seen running (`=2.3.0`) for you to widen. Drafts are never published, and an
invalid one fails the command. `pacto_create` takes `paths`, `cluster` and
`namespaces` for the same.

**Contract format 2.1.** `pactoVersion: "2.1"` adds a `publish` section (the
contract bundle repository, the image, chart or manifests the service
publishes, how each is built and signed and the deploy artifact Flux applies),
a `checks` list of test and eval suites shipped in the bundle (Pacto only
hashes each one and takes a runner's verdict about that exact file), an `mcp`
interface type for the tools a service exposes and `external: true` on a
dependency this Pacto system does not deliver. An external dependency either
points at a contract and is compared by its range as usual, or has no `ref`
and names a free-text `version` that nothing can check, so anything relying on
it is Unknown. An AI agent is a normal service: tools as an `mcp` interface,
its model as an external dependency, its evals as a check. `pacto diff`
compares tools one by one and treats a new external version, and a suite
removed, moved or edited, as needing at least a minor. A 2.0 contract keeps
working unchanged; `publish`, `checks`, an external dependency or an `mcp`
interface in a 2.0 contract fails validation with
`NEEDS_PACTO_2_1`, which says to set `pactoVersion: "2.1"`. Older Pacto
versions reject a 2.1 contract, and that reaches consumers too: once a provider
publishes a 2.1 bundle, a consumer whose Pacto or operator predates 2.1 fails to
resolve it as a dependency, though the consumer changed nothing. Upgrade the
consumers' Pacto and operator before a provider moves to 2.1.

**`pacto publish`** publishes what the contract declares as the version it
states. `--plan` shows, for every artifact, whether its tag is absent,
identical, adoptable, in conflict or unreadable and which checks refuse the
publish. A publish builds what is absent (`docker buildx` or a command writing
to `$PACTO_OUT`), pushes by digest with the version and the git commit (read
from `.git`, or `--revision`; it refuses a checkout with uncommitted changes
unless `--revision` names the revision), attaches the SBOM file the
build wrote (Pacto never makes one), signs every digest and checks each new
signature against the configured identity before any tag is written. It never
overwrites a tag, so a second run changes nothing and a run that stopped
halfway finishes when run again from the same commit; a conflict says to publish
a higher `service.version`. The plan also opens the signer and checks for a
`docker buildx` builder and each build program, so a missing key or builder
refuses it before anything is pushed. `pacto publish [dir]` takes the
directory as `init` and `validate` do. It refuses a version too small for the
change: a breaking change needs a major (a minor before 1.0) and a change Pacto
cannot classify at least a minor. A refused publish is kept as a signed refusal
in the bundle repository. `pacto diff` reports the same `requiredBump` and
`pacto validate` refuses an `sbom` on a `dockerfile` build. `--propose
<namespace>/<stage>` hands the version to delivery. `pacto mcp --publish` adds
`pacto_publish_plan` and `pacto_publish` over stdio.

**Signing.** Pacto signs and verifies with sigstore-go and needs no `cosign`
binary; signatures are Sigstore bundle referrers that `cosign verify` and Flux's
`spec.verify` check. It signs with a key file (`COSIGN_PASSWORD` decrypts a
cosign key), keyless through public-good Fulcio and Rekor, or a cloud KMS key.
The KMS providers are optional: the default build leaves the AWS, GCP, Azure
and Vault SDKs out (the linux/amd64 binary drops from 75.3 MB to 64.3 MB), and
a KMS key in that build fails with an error naming the build to use (`go
install -tags kms ...`, or `kms_aws`, `kms_gcp`, `kms_azure`,
`kms_hashivault`).

**Delivery.** `pkg/delivery` holds the operations (`delivery.pacto.trianalab.io/promote`,
`rollback`, `override`, `freeze`, `unfreeze`, `autonomy` and `acknowledge`) and `PlanFor`, the one planning function
the promoter, the CLI, the MCP tools and the dashboard call. A promote is
refused as `NotPublished` unless `pacto publish` published it and Flux verified
its signature, or its upstream Stage verified and offered it. It then compares
the running contract with the new one and checks the new version against every
consumer's declared range. A consumer the change breaks holds it, asks only
that consumer's owners and says which service must move first and to which
verified version. Compatibility comes from the declared ranges, not from which
fields or endpoints a consumer uses. Anything Pacto cannot see holds the change
for a person to accept the unknown, a breaking change no contract depends on
included; Unknown never passes. Health checks keep
running after release: a `mira` provider judges the error ratio of the
service's traces, `http` is the plug-in format for any other tool, `eval`
runs one of the `checks` the version ships (the Stage check's `suite` names it,
and a version that does not ship it is held for a person), a verdict counting
only about that suite's sha256, and `pacto.runtime` fails a version the node agent
saw calling a service it never declared and passes only after a soak. A
workload the node agent saw calling a service with no contract of its own
counts as an unknown consumer: a breaking change waits for a person to accept
it or for that caller to get a contract, and the question prints the
`pacto init --cluster` command that drafts one; while the agent's view is
partial (for example a pod on the host network), every breaking change waits
the same way. The fleet snapshot lists these callers (`uncontractedCallers`).
A confirmed failure rolls
back once, then freezes the Stage.

Every operation that ends becomes a signed record: an in-toto statement in a
Sigstore bundle, chained per Stage and verifiable with standard Sigstore tools
without Pacto, against a trust config that says which keys may sign which
streams. `pkg/signing` and `pkg/ocireferrer` are the shared signing and
referrer-write paths; the Evidence Server uses the latter.

`pacto delivery types`, `describe`, `plan`, `propose`, `get`, `cancel`,
`approve`, `explain`, `history`, `blame`, `undo` and `record verify` drive and
read delivery over the ambient cluster, except `approve`: it logs the person
in again in the browser and records the decision with that login alone, and
only a decision the promoter would count, or it says why not. `plan` gathers
its input as the promoter does, so it predicts what the promoter will do. `history` and `record verify` read the
signed records, say `asOf`, `source` and `completeness` and list forks, gaps
and anything unreadable; `--all` walks every Stage you can list. `pacto mcp
--delivery` serves the read tools to anyone and propose and cancel only to
agents; there is no approve tool. Over HTTP, `pacto mcp` verifies a bearer
token on every request (`--auth-issuer`, `--resource`, `--audience`) and
exchanges it for one the apiserver accepts, so each caller acts as itself.

**The autonomy ladder.** Each Stage is on one level: `observe` plans every
new version and moves nothing, recording what it would have done; `advise`
ships only what a person asked for or approved; `auto-nonbreaking` promotes
non-breaking changes on its own; `autonomous` promotes any change the graph
clears. An `autonomy` operation moves a Stage one level: lowering needs nobody
(an agent never lowers to observe), raising needs a person of the running
service's team other than whoever asked, leaving observe needs the applyRef
labelled and an observed digest, and an automatic level needs the recorder, so
every grant of autonomy is a signed record. Pacto proposes the next level once
a Stage earned it (10 verified changes by default) and lists what blocks it;
a raise it has not proposed asks the approver to accept the unknown. When a
version Pacto shipped on its own fails and rolls back, the promoter lowers the
Stage one level; an Unknown never does. `pacto delivery propose --type
autonomy --to <level>` asks for a move.

**Dashboard.** `pacto dashboard` signs people in with OpenID Connect
(`--oidc-*`, secret in `PACTO_DASHBOARD_OIDC_CLIENT_SECRET`) and gains a
Delivery tab: an inbox of what waits on your teams, an environments board, a
page per change with its plan, verdicts and proof, history with blame and undo,
published versions with recent refused publishes and a path to autonomy: each
Stage's level, what Pacto would have done on observe, the next proposal and
what blocks it, with the command that fixes each blocker. Promote, roll back,
unfreeze, cancel and decisions run there under the person's own token. A
decision needs a recent login and membership of a team the question names, and
is never taken from an agent; the dashboard offers and records only a decision
the promoter would count, and says up front when a cluster reports no login
time, so nobody can approve there. `--auth-required` puts the whole dashboard
behind sign-in. The dashboard also takes a new look: serif titles and numbers,
a grouped data-sources menu and labelled status bars.

**Demos.** The browser demo plays the payments story from `examples/story` on
Pacto's own code over fixed data and labels it simulated; the story now starts
fraud on observe and adds the advise raise and the automatic step-down; its Wasm is about
4 MB to download. The Compose demo has no cluster or sign-in, so it shows no
Delivery tab; it now shows `pacto diff` and `pacto init --plan`.

**Behaviour change: `pacto push` fails on a conflicting reference.** Before,
pushing to a reference that already held a bundle printed a warning, wrote
nothing and exited 0, whatever the bundle held. Now different content under an
existing reference exits non-zero, so a script that relied on the old exit
status fails; pass `--force` to overwrite the reference instead. The same
content pushed again still exits 0 and writes nothing.

**New direct dependencies:** `github.com/coreos/go-oidc/v3` (dashboard
sign-in), `github.com/sigstore/sigstore-go`, `github.com/sigstore/sigstore`,
`github.com/sigstore/protobuf-specs`, `github.com/theupdateframework/go-tuf/v2`,
`github.com/go-jose/go-jose/v4` and `github.com/digitorus/timestamp` (signing
and verifying without cosign), and `golang.org/x/oauth2` and
`google.golang.org/protobuf` (once indirect, now direct). The four
`github.com/sigstore/sigstore/pkg/signature/kms/{aws,azure,gcp,hashivault}`
modules pull in the AWS, Azure, Google Cloud and Vault SDKs; they are linked
only into builds with `-tags kms` (or `kms_aws` and the like), which the
pacto-delivery image uses and the default pacto CLI does not. Together they
add about 100 indirect modules.
