# Who may set a service owner

`service.owner` routes reviews: impact analysis, owner pages and the
Backstage export name it. So declaring a service never claims it. A
[declaration](publishing.md#declare-a-running-service) that sets, changes or
removes the owner is allowed only when its signer belongs to the current
owner or is a default approver. Anyone else is refused before anything is
pushed:

```text
$ pacto declare invoices --registry ghcr.io/acme --yes
refused: service.owner is attested as team payments (CODEOWNERS:7 at a1b2c3, signed by github.com/acme/invoices/.github/workflows/ci.yml); only payments members or default approvers (platform-admins) may set it
```

## The rule

Pacto checks each owner change in this order and stops at the first match:

1. The owner is unchanged: allowed.
2. The signer is no person (an agent, a CI pipeline or a workload identity):
   refused with `OWNER_SIGNER_NOT_PERSON`, whoever it acts for.
3. The signer is a member of the current owner, declared or attested:
   allowed.
4. The signer matches a default approver: allowed.
5. No verified owner policy was read: unknown, `OWNER_POLICY_UNVERIFIED`.
   Unknown is never allowed.
6. Anyone else: refused with `OWNER_SIGNER_NOT_ALLOWED`.

| Code | Meaning |
|---|---|
| `OWNER_SIGNER_NOT_PERSON` | An agent, CI or workload signer never sets `service.owner` |
| `OWNER_SIGNER_NOT_ALLOWED` | The person is no member of the current owner and no default approver |
| `OWNER_POLICY_UNVERIFIED` | No verified owner policy was read, so who may set the owner is unknown |
| `OWNER_SIGNER_UNVERIFIED` | No signature of a published owner-setting version verified |

A signer is a member of an owner when a rule of the policy's `teams` entry for
the owner's team matches it, or when its subject is an email equal to the
owner's DRI or to an `email` contact and its issuer is one of the policy's
`emailIssuers`. With no `emailIssuers`, the issuers are
`https://accounts.google.com`, `https://github.com/login/oauth` and
`https://login.microsoftonline.com`. A signing certificate carries no groups,
so without a team rule only the DRI and email contacts count. A current owner
that is only declared via a target or inferred makes nobody a member.

## Agents and CI

An agent never sets an owner. [`pacto_declare`](mcp-reference.md#declare-a-running-service)
writes a draft carrying `on_behalf_of`, and its plan lists who may accept the
owner change in `mayAccept`. `on_behalf_of` is recorded and grants nothing:
the person it names signs with `pacto declare --from-draft` and is checked
like anyone else.

## The current owner

The reference for a change is the service's effective owner before it. Pacto
walks the service's published versions in order and takes the owner of the
last one whose owner was not refused or left unchecked: its declared owner,
else its attested owner. The baseline a declaration names for itself is never
the reference. A first owner has no current owner, so setting it takes a
default approver.

## Attested owners

An owner read from CODEOWNERS or `catalog-info.yaml` (the record's
`class: ownerFile`) is inferred. It becomes **attested** when all of these
hold:

- the bundle carries a signature that verified, by a CI identity the
  [publisher rule](integrations/kubernetes/delivery-security.md#which-publishers-pacto-accepts) accepts;
- the certificate names a source repository and the commit it was issued for;
- that commit equals the record's `inputs.commit`.

The bundle, record included, is what the CI identity signed. A hand-edited
marker, an unsigned or person-signed bundle, a refused CI identity, an empty
commit and a commit mismatch each leave the owner inferred.

The first repository whose CI signs a service is trusted for attestation.
When attested versions of one service were signed from two repositories, the
service has no attested owner and reports `OWNER_CONFLICT`. A declared owner
always outranks an attested one.

An attested owner routes reviews. It never clears a gate and never decides
who may answer a delivery question: delivery reads the running contract's
declared owner only.

## Owners the fleet ignores

The fleet judges every published version that changes the owner and either
carries a declaration or has a signature other than accepted CI provenance.
It drops the owner of a version the rule refused (`OWNER_DECLARATION_REFUSED`)
and of a declaration it could not check (`OWNER_DECLARATION_UNVERIFIED`, which
makes the answer partial). The operator marks a running bundle the same way
with the [`OwnerVerified` condition](integrations/kubernetes/owner-policy.md#the-ownerverified-condition).

An unsigned version with no declaration keeps its owner. Whoever can write to
the registry can already push any contract, so registry write access stays
the trust boundary for unsigned pushes.

## Where the policy comes from

The owner policy is the operator chart's `declare` block; see
[Owner policy](integrations/kubernetes/owner-policy.md). The operator, the
dashboard and `pacto mcp --owner-policy` over HTTP read it from its
ConfigMap. `pacto declare` and `pacto mcp` over stdio read it from the signed
target snapshots beside the registry, verified with the trust config in
`$PACTO_DELIVERY_TRUST`. They use it only when every cluster's snapshot is
complete, fresh, signed by a key scoped to that cluster and carries the same
policy. Anything else (no trust config, a stale or unscoped snapshot, clusters
that disagree) is no policy, and every owner change reads as
`OWNER_POLICY_UNVERIFIED`. A local file is never accepted.
