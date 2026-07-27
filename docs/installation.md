# Installation
---

## Via installer script

Install with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

!!! warning
    The installer script may request elevated permissions (sudo) to install the binary to `/usr/local/bin`. To install without elevated permissions, pass `--no-sudo` through to the script, or set `PACTO_INSTALL_DIR` to a custom directory as an environment variable before `bash`:

    ```bash
    # Install without sudo
    curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash -s -- --no-sudo

    # Install to a custom directory
    curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | PACTO_INSTALL_DIR="$HOME/.local/bin" bash
    ```

Verify the installation:

```bash
pacto version
```

## Via Go

Requires [Go 1.26](https://go.dev/dl/) or later.

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

## Updating

If you installed pacto via the installer script or from a GitHub release, you can update in-place:

```bash
# Update to the latest release
pacto update

# Update to a specific version
pacto update v1.2.0
```

This downloads the new binary, **verifies its SHA-256 against the `checksums.txt` published with the release**, and only then replaces the current one. If the download fails verification, the update is aborted and the existing binary is left untouched.

!!! note
    If you installed via `go install`, use `go install github.com/trianalab/pacto/v3/cmd/pacto@latest` to update instead.

Pacto also checks for updates automatically and notifies you when a newer version is available. See the [`pacto update` reference](cli-reference.md#pacto-update) for [update notifications](cli-reference.md#update-notifications) and the [`PACTO_NO_UPDATE_CHECK` environment variable](cli-reference.md#environment-variables).

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

