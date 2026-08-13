# pacto-operator v4 fixture — provenance

This is a byte-faithful vendored copy of the **real** previous-major (v4) operator
Helm chart, used by `tests/acceptance/kind/upgrade-v4-v5.sh` to prove a genuine
cross-major chart + CRD upgrade (v4 -> v5) against a live kind cluster — not the
"same chart repackaged with an older number" fixture that `run.sh` uses.

## Why vendored (not `helm pull`)

The task's stated coordinate for the published v4 chart —
`oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator:4.7.0` — is **not
reachable**. Both retrieval paths return not-found:

```
$ helm pull oci://ghcr.io/trianalab/pacto-operator/charts/pacto-operator --version 4.7.0
Error: ... ghcr.io/trianalab/pacto-operator/charts/pacto-operator:4.7.0: not found

$ crane digest ghcr.io/trianalab/pacto-operator/charts/pacto-operator:4.7.0
Error: ... MANIFEST_UNKNOWN: manifest unknown
```

The v4 **image** IS preserved and reachable, so the test pulls it at runtime and
this fixture supplies the chart the image shipped with.

## Recorded real v4 coordinates + digests

Digests captured and verified as of **2026-07-27**.

| Artifact | Coordinate | Digest / ref | Reachable |
|----------|------------|--------------|-----------|
| v4 controller image (index) | `ghcr.io/trianalab/pacto-operator/pacto-controller:4.7.0` | `sha256:a2e8e27dd8b080e797436ab376cef3f95467c7f91c9408bacc09aad8ff769e7d` | yes (`crane digest`) |
| &nbsp;&nbsp;↳ linux/amd64 | (per-platform manifest) | `sha256:3b507afc7f3faa718beda1334a1902498242c1d275ba5dfcb79b4d88b08d0e84` | yes |
| &nbsp;&nbsp;↳ linux/arm64 | (per-platform manifest) | `sha256:6f75948f806f7e49b94d67e58dcec5e543d3a56e31e0dc1dadbffcc5f68ca11f` | yes |
| v4 chart (published) | `ghcr.io/trianalab/pacto-operator/charts/pacto-operator:4.7.0` | — | **no** (MANIFEST_UNKNOWN) |
| v4 chart source | `github.com/TrianaLab/pacto-operator` tag `v4.7.0`, commit `a889498538fd29421ffe2644dfd21b544dff9eb6` | tarball sha256 `b7d6c09d406344394ec615fcd3e0522c27c40f015c736154183e5a1194a77a56` | yes (`gh api .../tarball/v4.7.0`) |

**Verify the pinned image** (the exact check `v4-to-v5-upgrade.sh` STEP 0 runs; the
test fails closed if this drifts):

```
$ crane digest ghcr.io/trianalab/pacto-operator/pacto-controller:4.7.0
sha256:a2e8e27dd8b080e797436ab376cef3f95467c7f91c9408bacc09aad8ff769e7d
# fallback without crane:
$ docker buildx imagetools inspect ghcr.io/trianalab/pacto-operator/pacto-controller:4.7.0
```

The image's own OCI labels confirm the source: `org.opencontainers.image.source
= https://github.com/TrianaLab/pacto-operator`, `.revision =
a889498538fd29421ffe2644dfd21b544dff9eb6`, `.version = 4.7.0`.

## What was vendored

Everything under `charts/pacto-operator/` from the v4.7.0 source tree, unmodified:
chart templates, the **v4 CRDs** (`crds/`), v4 default `values.yaml`,
`values.schema.json` and the v4 deployment args (`templates/_helpers.tpl` ->
`pacto-operator.controllerArgs`). The `Chart.yaml` version is left at the source
value `0.1.0`; the v4.7.0 release was produced by a package-time
`helm package --version 4.7.0 --app-version 4.7.0` override, and the upgrade test
reproduces that exactly (it packages this fixture with the same override and pins
`image.tag=4.7.0` to the preserved v4 image).

## The v4 -> v5 delta this fixture exercises (real, not cosmetic)

The v4 CRD and the current-tree (v5) CRD both serve/store `v1alpha1` (additive
schema evolution across the major), but the schema genuinely differs — e.g. the
printer columns changed `Passed/Failed` (v4) -> `Errors/Warnings` (v5) and v5 adds
`spec.target.configBindings` / `spec.target.interfaceBindings` plus
`status.findings` / `status.evaluationCoverage`. The test uses the printer-column
rename as an observable proof that the CRD actually migrated.
