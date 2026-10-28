# Delivery reference: plan, propose and approve

How a change is planned, proposed and approved, for
[Pacto delivery](delivery.md). Setting up a Stage is in
[Delivery reference: setup and gates](delivery-setup.md).

```bash
pacto delivery plan -n prod --stage payments --to sha256:...
pacto delivery propose -n prod --stage payments --to sha256:... --intent-digest sha256:...
pacto delivery approve -n prod <operation> --decision approve
```

`approve` decides as your kubeconfig signs you in. Where the Stage asks for a
recent sign-in, it makes you log in again in your browser through its own
`pacto-approve` client instead, so it needs the issuer the cluster trusts
(`--oidc-issuer-url` or `PACTO_DELIVERY_OIDC_ISSUER_URL`). See
[approve from the terminal](delivery-identity.md#approve-from-the-terminal).

`plan` predicts each gate's verdict and the one question a person would be
asked. `propose` with the plan's intent digest refuses with `PlanStale` if
anything moved since.

## Cancel and roll back

`cancel` works until the pointer moves; after that,
propose `--type rollback --undoes <operation>`. A tag is refused. A rollback
runs no gates, so only a person or break-glass may roll back a rollback or an
override: an agent proposes a promote of that version instead, which the gates
judge. One check applies to a rollback a person or agent raises: when a
consumer's range does not accept the version it returns to (checkout moved to
`^3` and the rollback goes back to 2.x), that consumer's team approves it
first. Nothing Pacto cannot see holds a rollback, and the promoter's own
rollbacks never wait. An agent may
cancel only an operation it proposed; a rollback or freeze the promoter raised,
or anyone else's operation, is cancelled by a person or break-glass. Once an
operation has moved the pointer nobody deletes it, not even break-glass:
re-checks, rollbacks and history cite it. Only the promoter (after
`delivery.recorder.operationTTL`, and never the one the Stage runs) and the
namespace controller remove it. With the recorder off, history names any seq
whose Operation is gone. Names ending in `-rollback`, `-freeze` or `-stepdown`,
and `<stage>-auto-` or `<stage>-witnessed-` names, are the promoter's own:
admission refuses them to everyone else, so nobody can take the name of a
rollback before the promoter raises it.

`pacto mcp --delivery` gives agents the tools
[Which tools exist](../../mcp-integration.md#which-tools-exist) lists, with
propose and cancel only when the apiserver says the caller is an agent.

## When another service must move first

When a promote breaks a consumer, the plan names a version of that consumer to
ship first: one that accepts both the running and the new provider version,
fits the other services there, is newer than what runs and was offered by its
upstream Stage. It shows in `plan`, `explain`, `status.plan.order` and the
dashboard's **Update these first**. While a change waits, the promoter plans
again, so a consumer that moved stops being asked and one newly broken is
asked. Pacto never promotes the other service itself.

Only consumers in the same environment count. A Pacto's
`delivery.pacto.trianalab.io/environment` label places it; without one, it is
where a Stage's Kustomization applies (`targetNamespace`), even if you deployed
it there for another environment, so label it then. A Pacto that cannot be
placed counts everywhere and, as a consumer, asks a person.

## Who may do what

| Identity | May | May not |
|---|---|---|
| Agent (its service account or `pacto:agents`) | propose, read and cancel its own operations | approve, freeze, unfreeze, write status, edit the pointer |
| Person | propose, read and cancel any operation, approve a question their team is asked, freeze, unfreeze | write status (beyond clearing a Stage's `upstreamSeen`) |
| Break-glass (`pacto:break-glass`) | override and edit `spec.ref.digest` | |
| Promoter | write status and raise its own operations | sign or push |
| Recorder | write the record fields and finalizer; sign and push records | anything else |

Admission requires an Approval's `spec.by` and `spec.groups` to match the
requesting user, and a `spec.authTime` to match its login time. The promoter
does not count a login older than `delivery.promoter.authFreshness` when you
[set one](delivery-identity.md#a-recent-sign-in-to-decide), an approval from
the operation's own actor or
whoever it acts for, one from outside the teams asked, one given before the
question was last asked, an accepted unknown where an approval is needed or an
approval once the version shipped. The operation's `status.message` says why
an Approval does not count. Once given, an Approval's spec cannot
change; the promoter makes its operation its owner, so deleting the operation
deletes its Approvals. A break-glass edit of the pointer is never undone: it
is recorded as a witnessed override and the Stage freezes.
