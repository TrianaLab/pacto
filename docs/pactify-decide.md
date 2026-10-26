# Decide, unfreeze and roll back

What a person does once a service ships through Pacto delivery: decide on a
change that waits, let a frozen environment move again and take a version back
out. It follows [Pactify a service](pactify.md); the commands use the Stage
`my-service` that `pacto init --delivery` wrote.

## When a change waits or an environment freezes

A waiting change is an Operation in the phase `Held`; a frozen Stage shows
`true` under `Frozen`. Only a person decides, never an AI agent, and never the
person who asked for the change:

```bash
kubectl get operations.delivery.pacto.trianalab.io -n test  # find the held one
pacto delivery explain -n test <operation>            # what is asked and why
pacto delivery approve -n test <operation> --decision approve
pacto delivery approve -n test <operation> --decision accept-unknown
pacto delivery approve -n test <operation> --decision reject
pacto delivery cancel -n test <operation>             # withdraw it instead
pacto delivery propose -n test --stage my-service --type unfreeze --reason "fixed"
```

`approve` lets a change that breaks a consumer go ahead; `accept-unknown` lets
one go ahead that Pacto could not check, recorded as accepted, never as a pass.
To unfreeze is to let automation move the Stage again; it takes effect
without anyone else's approval. Each needs the `pacto-delivery-approver` role,
bound to a group or, with `--user`, to one person. Only members of the team
that owns an affected service may answer a question about it:

```bash
kubectl create clusterrolebinding payments-approvers \
  --clusterrole=pacto-delivery-approver --group=payments
```

Deciding (approve, accept-unknown or reject) also needs a company login the
cluster trusts, done within the promoter's login freshness (15 minutes by
default, `promoter.authFreshness`). A cluster admin sets that up once
with [Sign-in, approvals and agents](integrations/kubernetes/delivery-identity.md),
which says how to check a managed cluster can. `pacto delivery approve` does
that login itself: it prints an address, you log in again in your browser, and
it records the decision with that login, never your kubeconfig's (set
`PACTO_DELIVERY_OIDC_ISSUER_URL` to your identity provider). Where the cluster
cannot, nobody decides: cancel the change, fix what it asks about and publish
again, or have someone with the approver role in the group `pacto:break-glass`
force the held version, as below. That skips every check, shows in the history
and freezes the Stage until a person unfreezes it, which needs no fresh login.

```bash
to=$(kubectl -n test get operations.delivery.pacto.trianalab.io <operation> -o jsonpath='{.spec.params.to}')
pacto delivery propose -n test --stage my-service --type override --to "$to" --reason "why"
```

In the dashboard (`pacto dashboard`), the Delivery tab's **Inbox** lists the
changes **Waiting for your decision**: open one and press **Approve** (or
**Accept the unknown**) or **Reject**. Its **Environments** view has an
**Unfreeze** button on each frozen Stage.

## Roll back by hand

Pacto rolls a failed version back on its own. To roll back a version that
passed its checks but you still do not want, name the change that put it in
place, then undo it:

```bash
pacto delivery blame -n prod --stage my-service   # its change: line names the operation
pacto delivery undo -n prod <operation>           # plans the undo and prints the command; proposes nothing
pacto delivery propose -n prod --stage my-service --type rollback --undoes <operation> --reason "why"
```

`undo` prints the exact `propose` command for the case at hand. A rollback can
only undo the change the Stage runs now. If the Stage has moved since, or that
Operation was deleted, `undo` plans a promote to the earlier version instead,
and every gate runs. A rollback on a Stage with `auto: true` puts it on
auto-hold, so automation does not undo your choice. In the dashboard, the
Delivery tab's **History** has an **Undo** button on each change that moved a
Stage. More in
[Signed records: history, blame and undo](integrations/kubernetes/delivery-records.md#history-blame-and-undo).
