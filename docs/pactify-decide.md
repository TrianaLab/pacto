# Decide, unfreeze and roll back

This page is for the person Pacto asks to decide on a change. You need no
other page to do it. If you set up Pacto for your team, read
[Pactify a service](pactify.md) first.

## The words on this page

Pacto decides which version of each service runs in each environment, such
as test or prod. Every service has a **contract**, a file called `pacto.yaml`
that says who owns the service, what it offers and which other services it
calls.

| Word | Meaning |
|---|---|
| Stage | One service in one environment, for example payments in prod. Its name is the service name and its Kubernetes namespace is the environment, so `-n prod` below means "in prod" |
| Change | A request to move a Stage to another version. In Kubernetes it is an object of kind `Operation` |
| Consumer | A service whose contract says it calls yours, and which versions of yours it accepts |
| Held | A change that waits for a person |
| Frozen | A Stage that automation no longer moves until a person unfreezes it. It shows under `FROZEN` |
| On hold | A Stage that automation no longer moves after a person rolled it back by hand, so it does not undo that choice. It shows under `HOLD`, and an unfreeze clears it too |
| Unknown | Pacto could not see enough to say whether the change is safe |

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
  whatever the rung.
- **The Stage is on advise.** A person approves every promote that a person
  did not ask for; see [the autonomy ladder](autonomy.md).
- **The change is not known to be non-breaking and the Stage is on auto for
  non-breaking.** It is breaking, possibly breaking or could not be
  classified. Every consumer's contract accepts it, but this rung ships only
  non-breaking changes on its own, so a person approves it.
- **The Stage requires records and the recorder cannot write them now.** A
  person approves the promote or rollback without a record yet.

You are asked only if you are in the team that owns an affected service, and
never about a change you asked for yourself. An AI agent can ask for changes,
but it can never decide one.

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
| `approve` | The question says "Approve" and you accept it. Pacto asks that when the change breaks a consumer, the Stage asks for every promote, the Stage is on advise, the Stage is on auto for non-breaking and the change is not known to be non-breaking or the Stage requires records the recorder cannot write now | The version goes ahead |
| `accept-unknown` | The question says "Accept the unknown": a check said Unknown and you accept the risk | The version goes ahead; the record says "accepted", never "passed". If the version already shipped and a later check said Unknown, only this or `reject` answers |
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
the changes **Waiting for your decision**. Open one and press **Approve**,
**Accept the unknown** or **Reject**.

## What you need to be allowed

Two things, both set up once by your cluster admin:

1. **A role.** Your Kubernetes user or group needs the approver role, and
   your group must be the owner team written in the contract of the service
   you are asked about: for a breaking change, the consumer that would break;
   otherwise, the service whose version is moving.
   Ask your admin for a binding like this one, for the team `payments`:

   ```bash
   kubectl create clusterrolebinding payments-approvers \
     --clusterrole=pacto-operator-delivery-approver --group=payments
   ```

2. **A fresh company login.** The cluster must trust your company sign-in,
   and you must have signed in within the promoter's login freshness: 15 minutes by
   default, set by `delivery.promoter.authFreshness`. You do not do
   anything special: `pacto delivery approve` prints an address, you sign in
   again in your browser and it sends your answer with that login. The
   dashboard asks you to sign in again in the same way. Before the first
   time, set your company's sign-in address, which your admin gives you, in
   your shell: `export PACTO_DELIVERY_OIDC_ISSUER_URL=https://login.example.com`.

Some clusters cannot check a fresh login. It works where your admin sets
the Kubernetes API server's flags, and nobody has tested it yet on managed
clusters such as EKS, GKE and AKS. There, nobody can give any of the three answers,
reject included. Ask your admin, who can
check with [Sign-in, approvals and agents](integrations/kubernetes/delivery-identity.md#where-approving-works).
A held change then has three ways out: the person who asked cancels it and
publishes a version that asks nothing, it ends after 24 hours, or someone in
the break-glass group forces it (below).

## When a Stage is frozen

A frozen Stage shows `true` under `FROZEN` in
`kubectl get stages.delivery.pacto.trianalab.io -n prod`, and its reason says
why. A failed promote is rolled back once, whoever asked for it. Pacto
freezes the Stage instead when there is no earlier good version, the
automatic rollback itself failed or someone chose the running version by hand
(a rollback, or a promote during auto-hold). A forced version
(below) also freezes it. Fix the cause first, then let automation move it
again:

```bash
pacto delivery propose -n prod --stage payments --type unfreeze --reason "fixed the config"
```

Despite the name `propose`, an unfreeze takes effect at once and needs nobody
else. It also clears `HOLD`, so run it on a Stage that is on hold even when
`FROZEN` shows `false`. It needs the approver role but no fresh login. In the dashboard, the
**Environments** view has an **Unfreeze** button on each frozen Stage.

## Roll back by hand

Pacto rolls a failed version back on its own. To take out a version that
passed its checks but you still do not want, find the change that put it
there and undo it:

```bash
pacto delivery blame -n prod --stage payments   # the "change:" line is the change to undo
pacto delivery undo -n prod <change>            # prints the exact rollback command; changes nothing
```

Run the command `undo` prints. If the Stage has moved on since, `undo`
prints a normal change to the earlier version instead, and every check runs
again. After a manual rollback, automation stops moving that Stage (the
`HOLD` column shows `true`) until a person unfreezes it or asks for a new
version, so it does not undo your choice. In the dashboard, the
**History** view has an **Undo** button on each change.

## Force a version (break-glass)

Someone in the break-glass group, `pacto:break-glass` by default, can force a
version past every check. It shows in the history and freezes the Stage until
a person unfreezes it. Use it only when nothing else works. `<change>` is
the held change whose version you force:

```bash
to=$(kubectl -n prod get operations.delivery.pacto.trianalab.io <change> -o jsonpath='{.spec.params.to}')
pacto delivery propose -n prod --stage payments --type override --to "$to" --reason "why"
```
