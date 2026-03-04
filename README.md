[![CI](https://github.com/TrianaLab/pacto/actions/workflows/coverage.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/coverage.yml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto)](https://pkg.go.dev/github.com/trianalab/pacto)
[![Go Report Card](https://goreportcard.com/badge/github.com/trianalab/pacto)](https://goreportcard.com/report/github.com/trianalab/pacto)
[![codecov](https://codecov.io/gh/TrianaLab/pacto/graph/badge.svg?token=DI2AL1DL9T)](https://codecov.io/gh/TrianaLab/pacto)
[![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto** (/ˈpak.to/ — from Spanish: *pact*, *agreement*) is an open, OCI-distributed contract standard for cloud-native services.

It provides a single, declarative source of truth that bridges the gap between **developers** who build services and **platform engineers** who run them. A Pacto contract captures everything a platform needs to know about a service — its interfaces, configuration, runtime semantics, dependencies, and scaling requirements — without assuming any specific infrastructure.

## Documentation

Full documentation is available at **[trianalab.github.io/pacto](https://trianalab.github.io/pacto)**.

- [Installation](https://trianalab.github.io/pacto/installation)
- [Quickstart](https://trianalab.github.io/pacto/quickstart)
- [Contract Reference](https://trianalab.github.io/pacto/contract-reference)
- [For Developers](https://trianalab.github.io/pacto/developers)
- [For Platform Engineers](https://trianalab.github.io/pacto/platform-engineers)
- [CLI Reference](https://trianalab.github.io/pacto/cli-reference)
- [Plugin Development](https://trianalab.github.io/pacto/plugins)
- [Architecture](https://trianalab.github.io/pacto/architecture)
- [Examples](https://trianalab.github.io/pacto/examples) (PostgreSQL, Redis, RabbitMQ, NGINX, Cron Worker)

## Quick example

```yaml
pactoVersion: "1.0"

service:
  name: payments-api
  version: 2.1.0
  owner: team/payments

interfaces:
  - name: rest-api
    type: http
    port: 8080
    visibility: public
    contract: interfaces/openapi.yaml

runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: local
      durability: persistent
    dataCriticality: high
  health:
    interface: rest-api
    path: /health

dependencies:
  - ref: ghcr.io/acme/auth-pacto@sha256:abc123
    required: true
    compatibility: "^2.0.0"

scaling:
  min: 2
  max: 10
```

## Installation

### Via installer script

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

> **Note:** The installer script may request elevated permissions (sudo) to install the binary to `/usr/local/bin`. You can use `--no-sudo` to install without elevated permissions or set `PACTO_INSTALL_DIR` to a custom directory.

### Via Go

```bash
go install github.com/trianalab/pacto/cmd/pacto@latest
```

### Build from source

```bash
git clone https://github.com/TrianaLab/pacto.git
cd pacto
make build
```

## Getting started

```bash
# Scaffold a new contract
pacto init my-service

# Validate
pacto validate my-service/pacto.yaml

# Pack and push
pacto pack my-service/pacto.yaml
pacto push ghcr.io/your-org/my-service-pacto:1.0.0 -p my-service/pacto.yaml
```

## License

[MIT](LICENSE)
