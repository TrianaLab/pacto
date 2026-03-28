---
layout: default
title: Dashboard Container
nav_order: 7.5
---

# Dashboard Container
{: .no_toc }

---

<details open markdown="block">
  <summary>Table of contents</summary>
- TOC
{:toc}
</details>

The Pacto dashboard is published as a container image for production and Kubernetes deployments. It provides the same contract exploration experience as the CLI's `pacto dashboard` command — dependency graphs, version history, interfaces, configuration schemas, and diffs — in a deployable container.

## Image

```
ghcr.io/trianalab/pacto-dashboard:<version>
```

The image tag always matches the Pacto release version (e.g., `1.2.3`). There is no `latest` tag. The container runs the exact `pacto` binary for that version.

## Quick Start

```bash
# Run with OCI registry sources
docker run -p 3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a,ghcr.io/org/svc-b \
  ghcr.io/trianalab/pacto-dashboard:1.2.3

# Run with registry authentication
docker run -p 3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a \
  -e PACTO_REGISTRY_TOKEN=ghp_xxx \
  ghcr.io/trianalab/pacto-dashboard:1.2.3
```

## Local Development

Build and run the dashboard container locally using Make:

```bash
# Build the image (tagged with current git version)
make docker-build

# Build and run (mounts ~/.kube/config and ~/.cache/pacto automatically)
make docker-run
```

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `PACTO_DASHBOARD_HOST` | Bind address for the server | `0.0.0.0` (in image), `127.0.0.1` (CLI) |
| `PACTO_DASHBOARD_PORT` | HTTP server port | `3000` |
| `PACTO_DASHBOARD_NAMESPACE` | Kubernetes namespace filter (empty = all) | `""` |
| `PACTO_DASHBOARD_REPO` | Comma-separated OCI repositories to scan | `""` |
| `PACTO_DASHBOARD_DIAGNOSTICS` | Enable source diagnostics panel (`true`) | `false` |
| `PACTO_CACHE_DIR` | OCI bundle cache directory | `/home/pacto/.cache/pacto/oci` |
| `PACTO_NO_CACHE` | Disable OCI bundle caching (`1`) | `0` |
| `PACTO_NO_UPDATE_CHECK` | Disable update checks (`1`) | `1` (set in image) |
| `PACTO_REGISTRY_USERNAME` | Registry authentication username | `""` |
| `PACTO_REGISTRY_PASSWORD` | Registry authentication password | `""` |
| `PACTO_REGISTRY_TOKEN` | Registry authentication token | `""` |

All `PACTO_DASHBOARD_*` variables map to the corresponding `--host`, `--port`, `--namespace`, and `--diagnostics` CLI flags. The `--repo` flag can be repeated on the CLI; in the container, use the comma-separated `PACTO_DASHBOARD_REPO` env var instead.

## Data Sources

The dashboard auto-detects available data sources at startup:

- **oci**: Enabled when `PACTO_DASHBOARD_REPO` is set, or **automatically discovered from K8s `imageRef` fields** when the Kubernetes source is active. Scans OCI registries for published contracts — providing full contract bundles, version history, interfaces, and diffs.
- **cache**: Enabled when the cache directory contains previously pulled bundles. The cache directory is writable inside the container at `/home/pacto/.cache/pacto/oci/` (configurable via `PACTO_CACHE_DIR`).
- **k8s**: Enabled when a valid kubeconfig is mounted or when running inside a Kubernetes cluster (in-cluster config). Provides runtime state from the [Pacto operator]({{ site.baseurl }}{% link operator.md %}).
- **local**: Enabled when a `pacto.yaml` is found in the working directory (mount via volume).

### Kubernetes + OCI hybrid mode

When deployed alongside the Pacto operator in Kubernetes, the dashboard automatically discovers OCI repositories from the `imageRef` fields in Pacto CRD statuses — no `PACTO_DASHBOARD_REPO` needed. This creates a hybrid view: **runtime truth from the operator + contract truth from OCI**, giving you version history, interface details, configuration schemas, and diffs for every service the operator manages.

### Kubernetes Source

To enable the Kubernetes data source, mount a kubeconfig:

```bash
docker run -p 3000:3000 \
  -v ~/.kube/config:/home/pacto/.kube/config:ro \
  -e PACTO_DASHBOARD_NAMESPACE=production \
  ghcr.io/trianalab/pacto-dashboard:1.2.3
```

When running inside a Kubernetes cluster, the in-cluster config is used automatically (no mount needed).

### Local Source

To scan a local contract directory:

```bash
docker run -p 3000:3000 \
  -v /path/to/contracts:/data:ro \
  ghcr.io/trianalab/pacto-dashboard:1.2.3 \
  dashboard /data
```

## Operational Endpoints

| Endpoint | Description |
|---|---|
| `GET /health` | Returns `{"status": "ok", "version": "..."}`. Use for liveness and readiness probes. |
| `GET /metrics` | Returns `{"serviceCount": N, "sourceCount": N}`. |
| `GET /openapi` | OpenAPI 3.1 specification (includes server URL matching the bind address). |
| `GET /docs` | Interactive API documentation. |

The image includes a Docker `HEALTHCHECK` that polls `/health` every 10 seconds.

## Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pacto-dashboard
spec:
  replicas: 1
  selector:
    matchLabels:
      app: pacto-dashboard
  template:
    metadata:
      labels:
        app: pacto-dashboard
    spec:
      containers:
        - name: dashboard
          image: ghcr.io/trianalab/pacto-dashboard:1.2.3
          ports:
            - containerPort: 3000
          env:
            - name: PACTO_DASHBOARD_REPO
              value: "ghcr.io/org/svc-a,ghcr.io/org/svc-b"
            - name: PACTO_REGISTRY_TOKEN
              valueFrom:
                secretKeyRef:
                  name: pacto-registry
                  key: token
          livenessProbe:
            httpGet:
              path: /health
              port: 3000
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /health
              port: 3000
            initialDelaySeconds: 3
            periodSeconds: 5
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
          securityContext:
            runAsNonRoot: true
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
          volumeMounts:
            - name: cache
              mountPath: /home/pacto/.cache
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: cache
          emptyDir: {}
        - name: tmp
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: pacto-dashboard
spec:
  selector:
    app: pacto-dashboard
  ports:
    - port: 80
      targetPort: 3000
```

## Build and Release

The dashboard image is built and published automatically when a new Pacto version is released. The `docker` job in the auto-release pipeline (`.github/workflows/auto-release.yml`) builds multi-architecture images (`linux/amd64`, `linux/arm64`) and pushes to `ghcr.io/trianalab/pacto-dashboard` with the matching version tag (without `v` prefix).

The image version always matches the Pacto CLI version. There is no separate versioning scheme for the dashboard container.
