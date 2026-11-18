# Publish

`publish` says what a service publishes, where each artifact goes and how it
is built. Only [`pacto publish`](../publishing.md) and
[`pacto ci --publish`](../publish-from-ci.md) read it. Leave it out and
`pacto ci --publish` derives one from the folder. The other
sections are in [Contract sections](sections.md),
[Configuration and policy](configuration-and-policy.md),
[Dependencies and state](dependencies-and-state.md) and
[Readiness](readiness.md).

## `publish`

Optional. Needs `pactoVersion: "2.1"`: a 2.0 contract with a `publish` section
fails validation with `NEEDS_PACTO_2_1`. Every other 2.0 contract keeps working
unchanged. Pacto core older than 4.1.0 and operator older than 6.1.0 reject a
2.1 contract, and that includes your consumers. Once you publish a 2.1 bundle,
a consumer on an older Pacto or operator fails to resolve it as a
dependency. Have your consumers upgrade first.

```yaml
pactoVersion: "2.1"
service: {name: payments, version: 2.4.0}  # every tag is this version
publish:
  bundle: oci://ghcr.io/acme/pacto/payments  # the contract bundle
  deploy: manifests                          # the artifact an applier deploys
  artifacts:
    - name: api
      type: image
      ref: oci://ghcr.io/acme/payments       # no tag
      build:
        command: [./hack/build-image.sh]     # an OCI layout to $PACTO_OUT, and the SBOM
      sbom: sbom/api.spdx.json               # sbom/ is listed in .pactoignore
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
| `deploy` | The name of the `manifests` or `chart` artifact any applier deploys. Pacto adds the `delivery.pacto.trianalab.io/contract` annotation to it and tags it last |
| `artifacts[].name` | Unique. Later builds see the digest as `PACTO_DIGEST_<NAME>` (upper case, `-` becomes `_`) |
| `artifacts[].type` | `image`, `chart` or `manifests` |
| `artifacts[].ref` | The repository, as `oci://host/path` with no tag |
| `artifacts[].build` | Deprecated in `pactoVersion: "2.5"`, where it is optional: leave it out and `pacto ci --publish` detects the build, the folder's build tool for an image and Pacto's own assembly for manifests or a chart. A 2.5 contract that declares it gets a `PUBLISH_BUILD_DEPRECATED` warning; an older contract without it fails with `NEEDS_PACTO_2_5` |
| `artifacts[].build.dockerfile` | `context`, optional `file` and `platforms`. Runs `docker buildx` into an OCI image layout. Images only. Deprecated in 2.5 |
| `artifacts[].build.command` | A command run in the contract directory that writes the artifact to `$PACTO_OUT`: an OCI image layout for an image, one `.tgz` for a chart, files for manifests |
| `artifacts[].sbom` | The SPDX (`.spdx.json`) or CycloneDX (`.cdx.json`) JSON file the build writes, relative to the contract directory. Pacto attaches it to the image and signs it. The image's build command must write it, for example with `syft dir:. -o spdx-json=sbom/api.spdx.json` inside the command. Pacto refuses a file older than the build, and refuses `sbom` on a `dockerfile` build because that build writes no SBOM. A path inside the contract directory must be in `.pactoignore` (`publish --plan` names the line). Images only |
| `sign` | Exactly one of `keyless: true`, `key` (a PEM key file, such as a cosign key) or `kms` (a KMS key URI, which needs a `pacto` built with `-tags kms`). `transparencyLog: false` keeps signatures out of the public log (`pacto init --from <golden path> --no-transparency-log` writes it); keyless signing refuses it. Optional: without `sign`, Pacto signs keyless as the identity the CI's OIDC token names ([Signing](../publish-from-ci.md#signing)) |
| `sign.identity`, `sign.issuer` | Required with `keyless: true`: the certificate identity the signature carries and its OIDC issuer. Pacto keeps or adopts only what this identity signed |

Builders write to `$PACTO_OUT` and Pacto does all the pushing. It pushes every
artifact to a new tag, signs each digest with Sigstore and stamps
`org.opencontainers.image.version` and `org.opencontainers.image.revision` on
each manifest it pushes. `deploy` names a `manifests` or `chart` artifact,
never an image.
