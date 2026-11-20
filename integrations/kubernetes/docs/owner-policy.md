# Owner policy

The owner policy names who may set a service owner beside the owner's own
members: the default approvers, the signers who count as members of a team
and the issuers whose email subjects count as people. The rule that uses it
is in [Who may set a service owner](../../owner-setting.md).

## The `declare` values

```yaml
declare:
  defaultApprovers:
    bootstrap:
      - name: platform-admins
        issuer: https://accounts.google.com
        subject: ^admin@acme\.com$
  teams:
    payments:
      - name: ana
        issuer: https://github.com/login/oauth
        subject: ^ana@acme\.com$
  emailIssuers: []
  trustedRoot: ""
```

| Value | Meaning |
|---|---|
| `declare.defaultApprovers.bootstrap` | Signers who may set any service owner, as `{name, issuer, subject}` |
| `declare.teams` | Per team, the signers who count as its members |
| `declare.emailIssuers` | Issuers whose email subjects match an owner's DRI or email contacts; empty uses Google, GitHub and Microsoft |
| `declare.trustedRoot` | A Sigstore `trusted_root.json` for an air-gapped install or a private Sigstore; empty uses `delivery.publishers.trustedRoot`, else public-good Sigstore |

`issuer` is compared exactly. `subject` is a pattern anchored with `^` and `$`
that matches one identity. A policy holds at most 200 rules.

With `bootstrap` and `teams` both empty the chart renders nothing, and every
owner change reads as unverified.

## The ConfigMap

The chart renders the policy into the ConfigMap
`<fullname>-owner-policy-<hash>` in the release namespace, with
`immutable: true` and a name taken from its content. The operator mounts it
at `/etc/pacto/owner-policy` and passes it to the dashboard it runs. Only a
`helm upgrade` changes the policy, and no Role in the chart grants a write to
that ConfigMap. The operator's one ConfigMap rule without a name is `create`,
which detection needs for `<fullname>-detected`; `create` cannot replace a
ConfigMap that already exists.

## Where CLIs and agents read it

With `registry` set, delivery on or off, the operator publishes the policy as
the `ownerPolicy` section of the cluster's signed target snapshot in
`<registry>/pacto-targets`. `pacto declare` and `pacto mcp` over stdio verify
those snapshots with the trust config in `$PACTO_DELIVERY_TRUST`. They use the
policy only when every cluster's snapshot is complete, fresh, signed by a key
scoped to that cluster and carries the same policy. A development cluster's
approvers therefore never approve owners for the whole fleet: give every
cluster the same `declare` block. Without `registry` only the operator and its
dashboard read the policy.

## The `OwnerVerified` condition

The operator judges a Pacto whose bundle, pulled from a registry, changes the
owner of the revision the Pacto ran before and either carries a declaration or
has a signature other than CI provenance the publisher rule accepts. It sets
`OwnerVerified`:

| Status | Reason | Meaning |
|---|---|---|
| `True` | `OwnerSignerAllowed` | A member of the current owner or a default approver signed it |
| `False` | `OwnerSignerNotPerson` | An agent, CI or workload identity signed it |
| `False` | `OwnerSignerNotAllowed` | A person outside the owner's members and the default approvers signed it |
| `Unknown` | `OwnerSignerUnverified` | No signature verified, or the policy, the Sigstore root or the previous revision could not be read |

On anything but `True`, `status.contract.owner` stays empty and the fleet
ignores the owner. The condition is missing for every other Pacto; see
[Troubleshooting](troubleshooting.md#reading-the-conditions).
