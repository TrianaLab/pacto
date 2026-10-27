---

## Environment variables

Boolean variables are parsed the way Go parses a boolean: `1`, `t`, `T`,
`true`, `TRUE` and `True` enable them and `0`, `f`, `F`, `false`, `FALSE` and
`False` disable them. Any other value, including `yes` and `on`,
reads as *off* with no warning. The two opt-outs `PACTO_NO_ANIM` and
`PACTO_NO_UPDATE_CHECK` are the exception: for them any other non-empty value
reads as *on*.

| Variable | Description |
|----------|-------------|
| `PACTO_CACHE_DIR` | Cache directory for the `pacto dashboard` bundle scan. The core CLI cache location is controlled by `XDG_CACHE_HOME` (see below). |
| `PACTO_NO_CACHE` | Boolean. Disable OCI bundle caching (equivalent to `--no-cache`) |
| `PACTO_NO_ANIM` | Boolean. Disables spinners and animations (equivalent to `--no-anim`). |
| `PACTO_NO_UPDATE_CHECK` | Boolean. Disables automatic update checks. |
| `PACTO_REGISTRY_USERNAME` | Registry username for authentication |
| `PACTO_REGISTRY_PASSWORD` | Registry password for authentication |
| `PACTO_REGISTRY_TOKEN` | Registry token for authentication |
| `PACTO_INSECURE_REGISTRIES` | Comma-separated registry **hosts** that Pacto reaches over plain HTTP. It applies to every registry access: `pacto push`, `pacto pull`, dependency and policy resolution and the Evidence Server. Scoped per host, so HTTPS registries are unaffected. Loopback hosts use plain HTTP without being listed. Use it only for a controlled in-cluster registry: it disables transport encryption, so credentials and payloads travel in the clear. |
| `PACTO_EVIDENCE_SOURCE_URL` | Base URL of an Evidence Server whose read-only [operational-graph](operational-graph.md) contribution `pacto dashboard` should consume. The operator sets it when it wires a managed Evidence Server. Unset means *no evidence source configured*, which the dashboard reports separately from an unavailable source. |
| `XDG_CONFIG_HOME` | Override config directory (default: `~/.config`). Affects `pacto login` credentials and update check cache |
| `XDG_CACHE_HOME` | Override cache directory (default: `~/.cache`). Affects OCI bundle cache location |

### Dashboard environment variables

The following variables configure the dashboard when set (see also [Dashboard Container](dashboard-docker.md)). Each mirrors the flag named beside it: an explicitly passed flag wins, and otherwise the variable overrides that flag's default.

| Variable | Flag | Description |
|----------|------|-------------|
| `PACTO_DASHBOARD_REPO` | *(positional args)* | **Comma**-separated OCI repositories, used only when no positional `oci://` argument is given |
| `PACTO_DASHBOARD_HOST` | `--host` | Bind address (default: `127.0.0.1`) |
| `PACTO_DASHBOARD_PORT` | `--port` | HTTP port (default: `3000`) |
| `PACTO_DASHBOARD_NAMESPACE` | `--namespace` | Kubernetes namespace filter (empty = all) |
| `PACTO_DASHBOARD_CORS_ORIGIN` | `--cors-origin` | One explicit origin allowed to call the API. Unset means same-origin only. |
| `PACTO_DASHBOARD_TRACES` | `--traces` | OTLP/JSON trace files to fold observed dependencies from |
| `PACTO_DASHBOARD_TRACE_SOURCES` | `--trace-source` | Named offline trace sources as `NAME=PATH`, where `NAME` is the source's identity (what the fleet, the API and the dashboard's Data Source list call it) and `PATH` is the trace file location. Identity and location are separate, so reordering the configuration or moving the file keeps the source's name. Two sources whose files share a basename stay two sources. A name must be unique across every Data Source the dashboard assembles. |

!!! warning "The two list variables split on **whitespace**"
    `PACTO_DASHBOARD_TRACES` and `PACTO_DASHBOARD_TRACE_SOURCES` go through the
    same generic flag binding as every other dashboard variable, which splits a
    multi-valued string on spaces. `PACTO_DASHBOARD_TRACES="a.json b.json"` supplies
    two files; `PACTO_DASHBOARD_TRACES=a.json,b.json` supplies **one**, named
    `a.json,b.json`. `PACTO_DASHBOARD_REPO` is read directly and splits on
    commas.

    The dashboard starts either way: it skips a trace file it cannot read, so the
    symptom is missing observed dependencies with no error. Check the snapshot's reported limitations if the operational
    graph looks emptier than you expect.

---

## Authentication

Pacto follows this credential resolution chain:

1. Environment variables (`PACTO_REGISTRY_TOKEN`, or `PACTO_REGISTRY_USERNAME` + `PACTO_REGISTRY_PASSWORD`)
2. Pacto config (`~/.config/pacto/config.json`, written by `pacto login`, which persists `--username` and `--password`)
3. GitHub CLI (`gh auth token`, for `ghcr.io` and `docker.pkg.github.com` only)
4. Docker config (`~/.docker/config.json`) and credential helpers (including cloud-registry helpers for ECR, GCR and ACR)
5. Anonymous fallback

For GitHub registries, step 3 means you can skip `pacto login` entirely if you have `gh` authenticated with the `write:packages` scope (see [`pacto login`](#pacto-login) above).

Pacto stores no credentials in contract files.
