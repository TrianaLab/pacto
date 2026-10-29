# Decide, unfreeze and roll back

!!! warning "Alpha"
    Delivery is new: its settings and commands may still change.

This page is for the person Pacto asks to decide on a change. It covers
everything you need to do it. If you set up Pacto for your team, read
[Pactify a service](pactify.md) first.

Words in links are defined in the [glossary](glossary.md). Commands below name
a [Stage](glossary.md#stage) by its service and its environment's namespace,
so `-n prod` means "in prod". A [change](glossary.md#change) is a Kubernetes
object of kind `Operation`.

## Why you were asked

Pacto holds a change and asks a person in these cases:

- **It would break a consumer.** For example, payments 3.0.0 removes a field
  and the checkout contract accepts only payments 2.x. Pacto asks the team
  that owns checkout, because theirs is the service it would break.
- **It could not check something.** Monitoring had too little data, or no
  contract depends on the part that changed, so a service without a contract
  might call it. That check says Unknown.
- **The Stage is set to ask for every promote.** This is `approval.always`,
  which [pactify](pactify.md) turns on. A person approves every promote,
  whatever the level.
- **The Stage is on advise.** A person approves every promote that a person
  did not ask for; see [the autonomy ladder](autonomy.md).
- **The Stage is on auto for non-breaking and the change is breaking,
  possibly breaking or unclassified.** Every consumer's contract accepts it, but this level ships only
  non-breaking changes on its own, so a person approves it.
- **The Stage requires records and the [recorder](glossary.md#recorder)
  cannot write them now.** A person approves the promote or rollback to go
  ahead before its signed record exists. The change itself runs as usual.
- **Someone asked to raise the Stage's level.** A raise lets Pacto do more on
  its own and keeps the running version. A person of the team that owns the service,
  other than the one who asked, approves it.
- **The version changes who owns the service** (the team, or the
  [DRI](glossary.md#dri) when no team is named). The team that owns the service now approves it, so the
  author of a version never picks who approves it. A version that only edits
  the owner's contacts does not wait. When the running contract cannot be
  read, no team is asked, and only [break-glass](glossary.md#break-glass) can
  answer if the [promoter](glossary.md#promoter), the part of delivery that
  runs each change, requires an owner team.
- **The version number is too small for the change.** A breaking change
  published as a minor (1.9.0 after 1.8.0) would pass every `^1` range, so a
  person approves it. So does a version whose contract moved to another
  registry domain, because consumers declared against the old domain would
  not be counted.
- **A person or agent asked to roll back to a version a consumer has moved
  past.** That consumer's team approves it first; see
  [Cancel and roll back](integrations/kubernetes/delivery-operations.md#cancel-and-roll-back).

Pacto asks members of the [owner team](glossary.md#owner-team) of each
affected service, or the changed service's own team when none of them names
one, and skips the person who asked. If an AI agent asked for you
without naming you, leave that change to a teammate; the rule is in
[Who may approve](integrations/kubernetes/delivery.md#who-may-approve).

## Look, then decide

Find the held change and read what Pacto asks:

```bash
kubectl get operations.delivery.pacto.trianalab.io -n prod   # PHASE shows Held
pacto delivery explain -n prod <change>                       # what is asked and why
```

The `question:` line `explain` prints ends with what it asks for, for
example `Approve shipping it anyway?` or `Accept the unknown?`. Give that
answer, or reject.

Then give one answer:

| Answer | Use it when | What happens |
|---|---|---|
| `approve` | The question says "Approve" and you accept it. Pacto asks that in every case in [Why you were asked](#why-you-were-asked) except a check that said Unknown | Once every team asked has approved, the version goes ahead, or for a raise, the Stage moves up one level |
| `accept-unknown` | The question says "Accept the unknown": a check said Unknown and you accept the risk | The version goes ahead and the record says "accepted". If the version already shipped and a later check said Unknown, only this or `reject` answers |
| `reject` | You do not want this version here | If it has not shipped, the change ends and the Stage keeps the version it runs. If it already shipped, Pacto rolls it back to the version before. Someone can ask again later |

```bash
pacto delivery approve -n prod <change> --decision approve   # or accept-unknown, or reject
```

`<change>` is the name in the `NAME` column of the first command. All three
answers go through `approve`; the `--decision` flag says which one.

A held change that nobody answers ends on its own after 24 hours. If it had
not shipped, it ends as `Expired` and the Stage keeps the version it runs. If
it had shipped and the question was about a later Unknown check, the Stage
freezes instead, so a person looks at it. The person
who asked can withdraw it at any time with `pacto delivery cancel -n prod <change>`.

In the dashboard (`pacto dashboard`), the **Delivery** tab's **Inbox** lists
the changes **Waiting for your decision**. Changes another team may answer
too sit behind a **Shared with other teams** count, and frozen Stages and
changes that could not finish behind their own counts. Open a change: the
page leads with the question and two cards, **If you approve** and **If you
reject**, each saying what happens and each with its own button. Write your
note, then press **Approve shipping** (or **Accept the unknown**) or
**Reject**. The checks, the affected services and the timeline are folded
below, one click away.

## What you need to be allowed

Three things, all set up once by your cluster admin:

1. **A role.** Your Kubernetes user or group needs the approver role, and
   your group must be the owner team written in the contract of the service
   you are asked about: for a breaking change, the consumer that would break;
   otherwise, the service whose version or level is moving.
   Ask your admin for a binding like this one, for the team `payments`:

   ```bash
   kubectl create clusterrolebinding payments-approvers \
     --clusterrole=pacto-operator-delivery-approver --group=payments
   ```

2. **The people group.** If your admin named the groups people are in when
   turning delivery on (`humanGroups`), your login must be in one. Anyone
   outside them is treated like an AI agent and can never approve.
3. **A recent company login, if your admin asks for one.** By default you
   decide with the sign-in you have. If your admin sets
   `delivery.promoter.authFreshness`
   ([a recent sign-in to decide](integrations/kubernetes/delivery-identity.md#a-recent-sign-in-to-decide)),
   the cluster must trust your company sign-in and you must have signed in
   within that time. Then `pacto delivery approve` prints an address, you
   sign in again in your browser and it sends your answer with that login,
   and the dashboard asks you to sign in again in the same way. Before the
   first time, set your company's sign-in address, which your admin gives
   you, in your shell: `export PACTO_DELIVERY_OIDC_ISSUER_URL=https://login.example.com`.

Where a recent sign-in is required but the cluster cannot check it
([where approving works](integrations/kubernetes/delivery-identity.md#where-approving-works)),
nobody can give any of the three answers, reject included. A held change then
has three ways out: the person who asked cancels it and
publishes a version that asks nothing, it ends after 24 hours, or someone in
the break-glass group forces it (below).

## When a Stage is frozen

A frozen Stage shows `true` under `FROZEN` in
`kubectl get stages.delivery.pacto.trianalab.io -n prod`, and its reason says
why: [What happens to a change](integrations/kubernetes/delivery.md#what-happens-to-a-change)
lists when a failed version freezes a Stage instead of rolling back. A forced
version (below) also freezes it. Fix the cause first, then let automation move it
again:

```bash
pacto delivery propose -n prod --stage payments --type unfreeze --reason "fixed the config"
```

An unfreeze takes effect at once and needs nobody else, even though the
command is `propose`. It also clears `HOLD`, so run it on a Stage that is on hold even when
`FROZEN` shows `false`. It needs the approver role but no fresh login, and
only the team that owns the service (the owner team in the contract running on
the Stage) or break-glass may unfreeze it: a person of another team is refused
with `NotYourTeam`, even with a cluster-wide approver binding. In the dashboard, the
**Environments** view has an **Unfreeze** button on each frozen Stage, shown
only to the people the same rule lets unfreeze it.

## Roll back by hand

Pacto rolls a failed version back on its own. To remove a version that
passed its checks, find the change that put it there and undo it:

```bash
pacto delivery blame -n prod --stage payments   # the "change:" line is the change to undo
pacto delivery undo -n prod <change>            # prints the exact rollback command; changes nothing
```

Run the command `undo` prints. If the Stage has moved on since, `undo`
prints a normal change to the earlier version instead, and every check runs
again. After a manual rollback, automation stops moving that Stage (the
`HOLD` column shows `true`) until a person unfreezes it or asks for a new
version, so your choice stays in place. In the dashboard, the
**History** view has an **Undo** button on each change.

## Force a version (break-glass)

Someone in the [break-glass](glossary.md#break-glass) group can force a
version past every check. It shows in the history and freezes the Stage until
a person unfreezes it. Use it only when nothing else works. `<change>` is
the held change whose version you force:

```bash
to=$(kubectl -n prod get operations.delivery.pacto.trianalab.io <change> -o jsonpath='{.spec.params.to}')
pacto delivery propose -n prod --stage payments --type override --to "$to" --reason "why"
```
