---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

pacto ci --publish keeps the owner and the accepted ranges a person declared
in the registry with pacto declare until the repository's pacto.yaml writes
them. The owner is carried only when the owner rule accepts the declaration,
and a range only when an owner of the service signed it. A counted
declaration is the baseline the next derived version is measured from, so
the first CI publish after a declaration no longer stops at no verifiable
baseline. The published pacto.inferred.yaml cites the declaration under
inputs.declaration, and the plan prints and returns (carried, in JSON) each
carried, overridden or dropped field. A plain pacto publish carries nothing
and says which declared fields the new version leaves out. The fleet field
provenance of a carried field names the declaration it was carried from.
pkg/publish gains VerifiedBaseline, FollowsBaseline, NotCounted and
Derived.BaselineRecord, and DeriveInput.Verify receives the tag.
