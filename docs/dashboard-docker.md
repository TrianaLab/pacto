# Dashboard Container

The dashboard ships as a container image running the same `pacto dashboard`
server as the CLI. The [platform engineer guide](platform-engineers.md) covers
where it fits in operator, compliance and impact-analysis work.

## Image

```text
ghcr.io/trianalab/pacto/dashboard:<version>
```

The tag matches the Pacto release version, without a `v` prefix, and there is no
`latest` — swap the pinned version below for the release you want. The image is
signed keylessly; verify it with the command in
[Supply chain](installation.md#supply-chain-what-is-signed-and-what-is-not).

## Quick Start

Every `docker run` here publishes to `127.0.0.1` on purpose: the image sets
`--host 0.0.0.0` and the dashboard has [no authentication at all](#security).

```bash
# Run with OCI registry sources
docker run -p 127.0.0.1:3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a,ghcr.io/org/svc-b \
  ghcr.io/trianalab/pacto/dashboard:3.3.3

# Run with registry authentication
docker run -p 127.0.0.1:3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a \
  -e PACTO_REGISTRY_TOKEN=ghp_xxx \
  ghcr.io/trianalab/pacto/dashboard:3.3.3
```

To build the image from a checkout instead, `make docker-build` and
`make docker-run` tag it with the version derived from your git state.

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `PACTO_DASHBOARD_HOST` | Bind address for the server | `0.0.0.0` (in image), `127.0.0.1` (CLI) |
| `PACTO_DASHBOARD_PORT` | HTTP server port | `3000` |
| `PACTO_DASHBOARD_NAMESPACE` | Kubernetes namespace filter (empty = all) | `""` |
| `PACTO_DASHBOARD_REPO` | Comma-separated OCI repositories to scan | `""` |
| `PACTO_DASHBOARD_CORS_ORIGIN` | Trusted cross-origin allowed to call the API | `""` (same-origin only) |
| `PACTO_DASHBOARD_TRACES` | Offline OTLP/JSON trace exports to fold observed dependencies from. **Space-separated** list of paths | `""` |
| `PACTO_DASHBOARD_TRACE_SOURCES` | The same input with a stable identity per file: **space-separated** `NAME=PATH` entries, where `NAME` is the Data Source name the API and UI show | `""` |
| `PACTO_CACHE_DIR` | Directory the dashboard scans for cached OCI bundles (read side) | `/home/pacto/.cache/pacto/oci` |
| `PACTO_NO_CACHE` | Disable OCI bundle caching (`1`) | `0` |
| `PACTO_NO_UPDATE_CHECK` | Disable update checks (`1`) | `1` (set in image) |
| `PACTO_REGISTRY_USERNAME` | Registry authentication username | `""` |
| `PACTO_REGISTRY_PASSWORD` | Registry authentication password | `""` |
| `PACTO_REGISTRY_TOKEN` | Registry authentication token | `""` |

Most `PACTO_DASHBOARD_*` variables map to the CLI flag of the same name. Two do
not: `PACTO_DASHBOARD_REPO` is the repository positional argument, and
`PACTO_DASHBOARD_TRACE_SOURCES` maps to `--trace-source`, singular.

The two trace variables are the container's only way to feed observed
dependencies into the operational graph as
[named observation sources](operational-graph.md#sources). Pacto ships no
OpenTelemetry (OTLP) receiver, so observed evidence arrives as offline trace
exports you mount in. Under Kubernetes the operator-managed dashboard sets
`PACTO_DASHBOARD_TRACE_SOURCES` for you from `dashboard.observation.sources` —
see [offline trace sources](integrations/kubernetes/installation.md#offline-trace-sources-for-the-dashboard).

`PACTO_CACHE_DIR` sets only the directory the dashboard **scans**. The **write**
location is `XDG_CACHE_HOME`, so persisting the cache means mounting a volume at
`~/.cache/pacto/oci`, as the [Kubernetes example](#kubernetes-deployment) does;
the container default works because `HOME=/home/pacto` makes the two coincide.
The CLI reference lists [every variable](cli-reference.md#environment-variables).

## Data Sources

The dashboard auto-detects its sources at startup. The container-specific
bindings are:

- **`oci`**: `PACTO_DASHBOARD_REPO` is set, or repositories are discovered from Kubernetes `resolvedRef` fields. Supplies bundles, version history, interfaces and diffs.
- **`cache`**: the on-disk OCI cache, surfaced as its own source only as an offline baseline when no registry is configured.
- **`k8s`**: a kubeconfig is mounted, or the container runs in-cluster. Supplies runtime state from the [Pacto operator](integrations/kubernetes/overview.md).
- **`local`**: a `pacto.yaml` is found in the working directory (mount via volume).

### Kubernetes + OCI hybrid mode

With the Kubernetes source active and the operator populating
`status.contract.resolvedRef`, the dashboard discovers the contract repositories
itself: **runtime truth from the operator plus contract truth from OCI**. If a
registry is unreachable or a private one is unauthenticated it **silently
degrades to Kubernetes-only**, without the OCI-backed history, interfaces,
schemas and diffs.

### Mounting the `k8s` and `local` sources

Both arrive as volumes; inside a cluster the in-cluster config is used
automatically, with no kubeconfig mount:

```bash
# k8s: a kubeconfig, scoped to one namespace
docker run -p 127.0.0.1:3000:3000 \
  -v ~/.kube/config:/home/pacto/.kube/config:ro \
  -e PACTO_DASHBOARD_NAMESPACE=production \
  ghcr.io/trianalab/pacto/dashboard:3.3.3

# local: a contract directory
docker run -p 127.0.0.1:3000:3000 \
  -v /path/to/contracts:/data:ro \
  ghcr.io/trianalab/pacto/dashboard:3.3.3 dashboard /data
```

## Operational Endpoints

| Endpoint | Description |
|---|---|
| `GET /health` | Returns `{"status": "ok", "version": "..."}`. Use for liveness and readiness probes. |
| `GET /metrics` | Returns `{"serviceCount": N, "sourceCount": N}`. |
| `GET /openapi.json` | OpenAPI 3.1 specification (includes a server URL matching the bind address). Also served as `/openapi.yaml`, and downgraded to OpenAPI 3.0.3 at `/openapi-3.0.json`. |
| `GET /docs` | Interactive API documentation. |

`/openapi` on its own is a prefix, not a route: it returns `404`. The image also
carries a Docker `HEALTHCHECK` polling `/health` every 10 seconds.

## Security

!!! warning "The dashboard has no authentication — do not expose it"

    It ships no login, no API key and no authorization. Anyone who can reach
    the port reads every contract, dependency and compliance result, and can
    make the container pull from your registries. Publish it to `127.0.0.1`
    (`-p 127.0.0.1:3000:3000`) or put an authenticating proxy in front of it.
    The protections below are CSRF and resource-exhaustion defences, not access
    control, and none of them stop a client that can simply reach the port.

A few endpoints mutate local state (`POST /api/resolve`, `POST /api/versions`
and `POST /api/refresh` pull and cache OCI artifacts). Against those:

- **Same-origin only by default.** No `Access-Control-Allow-Origin` header is
  emitted and cross-origin *mutating* requests get a `403`, so a malicious page
  in your browser cannot drive the dashboard. The bundled UI is unaffected.
- **Explicit cross-origin opt-in.** `--cors-origin https://your-app` (or
  `PACTO_DASHBOARD_CORS_ORIGIN`) allows one trusted cross-origin client.
- **HTTP timeouts** guard against slow-client (Slowloris) exhaustion.

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
          image: ghcr.io/trianalab/pacto/dashboard:3.3.3
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
