# The autonomy ladder

Pacto does not start by shipping your changes. It starts by watching. It earns
more freedom one step at a time, and a person says yes to each step. This page
explains those steps, called rungs, in plain words.

You need the delivery part of Pacto for this. If you have not met it yet, read
[Delivery](integrations/kubernetes/delivery.md) first.

## The four rungs

Each environment of each service (a **Stage**) sits on one rung.

| Rung | What Pacto does | Who moves a version |
|---|---|---|
| **observe** | Works out what it would do for every change and moves nothing | Your GitOps tool, as today |
| **advise** | Shows each new version and its plan | A person asks, or approves what an agent asked for |
| **auto for non-breaking** | Ships changes that break no one on its own | Pacto, for non-breaking changes; a person for the rest |
| **autonomous** | Ships every change the contracts clear on its own | Pacto, unless a consumer would break or something is Unknown |

No rung skips the safety rules. On every rung, a change that would break a
consumer waits for that consumer's team, and a check that cannot see enough
(Unknown) holds the change for a person. The rung only decides how often a
person is asked when the contracts say a change is safe.

## Cold start: begin by watching

A new Stage starts on **observe**. Pactifying a service always puts it there.
Flux or Argo CD keep doing what they do today, and Pacto changes nothing.

For each version your GitOps tool runs, Pacto writes down what it would have
done: ship it, hold it for a team, or ask a question. It then runs the same
checks it would run after a real release. The dashboard shows "what Pacto
would have done" next to what really happened. These notes are advice. They
are not signed records.

Watching also shows what is missing. If Pacto sees a service call yours at
runtime and that caller has no contract, it treats the caller as a consumer it
cannot judge. A breaking change then waits for a person instead of shipping,
and Pacto names the caller so you can give it a contract with `pacto init`.

## Earned autonomy: Pacto proposes, a person decides

Pacto proposes the next rung when the evidence supports it. By default the
evidence is the last 10 changes on that Stage:

- **observe to advise**: the versions GitOps ran all passed Pacto's checks.
- **advise to auto for non-breaking**: the versions Pacto promoted all passed
  their checks, with no rollback and no failure.
- **auto for non-breaking to autonomous**: the same, plus at least one
  breaking change that the contracts cleared, a person approved and that then
  passed its checks.

When something blocks a proposal, Pacto says what and how to fix it, for
example "this caller has no contract" or "this consumer declares no version
range".

A rung goes up only through a request that a different person approves. It
goes up one rung at a time. The owners of the service approve it. Raising a
Stage to either automatic rung needs the recorder, so every grant of autonomy
leaves a signed record you can check without Pacto.

Lowering a rung needs no approval. A person can do it at any time. An AI
agent can too, but never down to observe.

## Step-down: one failure costs one rung

If a version that Pacto shipped on its own fails its checks and Pacto rolls it
back, the Stage also drops one rung. Autonomous goes back to auto for
non-breaking, and auto for non-breaking goes back to advise. The drop is
recorded like any other change and needs no approval. The count of good
changes starts again from zero, so Pacto has to earn the rung back.

An Unknown never causes a step-down. Unknown means Pacto could not see, not
that the version failed.

## Leaving observe

On observe, your GitOps tool still writes which version runs. Before Pacto can
take over that one field, a person does two things:

1. Removes the version field from the manifest in git, so git stops setting it.
2. Adds a label to that object, so only Pacto can change the version from then
   on.

Pacto proposes advise only once the label is in place. The
[ladder reference](autonomy-reference.md) has the exact steps for Flux and
Argo CD.

## Where to go next

- [Pactify a service](pactify.md): give a service a contract and start it on observe
- [Decide, unfreeze and roll back](pactify-decide.md): answer the questions Pacto asks
- [Ladder reference](autonomy-reference.md): the exact rules, fields and commands
- [Glossary](glossary.md): every word on this page
