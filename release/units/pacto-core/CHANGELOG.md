# @pacto/core

## 4.3.0

### Minor Changes

- 039d160: Contracts can declare behavioral guarantees (`pactoVersion: "2.2"`). An
  interface lists what consumers may rely on in `guarantees`: `delivery`,
  `ordering`, `dedupeWindow` and `retention` for events, `idempotent` and
  `timeout` for calls. A dependency lists what it relies on in `requires`.
  A `"2.0"` or `"2.1"` contract that uses either reports `NEEDS_PACTO_2_2`.
  `pacto validate` reads the provider contract and reports every unmet guarantee
  as `GUARANTEE_UNMET` in its `guarantees` stage, or warns with `GUARANTEE_UNVERIFIED` when it cannot read
  it. The stage passes only when every requirement was checked, and is skipped
  with a reason naming the dependency otherwise. `pacto graph` marks refused edges, `pacto diff` classifies a removed or
  weakened guarantee as breaking and `pacto impact` names the consumers whose
  requirement a change breaks, in its text and JSON output.
- 039d160: Contracts can declare an interface's `compatibilityEpoch`
  (`pactoVersion: "2.2"`), an explicit marker for a breaking change no spec shows, such as an amount switching from
  cents to euros.
  
  - Raise `interfaces[].compatibilityEpoch` and `pacto diff` reports
    `interfaces.compatibilityEpoch` as `BREAKING`, naming the interface, so
    `pacto impact` lists its consumers. Lowering it is `BREAKING` too: consumers
    that adapted to the higher epoch break in turn.
  - A `"2.0"` or `"2.1"` contract that sets it reports `NEEDS_PACTO_2_2`.
- 039d160: Add `pactoVersion: "2.2"` and an interface lifecycle, so a provider can retire
  an interface on a schedule its consumers can see. Every 2.1 contract is valid
  under 2.2, which also carries an interface's `compatibilityEpoch` and
  `guarantees` and a dependency's `requires`. A 2.2 contract needs Pacto core
  4.3.0 or later and operator 6.3.0 or later. A `"2.0"` or `"2.1"` contract that
  uses a 2.2 field reports `NEEDS_PACTO_2_2` for each.
  
  ```yaml
  pactoVersion: "2.2"
  interfaces:
    - name: orders-v1
      type: openapi
      ref: interfaces/orders-v1.yaml
      lifecycle:
        deprecated: "2026-10-01"
        sunset: "2027-04-01"
        successor: orders-v2
  ```
  
  - `pacto validate` checks that both dates are `YYYY-MM-DD`, that the sunset is
    not before the deprecation (`INVALID_INTERFACE_LIFECYCLE`) and that the
    successor names another interface or is an absolute URL
    (`INTERFACE_SUCCESSOR_UNKNOWN`). A 2.0 or 2.1 contract with a lifecycle
    reports `NEEDS_PACTO_2_2`.
  - `pacto validate --closure` warns a consumer about each deprecated interface
    of a service it depends on directly, naming the interface, its sunset and its
    successor (`DEPENDENCY_INTERFACE_DEPRECATED`). From the sunset date the code
    is `DEPENDENCY_INTERFACE_SUNSET`.
  - `pacto diff` reports deprecating an interface as non-breaking and moving its
    sunset earlier as potentially breaking. Removing an interface before its
    sunset stays breaking, and the reason says so.
  - `pacto explain`, the MCP contract summary and the dashboard API show an
    interface's lifecycle.
- 039d160: `pacto diff` judges every interface schema change by the position the schema
  sits in, and resolves `$ref` first, so a schema defined under OpenAPI or
  AsyncAPI `components` is judged where an operation or a message uses it.
  
  **`pacto diff` and `pacto impact` report more changes as `BREAKING`.** A CI
  job that gates on `BREAKING` can fail on a change it passed before. Each of
  these was `POTENTIAL_BREAKING` or went unreported:
  
  - In a response or an MCP `outputSchema`, widening breaks: a new `enum` value
    or `oneOf` variant, a looser limit, a dropped `pattern`, a wider `type`.
  - In a request body, a parameter or an MCP `inputSchema`, narrowing breaks: a
    dropped `enum` value, a tighter limit, a new `pattern`.
  - An AsyncAPI message's `payload` and `headers` schemas are records that old
    and new code read and write at once, so widening and narrowing both break
    there. Only a new optional property passes. Removing a payload property is
    `BREAKING`, as it already was from a response. A flipped operation
    `action`, a 2.x `publish`/`subscribe` flip and an operation linked to a
    different channel are `BREAKING`. Other edits to a channel or an operation,
    such as a binding, stay `POTENTIAL_BREAKING`.
  - Inside an interface schema, an edit the rules cannot rank, such as a new
    `default` or anything under `not`, is `BREAKING`.
  - A change to a component, or to a path item, reached through `$ref` was
    never reported. It is
    now judged in every position that uses it, and one change carries the
    worst grade, with a reason naming the component and its usages.
  - A top-level proto `enum` is compared for the first time: a removed enum and a removed
    or renumbered value are `BREAKING`. An added value is `POTENTIAL_BREAKING`,
    because the scan cannot tell whether the enum travels in a request or a
    response. An enum nested inside a message is still skipped.
  
  Some changes grade lower than before. A `$ref` that points at a different
  component is graded by the structural difference, so renaming a component
  with identical content is no change. A remote ref, or one whose target is
  missing, is `POTENTIAL_BREAKING` with a reason saying Pacto could not
  resolve it, and so is a ref whose target goes away while its text stays the
  same. A component nothing references is reported as `NON_BREAKING`. An
  `x-` extension keyword inside an interface schema is `POTENTIAL_BREAKING` in
  every position: tools read it, and Pacto cannot tell whether a change to one
  reaches a reader of the data. The safe direction of each edit above is
  `NON_BREAKING`, and so is a new optional property in a request or a
  response. Documentation edits (`description`, `title`, `summary`, `example`,
  `examples`, `$comment`, `externalDocs`, `deprecated`, `tags`) are `NON_BREAKING`
  everywhere, configuration and policy schemas included.
