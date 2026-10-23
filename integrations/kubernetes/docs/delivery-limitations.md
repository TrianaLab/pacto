# What delivery does not do

The limits of [Pacto delivery](delivery.md), in one place.

## Scope

- Flux applies by default. Argo CD can apply a Stage instead, and a Stage can
  follow a Stage in another cluster through its signed records. Both have
  limits, listed in [Argo CD and several clusters](delivery-reach.md).
- The four built-in operation types are the only ones. External types come
  later.
- Compatibility comes from the version range each consumer declares. Pacto
  does not know which fields or endpoints a consumer uses, and the calls it
  observes say only which service calls which.
- Stage approval policy is `approval.always`, not an expression.
- Flux checks the signatures, so a digest it never verified on the candidate
  source cannot be promoted, even if `pacto publish` signed it.

## Placing a Pacto in an environment

Pacto places a Pacto by its `delivery.pacto.trianalab.io/environment` label or
by the namespace a Stage's Kustomization applies into. A Pacto in a namespace
that a Stage of one environment applies into is placed there, even if you
deployed it there for another environment by other means: label it. A
Kustomization with no `targetNamespace` places nothing, so the Pactos it
applies count in every environment.

## Without the recorder

- History reads the Operations still in the cluster, so it is partial.
- An Operation keeps its record finalizer until it is deleted; the promoter
  then lets it go.

## Records

- A keyless record is checked against its certificate's identity, issuer and
  chain. With Rekor off there is no signed time, so the chain is checked at
  the moment the certificate was issued.
- Deleting a Stage while one of its changes runs ends that change `Errored`
  with no stream number, so it gets no record even though it moved the Stage.
  Wait for a Stage's changes to conclude before you delete it.
- The recorder notices a tampered record or a broken chain only when it next
  records on that Stage. `pacto delivery record verify` checks any time.
- A Stage that follows another cluster sees the upstream's history only as far
  as its records reach the registry. A failure whose record is not written yet,
  or a stream cut at its newest records, is not seen there.
- A check after release that fails and freezes the upstream Stage without a
  rollback writes no record, so a Stage in another cluster does not see that
  failure and can still take that version.
- GitHub's registry and released `distribution` have no native Referrers API,
  so they hold no records.

## Identity

- An agent holding a person's credentials, live session, refresh token or
  password is that person to the cluster. Fresh sign-in raises the bar; it
  does not close the gap.
- A stolen dashboard session can approve only in the ten minutes after that
  person signed in again, and can take the person's other actions until it
  ends.
- Sessions live in the dashboard's memory: a restart signs everyone out, and
  the dashboard runs as one replica. A session ends after an hour without use
  or twelve hours in all, and the dashboard revokes its refresh token at the
  provider within fifteen minutes of that.

## Dashboard

- A change's timeline shows only the times on the Operation, not Kubernetes
  events. The services it affects link to their own pages on the operational
  graph rather than a graph inside the change.
- Published versions are the ones your Stages know about. A version no Stage
  has seen yet is not listed.
- Refused publishes are read from the contract repository of each service with
  a Stage, and only refusals of the version rule are kept there. The dashboard
  does not check their signatures, so the list is what the registry holds. A
  service whose Stages know no version yet shows none and makes the list
  incomplete.
