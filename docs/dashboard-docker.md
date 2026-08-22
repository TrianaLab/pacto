# Dashboard Container
The Pacto dashboard is published as a container image for production and Kubernetes deployments. It runs the same `pacto dashboard` server in a deployable container. See the [platform engineer guide](platform-engineers.md) for how the dashboard fits into operator, compliance and blast-radius workflows.

---

## Image

```
ghcr.io/trianalab/pacto/dashboard:<version>
```

The image tag always matches the Pacto release version, without a `v` prefix — `3.2.1` for Pacto 3.2.1. There is no `latest` tag, so every snippet on this page pins a concrete version; swap it for the release you want. The container runs the exact `pacto` binary for that version.

## Quick Start

```bash
# Run with OCI registry sources
docker run -p 3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a,ghcr.io/org/svc-b \
  ghcr.io/trianalab/pacto/dashboard:3.2.1

# Run with registry authentication
docker run -p 3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a \
  -e PACTO_REGISTRY_TOKEN=ghp_xxx \
  ghcr.io/trianalab/pacto/dashboard:3.2.1
```

## Local Development

To build the image yourself rather than pull it, you need the repository — both
targets below run `docker build` against the repository root and tag the image with
the version derived from your checkout's git state:

```bash
git clone https://github.com/TrianaLab/pacto.git
cd pacto

# Build the image (tagged with the version derived from git describe)
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
| `PACTO_DASHBOARD_CORS_ORIGIN` | Trusted cross-origin allowed to call the API | `""` (same-origin only) |
| `PACTO_DASHBOARD_TRACES` | Offline OTLP/JSON trace exports to fold observed dependencies from. **Space-separated** list of paths | `""` |
| `PACTO_DASHBOARD_TRACE_SOURCES` | The same input with a stable identity per file: **space-separated** `NAME=PATH` entries, where `NAME` is the Data Source name the API and UI show | `""` |
| `PACTO_CACHE_DIR` | Directory the dashboard scans for cached OCI bundles (read side) | `/home/pacto/.cache/pacto/oci` |
| `PACTO_NO_CACHE` | Disable OCI bundle caching (`1`) | `0` |
| `PACTO_NO_UPDATE_CHECK` | Disable update checks (`1`) | `1` (set in image) |
| `PACTO_REGISTRY_USERNAME` | Registry authentication username | `""` |
| `PACTO_REGISTRY_PASSWORD` | Registry authentication password | `""` |
| `PACTO_REGISTRY_TOKEN` | Registry authentication token | `""` |

Each `PACTO_DASHBOARD_*` variable maps to the corresponding CLI flag: `--host`, `--port`, `--namespace`, `--diagnostics`, `--cors-origin`, `--traces` and `--trace-source`. OCI repositories can be passed as `oci://` positional arguments on the CLI; in the container, use the comma-separated `PACTO_DASHBOARD_REPO` env var instead.

The two trace variables are the container's only way to feed observed dependencies into the [Operational Graph](operational-graph.md#named-observation-sources): Pacto ships no OpenTelemetry (OTLP) receiver, so observed evidence arrives as offline trace exports you mount into the container. Watch the separator — these two take a **space-separated** list, unlike the comma-separated `PACTO_DASHBOARD_REPO`; a comma-joined value is read as one path, and a path that does not resolve leaves that Data Source reported as unavailable. Under Kubernetes the operator-managed dashboard sets `PACTO_DASHBOARD_TRACE_SOURCES` for you from `dashboard.observation.sources`, so configure it there instead — see [offline trace sources](integrations/kubernetes/installation.md#offline-trace-sources-for-the-dashboard).

> **Note:** `PACTO_CACHE_DIR` sets only the directory the dashboard **scans** for cached OCI bundles (read side); it is an **environment variable only** — there is no `--cache-dir` flag. When it is unset, the dashboard resolves this directory from the bundle store's `CacheDir()` (defaulting to `~/.cache/pacto/oci`). The core CLI/OCI cache **write** location is controlled by `XDG_CACHE_HOME` (default `~/.cache/pacto/oci`), not `PACTO_CACHE_DIR` — so to persist the cache, mount a volume at that path (as the [Kubernetes example](#kubernetes-deployment) does) or set `XDG_CACHE_HOME`. The container default works because `HOME=/home/pacto` makes the read and write paths coincide. See the [environment variables](cli-reference.md#environment-variables) in the CLI reference for the full picture.

## Data Sources