- 039d160: `pacto validate` reports one PASS, FAIL or SKIP line per stage (load, lock,
  structural, cross-field, policy, guarantees, readiness and closure), lists each error
  under its stage with its file:line:column when it can be located (never
  with `--set` or `-f`, which validate a merged document), and ends
  with a `Next:` line naming the one command to run now. A failed
  load or lock stage skips every stage after it, and a failed structural or
  cross-field stage skips the stages after it up to policy. The guarantees,
  readiness and closure stages run even when an earlier stage fails. The JSON output adds `stages`, `next`, `file` and each error's
  `line` and `column`, and `pacto_check` returns the same `stages`, `next` and
  locations. An unknown field or a mistyped enum value carries a `did you mean`
  hint naming the closest valid one.
  
  The text output of `pacto validate` now goes to stdout as one stream: the
  error and warning lines moved there from stderr. The one-line failure summary
  stays on stderr, and exit codes are unchanged.

## 4.2.0

### Minor Changes

- 9d4ec34: Make Pacto easier for an agent to drive from the CLI help and the MCP tool
  text alone.
  
  **`pacto impact` has a new exit status.** It now exits `2` when a breaking
  change cannot be shown to be safe. A CI job that treated any non-zero exit as
  a failure fails on these runs too; one that gates on deployed impact alone can
  treat `2` as a pass.
  
  `pacto impact` and the fleet tools no longer give answers that read as safe
  when they are not.
  
  - `pacto impact` adds the new revision to the snapshot when no fleet source
    holds the changed service, so `--local ./fleet` with only the consumers
    finds them instead of answering "No affected consumers".
  - Every impact answer ends in a release gate, `gate` and `gateReason`, and the
    exit status follows it: `0` clear, `1` blocked (a breaking change reaches an
    incompatible consumer on a known target) and the new `2` unverified (a
    breaking change on a partial answer, or with consumers none of which is shown
    to be safe). A declared-only run of a breaking change with consumers now
    exits `2` instead of `0`. The `pacto_impact` MCP tool, the dashboard's Change analysis
    view and the terminal UI show the same gate and its reason.
  - On a breaking change, a consumer whose range accepts the new version carries
    a `note`: the range match does not show that it avoids what the change
    removes. An open range such as `>=1.0.0` is named as open.
  - A declared dependency carries `rangeStatus` (`satisfied`, `unsatisfied` or
    `unknown`) beside `resolved`. A provider in the fleet at a version outside the
    range is reported by `pacto fleet status`, `pacto_fleet_status`, `explain`
    and the dashboard's attention list as `DEPENDENCY_RANGE_UNSATISFIED`, in the
    new `out-of-range` category (`--unsatisfied-ranges`, `unsatisfied_ranges`).
  - `pacto_fleet_status` with no category selected, including empty input,
    reports every category, as `pacto fleet status` does. It returned
    `items: null` before.
  - `owner` has one shape in `search` and `get`, the contract's
    `{team, dri, contacts}`, and is left out when no contract declares one. A
    service read only from Kubernetes takes its owner from the operator's
    `status.contract.owner`, where it was empty before.
  
  Delivery plans are readable to an agent from the tools alone. The CLI and the
  MCP delivery tools now say the same thing. The operator's promoter shares this
  code, so its messages and the Rejected-plan behaviour change with it.
  
  - `pacto delivery candidates` and the `pacto_delivery_candidates` tool list the
    versions a Stage may take, with their digests: what its candidate source
    offers that `pacto publish` published. The `to` of plan and propose takes a
    version (`3.0.0`), a tag or a digest. A version or a tag must name exactly one
    candidate; Pacto refuses one that names none or several, or that it cannot
    match because a candidate cannot be read, and says why. A missing `to` says
    where to find a valid value.
  - A plan carries `next`: which teams must sign (and that their approval also
    meets a Stage's rule that a person approves every promote), that an agent
    cannot answer and the operation is then Held, and that an operation nobody
    decides before its approval deadline concludes Expired with nothing changed.
    `pacto delivery get`, `pacto_delivery_get` and `pacto delivery explain` say
    what happens at the deadline of a held operation.
  - A plan predicted Rejected asks nobody anything, and its failing verdict says
    what to do first, such as promoting the version on the upstream Stage.
  - The message for a consumer that must move first names the Stage to promote it
    on in plain words.
  - `PlanStale` names the reason or the on-behalf-of person when a propose adds
    one the plan did not have, and otherwise lists what the intent digest binds
    instead of saying the Stage moved.
  
  `pacto init` and `pacto_create` with `pactify` read more of a service into its
  contract. The workload's environment, including the ConfigMaps it reads,
  becomes a configuration schema with each value as its default. A URL in it
  that names another service in the cluster becomes a dependency: with that
  service's contract in `--fleet`, its bundle and a range from its version;
  without it, a placeholder range that the contract comment and Unknown name. A
  readiness, liveness or startup probe becomes a health check.
  
  `pacto init --describe-only` (`describe_only` in `pacto_create`) writes a
  contract that only describes the service: no publish section, no signing key
  and no `.pactoignore`. Without it, `init` still writes the publish section and
  now says how to leave it out.
  
  `pacto init`, `pacto_create` and `pacto_edit` flag an owner written as
  `team/billing`, and `pacto_check` suggests the `pacto_edit` call that writes
  `billing`. `pacto_check` also explains how to declare a service of
  your own whose contract is not published yet, so it shows as unresolved until it
  is. The contract schema and the `DEPENDENCY_REF_REQUIRED` message say the same.
  
  The MCP server instructions, `pacto_schema` and `pacto --help` now describe
  what a contract holds and say delivery is opt-in, and `pacto_schema` returns
  how to describe an AI agent as a service.
- 67de328: `pacto validate --closure` checks every contract in the dependency closure
  against the policies the root contract resolves. A dependency that cannot be
  resolved fails the run, and each finding names the dependency it came from.

### Patch Changes

- 67de328: The dashboard delivery tab that lists Stages and its page are now named
  **Stages**, as the glossary calls them. The URL `#/delivery/environments` and
  the API path stay the same, so existing links keep working.
- 67de328: Redeploy pacto.run/latest with the rewritten documentation: the front door,
  the comparison page, the delivery security model and the guide to generate
  contracts from what you already have. This changeset carries documentation
  only. This core patch is the release that redeploys pacto.run/latest.
- 67de328: `pacto init` skips the files `.pactoignore` lists, and it no longer takes a Backstage `catalog-info.yaml` or a `skaffold.yaml` for a Kubernetes manifest, so neither lands in the `manifests` artifact Flux applies.
- 67de328: `pacto init` no longer reads a Helm chart's templates as Kubernetes manifests, so unrendered templates stay out of the `manifests` artifact and never become the contract's workload. A chart's `crds/` folder is skipped too, whether the chart sits in a subfolder or at the service root. Unknown names every chart found and says how to render it.

## 4.1.0

### Minor Changes

- 407c06e: A dashboard people sign in to now reads delivery Stages and Operations from one
  shared cache, which its ServiceAccount lists and watches, instead of listing
  them again with every person's token. Each person sees only what their own
  RBAC allows: the dashboard asks the API server who their token is and, with a
  SubjectAccessReview, whether they may make the same list or get, keeping each
  answer 10 seconds; a list across every namespace needs a cluster-wide allow.
  Every action (propose, approve, cancel, roll back, unfreeze) and every read it
  is built on still uses the person's own token, and the dashboard reads with
  that token until the cache has loaded and for a moment after the person
  changes something. With 2,000 Stages and 10,000 Operations, fifty people
  opening the inbox at once wait about a second instead of 12. The operator
  grants the dashboard's ServiceAccount get, list and watch on Stages and
  Operations, nothing more in the delivery group, and holds that right itself
  while the dashboard is enabled.
- 407c06e: A shared dashboard can sign people in, and then shows nothing until they do.
  `pacto dashboard` takes an OpenID Connect issuer (`--oidc-*`, the client secret
  in `PACTO_DASHBOARD_OIDC_CLIENT_SECRET`), a private issuer CA
  (`--oidc-ca-file`), an internal issuer address (`--oidc-internal-issuer-url`),
  the API server's claim mapping, session lifetimes (`--session-idle-timeout`,
  `--session-max-age`) and `--token-login`, which takes a pasted Kubernetes token
  for clusters that trust no identity provider. The API server names each
  signed-in person (SelfSubjectReview), and every view and action runs with
  their token and RBAC. `/api/session` says who is signed in and how; every
  other endpoint answers `401` with the code `LoginRequired` until they sign in.
  The header names who they are, and the sign-in button says Sign in with OIDC
  or names the provider (`--oidc-display-name`). A remote `pacto mcp` reads the
  same claim mapping flags and logs each caller as the mapping names them.
  
  The `pacto-operator` chart gains `dashboard.auth`: `oidc` (`issuerURL`,
  `internalIssuerURL`, `caBundle`, `clientID`, `clientSecretRef`, `redirectURL`,
  `extraScopes`, `displayName` and the claim mapping `usernameClaim`,
  `usernamePrefix`, `groupsClaim`, `groupsPrefix`), `session` (`idleTimeout`,
  `maxAge`) and `tokenLogin.enabled`. See
  [Dashboard authentication](https://pacto.run/latest/integrations/kubernetes/dashboard-authentication/).
- 407c06e: A dashboard people sign in to now shows each person only what Kubernetes RBAC
  lets them read. A namespace's services, revisions, targets, edges and evidence
  appear only if the person may list Pactos there, checked with a
  SubjectAccessReview for their username and groups, as the API server names
  them, and kept a minute. One filter covers every fleet view, counts, search, the graph, change
  analysis and a remote `pacto mcp --fleet` with `--auth-issuer`. The dashboard
  says when services are hidden without naming them, and a service page counts
  the hidden services on each side. A service known only from a registry, with no
  namespace, shows only to people who may list Pactos in every namespace. Local
  and sign-in-free dashboards are unchanged.
  The operator grants the dashboard's ServiceAccount create on
  `subjectaccessreviews` and holds that right itself while the dashboard is enabled.
- 407c06e: Add Pacto delivery, the opt-in part of Pacto that decides which version each
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
  is reported as Unknown. A file holding Secret values, or a
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
  build wrote (Pacto does not generate SBOMs), signs every digest and checks each new
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
  verified version. Compatibility comes from each consumer's declared version
  range; Pacto does not check which fields or endpoints a consumer uses. Anything Pacto cannot see holds the change
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
  read delivery over the ambient cluster. `approve` records only a decision the
  promoter would count, or it says why not; where the Stage asks for a recent
  sign-in, it logs the person in again in the browser and records the decision
  with that login alone, trusting a private issuer CA with `--oidc-ca-file`. `plan` gathers
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
  
  **Dashboard.** `pacto dashboard` gains a Delivery tab for signed-in people
  (`--delivery-namespace`, `--delivery-trust`, `--mira-url`): an inbox of what waits on your teams, an environments board, a
  page per change with its plan, verdicts and proof, history with blame and undo,
  published versions with recent refused publishes and a path to autonomy: each
  Stage's level, what Pacto would have done on observe, the next proposal and
  what blocks it, with the command that fixes each blocker. Promote, roll back,
  unfreeze, cancel and decisions run there under the person's own token. A
  decision needs membership of a team the question names and is never taken
  from an agent or a pasted token; the dashboard offers and records only a
  decision the promoter would count. Where the Stage asks for a recent sign-in,
  the dashboard says when a person's sign-in is too old, asks in a dialog to
  sign in again and keeps their choice and note for one more click, and says up
  front when a cluster reports no login time, so nobody can approve there.
  The dashboard also takes a new look: serif titles and numbers,
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

### Patch Changes

- 9d96a75: Update the dashboard frontend's dependencies. The bundle the `pacto` binary and
  the dashboard image serve moves to Mermaid 12.2, Cytoscape 3.34.4,
  cytoscape-dagre 4.0.1, Marked 18.1 and Svelte 5.57. Mermaid 12 lays diagrams out
  with ELK and draws them in its new `neo` look by default, so bundle docs keep the
  dagre layout and classic look they had before.
  
  The dashboard's accessible names now begin with the visible text of the control
  (the navbar search, the graph's Reset layout button and the change count in
  delivery history), as WCAG 2.5.3 asks and axe-core 4.14 checks.
  
  Build and test tooling moves to Playwright 1.64, axe-core 4.14, jsdom 30,
  svelte-check 4.7, the Svelte Vite plugin 7.3 and mermaid-cli 12. TypeScript stays
  on 5.9: svelte-check and openapi-typescript do not support TypeScript 7 yet.
- 9f5f638: Update `github.com/go-jose/go-jose/v4` to v4.1.6 to fix CVE-2026-116587, a
  denial of service when parsing JWKs that contain RSA keys. It reaches the
  `pacto` binary and the operator through OIDC sign-in and Sigstore signing.
  
  `golang.org/x/net` moves to v0.62.0 (GO-2026-6860, GO-2026-6866) and the
  operator's `github.com/google/cel-go` to v0.30.0 (GO-2026-6094). govulncheck
  finds none of the three reachable, so this is hygiene rather than an exposure fix.
  
  The dashboard frontend's dependencies are updated too, which fixes the known
  advisories in the bundle the `pacto` binary and the dashboard image serve:
  DOMPurify 3.4.17, KaTeX 0.18 (used by Mermaid diagrams), devalue 5.9.4 and
  source-map-js 1.2.2. The build and test tooling moves to Vite 8.3 and Vitest
  4.1.12.

## 4.0.0

### Major Changes

- 9b84eda: Fixes from a whole-repo architecture review, and the breaking cleanups of the
  v4 major: camelCase JSON everywhere, the deprecated v3 API removed and a few
  APIs reshaped. Every breaking change is listed under **Breaking changes** with
  its migration.
  
  **Behaviour changes**
  
  - An `extension` capability no longer keeps a contract at Unknown forever.
    Pacto has no evaluator for one, so `validation.Evaluate` now lists it in
    `Coverage.Unverified` as `capabilities[ref=<ref>]` instead of counting it as a
    required assertion and emitting `EXTENSION_EVALUATOR_UNAVAILABLE`. A contract
    declaring an extension can reach Compliant; that verdict says nothing about the
    extension. The code is removed (see below).
  - An evidence observation with outcome `Observed` must carry every
    verdict-bearing payload key for its kind (`present`, `type`, `reachable` or
    `durable`, plus `conformant` for a present configuration). A missing or `null`
    key used to decode to `false` and read as a confirmed violation; it is now
    rejected. The payloads Pacto's own producers emit always carry these keys.
  - `PACTO_NO_ANIM` and `PACTO_NO_UPDATE_CHECK` share one parser: Go's boolean
    spellings, with any other non-empty value read as set. `PACTO_NO_ANIM=0` or
    `false` now leaves animations on, and `PACTO_NO_UPDATE_CHECK=true` now disables
    the update check. `1` works as before.
  
  **Fixes**
  
  - MCP `pacto_check` runs the same validation path as `pacto validate`,
    including `pacto.lock` verification, so a drifted lockfile no longer passes.
  - MCP `pacto_edit add_config_properties` writes the properties into the first
    configuration's schema file, merging with what is there, instead of declaring
    a configuration and dropping them. Edited files are written through a
    uniquely named temporary file that keeps the target's permissions.
  - `pacto agent --docker-label-filter key=value` (repeatable) narrows `--docker`
    to containers carrying every given label, so one agent on a shared daemon can
    keep to one Compose project. The Compose demo now does.
  - Evidence ingestion keys a record's domain by the same rule the fleet sources
    use (`contract.OCIDomain`), and keeps the evidence validation detail in the
    server log. Sending evidence uses a client with a timeout and a bounded
    response read.
  - Free text in `pacto doc` tables escapes `|` and folds line breaks.
  - The CLI reference now states that `PACTO_INSECURE_REGISTRIES` applies to
    every registry access and that an unknown `--output-format` is rejected.
  
  **API additions**
  
  - `contract.OCIScheme`, `TrimOCIScheme`, `SplitOCIDigest`, `SplitOCITag`,
    `SplitOCIRef` and `OCIDomain`: one lenient set of reference helpers, now used
    everywhere Pacto split references by hand.
  - `contract.DocsDir` and `contract.WalkDocs`, `lock.ReadFS` and
    `capability.ToolsForContract`: the bundle readers the fleet, the offline view
    and MCP share.
  - `finding.Registry` returns every registry row; `fleet.Paginate` and
    `fleet.Page` page a sorted list; `fleet.AttentionCategoriesList` returns the
    attention order as a copy; `fleet.SourceKindKubernetes` names the Kubernetes
    source kind.
  
  **Breaking changes**
  
  - **The Go module path becomes `github.com/trianalab/pacto/v4`** (was
    `.../v3`). Update every import, and install the CLI with
    `go install github.com/trianalab/pacto/v4/cmd/pacto@latest`.
  - **camelCase JSON.** `finding.Finding`, `finding.SubjectRef` and
    `finding.EvidenceRef` (`code`, `severity`, `category`, `subject.kind`,
    `contractPath`, `message`, `evidenceRefs[].source` …), `readiness.Result` and
    `readiness.CheckResult` (`score`, `minScore`, `checks[].id` …),
    `contract.ValidationError`/`ValidationWarning`, `validation.ValidationResult`
    and the `--output-format json` of `pacto init`, `validate`, `pack`, `pull` and
    `push` (`dir`, `path`, `valid`, `errors`, `name`, `version`, `ref`, `digest`)
    were PascalCase on the wire. That reaches `pacto agent` and `pacto run`
    output, MCP answers, the dashboard `/api/fleet/*` answers and the
    Evidence Server's records and target projection. Migration: read the
    camelCase keys. Evidence records already stored in a registry and answers from
    an older Evidence Server still decode, because the decoder matches keys
    without regard to case. The evidence envelope (`pacto.dev/evidence/v1`) is a
    versioned protocol and does not change.
  - **`pkg/dashboard.Server` takes an options struct.** `NewServer(dashboard.Options{UI,
    Host, Port, Version, CORSOrigin, Logger, Fleet, Impact, Refresh})` and
    `Serve(ctx)` replace `NewServer(ui)`, the seven setters (`SetFleetProvider`,
    `SetImpactProvider`, `SetFleetRefresher`, `SetListenAddr`, `SetVersion`,
    `SetCORSOrigin`, `SetLogger`) and `Serve(ctx, port, host...)`. The OpenAPI
    server URL is derived from `Host` and `Port`.
  - **Fleet HTTP filters are enums.** `status` on `/api/fleet/services`,
    `/api/fleet/entities` and `/api/fleet/attention` accepts only the seven
    compliance states, and `kinds` on `/api/fleet/entities` is a comma-separated
    list of `service`, `revision`, `target`, `owner` and `source` checked item by
    item. An unknown value or an empty item (a trailing comma) is answered with 422
    naming the parameter (`query.kinds[1]`). Migration: send only listed values and
    no empty items.
  - **`/api/sources` reports Kubernetes as `kubernetes`,** the fleet source kind,
    instead of `k8s`. The dashboard's source pill follows.
  - **Product impact moved to `pkg/impact`.** `impact.Product`,
    `impact.ProductConsumer`, `impact.ConsumersPage`, `impact.TallyBucket`,
    `impact.ProductChange`, `impact.ChangesPreview`, `impact.ProductRevisions`,
    `impact.ImmutableRef` and `impact.BuildProduct` hold the route-neutral logic;
    `dashboard.ProductImpact` embeds `impact.Product` and adds hrefs. Gone from
    `pkg/dashboard`: `ProductChange`, `ProductChangesPreview`, `ProductRefsPreview`
    (owners and active targets are `ProductRefPreview`), `ImpactTallyBucket` and
    the `MaxImpact*`, `DefaultImpactConsumers` and `MaxChangeValueRunes` constants
    (now `impact.MaxProduct*`, `impact.DefaultProductConsumers` and
    `impact.MaxChangeValueRunes`). The JSON field names of `POST /api/fleet/impact`
    do not change; three OpenAPI component names do (`Impact.ChangesPreview`,
    `Impact.ProductChange`, `Impact.TallyBucket`).
  - **MCP `pacto_create` no longer infers `state` from the description.** Words
    such as postgres, db, redis, cache, persistent or "stores data" used to make the
    contract stateful; a runtime that disagrees is now a confirmed contradiction,
    so a guess would turn into a false violation. Migration: pass `stores_data`,
    `data_survives_restart` and `data_shared_across_instances` when the service
    keeps state. Without them the contract is stateless.
  - **Removed deprecated API.**
    - `pkg/finding`: `CategoryInterfaceMismatch`; `CodeExtensionEvaluatorUnavailable`
      and its registry row (a stored finding with that code no longer has a
      category); the retired `CodeInvalidInterfaceType`, `CodeInterfaceRefRequired`,
      `CodeInvalidCapabilityType`, `CodeCapabilityRefRequired` and
      `CodeUnsupportedPolicyTarget` (all reported as `CodeSchemaViolation`);
      `DefaultSeverity` (use `SeverityFor(code, true)`).
    - `pkg/fleet`: `BuildOptions.AllowPartial` (use `DisallowPartial`),
      `AttentionCategories` (use `AttentionCategoriesList()`), `ParseTargetKey`,
      `IsDigestPinnedRef` (use `ParseCanonicalOCIRef` or `ClassifyContentIdentity`),
      `FleetSnapshot.ObservedDependents` and `ObservedDependencies` (use the query
      neighborhood), `MemorySource.WithCollectFunc`, `ProductSeverityUnknown` (use
      `ProductSeverity(finding.SeverityUnknown)`), `LimitationRevisionConflict` and
      `LimitationRevisionContentMutable` (nothing emitted them).
    - `pkg/contractview`: `LookupValidation`, `ValidationCatalogEntry`,
      `NormalizeContractStatus`, the `Condition`, `EndpointStatus`,
      `ResourcesInfo`, `PortsInfo`, `ChecksSummary`, `ComplianceCounts` and
      `SectionInfo` types, the `Section*` and `VersionPolicy*` constants and the
      `ServiceDetails` fields nothing populated (`Endpoints`, `Conditions`,
      `ChecksSummary`, `Resources`, `Ports`, `RuntimeEvaluated`, `SectionMeta`,
      `VersionPolicy`, `LatestAvailable`, `UpdateAvailable`). `ComputeCompliance`
      takes only the contract status, and `ComplianceInfo` carries only `status`.
    - `pkg/dashboard`: the forwarders `ServiceFromContract`,
      `ServiceDetailsFromBundle`, `GlobalGraphFromResult`, `NormalizeContractStatus`,
      `ComputeCompliance`, `LookupValidation` and `ApplyLock` (call
      `pkg/contractview`), the inert `ComputeDiff`, `DiffResultFromEngine`,
      `GraphFromResult` and `ComputeRuntimeDiff` (call `diff.Compare` and
      `contractview.GlobalGraphFromResult`) and the aliases of the removed
      contract-view types and constants.
    - `pkg/oci`: `WithNameOptions` (use `WithInsecureRegistries`); `LocalOnly`,
      `RemoteAllowed` and the `ResolveMode` type, so `Resolver.Resolve`,
      `ResolveConstrained` and `ResolvePinned` drop their mode argument;
      `CachedStore.PullCached` and `PullCachedPinned` (use `Pull` and
      `PullPinned`); `ReadCachedRef` (use `ReadCacheEntry`); `Resolver.ListVersions`
      and `FetchAllVersions` (use `BundleStore.ListTags` with `FilterSemverTags`);
      `ExportedUserHomeDirFn` and `SetUserHomeDirFn` (use `t.Setenv("HOME", dir)`).
    - `pkg/evidenceingest`: `Source` and `NewSource` (read the evidence-source HTTP
      projection at `TargetsPath`), `MemoryStore`, `NewMemoryStore` and the
      `onAccept` argument of `NewHandler` (wrap `Store.Commit` to observe accepts).
    - `graph.RenderTree` (use `RenderTreeColored(r, graph.TreeColors{})`),
      `sbom.HasSBOM` (check `ParseFromFS` for a nil document),
      `otelobserver.Observe` (use `ParseTraces`, `DependencyEdges` and
      `EvidenceSets`), `semver.LessDesc` (use `Compare`) and
      `catalog.ContentID.Zero` (compare with `catalog.ContentID{}`).
  
  **API additions in v4**
  
  - `runtimefacts.CollectorKubernetes`, `CollectorKubernetesKernel` and
    `CollectorKernel` name the Kubernetes collectors; the operator uses them and
    `go run ./cmd/gendocs registry` publishes them.
- 9b84eda: Judge more of the contract at runtime, without turning an inability to observe
  into a violation.
  
  Evidence payloads gain facts a producer may report: `InterfaceObservation`
  carries `protocol` (`http1`, `http2`, `tls`, `other`) and `exposure`
  (`external`, `cluster`), `DependencyObservation` carries the provider's
  `version` and `undeclared` and `PersistenceObservation` carries `shared`. All
  are `omitempty`, so a producer that does not set them emits the same JSON as
  before.
  
  `validation.Evaluate` checks them as facets of an observed assertion:
  
  - `INTERFACE_PROTOCOL_MISMATCH` — `openapi` answering neither HTTP/1 nor HTTP/2,
    or `grpc` answering anything but HTTP/2.
  - `INTERFACE_VISIBILITY_MISMATCH` — an `internal` interface (the schema default,
    now applied when `visibility` is omitted) exposed outside the cluster.
  - `DEPENDENCY_VERSION_MISMATCH` — the provider's version outside
    `compatibility`; an error for a required dependency, a warning for an
    optional one.
  - `PERSISTENCE_MISMATCH` now also covers `local` scope with storage shared
    between replicas.
  - `DEPENDENCY_UNDECLARED` — a reachable peer no dependency declares; always a
    warning, because a contract does not claim its dependency list is complete.
  
  A facet with no usable fact is listed in the new `Coverage.Unverified` and never
  changes the status. Every finding's `EvidenceRef` now names the `collector` and
  `detectedAt` of the observation it came from.
  
  `pkg/validation/schema/field-coverage.yaml` inventories every schema path and
  runtime-relevant enum value with its runtime class, evidence, rule, outcomes,
  limitations and tests; a test fails when the schema and the inventory disagree.
  Every runtime-checked field also says how far each way of running runtime
  conformance verifies it (`platforms`: `kubernetes`, `agent`, `run`, each
  `verified`, `partial` or `unverified` with a reason and its tests).
  
  New `pacto agent` command (Linux): a host daemon that watches TCP listeners and
  connection outcomes with eBPF and attributes each socket to the workload whose
  cgroup created it (a Kubernetes pod, a container, a systemd unit or a
  `pacto-run-<n>` session), falling back to its network namespace when the cgroup
  names none. `--sink stdout` prints one JSON snapshot per line; `--sink
  kubernetes` writes the node's `NodeRuntimeObservation` and is what the
  operator's node agent DaemonSet runs. Attribution reads the socket's cgroup id
  in the kernel, so on cgroup v2 workloads that share the host's network (systemd
  units, host-network containers) are told apart too.
  
  `--sink local` and `--sink evidence` check workloads against their contracts on
  the host itself. Workloads come from `--bindings` (a YAML list of `match`
  (`unit`, `container`, `cgroup`, `session` or `pid`), `contract`, `host`,
  `interfaces` and `dependencies`) and, with `--docker`, from running containers
  labelled `dev.pacto.contract`, `dev.pacto.bind.<interface>` and
  `dev.pacto.dep.<dependency>`. Interfaces and dependencies are judged from the
  kernel, health and metrics capabilities and (with `--enable-probing`) the
  interface protocol by probing the bound port. `local` prints every workload's
  compliance each round; `evidence` signs each workload's evidence and sends it
  to an Evidence Server, which needs contracts pinned by digest. Exposure,
  provider version and persistence scope have no source off Kubernetes and stay
  unverified; a declared `workload`, persistent durability or required
  configuration reads `EVIDENCE_MISSING`. A workload that reaches another
  checked workload its contract does not declare reports `DEPENDENCY_UNDECLARED`
  named by that workload's contract service, with the operator's suppression
  rule. A bindings-file workload starts with its oldest process, so earlier
  connections are ignored. An agent off the host's network reports probes of
  loopback-bound workloads unsupported, with a note, instead of probing its own
  loopback.
  
  New `pacto run --contract <dir|oci ref> [--bind iface=port] [--dep name=host:port]
  -- command`: runs one command and checks its process tree against the contract
  while it runs, printing a round every `--interval` and a final one after the
  command exits, then exits with the command's exit code (`--fail-on
  noncompliant|unknown` turns a bad final verdict into exit 1). With the
  privileges for it on Linux the command starts in its own cgroup and is observed
  with eBPF (collector `pacto-run+ebpf`); otherwise it falls back to polling
  `/proc`, says so, and never reports a dependency unreachable because it cannot
  see a failed connection (collector `pacto-run`); it falls back the same way
  when the program attached but the command cannot start in the cgroup. The
  command runs in a process group of its own: on a terminal pacto run owns, the
  group takes the terminal over, otherwise SIGINT and SIGTERM are passed on to the
  whole group, so a Ctrl-C reaches it once. Elsewhere the command runs
  unobserved and every runtime assertion is unsupported. The command's stdin,
  stdout and stderr stay its own: rounds go to stderr, or with `--report <file>`
  to a file, one whole round per write. `--evidence-*` signs and sends every
  round to an Evidence Server.
  
  New public packages: `pkg/runtimefacts`, the platform-neutral runtime snapshot,
  workload bindings and the verdicts that turn them into observations (the
  operator now uses them too), and `pkg/probe`, the SSRF-safe HTTP health,
  metrics and protocol probes, moved out of the operator.

## 3.3.6

### Patch Changes

- 79de1ba: This week's dependency bumps in one go: `bubbletea` 2.0.10 → 2.1.0 and
  `x/sync` 0.23.0 → 0.24.0 in the engine, OpenTelemetry 1.46.0 → 1.47.0 (and the
  Prometheus exporter 0.68.0 → 0.69.0) in the operator module. No source changes.
  
  Go goes 1.26.6 → 1.26.9 and `x/net` 0.59.0 → 0.60.0 in both modules, because
  govulncheck flagged reachable `net/http` and `x/net/http2` vulns
  (GO-2026-6607 to GO-2026-6617) and wouldn't let any of the bot's PRs through.
  Both shipped binaries were exposed, so it's worth the patch release.

## 3.3.5

### Patch Changes

- 1ee5a37: Retitle the homepage section that compared Pacto to a service catalog, and fix four dead links.
  
  "What a service catalog does not do" headed a section on both the README and
  the homepage without anything setting the comparison up — neither page mentions
  a catalog before it — and the section then did not describe catalogs. What
  follows it is a worked `pacto impact` run answering which consumers a major
  version bump breaks, so the heading now says that. The comparison keeps the two
  places that have the context for it: the "What Pacto is not" list in the model,
  and the comparison table in the README.
  
  Four links were dead. The operator chart's README pointed at an
  `api-reference.md` that exists nowhere in the repo, which mattered because that
  README is the package description on Artifact Hub; it now points at the CRD
  reference page. Two links named a `pacto-dashboard` repository that does not
  exist. The Cosign overview and the Kubernetes resource-quantity pages had both
  moved. Nothing checked any of them: `docs_lint.check_links` walks root-level
  Markdown only, and mkdocs validates only what it builds.

## 3.3.4

### Patch Changes

- ae1c923: Rebuild the documentation around what a reader is trying to do, and cut it in half.
  
  The site used to be organised by subsystem, so finding out what Pacto does meant
  reading about how it is built. Twelve top-level navigation sections are now
  eight: the homepage, the changelog and the six a reader moves through in order —
  **Try it**, **Get started**, **Guides**, **How Pacto decides**, **Reference**,
  **Integrations**. Concepts and
  the operational graph merged into How Pacto decides, patterns moved under
  Guides, examples split across the two entry sections and Project internals left
  the site altogether.
  
  Someone who lands on the homepage can open the live dashboard demo without
  installing anything, and the quickstart that follows reaches a validated
  contract, a breaking change and the list of affected consumers entirely on a
  local filesystem. Publishing to a registry moved to the end of that page as an
  optional step, because it was the first thing standing between a new reader and
  a result. Committing the contract to a repository is now its own short page, CI
  integration, rather than a section buried in installation.
  
  **Twenty-eight pages are gone.** Most of what they said lives on the page that
  already owned the subject; nothing was silently dropped:
  
  - Seven concept and graph pages — concepts, concept boundaries, collectors,
    observation sources, fleet queries, fleet sources and impact surfaces — are
    now sections of [the model](https://pacto.run/latest/model/), [the
    operational graph](https://pacto.run/latest/operational-graph/), [impact
    analysis](https://pacto.run/latest/impact/) and [fleet
    tools](https://pacto.run/latest/fleet-tools/).
  - Six pattern pages are one page, [composition
    patterns](https://pacto.run/latest/patterns/), and the developer and
    platform-engineer guides no longer restate those patterns.
  - Three evidence pages — the OCI storage layout, the protocol and the security
    model — are one page, [evidence](https://pacto.run/latest/evidence/).
  - Three MCP pages — agent capabilities, authoring tools and catalog discovery —
    are folded into [AI assistants
    (MCP)](https://pacto.run/latest/mcp-integration/), and the per-tool argument
    tables moved to the [CLI
    reference](https://pacto.run/latest/cli-reference/) alongside the commands.
  - The separate agent walkthrough is folded back into the [guided
    tour](https://pacto.run/latest/examples/demo-tour/), which now covers both
    ways through the demo in one pass.
  - The architecture, tooling-architecture, release and testing pages were never
    for site readers. They are contributor documentation and now sit in
    `ARCHITECTURE.md`, `CONTRIBUTING.md` and `release/README.md` in the
    repository, next to the code they describe.
  - The dashboard-architecture and compliance-scenario pages are **removed**, not
    moved. `ARCHITECTURE.md` carries one short dashboard section, not the
    fourteen the old page had, and the scenario-to-proof map is gone.
  
  The corpus falls from 71 pages to 47 and from 101,000 words to 57,000 — **-43%
  measured over everything a page contains, -47% measured over running prose
  alone.** The surviving pages are shorter as well, because most of what came out
  was the same thing said on three pages.
  
  This repository has no `mkdocs-redirects`, so **every one of those twenty-eight
  URLs is permanently dead**, as is any bookmark to a heading that moved onto a
  different page. Readiness is the one most likely to be linked: it was a section
  of the contract reference and is now [a page of its
  own](https://pacto.run/latest/contract-reference/readiness/), so
  `contract-reference/#readiness` no longer resolves to anything. If you have
  linked to a Pacto documentation page from a runbook or a wiki, this release is
  the one that breaks it. `mike` keeps prior versions live at their own URL
  prefixes, which is the only mitigation there is: every version deploys under
  its own number, so a bookmark to `pacto.run/latest/collectors/` still resolves
  at `pacto.run/3.3.3/collectors/`. The list above is the map from what was
  removed to the page that now carries it.
  
  **Three claims the documentation used to make are now rejected by the linter.**
  Vale gained tokens for "policy enforcement" and "enforcement layer" — Pacto
  checks policy, it does not act on a live system, and the layer that does the
  checking is named "Policy checks" — and for "blast radius", which is borrowed
  jargon for something the tools report precisely as the consumers a change
  affects. Three more tokens catch prose habits rather than claims: "leverage" is
  always "use" here, "load-bearing" never named what it held up, and an opening
  that announces the page instead of starting it is now a lint error. The
  disclaimers that say what Pacto is *not* are deliberately still allowed to use
  the words they disclaim, and the finding codes consumers branch on are not
  claims either, so no token matches them.

