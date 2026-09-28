# Installation

Three ways in. The installer script is the shortest; the Go and from-source builds are for contributors and for machines that already have a Go toolchain.

| Method | Installs | `pacto version` reports |
|---|---|---|
| [Installer script](#via-installer-script) | `pacto` plus both official plugins | the release you installed |
| [`go install`](#via-go) | `pacto` only | `dev`, with commit and date `unknown` |
| [From source](#from-source) | `pacto` only | version, commit and date from your checkout |

Not installing yet? The [live dashboard demo](examples/dashboard-demo.md) runs Pacto in your browser with nothing to download.

## Via installer script

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

This installs into `/usr/local/bin`: `pacto`, `pacto-plugin-schema-infer` and `pacto-plugin-openapi-infer`. The script verifies SHA-256 checksums before installing. Plugin installation is best-effort: if it fails the script warns, installs `pacto` anyway and exits 0.

Pass `--version` for a specific release: `bash -s -- --version v3.1.4`.

!!! warning "If the script cannot find a version (GitHub API rate limit)"
    The script resolves the version through the anonymous GitHub API (60 requests per hour per IP). On a shared or NAT'd address it can run out and exit with `Failed to fetch latest version`. Set `GH_TOKEN` to any GitHub token (no scopes needed) and re-run:

    ```bash
    curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh \
      | GH_TOKEN="$(gh auth token)" bash
    ```

    `gh auth token` prints the token the [GitHub CLI](https://cli.github.com/) already holds. Without `gh`, create a fine-grained personal access token with no permissions at [github.com/settings/tokens](https://github.com/settings/tokens).

### Installing without sudo

The installer calls `sudo` to write to `/usr/local/bin`. To install into your home directory with no `sudo` at all:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh \
  | PACTO_INSTALL_DIR="$HOME/.local/bin" bash -s -- --no-sudo
```

`$HOME/.local/bin` must be on your `PATH`, and the directory must already exist.

## Via Go

```bash
go install github.com/trianalab/pacto/v3/cmd/pacto@latest
```

Requires [Go 1.26.6](https://go.dev/dl/) or later. The binary is placed in `$GOBIN` (typically `~/go/bin`).

!!! warning "A `go install` build reports itself as `dev`"
    Release metadata is injected at link time, which `go install` does not do. `pacto version` prints `Pacto: dev`, and `pacto update` refuses to run. Use the installer script or a [release binary](https://github.com/TrianaLab/pacto/releases) if you need version metadata.

## From source

Requires Go 1.26.6 or later, `make` and `git`.

```bash
git clone https://github.com/TrianaLab/pacto.git
cd pacto
make build
```

`make build` installs into `$GOBIN` (typically `~/go/bin`) and stamps the version, commit and build date.

## Verify the installation

```bash
pacto version
```

The first line should match the release you installed, or say `dev` if you used `go install`. If it names an older release, an earlier copy is ahead on your `PATH`: `which pacto` shows which one won.

## Installing the official plugins

The two official plugins (`pacto-plugin-schema-infer` and `pacto-plugin-openapi-infer`) are separate binaries maintained in the [pacto-plugins](https://github.com/TrianaLab/pacto-plugins) repository.

| Install method | Plugins |
|----------------|---------|
| [Installer script](#via-installer-script) | Installed alongside the CLI (best-effort). If the plugin release cannot be fetched the script prints `Warning: failed to fetch latest plugins version, skipping plugin installation` and continues. |
| [`go install`](#via-go) | Not installed |
| [From source](#from-source) | Not installed |

Without them, `pacto generate schema-infer` and `pacto generate openapi-infer` fail with `plugin "<name>" not found`. To install them by hand, download the binaries from the [pacto-plugins releases](https://github.com/TrianaLab/pacto-plugins/releases) and put them on your `PATH` or in `~/.config/pacto/plugins/`. See [Plugins](plugins.md) for the protocol and how to write your own.

## Update the CLI

`pacto update` works on a version-stamped binary (one from the installer script or a GitHub release). A `go install` build cannot use it; re-run `go install github.com/trianalab/pacto/v3/cmd/pacto@latest` instead.

```bash
pacto update              # latest release
pacto update v3.1.4       # specific version
```

This verifies the new binary's SHA-256 before replacing the current one. If verification fails, the update is aborted and the existing binary is left untouched. See the [`pacto update` reference](cli-reference.md#pacto-update) for update notifications and the `PACTO_NO_UPDATE_CHECK` environment variable.

## Supply chain: what is signed and what is not

Pacto's release pipeline signs some artifacts and not others. A signature you assume exists is worse than one you know does not:

| Artifact | What ships with it |
| --- | --- |
| `ghcr.io/trianalab/pacto/operator` | Cosign signature (keyless) |
| `ghcr.io/trianalab/pacto/charts/pacto-operator` | Cosign signature (keyless) |
| `ghcr.io/trianalab/pacto/dashboard` | Cosign signature (keyless) |
| CLI binaries on the GitHub release | `checksums.txt` (SHA-256) and an SPDX SBOM. **No signature, no provenance attestation.** |
| `ghcr.io/trianalab/pacto/dashboard-contract` | Nothing. **Unsigned.** |
| The demo contract bundles (`ghcr.io/trianalab/pacto/<service>`) | Nothing. **Unsigned.** |

The three signed images are signed keylessly by the release workflow through GitHub's OIDC issuer, so verification pins *who built it* rather than a key you have to trust separately:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/TrianaLab/pacto/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/trianalab/pacto/operator:<version>
```

A successful run prints the certificate subject and the workflow ref it was issued to. Anything else — including `no signatures found` — means do not deploy it.

For the CLI binaries the check available to you is the checksum:

```bash
shasum -a 256 -c checksums.txt --ignore-missing
```

`pacto update` performs this check before replacing the binary. `checksums.txt` proves the download matches what the release published; it does not prove who published it. If your policy requires signed CLI binaries, build from source at the tag instead.

---

Next: [Quickstart](quickstart.md). For uninstall steps, see [CONTRIBUTING.md](https://github.com/TrianaLab/pacto/blob/main/CONTRIBUTING.md).
