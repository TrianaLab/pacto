# Ladder reference

The exact rules behind [the autonomy ladder](autonomy.md). Reference page:
read it when you need a field name, a blocker or a command.

## Levels and what each one changes

| Level (`status.level`) | Automatic promotes | Promote not raised by a person |
|---|---|---|
| `observe` | none | Rejected: GitOps owns the version field |
| `advise` | none | Held for a person (`approve`) |
| `auto-nonbreaking` | `NON_BREAKING` changes only | Held for a person unless the change is `NON_BREAKING` |
| `autonomous` | any plan that proceeds | Same floors as any promote |

On every level the existing floors stay: an incompatible consumer asks its
owners, Unknown holds for `accept-unknown`, `approval.always` asks a person and
an automatic rollback that cannot run freezes the Stage. Rollbacks and
break-glass overrides are never rejected by the level.

## Stage fields

| Field | Meaning |
|---|---|
| `spec.startLevel` | Level a new Stage starts on, read once while `status.level` is empty. Default `observe`. Any level is accepted, because creating a Stage is an admin action. A Stage whose status is lost seeds back to this level, so an automatic start level comes back automatic. `pacto init --delivery` always writes `observe` |
| `spec.autonomy.proposeAfter` | How many qualifying changes earn a proposal. Default 10, from 1 to 20 |
| `status.level` | Current level. After the start, only `delivery.pacto.trianalab.io/autonomy` operations change it |
| `status.observedRevision` | On observe: the digest the GitOps source reports and since when Pacto saw it |
| `status.shadow` | On observe: the newest 20 "would have" plans, with when the source reported each and its shadow verification |
| `status.proposal` | The next level Pacto proposes with its evidence, or the blockers that stop it |

`kubectl get stages` shows a `Level` column.

## When Pacto proposes the next level

N is `proposeAfter`. Evidence comes from the Stage's own status windows,
which hold at most 20 entries.

| From → to | Needs |
|---|---|
| `observe` → `advise` | The newest N shadow entries the source reported all verified, none failed; the applyRef labelled; an observed digest |
| `advise` → `auto-nonbreaking` | The newest N concluded promotes all Succeeded and verified; no automatic rollback or failed digest in the window; not frozen; `RecordStoreReady=True` |
| `auto-nonbreaking` → `autonomous` | The same, plus at least one cleared breaking promote in the window; the newest plan had a complete graph and no uncontracted callers |

A cleared breaking promote is a promote whose change was not `NON_BREAKING`,
whose impact check passed and that then verified.

## Blockers

| Kind | Meaning | Fix |
|---|---|---|
| `missing-contract` | A caller seen at runtime has no contract | `pacto init` for it |
| `undeclared-range` | A consumer declares no usable version range | Add `compatibility` to its dependency |
| `unlabelled-apply-ref` | The applyRef lacks `delivery.pacto.trianalab.io/apply-ref` | See [Leaving observe](#leaving-observe) |
| `no-observed-digest` | The GitOps source reports no digest | Point the source at a digest-addressed artifact |
| `no-record-store` | The recorder is not ready | Turn on the recorder |
| `needs-verified` | Not enough verified changes yet | Wait |
| `needs-cleared-breaking` | No cleared breaking promote in the window | Approve one breaking change on `auto-nonbreaking` |
| `incident` | A failure or rollback in the window | Wait for N clean changes |
| `partial-graph` | The newest plan could not see the whole graph | Fix the missing sources |

## Changing the level

A level change is an operation of type `delivery.pacto.trianalab.io/autonomy`:

```bash
pacto delivery propose --type delivery.pacto.trianalab.io/autonomy --stage payments-prod --to advise
pacto delivery approve <operation>
```

- One level at a time. A jump of two levels is Rejected.
- `From` must equal the current level, or the request is Rejected as stale.
- Raising needs an Approval from a person other than the raiser, from the team
  the running contract declares. If that contract cannot be read the verdict
  is Unknown.
- A raise passes only when `status.proposal` names exactly the next level and
  the Stage's status still earns it when the raise is planned. Anything else
  is Unknown and needs `accept-unknown`.
- Raising to `auto-nonbreaking` or `autonomous` without `RecordStoreReady=True`
  is Rejected.
- Lowering needs no approval. An agent may not lower to `observe`. The promoter
  never raises, and lowers only one level from an automatic level (the
  step-down below); admission enforces both.
- Lowering to `observe` clears `status.pointer`: GitOps owns the version
  again, and leaving observe adopts what the source then reports.
- The approval is checked again just before the level moves. A proposal that
  lapsed in between sends the request back to a person.

## Step-down

When a promote that shipped with no person on an automatic level (raised by the
promoter or an agent, with no approval) has a confirmed Fail (whether it is
then rolled back or the Stage freezes), the controller raises a `delivery.pacto.trianalab.io/autonomy` operation that
lowers the Stage one level (never below `advise`). It needs no approval and
concludes like any other operation. The proposal window starts again. Unknown
never steps a Stage down.

## Leaving observe

The swap that moves `observe` → `advise` also sets `status.pointer` to the
observed digest, so Pacto owns exactly what was running. Before approving it:

1. Remove the version field from the git manifest. Flux reapplies `spec.ref`
   from git. For Argo CD, add `ignoreDifferences` on
   `/spec/source/targetRevision` with `RespectIgnoreDifferences=true`.
2. Label the applyRef `delivery.pacto.trianalab.io/apply-ref=<stage name>`. From then on,
   admission lets only the promoter and break-glass change the version.

## Records

A `delivery.pacto.trianalab.io/autonomy` operation is signed like any other when the recorder is
on. The record carries the Stage policy, the approver, the `pacto.autonomy`
verdict with the evidence it cited, and `outcome.level` (`{from, to}`). Check it
without Pacto:

```bash
cosign verify-blob-attestation --bundle record.json ...   # prints Verified OK
jq -r .dsseEnvelope.payload record.json | base64 -d | jq .predicate.outcome.level
```

[Check one record without Pacto](verify-a-record.md#check-one-record-without-pacto)
gives the full commands.

Shadow entries are status, not records.
