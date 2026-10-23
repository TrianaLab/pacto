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
4. Done by the core track in the SBOM commit below: the `publish.sign.kms`
   schema description now says it needs a `-tags kms` build.

## No git or syft programs in `pacto publish`

The source revision is read from `.git` (HEAD, loose refs, packed-refs, a
worktree's or submodule's gitdir file) with no `git` program. `syft` is gone:
`publish.artifacts[].sbom` is now a path (string, `.spdx.json` or `.cdx.json`,
relative to the contract directory) to the SBOM file the build writes. Pacto
checks the file is that format, attaches it as a referrer
(`application/spdx+json` or `application/vnd.cyclonedx+json`) and signs it; it
never makes one. A build that writes into the contract directory after the plan
(an SBOM not listed in `.pactoignore`) now fails with "the contract directory
changed while publishing" instead of a registry digest error. `docker buildx`,
`gh` and `docker-credential-*` stay.

5. docs track, `docs/contract-reference/publish.md`: the example's
   `sbom: true` becomes `sbom: sbom/api.spdx.json`, and the `artifacts[].sbom`
   row becomes "The SPDX (`.spdx.json`) or CycloneDX (`.cdx.json`) JSON file the
   build writes, relative to the contract directory. Pacto attaches it to the
   image and signs it; it never makes one. The image's build command must write
   it (for example `syft dir:. -o spdx-json=sbom/api.spdx.json` inside the
   command): a file older than the build is refused, and so is `sbom` on a
   `dockerfile` build, which writes none. A path inside the contract directory
   must be in `.pactoignore` (`publish --plan` names the line). Images only".
6. docs track, `docs/publishing.md` line 67 ("attaches an SBOM made by `syft`")
   and line 77 (the list of needed programs): Pacto attaches the declared SBOM
   file; syft is no longer needed, and the revision comes from `.git` without
   git. A file in `sbom/` is also what `pacto diff` reads for package changes,
   so `sbom/<name>.spdx.json` serves both.
7. ci track (optional, not required by this change): the release workflows
   still install syft to make the CLI release's own SBOM
   (`release/orchestrator/build-cli.sh`, `ci.yml`, `release.yml`, `pacto.yml`).
   That is the release pipeline, not Pacto at runtime, so it is left as is.
8. dash track and integrator: the `sbom` type change reaches the dashboard's
   generated OpenAPI (`Contract.PublishArtifact.sbom` boolean to string). The
   core branch regenerated it with `make generate-dashboard-sdk` (one line in
   each of `pkg/dashboard/frontend/src/lib/generated/openapi.json`,
   `.../schema.ts` and `examples/demo/pacto-dashboard/interfaces/openapi.json`);
   no frontend code reads the field, so the UI bundle is unchanged. On a merge
   conflict, rerun `make generate-dashboard-sdk` rather than hand-merging.

## `pacto init` pactifies (ledger B)

`pacto init [dir]` (default: the current folder) writes a blank contract into
an empty or new folder, as before, and no longer fails on an existing one. A
folder with a Dockerfile or Kubernetes manifests gets a contract read from
them (publish section included) and a report: Found, Depends on, Used by
(`--fleet <dir of contracts>`), Unknown, Next. `--delivery` adds
`delivery/pacto.yaml` (the Pacto resource, shipped inside every version: the
manifests build fills in `@PACTO_VERSION@`) and `delivery/<env>.yaml` per
`--env` (default test, prod): candidate OCIRepository (first env), apply
OCIRepository labelled `delivery.pacto.trianalab.io/apply-ref`, Kustomization
and Stage. `--plan` prints every file's content and writes nothing. It only
adds files and missing `.pactoignore` lines. MCP: `pacto_create` with
`pactify: true` (plus `delivery`, `registry`, `environments`, `fleet_roots`;
`dry_run` plans). Code: `internal/pactify`.

9. story track, `examples/story/contracts/fraud/1.0.0/pacto.yaml`: the header
   comment says "pacto init does not read a Dockerfile or manifests yet (ledger
   B); no test derives it from plain/fraud". That is no longer true:
   `pacto init --owner risk` in `plain/fraud` writes this contract (the fields
   match; it adds a two-line header comment, and with `--delivery` the
   manifests build also ships `delivery/pacto.yaml`). Suggested: a story test
   that runs `pactify.Init(ctx, pactify.Options{Dir: <copy of plain/fraud>,
   Owner: "risk", Plan: true}, nil)` and compares the parsed contract with
   `contracts/fraud/1.0.0` (the core branch pins the same thing against a copy
   in `internal/pactify/testdata/fraud`; keep the two copies in step).
10. docs track: the "Pactify a service" front door (ledger E) can now show
    `pacto init --plan`, then `pacto init --delivery --owner <team>`, then
    `pacto publish --plan`. `docs/mcp-integration.md` (pacto_create section)
    can mention `pactify`. `docs/cli-reference.md` is regenerated
    (`make gen-cli-docs`); on conflict regenerate, do not hand-merge.
11. demos track: the browser pactify step and the Compose `pacto init --plan`
    step can use the plan output verbatim; the Kubernetes demo's agent can
    call `pacto_create` with `pactify: true, delivery: true` on `plain/fraud`.
12. docs track, from the core fix round: `pacto publish --plan` has three new
    checks, `refs` (the bundle and every artifact need their own repository),
    `sbom` (no `sbom` on a `dockerfile` build) and `build outputs` (an SBOM
    inside the contract directory must be in `.pactoignore`). `pacto init` puts
    the bundle at `<registry>/contracts/<name>` when the image is at
    `<registry>/<name>`, leaves a file holding a Secret's values out of the
    shipped manifests (Next says to use a SealedSecret, an ExternalSecret or
    SOPS) and reports a kept `delivery/<env>.yaml` whose Stage reads from the
    wrong place for `--env`. Whoever owns `pkg/validation` may also reject
    `sbom` with a `dockerfile` builder in crossfield validation so `pacto
    validate` says it too; the publish plan already refuses it.
