---
title: Installation
layout: default
nav_order: 2
---

# Installation
{: .no_toc }

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

## Via installer script

The fastest way to install Pacto:

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

{: .warning }
The installer script may request elevated permissions (sudo) to install the binary to `/usr/local/bin`. You can use `--no-sudo` to install without elevated permissions or set `PACTO_INSTALL_DIR` to a custom directory.

Verify the installation:

```bash
pacto version
```

## Via Go

Requires [Go 1.25](https://go.dev/dl/) or later.

```bash
go install github.com/trianalab/pacto/cmd/pacto@latest
```

## From source (manual build)

```bash
git clone https://github.com/TrianaLab/pacto.git
cd pacto
make build
```

The binary is placed in your `$GOBIN` directory (typically `~/go/bin`).

## Build targets

```bash
make build    # Compile the pacto binary with version injection
make test     # Run all tests
make lint     # Run go vet
make clean    # Remove build artifacts
```

{: .note }
Pre-built binaries and package manager support (Homebrew, apt, etc.) are planned for future releases.
