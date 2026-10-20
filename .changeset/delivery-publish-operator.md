---
"@pacto/k8s-module": minor
---

The delivery promoter promotes only versions `pacto publish` published.

A Stage's candidate `OCIRepository` must set `spec.verify`. The promoter
records each digest Flux verified there in the new `status.published`, and
auto-promotion offers only those. A promote of anything else, an agent's
included, concludes `Rejected` with `NotPublished`; a Stage with no candidate
source, or one whose source has no `spec.verify`, promotes nothing. A
downstream Stage takes only what its upstream Stage verified.

A failed list of Operations while taking the Stage lock, admitting a rollback
or checking for a newer promote is now an error the reconcile retries, never
"no operations": a lock read as free could let two promotes in, and a valid
rollback could be refused as unbound.

Apply the delivery CRDs before `helm upgrade pacto-delivery`: the Stage CRD
gains `status.published`.
