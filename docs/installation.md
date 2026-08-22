# Installation
---

## Via installer script

Install with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

This installs three binaries into `/usr/local/bin`: `pacto` itself and the two
official plugins, `pacto-plugin-schema-infer` and `pacto-plugin-openapi-infer`
(see [Plugins](plugins.md)). Plugin installation is best-effort — if it fails,
`pacto` is still installed.

!!! warning "Installing without sudo"
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
    the script does not create it.

Verify the installation:

```bash
pacto version
```

```text
Pacto:                v3.2.1
Git Commit:           497a8a79e229a61179184ec338edc4677b1d6ebb
Build Date:           2026-08-22T18:03:35+02:00
Go OS/Arch:           darwin/arm64
```

The version, commit, date and platform reflect the release you installed and
your machine; only the field names are fixed.

## Via Go

Requires [Go 1.26.6](https://go.dev/dl/) or later — the version in `go.mod`.

```bash
go install github.com/trianalab/pacto/v3/cmd/pacto@latest
```

## From source (manual build)

```bash
git clone https://github.com/TrianaLab/pacto.git
cd pacto
make build
```

The binary is placed in your `$GOBIN` directory (typically `~/go/bin`).

!!! note "The Go and from-source paths install `pacto` only"
    Neither `go install` nor `make build` installs the official plugins — only
    the installer script does. If you need `pacto generate`, install the plugins
    separately from [`TrianaLab/pacto-plugins`](https://github.com/TrianaLab/pacto-plugins)
    and put them on your `PATH`. See [Plugins](plugins.md).

!!! note "`go install` does not stamp a version"
    Release metadata is injected at link time, which `go install` does not do,
    so a Go-installed binary reports `Pacto: dev`, `Git Commit: unknown` and
    `Build Date: unknown`. It is the same code; only the stamp is missing.
    `make build` from a clone stamps them from your checkout. Use the installer
    script or a release binary when the reported version has to be meaningful.

## Updating

If you installed pacto via the installer script or from a GitHub release, you can update in-place:

```bash
# Update to the latest release
pacto update

# Update to (or roll back to) a specific released version
pacto update v3.1.4
```

This downloads the new binary, **verifies its SHA-256 against the `checksums.txt` published with the release**, and only then replaces the current one. If the download fails verification, the update is aborted and the existing binary is left untouched.

!!! note
    If you installed via `go install`, use `go install github.com/trianalab/pacto/v3/cmd/pacto@latest` to update instead.

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
make build    # Compile the pacto binary with version injection
make test     # Run all tests
make lint     # Run gofmt check and go vet
make clean    # Remove build artifacts
```

!!! note
    Package manager support (Homebrew, apt, etc.) is planned for future releases. Pre-built binaries are already published on GitHub Releases and are what the installer script and `pacto update` download.

Next: [Quickstart](quickstart.md).

