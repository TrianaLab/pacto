---
"@pacto/k8s-module": minor
---

The delivery promoter promotes only versions `pacto publish` published.

A Stage's candidate `OCIRepository` must set `spec.verify` naming the
publisher: a `secretRef` with its public key or a `matchOIDCIdentity` for
keyless signatures. The promoter records each digest Flux verified there in the
new `status.published`, and auto-promotion offers only those. A promote of
anything else, an agent's included, concludes `Rejected` with `NotPublished`; a
Stage with no candidate source, or whose `spec.verify` names no identity,
promotes nothing and says so: its new `Ready` condition is `False`, with the
reason `CandidateUnverified`, `CandidateNotFound` or `NoCandidateSource`, and
`kubectl get stage` shows it. A promote that passed the published gate on a
Stage and verified there is recorded in the new `status.offered`, and a
downstream Stage takes only what its upstream Stage offers. A version a person
let through by accepting an unknown published verdict is never offered on.
An upstream Stage that does not exist or that the promoter may not
read offers nothing, and any other failed read is retried. Versions verified
before this release are not offered downstream until they are promoted again.

A failed list of Operations while taking the Stage lock, admitting a rollback
or checking for a newer promote is now an error the reconcile retries, never
"no operations": a lock read as free could let two promotes in, and a valid
rollback could be refused as unbound.

Apply the delivery CRDs before `helm upgrade pacto-delivery`: the Stage CRD
gains `status.published`, `status.offered` and `status.conditions`.
