# Publish

`publish` says what a service publishes, where each artifact goes and how it
is built. [`pacto publish`](../publishing.md) reads it and nothing else does. The other
sections are in [Contract sections](sections.md),
[Configuration and policy](configuration-and-policy.md),
[Dependencies and state](dependencies-and-state.md) and
[Readiness](readiness.md).

## `publish`

Optional. Needs `pactoVersion: "2.1"`: a 2.0 contract with a `publish` section
fails validation with `NEEDS_PACTO_2_1`, and every other 2.0 contract keeps
working unchanged. Pacto versions older than the one that added 2.1 reject a
2.1 contract, and that includes your consumers: once you publish a 2.1 bundle,
a consumer whose Pacto or operator predates 2.1 cannot resolve it as a
dependency, though it changed nothing. Have your consumers upgrade first.

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
| `deploy` | The name of the `manifests` artifact a Flux Kustomization applies. Pacto adds the `pacto.dev/contract` annotation to it and tags it last |
| `artifacts[].name` | Unique. Later builds see the digest as `PACTO_DIGEST_<NAME>` (upper case, `-` becomes `_`) |
| `artifacts[].type` | `image`, `chart` or `manifests` |
| `artifacts[].ref` | The repository, as `oci://host/path` with no tag |
| `artifacts[].build.dockerfile` | `context`, optional `file` and `platforms`. Runs `docker buildx` into an OCI image layout. Images only |
| `artifacts[].build.command` | A command run in the contract directory that writes the artifact to `$PACTO_OUT`: an OCI image layout for an image, one `.tgz` for a chart, files for manifests |
| `artifacts[].sbom` | The SPDX (`.spdx.json`) or CycloneDX (`.cdx.json`) JSON file the build writes, relative to the contract directory. Pacto attaches it to the image and signs it; it never makes one. The image's build command must write it (for example `syft dir:. -o spdx-json=sbom/api.spdx.json` inside the command): a file older than the build is refused, and so is `sbom` on a `dockerfile` build, which writes none. A path inside the contract directory must be in `.pactoignore` (`publish --plan` names the line). Images only |
| `sign` | Exactly one of `keyless: true`, `key` (a PEM key file, such as a cosign key) or `kms` (a KMS key URI, which needs a `pacto` built with `-tags kms`). `transparencyLog: false` keeps signatures out of the public log (`pacto init --no-transparency-log` writes it); keyless signing refuses it. Publishing refuses without `sign` |
| `sign.identity`, `sign.issuer` | Required with `keyless: true`: the certificate identity the signature carries and its OIDC issuer. Pacto keeps or adopts only what this identity signed |

Builders never push. Pacto pushes every artifact itself, never over an existing
tag, signs each digest with Sigstore and stamps `org.opencontainers.image.version`
and `org.opencontainers.image.revision` on each manifest it pushes. A chart is
published but cannot be the deploy artifact: the promoter confirms a release only
through Kustomizations.
