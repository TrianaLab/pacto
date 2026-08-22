# Security Policy

## Supported Versions

Pacto ships two independently versioned lines:

| Component | Supported |
| --------- | --------- |
| Pacto CLI and core library (`v3.x`) | latest release only |
| Kubernetes operator, Helm chart and Go module (`v5.x`) | latest release only |

Only the latest release of each line is actively supported with security updates. `releases/latest` on GitHub tracks the core; the operator version released alongside it is recorded in `release/release-manifest.json`. We recommend always running the most recent version of both.

## Reporting a Vulnerability

If you discover a security vulnerability in Pacto, please report it responsibly. **Do not open a public GitHub issue.**

### How to Report

1. **Report privately:** open a [GitHub Security Advisory](https://github.com/TrianaLab/pacto/security/advisories/new). There is no email channel.
2. Include the following in your report:
   - A description of the vulnerability
   - Steps to reproduce the issue
   - The potential impact
   - Any suggested fixes (if applicable)

### What to Expect

- **Acknowledgment:** We will acknowledge receipt of your report within **48 hours**.
- **Updates:** We will provide status updates as we investigate and work on a fix.
- **Disclosure:** Once a fix is released, we will coordinate with you on public disclosure. We aim to resolve critical issues within **30 days**.

## Security Practices

- The core CLI runs at **build time and CI time** — it has no runtime agents, sidecars, or persistent infrastructure. The optional [Kubernetes Operator](https://github.com/TrianaLab/pacto/tree/main/integrations/kubernetes) adds runtime compliance.
- The operator reads cluster state to compare it against contracts and never modifies the workloads it observes. It does create and manage its own components — the dashboard and the Evidence Server — and with the chart's default `dashboard.enabled: true` that requires cluster-wide write on Deployments, Services, ServiceAccounts, Secrets, ClusterRoles and ClusterRoleBindings. Set `dashboard.enabled: false` for an install whose only writes are Events and its own custom resources.
- The operator ships as a **distroless, non-root** image (`gcr.io/distroless/static:nonroot`, UID 65532) and runs with `readOnlyRootFilesystem: true` and `allowPrivilegeEscalation: false`.
- Contracts are distributed as **OCI artifacts** through standard container registries.
- All dependencies are kept up to date and monitored for known vulnerabilities.

## Scope

The following are in scope for security reports:

- The `pacto` CLI and its core libraries
- Official plugins (e.g., `pacto-plugin-schema-infer`)
- OCI artifact push/pull operations
- Contract validation logic
- The Kubernetes operator: controller, CRD definitions, RBAC permissions and Helm chart defaults

The following are **out of scope**:

- Third-party integrations or tools consuming Pacto contracts
- Vulnerabilities in upstream dependencies (report these to the upstream project, but let us know so we can update)
