# Installation

Three ways in. The installer script is the shortest and the only one that also
brings the plugins; Go and from-source builds are for contributors and for
machines that already have a Go toolchain.

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

This installs three binaries into `/usr/local/bin`: `pacto` itself and the two
official plugins, `pacto-plugin-schema-infer` and `pacto-plugin-openapi-infer`
(see [Plugins](plugins.md)). Plugin installation is best-effort — if it fails,
the script prints a warning, installs `pacto` anyway and still exits 0. Re-run
the script to retry the plugins.

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

!!! note "The Go and from-source paths install `pacto` only"
    Neither `go install` nor `make build` installs the official plugins — only
    the installer script does. If you need `pacto generate`, install the plugins
    separately from [`TrianaLab/pacto-plugins`](https://github.com/TrianaLab/pacto-plugins)
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
    Package manager support (Homebrew, apt, etc.) is planned for future releases. Pre-built binaries are already published on GitHub Releases and are what the installer script and `pacto update` download.

Next: [Quickstart](quickstart.md).

