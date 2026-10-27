# The autonomy ladder

Pacto starts by watching your changes. It earns more freedom one step at a
time, and a person confirms each step. This page explains those steps, called
levels. Words in links are defined in the
[glossary](glossary.md).

## The four levels

Each [Stage](glossary.md#stage) sits on one level.

| Level | What Pacto does | Who moves a version |
|---|---|---|
| **observe** | Works out what it would do for every change and moves nothing | Your GitOps tool, as today |
| **advise** | Shows each new version and what it would do with it | A person asks, or approves what an [AI agent](glossary.md#agent) asked for |
| **auto for non-breaking** | Ships [non-breaking](glossary.md#breaking-change) changes on its own | Pacto, for non-breaking changes; a person for the rest |
| **autonomous** | Ships every change the contracts [clear](glossary.md#clear) on its own | Pacto, unless a consumer would break or a check is [Unknown](glossary.md#unknown) |

Once Pacto moves versions, the same safety rules hold on every level: a change
that could break a [consumer](glossary.md#consumer), or that a check could not
judge, waits for the teams [Why you were asked](pactify-decide.md#why-you-were-asked)
names. The level only decides how often a person is asked when the contracts
say a change is safe.

### Manifests and the levels

The levels judge only the contract. The manifests that ship with it play no
part. A version
with the same contract and new manifests (a new ClusterRoleBinding, say) is
non-breaking, so an automatic level ships it with no person. What those
manifests can do is what the account Flux applies them as may do. Give the
Kustomization a namespaced `serviceAccountName`, as `pacto init --delivery`
does, and keep the key that signs `pacto publish` as safe as the cluster: it
is the root of trust. The Stage's [`Mediated`](glossary.md#mediated) condition is `False` with the
reason `ApplierUnscoped` while the Kustomization runs as Flux itself.

## Cold start: begin by watching

A new Stage starts on **observe**. Connecting a service to delivery with
`pacto init --delivery` (part of [pactifying](glossary.md#pactify) it) always puts it there. Flux or Argo CD keep
doing what they do today, and Pacto changes nothing.

For each version your GitOps tool runs, Pacto writes down what it would have
done: ship it, or hold it and ask a team. Then it runs the checks it would run
after a real release. The Pacto dashboard, a web page, shows "what Pacto would
have done" next to what really happened. These notes are advice only.

Watching also shows what is missing. If Pacto sees a service call yours and
that caller has no contract, Pacto cannot know which versions it accepts. A
breaking change then waits for a person, and Pacto names
the caller so you can give it a contract.

## Earned autonomy: Pacto proposes, a person decides

Pacto proposes the next level when the evidence supports it. By default the
evidence is the last 10 versions on that Stage, and Pacto waits until there
are 10. For a service that rarely releases, the platform team can lower that
number, down to 1:

- **observe to advise**: the versions GitOps ran all passed Pacto's checks.
- **advise to auto for non-breaking**: the versions Pacto moved all passed
  their checks, with no rollback and no failure.
- **auto for non-breaking to autonomous**: the same, plus at least one
  breaking change that the contracts cleared, a person approved and that then
  passed its checks, and no caller seen at runtime without a contract.

When something blocks a proposal, Pacto says what and how to fix it, for
example "this caller has no contract".

A proposal changes nothing by itself. To act on it, someone asks for the
raise in the dashboard or with `pacto delivery propose`, and a different
person of the service's [owner team](glossary.md#owner-team) approves it.
Someone can also ask for a raise Pacto has not proposed;
then the approver must [accept the unknown](glossary.md#accept-the-unknown). A level
goes up one step at a time. Raising a Stage to either automatic level also
needs the [recorder](glossary.md#recorder), so every grant of autonomy leaves a
signed [record](glossary.md#record) that anyone can check without Pacto.

Lowering a level needs no approval. A person can do it at any time. An AI
agent can too, except down to observe: once git stops setting the version
(see [Leaving observe](#leaving-observe)), a Stage on observe has nothing moving it at
all, and only a person may stop a service that way.

## Step-down: one failure costs one level

On every level where Pacto moves versions, a promote that fails its checks
after release is rolled back once or the Stage freezes, as
[What happens to a change](integrations/kubernetes/delivery.md#what-happens-to-a-change)
explains.

If the failed version is one Pacto shipped with no person, the Stage also
drops one level, whether it was rolled back or froze. Autonomous goes back to auto for
non-breaking, and auto for non-breaking goes back to advise. The drop is
recorded like any other change and needs no approval. The count of good
versions starts again from zero, so Pacto has to earn the level back.

A version that shipped with no person and that an AI agent rolls back before
its checks judge it counts as failed too: it is never promoted on that Stage
again and the Stage drops a level. The failure stays on the Stage's record.

An Unknown never causes a step-down, because Unknown means Pacto could not see
enough to judge the version.

## Leaving observe

On observe, your GitOps tool still writes which version runs. To leave it, a
person removes the version from the GitOps manifest in git and labels the
object Pacto then writes, once enough versions passed their checks. Someone
asks for the raise to advise and a second person approves it. Between those
two steps nothing moves the version, so do them promptly.
[Ship the next version](pactify.md#5-ship-the-next-version) gives the
commands, and the [ladder reference](autonomy-reference.md#leaving-observe)
names the field and the label for Flux and Argo CD.

## What's next

- [Pactify a service](pactify.md): give a service a contract and start it on observe
- [Delivery](integrations/kubernetes/delivery.md): what happens to every change
- [Decide, unfreeze and roll back](pactify-decide.md): answer the questions Pacto asks
- [Ladder reference](autonomy-reference.md): the exact rules, fields and commands
