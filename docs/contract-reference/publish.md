# Publish

`publish` says what a service publishes, where each artifact goes and how it
is built. [`pacto publish`](../publishing.md) reads it and nothing else does. The other
sections are in [Contract sections](sections.md),
[Configuration and policy](configuration-and-policy.md),
[Dependencies and state](dependencies-and-state.md) and
[Readiness](readiness.md).

## `publish`

Optional. Needs `pactoVersion: "2.1"`: a 2.0 contract with a `publish` section
fails validation, and every other 2.0 contract keeps working unchanged. Pacto
versions older than the one that added 2.1 reject a 2.1 contract.

```yaml
pactoVersion: "2.1"
service: {name: payments, version: 2.4.0}  # every tag is this version
publish:
  bundle: oci://ghcr.io/acme/pacto/payments  # the contract bundle
  deploy: manifests                          # the artifact Flux applies
  artifacts:
    - name: api
      type: image
      ref: oci://ghcr.io/acme/payments       # no tag
      build:
        dockerfile: {context: ., platforms: [linux/amd64, linux/arm64]}
      sbom: true
    - name: manifests
      type: manifests
      ref: oci://ghcr.io/acme/deploy/payments
      build:
        command: [./hack/render.sh]          # writes a directory to $PACTO_OUT
  sign: {key: cosign.key}
```

| Field | Meaning |
|---|---|
| `bundle` | Required. The repository the contract bundle goes to, as `oci://host/path` with no tag |
| `deploy` | The name of the `manifests` artifact a Flux Kustomization applies. Pacto adds the `pacto.dev/contract` annotation to it and tags it last |
| `artifacts[].name` | Unique. Later builds see the digest as `PACTO_DIGEST_<NAME>` (upper case, `-` becomes `_`) |
| `artifacts[].type` | `image`, `chart` or `manifests` |
| `artifacts[].ref` | The repository, as `oci://host/path` with no tag |
| `artifacts[].build.dockerfile` | `context`, optional `file` and `platforms`. Runs `docker buildx` into an OCI image layout. Images only |
| `artifacts[].build.command` | A command run in the contract directory that writes the artifact to `$PACTO_OUT`: an OCI image layout for an image, one `.tgz` for a chart, files for manifests |
| `artifacts[].sbom` | Attach an SBOM made by `syft` to the image. Images only |
| `sign` | Exactly one of `keyless: true`, `key` (a cosign key file) or `kms` (a cosign KMS URI). `transparencyLog: false` keeps signatures out of the public log. Publishing refuses without it |

Builders never push. Pacto pushes every artifact itself, never over an existing
tag, signs each digest with `cosign` and stamps `org.opencontainers.image.version`
and `org.opencontainers.image.revision` on each manifest it pushes. A chart is
published but cannot be the deploy artifact: the promoter confirms a release only
through Kustomizations.
