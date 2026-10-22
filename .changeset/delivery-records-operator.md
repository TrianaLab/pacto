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
and remove the record finalizer. A record can be late, never skipped. The
recorder adopts a record it already wrote for the same conclusion, so an etcd
restore continues the stream instead of forking it. An operation that
concludes again after a restore gets a second record. The recorder stops
writing a
stream with two records under one number, a record it cannot verify or a head
the registry does not hold. Each Stage's `RecordStoreReady` condition says
whether records are being written, and `ReferrersUnsupported` means the
registry has no Referrers API: records are unsupported there. The metric
`pacto_delivery_records_pending` shows the lag.

The `pacto-delivery` chart gains `recorder.*`, off by default: the streams
repository, a Docker config Secret for push and one of an Ed25519 key Secret,
a KMS key URI or keyless signing. Keyless signing presents a projected service
account token for the `sigstore` audience to the Fulcio in
`recorder.signing.keyless.fulcioURL`, dates each signature with the timestamp
authority in `timestampURL` and needs `trustedRoot`, the Sigstore trusted root
naming both, which the recorder also publishes as its own trust entry. With the recorder on, the promoter
gets `--records` and leaves the record finalizer to it.

The recorder names each stream's anchor in `status.stream.anchor`, verifies a
stream against its own key plus the keys `recorder.trust` lists before it
extends it, and can post a CDEvent for every record to `recorder.cdeventsSink`.
A stream in conflict freezes its Stage until it is repaired and a person
unfreezes it. With the recorder on, the promoter deletes a recorded operation
after `recorder.operationTTL` (a week by default), never the one a Stage runs,
and its role gains `delete` on operations for that. An admission policy keeps
`status.record` and the stream's `recordedSeq`, `head` and `anchor` to the
recorder, and keeps the recorder away from what runs: the locks, numbers and
conclusions a Stage keeps and the verdicts, approvals and policy of an
operation. Missing numbers all wait one grace period together. An operation
left over from a deleted Stage is recorded under that Stage, never the one
recreated with its name. The delivery image is now built with `--target delivery`. The
recorder signs with sigstore-go, so the image carries no cosign. Apply the delivery CRDs
before `helm upgrade pacto-delivery`: the Stage and Operation CRDs gain the
record fields.
