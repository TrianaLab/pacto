---
"@pacto/k8s-module": minor
---

Add the delivery recorder, which writes a signed record of every operation
that ends to the registry.

`delivery recorder` runs as its own Deployment with its own ServiceAccount,
and it is the only holder of the signing key and the registry push
credentials. For each Stage it writes records in order: it reads the stream
from its anchor in the streams repository, chains each new record from the
newest one there, attaches it to the version the Stage moved to, the version
it moved from, the contract of the new version and the anchor, and confirms
the Referrers API lists it. Only then does it advance
`status.stream.recordedSeq` and `head`, set `status.record` on the operation
and remove the record finalizer. A record can be late, never skipped. An
operation that already has a record is not written twice, so an etcd restore
continues the stream instead of forking it, and the recorder stops writing a
stream with two records under one number, a record it cannot verify or a head
the registry does not hold. Each Stage's `RecordStoreReady` condition says
whether records are being written, and `ReferrersUnsupported` means the
registry has no Referrers API: records are unsupported there. The metric
`pacto_delivery_records_pending` shows the lag.

The `pacto-delivery` chart gains `recorder.*`, off by default: the streams
repository, a Docker config Secret for push and one of an Ed25519 key Secret,
a cosign KMS key URI or keyless signing. With the recorder on, the promoter
gets `--records` and leaves the record finalizer to it. The promoter's role
does not grow. The delivery image is now built with `--target delivery` and
carries cosign 2.6.5 for KMS and keyless signing. Apply the delivery CRDs
before `helm upgrade pacto-delivery`: the Stage and Operation CRDs gain the
record fields.
