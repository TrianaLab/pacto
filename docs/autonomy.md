# The autonomy ladder

Pacto does not start by shipping your changes. It starts by watching. It earns
more freedom one step at a time, and a person says yes to each step. This page
explains those steps, called levels, in plain words.

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
- An **AI agent**, such as Claude connected to Pacto, can ask for a change
  but can never approve one.

## The four levels

Each Stage sits on one level.

| Level | What Pacto does | Who moves a version |
|---|---|---|
| **observe** | Works out what it would do for every change and moves nothing | Your GitOps tool, as today |
| **advise** | Shows each new version and what it would do with it | A person asks, or approves what an AI agent asked for |
| **auto for non-breaking** | Ships changes that are not breaking on its own | Pacto, for non-breaking changes; a person for the rest |
| **autonomous** | Ships every change the contracts clear on its own | Pacto, unless a consumer would break or a check is Unknown |

Once Pacto moves versions, the safety rules hold on every level. A change that
would break a consumer waits for that consumer's team, and one that breaks
consumers of several teams waits for all of them. So does a rollback a person
or agent asks for to a version a consumer has moved past. A version that
changes who owns the service (the team, or the DRI when no team is named)
waits for the team that owns the service now, so the author of a version never
picks who approves it. A version that only edits the owner's contacts does not
wait. When the running contract
cannot be read, no team is asked, and only break-glass can answer if the
promoter requires an owner team. A change with an Unknown check waits for the
team that owns the service. The level only decides how often a person is asked
when the contracts say a change is safe.

Consumers declare the versions they accept as ranges, and Pacto checks each
range against the version the change is published as. It also checks that
version against the change: a breaking change published as a minor (1.9.0
after 1.8.0) would pass every `^1` range, so it waits for a person. So does a
version whose contract moved to another registry domain, because consumers
declared against the old domain would not be counted.

### What the levels do not judge

The levels judge the contract, never the manifests that ship with it. A version
with the same contract and new manifests (a new ClusterRoleBinding, say) is
non-breaking, so an automatic level ships it with no person. What those
manifests can do is what the account Flux applies them as may do. Give the
Kustomization a namespaced `serviceAccountName`, as `pacto init --delivery`
does, and keep the key that signs `pacto publish` as safe as the cluster: it
is the root of trust. The Stage's `Mediated` condition is `False` with the
reason `ApplierUnscoped` while the Kustomization runs as Flux itself.

## Cold start: begin by watching

A new Stage starts on **observe**. Connecting a service to delivery with
`pacto init --delivery` (part of pactifying it) always puts it there. Flux or Argo CD keep
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
person approves it. Someone can also ask for a raise Pacto has not proposed;
then the approver must accept that the evidence is missing (accept the
unknown). The approver must belong to the team that owns the service, as its
contract says. A level
goes up one step at a time. Raising a Stage to either automatic level also
needs the recorder, the part of Pacto that signs a record of each decision
and stores it next to your images. So every grant of autonomy leaves a
signed record that anyone can check with standard tools, without Pacto.

Lowering a level needs no approval. A person can do it at any time. An AI
agent can too, but never down to observe: once git no longer sets the version
(see "Leaving observe" below), a Stage on observe has nothing moving it at
all, and only a person may stop a service that way.

## Step-down: one failure costs one level

On every level where Pacto moves versions, a promote that fails its checks
after release is rolled back once to the version that ran before, whoever
asked for it. The Stage freezes instead when there is nothing safe to go back
to, the rollback itself fails or someone chose the running version by hand (a
rollback, or a promote during auto-hold: the pause a hand rollback sets so
automation does not undo it). Then nothing moves until a person rolls back or
unfreezes it.

If the failed version is one Pacto shipped with no person, the Stage also
drops one level, whether it was rolled back or froze. Autonomous goes back to auto for
non-breaking, and auto for non-breaking goes back to advise. The drop is
recorded like any other change and needs no approval. The count of good
versions starts again from zero, so Pacto has to earn the level back.

A version that shipped with no person and that an AI agent rolls back before
its checks judge it counts as failed too: it is never promoted on that Stage
again and the Stage drops a level, so an agent cannot pull its own failing
change and keep its record clean.

An Unknown never causes a step-down. Unknown means Pacto could not see, not
that the version failed.

## Leaving observe

On observe, your GitOps tool still writes which version runs. Leaving observe
goes in this order:

1. Keep deploying through GitOps as before until the checks have passed on
   enough versions it applied (10 by default). The Stage's proposal lists
   what is still missing.
2. A person removes the version from the GitOps manifest in git, so git stops
   setting it, and adds a label to that manifest's object in the cluster, so
   only Pacto can change the version from then on. Pacto then proposes advise.
3. Someone asks for the raise and a second person approves it. Do it
   promptly: between step 2 and the raise, nothing moves the version.

The [ladder reference](autonomy-reference.md) names the field and the label for
Flux and Argo CD.

## Where to go next

- [Pactify a service](pactify.md): give a service a contract and start it on observe
- [Delivery](integrations/kubernetes/delivery.md): what happens to every change
- [Decide, unfreeze and roll back](pactify-decide.md): answer the questions Pacto asks
- [Ladder reference](autonomy-reference.md): the exact rules, fields and commands
- [Glossary](glossary.md): every word on this page
