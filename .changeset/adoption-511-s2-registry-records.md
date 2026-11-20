---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Keep approvals and delivery records in the registry on every registry. The
recorder signs an `operation-intent/v1` record for every admitted Operation
and an `approval/v1` record for every Approval made in the cluster, on behalf
of the person admission verified. With `delivery.identities.signers` set,
`pacto delivery approve` signs the decision keyless after a fresh login and
pushes it to the Stage's approvals repository
(`delivery.recorder.approvalsRepository`); it refuses while
`SIGSTORE_ID_TOKEN` or a CI token is set. With the recorder on, an approval
counts only once its record is written, and a person's signed approval only
once the promoter verified it. The recorder anchors the approvals repository
only once a signer is set, and a failure there never stops operation
records. `pacto delivery record
get/verify <ref>` and `approve --intent <ref>` read records with no
kubeconfig, and the stdio MCP record tools take `ref`; MCP still has no
approve tool. On registries without the OCI Referrers API, approval and
intent records use write-once tags, and publisher signatures and SBOMs also
get write-once `pacto-sig-<subject hex>-<12 hex>` tags that Pacto reads in
place of the shared `sha256-<hex>` index. A recorded `native` registry mode
can no longer narrow at admission.

Upgrade order: operation records now name each approval's record, and earlier
readers decode records strictly. Upgrade the CLIs and every promoter that
follows these streams before the recorder.