## 3.3.3

### Patch Changes

- fa6d27a: Take the week's dependency bumps in one go: `modelcontextprotocol/go-sdk`
  1.7.0 → 1.8.0 in the engine, and `controller-runtime` 0.25.0 → 0.25.1,
  `ginkgo` 2.32.2 → 2.33.0 and `gomega` 1.43.0 → 1.43.1 in the operator module.
  No source changes — all four are drop-in.
  
  Dependabot also learns to group: Kubernetes libraries move as one train
  because client-go, apimachinery and controller-runtime pass each other's
  types, Charm moves as one because bubbletea, bubbles and lipgloss share the v2
  renderer, everything else minor-or-patch lands in a single weekly PR. Five
  open PRs for one afternoon of bumps is how a bot teaches you to stop reading
  it.

## 3.3.2

### Patch Changes

- 12b7c03: Gate the documentation deterministically, and cut the corpus to fit the gate.
  
  `make docs-lint` is a new leg of `make docs-check`, so CI and a laptop run the
  identical command. Three tools, three concerns, no overlap — a rule has exactly
  one home:
  
  - **markdownlint** (`.markdownlint-cli2.jsonc`) owns Markdown syntax.
  - **Vale** (`.vale.ini`, `.vale/styles/Pacto/`) owns prose: marketing language,
    buzzwords, filler, weasel words, unsupported claims and the terminology this
    project has already settled on.
  - **`release/scripts/docs_lint.py`** owns structure: 1200 prose words per page,
    250 per section, headings no deeper than `####`.
  
  No model is involved in any of it. The same input always produces the same
  verdict, which is the point — a documentation rule nobody can forget is worth
  more than one every contributor is asked to remember. Both linters are pinned
  by version and neither is vendored; a missing tool is a hard failure, never a
  silent skip.
  
  The word budgets count running prose only. Front matter, HTML comments, fenced
  code (including mermaid), tables and lists are exempt by design: what makes a
  section unreadable is unbroken running text, and a table or a bulleted
  reference list is already broken up. Counting them punished pages for being
  reference pages. `docs_lint.py` self-tests the counter on every run, because a
  counter that quietly stops counting reads exactly like a corpus in good shape.
  
  Scope lives in one place — `scope()` hands both external tools an explicit file
  list rather than a glob, covering the published site, each integration's
  hand-written docs and the root Markdown a reader meets on GitHub. Generated
  pages are out: `docs/cli-reference.md` and the per-integration `generated/`
  trees are already drift-gated against their real sources, and prose rules would
  only fight the generator.
  
  A fourth leg resolves the links and anchors in the root Markdown files.
  `mkdocs build --strict` already validates everything inside `docs/`, and the
  integration hook copies `integrations/*/docs` into it, so those were covered;
  the six files at the repository root were outside `docs_dir` and gated by
  nothing.
  
  **Eleven pages were split into twenty-seven to fit the page budget** —
  architecture, concepts, contract-reference/sections, impact, mcp-integration,
  operational-graph, platform-engineers, the demo tour, the release and testing
  pages under `maintainers/`, and the Kubernetes installation page. Each split
  promoted its extracted sections to a new page, added the `mkdocs.yml` nav entry
  and rewrote every inbound link. Everything else came off by deleting prose, not
  by moving it.
  
  **Eleven pages were removed and what they said survives on the page that
  already owned the subject.** The eight one-contract example stubs are now
  sections of [Example contracts](https://pacto.run/latest/examples/); the
  ingestion endpoints are a section of evidence tooling, ingestion and security;
  override files are a section of configurations as composable claims; and the
  developer day-to-day page repeated the contract reference, the breaking-change
  section of the platform-engineer guide and the MCP page, so it is gone and the
  three links point at those.
  
  This repository has no `mkdocs-redirects`, so **a page that was removed and a
  heading that moved to a new page are both permanently dead deep links** for
  anyone who bookmarked them. `mike` keeps prior versions live at their own URL
  prefixes, and each origin page names where its content went.

## 3.3.1

### Patch Changes

- 36e2ef2: Stop the OCI bundle cache from reporting a failure when a concurrent pull of the
  same reference commits first.
  
  A cache entry is built in a staging directory and swapped into place with one
  rename, so a reader sees a whole entry or none. Two pulls of the same reference
  aim at the same destination, though, and nothing serializes them — the second
  pull is usually a second `pacto` process sharing one cache directory. Both clear
  the destination, one rename lands, and the loser's fails onto the winner's fresh
  entry with `directory not empty`.
  
  Nothing was wrong: the entry the losing pull set out to write was on disk, put
  there whole by the other one. But the loser reported the rename error, and the
  caller logs it, so a healthy parallel pull told the user `could not cache the
  pulled bundle`. The commit now asks the filesystem what is actually there and
  treats a lost race as the success it was.

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
    hundred, the same steps the backend will honour, because a control that offers a
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
