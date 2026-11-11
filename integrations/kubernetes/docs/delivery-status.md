# Delivery reference: status, progress and lifecycle

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

How to read where a service stands in [Pacto delivery](delivery.md): what
each Stage runs, why a version cannot move yet, what you may ask for and how
one version travelled. Every answer here only reads; changes still go through
[plan, propose and approve](delivery-operations.md).

## Where each Stage stands

```bash
pacto delivery status --service payments
pacto delivery status --service payments --why
```

`pacto delivery status`, the MCP tool `pacto_delivery_status` and the
dashboard's service page (`/api/delivery/environments?service=payments`)
return one row per Stage of the service you can see. They read with your own
identity. Without `-n` the CLI reads every namespace you can list; a namespace
you cannot list makes the answer `partial` and appears in `unreadable`. A
partial answer never means the service has no other Stages. The MCP tool
takes `service`, `domain`, `namespace` and `why`; on a person's server, which
registers no propose tool, its `note` says so.

Rows come in promotion order, taken from each Stage's `spec.candidates`, and
each row names its `upstream`: the Stage, signed records or OCIRepository its
candidates come from. Cycles and environments that services order both ways
are named in the `note` and are never silently ordered.

| Field | What it says |
|---|---|
| `state` | The one state the board also counts: `failed`, `frozen`, `autohold`, `waiting`, `empty`, `unverified` or `verified`. Status rows add `unknown` when the Stage's operations could not be read (`heldUnknown`; the CLI prints `? held`) |
| `version`, `contractRef` | The version the running artifact's contract declares and the digest-pinned contract it names (`delivery.pacto.trianalab.io/contract`). The dashboard links it to the fleet revision page |
| `sourceRevision` | The source revision `pacto publish` stamped on the artifact (`org.opencontainers.image.revision`): an annotation, not signed |
| `lastCheck` | The newest re-check after release: the worst result, when it ran and each verdict with its citation. `current` is false once the Stage moved to another version |
| `watched` | The `Watched` condition while `lastCheck` is about the running version, else `Unknown` with reason `NotRechecked` |
| `ready`, `readyFix` | The Ready condition and, when it is False, the change that clears it |
| `offers` | What you may ask for here, below |

The CLI labels a Pass on `pacto.diff`, `pacto.impact` or
`pacto.environment` as Checked, as the dashboard does: those gates pass once
they ran. Read the message and the plan's floor to learn whether what they
found holds the change. The signed `result` stays Pass.

Each status answer costs one Operation list and one history read per Stage,
bounded like `pacto delivery history --all` (500 Stages in two minutes).

## Why a version cannot move yet

