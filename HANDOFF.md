# HANDOFF requests from the core track

## KMS providers are opt-in (commit 5fd3808e)

The default `pacto` build no longer links the AWS, GCP, Azure or Vault KMS
providers. `-tags kms` links all four; `-tags kms_aws`, `kms_gcp`, `kms_azure`
or `kms_hashivault` links one. The pacto-delivery image keeps all four through
`ARG GO_TAGS=kms` in `integrations/kubernetes/Dockerfile` (the default, so no
workflow, kind script or chart value has to change).

1. docs track, `docs/publishing.md` (the `kms:` row of the signing table, line
   about 90): add that the released `pacto` binaries leave cloud KMS out and a
   KMS key needs `go install -tags kms github.com/trianalab/pacto/v4/cmd/pacto@<version>`
   (or one provider's tag); the error says so too.
2. docs track, `integrations/kubernetes/docs/delivery-records.md` (signing
   section): the delivery image ships every KMS provider; `--build-arg GO_TAGS=`
   builds one without them, `GO_TAGS=kms_aws` with one.
3. logic track (chart), `integrations/kubernetes/charts/pacto-delivery/values.yaml`
   comment on `recorder.signing.kms` (and the generated chart README): optional
   note that the default image includes every provider. No value change needed.
4. owner of `pkg/validation/schema/pacto-v2.1.schema.json`: the `publish.sign.kms`
   description says "A cosign KMS key URI"; consider "A KMS key URI (needs a
   pacto build with -tags kms)". Regenerate dependants with the repo targets.
