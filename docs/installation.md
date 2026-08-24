# Installation

Three ways in. The installer script is the shortest; the Go and from-source
builds are for contributors and for machines that already have a Go toolchain.

| Method | Installs | `pacto version` reports |
|---|---|---|
| [Installer script](#via-installer-script) | `pacto` plus both official plugins | the release you installed |
| [`go install`](#via-go) | `pacto` only | `dev`, with commit and date `unknown` |
| [From source](#from-source-manual-build) | `pacto` only | version, commit and date from your checkout |

Not installing yet? The [live dashboard demo](examples/dashboard-demo.md) runs
Pacto in your browser with nothing to download, and the
[Compose demo](examples/compose-demo.md) runs a whole fleet without the CLI.

## Via installer script

You need a POSIX shell, `curl` or `wget`, and either the ability to `sudo` into
`/usr/local/bin` or a writable directory of your own (see [Installing without
sudo](#installing-without-sudo)). Linux and macOS run it directly; on Windows use
Git Bash, MSYS2 or Cygwin. The script downloads `checksums.txt` alongside the
binary and verifies SHA-256 with `sha256sum` or `shasum`; if the checksums file
or both tools are missing it prints `Warning: ... skipping verification` and
installs anyway.

Note that the two paths differ here: [`pacto update`](#updating) aborts rather
than replace a binary it could not verify, while the installer proceeds. On a
machine with neither `sha256sum` nor `shasum`, a first install is therefore
unverified. If you need it to fail closed instead, take a
[release binary](https://github.com/TrianaLab/pacto/releases) and check it
against the release's `checksums.txt` yourself.

Install with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

This installs into `/usr/local/bin`: `pacto`, `pacto-plugin-schema-infer` and
`pacto-plugin-openapi-infer` (see [Plugins](plugins.md)). Plugin installation is
best-effort — if it fails, the script prints a warning, installs `pacto` anyway
and still exits 0. Re-run the script to retry the plugins.

Pass `--version` to install a specific release instead of the latest:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh \
  | bash -s -- --version v3.1.4
```

!!! warning "If the script cannot find a version (GitHub API rate limit)"
    The script resolves the version through the anonymous GitHub API, which
    allows 60 requests per hour per IP address. On a shared or NAT'd address it
    can run out and exit without installing anything, reporting
    `Failed to fetch latest version` or `Version <tag> not found in TrianaLab/pacto releases`.
    Set `GH_TOKEN` (or `GITHUB_TOKEN`) to any GitHub token — no scopes needed —
    and re-run:

    ```bash
    curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh \
      | GH_TOKEN="$(gh auth token)" bash
    ```

    `gh auth token` just prints the token the [GitHub CLI](https://cli.github.com/)
    already holds. Without `gh`, create a fine-grained personal access token with
    no permissions selected at
    [github.com/settings/tokens](https://github.com/settings/tokens) and pass it
    directly: `GH_TOKEN=github_pat_… bash`.

### Installing without sudo

The installer writes to `/usr/local/bin` and calls `sudo` to do it. `--no-sudo`
is what turns that off. Choosing a writable directory does **not**: the script
still calls `sudo` even when the target needs no elevation, so to install into
your home directory you need both.

```bash
# Install to a directory you own, with no sudo at all
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh \
  | PACTO_INSTALL_DIR="$HOME/.local/bin" bash -s -- --no-sudo
```

`$HOME/.local/bin` must be on your `PATH`, and the directory must already exist —
the script does not create it. Everything the script reads from the environment
composes, so a rate-limited machine installing into its own directory passes
both:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh \
  | GH_TOKEN="$(gh auth token)" PACTO_INSTALL_DIR="$HOME/.local/bin" bash -s -- --no-sudo
```

## Via Go

Requires [Go 1.26.6](https://go.dev/dl/) or later — the version in `go.mod`.
The binary is placed in your `$GOBIN` directory (typically `~/go/bin`), which
must be on your `PATH`.

```bash
go install github.com/trianalab/pacto/v3/cmd/pacto@latest
```

!!! warning "A `go install` build reports itself as `dev`"
    Release metadata is injected at link time, which `go install` does not do.
    The code is exactly the release you asked for, but the binary cannot tell
    you which one, and three things follow from that:

    - `pacto version` prints `Pacto: dev`, `Git Commit: unknown` and
      `Build Date: unknown`, so it cannot confirm what you installed.
    - `pacto update` refuses to run: `cannot update a dev build; install a
      release build from https://github.com/TrianaLab/pacto/releases`. Update
      by re-running `go install ...@latest` instead.
    - [`pacto lock`](cli-reference.md#pacto-lock) records `pacto: version: dev`
      in `pacto.lock`, so the lockfile does not say which CLI produced it.

    Use the installer script or a [release binary](https://github.com/TrianaLab/pacto/releases)
    if any of those matter to you.

## From source (manual build)

Requires Go 1.26.6 or later, `make` and `git`.

```bash
git clone https://github.com/TrianaLab/pacto.git
cd pacto
make build
```

`make build` **installs** into your `$GOBIN` directory (typically `~/go/bin`) —
it does not leave a binary in `./bin` inside the clone. It stamps the version,
commit and build date from your checkout, so `pacto version` reports something
meaningful.

!!! note "Installing the plugins separately"
    If you need `pacto generate` after a Go or from-source install, take the
    plugins from [`TrianaLab/pacto-plugins`](https://github.com/TrianaLab/pacto-plugins)
    and put them on your `PATH`. See [Plugins](plugins.md).

## Verify the installation

Whichever path you took, check what your shell actually resolves:

```bash
pacto version
```

```text
Pacto:                v3.2.1
Git Commit:           497a8a79e229a61179184ec338edc4677b1d6ebb
Build Date:           2026-08-22T18:03:35+02:00
Go OS/Arch:           darwin/arm64
```

The four field names are fixed; the values are yours. The first line is the one
to read: it should match the release you just installed — or say `dev` if you
used `go install`. If it names an older release, an earlier copy is still ahead
on your `PATH`; `which pacto` (`where pacto` on Windows) shows which one won.

## Supply chain: what is signed and what is not

Pacto's release pipeline signs some artifacts and not others. A signature you
assume exists is worse than one you know does not:

| Artifact | What ships with it |
| --- | --- |
| `ghcr.io/trianalab/pacto/operator` | Cosign signature (keyless) |
| `ghcr.io/trianalab/pacto/charts/pacto-operator` | Cosign signature (keyless) |
| `ghcr.io/trianalab/pacto/dashboard` | Cosign signature (keyless) |
| CLI binaries on the GitHub release | `checksums.txt` (SHA-256) and an SPDX SBOM. **No signature, no provenance attestation.** |
| `ghcr.io/trianalab/pacto/dashboard-contract` | Nothing. **Unsigned.** |
| The demo contract bundles (`ghcr.io/trianalab/pacto/<service>`) | Nothing. **Unsigned.** |

The three signed images are signed keylessly by the release workflow through
GitHub's OIDC issuer, so verification pins *who built it* rather than a key you
have to trust separately:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/TrianaLab/pacto/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/trianalab/pacto/operator:<version>
```

A successful run prints the certificate subject and the workflow ref it was
issued to. Anything else — including `no signatures found` — means do not deploy
it.

For the CLI binaries the check available to you is the checksum:

```bash
# Download the binary and checksums.txt from the release, then:
shasum -a 256 -c checksums.txt --ignore-missing
```

`pacto update` performs exactly this check for you before it replaces the
binary. `checksums.txt` proves the download matches what the release published;
it does not prove who published it. If your policy requires signed CLI binaries,
build from source at the tag instead.

!!! warning "Do not expect a byte-identical rebuild"
    Rebuilding a released binary from its tag will **not** reproduce the
    published SHA-256, even with the same Go version and build flags. The
    release binaries embed a `+dirty` VCS stamp that a clean checkout cannot
    produce. Treat the published checksums as integrity for the download, not as
    a reproducible-build guarantee.

## Update or roll back the CLI { #updating }

`pacto update` works on a version-stamped binary — one from the installer
script or a GitHub release. A `go install` build cannot use it; re-run
`go install github.com/trianalab/pacto/v3/cmd/pacto@latest` instead.

```bash
# Update to the latest release
pacto update

# Update to (or roll back to) a specific released version
pacto update v3.1.4
```

This downloads the new binary, **verifies its SHA-256 against the `checksums.txt` published with the release**, and only then replaces the current one. If the download fails verification, the update is aborted and the existing binary is left untouched.

Pacto also checks for updates automatically and notifies you when a newer version is available. See the [`pacto update` reference](cli-reference.md#pacto-update) for [update notifications](cli-reference.md#update-notifications) and the [`PACTO_NO_UPDATE_CHECK` environment variable](cli-reference.md#environment-variables).

## Uninstall

Pacto has no uninstaller. Remove the binaries, then the state they wrote:

```bash
# 1. The binaries (adjust the directory if you set PACTO_INSTALL_DIR, or use
#    ~/go/bin if you installed with `go install` or `make build`).
sudo rm -f /usr/local/bin/pacto \
           /usr/local/bin/pacto-plugin-schema-infer \
           /usr/local/bin/pacto-plugin-openapi-infer

# 2. Registry credentials and the update-check timestamp.
rm -rf ~/.config/pacto

# 3. The pulled-bundle cache.
rm -rf ~/.cache/pacto
```

If you set `XDG_CONFIG_HOME` or `XDG_CACHE_HOME`, the last two live under
`$XDG_CONFIG_HOME/pacto` and `$XDG_CACHE_HOME/pacto` instead. Removing
`~/.config/pacto` deletes stored registry credentials; run
[`pacto logout <registry>`](cli-reference.md#pacto-logout) first if you would
rather remove them one registry at a time.

## Build targets

```bash
make build    # Build pacto into $GOBIN, stamping version, commit and build date
make test     # Run all tests
make lint     # Run gofmt, go vet and the repository's own checks
make clean    # Delete $GOBIN/pacto and the coverage files
```

!!! note
    Package manager support (Homebrew, apt, etc.) is planned for future releases.

Next: [Quickstart](quickstart.md). For what changed in the version you just
installed, see the [changelog](changelog.md).