`--why` (MCP `why: true`, the dashboard's `/api/delivery/progress`) adds
`progress` to each row: a `state`, a one-sentence `summary`, every reason with
its fix and the promote planned for you. The plan is a dry run and proposes
nothing. A status answer plans at most 16 Stages and names the rest in its
`note`; name a namespace with `-n` to plan the others.

| State | Meaning |
|---|---|
| `up-to-date` | The Stage runs its newest candidate and nothing is open on it |
| `can-promote` | Nothing holds the candidate back, and there is a candidate to move to |
| `needs-person` | A held change or the plan waits for a person's decision |
| `blocked` | Something must change first: Ready False, no candidate, frozen, observe level, RBAC, a rejected or waiting plan |
| `unknown` | Pacto could not read the operations, the identity policy, the RBAC answer, the Ready condition or the plan; the status predates the Stage's spec; or a change is open on a Stage with nothing to move to |

`completeness` is `partial` whenever a reason ends in `Unknown` or is
`StatusStale`, whatever the state: a reason may be missing.

Reason codes: `NotReady`, `ReadyUnknown`, `StatusStale`, `NoCandidate`, `Frozen`, `AutoHold`,
`Held`, `InFlight`, `Level`, `OpsUnknown`, `PolicyUnknown`, `NotPermitted`,
`RBACUnknown`, `PlanRejected`, `PlanHeld`, `PlanWaits` and `PlanUnknown`. A fix
prints a command only when you may run it: the approve command only for a
person of the operation's owner teams who did not ask for it, the unfreeze
command only for someone admission lets unfreeze and never an approve
command for an agent.

`autoPromote` is the promoter's own reason for not promoting on its own: the
level is `observe` or `advise`, the Stage is frozen or on auto-hold, there is
no candidate or an operation is already in flight. The promoter and this
answer share the rule, so they cannot disagree.

| Ready reason | Fix |
|---|---|
| `Unguarded` | Install or repair the delivery admission policies and their bindings (`delivery.enabled` on the pacto-operator chart) |
| `CandidateNotFound` | Create the OCIRepository `spec.candidates.ociRepositoryRef` names |
| `CandidateUnverified` | Set `spec.verify` on the candidate OCIRepository with a `secretRef` or `matchOIDCIdentity` naming the publisher, then publish with `pacto publish` |
| `UpstreamUntrusted` | Give the promoter the upstream recorder's key (`promoter.upstreamTrust`) |
| `UpstreamUnreadable` | Make the upstream records readable: the repository, the registry credentials and the upstream trust key |
| `NoCandidateSource` | Set one of `spec.candidates.ociRepositoryRef`, `upstreamStage` or `upstreamRecords` |
| `ApplierUnreadable` | Create the object `spec.applyRef` names, or install its CRD, and let the promoter read it |
| `ApplierUnlabelled` | Label the applier `delivery.pacto.trianalab.io/apply-ref=<stage>` |
| `ApplierMultiSource` | Give the Argo CD Application one OCI `spec.source` instead of `spec.sources` |
| `ApplierNotOCI` | Point the Application's `spec.source.repoURL` at an `oci://` repository |
| `ApplierNotAutomated` | Set `spec.syncPolicy.automated` on the Application |

## What you may do

`offers` lists `promote`, `unfreeze` (while frozen or on auto-hold), `freeze`
and `undo`, each `available` or not with a `reason`. `actions` keeps only the
available promote and unfreeze, as before. Freeze and undo are offered only
when the request names a service, so the many-service board costs no history
read. An observe Stage and a Stage whose admission policies are missing (Ready
False `Unguarded`) never offer a promote.

An undo offer is labelled undo, never rollback: when the shipping Operation is
gone, undo becomes a promote back that runs every gate.
`pacto delivery undo` or `/api/delivery/undo` gives the definitive plan.

Offers fail closed. Pacto asks the apiserver once per namespace whether you
may create Operations; a denial, or a review that fails, makes every offer
there unavailable and says why. When the delivery identity policy
cannot be read, every offer is unavailable with the error, and a NotFound
suggests `--delivery-namespace`; see
[approvals and agents](delivery-identity.md). Offers only describe: proposing
still goes through `pacto delivery propose`, the dashboard's propose action or
an agent's propose tool with your own identity. Admission decides.

## Follow one version

```bash
pacto delivery lifecycle --service payments --version 3.0.0
```

`pacto delivery lifecycle`, `pacto_delivery_lifecycle` and
`/api/delivery/lifecycle` take a service and exactly one of `--digest` (the
artifact), `--version` (its contract's version) or `--contract` (a manifest
digest or a digest-pinned reference), with an optional domain and namespace. A version or contract is found among at most 200
artifacts the Stages remember; several matches is an error that lists them.
When artifacts were left unread, past that limit or because the registry
failed, or a Stage listing was refused, a miss says what was not read and a
single match says another may share it and makes the answer `partial`. The
dashboard answers `404 LifecycleNotFound` only for a miss from a complete
search and `400 LifecycleUnresolved` otherwise.

`signature` follows the Publish page's rule: `signed` when a Stage you can see
names the artifact in `status.published` or `status.offered`, `unsigned` only
when an entry Stage whose published list never reached its cap does not name
it, else `unknown`. The source revision is an annotation, not signed.

Per Stage, each event has a role: `arrived`, `left` or `attempted`.
`verified` and `failed` read `yes`, `no` or `unknown`; a partial history or a
list at its cap answers `unknown`, never "never reached". Each Stage lists its
newest 50 events. `note` names a Stage that runs the artifact on a complete
history with no `arrived` event: it was placed before its records began or
moved outside Pacto.

## One record

```bash
pacto delivery record get -n prod --stage payments sha256:...
```

`pacto delivery record get`, `pacto_delivery_record_get` and
`/api/delivery/record` take a namespace, a Stage and the record id a history
line names (or the short id a text history prints when
only one record starts with it), and return that record: the change, who asked and approved, every
verdict with how to run it again and whether its signature was checked
against a trust config. A record that does not verify is never returned;
history lists it as `unverifiedRecord`.
