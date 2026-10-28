# Ladder reference

This page lists the rules behind [the autonomy ladder](autonomy.md): its
fields, blockers and commands.

## Levels and what each one changes

| Level (`status.level`) | Automatic promotes | Promote not raised by a person |
|---|---|---|
| `observe` | none | Rejected: GitOps owns the version field |
| `advise` | none | Held for a person (`approve`) |
| `auto-nonbreaking` | `NON_BREAKING` changes only | Held for a person unless the change is `NON_BREAKING` |
| `autonomous` | any plan that proceeds | Same floors as any promote |

On every level the holds in
[Why you were asked](pactify-decide.md#why-you-were-asked) stay, and an
automatic rollback that cannot run freezes the Stage. The level never rejects
rollbacks or [break-glass](glossary.md#break-glass) overrides.

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
  the running contract declares. If Pacto cannot read that contract, the
  verdict is Unknown.
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

The [step-down](autonomy.md#step-down-one-failure-costs-one-level) is a
`delivery.pacto.trianalab.io/autonomy` operation the controller raises when a
promote with no approval (raised by the promoter or an agent) has a confirmed
Fail on an automatic level. It lowers the Stage one level, never below
`advise`, needs no approval and restarts the proposal window.

## Leaving observe

The raise from `observe` to `advise` also sets `status.pointer` to the
observed digest, so Pacto owns exactly what was running. It needs two things
in place first, as [Ship the next version](pactify.md#5-ship-the-next-version)
does step by step:

| Applier | Version field git must stop setting |
|---|---|
| Flux `OCIRepository` | `spec.ref`, removed from the git manifest |
| Argo CD `Application` | `/spec/source/targetRevision`, under `ignoreDifferences` with `RespectIgnoreDifferences=true` |

The applyRef carries the label `delivery.pacto.trianalab.io/apply-ref=<stage name>`. From then on,
admission lets only the promoter and break-glass change the version.

## Records

A `delivery.pacto.trianalab.io/autonomy` operation is signed like any other when the recorder is
on. The record carries the Stage policy, the approver, the `pacto.autonomy`
verdict with the evidence it cited, and `outcome.level` (`{from, to}`).
[Check one record without Pacto](verify-a-record.md#check-one-record-without-pacto)
gives the commands; read `.predicate.outcome.level` from the payload.

Shadow entries are status, not records.
