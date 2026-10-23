---
"@pacto/core": minor
---

The cloud KMS signing providers are optional. The default `pacto` build signs
and verifies with a key file or keyless and leaves the AWS, GCP, Azure and Vault
SDKs out, which takes the linux/amd64 release binary from 75.3 MB to 64.3 MB. A
KMS key in a build without its provider fails with an error that names the
build to use: `go install -tags kms github.com/trianalab/pacto/v4/cmd/pacto@<version>`
links all four, and `-tags kms_aws`, `kms_gcp`, `kms_azure` or `kms_hashivault`
links one. The pacto-delivery image keeps every provider (build argument
`GO_TAGS=kms`, the default); `--build-arg GO_TAGS=` builds it without them.