The dashboard auto-detects available data sources at startup. See the [source model](architecture.md#source-model) and [resolution model](architecture.md#resolution-model) for how sources merge and prioritize; the container-specific bindings are:

- **oci**: Enabled when `PACTO_DASHBOARD_REPO` is set, or auto-discovered from K8s `resolvedRef` fields. Provides contract bundles, version history, interfaces and diffs. (On-disk cache at `/home/pacto/.cache/pacto/oci/` is used internally — see [architecture](architecture.md#source-model).)
- **cache**: The on-disk OCI cache is internal to the OCI source; it surfaces as a distinct `cache` source only as an offline baseline when no registry is configured and the cache has entries.
- **k8s**: Enabled when a valid kubeconfig is mounted or when running inside a Kubernetes cluster (in-cluster config). Provides runtime state from the [Pacto operator](integrations/kubernetes/overview.md).
- **local**: Enabled when a `pacto.yaml` is found in the working directory (mount via volume).

### Kubernetes + OCI hybrid mode

When deployed alongside the Pacto operator in Kubernetes, the dashboard automatically discovers OCI repositories from the `resolvedRef` fields in Pacto CRD statuses — no `PACTO_DASHBOARD_REPO` needed. This creates a hybrid view: **runtime truth from the operator + contract truth from OCI**, giving you version history, interface details, configuration schemas, and diffs for every service the operator manages.

**Prerequisites.** Hybrid mode only activates when all of the following hold:

- A mounted kubeconfig **or** in-cluster config so the Kubernetes source is active.
- The Pacto operator is running and has populated `status.contract.resolvedRef` on the Pacto resources to discover.
- The discovered registries are reachable and (for private repositories) authenticated via `PACTO_REGISTRY_*` credentials.

If any prerequisite is missing — no `resolvedRef`, an unreachable registry, or missing credentials — the dashboard **silently degrades to k8s-only**: it still shows runtime state from the operator, but without the OCI-backed version history, interfaces, schemas, and diffs.

### Kubernetes Source

To enable the Kubernetes data source, mount a kubeconfig:

```bash
docker run -p 3000:3000 \
  -v ~/.kube/config:/home/pacto/.kube/config:ro \
  -e PACTO_DASHBOARD_NAMESPACE=production \
  ghcr.io/trianalab/pacto/dashboard:3.2.1
```

When running inside a Kubernetes cluster, the in-cluster config is used automatically (no mount needed).

### Local Source

To scan a local contract directory:

```bash
docker run -p 3000:3000 \
  -v /path/to/contracts:/data:ro \
  ghcr.io/trianalab/pacto/dashboard:3.2.1 \
  dashboard /data
```

## Operational Endpoints

| Endpoint | Description |
|---|---|
| `GET /health` | Returns `{"status": "ok", "version": "..."}`. Use for liveness and readiness probes. |
| `GET /metrics` | Returns `{"serviceCount": N, "sourceCount": N}`. |
| `GET /openapi.json` | OpenAPI 3.1 specification (includes a server URL matching the bind address). Also served as `/openapi.yaml`, and downgraded to OpenAPI 3.0.3 at `/openapi-3.0.json`. |
| `GET /docs` | Interactive API documentation. |

`/openapi` on its own is a prefix, not a route: it returns `404`. Ask for one of the three suffixed paths above.

The image includes a Docker `HEALTHCHECK` that polls `/health` every 10 seconds.

## Security

The dashboard is a read-mostly observability UI, but a few endpoints mutate
local state (`POST /api/resolve`, `POST /api/versions`, `POST /api/refresh`
pull and cache OCI artifacts). The server applies these protections:

- **Same-origin only by default.** No `Access-Control-Allow-Origin` header is
  emitted, and cross-origin *mutating* requests are rejected with `403`. This
  prevents a malicious web page in the operator's browser from driving the
  dashboard (CSRF/SSRF). The bundled UI is served same-origin and is unaffected.
- **Explicit cross-origin opt-in.** Pass `--cors-origin https://your-app` (or
  `PACTO_DASHBOARD_CORS_ORIGIN`) to allow one trusted cross-origin client.
- **HTTP timeouts** (`ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout`) guard
  against slow-client (Slowloris) exhaustion, and shutdown is graceful.

The server binds to `127.0.0.1` by default. Setting `--host 0.0.0.0` (as the
container image does) exposes the dashboard — including the unauthenticated
mutating endpoints — to the network, so run it only on a trusted network or
behind an authenticating proxy.

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
          image: ghcr.io/trianalab/pacto/dashboard:3.2.1
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

The dashboard image is built and published automatically when a new Pacto version is released. In the release pipeline (`.github/workflows/release.yml`), the `dashboard-image-build` job builds one image per architecture natively (`linux/amd64`, `linux/arm64`) and pushes each by digest; the `dashboard-image` job merges those children into a single multi-arch index published to `ghcr.io/trianalab/pacto/dashboard` under the release version as its tag (no `v` prefix).
