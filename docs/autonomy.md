# The autonomy ladder

Pacto does not start by shipping your changes. It starts by watching. It earns
more freedom one step at a time, and a person says yes to each step. This page
explains those steps, called rungs, in plain words.

## Words you need first

- **Pacto** decides which version of each of your services runs where. Flux
  or Argo CD, the tools that keep a Kubernetes cluster in line with what you
  ask, still do the switch. This part of Pacto is called delivery, and you
  install it separately.
- A **contract** is a file each service keeps. It says who owns the service
  and which other services it calls, with the versions of them it accepts,
  for example "checkout accepts payments 2.x".
- A **consumer** is a service that calls another one. Checkout is a consumer
  of payments.
- A **breaking change** removes or changes something consumers may use.
  Payments 3.0.0 that removes a field is breaking.
- The contracts **clear** a change when every consumer accepts the new
  version. If checkout's contract says it accepts payments 3.x, Pacto knows
  payments 3.0.0 is safe for it, even though the change is breaking.
- A **check** is a test Pacto runs on a version: was it published and signed,
  does the running service do what its contract says, are error rates normal.
  Each check answers Pass, Fail or **Unknown**, meaning it could not see
  enough to say, for example because monitoring data is missing.
- A **Stage** is one service in one environment, such as payments in prod.

## The four rungs

Each Stage sits on one rung.

| Rung | What Pacto does | Who moves a version |
|---|---|---|
| **observe** | Works out what it would do for every change and moves nothing | Your GitOps tool, as today |
| **advise** | Shows each new version and what it would do with it | A person asks, or approves what an AI agent asked for |
| **auto for non-breaking** | Ships changes that are not breaking on its own | Pacto, for non-breaking changes; a person for the rest |
| **autonomous** | Ships every change the contracts clear on its own | Pacto, unless a consumer would break or a check is Unknown |

Once Pacto moves versions, the safety rules hold on every rung. A change that
would break a consumer waits for that consumer's team. A change with an
Unknown check waits for a person. The rung only decides how often a person is
asked when the contracts say a change is safe.

## Cold start: begin by watching

A new Stage starts on **observe**. Giving a service a contract with
`pacto init` (called pactifying it) always puts it there. Flux or Argo CD keep
doing what they do today, and Pacto changes nothing.

For each version your GitOps tool runs, Pacto writes down what it would have
done: ship it, or hold it and ask a team. Then it runs the checks it would run
after a real release. The Pacto dashboard, a web page, shows "what Pacto would
have done" next to what really happened. These notes are advice only.

Watching also shows what is missing. If Pacto sees a service call yours and
that caller has no contract, Pacto cannot know which versions it accepts. A
breaking change then waits for a person instead of shipping, and Pacto names
the caller so you can give it a contract.

## Earned autonomy: Pacto proposes, a person decides

Pacto proposes the next rung when the evidence supports it. By default the
evidence is the last 10 versions on that Stage, and Pacto waits until there
are 10. For a service that rarely releases, the platform team can lower that
number, down to 1:

- **observe to advise**: the versions GitOps ran all passed Pacto's checks.
- **advise to auto for non-breaking**: the versions Pacto moved all passed
  their checks, with no rollback and no failure.
- **auto for non-breaking to autonomous**: the same, plus at least one
  breaking change that the contracts cleared, a person approved and that then
  passed its checks.

When something blocks a proposal, Pacto says what and how to fix it, for
example "this caller has no contract".

A proposal changes nothing by itself. To act on it, someone asks for the
raise in the dashboard or with `pacto delivery propose`, and a different
person approves it. Someone can also ask for a raise Pacto has not proposed;
then the approver must accept that the evidence is missing (accept the
unknown). That person
must belong to the team that owns the service, as its contract says. A rung
goes up one step at a time. Raising a Stage to either automatic rung also
needs the recorder, the part of Pacto that signs a record of each decision
and stores it next to your images. So every grant of autonomy leaves a
signed record that anyone can check with standard tools, without Pacto.

Lowering a rung needs no approval. A person can do it at any time. An AI
agent can too, but never down to observe: once git no longer sets the version
(see "Leaving observe" below), a Stage on observe has nothing moving it at
all, and only a person may stop a service that way.

## Step-down: one failure costs one rung

On every rung where Pacto moves versions, a version that fails its checks
after release is rolled back to the version that ran before, with no one
asked. If there is no safe version to go back to, or the rollback itself
fails, the Stage freezes instead: nothing moves until a person rolls back or
unfreezes it.

If the failed version is one Pacto shipped on its own, the Stage also drops
one rung. Autonomous goes back to auto for
non-breaking, and auto for non-breaking goes back to advise. The drop is
recorded like any other change and needs no approval. The count of good
versions starts again from zero, so Pacto has to earn the rung back.

An Unknown never causes a step-down. Unknown means Pacto could not see, not
that the version failed.

## Leaving observe

On observe, your GitOps tool still writes which version runs. Before Pacto can
take over that one field, a person does two things:

1. Removes the version from the GitOps manifest in git, so git stops setting it.
2. Adds a label to that manifest's object in the cluster, so only Pacto can
   change the version from then on.

Pacto proposes advise only once the label is in place. The
[ladder reference](autonomy-reference.md) names the field and the label for
Flux and Argo CD.

## Where to go next

- [Pactify a service](pactify.md): give a service a contract and start it on observe
- [Delivery](integrations/kubernetes/delivery.md): what happens to every change
- [Decide, unfreeze and roll back](pactify-decide.md): answer the questions Pacto asks
- [Ladder reference](autonomy-reference.md): the exact rules, fields and commands
- [Glossary](glossary.md): every word on this page
