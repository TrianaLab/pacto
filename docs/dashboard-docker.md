# Dashboard Container

Run the Pacto dashboard, a web UI for your services, their contracts,
dependencies and compliance, as a container. It is the same server as
`pacto dashboard` in the CLI, packaged for Docker and Kubernetes.

## Image

```text
ghcr.io/trianalab/pacto/dashboard:<version>
```

The tag matches the Pacto release version, without a `v` prefix. The image has
no `latest` tag, so swap the pinned version below for the release you want. The image is
signed keylessly; verify it with the command in
[Supply chain](installation.md#supply-chain-what-is-signed-and-what-is-not).

## Quick Start

Every `docker run` here publishes to `127.0.0.1` on purpose: the image sets
`--host 0.0.0.0` and the dashboard has [no authentication by default](#security).

```bash
# Run with OCI registry sources
docker run -p 127.0.0.1:3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a,ghcr.io/org/svc-b \
  ghcr.io/trianalab/pacto/dashboard:4.1.0

# Run with registry authentication
docker run -p 127.0.0.1:3000:3000 \
  -e PACTO_DASHBOARD_REPO=ghcr.io/org/svc-a \
  -e PACTO_REGISTRY_TOKEN=ghp_xxx \
  ghcr.io/trianalab/pacto/dashboard:4.1.0
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
| `PACTO_DASHBOARD_OIDC_ISSUER_URL` | Identity provider people sign in with before the dashboard shows anything; every view then follows their Kubernetes RBAC (also `_CLIENT_ID`, `_CLIENT_SECRET`, `_REDIRECT_URL`, `_EXTRA_SCOPES`, `_DISPLAY_NAME` for the provider's name on the sign-in button and the claim mapping, see [dashboard authentication](integrations/kubernetes/dashboard-authentication.md)) | `""` (no sign-in) |
| `PACTO_DASHBOARD_TOKEN_LOGIN` | `true` lets people sign in by pasting a Kubernetes bearer token | `false` |
| `PACTO_DASHBOARD_SESSION_IDLE_TIMEOUT`, `PACTO_DASHBOARD_SESSION_MAX_AGE` | How long a session lasts idle and in all | `1h`, `12h` |
| `PACTO_DASHBOARD_TRACE_SOURCES` | The same input with a stable identity per file: **space-separated** `NAME=PATH` entries, where `NAME` is the Data Source name the API and UI show | `""` |
| `PACTO_CACHE_DIR` | Directory the dashboard scans for cached OCI bundles (read side) | `/home/pacto/.cache/pacto/oci` |
| `PACTO_NO_CACHE` | Disable OCI bundle caching (`1`) | `0` |
| `PACTO_NO_UPDATE_CHECK` | Disable update checks (`1`) | `1` (set in image) |
| `PACTO_REGISTRY_USERNAME` | Registry authentication username | `""` |
| `PACTO_REGISTRY_PASSWORD` | Registry authentication password | `""` |
| `PACTO_REGISTRY_TOKEN` | Registry authentication token | `""` |

Most `PACTO_DASHBOARD_*` variables map to the CLI flag of the same name. Two
are different: `PACTO_DASHBOARD_REPO` is the repository positional argument, and
`PACTO_DASHBOARD_TRACE_SOURCES` maps to `--trace-source`, singular.

The two trace variables let the dashboard learn which services really call
each other, from trace files. Pacto has no OpenTelemetry (OTLP) receiver, so
you export traces to files and mount them in. Each file becomes a
[named observation source](operational-graph-reference.md#sources). Under Kubernetes the operator-managed dashboard sets
`PACTO_DASHBOARD_TRACE_SOURCES` for you from `dashboard.observation.sources`.
See [offline trace sources](integrations/kubernetes/installation.md#offline-trace-sources-for-the-dashboard).

`PACTO_CACHE_DIR` sets only where the dashboard **reads** cached bundles. It
**writes** them under `XDG_CACHE_HOME`. To keep the cache, mount a volume at
`~/.cache/pacto/oci`. The defaults line up because the image sets
`HOME=/home/pacto`. The Kubernetes example below mounts an `emptyDir` there
instead, so its cache is rebuilt on every restart.
The CLI reference lists [every variable](cli-reference.md#environment-variables).

## Data Sources

The dashboard auto-detects its sources at startup. The container-specific
bindings are:

- **`oci`**: `PACTO_DASHBOARD_REPO` is set, or repositories are discovered from Kubernetes `resolvedRef` fields. Supplies bundles, version history, interfaces and diffs.
- **`cache`**: the on-disk OCI cache, surfaced as its own source only as an offline baseline when no registry is configured.
- **`k8s`**: a kubeconfig is mounted, or the container runs in-cluster. Supplies runtime state from the [Pacto operator](integrations/kubernetes/overview.md).
- **`local`**: a `pacto.yaml` is found in the working directory (mount via volume).

### Kubernetes + OCI hybrid mode

When the Kubernetes source is on and the operator fills
`status.contract.resolvedRef`, the dashboard finds the contract repositories
by itself. You get **what is running from the operator and the contracts from
the registry**. If a registry is unreachable, or private and not logged in, the
dashboard **quietly falls back to Kubernetes only**: no version history,
interfaces, schemas or diffs.

### Mounting the `k8s` and `local` sources

Both arrive as volumes; inside a cluster the in-cluster config is used
automatically, with no kubeconfig mount:

```bash
# k8s: a kubeconfig, scoped to one namespace
docker run -p 127.0.0.1:3000:3000 \
  -v ~/.kube/config:/home/pacto/.kube/config:ro \
  -e PACTO_DASHBOARD_NAMESPACE=production \
  ghcr.io/trianalab/pacto/dashboard:4.1.0

# local: a contract directory
docker run -p 127.0.0.1:3000:3000 \
  -v /path/to/contracts:/data:ro \
  ghcr.io/trianalab/pacto/dashboard:4.1.0 dashboard /data
```

## Operational Endpoints

| Endpoint | Description |
|---|---|
| `GET /health` | Returns `{"status": "ok", "version": "..."}`. Use for liveness and readiness probes. |
| `GET /metrics` | Returns `{"serviceCount": N, "sourceCount": N}`. |
| `GET /openapi.json` | OpenAPI 3.1 specification (includes a server URL matching the bind address). Also served as `/openapi.yaml`, and downgraded to OpenAPI 3.0.3 at `/openapi-3.0.json`. |
| `GET /docs` | Interactive API documentation. |

`/openapi` on its own is a prefix and returns `404`. The image also
carries a Docker `HEALTHCHECK` polling `/health` every 10 seconds.

The `status` filters of `/api/fleet/services`, `/api/fleet/entities` and
`/api/fleet/attention` and the comma-separated `kinds` filter of
`/api/fleet/entities` are enums checked item by item: an unknown value or an
empty item such as a trailing comma gets a 422 that names the parameter, for
example `query.kinds[1]`.

## Security

!!! warning "Keep the dashboard private unless sign-in protects it"

    By default the dashboard has no login, API key or authorization. Anyone who
    can reach the port reads every contract, dependency and compliance result,
    and can make the container pull from your registries. Publish it to
    `127.0.0.1` (`-p 127.0.0.1:3000:3000`), put an authenticating proxy in front
    of it, or configure [sign-in](integrations/kubernetes/dashboard-authentication.md)
    with `PACTO_DASHBOARD_OIDC_ISSUER_URL`. The protections below defend against
    CSRF and resource exhaustion. They give no access control against a client
    that reaches the port.

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
          image: ghcr.io/trianalab/pacto/dashboard:4.1.0
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
